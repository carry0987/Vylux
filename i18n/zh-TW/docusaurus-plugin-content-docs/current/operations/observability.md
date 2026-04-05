---
title: 可觀測性
description: "健康檢查、Prometheus metrics、OpenTelemetry tracing 與本機 Jaeger 驗證流程。"
---

# 可觀測性

## 端點（HTTP）

- `GET /healthz`: liveness probe
- `GET /readyz`: readiness probe
- `GET /metrics`: Prometheus metrics

`/readyz` 目前會檢查：

- PostgreSQL
- Redis
- source bucket
- media bucket

任何一項失敗都會回 `503`，並增加 `vylux_readiness_failures_total{check=...}`。

## 工作程序指標

當 `MODE=worker` 時，Vylux 會另外啟一個輕量 HTTP listener：

- port: `WORKER_METRICS_PORT`，預設 `3001`
- endpoints: `/metrics`、`/healthz`

若設定 `WORKER_METRICS_PORT=0`，則不啟動這個 listener。

`WORKER_METRICS_PORT` 與 `OTEL_EXPORTER_OTLP_ENDPOINT` 的完整預設值與驗證規則，請見 [設定](./configuration)。

## Prometheus 指標族

目前最重要的 metrics families 包括：

| Metric | 說明 |
| --- | --- |
| `vylux_http_requests_total` | HTTP request 次數，依 method / route / status 分組 |
| `vylux_http_request_duration_seconds` | HTTP request latency |
| `vylux_image_cache_events_total` | 圖片快取命中與失敗，依 layer / result 分組 |
| `vylux_image_results_total` | 圖片請求最終結果，例如 `processed`、`memory_hit` |
| `vylux_image_errors_total` | 圖片請求錯誤，依 stage / status 分組 |
| `vylux_worker_tasks_total` | worker task 執行次數，依 task type / result 分組 |
| `vylux_worker_task_duration_seconds` | worker task latency |
| `vylux_readiness_failures_total` | readiness 失敗次數 |
| `vylux_queue_tasks` | 各 queue 在不同 state 的 task 數量 |
| `vylux_queue_metrics_sync_failures_total` | 抓取 queue 深度時的同步錯誤 |

## Tracing

Vylux 目前使用 OpenTelemetry，trace context 會從 HTTP request 傳到非同步 queue payload，再一路延續到 worker 任務與 webhook callback。

### 相關 header

- `traceparent`
- `tracestate`
- `X-Trace-ID`

`X-Trace-ID` 只是方便人工除錯的回應 / callback header；真正的 trace propagation 仍以 W3C headers 為主。

### 啟用方式

只要設定：

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
```

或正式環境中對應的 OTLP HTTP endpoint，Vylux 就會啟用 exporter。若此值留空，spans 仍會在 process 內建立，但不會送出。

## 本機 Jaeger 驗證

若你想在本機檢查 request -> worker -> callback 的完整 trace，可使用最小 collector + Jaeger 組合。下面這段已吸收目前本地輔助配置的關鍵內容，所以正式 docs 不依賴 repo 內的臨時 local 目錄。

### docker-compose 範例

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

collector config 的最小 traces pipeline：

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

### 驗證流程

1. 啟動 Jaeger 與 collector
2. 對 server 與 worker 都設定 `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`
3. 建立一個 `POST /api/jobs`，最好用 `video:transcode` 或 `video:full`
4. 從 HTTP 回應 header 或日誌拿到 `X-Trace-ID`
5. 到 Jaeger UI `http://localhost:16686` 搜尋 service `vylux` 或直接貼上 trace ID

## 建議監看的訊號

- `/readyz` 是否開始持續失敗
- queue depth 是否長時間累積在 `pending` 或 `retry`
- `vylux_worker_task_duration_seconds` 是否出現明顯長尾
- `vylux_image_errors_total` 是否因 source storage 或 decode 問題升高
- webhook callback 是否常見 `callback_failed`

## 排障提示

- `GET /healthz` 成功但 `/readyz` 失敗：通常是 PostgreSQL、Redis 或 buckets 無法連線
- worker metrics 空白：確認是否真的以 `--mode=worker` 啟動，且 `WORKER_METRICS_PORT` 不為 `0`
- Jaeger 看不到 trace：先確認 exporter endpoint 是 HTTP OTLP，而不是 Jaeger UI port
