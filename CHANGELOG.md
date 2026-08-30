# Changelog

## [2.2.0](https://github.com/carry0987/Vylux/compare/v2.1.0...v2.2.0) (2026-08-30)


### Features

* standardize API JSON responses ([#42](https://github.com/carry0987/Vylux/issues/42)) ([e31de0c](https://github.com/carry0987/Vylux/commit/e31de0c5c88f2ca897df9d40ef13e4c2004613f8))

## [2.1.0](https://github.com/carry0987/Vylux/compare/v2.0.0...v2.1.0) (2026-08-25)


### Features

* **audio:** add protected HLS streaming with asset-scoped keys ([#38](https://github.com/carry0987/Vylux/issues/38)) ([311a362](https://github.com/carry0987/Vylux/commit/311a36212fbdf36dd0e740ae45f6e2c7954f6d03))

## [2.0.0](https://github.com/carry0987/Vylux/compare/v1.2.1...v2.0.0) (2026-08-24)


### ⚠ BREAKING CHANGES

* **cleanup:** `DELETE /api/media/:hash` no longer always returns `204 No Content`. It now returns `503 Service Unavailable` when cleanup cannot be confirmed and the caller should retry.
* add audio processing and split media job creation routes ([#31](https://github.com/carry0987/Vylux/issues/31))

### Features

* add audio processing and split media job creation routes ([#31](https://github.com/carry0987/Vylux/issues/31)) ([827c849](https://github.com/carry0987/Vylux/commit/827c84999b47809c41d68638c4b93e8106e7f281))


### Bug Fixes

* **cleanup:** return retryable failures for incomplete cleanup ([#33](https://github.com/carry0987/Vylux/issues/33)) ([08fa114](https://github.com/carry0987/Vylux/commit/08fa114964e1939afe8878b6d23adb55a5a2d38a))

## [1.2.1](https://github.com/carry0987/Vylux/compare/v1.2.0...v1.2.1) (2026-07-23)


### Bug Fixes

* **queue:** always include options in video full task payload ([bf85e8a](https://github.com/carry0987/Vylux/commit/bf85e8acd18134b5a5fb3b55cd639e1fc28e0130))

## [1.2.0](https://github.com/carry0987/Vylux/compare/v1.1.0...v1.2.0) (2026-07-19)


### Features

* standardize canceled job status across jobs and queue flow ([3a7e638](https://github.com/carry0987/Vylux/commit/3a7e638d8b3dc5a3e576c59f3edb28c0a698ae03))


### Bug Fixes

* **ci:** Add formatting & tidy ([3748a81](https://github.com/carry0987/Vylux/commit/3748a816fe4088620f98179158867f16c9fdbbc6))
* Update packages ([c57ce23](https://github.com/carry0987/Vylux/commit/c57ce234e3af267f28bfebc23b9a217ac9d92788))
* Update Shaka Packager to v3.9.2 ([b80608c](https://github.com/carry0987/Vylux/commit/b80608cecf158f2b8b967eff21f757eeff49764f))

## [1.1.0](https://github.com/carry0987/Vylux/compare/v1.0.0...v1.1.0) (2026-04-07)


### Features

* **config:** validate and normalize URL settings ([72b3789](https://github.com/carry0987/Vylux/commit/72b3789387a3ebe50a1d9ba405cc1525b498d146))

## 1.0.0 (2026-04-05)


### Features

* import initial Vylux codebase ([7cf04f2](https://github.com/carry0987/Vylux/commit/7cf04f2bb1a569fdd8ad991fb19662816eaddba7))
