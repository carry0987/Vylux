---
title: Testing
description: "Unit tests, integration tests, and recommended release smoke-test flows."
sidebar_position: 2
---

# Testing

## Unit tests

```bash
go test -short ./...
```

## Full test suite

```bash
go test ./...
```

## Integration tests

```bash
go test -v ./tests/integration
```

## Manual smoke tests

The release-focused manual checks should cover:

- `video:preview` with `gif`
- `video:preview` with `webp`
- `video:transcode` with `encrypt=true`

For the full setup of `BASE_URL`, `API_KEY`, buckets, and secrets used by these smoke tests, see [Configuration](../operations/configuration).

## Suggested smoke-test flow

### `video:preview` with `gif`

```bash showLineNumbers
BASE_URL='http://localhost:3000'
API_KEY='replace-with-api-key'

curl -s \
    -X POST "$BASE_URL/api/jobs" \
    -H 'Content-Type: application/json' \
    -H "X-API-Key: $API_KEY" \
    -d '{
        "type": "video:preview",
        "hash": "smoke-preview-gif",
        "source": "uploads/sample.mp4",
        "options": {
            "start_sec": 1,
            "duration": 3,
            "width": 480,
            "fps": 12,
            "format": "gif"
        }
    }'
```

### `video:preview` with `webp`

Repeat the same flow with `format` set to `webp`, then confirm that `results.format` and the output key match.

### `video:transcode` with `encrypt=true`

```bash showLineNumbers
curl -s \
    -X POST "$BASE_URL/api/jobs" \
    -H 'Content-Type: application/json' \
    -H "X-API-Key: $API_KEY" \
    -d '{
        "type": "video:transcode",
        "hash": "smoke-transcode-encrypted",
        "source": "uploads/sample.mp4",
        "options": {
            "encrypt": true
        }
    }'
```

After completion, confirm at least:

- `results.streaming.encrypted == true`
- `results.streaming.master_playlist` exists
- `results.encryption.scheme == "cbcs"`
- a variant playlist contains `#EXT-X-KEY`
- `/api/key/{hash}` returns `401` without a token and 16 bytes with a valid token

## Recently validated scenarios

- portrait HLS ladder playback
- playlist `RESOLUTION` alignment with API results
- CBCS key delivery semantics for `401`, `403`, and `200`
- preview output generation for `gif` and `webp`

## Test data and fixtures

- unit tests are mainly under `internal/.../*_test.go`
- integration tests are mainly under `tests/integration`
- shared test helpers are mainly under `tests/testutil`

## Release gate

Before shipping, confirm:

- `go test ./...` passes
- non-encrypted HLS output plays correctly
- encrypted playback returns `#EXT-X-KEY` and enforces key authorization
- preview jobs generate the expected artifacts

If you also validate observability, run the smoke-test flow together with the [Observability](../operations/observability) Jaeger checklist so you confirm that HTTP and worker spans are connected.
