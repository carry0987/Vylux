package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"Vylux/internal/db/dbq"
	"Vylux/internal/handler"
	"Vylux/internal/signature"
)

// TestCleanup_DeleteMedia verifies the DELETE /api/media/:hash endpoint.
func TestCleanup_DeleteMedia(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, cleanup := newTestServer(t)
	defer cleanup()

	body := handler.JobRequest{
		Type:        "image:thumbnail",
		Hash:        "cleanup-test-hash",
		Source:      "uploads/test.jpg",
		CallbackURL: "http://example.com/callback",
	}
	jsonBody, _ := json.Marshal(body)

	createReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/jobs", bytes.NewReader(jsonBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-API-Key", cfg.APIKey)

	resp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	resp.Body.Close()

	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/media/"+body.Hash, nil)
	delReq.Header.Set("X-API-Key", cfg.APIKey)

	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE /api/media/:hash: %v", err)
	}
	defer delResp.Body.Close()

	if delResp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(delResp.Body)
		t.Errorf("expected 204, got %d: %s", delResp.StatusCode, string(respBody))
	}

	// Verify deletion is idempotent (second DELETE should also return 204).
	delReq2, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/media/"+body.Hash, nil)
	delReq2.Header.Set("X-API-Key", cfg.APIKey)

	delResp2, err := http.DefaultClient.Do(delReq2)
	if err != nil {
		t.Fatalf("second DELETE: %v", err)
	}
	defer delResp2.Body.Close()

	if delResp2.StatusCode != http.StatusNoContent {
		t.Errorf("idempotent DELETE: expected 204, got %d", delResp2.StatusCode)
	}
}

// TestCleanup_DeleteMedia_Unauthorized verifies that DELETE without API key fails.
func TestCleanup_DeleteMedia_Unauthorized(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, cleanup := newTestServer(t)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/media/some-hash", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestCleanup_DeleteMedia_RemovesTrackedImageCache(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, store, queries, lru, cleanup := newS3BackedTestServerWithDeps(t)
	defer cleanup()

	ctx := context.Background()
	hash := strings.Repeat("a", 64)
	sourceKey := "uploads/" + hash[:2] + "/" + hash
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
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var entry dbq.ImageCacheEntry
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, listErr := queries.ListImageCacheEntriesByHash(ctx, hash)
		if listErr == nil && len(entries) == 1 {
			entry = entries[0]
			exists, existsErr := store.Exists(ctx, cfg.MediaBucket, entry.StorageKey)
			if existsErr == nil && exists {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("expected tracked image cache entry and stored cache object to exist before cleanup")
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, ok := lru.Get(entry.CacheKey); !ok {
		t.Fatal("expected image cache entry to be present in LRU before cleanup")
	}

	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/media/"+hash, nil)
	delReq.Header.Set("X-API-Key", cfg.APIKey)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE /api/media/:hash: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(delResp.Body)
		t.Fatalf("expected 204, got %d: %s", delResp.StatusCode, string(body))
	}

	entries, err := queries.ListImageCacheEntriesByHash(ctx, hash)
	if err != nil {
		t.Fatalf("list image cache entries after cleanup: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected tracked image cache entries to be deleted, got %d", len(entries))
	}
	if _, ok := lru.Get(entry.CacheKey); ok {
		t.Fatal("expected tracked image cache entry to be removed from LRU")
	}
	exists, err := store.Exists(ctx, cfg.MediaBucket, entry.StorageKey)
	if err != nil {
		t.Fatalf("check stored cache object after cleanup: %v", err)
	}
	if exists {
		t.Fatal("expected stored cache object to be deleted")
	}
}
