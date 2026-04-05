package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"Vylux/internal/config"
	"Vylux/internal/db/dbq"
	"Vylux/internal/encryption"
	"Vylux/internal/queue"
	"Vylux/internal/storage"
	apptracing "Vylux/internal/tracing"
	"Vylux/internal/webhook"

	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Deps holds shared dependencies for all worker task handlers.
type Deps struct {
	SourceStore storage.Storage
	MediaStore  storage.Storage
	Queries     *dbq.Queries
	QueueClient *queue.Client
	Config      *config.Config
	KeyWrapper  *encryption.KeyWrapper
	Webhook     *webhook.Client
}

// jobMeta carries per-task metadata needed for webhook delivery.
type jobMeta struct {
	Type        string
	Hash        string
	CallbackURL string
}

// ── S3 key helpers ──

// imageS3Key builds the S3 object key for an image variant.
func imageS3Key(hash, variant, format string) string {
	prefix := hash
	if len(hash) >= 2 {
		prefix = hash[:2]
	}
	return "images/" + prefix + "/" + hash + "/" + variant + "." + format
}

// videoS3Key builds the S3 object key for a video asset.
func videoS3Key(hash, relPath string) string {
	prefix := hash
	if len(hash) >= 2 {
		prefix = hash[:2]
	}
	return "videos/" + prefix + "/" + hash + "/" + relPath
}

// ── I/O helpers ──

func startWorkerSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return apptracing.Tracer("vylux/worker").Start(ctx, name, trace.WithAttributes(attrs...))
}

func recordSpanError(span trace.Span, err error) {
	if err == nil {
		return
	}

	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

func prepareTempRoot(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = config.ScratchDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create scratch dir %s: %w", dir, err)
	}

	return dir, nil
}

// fetchSource reads an object from S3 fully into memory.
func fetchSource(ctx context.Context, store storage.Storage, bucket, key string) ([]byte, error) {
	ctx, span := startWorkerSpan(ctx, "worker.fetch.source",
		attribute.String("storage.role", "source"),
		attribute.String("storage.bucket", bucket),
		attribute.String("storage.key", key),
	)
	defer span.End()

	rc, err := store.Get(ctx, bucket, key)
	if err != nil {
		recordSpanError(span, err)
		return nil, fmt.Errorf("fetch %s/%s: %w", bucket, key, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		recordSpanError(span, err)
		return nil, err
	}

	span.SetAttributes(attribute.Int("storage.bytes", len(data)))
	return data, nil
}

// downloadToTemp downloads an S3 object to a temporary file.
func downloadToTemp(ctx context.Context, store storage.Storage, bucket, key, pattern string) (string, func(), error) {
	ctx, span := startWorkerSpan(ctx, "worker.download.source",
		attribute.String("storage.role", "source"),
		attribute.String("storage.bucket", bucket),
		attribute.String("storage.key", key),
	)
	defer span.End()

	rc, err := store.Get(ctx, bucket, key)
	if err != nil {
		recordSpanError(span, err)
		return "", nil, fmt.Errorf("fetch %s/%s: %w", bucket, key, err)
	}
	defer rc.Close()

	tempDir, err := prepareTempRoot("")
	if err != nil {
		recordSpanError(span, err)
		return "", nil, err
	}

	f, err := os.CreateTemp(tempDir, pattern)
	if err != nil {
		recordSpanError(span, err)
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}

	n, err := io.Copy(f, rc)
	if err != nil {
		f.Close()
		os.Remove(f.Name())
		recordSpanError(span, err)
		return "", nil, fmt.Errorf("download to temp: %w", err)
	}

	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		recordSpanError(span, err)
		return "", nil, fmt.Errorf("close temp file: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("storage.bytes", n),
		attribute.String("file.path", f.Name()),
	)
	cleanup := func() { os.Remove(f.Name()) }
	return f.Name(), cleanup, nil
}

// uploadBytes writes byte data to S3.
func uploadBytes(ctx context.Context, store storage.Storage, bucket, key, contentType string, data []byte) error {
	ctx, span := startWorkerSpan(ctx, "worker.upload.object",
		attribute.String("storage.role", "media"),
		attribute.String("storage.bucket", bucket),
		attribute.String("storage.key", key),
		attribute.String("storage.content_type", contentType),
		attribute.Int("storage.bytes", len(data)),
	)
	defer span.End()

	err := store.Put(ctx, bucket, key, bytes.NewReader(data), contentType)
	recordSpanError(span, err)
	return err
}

// uploadHLSDir walks outDir and uploads all HLS files to S3.
func uploadHLSDir(ctx context.Context, store storage.Storage, bucket, hash, outDir string) ([]string, error) {
	ctx, span := startWorkerSpan(ctx, "worker.upload.hls_dir",
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
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		key := videoS3Key(hash, rel)
		if err := store.Put(ctx, bucket, key, bytes.NewReader(data), ct); err != nil {
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
	return uploaded, err
}

// hlsMimeType returns the MIME type for an HLS-related file extension.
func hlsMimeType(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".m4s":
		return "video/iso.segment"
	case ".mp4":
		return "video/mp4"
	default:
		return ""
	}
}

// imageMimeType returns the MIME type for an image format string.
func imageMimeType(format string) string {
	switch strings.ToLower(format) {
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "avif":
		return "image/avif"
	case "jpeg", "jpg":
		return "image/jpeg"
	case "png":
		return "image/png"
	default:
		return "application/octet-stream"
	}
}

// ── Job status helpers ──

// completeJob updates the job to completed with results and progress 100,
// then fires the webhook callback in a background goroutine.
func (d *Deps) completeJob(ctx context.Context, jobID string, meta jobMeta, results any) error {
	data, err := json.Marshal(results)
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	if err := d.Queries.UpdateJobCompletion(ctx, dbq.UpdateJobCompletionParams{
		ID:      jobID,
		Results: data,
	}); err != nil {
		slog.Error("update completed job", apptracing.LogFields(ctx, "job_id", jobID, "error", err)...)
	}
	slog.Info("job completed", apptracing.LogFields(ctx, "job_id", jobID)...)

	// Deliver webhook callback in the background.
	if d.Webhook != nil && meta.CallbackURL != "" {
		go d.Webhook.Deliver(ctx, jobID, meta.CallbackURL, webhook.CallbackPayload{
			JobID:   jobID,
			Type:    meta.Type,
			Hash:    meta.Hash,
			Status:  "completed",
			Results: results,
		})
	}

	return nil
}

// failJob marks a job as failed with the given error, fires the failure
// webhook callback, and returns the original error.
func (d *Deps) failJob(ctx context.Context, jobID string, meta jobMeta, jobErr error) error {
	return d.failJobWithResult(ctx, jobID, meta, jobErr, nil)
}

func (d *Deps) failJobWithResult(ctx context.Context, jobID string, meta jobMeta, jobErr error, results any) error {
	var data []byte
	var err error
	if results != nil {
		data, err = json.Marshal(results)
		if err != nil {
			return fmt.Errorf("marshal failure results: %w", err)
		}
	}

	if err := d.Queries.UpdateJobFailure(ctx, dbq.UpdateJobFailureParams{
		ID:      jobID,
		Error:   pgtype.Text{String: jobErr.Error(), Valid: true},
		Results: data,
	}); err != nil {
		slog.Error("update job status (fail)", apptracing.LogFields(ctx, "job_id", jobID, "error", err)...)
	}

	// Deliver failure webhook callback in the background.
	if d.Webhook != nil && meta.CallbackURL != "" {
		go d.Webhook.Deliver(ctx, jobID, meta.CallbackURL, webhook.CallbackPayload{
			JobID:   jobID,
			Type:    meta.Type,
			Hash:    meta.Hash,
			Status:  "failed",
			Error:   jobErr.Error(),
			Results: results,
		})
	}

	return jobErr
}

// setProgress is a convenience wrapper for UpdateJobProgress.
func (d *Deps) setProgress(ctx context.Context, jobID string, pct int32) {
	if err := d.Queries.UpdateJobProgress(ctx, dbq.UpdateJobProgressParams{
		ID:       jobID,
		Progress: pct,
	}); err != nil {
		slog.Error("update job progress", apptracing.LogFields(ctx, "job_id", jobID, "pct", pct, "error", err)...)
	}
}
