package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"Vylux/internal/signature"
)

func TestOriginalHandler_WithS3CompatibleStorage(t *testing.T) {
	ts, cfg, store, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	ctx := context.Background()
	objectKey := "uploads/demo/sample image.png"
	body := buildTestPNG(t)
	if err := store.Put(ctx, cfg.SourceBucket, objectKey, bytes.NewReader(body), "image/png"); err != nil {
		t.Fatalf("upload source object: %v", err)
	}

	requestKey := strings.ReplaceAll(url.PathEscape(objectKey), "+", "%20")
	sig, err := signature.SignOriginal(cfg.HMACSecret, requestKey)
	if err != nil {
		t.Fatalf("sign original URL: %v", err)
	}

	resp, err := http.Get(ts.URL + "/original/" + sig + "/" + requestKey)
	if err != nil {
		t.Fatalf("GET /original: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("expected image/png content type, got %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("expected immutable cache header, got %q", got)
	}

	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatal("proxied original body did not match stored object")
	}
}

func TestOriginalHandler_InvalidSignatureRejected(t *testing.T) {
	ts, _, _, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/original/bad-signature/uploads%2Fdemo%2Fsample.png")
	if err != nil {
		t.Fatalf("GET /original with invalid signature: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestOriginalHandler_MissingObjectReturnsNotFound(t *testing.T) {
	ts, cfg, _, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	requestKey := "uploads%2Fmissing%2Fsample.png"
	sig, err := signature.SignOriginal(cfg.HMACSecret, requestKey)
	if err != nil {
		t.Fatalf("sign original URL: %v", err)
	}

	resp, err := http.Get(ts.URL + "/original/" + sig + "/" + requestKey)
	if err != nil {
		t.Fatalf("GET missing /original object: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
