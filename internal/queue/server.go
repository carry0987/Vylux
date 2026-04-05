package queue

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"Vylux/internal/config"
	appmetrics "Vylux/internal/metrics"
	apptracing "Vylux/internal/tracing"

	"github.com/hibiken/asynq"
	redis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Server wraps asynq.Server to manage the worker lifecycle.
type Server struct {
	normal      *asynq.Server
	large       *asynq.Server
	mux         *asynq.ServeMux
	concurrency int
	largeJobs   int
}

// NewServer creates a new asynq worker server.
// Task handlers must be registered via Handle() before calling Run().
func NewServer(cfg *config.Config) (*Server, error) {
	opt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}

	errorHandler := asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
		retried, _ := asynq.GetRetryCount(ctx)
		maxRetry, _ := asynq.GetMaxRetry(ctx)

		slog.Error("task failed",
			apptracing.LogFields(ctx,
				"type", task.Type(),
				"retry", fmt.Sprintf("%d/%d", retried, maxRetry),
				"error", err,
			)...,
		)
	})

	srv := asynq.NewServer(opt, asynq.Config{
		Concurrency: cfg.WorkerConcurrency,
		Queues: map[string]int{
			QueueCritical: 6, // image thumbnails — fast, highest priority
			QueueDefault:  3, // normal video tasks
		},
		ErrorHandler: errorHandler,
	})

	largeSrv := asynq.NewServer(opt, asynq.Config{
		Concurrency: cfg.LargeWorkerConcurrency,
		Queues: map[string]int{
			QueueVideoLarge: 1, // large file transcoding — dedicated low-concurrency pool
		},
		ErrorHandler: errorHandler,
	})

	return &Server{
		normal:      srv,
		large:       largeSrv,
		mux:         asynq.NewServeMux(),
		concurrency: cfg.WorkerConcurrency,
		largeJobs:   cfg.LargeWorkerConcurrency,
	}, nil
}

// Handle registers a handler for the given task type.
func (s *Server) Handle(taskType string, handler asynq.Handler) {
	s.mux.Handle(taskType, instrumentHandler(taskType, handler))
}

// HandleFunc registers a handler function for the given task type.
func (s *Server) HandleFunc(taskType string, handler func(context.Context, *asynq.Task) error) {
	s.Handle(taskType, asynq.HandlerFunc(handler))
}

// Run starts the worker server and blocks until ctx is cancelled.
// After ctx cancellation it performs a graceful shutdown.
func (s *Server) Run(ctx context.Context) error {
	// Start the asynq server in a goroutine.
	errCh := make(chan error, 2)

	go func() {
		if err := s.normal.Run(s.mux); err != nil {
			errCh <- fmt.Errorf("asynq normal server: %w", err)
		}
	}()

	go func() {
		if err := s.large.Run(s.mux); err != nil {
			errCh <- fmt.Errorf("asynq large server: %w", err)
		}
	}()

	slog.Info("asynq worker started",
		"normal_concurrency", s.concurrency,
		"large_concurrency", s.largeJobs,
	)

	// Wait for context cancellation or server error.
	select {
	case <-ctx.Done():
		slog.Info("shutting down asynq worker...")
		s.normal.Shutdown()
		s.large.Shutdown()

		return nil
	case err := <-errCh:
		return err
	}
}

// Inspector returns an asynq.Inspector for the same Redis instance,
// useful for querying task status or cancelling tasks.
func NewInspector(redisURL string) (*asynq.Inspector, error) {
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}

	return asynq.NewInspector(opt), nil
}

// PingRedis verifies that Redis is reachable using the configured connection URL.
func PingRedis(ctx context.Context, redisURL string) error {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("parse redis URL: %w", err)
	}

	client := redis.NewClient(opt)
	defer client.Close()

	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}

	return nil
}

func instrumentHandler(taskType string, handler asynq.Handler) asynq.Handler {
	return asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
		ctx = apptracing.ContextWithCarrier(ctx, apptracing.CarrierFromJSON(task.Payload()))
		taskID, _ := asynq.GetTaskID(ctx)
		ctx, span := apptracing.Tracer("vylux/worker").Start(ctx, taskType,
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(
				attribute.String("job.type", taskType),
				attribute.String("job.id", taskID),
			),
		)
		started := time.Now()
		defer span.End()

		err := handler.ProcessTask(ctx, task)
		result := "success"
		if err != nil {
			result = "error"
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}

		span.SetAttributes(attribute.String("job.result", result))
		appmetrics.ObserveWorkerTask(taskType, result, time.Since(started))
		return err
	})
}
