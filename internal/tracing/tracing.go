package tracing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	HeaderTraceID = "X-Trace-ID"

	carrierTraceparent = "traceparent"
	carrierTracestate  = "tracestate"
	defaultServiceName = "vylux"
)

// Config controls tracing initialization.
type Config struct {
	Endpoint       string
	ServiceName    string
	ServiceVersion string
}

// TraceCarrier serializes trace propagation data into queue payloads.
type TraceCarrier struct {
	Traceparent string `json:"traceparent,omitempty"`
	Tracestate  string `json:"tracestate,omitempty"`
}

// Init configures the global OpenTelemetry tracer provider and propagators.
// When Endpoint is empty, spans are still created locally but are not exported.
func Init(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = defaultServiceName
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			attribute.String("service.name", serviceName),
			attribute.String("service.version", cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("build trace resource: %w", err)
	}

	providerOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
	}

	if cfg.Endpoint != "" {
		exporterOpts, err := exporterOptions(cfg.Endpoint)
		if err != nil {
			return nil, err
		}

		exporter, err := otlptracehttp.New(ctx, exporterOpts...)
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}

		providerOpts = append(providerOpts, sdktrace.WithBatcher(exporter))
	}

	tp := sdktrace.NewTracerProvider(providerOpts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// Tracer returns a named tracer from the global provider.
func Tracer(name string) trace.Tracer {
	if name == "" {
		name = defaultServiceName
	}

	return otel.Tracer(name)
}

// TraceID returns the current trace ID as a lowercase hex string.
func TraceID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}

	return sc.TraceID().String()
}

// LogFields prepends trace_id when one is present in the context.
func LogFields(ctx context.Context, kv ...any) []any {
	traceID := TraceID(ctx)
	if traceID == "" {
		return kv
	}

	fields := make([]any, 0, len(kv)+2)
	fields = append(fields, "trace_id", traceID)
	fields = append(fields, kv...)
	return fields
}

// CaptureCarrier injects the current trace propagation fields into a serializable carrier.
func CaptureCarrier(ctx context.Context) TraceCarrier {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)

	return TraceCarrier{
		Traceparent: carrier[carrierTraceparent],
		Tracestate:  carrier[carrierTracestate],
	}
}

// ContextWithCarrier extracts propagation fields from a carrier into the provided context.
func ContextWithCarrier(ctx context.Context, carrier TraceCarrier) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if carrier.Traceparent == "" && carrier.Tracestate == "" {
		return ctx
	}

	textMapCarrier := propagation.MapCarrier{}
	if carrier.Traceparent != "" {
		textMapCarrier[carrierTraceparent] = carrier.Traceparent
	}
	if carrier.Tracestate != "" {
		textMapCarrier[carrierTracestate] = carrier.Tracestate
	}

	return otel.GetTextMapPropagator().Extract(ctx, textMapCarrier)
}

// BackgroundContext detaches cancellation while preserving trace propagation data.
func BackgroundContext(parent context.Context) context.Context {
	return ContextWithCarrier(context.Background(), CaptureCarrier(parent))
}

// CarrierFromJSON extracts trace propagation fields from a JSON payload.
func CarrierFromJSON(payload []byte) TraceCarrier {
	var carrier TraceCarrier
	_ = json.Unmarshal(payload, &carrier)
	return carrier
}

func exporterOptions(rawEndpoint string) ([]otlptracehttp.Option, error) {
	if !strings.Contains(rawEndpoint, "://") {
		return []otlptracehttp.Option{otlptracehttp.WithEndpoint(rawEndpoint)}, nil
	}

	u, err := url.Parse(rawEndpoint)
	if err != nil {
		return nil, fmt.Errorf("parse OTEL_EXPORTER_OTLP_ENDPOINT: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT must include a host")
	}

	opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(u.Host)}
	switch strings.ToLower(u.Scheme) {
	case "http":
		opts = append(opts, otlptracehttp.WithInsecure())
	case "https":
		// HTTPS is the default.
	default:
		return nil, fmt.Errorf("unsupported OTEL_EXPORTER_OTLP_ENDPOINT scheme: %q", u.Scheme)
	}

	if u.Path != "" && u.Path != "/" {
		opts = append(opts, otlptracehttp.WithURLPath(u.Path))
	}

	return opts, nil
}
