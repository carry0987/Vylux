---
title: 部署
description: "Docker、單進程、server/worker 拆分與 K8s 的部署建議，包含目前映像的 mode 預設行為。"
---

# 部署

## Docker image 的啟動語義

目前 Docker image 使用：

```dockerfile
ENTRYPOINT ["vylux"]
CMD ["--mode=all"]
```

代表：

- 映像的預設行為是 `all`
- 但 `--mode=all` 只是預設參數，不是硬限制
- 在 Docker、Compose、K8s 都可以覆蓋 `args` 改成 `--mode=server` 或 `--mode=worker`

這樣做的目的，是讓本機與最小部署可以開箱即用；到了正式環境，再依角色拆分。

## 本機開發

最常見的本機組合是：

- PostgreSQL via Docker
- Redis via Docker
- RustFS 或其他 S3-compatible storage
- Vylux host process，或直接跑 `all` mode image

```bash showLineNumbers
docker compose -f docker-compose.dev.yml up -d
go run ./cmd/vylux
```

這些部署範例背後對應的完整環境變數、預設值與驗證規則，請見 [設定](./configuration)。

## 執行模式

- `MODE=all`: 單進程同時啟動 HTTP 與 worker
- `MODE=server`: 僅服務 API 與 proxy 層
- `MODE=worker`: 僅處理 queue 任務

若你直接用 CLI flag，會覆蓋 `MODE` 環境變數：

```bash showLineNumbers
./bin/vylux --mode=server
./bin/vylux --mode=worker
```

## 單進程部署

當你的流量與任務量還小，最簡單的做法是直接跑 `all`：

```bash showLineNumbers
docker run --rm \
    --env-file .env \
    -p 3000:3000 \
  -v vylux-scratch:/var/cache/vylux \
    ghcr.io/carry0987/vylux:latest
```

適合情境：

- 開發與 staging
- 單台 VM 或低流量環境
- 還不需要獨立水平擴展 worker

image 會固定設定 `TMPDIR=/var/cache/vylux`，並把 `/var/cache/vylux` 宣告成 Docker volume。若你不明確掛載，Docker 仍會幫你建立 anonymous volume；正式環境建議改成顯式 named volume 或平台管理磁碟，避免 scratch 空間使用量變得不透明。

## Docker Compose 部署

repo 目前提供的 `docker-compose.yml` 也是用單一 `vylux` service 跑預設 `all` mode，並同時啟動：

- PostgreSQL
- Redis
- 可選的 Cloudflare Tunnel

這個版本的特點：

- `vylux` healthcheck 走 `GET /healthz`
- `/var/cache/vylux` 掛為獨立 scratch volume
- image 內的 `TMPDIR` 也指向 `/var/cache/vylux`，避免大型暫存資料散落在其他 temp 路徑
- 不再需要獨立的 key tmpfs，因為 raw encryption key 直接透過 Shaka Packager CLI 參數傳遞，不會先落成磁碟檔案

最小啟動命令：

```bash
docker compose up -d --build
```

## server / worker 拆分部署

正式環境更推薦把 `server` 與 `worker` 拆開，各自用同一個 image 啟動不同 args。

### 為什麼拆分

- API 與 worker 的 CPU / memory 壓力型態不同
- worker 需要獨立擴展處理 FFmpeg / libvips / packager 任務
- HTTP server 與 queue 消費可以分開做 probe、autoscaling 與故障隔離

### Docker 範例

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

若你的部署會處理大型轉碼，至少要一起考慮：

- `WORKER_CONCURRENCY`：一般任務池併發度
- `LARGE_WORKER_CONCURRENCY`：`video:large` 專用池併發度
- `/var/cache/vylux` 的容量與 IO 性能

### K8s 形態建議

K8s 下建議至少拆成兩個 Deployment：

- `vylux-server`
- `vylux-worker`

兩者共用：

- 同一個 image
- 同一組 PostgreSQL、Redis、S3 credentials
- 同一組 `API_KEY`、`WEBHOOK_SECRET`、`KEY_TOKEN_SECRET`、`ENCRYPTION_KEY`

只需要在 Pod spec 覆蓋 args：

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

### probe 建議

server：

- liveness: `GET /healthz`
- readiness: `GET /readyz`
- metrics: `GET /metrics`

worker：

- liveness: `GET :WORKER_METRICS_PORT/healthz`
- metrics: `GET :WORKER_METRICS_PORT/metrics`

若在 Kubernetes 上執行大型轉碼，每個 worker Pod 都應有足夠的 `/var/cache/vylux` 本地 scratch 容量。`video:large` 佇列被獨立出來，就是為了在限制大型任務併發的同時，不拖慢一般工作吞吐。

worker-only mode 沒有 `/readyz` HTTP endpoint，因此 readiness 通常可用啟動成功 + Redis / DB 外部依賴監控來補足，或在平台上以較保守的 startupProbe 控制。

## 儲存桶存取模型

- source store: 透過 `SOURCE_S3_*` 對 `SOURCE_BUCKET` 做唯讀存取
- media store: 透過 `MEDIA_S3_*` 對 `MEDIA_BUCKET` 做讀寫存取

這個模型讓上游應用與 Vylux 的權限邊界更清楚，也讓 server / worker 共享相同資料平面而無需本地持久卷。

## 部署檢查清單

- `DATABASE_URL`、`REDIS_URL`、`SOURCE_S3_*`、`MEDIA_S3_*`、bucket 名稱已正確設定
- `BASE_URL` 指向對外公開的媒體域名，且不帶 trailing slash
- image 內或 runtime 內可找到 `ffmpeg`、`vips`、`packager`
- server 與 worker 使用相同 secret material
- source bucket 與 media bucket 均可被 probe 與 runtime 存取
- metrics 與 tracing 已接到你的監控系統

## 不建議的做法

- 在 K8s 長期維持 `all` mode 再嘗試各自擴展 API 與 worker
- 讓前端或公開客戶端持有 `API_KEY`、`HMAC_SECRET` 或 `KEY_TOKEN_SECRET`
- 讓 source bucket 與 media bucket 混用相同寫入權限而失去權限邊界
