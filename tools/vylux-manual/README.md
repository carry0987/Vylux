# Vylux Manual Test Toolkit

This folder contains local-only Python helpers for manual testing against a Vylux instance.

The toolkit is intentionally kept on the `gh-pages` branch so the main application branch stays clean.

## Requirements

- Python 3.11+
- `boto3` for RustFS object operations

Install the optional dependency with:

```bash
python3 -m pip install boto3
```

## Environment loading

The toolkit reads:

1. `.env`
2. `.env.local` (overrides `.env`)
3. matching process environment variables (override both)

This matches the local development workflow documented in the site.

## Typical usage

Run from the docs branch root:

```bash
python3 tools/vylux-manual/run.py --help
python3 tools/vylux-manual/run.py upload source uploads/demo.flac /path/to/demo.flac
python3 tools/vylux-manual/run.py create-audio a0b1c2d3 uploads/demo.flac --encrypt --waveform
python3 tools/vylux-manual/run.py job-status <job-id>
```

## Security notes

- Do not expose `API_KEY`, `HMAC_SECRET`, or `KEY_TOKEN_SECRET` to browsers.
- These helpers are for local or trusted engineering workflows.
- The toolkit is not a production SDK.
