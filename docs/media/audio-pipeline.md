---
title: Audio Pipeline
description: "The audio processing path, public create contract, output profiles, storage layout, and worker behavior for first-class audio jobs."
---

# Audio Pipeline

Vylux exposes audio processing through `POST /api/audio/jobs`.

Audio is treated as its own media domain. It is not modeled as a special case of video.

## Public create contract

Audio jobs are created through a domain-specific route:

- `POST /api/audio/jobs`

The request body is output-oriented. It does not use the old generic `type + options` schema and it does not require a discriminator like `media_kind`.

Typical request:

```json
{
  "source": {
    "hash": "sha256...",
    "key": "uploads/example.flac"
  },
  "pipeline": {
    "package": {
      "hls": {
        "enabled": true,
        "profile": "stream_aac_standard"
      }
    },
    "downloads": [
      {"profile": "download_mp3_high"},
      {"profile": "download_flac_standard"}
    ],
    "waveform": {
      "enabled": true,
      "profile": "waveform_standard",
      "bins": 2048
    }
  },
  "delivery": {
    "callback_url": "https://app.example.com/internal/media/callback"
  }
}
```

## Supported outputs

The current audio path can produce:

- audio-only HLS for playback
- MP3 download artifacts
- FLAC download artifacts
- waveform JSON artifacts
- probe metadata in the job result payload

## Storage layout

Derived audio assets live under the dedicated `audio/...` namespace:

- `audio/{prefix}/{hash}/hls/...`
- `audio/{prefix}/{hash}/downloads/...`
- `audio/{prefix}/{hash}/waveform/...`

Probe analysis is currently persisted in the job result payload rather than as a separate `analysis/...` file.

## Processing stages

The worker records stage-level state for:

- `source`
- `probe`
- `package`
- `downloads`
- `waveform`

Failures are captured as structured stage results rather than collapsing into one opaque error string.

## Worker behavior

`audio:transcode` is the internal worker task used by the current audio pipeline. Its execution pattern is:

1. download the source object to the scratch workspace
2. probe the source with `ffprobe`
3. validate that the source format is supported
4. optionally generate waveform JSON
5. optionally generate audio-only HLS output
6. optionally generate MP3 and FLAC download artifacts
7. persist the final job result and send the callback if configured

The current retry model is whole-job retry rather than per-stage retry orchestration.

## Playback entrypoint

For audio HLS, the current public playback entrypoint is:

```text
/stream/{hash}/hls/master.m3u8
```

Treat the `results.streaming.master_playlist` value as a storage-backed route target, not as a raw bucket URL to expose directly.

## Related pages

- [Jobs API](../api/jobs)
- [Playback API](../api/playback)
- [Request Lifecycle](../architecture/request-lifecycle)