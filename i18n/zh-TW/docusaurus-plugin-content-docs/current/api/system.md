---
title: 系統端點
description: "健康檢查、readiness 與 Prometheus metrics 端點。"
---

# 系統端點

## 端點總覽

| Endpoint | 用途 | Auth |
| --- | --- | --- |
| `GET /healthz` | liveness probe | 無 |
| `GET /readyz` | readiness probe | 無 |
| `GET /metrics` | 主 HTTP process 的 Prometheus metrics | 無 |
| `GET :WORKER_METRICS_PORT/healthz` | worker-only process 的 liveness | 無 |
| `GET :WORKER_METRICS_PORT/metrics` | worker-only process 的 Prometheus metrics | 無 |

`WORKER_METRICS_PORT` 的預設值與驗證規則，請見 [設定](../operations/configuration)。

## `GET /healthz`

只要 process 活著就回：

```text
200 OK
OK
```

適合用於 liveness probe，不代表下游依賴已就緒。

## `GET /readyz`

`/readyz` 會在 2 秒 timeout 內依序檢查：

- PostgreSQL
- Redis
- source bucket
- media bucket

### curl 範例

```bash
curl -i http://localhost:3000/readyz
```

成功：

```text
200 OK
OK
```

失敗時會回 `503 Service Unavailable`，body 會指出是哪個依賴失敗，例如：

```text
not ready: redis: dial tcp 127.0.0.1:6381: connect: connection refused
```

## `GET /metrics`

主 HTTP process 的 metrics 由 `/metrics` 暴露。典型 metric families 包括：

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

### curl 範例

```bash
curl -s http://localhost:3000/metrics | rg '^vylux_'
```

## worker-only metrics listener

當你以 `--mode=worker` 啟動 Vylux，worker 會另外開一個輕量 listener：

- port：`WORKER_METRICS_PORT`，預設 `3001`
- endpoints：`/healthz`、`/metrics`
- `WORKER_METRICS_PORT=0` 時停用

這讓 K8s 或其他平台可以把 worker 跟 HTTP server 分開探測與抓取 metrics。

## 追蹤 header

除了 metrics 外，HTTP request 與 webhook/callback 也會攜帶 tracing 資訊：

- `traceparent`
- `tracestate`
- `X-Trace-ID`

`X-Trace-ID` 適合人工除錯與日誌關聯；真正的 trace context 仍以 W3C Trace Context headers 為主。
