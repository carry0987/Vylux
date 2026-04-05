package handler

import (
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"

	"Vylux/internal/signature"
	"Vylux/internal/storage"
	apptracing "Vylux/internal/tracing"

	"github.com/labstack/echo/v5"
)

// ThumbHandler serves pre-generated thumbnails and video covers from the
// media-bucket. Objects are produced asynchronously by image:thumbnail and
// video:cover queue handlers and then served via this synchronous endpoint.
//
// Route:
//
//	GET /thumb/{sig}/{encoded_key}
type ThumbHandler struct {
	mediaStore  storage.Storage
	mediaBucket string
	hmacSecret  string
}

// NewThumbHandler creates a ThumbHandler.
func NewThumbHandler(mediaStore storage.Storage, mediaBucket, hmacSecret string) *ThumbHandler {
	return &ThumbHandler{
		mediaStore:  mediaStore,
		mediaBucket: mediaBucket,
		hmacSecret:  hmacSecret,
	}
}

// Handle serves a signed thumbnail object from the media bucket.
func (h *ThumbHandler) Handle(c *echo.Context) error {
	sig := c.Param("sig")
	rawKey := c.Param("*")
	rawKey = strings.TrimPrefix(rawKey, "/")

	if sig == "" || rawKey == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing signature or object key")
	}

	ok, err := signature.VerifyThumb(h.hmacSecret, sig, rawKey)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "invalid signature")
	}

	objectKey, err := signature.CanonicalizeObjectKey(rawKey)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	rc, err := h.mediaStore.Get(c.Request().Context(), h.mediaBucket, objectKey)
	if err != nil {
		slog.Warn("thumb fetch failed", apptracing.LogFields(c.Request().Context(), "bucket", h.mediaBucket, "key", objectKey, "error", err)...)
		return echo.NewHTTPError(http.StatusNotFound, "not found")
	}
	defer rc.Close()

	contentType := thumbContentType(objectKey)

	resp := c.Response()
	resp.Header().Set("Content-Type", contentType)
	resp.Header().Set("Access-Control-Allow-Origin", "*")
	resp.WriteHeader(http.StatusOK)

	if _, err := io.Copy(resp, rc); err != nil {
		slog.Warn("thumb copy failed", apptracing.LogFields(c.Request().Context(), "bucket", h.mediaBucket, "key", objectKey, "error", err)...)
	}

	return nil
}

// thumbContentType returns the MIME type for a thumbnail object key.
func thumbContentType(objectKey string) string {
	ext := strings.ToLower(path.Ext(objectKey))
	if ext != "" {
		if ct := mime.TypeByExtension(ext); ct != "" {
			return ct
		}
	}
	return "application/octet-stream"
}
