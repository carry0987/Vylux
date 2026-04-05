---
title: Cleanup API
description: "Management endpoint for deleting media assets, job records, and related caches."
---

# Cleanup API

`DELETE /api/media/{hash}`

This endpoint requires `X-API-Key` and deletes the media assets and related tracking data for a given hash.

For the exact `API_KEY` and base-URL configuration used by internal callers, see [Configuration](../operations/configuration).

## curl example

```bash showLineNumbers
BASE_URL='http://localhost:3000'
API_KEY='replace-with-api-key'
MEDIA_HASH='movie-2026-04-01'

curl -i \
    -X DELETE \
    -H "X-API-Key: $API_KEY" \
    "$BASE_URL/api/media/$MEDIA_HASH"
```

Success returns `204 No Content`.

## Current cleanup scope

- media-bucket assets derived from the hash
- tracked synchronous image-cache records
- related active, retry, and scheduled queue tasks
- encryption-key records and job records

## Semantics

- cleanup is best-effort and safe to call repeatedly
- already-missing resources still result in `204`, which makes upstream cleanup idempotent
- this endpoint is best used by internal admin tools, retention jobs, or compensating workflows rather than public clients
