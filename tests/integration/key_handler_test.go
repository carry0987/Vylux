package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
	"uuid"

	"Vylux/internal/db/dbq"
	"Vylux/internal/encryption"
)

// TestKeyHandler_NoToken verifies 401 without a token.
func TestKeyHandler_NoToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, cleanup := newTestServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/key/some-key")
	if err != nil {
		t.Fatalf("GET /api/key/:id: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	assertJSONErrorResponse(t, resp, "Unauthorized")
}

// TestKeyHandler_InvalidToken verifies 403 with an invalid token.
func TestKeyHandler_InvalidToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, _, queries, _, cleanup := newS3BackedTestServerWithDeps(t)
	defer cleanup()

	keyID := seedEncryptionKey(t, cfg.EncryptionKey, queries, "some-hash", encryption.AssetTypeVideo)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/key/"+keyID, nil)
	req.Header.Set("Authorization", "Bearer invalid.token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/key/:id: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
	assertJSONErrorResponse(t, resp, "Forbidden")
}

// TestKeyHandler_ExpiredToken verifies 403 with an expired token.
func TestKeyHandler_ExpiredToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, _, queries, _, cleanup := newS3BackedTestServerWithDeps(t)
	defer cleanup()

	hash := "expired-token-hash"
	keyID := seedEncryptionKey(t, cfg.EncryptionKey, queries, hash, encryption.AssetTypeVideo)
	token := generateKeyToken(hash, cfg.KeyTokenSecret, -1*time.Hour)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/key/"+keyID, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/key/:id: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
	assertJSONErrorResponse(t, resp, "Forbidden")
}

// TestKeyHandler_HashMismatch verifies 403 when token hash doesn't match URL hash.
func TestKeyHandler_HashMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, cfg, _, queries, _, cleanup := newS3BackedTestServerWithDeps(t)
	defer cleanup()

	keyID := seedEncryptionKey(t, cfg.EncryptionKey, queries, "correct-hash", encryption.AssetTypeVideo)
	token := generateKeyToken("wrong-hash", cfg.KeyTokenSecret, 1*time.Hour)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/key/"+keyID, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/key/:id: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
	assertJSONErrorResponse(t, resp, "Forbidden")
}

func assertJSONErrorResponse(t *testing.T, resp *http.Response, wantMessage string) {
	t.Helper()

	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("expected JSON content type, got %q", got)
	}

	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Message != wantMessage {
		t.Fatalf("expected message %q, got %#v", wantMessage, body)
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

func seedEncryptionKey(t *testing.T, encryptionKey string, queries *dbq.Queries, hash, assetType string) string {
	t.Helper()

	wrapper, err := encryption.NewKeyWrapper(encryptionKey)
	if err != nil {
		t.Fatalf("new key wrapper: %v", err)
	}

	wrappedKey, wrapNonce, kekVersion, err := wrapper.Wrap([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatalf("wrap key: %v", err)
	}

	keyID := uuid.New()
	if _, err := queries.UpsertStreamEncryptionKey(context.Background(), dbq.UpsertStreamEncryptionKeyParams{
		ID:            keyID,
		SourceHash:    hash,
		AssetType:     assetType,
		PackagingType: encryption.PackagingTypeHLS,
		WrappedKey:    wrappedKey,
		WrapNonce:     wrapNonce,
		KekVersion:    kekVersion,
		Kid:           "kid",
		Scheme:        encryption.DefaultProtectionScheme,
	}); err != nil {
		t.Fatalf("seed encryption key: %v", err)
	}

	return keyID.String()
}
