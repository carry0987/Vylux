package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"Vylux/internal/cache"
	"Vylux/internal/config"
	"Vylux/internal/db/dbq"
	"Vylux/internal/encryption"
	"Vylux/internal/handler"
	appmetrics "Vylux/internal/metrics"
	"Vylux/internal/queue"
	"Vylux/internal/storage"
	apptracing "Vylux/internal/tracing"

	custommw "Vylux/internal/server/middleware"

	"github.com/hibiken/asynq"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	redis "github.com/redis/go-redis/v9"
)

// Deps holds shared dependencies that the HTTP server needs.
type Deps struct {
	SourceStore storage.Storage
	MediaStore  storage.Storage
	Cache       *cache.LRU
	QueueClient *queue.Client
	DBQueries   *dbq.Queries
	Inspector   *asynq.Inspector
	Redis       *redis.Client
	KeyWrapper  *encryption.KeyWrapper
	DBPing      func(context.Context) error
	RedisPing   func(context.Context) error
}

// Server wraps the Echo instance and configuration.
type Server struct {
	echo *echo.Echo
	cfg  *config.Config
	deps *Deps
}

// New creates and configures the Echo HTTP server.
func New(cfg *config.Config, deps *Deps) *Server {
	e := echo.New()
	appmetrics.ConfigureInspector(deps.Inspector)

	// Built-in middleware
	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	e.Use(apptracing.Middleware())
	e.Use(appmetrics.HTTPMiddleware())

	s := &Server{echo: e, cfg: cfg, deps: deps}
	s.registerRoutes()

	return s
}

// Start runs the HTTP server and blocks until ctx is canceled, then shuts down gracefully.
func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.cfg.Port)
	slog.Info("HTTP server listening", "addr", addr)

	sc := echo.StartConfig{
		Address:         addr,
		HideBanner:      true,
		HidePort:        true,
		GracefulTimeout: s.cfg.ShutdownTimeout,
	}

	return sc.Start(ctx, s.echo)
}

// Handler returns the underlying http.Handler (the Echo router).
// This is used by httptest.NewServer in integration tests.
func (s *Server) Handler() http.Handler {
	return s.echo
}

// registerRoutes sets up all route groups and handlers.
func (s *Server) registerRoutes() {
	// Health checks — no auth required
	readyzHandler := handler.NewReadyzHandler(
		s.deps.SourceStore,
		s.deps.MediaStore,
		s.cfg.SourceBucket,
		s.cfg.MediaBucket,
		s.deps.DBPing,
		s.deps.RedisPing,
	)
	s.echo.GET("/healthz", handler.Healthz)
	s.echo.GET("/readyz", readyzHandler.Handle)
	s.echo.GET("/metrics", appmetrics.Handler)

	// ── Image processing (HMAC verified in handler, CDN cache headers) ──
	imgHandler := handler.NewImageHandler(
		s.deps.SourceStore,
		s.deps.MediaStore,
		s.deps.Cache,
		s.deps.DBQueries,
		s.cfg.SourceBucket,
		s.cfg.MediaBucket,
		s.cfg.HMACSecret,
	)
	originalHandler := handler.NewOriginalHandler(
		s.deps.SourceStore,
		s.cfg.SourceBucket,
		s.cfg.HMACSecret,
	)
	s.echo.GET("/img/:sig/:opts/*", imgHandler.Handle,
		custommw.CacheHeaders(true),
	)
	s.echo.GET("/original/:sig/*", originalHandler.Handle,
		custommw.CacheHeaders(true),
	)

	// ── Thumbnail / cover serving (HMAC verified in handler, CDN cache headers) ──
	thumbHandler := handler.NewThumbHandler(
		s.deps.MediaStore,
		s.cfg.MediaBucket,
		s.cfg.HMACSecret,
	)
	s.echo.GET("/thumb/:sig/*", thumbHandler.Handle,
		custommw.CacheHeaders(true),
	)

	// ── Job API (API key protected) ──
	jobHandler := handler.NewJobHandler(
		s.deps.DBQueries,
		s.deps.QueueClient,
		s.deps.SourceStore,
		s.cfg.SourceBucket,
		s.cfg.LargeFileThreshold,
		s.cfg.MaxFileSize,
	)
	api := s.echo.Group("/api", custommw.APIKeyAuth(s.cfg.APIKey))
	api.POST("/jobs", jobHandler.Create,
		custommw.RedisRateLimit(s.deps.Redis, "jobs", 30, time.Minute, func(c *echo.Context) string {
			return custommw.HashRateLimitKey(c.Request().Header.Get("X-API-Key"))
		}),
	)
	api.GET("/jobs/:id", jobHandler.GetStatus)
	api.POST("/jobs/:id/retry", jobHandler.Retry,
		custommw.RedisRateLimit(s.deps.Redis, "jobs", 30, time.Minute, func(c *echo.Context) string {
			return custommw.HashRateLimitKey(c.Request().Header.Get("X-API-Key"))
		}),
	)

	// ── Stream (HLS proxy, CDN cache headers) ──
	streamHandler := handler.NewStreamHandler(
		s.deps.MediaStore,
		s.cfg.MediaBucket,
	)
	s.echo.GET("/stream/:hash/*", streamHandler.Handle,
		custommw.CacheHeaders(true),
	)

	// ── Cleanup ──
	cleanupHandler := handler.NewCleanupHandler(
		s.deps.MediaStore,
		s.deps.Cache,
		s.deps.DBQueries,
		s.deps.Inspector,
		s.cfg.MediaBucket,
	)
	api.DELETE("/media/:hash", cleanupHandler.Handle)

	// ── Key delivery (Bearer token auth, no API key) ──
	keyHandler := handler.NewKeyHandler(
		s.deps.DBQueries,
		s.cfg.KeyTokenSecret,
		s.deps.KeyWrapper,
	)
	s.echo.GET("/api/key/:hash", keyHandler.Handle,
		custommw.RedisRateLimit(s.deps.Redis, "key", 120, time.Minute, func(c *echo.Context) string {
			auth := c.Request().Header.Get("Authorization")
			if strings.HasPrefix(auth, "Bearer ") {
				return "token:" + custommw.HashRateLimitKey(strings.TrimPrefix(auth, "Bearer "))
			}
			return "ip:" + custommw.HashRateLimitKey(c.RealIP())
		}),
	)
}
