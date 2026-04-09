---
title: Image Delivery API
description: "URL models, HMAC signing rules, and curl examples for `/img`, `/original`, and `/thumb`."
---

# Image Delivery API

These endpoints synchronously return processed images or existing media objects and are suitable for browsers, CDNs, and upstream applications.

For the complete definitions of `BASE_URL` and `HMAC_SECRET` used by the signing examples below, see [Configuration](../operations/configuration).

## Endpoint overview

### `/img`

- endpoint: `GET /img/{sig}/{opts}/{encoded_source}.{format}`
- auth: HMAC-signed URL
- backing storage: reads the source bucket and writes the cached derivative into the media bucket

### `/original`

- endpoint: `GET /original/{sig}/{encoded_key}`
- auth: HMAC-signed URL
- backing storage: proxies the source bucket without transforming the object

### `/thumb`

- endpoint: `GET /thumb/{sig}/{encoded_key}`
- auth: HMAC-signed URL
- backing storage: proxies an existing thumbnail, cover, or other static media object from the media bucket

## `GET /img/{sig}/{opts}/{encoded_source}.{format}`

### Path shape

```text
/img/{sig}/{opts}/{encoded_source}.{format}
```

Examples:

```text
/img/<sig>/w640_h360_q80/uploads%2Favatars%2Fsample.jpg.webp
/img/<sig>/w1200/uploads%2Fproducts%2Fhero.png.avif
```

### Parameter semantics

| Segment | Meaning |
| --- | --- |
| `sig` | hex-encoded `HMAC-SHA256` signature |
| `opts` | image options such as `w640_h360_q80` |
| `encoded_source` | URL-escaped source object key |
| `format` | output format: `webp`, `avif`, `jpg`, `png`, or `gif` |

### Canonicalization rules

Vylux signs a canonical form, not the raw browser URL string:

- `opts` are normalized into `w -> h -> q` order
- `encoded_source` is decoded before signing
- `jpeg` is normalized to `jpg`

Conceptually, the signature input is:

```text
{canonical_options}/{decoded_source_key}.{canonical_format}
```

:::tip Sign the canonical form, not the browser URL
If your signer and Vylux disagree on option order, decoded object key, or `jpeg` versus `jpg`, the request will fail with `403` even though the browser URL looks plausible.
:::

### shell signing and curl example

```bash showLineNumbers
BASE_URL='http://localhost:3000'
HMAC_SECRET='replace-with-hmac-secret'
OPTIONS='w640_h360_q80'
ENCODED_SOURCE='uploads%2Favatars%2Fsample.jpg.webp'
CANONICAL_SOURCE='uploads/avatars/sample.jpg.webp'

SIG="$(printf '%s/%s' "$OPTIONS" "$CANONICAL_SOURCE" \
  | openssl dgst -sha256 -hmac "$HMAC_SECRET" -hex \
  | sed 's/^.* //')"

curl -L "$BASE_URL/img/$SIG/$OPTIONS/$ENCODED_SOURCE"
```

### Successful response behavior

- `200 OK`
- `Content-Type` matches the output format
- `Cache-Control: public, max-age=31536000, immutable`
- `ETag` derived from the returned bytes

### Failure semantics

| Status | Meaning |
| --- | --- |
| `400` | invalid options, invalid source encoding, or unsupported output format |
| `403` | invalid signature |
| `404` | source object not found |
| `422` | source exists but cannot be processed |
| `502` | source storage temporarily unavailable |
| `500` | internal processing failure |

## `GET /original/{sig}/{encoded_key}`

This endpoint proxies original objects from the source bucket without transforming them.

### shell signing and curl example

```bash showLineNumbers
BASE_URL='http://localhost:3000'
HMAC_SECRET='replace-with-hmac-secret'
ENCODED_KEY='uploads%2Fsample.mp4'
CANONICAL_KEY='uploads/sample.mp4'

SIG="$(printf '/%s' "$CANONICAL_KEY" \
  | openssl dgst -sha256 -hmac "$HMAC_SECRET" -hex \
  | sed 's/^.* //')"

curl -L "$BASE_URL/original/$SIG/$ENCODED_KEY"
```

### Behavior notes

- validates the HMAC before touching storage
- infers `Content-Type` from extension first, then falls back to content sniffing
- returns `404` when the object does not exist

## `GET /thumb/{sig}/{encoded_key}`

This endpoint proxies existing thumbnails, video covers, or other static media objects from the media bucket.

### shell signing and curl example

```bash showLineNumbers
BASE_URL='http://localhost:3000'
HMAC_SECRET='replace-with-hmac-secret'
ENCODED_KEY='videos%2Fab%2Fabcdef%2Fcover.jpg'
CANONICAL_KEY='videos/ab/abcdef/cover.jpg'

SIG="$(printf 'thumb/%s' "$CANONICAL_KEY" \
  | openssl dgst -sha256 -hmac "$HMAC_SECRET" -hex \
  | sed 's/^.* //')"

curl -L "$BASE_URL/thumb/$SIG/$ENCODED_KEY"
```

### Behavior notes

- the `thumb/` signing domain prevents signature reuse across `/thumb` and `/original`
- successful responses include `Access-Control-Allow-Origin: *`

## Practical guidance

:::warning Keep `HMAC_SECRET` on the trusted side
Browsers and public clients should receive only the final signed URL. Do not expose `HMAC_SECRET`, and do not ask the browser to construct signatures itself.
:::

- never expose `HMAC_SECRET` to browsers or public clients
- generate signed URLs in your upstream application or auth service
- rely on stable object keys or content hashes so CDN caching stays predictable

### Where the signer should live

In most deployments, the `/img`, `/original`, and `/thumb` signer should live in one of these trusted places:

- your main application backend
- a dedicated internal signing service
- a trusted edge worker that can safely read `HMAC_SECRET`

The usual request flow is:

1. the user asks your application for an image or asset
2. your application checks whether that user may access the media record
3. your application signs the Vylux URL server-side
4. your application returns the final signed URL to the browser

That means browsers should receive only the final URL, never the secret and never the unsigned path.

If you are exposing generated covers, previews, or thumbnails from job results, convert the returned media-bucket key into a signed `/thumb` URL at that same trusted layer.
