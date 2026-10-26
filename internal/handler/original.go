package handler

import (
	"bufio"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"

	"Vylux/internal/encryption"
	"Vylux/internal/signature"
	"Vylux/internal/storage"
	apptracing "Vylux/internal/tracing"

	"github.com/labstack/echo/v5"
)

// OriginalHandler proxies signed source objects from the original uploads bucket.
//
// Route:
//
//	GET /original/{sig}/{encoded_key}
type OriginalHandler struct {
	sourceStore  storage.Storage
	sourceBucket string
	hmacSecret   string
	sourceCipher *encryption.ImageSourceCipher
}

// NewOriginalHandler creates an OriginalHandler.
func NewOriginalHandler(sourceStore storage.Storage, sourceCipher *encryption.ImageSourceCipher, sourceBucket, hmacSecret string) *OriginalHandler {
	return &OriginalHandler{
		sourceStore:  sourceStore,
		sourceBucket: sourceBucket,
		hmacSecret:   hmacSecret,
		sourceCipher: sourceCipher,
	}
}

// Handle serves a signed original object from storage.
func (h *OriginalHandler) Handle(c *echo.Context) error {
	sig := c.Param("sig")
	rawKey := c.Param("*")
	if h.sourceCipher.Enabled() {
		rawKey = c.Param("source")
	}
	if sig == "" || rawKey == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing signature or object key")
	}

	var (
		ok  bool
		err error
	)
	if h.sourceCipher.Enabled() {
		ok, err = signature.VerifyEncryptedOriginal(h.hmacSecret, sig, rawKey)
	} else {
		ok, err = signature.VerifyOriginal(h.hmacSecret, sig, rawKey)
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "invalid signature")
	}

	var objectKey string
	if h.sourceCipher.Enabled() {
		objectKey, err = h.sourceCipher.Decrypt(rawKey)
		if err != nil {
			return echo.NewHTTPError(http.StatusForbidden, "invalid source token")
		}
	} else {
		objectKey, err = signature.CanonicalizeObjectKey(rawKey)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
	}
	sourceID := sourceFingerprint(objectKey)

	rc, err := h.sourceStore.Get(c.Request().Context(), h.sourceBucket, objectKey)
	if err != nil {
		slog.Warn("original fetch failed", apptracing.LogFields(c.Request().Context(), "bucket", h.sourceBucket, "source_fingerprint", sourceID, "error", err)...)
		return echo.NewHTTPError(http.StatusNotFound, "not found")
	}
	defer rc.Close()

	reader := bufio.NewReader(rc)
	contentType := contentTypeForObject(objectKey, reader)

	resp := c.Response()
	resp.Header().Set("Content-Type", contentType)
	resp.WriteHeader(http.StatusOK)

	if _, err := io.Copy(resp, reader); err != nil {
		slog.Warn("original proxy copy failed", apptracing.LogFields(c.Request().Context(), "bucket", h.sourceBucket, "source_fingerprint", sourceID, "error", err)...)
	}

	return nil
}

func contentTypeForObject(objectKey string, reader *bufio.Reader) string {
	ext := strings.ToLower(path.Ext(objectKey))
	if ext != "" {
		if contentType := mime.TypeByExtension(ext); contentType != "" {
			return contentType
		}
	}

	sniff, err := reader.Peek(512)
	if err == nil || len(sniff) > 0 {
		return http.DetectContentType(sniff)
	}

	return "application/octet-stream"
}
