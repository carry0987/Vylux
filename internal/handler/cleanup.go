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
// It cleans up all resources associated with a content hash:
//   - Cancel in-flight asynq tasks
//   - Delete S3 derived files (images + videos)
//   - Delete encryption key (DB)
//   - Delete job records (DB)
//
// Returns 204 No Content once every resource is gone, and 500 when any of them
// could not be removed. Cleanup is idempotent, so a caller can retry a failure
// until it succeeds.
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

	if err := h.cleaner.Cleanup(c.Request().Context(), hash); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "media cleanup incomplete")
	}

	return c.NoContent(http.StatusNoContent)
}
