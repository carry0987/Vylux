package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"Vylux/internal/cache"
	appcleanup "Vylux/internal/cleanup"
	"Vylux/internal/db/dbq"
	"Vylux/internal/deployment"
	"Vylux/internal/lifecycle"
	"Vylux/internal/storage"

	"github.com/hibiken/asynq"
	"github.com/labstack/echo/v5"
)

const cleanupConfirmedHeader = "X-Vylux-Cleanup-Confirmed"

type CleanupHandler struct {
	cleaner mediaCleaner
	target  deployment.Target
}

type mediaCleaner interface {
	Cleanup(ctx context.Context, hash string) error
	StrictCleanup(ctx context.Context, hash, source string) error
}

type strictCleanupRequest struct {
	Source                string `json:"source"`
	ProtocolVersion       int16  `json:"protocol_version"`
	DeploymentID          string `json:"deployment_id"`
	SourceBackendIdentity string `json:"source_backend_identity"`
	MediaBackendIdentity  string `json:"media_backend_identity"`
}

// NewCleanupHandler creates a CleanupHandler.
func NewCleanupHandler(
	store storage.Storage,
	lru *cache.LRU,
	queries *dbq.Queries,
	inspector *asynq.Inspector,
	mediaBucket string,
	target deployment.Target,
	coordinators ...lifecycle.HashCoordinator,
) *CleanupHandler {
	return &CleanupHandler{
		cleaner: appcleanup.NewCleaner(store, lru, queries, inspector, mediaBucket, coordinators...),
		target:  target,
	}
}

// Handle processes DELETE /api/media/:hash.
func (h *CleanupHandler) Handle(c *echo.Context) error {
	hash := c.Param("hash")
	if hash == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing hash")
	}

	// The old administrator purge remains reusable and intentionally carries no
	// durable-GC confirmation header.
	if err := h.cleaner.Cleanup(c.Request().Context(), hash); err != nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "media cleanup incomplete").Wrap(err)
	}

	return c.NoContent(http.StatusNoContent)
}

// HandleStrict processes a protocol-v2, target-fenced DELETE /api/media/:hash/strict request.
func (h *CleanupHandler) HandleStrict(c *echo.Context) error {
	h.target.SetHeaders(c.Response().Header())
	hash := c.Param("hash")
	if hash == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing hash")
	}

	var req strictCleanupRequest
	decoder := json.NewDecoder(c.Request().Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid strict cleanup body").Wrap(err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid strict cleanup body").Wrap(err)
	}
	if strings.TrimSpace(req.Source) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "source is required")
	}

	expected := deployment.Target{
		ProtocolVersion:       req.ProtocolVersion,
		DeploymentID:          req.DeploymentID,
		SourceBackendIdentity: req.SourceBackendIdentity,
		MediaBackendIdentity:  req.MediaBackendIdentity,
	}
	if err := expected.Validate(); err != nil {
		return echo.NewHTTPError(
			http.StatusBadRequest,
			"invalid deployment target precondition",
		).Wrap(err)
	}
	if err := h.target.Require(expected); err != nil {
		if errors.Is(err, deployment.ErrTargetMismatch) {
			return echo.NewHTTPError(
				http.StatusPreconditionFailed,
				"deployment target precondition failed",
			).Wrap(err)
		}
		return echo.NewHTTPError(
			http.StatusServiceUnavailable,
			"deployment target is unavailable",
		).Wrap(err)
	}

	if err := h.cleaner.StrictCleanup(c.Request().Context(), hash, req.Source); err != nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "strict media cleanup incomplete").Wrap(err)
	}

	c.Response().Header().Set(cleanupConfirmedHeader, "1")
	return c.NoContent(http.StatusNoContent)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("multiple JSON values")
	}
	return err
}
