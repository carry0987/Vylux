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

type StageResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type CleanupResult struct {
	TaskCancellation    StageResult `json:"task_cancellation"`
	MediaObjects        StageResult `json:"media_objects"`
	TrackedCacheObjects StageResult `json:"tracked_cache_objects"`
	EncryptionKey       StageResult `json:"encryption_key"`
	ImageCacheIndex     StageResult `json:"image_cache_index"`
	JobRows             StageResult `json:"job_rows"`
}

type namedStageResult struct {
	name   string
	result StageResult
}

func NewCleanupResult() CleanupResult {
	return CleanupResult{
		TaskCancellation:    StageResult{Status: "pending"},
		MediaObjects:        StageResult{Status: "pending"},
		TrackedCacheObjects: StageResult{Status: "pending"},
		EncryptionKey:       StageResult{Status: "pending"},
		ImageCacheIndex:     StageResult{Status: "pending"},
		JobRows:             StageResult{Status: "pending"},
	}
}

func (r *CleanupResult) ConfirmedGone() bool {
	return r.TaskCancellation.Status == "done" &&
		r.MediaObjects.Status == "done" &&
		r.TrackedCacheObjects.Status == "done"
}

func (r *CleanupResult) HasWarnings() bool {
	return r.EncryptionKey.Status == "failed" ||
		r.ImageCacheIndex.Status == "failed" ||
		r.JobRows.Status == "failed"
}

func (r *CleanupResult) Err() error {
	if r.ConfirmedGone() {
		return nil
	}

	var failed []string
	for _, stage := range r.criticalStages() {
		if stage.result.Status == "failed" {
			if stage.result.Error != "" {
				failed = append(failed, fmt.Sprintf("%s: %s", stage.name, stage.result.Error))
			} else {
				failed = append(failed, stage.name)
			}
		}
	}

	if len(failed) == 0 {
		return fmt.Errorf("cleanup incomplete")
	}
	return fmt.Errorf("cleanup incomplete: %s", strings.Join(failed, "; "))
}

func (r *CleanupResult) CompletedStages() []string {
	var completed []string
	for _, stage := range r.allStages() {
		if stage.result.Status == "done" {
			completed = append(completed, stage.name)
		}
	}
	return completed
}

func (r *CleanupResult) FailedStages() []string {
	var failed []string
	for _, stage := range r.allStages() {
		if stage.result.Status == "failed" {
			failed = append(failed, stage.name)
		}
	}
	return failed
}

func (r *CleanupResult) criticalStages() []namedStageResult {
	return []namedStageResult{
		{name: "task_cancellation", result: r.TaskCancellation},
		{name: "media_objects", result: r.MediaObjects},
		{name: "tracked_cache_objects", result: r.TrackedCacheObjects},
	}
}

func (r *CleanupResult) allStages() []namedStageResult {
	return []namedStageResult{
		{name: "task_cancellation", result: r.TaskCancellation},
		{name: "media_objects", result: r.MediaObjects},
		{name: "tracked_cache_objects", result: r.TrackedCacheObjects},
		{name: "encryption_key", result: r.EncryptionKey},
		{name: "image_cache_index", result: r.ImageCacheIndex},
		{name: "job_rows", result: r.JobRows},
	}
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

func (c *Cleaner) Cleanup(ctx context.Context, hash string) CleanupResult {
	result := NewCleanupResult()
	slog.Info("cleanup started", apptracing.LogFields(ctx, "hash", hash)...)

	result.TaskCancellation = c.cancelTasks(ctx, hash)
	result.MediaObjects = c.deleteMediaObjects(ctx, hash)
	result.TrackedCacheObjects, result.ImageCacheIndex = c.deleteTrackedImageCaches(ctx, hash)

	if !result.ConfirmedGone() {
		slog.Warn("cleanup incomplete", apptracing.LogFields(ctx, "hash", hash, "error", result.Err())...)
		return result
	}

	result.EncryptionKey = c.deleteEncryptionKey(ctx, hash)
	result.JobRows = c.deleteJobs(ctx, hash)
	if result.HasWarnings() {
		slog.Warn("cleanup completed with warnings", apptracing.LogFields(ctx, "hash", hash, "failed_stages", strings.Join(result.FailedStages(), ","))...)
		return result
	}

	slog.Info("cleanup completed", apptracing.LogFields(ctx, "hash", hash)...)
	return result
}

func (c *Cleaner) cancelTasks(ctx context.Context, hash string) StageResult {
	jobs, err := c.queries.ListJobsByHash(ctx, hash)
	if err != nil {
		slog.Warn("cleanup: list jobs for cancel failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		return StageResult{Status: "failed", Error: err.Error()}
	}

	queues := c.queueNames(ctx)
	for i := range jobs {
		job := &jobs[i]
		if job.Status == "completed" || job.Status == "canceled" {
			continue
		}
		if err := c.cancelTask(ctx, job.ID, queues); err != nil {
			return StageResult{Status: "failed", Error: err.Error()}
		}
	}

	return StageResult{Status: "done"}
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
	return fmt.Errorf("task %s still present after cancellation attempts", taskID)
}

func (c *Cleaner) deleteMediaObjects(ctx context.Context, hash string) StageResult {
	for _, prefix := range []string{
		s3PrefixForHash(hash, "images"),
		s3PrefixForHash(hash, "videos"),
		s3PrefixForHash(hash, "audio"),
	} {
		if err := c.deletePrefix(ctx, prefix); err != nil {
			return StageResult{Status: "failed", Error: err.Error()}
		}
	}
	return StageResult{Status: "done"}
}

func (c *Cleaner) deleteTrackedImageCaches(ctx context.Context, hash string) (StageResult, StageResult) {
	entries, err := c.queries.ListImageCacheEntriesByHash(ctx, hash)
	if err != nil {
		slog.Warn("cleanup: list image cache entries failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		failed := StageResult{Status: "failed", Error: err.Error()}
		return failed, failed
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
			return StageResult{Status: "failed", Error: err.Error()}, StageResult{Status: "pending"}
		}
	}

	if err := c.queries.DeleteImageCacheEntriesByHash(ctx, hash); err != nil {
		slog.Warn("cleanup: delete image cache index failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		return StageResult{Status: "done"}, StageResult{Status: "failed", Error: err.Error()}
	}

	return StageResult{Status: "done"}, StageResult{Status: "done"}
}

func (c *Cleaner) deleteEncryptionKey(ctx context.Context, hash string) StageResult {
	if err := c.queries.DeleteEncryptionKey(ctx, hash); err != nil {
		slog.Warn("cleanup: delete encryption key failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		return StageResult{Status: "failed", Error: err.Error()}
	}
	return StageResult{Status: "done"}
}

func (c *Cleaner) deleteJobs(ctx context.Context, hash string) StageResult {
	if err := c.queries.DeleteJobsByHash(ctx, hash); err != nil {
		slog.Warn("cleanup: delete jobs failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		return StageResult{Status: "failed", Error: err.Error()}
	}
	return StageResult{Status: "done"}
}

func (c *Cleaner) deletePrefix(ctx context.Context, prefix string) error {
	keys, err := c.store.List(ctx, c.mediaBucket, prefix)
	if err != nil {
		slog.Warn("cleanup: list storage objects failed", apptracing.LogFields(ctx, "prefix", prefix, "error", err)...)
		return err
	}

	for _, key := range keys {
		if err := c.store.Delete(ctx, c.mediaBucket, key); err != nil {
			slog.Warn("cleanup: delete storage object failed", apptracing.LogFields(ctx, "key", key, "error", err)...)
			return err
		}
	}
	return nil
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
