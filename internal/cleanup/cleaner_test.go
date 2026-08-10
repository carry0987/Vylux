package cleanup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"Vylux/internal/cache"
	"Vylux/internal/db/dbq"
	"Vylux/internal/lifecycle"
	"Vylux/internal/queue"

	"github.com/hibiken/asynq"
)

type fakeStore struct {
	objects      map[string][]byte
	deleted      []string
	listErr      error
	deleteErrors map[string]error
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
	if err := s.deleteErrors[key]; err != nil {
		return err
	}
	delete(s.objects, key)
	s.deleted = append(s.deleted, key)
	return nil
}

func (s *fakeStore) List(_ context.Context, _, prefix string) ([]string, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
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

func (s *fakeStore) CheckUnversioned(context.Context, string) error {
	return nil
}

type fakeQueries struct {
	jobs              []dbq.Job
	cacheEntries      []dbq.ImageCacheEntry
	deletedJobsHash   string
	deletedKeysHash   string
	deletedCacheHash  string
	listedCacheHashes []string
	listJobsErr       error
	deleteKeyErr      error
	deleteJobsErr     error
	listCacheErr      error
	deleteCacheErr    error
}

func (q *fakeQueries) ListJobsByHash(context.Context, string) ([]dbq.Job, error) {
	return q.jobs, q.listJobsErr
}

func (q *fakeQueries) DeleteEncryptionKey(_ context.Context, hash string) error {
	if q.deleteKeyErr != nil {
		return q.deleteKeyErr
	}
	q.deletedKeysHash = hash
	return nil
}

func (q *fakeQueries) DeleteJobsByHash(_ context.Context, hash string) error {
	if q.deleteJobsErr != nil {
		return q.deleteJobsErr
	}
	q.deletedJobsHash = hash
	return nil
}

func (q *fakeQueries) ListImageCacheEntriesByHash(_ context.Context, hash string) ([]dbq.ImageCacheEntry, error) {
	q.listedCacheHashes = append(q.listedCacheHashes, hash)
	return q.cacheEntries, q.listCacheErr
}

func (q *fakeQueries) DeleteImageCacheEntriesByHash(_ context.Context, hash string) error {
	if q.deleteCacheErr != nil {
		return q.deleteCacheErr
	}
	q.deletedCacheHash = hash
	q.cacheEntries = nil
	return nil
}

type fakeInspector struct {
	tasks      map[string]map[string]*asynq.TaskInfo
	log        []string
	queuesErr  error
	cancelErr  error
	deleteErr  error
	inspectErr error
}

func (i *fakeInspector) CancelProcessing(id string) error {
	if i.cancelErr != nil {
		return i.cancelErr
	}
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
	if i.deleteErr != nil {
		return i.deleteErr
	}
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
	if i.inspectErr != nil {
		return nil, i.inspectErr
	}
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
	if i.queuesErr != nil {
		return nil, i.queuesErr
	}
	queues := make([]string, 0, len(i.tasks))
	for queue := range i.tasks {
		queues = append(queues, queue)
	}
	return queues, nil
}

type fakeLifecycleCoordinator struct {
	mu             sync.Mutex
	fences         []strictCleanupCall
	lockCalls      int
	readinessCalls int
	fenceErr       error
	readinessErr   error
}

type strictCleanupCall struct {
	hash   string
	source string
}

func (c *fakeLifecycleCoordinator) WithHashLock(_ context.Context, _ string, run func(*dbq.Queries) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lockCalls++
	return run(nil)
}

func (c *fakeLifecycleCoordinator) Fence(_ context.Context, hash, source string) error {
	if c.fenceErr != nil {
		return c.fenceErr
	}
	c.fences = append(c.fences, strictCleanupCall{hash: hash, source: source})
	return nil
}

func (c *fakeLifecycleCoordinator) AdvanceStrictCleanupReadiness(context.Context) error {
	return errors.New("unexpected readiness advance")
}

func (c *fakeLifecycleCoordinator) RequireStrictCleanupReady(context.Context) error {
	c.readinessCalls++
	return c.readinessErr
}

func (c *fakeLifecycleCoordinator) RequireStrictCleanupReadyLocked(context.Context, *dbq.Queries) error {
	c.readinessCalls++
	return c.readinessErr
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
	if err := cleaner.Cleanup(ctx, hash); err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}

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

func TestCleanerCleanupIsIdempotentWhenResourcesAreMissing(t *testing.T) {
	cleaner := NewCleaner(&fakeStore{}, nil, &fakeQueries{}, nil, "media")

	if err := cleaner.Cleanup(context.Background(), strings.Repeat("b", 64)); err != nil {
		t.Fatalf("Cleanup returned error for missing resources: %v", err)
	}
}

func TestCleanerCleanupStopsBeforePhysicalDeletionWhenJobDiscoveryFails(t *testing.T) {
	hash := strings.Repeat("c", 64)
	key := s3PrefixForHash(hash, "images") + "thumb.webp"
	store := &fakeStore{objects: map[string][]byte{key: []byte("image")}}
	queries := &fakeQueries{listJobsErr: errors.New("database unavailable")}
	cleaner := NewCleaner(store, nil, queries, nil, "media")

	err := cleaner.Cleanup(context.Background(), hash)
	if err == nil || !strings.Contains(err.Error(), "list jobs by hash") {
		t.Fatalf("expected job discovery error, got %v", err)
	}
	if _, ok := store.objects[key]; !ok {
		t.Fatal("physical object must remain when task discovery is incomplete")
	}
	if queries.deletedJobsHash != "" || queries.deletedKeysHash != "" {
		t.Fatal("database cleanup must not continue after task discovery fails")
	}
}

func TestCleanerCleanupRequiresInspectorForCancellableJobs(t *testing.T) {
	hash := strings.Repeat("d", 64)
	key := s3PrefixForHash(hash, "videos") + "master.m3u8"
	store := &fakeStore{objects: map[string][]byte{key: []byte("playlist")}}
	queries := &fakeQueries{jobs: []dbq.Job{{ID: "processing-job", Status: "processing", Hash: hash}}}
	cleaner := NewCleaner(store, nil, queries, nil, "media")

	err := cleaner.Cleanup(context.Background(), hash)
	if err == nil || !strings.Contains(err.Error(), "inspector is unavailable") {
		t.Fatalf("expected inspector error, got %v", err)
	}
	if _, ok := store.objects[key]; !ok {
		t.Fatal("physical object must remain while a task cannot be cancelled")
	}
}

func TestCleanerCleanupReportsQueueDiscoveryFailure(t *testing.T) {
	hash := strings.Repeat("e", 64)
	queries := &fakeQueries{jobs: []dbq.Job{{ID: "queued-job", Status: "queued", Hash: hash}}}
	inspector := &fakeInspector{
		tasks:     map[string]map[string]*asynq.TaskInfo{},
		queuesErr: errors.New("redis unavailable"),
	}
	cleaner := NewCleaner(&fakeStore{}, nil, queries, inspector, "media")

	err := cleaner.Cleanup(context.Background(), hash)
	if err == nil || !strings.Contains(err.Error(), "list queues") {
		t.Fatalf("expected queue discovery error, got %v", err)
	}
}

func TestCleanerCleanupDoesNotDeleteArtifactsWhileTaskRemainsActive(t *testing.T) {
	hash := strings.Repeat("f", 64)
	key := s3PrefixForHash(hash, "videos") + "segment.ts"
	store := &fakeStore{objects: map[string][]byte{key: []byte("segment")}}
	queries := &fakeQueries{jobs: []dbq.Job{{ID: "active-job", Status: "processing", Hash: hash}}}
	inspector := &fakeInspector{
		tasks: map[string]map[string]*asynq.TaskInfo{
			queue.QueueDefault: {
				"active-job": {ID: "active-job", Queue: queue.QueueDefault, State: asynq.TaskStateActive},
			},
		},
		cancelErr: errors.New("cancel unavailable"),
	}
	cleaner := NewCleaner(store, nil, queries, inspector, "media")

	err := cleaner.Cleanup(context.Background(), hash)
	if err == nil || !strings.Contains(err.Error(), "still active") {
		t.Fatalf("expected active task error, got %v", err)
	}
	if _, ok := store.objects[key]; !ok {
		t.Fatal("physical object must remain while its task is active")
	}
}

func TestCleanerCleanupReportsTaskInspectorFailures(t *testing.T) {
	hash := strings.Repeat("0", 64)
	testErr := errors.New("injected inspector failure")

	tests := []struct {
		name      string
		want      string
		inspector *fakeInspector
	}{
		{
			name: "inspect task",
			want: "inspect queue",
			inspector: &fakeInspector{
				tasks:      map[string]map[string]*asynq.TaskInfo{},
				inspectErr: testErr,
			},
		},
		{
			name: "delete task",
			want: "delete task from queue",
			inspector: &fakeInspector{
				tasks: map[string]map[string]*asynq.TaskInfo{
					queue.QueueDefault: {
						"retry-job": {ID: "retry-job", Queue: queue.QueueDefault, State: asynq.TaskStateRetry},
					},
				},
				deleteErr: testErr,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queries := &fakeQueries{jobs: []dbq.Job{{ID: "retry-job", Status: "failed", Hash: hash}}}
			cleaner := NewCleaner(&fakeStore{}, nil, queries, tt.inspector, "media")

			err := cleaner.Cleanup(context.Background(), hash)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestCleanerCleanupSurfacesEveryPhysicalAndDatabaseFailure(t *testing.T) {
	hash := strings.Repeat("1", 64)
	derivedKey := s3PrefixForHash(hash, "images") + "thumb.webp"
	cacheKey := "cache/tracked.webp"
	testErr := errors.New("injected cleanup failure")

	tests := []struct {
		name      string
		want      string
		configure func(store *fakeStore, queries *fakeQueries)
	}{
		{
			name: "list derived objects",
			want: "list storage prefix",
			configure: func(store *fakeStore, _ *fakeQueries) {
				store.listErr = testErr
			},
		},
		{
			name: "delete derived object",
			want: "delete storage object",
			configure: func(store *fakeStore, _ *fakeQueries) {
				store.objects[derivedKey] = []byte("image")
				store.deleteErrors[derivedKey] = testErr
			},
		},
		{
			name: "list tracked cache",
			want: "list tracked image caches",
			configure: func(_ *fakeStore, queries *fakeQueries) {
				queries.listCacheErr = testErr
			},
		},
		{
			name: "delete tracked cache object",
			want: "delete tracked cache object",
			configure: func(store *fakeStore, queries *fakeQueries) {
				store.objects[cacheKey] = []byte("image")
				store.deleteErrors[cacheKey] = testErr
				queries.cacheEntries = []dbq.ImageCacheEntry{{Hash: hash, CacheKey: "lru", StorageKey: cacheKey}}
			},
		},
		{
			name: "delete tracked cache index",
			want: "delete image cache index",
			configure: func(_ *fakeStore, queries *fakeQueries) {
				queries.deleteCacheErr = testErr
			},
		},
		{
			name: "delete encryption key",
			want: "delete encryption key",
			configure: func(_ *fakeStore, queries *fakeQueries) {
				queries.deleteKeyErr = testErr
			},
		},
		{
			name: "delete jobs",
			want: "delete jobs",
			configure: func(_ *fakeStore, queries *fakeQueries) {
				queries.deleteJobsErr = testErr
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{objects: map[string][]byte{}, deleteErrors: map[string]error{}}
			queries := &fakeQueries{}
			tt.configure(store, queries)
			cleaner := NewCleaner(store, nil, queries, nil, "media")

			err := cleaner.Cleanup(context.Background(), hash)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestCleanerCleanupPreservesTrackedCacheIndexWhenObjectDeleteFails(t *testing.T) {
	hash := strings.Repeat("2", 64)
	storageKey := "cache/retry.webp"
	store := &fakeStore{
		objects:      map[string][]byte{storageKey: []byte("cache")},
		deleteErrors: map[string]error{storageKey: errors.New("storage unavailable")},
	}
	queries := &fakeQueries{
		cacheEntries: []dbq.ImageCacheEntry{{Hash: hash, CacheKey: "retry", StorageKey: storageKey}},
	}
	cleaner := NewCleaner(store, nil, queries, nil, "media")

	if err := cleaner.Cleanup(context.Background(), hash); err == nil {
		t.Fatal("expected tracked cache deletion error")
	}
	if queries.deletedCacheHash != "" {
		t.Fatal("tracked cache index must remain available for a retry")
	}
	if len(queries.cacheEntries) != 1 {
		t.Fatal("tracked cache row must remain available for a retry")
	}
}

func TestCleanerStrictCleanupFencesExactSourceAndDeletesHashNamespace(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("a", 64)
	source := "uploads/" + hash + "-upload-id.png"
	cacheKey := lifecycle.CacheStorageKey(hash, strings.Repeat("b", 64), "webp")
	store := &fakeStore{objects: map[string][]byte{cacheKey: []byte("cache")}}
	queries := &fakeQueries{jobs: []dbq.Job{{ID: "completed-job", Hash: hash, Source: source}}}
	inspector := &fakeInspector{tasks: map[string]map[string]*asynq.TaskInfo{
		queue.QueueDefault: {
			"completed-job": {ID: "completed-job", Queue: queue.QueueDefault, State: asynq.TaskStateCompleted},
		},
	}}
	coordinator := &fakeLifecycleCoordinator{}
	cleaner := NewCleaner(store, nil, queries, inspector, "media", coordinator)

	if err := cleaner.StrictCleanup(ctx, hash, source); err != nil {
		t.Fatalf("StrictCleanup returned error: %v", err)
	}
	if len(coordinator.fences) != 1 || coordinator.fences[0] != (strictCleanupCall{hash: hash, source: source}) {
		t.Fatalf("expected exact durable fence, got %#v", coordinator.fences)
	}
	if coordinator.readinessCalls != 2 {
		t.Fatalf("expected O(1) readiness checks before and after lock acquisition, got %d", coordinator.readinessCalls)
	}
	if _, ok := store.objects[cacheKey]; ok {
		t.Fatalf("expected cache namespace object %q to be deleted", cacheKey)
	}
	if queries.deletedKeysHash != hash || queries.deletedJobsHash != hash {
		t.Fatalf("expected key and jobs deletion, got key=%q jobs=%q", queries.deletedKeysHash, queries.deletedJobsHash)
	}
}

func TestCleanerStrictCleanupKeepsWhitespaceDistinctJobSourcesSeparate(t *testing.T) {
	hash := strings.Repeat("d", 64)
	rawSource := "uploads/ " + hash + ".png "
	canonicalSource := "uploads/" + hash + ".png"
	key := s3PrefixForHash(hash, "images") + "thumb.webp"
	store := &fakeStore{objects: map[string][]byte{key: []byte("image")}}
	queries := &fakeQueries{jobs: []dbq.Job{{ID: "canonical-job", Hash: hash, Source: canonicalSource}}}
	coordinator := &fakeLifecycleCoordinator{}
	cleaner := NewCleaner(store, nil, queries, nil, "media", coordinator)

	err := cleaner.StrictCleanup(context.Background(), hash, rawSource)
	if err == nil || !strings.Contains(err.Error(), "different source") {
		t.Fatalf("expected whitespace-distinct source conflict, got %v", err)
	}
	if len(coordinator.fences) != 1 {
		t.Fatalf("expected one exact fence, got %#v", coordinator.fences)
	}
	if got := coordinator.fences[0].source; got != rawSource {
		t.Fatalf("fenced source = %q, want exact %q", got, rawSource)
	}
	if _, ok := store.objects[key]; !ok {
		t.Fatal("whitespace-distinct job source must stop physical deletion")
	}
}

func TestCleanerStrictCleanupFailsClosedBeforeResourcesWhenReadinessIncomplete(t *testing.T) {
	hash := strings.Repeat("b", 64)
	source := "uploads/" + hash + ".png"
	key := s3PrefixForHash(hash, "images") + "thumb.webp"
	store := &fakeStore{objects: map[string][]byte{key: []byte("image")}}
	queries := &fakeQueries{}
	coordinator := &fakeLifecycleCoordinator{readinessErr: lifecycle.ErrStrictReadinessIncomplete}
	cleaner := NewCleaner(store, nil, queries, nil, "media", coordinator)

	err := cleaner.StrictCleanup(context.Background(), hash, source)
	if !errors.Is(err, lifecycle.ErrStrictReadinessIncomplete) {
		t.Fatalf("expected readiness failure, got %v", err)
	}
	if _, ok := store.objects[key]; !ok {
		t.Fatal("readiness failure must stop before physical deletion")
	}
	if len(coordinator.fences) != 1 {
		t.Fatal("strict retry safety requires the tombstone before readiness checks")
	}
}

func TestCleanerStrictCleanupRejectsJobsForDifferentSource(t *testing.T) {
	hash := strings.Repeat("c", 64)
	source := "uploads/" + hash + ".png"
	key := s3PrefixForHash(hash, "images") + "thumb.webp"
	store := &fakeStore{objects: map[string][]byte{key: []byte("image")}}
	queries := &fakeQueries{jobs: []dbq.Job{{ID: "other", Hash: hash, Source: "uploads/other.png"}}}
	coordinator := &fakeLifecycleCoordinator{}
	cleaner := NewCleaner(store, nil, queries, nil, "media", coordinator)

	err := cleaner.StrictCleanup(context.Background(), hash, source)
	if err == nil || !strings.Contains(err.Error(), "different source") {
		t.Fatalf("expected exact-source conflict, got %v", err)
	}
	if _, ok := store.objects[key]; !ok {
		t.Fatal("source conflict must stop before physical deletion")
	}
}
