---
title: Cleanup API
description: "Management endpoint for deleting media assets, job records, and related caches."
---

# Cleanup API

`DELETE /api/media/{hash}`

This endpoint requires `X-API-Key` and deletes media assets plus related tracking data for a given hash.

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

Response status depends on whether Vylux can confirm that media is gone.

## Current cleanup scope

- media-bucket assets derived from the hash
- tracked synchronous image-cache records
- related active, retry, and scheduled queue tasks
- encryption-key records and job records

## Semantics

- `204 No Content` means cleanup completed or the hash was already fully cleaned up
- `503 Service Unavailable` means Vylux could not confirm cleanup completion and the caller should retry
- retries are safe: a later retry can still converge on `204` after a partial failure
- critical cleanup stages are task cancellation, media object deletion, and tracked cache object deletion
- metadata cleanup such as encryption-key and job-row deletion runs only after critical cleanup succeeds
- this endpoint is best used by internal admin tools, retention jobs, or compensating workflows rather than public clients

### Example incomplete cleanup response

```json
{
    "message": "cleanup incomplete for movie-2026-04-01",
    "retryable": true,
    "completed_stages": ["task_cancellation"],
    "failed_stages": ["media_objects"]
}
```

### Practical caller guidance

- only treat `204` as confirmation that it is safe to forget the hash
- treat `503` as a retryable cleanup failure
- do not delete your own source-of-truth record until cleanup reaches `204`
