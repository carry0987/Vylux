package tracing

import (
	"crypto/rand"
	"fmt"

	"go.opentelemetry.io/otel/trace"
)

func newSpanID() (trace.SpanID, error) {
	var spanID trace.SpanID
	if _, err := rand.Read(spanID[:]); err != nil {
		return trace.SpanID{}, fmt.Errorf("generate span id: %w", err)
	}
	if !spanID.IsValid() {
		return newSpanID()
	}

	return spanID, nil
}
