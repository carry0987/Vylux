package storage

import (
	"context"
	"io"
	"time"

	appmetrics "Vylux/internal/metrics"
	apptracing "Vylux/internal/tracing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type instrumentedStorage struct {
	inner  Storage
	role   string
	driver string
}

// WithInstrumentation wraps a storage backend with role-aware tracing and metrics.
func WithInstrumentation(inner Storage, role, driver string) Storage {
	if inner == nil {
		return nil
	}

	return &instrumentedStorage{inner: inner, role: role, driver: driver}
}

func (s *instrumentedStorage) Get(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	var rc io.ReadCloser
	err := s.observe(ctx, "get", bucket, key, "", func(ctx context.Context, span trace.Span) error {
		var innerErr error
		rc, innerErr = s.inner.Get(ctx, bucket, key)
		return innerErr
	})
	return rc, err
}

func (s *instrumentedStorage) Put(ctx context.Context, bucket, key string, data io.Reader, contentType string) error {
	return s.observe(ctx, "put", bucket, key, contentType, func(ctx context.Context, span trace.Span) error {
		return s.inner.Put(ctx, bucket, key, data, contentType)
	})
}

func (s *instrumentedStorage) Exists(ctx context.Context, bucket, key string) (bool, error) {
	var exists bool
	err := s.observe(ctx, "exists", bucket, key, "", func(ctx context.Context, span trace.Span) error {
		var innerErr error
		exists, innerErr = s.inner.Exists(ctx, bucket, key)
		span.SetAttributes(attribute.Bool("storage.exists", exists))
		return innerErr
	})
	return exists, err
}

func (s *instrumentedStorage) Size(ctx context.Context, bucket, key string) (int64, error) {
	var size int64
	err := s.observe(ctx, "size", bucket, key, "", func(ctx context.Context, span trace.Span) error {
		var innerErr error
		size, innerErr = s.inner.Size(ctx, bucket, key)
		if innerErr == nil {
			span.SetAttributes(attribute.Int64("storage.size", size))
		}
		return innerErr
	})
	return size, err
}

func (s *instrumentedStorage) Delete(ctx context.Context, bucket, key string) error {
	return s.observe(ctx, "delete", bucket, key, "", func(ctx context.Context, span trace.Span) error {
		return s.inner.Delete(ctx, bucket, key)
	})
}

func (s *instrumentedStorage) List(ctx context.Context, bucket, prefix string) ([]string, error) {
	var keys []string
	err := s.observe(ctx, "list", bucket, prefix, "", func(ctx context.Context, span trace.Span) error {
		var innerErr error
		keys, innerErr = s.inner.List(ctx, bucket, prefix)
		if innerErr == nil {
			span.SetAttributes(attribute.Int("storage.objects", len(keys)))
		}
		return innerErr
	})
	return keys, err
}

func (s *instrumentedStorage) HeadBucket(ctx context.Context, bucket string) error {
	return s.observe(ctx, "head_bucket", bucket, "", "", func(ctx context.Context, span trace.Span) error {
		return s.inner.HeadBucket(ctx, bucket)
	})
}

func (s *instrumentedStorage) observe(ctx context.Context, operation, bucket, key, contentType string, run func(context.Context, trace.Span) error) error {
	started := time.Now()
	ctx, span := apptracing.Tracer("vylux/storage").Start(ctx, "storage."+operation,
		trace.WithAttributes(s.baseAttributes(operation, bucket, key, contentType)...),
	)
	defer span.End()

	err := run(ctx, span)
	result := "ok"
	if err != nil {
		result = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	appmetrics.ObserveStorageOperation(s.role, s.driver, operation, result, time.Since(started))
	span.SetAttributes(attribute.String("storage.result", result))

	return err
}

func (s *instrumentedStorage) baseAttributes(operation, bucket, key, contentType string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("storage.role", s.role),
		attribute.String("storage.driver", s.driver),
		attribute.String("storage.operation", operation),
		attribute.String("storage.bucket", bucket),
	}
	if key != "" {
		attrs = append(attrs, attribute.String("storage.key", key))
	}
	if contentType != "" {
		attrs = append(attrs, attribute.String("storage.content_type", contentType))
	}

	return attrs
}
