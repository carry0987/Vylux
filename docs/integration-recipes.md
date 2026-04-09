---
title: Integration Recipes
description: "Two concrete end-to-end recipes: publishing a generated cover image and wiring encrypted HLS playback with Vylux."
---

# Integration Recipes

This page turns the abstract endpoint rules into concrete integration sequences.

If you have already read [Integration Guide](./integration-guide), these recipes show what the application-side workflow actually looks like.

## Recipe 1: publish a generated cover image

Use this flow when your application creates a `video:cover` job or reads the `cover` artifact from a `video:full` workflow.

Goal:

Return a browser-safe public URL for the generated cover image without exposing raw bucket paths or `HMAC_SECRET`.

Flow:

1. your backend submits a `video:cover` or `video:full` job with `X-API-Key`
2. your backend polls the job or receives the callback payload
3. your backend reads the generated key, for example `videos/mo/movie-2026-04-01/cover.jpg`
4. your backend signs a `/thumb/{sig}/{encoded_key}` URL with `HMAC_SECRET`
5. your backend returns that `/thumb/...` URL to the browser

The browser should receive something like:

```text
https://media.example.com/thumb/<sig>/videos%2Fmo%2Fmovie-2026-04-01%2Fcover.jpg
```

The browser should not receive:

- a raw media-bucket key such as `videos/mo/movie-2026-04-01/cover.jpg`
- `HMAC_SECRET`
- a direct storage URL that bypasses Vylux delivery semantics

Typical backend responsibilities:

- verify that the caller may access the requested media record
- load the stored cover key from your database or the finished job payload
- sign the `/thumb` URL just before returning the API response

Why `/thumb` is the right endpoint here:

Generated covers and thumbnails already exist in the media bucket. They do not need `/img` transformation at read time.

Use `/thumb` when the asset already exists.

Use `/img` only when you need on-demand transformation from a source-bucket object.

## Recipe 2: play encrypted HLS output

Use this flow when `video:transcode` or `video:full` finished with encryption enabled.

Goal:

Let the player load `/stream/{hash}/master.m3u8` while keeping content-key access behind short-lived Bearer tokens.

Flow:

1. your backend submits the transcode job
2. your backend waits for `status=completed`
3. your backend exposes `/stream/{hash}/master.m3u8` as the player entrypoint
4. your backend mints a short-lived Bearer token whose payload contains the same media `hash`
5. your frontend initializes the player with the playlist URL and the token
6. the player fetches playlists and segments from `/stream/{hash}/*`
7. the player adds `Authorization: Bearer {token}` only when requesting `/api/key/{hash}`

Your frontend usually needs two values:

- a playlist URL such as `https://media.example.com/stream/movie-2026-04-01/master.m3u8`
- a short-lived key token for `/api/key/movie-2026-04-01`

What not to do:

- do not append the key token to the playlist URL query string
- do not append the key token to segment URLs
- do not expose `KEY_TOKEN_SECRET` to the browser
- do not assume `results.streaming.master_playlist` is itself the final public URL

Recommended backend shape:

1. your application authenticates the user
2. your application checks authorization for the requested media hash
3. your application returns the public `/stream/{hash}/master.m3u8` URL
4. your application returns a short-lived Bearer token generated server-side

If you prefer, the token may also be issued by a separate internal auth service instead of the main application backend.

Minimal player contract:

- load the playlist from `/stream/{hash}/master.m3u8`
- inject the `Authorization` header only on `/api/key/` requests

That keeps HLS objects cacheable while leaving key delivery protected.

:::warning The public client should never assemble these URLs from secrets
In both recipes, your backend or an internal auth/signing service should prepare the final URL or token. Browsers should receive only the final values they need to use.
:::

## Cross-check before release

Before calling these integrations done, verify:

- cover and thumbnail URLs returned by your app use `/thumb`, not raw bucket keys
- encrypted HLS playback works with `/stream/{hash}/master.m3u8`
- `/api/key/{hash}` returns `401` without a token and `200` with a valid token
- no public client ever sees `API_KEY`, `HMAC_SECRET`, or `KEY_TOKEN_SECRET`

## Related docs

- [Integration Guide](./integration-guide)
- [Jobs API](./api/jobs)
- [Image Delivery API](./api/image-delivery)
- [Playback API](./api/playback)
