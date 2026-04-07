package config

import (
	"strings"
	"testing"
)

func TestValidate_PortRanges(t *testing.T) {
	tests := []struct {
		name        string
		port        int
		workerPort  int
		wantErrPart string
	}{
		{name: "valid ports", port: 3000, workerPort: 3001},
		{name: "worker metrics disabled", port: 3000, workerPort: 0},
		{name: "invalid server port zero", port: 0, workerPort: 3001, wantErrPart: "PORT must be between 1 and 65535"},
		{name: "invalid server port high", port: 70000, workerPort: 3001, wantErrPart: "PORT must be between 1 and 65535"},
		{name: "invalid worker metrics negative", port: 3000, workerPort: -1, wantErrPart: "WORKER_METRICS_PORT must be between 0 and 65535"},
		{name: "invalid worker metrics high", port: 3000, workerPort: 70000, wantErrPart: "WORKER_METRICS_PORT must be between 0 and 65535"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Port = tt.port
			cfg.WorkerMetricsPort = tt.workerPort

			errs := cfg.Validate()
			if tt.wantErrPart == "" {
				if len(errs) != 0 {
					t.Fatalf("expected no validation errors, got %v", errs)
				}
				return
			}

			for _, err := range errs {
				if strings.Contains(err, tt.wantErrPart) {
					return
				}
			}

			t.Fatalf("expected validation error containing %q, got %v", tt.wantErrPart, errs)
		})
	}
}

func TestValidate_WorkerAndScratchSettings(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*Config)
		wantErrPart string
	}{
		{name: "invalid worker concurrency", mutate: func(cfg *Config) { cfg.WorkerConcurrency = 0 }, wantErrPart: "WORKER_CONCURRENCY must be at least 1"},
		{name: "invalid large worker concurrency", mutate: func(cfg *Config) { cfg.LargeWorkerConcurrency = 0 }, wantErrPart: "LARGE_WORKER_CONCURRENCY must be at least 1"},
		{name: "threshold above max", mutate: func(cfg *Config) { cfg.LargeFileThreshold = 20; cfg.MaxFileSize = 10 }, wantErrPart: "LARGE_FILE_THRESHOLD must be less than or equal to MAX_FILE_SIZE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)

			errs := cfg.Validate()
			for _, err := range errs {
				if strings.Contains(err, tt.wantErrPart) {
					return
				}
			}

			t.Fatalf("expected validation error containing %q, got %v", tt.wantErrPart, errs)
		})
	}
}

func TestValidate_URLSettings(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*Config)
		wantErrPart string
	}{
		{name: "invalid database url host", mutate: func(cfg *Config) { cfg.DatabaseURL = "postgres:///db" }, wantErrPart: "DATABASE_URL must include a host"},
		{name: "invalid redis url", mutate: func(cfg *Config) { cfg.RedisURL = "://redis" }, wantErrPart: "REDIS_URL must be a valid redis URL"},
		{name: "invalid base url scheme", mutate: func(cfg *Config) { cfg.BaseURL = "ftp://media.example.com" }, wantErrPart: "BASE_URL must use http:// or https://"},
		{name: "invalid source endpoint host", mutate: func(cfg *Config) { cfg.SourceS3Endpoint = "https:///bucket" }, wantErrPart: "SOURCE_S3_ENDPOINT must include a host"},
		{name: "invalid media endpoint scheme", mutate: func(cfg *Config) { cfg.MediaS3Endpoint = "s3://media.example.com" }, wantErrPart: "MEDIA_S3_ENDPOINT must use http:// or https://"},
		{name: "invalid otel endpoint path without scheme", mutate: func(cfg *Config) { cfg.OTELEndpoint = "collector:4318/v1/traces" }, wantErrPart: "OTEL_EXPORTER_OTLP_ENDPOINT without a scheme must be host[:port] only"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)

			errs := cfg.Validate()
			for _, err := range errs {
				if strings.Contains(err, tt.wantErrPart) {
					return
				}
			}

			t.Fatalf("expected validation error containing %q, got %v", tt.wantErrPart, errs)
		})
	}
}

func TestNormalize_URLSettings(t *testing.T) {
	cfg := validConfig()
	cfg.BaseURL = "https://media.example.com/"
	cfg.SourceS3Endpoint = "https://source.example.r2.cloudflarestorage.com/"
	cfg.MediaS3Endpoint = "https://media.example.r2.cloudflarestorage.com/"
	cfg.OTELEndpoint = "https://otel.example.com/v1/traces/"

	cfg.normalize()

	if cfg.BaseURL != "https://media.example.com" {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, "https://media.example.com")
	}
	if cfg.SourceS3Endpoint != "https://source.example.r2.cloudflarestorage.com" {
		t.Fatalf("SourceS3Endpoint = %q, want %q", cfg.SourceS3Endpoint, "https://source.example.r2.cloudflarestorage.com")
	}
	if cfg.MediaS3Endpoint != "https://media.example.r2.cloudflarestorage.com" {
		t.Fatalf("MediaS3Endpoint = %q, want %q", cfg.MediaS3Endpoint, "https://media.example.r2.cloudflarestorage.com")
	}
	if cfg.OTELEndpoint != "https://otel.example.com/v1/traces" {
		t.Fatalf("OTELEndpoint = %q, want %q", cfg.OTELEndpoint, "https://otel.example.com/v1/traces")
	}

	if errs := cfg.Validate(); len(errs) != 0 {
		t.Fatalf("expected normalized config to validate cleanly, got %v", errs)
	}
}

func validConfig() *Config {
	return &Config{
		Port:                   3000,
		Mode:                   "all",
		DatabaseURL:            "postgres://user:pass@localhost:5432/db",
		RedisURL:               "redis://localhost:6379",
		SourceS3Endpoint:       "https://source.example.r2.cloudflarestorage.com",
		SourceS3AccessKey:      "source-access",
		SourceS3SecretKey:      "source-secret",
		SourceS3Region:         "auto",
		SourceBucket:           "source",
		MediaS3Endpoint:        "https://media.example.r2.cloudflarestorage.com",
		MediaS3AccessKey:       "media-access",
		MediaS3SecretKey:       "media-secret",
		MediaS3Region:          "auto",
		MediaBucket:            "media",
		HMACSecret:             strings.Repeat("a", 64),
		APIKey:                 strings.Repeat("b", 64),
		WebhookSecret:          strings.Repeat("c", 64),
		KeyTokenSecret:         strings.Repeat("d", 32),
		EncryptionKey:          strings.Repeat("e", 64),
		WorkerConcurrency:      10,
		LargeWorkerConcurrency: 1,
		WorkerMetricsPort:      3001,
		LargeFileThreshold:     5 * 1024 * 1024 * 1024,
		MaxFileSize:            50 * 1024 * 1024 * 1024,
		CacheMaxSize:           1024,
		FFmpegPath:             "ffmpeg",
		ShakaPackagerPath:      "packager",
		LogLevel:               "INFO",
		BaseURL:                "https://media.example.com",
	}
}
