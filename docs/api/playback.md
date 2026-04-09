---
title: Playback API
description: "Route layout, authorization model, and curl examples for `/stream/{hash}/*` and `/api/key/{hash}`."
---

# Playback API

## Endpoint overview

### Playlist and segments

- endpoint: `GET /stream/{hash}/*`
- auth: none
- purpose: proxy HLS playlists, init segments, and media segments from the media bucket

### Key delivery

- endpoint: `GET /api/key/{hash}`
- auth: `Authorization: Bearer {token}`
- purpose: return the 16-byte content key for encrypted HLS playback

## `GET /stream/{hash}/*`

`GET /stream/{hash}/*`

Common paths include:

```text
/stream/{hash}/master.m3u8
/stream/{hash}/audio/und_aac_2ch/playlist.m3u8
/stream/{hash}/video/r1080_h264/playlist.m3u8
/stream/{hash}/video/r1080_h264/seg_1.m4s
```

### curl examples

```bash showLineNumbers
MEDIA_HASH='movie-2026-04-01'

curl -s \
    "http://localhost:3000/stream/$MEDIA_HASH/master.m3u8"

curl -s \
    "http://localhost:3000/stream/$MEDIA_HASH/video/r720_h264/playlist.m3u8"

curl -I \
    "http://localhost:3000/stream/$MEDIA_HASH/video/r720_h264/init.mp4"
```

### Behavior notes

- proxies objects directly from the media bucket
- supports `.m3u8`, `.m4s`, `.mp4`, `.jpg`, and `.webp`
- rejects path traversal attempts containing `..`
- returns `404` when the object is missing
- currently sets `Cache-Control: public, max-age=31536000, immutable`
- returns `Access-Control-Allow-Origin: *`

:::tip Start playback from the master playlist
For public playback, the normal entrypoint is `/stream/{hash}/master.m3u8`. Treat the object keys stored in job results as backing storage paths, not as browser-facing URLs.
:::

## `GET /api/key/{hash}`

`GET /api/key/{hash}`

This endpoint does not use `X-API-Key`. It only accepts:

```text
Authorization: Bearer {token}
```

:::warning Do not move playback secrets into the browser
`/api/key/{hash}` is intentionally separated from `X-API-Key`. Keep `KEY_TOKEN_SECRET` in a trusted backend or auth service, and never place Bearer tokens into playlist URLs or segment URLs.
:::

For the exact meaning of `KEY_TOKEN_SECRET` and related playback configuration such as `BASE_URL`, see [Configuration](../operations/configuration).

### Token format

The Bearer token contains two base64url fragments:

```text
base64url({"hash":"...","exp":<unix_timestamp>}).base64url(HMAC-SHA256(payload_b64, KEY_TOKEN_SECRET))
```

The signature covers the base64url payload string, not the raw JSON bytes.

### shell token generation example

```bash showLineNumbers
MEDIA_HASH='movie-2026-04-01'
KEY_TOKEN_SECRET='replace-with-key-token-secret'

PAYLOAD="$(jq -cn \
    --arg hash "$MEDIA_HASH" \
    --argjson exp "$(($(date +%s) + 3600))" \
    '{hash: $hash, exp: $exp}')"

PAYLOAD_B64="$(printf '%s' "$PAYLOAD" \
    | openssl base64 -A \
    | tr '+/' '-_' \
    | tr -d '=')"

SIG_B64="$(printf '%s' "$PAYLOAD_B64" \
    | openssl dgst -sha256 -mac HMAC -macopt "key:$KEY_TOKEN_SECRET" -binary \
    | openssl base64 -A \
    | tr '+/' '-_' \
    | tr -d '=')"

TOKEN="$PAYLOAD_B64.$SIG_B64"
```

### curl examples

```bash showLineNumbers
curl -i "http://localhost:3000/api/key/$MEDIA_HASH"

curl -s \
    -H "Authorization: Bearer $TOKEN" \
    "http://localhost:3000/api/key/$MEDIA_HASH" \
    | wc -c
```

On success, the last command should print `16` because the endpoint returns the raw 16-byte content key.

### Response semantics

| Status | Meaning |
| --- | --- |
| `200` | token verified and the content key was returned as `application/octet-stream` |
| `401` | missing `Authorization: Bearer ...` header |
| `403` | invalid signature, expired token, or hash mismatch |
| `404` | no encryption key exists for the media hash |
| `500` | unwrap failure or other internal error |

This endpoint also uses a Redis-backed rate limit. The current default is 120 requests per minute, keyed by Bearer token or remote IP.

### Where token issuance should live

In a typical deployment, your application or an internal auth service should mint the Bearer token after it has already decided that the current user may watch the requested media.

The usual flow is:

1. the client asks your application to play media `hash=X`
2. your application authenticates the caller and checks authorization
3. your application returns the public playlist URL `/stream/X/master.m3u8`
4. your application also returns a short-lived Bearer token whose payload contains `hash=X`
5. the player attaches that token only on `/api/key/X` requests

Do not move `KEY_TOKEN_SECRET` into browser code, and do not place the token into playlist or segment URLs.

If you want a cleaner separation of concerns, the playback token may come from a dedicated internal auth service rather than the same backend that serves your application API.
