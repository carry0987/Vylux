package handler

import (
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"Vylux/internal/storage"
	apptracing "Vylux/internal/tracing"

	"github.com/labstack/echo/v5"
)

// StreamHandler serves HLS streaming assets (m3u8, init.mp4, m4s segments)
// from the media-bucket via S3 proxy with CDN-friendly cache headers.
//
// Routes:
//
//	GET /stream/:hash/*  →  S3: videos/{hash_prefix}/{hash}/{path}
type StreamHandler struct {
	mediaStore  storage.Storage
	mediaBucket string
}

// NewStreamHandler creates a StreamHandler.
func NewStreamHandler(mediaStore storage.Storage, mediaBucket string) *StreamHandler {
	return &StreamHandler{
		mediaStore:  mediaStore,
		mediaBucket: mediaBucket,
	}
}

// Handle proxies an HLS asset from S3.
//
//	/stream/{hash}/master.m3u8
//	/stream/{hash}/r1080_av1/playlist.m3u8
//	/stream/{hash}/r1080_av1/init.mp4
//	/stream/{hash}/r1080_av1/seg_000.m4s
func (h *StreamHandler) Handle(c *echo.Context) error {
	hash := c.Param("hash")
	filePath := c.Param("*")
	filePath = strings.TrimPrefix(filePath, "/")

	if hash == "" || filePath == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing hash or file path")
	}

	// Validate the file path to prevent directory traversal.
	if strings.Contains(filePath, "..") {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid path")
	}

	// Determine content type.
	ct := hlsContentType(filePath)
	if ct == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "unsupported file type")
	}

	// Build S3 key: videos/{hash_prefix}/{hash}/{filePath}
	s3Key := videoS3Key(hash, filePath)

	rc, err := h.mediaStore.Get(c.Request().Context(), h.mediaBucket, s3Key)
	if err != nil {
		slog.Warn("stream fetch failed", apptracing.LogFields(c.Request().Context(), "key", s3Key, "error", err)...)
		return echo.NewHTTPError(http.StatusNotFound, "not found")
	}
	defer rc.Close()

	// Set cache headers — immutable for segments and init, shorter for m3u8.
	resp := c.Response()
	if strings.HasSuffix(filePath, ".m3u8") {
		// Playlists may be updated (e.g. live), but for VOD they are immutable.
		// Use a long cache but allow revalidation.
		resp.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// Segments and init are truly immutable (content-addressed by hash).
		resp.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}

	resp.Header().Set("Content-Type", ct)
	resp.Header().Set("Access-Control-Allow-Origin", "*")
	resp.WriteHeader(http.StatusOK)

	if _, err := io.Copy(resp, rc); err != nil {
		slog.Warn("stream copy failed", apptracing.LogFields(c.Request().Context(), "key", s3Key, "error", err)...)
		// Headers already sent; can't change status code.
	}

	return nil
}

// videoS3Key builds the S3 object key for a video asset.
// Pattern: videos/{hash[0:2]}/{hash}/{filePath}
func videoS3Key(hash, filePath string) string {
	prefix := hash
	if len(hash) >= 2 {
		prefix = hash[:2]
	}
	return "videos/" + prefix + "/" + hash + "/" + filePath
}

// hlsContentType returns the MIME type for an HLS-related file extension.
// Returns empty string for unsupported types.
func hlsContentType(filePath string) string {
	ext := strings.ToLower(path.Ext(filePath))
	switch ext {
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".m4s":
		return "video/iso.segment"
	case ".mp4":
		return "video/mp4"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}
