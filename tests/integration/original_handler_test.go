package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"Vylux/internal/config"
	"Vylux/internal/encryption"
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

func TestOriginalHandler_WithEncryptedSourceToken(t *testing.T) {
	const imageSourceKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	ts, cfg, store, _, _, cleanup := newS3BackedTestServerWithDeps(t, func(cfg *config.Config) {
		cfg.ImageSourceKey = imageSourceKey
	})
	defer cleanup()

	ctx := context.Background()
	objectKey := "private/uploads/original image.png"
	body := buildTestPNG(t)
	if err := store.Put(ctx, cfg.SourceBucket, objectKey, bytes.NewReader(body), "image/png"); err != nil {
		t.Fatalf("upload source object: %v", err)
	}

	sourceCipher, err := encryption.NewImageSourceCipher(imageSourceKey)
	if err != nil {
		t.Fatalf("new image source cipher: %v", err)
	}
	token, err := sourceCipher.Encrypt(objectKey)
	if err != nil {
		t.Fatalf("encrypt source key: %v", err)
	}
	sig, err := signature.SignEncryptedOriginal(cfg.HMACSecret, token)
	if err != nil {
		t.Fatalf("sign encrypted original URL: %v", err)
	}
	requestURL := ts.URL + "/original/" + sig + "/" + token
	if strings.Contains(requestURL, "original") && strings.Contains(requestURL, "image.png") {
		t.Fatalf("encrypted original URL exposed source key: %s", requestURL)
	}

	resp, err := http.Get(requestURL)
	if err != nil {
		t.Fatalf("GET encrypted /original: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read original response: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("encrypted original response did not match stored object")
	}
}

func TestOriginalHandler_EncryptedTokenErrorsArePrivate(t *testing.T) {
	const imageSourceKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	ts, cfg, _, _, _, cleanup := newS3BackedTestServerWithDeps(t, func(cfg *config.Config) {
		cfg.ImageSourceKey = imageSourceKey
	})
	defer cleanup()

	sourceCipher, err := encryption.NewImageSourceCipher(imageSourceKey)
	if err != nil {
		t.Fatalf("new image source cipher: %v", err)
	}
	validToken, err := sourceCipher.Encrypt("private/uploads/secret-name.png")
	if err != nil {
		t.Fatalf("encrypt source key: %v", err)
	}
	tamperedToken := validToken[:len(validToken)-1] + "A"
	invalidTokens := []string{"plain-image.png", "not-a-token", tamperedToken}

	var expectedBody string
	for _, token := range invalidTokens {
		sig, err := signature.SignEncryptedOriginal(cfg.HMACSecret, token)
		if err != nil {
			t.Fatalf("sign invalid token %q: %v", token, err)
		}
		resp, err := http.Get(ts.URL + "/original/" + sig + "/" + token)
		if err != nil {
			t.Fatalf("GET invalid encrypted original %q: %v", token, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			t.Fatalf("read invalid token response: %v", readErr)
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("token %q: expected 403, got %d", token, resp.StatusCode)
		}
		if strings.Contains(string(body), token) || strings.Contains(string(body), "secret-name") {
			t.Fatalf("token %q leaked sensitive details in response: %s", token, body)
		}
		if expectedBody == "" {
			expectedBody = string(body)
		} else if string(body) != expectedBody {
			t.Fatalf("token %q returned distinguishable error body %q, want %q", token, body, expectedBody)
		}
	}

	resp, err := http.Get(ts.URL + "/original/bad-signature/not-a-token")
	if err != nil {
		t.Fatalf("GET invalid signature and token: %v", err)
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		t.Fatalf("read invalid signature response: %v", readErr)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected invalid signature to return 403, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "invalid signature") {
		t.Fatalf("expected signature validation before token decryption, got %s", body)
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
