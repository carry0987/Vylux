---
title: Image Delivery API
description: "URL models, HMAC signing rules, and curl examples for `/img`, `/original`, and `/thumb`."
---

# Image Delivery API

These endpoints synchronously return processed images or existing media objects and are suitable for browsers, CDNs, and upstream applications.

For the complete definitions of `BASE_URL`, `HMAC_SECRET`, and optional `IMAGE_SOURCE_KEY` used by the signing examples below, see [Configuration](../operations/configuration).

## Endpoint overview

### `/img`

- endpoint: `GET /img/{sig}/{opts}/{source}.{format}`
- auth: HMAC-signed URL
- backing storage: reads the source bucket and writes the cached derivative into the media bucket

### `/original`

- endpoint: `GET /original/{sig}/{source}`
- auth: HMAC-signed URL
- backing storage: proxies the source bucket without transforming the object

### `/thumb`

- endpoint: `GET /thumb/{sig}/{encoded_key}`
- auth: HMAC-signed URL
- backing storage: proxies an existing thumbnail, cover, or other static media object from the media bucket

## `GET /img/{sig}/{opts}/{source}.{format}`

### Path shape

With `IMAGE_SOURCE_KEY` enabled:

```text
/img/{sig}/{opts}/{source_token}.{format}
```

With `IMAGE_SOURCE_KEY` empty or unset:

```text
/img/{sig}/{opts}/{encoded_source}.{format}
```

Examples:

```text
/img/<sig>/w640_h360_q80/uploads%2Favatars%2Fsample.jpg.webp
/img/<sig>/w1200/uploads%2Fproducts%2Fhero.png.avif
/img/<sig>/w640_h360_q80/AQABAgMEBQYHCAkK....webp
```

### Parameter semantics

| Segment | Meaning |
| --- | --- |
| `sig` | hex-encoded `HMAC-SHA256` signature |
| `opts` | image options such as `w640_h360_q80` |
| `source` | AES-256-GCM source token when `IMAGE_SOURCE_KEY` is enabled; otherwise a URL-escaped source object key |
| `format` | output format: `webp`, `avif`, `jpg`, `png`, or `gif` |

Animation notes:

- animated output is supported only for `webp` and `gif`
- animated source images cannot be converted to static outputs such as `avif`, `jpg`, or `png`
- HEIF-family inputs such as `heic`, `heif`, and `avif` are accepted as source formats when the runtime image includes HEIF support, but they are currently treated as static images on the synchronous `/img` path
- animated AVIF and animated HEIF sequences are not currently supported on the synchronous `/img` path

### Canonicalization rules

Vylux signs a canonical form, not the raw browser URL string. In both modes, `opts` are normalized into `w -> h -> q` order and `jpeg` is normalized to `jpg`.

When `IMAGE_SOURCE_KEY` is empty, `encoded_source` is decoded before signing. Conceptually, the signature input is:

```text
{canonical_options}/{decoded_source_key}.{canonical_format}
```

When `IMAGE_SOURCE_KEY` is enabled, the source key is encrypted first. Vylux signs these NUL-separated fields:

```text
"vylux/image-url/v1" || "\x00" || canonical_options ||
"\x00" || source_token || "\x00" || canonical_format
```

The token binary layout is:

```text
0x01 || nonce[12] || AES-256-GCM(source_key, nonce, "vylux/image-source/v1")
```

Encode the complete value as unpadded Base64URL. AES-GCM appends a 16-byte authentication tag. Generate a fresh cryptographically random nonce for each new token; never fix, reuse, or derive it from the source key.

:::tip Sign the canonical form, not the browser URL
If your signer and Vylux disagree on option order, decoded object key, or `jpeg` versus `jpg`, the request will fail with `403` even though the browser URL looks plausible.
:::

### Plaintext-mode shell signing and curl example

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

### Node.js encrypted-source example

The following Node.js 22+ example uses only `node:crypto`. Run it only in a trusted backend that can read both secrets.

```js showLineNumbers
import { createCipheriv, createHmac, randomBytes } from "node:crypto";

const BASE_URL = process.env.BASE_URL ?? "http://localhost:3000";
const HMAC_SECRET = process.env.HMAC_SECRET;
const IMAGE_SOURCE_KEY = process.env.IMAGE_SOURCE_KEY;
const SOURCE_AAD = Buffer.from("vylux/image-source/v1", "utf8");

if (!HMAC_SECRET || !IMAGE_SOURCE_KEY) {
  throw new Error("HMAC_SECRET and IMAGE_SOURCE_KEY are required");
}

function createSourceToken(sourceKey, nonce = randomBytes(12)) {
  const key = Buffer.from(IMAGE_SOURCE_KEY, "hex");
  const plaintext = Buffer.from(sourceKey, "utf8");
  if (IMAGE_SOURCE_KEY.length !== 64 || key.length !== 32) {
    throw new Error("IMAGE_SOURCE_KEY must be exactly 64 hex characters");
  }
  if (plaintext.length === 0 || plaintext.length > 1024) {
    throw new Error("source key must contain 1..1024 UTF-8 bytes");
  }

  const cipher = createCipheriv("aes-256-gcm", key, nonce);
  cipher.setAAD(SOURCE_AAD);
  const ciphertext = Buffer.concat([cipher.update(plaintext), cipher.final()]);
  const tokenBytes = Buffer.concat([
    Buffer.from([1]),
    nonce,
    ciphertext,
    cipher.getAuthTag(),
  ]);
  return tokenBytes.toString("base64url");
}

function signDomain(domain, ...parts) {
  const mac = createHmac("sha256", Buffer.from(HMAC_SECRET, "utf8"));
  mac.update(domain, "utf8");
  for (const part of parts) {
    mac.update(Buffer.from([0]));
    mac.update(part, "utf8");
  }
  return mac.digest("hex");
}

function imageURL(token, { width, height, quality, format }) {
  const options = [
    width !== undefined ? `w${width}` : undefined,
    height !== undefined ? `h${height}` : undefined,
    quality !== undefined ? `q${quality}` : undefined,
  ].filter(Boolean).join("_");
  const canonicalFormat = format.toLowerCase() === "jpeg" ? "jpg" : format.toLowerCase();
  const signature = signDomain(
    "vylux/image-url/v1",
    options,
    token,
    canonicalFormat,
  );
  return `${BASE_URL}/img/${signature}/${options}/${token}.${canonicalFormat}`;
}

function originalURL(token) {
  const signature = signDomain("vylux/original-url/v1", token);
  return `${BASE_URL}/original/${signature}/${token}`;
}

const token = createSourceToken("uploads/avatars/sample.jpg");
console.log({
  token,
  image: imageURL(token, { width: 640, height: 360, quality: 80, format: "webp" }),
  original: originalURL(token),
});
```

Store and reuse `token` when you want stable CDN URLs. Generating another token for the same source creates another valid URL; it does not invalidate earlier tokens.

To verify another implementation, temporarily call `createSourceToken` with this fixed vector:

```text
IMAGE_SOURCE_KEY = 00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff
nonce            = 000102030405060708090a0b
source key       = static/forum/recommend-cover.png
expected token   = AQABAgMEBQYHCAkKC2Vs1EIpKuLQpkkakR6nAUwBWMoblWBW_FZUJL4KkkRQvPT1Ob-M_IsC7s3mG3OUcg
```

Pass `Buffer.from("000102030405060708090a0b", "hex")` as the second argument. A fixed nonce is only for interoperability tests; production code must use `randomBytes(12)`.

### Successful response behavior

- `200 OK`
- `Content-Type` matches the output format
- `Cache-Control: public, max-age=31536000, immutable`
- `ETag` derived from the returned bytes

### Failure semantics

| Status | Meaning |
| --- | --- |
| `400` | invalid options, invalid source encoding, or unsupported output format |
| `403` | invalid signature or invalid encrypted source token |
| `404` | source object not found |
| `422` | source exists but cannot be processed |
| `502` | source storage temporarily unavailable |
| `500` | internal processing failure |

## `GET /original/{sig}/{source}`

This endpoint proxies original objects from the source bucket without transforming them.

When source encryption is enabled, use the same token generated for `/img` and sign `"vylux/original-url/v1\x00{token}"`. The Node.js example above demonstrates both routes.

### Plaintext-mode shell signing and curl example

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
- never expose `IMAGE_SOURCE_KEY` to browsers or public clients
- generate signed URLs in your upstream application or auth service
- store and reuse encrypted source tokens when CDN URL stability matters

### Where the signer should live

In most deployments, the `/img`, `/original`, and `/thumb` signer should live in one of these trusted places:

- your main application backend
- a dedicated internal signing service
- a trusted edge worker that can safely read `HMAC_SECRET` and, when enabled, `IMAGE_SOURCE_KEY`

The usual request flow is:

1. the user asks your application for an image or asset
2. your application checks whether that user may access the media record
3. your application signs the Vylux URL server-side
4. your application returns the final signed URL to the browser

That means browsers should receive only the final URL, never the secret and never the unsigned path.

If you are exposing generated covers, previews, or thumbnails from job results, convert the returned media-bucket key into a signed `/thumb` URL at that same trusted layer.
