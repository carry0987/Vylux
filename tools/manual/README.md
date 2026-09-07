# Vylux Manual Test Toolkit

This folder contains local-only Python helpers for manual testing against a Vylux instance.

The toolkit is intentionally kept on the `gh-pages` branch so the main application branch stays clean.

## Requirements

- Python 3.11+
- `boto3` for RustFS object operations

## Quick start

Install the toolkit in editable mode from the repo root:

```bash
python -m pip install -e tools/manual
```

Install the optional dependency with:

```bash
python -m pip install -e 'tools/manual[rustfs]'
```

Check that the CLI is available:

```bash
vylux-manual --help
```

If you prefer the module form, you can run:

```bash
python -m vylux_manual --help
```

## Environment loading

The toolkit reads:

1. `.env`
2. `.env.local` (overrides `.env`)
3. matching process environment variables (override both)

This matches the local development workflow documented in the site.

## How to use it

1. Prepare `.env` or `.env.local` in the repo root.
2. Install the toolkit with `python -m pip install -e tools/manual`.
3. Run `vylux-manual --help` to see all supported commands.
4. Use one command at a time for the exact workflow you want to test.

Typical examples:

```bash
vylux-manual upload source uploads/demo.flac /path/to/demo.flac
vylux-manual create-audio a0b1c2d3 uploads/demo.flac --encrypt --waveform
vylux-manual job-status <job-id>
```

## What each command is for

- `upload`, `list`, `head`, `delete-object`: interact with the RustFS `source` and `media` buckets.
- `create-audio`, `create-video-transcode`, `create-video-full`: submit media jobs to a Vylux instance.
- `job-status`, `job-retry`, `delete-media`: inspect or clean up server-side processing results.
- `image-url`, `original-url`, `thumb-url`, `key-url`: build signed delivery URLs and playback-related tokens.

If you want to test the whole flow end to end, the usual order is:

1. Upload a source file.
2. Create a job.
3. Poll the job with `job-status`.
4. Generate delivery URLs after processing completes.

## Example workflow

```bash
vylux-manual upload source uploads/demo.mp4 /path/to/demo.mp4
vylux-manual create-video-transcode deadbeef uploads/demo.mp4 --encrypt
vylux-manual job-status <job-id>
vylux-manual thumb-url videos/mo/movie-2026-04-01/cover.jpg
```

## Security notes

- Do not expose `API_KEY`, `HMAC_SECRET`, or `KEY_TOKEN_SECRET` to browsers.
- These helpers are for local or trusted engineering workflows.
- The toolkit is not a production SDK.
