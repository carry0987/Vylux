---
title: Manual Local Testing Toolkit
description: "How to use the Python helpers on the gh-pages branch for local RustFS uploads, job creation, playback signing, and cleanup flows."
---

# Manual Local Testing Toolkit

The `gh-pages` branch ships a small Python toolkit for engineers who want to manually exercise a local Vylux environment without keeping those helpers in the main application branch.

The toolkit lives under:

```text
tools/vylux-manual/
```

It is intended for local and trusted engineering workflows, not as a production SDK.

## Why it lives on the docs branch

These helpers are useful for manual testing, but they are not part of the Vylux runtime.

Keeping them on the docs branch gives you:

- a clean main application branch
- versioned tooling that can evolve with the docs
- a stable place for engineers to clone and run local test helpers

## Prerequisites

- Python 3.11+
- `boto3` if you want to upload, list, head, or delete objects in RustFS
- a working `.env` and, when needed, `.env.local`

Install the optional RustFS dependency with:

```bash showLineNumbers
+python3 -m pip install boto3
```

## Environment loading order

The toolkit loads configuration in this order:

1. `.env`
2. `.env.local` with override behavior
3. matching process environment variables with highest priority

This mirrors the common Vylux local-development pattern:

- `.env` for container-to-container addresses such as `postgres`, `redis`, or `otel-collector`
- `.env.local` for host-to-container overrides such as `localhost:5434`, `localhost:6381`, or `localhost:9002`

## Entry point

Run the CLI from the `gh-pages` branch root:

```bash showLineNumbers
python3 tools/vylux-manual/run.py --help
```

## Supported commands

The toolkit currently supports:

- uploading objects to the `source` or `media` bucket
- listing objects in the `source` or `media` bucket
- reading object metadata with `head`
- deleting objects from the `source` or `media` bucket
- creating audio jobs
- creating video transcode jobs
- creating video full jobs
- fetching job status
- retrying failed jobs
- deleting derived media by content hash
- building signed `/img`, `/original`, and `/thumb` URLs
- building `/api/key/{id}` URLs and Bearer tokens

## Common flows

### Upload a source object

```bash showLineNumbers
python3 tools/vylux-manual/run.py upload source uploads/demo.flac /path/to/demo.flac
python3 tools/vylux-manual/run.py upload source uploads/demo.mp4 /path/to/demo.mp4
```

### Create an encrypted audio HLS job

```bash showLineNumbers
python3 tools/vylux-manual/run.py create-audio a0b1c2d3 uploads/demo.flac --encrypt --waveform
```

### Create an encrypted video transcode job

```bash showLineNumbers
python3 tools/vylux-manual/run.py create-video-transcode deadbeef uploads/demo.mp4 --encrypt
```

### Check job status

```bash showLineNumbers
python3 tools/vylux-manual/run.py job-status <job-id>
```

### Delete derived media for a hash

```bash showLineNumbers
python3 tools/vylux-manual/run.py delete-media deadbeef
```

## Signed delivery helpers

The toolkit can also construct the public-facing routes that your application would normally sign or mint.

### Signed image URL

```bash showLineNumbers
python3 tools/vylux-manual/run.py image-url uploads/sample.png webp --options w320_h180
```

### Signed original URL

```bash showLineNumbers
python3 tools/vylux-manual/run.py original-url uploads/sample.png
```

### Signed thumbnail URL

```bash showLineNumbers
python3 tools/vylux-manual/run.py thumb-url videos/mo/movie-2026-04-01/cover.jpg
```

### Key endpoint URL and Bearer token

```bash showLineNumbers
python3 tools/vylux-manual/run.py key-url <key-id> <content-hash> --ttl 3600
```

## Security notes

- Never expose `API_KEY`, `HMAC_SECRET`, or `KEY_TOKEN_SECRET` to browsers.
- Treat this toolkit as a trusted local helper, not as a public client.
- If you share shell history or screenshots, remember that command lines may include sensitive values.

## Related pages

- [Getting Started](../getting-started)
- [Jobs API](../api/jobs)
- [Playback API](../api/playback)
- [Encrypted Streaming](../media/encrypted-streaming)
