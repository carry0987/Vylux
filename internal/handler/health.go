package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	appmetrics "Vylux/internal/metrics"
	"Vylux/internal/storage"

	"github.com/labstack/echo/v5"
)

type healthResponse struct {
	Status string `json:"status"`
}

type readinessCheckResponse struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type readinessFailureResponse struct {
	Status string                   `json:"status"`
	Error  string                   `json:"error"`
	Checks []readinessCheckResponse `json:"checks"`
}

// Healthz is a liveness probe — always returns 200 if the process is alive.
func Healthz(c *echo.Context) error {
	return c.JSON(http.StatusOK, healthResponse{Status: "ok"})
}

// ReadyzHandler checks whether all critical dependencies are ready to serve traffic.
type ReadyzHandler struct {
	sourceStore  storage.Storage
	mediaStore   storage.Storage
	sourceBucket string
	mediaBucket  string
	dbPing       func(context.Context) error
	redisPing    func(context.Context) error
}

// NewReadyzHandler creates a readiness probe handler.
func NewReadyzHandler(
	sourceStore storage.Storage,
	mediaStore storage.Storage,
	sourceBucket, mediaBucket string,
	dbPing func(context.Context) error,
	redisPing func(context.Context) error,
) *ReadyzHandler {
	return &ReadyzHandler{
		sourceStore:  sourceStore,
		mediaStore:   mediaStore,
		sourceBucket: sourceBucket,
		mediaBucket:  mediaBucket,
		dbPing:       dbPing,
		redisPing:    redisPing,
	}
}

// Handle serves GET /readyz.
func (h *ReadyzHandler) Handle(c *echo.Context) error {
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
			return h.mediaStore.HeadBucket(ctx, h.mediaBucket)
		}},
	}

	results := make([]readinessCheckResponse, 0, len(checks))
	ready := true

	for _, check := range checks {
		if check.run == nil {
			appmetrics.ObserveReadinessFailure(check.name)
			ready = false
			results = append(results, readinessCheckResponse{
				Name:   check.name,
				Status: "failed",
				Error:  "probe unavailable",
			})
			continue
		}

		if err := check.run(ctx); err != nil {
			appmetrics.ObserveReadinessFailure(check.name)
			ready = false
			results = append(results, readinessCheckResponse{
				Name:   check.name,
				Status: "failed",
				Error:  err.Error(),
			})
			continue
		}

		results = append(results, readinessCheckResponse{
			Name:   check.name,
			Status: "ok",
		})
	}

	if !ready {
		return c.JSON(http.StatusServiceUnavailable, readinessFailureResponse{
			Status: "not_ready",
			Error:  "readiness checks failed",
			Checks: results,
		})
	}

	return c.JSON(http.StatusOK, healthResponse{Status: "ok"})
}
