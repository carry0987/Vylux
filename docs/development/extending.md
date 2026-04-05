---
title: Extending Vylux
description: "Maintenance guidance when adding job types, extending pipelines, or keeping docs synchronized with implementation."
---

# Extending Vylux

When you add or change a workflow, keep the runtime, persistence layer, and docs synchronized.

## Start from the boundary

When you extend Vylux, first decide which layer owns the change:

- HTTP endpoint
- queue task
- worker handler
- media processing primitive
- storage / persistence shape

## Typical extension points for media jobs

1. add or change the job type and payload schema
2. add request validation or endpoint wiring where the job enters the system
3. add worker handlers and task wiring
4. update persistence fields, result shapes, and cleanup logic for new artifacts
5. add unit tests and smoke tests
6. update docs and release-validation guidance

## Practical rule

Prefer changes that preserve the current conventions:

- clear job typing
- stable artifact layout
- explicit retry semantics
- verifiable outputs through API and playback paths

If docs and implementation drift, trust the verified implementation first, then update the docs site and any release test procedure at the same time.
