---
title: Jobs API
description: "HTTP APIs for creating, querying, retrying, and receiving callbacks for asynchronous audio and video jobs."
---

# Jobs API

All job-management endpoints live under `/api/*` and require:

```text
X-API-Key: {internal_api_key}
```

:::warning Internal-only surface
`X-API-Key` is for trusted callers such as your backend, worker control plane, or internal tools. Do not expose it to browsers or third-party clients.
:::

For the exact meaning of `API_KEY`, `SOURCE_S3_*`, `MEDIA_S3_*`, bucket names, and related runtime settings used by these examples, see [Configuration](../operations/configuration).

## Route model

- `POST /api/audio/jobs` creates audio jobs
- `POST /api/video/jobs` creates video jobs
- `GET /api/jobs/{id}` reads job status regardless of domain
- `POST /api/jobs/{id}/retry` retries an existing failed job regardless of domain

:::note The old generic create route is retired
`POST /api/jobs` is no longer a public create endpoint. New audio and video work must use the domain routes above.
:::

## Auth and rate limits

- create routes and lifecycle routes require `X-API-Key`
- create and retry requests currently use a Redis-backed fixed-window rate limit of 30 requests per minute per API key

## Common HTTP error envelope

Authentication, authorization, rate-limit, and route-level HTTP errors on `/api/*` are returned as JSON instead of plain text:

```json
{"message":"Unauthorized"}
```

When the fixed-window limiter rejects a request, Vylux returns `429 Too Many Requests` and also sets `Retry-After` to the remaining window in seconds.

Typical rate-limit response:

```http
HTTP/1.1 429 Too Many Requests
Content-Type: application/json
Retry-After: 60

{"message":"Too Many Requests"}
```

## `POST /api/audio/jobs`

Create a new asynchronous audio job. If an equivalent request already exists and is still active, Vylux returns the existing job or completed result instead of enqueuing a duplicate.

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

### Field reference

| Field | Required | Description |
| --- | --- | --- |
| `source.hash` | yes | stable media identifier chosen by the caller |
| `source.key` | yes | source object key in the configured source bucket |
| `pipeline.package.hls.profile` | no | currently `stream_aac_standard` when HLS packaging is enabled |
| `pipeline.package.hls.encryption.enabled` | no | when `true`, generate protected audio HLS and suppress MP3/FLAC download outputs |
| `pipeline.downloads[].profile` | no | currently `download_mp3_high` and `download_flac_standard` |
| `pipeline.waveform.profile` | no | currently `waveform_standard` |
| `delivery.callback_url` | no | optional webhook destination for final job state |

Audio create requests do not use the old `type + options` contract and do not require an `asset_type` discriminator.

### Source preflight checks

Before Vylux accepts the audio job, it performs a source preflight check:

- verify that the source object exists in the configured source bucket
- fetch the object size from storage
- reject the request if the object exceeds `MAX_FILE_SIZE`
- use the measured size to route the task when needed

### curl example

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

Create a new asynchronous video job.

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

### Supported deliverable combinations

The current public contract supports:

- `cover` only
- `preview` only
- `package.hls` only
- `cover + preview + package.hls`

The public API does not expose internal worker vocabulary such as `video:cover`, `video:preview`, `video:transcode`, or `video:full`.

### Source preflight checks

For HLS packaging and full-process video requests, Vylux also performs source preflight checks before enqueueing so it can confirm existence, measure actual size, reject over-limit files, and route large work.

### curl example

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

| Status | Meaning |
| --- | --- |
| `202 Accepted` | a new job was created and queued |
| `200 OK` | idempotency hit; returns the existing job or existing result |
| `401 Unauthorized` | missing or invalid `X-API-Key` |
| `400 Bad Request` | invalid JSON, unsupported schema, unsupported deliverable combination, or missing source object |
| `413 Request Entity Too Large` | source exceeds `MAX_FILE_SIZE` |
| `429 Too Many Requests` | Redis-backed fixed-window limit exceeded; response body uses the JSON error envelope |
| `500 Internal Server Error` | enqueue or persistence failure |

The retired generic `POST /api/jobs` route is not part of this contract and should be treated as unavailable. If an older client still calls it, expect a JSON `404` response with `{"message":"Not Found"}` rather than a plain-text body.

A new job usually returns:

```json
{
  "job_id": "25b0dd17-9ef4-4512-baa4-5d80d2a55b41",
  "hash": "movie-2026-04-01",
  "status": "queued"
}
```

## `GET /api/jobs/{id}`

Query job state, progress, errors, and final results.

### Common response fields

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

### Audio result example

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

### Video result notes

- `video:transcode` returns a transcode-oriented streaming payload
- `video:full` returns a workflow-oriented `results` payload with `stages`, `artifacts`, and `retry_plan`

### Turning result fields into public URLs

:::info Job results often contain storage-backed route targets, not browser-ready bucket URLs
If you expose `results.artifacts.cover.key` or `results.streaming.master_playlist` directly to clients, you will usually leak the wrong abstraction. Convert them into `/thumb`, `/stream`, or tokenized `/api/key` usage first.
:::

Use the following mapping when wiring Vylux into your application:

| Result field | Meaning | Typical public-facing action |
| --- | --- | --- |
| `results.artifacts.cover.key` | cover image object key in the media bucket | sign a `/thumb/{sig}/{encoded_key}` URL |
| `results.artifacts.preview.key` | preview image object key in the media bucket | sign a `/thumb/{sig}/{encoded_key}` URL |
| `results.streaming.master_playlist` | HLS master playlist storage key | expose `/stream/{hash}/master.m3u8` for video or `/stream/{hash}/hls/master.m3u8` for audio |
| `results.encryption.key_endpoint` | public key endpoint for encrypted playback | send a Bearer token only when the player requests it |

## `POST /api/jobs/{id}/retry`

Only failed jobs can be retried.

:::warning Retry is not a generic rerun button
If the source job is not in `failed` state, Vylux returns `409 Conflict` rather than creating a duplicate retry chain.
:::

### curl example

```bash showLineNumbers
curl -s \
    -X POST \
    -H "X-API-Key: $API_KEY" \
    "$BASE_URL/api/jobs/$JOB_ID/retry"
```

### Response example

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

## Callback payloads

When `callback_url` is provided, Vylux sends a webhook after completion or failure.

The payload includes:

- `job_id`
- `type`
- `hash`
- `status`
- `error` when failed
- `results` when available
