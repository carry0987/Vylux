---
title: 圖片與媒體投遞 API
description: "`/img`、`/original`、`/thumb` 的 URL 模型、HMAC 簽名方式與 curl 範例。"
---

# 圖片與媒體投遞 API

這一組端點負責同步回應圖片或既有媒體物件，適合直接給瀏覽器、CDN 或上游應用使用。

下方簽名範例涉及的 `BASE_URL`、`HMAC_SECRET` 與 optional `IMAGE_SOURCE_KEY` 完整說明，請見 [設定](../operations/configuration)。

## 端點總覽

### `/img`

- endpoint: `GET /img/{sig}/{opts}/{source}.{format}`
- auth: HMAC URL 簽名
- 後端儲存: 從 source bucket 讀圖，並把衍生快取寫進 media bucket

### `/original`

- endpoint: `GET /original/{sig}/{source}`
- auth: HMAC URL 簽名
- 後端儲存: 不做轉換，直接代理 source bucket 物件

### `/thumb`

- endpoint: `GET /thumb/{sig}/{encoded_key}`
- auth: HMAC URL 簽名
- 後端儲存: 從 media bucket 代理既有縮圖、封面或靜態媒體物件

## `GET /img/{sig}/{opts}/{source}.{format}`

### 路徑格式

啟用 `IMAGE_SOURCE_KEY` 時：

```text
/img/{sig}/{opts}/{source_token}.{format}
```

`IMAGE_SOURCE_KEY` 為空或未設定時：

```text
/img/{sig}/{opts}/{encoded_source}.{format}
```

例子：

```text
/img/<sig>/w640_h360_q80/uploads%2Favatars%2Fsample.jpg.webp
/img/<sig>/w1200/uploads%2Fproducts%2Fhero.png.avif
/img/<sig>/w640_h360_q80/AQABAgMEBQYHCAkK....webp
```

### 參數語義

| 片段 | 說明 |
| --- | --- |
| `sig` | `HMAC-SHA256` 十六進位簽名 |
| `opts` | 圖片參數，支援 `w`、`h`、`q`，例如 `w640_h360_q80` |
| `source` | 啟用 `IMAGE_SOURCE_KEY` 時為 AES-256-GCM source token；否則為 URL-escaped source object key |
| `format` | 輸出格式，支援 `webp`、`avif`、`jpg`、`png`、`gif` |

動畫補充說明：

- 動畫輸出目前只支援 `webp` 與 `gif`
- 動畫來源圖不能轉成 `avif`、`jpg`、`png` 這類靜態輸出格式
- `heic`、`heif`、`avif` 這類 HEIF-family 輸入，在 runtime image 具備 HEIF 支援時可作為 source 格式使用；但在目前同步 `/img` 路徑中，仍以靜態圖片流程處理
- animated AVIF 與 animated HEIF sequence 目前不在同步 `/img` 路徑的支援範圍內

### 簽名 canonicalization

兩種模式都會把 `opts` 固定排序為 `w` -> `h` -> `q`，並將 `jpeg` 正規化為 `jpg`。

明文模式會先把 `encoded_source` 解碼成 object key，再簽署：

```text
{canonical_options}/{decoded_source_key}.{canonical_format}
```

加密模式先加密 source key，再簽署以下以 NUL 分隔的欄位：

```text
"vylux/image-url/v1" || "\x00" || canonical_options ||
"\x00" || source_token || "\x00" || canonical_format
```

Token binary layout：

```text
0x01 || nonce[12] || AES-256-GCM(source_key, nonce, "vylux/image-source/v1")
```

完整結果使用無 padding Base64URL 編碼。AES-GCM 會附加 16-byte authentication tag。每個新 token 都必須使用密碼學安全的隨機 nonce；禁止固定、重用或從 source key 推導 nonce。

:::tip 要簽的是 canonical form，不是瀏覽器看到的原始 URL
如果 signer 與 Vylux 對 `opts` 順序、decoded object key 或 `jpeg` / `jpg` 的正規化理解不同，就算 URL 看起來合理，也會回 `403`。
:::

### 明文模式 shell 簽名與 curl 範例

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

### Node.js 加密來源範例

以下 Node.js 22+ 範例只使用 `node:crypto`。請只在能安全讀取兩個 secrets 的可信 backend 執行。

```js showLineNumbers
import { createCipheriv, createHmac, randomBytes } from "node:crypto";

const BASE_URL = process.env.BASE_URL ?? "http://localhost:3000";
const HMAC_SECRET = process.env.HMAC_SECRET;
const IMAGE_SOURCE_KEY = process.env.IMAGE_SOURCE_KEY;
const SOURCE_AAD = Buffer.from("vylux/image-source/v1", "utf8");

if (!HMAC_SECRET || !IMAGE_SOURCE_KEY) {
  throw new Error("HMAC_SECRET and IMAGE_SOURCE_KEY are required");
}

function createSourceToken(sourceKey, nonce = randomBytes(12)) {
  const key = Buffer.from(IMAGE_SOURCE_KEY, "hex");
  const plaintext = Buffer.from(sourceKey, "utf8");
  if (IMAGE_SOURCE_KEY.length !== 64 || key.length !== 32) {
    throw new Error("IMAGE_SOURCE_KEY must be exactly 64 hex characters");
  }
  if (plaintext.length === 0 || plaintext.length > 1024) {
    throw new Error("source key must contain 1..1024 UTF-8 bytes");
  }

  const cipher = createCipheriv("aes-256-gcm", key, nonce);
  cipher.setAAD(SOURCE_AAD);
  const ciphertext = Buffer.concat([cipher.update(plaintext), cipher.final()]);
  return Buffer.concat([
    Buffer.from([1]),
    nonce,
    ciphertext,
    cipher.getAuthTag(),
  ]).toString("base64url");
}

function signDomain(domain, ...parts) {
  const mac = createHmac("sha256", Buffer.from(HMAC_SECRET, "utf8"));
  mac.update(domain, "utf8");
  for (const part of parts) {
    mac.update(Buffer.from([0]));
    mac.update(part, "utf8");
  }
  return mac.digest("hex");
}

function imageURL(token, { width, height, quality, format }) {
  const options = [
    width !== undefined ? `w${width}` : undefined,
    height !== undefined ? `h${height}` : undefined,
    quality !== undefined ? `q${quality}` : undefined,
  ].filter(Boolean).join("_");
  const canonicalFormat = format.toLowerCase() === "jpeg" ? "jpg" : format.toLowerCase();
  const signature = signDomain("vylux/image-url/v1", options, token, canonicalFormat);
  return `${BASE_URL}/img/${signature}/${options}/${token}.${canonicalFormat}`;
}

function originalURL(token) {
  const signature = signDomain("vylux/original-url/v1", token);
  return `${BASE_URL}/original/${signature}/${token}`;
}

const token = createSourceToken("uploads/avatars/sample.jpg");
console.log({
  token,
  image: imageURL(token, { width: 640, height: 360, quality: 80, format: "webp" }),
  original: originalURL(token),
});
```

需要穩定 CDN URL 時，請儲存並重用 `token`。為同一來源生成新 token 不會使舊 token 失效。

跨語言固定測試向量：

```text
IMAGE_SOURCE_KEY = 00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff
nonce            = 000102030405060708090a0b
source key       = static/forum/recommend-cover.png
expected token   = AQABAgMEBQYHCAkKC2Vs1EIpKuLQpkkakR6nAUwBWMoblWBW_FZUJL4KkkRQvPT1Ob-M_IsC7s3mG3OUcg
```

測試時將 `Buffer.from("000102030405060708090a0b", "hex")` 傳入第二個參數。固定 nonce 僅供 interoperability test；production 必須使用 `randomBytes(12)`。

### 成功回應特性

- `200 OK`
- `Content-Type` 為輸出格式對應的 MIME type
- `Cache-Control: public, max-age=31536000, immutable`
- `ETag` 依輸出內容計算

### 失敗語義

| 狀態碼 | 代表情況 |
| --- | --- |
| `400` | 參數錯誤、source encoding 錯誤、unsupported format |
| `403` | 簽名無效或 encrypted source token 無效 |
| `404` | source object 不存在 |
| `422` | 原圖存在但無法解碼，或動畫轉靜態等不可處理情況 |
| `502` | source storage 暫時不可用 |
| `500` | 其他內部處理錯誤 |

## `GET /original/{sig}/{source}`

這個端點用於受控地代理 source bucket 原始物件，不做任何轉換。

啟用 source encryption 時，請重用 `/img` 的 token，並簽署 `"vylux/original-url/v1\x00{token}"`。上方 Node.js 範例同時示範兩種路由。

### 明文模式 shell 簽名與 curl 範例

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

:::warning `HMAC_SECRET` 必須留在可信任的一側
瀏覽器與公開客戶端只應拿到最終已簽名 URL，不應持有 `HMAC_SECRET`，也不應自己計算簽名。
:::

- 不要在前端暴露 `HMAC_SECRET`；簽名應由上游應用或授權服務產生
- 不要在前端暴露 `IMAGE_SOURCE_KEY`
- 讓 object key 與輸出格式進入簽名範圍，避免 URL 被竄改後仍可命中
- 需要穩定 CDN URL 時，請儲存並重用 encrypted source token

### signer 應該放在哪一層

在大多數部署裡，`/img`、`/original`、`/thumb` 的 signer 應該放在這類可信任位置之一：

- 你的主應用 backend
- 專門的內部簽名服務
- 能安全讀取 `HMAC_SECRET` 與 optional `IMAGE_SOURCE_KEY` 的可信 edge worker

常見請求流程是：

1. 使用者先向你的應用要求某張圖片或資產
2. 你的應用判斷這個使用者是否可存取對應媒體
3. 你的應用在 server-side 簽出 Vylux URL
4. 你的應用把最終已簽名 URL 回給瀏覽器

也就是說，瀏覽器只應該收到最終 URL，不應看到 secret，也不應自己持有 unsigned path 來簽名。

若你要公開 job 結果中的 cover、preview、thumbnail 等資產，也應在這同一個可信層，把 media-bucket key 轉成已簽名的 `/thumb` URL。
