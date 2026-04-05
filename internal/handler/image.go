package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"Vylux/internal/cache"
	"Vylux/internal/db/dbq"
	"Vylux/internal/image"
	appmetrics "Vylux/internal/metrics"
	"Vylux/internal/signature"
	"Vylux/internal/storage"
	apptracing "Vylux/internal/tracing"

	"github.com/labstack/echo/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/singleflight"
)

// ImageHandler handles synchronous image processing requests.
//
//	GET /img/{sig}/{options}/{encoded_source}.{format}
type ImageHandler struct {
	sourceStore  storage.Storage
	mediaStore   storage.Storage
	cache        *cache.LRU
	queries      *dbq.Queries
	sourceBucket string
	mediaBucket  string
	hmacSecret   string

	sourceFlight  singleflight.Group
	processFlight singleflight.Group
}

// NewImageHandler creates an ImageHandler with the given dependencies.
func NewImageHandler(
	sourceStore storage.Storage,
	mediaStore storage.Storage,
	lru *cache.LRU,
	queries *dbq.Queries,
	sourceBucket, mediaBucket, hmacSecret string,
) *ImageHandler {
	return &ImageHandler{
		sourceStore:  sourceStore,
		mediaStore:   mediaStore,
		cache:        lru,
		queries:      queries,
		sourceBucket: sourceBucket,
		mediaBucket:  mediaBucket,
		hmacSecret:   hmacSecret,
	}
}

// Handle processes an image request.
//
// Route: GET /img/:sig/:opts/*source
//
// The *source wildcard captures the source key plus output format extension as
// Echo presents it to the handler, e.g. "media/uploads/abc.jpg.webp".
func (h *ImageHandler) Handle(c *echo.Context) error {
	sig := c.Param("sig")
	optsRaw := c.Param("opts")
	sourcePath := c.Param("*") // everything after /img/:sig/:opts/

	// Strip leading slash if Echo adds one.
	sourcePath = strings.TrimPrefix(sourcePath, "/")
	if sourcePath == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing source path")
	}

	// Split output format from the source path.
	// e.g. "media%2Fuploads%2Fabc.jpg.webp"  →  ext = ".webp"
	ext := path.Ext(sourcePath)
	format := image.ParseFormat(ext)
	if format == image.FormatOriginal {
		return echo.NewHTTPError(http.StatusBadRequest, "unsupported output format")
	}

	// The actual S3 key is the source path without the output format extension,
	// then URL-decoded if the client percent-encoded any reserved characters.
	encodedSource := strings.TrimSuffix(sourcePath, ext)
	sourceKey, err := url.PathUnescape(encodedSource)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid source encoding")
	}

	// ── 1. Parse processing options ──
	opts, err := image.ParseOptions(optsRaw)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("bad options: %v", err))
	}
	opts.Format = format

	// ── 2. Verify HMAC signature against canonicalized request components ──
	ok, err := signature.VerifyImage(h.hmacSecret, sig, optsRaw, sourcePath)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "invalid signature")
	}

	// ── 3. Cache lookup ──
	cacheKey := processingHash(sourceKey, opts)

	// 3a. Memory LRU
	if data, ok := h.cache.Get(cacheKey); ok {
		h.trackCacheEntry(c.Request().Context(), sourceKey, cacheKey, s3CacheKey(format, cacheKey))
		appmetrics.ObserveImageCache("memory", "hit")
		appmetrics.ObserveImageResult("memory_hit")
		return h.sendImage(c, data, opts.Format)
	}
	appmetrics.ObserveImageCache("memory", "miss")

	// 3b. S3 cache
	storageCacheKey := s3CacheKey(format, cacheKey)
	if data, err := h.fetchFromStorage(c.Request().Context(), h.mediaStore, h.mediaBucket, storageCacheKey); err == nil {
		h.trackCacheEntry(c.Request().Context(), sourceKey, cacheKey, storageCacheKey)
		appmetrics.ObserveImageCache("storage", "hit")
		appmetrics.ObserveImageResult("storage_hit")
		// Populate LRU for subsequent in-instance hits.
		h.cache.Set(cacheKey, data)
		return h.sendImage(c, data, opts.Format)
	}
	appmetrics.ObserveImageCache("storage", "miss")

	// ── 4. Fetch original (singleflight) ──
	rawVal, err, _ := h.sourceFlight.Do(sourceKey, func() (any, error) {
		fetchCtx := apptracing.BackgroundContext(c.Request().Context())
		fetchCtx, span := apptracing.Tracer("vylux/image").Start(fetchCtx, "image.fetch.original",
			trace.WithAttributes(
				attribute.String("storage.role", "source"),
				attribute.String("storage.bucket", h.sourceBucket),
				attribute.String("media.hash", sourceKey),
			),
		)
		defer span.End()

		data, fetchErr := h.fetchFromStorage(fetchCtx, h.sourceStore, h.sourceBucket, sourceKey)
		if fetchErr != nil {
			span.RecordError(fetchErr)
			span.SetStatus(codes.Error, fetchErr.Error())
		}

		return data, fetchErr
	})
	if err != nil {
		status := http.StatusBadGateway
		message := "source storage unavailable"
		if storage.IsNotFound(err) {
			status = http.StatusNotFound
			message = "source image not found"
		}
		appmetrics.ObserveImageError("source_fetch", status)
		appmetrics.ObserveImageResult("error")
		slog.Warn("source fetch failed", apptracing.LogFields(c.Request().Context(), "key", sourceKey, "status", status, "error", err)...)
		return echo.NewHTTPError(status, message)
	}
	raw := rawVal.([]byte)

	// ── 5. Process image (singleflight) ──
	resultVal, err, _ := h.processFlight.Do(cacheKey, func() (any, error) {
		processCtx, span := apptracing.Tracer("vylux/image").Start(c.Request().Context(), "image.process",
			trace.WithAttributes(
				attribute.String("image.format", opts.Format.Ext()),
				attribute.Int("image.width", opts.Width),
				attribute.Int("image.height", opts.Height),
			),
		)
		defer span.End()

		result, processErr := image.Process(raw, opts)
		if processErr != nil {
			span.RecordError(processErr)
			span.SetStatus(codes.Error, processErr.Error())
		}

		_ = processCtx
		return result, processErr
	})
	if err != nil {
		status := http.StatusInternalServerError
		message := "image processing failed"
		if errors.Is(err, image.ErrDecodeImage) {
			status = http.StatusUnprocessableEntity
			message = "unprocessable source image"
		} else if errors.Is(err, image.ErrAnimatedToStatic) {
			status = http.StatusUnprocessableEntity
			message = err.Error()
		}
		appmetrics.ObserveImageError("process", status)
		appmetrics.ObserveImageResult("error")
		slog.Warn("image processing failed", apptracing.LogFields(c.Request().Context(), "key", sourceKey, "opts", optsRaw, "status", status, "error", err)...)
		return echo.NewHTTPError(status, message)
	}
	result := resultVal.([]byte)

	// ── 6. Write caches ──
	// Synchronous: memory LRU
	h.cache.Set(cacheKey, result)
	h.trackCacheEntry(c.Request().Context(), sourceKey, cacheKey, storageCacheKey)

	// Asynchronous: S3 write-back
	go func() {
		ct := opts.Format.String()
		writeCtx := apptracing.BackgroundContext(c.Request().Context())
		if putErr := h.mediaStore.Put(writeCtx, h.mediaBucket, storageCacheKey, bytes.NewReader(result), ct); putErr != nil {
			slog.Warn("S3 cache write-back failed", apptracing.LogFields(writeCtx, "key", storageCacheKey, "error", putErr)...)
		}
	}()

	// ── 7. Respond ──
	appmetrics.ObserveImageResult("processed")
	return h.sendImage(c, result, opts.Format)
}

// sendImage writes image bytes as the response with CDN-friendly cache headers.
func (h *ImageHandler) sendImage(c *echo.Context, data []byte, f image.Format) error {
	resp := c.Response()
	resp.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	resp.Header().Set("Vary", "Accept")

	// ETag based on content hash (lightweight — data is already in memory).
	etag := `"` + shortHash(data) + `"`
	resp.Header().Set("ETag", etag)

	if match := c.Request().Header.Get("If-None-Match"); match == etag {
		return c.NoContent(http.StatusNotModified)
	}

	return c.Blob(http.StatusOK, f.String(), data)
}

// fetchFromStorage reads an entire object into memory.
func (h *ImageHandler) fetchFromStorage(ctx context.Context, store storage.Storage, bucket, key string) ([]byte, error) {
	rc, err := store.Get(ctx, bucket, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	return io.ReadAll(rc)
}

// processingHash produces a deterministic hex string that uniquely identifies
// the (source, options) combination. Used as the LRU key and S3 cache path.
func processingHash(source string, opts image.Options) string {
	h := sha256.New()
	h.Write([]byte(source))
	h.Write([]byte(fmt.Sprintf("/w%d_h%d_q%d.%s", opts.Width, opts.Height, opts.EffectiveQuality(), opts.Format.Ext())))
	return hex.EncodeToString(h.Sum(nil))
}

func s3CacheKey(format image.Format, cacheKey string) string {
	return "cache/" + cacheKey + "." + format.Ext()
}

var contentHashPattern = regexp.MustCompile(`(?i)^(?:sha256:)?([a-f0-9]{64})$`)

func extractContentHash(source string) string {
	for _, segment := range strings.Split(source, "/") {
		match := contentHashPattern.FindStringSubmatch(segment)
		if len(match) == 2 {
			return strings.ToLower(match[1])
		}
	}
	return ""
}

func (h *ImageHandler) trackCacheEntry(ctx context.Context, sourceKey, cacheKey, storageKey string) {
	if h.queries == nil {
		return
	}
	hash := extractContentHash(sourceKey)
	if hash == "" {
		return
	}
	if err := h.queries.UpsertImageCacheEntry(ctx, dbq.UpsertImageCacheEntryParams{
		Hash:       hash,
		CacheKey:   cacheKey,
		StorageKey: storageKey,
	}); err != nil {
		slog.Warn("track image cache entry failed", apptracing.LogFields(ctx, "hash", hash, "storage_key", storageKey, "error", err)...)
	}
}

// shortHash returns the first 16 hex chars of a SHA-256 over data (for ETags).
func shortHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}
