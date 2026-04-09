---
title: 整合導覽
description: "把 Vylux 的 job 結果、object key 與對外 URL、簽名請求、播放流程串成一條可實作的整合路徑。"
---

# 整合導覽

這一頁的目的是把分散在各 API 文件裡的規則，整理成實際整合時要走的路徑。

先講結論：

- Vylux 負責處理媒體並提供投遞端點
- 你的上游應用負責決定誰可以存取這些媒體
- 圖片 URL 的 HMAC 簽名與播放用 Bearer token 都應由你的應用產生
- job 結果常常回的是 storage key，不一定是可直接公開給客戶端的 URL

如果你已經知道簽名規則，這一頁可以當成「不同 use case 應該走哪個 endpoint」的總覽。

:::tip 整合時最核心的規則
job 結果常常只會給你 storage key。你的應用通常還需要把它轉成已簽名的 `/thumb` URL、對外的 `/stream/{hash}` 播放入口，或帶 token 的 `/api/key/{hash}` 存取方式。
:::

## Vylux 負責什麼，不負責什麼

Vylux 會負責：

- 把來源媒體處理成各種輸出
- 提供已簽名的圖片與媒體投遞端點
- 提供 `/stream/{hash}/*` 下的 HLS playlist 與 segment 存取
- 驗證 `/api/key/{hash}` 的 Bearer token

Vylux 不負責：

- 判斷哪個終端使用者可以看哪份媒體
- 幫你簽發業務層的 auth token 或 session
- 儲存你的授權規則
- 直接把 bucket URL 當成公開 URL 給前端

這是刻意的邊界設計。Vylux 專注在媒體處理與媒體投遞語義；你的應用專注在存取控制。

## Secret 與簽名責任

| Secret | 用途 | 應由誰持有 |
| --- | --- | --- |
| `API_KEY` | 內部 `/api/*` 管理端點，例如 `/api/jobs` | 只應在你的 backend 或內部工具中持有 |
| `HMAC_SECRET` | 簽署 `/img`、`/original`、`/thumb` URL | 只應在你的 backend 或簽名服務中持有 |
| `KEY_TOKEN_SECRET` | 簽署 `/api/key/{hash}` 的 Bearer token | 只應在你的 backend 或授權服務中持有 |

不要把這些值暴露給瀏覽器、行動 App 或任何公開客戶端。

## 先選對 endpoint

| Use case | Endpoint 形式 | 補充 |
| --- | --- | --- |
| 即時 resize 或轉檔來源圖片 | `/img/{sig}/{opts}/{encoded_source}.{format}` | 使用 `HMAC_SECRET` 簽名 |
| 受控地顯示或下載來源原檔 | `/original/{sig}/{encoded_key}` | 使用 `HMAC_SECRET` 簽名 |
| 顯示已產生的縮圖、封面、preview 或其他 media-bucket 內圖片資產 | `/thumb/{sig}/{encoded_key}` | 使用 `HMAC_SECRET` 簽名，且 signing domain 要加 `thumb/` |
| 播放 HLS | `/stream/{hash}/master.m3u8` | playlist 與 segment 請求本身不需要 auth header |
| 取得加密播放所需 content key | `/api/key/{hash}` 搭配 `Authorization: Bearer {token}` | token 用 `KEY_TOKEN_SECRET` 簽署 |

## job 結果如何變成對外 URL

job 結果常常會回 `videos/.../cover.jpg` 或 `videos/.../master.m3u8` 這種 object key。

除非文件明確說這已經是 public endpoint，否則應把它當成內部 storage reference，而不是直接公開給客戶端的 URL。

| Job result 欄位 | 代表什麼 | 你的應用通常應該公開什麼 |
| --- | --- | --- |
| 圖片類輸出的 `results.key` | media bucket 內的 object key | 簽一個 `/thumb/{sig}/{encoded_key}` URL |
| `results.artifacts.cover.key` | media bucket 內生成的 cover key | 簽一個 `/thumb/{sig}/{encoded_key}` URL |
| `results.artifacts.preview.key` | media bucket 內生成的 preview key | 簽一個 `/thumb/{sig}/{encoded_key}` URL |
| `results.streaming.master_playlist` | media bucket 內 HLS master playlist 的 object key | 對外公開 `/stream/{hash}/master.m3u8` |
| `results.encryption.key_endpoint` | 已經是對外的 key endpoint URL | 僅在播放器請求它時附上 Bearer token |

這裡有兩個容易混淆的重點：

- `videos/mo/movie-2026-04-01/cover.jpg` 這種 key 不是你應直接公開給瀏覽器的最終 URL
- HLS 播放時，穩定的對外入口應是 `/stream/{hash}` 這組路徑，而不是 raw media-bucket key

## 常見整合模式

### 模式 1：動態圖片投遞

你的應用持有一個來源 object key，例如 `uploads/avatars/sample.jpg`，當客戶端要某個尺寸或格式時，再動態簽出 `/img` URL。

流程：

1. 你的應用決定輸出參數，例如 `w640_h360_q80` 與 `webp`
2. 你的應用用 `HMAC_SECRET` 對 canonical payload 做簽名
3. 你的應用把最終 `/img/...` URL 回給瀏覽器或 CDN

下一步可讀：

- [圖片與媒體投遞 API](./api/image-delivery)
- [圖片處理流程](./media/image-pipeline)

### 模式 2：縮圖、封面與 preview 資產

對 `image:thumbnail`、`video:cover`、`video:preview` 這些 job，Vylux 會把穩定的輸出寫進 media bucket。

你的應用通常應該：

1. 從 job 結果或 callback payload 讀出 media-bucket key
2. 用 `HMAC_SECRET` 簽一個 `/thumb/{sig}/{encoded_key}` URL
3. 把這個 `/thumb` URL 回給客戶端

這樣做比直接暴露 storage key 更好，因為投遞語義與簽名都能維持在 Vylux 的邊界之內。

### 模式 3：未加密 HLS 播放

對未加密的 `video:transcode` 或 `video:full`：

1. 你的應用送出 job
2. 等待 `status=completed`
3. 對外公開 `/stream/{hash}/master.m3u8`
4. 播放器會繼續抓 `/stream/{hash}/*` 下的 playlist 與 segments

這種情況不需要 Bearer token。

### 模式 4：加密 HLS 播放

對有開啟加密的 `video:transcode` 或 `video:full`：

1. 你的應用送出 job 並等待完成
2. 對外公開 `/stream/{hash}/master.m3u8`
3. 你的應用產生一個 payload 內含同一個 media `hash` 的 Bearer token
4. 播放器只在 `/api/key/{hash}` 請求上附 `Authorization: Bearer {token}`

不要把 token 放在 playlist URL 或 segment URL 上。

下一步可讀：

- [播放 API](./api/playback)
- [加密串流](./media/encrypted-streaming)

## 端到端檢查清單

若你要判斷整合是否完整，至少確認：

- backend 可以帶 `X-API-Key` 建立 jobs
- backend 可以用 `HMAC_SECRET` 簽 `/img`、`/original`、`/thumb`
- 前端永遠看不到 `API_KEY`、`HMAC_SECRET`、`KEY_TOKEN_SECRET`
- HLS 播放入口使用 `/stream/{hash}/master.m3u8`
- 加密播放只對 `/api/key/{hash}` 附 `Authorization` header

## 接下來看哪裡

- [整合 Recipes](./integration-recipes) 看兩條具體的端到端流程
- [快速開始](./getting-started) 了解本機啟動與 smoke test
- [工作 API](./api/jobs) 了解 job schema 與結果 payload
- [圖片與媒體投遞 API](./api/image-delivery) 了解 HMAC 簽名細節
- [播放 API](./api/playback) 了解 token 格式與 key endpoint 行為
