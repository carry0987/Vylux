# Vylux
![CI](https://github.com/carry0987/Vylux/actions/workflows/ci.yml/badge.svg)  

**Vylux** is a standalone media processing service written in Go. It combines real-time image transformation with asynchronous audio and video workflows for waveforms, covers, animated previews, protected streaming, downloadable derivatives, and HLS CMAF packaging.

## What it does

- Real-time image resize, format conversion, and signed delivery
- Async audio and video jobs over Redis/asynq
- Audio-only HLS, protected audio HLS, MP3/FLAC download artifacts, and waveform generation
- Video cover extraction, animated previews, and HLS CMAF output with AV1 and H.264 ladders
- Asset-scoped encrypted playback with raw-key CBCS and token-protected key delivery
- PostgreSQL job state, Prometheus metrics, and OpenTelemetry tracing

## Read the docs

- [Docs site](https://carry0987.github.io/Vylux/)
- [Docs entry](https://carry0987.github.io/Vylux/docs/intro)
- [Getting started](https://carry0987.github.io/Vylux/docs/getting-started)
- [Audio pipeline](https://carry0987.github.io/Vylux/docs/media/audio-pipeline)
- [Configuration](https://carry0987.github.io/Vylux/docs/operations/configuration)
- [Testing](https://carry0987.github.io/Vylux/docs/development/testing)
- [Architecture overview](https://carry0987.github.io/Vylux/docs/architecture/overview)

## Published container image

The official runtime image is `ghcr.io/carry0987/vylux:latest`.

GitHub Actions builds and publishes it automatically from the release workflow, including a multi-platform manifest for `linux/amd64` and `linux/arm64`.

## Quick start

For containerized deployment, copy the example environment file and start the repository `docker-compose.yml` directly:

```bash
cp .env.example .env
docker compose up -d
```

For host-run local development, build Vylux locally and use `docker-compose.dev.yml` for infrastructure:

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

Generate the five required secrets and an optional image-source encryption key, then append them to `.env`:

```bash
cat >> .env <<EOF
HMAC_SECRET=$(openssl rand -hex 32)
API_KEY=$(openssl rand -hex 32)
WEBHOOK_SECRET=$(openssl rand -hex 32)
KEY_TOKEN_SECRET=$(openssl rand -hex 16)
ENCRYPTION_KEY=$(openssl rand -hex 32)
IMAGE_SOURCE_KEY=$(openssl rand -hex 32)
EOF
```

`IMAGE_SOURCE_KEY` enables AES-256-GCM source tokens for `/img` and `/original`. Configure the same key in the trusted application that generates signed image URLs. Leave it empty to retain plaintext source-key URLs.

### Encrypted image source protocol

When `IMAGE_SOURCE_KEY` is set, encode the exact UTF-8 source object key as:

```text
token_bytes = 0x01 || nonce || AES-256-GCM(source_key, nonce, aad)
nonce       = 12 cryptographically random bytes
aad         = "vylux/image-source/v1"
token       = unpadded Base64URL(token_bytes)
```

The AES-GCM result includes its 16-byte authentication tag. Never reuse, fix, or derive the nonce from the source key. Store and reuse the generated token when stable CDN URLs are required.

Encrypted-source URLs use these forms:

```text
/img/{signature}/{options}/{token}.{format}
/original/{signature}/{token}
```

Sign with the existing `HMAC_SECRET` value as UTF-8 bytes. Each `\x00` below is one NUL byte:

```text
image input = "vylux/image-url/v1" || "\x00" || canonical_options ||
			  "\x00" || token || "\x00" || canonical_format

original input = "vylux/original-url/v1" || "\x00" || token
```

Use the full HMAC-SHA256 digest encoded as lowercase hex. The fixed interoperability vector is:

```text
IMAGE_SOURCE_KEY = 00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff
nonce            = 000102030405060708090a0b
source_key       = static/forum/recommend-cover.png
token            = AQABAgMEBQYHCAkKC2Vs1EIpKuLQpkkakR6nAUwBWMoblWBW_FZUJL4KkkRQvPT1Ob-M_IsC7s3mG3OUcg

HMAC_SECRET      = 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
options          = w671_h278_q90
format           = avif
image_signature  = a01588e17e4abaeb812697d3b41006dd90d60f44c287c918731ac9e27ffbcb55
original_signature = ea4f2676019686704ca7bd20e5b3a801f6f218e4f4c4d2b7bde7978c482c0021
```

Then run Vylux:

```bash
./bin/vylux

# or split roles
./bin/vylux --mode=server
./bin/vylux --mode=worker
```

In `--mode=worker`, Vylux starts a lightweight metrics listener on `WORKER_METRICS_PORT` (default `3001`) serving `/metrics` and `/healthz`.

For containerized media processing, Vylux always uses `/var/cache/vylux` as its scratch workspace. The image sets `TMPDIR` to that path and declares it as a Docker volume so large source downloads, intermediate encodes, waveform generation, and packaged output stay on one disk-backed workspace by default.

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
