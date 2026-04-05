---
title: System Endpoints
description: "Health, readiness, and Prometheus metrics endpoints."
---

# System Endpoints

## Endpoint overview

| Endpoint | Purpose | Auth |
| --- | --- | --- |
| `GET /healthz` | liveness probe | none |
| `GET /readyz` | readiness probe | none |
| `GET /metrics` | Prometheus metrics for the main HTTP process | none |
| `GET :WORKER_METRICS_PORT/healthz` | liveness for worker-only mode | none |
| `GET :WORKER_METRICS_PORT/metrics` | Prometheus metrics for worker-only mode | none |

For the defaults and validation rules of `WORKER_METRICS_PORT`, see [Configuration](../operations/configuration).

## `GET /healthz`

As long as the process is alive, this endpoint returns:

```text
200 OK
OK
```

Use it for liveness, not dependency readiness.

## `GET /readyz`

`/readyz` checks all critical dependencies within a 2-second timeout:

- PostgreSQL
- Redis
- source bucket
- media bucket

### curl example

```bash
curl -i http://localhost:3000/readyz
```

Success:

```text
200 OK
OK
```

Failures return `503 Service Unavailable` with a short plain-text explanation such as:

```text
not ready: redis: dial tcp 127.0.0.1:6381: connect: connection refused
```

## `GET /metrics`

The main HTTP process exposes Prometheus metrics at `/metrics`. The most useful metric families include:

- `vylux_http_requests_total`
- `vylux_http_request_duration_seconds`
- `vylux_image_cache_events_total`
- `vylux_image_results_total`
- `vylux_image_errors_total`
- `vylux_worker_tasks_total`
- `vylux_worker_task_duration_seconds`
- `vylux_readiness_failures_total`
- `vylux_queue_tasks`
- `vylux_queue_metrics_sync_failures_total`

### curl example

```bash
curl -s http://localhost:3000/metrics | rg '^vylux_'
```

## Worker-only metrics listener

When Vylux runs in `--mode=worker`, it starts a lightweight listener on `WORKER_METRICS_PORT`:

- default port: `3001`
- endpoints: `/healthz` and `/metrics`
- set `WORKER_METRICS_PORT=0` to disable it

This is useful when server and worker run as separate deployments and need separate probes and scraping targets.

## Tracing headers

Beyond metrics, Vylux also propagates tracing through:

- `traceparent`
- `tracestate`
- `X-Trace-ID`

`X-Trace-ID` is mainly for operator-visible logs and manual correlation. The authoritative trace context is the W3C Trace Context headers.
