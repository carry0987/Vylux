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
	"strings"
	"time"

	"Vylux/internal/cache"
	"Vylux/internal/db/dbq"
	"Vylux/internal/image"
	"Vylux/internal/lifecycle"
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

const imageSourceFetchTimeout = 30 * time.Second

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
	coordinator  lifecycle.HashCoordinator

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
	coordinators ...lifecycle.HashCoordinator,
) *ImageHandler {
	handler := &ImageHandler{
		sourceStore:  sourceStore,
		mediaStore:   mediaStore,
		cache:        lru,
		queries:      queries,
		sourceBucket: sourceBucket,
		mediaBucket:  mediaBucket,
		hmacSecret:   hmacSecret,
	}
	if len(coordinators) > 0 {
		handler.coordinator = coordinators[0]
	}
	return handler
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

	hash, ok := lifecycle.ExtractHash(sourceKey)
	if !ok {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "source path does not contain an attributable content hash")
	}
	if h.coordinator == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "image lifecycle coordinator is unavailable")
	}

	return h.coordinator.WithHashLock(c.Request().Context(), hash, func(queries *dbq.Queries) error {
		if err := lifecycle.RejectTombstoned(c.Request().Context(), queries, hash, sourceKey); err != nil {
			if errors.Is(err, lifecycle.ErrTombstoned) {
				return echo.NewHTTPError(http.StatusGone, "source image was permanently deleted").Wrap(err)
			}
			return echo.NewHTTPError(http.StatusServiceUnavailable, "image lifecycle lookup failed").Wrap(err)
		}

		return h.handleLocked(c, queries, hash, sourceKey, optsRaw, opts)
	})
}

func (h *ImageHandler) handleLocked(
	c *echo.Context,
	queries *dbq.Queries,
	hash string,
	sourceKey string,
	optsRaw string,
	opts image.Options,
) error {
	// ── 3. Cache lookup ──
	processingKey := processingHash(sourceKey, opts)
	cacheKey := lifecycle.CacheMemoryKey(hash, processingKey)
	storageCacheKey := lifecycle.CacheStorageKey(hash, processingKey, opts.Format.Ext())

	// 3a. Memory LRU
	if data, ok := h.cache.Get(cacheKey); ok {
		if err := h.trackCacheEntry(c.Request().Context(), queries, hash, cacheKey, storageCacheKey); err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "cache index unavailable").Wrap(err)
		}
		appmetrics.ObserveImageCache("memory", "hit")
		appmetrics.ObserveImageResult("memory_hit")
		return h.sendImage(c, data, opts.Format)
	}
	appmetrics.ObserveImageCache("memory", "miss")

	// 3b. S3 cache
	if data, err := h.fetchFromStorage(c.Request().Context(), h.mediaStore, h.mediaBucket, storageCacheKey); err == nil {
		if err := h.trackCacheEntry(c.Request().Context(), queries, hash, cacheKey, storageCacheKey); err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "cache index unavailable").Wrap(err)
		}
		appmetrics.ObserveImageCache("storage", "hit")
		appmetrics.ObserveImageResult("storage_hit")
		// Populate LRU for subsequent in-instance hits.
		h.cache.Set(cacheKey, data)
		return h.sendImage(c, data, opts.Format)
	}
	appmetrics.ObserveImageCache("storage", "miss")

	// ── 4. Fetch original (singleflight) ──
	rawVal, err, _ := h.sourceFlight.Do(sourceKey, func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(c.Request().Context(), imageSourceFetchTimeout)
		defer cancel()
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
	resultVal, err, _ := h.processFlight.Do(processingKey, func() (any, error) {
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
	if err := h.mediaStore.Put(
		c.Request().Context(),
		h.mediaBucket,
		storageCacheKey,
		bytes.NewReader(result),
		opts.Format.String(),
	); err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, "cache storage write failed").Wrap(err)
	}
	if err := h.trackCacheEntry(c.Request().Context(), queries, hash, cacheKey, storageCacheKey); err != nil {
		deleteErr := h.mediaStore.Delete(c.Request().Context(), h.mediaBucket, storageCacheKey)
		return echo.NewHTTPError(http.StatusServiceUnavailable, "cache index write failed").Wrap(errors.Join(err, deleteErr))
	}
	h.cache.Set(cacheKey, result)

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
	_, _ = fmt.Fprintf(h, "/w%d_h%d_q%d.%s", opts.Width, opts.Height, opts.EffectiveQuality(), opts.Format.Ext())
	return hex.EncodeToString(h.Sum(nil))
}

func (h *ImageHandler) trackCacheEntry(ctx context.Context, queries *dbq.Queries, hash, cacheKey, storageKey string) error {
	if queries == nil {
		return fmt.Errorf("cache index database is unavailable")
	}
	if err := queries.UpsertImageCacheEntry(ctx, dbq.UpsertImageCacheEntryParams{
		Hash:       hash,
		CacheKey:   cacheKey,
		StorageKey: storageKey,
	}); err != nil {
		return fmt.Errorf("track image cache entry: %w", err)
	}
	return nil
}

// shortHash returns the first 16 hex chars of a SHA-256 over data (for ETags).
func shortHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}
