---
title: 工作 API
description: "建立、查詢、回呼與補跑非同步媒體工作的 HTTP API，含實際 schema 與 curl 範例。"
---

# 工作 API

所有 job 管理端點都走 `/api/*`，並要求：

```text
X-API-Key: {internal_api_key}
```

:::warning 這是 internal-only 的管理面
`X-API-Key` 應只提供給可信任的呼叫端，例如你的 backend、控制平面或內部工具。不要把它暴露給瀏覽器或第三方客戶端。
:::

這些範例所依賴的 `API_KEY`、`SOURCE_S3_*`、`MEDIA_S3_*`、bucket 名稱與其他相關執行設定，請見 [設定](../operations/configuration)。

## 認證與速率限制

- `POST /api/jobs` 與 `POST /api/jobs/:id/retry` 需要 `X-API-Key`
- `GET /api/jobs/:id` 也需要 `X-API-Key`
- `POST /api/jobs` 與 retry 目前套用 Redis-based fixed-window rate limit：每個 API key 每分鐘 30 次

## `POST /api/jobs`

建立新的非同步工作。若相同請求已存在且尚未失敗，系統會走 idempotency 路徑，直接回傳既有工作或既有結果。

### Request body

```json
{
    "type": "video:preview",
    "hash": "preview-sample-001",
    "source": "uploads/sample.mp4",
    "options": {
        "start_sec": 1,
        "duration": 3,
        "width": 480,
        "fps": 12,
        "format": "webp"
    },
    "callback_url": "https://app.example.com/internal/media/callback"
}
```

### 欄位說明

| 欄位 | 必填 | 說明 |
| --- | --- | --- |
| `type` | 是 | `image:thumbnail`、`video:cover`、`video:preview`、`video:transcode`、`video:full` |
| `hash` | 是 | 上游用來識別這份媒體的穩定 ID；不一定要是 SHA-256，但應具備穩定性 |
| `source` | 是 | 已配置 source bucket 中的 object key |
| `options` | 否 | 依 `type` 而定；未知欄位會被拒絕 |
| `callback_url` | 否 | 任務完成或失敗後，Vylux 會以 webhook POST 回呼 |

`source_bucket` 已不是 request body 的合法欄位。來源 bucket 由 runtime 設定決定，而未知欄位會被直接拒絕。

### 影片來源的前置檢查

在接受 `video:transcode` 與 `video:full` 前，Vylux 會先做來源檢查：

- 確認來源 object 確實存在於目前配置的 source bucket
- 從 storage 取得實際 object size
- 若超過 `MAX_FILE_SIZE`，直接拒絕請求
- 依實際大小把工作路由到 `default` 或 `video:large`

也就是說，大檔路由依據的是 storage 內的真實檔案大小，而不是呼叫端自行聲明的值。

### 各 job type 的 `options`

### `image:thumbnail`

```json
{
    "outputs": [
        {"variant": "thumb", "width": 300, "format": "webp"},
        {"variant": "large", "width": 1280, "format": "jpg"}
    ]
}
```

若未提供 `outputs`，系統會預設產生一個 `thumb` 變體：`300w webp`。

### `video:cover`

```json
{
    "timestamp_sec": 1
}
```

### `video:preview`

```json
{
    "start_sec": 2,
    "duration": 3,
    "width": 480,
    "fps": 10,
    "format": "webp"
}
```

`format` 支援 `webp` 與 `gif`。

### `video:transcode`

```json
{
    "encrypt": true
}
```

### `video:full`

`video:full` 必須使用巢狀 schema，舊版 flat options 會被拒絕：

```json
{
    "cover": {
        "timestamp_sec": 1
    },
    "preview": {
        "start_sec": 1,
        "duration": 3,
        "width": 480,
        "fps": 12,
        "format": "webp"
    },
    "transcode": {
        "encrypt": true
    }
}
```

### curl 範例

```bash showLineNumbers
BASE_URL='http://localhost:3000'
API_KEY='replace-with-api-key'

curl -s \
    -X POST "$BASE_URL/api/jobs" \
    -H 'Content-Type: application/json' \
    -H "X-API-Key: $API_KEY" \
    -d '{
        "type": "video:full",
        "hash": "movie-2026-04-01",
        "source": "uploads/sample.mp4",
        "options": {
            "cover": {"timestamp_sec": 1},
            "preview": {
                "start_sec": 1,
                "duration": 3,
                "width": 480,
                "fps": 12,
                "format": "webp"
            },
            "transcode": {"encrypt": true}
        },
        "callback_url": "https://app.example.com/internal/media/callback"
    }'
```

### Response semantics

| 狀態碼 | 代表情況 |
| --- | --- |
| `202 Accepted` | 新 job 已建立並入列 |
| `200 OK` | 命中 idempotency；回傳既有 job 或既有結果 |
| `400 Bad Request` | JSON 錯誤、type 不支援、`options` schema 不合法，或影片來源 object 不存在 |
| `413 Request Entity Too Large` | 影片來源超過 `MAX_FILE_SIZE` |
| `500 Internal Server Error` | enqueue 或資料庫流程失敗 |

新 job 通常回傳：

```json
{
    "job_id": "25b0dd17-9ef4-4512-baa4-5d80d2a55b41",
    "hash": "movie-2026-04-01",
    "status": "queued"
}
```

若該請求已完成，可能直接回傳：

```json
{
    "hash": "movie-2026-04-01",
    "status": "completed",
    "results": {
        "streaming": {
            "protocol": "hls",
            "container": "cmaf",
            "encrypted": true,
            "master_playlist": "videos/mo/movie-2026-04-01/master.m3u8"
        }
    }
}
```

## `GET /api/jobs/{id}`

查詢工作狀態、進度、錯誤與結果。

### curl 範例

```bash showLineNumbers
curl -s \
    -H "X-API-Key: $API_KEY" \
    "$BASE_URL/api/jobs/$JOB_ID"
```

### 回傳欄位

- `job_id`
- `type`
- `hash`
- `status`
- `callback_status`
- `progress`
- `retry_of_job_id`
- `error`
- `results`
- `created_at`
- `updated_at`

### Transcode result

典型回應：

```json
{
    "job_id": "25b0dd17-9ef4-4512-baa4-5d80d2a55b41",
    "type": "video:transcode",
    "hash": "movie-2026-04-01",
    "status": "completed",
    "callback_status": "sent",
    "progress": 95,
    "results": {
        "streaming": {
            "protocol": "hls",
            "container": "cmaf",
            "encrypted": true,
            "master_playlist": "videos/mo/movie-2026-04-01/master.m3u8",
            "default_audio_track_id": "audio_und_aac_2ch"
        },
        "audio_tracks": [
            {
                "id": "audio_und_aac_2ch",
                "role": "main",
                "language": "und",
                "codec": "mp4a.40.2",
                "channels": 2,
                "bitrate": 128000,
                "playlist": "videos/mo/movie-2026-04-01/audio/und_aac_2ch/playlist.m3u8",
                "init": "videos/mo/movie-2026-04-01/audio/und_aac_2ch/init.mp4",
                "segment_count": 12
            }
        ],
        "video_tracks": [
            {
                "id": "r720_h264",
                "codec": "h264",
                "width": 1280,
                "height": 720,
                "bitrate": 1800000,
                "playlist": "videos/mo/movie-2026-04-01/video/r720_h264/playlist.m3u8",
                "init": "videos/mo/movie-2026-04-01/video/r720_h264/init.mp4",
                "segment_count": 12,
                "audio_track_ids": ["audio_und_aac_2ch"]
            }
        ],
        "encryption": {
            "scheme": "cbcs",
            "kid": "ab12cd34ef56...",
            "key_endpoint": "https://media.example.com/api/key/movie-2026-04-01"
        }
    },
    "created_at": "2026-04-01T08:00:00Z",
    "updated_at": "2026-04-01T08:00:21Z"
}
```

### Full workflow result

對 `video:full`，`results` 會改為 workflow 結構：

```json
{
    "stages": {
        "source": {"status": "ready"},
        "cover": {"status": "ready"},
        "preview": {"status": "ready"},
        "transcode": {"status": "failed", "error": "upload HLS: ...", "retryable": true}
    },
    "artifacts": {
        "cover": {"key": "videos/mo/movie-2026-04-01/cover.jpg", "format": "jpg", "size": 182345},
        "preview": {"key": "videos/mo/movie-2026-04-01/preview.webp", "format": "webp", "size": 98432}
    },
    "retry_plan": {
        "allowed": true,
        "strategy": "retry_tasks",
        "job_types": ["video:transcode"],
        "stages": ["transcode"],
        "reason": "transcode upload failed"
    }
}
```

### 如何把結果欄位轉成對外 URL

:::info 很多 job 結果是 storage key，不是可直接給瀏覽器的 URL
如果你直接把 `results.key`、`results.artifacts.cover.key` 或 `results.streaming.master_playlist` 暴露給 client，通常會把錯誤的抽象層暴露出去。請先把它們轉成 `/thumb`、`/stream` 或帶 token 的 `/api/key` 使用方式。
:::

很多 `results` 欄位回傳的是 storage key，不是可以直接丟給瀏覽器的 public URL。

實際整合時，建議用下面這個映射：

| Result 欄位 | 代表什麼 | 對外通常應該怎麼做 |
| --- | --- | --- |
| 圖片類輸出的 `results.key` | media bucket 內的 object key | 簽一個 `/thumb/{sig}/{encoded_key}` URL |
| `results.artifacts.cover.key` | media bucket 內的 cover 圖片 key | 簽一個 `/thumb/{sig}/{encoded_key}` URL |
| `results.artifacts.preview.key` | media bucket 內的 preview 圖片 key | 簽一個 `/thumb/{sig}/{encoded_key}` URL |
| `results.streaming.master_playlist` | media bucket 內 HLS master playlist 的 object key | 對外公開 `/stream/{hash}/master.m3u8` |
| `results.encryption.key_endpoint` | 加密播放的 public key endpoint | 只在播放器請求它時附上 Bearer token |

也就是說：

- 圖片類衍生資產通常要轉成 `/thumb` URL
- HLS 播放通常從 `/stream/{hash}/master.m3u8` 開始
- 加密播放只額外對 `/api/key/{hash}` 增加 Bearer token，不要把 token 放進 playlist 或 segment URL

如果你想看跨端點的整體整合路徑，請讀 [整合導覽](../integration-guide)。

## `POST /api/jobs/{id}/retry`

只有 `failed` job 可以補跑。

:::warning Retry 不是通用的重新執行按鈕
如果原 job 不在 `failed` 狀態，Vylux 會回 `409 Conflict`，而不是幫你建立新的 retry 鏈。
:::

### curl 範例

```bash showLineNumbers
curl -s \
    -X POST \
    -H "X-API-Key: $API_KEY" \
    "$BASE_URL/api/jobs/$JOB_ID/retry"
```

### 回應範例

```json
{
    "source_job_id": "25b0dd17-9ef4-4512-baa4-5d80d2a55b41",
    "strategy": "retry_tasks",
    "jobs": [
        {
            "job_id": "e02e7d95-6db8-48e0-b5f2-6e12fd7cb056",
            "type": "video:transcode",
            "status": "queued",
            "retry_of_job_id": "25b0dd17-9ef4-4512-baa4-5d80d2a55b41"
        }
    ]
}
```

若原 job 不是 `failed`，會回 `409 Conflict`。

## callback webhook

當 `callback_url` 非空時，Vylux 會在工作完成或失敗後送出 `POST` webhook：

- `Content-Type: application/json`
- `X-Signature: sha256=<hex>`
- `traceparent` / `tracestate`
- `X-Trace-ID`

payload 範例：

```json
{
    "job_id": "25b0dd17-9ef4-4512-baa4-5d80d2a55b41",
    "type": "video:transcode",
    "hash": "movie-2026-04-01",
    "status": "completed",
    "results": {
        "streaming": {
            "protocol": "hls",
            "container": "cmaf",
            "encrypted": true,
            "master_playlist": "videos/mo/movie-2026-04-01/master.m3u8"
        }
    }
}
```

Webhook delivery 最多會重試 5 次，採 exponential backoff；若最終仍失敗，job 的 `callback_status` 會記為 `callback_failed`。

:::tip 把 callback 當成快速確認機制
盡量快速回 `2xx`，再把 DB 更新、推播或其他後續處理移到你自己的非同步流程。這樣 Vylux 的重試才會聚焦在真正的 delivery 問題，而不是被下游延遲拖慢。
:::

實務上建議把 callback 當成快速確認機制：

- 每次 callback 請求 timeout 為 10 秒
- 只要上游回 `HTTP 2xx` 就視為送達成功
- 上游收到後應先快速回應，再把 DB 更新、推播或其他後續處理移到自己的非同步流程

這樣 callback 重試才會聚焦在真正的 delivery failure，而不是被下游業務邏輯過慢拖累。

## 設計備註

- idempotency 不是只看 `hash`，而是看 `type + hash + source + canonicalized options`
- `video:full` 會在單一 worker workflow 內協調 cover、preview、transcode，不再拆成父子工作
