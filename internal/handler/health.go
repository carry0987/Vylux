package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"Vylux/internal/deployment"
	"Vylux/internal/lifecycle"
	appmetrics "Vylux/internal/metrics"
	"Vylux/internal/storage"

	"github.com/labstack/echo/v5"
)

// Healthz is a liveness probe — always returns 200 if the process is alive.
func Healthz(c *echo.Context) error {
	return c.String(http.StatusOK, "OK")
}

// ReadyzHandler checks whether all critical dependencies are ready to serve traffic.
type ReadyzHandler struct {
	sourceStore  storage.Storage
	mediaStore   storage.Storage
	sourceBucket string
	mediaBucket  string
	dbPing       func(context.Context) error
	redisPing    func(context.Context) error
	readiness    lifecycle.StrictReadiness
	target       deployment.Target
}

// NewReadyzHandler creates a readiness probe handler.
func NewReadyzHandler(
	sourceStore storage.Storage,
	mediaStore storage.Storage,
	sourceBucket, mediaBucket string,
	dbPing func(context.Context) error,
	redisPing func(context.Context) error,
	readiness lifecycle.StrictReadiness,
	target deployment.Target,
) *ReadyzHandler {
	return &ReadyzHandler{
		sourceStore:  sourceStore,
		mediaStore:   mediaStore,
		sourceBucket: sourceBucket,
		mediaBucket:  mediaBucket,
		dbPing:       dbPing,
		redisPing:    redisPing,
		readiness:    readiness,
		target:       target,
	}
}

// Handle serves GET /readyz.
func (h *ReadyzHandler) Handle(c *echo.Context) error {
	h.target.SetHeaders(c.Response().Header())
	if err := h.target.Validate(); err != nil {
		appmetrics.ObserveReadinessFailure("deployment target")
		return c.String(
			http.StatusServiceUnavailable,
			fmt.Sprintf("not ready: deployment target: %v", err),
		)
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
	defer cancel()

	checks := []struct {
		name string
		run  func(context.Context) error
	}{
		{name: "postgres", run: h.dbPing},
		{name: "redis", run: h.redisPing},
		{name: "source bucket", run: func(ctx context.Context) error {
			if h.sourceStore == nil {
				return fmt.Errorf("source storage is not configured")
			}
			return h.sourceStore.HeadBucket(ctx, h.sourceBucket)
		}},
		{name: "media bucket", run: func(ctx context.Context) error {
			if h.mediaStore == nil {
				return fmt.Errorf("media storage is not configured")
			}
			if err := h.mediaStore.HeadBucket(ctx, h.mediaBucket); err != nil {
				return err
			}
			if err := lifecycle.CheckDeletionSemantics(ctx, h.mediaStore, h.mediaBucket); err != nil {
				return err
			}
			if h.readiness == nil {
				return fmt.Errorf("strict cleanup readiness gate is unavailable")
			}
			return h.readiness.AdvanceStrictCleanupReadiness(ctx)
		}},
	}

	for _, check := range checks {
		if check.run == nil {
			appmetrics.ObserveReadinessFailure(check.name)
			return c.String(http.StatusServiceUnavailable, fmt.Sprintf("not ready: %s probe unavailable", check.name))
		}

		if err := check.run(ctx); err != nil {
			appmetrics.ObserveReadinessFailure(check.name)
			return c.String(http.StatusServiceUnavailable, fmt.Sprintf("not ready: %s: %v", check.name, err))
		}
	}

	return c.String(http.StatusOK, "OK")
}
