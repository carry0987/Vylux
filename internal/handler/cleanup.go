package handler

import (
	"net/http"

	"Vylux/internal/cache"
	appcleanup "Vylux/internal/cleanup"
	"Vylux/internal/db/dbq"
	"Vylux/internal/storage"

	"github.com/hibiken/asynq"
	"github.com/labstack/echo/v5"
)

// CleanupHandler handles DELETE /api/media/:hash.
//
// It performs best-effort cleanup of all resources associated with a content hash:
//   - Cancel in-flight asynq tasks
//   - Delete S3 derived files (images + videos)
//   - Delete encryption key (DB)
//   - Delete job records (DB)
//
// Always returns 204 No Content (idempotent).
type CleanupHandler struct {
	cleaner *appcleanup.Cleaner
}

// NewCleanupHandler creates a CleanupHandler.
func NewCleanupHandler(
	store storage.Storage,
	lru *cache.LRU,
	queries *dbq.Queries,
	inspector *asynq.Inspector,
	mediaBucket string,
) *CleanupHandler {
	return &CleanupHandler{
		cleaner: appcleanup.NewCleaner(store, lru, queries, inspector, mediaBucket),
	}
}

// Handle processes DELETE /api/media/:hash.
func (h *CleanupHandler) Handle(c *echo.Context) error {
	hash := c.Param("hash")
	if hash == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing hash")
	}

	h.cleaner.Cleanup(c.Request().Context(), hash)

	return c.NoContent(http.StatusNoContent)
}
