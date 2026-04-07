---
title: Getting Started
description: "Start Vylux locally with the shortest possible path, prepare dependencies, create the first job, and validate service health."
---

# Getting Started

## Prerequisites

- Go 1.26+
- Docker and Docker Compose
- `curl`
- S3-compatible storage for source and derived assets such as RustFS, R2, or S3

If you use the repository Docker image directly, the runtime already contains `ffmpeg`, `vips`, and `packager`. If you run Vylux on the host with `go run`, install FFmpeg, libvips, `pkg-config`, and Shaka Packager locally. On macOS with Homebrew, `brew install vips pkg-config` provides the libvips toolchain that the Go image pipeline links against.

## Recommended local development flow

### 1. Start the infrastructure services

```bash
docker compose -f docker-compose.dev.yml up -d
```

This starts:

- PostgreSQL on `localhost:5434`
- Redis on `localhost:6381`
- RustFS S3 API on `localhost:9002`
- RustFS Console on `localhost:9003`

### 2. Prepare environment variables

```bash
cp .env.example .env.local
```

For the complete environment-variable reference, validation rules, and secret guidance, see [Configuration](./operations/configuration).

If you use `docker-compose.dev.yml`, at minimum point the following settings to localhost:

```ini showLineNumbers
DATABASE_URL=postgres://myuser:mypassword@localhost:5434/mydb
REDIS_URL=redis://localhost:6381
SOURCE_S3_ENDPOINT=http://localhost:9002
SOURCE_S3_REGION=auto
SOURCE_BUCKET=app-bucket
MEDIA_S3_ENDPOINT=http://localhost:9002
MEDIA_S3_REGION=auto
MEDIA_BUCKET=media-bucket
BASE_URL=http://localhost:3000
```

The most important required settings are:

- `DATABASE_URL`
- `REDIS_URL`
- `SOURCE_S3_ENDPOINT`
- `SOURCE_S3_ACCESS_KEY`
- `SOURCE_S3_SECRET_KEY`
- `SOURCE_BUCKET`
- `MEDIA_S3_ENDPOINT`
- `MEDIA_S3_ACCESS_KEY`
- `MEDIA_S3_SECRET_KEY`
- `MEDIA_BUCKET`
- `HMAC_SECRET`
- `WEBHOOK_SECRET`
- `API_KEY`
- `KEY_TOKEN_SECRET`
- `ENCRYPTION_KEY`
- `FFMPEG_PATH`
- `SHAKA_PACKAGER_PATH`

Generate the common secrets with `openssl`:

```bash showLineNumbers
cat >> .env.local <<EOF
HMAC_SECRET=$(openssl rand -hex 32)
API_KEY=$(openssl rand -hex 32)
WEBHOOK_SECRET=$(openssl rand -hex 32)
KEY_TOKEN_SECRET=$(openssl rand -hex 16)
ENCRYPTION_KEY=$(openssl rand -hex 32)
EOF
```

### 3. Create the storage buckets and upload sample source objects

Vylux does not create `SOURCE_BUCKET` or `MEDIA_BUCKET` for you. At minimum you need:

- a source bucket for original objects, configured by `SOURCE_BUCKET` and `SOURCE_S3_*`
- a media bucket for image cache entries, thumbnails, previews, covers, and HLS output, configured by `MEDIA_BUCKET` and `MEDIA_S3_*`

If both roles use the same local RustFS instance or the same S3 provider, still set both storage groups explicitly. Vylux does not infer media settings from source settings.

For S3-compatible storage, Vylux writes derived objects with CRC32C upload checksums. That works with AWS S3, Cloudflare R2, and RustFS in the supported setup here; validate checksum-header support first if you swap in another S3-compatible service.

Upload at least one test asset such as:

- image: `uploads/sample.jpg`
- video: `uploads/sample.mp4`

### 4. Start Vylux

```bash
go run ./cmd/vylux
```

Or split roles into two processes:

```bash showLineNumbers
go run ./cmd/vylux --mode=server
go run ./cmd/vylux --mode=worker
```

If startup fails with a linker error such as `library 'vips' not found`, the most common causes are:

- libvips is not installed on the host
- `pkg-config` cannot resolve the current libvips installation
- Go's build cache still contains stale cgo linker flags from an older Homebrew Cellar path

On macOS with Homebrew, the fastest recovery path is usually:

```bash showLineNumbers
brew install vips pkg-config
go clean -cache
go run ./cmd/vylux
```

If `brew install` reports that both packages are already installed, rerun `go clean -cache` anyway after a Homebrew upgrade. That forces cgo to rebuild with the current `pkg-config` output instead of reusing stale linker paths.

### 5. Validate service health

Check liveness, readiness, and metrics first:

```bash showLineNumbers
curl -i http://localhost:3000/healthz
curl -i http://localhost:3000/readyz
curl -s http://localhost:3000/metrics | rg '^vylux_'
```

If you also run worker-only mode:

```bash showLineNumbers
curl -i http://localhost:3001/healthz
curl -s http://localhost:3001/metrics | rg '^vylux_'
```

## Minimal validation order

The smallest useful API validation flow is:

1. Create a preview job

```bash showLineNumbers
BASE_URL='http://localhost:3000'
API_KEY='replace-with-api-key'

curl -s \
    -X POST "$BASE_URL/api/jobs" \
    -H 'Content-Type: application/json' \
    -H "X-API-Key: $API_KEY" \
    -d '{
        "type": "video:preview",
        "hash": "quickstart-preview-sample",
        "source": "uploads/sample.mp4",
        "options": {
            "start_sec": 1,
            "duration": 3,
            "width": 480,
            "fps": 12,
            "format": "webp"
        }
    }'
```

2. Poll the job until it becomes `completed` or `failed`

```bash showLineNumbers
curl -s \
    -H "X-API-Key: $API_KEY" \
    http://localhost:3000/api/jobs/<job-id>
```

3. Validate the derived asset from `results.key` or `results.streaming.master_playlist`

At this point, do not stop at the storage key alone:

- if the job returned an image-like media-bucket key such as a cover, preview, or thumbnail, convert it into a signed `/thumb/{sig}/{encoded_key}` URL before exposing it to a browser
- if the job returned streaming results, use `/stream/{hash}/master.m3u8` as the public playback entrypoint rather than the raw `master_playlist` storage key
- if encrypted playback is enabled, mint a Bearer token for `/api/key/{hash}` and attach it only on key requests

For the full mapping from job results to public URLs, see [Integration Guide](./integration-guide).

Before release, cover at least these three smoke-test groups:

- `video:preview` with `gif`
- `video:preview` with `webp`
- `video:transcode` with `encrypt=true`

## What you should observe after startup

- `GET /healthz` returns `200`
- `GET /readyz` confirms PostgreSQL, Redis, and buckets are ready
- `GET /metrics` exposes Prometheus metrics
- `POST /api/jobs` can enqueue asynchronous media work

Next, most teams continue with [Integration Guide](./integration-guide), [Configuration](./operations/configuration), [Jobs API](./api/jobs), and [Observability](./operations/observability).
