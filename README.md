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
DEPLOYMENT_ID=<uuid-generated-once-for-this-logical-deployment>
DATABASE_URL=postgres://myuser:mypassword@localhost:5434/mydb
REDIS_URL=redis://localhost:6381
SOURCE_PROVIDER_KIND=s3
SOURCE_S3_ENDPOINT=http://localhost:9002
SOURCE_S3_ACCESS_KEY=replace-me
SOURCE_S3_SECRET_KEY=replace-me
SOURCE_S3_REGION=us-east-1
SOURCE_BUCKET=app-source-bucket
MEDIA_PROVIDER_KIND=s3
MEDIA_S3_ENDPOINT=http://localhost:9002
MEDIA_S3_ACCESS_KEY=replace-me
MEDIA_S3_SECRET_KEY=replace-me
MEDIA_S3_REGION=us-east-1
MEDIA_BUCKET=media-bucket
```

When source and media share the same S3-compatible backend, repeat the endpoint and credential values explicitly for both roles. Vylux does not support implicit fallback from `SOURCE_*` to `MEDIA_*`.

Generate a UUID once (for example with `uuidgen`), write it to `DEPLOYMENT_ID`, and never regenerate it for ordinary restarts or replica deployments.

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

## Deployment identity and protocol v2

Every Vylux process is bound to one logical deployment and to the exact source and media backends that deployment owns. The target object is:

```json
{
  "protocol_version": 2,
  "deployment_id": "550e8400-e29b-41d4-a716-446655440000",
  "source_backend_identity": "sha256:v1:<64 lowercase hex characters>",
  "media_backend_identity": "sha256:v1:<64 lowercase hex characters>"
}
```

`DEPLOYMENT_ID` is a canonical, non-zero UUID. Keep it stable across restarts, releases, and replicas of the same logical deployment. Do not reuse it for a replacement deployment or a source/media provider migration.

Backend fingerprints are non-secret and deterministic across the host and Vylux. Credentials, public delivery URLs, `BASE_URL`, and API keys are excluded, so routine key rotation does not change identity. The provider kind is deliberately included:

- S3: SHA-256 of the compact JSON array `["s3", normalizedEndpoint, region, bucket]`.
- R2: SHA-256 of the compact JSON array `["r2", normalizedEndpoint, bucket]`.

The endpoint is parsed as an HTTP(S) URL; user info, query, fragment, default ports, and trailing slashes are removed, while the scheme and host are lowercased. The result is encoded as `sha256:v1:<lowercase hex>`. `SOURCE_PROVIDER_KIND` and `MEDIA_PROVIDER_KIND` must each be explicitly set to `s3` or `r2`; an API key shared by two deployments never makes their targets interchangeable.

### Discovery, readiness, jobs, and strict deletion

| Surface | Protocol v2 behavior |
| --- | --- |
| `GET /readyz` | Unauthenticated. A successful response keeps the exact body `OK` and includes the four target headers below. |
| `GET /api/deployment` | Requires `X-API-Key`. Returns the target object as JSON and includes the same headers. |
| `POST /api/jobs` | `deployment_target` is optional for rolling compatibility. When supplied, Vylux validates it before any database or queue side effect. Create, status, and retry responses echo the actual target. |
| `DELETE /api/media/:hash/strict` | Requires all four target fields in the flat request body. Vylux validates and compares them before Cleaner and its durable Fence can run. |

The identity headers are:

- `X-Vylux-Protocol-Version`
- `X-Vylux-Deployment-ID`
- `X-Vylux-Source-Backend-Identity`
- `X-Vylux-Media-Backend-Identity`

A strict deletion request is:

```json
{
  "source": "uploads/<hash>-<upload-id>.png",
  "protocol_version": 2,
  "deployment_id": "550e8400-e29b-41d4-a716-446655440000",
  "source_backend_identity": "sha256:v1:<64 lowercase hex characters>",
  "media_backend_identity": "sha256:v1:<64 lowercase hex characters>"
}
```

A complete deletion returns `204`, all four identity headers, and `X-Vylux-Cleanup-Confirmed: 1`. A malformed or incomplete target returns `400`; a well-formed but different deployment/source/media target returns `412`; an unavailable service target or incomplete cleanup returns `503`. None of those failures carries the confirmation header. In particular, a legacy strict body containing only `source` returns `400` and remains retryable without destructive side effects.

The strict `source` value is an exact object-key identity. Vylux may inspect trimmed views only to reject a blank value and to attribute the content hash within individual path segments; it never trims or rewrites the value used by the job, cleanup fence, tombstone, storage lookup, or database lookup. Keys that differ only by segment-boundary whitespace remain distinct.

Adding `deployment_target` to an otherwise identical job request does not change its existing request fingerprint, so a v1 request ID can be replayed during rollout. Omitting the field remains accepted only as a transport-compatibility measure; new host code should send it.

### Database binding and legacy null rows

Migration `003_deployment_target.sql` adds nullable `protocol_version`, `deployment_id`, `source_backend_identity`, and `media_backend_identity` columns to the singleton `media_lifecycle_readiness` row. The four columns are constrained to be either all null or all populated.

On the first protocol-v2 startup, Vylux atomically fills the all-null row from the verified runtime configuration, reads it back, and compares every field. Concurrent replicas with the same target succeed. Any later deployment ID, provider kind, endpoint, region, or bucket drift fails startup instead of silently rebinding.

The first binding is an operator-controlled legacy backfill, not proof of where historical artifacts were written. Before that first v2 start, verify the active v1 source/media configuration against provider records and backups. If the database contains artifacts from more than one target, or provenance is uncertain, leave destructive cleanup paused and split or audit the deployment; do not clear the columns or bind current settings by assumption.

A per-deployment binding is sufficient inside Vylux because that database cannot change target after binding. The host must still persist this full target on every physical-delete task (and retain it for old artifacts); current host settings or an internal URL alone are not historical identity.

### Compatibility-first rollout order

1. Pause host durable/strict physical deletion. Job processing may continue, but do not let a load balancer send strict v1 deletion to old replicas during the cutover.
2. Back up PostgreSQL. Verify the exact source and media provider kinds, endpoints, regions, and buckets. Generate one deployment UUID and configure the identical target on every replica.
3. Apply pending additive migrations in numeric order (`002_media_lifecycle.sql`, then `003_deployment_target.sql`). Old binaries ignore the new nullable columns, so schema rollout can precede binary rollout.
4. Start one v2 canary against the unchanged backends. Its startup performs the one-time bind. Verify its identity headers on `/readyz` and its authenticated `/api/deployment` response. `/readyz` may remain `503` until the existing bounded cache audit completes; do not bypass that gate.
5. Roll every remaining Vylux server and worker replica with the same environment. A replica with drift must fail startup. Confirm all load-balanced replicas report the same target before continuing.
6. Deploy the host-side v2 schema, per-task target persistence, discovery/readiness client, and strict request preconditions. Legacy tasks with unknown identity must remain blocked; backfill only from trustworthy per-file/provider receipts.
7. Re-enable durable deletion only after the host has observed the expected v2 target and all Vylux replicas pass readiness. Keep the returned identity headers and `X-Vylux-Cleanup-Confirmed: 1` as the deletion acknowledgement contract.

For a provider migration, create a new logical deployment with a new UUID and a separate lifecycle database/queue boundary, keep the old deployment available to drain old tasks, and route new artifacts to the new target. Do not reuse an API key, change `internalUrl`, clear the binding row, or edit the old deployment in place as a substitute for target migration.

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
