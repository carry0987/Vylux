package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"Vylux/internal/db/dbq"
	"Vylux/internal/lifecycle"
	"Vylux/internal/queue"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
)

type taskHandlerFactory func(*Deps) func(context.Context, *asynq.Task) error

type lifecyclePayload struct {
	Hash        string `json:"hash"`
	Source      string `json:"source"`
	CallbackURL string `json:"callback_url"`
}

type taskIntentQueries interface {
	GetJob(ctx context.Context, id string) (dbq.Job, error)
}

// GuardLifecycle holds the cross-instance hash lock for the complete worker
// execution, including every object-store Put. A durable tombstone rejects a
// queued task before it can read or write media.
func GuardLifecycle(d *Deps, factory taskHandlerFactory) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, task *asynq.Task) error {
		if d == nil || d.Lifecycle == nil {
			return fmt.Errorf("worker lifecycle coordinator is unavailable")
		}

		var payload lifecyclePayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return skipTask("decode lifecycle payload: %v", err)
		}
		if err := validateLifecyclePayload(payload); err != nil {
			return errors.Join(asynq.SkipRetry, err)
		}
		taskID, ok := asynq.GetTaskID(ctx)
		if !ok || strings.TrimSpace(taskID) == "" {
			return skipTask("task is missing its durable job ID")
		}

		return d.Lifecycle.WithHashLock(ctx, payload.Hash, func(queries *dbq.Queries) error {
			if err := requireTaskIntent(ctx, queries, taskID, task.Type(), payload, task.Payload()); err != nil {
				return err
			}
			if err := lifecycle.RejectTombstoned(ctx, queries, payload.Hash, payload.Source); err != nil {
				if errors.Is(err, lifecycle.ErrTombstoned) {
					return errors.Join(asynq.SkipRetry, err)
				}
				return err
			}

			lockedDeps := *d
			lockedDeps.Queries = queries
			return factory(&lockedDeps)(ctx, task)
		})
	}
}

func validateLifecyclePayload(payload lifecyclePayload) error {
	if payload.Hash == "" || strings.TrimSpace(payload.Source) == "" {
		return fmt.Errorf("task lifecycle payload requires hash and source")
	}
	hash, ok := lifecycle.NormalizeHash(payload.Hash)
	if !ok || payload.Hash != hash {
		return fmt.Errorf("task lifecycle hash must be canonical lowercase SHA-256")
	}
	sourceHash, ok := lifecycle.ExtractHash(payload.Source)
	if !ok || sourceHash != hash {
		return fmt.Errorf("task lifecycle source does not match hash")
	}
	return nil
}

func requireTaskIntent(
	ctx context.Context,
	queries taskIntentQueries,
	taskID, taskType string,
	payload lifecyclePayload,
	rawPayload []byte,
) error {
	if queries == nil {
		return fmt.Errorf("worker job-intent database is unavailable")
	}
	job, err := queries.GetJob(ctx, taskID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skipTask("task %s has no durable job intent", taskID)
		}
		return fmt.Errorf("read durable job intent %s: %w", taskID, err)
	}
	if job.ID != taskID ||
		job.Type != taskType ||
		job.Hash != payload.Hash ||
		job.Source != payload.Source ||
		job.CallbackUrl != payload.CallbackURL {
		return skipTask("task %s does not match its durable job intent", taskID)
	}
	if err := requireTaskOptions(job, rawPayload); err != nil {
		return skipTask("task %s options do not match its durable job intent: %v", taskID, err)
	}

	switch job.Status {
	case "queued", "processing", "failed":
		return nil
	case "completed", "canceled":
		return skipTask("task %s belongs to terminal job status %s", taskID, job.Status)
	default:
		return skipTask("task %s has unsupported job status %s", taskID, job.Status)
	}
}

func requireTaskOptions(job dbq.Job, rawPayload []byte) error {
	switch job.Type {
	case queue.TypeImageThumbnail:
		var stored struct {
			Outputs []queue.ThumbnailOutput `json:"outputs"`
		}
		var task queue.ImageThumbnailPayload
		if err := decodeIntentOptions(job.Options, &stored, rawPayload, &task); err != nil {
			return err
		}
		if !reflect.DeepEqual(stored.Outputs, task.Outputs) {
			return fmt.Errorf("thumbnail outputs differ")
		}
	case queue.TypeVideoCover:
		var stored queue.VideoCoverOptions
		var task queue.VideoCoverPayload
		if err := decodeIntentOptions(job.Options, &stored, rawPayload, &task); err != nil {
			return err
		}
		if stored.TimestampSec != task.TimestampSec {
			return fmt.Errorf("cover options differ")
		}
	case queue.TypeVideoPreview:
		var stored queue.VideoPreviewOptions
		var task queue.VideoPreviewPayload
		if err := decodeIntentOptions(job.Options, &stored, rawPayload, &task); err != nil {
			return err
		}
		taskOptions := queue.VideoPreviewOptions{
			StartSec: task.StartSec,
			Duration: task.Duration,
			Width:    task.Width,
			FPS:      task.FPS,
			Format:   task.Format,
		}
		if stored != taskOptions {
			return fmt.Errorf("preview options differ")
		}
	case queue.TypeVideoTranscode:
		var stored queue.VideoTranscodeOptions
		var task queue.VideoTranscodePayload
		if err := decodeIntentOptions(job.Options, &stored, rawPayload, &task); err != nil {
			return err
		}
		if stored.Encrypt != task.Encrypt {
			return fmt.Errorf("transcode options differ")
		}
	case queue.TypeVideoFull:
		var stored queue.VideoFullOptions
		var task queue.VideoFullPayload
		if err := decodeIntentOptions(job.Options, &stored, rawPayload, &task); err != nil {
			return err
		}
		if !reflect.DeepEqual(stored, task.Options) {
			return fmt.Errorf("full-video options differ")
		}
	default:
		return fmt.Errorf("unsupported job type %q", job.Type)
	}
	return nil
}

func decodeIntentOptions(storedPayload []byte, stored any, taskPayload []byte, task any) error {
	if len(storedPayload) == 0 {
		storedPayload = []byte(`{}`)
	}
	if err := json.Unmarshal(storedPayload, stored); err != nil {
		return fmt.Errorf("decode stored options: %w", err)
	}
	if err := json.Unmarshal(taskPayload, task); err != nil {
		return fmt.Errorf("decode task options: %w", err)
	}
	return nil
}

func skipTask(format string, args ...any) error {
	return errors.Join(asynq.SkipRetry, fmt.Errorf(format, args...))
}
