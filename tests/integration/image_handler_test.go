package integration

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"Vylux/internal/cache"
	"Vylux/internal/config"
	"Vylux/internal/db"
	"Vylux/internal/db/dbq"
	"Vylux/internal/deployment"
	"Vylux/internal/encryption"
	"Vylux/internal/lifecycle"
	"Vylux/internal/queue"
	"Vylux/internal/server"
	"Vylux/internal/signature"
	"Vylux/internal/storage"
	"Vylux/migrations"
	"Vylux/tests/testutil"

	redis "github.com/redis/go-redis/v9"
)

type testServerOptions struct {
	sourceStoreDecorator func(storage.Storage) storage.Storage
	mediaStoreDecorator  func(storage.Storage) storage.Storage
}

func newS3BackedTestServerWithOptions(t *testing.T, options testServerOptions) (*httptest.Server, *config.Config, storage.Storage, *dbq.Queries, *cache.LRU, *db.Pool, func()) {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	pg := testutil.StartPostgres(ctx, t)
	rd := testutil.StartRedis(ctx, t)
	rs := testutil.StartRustFS(ctx, t)

	if err := testutil.CreateBuckets(ctx, rs.Endpoint, rs.AccessKey, rs.SecretKey, rs.Region, "source", "media"); err != nil {
		t.Fatalf("create rustfs buckets: %v", err)
	}

	if err := db.Migrate(ctx, pg.DSN, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, err := db.Connect(ctx, pg.DSN)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}

	store, err := storage.NewS3(ctx, storage.S3Config{
		Endpoint:  rs.Endpoint,
		AccessKey: rs.AccessKey,
		SecretKey: rs.SecretKey,
		Region:    rs.Region,
	})
	if err != nil {
		t.Fatalf("new s3 store: %v", err)
	}

	sourceStore := storage.WithInstrumentation(store, "source", "s3")
	mediaStore := storage.WithInstrumentation(store, "media", "s3")
	if options.sourceStoreDecorator != nil {
		sourceStore = options.sourceStoreDecorator(sourceStore)
	}
	if options.mediaStoreDecorator != nil {
		mediaStore = options.mediaStoreDecorator(mediaStore)
	}

	queries := dbq.New(pool)
	lifecycleCoordinator := lifecycle.NewCoordinator(pool)
	lru := cache.New(64 * 1024 * 1024)
	lifecycleCoordinator.ConfigureStrictCleanupReadiness(mediaStore, "media")
	if _, err := pool.Exec(ctx, `
		UPDATE media_lifecycle_readiness
		SET cache_audit_armed = TRUE, updated_at = now()
		WHERE singleton = TRUE
	`); err != nil {
		t.Fatalf("arm strict cleanup readiness: %v", err)
	}

	queueClient, err := queue.NewClient(rd.URL)
	if err != nil {
		t.Fatalf("queue client: %v", err)
	}

	inspector, err := queue.NewInspector(rd.URL)
	if err != nil {
		t.Fatalf("queue inspector: %v", err)
	}

	cfg := &config.Config{
		DeploymentID:       "550e8400-e29b-41d4-a716-446655440001",
		SourceProviderKind: "s3",
		MediaProviderKind:  "s3",
		Port:               3000,
		Mode:               "server",
		BaseURL:            "http://localhost:3000",
		DatabaseURL:        pg.DSN,
		RedisURL:           rd.URL,
		SourceS3Endpoint:   rs.Endpoint,
		SourceS3AccessKey:  rs.AccessKey,
		SourceS3SecretKey:  rs.SecretKey,
		SourceS3Region:     rs.Region,
		SourceBucket:       "source",
		MediaS3Endpoint:    rs.Endpoint,
		MediaS3AccessKey:   rs.AccessKey,
		MediaS3SecretKey:   rs.SecretKey,
		MediaS3Region:      rs.Region,
		MediaBucket:        "media",
		HMACSecret:         "test-hmac-secret",
		APIKey:             "test-api-key",
		WebhookSecret:      "test-webhook-secret",
		KeyTokenSecret:     "test-key-token-secret",
		EncryptionKey:      "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		LargeFileThreshold: 5 * 1024 * 1024 * 1024,
		CacheMaxSize:       64 * 1024 * 1024,
	}

	expectedTarget, err := cfg.DeploymentTarget()
	if err != nil {
		t.Fatalf("build deployment target: %v", err)
	}
	target, err := deployment.BindTarget(ctx, queries, expectedTarget)
	if err != nil {
		t.Fatalf("bind deployment target: %v", err)
	}

	keyWrapper, err := encryption.NewKeyWrapper(cfg.EncryptionKey)
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	redisOpt, err := redis.ParseURL(rd.URL)
	if err != nil {
		t.Fatalf("parse redis URL: %v", err)
	}
	redisClient := redis.NewClient(redisOpt)

	deps := &server.Deps{
		SourceStore: sourceStore,
		MediaStore:  mediaStore,
		Cache:       lru,
		QueueClient: queueClient,
		DBQueries:   queries,
		Lifecycle:   lifecycleCoordinator,
		Inspector:   inspector,
		Redis:       redisClient,
		KeyWrapper:  keyWrapper,
		DBPing:      pool.Ping,
		RedisPing: func(ctx context.Context) error {
			return redisClient.Ping(ctx).Err()
		},
		Target: target,
	}

	ts := httptest.NewServer(server.New(cfg, deps).Handler())

	cleanup := func() {
		ts.Close()
		queueClient.Close()
		inspector.Close()
		redisClient.Close()
		pool.Close()
	}

	return ts, cfg, store, queries, lru, pool, cleanup
}

func newS3BackedTestServerWithDeps(t *testing.T) (*httptest.Server, *config.Config, storage.Storage, *dbq.Queries, *cache.LRU, func()) {
	ts, cfg, store, queries, lru, _, cleanup := newS3BackedTestServerWithOptions(t, testServerOptions{})
	return ts, cfg, store, queries, lru, cleanup
}

func newS3BackedTestServer(t *testing.T) (*httptest.Server, *config.Config, storage.Storage, func()) {
	ts, cfg, store, _, _, cleanup := newS3BackedTestServerWithDeps(t)
	return ts, cfg, store, cleanup
}

func buildTestPNG(t *testing.T) []byte {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
	img.Set(1, 0, color.NRGBA{R: 0, G: 255, B: 0, A: 255})
	img.Set(0, 1, color.NRGBA{R: 0, G: 0, B: 255, A: 255})
	img.Set(1, 1, color.NRGBA{R: 255, G: 255, B: 0, A: 255})

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}

	return buf.Bytes()
}

func TestImageHandler_WithS3CompatibleStorage(t *testing.T) {
	ts, cfg, store, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	ctx := context.Background()
	hash := strings.Repeat("a", 64)
	sourceKey := "uploads/aa/" + hash + "-upload-id.png"
	if err := store.Put(ctx, cfg.SourceBucket, sourceKey, bytes.NewReader(buildTestPNG(t)), "image/png"); err != nil {
		t.Fatalf("upload source fixture: %v", err)
	}

	requestSource := strings.ReplaceAll(url.PathEscape(sourceKey), "+", "%20") + ".webp"
	sig, err := signature.SignImage(cfg.HMACSecret, "w64", requestSource)
	if err != nil {
		t.Fatalf("sign image URL: %v", err)
	}

	resp, err := http.Get(ts.URL + "/img/" + sig + "/w64/" + requestSource)
	if err != nil {
		t.Fatalf("GET /img: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/webp" {
		t.Fatalf("expected image/webp content type, got %q", got)
	}

	keys, listErr := store.List(ctx, cfg.MediaBucket, lifecycle.CacheNamespace(hash))
	if listErr != nil {
		t.Fatalf("list cache objects: %v", listErr)
	}
	if len(keys) != 1 || !lifecycle.IsNamespacedCacheKey(keys[0]) {
		t.Fatalf("expected one hash-namespaced cache object, got %v", keys)
	}
}

func TestImageHandler_SourceNotFoundReturns404(t *testing.T) {
	ts, cfg, _, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	sourceKey := "uploads/bb/" + strings.Repeat("b", 64) + ".png"
	requestSource := strings.ReplaceAll(url.PathEscape(sourceKey), "+", "%20") + ".webp"
	sig, err := signature.SignImage(cfg.HMACSecret, "w64", requestSource)
	if err != nil {
		t.Fatalf("sign image URL: %v", err)
	}

	resp, err := http.Get(ts.URL + "/img/" + sig + "/w64/" + requestSource)
	if err != nil {
		t.Fatalf("GET /img: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Fallback") != "" {
		t.Fatal("expected no fallback header on error response")
	}
}

func TestImageHandler_CorruptSourceReturns422(t *testing.T) {
	ts, cfg, store, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	ctx := context.Background()
	hash := strings.Repeat("c", 64)
	sourceKey := "uploads/cc/" + hash + "-upload-id.png"
	if err := store.Put(ctx, cfg.SourceBucket, sourceKey, bytes.NewReader([]byte("not-a-valid-image")), "image/png"); err != nil {
		t.Fatalf("upload corrupt fixture: %v", err)
	}

	requestSource := strings.ReplaceAll(url.PathEscape(sourceKey), "+", "%20") + ".webp"
	sig, err := signature.SignImage(cfg.HMACSecret, "w64", requestSource)
	if err != nil {
		t.Fatalf("sign image URL: %v", err)
	}

	resp, err := http.Get(ts.URL + "/img/" + sig + "/w64/" + requestSource)
	if err != nil {
		t.Fatalf("GET /img: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Fallback") != "" {
		t.Fatal("expected no fallback header on error response")
	}
}

func TestImageHandler_UnattributableSourcePathReturns422(t *testing.T) {
	ts, cfg, _, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	requestSource := strings.ReplaceAll(url.PathEscape("legacy/unindexed-image.png"), "+", "%20") + ".webp"
	sig, err := signature.SignImage(cfg.HMACSecret, "w64", requestSource)
	if err != nil {
		t.Fatalf("sign image URL: %v", err)
	}

	resp, err := http.Get(ts.URL + "/img/" + sig + "/w64/" + requestSource)
	if err != nil {
		t.Fatalf("GET /img: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", resp.StatusCode)
	}
}

func TestImageHandler_UnsupportedFormatReturns400(t *testing.T) {
	ts, cfg, _, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	requestSource := strings.ReplaceAll(url.PathEscape("sample-image.png"), "+", "%20") + ".bmp"
	sig, err := signature.SignImage(cfg.HMACSecret, "w64", requestSource)
	if err != nil {
		t.Fatalf("sign image URL: %v", err)
	}

	resp, err := http.Get(ts.URL + "/img/" + sig + "/w64/" + requestSource)
	if err != nil {
		t.Fatalf("GET /img: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Fallback") != "" {
		t.Fatal("expected no fallback header on error response")
	}
}
