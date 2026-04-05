package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestMiddlewareSetsTraceIDHeader(t *testing.T) {
	shutdown, err := Init(context.Background(), Config{ServiceName: "vylux-test", ServiceVersion: "test"})
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	defer func() {
		if shutdownErr := shutdown(context.Background()); shutdownErr != nil {
			t.Fatalf("shutdown tracer provider: %v", shutdownErr)
		}
	}()

	e := echo.New()
	e.Use(Middleware())
	e.GET("/healthz", func(c *echo.Context) error {
		return c.String(http.StatusOK, "OK")
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()
	e.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	if traceID := resp.Header().Get(HeaderTraceID); traceID == "" {
		t.Fatal("expected X-Trace-ID response header")
	}
}

func TestMiddlewarePreservesIncomingTraceparent(t *testing.T) {
	shutdown, err := Init(context.Background(), Config{ServiceName: "vylux-test", ServiceVersion: "test"})
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	defer func() {
		if shutdownErr := shutdown(context.Background()); shutdownErr != nil {
			t.Fatalf("shutdown tracer provider: %v", shutdownErr)
		}
	}()

	parentCtx, span := Tracer("test").Start(context.Background(), "parent")
	carrier := CaptureCarrier(parentCtx)
	parentTraceID := TraceID(parentCtx)
	span.End()

	e := echo.New()
	e.Use(Middleware())
	e.GET("/healthz", func(c *echo.Context) error {
		if got := TraceID(c.Request().Context()); got != parentTraceID {
			return c.String(http.StatusInternalServerError, got)
		}
		return c.String(http.StatusOK, "OK")
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("traceparent", carrier.Traceparent)
	if carrier.Tracestate != "" {
		req.Header.Set("tracestate", carrier.Tracestate)
	}
	resp := httptest.NewRecorder()
	e.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	if got := resp.Header().Get(HeaderTraceID); got != parentTraceID {
		t.Fatalf("response trace ID = %q, want %q", got, parentTraceID)
	}
}

func TestMiddlewareIgnoresIncomingCustomTraceIDHeader(t *testing.T) {
	shutdown, err := Init(context.Background(), Config{ServiceName: "vylux-test", ServiceVersion: "test"})
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	defer func() {
		if shutdownErr := shutdown(context.Background()); shutdownErr != nil {
			t.Fatalf("shutdown tracer provider: %v", shutdownErr)
		}
	}()

	const customTraceID = "0123456789abcdef0123456789abcdef"

	e := echo.New()
	e.Use(Middleware())
	e.GET("/healthz", func(c *echo.Context) error {
		if got := TraceID(c.Request().Context()); got == customTraceID {
			return c.String(http.StatusInternalServerError, got)
		}
		return c.String(http.StatusOK, "OK")
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set(HeaderTraceID, customTraceID)
	resp := httptest.NewRecorder()
	e.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	if got := resp.Header().Get(HeaderTraceID); got == customTraceID {
		t.Fatalf("custom X-Trace-ID should not control trace context, got %q", got)
	}
}
