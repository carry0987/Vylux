---
title: Integration Guide
description: "A practical map from Vylux job results and object keys to the public URLs, signed requests, and playback flows your application must assemble."
---

# Integration Guide

This page connects the individual API documents into one integration-oriented view.

The short version is:

- Vylux processes media and exposes delivery endpoints
- your application decides who may access that media
- your application signs image URLs and mints playback Bearer tokens
- job results often contain storage keys, not final public URLs

If you already know the signing rules, use this page as the map of which endpoint belongs to which use case.

:::tip The core integration rule
Job results often give you storage keys. Your application is usually responsible for turning those keys into signed `/thumb` URLs, public `/stream/{hash}` entrypoints, or tokenized `/api/key/{hash}` access.
:::

## What Vylux does and does not do

Vylux is responsible for:

- processing source media into derived outputs
- serving signed image and media-delivery endpoints
- serving HLS playlists and segments under `/stream/{hash}/*`
- validating Bearer tokens for `/api/key/{hash}`

Vylux is not responsible for:

- deciding which end user is allowed to see a given asset
- minting your business-layer auth tokens or session cookies
- storing your authorization policy
- exposing raw bucket URLs directly to browsers

That separation is intentional. Vylux owns media processing and media delivery semantics. Your application owns access control.

## Secret and signing responsibilities

| Secret | Used for | Who should hold it |
| --- | --- | --- |
| `API_KEY` | internal `/api/*` management endpoints such as `/api/jobs` | your backend or internal tooling only |
| `HMAC_SECRET` | signing `/img`, `/original`, and `/thumb` URLs | your backend or auth/signing service only |
| `KEY_TOKEN_SECRET` | signing Bearer tokens for `/api/key/{hash}` | your backend or auth/signing service only |

Do not expose any of these values to browsers, mobile apps, or other public clients.

## Pick the right endpoint

| Use case | Endpoint shape | Notes |
| --- | --- | --- |
| dynamically resize or reformat a source image | `/img/{sig}/{opts}/{encoded_source}.{format}` | signed with `HMAC_SECRET` |
| allow controlled download or display of the original source file | `/original/{sig}/{encoded_key}` | signed with `HMAC_SECRET` |
| expose an already-generated thumbnail, cover, preview, or other media-bucket image asset | `/thumb/{sig}/{encoded_key}` | signed with `HMAC_SECRET` using the `thumb/` signing domain |
| play HLS output | `/stream/{hash}/master.m3u8` | no auth header on playlist and segment requests |
| fetch an encrypted content key during playback | `/api/key/{hash}` with `Authorization: Bearer {token}` | token signed with `KEY_TOKEN_SECRET` |

## How job results become user-facing URLs

Job results often return object keys such as `videos/.../cover.jpg` or `videos/.../master.m3u8`.

Treat those values as internal storage references unless the docs explicitly say they are already public endpoints.

| Job result field | Meaning | What your app should usually expose |
| --- | --- | --- |
| `results.key` from image-style outputs | object key in the media bucket | sign a `/thumb/{sig}/{encoded_key}` URL |
| `results.artifacts.cover.key` | generated cover object key in the media bucket | sign a `/thumb/{sig}/{encoded_key}` URL |
| `results.artifacts.preview.key` | generated preview object key in the media bucket | sign a `/thumb/{sig}/{encoded_key}` URL |
| `results.streaming.master_playlist` | HLS master playlist object key in the media bucket | expose `/stream/{hash}/master.m3u8` |
| `results.encryption.key_endpoint` | already-public key endpoint URL | attach a Bearer token only when the player requests it |

Two important consequences follow from this:

- a media-bucket key like `videos/mo/movie-2026-04-01/cover.jpg` is not itself the browser URL you should publish
- for HLS playback, the stable public entry is the `/stream/{hash}` route family, not the raw media-bucket key

## Common integration patterns

### Pattern 1: dynamic image delivery

Your application stores a source object key such as `uploads/avatars/sample.jpg` and signs an `/img` URL when a client asks for a transformed image.

Flow:

1. your app chooses output options such as `w640_h360_q80` and `webp`
2. your app signs the canonical payload with `HMAC_SECRET`
3. your app returns the final `/img/...` URL to the browser or CDN

Read next:

- [Image Delivery API](./api/image-delivery)
- [Image Pipeline](./media/image-pipeline)

### Pattern 2: generated thumbnails, covers, and previews

For `image:thumbnail`, `video:cover`, and `video:preview`, Vylux writes stable derived assets into the media bucket.

Your app should usually:

1. read the returned media-bucket key from the job result or callback payload
2. sign a `/thumb/{sig}/{encoded_key}` URL with `HMAC_SECRET`
3. return that `/thumb` URL to the client

Use `/thumb` instead of exposing the storage key directly so delivery behavior stays consistent and signatures remain under your control.

### Pattern 3: unencrypted HLS playback

For `video:transcode` or `video:full` without encryption:

1. your app submits the job
2. your app waits for `status=completed`
3. your app exposes `/stream/{hash}/master.m3u8` to the player
4. the player fetches playlists and segments from `/stream/{hash}/*`

You do not need a Bearer token for this case.

### Pattern 4: encrypted HLS playback

For `video:transcode` or `video:full` with encryption enabled:

1. your app submits the job and waits for completion
2. your app exposes `/stream/{hash}/master.m3u8` to the player
3. your app mints a Bearer token whose payload contains the same media `hash`
4. the player attaches `Authorization: Bearer {token}` only on `/api/key/{hash}` requests

Do not put that token into the playlist URL or the segment URL.

Read next:

- [Playback API](./api/playback)
- [Encrypted Streaming](./media/encrypted-streaming)

## End-to-end checklist

Before calling your integration complete, verify all of the following:

- your backend can create jobs with `X-API-Key`
- your backend can sign `/img`, `/original`, and `/thumb` URLs with `HMAC_SECRET`
- your frontend never sees `API_KEY`, `HMAC_SECRET`, or `KEY_TOKEN_SECRET`
- your player uses `/stream/{hash}/master.m3u8` as the public HLS entrypoint
- encrypted playback only sends `Authorization` headers to `/api/key/{hash}`

## Where to go deeper

- [Integration Recipes](./integration-recipes) for two concrete end-to-end flows
- [Getting Started](./getting-started) for local startup and smoke tests
- [Jobs API](./api/jobs) for job schemas and result payloads
- [Image Delivery API](./api/image-delivery) for exact HMAC signing rules
- [Playback API](./api/playback) for token format and key-endpoint behavior
