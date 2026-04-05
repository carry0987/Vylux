---
title: 圖片處理流程
description: "同步圖片處理路徑、URL 簽名 canonicalization、cache 層次，以及 `image:thumbnail` 的關係。"
---

# 圖片處理流程

Vylux 的圖片處理是同步路徑，主要目標是低延遲與高命中率。

## 端點

- `/img/:sig/:opts/*`: 即時處理後回傳
- `/original/:sig/*`: 代理原始物件
- `/thumb/:sig/*`: 代理已產出的圖片資產

## `/img` 的核心流程

1. 從路徑解析 output format 與 `sourceKey`
2. 解析 `opts`，支援 `w`、`h`、`q`
3. 驗證 HMAC 簽名
4. 查記憶體 LRU
5. 查 media bucket 中的 storage cache
6. miss 時從 source bucket 讀原圖
7. 呼叫 libvips 做 resize / encode
8. 同步寫回 LRU，非同步寫回 storage cache
9. 回傳圖片 bytes 與 CDN-friendly header

## 簽名 canonicalization

Vylux 不直接對 request URL 原字串做 HMAC，而是先 canonicalize：

- `opts` 會整理成固定順序 `w -> h -> q`
- `encoded_source` 會先 decode 成 object key
- `jpeg` 會正規化為 `jpg`

這樣 `w300_h200_q80` 與 `h200_w300_q80` 不會造成不同 signature 與不同 cache key。

## 快取層次

| 層 | 角色 | 寫入時機 |
| --- | --- | --- |
| 記憶體 LRU | 同一個 process 內的熱資料命中 | 同步 |
| media bucket storage cache | 跨 process / 跨 Pod 共用的派生圖片快取 | 非同步 |
| CDN | 對外分發層 | 由 response header 驅動 |

回應 header 目前設計為：

- `Cache-Control: public, max-age=31536000, immutable`
- `ETag` 依輸出內容計算
- `Vary: Accept`

## 快取路徑

圖片即時處理結果會落到：

```text
cache/{processing_hash}.{format}
```

其中 `processing_hash` 來自 source key 與處理參數的組合，而不是上游提交 job 時的 `hash`。

## singleflight 與併發

同步圖片路徑內部使用兩組 singleflight：

- `sourceFlight`：避免同一張原圖被重複抓取
- `processFlight`：避免同一組轉換被重複運算

這對快取冷啟動時的突發流量很重要，因為多個相同 request 不會同時觸發多次昂貴處理。

## `/original` 與 `/thumb`

這兩個端點不做轉換：

- `/original` 代理 source bucket 原始物件
- `/thumb` 代理 media bucket 已存在的縮圖、cover 或其他靜態圖片

兩者都要求 HMAC URL 簽名，但 `/thumb` 會使用 `thumb` domain prefix，避免與 `/original` 互相重用 signature。

## `image:thumbnail` 與同步圖片路徑的差別

雖然兩者都處理圖片，但用途不同：

| 路徑 | 型態 | 輸出位置 | 典型用途 |
| --- | --- | --- | --- |
| `/img` | 同步 | `cache/{processing_hash}.{format}` | 瀏覽器即時轉圖、動態尺寸 |
| `image:thumbnail` | 非同步 | `images/{prefix}/{hash}/{variant}.{format}` | 穩定變體、後台預先生成 |

`image:thumbnail` 不依賴 `/img`，而是由 worker 直接處理並產出明確 variant 名稱。

## 錯誤語義

同步圖片處理目前的語義偏嚴格：

- `400`：參數或格式錯誤
- `403`：簽名錯誤
- `404`：source object 不存在
- `422`：來源存在但無法處理
- `502`：source storage 暫時失敗
- `500`：其他內部錯誤

這條路徑不做偽裝 fallback，讓錯誤能被上游與監控系統清楚觀察。
