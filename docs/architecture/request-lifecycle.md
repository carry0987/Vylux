---
title: Request Lifecycle
description: "The most important Vylux flows: real-time images, job submission, worker execution, playback, and cleanup."
---

# Request Lifecycle

:::tip Choose the flow that matches what you are debugging
If the issue appears before any queue work exists, start with image delivery or job submission. If the issue appears after a job was accepted, jump to worker execution, playback, or cleanup.
:::

## Flow map

### Synchronous HTTP

- real-time image delivery through `/img`
- request validation, cache lookup, source fetch, and immediate response

### Asynchronous jobs

- `POST /api/jobs` validation and idempotency
- Redis / asynq enqueueing and worker-side execution

### Playback and cleanup

- `/stream/{hash}/*` playback reads from the media bucket
- `/api/key/{hash}` key delivery for encrypted playback
- cleanup removes derived assets, queue work, and related metadata

## 1. Real-time image flow

```mermaid
sequenceDiagram
	participant Client
	participant Server as Vylux Server
	participant LRU as Memory LRU
	participant Media as Media Bucket
	participant Source as Source Bucket
	participant Vips as libvips

	Client->>Server: GET /img/{sig}/{opts}/{encoded_source}.{format}
	Server->>Server: validate HMAC and canonical options
	Server->>LRU: lookup
	alt LRU hit
		LRU-->>Server: image bytes
	else LRU miss
		Server->>Media: read cache/{processing_hash}.{format}
		alt storage cache hit
			Media-->>Server: image bytes
			Server->>LRU: repopulate memory cache
		else storage cache miss
			Server->>Source: fetch source object
			Source-->>Server: original bytes
			Server->>Vips: transform image
			Vips-->>Server: result bytes
			Server->>LRU: write synchronously
			Server->>Media: write storage cache asynchronously
		end
	end
	Server-->>Client: image bytes + Cache-Control + ETag
```

Key implementation details:

1. validate the HMAC signature and normalized options
2. check memory LRU, then the media-bucket storage cache
3. on a miss, fetch the original from the source bucket
4. use singleflight to suppress duplicate fetch and transform work
5. transform with libvips
6. write the result to memory immediately and to storage asynchronously
7. return CDN-friendly cache headers and an `ETag`

This path is fully synchronous and does not require the queue.

:::note `/img` never waits for a worker
If a request is on the real-time image path, the queue is not involved. Debug request validation, cache behavior, source reads, and libvips execution first.
:::

## 2. Job submission flow

```mermaid
sequenceDiagram
	participant App as Upstream App
	participant Server as Vylux Server
	participant PG as PostgreSQL
	participant Redis as Redis / asynq

	App->>Server: POST /api/jobs
	Server->>Server: validate API key and JSON schema
	Server->>Server: canonicalize options
	Server->>PG: idempotency lookup by request fingerprint
	alt existing active or completed job
		PG-->>Server: existing job/result
		Server-->>App: 200 OK
	else new job
		Server->>Redis: enqueue task
		Server->>PG: create job row
		Server-->>App: 202 Accepted
	end
```

The important part is not only enqueueing work. The server first computes a request fingerprint from:

1. `type`
2. `hash`
3. `source`
4. canonicalized `options`

This is what gives `POST /api/jobs` its idempotency behavior. The source bucket itself is deployment-owned runtime config, not caller input.

For video jobs, the server also checks the configured source store before enqueueing so it can confirm existence, measure actual size, and route oversized work to `video:large` when needed.

:::tip A `202 Accepted` means the request passed the server boundary
Once the server accepts the job, the next debugging boundary is worker execution rather than request validation.
:::

## 3. Worker execution flow

Worker execution falls into two categories: single-stage jobs and the `video:full` workflow.

### Single-stage jobs

- `image:thumbnail`
- `video:cover`
- `video:preview`
- `video:transcode`

Shared pattern:

1. dequeue a task
2. mark the job as `processing`
3. download or fetch the source media
4. run the media toolchain
5. upload artifacts to the media bucket
6. persist progress and final results
7. optionally send a webhook callback

### `video:full`

`video:full` is not implemented as a parent job that spawns child jobs. Instead it runs as one workflow task:

1. download the source once
2. run cover and preview in parallel
3. if either fails, emit `stages` and `retry_plan`
4. only proceed to transcode if both succeed
5. persist one aggregated result payload

This keeps the external API simple while preserving stage-level observability.

:::info `video:full` is one workflow, not a parent/child graph
When you inspect logs, traces, or retry behavior, think of `video:full` as one worker-owned workflow with stage-level results, not as multiple independent public jobs.
:::

## 4. Playback flow

```mermaid
sequenceDiagram
	participant Player
	participant Server as Vylux Server
	participant Media as Media Bucket
	participant KeyAPI as /api/key/{hash}
	participant PG as PostgreSQL

	Player->>Server: GET /stream/{hash}/master.m3u8
	Server->>Media: read videos/{prefix}/{hash}/master.m3u8
	Media-->>Server: playlist
	Server-->>Player: playlist
	Player->>Server: GET /stream/{hash}/video/.../seg_000.m4s
	Server->>Media: fetch object
	Server-->>Player: segment bytes
	opt encrypted playback
		Player->>KeyAPI: GET /api/key/{hash} + Bearer token
		KeyAPI->>PG: fetch wrapped key row
		PG-->>KeyAPI: wrapped key material
		KeyAPI-->>Player: 16-byte content key
	end
```

The server does not keep local copies of segments. It maps `/stream/{hash}/...` directly to media-bucket objects.

:::note Playback uses stable public routes over storage keys
Public players should use `/stream/{hash}` and `/api/key/{hash}`. Raw media-bucket keys remain an internal storage detail.
:::

## 5. Cleanup flow

`DELETE /api/media/{hash}` is shorter-lived but important for consistency:

1. resolve media-bucket objects associated with the hash
2. clear image-cache tracking and related metadata
3. cancel active, retry, or scheduled queue tasks
4. remove encryption keys and job records

This flow is intentionally best-effort and idempotent, which makes it suitable for upstream compensation or retention jobs.

:::tip Cleanup should be safe to call again
Because cleanup is best-effort and idempotent, upstream retention or compensation flows can retry it without treating every repeat call as an error.
:::
