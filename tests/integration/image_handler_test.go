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
	"time"

	"Vylux/internal/cache"
	"Vylux/internal/config"
	"Vylux/internal/db/dbq"
	"Vylux/internal/encryption"
	"Vylux/internal/queue"
	"Vylux/internal/server"
	"Vylux/internal/signature"
	"Vylux/internal/storage"

	redis "github.com/redis/go-redis/v9"
)

func newS3BackedTestServerWithDeps(t *testing.T) (*httptest.Server, *config.Config, storage.Storage, *dbq.Queries, *cache.LRU, func()) {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	integrationEnv.mu.Lock()
	locked := true
	release := func() {
		if locked {
			integrationEnv.mu.Unlock()
			locked = false
		}
	}
	if err := integrationEnv.reset(ctx); err != nil {
		release()
		t.Fatalf("reset shared integration env: %v", err)
	}

	store := integrationEnv.rawStore
	queries := newIntegrationQueries()
	lru := newIntegrationLRU()

	queueClient, err := queue.NewClient(integrationEnv.baseConfig.RedisURL)
	if err != nil {
		release()
		t.Fatalf("queue client: %v", err)
	}

	inspector, err := queue.NewInspector(integrationEnv.baseConfig.RedisURL)
	if err != nil {
		queueClient.Close()
		release()
		t.Fatalf("queue inspector: %v", err)
	}

	cfg := cloneIntegrationConfig()

	keyWrapper, err := encryption.NewKeyWrapper(cfg.EncryptionKey)
	if err != nil {
		inspector.Close()
		queueClient.Close()
		release()
		t.Fatalf("key wrapper: %v", err)
	}

	redisOpt, err := redis.ParseURL(integrationEnv.baseConfig.RedisURL)
	if err != nil {
		inspector.Close()
		queueClient.Close()
		release()
		t.Fatalf("parse redis URL: %v", err)
	}
	redisClient := redis.NewClient(redisOpt)

	deps := &server.Deps{
		SourceStore: storage.WithInstrumentation(store, "source", "s3"),
		MediaStore:  storage.WithInstrumentation(store, "media", "s3"),
		Cache:       lru,
		QueueClient: queueClient,
		DBQueries:   queries,
		Inspector:   inspector,
		Redis:       redisClient,
		KeyWrapper:  keyWrapper,
		DBPing:      integrationEnv.pool.Ping,
		RedisPing: func(ctx context.Context) error {
			return redisClient.Ping(ctx).Err()
		},
	}

	ts := httptest.NewServer(server.New(cfg, deps).Handler())

	cleanup := func() {
		ts.Close()
		queueClient.Close()
		inspector.Close()
		redisClient.Close()
		if err := integrationEnv.reset(context.Background()); err != nil {
			t.Logf("reset shared integration env: %v", err)
		}
		release()
	}

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
	sourceKey := "sample image.png"
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

	deadline := time.Now().Add(5 * time.Second)
	for {
		keys, listErr := store.List(ctx, cfg.MediaBucket, "cache/")
		if listErr == nil && len(keys) > 0 {
			return
		}
		if time.Now().After(deadline) {
			if listErr != nil {
				t.Fatalf("list cache objects: %v", listErr)
			}
			t.Fatal("expected cached object to be written to media bucket")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestImageHandler_SourceNotFoundReturns404(t *testing.T) {
	ts, cfg, _, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	requestSource := strings.ReplaceAll(url.PathEscape("missing-image.png"), "+", "%20") + ".webp"
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
	sourceKey := "corrupt-image.png"
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
