---
title: 整合 Recipes
description: "兩條可直接落地的端到端整合流程：公開生成封面圖，以及接上加密 HLS 播放。"
---

# 整合 Recipes

這一頁把抽象的 endpoint 規則，收斂成可以直接落地的整合流程。

如果你已讀過 [整合導覽](./integration-guide)，這一頁就是更具體的 application-side 做法。

## Recipe 1：公開一張已生成的封面圖

這條流程適用於你的應用建立了 `video:cover` job，或從 `video:full` workflow 讀到 `cover` artifact。

目標：

回傳一個可以安全給瀏覽器使用的封面圖 URL，同時不暴露 raw bucket path 或 `HMAC_SECRET`。

流程：

1. 你的 backend 以 `X-API-Key` 送出 `video:cover` 或 `video:full` job
2. 你的 backend 輪詢 job 結果或接收 callback payload
3. 你的 backend 讀出生成結果 key，例如 `videos/mo/movie-2026-04-01/cover.jpg`
4. 你的 backend 用 `HMAC_SECRET` 簽出 `/thumb/{sig}/{encoded_key}` URL
5. 你的 backend 把這個 `/thumb/...` URL 回給瀏覽器

瀏覽器應該拿到類似這樣的 URL：

```text
https://media.example.com/thumb/<sig>/videos%2Fmo%2Fmovie-2026-04-01%2Fcover.jpg
```

瀏覽器不應該拿到：

- raw media-bucket key，例如 `videos/mo/movie-2026-04-01/cover.jpg`
- `HMAC_SECRET`
- 繞過 Vylux 投遞語義的直接 storage URL

backend 通常要做的事：

- 驗證當前呼叫者是否可以存取這份媒體
- 從資料庫或完成後的 job payload 取出 cover key
- 在回應 API 之前，現場簽出 `/thumb` URL

為什麼這裡要用 `/thumb`：

cover 與 thumbnail 都已經是 media bucket 內存在的衍生資產，不需要再走 `/img` 做即時轉換。

資產已存在時，用 `/thumb`。

只有在你要從 source bucket 原圖做即時轉換時，才用 `/img`。

## Recipe 2：接上加密 HLS 播放

這條流程適用於 `video:transcode` 或 `video:full` 在開啟加密後完成的情境。

目標：

讓播放器載入 `/stream/{hash}/master.m3u8`，同時把 content-key 存取放在短效 Bearer token 後面。

流程：

1. 你的 backend 送出 transcode job
2. 你的 backend 等待 `status=completed`
3. 你的 backend 對外公開 `/stream/{hash}/master.m3u8` 作為播放入口
4. 你的 backend 產生一個短效 Bearer token，payload 內必須包含相同 media `hash`
5. 你的 frontend 用 playlist URL 與 token 初始化播放器
6. 播放器會從 `/stream/{hash}/*` 抓 playlist 與 segments
7. 播放器只在請求 `/api/key/{hash}` 時附上 `Authorization: Bearer {token}`

frontend 通常只需要兩個值：

- playlist URL，例如 `https://media.example.com/stream/movie-2026-04-01/master.m3u8`
- 給 `/api/key/movie-2026-04-01` 使用的短效 key token

不要這樣做：

- 不要把 key token 放進 playlist URL query string
- 不要把 key token 放進 segment URL
- 不要把 `KEY_TOKEN_SECRET` 暴露到瀏覽器
- 不要把 `results.streaming.master_playlist` 直接當成最終 public URL

建議的 backend 結構：

1. 應用先驗證使用者身份
2. 應用確認這個使用者可以存取該 media hash
3. 應用回傳 public `/stream/{hash}/master.m3u8` URL
4. 應用同時回傳由 server-side 產生的短效 Bearer token

如果你想把責任拆開，也可以由獨立的內部授權服務簽發 token，而不是主應用 backend 本身。

播放器至少要做到的事：

- 從 `/stream/{hash}/master.m3u8` 載入 playlist
- 只對 `/api/key/` 請求加上 `Authorization` header

這樣才能同時保留 HLS 物件的可快取性，以及 key delivery 的受控邊界。

:::warning 公開客戶端不應自己從 secrets 組出這些 URL
在這兩條流程裡，最終 URL 或 token 都應由 backend 或內部 auth/signing service 準備好；瀏覽器只應拿到實際要用的最終值。
:::

## 發佈前交叉檢查

在你認定整合完成前，至少確認：

- 應用回傳給前端的 cover / thumbnail URL 走的是 `/thumb`，不是 raw bucket key
- 加密 HLS 播放走的是 `/stream/{hash}/master.m3u8`
- `/api/key/{hash}` 在沒 token 時回 `401`，有合法 token 時回 `200`
- 公開客戶端永遠看不到 `API_KEY`、`HMAC_SECRET`、`KEY_TOKEN_SECRET`

## 相關文件

- [整合導覽](./integration-guide)
- [工作 API](./api/jobs)
- [圖片與媒體投遞 API](./api/image-delivery)
- [播放 API](./api/playback)
