---
title: Deployment
description: "Deployment guidance for Docker, single-process mode, split server/worker layouts, and Kubernetes, including the current image mode defaults."
---

# Deployment

## Docker image runtime semantics

The current Docker image uses:

```dockerfile
ENTRYPOINT ["vylux"]
CMD ["--mode=all"]
```

That means:

- the image defaults to `all`
- `--mode=all` is only a default argument, not a lock-in
- Docker, Compose, and Kubernetes can all override `args` to run `--mode=server` or `--mode=worker`

This keeps the smallest deployment shape convenient while still supporting split roles later.

## Local development

The most common local shape is:

- PostgreSQL via Docker
- Redis via Docker
- RustFS or another S3-compatible storage service
- Vylux running either on the host or as the image in `all` mode

The repository exposes two different local workflows:

- `docker-compose.dev.yml`: infrastructure only, with Vylux running on the host
- `docker-compose.yml`: Vylux, PostgreSQL, Redis, and optional Cloudflare Tunnel all running in containers

For example:

```bash showLineNumbers
docker compose -f docker-compose.dev.yml up -d
go run ./cmd/vylux
```

For the complete environment-variable reference, default values, and validation rules behind these deployment examples, see [Configuration](./configuration).

## Runtime modes

- `MODE=all`: run the HTTP server and worker in one process
- `MODE=server`: run only the HTTP server and delivery endpoints
- `MODE=worker`: run only the queue worker

CLI flags override the environment variable:

```bash showLineNumbers
./bin/vylux --mode=server
./bin/vylux --mode=worker
```

## Single-process mode

This is the simplest production shape when scale is small or operational simplicity matters more than isolation.

```bash showLineNumbers
docker run --rm \
    --env-file .env \
    -p 3000:3000 \
    -v vylux-scratch:/var/cache/vylux \
    ghcr.io/carry0987/vylux:latest
```

This mode is reasonable for local development, staging, or small deployments.

The image sets `TMPDIR=/var/cache/vylux` and declares `/var/cache/vylux` as a Docker volume. If you do not mount anything explicitly, Docker will create an anonymous volume. In production, prefer an explicit named volume or platform-managed disk so scratch usage remains visible and easier to manage.

## Docker Compose deployment

The repository `docker-compose.yml` uses a single `vylux` service in the default `all` mode and also starts:

- PostgreSQL
- Redis
- an optional Cloudflare Tunnel sidecar

Operational notes:

- the container health check uses `GET /healthz`
- `/var/cache/vylux` is mounted as the dedicated scratch volume
- the image also sets `TMPDIR=/var/cache/vylux`, so tool-level temp files land on the same workspace
- there is no separate key tmpfs mount because raw encryption keys are passed directly to Shaka Packager and are not staged as files on disk
- a `ports:` mapping is only required if you want direct host access such as `http://localhost:3000` or `http://localhost:3100`

Minimal startup:

```bash
docker compose up -d --build
```

:::tip When `ports:` is optional
If all external traffic goes through Cloudflare Tunnel, the `vylux` service does not need a host `ports:` mapping. Keep `ports:` only when you also want direct host-side access for local browser or `curl` testing.
:::

### Container-network semantics

Inside `docker-compose.yml`, each container gets its own `localhost`. That means:

- `localhost` inside the `vylux` container points back to the `vylux` container itself
- `localhost` inside the `tunnel` container points back to `cloudflared`, not to `vylux`
- cross-container traffic should use compose service names such as `postgres`, `redis`, and `vylux`

If you run Vylux itself inside compose, do not reuse host-only examples such as `SOURCE_S3_ENDPOINT=http://localhost:9002` unless the storage endpoint is actually reachable from inside that container.

### Cloudflare Tunnel sidecar

:::warning `localhost` is the wrong tunnel origin in Docker
When `cloudflared` runs in Docker, `http://localhost:3100` points back to the tunnel container itself, not to the `vylux` service.
:::

The tunnel sidecar shares the compose network with `vylux`, so the origin service should be configured with the compose service name, for example `http://vylux:3100`.

Do not point the tunnel origin at `http://localhost:3100` when `cloudflared` runs in Docker. In that case `localhost` resolves inside the tunnel container and typically produces `502` errors with logs like `dial tcp [::1]:3100: connect: connection refused`.

## Split mode

For larger environments, run the HTTP server and queue worker separately.

### Why split

- isolated failure domains
- easier horizontal scaling
- worker-specific metrics and resource tuning

### Docker example

```bash showLineNumbers
docker run -d \
    --name vylux-server \
    --env-file .env \
    -p 3000:3000 \
    ghcr.io/carry0987/vylux:latest \
    --mode=server

docker run -d \
    --name vylux-worker \
    --env-file .env \
    -p 3001:3001 \
  -v vylux-scratch:/var/cache/vylux \
    ghcr.io/carry0987/vylux:latest \
    --mode=worker
```

If you expect large transcodes, also tune:

- `WORKER_CONCURRENCY` for the normal pool
- `LARGE_WORKER_CONCURRENCY` for the dedicated `video:large` pool
- the size and performance of the `/var/cache/vylux` scratch volume

### Kubernetes guidance

On Kubernetes, the recommended baseline is two Deployments using the same image:

- `vylux-server`
- `vylux-worker`

Both should share the same PostgreSQL, Redis, buckets, and secret material. In practice that means at least the same:

- image
- PostgreSQL and Redis connectivity
- S3 credentials and bucket names
- `API_KEY`, `WEBHOOK_SECRET`, `KEY_TOKEN_SECRET`, and `ENCRYPTION_KEY`

The Pod spec only needs different `args`:

```yml showLineNumbers
containers:
- name: vylux-server
  image: ghcr.io/carry0987/vylux:latest
  args: ["--mode=server"]
```

```yml showLineNumbers
containers:
- name: vylux-worker
  image: ghcr.io/carry0987/vylux:latest
  args: ["--mode=worker"]
```

### Probe guidance

Server:

- liveness: `GET /healthz`
- readiness: `GET /readyz`
- metrics: `GET /metrics`

Worker:

- liveness: `GET :WORKER_METRICS_PORT/healthz`
- metrics: `GET :WORKER_METRICS_PORT/metrics`

If you schedule large transcodes on Kubernetes, each worker Pod should have enough local scratch capacity for `/var/cache/vylux`. The `video:large` queue is intentionally separated so you can keep normal job throughput stable while limiting concurrent large transcodes.

Worker-only mode does not expose `/readyz`, so startup and readiness handling should be conservative at the platform layer.

## Bucket access model

- source store: read only access to `SOURCE_BUCKET` via `SOURCE_S3_*`
- media store: read/write access to `MEDIA_BUCKET` via `MEDIA_S3_*`

This keeps the privilege boundary clear and allows server and worker to share the same storage plane without local persistent volumes.

## Deployment checklist

- media tools installed and reachable in PATH or env config
- server and worker receive the same `SOURCE_S3_*`, `MEDIA_S3_*`, bucket names, and secrets
- Redis and PostgreSQL reachable from both processes
- health and metrics endpoints exposed to your platform

Also verify:

- `BASE_URL` points to the public media hostname and does not end with `/`
- server and worker receive the same secret material
- tracing and metrics are wired into your platform before load testing

## Anti-patterns

- keeping `all` mode in Kubernetes while trying to scale API and worker independently
- exposing `API_KEY`, `HMAC_SECRET`, or `KEY_TOKEN_SECRET` to public clients
- giving source and media buckets indistinguishable write permissions when they should represent different trust boundaries
