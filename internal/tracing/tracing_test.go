package tracing

import (
	"context"
	"testing"
)

func TestCarrierRoundTrip(t *testing.T) {
	shutdown, err := Init(context.Background(), Config{ServiceName: "vylux-test", ServiceVersion: "test"})
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	defer func() {
		if shutdownErr := shutdown(context.Background()); shutdownErr != nil {
			t.Fatalf("shutdown tracer provider: %v", shutdownErr)
		}
	}()

	ctx, span := Tracer("test").Start(context.Background(), "root")
	defer span.End()

	carrier := CaptureCarrier(ctx)
	if carrier.Traceparent == "" {
		t.Fatal("expected traceparent to be captured")
	}

	restored := ContextWithCarrier(context.Background(), carrier)
	if got, want := TraceID(restored), TraceID(ctx); got != want {
		t.Fatalf("TraceID(restored) = %q, want %q", got, want)
	}

	jsonCarrier := CarrierFromJSON([]byte(`{"traceparent":"` + carrier.Traceparent + `","tracestate":"` + carrier.Tracestate + `"}`))
	if jsonCarrier.Traceparent != carrier.Traceparent {
		t.Fatalf("CarrierFromJSON traceparent = %q, want %q", jsonCarrier.Traceparent, carrier.Traceparent)
	}
}
