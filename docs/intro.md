---
sidebar_position: 1
title: Introduction
description: "The entry page for the Vylux docs, outlining service capabilities, deployment shapes, and the recommended reading path."
---

# Vylux

Vylux is a standalone media processing service that separates real-time image transformation and asynchronous video processing into deployable infrastructure capabilities. It does not own your business model. It accepts source objects, validation data, and processing parameters, then produces cacheable, traceable, playable media assets.

Current core capabilities include:

- real-time image transformation through `/img`
- signed original and thumbnail delivery through `/original` and `/thumb`
- async jobs: `image:thumbnail`, `video:cover`, `video:preview`, `video:transcode`, and `video:full`
- HLS CMAF output with AV1 and H.264 ladders
- encrypted playback with Bearer-token key delivery through `/api/key/{hash}`
- PostgreSQL job state, Redis queues, Prometheus metrics, and OpenTelemetry tracing

:::tip Start from the page that matches your job
Most confusion comes from reading an API page before you have chosen your deployment shape or trust boundary. Pick the path below that matches what you are actually trying to do.
:::

## Runtime shapes

### `all`

- runs the HTTP server and the worker in one process
- best for local development, staging, and smaller single-node deployments

### `server`

- runs only the HTTP server, image delivery endpoints, playback routes, and main metrics surface
- pair this with a separate worker when you want cleaner scaling and failure isolation

### `worker`

- runs only the queue consumer and the worker metrics listener
- use this when FFmpeg, libvips, and packaging workloads should scale independently from HTTP traffic

This lets you use the same image for local development, Docker Compose, single-node deployments, and split server/worker layouts on Kubernetes.

## Verified capabilities today

- `/img` real-time image resize, format conversion, and caching
- animated `video:preview` output in `webp` and `gif`
- `video:transcode` HLS CMAF output
- dual-codec ladders with AV1 and H.264
- portrait and non-16:9 video output sizing
- raw-key CBCS / SAMPLE-AES protected streaming
- `/api/key/{hash}` Bearer-token key delivery
- PostgreSQL job state, Redis queues, Prometheus metrics, and OpenTelemetry tracing

## Recommended reading path

### First run

1. Start with [Getting Started](./getting-started)
2. Continue with [Configuration](./operations/configuration) and [Deployment](./operations/deployment)
3. Finish with [Observability](./operations/observability) once the service is reachable

### Application integration

1. Read [Integration Guide](./integration-guide)
2. Continue with [Jobs API](./api/jobs) and [Image Delivery API](./api/image-delivery)
3. If you need streaming or DRM-style flows, continue with [Playback API](./api/playback) and [Encrypted Streaming](./media/encrypted-streaming)

### Operations

1. Read [Deployment](./operations/deployment)
2. Continue with [Configuration](./operations/configuration)
3. Use [System Endpoints](./api/system) and [Observability](./operations/observability) for probes, metrics, and troubleshooting

## Docs scope

This docs site focuses on:

- service structure and core data flow
- media processing pipelines
- HTTP endpoints, auth models, and curl examples
- deployment, configuration, observability, and testing

## Docs principles

- current code and tests take priority over old design notes
- operational usefulness takes priority over abstract architecture prose
- knowledge that used to live in temporary root-level files or local helper folders is being folded into these docs so the published site remains self-contained
