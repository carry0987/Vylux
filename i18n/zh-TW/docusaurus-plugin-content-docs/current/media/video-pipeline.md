---
title: 影片處理流程
description: "cover、preview、transcode、video:full 的處理模式，包含 queue、輸出路徑、ladder 與 workflow 行為。"
---

# 影片處理流程

Vylux 的影片處理目前有四個主要 job 類型：

- `video:cover`
- `video:preview`
- `video:transcode`
- `video:full`

## 任務與輸出對照

| Job type | 主要輸出 | 典型 key |
| --- | --- | --- |
| `video:cover` | 靜態封面 | `videos/{prefix}/{hash}/cover.jpg` |
| `video:preview` | 動態預覽 | `videos/{prefix}/{hash}/preview.webp` 或 `preview.gif` |
| `video:transcode` | HLS CMAF 套件 | `videos/{prefix}/{hash}/master.m3u8` 與 `audio/`、`video/` 子樹 |
| `video:full` | cover + preview + transcode 聚合結果 | 同上，但以單一 workflow 結果回寫 |

## Queue 與重試

- `video:cover`：`default` queue，`MaxRetry(3)`
- `video:preview`：`default` queue，`MaxRetry(3)`
- `video:transcode`：一般檔案進 `default`，達到 `LARGE_FILE_THRESHOLD` 的檔案進 `video:large`，並使用 `MaxRetry(2)`
- `video:full`：一般檔案進 `default`，達到 `LARGE_FILE_THRESHOLD` 的檔案進 `video:large`，並使用 `MaxRetry(2)`

Worker server 現在拆成兩個 pool：

- normal pool：處理 `critical` 與 `default`
- large pool：專門處理 `video:large`

`LARGE_WORKER_CONCURRENCY` 用來控制 large pool，預設值為 `1`。

## 大檔案行為

大檔轉碼在操作上需要額外預期：

- `LARGE_FILE_THRESHOLD` 預設是 `5 GiB`
- 超過這個門檻後，`video:transcode` 與 `video:full` 會被路由到 `video:large`
- 路由依據是提交時從 storage 查到的實際來源檔案大小
- 若來源超過 `MAX_FILE_SIZE`，API 會在入列前直接拒絕請求
- 這類任務通常比 preview 或 cover 跑得更久，也更吃暫存磁碟空間

實務上可分成兩種輸出處理方式：

- 較小輸出可等整個輸出目錄完成後再一次上傳
- 較大輸出則可能在檔案穩定後分批上傳，以降低本地暫存壓力

如果你的場景包含超大來源影片，應預先規劃 worker 的 scratch space、較保守的 concurrency，以及更長的任務生命週期。

容器部署下，標準 scratch workspace 是 `/var/cache/vylux`。來源下載、轉碼 intermediate MP4、HLS 打包輸出，以及 `TMPDIR` 相關的暫存使用，都應集中到這個路徑。

## `video:cover`

`video:cover` 的流程最短：

1. 下載來源影片到暫存檔
2. 在指定時間點擷取 frame，預設 `1s`
3. 產出 JPEG cover
4. 上傳到 `cover.jpg`
5. 回寫 artifact result

雖然 `video:cover` 也會使用共享 scratch workspace 來下載來源影片，但它本身仍是小型工作，因此維持在 `default` queue。

## `video:preview`

preview 輸出目前支援：

- `preview.webp`
- `preview.gif`

預設格式是 `webp`，若外部系統需要，也可以明確請求 `gif`。

典型參數包括：

- `start_sec`
- `duration`
- `width`
- `fps`
- `format`

## `video:transcode`

轉碼流程採兩段式：

1. FFmpeg 先輸出 audio 與各 video intermediate MP4
2. Shaka Packager 再輸出 HLS CMAF

更完整地說：

1. `ffprobe` 先取得來源影片的 display geometry 與 audio 情況
2. 依幾何資訊解析實際可輸出的 ladder
3. 先產生共享 audio track 與各 codec family 的 intermediate MP4
4. 交給 Shaka Packager 產出 `master.m3u8`、variant playlists、`init.mp4`、`.m4s` segments
5. 遞迴上傳整個輸出目錄到 media bucket

## 標準梯級（rungs）

系統內部用 canonical rungs 表示 ladder：

- `r1080`
- `r720`
- `r480`
- `r360`
- `r240`

每個 rung 會同時生成 AV1 與 H.264 版本，因此預設是雙 codec ladder。

但 rung 名稱不代表最終一定是固定 16:9 尺寸。`r720_h264` 表示的是 rung 類型，不是強制 `1280x720`。

## 實際尺寸如何決定

worker 會先用 `ffprobe` 取得來源影片的顯示幾何資訊，再套用以下規則：

- 依來源短邊過濾 rung
- 保持原始比例
- 避免 upscale
- 考慮 rotation metadata
- 縮放後用 `setsar=1` 固定為 square pixels

因此 API results 的 `width/height`、播放器看到的 `RESOLUTION`、以及實際顯示尺寸會保持一致。

例如：

- `1920x1080` → `1920x1080`, `1280x720`, `854x480`, `640x360`, `426x240`
- `1080x1920` → `608x1080`, `406x720`, `270x480`, `202x360`, `136x240`

## 雙 codec 輸出

在支援 AV1 的環境中，預設輸出兩套 ladder：

- AV1
- H.264

播放器會依 `CODECS` 能力選擇；不支援 AV1 的裝置自動落回 H.264。

## Audio 軌模型

目前預設會產生一條共用 audio track：

- ID：`und_aac_2ch`
- codec：AAC
- channels：2

所有 video variants 會透過 playlist metadata 指向這條 audio track。

## `video:full`

`video:full` 是現行推薦的聚合任務。它的邏輯是：

1. 下載來源影片一次
2. cover 與 preview 並行
3. 若前兩者都成功，再進 transcode
4. 聚合 `artifacts`、`stages`、`retry_plan`

`video:full` 本身也會套用大檔路由規則；如果來源影片很大，整個 workflow 會先進到專用的 `video:large` worker pool。

失敗時不只是一個錯誤字串，而是保留：

- 哪個 stage 失敗
- 是否 retryable
- 建議補跑哪些 job types

這對上游管理界面與自動補償流程很有用。

## 產出結果結構

`video:transcode` / `video:full` 的 transcode artifact 目前會包含：

- `streaming.protocol = hls`
- `streaming.container = cmaf`
- `streaming.master_playlist`
- `audio_tracks[]`
- `video_tracks[]`
- 若加密則附 `encryption.scheme`、`kid`、`key_endpoint`

## 何時看下一頁

如果你關心加密轉碼與 `/api/key/{hash}` 的整條生命週期，下一頁讀 [加密串流](./encrypted-streaming)。
