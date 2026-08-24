package handler

import (
	"fmt"
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
// It attempts cleanup of all resources associated with a content hash:
//   - Cancel in-flight asynq tasks
//   - Delete S3 derived files (images + videos)
//   - Delete encryption key (DB)
//   - Delete job records (DB)
//
// Returns 204 when media is confirmed gone. Returns 503 when cleanup is incomplete
// and the caller should retry.
type CleanupHandler struct {
	cleaner *appcleanup.Cleaner
}

type cleanupFailureResponse struct {
	Message         string   `json:"message"`
	Retryable       bool     `json:"retryable"`
	CompletedStages []string `json:"completed_stages,omitempty"`
	FailedStages    []string `json:"failed_stages,omitempty"`
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

	result := h.cleaner.Cleanup(c.Request().Context(), hash)
	if !result.ConfirmedGone() {
		return c.JSON(http.StatusServiceUnavailable, cleanupFailureResponse{
			Message:         fmt.Sprintf("cleanup incomplete for %s", hash),
			Retryable:       true,
			CompletedStages: result.CompletedStages(),
			FailedStages:    result.FailedStages(),
		})
	}

	return c.NoContent(http.StatusNoContent)
}
