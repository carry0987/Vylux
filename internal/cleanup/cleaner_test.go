package cleanup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"Vylux/internal/cache"
	"Vylux/internal/db/dbq"
	"Vylux/internal/queue"

	"github.com/hibiken/asynq"
)

type fakeStore struct {
	objects map[string][]byte
	deleted []string
}

func (s *fakeStore) Get(_ context.Context, _, key string) (io.ReadCloser, error) {
	data, ok := s.objects[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *fakeStore) Put(_ context.Context, _, key string, data io.Reader, _ string) error {
	body, err := io.ReadAll(data)
	if err != nil {
		return err
	}
	if s.objects == nil {
		s.objects = map[string][]byte{}
	}
	s.objects[key] = body
	return nil
}

func (s *fakeStore) Exists(_ context.Context, _, key string) (bool, error) {
	_, ok := s.objects[key]
	return ok, nil
}

func (s *fakeStore) Size(_ context.Context, _, key string) (int64, error) {
	data, ok := s.objects[key]
	if !ok {
		return 0, errors.New("not found")
	}

	return int64(len(data)), nil
}

func (s *fakeStore) Delete(_ context.Context, _, key string) error {
	delete(s.objects, key)
	s.deleted = append(s.deleted, key)
	return nil
}

func (s *fakeStore) List(_ context.Context, _, prefix string) ([]string, error) {
	var keys []string
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func (s *fakeStore) HeadBucket(context.Context, string) error {
	return nil
}

type fakeQueries struct {
	jobs              []dbq.Job
	cacheEntries      []dbq.ImageCacheEntry
	deletedJobsHash   string
	deletedKeysHash   string
	deletedCacheHash  string
	listedCacheHashes []string
}

func (q *fakeQueries) ListJobsByHash(context.Context, string) ([]dbq.Job, error) {
	return q.jobs, nil
}

func (q *fakeQueries) DeleteEncryptionKey(_ context.Context, hash string) error {
	q.deletedKeysHash = hash
	return nil
}

func (q *fakeQueries) DeleteJobsByHash(_ context.Context, hash string) error {
	q.deletedJobsHash = hash
	return nil
}

func (q *fakeQueries) ListImageCacheEntriesByHash(_ context.Context, hash string) ([]dbq.ImageCacheEntry, error) {
	q.listedCacheHashes = append(q.listedCacheHashes, hash)
	return q.cacheEntries, nil
}

func (q *fakeQueries) DeleteImageCacheEntriesByHash(_ context.Context, hash string) error {
	q.deletedCacheHash = hash
	q.cacheEntries = nil
	return nil
}

type fakeInspector struct {
	tasks map[string]map[string]*asynq.TaskInfo
	log   []string
}

func (i *fakeInspector) CancelProcessing(id string) error {
	for _, byQueue := range i.tasks {
		if task, ok := byQueue[id]; ok && task.State == asynq.TaskStateActive {
			task.State = asynq.TaskStateRetry
			i.log = append(i.log, "cancel:"+id)
			return nil
		}
	}
	return asynq.ErrTaskNotFound
}

func (i *fakeInspector) DeleteTask(queue, id string) error {
	byQueue, ok := i.tasks[queue]
	if !ok {
		return asynq.ErrQueueNotFound
	}
	task, ok := byQueue[id]
	if !ok {
		return asynq.ErrTaskNotFound
	}
	if task.State == asynq.TaskStateActive {
		return errors.New("task still active")
	}
	delete(byQueue, id)
	i.log = append(i.log, "delete:"+queue+":"+id)
	return nil
}

func (i *fakeInspector) GetTaskInfo(queue, id string) (*asynq.TaskInfo, error) {
	byQueue, ok := i.tasks[queue]
	if !ok {
		return nil, asynq.ErrQueueNotFound
	}
	task, ok := byQueue[id]
	if !ok {
		return nil, asynq.ErrTaskNotFound
	}
	clone := *task
	return &clone, nil
}

func (i *fakeInspector) Queues() ([]string, error) {
	queues := make([]string, 0, len(i.tasks))
	for queue := range i.tasks {
		queues = append(queues, queue)
	}
	return queues, nil
}

func TestCleanerCleanupDeletesTrackedCachesAndCancelableTasks(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("a", 64)
	trackedCacheKey := "cache-key-1"
	trackedStorageKey := "cache/abcdef.webp"

	lru := cache.New(1024)
	lru.Set(trackedCacheKey, []byte("cached"))

	store := &fakeStore{objects: map[string][]byte{
		s3PrefixForHash(hash, "images") + "thumb.webp":  []byte("img"),
		s3PrefixForHash(hash, "videos") + "master.m3u8": []byte("playlist"),
		trackedStorageKey: []byte("sync-cache"),
	}}
	queries := &fakeQueries{
		jobs: []dbq.Job{
			{ID: "failed-retry", Status: "failed", Hash: hash},
			{ID: "active-task", Status: "processing", Hash: hash},
		},
		cacheEntries: []dbq.ImageCacheEntry{{Hash: hash, CacheKey: trackedCacheKey, StorageKey: trackedStorageKey}},
	}
	inspector := &fakeInspector{tasks: map[string]map[string]*asynq.TaskInfo{
		queue.QueueDefault: {
			"failed-retry": {ID: "failed-retry", Queue: queue.QueueDefault, State: asynq.TaskStateRetry},
			"active-task":  {ID: "active-task", Queue: queue.QueueDefault, State: asynq.TaskStateActive},
		},
	}}

	cleaner := NewCleaner(store, lru, queries, inspector, "media")
	cleaner.Cleanup(ctx, hash)

	if _, ok := lru.Get(trackedCacheKey); ok {
		t.Fatal("expected tracked LRU entry to be removed")
	}
	if exists, _ := store.Exists(ctx, "media", trackedStorageKey); exists {
		t.Fatal("expected tracked storage cache object to be removed")
	}
	if exists, _ := store.Exists(ctx, "media", s3PrefixForHash(hash, "images")+"thumb.webp"); exists {
		t.Fatal("expected derived image asset to be removed")
	}
	if exists, _ := store.Exists(ctx, "media", s3PrefixForHash(hash, "videos")+"master.m3u8"); exists {
		t.Fatal("expected derived video asset to be removed")
	}
	if queries.deletedJobsHash != hash {
		t.Fatalf("expected jobs delete for %q, got %q", hash, queries.deletedJobsHash)
	}
	if queries.deletedKeysHash != hash {
		t.Fatalf("expected encryption key delete for %q, got %q", hash, queries.deletedKeysHash)
	}
	if queries.deletedCacheHash != hash {
		t.Fatalf("expected cache index delete for %q, got %q", hash, queries.deletedCacheHash)
	}
	if _, err := inspector.GetTaskInfo(queue.QueueDefault, "failed-retry"); !errors.Is(err, asynq.ErrTaskNotFound) {
		t.Fatalf("expected failed retry task to be deleted, got %v", err)
	}
	if _, err := inspector.GetTaskInfo(queue.QueueDefault, "active-task"); !errors.Is(err, asynq.ErrTaskNotFound) {
		t.Fatalf("expected active task to be deleted after cancellation, got %v", err)
	}
}
