package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"Vylux/internal/db/dbq"
	"Vylux/internal/jobflow"
	"Vylux/internal/queue"
	apptracing "Vylux/internal/tracing"
	"Vylux/internal/video"

	"github.com/hibiken/asynq"
	"go.opentelemetry.io/otel/attribute"
)

// previewResult describes the generated animated preview.
type previewResult = jobflow.PreviewArtifact

// HandleVideoPreview returns an asynq handler for video:preview tasks.
//
// Flow:
//  1. Download source video to temp file
//  2. Generate animated preview (WebP/GIF) via FFmpeg
//  3. Upload to media-bucket
//  4. Update job results in DB
func HandleVideoPreview(d *Deps) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var p queue.VideoPreviewPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal video:preview payload: %w", err)
		}

		taskID, _ := asynq.GetTaskID(ctx)
		slog.Info("processing video:preview",
			apptracing.LogFields(ctx,
				"job_id", taskID,
				"hash", p.Hash,
				"format", p.Format,
			)...,
		)

		meta := jobMeta{
			Type:        queue.TypeVideoPreview,
			Hash:        p.Hash,
			CallbackURL: p.CallbackURL,
		}

		_ = d.Queries.UpdateJobStatus(ctx, dbq.UpdateJobStatusParams{
			ID:     taskID,
			Status: "processing",
		})

		// Build preview options (GeneratePreview handles zero-value defaults).
		opts := video.PreviewOptions{
			StartSec: p.StartSec,
			Duration: p.Duration,
			Width:    p.Width,
			FPS:      p.FPS,
			Format:   p.Format,
		}

		// Download source video.
		tmpPath, cleanup, err := downloadToTemp(ctx, d.SourceStore, d.Config.SourceBucket, p.Source, "vylux-preview-*")
		if err != nil {
			return d.failJob(ctx, taskID, meta, fmt.Errorf("download source: %w", err))
		}
		defer cleanup()

		d.setProgress(ctx, taskID, 30)

		// Generate animated preview.
		previewCtx, span := startWorkerSpan(ctx, "worker.video.generate_preview",
			attribute.String("file.path", tmpPath),
			attribute.Float64("video.start_sec", opts.StartSec),
			attribute.Float64("video.duration_sec", opts.Duration),
			attribute.Int("image.width", opts.Width),
			attribute.Int("video.fps", opts.FPS),
			attribute.String("image.format", opts.Format),
		)
		result, err := video.GeneratePreview(previewCtx, tmpPath, opts)
		if err != nil {
			recordSpanError(span, err)
			span.End()
			return d.failJob(ctx, taskID, meta, fmt.Errorf("generate preview: %w", err))
		}
		span.SetAttributes(
			attribute.String("image.format", result.Format),
			attribute.Int("image.output_bytes", len(result.Data)),
		)
		span.End()

		d.setProgress(ctx, taskID, 70)

		// Upload to media-bucket.
		ext := result.Format
		if ext == "" {
			ext = "webp"
		}
		key := videoS3Key(p.Hash, "preview."+ext)
		ct := imageMimeType(ext)

		if err := uploadBytes(ctx, d.MediaStore, d.Config.MediaBucket, key, ct, result.Data); err != nil {
			return d.failJob(ctx, taskID, meta, fmt.Errorf("upload preview: %w", err))
		}

		return d.completeJob(ctx, taskID, meta, previewResult{
			Key:    key,
			Format: result.Format,
			Size:   len(result.Data),
		})
	}
}
