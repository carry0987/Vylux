package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"Vylux/internal/config"
	"Vylux/internal/db"
	"Vylux/internal/db/dbq"
	"Vylux/internal/deployment"
	"Vylux/internal/handler"
	"Vylux/internal/lifecycle"
	"Vylux/internal/queue"
	"Vylux/internal/signature"
	"Vylux/internal/storage"

	"github.com/jackc/pgx/v5/pgxpool"
)

type blockingPutStore struct {
	storage.Storage
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingPutStore) Put(ctx context.Context, bucket, key string, data io.Reader, contentType string) error {
	if strings.HasPrefix(key, "cache/") {
		s.once.Do(func() { close(s.started) })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.release:
		}
	}
	return s.Storage.Put(ctx, bucket, key, data, contentType)
}

func (s *blockingPutStore) CheckUnversioned(ctx context.Context, bucket string) error {
	checker, ok := s.Storage.(interface {
		CheckUnversioned(context.Context, string) error
	})
	if !ok {
		return fmt.Errorf("wrapped storage does not expose versioning state")
	}
	return checker.CheckUnversioned(ctx, bucket)
}

func (s *blockingPutStore) ListPage(
	ctx context.Context,
	bucket, prefix, continuation string,
	limit int32,
) ([]string, string, bool, error) {
	pager, ok := s.Storage.(interface {
		ListPage(context.Context, string, string, string, int32) ([]string, string, bool, error)
	})
	if !ok {
		return nil, "", false, fmt.Errorf("wrapped storage does not expose bounded listing")
	}
	return pager.ListPage(ctx, bucket, prefix, continuation, limit)
}

type blockingSizeStore struct {
	storage.Storage
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingSizeStore) Size(ctx context.Context, bucket, key string) (int64, error) {
	s.once.Do(func() { close(s.started) })
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-s.release:
	}
	return s.Storage.Size(ctx, bucket, key)
}

type blockingGetStore struct {
	storage.Storage
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingGetStore) Get(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	s.once.Do(func() { close(s.started) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.release:
	}
	return s.Storage.Get(ctx, bucket, key)
}

type httpCallResult struct {
	response *http.Response
	err      error
}

func markStrictReadinessComplete(t *testing.T, pool *db.Pool) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		UPDATE media_lifecycle_readiness
		SET cache_audit_armed = TRUE, cache_audit_cursor = '', cache_audit_complete = TRUE, cache_audit_error = '', updated_at = now()
		WHERE singleton = TRUE
	`); err != nil {
		t.Fatalf("mark strict readiness complete: %v", err)
	}
}

func strictCleanupRequest(t *testing.T, baseURL string, cfg *config.Config, hash, source string) *http.Request {
	t.Helper()
	target, err := cfg.DeploymentTarget()
	if err != nil {
		t.Fatalf("build strict cleanup target: %v", err)
	}
	return strictCleanupRequestForTarget(t, baseURL, cfg.APIKey, hash, source, target)
}

func strictCleanupRequestForTarget(
	t *testing.T,
	baseURL, apiKey, hash, source string,
	target deployment.Target,
) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"source":                  source,
		"protocol_version":        target.ProtocolVersion,
		"deployment_id":           target.DeploymentID,
		"source_backend_identity": target.SourceBackendIdentity,
		"media_backend_identity":  target.MediaBackendIdentity,
	})
	if err != nil {
		t.Fatalf("marshal strict cleanup request: %v", err)
	}
	req, err := http.NewRequest(http.MethodDelete, baseURL+"/api/media/"+hash+"/strict", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create strict cleanup request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	return req
}

func awaitHTTPResult(t *testing.T, result <-chan httpCallResult) *http.Response {
	t.Helper()
	select {
	case call := <-result:
		if call.err != nil {
			t.Fatalf("HTTP request failed: %v", call.err)
		}
		return call.response
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for HTTP request")
		return nil
	}
}

func assertStillBlocked(t *testing.T, result <-chan httpCallResult) {
	t.Helper()
	select {
	case call := <-result:
		if call.response != nil {
			call.response.Body.Close()
		}
		t.Fatalf("request completed before the hash writer released: %v", call.err)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestStrictCleanupWaitsForBlockedCachePutAndRemovesLateWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	var blocker *blockingPutStore
	ts, cfg, store, _, _, pool, cleanup := newS3BackedTestServerWithOptions(t, testServerOptions{
		mediaStoreDecorator: func(inner storage.Storage) storage.Storage {
			blocker = &blockingPutStore{
				Storage: inner,
				started: make(chan struct{}),
				release: make(chan struct{}),
			}
			return blocker
		},
	})
	defer cleanup()
	markStrictReadinessComplete(t, pool)

	hash := strings.Repeat("7", 64)
	source := "uploads/" + hash + "-550e8400-e29b-41d4-a716-446655440000.png"
	if err := store.Put(t.Context(), cfg.SourceBucket, source, bytes.NewReader(buildTestPNG(t)), "image/png"); err != nil {
		t.Fatalf("seed source image: %v", err)
	}
	requestSource := strings.ReplaceAll(url.PathEscape(source), "+", "%20") + ".webp"
	sig, err := signature.SignImage(cfg.HMACSecret, "w64", requestSource)
	if err != nil {
		t.Fatalf("sign image request: %v", err)
	}
	imageURL := ts.URL + "/img/" + sig + "/w64/" + requestSource

	imageResult := make(chan httpCallResult, 1)
	go func() {
		resp, callErr := http.Get(imageURL)
		imageResult <- httpCallResult{response: resp, err: callErr}
	}()
	select {
	case <-blocker.started:
	case <-time.After(15 * time.Second):
		t.Fatal("image handler never reached cache Put")
	}

	cleanupResult := make(chan httpCallResult, 1)
	cleanupRequest := strictCleanupRequest(t, ts.URL, cfg, hash, source)
	go func() {
		resp, callErr := http.DefaultClient.Do(cleanupRequest)
		cleanupResult <- httpCallResult{response: resp, err: callErr}
	}()
	assertStillBlocked(t, cleanupResult)
	close(blocker.release)

	imageResp := awaitHTTPResult(t, imageResult)
	defer imageResp.Body.Close()
	if imageResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(imageResp.Body)
		t.Fatalf("expected image write to finish first with 200, got %d: %s", imageResp.StatusCode, body)
	}

	cleanupResp := awaitHTTPResult(t, cleanupResult)
	defer cleanupResp.Body.Close()
	if cleanupResp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(cleanupResp.Body)
		t.Fatalf("expected strict cleanup 204, got %d: %s", cleanupResp.StatusCode, body)
	}
	if got := cleanupResp.Header.Get("X-Vylux-Cleanup-Confirmed"); got != "1" {
		t.Fatalf("expected strict cleanup confirmation, got %q", got)
	}
	keys, err := store.List(t.Context(), cfg.MediaBucket, lifecycle.CacheNamespace(hash))
	if err != nil {
		t.Fatalf("list cache namespace: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("strict cleanup left cache objects behind: %v", keys)
	}

	resp, err := http.Get(imageURL)
	if err != nil {
		t.Fatalf("GET tombstoned image: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("expected tombstoned image source to return 410, got %d", resp.StatusCode)
	}
}

func TestImageCacheTrackingFailureCompensatesObjectAndMemoryPublication(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, store, queries, lru, pool, cleanup := newS3BackedTestServerWithOptions(t, testServerOptions{})
	defer cleanup()
	if _, err := pool.Exec(t.Context(), `
		CREATE OR REPLACE FUNCTION reject_image_cache_tracking()
		RETURNS TRIGGER AS $$
		BEGIN
			RAISE EXCEPTION 'injected image cache tracking failure';
		END;
		$$ LANGUAGE plpgsql
	`); err != nil {
		t.Fatalf("create tracking failure function: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		CREATE TRIGGER reject_image_cache_tracking
		BEFORE INSERT OR UPDATE ON image_cache_entries
		FOR EACH ROW EXECUTE FUNCTION reject_image_cache_tracking()
	`); err != nil {
		t.Fatalf("create tracking failure trigger: %v", err)
	}

	hash := strings.Repeat("8", 64)
	source := "uploads/" + hash + ".png"
	if err := store.Put(t.Context(), cfg.SourceBucket, source, bytes.NewReader(buildTestPNG(t)), "image/png"); err != nil {
		t.Fatalf("seed source image: %v", err)
	}
	requestSource := strings.ReplaceAll(url.PathEscape(source), "+", "%20") + ".webp"
	sig, err := signature.SignImage(cfg.HMACSecret, "w64", requestSource)
	if err != nil {
		t.Fatalf("sign image request: %v", err)
	}
	resp, err := http.Get(ts.URL + "/img/" + sig + "/w64/" + requestSource)
	if err != nil {
		t.Fatalf("GET /img: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected tracking failure 503, got %d: %s", resp.StatusCode, body)
	}
	keys, err := store.List(t.Context(), cfg.MediaBucket, lifecycle.CacheNamespace(hash))
	if err != nil {
		t.Fatalf("list cache namespace: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("tracking compensation left object(s): %v", keys)
	}
	entries, err := queries.ListImageCacheEntriesByHash(t.Context(), hash)
	if err != nil {
		t.Fatalf("list cache index: %v", err)
	}
	if len(entries) != 0 || lru.Len() != 0 {
		t.Fatalf("tracking failure published cache state: rows=%d lru=%d", len(entries), lru.Len())
	}
}

func TestStrictCleanupSerializesWithJobAdmissionAndPermanentlyRejectsReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	var blocker *blockingSizeStore
	ts, cfg, store, _, _, pool, cleanup := newS3BackedTestServerWithOptions(t, testServerOptions{
		sourceStoreDecorator: func(inner storage.Storage) storage.Storage {
			blocker = &blockingSizeStore{
				Storage: inner,
				started: make(chan struct{}),
				release: make(chan struct{}),
			}
			return blocker
		},
	})
	defer cleanup()
	markStrictReadinessComplete(t, pool)

	hash := strings.Repeat("9", 64)
	source := "uploads/" + hash + "-operation.mp4"
	if err := store.Put(t.Context(), cfg.SourceBucket, source, bytes.NewReader([]byte("video fixture")), "video/mp4"); err != nil {
		t.Fatalf("seed video source: %v", err)
	}
	requestID := "550e8400-e29b-41d4-a716-446655440000"
	body, err := json.Marshal(handler.JobRequest{
		RequestID:   requestID,
		Type:        queue.TypeVideoTranscode,
		Hash:        hash,
		Source:      source,
		CallbackURL: "http://example.test/vylux-webhook",
	})
	if err != nil {
		t.Fatalf("marshal job request: %v", err)
	}
	newPostRequest := func() *http.Request {
		req, requestErr := http.NewRequest(http.MethodPost, ts.URL+"/api/jobs", bytes.NewReader(body))
		if requestErr != nil {
			t.Fatalf("create job request: %v", requestErr)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", cfg.APIKey)
		return req
	}

	postResult := make(chan httpCallResult, 1)
	postRequest := newPostRequest()
	go func() {
		resp, callErr := http.DefaultClient.Do(postRequest)
		postResult <- httpCallResult{response: resp, err: callErr}
	}()
	select {
	case <-blocker.started:
	case <-time.After(15 * time.Second):
		t.Fatal("job admission never reached source Size")
	}

	cleanupResult := make(chan httpCallResult, 1)
	cleanupRequest := strictCleanupRequest(t, ts.URL, cfg, hash, source)
	go func() {
		resp, callErr := http.DefaultClient.Do(cleanupRequest)
		cleanupResult <- httpCallResult{response: resp, err: callErr}
	}()
	assertStillBlocked(t, cleanupResult)
	close(blocker.release)

	postResp := awaitHTTPResult(t, postResult)
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusAccepted {
		responseBody, _ := io.ReadAll(postResp.Body)
		t.Fatalf("expected admission 202, got %d: %s", postResp.StatusCode, responseBody)
	}
	var created handler.JobResponse
	if err := json.NewDecoder(postResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	if created.JobID == nil || *created.JobID != requestID {
		t.Fatalf("expected job_id=request_id %q, got %#v", requestID, created.JobID)
	}

	cleanupResp := awaitHTTPResult(t, cleanupResult)
	defer cleanupResp.Body.Close()
	if cleanupResp.StatusCode != http.StatusNoContent || cleanupResp.Header.Get("X-Vylux-Cleanup-Confirmed") != "1" {
		responseBody, _ := io.ReadAll(cleanupResp.Body)
		t.Fatalf("expected confirmed strict cleanup, got %d: %s", cleanupResp.StatusCode, responseBody)
	}

	statusReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/jobs/"+requestID, nil)
	statusReq.Header.Set("X-API-Key", cfg.APIKey)
	statusResp, err := http.DefaultClient.Do(statusReq)
	if err != nil {
		t.Fatalf("GET cleaned job: %v", err)
	}
	statusResp.Body.Close()
	if statusResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected cleaned job row to be absent, got %d", statusResp.StatusCode)
	}

	replayResp, err := http.DefaultClient.Do(newPostRequest())
	if err != nil {
		t.Fatalf("replay tombstoned admission: %v", err)
	}
	defer replayResp.Body.Close()
	if replayResp.StatusCode != http.StatusGone {
		responseBody, _ := io.ReadAll(replayResp.Body)
		t.Fatalf("expected tombstoned replay 410, got %d: %s", replayResp.StatusCode, responseBody)
	}
}

func TestCancelledImageSourceFetchReleasesHashLockForStrictCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	var blocker *blockingGetStore
	ts, cfg, store, _, _, pool, cleanup := newS3BackedTestServerWithOptions(t, testServerOptions{
		sourceStoreDecorator: func(inner storage.Storage) storage.Storage {
			blocker = &blockingGetStore{
				Storage: inner,
				started: make(chan struct{}),
				release: make(chan struct{}),
			}
			return blocker
		},
	})
	defer cleanup()
	defer close(blocker.release)
	markStrictReadinessComplete(t, pool)

	hash := strings.Repeat("6", 64)
	source := "uploads/" + hash + ".png"
	if err := store.Put(t.Context(), cfg.SourceBucket, source, bytes.NewReader(buildTestPNG(t)), "image/png"); err != nil {
		t.Fatalf("seed source image: %v", err)
	}
	requestSource := strings.ReplaceAll(url.PathEscape(source), "+", "%20") + ".webp"
	sig, err := signature.SignImage(cfg.HMACSecret, "w64", requestSource)
	if err != nil {
		t.Fatalf("sign image request: %v", err)
	}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	imageRequest, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodGet,
		ts.URL+"/img/"+sig+"/w64/"+requestSource,
		nil,
	)
	if err != nil {
		t.Fatalf("create image request: %v", err)
	}
	imageResult := make(chan httpCallResult, 1)
	go func() {
		resp, callErr := http.DefaultClient.Do(imageRequest)
		imageResult <- httpCallResult{response: resp, err: callErr}
	}()

	select {
	case <-blocker.started:
	case <-time.After(15 * time.Second):
		t.Fatal("image handler never reached source Get")
	}
	cancelRequest()
	select {
	case call := <-imageResult:
		if call.response != nil {
			call.response.Body.Close()
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled source fetch did not release the request")
	}

	cleanupResponse, err := http.DefaultClient.Do(strictCleanupRequest(t, ts.URL, cfg, hash, source))
	if err != nil {
		t.Fatalf("strict cleanup after cancelled source fetch: %v", err)
	}
	defer cleanupResponse.Body.Close()
	if cleanupResponse.StatusCode != http.StatusNoContent ||
		cleanupResponse.Header.Get("X-Vylux-Cleanup-Confirmed") != "1" {
		body, _ := io.ReadAll(cleanupResponse.Body)
		t.Fatalf("expected confirmed cleanup after cancellation, got %d: %s", cleanupResponse.StatusCode, body)
	}
}

func TestFencePersistsBeforeAnActiveWriterReleasesTheHashLock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, _, _, queries, _, pool, cleanup := newS3BackedTestServerWithOptions(t, testServerOptions{})
	defer cleanup()
	coordinator := lifecycle.NewCoordinator(pool)
	hash := strings.Repeat("5", 64)
	source := "uploads/" + hash + ".png"
	writerStarted := make(chan struct{})
	releaseWriter := make(chan struct{})
	writerDone := make(chan error, 1)
	defer func() {
		select {
		case <-releaseWriter:
		default:
			close(releaseWriter)
		}
	}()

	go func() {
		writerDone <- coordinator.WithHashLock(context.Background(), hash, func(*dbq.Queries) error {
			close(writerStarted)
			<-releaseWriter
			return nil
		})
	}()
	select {
	case <-writerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not acquire the hash lock")
	}

	fenceCtx, cancelFence := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancelFence()
	if err := coordinator.Fence(fenceCtx, hash, source); err != nil {
		t.Fatalf("fence waited for active writer lock: %v", err)
	}
	tombstoned, err := queries.IsMediaTombstoned(t.Context(), dbq.IsMediaTombstonedParams{Hash: hash, Source: source})
	if err != nil || !tombstoned {
		t.Fatalf("fence was not durable while writer remained active: tombstoned=%v err=%v", tombstoned, err)
	}
	close(releaseWriter)
	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatalf("writer lock returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not release after test signal")
	}
}

func TestHashLockPanicDoesNotLeakSessionAdvisoryLock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, _, _, _, _, pool, cleanup := newS3BackedTestServerWithOptions(t, testServerOptions{})
	defer cleanup()
	coordinator := lifecycle.NewCoordinator(pool)
	hash := strings.Repeat("4", 64)

	func() {
		defer func() {
			if recovered := recover(); recovered != "injected lifecycle panic" {
				t.Fatalf("unexpected recovered panic: %v", recovered)
			}
		}()
		_ = coordinator.WithHashLock(t.Context(), hash, func(*dbq.Queries) error {
			panic("injected lifecycle panic")
		})
	}()

	var lockCount int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM pg_locks
		WHERE locktype = 'advisory'
		  AND granted
		  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
	`).Scan(&lockCount); err != nil {
		t.Fatalf("inspect advisory locks: %v", err)
	}
	if lockCount != 0 {
		t.Fatalf("panic leaked %d PostgreSQL advisory lock(s)", lockCount)
	}
}

func TestLockedReadinessUsesOneConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, _, store, _, _, pool, cleanup := newS3BackedTestServerWithOptions(t, testServerOptions{})
	defer cleanup()
	markStrictReadinessComplete(t, pool)

	poolConfig := pool.Config().Copy()
	poolConfig.MaxConns = 1
	singleConnectionPool, err := pgxpool.NewWithConfig(t.Context(), poolConfig)
	if err != nil {
		t.Fatalf("create single-connection pool: %v", err)
	}
	defer singleConnectionPool.Close()
	if err := singleConnectionPool.Ping(t.Context()); err != nil {
		t.Fatalf("ping single-connection pool: %v", err)
	}

	coordinator := lifecycle.NewCoordinator(singleConnectionPool)
	coordinator.ConfigureStrictCleanupReadiness(store, "media")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	err = coordinator.WithHashLock(ctx, strings.Repeat("3", 64), func(queries *dbq.Queries) error {
		return coordinator.RequireStrictCleanupReadyLocked(ctx, queries)
	})
	if err != nil {
		t.Fatalf("locked readiness required a second pool connection: %v", err)
	}
}

func TestStrictCleanupWrongDeploymentHasNoTombstoneOrCleanerSideEffect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, _, queries, _, _, cleanup := newS3BackedTestServerWithOptions(t, testServerOptions{})
	defer cleanup()
	actual, err := cfg.DeploymentTarget()
	if err != nil {
		t.Fatalf("build actual target: %v", err)
	}
	wrong, err := deployment.NewTarget(
		"550e8400-e29b-41d4-a716-446655440099",
		deployment.BackendConfig{
			ProviderKind: "s3",
			Endpoint:     "https://source-b.example.test",
			Region:       cfg.SourceS3Region,
			Bucket:       cfg.SourceBucket,
		},
		deployment.BackendConfig{
			ProviderKind: "s3",
			Endpoint:     "https://media-b.example.test",
			Region:       cfg.MediaS3Region,
			Bucket:       cfg.MediaBucket,
		},
	)
	if err != nil {
		t.Fatalf("build wrong target: %v", err)
	}

	hash := strings.Repeat("4", 64)
	source := "uploads/" + hash + ".png"
	resp, err := http.DefaultClient.Do(strictCleanupRequestForTarget(
		t,
		ts.URL,
		cfg.APIKey,
		hash,
		source,
		wrong,
	))
	if err != nil {
		t.Fatalf("strict cleanup against wrong deployment: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPreconditionFailed {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 412, got %d: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Vylux-Cleanup-Confirmed") != "" {
		t.Fatal("wrong deployment must not receive cleanup confirmation")
	}
	if resp.Header.Get(deployment.HeaderDeploymentID) != actual.DeploymentID {
		t.Fatalf("response did not identify actual deployment: %q", resp.Header.Get(deployment.HeaderDeploymentID))
	}
	tombstoned, err := queries.IsMediaTombstoned(t.Context(), dbq.IsMediaTombstonedParams{Hash: hash, Source: source})
	if err != nil {
		t.Fatalf("check tombstone: %v", err)
	}
	if tombstoned {
		t.Fatal("wrong-target request persisted Fence tombstone")
	}
}

func TestDeploymentTargetBindingIsReplicaSafeAndRejectsConfigDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	_, cfg, _, queries, _, _, cleanup := newS3BackedTestServerWithOptions(t, testServerOptions{})
	defer cleanup()
	expected, err := cfg.DeploymentTarget()
	if err != nil {
		t.Fatalf("build expected target: %v", err)
	}

	const replicas = 8
	replicaErrors := make(chan error, replicas)
	var wg sync.WaitGroup
	for range replicas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			actual, bindErr := deployment.BindTarget(t.Context(), queries, expected)
			if bindErr == nil && actual != expected {
				bindErr = fmt.Errorf("replica observed target %#v", actual)
			}
			replicaErrors <- bindErr
		}()
	}
	wg.Wait()
	close(replicaErrors)
	for bindErr := range replicaErrors {
		if bindErr != nil {
			t.Fatalf("same-target replica failed: %v", bindErr)
		}
	}

	drifted, err := deployment.NewTarget(
		expected.DeploymentID,
		deployment.BackendConfig{
			ProviderKind: cfg.SourceProviderKind,
			Endpoint:     "https://migrated-source.example.test",
			Region:       cfg.SourceS3Region,
			Bucket:       cfg.SourceBucket,
		},
		deployment.BackendConfig{
			ProviderKind: cfg.MediaProviderKind,
			Endpoint:     cfg.MediaS3Endpoint,
			Region:       cfg.MediaS3Region,
			Bucket:       cfg.MediaBucket,
		},
	)
	if err != nil {
		t.Fatalf("build drifted target: %v", err)
	}
	if _, err := deployment.BindTarget(t.Context(), queries, drifted); !errors.Is(err, deployment.ErrTargetMismatch) {
		t.Fatalf("expected persisted config-drift rejection, got %v", err)
	}

	row, err := queries.GetMediaDeploymentTarget(t.Context())
	if err != nil {
		t.Fatalf("read persisted target: %v", err)
	}
	if row.DeploymentID != expected.DeploymentID || row.SourceBackendIdentity != expected.SourceBackendIdentity {
		t.Fatalf("drift attempt changed persisted target: %#v", row)
	}
}
