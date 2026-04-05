package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	apptracing "Vylux/internal/tracing"
)

func TestSendInjectsTraceHeaders(t *testing.T) {
	shutdown, err := apptracing.Init(context.Background(), apptracing.Config{ServiceName: "vylux-test", ServiceVersion: "test"})
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	defer func() {
		if shutdownErr := shutdown(context.Background()); shutdownErr != nil {
			t.Fatalf("shutdown tracer provider: %v", shutdownErr)
		}
	}()

	ctx, span := apptracing.Tracer("test").Start(context.Background(), "webhook")
	defer span.End()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(apptracing.HeaderTraceID); got == "" {
			t.Fatal("expected X-Trace-ID header")
		} else if want := apptracing.TraceID(ctx); got != want {
			t.Fatalf("X-Trace-ID = %q, want %q", got, want)
		}

		if got := r.Header.Get("traceparent"); got == "" {
			t.Fatal("expected traceparent header")
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient("secret", nil)
	if err := client.send(ctx, server.URL, []byte(`{}`), "sig"); err != nil {
		t.Fatalf("send returned error: %v", err)
	}
}
