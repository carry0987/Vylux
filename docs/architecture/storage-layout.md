---
title: Storage Layout
description: "How the source store, media store, PostgreSQL, and `/stream` routes map to each other in the current implementation."
---

# Storage Layout

Vylux uses two logical storage roles:

- source store: reads original uploads and upstream source objects from `SOURCE_BUCKET` through `SOURCE_S3_*`
- media store: reads and writes generated outputs, manifests, keys, previews, and derivative images in `MEDIA_BUCKET` through `MEDIA_S3_*`

These two roles may point at the same S3-compatible service or at different backends, but Vylux configures them separately and does not infer one role from the other.

There are also two non-bucket state stores:

- PostgreSQL: job state, workflow results, wrapped encryption keys, and image-cache tracking
- Redis: queue state and rate-limit counters

## Storage roles

The source store is treated as immutable input. The media store is where Vylux writes processed artifacts and cleanup targets.

## Media-bucket structure

```text
media-bucket/
├── images/{prefix}/{hash}/...
├── videos/{prefix}/{hash}/
│   ├── cover.jpg
│   ├── preview.webp
│   ├── preview.gif
│   ├── master.m3u8
│   ├── audio/und_aac_2ch/
│   │   ├── init.mp4
│   │   ├── playlist.m3u8
│   │   └── seg_*.m4s
│   └── video/
│       ├── r1080_av1/
│       ├── r720_av1/
│       ├── r480_av1/
│       ├── r360_av1/
│       ├── r240_av1/
│       ├── r1080_h264/
│       ├── r720_h264/
│       ├── r480_h264/
│       ├── r360_h264/
│       └── r240_h264/
└── cache/{processing_hash}.{format}
```

## Object-key naming rules

### Synchronous image cache

Real-time image results are written to:

```text
cache/{processing_hash}.{format}
```

`processing_hash` is derived from:

- the source object key
- width / height / quality
- output format

### Asynchronous image thumbnails

`image:thumbnail` writes to:

```text
images/{hash_prefix}/{hash}/{variant}.{format}
```

Example:

```text
images/ab/abcdef1234/thumb.webp
images/ab/abcdef1234/large.jpg
```

### Video outputs

Cover images, previews, and HLS artifacts all use:

```text
videos/{hash_prefix}/{hash}/{relative_path}
```

Examples:

```text
videos/ab/abcdef1234/cover.jpg
videos/ab/abcdef1234/preview.webp
videos/ab/abcdef1234/master.m3u8
videos/ab/abcdef1234/audio/und_aac_2ch/init.mp4
videos/ab/abcdef1234/video/r720_h264/seg_000.m4s
```

## Why the prefix exists

Both image and video object keys use the first two characters of the media hash as a prefix to avoid overloading a single flat namespace:

```text
images/{hash[0:2]}/{hash}/...
videos/{hash[0:2]}/{hash}/...
```

## Playback mapping

Depending on the job type, the media bucket may contain:

- image cache objects
- `cover.jpg`
- `preview.webp`
- `preview.gif`
- `master.m3u8`
- `video/...` variant playlists and segments
- `audio/...` playlists and segments

The `/stream/{hash}/*` routes provide stable playback-facing URLs for objects already stored in the media bucket.

Externally:

```text
/stream/{hash}/master.m3u8
/stream/{hash}/audio/{track_id}/playlist.m3u8
/stream/{hash}/video/{variant}/playlist.m3u8
/stream/{hash}/video/{variant}/seg_1.m4s
```

Internally:

```text
videos/{hash_prefix}/{hash}/{filePath}
```

where `filePath` is the remainder after `/stream/{hash}/`.

## What does not live in object storage

Some important data is deliberately not stored in the media bucket:

- wrapped content keys are stored in PostgreSQL, not as bucket objects
- `wrap_nonce` and `kek_version` also live in PostgreSQL
- image-cache tracking rows live in PostgreSQL
- job status, progress, results, and retry plans live in PostgreSQL

This split exists because static artifacts belong in object storage, while queryable and mutable metadata belongs in the database.

## Source-store role

The source bucket should only hold upstream-provided originals, for example:

```text
uploads/sample.jpg
uploads/sample.mp4
uploads/user-avatars/123.png
```

Vylux reads from these objects but should not write derived artifacts back into the source bucket.

The `/original` endpoint is a signed source-object proxy: it reads from the source store and streams the original object without copying it into the media store.

## Cleanup scope

When cleanup runs for a media hash, Vylux removes:

- `images/{prefix}/{hash}/...`
- `videos/{prefix}/{hash}/...`
- related generated-image cache tracking rows
- the corresponding encryption-key rows and job rows
