---
title: Video Pipeline
description: "The cover, preview, transcode, and `video:full` workflows, including queues, output paths, ladder resolution, and workflow behavior."
---

# Video Pipeline

Vylux currently exposes four main video job types:

- `video:cover`
- `video:preview`
- `video:transcode`
- `video:full`

## Jobs and outputs

| Job type | Primary output | Typical key |
| --- | --- | --- |
| `video:cover` | static poster image | `videos/{prefix}/{hash}/cover.jpg` |
| `video:preview` | animated preview | `videos/{prefix}/{hash}/preview.webp` or `preview.gif` |
| `video:transcode` | HLS CMAF package | `videos/{prefix}/{hash}/master.m3u8` plus `audio/` and `video/` subtrees |
| `video:full` | aggregated cover + preview + transcode result | same artifacts, returned through one workflow payload |

## Queues and retries

- `video:cover`: `default` queue, `MaxRetry(3)`
- `video:preview`: `default` queue, `MaxRetry(3)`
- `video:transcode`: `default` queue for normal files, `video:large` with `MaxRetry(2)` for files at or above `LARGE_FILE_THRESHOLD`
- `video:full`: `default` queue for normal files, `video:large` with `MaxRetry(2)` for files at or above `LARGE_FILE_THRESHOLD`

The worker server now runs two pools:

- a normal pool for `critical` and `default`
- a dedicated pool for `video:large`

`LARGE_WORKER_CONCURRENCY` controls the dedicated large-job pool and defaults to `1`.

## Large-file behavior

Large transcodes deserve separate operational expectations:

- `LARGE_FILE_THRESHOLD` defaults to `5 GiB`
- above that threshold, `video:transcode` and `video:full` are routed to `video:large`
- routing is based on the actual source object size looked up at submission time
- if the source exceeds `MAX_FILE_SIZE`, the API rejects the request before it is queued
- large jobs are expected to run much longer and require more temporary disk space than normal preview or cover tasks

In practice there are two storage behaviors:

- smaller outputs can be uploaded after the full output directory is complete
- larger outputs may be uploaded incrementally as files stabilize, which reduces temporary-disk pressure

If you operate Vylux with very large source media, plan for worker-local scratch space, conservative concurrency, and longer task lifetimes.

In containerized deployments, the canonical scratch workspace is `/var/cache/vylux`. Source downloads, encoded intermediate MP4 files, packaged HLS output, and `TMPDIR`-based temp usage are all expected to land there.

## `video:cover`

`video:cover` is the shortest video workflow:

1. download the source video into a temp file
2. extract a representative frame, defaulting to `1s`
3. encode a JPEG cover
4. upload it as `cover.jpg`
5. persist the artifact result

The source download still uses the shared scratch workspace, but the output itself is small and remains a normal `default`-queue task.

## `video:preview`

`video:preview` generates short animated previews. The currently validated output formats are:

- `webp`
- `gif`

Common options include:

- `start_sec`
- `duration`
- `width`
- `fps`
- `format`

## `video:transcode`

`video:transcode` produces HLS CMAF output with geometry-aware ladders.

In more detail, it works like this:

1. run `ffprobe` to determine the source display geometry and whether audio exists
2. resolve the actual output ladder from the source geometry
3. generate a shared audio MP4 track and per-codec intermediate MP4 files
4. hand those tracks to Shaka Packager to generate `master.m3u8`, variant playlists, `init.mp4`, and `.m4s` segments
5. recursively upload the output directory into the media bucket

Current validated behavior includes:

- AV1 and H.264 variant generation
- portrait-aware output sizing
- no upscaling beyond source constraints
- `setsar=1` after scaling so reported dimensions match playlist `RESOLUTION`

## Canonical rungs

Internally, the default ladder uses canonical rungs:

- `r1080`
- `r720`
- `r480`
- `r360`
- `r240`

Each rung is emitted in both AV1 and H.264 by default, which creates a dual-codec ladder.

The rung name does not guarantee a fixed 16:9 output size. `r720_h264` means a rung class, not always exactly `1280x720`.

## How actual dimensions are resolved

The worker probes the source display geometry and then applies these rules:

- filter rungs by source short edge
- preserve aspect ratio
- avoid upscaling
- honor rotation metadata
- force square pixels with `setsar=1`

As a result, the width and height reported in job results, the `RESOLUTION` values in the HLS master playlist, and the actual display dimensions stay aligned.

Example outputs:

- `1920x1080` -> `1920x1080`, `1280x720`, `854x480`, `640x360`, `426x240`
- `1080x1920` -> `608x1080`, `406x720`, `270x480`, `202x360`, `136x240`

## Dual-codec output

In environments that support AV1, the default output contains two ladders:

- AV1
- H.264

Players can select variants using the `CODECS` metadata. Clients without AV1 support fall back to H.264 automatically.

## Audio-track model

The current default output uses one shared audio track:

- ID: `und_aac_2ch`
- codec: AAC
- channels: 2

Video variants reference this shared audio rendition through playlist metadata.

## `video:full`

`video:full` is the current aggregate workflow. Its logic is:

1. download the source video once
2. run cover and preview in parallel
3. if both succeed, run transcode
4. aggregate `artifacts`, `stages`, and `retry_plan`

`video:full` itself is also subject to large-file routing. A very large source video enters the dedicated `video:large` pool before any stage work begins.

Failures do not collapse into a single string only. They preserve:

- which stage failed
- whether it is retryable
- which job types should be retried

This is useful for upstream admin interfaces and automated retry logic.

## Result structure

The transcode artifact currently includes:

- `streaming.protocol = hls`
- `streaming.container = cmaf`
- `streaming.master_playlist`
- `audio_tracks[]`
- `video_tracks[]`
- when encrypted, `encryption.scheme`, `kid`, and `key_endpoint`

## Output conventions

Variant IDs use rung-based names such as `r1080_av1` and `r720_h264`, while the actual playlist resolution reflects the resolved display size.
