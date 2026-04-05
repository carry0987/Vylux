package tracing

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Middleware extracts trace context from incoming requests, starts a server span,
// and exposes the resulting trace ID via X-Trace-ID.
func Middleware() echo.MiddlewareFunc {
	tracer := Tracer("vylux/http")

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			route := c.Path()
			if route == "" {
				route = req.URL.Path
			}

			ctx := extractRequestContext(req)
			ctx, span := tracer.Start(ctx, route,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.method", req.Method),
					attribute.String("http.route", route),
				),
			)
			defer span.End()

			if traceID := TraceID(ctx); traceID != "" {
				c.Response().Header().Set(HeaderTraceID, traceID)
			}

			c.SetRequest(req.WithContext(ctx))
			err := next(c)

			_, statusCode := echo.ResolveResponseStatus(c.Response(), err)
			if statusCode == 0 {
				statusCode = http.StatusOK
			}
			span.SetAttributes(attribute.Int("http.status_code", statusCode))

			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			} else {
				span.SetStatus(codes.Unset, "")
			}

			return err
		}
	}
}

func extractRequestContext(req *http.Request) context.Context {
	ctx := req.Context()
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(req.Header))
	return ctx
}
