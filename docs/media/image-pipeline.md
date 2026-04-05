---
title: Image Pipeline
description: "The synchronous image-processing path, URL-signing canonicalization, cache layers, and how it differs from `image:thumbnail`."
---

# Image Pipeline

Vylux handles images on a synchronous request path optimized for low latency and high cache hit rates.

## Endpoints

- `/img/:sig/:opts/*`: transform and return an image immediately
- `/original/:sig/*`: proxy the original object
- `/thumb/:sig/*`: proxy generated image assets

## `/img` core flow

1. parse the output format and source key from the request path
2. parse `opts`, currently supporting `w`, `h`, and `q`
3. validate the HMAC signature
4. check the memory LRU
5. check the media-bucket storage cache
6. on a miss, fetch the original from the source bucket
7. transform the image with libvips
8. write the result into the memory LRU synchronously and the storage cache asynchronously
9. return the image bytes with CDN-friendly headers

## Signing canonicalization

Vylux does not sign the raw browser URL string directly. It first canonicalizes:

- `opts` into `w -> h -> q` order
- the encoded source path into a decoded object key
- `jpeg` into `jpg`

This prevents logically equivalent requests from producing different signatures or cache keys.

## Cache layers

| Layer | Role | Write timing |
| --- | --- | --- |
| memory LRU | hot results inside one process | synchronous |
| media-bucket storage cache | shared derived-image cache across processes and pods | asynchronous |
| CDN | public distribution layer | driven by response headers |

Current response behavior includes:

- `Cache-Control: public, max-age=31536000, immutable`
- `ETag` based on the returned bytes
- `Vary: Accept`

## Cache path

Real-time image results are written to:

```text
cache/{processing_hash}.{format}
```

`processing_hash` is derived from the source key and transformation parameters, not from the upstream job `hash` field.

## singleflight and concurrency

The synchronous image path uses two singleflight groups:

- `sourceFlight`: suppress duplicate fetches of the same original image
- `processFlight`: suppress duplicate transforms for the same source-plus-options combination

This matters when a cold cache receives a burst of identical requests.

## `/original` and `/thumb`

These two endpoints do not transform content:

- `/original` proxies objects from the source bucket
- `/thumb` proxies already-generated image assets from the media bucket

Both require HMAC-signed URLs, but `/thumb` uses a dedicated `thumb` signing domain so signatures cannot be reused across `/thumb` and `/original`.

## How `image:thumbnail` differs

Although both features deal with images, they serve different purposes:

| Path | Mode | Output location | Typical use |
| --- | --- | --- | --- |
| `/img` | synchronous | `cache/{processing_hash}.{format}` | browser-facing dynamic image delivery |
| `image:thumbnail` | asynchronous | `images/{prefix}/{hash}/{variant}.{format}` | stable pre-generated variants |

`image:thumbnail` does not route through `/img`. The worker generates named variants directly.

## Error semantics

The image path is intentionally strict:

- `400`: invalid parameters or unsupported format
- `403`: invalid signature
- `404`: missing source object
- `422`: source exists but cannot be processed
- `502`: temporary source-storage failure
- `500`: unexpected internal error

There is no pretend-success fallback image. Failures remain visible to upstream callers and observability tooling.
