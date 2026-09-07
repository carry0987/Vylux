---
title: Manual Local Testing Toolkit
description: "How to use the Vylux local manual testing toolkit to quickly understand and manually test Vylux workflows, including object uploads, job creation, playback signing, and cleanup."
---

# Manual Local Testing Toolkit

The `gh-pages` branch ships a small Python toolkit for engineers who want to manually exercise a local Vylux environment without keeping those helpers in the main application branch.

The toolkit lives under:

```text
tools/manual/
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

Install the toolkit in editable mode from the repo root:

```bash showLineNumbers
python3 -m pip install -e tools/manual
```

Install the optional RustFS dependency with:

```bash showLineNumbers
python3 -m pip install -e 'tools/manual[rustfs]'
```

## Environment loading order

The toolkit loads configuration in this order:

1. `.env`
2. `.env.local` with override behavior
3. matching process environment variables with highest priority

This mirrors the common Vylux local-development pattern:

- `.env` for container-to-container addresses such as `postgres`, `redis`, or `otel-collector`
- `.env.local` for host-to-container overrides such as `localhost:5434`, `localhost:6381`, or `localhost:9002`

## Quick start

From the `gh-pages` branch root:

```bash showLineNumbers
python3 -m pip install -e tools/manual
vylux-manual --help
```

If you prefer the module form, use:

```bash showLineNumbers
python3 -m vylux_manual --help
```

Use the toolkit like this:

1. Put the required Vylux settings in `.env` or `.env.local` at the repo root.
2. Install the toolkit once in editable mode.
3. Run `vylux-manual --help` to list commands.
4. Execute one command for the exact slice you want to test: upload, submit a job, inspect job status, or build a signed URL.

The generated `vylux-manual` command is the primary entry point shown in this guide.

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
vylux-manual upload source uploads/demo.flac /path/to/demo.flac
vylux-manual upload source uploads/demo.mp4 /path/to/demo.mp4
```

### Create an encrypted audio HLS job

```bash showLineNumbers
vylux-manual create-audio a0b1c2d3 uploads/demo.flac --encrypt --waveform
```

### Create an encrypted video transcode job

```bash showLineNumbers
vylux-manual create-video-transcode deadbeef uploads/demo.mp4 --encrypt
```

### Check job status

```bash showLineNumbers
vylux-manual job-status <job-id>
```

### Delete derived media for a hash

```bash showLineNumbers
vylux-manual delete-media deadbeef
```

## Recommended workflow

For most manual tests, use this sequence:

1. Upload the source file to the `source` bucket.
2. Submit the media job you want to inspect.
3. Poll with `job-status` until processing completes.
4. Generate signed playback or delivery URLs for the resulting assets.

This keeps the toolkit focused on one-off validation and learning flows rather than automation.

## Signed delivery helpers

The toolkit can also construct the public-facing routes that your application would normally sign or mint.

### Signed image URL

```bash showLineNumbers
vylux-manual image-url uploads/sample.png webp --options w320_h180
```

### Signed original URL

```bash showLineNumbers
vylux-manual original-url uploads/sample.png
```

### Signed thumbnail URL

```bash showLineNumbers
vylux-manual thumb-url videos/mo/movie-2026-04-01/cover.jpg
```

### Key endpoint URL and Bearer token

```bash showLineNumbers
vylux-manual key-url <key-id> <content-hash> --ttl 3600
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
