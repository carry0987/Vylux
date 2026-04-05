package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"

	"Vylux/internal/db/dbq"
	"Vylux/internal/encryption"
	"Vylux/internal/jobflow"
	"Vylux/internal/queue"
	apptracing "Vylux/internal/tracing"
	"Vylux/internal/video"

	"github.com/hibiken/asynq"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"
)

type coverAttempt struct {
	artifact *coverResult
	data     []byte
	errCode  string
	err      error
}

type previewAttempt struct {
	artifact *previewResult
	data     []byte
	errCode  string
	err      error
}

// HandleVideoFull returns an asynq handler for video:full tasks.
//
// This is the all-in-one handler that performs cover extraction,
// animated preview generation, and split-track HLS CMAF packaging sequentially
// (cover + preview in parallel, then transcode).
//
// Flow:
//  1. Download source video to temp file (once)
//  2. Extract cover + generate preview in parallel
//  3. Encode and package HLS CMAF output
//  4. Upload all outputs to media-bucket
//  5. Update job with aggregated results
func HandleVideoFull(d *Deps) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var p queue.VideoFullPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal video:full payload: %w", err)
		}

		taskID, _ := asynq.GetTaskID(ctx)
		encrypt := p.Options.Transcode != nil && p.Options.Transcode.Encrypt
		slog.Info("processing video:full",
			apptracing.LogFields(ctx,
				"job_id", taskID,
				"hash", p.Hash,
				"encrypt", encrypt,
			)...,
		)

		meta := jobMeta{
			Type:        queue.TypeVideoFull,
			Hash:        p.Hash,
			CallbackURL: p.CallbackURL,
		}

		result := jobflow.NewVideoFullResult()

		_ = d.Queries.UpdateJobStatus(ctx, dbq.UpdateJobStatusParams{
			ID:     taskID,
			Status: "processing",
		})

		// ── 1. Download source video ──
		tmpPath, cleanupSrc, err := downloadToTemp(ctx, d.SourceStore, d.Config.SourceBucket, p.Source, "vylux-full-*")
		if err != nil {
			result.Stages.Source = failedStage("download_failed", fmt.Sprintf("download source: %v", err))
			result.Stages.Cover = skippedStage("blocked_by_source_failure")
			result.Stages.Preview = skippedStage("blocked_by_source_failure")
			result.Stages.Transcode = skippedStage("blocked_by_source_failure")
			setRetryPlan(&result, jobflow.RetryStrategyRetryJob, []string{queue.TypeVideoFull}, []string{jobflow.StageSource}, "source download failed")
			return d.failJobWithResult(ctx, taskID, meta, videoFullFailureError(result), result)
		}
		defer cleanupSrc()
		result.Stages.Source = readyStage()

		d.setProgress(ctx, taskID, 5)

		// ── 2. Cover + Preview in parallel ──
		var coverOutcome coverAttempt
		var previewOutcome previewAttempt

		g, gCtx := errgroup.WithContext(ctx)

		// Cover extraction.
		g.Go(func() error {
			ts := 1.0
			if p.Options.Cover != nil && p.Options.Cover.TimestampSec > 0 {
				ts = p.Options.Cover.TimestampSec
			}

			coverCtx, span := startWorkerSpan(gCtx, "worker.video.extract_cover",
				attribute.String("file.path", tmpPath),
				attribute.Float64("video.timestamp_sec", ts),
			)
			r, err := video.ExtractCover(coverCtx, tmpPath, ts)
			if err != nil {
				recordSpanError(span, err)
				span.End()
				coverOutcome.errCode = "extract_failed"
				coverOutcome.err = fmt.Errorf("extract cover: %w", err)
				return nil
			}
			span.SetAttributes(
				attribute.String("image.format", r.Format),
				attribute.Int("image.output_bytes", len(r.Data)),
			)
			span.End()
			coverOutcome.artifact = &coverResult{Format: r.Format, Size: len(r.Data)}
			coverOutcome.data = r.Data
			return nil
		})

		// Preview generation.
		g.Go(func() error {
			opts := video.DefaultPreviewOptions()
			if p.Options.Preview != nil {
				if p.Options.Preview.StartSec != 0 {
					opts.StartSec = p.Options.Preview.StartSec
				}
				if p.Options.Preview.Duration != 0 {
					opts.Duration = p.Options.Preview.Duration
				}
				if p.Options.Preview.Width != 0 {
					opts.Width = p.Options.Preview.Width
				}
				if p.Options.Preview.FPS != 0 {
					opts.FPS = p.Options.Preview.FPS
				}
				if p.Options.Preview.Format != "" {
					opts.Format = p.Options.Preview.Format
				}
			}

			previewCtx, span := startWorkerSpan(gCtx, "worker.video.generate_preview",
				attribute.String("file.path", tmpPath),
				attribute.Float64("video.start_sec", opts.StartSec),
				attribute.Float64("video.duration_sec", opts.Duration),
				attribute.Int("image.width", opts.Width),
				attribute.Int("video.fps", opts.FPS),
				attribute.String("image.format", opts.Format),
			)
			r, err := video.GeneratePreview(previewCtx, tmpPath, opts)
			if err != nil {
				recordSpanError(span, err)
				span.End()
				previewOutcome.errCode = "generate_failed"
				previewOutcome.err = fmt.Errorf("generate preview: %w", err)
				return nil
			}
			span.SetAttributes(
				attribute.String("image.format", r.Format),
				attribute.Int("image.output_bytes", len(r.Data)),
			)
			span.End()
			previewOutcome.artifact = &previewResult{Format: r.Format, Size: len(r.Data)}
			previewOutcome.data = r.Data
			return nil
		})

		_ = g.Wait()

		d.setProgress(ctx, taskID, 20)

		// Upload cover if it was generated successfully.
		if coverOutcome.err != nil {
			result.Stages.Cover = failedStage(coverOutcome.errCode, coverOutcome.err.Error())
		} else {
			coverKey := videoS3Key(p.Hash, "cover.jpg")
			if err := uploadBytes(ctx, d.MediaStore, d.Config.MediaBucket, coverKey, "image/jpeg", coverOutcome.data); err != nil {
				result.Stages.Cover = failedStage("upload_failed", fmt.Sprintf("upload cover: %v", err))
			} else {
				coverOutcome.artifact.Key = coverKey
				result.Artifacts.Cover = coverOutcome.artifact
				result.Stages.Cover = readyStage()
			}
		}

		// Upload preview if it was generated successfully.
		if previewOutcome.err != nil {
			result.Stages.Preview = failedStage(previewOutcome.errCode, previewOutcome.err.Error())
		} else {
			previewExt := previewOutcome.artifact.Format
			if previewExt == "" {
				previewExt = "webp"
			}
			previewKey := videoS3Key(p.Hash, "preview."+previewExt)
			if err := uploadBytes(ctx, d.MediaStore, d.Config.MediaBucket, previewKey, imageMimeType(previewExt), previewOutcome.data); err != nil {
				result.Stages.Preview = failedStage("upload_failed", fmt.Sprintf("upload preview: %v", err))
			} else {
				previewOutcome.artifact.Key = previewKey
				result.Artifacts.Preview = previewOutcome.artifact
				result.Stages.Preview = readyStage()
			}
		}

		if result.Stages.Cover.Status == jobflow.StatusFailed || result.Stages.Preview.Status == jobflow.StatusFailed {
			result.Stages.Transcode = skippedStage("blocked_by_failed_dependencies")
			setRetryPlan(&result, jobflow.RetryStrategyRetryTasks, retryTasksForCoverPreview(result), retryStagesForCoverPreview(result), "cover/preview stage failed")
			return d.failJobWithResult(ctx, taskID, meta, videoFullFailureError(result), result)
		}

		d.setProgress(ctx, taskID, 25)

		// ── 3. HLS Transcode ──
		scratchDir, err := prepareTempRoot("")
		if err != nil {
			result.Stages.Transcode = failedStage("prepare_failed", err.Error())
			setRetryPlan(&result, jobflow.RetryStrategyRetryTasks, []string{queue.TypeVideoTranscode}, []string{jobflow.StageTranscode}, "transcode preparation failed")
			return d.failJobWithResult(ctx, taskID, meta, videoFullFailureError(result), result)
		}

		outDir, err := os.MkdirTemp(scratchDir, "vylux-hls-*")
		if err != nil {
			result.Stages.Transcode = failedStage("prepare_failed", fmt.Sprintf("create temp dir: %v", err))
			setRetryPlan(&result, jobflow.RetryStrategyRetryTasks, []string{queue.TypeVideoTranscode}, []string{jobflow.StageTranscode}, "transcode preparation failed")
			return d.failJobWithResult(ctx, taskID, meta, videoFullFailureError(result), result)
		}
		defer os.RemoveAll(outDir)

		transcodeOpts := video.TranscodeOptions{
			Variants:   video.DefaultVariants(),
			AudioTrack: video.DefaultAudioTrack(),
		}

		var encMaterial *encryption.Material

		// Generate raw-key encryption metadata for Shaka packaging.
		if encrypt {
			encryptCtx, span := startWorkerSpan(ctx, "worker.video.setup_encryption",
				attribute.String("media.hash", p.Hash),
			)
			encMaterial, err = encryption.SetupHLSEncryption(encryptCtx, p.Hash, d.Config.BaseURL, d.Queries, d.KeyWrapper)
			if err != nil {
				recordSpanError(span, err)
				span.End()
				result.Stages.Transcode = failedStage("encryption_setup_failed", fmt.Sprintf("setup encryption: %v", err))
				setRetryPlan(&result, jobflow.RetryStrategyRetryTasks, []string{queue.TypeVideoTranscode}, []string{jobflow.StageTranscode}, "transcode stage failed")
				return d.failJobWithResult(ctx, taskID, meta, videoFullFailureError(result), result)
			}
			span.End()

			transcodeOpts.Encryption = &video.EncryptionConfig{
				KeyID:            fmt.Sprintf("%x", encMaterial.KeyID),
				Key:              encMaterial.Key,
				ProtectionScheme: encMaterial.ProtectionScheme,
				HLSKeyURI:        encMaterial.KeyURI,
			}
			slog.Info("CMAF raw-key encryption enabled (full)", apptracing.LogFields(ctx, "job_id", taskID, "hash", p.Hash)...)
		}

		slog.Info("starting transcode (full)",
			apptracing.LogFields(ctx,
				"job_id", taskID,
				"variants", len(transcodeOpts.Variants),
			)...,
		)

		transcodeCtx, span := startWorkerSpan(ctx, "worker.video.transcode",
			attribute.String("file.path", tmpPath),
			attribute.String("file.output_dir", outDir),
			attribute.Int("video.variant_count", len(transcodeOpts.Variants)),
			attribute.Int("video.audio_track_count", 1),
			attribute.Bool("video.encrypt", encrypt),
		)
		tcResults, err := video.Transcode(transcodeCtx, tmpPath, outDir, transcodeOpts)
		if err != nil {
			recordSpanError(span, err)
			span.End()
			result.Stages.Transcode = failedStage("transcode_failed", fmt.Sprintf("transcode: %v", err))
			setRetryPlan(&result, jobflow.RetryStrategyRetryTasks, []string{queue.TypeVideoTranscode}, []string{jobflow.StageTranscode}, "transcode stage failed")
			return d.failJobWithResult(ctx, taskID, meta, videoFullFailureError(result), result)
		}
		span.SetAttributes(
			attribute.Int("video.variant_count", len(tcResults.VideoTracks)),
			attribute.Int("video.audio_track_count", len(tcResults.AudioTracks)),
		)
		span.End()

		d.setProgress(ctx, taskID, 80)

		// Upload all HLS files.
		uploadedKeys, err := uploadHLSDir(ctx, d.MediaStore, d.Config.MediaBucket, p.Hash, outDir)
		if err != nil {
			result.Stages.Transcode = failedStage("upload_failed", fmt.Sprintf("upload HLS: %v", err))
			setRetryPlan(&result, jobflow.RetryStrategyRetryTasks, []string{queue.TypeVideoTranscode}, []string{jobflow.StageTranscode}, "transcode upload failed")
			return d.failJobWithResult(ctx, taskID, meta, videoFullFailureError(result), result)
		}

		d.setProgress(ctx, taskID, 95)

		// ── 4. Build aggregated result ──
		transcodeArtifact := buildTranscodeResult(p.Hash, tcResults, uploadedKeys, encMaterial)
		result.Artifacts.Transcode = &transcodeArtifact
		result.Stages.Transcode = readyStage()
		result.RetryPlan = jobflow.RetryPlan{Allowed: false, Strategy: jobflow.RetryStrategyNone}

		return d.completeJob(ctx, taskID, meta, result)
	}
}

func readyStage() jobflow.StageState {
	return jobflow.StageState{Status: jobflow.StatusReady}
}

func failedStage(code, message string) jobflow.StageState {
	return jobflow.StageState{
		Status:    jobflow.StatusFailed,
		ErrorCode: code,
		Error:     message,
		Retryable: true,
	}
}

func skippedStage(reason string) jobflow.StageState {
	return jobflow.StageState{
		Status: jobflow.StatusSkipped,
		Reason: reason,
	}
}

func setRetryPlan(result *jobflow.VideoFullResult, strategy string, jobTypes []string, stages []string, reason string) {
	result.RetryPlan = jobflow.RetryPlan{
		Allowed:  len(jobTypes) > 0,
		Strategy: strategy,
		JobTypes: dedupeStrings(jobTypes),
		Stages:   dedupeStrings(stages),
		Reason:   reason,
	}
}

func retryTasksForCoverPreview(result jobflow.VideoFullResult) []string {
	tasks := make([]string, 0, 3)
	if result.Stages.Cover.Status == jobflow.StatusFailed {
		tasks = append(tasks, queue.TypeVideoCover)
	}
	if result.Stages.Preview.Status == jobflow.StatusFailed {
		tasks = append(tasks, queue.TypeVideoPreview)
	}
	tasks = append(tasks, queue.TypeVideoTranscode)
	return tasks
}

func retryStagesForCoverPreview(result jobflow.VideoFullResult) []string {
	stages := make([]string, 0, 3)
	if result.Stages.Cover.Status == jobflow.StatusFailed {
		stages = append(stages, jobflow.StageCover)
	}
	if result.Stages.Preview.Status == jobflow.StatusFailed {
		stages = append(stages, jobflow.StagePreview)
	}
	stages = append(stages, jobflow.StageTranscode)
	return stages
}

func videoFullFailureError(result jobflow.VideoFullResult) error {
	failed := make([]string, 0, 3)
	for _, item := range []struct {
		name   string
		status string
	}{
		{name: jobflow.StageSource, status: result.Stages.Source.Status},
		{name: jobflow.StageCover, status: result.Stages.Cover.Status},
		{name: jobflow.StagePreview, status: result.Stages.Preview.Status},
		{name: jobflow.StageTranscode, status: result.Stages.Transcode.Status},
	} {
		if item.status == jobflow.StatusFailed {
			failed = append(failed, item.name)
		}
	}
	if len(failed) == 0 {
		return fmt.Errorf("video:full failed")
	}
	sort.Strings(failed)
	return fmt.Errorf("video:full failed: %v", failed)
}

func dedupeStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
