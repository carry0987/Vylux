package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"Vylux/internal/db/dbq"
	"Vylux/internal/encryption"
	"Vylux/internal/jobflow"
	"Vylux/internal/queue"
	apptracing "Vylux/internal/tracing"
	"Vylux/internal/video"

	"github.com/hibiken/asynq"
	"go.opentelemetry.io/otel/attribute"
)

// transcodeResult describes the packaged split-track HLS CMAF output.
type transcodeResult = jobflow.TranscodeArtifact
type streamingResult = jobflow.StreamingArtifact
type audioTrackResult = jobflow.AudioTrackArtifact
type videoTrackResult = jobflow.VideoTrackArtifact
type encryptionResult = jobflow.EncryptionArtifact

// HandleVideoTranscode returns an asynq handler for video:transcode tasks.
//
// Flow:
//  1. Download source video to temp file
//  2. Encode split video/audio MP4 tracks via FFmpeg
//  3. Package encrypted HLS CMAF output via Shaka Packager
//  4. Upload packaged assets to media-bucket
//  5. Update job results in DB
func HandleVideoTranscode(d *Deps) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var p queue.VideoTranscodePayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal video:transcode payload: %w", err)
		}

		taskID, _ := asynq.GetTaskID(ctx)
		slog.Info("processing video:transcode",
			apptracing.LogFields(ctx,
				"job_id", taskID,
				"hash", p.Hash,
				"encrypt", p.Encrypt,
			)...,
		)

		meta := jobMeta{
			Type:        queue.TypeVideoTranscode,
			Hash:        p.Hash,
			CallbackURL: p.CallbackURL,
		}

		_ = d.Queries.UpdateJobStatus(ctx, dbq.UpdateJobStatusParams{
			ID:     taskID,
			Status: "processing",
		})

		// Download source video to temp file.
		tmpPath, cleanupSrc, err := downloadToTemp(ctx, d.SourceStore, d.Config.SourceBucket, p.Source, "vylux-transcode-*")
		if err != nil {
			return d.failJob(ctx, taskID, meta, fmt.Errorf("download source: %w", err))
		}
		defer cleanupSrc()

		d.setProgress(ctx, taskID, 5)

		// Create temp output directory for HLS files.
		scratchDir, err := prepareTempRoot("")
		if err != nil {
			return d.failJob(ctx, taskID, meta, err)
		}

		outDir, err := os.MkdirTemp(scratchDir, "vylux-hls-*")
		if err != nil {
			return d.failJob(ctx, taskID, meta, fmt.Errorf("create temp dir: %w", err))
		}
		defer os.RemoveAll(outDir)

		// Configure transcode options.
		opts := video.TranscodeOptions{
			Variants:   video.DefaultVariants(),
			AudioTrack: video.DefaultAudioTrack(),
		}

		var encMaterial *encryption.Material

		// Generate raw-key encryption metadata for Shaka packaging.
		if p.Encrypt {
			encryptCtx, span := startWorkerSpan(ctx, "worker.video.setup_encryption",
				attribute.String("media.hash", p.Hash),
			)
			encMaterial, err = encryption.SetupHLSEncryption(encryptCtx, p.Hash, d.Config.BaseURL, d.Queries, d.KeyWrapper)
			if err != nil {
				recordSpanError(span, err)
				span.End()
				return d.failJob(ctx, taskID, meta, fmt.Errorf("setup encryption: %w", err))
			}
			span.End()

			opts.Encryption = &video.EncryptionConfig{
				KeyID:            fmt.Sprintf("%x", encMaterial.KeyID),
				Key:              encMaterial.Key,
				ProtectionScheme: encMaterial.ProtectionScheme,
				HLSKeyURI:        encMaterial.KeyURI,
			}
			slog.Info("CMAF raw-key encryption enabled", apptracing.LogFields(ctx, "job_id", taskID, "hash", p.Hash)...)
		}

		d.setProgress(ctx, taskID, 10)

		// Run FFmpeg transcode.
		slog.Info("starting transcode",
			apptracing.LogFields(ctx,
				"job_id", taskID,
				"variants", len(opts.Variants),
			)...,
		)

		transcodeCtx, span := startWorkerSpan(ctx, "worker.video.transcode",
			attribute.String("file.path", tmpPath),
			attribute.String("file.output_dir", outDir),
			attribute.Int("video.variant_count", len(opts.Variants)),
			attribute.Int("video.audio_track_count", 1),
			attribute.Bool("video.encrypt", p.Encrypt),
		)
		results, err := video.Transcode(transcodeCtx, tmpPath, outDir, opts)
		if err != nil {
			recordSpanError(span, err)
			span.End()
			return d.failJob(ctx, taskID, meta, fmt.Errorf("transcode: %w", err))
		}
		span.SetAttributes(
			attribute.Int("video.variant_count", len(results.VideoTracks)),
			attribute.Int("video.audio_track_count", len(results.AudioTracks)),
		)
		span.End()

		d.setProgress(ctx, taskID, 75)

		// Upload all HLS files to media-bucket.
		uploadedKeys, err := uploadHLSDir(ctx, d.MediaStore, d.Config.MediaBucket, p.Hash, outDir)
		if err != nil {
			return d.failJob(ctx, taskID, meta, fmt.Errorf("upload HLS: %w", err))
		}

		d.setProgress(ctx, taskID, 95)

		return d.completeJob(ctx, taskID, meta, buildTranscodeResult(p.Hash, results, uploadedKeys, encMaterial))
	}
}

func buildTranscodeResult(hash string, result *video.TranscodeResult, uploadedKeys []string, material *encryption.Material) transcodeResult {
	out := transcodeResult{
		Streaming: streamingResult{
			Protocol:       "hls",
			Container:      "cmaf",
			Encrypted:      material != nil,
			MasterPlaylist: videoS3Key(hash, result.MasterPlaylistPath),
		},
		UploadedKeys: uploadedKeys,
	}

	for _, track := range result.AudioTracks {
		out.AudioTracks = append(out.AudioTracks, audioTrackResult{
			ID:       track.ID,
			Role:     track.Role,
			Language: track.Language,
			Codec:    track.Codec,
			Channels: track.Channels,
			Bitrate:  track.Bitrate,
			Playlist: videoS3Key(hash, track.PlaylistPath),
			Init:     videoS3Key(hash, track.InitPath),
			Segments: len(track.Segments),
		})
	}
	if len(out.AudioTracks) > 0 {
		out.Streaming.DefaultAudioTrackID = out.AudioTracks[0].ID
	}

	for _, track := range result.VideoTracks {
		videoTrack := videoTrackResult{
			ID:       track.ID,
			Codec:    string(track.Codec),
			Width:    track.Width,
			Height:   track.Height,
			Bitrate:  track.Bitrate,
			Playlist: videoS3Key(hash, track.PlaylistPath),
			Init:     videoS3Key(hash, track.InitPath),
			Segments: len(track.Segments),
		}
		if track.AudioTrackID != "" {
			videoTrack.AudioTrackIDs = []string{track.AudioTrackID}
		}
		out.VideoTracks = append(out.VideoTracks, videoTrack)
	}

	if material != nil {
		out.Encryption = &encryptionResult{
			Scheme:      material.ProtectionScheme,
			KID:         fmt.Sprintf("%x", material.KeyID),
			KeyEndpoint: material.KeyURI,
		}
	}

	return out
}
