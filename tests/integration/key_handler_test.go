package integration

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestKeyHandler_NoToken verifies 401 without a token.
func TestKeyHandler_NoToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, cleanup := newTestServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/key/some-hash")
	if err != nil {
		t.Fatalf("GET /api/key/:hash: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

// TestKeyHandler_InvalidToken verifies 403 with an invalid token.
func TestKeyHandler_InvalidToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, cleanup := newTestServer(t)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/key/some-hash", nil)
	req.Header.Set("Authorization", "Bearer invalid.token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/key/:hash: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

// TestKeyHandler_ExpiredToken verifies 403 with an expired token.
func TestKeyHandler_ExpiredToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, cleanup := newTestServer(t)
	defer cleanup()

	hash := "expired-token-hash"
	token := generateKeyToken(hash, cfg.KeyTokenSecret, -1*time.Hour)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/key/"+hash, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/key/:hash: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

// TestKeyHandler_HashMismatch verifies 403 when token hash doesn't match URL hash.
func TestKeyHandler_HashMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, cleanup := newTestServer(t)
	defer cleanup()

	token := generateKeyToken("wrong-hash", cfg.KeyTokenSecret, 1*time.Hour)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/key/correct-hash", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/key/:hash: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

// generateKeyToken creates a signed key token for testing.
// Format: base64url(JSON{hash,exp}) + "." + base64url(HMAC-SHA256)
func generateKeyToken(hash, secret string, ttl time.Duration) string {
	type tokenPayload struct {
		Hash string `json:"hash"`
		Exp  int64  `json:"exp"`
	}

	payload := tokenPayload{
		Hash: hash,
		Exp:  time.Now().Add(ttl).Unix(),
	}

	payloadBytes, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payloadB64))
	sig := mac.Sum(nil)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return payloadB64 + "." + sigB64
}
