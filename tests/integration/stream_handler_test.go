package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

func streamObjectKey(hash, relPath string) string {
	prefix := hash
	if len(hash) >= 2 {
		prefix = hash[:2]
	}
	return "videos/" + prefix + "/" + hash + "/" + relPath
}

func TestStreamHandler_WithS3CompatibleStorage(t *testing.T) {
	ts, cfg, store, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	ctx := context.Background()
	hash := "abcdef1234567890"
	playlistKey := streamObjectKey(hash, "master.m3u8")
	playlistBody := []byte("#EXTM3U\n#EXT-X-VERSION:7\n")

	if err := store.Put(ctx, cfg.MediaBucket, playlistKey, bytes.NewReader(playlistBody), "application/vnd.apple.mpegurl"); err != nil {
		t.Fatalf("upload playlist: %v", err)
	}

	resp, err := http.Get(ts.URL + "/stream/" + hash + "/master.m3u8")
	if err != nil {
		t.Fatalf("GET /stream master playlist: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/vnd.apple.mpegurl" {
		t.Fatalf("expected playlist content type, got %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected wildcard CORS header, got %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("expected immutable cache header, got %q", got)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read playlist body: %v", err)
	}
	if !bytes.Equal(body, playlistBody) {
		t.Fatalf("unexpected playlist body: got %q want %q", body, playlistBody)
	}
}

func TestStreamHandler_InvalidPathRejected(t *testing.T) {
	ts, _, _, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/stream/abcdef1234567890/../secret.m3u8")
	if err != nil {
		t.Fatalf("GET /stream invalid path: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}
