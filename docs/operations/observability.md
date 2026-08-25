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

If you want to inspect end-to-end traces from the HTTP request into worker execution, use a minimal collector plus Jaeger stack. The key point is that the collector must load a real config file and forward OTLP traces to Jaeger; simply exposing `4317` and `4318` is not enough.

### docker-compose example

```yml showLineNumbers
services:
  jaeger:
    image: jaegertracing/all-in-one:1.76.0
    tmpfs:
      - /tmp
    restart: unless-stopped
    environment:
      COLLECTOR_OTLP_ENABLED: true
    ports:
      - 16686:16686

  otel-collector:
    image: otel/opentelemetry-collector-contrib:0.148.0
    volumes:
      - ./otel-collector.yml:/etc/otelcol-contrib/config.yaml:ro
    command: ["--config=/etc/otelcol-contrib/config.yaml"]
    restart: unless-stopped
    depends_on:
      - jaeger
    ports:
      - 4317:4317
      - 4318:4318
      - 13133:13133
```

The collector must mount a config file into the container. A practical local filename is `otel-collector.yml` at the repo root.

### `otel-collector.yml`

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

extensions:
  health_check:
    endpoint: 0.0.0.0:13133

service:
  extensions: [health_check]
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [debug, otlp/jaeger]
```

### What each block does

- `receivers.otlp` opens OTLP gRPC on `4317` and OTLP HTTP on `4318`
- `processors.batch` groups spans before export so the collector does not forward them one-by-one
- `exporters.debug` prints received spans to the collector logs, which is the fastest way to verify that Vylux is actually exporting
- `exporters.otlp/jaeger` forwards traces to Jaeger over OTLP gRPC on the internal Docker network
- `extensions.health_check` exposes the collector health endpoint on `13133`
- `service.pipelines.traces` wires the trace receiver, processor, and exporters together; without this pipeline, Jaeger will stay empty even though the collector container is up

### Choosing the right OTLP endpoint

Use the OTLP endpoint that matches where the Vylux process itself runs:

- if Vylux runs inside Docker Compose, use `OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318`
- if Vylux runs on the host while collector runs in Docker, use `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`

That is why a typical setup uses:

- `.env` for container-to-container addresses such as `http://otel-collector:4318`
- `.env.local` for host-to-container addresses such as `http://localhost:4318`

### Validation flow

1. Start Jaeger and the collector with `otel-collector.yml` mounted into the collector container.
2. Set `OTEL_EXPORTER_OTLP_ENDPOINT` to the correct OTLP HTTP address for both server and worker.
3. Submit a `POST /api/audio/jobs` or `POST /api/video/jobs` request, or hit another traced HTTP route.
4. Capture the `X-Trace-ID` from the HTTP response headers or logs.
5. Check the collector logs first. The `debug` exporter should print spans.
6. Open `http://localhost:16686` and search for service `vylux` or paste the trace ID directly.

:::tip The collector must load a real config file
If you run the collector through Docker Compose, mount the config file into the container and start it with `--config=...`. Exposing ports alone is not enough to forward traces to Jaeger.
:::

### Example local verification commands

```bash showLineNumbers
docker compose -f docker-compose.dev.yml up -d
docker compose -f docker-compose.dev.yml logs -f otel-collector
curl -i http://localhost:3100/healthz
```

If traces still do not appear in Jaeger after a media request:

1. confirm Vylux actually loaded a non-empty `OTEL_EXPORTER_OTLP_ENDPOINT`
2. confirm the collector logs show spans through the `debug` exporter
3. confirm the collector config file was mounted to `/etc/otelcol-contrib/config.yaml`
4. confirm the collector is started with `--config=/etc/otelcol-contrib/config.yaml`
5. confirm Vylux is using the correct host name for where it runs: `otel-collector` inside Compose, `localhost` on the host
6. confirm you are opening the Jaeger UI on `16686`, not trying to send traces there

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
If `cloudflared` logs show `dial tcp [::1]:3000` or `127.0.0.1:3000`, the tunnel origin is pointed at `localhost` inside the tunnel container. Use `http://app:<PORT>` instead.
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
