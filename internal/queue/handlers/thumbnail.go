package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"Vylux/internal/db/dbq"
	"Vylux/internal/image"
	"Vylux/internal/queue"
	apptracing "Vylux/internal/tracing"

	"github.com/hibiken/asynq"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// thumbnailResult describes a single generated thumbnail.
type thumbnailResult struct {
	Variant string `json:"variant"`
	Key     string `json:"key"`
	Format  string `json:"format"`
	Size    int    `json:"size"`
}

// HandleImageThumbnail returns an asynq handler for image:thumbnail tasks.
//
// Flow:
//  1. Fetch source image from app-bucket
//  2. For each output variant: resize + format-convert via vipsgen
//  3. Upload to media-bucket
//  4. Update job results in DB
func HandleImageThumbnail(d *Deps) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var p queue.ImageThumbnailPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal image:thumbnail payload: %w", err)
		}

		taskID, _ := asynq.GetTaskID(ctx)
		slog.Info("processing image:thumbnail",
			apptracing.LogFields(ctx,
				"job_id", taskID,
				"hash", p.Hash,
				"outputs", len(p.Outputs),
			)...,
		)

		meta := jobMeta{
			Type:        queue.TypeImageThumbnail,
			Hash:        p.Hash,
			CallbackURL: p.CallbackURL,
		}

		// Mark processing.
		_ = d.Queries.UpdateJobStatus(ctx, dbq.UpdateJobStatusParams{
			ID:     taskID,
			Status: "processing",
		})

		// Provide sensible defaults when no outputs are specified.
		if len(p.Outputs) == 0 {
			p.Outputs = []queue.ThumbnailOutput{
				{Variant: "thumb", Width: 300, Format: "webp"},
			}
		}

		// Fetch source image bytes.
		src, err := fetchSource(ctx, d.SourceStore, d.Config.SourceBucket, p.Source)
		if err != nil {
			return d.failJob(ctx, taskID, meta, fmt.Errorf("fetch source: %w", err))
		}

		results := make([]thumbnailResult, 0, len(p.Outputs))

		for i, out := range p.Outputs {
			canonicalFormat := image.ParseFormat(out.Format).Ext()
			opts := image.Options{
				Width:  out.Width,
				Height: out.Height,
				Format: image.ParseFormat(out.Format),
			}

			variantCtx, span := apptracing.Tracer("vylux/worker").Start(ctx, "worker.image.process.variant",
				trace.WithAttributes(
					attribute.String("image.variant", out.Variant),
					attribute.String("image.format", canonicalFormat),
					attribute.Int("image.width", out.Width),
					attribute.Int("image.height", out.Height),
				),
			)

			_ = variantCtx
			processed, err := image.Process(src, opts)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				span.End()
				return d.failJob(ctx, taskID, meta, fmt.Errorf("process variant %s: %w", out.Variant, err))
			}
			span.SetAttributes(attribute.Int("image.output_bytes", len(processed)))
			span.End()

			key := imageS3Key(p.Hash, out.Variant, canonicalFormat)
			ct := imageMimeType(canonicalFormat)

			if err := uploadBytes(ctx, d.MediaStore, d.Config.MediaBucket, key, ct, processed); err != nil {
				return d.failJob(ctx, taskID, meta, fmt.Errorf("upload %s: %w", out.Variant, err))
			}

			results = append(results, thumbnailResult{
				Variant: out.Variant,
				Key:     key,
				Format:  canonicalFormat,
				Size:    len(processed),
			})

			// Update progress.
			pct := int32((i + 1) * 100 / len(p.Outputs))
			d.setProgress(ctx, taskID, pct)
		}

		return d.completeJob(ctx, taskID, meta, results)
	}
}
