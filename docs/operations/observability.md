---
title: Observability
description: "Health checks, Prometheus metrics, OpenTelemetry tracing, and a practical local Jaeger validation flow."
---

# Observability

## Health endpoints

- `GET /healthz`: process liveness
- `GET /readyz`: readiness across PostgreSQL, Redis, and buckets
- `GET /metrics`: Prometheus metrics for the main server

Any readiness failure increments `vylux_readiness_failures_total{check=...}`.

:::tip Probe in this order
When diagnosing a fresh deployment, check `/healthz` first, then `/readyz`, and only then look at application behavior. That separates process startup failures from dependency failures quickly.
:::

## Worker metrics

When running worker-only mode, Vylux can expose a separate listener on `WORKER_METRICS_PORT` for worker metrics and basic health checks.

For the exact defaults and validation rules of `WORKER_METRICS_PORT` and `OTEL_EXPORTER_OTLP_ENDPOINT`, see [Configuration](./configuration).

## Prometheus metric families

The most useful metric families today are:

| Metric | Meaning |
| --- | --- |
| `vylux_http_requests_total` | HTTP request count by method, route, and status |
| `vylux_http_request_duration_seconds` | HTTP request latency |
| `vylux_image_cache_events_total` | image cache hits and misses by layer |
| `vylux_image_results_total` | top-level image request outcomes |
| `vylux_image_errors_total` | image failures by stage and status |
| `vylux_worker_tasks_total` | worker task attempts by task type and result |
| `vylux_worker_task_duration_seconds` | worker task latency |
| `vylux_readiness_failures_total` | readiness failures by dependency check |
| `vylux_queue_tasks` | queue depth by queue and state |
| `vylux_queue_metrics_sync_failures_total` | failures while refreshing queue-depth metrics |

## Tracing

OpenTelemetry tracing is integrated across HTTP requests and queued media tasks. The system propagates trace context into async workflows so job execution is visible as part of the same trace tree.

### Relevant headers

- `traceparent`
- `tracestate`
- `X-Trace-ID`

`X-Trace-ID` is a convenience header for manual debugging and log correlation. The authoritative context still comes from the W3C trace headers.

### Enabling export

Set:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
```

or another OTLP HTTP endpoint. If the variable is empty, spans are still created locally but are not exported.

## Local Jaeger validation

If you want to inspect end-to-end traces from the HTTP request into worker execution, use a minimal collector plus Jaeger stack. The following inline example captures the important details that were previously kept in local helper files, so the published docs remain self-contained.

### docker-compose example

```yml showLineNumbers
services:
  jaeger:
    image: jaegertracing/all-in-one:1.76.0
    restart: unless-stopped
    environment:
      COLLECTOR_OTLP_ENABLED: true
    ports:
      - 16686:16686

  otel-collector:
    image: otel/opentelemetry-collector-contrib:0.148.0
    command: [--config=/etc/otelcol/otelcol.yaml]
    restart: unless-stopped
    depends_on:
      - jaeger
    ports:
      - 4317:4317
      - 4318:4318
      - 13133:13133
```

Minimal collector trace pipeline:

```yml showLineNumbers
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

processors:
  batch:

exporters:
  debug:
    verbosity: normal
  otlp/jaeger:
    endpoint: jaeger:4317
    tls:
      insecure: true

service:
  extensions: [health_check]
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [debug, otlp/jaeger]
```

### Validation flow

1. Start Jaeger and the collector.
2. Set `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318` for both server and worker.
3. Submit a `POST /api/jobs`, ideally `video:transcode` or `video:full`.
4. Capture the `X-Trace-ID` from the HTTP response headers or logs.
5. Open `http://localhost:16686` and search for service `vylux` or paste the trace ID directly.

## What to watch

- readiness failures
- queue depth and task latency
- image cache behavior
- media job success and failure trends

## Troubleshooting hints

:::danger `localhost` health checks fail from the host
If `curl http://localhost:<PORT>/healthz` returns connection refused, the container port is usually not published to the host, or you are testing the wrong host port.
:::

:::warning Cloudflare Tunnel returns `502`
If `cloudflared` logs show `dial tcp [::1]:3100` or `127.0.0.1:3100`, the tunnel origin is pointed at `localhost` inside the tunnel container. Use `http://vylux:<PORT>` instead.
:::

:::info `/healthz` is green but `/readyz` is red
This usually means the Vylux process is alive but PostgreSQL, Redis, or bucket reachability is broken.
:::

:::note Worker metrics are empty
Confirm that Vylux is actually running in `--mode=worker` and that `WORKER_METRICS_PORT` is not `0`.
:::

:::tip Jaeger shows no traces
Verify that the exporter points to an OTLP HTTP endpoint, not the Jaeger UI port.
:::
