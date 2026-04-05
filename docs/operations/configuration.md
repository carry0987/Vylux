---
title: Configuration
description: "Complete environment-variable reference for Vylux, including purpose, defaults, requiredness, and validation rules."
---

# Configuration

Vylux is configured almost entirely through environment variables. In practice, the CLI `--mode` flag overrides the `MODE` environment variable, while the rest of the process settings are loaded at startup.

## Configuration principles

- when you split server and worker, keep all shared infrastructure and secret settings aligned between both processes
- inject secrets through your deployment platform rather than baking them into images or front-end code
- `BASE_URL` must not end with a trailing slash because it is used to build playback key-delivery URLs

## Core runtime

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `PORT` | no | `3000` | HTTP listen port for server or all mode |
| `MODE` | no | `all` | `all`, `server`, or `worker` |
| `BASE_URL` | recommended | empty | public media base URL, such as `https://media.example.com` |
| `LOG_LEVEL` | no | `INFO` | `DEBUG`, `INFO`, `WARN`, or `ERROR` |

## Data and queues

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `DATABASE_URL` | yes | none | PostgreSQL DSN; must start with `postgres://` or `postgresql://` |
| `REDIS_URL` | yes | none | Redis connection string used by asynq, rate limiting, and worker coordination |

## Object storage

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `SOURCE_S3_ENDPOINT` | yes | none | S3-compatible endpoint for the source-storage role |
| `SOURCE_S3_ACCESS_KEY` | yes | none | access key for the source-storage role |
| `SOURCE_S3_SECRET_KEY` | yes | none | secret key for the source-storage role |
| `SOURCE_S3_REGION` | no | `auto` | source-storage region; R2 usually uses `auto` |
| `SOURCE_BUCKET` | yes | none | source bucket; Vylux treats it as read-only input |
| `MEDIA_S3_ENDPOINT` | yes | none | S3-compatible endpoint for the media-storage role |
| `MEDIA_S3_ACCESS_KEY` | yes | none | access key for the media-storage role |
| `MEDIA_S3_SECRET_KEY` | yes | none | secret key for the media-storage role |
| `MEDIA_S3_REGION` | no | `auto` | media-storage region; R2 usually uses `auto` |
| `MEDIA_BUCKET` | yes | none | media bucket; Vylux reads and writes derived assets here |

Vylux sends `x-amz-checksum-algorithm: CRC32C` on S3-compatible `PutObject` uploads for object-integrity verification. AWS S3, Cloudflare R2, and current RustFS releases are expected to handle this correctly. If you deploy against a less common S3-compatible backend, verify checksum-header support before rollout.

Vylux requires both source and media storage settings explicitly. Even if both roles point at the same R2 account, RustFS instance, or S3 service, set both `SOURCE_S3_*` and `MEDIA_S3_*`; there is no implicit fallback from one role to the other.

## Security and secrets

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `HMAC_SECRET` | yes | none | signs image and media-delivery URLs |
| `API_KEY` | yes | none | protects `/api/*` management endpoints |
| `WEBHOOK_SECRET` | yes | none | signs webhook callback payloads |
| `KEY_TOKEN_SECRET` | yes | none | verifies Bearer tokens for `/api/key/{hash}` |
| `ENCRYPTION_KEY` | yes | none | KEK used to wrap stored content keys |

### Quick secret generation

Vylux commonly uses five secrets. You can generate them with `openssl`:

```bash showLineNumbers
# HMAC_SECRET
openssl rand -hex 32

# API_KEY
openssl rand -hex 32

# WEBHOOK_SECRET
openssl rand -hex 32

# KEY_TOKEN_SECRET
openssl rand -hex 16

# ENCRYPTION_KEY
openssl rand -hex 32
```

To append all five to `.env` in one step:

```bash showLineNumbers
cat >> .env <<EOF
HMAC_SECRET=$(openssl rand -hex 32)
API_KEY=$(openssl rand -hex 32)
WEBHOOK_SECRET=$(openssl rand -hex 32)
KEY_TOKEN_SECRET=$(openssl rand -hex 16)
ENCRYPTION_KEY=$(openssl rand -hex 32)
EOF
```

If `openssl` is unavailable, use `/dev/urandom` instead:

```bash showLineNumbers
head -c 32 /dev/urandom | xxd -p -c 64
head -c 16 /dev/urandom | xxd -p -c 32
```

### Secret reference

| Variable | Bits | Purpose |
| --- | --- | --- |
| `HMAC_SECRET` | 256 | Signs `/img`, `/original`, and related image URLs |
| `API_KEY` | 256 | Protects `/api/*` management and job endpoints |
| `WEBHOOK_SECRET` | 256 | Signs webhook callback payloads |
| `KEY_TOKEN_SECRET` | 128 | Signs playback key-delivery tokens |
| `ENCRYPTION_KEY` | 256 | Wraps stored content keys before persistence |

All five values are expected to be hex strings. `HMAC_SECRET`, `API_KEY`, `WEBHOOK_SECRET`, and `ENCRYPTION_KEY` should be at least 32 bytes. `KEY_TOKEN_SECRET` should be at least 16 bytes.

## Worker, cache, and media toolchain

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `WORKER_CONCURRENCY` | no | `10` | worker concurrency |
| `LARGE_WORKER_CONCURRENCY` | no | `1` | dedicated concurrency for the `video:large` worker pool |
| `WORKER_METRICS_PORT` | no | `3001` | worker-only metrics listener; set to `0` to disable |
| `LARGE_FILE_THRESHOLD` | no | `5368709120` | bytes threshold for routing large tasks to the lower-priority queue |
| `MAX_FILE_SIZE` | no | `53687091200` | maximum accepted source size in bytes |
| `CACHE_MAX_SIZE` | no | `1073741824` | in-memory LRU image-cache limit in bytes |
| `FFMPEG_PATH` | no | `ffmpeg` | FFmpeg binary path |
| `SHAKA_PACKAGER_PATH` | no | `packager` | Shaka Packager binary path |

In containerized deployments, Vylux uses `/var/cache/vylux` as the scratch workspace for large temporary media files. If you want the actual storage to live somewhere else, mount your preferred host path, named volume, or platform disk to `/var/cache/vylux` inside the container.

## Timeouts and observability

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `SHUTDOWN_TIMEOUT` | no | `30s` | graceful HTTP shutdown timeout |
| `WORKER_SHUTDOWN_TIMEOUT` | no | `10m` | graceful worker shutdown timeout |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | no | empty | OTLP HTTP exporter endpoint, such as `http://localhost:4318` or `https://otel.example.com/v1/traces` |

## Compose and helper-service variables

The following values are not consumed directly by the Vylux binary but are used by compose or supporting services:

| Variable | Purpose |
| --- | --- |
| `POSTGRES_USER` | initialize the PostgreSQL container in compose |
| `POSTGRES_PASSWORD` | initialize the PostgreSQL container in compose |
| `POSTGRES_DB` | initialize the PostgreSQL container in compose |
| `TUNNEL_TOKEN` | used by the optional Cloudflare Tunnel container |

## Validation rules and common mistakes

- `MODE` must be `all`, `server`, or `worker`
- `PORT` must be within `1..65535`
- `WORKER_METRICS_PORT` must be within `0..65535`
- `WORKER_CONCURRENCY` must be at least `1`
- `LARGE_WORKER_CONCURRENCY` must be at least `1`
- `LARGE_FILE_THRESHOLD` must be at least `1`
- `MAX_FILE_SIZE` must be at least `1`
- `LARGE_FILE_THRESHOLD` must be less than or equal to `MAX_FILE_SIZE`
- `BASE_URL` must not end with `/`
- `DATABASE_URL` must begin with `postgres://` or `postgresql://`

## R2 and local S3 tips

- For Cloudflare R2, set both `SOURCE_S3_ENDPOINT` and `MEDIA_S3_ENDPOINT` to `https://<ACCOUNT_ID>.r2.cloudflarestorage.com`, and use `SOURCE_S3_REGION=auto` and `MEDIA_S3_REGION=auto`.
- For local RustFS testing, set both `SOURCE_S3_ENDPOINT` and `MEDIA_S3_ENDPOINT` to `http://localhost:9002`.
- Vylux uploads S3 objects with CRC32C checksums enabled; keep this in mind when validating compatibility with other S3-like providers.
- Keep the source bucket read-only and the media bucket read/write when possible.
