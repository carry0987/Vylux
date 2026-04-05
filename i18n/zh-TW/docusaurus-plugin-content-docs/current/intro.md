---
sidebar_position: 1
title: 簡介
description: "Vylux 的文件入口，概述服務能力、部署形態與應優先閱讀的頁面。"
---

# Vylux

Vylux 是一個獨立運行的媒體處理服務，將圖片即時轉換與影片非同步處理拆成可單獨部署的基礎能力。它不承載你的業務模型，只負責接收來源物件、驗證資訊與處理參數，輸出可快取、可追蹤、可播放的媒體資產。

目前的核心能力包括：

- 即時圖片轉換：`/img` 依 URL 參數即時 resize、轉檔與快取
- 原檔與縮圖代理：`/original`、`/thumb`
- 非同步工作：`image:thumbnail`、`video:cover`、`video:preview`、`video:transcode`、`video:full`
- HLS CMAF：AV1 + H.264 ladder、fMP4 segment、Shaka Packager 打包
- 加密播放：CBCS / SAMPLE-AES、`/api/key/{hash}` Bearer token 金鑰發放
- 維運能力：PostgreSQL job state、Redis queue、Prometheus metrics、OpenTelemetry tracing

## 服務形態

Vylux binary 只有一個，但支援三種執行模式：

- `all`：同時啟動 HTTP server 與 worker，適合本機與小規模部署
- `server`：只提供 HTTP API、圖片處理、播放代理與主要 metrics
- `worker`：只處理 queue 任務，並在 `WORKER_METRICS_PORT` 提供 `/metrics` 與 `/healthz`

這個設計讓同一個映像可同時用於 Docker Compose、單機部署，以及 K8s 下的 server/worker 拆分。

## 目前已驗證的能力

- `/img` 即時圖片縮放、格式轉換與快取
- `video:preview` 動態預覽，支援 `webp` 與 `gif`
- `video:transcode` 產出 HLS CMAF
- AV1 + H.264 雙 codec ladder
- portrait / non-16:9 影片的實際解析度輸出
- raw-key CBCS / SAMPLE-AES 加密串流
- `/api/key/{hash}` Bearer token 金鑰發放
- PostgreSQL job state、Redis queue、Prometheus metrics、OpenTelemetry tracing

## 建議閱讀路徑

1. 先讀 [快速開始](./getting-started)
2. 再讀 [整合導覽](./integration-guide)，先搞清楚 job 結果如何變成對外 URL、簽名請求與播放流程
3. 接著讀 [設定](./operations/configuration) 與 [部署](./operations/deployment)
4. 若你要接 API，先看 [工作 API](./api/jobs) 與 [圖片與媒體投遞 API](./api/image-delivery)
5. 若你關心串流保護與播放器接入，直接看 [播放 API](./api/playback) 與 [加密串流](./media/encrypted-streaming)
6. 若你在做維運或除錯，直接進入 [可觀測性](./operations/observability)

## 文件範圍

目前這個 docs site 聚焦於：

- 服務結構與核心資料流
- 媒體處理 pipeline
- HTTP endpoints、授權方式與 curl 範例
- 部署、設定、觀測與測試

## 文件原則

- 以目前程式碼與測試行為為準
- 以部署與接入可操作性為優先，而不是抽象設計描述
- 會吸收 repo 根目錄與本機輔助資料中已驗證、但不適合長期保留在正式發行包內的操作知識
