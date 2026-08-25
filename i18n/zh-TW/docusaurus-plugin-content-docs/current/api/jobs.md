---
title: 工作 API
description: "建立、查詢、補跑與接收 callback 的非同步音訊 / 影片工作 HTTP API。"
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

## 路由模型

- `POST /api/audio/jobs` 建立音訊工作
- `POST /api/video/jobs` 建立影片工作
- `GET /api/jobs/{id}` 查詢工作狀態，不分媒體領域
- `POST /api/jobs/{id}/retry` 補跑已失敗的工作，不分媒體領域

:::note 舊的 generic create route 已退役
`POST /api/jobs` 不再是 public create endpoint。新的音訊與影片工作必須使用上面的 domain route。
:::

## 認證與速率限制

- 建立路由與 lifecycle 路由都需要 `X-API-Key`
- create 與 retry 請求目前套用 Redis-based fixed-window rate limit：每個 API key 每分鐘 30 次

## `POST /api/audio/jobs`

建立新的非同步音訊工作。若相同請求已存在且尚未失敗，系統會走 idempotency 路徑，直接回傳既有工作或既有結果。

### Request body

```json
{
  "source": {
    "hash": "album-track-001",
    "key": "uploads/sample.flac"
  },
  "pipeline": {
    "package": {
      "hls": {
        "enabled": true,
        "profile": "stream_aac_standard"
      }
    },
    "downloads": [
      {"profile": "download_mp3_high"},
      {"profile": "download_flac_standard"}
    ],
    "waveform": {
      "enabled": true,
      "profile": "waveform_standard",
      "bins": 2048
    }
  },
  "delivery": {
    "callback_url": "https://app.example.com/internal/media/callback"
  }
}
```

### 欄位說明

| 欄位 | 必填 | 說明 |
| --- | --- | --- |
| `source.hash` | 是 | 上游為這份媒體選定的穩定識別值 |
| `source.key` | 是 | 已配置 source bucket 中的 object key |
| `pipeline.package.hls.profile` | 否 | 啟用 HLS 時，目前支援 `stream_aac_standard` |
| `pipeline.package.hls.encryption.enabled` | 否 | 設為 `true` 時產出受保護 audio HLS，並抑制 MP3/FLAC download output |
| `pipeline.downloads[].profile` | 否 | 目前支援 `download_mp3_high` 與 `download_flac_standard` |
| `pipeline.waveform.profile` | 否 | 目前支援 `waveform_standard` |
| `delivery.callback_url` | 否 | 任務完成或失敗後，Vylux 會以 webhook POST 回呼 |

音訊建立請求不再使用舊的 `type + options` 契約，也不需要 `asset_type` discriminator。

### Source 前置檢查

在接受音訊工作前，Vylux 會先：

- 確認來源 object 確實存在於目前配置的 source bucket
- 從 storage 取得實際 object size
- 若超過 `MAX_FILE_SIZE`，直接拒絕請求
- 依實際大小做任務路由

### curl 範例

```bash showLineNumbers
BASE_URL='http://localhost:3000'
API_KEY='replace-with-api-key'

curl -s \
    -X POST "$BASE_URL/api/audio/jobs" \
    -H 'Content-Type: application/json' \
    -H "X-API-Key: $API_KEY" \
    -d '{
        "source": {
            "hash": "album-track-001",
            "key": "uploads/sample.flac"
        },
        "pipeline": {
            "package": {
                "hls": {
                    "enabled": true,
                    "profile": "stream_aac_standard"
                }
            },
            "downloads": [
                {"profile": "download_mp3_high"},
                {"profile": "download_flac_standard"}
            ],
            "waveform": {
                "enabled": true,
                "profile": "waveform_standard",
                "bins": 2048
            }
        },
        "delivery": {
            "callback_url": "https://app.example.com/internal/media/callback"
        }
    }'
```

## `POST /api/video/jobs`

建立新的非同步影片工作。

### Request body

```json
{
  "source": {
    "hash": "movie-2026-04-01",
    "key": "uploads/sample.mp4"
  },
  "pipeline": {
    "cover": {
      "enabled": true,
      "timestamp_sec": 1
    },
    "preview": {
      "enabled": true,
      "start_sec": 1,
      "duration": 3,
      "width": 480,
      "fps": 12,
      "format": "webp"
    },
    "package": {
      "hls": {
        "enabled": true,
        "profile": "stream_video_standard",
        "encryption": {
          "enabled": true
        }
      }
    }
  },
  "delivery": {
    "callback_url": "https://app.example.com/internal/media/callback"
  }
}
```

### 支援的 deliverable 組合

目前 public 契約支援：

- 僅 `cover`
- 僅 `preview`
- 僅 `package.hls`
- `cover + preview + package.hls`

public API 不暴露內部 worker vocabulary，例如 `video:cover`、`video:preview`、`video:transcode`、`video:full`。

### Source 前置檢查

對 HLS 打包與完整影片處理請求，Vylux 也會在 enqueue 前做來源檢查，以便確認存在、量測實際大小、拒絕超限檔案，並在需要時把工作送往大檔 worker pool。

### curl 範例

```bash showLineNumbers
curl -s \
    -X POST "$BASE_URL/api/video/jobs" \
    -H 'Content-Type: application/json' \
    -H "X-API-Key: $API_KEY" \
    -d '{
        "source": {
            "hash": "movie-2026-04-01",
            "key": "uploads/sample.mp4"
        },
        "pipeline": {
            "cover": {"enabled": true, "timestamp_sec": 1},
            "preview": {
                "enabled": true,
                "start_sec": 1,
                "duration": 3,
                "width": 480,
                "fps": 12,
                "format": "webp"
            },
            "package": {
                "hls": {
                    "enabled": true,
                    "profile": "stream_video_standard",
                    "encryption": {"enabled": true}
                }
            }
        },
        "delivery": {
            "callback_url": "https://app.example.com/internal/media/callback"
        }
    }'
```

## Create response semantics

| 狀態碼 | 代表情況 |
| --- | --- |
| `202 Accepted` | 新 job 已建立並入列 |
| `200 OK` | 命中 idempotency；回傳既有 job 或既有結果 |
| `400 Bad Request` | JSON 錯誤、schema 不合法、deliverable 組合不支援，或來源 object 不存在 |
| `413 Request Entity Too Large` | 來源超過 `MAX_FILE_SIZE` |
| `500 Internal Server Error` | enqueue 或資料庫流程失敗 |

已退役的 generic `POST /api/jobs` route 不屬於這份 create contract，對外應視為不可用。

新 job 通常回傳：

```json
{
  "job_id": "25b0dd17-9ef4-4512-baa4-5d80d2a55b41",
  "hash": "movie-2026-04-01",
  "status": "queued"
}
```

## `GET /api/jobs/{id}`

查詢工作狀態、進度、錯誤與結果。

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

### 音訊結果範例

```json
{
  "job_id": "25b0dd17-9ef4-4512-baa4-5d80d2a55b41",
  "type": "audio:transcode",
  "hash": "album-track-001",
  "status": "completed",
  "callback_status": "sent",
  "progress": 100,
  "results": {
    "analysis": {
      "container": "flac",
      "duration_sec": 218.3
    },
    "streaming": {
      "protocol": "hls",
      "container": "cmaf",
      "encrypted": true,
      "master_playlist": "audio/al/album-track-001/hls/master.m3u8"
    },
    "encryption": {
      "scheme": "cbcs",
      "kid": "00112233445566778899aabbccddeeff",
      "key_endpoint": "https://media.example.com/api/key/3d4fda2e-5cf2-4a4a-8ebd-c9eb1d4e8f1f"
    },
    "downloads": [
      {"format": "mp3", "key": "audio/al/album-track-001/downloads/audio.mp3"},
      {"format": "flac", "key": "audio/al/album-track-001/downloads/audio.flac"}
    ],
    "waveform": {
      "key": "audio/al/album-track-001/waveform/waveform.json",
      "bins": 2048
    }
  }
}
```

### 影片結果補充

- `video:transcode` 回傳 transcode-oriented 的串流結果
- `video:full` 回傳 workflow-oriented 的 `results` payload，含 `stages`、`artifacts` 與 `retry_plan`

### 如何把結果欄位轉成對外 URL

:::info 很多 job 結果回的是 storage-backed route target，不是可直接給瀏覽器的 bucket URL
如果你直接把 `results.artifacts.cover.key` 或 `results.streaming.master_playlist` 暴露給 client，通常會把錯誤的抽象層暴露出去。請先把它們轉成 `/thumb`、`/stream` 或帶 token 的 `/api/key` 使用方式。
:::

| Result 欄位 | 代表什麼 | 對外通常應該怎麼做 |
| --- | --- | --- |
| `results.artifacts.cover.key` | media bucket 內的 cover key | 簽一個 `/thumb/{sig}/{encoded_key}` URL |
| `results.artifacts.preview.key` | media bucket 內的 preview key | 簽一個 `/thumb/{sig}/{encoded_key}` URL |
| `results.streaming.master_playlist` | HLS master playlist storage key | 影片對外走 `/stream/{hash}/master.m3u8`，音訊對外走 `/stream/{hash}/hls/master.m3u8` |
| `results.encryption.key_endpoint` | 加密播放的 public key endpoint | 只在播放器請求它時附上 Bearer token |

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

## callback payload

當 `callback_url` 非空時，Vylux 會在工作完成或失敗後送出 webhook。

payload 會包含：

- `job_id`
- `type`
- `hash`
- `status`
- 失敗時的 `error`
- 若有結果則附上 `results`
