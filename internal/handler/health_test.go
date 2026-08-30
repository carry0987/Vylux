package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestReadyzReturnsDetailedJSONFailures(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	resp := httptest.NewRecorder()

	h := NewReadyzHandler(
		nil,
		nil,
		"source",
		"media",
		func(_ context.Context) error { return nil },
		func(_ context.Context) error { return fmt.Errorf("redis unavailable") },
	)

	e.GET("/readyz", h.Handle)
	e.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusServiceUnavailable)
	}

	var body readinessFailureResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "not_ready" {
		t.Fatalf("expected status not_ready, got %#v", body)
	}
	if body.Error != "readiness checks failed" {
		t.Fatalf("expected summary error, got %#v", body)
	}
	if len(body.Checks) != 4 {
		t.Fatalf("expected 4 checks, got %#v", body.Checks)
	}
	if body.Checks[0].Name != "postgres" || body.Checks[0].Status != "ok" {
		t.Fatalf("unexpected postgres check: %#v", body.Checks[0])
	}
	if body.Checks[1].Name != "redis" || body.Checks[1].Status != "failed" || body.Checks[1].Error != "redis unavailable" {
		t.Fatalf("unexpected redis check: %#v", body.Checks[1])
	}
	if body.Checks[2].Name != "source bucket" || body.Checks[2].Status != "failed" || body.Checks[2].Error != "source storage is not configured" {
		t.Fatalf("unexpected source check: %#v", body.Checks[2])
	}
	if body.Checks[3].Name != "media bucket" || body.Checks[3].Status != "failed" || body.Checks[3].Error != "media storage is not configured" {
		t.Fatalf("unexpected media check: %#v", body.Checks[3])
	}
}
