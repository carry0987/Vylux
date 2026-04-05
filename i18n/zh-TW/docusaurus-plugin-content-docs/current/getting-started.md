---
title: 快速開始
description: "用最短路徑在本機啟動 Vylux、準備依賴、建立第一個 job 並驗證健康狀態。"
---

# 快速開始

## 先決條件

- Go 1.26+
- Docker 與 Docker Compose
- `curl`
- 供來源物件與輸出物件使用的 S3-compatible storage，例如 RustFS、R2 或 S3

如果你直接使用專案提供的 Docker image，runtime 內已包含 `ffmpeg`、`vips` 與 `packager`；若以 `go run` 在 host 上開發，則需要自己安裝 FFmpeg 與 Shaka Packager。

## 本機開發建議流程

### 1. 啟動基礎設施

```bash
docker compose -f docker-compose.dev.yml up -d
```

這會啟動：

- PostgreSQL：`localhost:5434`
- Redis：`localhost:6381`
- RustFS S3 API：`localhost:9002`
- RustFS Console：`localhost:9003`

### 2. 準備環境變數

```bash
cp .env.example .env.local
```

完整的環境變數說明、驗證規則與 secret 建議，請見 [設定](./operations/configuration)。

若你用 `docker-compose.dev.yml` 跑基礎設施，至少要把下列值改成對應的 localhost：

```ini showLineNumbers
DATABASE_URL=postgres://myuser:mypassword@localhost:5434/mydb
REDIS_URL=redis://localhost:6381
SOURCE_S3_ENDPOINT=http://localhost:9002
SOURCE_S3_REGION=auto
SOURCE_BUCKET=app-bucket
MEDIA_S3_ENDPOINT=http://localhost:9002
MEDIA_S3_REGION=auto
MEDIA_BUCKET=media-bucket
BASE_URL=http://localhost:3000
```

必填且最容易漏掉的設定組如下：

- `DATABASE_URL`
- `REDIS_URL`
- `SOURCE_S3_ENDPOINT`
- `SOURCE_S3_ACCESS_KEY`
- `SOURCE_S3_SECRET_KEY`
- `SOURCE_BUCKET`
- `MEDIA_S3_ENDPOINT`
- `MEDIA_S3_ACCESS_KEY`
- `MEDIA_S3_SECRET_KEY`
- `MEDIA_BUCKET`
- `HMAC_SECRET`
- `WEBHOOK_SECRET`
- `API_KEY`
- `KEY_TOKEN_SECRET`
- `ENCRYPTION_KEY`
- `FFMPEG_PATH`
- `SHAKA_PACKAGER_PATH`

可用 `openssl` 直接產生 secrets：

```bash showLineNumbers
cat >> .env.local <<EOF
HMAC_SECRET=$(openssl rand -hex 32)
API_KEY=$(openssl rand -hex 32)
WEBHOOK_SECRET=$(openssl rand -hex 32)
KEY_TOKEN_SECRET=$(openssl rand -hex 16)
ENCRYPTION_KEY=$(openssl rand -hex 32)
EOF
```

### 3. 建立 storage buckets 並放入測試素材

Vylux 不會幫你自動建立 `SOURCE_BUCKET` 與 `MEDIA_BUCKET`。在本機最少需要：

- source bucket：由 `SOURCE_BUCKET` 與 `SOURCE_S3_*` 指定，供 `source` 欄位讀取原始物件
- media bucket：由 `MEDIA_BUCKET` 與 `MEDIA_S3_*` 指定，供圖片快取、thumbnail、cover、preview、HLS 輸出寫入

若 source 與 media 都落在同一個 RustFS instance 或同一個 S3 服務，仍要把兩組 storage 設定都明確填好。Vylux 不會從 source 設定自動回推出 media 設定。

若你使用的是 S3-compatible storage，Vylux 會在寫入衍生物件時附帶 CRC32C upload checksum。這在目前支援的 AWS S3、Cloudflare R2 與 RustFS 配置下預期可正常運作；若你替換成其他 S3-compatible 服務，請先驗證其對 checksum header 的支援。

請先上傳至少一個可測試的來源檔，例如：

- 圖片：`uploads/sample.jpg`
- 影片：`uploads/sample.mp4`

### 4. 啟動服務

```bash
go run ./cmd/vylux
```

或拆成兩個進程：

```bash showLineNumbers
go run ./cmd/vylux --mode=server
go run ./cmd/vylux --mode=worker
```

### 5. 驗證服務是否可用

先檢查 liveness、readiness 與 metrics：

```bash showLineNumbers
curl -i http://localhost:3000/healthz
curl -i http://localhost:3000/readyz
curl -s http://localhost:3000/metrics | rg '^vylux_'
```

如果你以 `--mode=worker` 單獨啟動 worker，還可以檢查：

```bash showLineNumbers
curl -i http://localhost:3001/healthz
curl -s http://localhost:3001/metrics | rg '^vylux_'
```

## 最小驗證順序

最小可行的 API 驗證順序如下：

1. 建立一個 preview job

```bash showLineNumbers
BASE_URL='http://localhost:3000'
API_KEY='replace-with-api-key'

curl -s \
    -X POST "$BASE_URL/api/jobs" \
    -H 'Content-Type: application/json' \
    -H "X-API-Key: $API_KEY" \
    -d '{
        "type": "video:preview",
        "hash": "quickstart-preview-sample",
        "source": "uploads/sample.mp4",
        "options": {
            "start_sec": 1,
            "duration": 3,
            "width": 480,
            "fps": 12,
            "format": "webp"
        }
    }'
```

2. 查詢 job 狀態直到 `completed` 或 `failed`

```bash showLineNumbers
curl -s \
    -H "X-API-Key: $API_KEY" \
    http://localhost:3000/api/jobs/<job-id>
```

3. 確認產生的媒體資產可被存取

- 若是 `preview`，檢查 `results.key`
- 若是 `transcode`，檢查 `results.streaming.master_playlist`

做到這裡時，不要把 storage key 直接當成最終對外 URL：

- 若 job 回傳的是 cover、preview、thumbnail 這類 media-bucket key，應先轉成已簽名的 `/thumb/{sig}/{encoded_key}` URL 再提供給瀏覽器
- 若 job 回傳的是串流結果，對外播放入口應使用 `/stream/{hash}/master.m3u8`，而不是 raw `master_playlist` storage key
- 若開啟加密播放，還需要額外產生 `/api/key/{hash}` 用的 Bearer token，且只在 key 請求上附加

完整的 job 結果到對外 URL 映射，請看 [整合導覽](./integration-guide)。

發布前至少應覆蓋這三組 smoke test：

- `video:preview` with `gif`
- `video:preview` with `webp`
- `video:transcode` with `encrypt=true`

## 成功啟動後應可觀察到

- `GET /healthz` 回 `200`
- `GET /readyz` 可檢查 PostgreSQL、Redis 與 bucket 是否就緒
- `GET /metrics` 回 Prometheus metrics
- `POST /api/jobs` 可建立非同步處理工作

下一步通常是：

- 看 [整合導覽](./integration-guide) 先理清 URL、簽名與播放責任邊界
- 看 [設定](./operations/configuration) 校正所有 env vars
- 看 [工作 API](./api/jobs) 補齊 callback 與 retry 接入
- 看 [可觀測性](./operations/observability) 把 tracing 與 metrics 接進你的本機或正式環境
