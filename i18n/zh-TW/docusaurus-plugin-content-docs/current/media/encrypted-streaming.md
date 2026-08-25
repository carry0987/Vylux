---
title: 加密串流
description: "受保護 HLS 的 raw-key CBCS 實際生命週期，包含 stream key 儲存、`/api/key/{id}` 驗證與播放器整合。"
---

# 加密串流

Vylux 目前的受限影片模式是：

- HLS + CMAF
- raw-key encryption
- protection scheme: `cbcs`
- playlist 以 `#EXT-X-KEY` 指向 `/api/key/{id}`

## 只在哪些任務啟用

加密目前出現在：

- `POST /api/audio/jobs` 且 `pipeline.package.hls.encryption.enabled=true`
- `POST /api/video/jobs` 且 `pipeline.package.hls.encryption.enabled=true`
- 實作這些 public contract 的內部 `audio:transcode`、`video:transcode` 與 `video:full` worker flow

若未開啟 `encrypt`，整條 HLS pipeline 仍正常產出，只是不會產生 encryption metadata，也不會有 `/api/key/{id}` 的存取需求。

## key material 生命週期

```mermaid
sequenceDiagram
  participant Worker
  participant Wrapper as Key Wrapper
  participant PG as PostgreSQL
  participant Packager as Shaka Packager
  participant Player
  participant KeyAPI as /api/key/{id}

  Worker->>Worker: 生成 16-byte AES key
  Worker->>Worker: 生成 16-byte KID
  Worker->>Wrapper: Wrap(aesKey)
  Wrapper-->>Worker: wrapped_key + wrap_nonce + kek_version
  Worker->>PG: upsert encryption key row
  Worker->>Packager: raw-key packaging with cbcs + key URI
  Player->>KeyAPI: GET /api/key/{id} + Bearer token
  KeyAPI->>PG: 讀 wrapped key row
  KeyAPI->>Wrapper: Unwrap(...)
  Wrapper-->>KeyAPI: plaintext 16-byte AES key
  KeyAPI-->>Player: application/octet-stream
```

## 資料庫裡實際保存什麼

Vylux 不把 plaintext content key 存進資料庫，而是保存：

- `id`
- `source_hash`
- `asset_type`
- `packaging_type`
- `wrapped_key`
- `wrap_nonce`
- `kek_version`
- `kid`
- `scheme`

也就是說，資料庫裡存的是特定受保護串流資產的可解包 metadata，而不是可以直接被播放器使用的明文 key。

此外，raw AES content key 也不會先寫成暫存 key 檔。worker 會直接把 key material 透過 Shaka Packager 的 raw-key CLI 參數傳入，因此部署上不再需要為了保護磁碟 key 檔而另外準備 tmpfs mount。

## `BASE_URL` 的角色

當 worker 啟用加密時，會用：

```text
{BASE_URL}/api/key/{id}
```

其中 `id` 是該受保護資產對應 stream key record 的 UUID，並交給 packager 寫進 playlist。

因此：

- `BASE_URL` 應指向播放器實際可訪問的 Vylux 對外域名
- `BASE_URL` 不可有 trailing slash
- 若 `BASE_URL` 設錯，playlist 仍可能生成，但播放器抓 key 時會失敗

## 金鑰端點語義

`GET /api/key/{id}`

Header:

```text
Authorization: Bearer {token}
```

狀態碼語義：

- `200`: token 有效，回傳 16-byte AES key
- `401`: 缺少 token
- `403`: token 無效、過期或 hash 不匹配
- `404`: 找不到對應 key id 的 stream key record

此外：

- 成功回應會帶 `Cache-Control: no-store`
- 回傳 `application/octet-stream`
- 該端點不接受 `X-API-Key`，只接受 Bearer token

這個端點也有 Redis-based rate limit，避免 key endpoint 被高頻濫用。

## Bearer token 模型

token payload 至少包含：

- `hash`
- `exp`

handler 會檢查：

1. token 格式是否正確
2. HMAC-SHA256 簽名是否正確
3. `exp` 是否未過期
4. payload 內的 `hash` 是否與 request path 載入到的 stream key record 一致

## 整合模型

Vylux 不會自行簽發播放 token。你的上游應用應決定誰可以觀看受保護內容，並提供 `/api/key/{id}` 所需的 Bearer token。

## 測試方式

先由你的上游應用或測試工具產生一個對應該 media hash 的合法 Bearer token：

```bash
KEY_TOKEN='<valid bearer token for this media hash>'
```

搭配測試命令可以驗證：

- `results.streaming.encrypted == true`
- `results.encryption.key_endpoint` 存在
- media playlist 出現 `#EXT-X-KEY`
- 未帶 token 取得 `401`
- 帶錯誤、過期或 hash 不匹配 token 時取得 `403`
- key id 沒有對應 stream key row 時取得 `404`
- 帶合法 token 時回傳 `16` bytes key

## 播放器整合

若使用 hls.js，關鍵點是只對 `/api/key/` 請求附加 `Authorization` header，不要把 token 放進 query string。

```ts showLineNumbers
xhrSetup: (xhr, url) => {
  if (url.includes('/api/key/') && keyToken) {
    xhr.setRequestHeader('Authorization', `Bearer ${keyToken}`);
  }
}
```

## 為什麼採 raw-key + key API

這個模型的目的不是 DRM，而是把金鑰交付與媒體分發拆開：

- playlist 與 segments 可以交給 CDN 長時間快取
- key 仍經過受控 API 邊界
- 上游應用可以自行決定 token 的簽發與有效期

這對「受保護但不需要完整 DRM 平台」的場景很實用。
