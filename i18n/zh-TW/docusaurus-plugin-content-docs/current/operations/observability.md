---
title: 可觀測性
description: "健康檢查、Prometheus metrics、OpenTelemetry tracing 與本機 Jaeger 驗證流程。"
---

# 可觀測性

## 端點（HTTP）

- `GET /healthz`: liveness probe；process 活著時回 `{"status":"ok"}`
- `GET /readyz`: PostgreSQL、Redis 與 buckets 的 readiness probe；成功回 `{"status":"ok"}`，失敗回帶有 `checks[]` 的結構化 JSON
- `GET /metrics`: Prometheus metrics

`/readyz` 目前會檢查：

- PostgreSQL
- Redis
- source bucket
- media bucket

任何一項失敗都會回 `503`，並增加 `vylux_readiness_failures_total{check=...}`。

:::tip 建議的 probe 排查順序
剛部署完成時，先看 `/healthz`，再看 `/readyz`，最後才看應用層行為。這樣可以先把 process 啟動問題和依賴問題切開。
:::

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

若你想在本機檢查 request -> worker -> callback 的完整 trace，可使用最小 collector + Jaeger 組合。重點不只是 expose `4317` / `4318`，而是 collector 必須真的載入一份 config file，並把收到的 OTLP trace 轉送到 Jaeger。

### docker-compose 範例

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

collector 需要掛載實際的 config file。對本機開發來說，最直觀的檔名就是 repo root 下的 `otel-collector.yml`。

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

### 每一段在做什麼

- `receivers.otlp`：開 OTLP gRPC `4317` 與 OTLP HTTP `4318`
- `processors.batch`：把 spans 做 batch 後再往下送，避免每個 span 都單獨 export
- `exporters.debug`：把收到的 spans 印到 collector log，這是本機排查最快的觀察點
- `exporters.otlp/jaeger`：把 traces 透過 OTLP gRPC 轉送到 Docker network 內的 Jaeger
- `extensions.health_check`：提供 collector 的健康檢查端點 `13133`
- `service.pipelines.traces`：把 receiver、processor、exporter 串起來；如果沒有這段 pipeline，collector container 會活著，但 Jaeger 仍然會是空的

### OTLP endpoint 要怎麼選

要依 Vylux process 本身跑在哪裡來決定：

- 若 Vylux 跑在 Docker Compose 裡，請用 `OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318`
- 若 Vylux 跑在 host、collector 跑在 Docker 裡，請用 `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`

這也是為什麼常見做法是：

- `.env` 放 container-to-container 位址，例如 `http://otel-collector:4318`
- `.env.local` 放 host-to-container 位址，例如 `http://localhost:4318`

### 驗證流程

1. 啟動 Jaeger 與 collector，並確認 collector 有掛載 `otel-collector.yml`
2. 對 server 與 worker 都設定正確的 `OTEL_EXPORTER_OTLP_ENDPOINT`
3. 建立一個 `POST /api/audio/jobs` 或 `POST /api/video/jobs`，或呼叫其他會產生 trace 的 HTTP route
4. 從 HTTP 回應 header 或日誌拿到 `X-Trace-ID`
5. 先看 collector log，確認 `debug` exporter 有印出 spans
6. 再到 Jaeger UI `http://localhost:16686` 搜尋 service `vylux` 或直接貼上 trace ID

:::tip collector 必須真的載入 config 檔
如果你透過 Docker Compose 跑 collector，請把 config file mount 進容器，並用 `--config=...` 啟動。只有開 port 並不足以把 trace 轉送到 Jaeger。
:::

### 本機驗證命令範例

```bash showLineNumbers
docker compose -f docker-compose.dev.yml up -d
docker compose -f docker-compose.dev.yml logs -f otel-collector
curl -i http://localhost:3100/healthz
```

如果 Jaeger 還是看不到 traces，請按這個順序檢查：

1. 確認 Vylux 真的有載入非空的 `OTEL_EXPORTER_OTLP_ENDPOINT`
2. 確認 collector log 裡的 `debug` exporter 看得到 spans
3. 確認 `otel-collector.yml` 真的被 mount 到 `/etc/otelcol-contrib/config.yaml`
4. 確認 collector 的啟動命令真的有 `--config=/etc/otelcol-contrib/config.yaml`
5. 確認 Vylux 用的是對應執行環境的 host 名稱：Compose 內用 `otel-collector`，host 上跑則用 `localhost`
6. 確認你打開的是 Jaeger UI `16686`，而不是把 trace 送去 UI port

## 建議監看的訊號

- `/readyz` 是否開始持續失敗
- queue depth 是否長時間累積在 `pending` 或 `retry`
- `vylux_worker_task_duration_seconds` 是否出現明顯長尾
- `vylux_image_errors_total` 是否因 source storage 或 decode 問題升高
- webhook callback 是否常見 `callback_failed`

## 排障提示

:::danger 從 host 打 `localhost` 的 health check 失敗
如果 `curl http://localhost:<PORT>/healthz` 出現 connection refused，通常是容器 port 沒有發佈到 host，或是你打錯了 host port。
:::

:::warning Cloudflare Tunnel 回 `502`
如果 `cloudflared` 日誌出現 `dial tcp [::1]:3000` 或 `127.0.0.1:3000`，代表 tunnel origin 指到了 tunnel 容器裡的 `localhost`；此時應改成 `http://app:<PORT>`。
:::

:::info `/healthz` 是綠的，但 `/readyz` 是紅的
這通常表示 Vylux process 還活著，但 PostgreSQL、Redis 或 bucket 連線出了問題。
:::

:::note worker metrics 是空的
確認是否真的以 `--mode=worker` 啟動，且 `WORKER_METRICS_PORT` 不為 `0`。
:::

:::tip Jaeger 看不到 traces
先確認 exporter endpoint 指向的是 HTTP OTLP，而不是 Jaeger UI port。
:::
