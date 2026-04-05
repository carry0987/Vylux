---
title: 播放 API
description: "`/stream/{hash}/*` 與 `/api/key/{hash}` 的路徑模型、授權方式與 curl 範例。"
---

# 播放 API

## 端點總覽

| Endpoint | 用途 | Auth |
| --- | --- | --- |
| `GET /stream/{hash}/*` | 代理 HLS playlist、init segment、media segment | 無 |
| `GET /api/key/{hash}` | 發放加密 HLS 所需的 16-byte content key | `Authorization: Bearer {token}` |

## `GET /stream/{hash}/*`

`GET /stream/{hash}/*`

常見路徑：

```text
/stream/{hash}/master.m3u8
/stream/{hash}/audio/und_aac_2ch/playlist.m3u8
/stream/{hash}/video/r1080_h264/playlist.m3u8
/stream/{hash}/video/r1080_h264/seg_1.m4s
```

### curl 範例

```bash showLineNumbers
MEDIA_HASH='movie-2026-04-01'

curl -s \
    "http://localhost:3000/stream/$MEDIA_HASH/master.m3u8"

curl -s \
    "http://localhost:3000/stream/$MEDIA_HASH/video/r720_h264/playlist.m3u8"

curl -I \
    "http://localhost:3000/stream/$MEDIA_HASH/video/r720_h264/init.mp4"
```

### 行為說明

- 直接從 media bucket 讀取物件
- 支援 `.m3u8`、`.m4s`、`.mp4`、`.jpg`、`.webp`
- 路徑中含 `..` 會被拒絕
- 找不到物件時回 `404`
- 目前 playlist 與 segment 都會帶 `Cache-Control: public, max-age=31536000, immutable`
- 回應會附 `Access-Control-Allow-Origin: *`

## `GET /api/key/{hash}`

`GET /api/key/{hash}`

這個端點不使用 `X-API-Key`，而是只接受：

```text
Authorization: Bearer {token}
```

`KEY_TOKEN_SECRET` 與 `BASE_URL` 等播放相關設定的完整說明，請見 [設定](../operations/configuration)。

### Token 格式

Bearer token 由兩段 base64url 字串組成：

```text
base64url({"hash":"...","exp":<unix_timestamp>}).base64url(HMAC-SHA256(payload_b64, KEY_TOKEN_SECRET))
```

注意簽名覆蓋的是 `payload_b64`，不是原始 JSON bytes。

### shell 產 token 範例

```bash showLineNumbers
MEDIA_HASH='movie-2026-04-01'
KEY_TOKEN_SECRET='replace-with-key-token-secret'

PAYLOAD="$(jq -cn \
    --arg hash "$MEDIA_HASH" \
    --argjson exp "$(($(date +%s) + 3600))" \
    '{hash: $hash, exp: $exp}')"

PAYLOAD_B64="$(printf '%s' "$PAYLOAD" \
    | openssl base64 -A \
    | tr '+/' '-_' \
    | tr -d '=')"

SIG_B64="$(printf '%s' "$PAYLOAD_B64" \
    | openssl dgst -sha256 -mac HMAC -macopt "key:$KEY_TOKEN_SECRET" -binary \
    | openssl base64 -A \
    | tr '+/' '-_' \
    | tr -d '=')"

TOKEN="$PAYLOAD_B64.$SIG_B64"
```

### curl 範例

```bash showLineNumbers
curl -i "http://localhost:3000/api/key/$MEDIA_HASH"

curl -s \
    -H "Authorization: Bearer $TOKEN" \
    "http://localhost:3000/api/key/$MEDIA_HASH" \
    | wc -c
```

成功時最後一個指令應得到 `16`，代表回傳 16-byte content key。

### 回應語義

| 狀態碼 | 代表情況 |
| --- | --- |
| `200` | token 驗證成功，回傳 `application/octet-stream` 金鑰內容 |
| `401` | 缺少 `Authorization: Bearer ...` |
| `403` | token 簽名錯誤、已過期或 `hash` 不匹配 |
| `404` | 該媒體沒有對應的 encryption key |
| `500` | unwrap 失敗或其他內部錯誤 |

這個端點也有 Redis-based rate limit，預設每分鐘 120 次，依 Bearer token 或來源 IP 計算。

### token issuer 應該放在哪一層

在典型部署裡，Bearer token 應由你的應用 backend 或內部授權服務簽發，而且應該發生在它已經確認當前使用者可以觀看該媒體之後。

常見流程是：

1. client 向你的應用要求播放 `hash=X` 的媒體
2. 你的應用先驗證身份並檢查授權
3. 你的應用回傳 public playlist URL，也就是 `/stream/X/master.m3u8`
4. 你的應用同時回傳一個短效 Bearer token，且 payload 內含 `hash=X`
5. 播放器只在請求 `/api/key/X` 時附上這個 token

不要把 `KEY_TOKEN_SECRET` 放進瀏覽器程式碼，也不要把 token 塞進 playlist 或 segment URL。

如果你想把責任切得更乾淨，這個 playback token 也可以由獨立的內部授權服務簽發，而不是主應用 backend 本身。
