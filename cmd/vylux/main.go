package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"Vylux/internal/cache"
	"Vylux/internal/config"
	"Vylux/internal/db"
	"Vylux/internal/db/dbq"
	"Vylux/internal/encryption"
	appmetrics "Vylux/internal/metrics"
	"Vylux/internal/queue"
	"Vylux/internal/queue/handlers"
	"Vylux/internal/server"
	"Vylux/internal/storage"
	apptracing "Vylux/internal/tracing"
	"Vylux/internal/video"
	"Vylux/internal/webhook"
	"Vylux/migrations"

	"github.com/hibiken/asynq"
	"github.com/joho/godotenv"
	redis "github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
)

// version and commit are set at build time via -ldflags.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	// Parse --mode flag (overrides MODE env var)
	mode := flag.String("mode", "", "Run mode: all | server | worker")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("Vylux %s (%s)\n", version, commit)
		return
	}

	// Load .env files: .env first, then .env.local overrides.
	// Missing files are silently ignored (production uses real env vars).
	_ = godotenv.Overload(".env", ".env.local")

	// Load configuration from environment variables
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to load config: %v\n", err)
		os.Exit(1)
	}

	// CLI flag overrides env var
	if *mode != "" {
		cfg.Mode = *mode
	}

	// Validate configuration values.
	if errs := cfg.Validate(); len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "ERROR: invalid configuration:\n")
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		os.Exit(1)
	}

	// Set up structured logging
	setupLogger(cfg.LogLevel)

	// Print startup banner (human-readable, before JSON logs)
	printBanner(cfg)

	video.SetFFmpegPath(cfg.FFmpegPath)
	video.SetPackagerPath(cfg.ShakaPackagerPath)

	if err := run(cfg); err != nil {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

// run dispatches to the appropriate mode handler.
func run(cfg *config.Config) error {
	// Context with signal-based cancellation
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := apptracing.Init(ctx, apptracing.Config{
		Endpoint:       cfg.OTELEndpoint,
		ServiceName:    "vylux",
		ServiceVersion: version,
	})
	if err != nil {
		return fmt.Errorf("init tracing: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(shutdownCtx); err != nil {
			slog.Warn("shutdown tracing failed", "error", err)
		}
	}()

	// Run database migrations.
	if err := db.Migrate(ctx, cfg.DatabaseURL, migrations.FS); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// Establish database connection pool.
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db connect: %w", err)
	}
	defer pool.Close()

	// Initialize source and media storage.
	sourceStore, err := newS3Store(ctx, storage.S3Config{
		Endpoint:  cfg.SourceS3Endpoint,
		AccessKey: cfg.SourceS3AccessKey,
		SecretKey: cfg.SourceS3SecretKey,
		Region:    cfg.SourceS3Region,
	}, "source")
	if err != nil {
		return fmt.Errorf("source storage: %w", err)
	}

	mediaStore, err := newS3Store(ctx, storage.S3Config{
		Endpoint:  cfg.MediaS3Endpoint,
		AccessKey: cfg.MediaS3AccessKey,
		SecretKey: cfg.MediaS3SecretKey,
		Region:    cfg.MediaS3Region,
	}, "media")
	if err != nil {
		return fmt.Errorf("media storage: %w", err)
	}

	// Initialize LRU cache.
	lru := cache.New(cfg.CacheMaxSize)

	keyWrapper, err := encryption.NewKeyWrapper(cfg.EncryptionKey)
	if err != nil {
		return fmt.Errorf("encryption key wrapper: %w", err)
	}

	redisOpt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("parse redis URL: %w", err)
	}
	redisClient := redis.NewClient(redisOpt)
	defer redisClient.Close()

	// Initialize queue client.
	queueClient, err := queue.NewClient(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("queue client: %w", err)
	}
	defer queueClient.Close()

	// Initialize DB queries.
	queries := dbq.New(pool)

	// Initialize asynq inspector (for task cancellation).
	inspector, err := queue.NewInspector(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("queue inspector: %w", err)
	}
	defer inspector.Close()

	deps := &server.Deps{
		SourceStore: sourceStore,
		MediaStore:  mediaStore,
		Cache:       lru,
		QueueClient: queueClient,
		DBQueries:   queries,
		Inspector:   inspector,
		Redis:       redisClient,
		KeyWrapper:  keyWrapper,
		DBPing:      pool.Ping,
		RedisPing: func(ctx context.Context) error {
			return redisClient.Ping(ctx).Err()
		},
	}

	// Initialize webhook client.
	webhookClient := webhook.NewClient(cfg.WebhookSecret, queries)

	workerDeps := &handlers.Deps{
		SourceStore: sourceStore,
		MediaStore:  mediaStore,
		Queries:     queries,
		QueueClient: queueClient,
		Config:      cfg,
		KeyWrapper:  keyWrapper,
		Webhook:     webhookClient,
	}

	switch cfg.Mode {
	case "all":
		return runAll(ctx, cfg, deps, workerDeps)
	case "server":
		return runServer(ctx, cfg, deps)
	case "worker":
		return runWorker(ctx, cfg, workerDeps, inspector)
	default:
		slog.Error("unknown mode", "mode", cfg.Mode)

		return fmt.Errorf("unknown mode: %s", cfg.Mode)
	}
}

func newS3Store(ctx context.Context, cfg storage.S3Config, role string) (storage.Storage, error) {
	store, err := storage.NewS3(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return storage.WithInstrumentation(store, role, "s3"), nil
}

// runServer starts only the HTTP server.
func runServer(ctx context.Context, cfg *config.Config, deps *server.Deps) error {
	srv := server.New(cfg, deps)

	return srv.Start(ctx)
}

// runWorker starts only the asynq worker.
func runWorker(ctx context.Context, cfg *config.Config, d *handlers.Deps, inspector *asynq.Inspector) error {
	wrk, err := queue.NewServer(cfg)
	if err != nil {
		return fmt.Errorf("create worker: %w", err)
	}

	registerHandlers(wrk, d)
	if cfg.WorkerMetricsPort <= 0 {
		return wrk.Run(ctx)
	}

	appmetrics.ConfigureInspector(inspector)

	metricsSrv := appmetrics.NewServer(cfg.WorkerMetricsPort)

	g, gCtx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return wrk.Run(gCtx)
	})
	g.Go(func() error {
		return metricsSrv.Start(gCtx)
	})

	return g.Wait()
}

// runAll starts both the HTTP server and the worker concurrently.
func runAll(ctx context.Context, cfg *config.Config, deps *server.Deps, workerDeps *handlers.Deps) error {
	wrk, err := queue.NewServer(cfg)
	if err != nil {
		return fmt.Errorf("create worker: %w", err)
	}

	registerHandlers(wrk, workerDeps)

	srv := server.New(cfg, deps)

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return srv.Start(gCtx)
	})

	g.Go(func() error {
		return wrk.Run(gCtx)
	})

	return g.Wait()
}

// registerHandlers wires up all task handlers onto the worker server.
// Individual handler implementations live in internal/queue/handlers/.
func registerHandlers(wrk *queue.Server, d *handlers.Deps) {
	wrk.HandleFunc(queue.TypeImageThumbnail, handlers.HandleImageThumbnail(d))
	wrk.HandleFunc(queue.TypeVideoCover, handlers.HandleVideoCover(d))
	wrk.HandleFunc(queue.TypeVideoPreview, handlers.HandleVideoPreview(d))
	wrk.HandleFunc(queue.TypeVideoTranscode, handlers.HandleVideoTranscode(d))
	wrk.HandleFunc(queue.TypeVideoFull, handlers.HandleVideoFull(d))
	slog.Info("task handlers registered")
}

// printBanner prints a human-readable startup summary to stderr.
func printBanner(cfg *config.Config) {
	w := os.Stderr
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "  Vylux %s (%s)\n", version, shortCommit())
	fmt.Fprintf(w, "  Mode:    %s\n", cfg.Mode)
	switch cfg.Mode {
	case "worker":
		if cfg.WorkerMetricsPort > 0 {
			fmt.Fprintf(w, "  Metrics: http://0.0.0.0:%d\n", cfg.WorkerMetricsPort)
		} else {
			fmt.Fprintf(w, "  Metrics: disabled\n")
		}
	default:
		fmt.Fprintf(w, "  Listen:  http://0.0.0.0:%d\n", cfg.Port)
	}
	fmt.Fprintf(w, "  Buckets: source=%s  media=%s\n", cfg.SourceBucket, cfg.MediaBucket)
	fmt.Fprintf(w, "  Log:     %s\n", cfg.LogLevel)
	fmt.Fprintf(w, "\n")
}

// shortCommit returns the first 7 characters of the commit hash.
func shortCommit() string {
	if len(commit) > 7 {
		return commit[:7]
	}

	return commit
}

// setupLogger configures slog with JSON output and the specified level.
func setupLogger(level string) {
	var lvl slog.Level
	switch strings.ToUpper(level) {
	case "DEBUG":
		lvl = slog.LevelDebug
	case "WARN":
		lvl = slog.LevelWarn
	case "ERROR":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	slog.SetDefault(slog.New(handler))
}
