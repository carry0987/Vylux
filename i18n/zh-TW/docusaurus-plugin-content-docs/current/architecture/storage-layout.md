---
title: 儲存結構
description: "source store、media store、PostgreSQL 與 `/stream` 路徑之間的實際對應關係。"
---

# 儲存結構

Vylux 使用兩個邏輯上的 storage 角色：

- source store：透過 `SOURCE_S3_*` 讀取 `SOURCE_BUCKET` 中的原始檔與上游來源物件
- media store：透過 `MEDIA_S3_*` 讀寫 `MEDIA_BUCKET` 中的衍生媒體、manifests、keys、preview 與圖片輸出

這兩個角色可以落在同一個 S3-compatible 服務，也可以分開部署，但在 Vylux 內部仍是獨立設定，彼此不會自動回退。

另外還有兩塊非 bucket 狀態：

- PostgreSQL：保存 job state、workflow results、wrapped encryption keys、image cache tracking
- Redis：保存 queue state 與 rate limit 狀態

:::tip 把 storage reference 與 public URL 分開看
本頁大多數 object key 都是內部儲存路徑。對外客戶端通常應拿到的是已簽名的 `/thumb`、`/original`，或 `/stream/{hash}` 這類穩定 public route。
:::

## Storage 角色

source store 被視為不可變的輸入來源。media store 則是 Vylux 寫入處理結果與 cleanup 目標的位置。

### Source store

- 由 `SOURCE_BUCKET` 與 `SOURCE_S3_*` 設定
- 被視為上游擁有的輸入來源
- Vylux 只應讀取，不應把衍生輸出寫回去

### Media store

- 由 `MEDIA_BUCKET` 與 `MEDIA_S3_*` 設定
- 保存生成圖片、cover、preview、manifest、segment 與 cache entries
- Vylux 會在這裡讀寫

### State stores

- PostgreSQL 保存可變的 job、retry、key 與 cache-tracking metadata
- Redis 保存 queue state 與 rate-limit counters

## 媒體儲存桶結構

```text
media-bucket/
├── images/{prefix}/{hash}/...
├── videos/{prefix}/{hash}/
│   ├── cover.jpg
│   ├── preview.webp
│   ├── preview.gif
│   ├── master.m3u8
│   ├── audio/und_aac_2ch/
│   │   ├── init.mp4
│   │   ├── playlist.m3u8
│   │   └── seg_*.m4s
│   └── video/
│       ├── r1080_av1/
│       ├── r720_av1/
│       ├── r480_av1/
│       ├── r360_av1/
│       ├── r240_av1/
│       ├── r1080_h264/
│       ├── r720_h264/
│       ├── r480_h264/
│       ├── r360_h264/
│       └── r240_h264/
└── cache/{processing_hash}.{format}
```

## object key 命名規則

### 同步圖片快取

即時圖片處理結果寫入：

```text
cache/{processing_hash}.{format}
```

這裡的 `processing_hash` 來自 source object key、width、height、quality 與 output format。

### 非同步圖片 thumbnail

`image:thumbnail` 會寫入：

```text
images/{hash_prefix}/{hash}/{variant}.{format}
```

例如：

```text
images/ab/abcdef1234/thumb.webp
images/ab/abcdef1234/large.jpg
```

### 影片相關輸出

cover、preview、HLS artifacts 都寫入：

```text
videos/{hash_prefix}/{hash}/{relative_path}
```

例如：

```text
videos/ab/abcdef1234/cover.jpg
videos/ab/abcdef1234/preview.webp
videos/ab/abcdef1234/master.m3u8
videos/ab/abcdef1234/audio/und_aac_2ch/init.mp4
videos/ab/abcdef1234/video/r720_h264/seg_000.m4s
```

## 為什麼有 `hash_prefix`

圖片與影片 key 都會使用 `hash` 的前兩個字元作為 prefix，主要目的是避免單一路徑下聚集過多物件，讓 object key 分布更均勻：

```text
images/{hash[0:2]}/{hash}/...
videos/{hash[0:2]}/{hash}/...
```

## `/stream` path mapping

依 job 類型不同，media bucket 內可能出現：

- image cache objects
- `cover.jpg`
- `preview.webp`
- `preview.gif`
- `master.m3u8`
- `video/...` 變體 playlist 與 segments
- `audio/...` playlist 與 segments

`/stream/{hash}/*` 對外提供穩定的播放 URL，對應 media bucket 中已存在的物件。

對外 URL：

```text
/stream/{hash}/master.m3u8
/stream/{hash}/audio/{track_id}/playlist.m3u8
/stream/{hash}/video/{variant}/playlist.m3u8
/stream/{hash}/video/{variant}/seg_1.m4s
```

對內 bucket key：

```text
videos/{hash_prefix}/{hash}/{filePath}
```

其中 `filePath` 就是 `/stream/{hash}/` 後面的相對路徑。

:::note `/stream/{hash}` 才是穩定的播放對外契約
media bucket 內部可能儲存的是 `videos/{prefix}/{hash}/master.m3u8`，但對外播放入口仍應優先使用 `/stream/{hash}/master.m3u8`。
:::

## 什麼不在 bucket 內

有些關鍵資料不會寫進 media bucket：

- wrapped content keys 不存成 object，而是存 PostgreSQL
- key unwrap 所需的 `kek_version` 與 `wrap_nonce` 也在 PostgreSQL
- image cache entry tracking 在 PostgreSQL 的 `image_cache_entries`
- job status、progress、results、retry plan 都在 PostgreSQL

這樣做的原因是：

- segment 與圖片是大量靜態物件，適合 object storage
- 金鑰與工作狀態是需要查詢、更新與權限控制的 metadata，適合資料庫

## source store 角色

source bucket 原則上只承載上游應用提供的原始內容，例如：

```text
uploads/sample.jpg
uploads/sample.mp4
uploads/user-avatars/123.png
```

Vylux 會讀取這些物件，但不應把導出結果再寫回 source bucket。

`/original` 是帶簽名的 source-object proxy：它直接從 source store 讀取並串流原始物件，不會先複製到 media store。

## 清理影響範圍

當執行 cleanup 時，Vylux 會依 `hash` 清掉：

- `images/{prefix}/{hash}/...`
- `videos/{prefix}/{hash}/...`
- 相關生成圖片快取的 tracking records
- 對應的 encryption key 與 job rows
