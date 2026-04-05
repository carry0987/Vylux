package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"Vylux/internal/db/dbq"
	"Vylux/internal/encryption"
	apptracing "Vylux/internal/tracing"

	"github.com/labstack/echo/v5"
)

// KeyHandler serves the AES-128 decryption key for encrypted HLS streams.
//
// Endpoint: GET /api/key/:hash
//   - Authorization: Bearer {token}
//
// The handler verifies the token (HMAC-SHA256 signature, expiration, and hash match),
// then returns the 16-byte AES key with Cache-Control: no-store.
type KeyHandler struct {
	queries        *dbq.Queries
	keyTokenSecret string
	wrapper        *encryption.KeyWrapper
}

// NewKeyHandler creates a KeyHandler.
func NewKeyHandler(queries *dbq.Queries, keyTokenSecret string, wrapper *encryption.KeyWrapper) *KeyHandler {
	return &KeyHandler{
		queries:        queries,
		keyTokenSecret: keyTokenSecret,
		wrapper:        wrapper,
	}
}

// keyTokenPayload is the JSON structure embedded in the Bearer token.
type keyTokenPayload struct {
	Hash string `json:"hash"`
	Exp  int64  `json:"exp"` // Unix timestamp
}

// Handle serves GET /api/key/:hash.
func (h *KeyHandler) Handle(c *echo.Context) error {
	hash := c.Param("hash")
	if hash == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing hash")
	}

	// Extract token from Authorization header.
	var token string
	if auth := c.Request().Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimPrefix(auth, "Bearer ")
	}

	if token == "" {
		return c.String(http.StatusUnauthorized, "Unauthorized")
	}

	// Verify token.
	if err := h.verifyToken(token, hash); err != nil {
		return c.String(http.StatusForbidden, "Forbidden")
	}

	// Fetch from DB.
	ctx := c.Request().Context()
	row, err := h.queries.GetEncryptionKey(ctx, hash)
	if err != nil {
		return c.String(http.StatusNotFound, "Not Found")
	}

	aesKey, err := h.wrapper.Unwrap(row.WrappedKey, row.WrapNonce, row.KekVersion)
	if err != nil {
		slog.Error("unwrap encryption key failed", apptracing.LogFields(ctx, "hash", hash, "error", err)...)
		return c.String(http.StatusInternalServerError, "Internal Server Error")
	}

	c.Response().Header().Set("Cache-Control", "no-store")
	return c.Blob(http.StatusOK, "application/octet-stream", aesKey)
}

// verifyToken validates the HMAC-SHA256 token against the requested hash.
//
// Token format: base64url( JSON({ "hash": "...", "exp": <unix> }) ) + "." + base64url( HMAC-SHA256(payload, secret) )
func (h *KeyHandler) verifyToken(token, expectedHash string) error {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid token format")
	}

	payloadB64, sigB64 := parts[0], parts[1]

	// Decode and verify HMAC signature.
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}

	mac := hmac.New(sha256.New, []byte(h.keyTokenSecret))
	mac.Write([]byte(payloadB64)) // sign the base64-encoded payload to avoid canonicalization issues
	expectedSig := mac.Sum(nil)

	if !hmac.Equal(sigBytes, expectedSig) {
		return fmt.Errorf("invalid signature")
	}

	// Parse payload.
	var payload keyTokenPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	// Check expiration.
	if time.Now().Unix() > payload.Exp {
		return fmt.Errorf("token expired")
	}

	// Check hash match.
	if payload.Hash != expectedHash {
		return fmt.Errorf("hash mismatch")
	}

	return nil
}
