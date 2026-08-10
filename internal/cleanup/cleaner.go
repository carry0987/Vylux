package cleanup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"Vylux/internal/cache"
	"Vylux/internal/db/dbq"
	"Vylux/internal/lifecycle"
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
	coordinator lifecycle.HashCoordinator
}

func NewCleaner(
	store storage.Storage,
	lru *cache.LRU,
	queries queries,
	inspector taskInspector,
	mediaBucket string,
	coordinators ...lifecycle.HashCoordinator,
) *Cleaner {
	cleaner := &Cleaner{
		store:       store,
		cache:       lru,
		queries:     queries,
		inspector:   inspector,
		mediaBucket: mediaBucket,
	}
	if len(coordinators) > 0 {
		cleaner.coordinator = coordinators[0]
	}
	return cleaner
}

// Cleanup is the reusable legacy administrator purge. It deliberately does not
// create a permanent tombstone and therefore must not be used for host GC.
func (c *Cleaner) Cleanup(ctx context.Context, hash string) error {
	return c.withHashLock(ctx, hash, func(lockedQueries queries) error {
		return c.cleanupLocked(ctx, lockedQueries, hash)
	})
}

// StrictCleanup permanently fences an exact host object before deleting every
// hash-scoped Vylux resource. A failed attempt leaves the fence for safe retry.
func (c *Cleaner) StrictCleanup(ctx context.Context, hash, source string) error {
	var ok bool
	hash, ok = lifecycle.NormalizeHash(hash)
	if !ok {
		return fmt.Errorf("hash must be a 64-character hexadecimal SHA-256")
	}
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("source is required")
	}
	sourceHash, ok := lifecycle.ExtractHash(source)
	if !ok {
		return fmt.Errorf("source does not contain an attributable content hash")
	}
	if sourceHash != hash {
		return fmt.Errorf("source content hash does not match hash")
	}
	if c.coordinator == nil {
		return fmt.Errorf("strict cleanup requires lifecycle coordinator")
	}

	if err := c.coordinator.Fence(ctx, hash, source); err != nil {
		return err
	}
	readiness, ok := c.coordinator.(lifecycle.StrictReadiness)
	if !ok {
		return fmt.Errorf("strict cleanup readiness gate is unavailable")
	}
	if err := readiness.RequireStrictCleanupReady(ctx); err != nil {
		return err
	}
	if err := lifecycle.CheckDeletionSemantics(ctx, c.store, c.mediaBucket); err != nil {
		return err
	}
	if c.queries == nil {
		return fmt.Errorf("strict cleanup database is unavailable")
	}
	if err := validateJobSources(ctx, c.queries, hash, source); err != nil {
		return err
	}
	// Cancellation must happen before waiting for the hash lock, because every
	// active worker holds that lock for its complete write lifecycle.
	if err := c.cancelTasks(ctx, c.queries, hash); err != nil {
		return fmt.Errorf("cancel pre-fence media tasks: %w", err)
	}

	return c.coordinator.WithHashLock(ctx, hash, func(sessionQueries *dbq.Queries) error {
		lockedQueries := c.queries
		if sessionQueries != nil {
			lockedQueries = sessionQueries
		}
		if err := readiness.RequireStrictCleanupReadyLocked(ctx, sessionQueries); err != nil {
			return err
		}
		if err := lifecycle.CheckDeletionSemantics(ctx, c.store, c.mediaBucket); err != nil {
			return err
		}
		if err := validateJobSources(ctx, lockedQueries, hash, source); err != nil {
			return err
		}
		return c.cleanupLocked(ctx, lockedQueries, hash)
	})
}

func (c *Cleaner) withHashLock(ctx context.Context, hash string, run func(queries) error) error {
	if c.coordinator == nil {
		return run(c.queries)
	}
	return c.coordinator.WithHashLock(ctx, hash, func(sessionQueries *dbq.Queries) error {
		if sessionQueries == nil {
			return run(c.queries)
		}
		return run(sessionQueries)
	})
}

func (c *Cleaner) cleanupLocked(ctx context.Context, lockedQueries queries, hash string) error {
	slog.Info("cleanup started", apptracing.LogFields(ctx, "hash", hash)...)

	if err := c.cancelTasks(ctx, lockedQueries, hash); err != nil {
		slog.Warn("cleanup incomplete", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		return fmt.Errorf("cancel media tasks: %w", err)
	}

	if err := errors.Join(
		c.deleteMediaObjects(ctx, hash),
		c.deleteImageCaches(ctx, lockedQueries, hash),
	); err != nil {
		slog.Warn("cleanup incomplete", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		return fmt.Errorf("delete media resources: %w", err)
	}
	if err := c.deleteEncryptionKey(ctx, lockedQueries, hash); err != nil {
		return err
	}
	if err := c.deleteJobs(ctx, lockedQueries, hash); err != nil {
		return err
	}

	slog.Info("cleanup completed", apptracing.LogFields(ctx, "hash", hash)...)
	return nil
}

func validateJobSources(ctx context.Context, lockedQueries queries, hash, source string) error {
	jobs, err := lockedQueries.ListJobsByHash(ctx, hash)
	if err != nil {
		return fmt.Errorf("list jobs by hash: %w", err)
	}
	for _, job := range jobs {
		if job.Source != source {
			return fmt.Errorf("hash has job %s for a different source %q", job.ID, job.Source)
		}
	}
	return nil
}

func (c *Cleaner) cancelTasks(ctx context.Context, lockedQueries queries, hash string) error {
	jobs, err := lockedQueries.ListJobsByHash(ctx, hash)
	if err != nil {
		return fmt.Errorf("list jobs by hash: %w", err)
	}

	if len(jobs) == 0 {
		return nil
	}

	queues, queueErr := c.queueNames()
	var errs []error
	if queueErr != nil {
		errs = append(errs, queueErr)
	}
	for _, job := range jobs {
		if err := c.cancelTask(ctx, job.ID, queues); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (c *Cleaner) queueNames() ([]string, error) {
	queues := []string{queue.QueueCritical, queue.QueueDefault, queue.QueueVideoLarge}
	if c.inspector == nil {
		return queues, nil
	}

	actual, err := c.inspector.Queues()
	if err != nil {
		return queues, fmt.Errorf("list queues: %w", err)
	}

	for _, name := range actual {
		if name == "" || slices.Contains(queues, name) {
			continue
		}
		queues = append(queues, name)
	}

	return queues, nil
}

func (c *Cleaner) cancelTask(ctx context.Context, taskID string, queues []string) error {
	if c.inspector == nil {
		return fmt.Errorf("cancel task %s: inspector is unavailable", taskID)
	}

	for attempt := range cancelAttempts {
		if err := c.inspector.CancelProcessing(taskID); err != nil {
			slog.Debug("cleanup: cancel processing failed", apptracing.LogFields(ctx, "task_id", taskID, "attempt", attempt+1, "error", err)...)
		}

		found := false
		isActive := false
		var inspectErrs []error
		for _, queueName := range queues {
			info, err := c.inspector.GetTaskInfo(queueName, taskID)
			if err != nil {
				if !isInspectorNotFound(err) {
					inspectErrs = append(inspectErrs, fmt.Errorf("inspect queue %s: %w", queueName, err))
				}
				continue
			}
			found = true
			if info.State == asynq.TaskStateActive {
				isActive = true
				continue
			}
			if err := c.inspector.DeleteTask(queueName, taskID); err != nil {
				if !isInspectorNotFound(err) {
					inspectErrs = append(inspectErrs, fmt.Errorf("delete task from queue %s: %w", queueName, err))
				}
				continue
			}
			slog.Info("cleanup: deleted task", apptracing.LogFields(ctx, "task_id", taskID, "queue", queueName, "state", info.State.String())...)
		}

		if len(inspectErrs) == 0 && (!found || !isActive) {
			return nil
		}
		if attempt == cancelAttempts-1 {
			if len(inspectErrs) > 0 {
				return fmt.Errorf("cancel task %s: %w", taskID, errors.Join(inspectErrs...))
			}
			return fmt.Errorf("cancel task %s: task is still active after %d attempts", taskID, cancelAttempts)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("cancel task %s: %w", taskID, ctx.Err())
		case <-time.After(cancelBackoff):
		}
	}

	return nil
}

func (c *Cleaner) deleteMediaObjects(ctx context.Context, hash string) error {
	return errors.Join(
		c.deletePrefix(ctx, s3PrefixForHash(hash, "images")),
		c.deletePrefix(ctx, s3PrefixForHash(hash, "videos")),
	)
}

func (c *Cleaner) deleteImageCaches(ctx context.Context, lockedQueries queries, hash string) error {
	if c.cache != nil {
		c.cache.DeletePrefix(lifecycle.CacheMemoryNamespace(hash))
	}

	if err := c.deleteTrackedImageCaches(ctx, lockedQueries, hash); err != nil {
		return err
	}
	return c.deletePrefix(ctx, lifecycle.CacheNamespace(hash))
}

func (c *Cleaner) deleteTrackedImageCaches(ctx context.Context, lockedQueries queries, hash string) error {
	entries, err := lockedQueries.ListImageCacheEntriesByHash(ctx, hash)
	if err != nil {
		return fmt.Errorf("list tracked image caches: %w", err)
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
			errs = append(errs, fmt.Errorf("delete tracked cache object %s: %w", entry.StorageKey, err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if err := lockedQueries.DeleteImageCacheEntriesByHash(ctx, hash); err != nil {
		return fmt.Errorf("delete image cache index: %w", err)
	}

	return nil
}

func (c *Cleaner) deleteEncryptionKey(ctx context.Context, lockedQueries queries, hash string) error {
	if err := lockedQueries.DeleteEncryptionKey(ctx, hash); err != nil {
		return fmt.Errorf("delete encryption key: %w", err)
	}
	return nil
}

func (c *Cleaner) deleteJobs(ctx context.Context, lockedQueries queries, hash string) error {
	if err := lockedQueries.DeleteJobsByHash(ctx, hash); err != nil {
		return fmt.Errorf("delete jobs: %w", err)
	}
	return nil
}

func (c *Cleaner) deletePrefix(ctx context.Context, prefix string) error {
	keys, err := c.store.List(ctx, c.mediaBucket, prefix)
	if err != nil {
		return fmt.Errorf("list storage prefix %s: %w", prefix, err)
	}

	var errs []error
	for _, key := range keys {
		if err := c.store.Delete(ctx, c.mediaBucket, key); err != nil {
			errs = append(errs, fmt.Errorf("delete storage object %s: %w", key, err))
		}
	}

	if err := errors.Join(errs...); err != nil {
		return err
	}
	remaining, err := c.store.List(ctx, c.mediaBucket, prefix)
	if err != nil {
		return fmt.Errorf("verify storage prefix %s: %w", prefix, err)
	}
	if len(remaining) > 0 {
		return fmt.Errorf("storage prefix %s still contains %d object(s)", prefix, len(remaining))
	}
	return nil
}

func isInspectorNotFound(err error) bool {
	return errors.Is(err, asynq.ErrTaskNotFound) || errors.Is(err, asynq.ErrQueueNotFound)
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
