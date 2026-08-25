---
title: 音訊處理流程
description: "第一級音訊工作路徑的 public create contract、輸出 profile、儲存結構與 worker 行為。"
---

# 音訊處理流程

Vylux 透過 `POST /api/audio/jobs` 提供音訊處理能力。

音訊被視為獨立媒體領域，而不是影片的附帶情境。

## Public create contract

音訊工作透過 domain-specific 路由建立：

- `POST /api/audio/jobs`

request body 以輸出導向為主，不再使用舊的 generic `type + options` schema，也不需要 `asset_type` 這種 discriminator 欄位。

典型請求：

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

## 支援的輸出

目前音訊路徑可產出：

- audio-only HLS 播放輸出
- MP3 download artifact
- FLAC download artifact
- waveform JSON artifact
- job result payload 內的 probe metadata

## 儲存結構

衍生音訊資產都放在專用的 `audio/...` namespace：

- `audio/{prefix}/{hash}/hls/...`
- `audio/{prefix}/{hash}/downloads/...`
- `audio/{prefix}/{hash}/waveform/...`

probe analysis 目前直接保存在 job result payload，而不是另外寫成 `analysis/...` 檔案。

## 處理階段

worker 目前會為以下階段記錄結構化狀態：

- `source`
- `probe`
- `package`
- `downloads`
- `waveform`

失敗時會保留 stage-level 結果，而不是只回一條不可分辨的錯誤字串。

## Worker 行為

內部 worker task 目前使用 `audio:transcode`。整體流程如下：

1. 把來源 object 下載到 scratch workspace
2. 用 `ffprobe` 分析來源
3. 驗證來源格式是否支援
4. 視需求產生 waveform JSON
5. 視需求產生 audio-only HLS
6. 視需求產生 MP3 與 FLAC download artifact
7. 回寫最終 job result，並在需要時送出 callback

目前 retry model 是 whole-job retry，而不是 per-stage retry orchestration。

## 播放入口

音訊 HLS 對外播放入口目前是：

```text
/stream/{hash}/hls/master.m3u8
```

`results.streaming.master_playlist` 應視為 storage-backed route target，而不是 raw bucket URL。

## 相關頁面

- [工作 API](../api/jobs)
- [播放 API](../api/playback)
- [請求生命週期](../architecture/request-lifecycle)
