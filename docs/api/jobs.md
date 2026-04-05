---
title: Jobs API
description: "HTTP APIs for creating, querying, retrying, and receiving callbacks for asynchronous media jobs, including practical schemas and curl examples."
---

# Jobs API

All job-management endpoints live under `/api/*` and require:

```text
X-API-Key: {internal_api_key}
```

For the exact meaning of `API_KEY`, `SOURCE_S3_*`, `MEDIA_S3_*`, bucket names, and related runtime settings used by these examples, see [Configuration](../operations/configuration).

## Auth and rate limits

- `POST /api/jobs`, `GET /api/jobs/{id}`, and `POST /api/jobs/{id}/retry` require `X-API-Key`
- `POST /api/jobs` and retry requests currently use a Redis-backed fixed-window rate limit of 30 requests per minute per API key

## `POST /api/jobs`

Create a new asynchronous job. If an equivalent request already exists and is still active, Vylux will return the existing job or the completed result instead of enqueuing a duplicate.

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

### Field reference

| Field | Required | Description |
| --- | --- | --- |
| `type` | yes | `image:thumbnail`, `video:cover`, `video:preview`, `video:transcode`, or `video:full` |
| `hash` | yes | stable media identifier chosen by the caller |
| `source` | yes | source object key in the configured source bucket |
| `options` | no | schema depends on `type`; unknown fields are rejected |
| `callback_url` | no | optional webhook destination for final job state |

`source_bucket` is not part of the request body. Vylux chooses the source bucket from runtime configuration, and unknown fields are rejected.

### Video source preflight checks

Before Vylux accepts `video:transcode` or `video:full`, it performs a source preflight check:

- verify that the source object exists in the configured source bucket
- fetch the object size from storage
- reject the request if the object exceeds `MAX_FILE_SIZE`
- use the measured size to route the job to either `default` or `video:large`

This means large-file routing is based on the real source object size, not on a caller-provided hint.

### `options` by job type

#### `image:thumbnail`

```json
{
    "outputs": [
        {"variant": "thumb", "width": 300, "format": "webp"},
        {"variant": "large", "width": 1280, "format": "jpg"}
    ]
}
```

If `outputs` is omitted, Vylux defaults to a single `thumb` variant at `300w webp`.

#### `video:cover`

```json
{
    "timestamp_sec": 1
}
```

#### `video:preview`

```json
{
    "start_sec": 2,
    "duration": 3,
    "width": 480,
    "fps": 10,
    "format": "webp"
}
```

`format` supports `webp` and `gif`.

#### `video:transcode`

```json
{
    "encrypt": true
}
```

#### `video:full`

`video:full` must use the nested schema. Legacy flat options are rejected.

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

### curl example

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

| Status | Meaning |
| --- | --- |
| `202 Accepted` | a new job was created and queued |
| `200 OK` | idempotency hit; returns the existing job or existing result |
| `400 Bad Request` | invalid JSON, unsupported job type, invalid options schema, or missing source object for video jobs |
| `413 Request Entity Too Large` | video source exceeds `MAX_FILE_SIZE` |
| `500 Internal Server Error` | enqueue or persistence failure |

A new job usually returns:

```json
{
    "job_id": "25b0dd17-9ef4-4512-baa4-5d80d2a55b41",
    "hash": "movie-2026-04-01",
    "status": "queued"
}
```

An already completed idempotency hit may return:

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

Query job state, progress, errors, and final results.

### curl example

```bash showLineNumbers
curl -s \
    -H "X-API-Key: $API_KEY" \
    "$BASE_URL/api/jobs/$JOB_ID"
```

### Response fields

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

Typical transcode result payload:

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
        }
    },
    "created_at": "2026-04-01T08:00:00Z",
    "updated_at": "2026-04-01T08:00:21Z"
}
```

`video:full` uses a workflow-oriented `results` payload with `stages`, `artifacts`, and `retry_plan`.

### Turning result fields into public URLs

Many result fields are storage keys, not browser-ready public URLs.

Use the following mapping when wiring Vylux into your application:

| Result field | Meaning | Typical public-facing action |
| --- | --- | --- |
| `results.key` for generated image assets | media-bucket object key | sign a `/thumb/{sig}/{encoded_key}` URL |
| `results.artifacts.cover.key` | cover image object key in the media bucket | sign a `/thumb/{sig}/{encoded_key}` URL |
| `results.artifacts.preview.key` | preview image object key in the media bucket | sign a `/thumb/{sig}/{encoded_key}` URL |
| `results.streaming.master_playlist` | HLS master playlist object key in the media bucket | expose `/stream/{hash}/master.m3u8` |
| `results.encryption.key_endpoint` | public key endpoint for encrypted playback | send a Bearer token only when the player requests it |

In other words:

- image-like derived assets normally become `/thumb` URLs
- HLS playback normally starts from `/stream/{hash}/master.m3u8`
- encrypted playback adds Bearer-token access to `/api/key/{hash}`, not to playlist or segment URLs

For a cross-endpoint walkthrough, see [Integration Guide](../integration-guide).

## `POST /api/jobs/{id}/retry`

Only failed jobs can be retried.

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

If the source job is not `failed`, Vylux returns `409 Conflict`.

## Callback webhook

When `callback_url` is provided, Vylux sends a `POST` webhook after completion or failure with:

- `Content-Type: application/json`
- `X-Signature: sha256=<hex>`
- `traceparent` / `tracestate`
- `X-Trace-ID`

Payload example:

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

Webhook delivery retries up to 5 times with exponential backoff. If all attempts fail, `callback_status` becomes `callback_failed`.

Practical callback expectations:

- each callback attempt uses a 10-second timeout
- any HTTP `2xx` response is treated as delivery success
- upstream applications should acknowledge quickly and move database updates or fan-out work into their own async path

That keeps callback retries focused on actual delivery failures instead of slow downstream business logic.

## Validation notes

- idempotency is based on `type + hash + source + canonicalized options`
- `video:full` runs as one workflow in one worker handler rather than a parent/child job graph
