package cleanup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"Vylux/internal/cache"
	"Vylux/internal/db/dbq"
	"Vylux/internal/queue"
	"Vylux/internal/storage"
	apptracing "Vylux/internal/tracing"

	"github.com/hibiken/asynq"
)

const (
	cancelAttempts = 5
	cancelBackoff  = 50 * time.Millisecond
)

type taskInspector interface {
	CancelProcessing(id string) error
	DeleteTask(queue, id string) error
	GetTaskInfo(queue, id string) (*asynq.TaskInfo, error)
	Queues() ([]string, error)
}

type queries interface {
	ListJobsByHash(ctx context.Context, hash string) ([]dbq.Job, error)
	DeleteEncryptionKey(ctx context.Context, hash string) error
	DeleteJobsByHash(ctx context.Context, hash string) error
	ListImageCacheEntriesByHash(ctx context.Context, hash string) ([]dbq.ImageCacheEntry, error)
	DeleteImageCacheEntriesByHash(ctx context.Context, hash string) error
}

type Cleaner struct {
	store       storage.Storage
	cache       *cache.LRU
	queries     queries
	inspector   taskInspector
	mediaBucket string
}

func NewCleaner(store storage.Storage, lru *cache.LRU, queries queries, inspector taskInspector, mediaBucket string) *Cleaner {
	return &Cleaner{
		store:       store,
		cache:       lru,
		queries:     queries,
		inspector:   inspector,
		mediaBucket: mediaBucket,
	}
}

// Cleanup removes every resource associated with a content hash. Each step is
// still attempted even when an earlier one fails, so a retry can make progress;
// the returned error reports whatever did not complete, so a caller can tell
// whether the media is really gone before it forgets the hash.
func (c *Cleaner) Cleanup(ctx context.Context, hash string) error {
	slog.Info("cleanup started", apptracing.LogFields(ctx, "hash", hash)...)

	err := errors.Join(
		c.cancelTasks(ctx, hash),
		c.deleteMediaObjects(ctx, hash),
		c.deleteTrackedImageCaches(ctx, hash),
		c.deleteEncryptionKey(ctx, hash),
		c.deleteJobs(ctx, hash),
	)
	if err != nil {
		slog.Warn("cleanup incomplete", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		return err
	}

	slog.Info("cleanup completed", apptracing.LogFields(ctx, "hash", hash)...)

	return nil
}

func (c *Cleaner) cancelTasks(ctx context.Context, hash string) error {
	jobs, err := c.queries.ListJobsByHash(ctx, hash)
	if err != nil {
		slog.Warn("cleanup: list jobs for cancel failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		return fmt.Errorf("list jobs for cancel: %w", err)
	}

	queues := c.queueNames(ctx)
	var errs []error
	for i := range jobs {
		job := &jobs[i]
		if job.Status == "completed" || job.Status == "canceled" {
			continue
		}
		errs = append(errs, c.cancelTask(ctx, job.ID, queues))
	}

	return errors.Join(errs...)
}

func (c *Cleaner) queueNames(ctx context.Context) []string {
	queues := []string{queue.QueueCritical, queue.QueueDefault, queue.QueueVideoLarge}
	if c.inspector == nil {
		return queues
	}

	actual, err := c.inspector.Queues()
	if err != nil {
		slog.Debug("cleanup: list queues failed", apptracing.LogFields(ctx, "error", err)...)
		return queues
	}

	for _, name := range actual {
		if name == "" || slices.Contains(queues, name) {
			continue
		}
		queues = append(queues, name)
	}

	return queues
}

func (c *Cleaner) cancelTask(ctx context.Context, taskID string, queues []string) error {
	if c.inspector == nil {
		return nil
	}

	for attempt := range cancelAttempts {
		if err := c.inspector.CancelProcessing(taskID); err != nil {
			slog.Debug("cleanup: cancel processing failed", apptracing.LogFields(ctx, "task_id", taskID, "attempt", attempt+1, "error", err)...)
		}

		found := false
		deleted := false
		for _, queueName := range queues {
			info, err := c.inspector.GetTaskInfo(queueName, taskID)
			if err != nil {
				continue
			}
			found = true
			if info.State == asynq.TaskStateActive {
				continue
			}
			if err := c.inspector.DeleteTask(queueName, taskID); err != nil {
				slog.Debug("cleanup: delete task failed", apptracing.LogFields(ctx, "task_id", taskID, "queue", queueName, "state", info.State.String(), "error", err)...)
				continue
			}
			deleted = true
			slog.Info("cleanup: deleted task", apptracing.LogFields(ctx, "task_id", taskID, "queue", queueName, "state", info.State.String())...)
		}

		if !found || deleted {
			return nil
		}

		time.Sleep(cancelBackoff)
	}

	slog.Warn("cleanup: task still present after cancellation attempts", apptracing.LogFields(ctx, "task_id", taskID)...)

	// A task that is still queued can write new artifacts after the sweep, so
	// this is reported rather than swallowed.
	return fmt.Errorf("task %s still present after %d cancellation attempts", taskID, cancelAttempts)
}

func (c *Cleaner) deleteMediaObjects(ctx context.Context, hash string) error {
	return errors.Join(
		c.deletePrefix(ctx, s3PrefixForHash(hash, "images")),
		c.deletePrefix(ctx, s3PrefixForHash(hash, "videos")),
	)
}

func (c *Cleaner) deleteTrackedImageCaches(ctx context.Context, hash string) error {
	entries, err := c.queries.ListImageCacheEntriesByHash(ctx, hash)
	if err != nil {
		slog.Warn("cleanup: list image cache entries failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		return fmt.Errorf("list image cache entries: %w", err)
	}

	var errs []error
	for _, entry := range entries {
		if c.cache != nil {
			c.cache.Delete(entry.CacheKey)
		}
		if entry.StorageKey == "" {
			continue
		}
		if err := c.store.Delete(ctx, c.mediaBucket, entry.StorageKey); err != nil {
			slog.Warn("cleanup: delete tracked cache object failed", apptracing.LogFields(ctx, "hash", hash, "key", entry.StorageKey, "error", err)...)
			errs = append(errs, fmt.Errorf("delete tracked cache object %q: %w", entry.StorageKey, err))
		}
	}

	if err := c.queries.DeleteImageCacheEntriesByHash(ctx, hash); err != nil {
		slog.Warn("cleanup: delete image cache index failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		errs = append(errs, fmt.Errorf("delete image cache index: %w", err))
	}

	return errors.Join(errs...)
}

func (c *Cleaner) deleteEncryptionKey(ctx context.Context, hash string) error {
	if err := c.queries.DeleteEncryptionKey(ctx, hash); err != nil {
		slog.Warn("cleanup: delete encryption key failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		return fmt.Errorf("delete encryption key: %w", err)
	}

	return nil
}

func (c *Cleaner) deleteJobs(ctx context.Context, hash string) error {
	if err := c.queries.DeleteJobsByHash(ctx, hash); err != nil {
		slog.Warn("cleanup: delete jobs failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		return fmt.Errorf("delete jobs: %w", err)
	}

	return nil
}

func (c *Cleaner) deletePrefix(ctx context.Context, prefix string) error {
	keys, err := c.store.List(ctx, c.mediaBucket, prefix)
	if err != nil {
		slog.Warn("cleanup: list storage objects failed", apptracing.LogFields(ctx, "prefix", prefix, "error", err)...)
		return fmt.Errorf("list storage objects %q: %w", prefix, err)
	}

	var errs []error
	for _, key := range keys {
		if err := c.store.Delete(ctx, c.mediaBucket, key); err != nil {
			slog.Warn("cleanup: delete storage object failed", apptracing.LogFields(ctx, "key", key, "error", err)...)
			errs = append(errs, fmt.Errorf("delete storage object %q: %w", key, err))
		}
	}

	return errors.Join(errs...)
}

func s3PrefixForHash(hash, kind string) string {
	prefix := hash
	if len(hash) >= 2 {
		prefix = hash[:2]
	}
	return kind + "/" + prefix + "/" + hash + "/"
}

func IsTaskNotFound(err error) bool {
	return err != nil && errors.Is(err, asynq.ErrTaskNotFound)
}
