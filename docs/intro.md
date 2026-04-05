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

## Runtime shapes

The Vylux binary supports three runtime modes:

- `all`: run the HTTP server and the worker in one process
- `server`: run only the HTTP server, image delivery, and playback endpoints
- `worker`: run only the queue consumer and the worker metrics listener

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

1. Start with [Getting Started](./getting-started)
2. Read [Integration Guide](./integration-guide) to understand how job results become public URLs, signed requests, and playback flows
3. Continue with [Configuration](./operations/configuration) and [Deployment](./operations/deployment)
4. If you are integrating APIs, read [Jobs API](./api/jobs) and [Image Delivery API](./api/image-delivery)
5. If you care about playback integration, read [Playback API](./api/playback) and [Encrypted Streaming](./media/encrypted-streaming)
6. If you operate the service, continue with [Observability](./operations/observability)

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
