package config

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	redis "github.com/redis/go-redis/v9"
)

// Config holds all configuration for Vylux, populated from environment variables.
type Config struct {
	// Server
	Port    int    `env:"PORT" envDefault:"3000"` // Port to listen on
	Mode    string `env:"MODE" envDefault:"all"`  // "all" | "server" | "worker"
	BaseURL string `env:"BASE_URL" envDefault:""` // Public base URL for key delivery (e.g. https://static.example.com)

	// Database
	DatabaseURL string `env:"DATABASE_URL,required"`

	// Redis
	RedisURL string `env:"REDIS_URL,required"`

	// Source storage (read-only)
	SourceS3Endpoint  string `env:"SOURCE_S3_ENDPOINT,required"`
	SourceS3AccessKey string `env:"SOURCE_S3_ACCESS_KEY,required"`
	SourceS3SecretKey string `env:"SOURCE_S3_SECRET_KEY,required"`
	SourceS3Region    string `env:"SOURCE_S3_REGION" envDefault:"auto"`
	SourceBucket      string `env:"SOURCE_BUCKET,required"`

	// Media storage (read-write)
	MediaS3Endpoint  string `env:"MEDIA_S3_ENDPOINT,required"`
	MediaS3AccessKey string `env:"MEDIA_S3_ACCESS_KEY,required"`
	MediaS3SecretKey string `env:"MEDIA_S3_SECRET_KEY,required"`
	MediaS3Region    string `env:"MEDIA_S3_REGION" envDefault:"auto"`
	MediaBucket      string `env:"MEDIA_BUCKET,required"`

	// Secrets
	HMACSecret     string `env:"HMAC_SECRET,required"`      // Image URL signing
	APIKey         string `env:"API_KEY,required"`          // Internal API authentication
	WebhookSecret  string `env:"WEBHOOK_SECRET,required"`   // Webhook callback signing
	KeyTokenSecret string `env:"KEY_TOKEN_SECRET,required"` // AES key token verification
	EncryptionKey  string `env:"ENCRYPTION_KEY,required"`   // KEK for wrapped content keys

	// Worker
	WorkerConcurrency      int   `env:"WORKER_CONCURRENCY" envDefault:"10"`
	LargeWorkerConcurrency int   `env:"LARGE_WORKER_CONCURRENCY" envDefault:"1"`
	WorkerMetricsPort      int   `env:"WORKER_METRICS_PORT" envDefault:"3001"`
	LargeFileThreshold     int64 `env:"LARGE_FILE_THRESHOLD" envDefault:"5368709120"` // 5GB
	MaxFileSize            int64 `env:"MAX_FILE_SIZE" envDefault:"53687091200"`       // 50GB

	// Cache
	CacheMaxSize int64 `env:"CACHE_MAX_SIZE" envDefault:"1073741824"` // 1GB LRU

	// FFmpeg
	FFmpegPath        string `env:"FFMPEG_PATH" envDefault:"ffmpeg"`
	ShakaPackagerPath string `env:"SHAKA_PACKAGER_PATH" envDefault:"packager"`

	// Timeouts
	ShutdownTimeout       time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"30s"`
	WorkerShutdownTimeout time.Duration `env:"WORKER_SHUTDOWN_TIMEOUT" envDefault:"10m"`

	// Logging
	LogLevel string `env:"LOG_LEVEL" envDefault:"INFO"`

	// OpenTelemetry
	OTELEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
}

// ScratchDir is the fixed scratch workspace used for large temporary media files.
// Runtime uses this default path; tests may override it to a writable temp dir.
var ScratchDir = "/var/cache/vylux"

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return nil, err
	}

	cfg.normalize()

	return &cfg, nil
}

// Validate performs semantic validation on the loaded configuration.
// It returns a slice of human-readable error strings (empty if all OK).
func (c *Config) Validate() []string {
	var errs []string

	// Mode must be one of the known values.
	switch c.Mode {
	case "all", "server", "worker":
	default:
		errs = append(errs, fmt.Sprintf("MODE must be all|server|worker, got %q", c.Mode))
	}

	// HMAC_SECRET: expect 64 hex chars (256-bit).
	if err := validateHexKey(c.HMACSecret, 32, "HMAC_SECRET"); err != "" {
		errs = append(errs, err)
	}

	// API_KEY: expect 64 hex chars (256-bit).
	if err := validateHexKey(c.APIKey, 32, "API_KEY"); err != "" {
		errs = append(errs, err)
	}

	// WEBHOOK_SECRET: expect 64 hex chars (256-bit).
	if err := validateHexKey(c.WebhookSecret, 32, "WEBHOOK_SECRET"); err != "" {
		errs = append(errs, err)
	}

	// KEY_TOKEN_SECRET: expect 32 hex chars (128-bit).
	if err := validateHexKey(c.KeyTokenSecret, 16, "KEY_TOKEN_SECRET"); err != "" {
		errs = append(errs, err)
	}

	// ENCRYPTION_KEY: expect 64 hex chars (256-bit).
	if err := validateHexKey(c.EncryptionKey, 32, "ENCRYPTION_KEY"); err != "" {
		errs = append(errs, err)
	}

	if err := validatePostgresURL(c.DatabaseURL); err != "" {
		errs = append(errs, err)
	}

	if err := validateRedisURL(c.RedisURL); err != "" {
		errs = append(errs, err)
	}

	if c.BaseURL != "" {
		if err := validateHTTPURL(c.BaseURL, "BASE_URL"); err != "" {
			errs = append(errs, err)
		}
	}

	if err := validateHTTPURL(c.SourceS3Endpoint, "SOURCE_S3_ENDPOINT"); err != "" {
		errs = append(errs, err)
	}

	if err := validateHTTPURL(c.MediaS3Endpoint, "MEDIA_S3_ENDPOINT"); err != "" {
		errs = append(errs, err)
	}

	if err := validateOTLPEndpoint(c.OTELEndpoint); err != "" {
		errs = append(errs, err)
	}

	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, "PORT must be between 1 and 65535")
	}

	if c.WorkerMetricsPort < 0 || c.WorkerMetricsPort > 65535 {
		errs = append(errs, "WORKER_METRICS_PORT must be between 0 and 65535")
	}

	if c.WorkerConcurrency < 1 {
		errs = append(errs, "WORKER_CONCURRENCY must be at least 1")
	}

	if c.LargeWorkerConcurrency < 1 {
		errs = append(errs, "LARGE_WORKER_CONCURRENCY must be at least 1")
	}

	if c.LargeFileThreshold < 1 {
		errs = append(errs, "LARGE_FILE_THRESHOLD must be at least 1")
	}

	if c.MaxFileSize < 1 {
		errs = append(errs, "MAX_FILE_SIZE must be at least 1")
	}

	if c.LargeFileThreshold > c.MaxFileSize {
		errs = append(errs, "LARGE_FILE_THRESHOLD must be less than or equal to MAX_FILE_SIZE")
	}

	return errs
}

func (c *Config) normalize() {
	c.BaseURL = normalizeAbsoluteURL(c.BaseURL)
	c.SourceS3Endpoint = normalizeAbsoluteURL(c.SourceS3Endpoint)
	c.MediaS3Endpoint = normalizeAbsoluteURL(c.MediaS3Endpoint)
	if strings.Contains(c.OTELEndpoint, "://") {
		c.OTELEndpoint = normalizeAbsoluteURL(c.OTELEndpoint)
	}
}

func normalizeAbsoluteURL(raw string) string {
	if raw == "" {
		return ""
	}

	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func validateHTTPURL(raw, name string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Sprintf("%s must be a valid URL: %v", name, err)
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Sprintf("%s must use http:// or https://", name)
	}

	if u.Host == "" {
		return fmt.Sprintf("%s must include a host", name)
	}

	return ""
}

func validatePostgresURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Sprintf("DATABASE_URL must be a valid postgres URL: %v", err)
	}

	switch strings.ToLower(u.Scheme) {
	case "postgres", "postgresql":
	default:
		return "DATABASE_URL must start with postgres:// or postgresql://"
	}

	if u.Host == "" {
		return "DATABASE_URL must include a host"
	}

	return ""
}

func validateRedisURL(raw string) string {
	if _, err := redis.ParseURL(raw); err != nil {
		return fmt.Sprintf("REDIS_URL must be a valid redis URL: %v", err)
	}

	return ""
}

func validateOTLPEndpoint(raw string) string {
	if raw == "" {
		return ""
	}

	if strings.Contains(raw, "://") {
		return validateHTTPURL(raw, "OTEL_EXPORTER_OTLP_ENDPOINT")
	}

	if strings.ContainsAny(raw, "/?#") {
		return "OTEL_EXPORTER_OTLP_ENDPOINT without a scheme must be host[:port] only"
	}

	u, err := url.Parse("//" + raw)
	if err != nil {
		return fmt.Sprintf("OTEL_EXPORTER_OTLP_ENDPOINT must be host[:port] or an absolute http(s) URL: %v", err)
	}

	if u.Host == "" {
		return "OTEL_EXPORTER_OTLP_ENDPOINT must be host[:port] or an absolute http(s) URL"
	}

	return ""
}

// validateHexKey checks that s is a valid hex string decoding to exactly n bytes.
func validateHexKey(s string, n int, name string) string {
	b, err := hex.DecodeString(s)
	if err != nil {
		return fmt.Sprintf("%s is not valid hex (generate with: openssl rand -hex %d)", name, n)
	}
	if len(b) < n {
		return fmt.Sprintf("%s must be at least %d hex chars (%d-bit), got %d", name, n*2, n*8, len(s))
	}

	return ""
}
