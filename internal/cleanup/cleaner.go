package cleanup

import (
	"context"
	"errors"
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

func (c *Cleaner) Cleanup(ctx context.Context, hash string) {
	slog.Info("cleanup started", apptracing.LogFields(ctx, "hash", hash)...)

	c.cancelTasks(ctx, hash)
	c.deleteMediaObjects(ctx, hash)
	c.deleteTrackedImageCaches(ctx, hash)
	c.deleteEncryptionKey(ctx, hash)
	c.deleteJobs(ctx, hash)

	slog.Info("cleanup completed", apptracing.LogFields(ctx, "hash", hash)...)
}

func (c *Cleaner) cancelTasks(ctx context.Context, hash string) {
	jobs, err := c.queries.ListJobsByHash(ctx, hash)
	if err != nil {
		slog.Warn("cleanup: list jobs for cancel failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		return
	}

	queues := c.queueNames(ctx)
	for _, job := range jobs {
		if job.Status == "completed" || job.Status == "cancelled" {
			continue
		}
		c.cancelTask(ctx, job.ID, queues)
	}
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

func (c *Cleaner) cancelTask(ctx context.Context, taskID string, queues []string) {
	if c.inspector == nil {
		return
	}

	for attempt := 0; attempt < cancelAttempts; attempt++ {
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
			return
		}

		time.Sleep(cancelBackoff)
	}

	slog.Warn("cleanup: task still present after cancellation attempts", apptracing.LogFields(ctx, "task_id", taskID)...)
}

func (c *Cleaner) deleteMediaObjects(ctx context.Context, hash string) {
	c.deletePrefix(ctx, s3PrefixForHash(hash, "images"))
	c.deletePrefix(ctx, s3PrefixForHash(hash, "videos"))
}

func (c *Cleaner) deleteTrackedImageCaches(ctx context.Context, hash string) {
	entries, err := c.queries.ListImageCacheEntriesByHash(ctx, hash)
	if err != nil {
		slog.Warn("cleanup: list image cache entries failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		return
	}

	for _, entry := range entries {
		if c.cache != nil {
			c.cache.Delete(entry.CacheKey)
		}
		if entry.StorageKey == "" {
			continue
		}
		if err := c.store.Delete(ctx, c.mediaBucket, entry.StorageKey); err != nil {
			slog.Warn("cleanup: delete tracked cache object failed", apptracing.LogFields(ctx, "hash", hash, "key", entry.StorageKey, "error", err)...)
		}
	}

	if err := c.queries.DeleteImageCacheEntriesByHash(ctx, hash); err != nil {
		slog.Warn("cleanup: delete image cache index failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
	}
}

func (c *Cleaner) deleteEncryptionKey(ctx context.Context, hash string) {
	if err := c.queries.DeleteEncryptionKey(ctx, hash); err != nil {
		slog.Warn("cleanup: delete encryption key failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
	}
}

func (c *Cleaner) deleteJobs(ctx context.Context, hash string) {
	if err := c.queries.DeleteJobsByHash(ctx, hash); err != nil {
		slog.Warn("cleanup: delete jobs failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
	}
}

func (c *Cleaner) deletePrefix(ctx context.Context, prefix string) {
	keys, err := c.store.List(ctx, c.mediaBucket, prefix)
	if err != nil {
		slog.Warn("cleanup: list storage objects failed", apptracing.LogFields(ctx, "prefix", prefix, "error", err)...)
		return
	}

	for _, key := range keys {
		if err := c.store.Delete(ctx, c.mediaBucket, key); err != nil {
			slog.Warn("cleanup: delete storage object failed", apptracing.LogFields(ctx, "key", key, "error", err)...)
		}
	}
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
