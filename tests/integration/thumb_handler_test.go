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

func TestThumbHandler_ServesPreGeneratedThumbnail(t *testing.T) {
	ts, cfg, store, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	ctx := context.Background()
	body := buildTestPNG(t)

	// Simulate a pre-generated thumbnail uploaded by the image:thumbnail worker.
	// Key pattern: images/{hash[0:2]}/{hash}/thumb.webp
	objectKey := "images/ab/abcdef1234567890/thumb.png"
	if err := store.Put(ctx, cfg.MediaBucket, objectKey, bytes.NewReader(body), "image/png"); err != nil {
		t.Fatalf("upload thumbnail object: %v", err)
	}

	requestKey := strings.ReplaceAll(url.PathEscape(objectKey), "+", "%20")
	sig, err := signature.SignThumb(cfg.HMACSecret, requestKey)
	if err != nil {
		t.Fatalf("sign thumb URL: %v", err)
	}

	resp, err := http.Get(ts.URL + "/thumb/" + sig + "/" + requestKey)
	if err != nil {
		t.Fatalf("GET /thumb: %v", err)
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
		t.Fatal("proxied thumbnail body did not match stored object")
	}
}

func TestThumbHandler_ServesVideoCover(t *testing.T) {
	ts, cfg, store, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	ctx := context.Background()
	body := buildTestPNG(t) // any image data

	// Video covers are stored at: videos/{hash[0:2]}/{hash}/cover.jpg
	objectKey := "videos/ab/abcdef1234567890/cover.jpg"
	if err := store.Put(ctx, cfg.MediaBucket, objectKey, bytes.NewReader(body), "image/jpeg"); err != nil {
		t.Fatalf("upload cover object: %v", err)
	}

	requestKey := strings.ReplaceAll(url.PathEscape(objectKey), "+", "%20")
	sig, err := signature.SignThumb(cfg.HMACSecret, requestKey)
	if err != nil {
		t.Fatalf("sign thumb URL: %v", err)
	}

	resp, err := http.Get(ts.URL + "/thumb/" + sig + "/" + requestKey)
	if err != nil {
		t.Fatalf("GET /thumb (cover): %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatal("proxied cover body did not match stored object")
	}
}

func TestThumbHandler_InvalidSignatureRejected(t *testing.T) {
	ts, _, _, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/thumb/bad-signature/images%2Fab%2Fabc%2Fthumb.png")
	if err != nil {
		t.Fatalf("GET /thumb with invalid signature: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestThumbHandler_MissingObjectReturnsNotFound(t *testing.T) {
	ts, cfg, _, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	requestKey := "images%2Fab%2Fmissing%2Fthumb.png"
	sig, err := signature.SignThumb(cfg.HMACSecret, requestKey)
	if err != nil {
		t.Fatalf("sign thumb URL: %v", err)
	}

	resp, err := http.Get(ts.URL + "/thumb/" + sig + "/" + requestKey)
	if err != nil {
		t.Fatalf("GET missing /thumb object: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestThumbHandler_CrossEndpointSignatureBlocked(t *testing.T) {
	ts, cfg, store, cleanup := newS3BackedTestServer(t)
	defer cleanup()

	ctx := context.Background()
	objectKey := "images/ab/abc123/thumb.png"
	if err := store.Put(ctx, cfg.MediaBucket, objectKey, bytes.NewReader(buildTestPNG(t)), "image/png"); err != nil {
		t.Fatalf("upload object: %v", err)
	}

	// Sign with /original domain — must not work on /thumb.
	requestKey := strings.ReplaceAll(url.PathEscape(objectKey), "+", "%20")
	originalSig, err := signature.SignOriginal(cfg.HMACSecret, requestKey)
	if err != nil {
		t.Fatalf("sign original URL: %v", err)
	}

	resp, err := http.Get(ts.URL + "/thumb/" + originalSig + "/" + requestKey)
	if err != nil {
		t.Fatalf("GET /thumb with original sig: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-endpoint sig, got %d", resp.StatusCode)
	}
}
