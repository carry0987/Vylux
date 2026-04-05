# Vylux
![CI](https://github.com/carry0987/Vylux/actions/workflows/ci.yml/badge.svg)  

**Vylux** is a standalone media processing service written in Go. It combines real-time image transformation with asynchronous media jobs for covers, animated previews, and HLS CMAF transcoding.

## What it does

- Real-time image resize, format conversion, and signed delivery
- Async media jobs over Redis/asynq
- HLS CMAF output with AV1 and H.264 ladders
- Encrypted playback with raw-key CBCS and token-protected key delivery
- PostgreSQL job state, Prometheus metrics, and OpenTelemetry tracing

## Read the docs

- [Docs site](https://carry0987.github.io/Vylux/)
- [Docs entry](https://carry0987.github.io/Vylux/docs/intro)
- [Getting started](https://carry0987.github.io/Vylux/docs/getting-started)
- [Configuration](https://carry0987.github.io/Vylux/docs/operations/configuration)
- [Testing](https://carry0987.github.io/Vylux/docs/development/testing)
- [Architecture overview](https://carry0987.github.io/Vylux/docs/architecture/overview)

## Quick start

```bash
git clone https://github.com/carry0987/vylux.git && cd vylux
go build -o bin/vylux ./cmd/vylux
docker compose -f docker-compose.dev.yml up -d
cp .env.example .env
```

For host-side local development, override Docker hostnames in `.env.local`:

```dotenv
DATABASE_URL=postgres://myuser:mypassword@localhost:5434/mydb
REDIS_URL=redis://localhost:6381
SOURCE_S3_ENDPOINT=http://localhost:9002
SOURCE_S3_ACCESS_KEY=replace-me
SOURCE_S3_SECRET_KEY=replace-me
SOURCE_S3_REGION=us-east-1
SOURCE_BUCKET=app-source-bucket
MEDIA_S3_ENDPOINT=http://localhost:9002
MEDIA_S3_ACCESS_KEY=replace-me
MEDIA_S3_SECRET_KEY=replace-me
MEDIA_S3_REGION=us-east-1
MEDIA_BUCKET=media-bucket
```

When source and media share the same S3-compatible backend, repeat the endpoint and credential values explicitly for both roles. Vylux does not support implicit fallback from `SOURCE_*` to `MEDIA_*`.

Generate the five common secrets and append them to `.env`:

```bash
cat >> .env <<EOF
HMAC_SECRET=$(openssl rand -hex 32)
API_KEY=$(openssl rand -hex 32)
WEBHOOK_SECRET=$(openssl rand -hex 32)
KEY_TOKEN_SECRET=$(openssl rand -hex 16)
ENCRYPTION_KEY=$(openssl rand -hex 32)
EOF
```

Then run Vylux:

```bash
./bin/vylux

# or split roles
./bin/vylux --mode=server
./bin/vylux --mode=worker
```

In `--mode=worker`, Vylux starts a lightweight metrics listener on `WORKER_METRICS_PORT` (default `3001`) serving `/metrics` and `/healthz`.

For containerized transcoding, Vylux always uses `/var/cache/vylux` as its scratch workspace. The image sets `TMPDIR` to that path and declares it as a Docker volume so large source downloads, intermediate encodes, and packaged HLS output stay on one disk-backed workspace by default.

## Testing

```bash
go test -short ./...
go test -v ./tests/integration/
```

For release-focused manual validation, use the checklist in [Testing](https://carry0987.github.io/Vylux/docs/development/testing).

## Configuration source of truth

- full environment variables: [.env.example](.env.example)
- operations docs: [Configuration](https://carry0987.github.io/Vylux/docs/operations/configuration)

## License

This project is licensed under the Apache-2.0 License. See [LICENSE](LICENSE) for details.
