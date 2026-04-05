---
title: 圖片與媒體投遞 API
description: "`/img`、`/original`、`/thumb` 的 URL 模型、HMAC 簽名方式與 curl 範例。"
---

# 圖片與媒體投遞 API

這一組端點負責同步回應圖片或既有媒體物件，適合直接給瀏覽器、CDN 或上游應用使用。

下方簽名範例涉及的 `BASE_URL` 與 `HMAC_SECRET` 完整說明，請見 [設定](../operations/configuration)。

## 端點總覽

| Endpoint | 用途 | Auth | 後端 bucket |
| --- | --- | --- | --- |
| `GET /img/{sig}/{opts}/{encoded_source}.{format}` | 即時轉換圖片並快取 | HMAC URL 簽名 | source bucket 讀取，media bucket 寫快取 |
| `GET /original/{sig}/{encoded_key}` | 代理原始物件 | HMAC URL 簽名 | source bucket |
| `GET /thumb/{sig}/{encoded_key}` | 代理既有縮圖或封面 | HMAC URL 簽名 | media bucket |

## `GET /img/{sig}/{opts}/{encoded_source}.{format}`

### 路徑格式

```text
/img/{sig}/{opts}/{encoded_source}.{format}
```

例子：

```text
/img/<sig>/w640_h360_q80/uploads%2Favatars%2Fsample.jpg.webp
/img/<sig>/w1200/uploads%2Fproducts%2Fhero.png.avif
```

### 參數語義

| 片段 | 說明 |
| --- | --- |
| `sig` | `HMAC-SHA256` 十六進位簽名 |
| `opts` | 圖片參數，支援 `w`、`h`、`q`，例如 `w640_h360_q80` |
| `encoded_source` | URL-escaped 的原始 object key |
| `format` | 輸出格式，支援 `webp`、`avif`、`jpg`、`png`、`gif` |

### 簽名 canonicalization

Vylux 不是直接對瀏覽器 URL 字串做 HMAC，而是對 canonical form 做簽名：

- `opts` 會固定排序為 `w` -> `h` -> `q`
- `encoded_source` 會先解碼成 object key
- `jpeg` 會被正規化為 `jpg`

實際上，簽名時的字串可理解為：

```text
{canonical_options}/{decoded_source_key}.{canonical_format}
```

### shell 簽名與 curl 範例

```bash showLineNumbers
BASE_URL='http://localhost:3000'
HMAC_SECRET='replace-with-hmac-secret'
OPTIONS='w640_h360_q80'
ENCODED_SOURCE='uploads%2Favatars%2Fsample.jpg.webp'
CANONICAL_SOURCE='uploads/avatars/sample.jpg.webp'

SIG="$(printf '%s/%s' "$OPTIONS" "$CANONICAL_SOURCE" \
  | openssl dgst -sha256 -hmac "$HMAC_SECRET" -hex \
  | sed 's/^.* //')"

curl -L "$BASE_URL/img/$SIG/$OPTIONS/$ENCODED_SOURCE"
```

### 成功回應特性

- `200 OK`
- `Content-Type` 為輸出格式對應的 MIME type
- `Cache-Control: public, max-age=31536000, immutable`
- `ETag` 依輸出內容計算

### 失敗語義

| 狀態碼 | 代表情況 |
| --- | --- |
| `400` | 參數錯誤、source encoding 錯誤、unsupported format |
| `403` | 簽名無效 |
| `404` | source object 不存在 |
| `422` | 原圖存在但無法解碼，或動畫轉靜態等不可處理情況 |
| `502` | source storage 暫時不可用 |
| `500` | 其他內部處理錯誤 |

## `GET /original/{sig}/{encoded_key}`

這個端點用於受控地代理 source bucket 原始物件，不做任何轉換。

### shell 簽名與 curl 範例

```bash showLineNumbers
BASE_URL='http://localhost:3000'
HMAC_SECRET='replace-with-hmac-secret'
ENCODED_KEY='uploads%2Fsample.mp4'
CANONICAL_KEY='uploads/sample.mp4'

SIG="$(printf '/%s' "$CANONICAL_KEY" \
  | openssl dgst -sha256 -hmac "$HMAC_SECRET" -hex \
  | sed 's/^.* //')"

curl -L "$BASE_URL/original/$SIG/$ENCODED_KEY"
```

### 行為說明

- 驗證 HMAC 後，直接從 source bucket 讀取物件
- `Content-Type` 會優先依副檔名判斷，否則用 sniffing
- 找不到物件時回 `404`

## `GET /thumb/{sig}/{encoded_key}`

這個端點代理 media bucket 中已存在的縮圖、封面或其他靜態媒體物件。它常用於：

- `image:thumbnail` job 的輸出
- `video:cover` job 的輸出

### shell 簽名與 curl 範例

```bash showLineNumbers
BASE_URL='http://localhost:3000'
HMAC_SECRET='replace-with-hmac-secret'
ENCODED_KEY='videos%2Fab%2Fabcdef%2Fcover.jpg'
CANONICAL_KEY='videos/ab/abcdef/cover.jpg'

SIG="$(printf 'thumb/%s' "$CANONICAL_KEY" \
  | openssl dgst -sha256 -hmac "$HMAC_SECRET" -hex \
  | sed 's/^.* //')"

curl -L "$BASE_URL/thumb/$SIG/$ENCODED_KEY"
```

### 行為說明

- 簽名 domain 會加上 `thumb/` 前綴，避免與 `/original` 共用簽名
- 成功時回傳對應 MIME type，並帶 `Access-Control-Allow-Origin: *`

## 快取與簽名實務建議

- 不要在前端暴露 `HMAC_SECRET`；簽名應由上游應用或授權服務產生
- 讓 object key 與輸出格式進入簽名範圍，避免 URL 被竄改後仍可命中
- 圖片 URL 可以交給 CDN 長時間快取；內容變更應透過不同 key 或 hash 避免覆寫語義

### signer 應該放在哪一層

在大多數部署裡，`/img`、`/original`、`/thumb` 的 signer 應該放在這類可信任位置之一：

- 你的主應用 backend
- 專門的內部簽名服務
- 能安全讀取 `HMAC_SECRET` 的可信 edge worker

常見請求流程是：

1. 使用者先向你的應用要求某張圖片或資產
2. 你的應用判斷這個使用者是否可存取對應媒體
3. 你的應用在 server-side 簽出 Vylux URL
4. 你的應用把最終已簽名 URL 回給瀏覽器

也就是說，瀏覽器只應該收到最終 URL，不應看到 secret，也不應自己持有 unsigned path 來簽名。

若你要公開 job 結果中的 cover、preview、thumbnail 等資產，也應在這同一個可信層，把 media-bucket key 轉成已簽名的 `/thumb` URL。
