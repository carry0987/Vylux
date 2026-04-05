---
title: 設定
description: "Vylux 的完整環境變數參考，包括用途、預設值、必要性與驗證規則。"
---

# 設定

Vylux 主要透過環境變數配置。實作上，CLI `--mode` 會覆蓋 `MODE` 環境變數，其餘設定由 process 啟動時讀入。

## 設定原則

- server 與 worker 若拆成兩個 process，除了 `MODE`、`PORT`、`WORKER_METRICS_PORT` 之外，其餘關鍵設定通常應保持一致
- 所有 secrets 都應透過部署平台的 secret 管理機制注入，不應硬編在映像或前端程式中
- `BASE_URL` 不可帶結尾斜線，因為它會參與 key delivery URL 組裝

## 核心執行設定

| 變數 | 必填 | 預設值 | 說明 |
| --- | --- | --- | --- |
| `PORT` | 否 | `3000` | HTTP server 監聽埠；只影響 server / all mode |
| `MODE` | 否 | `all` | `all`、`server`、`worker` |
| `BASE_URL` | 建議 | 空字串 | 對外公開網址，用於組裝加密播放的 key endpoint，例如 `https://media.example.com` |
| `LOG_LEVEL` | 否 | `INFO` | `DEBUG`、`INFO`、`WARN`、`ERROR` |

## 資料與佇列

| 變數 | 必填 | 預設值 | 說明 |
| --- | --- | --- | --- |
| `DATABASE_URL` | 是 | 無 | PostgreSQL DSN，必須以 `postgres://` 或 `postgresql://` 開頭 |
| `REDIS_URL` | 是 | 無 | Redis 連線字串，供 asynq queue、rate limit 與 worker 使用 |

## 物件儲存

| 變數 | 必填 | 預設值 | 說明 |
| --- | --- | --- | --- |
| `SOURCE_S3_ENDPOINT` | 是 | 無 | source storage 角色使用的 S3-compatible endpoint |
| `SOURCE_S3_ACCESS_KEY` | 是 | 無 | source storage 角色使用的 access key |
| `SOURCE_S3_SECRET_KEY` | 是 | 無 | source storage 角色使用的 secret key |
| `SOURCE_S3_REGION` | 否 | `auto` | source storage 的 region；R2 通常用 `auto` |
| `SOURCE_BUCKET` | 是 | 無 | 原始素材 bucket；Vylux 視為唯讀輸入 |
| `MEDIA_S3_ENDPOINT` | 是 | 無 | media storage 角色使用的 S3-compatible endpoint |
| `MEDIA_S3_ACCESS_KEY` | 是 | 無 | media storage 角色使用的 access key |
| `MEDIA_S3_SECRET_KEY` | 是 | 無 | media storage 角色使用的 secret key |
| `MEDIA_S3_REGION` | 否 | `auto` | media storage 的 region；R2 通常用 `auto` |
| `MEDIA_BUCKET` | 是 | 無 | 處理結果 bucket；Vylux 在此讀寫衍生資產 |

Vylux 在 S3-compatible `PutObject` 上傳時，會送出 `x-amz-checksum-algorithm: CRC32C` 來做物件完整性驗證。AWS S3、Cloudflare R2 與目前版本的 RustFS 預期都能正確處理；如果你部署的是較少見的 S3-compatible 後端，建議先確認 checksum header 支援情況再上線。

即使 source 與 media 指向同一個 R2 account、RustFS instance 或 S3 服務，也必須把 `SOURCE_S3_*` 與 `MEDIA_S3_*` 都明確設好。Vylux 不會自動從其中一組推導另一組。

## 安全與秘密值

| 變數 | 必填 | 預設值 | 說明 |
| --- | --- | --- | --- |
| `HMAC_SECRET` | 是 | 無 | 圖片與媒體投遞 URL 簽名 |
| `API_KEY` | 是 | 無 | 保護 `/api/*` 管理端點 |
| `WEBHOOK_SECRET` | 是 | 無 | webhook callback `X-Signature` 的 HMAC 秘鑰 |
| `KEY_TOKEN_SECRET` | 是 | 無 | `/api/key/{hash}` Bearer token 驗證秘鑰 |
| `ENCRYPTION_KEY` | 是 | 無 | 包裝資料庫中 stored content key 的 KEK |

### 快速生成 secrets

Vylux 常用的五個 secret 可以直接用 `openssl` 生成：

```bash showLineNumbers
# HMAC_SECRET
openssl rand -hex 32

# API_KEY
openssl rand -hex 32

# WEBHOOK_SECRET
openssl rand -hex 32

# KEY_TOKEN_SECRET
openssl rand -hex 16

# ENCRYPTION_KEY
openssl rand -hex 32
```

如果你要一次寫進 `.env`：

```bash showLineNumbers
cat >> .env <<EOF
HMAC_SECRET=$(openssl rand -hex 32)
API_KEY=$(openssl rand -hex 32)
WEBHOOK_SECRET=$(openssl rand -hex 32)
KEY_TOKEN_SECRET=$(openssl rand -hex 16)
ENCRYPTION_KEY=$(openssl rand -hex 32)
EOF
```

若系統沒有 `openssl`，可改用 `/dev/urandom`：

```bash showLineNumbers
head -c 32 /dev/urandom | xxd -p -c 64
head -c 16 /dev/urandom | xxd -p -c 32
```

### Secret 用途

| Variable | Bits | Purpose |
| --- | --- | --- |
| `HMAC_SECRET` | 256 | 簽署 `/img`、`/original` 等圖片相關 URL |
| `API_KEY` | 256 | 保護 `/api/*` 管理與工作端點 |
| `WEBHOOK_SECRET` | 256 | 簽署 webhook callback payload |
| `KEY_TOKEN_SECRET` | 128 | 簽署加密播放用的 key token |
| `ENCRYPTION_KEY` | 256 | 包裝並保護資料庫內儲存的 content key |

這五個值目前都要求是十六進位字串。`HMAC_SECRET`、`API_KEY`、`WEBHOOK_SECRET`、`ENCRYPTION_KEY` 建議至少 32 bytes；`KEY_TOKEN_SECRET` 建議至少 16 bytes。

## Worker、快取與媒體工具鏈

| 變數 | 必填 | 預設值 | 說明 |
| --- | --- | --- | --- |
| `WORKER_CONCURRENCY` | 否 | `10` | worker 併發度 |
| `LARGE_WORKER_CONCURRENCY` | 否 | `1` | 專供 `video:large` worker pool 使用的併發度 |
| `WORKER_METRICS_PORT` | 否 | `3001` | worker-only mode metrics listener；設 `0` 可關閉 |
| `LARGE_FILE_THRESHOLD` | 否 | `5368709120` | 大檔任務切到低優先度 queue 的門檻，單位 bytes |
| `MAX_FILE_SIZE` | 否 | `53687091200` | 可接受的最大來源檔案大小，單位 bytes |
| `CACHE_MAX_SIZE` | 否 | `1073741824` | 記憶體 LRU 圖片快取上限，單位 bytes |
| `FFMPEG_PATH` | 否 | `ffmpeg` | FFmpeg binary 路徑 |
| `SHAKA_PACKAGER_PATH` | 否 | `packager` | Shaka Packager binary 路徑 |

容器部署下，Vylux 會固定使用 `/var/cache/vylux` 作為大檔暫存工作區；如果你想把實際儲存位置放在別處，請把你偏好的 host path、named volume 或平台磁碟映射到容器內的 `/var/cache/vylux`。

## 超時與 observability

| 變數 | 必填 | 預設值 | 說明 |
| --- | --- | --- | --- |
| `SHUTDOWN_TIMEOUT` | 否 | `30s` | HTTP server graceful shutdown timeout |
| `WORKER_SHUTDOWN_TIMEOUT` | 否 | `10m` | worker graceful shutdown timeout |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | 否 | 空字串 | OTLP HTTP exporter endpoint，例如 `http://localhost:4318` 或 `https://otel.example.com/v1/traces` |

## Compose / 輔助基礎設施變數

下列變數不是 Vylux binary 直接讀取，而是 compose 或依賴服務會用到：

| 變數 | 用途 |
| --- | --- |
| `POSTGRES_USER` | compose 中 PostgreSQL container 初始化帳號 |
| `POSTGRES_PASSWORD` | compose 中 PostgreSQL container 初始化密碼 |
| `POSTGRES_DB` | compose 中 PostgreSQL container 初始化資料庫 |
| `TUNNEL_TOKEN` | Cloudflare Tunnel container 使用；留空即可停用 |

## 驗證規則與常見錯誤

- `MODE` 必須是 `all`、`server` 或 `worker`
- `PORT` 必須在 `1..65535`
- `WORKER_METRICS_PORT` 必須在 `0..65535`
- `WORKER_CONCURRENCY` 必須至少為 `1`
- `LARGE_WORKER_CONCURRENCY` 必須至少為 `1`
- `LARGE_FILE_THRESHOLD` 必須至少為 `1`
- `MAX_FILE_SIZE` 必須至少為 `1`
- `LARGE_FILE_THRESHOLD` 必須小於或等於 `MAX_FILE_SIZE`
- `BASE_URL` 不得以 `/` 結尾
- `DATABASE_URL` 若不是 `postgres://...` 或 `postgresql://...`，啟動時就會被拒絕

## R2 與本機 S3 提示

- Cloudflare R2：`SOURCE_S3_ENDPOINT` 與 `MEDIA_S3_ENDPOINT` 都設為 `https://<ACCOUNT_ID>.r2.cloudflarestorage.com`，並使用 `SOURCE_S3_REGION=auto` 與 `MEDIA_S3_REGION=auto`
- RustFS 本機測試：`SOURCE_S3_ENDPOINT` 與 `MEDIA_S3_ENDPOINT` 都設為 `http://localhost:9002`
- Vylux 會在 S3 物件上傳時啟用 CRC32C checksum；若改用其他 S3-like provider，請先驗證其對 checksum header 的相容性
- source bucket 建議給唯讀權限；media bucket 才給讀寫權限
