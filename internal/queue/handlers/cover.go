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

// coverResult describes the generated cover image.
type coverResult = jobflow.CoverArtifact

// HandleVideoCover returns an asynq handler for video:cover tasks.
//
// Flow:
//  1. Download source video to temp file
//  2. Extract cover frame via FFmpeg
//  3. Upload JPEG to media-bucket
//  4. Update job results in DB
func HandleVideoCover(d *Deps) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var p queue.VideoCoverPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal video:cover payload: %w", err)
		}

		taskID, _ := asynq.GetTaskID(ctx)
		slog.Info("processing video:cover",
			apptracing.LogFields(ctx,
				"job_id", taskID,
				"hash", p.Hash,
				"timestamp_sec", p.TimestampSec,
			)...,
		)

		meta := jobMeta{
			Type:        queue.TypeVideoCover,
			Hash:        p.Hash,
			CallbackURL: p.CallbackURL,
		}

		_ = d.Queries.UpdateJobStatus(ctx, dbq.UpdateJobStatusParams{
			ID:     taskID,
			Status: "processing",
		})

		// Default timestamp.
		ts := p.TimestampSec
		if ts == 0 {
			ts = 1.0
		}

		// Download source video.
		tmpPath, cleanup, err := downloadToTemp(ctx, d.SourceStore, d.Config.SourceBucket, p.Source, "vylux-cover-*")
		if err != nil {
			return d.failJob(ctx, taskID, meta, fmt.Errorf("download source: %w", err))
		}
		defer cleanup()

		d.setProgress(ctx, taskID, 30)

		// Extract cover frame.
		extractCtx, span := startWorkerSpan(ctx, "worker.video.extract_cover",
			attribute.String("file.path", tmpPath),
			attribute.Float64("video.timestamp_sec", ts),
		)
		result, err := video.ExtractCover(extractCtx, tmpPath, ts)
		if err != nil {
			recordSpanError(span, err)
			span.End()
			return d.failJob(ctx, taskID, meta, fmt.Errorf("extract cover: %w", err))
		}
		span.SetAttributes(
			attribute.String("image.format", result.Format),
			attribute.Int("image.output_bytes", len(result.Data)),
		)
		span.End()

		d.setProgress(ctx, taskID, 70)

		// Upload to media-bucket.
		key := videoS3Key(p.Hash, "cover.jpg")
		if err := uploadBytes(ctx, d.MediaStore, d.Config.MediaBucket, key, "image/jpeg", result.Data); err != nil {
			return d.failJob(ctx, taskID, meta, fmt.Errorf("upload cover: %w", err))
		}

		return d.completeJob(ctx, taskID, meta, coverResult{
			Key:    key,
			Format: result.Format,
			Size:   len(result.Data),
		})
	}
}
