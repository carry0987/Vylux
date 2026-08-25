package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"Vylux/internal/audio"
	"Vylux/internal/db/dbq"
	"Vylux/internal/encryption"
	"Vylux/internal/queue"
	"Vylux/internal/storage"
	apptracing "Vylux/internal/tracing"

	"github.com/hibiken/asynq"
	"go.opentelemetry.io/otel/attribute"
)

// HandleAudioTranscode returns an asynq handler for audio:transcode tasks.
func HandleAudioTranscode(d *Deps) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var p queue.AudioTranscodePayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal audio:transcode payload: %w", err)
		}

		taskID, _ := asynq.GetTaskID(ctx)
		slog.Info("processing audio:transcode",
			apptracing.LogFields(ctx,
				"job_id", taskID,
				"hash", p.Hash,
				"hls", p.HLS,
				"encrypt", p.Encrypt,
				"mp3", p.MP3,
				"flac", p.FLAC,
				"waveform", p.Waveform,
			)...,
		)

		meta := jobMeta{
			Type:        queue.TypeAudioTranscode,
			Hash:        p.Hash,
			CallbackURL: p.CallbackURL,
		}
		result := audio.NewProcessResult(p.HLS, p.MP3 || p.FLAC, p.Waveform)

		_ = d.Queries.UpdateJobStatus(ctx, dbq.UpdateJobStatusParams{
			ID:     taskID,
			Status: "processing",
		})

		tmpPath, cleanupSrc, err := downloadToTemp(ctx, d.SourceStore, d.Config.SourceBucket, p.Source, "vylux-audio-*")
		if err != nil {
			result.Stages.Source = audio.FailedStage("download_failed", fmt.Sprintf("download source: %v", err))
			skipPendingAudioStages(&result, "blocked_by_source_failure")
			return failAudioProcess(ctx, d, taskID, meta, &result, audio.StageSource, "download_failed", fmt.Sprintf("download source: %v", err))
		}
		defer cleanupSrc()
		result.Stages.Source = audio.ReadyStage()

		d.setProgress(ctx, taskID, 10)

		probeCtx, span := startWorkerSpan(ctx, "worker.audio.probe",
			attribute.String("file.path", tmpPath),
		)
		probeResult, err := audio.Probe(probeCtx, tmpPath)
		if err != nil {
			recordSpanError(span, err)
			span.End()
			result.Stages.Probe = audio.FailedStage("probe_failed", fmt.Sprintf("probe source: %v", err))
			skipPendingAudioStages(&result, "blocked_by_probe_failure")
			return failAudioProcess(ctx, d, taskID, meta, &result, audio.StageProbe, "probe_failed", fmt.Sprintf("probe source: %v", err))
		}
		span.SetAttributes(
			attribute.String("audio.source_format", string(probeResult.SourceFormat)),
			attribute.String("audio.container", probeResult.Container),
			attribute.Int("audio.stream_count", len(probeResult.Streams)),
		)
		span.End()

		if !probeResult.SourceFormat.Supported() {
			message := fmt.Sprintf("unsupported audio source format: %s", probeResult.SourceFormat)
			result.Stages.Probe = audio.FailedStage("unsupported_source_format", message)
			skipPendingAudioStages(&result, "blocked_by_probe_failure")
			return failAudioProcess(ctx, d, taskID, meta, &result, audio.StageProbe, "unsupported_source_format", message)
		}
		result.Stages.Probe = audio.ReadyStage()

		result.Analysis = *probeResult
		d.setProgress(ctx, taskID, 25)

		if p.Waveform {
			waveformCtx, waveformSpan := startWorkerSpan(ctx, "worker.audio.generate_waveform",
				attribute.String("file.path", tmpPath),
				attribute.Int("audio.waveform_bins", p.WaveformBins),
			)
			waveform, err := audio.GenerateWaveform(waveformCtx, tmpPath, p.WaveformBins)
			if err != nil {
				recordSpanError(waveformSpan, err)
				waveformSpan.End()
				message := fmt.Sprintf("generate waveform: %v", err)
				result.Stages.Waveform = audio.FailedStage("generate_failed", message)
				skipPendingAudioStages(&result, "not_attempted_after_waveform_failure")
				return failAudioProcess(ctx, d, taskID, meta, &result, audio.StageWaveform, "generate_failed", message)
			}
			data, err := json.Marshal(waveform)
			if err != nil {
				recordSpanError(waveformSpan, err)
				waveformSpan.End()
				message := fmt.Sprintf("marshal waveform: %v", err)
				result.Stages.Waveform = audio.FailedStage("marshal_failed", message)
				skipPendingAudioStages(&result, "not_attempted_after_waveform_failure")
				return failAudioProcess(ctx, d, taskID, meta, &result, audio.StageWaveform, "marshal_failed", message)
			}
			key := audioS3Key(p.Hash, "waveform/waveform.json")
			if err := uploadBytes(ctx, d.MediaStore, d.Config.MediaBucket, key, "application/json", data); err != nil {
				recordSpanError(waveformSpan, err)
				waveformSpan.End()
				message := fmt.Sprintf("upload waveform: %v", err)
				result.Stages.Waveform = audio.FailedStage("upload_failed", message)
				skipPendingAudioStages(&result, "not_attempted_after_waveform_failure")
				return failAudioProcess(ctx, d, taskID, meta, &result, audio.StageWaveform, "upload_failed", message)
			}
			waveformSpan.SetAttributes(attribute.Int("audio.waveform_bin_count", len(waveform.Bins)))
			waveformSpan.End()
			result.Waveform = &audio.WaveformArtifact{Key: key, Bins: len(waveform.Bins)}
			result.Stages.Waveform = audio.ReadyStage()
		}

		if p.HLS {
			var encMaterial *encryption.Material
			if p.Encrypt {
				encryptCtx, encryptSpan := startWorkerSpan(ctx, "worker.audio.setup_encryption",
					attribute.String("media.hash", p.Hash),
				)
				encMaterial, err = encryption.SetupHLSEncryption(encryptCtx, p.Hash, encryption.AssetTypeAudio, d.Config.BaseURL, d.Queries, d.KeyWrapper)
				if err != nil {
					recordSpanError(encryptSpan, err)
					encryptSpan.End()
					message := fmt.Sprintf("setup encryption: %v", err)
					result.Stages.Package = audio.FailedStage("encryption_setup_failed", message)
					skipPendingAudioStages(&result, "not_attempted_after_package_failure")
					return failAudioProcess(ctx, d, taskID, meta, &result, audio.StagePackage, "encryption_setup_failed", message)
				}
				encryptSpan.End()
			}

			scratchDir, err := prepareTempRoot("")
			if err != nil {
				message := err.Error()
				result.Stages.Package = audio.FailedStage("prepare_failed", message)
				skipPendingAudioStages(&result, "not_attempted_after_package_failure")
				return failAudioProcess(ctx, d, taskID, meta, &result, audio.StagePackage, "prepare_failed", message)
			}
			hlsDir, err := os.MkdirTemp(scratchDir, "vylux-audio-hls-*")
			if err != nil {
				message := fmt.Sprintf("create audio hls dir: %v", err)
				result.Stages.Package = audio.FailedStage("prepare_failed", message)
				skipPendingAudioStages(&result, "not_attempted_after_package_failure")
				return failAudioProcess(ctx, d, taskID, meta, &result, audio.StagePackage, "prepare_failed", message)
			}
			defer os.RemoveAll(hlsDir)

			hlsCtx, hlsSpan := startWorkerSpan(ctx, "worker.audio.package_hls",
				attribute.String("file.path", tmpPath),
				attribute.String("file.output_dir", hlsDir),
			)
			hlsOptions := &audio.HLSOptions{}
			if encMaterial != nil {
				hlsOptions.Encryption = &audio.EncryptionConfig{
					KeyID:            fmt.Sprintf("%x", encMaterial.KeyID),
					Key:              encMaterial.Key,
					ProtectionScheme: encMaterial.ProtectionScheme,
					HLSKeyURI:        encMaterial.KeyURI,
				}
			}
			hlsResult, err := audio.PackageHLS(hlsCtx, tmpPath, hlsDir, hlsOptions)
			if err != nil {
				recordSpanError(hlsSpan, err)
				hlsSpan.End()
				message := fmt.Sprintf("package hls: %v", err)
				result.Stages.Package = audio.FailedStage("package_failed", message)
				skipPendingAudioStages(&result, "not_attempted_after_package_failure")
				return failAudioProcess(ctx, d, taskID, meta, &result, audio.StagePackage, "package_failed", message)
			}
			hlsSpan.SetAttributes(attribute.Int("audio.rendition_count", len(hlsResult.Renditions)))
			hlsSpan.End()

			uploadedKeys, err := uploadAudioHLSDir(ctx, d.MediaStore, d.Config.MediaBucket, p.Hash, hlsDir)
			if err != nil {
				message := fmt.Sprintf("upload audio hls: %v", err)
				result.Stages.Package = audio.FailedStage("upload_failed", message)
				skipPendingAudioStages(&result, "not_attempted_after_package_failure")
				return failAudioProcess(ctx, d, taskID, meta, &result, audio.StagePackage, "upload_failed", message)
			}

			result.Streaming = buildAudioStreamingResult(p.Hash, hlsResult, encMaterial != nil)
			if encMaterial != nil {
				result.Encryption = &audio.EncryptionArtifact{
					Scheme:      encMaterial.ProtectionScheme,
					KID:         fmt.Sprintf("%x", encMaterial.KeyID),
					KeyEndpoint: encMaterial.KeyURI,
				}
			}
			result.Stages.Package = audio.ReadyStage()
			_ = uploadedKeys
		}

		d.setProgress(ctx, taskID, 65)

		downloadDir, err := prepareTempRoot("")
		if err != nil {
			message := err.Error()
			result.Stages.Downloads = audio.FailedStage("prepare_failed", message)
			return failAudioProcess(ctx, d, taskID, meta, &result, audio.StageDownloads, "prepare_failed", message)
		}

		if p.MP3 {
			mp3Path := filepath.Join(downloadDir, fmt.Sprintf("%s.mp3", taskID))
			if err := audio.EncodeMP3(ctx, tmpPath, mp3Path, p.MP3Bitrate); err != nil {
				message := fmt.Sprintf("encode mp3: %v", err)
				result.Stages.Downloads = audio.FailedStage("encode_failed", message)
				return failAudioProcess(ctx, d, taskID, meta, &result, audio.StageDownloads, "encode_failed", message)
			}
			defer os.Remove(mp3Path)

			key := audioS3Key(p.Hash, "downloads/audio.mp3")
			if err := uploadFile(ctx, d.MediaStore, d.Config.MediaBucket, key, audioMimeType("mp3"), mp3Path); err != nil {
				message := fmt.Sprintf("upload mp3: %v", err)
				result.Stages.Downloads = audio.FailedStage("upload_failed", message)
				return failAudioProcess(ctx, d, taskID, meta, &result, audio.StageDownloads, "upload_failed", message)
			}
			info, err := os.Stat(mp3Path)
			if err != nil {
				message := fmt.Sprintf("stat mp3: %v", err)
				result.Stages.Downloads = audio.FailedStage("finalize_failed", message)
				return failAudioProcess(ctx, d, taskID, meta, &result, audio.StageDownloads, "finalize_failed", message)
			}
			result.Downloads = append(result.Downloads, audio.DownloadArtifact{
				Format:  "mp3",
				Bitrate: parseAudioBitrate(p.MP3Bitrate),
				Key:     key,
				Size:    info.Size(),
			})
		}

		if p.FLAC {
			flacPath := filepath.Join(downloadDir, fmt.Sprintf("%s.flac", taskID))
			if err := audio.EncodeFLAC(ctx, tmpPath, flacPath); err != nil {
				message := fmt.Sprintf("encode flac: %v", err)
				result.Stages.Downloads = audio.FailedStage("encode_failed", message)
				return failAudioProcess(ctx, d, taskID, meta, &result, audio.StageDownloads, "encode_failed", message)
			}
			defer os.Remove(flacPath)

			key := audioS3Key(p.Hash, "downloads/audio.flac")
			if err := uploadFile(ctx, d.MediaStore, d.Config.MediaBucket, key, audioMimeType("flac"), flacPath); err != nil {
				message := fmt.Sprintf("upload flac: %v", err)
				result.Stages.Downloads = audio.FailedStage("upload_failed", message)
				return failAudioProcess(ctx, d, taskID, meta, &result, audio.StageDownloads, "upload_failed", message)
			}
			info, err := os.Stat(flacPath)
			if err != nil {
				message := fmt.Sprintf("stat flac: %v", err)
				result.Stages.Downloads = audio.FailedStage("finalize_failed", message)
				return failAudioProcess(ctx, d, taskID, meta, &result, audio.StageDownloads, "finalize_failed", message)
			}
			result.Downloads = append(result.Downloads, audio.DownloadArtifact{
				Format: "flac",
				Key:    key,
				Size:   info.Size(),
			})
		}
		if p.MP3 || p.FLAC {
			result.Stages.Downloads = audio.ReadyStage()
		}

		d.setProgress(ctx, taskID, 95)
		return d.completeJob(ctx, taskID, meta, result)
	}
}

func skipPendingAudioStages(result *audio.ProcessResult, reason string) {
	if result == nil {
		return
	}
	stages := []*audio.StageState{
		&result.Stages.Source,
		&result.Stages.Probe,
		&result.Stages.Package,
		&result.Stages.Downloads,
		&result.Stages.Waveform,
	}
	for _, stage := range stages {
		if stage.Status == audio.StatusPending {
			*stage = audio.SkippedStage(reason)
		}
	}
}

func failAudioProcess(ctx context.Context, d *Deps, jobID string, meta jobMeta, result *audio.ProcessResult, stage, code, message string) error {
	if result != nil {
		result.MarkFailure(stage, code, message)
	}
	return d.failJobWithResult(ctx, jobID, meta, audioProcessFailureError(result), result)
}

func audioProcessFailureError(result *audio.ProcessResult) error {
	if result == nil || result.Failure == nil {
		return fmt.Errorf("audio process failed")
	}
	return fmt.Errorf("audio process failed at %s: %s", result.Failure.Stage, result.Failure.Message)
}

func buildAudioStreamingResult(hash string, result *audio.HLSResult, encrypted bool) *audio.HLSStreamingArtifact {
	out := &audio.HLSStreamingArtifact{
		Protocol:       "hls",
		Container:      "cmaf",
		Encrypted:      encrypted,
		MasterPlaylist: audioS3Key(hash, result.MasterPlaylistPath),
		Renditions:     make([]audio.RenditionArtifact, 0, len(result.Renditions)),
	}
	for i := range result.Renditions {
		rendition := &result.Renditions[i]
		out.Renditions = append(out.Renditions, audio.RenditionArtifact{
			ID:       rendition.ID,
			Role:     rendition.Role,
			Language: rendition.Language,
			Codec:    rendition.Codec,
			Channels: rendition.Channels,
			Bitrate:  rendition.Bitrate,
			Playlist: audioS3Key(hash, rendition.PlaylistPath),
			Init:     audioS3Key(hash, rendition.InitPath),
			Segments: len(rendition.Segments),
		})
	}
	if len(out.Renditions) > 0 {
		out.DefaultRenditionID = out.Renditions[0].ID
	}

	return out
}

func uploadAudioHLSDir(ctx context.Context, store storage.Storage, bucket, hash, outDir string) ([]string, error) {
	ctx, span := startWorkerSpan(ctx, "worker.upload.audio_hls_dir",
		attribute.String("storage.role", "media"),
		attribute.String("storage.bucket", bucket),
		attribute.String("media.hash", hash),
		attribute.String("file.path", outDir),
	)
	defer span.End()

	var uploaded []string
	err := filepath.Walk(outDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(outDir, path)
		if err != nil {
			return fmt.Errorf("rel path: %w", err)
		}
		ct := hlsMimeType(rel)
		if ct == "" {
			return nil
		}
		key := audioS3Key(hash, filepath.ToSlash(rel))
		if err := uploadFile(ctx, store, bucket, key, ct, path); err != nil {
			return fmt.Errorf("upload %s: %w", rel, err)
		}
		uploaded = append(uploaded, key)
		return nil
	})
	if err != nil {
		recordSpanError(span, err)
		return nil, err
	}

	span.SetAttributes(attribute.Int("storage.objects_uploaded", len(uploaded)))
	return uploaded, nil
}

func parseAudioBitrate(value string) int {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return 0
	}
	result, _ := strconv.Atoi(strings.TrimSuffix(value, "k"))
	if strings.HasSuffix(value, "k") {
		return result * 1000
	}
	return result
}
