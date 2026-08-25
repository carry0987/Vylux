---
title: 本機手動測試工具
description: "說明 gh-pages 分支上的 Python helpers，如何用來做 RustFS 上傳、建立工作、播放簽名與清理流程。"
---

# 本機手動測試工具

`gh-pages` 分支提供了一套小型 Python 工具，讓工程師可以在本機手動操作 Vylux，而不需要把這些 helper 放進主應用分支。

工具放在：

```text
tools/vylux-manual/
```

這套工具的定位是本機與可信任工程環境用的輔助工具，不是 production SDK。

## 為什麼放在 docs branch

這些 helper 對手動測試很有用，但它們不是 Vylux runtime 的一部分。

放在 docs branch 的好處是：

- main application branch 保持乾淨
- 工具可以和文檔一起版本化
- 工程師 clone docs branch 後就能直接使用

## 先決條件

- Python 3.11+
- 若要操作 RustFS object，需安裝 `boto3`
- 準備好可用的 `.env`，必要時再加 `.env.local`

安裝 RustFS 相關依賴：

```bash showLineNumbers
python3 -m pip install boto3
```

## 環境變數載入順序

工具會依序載入：

1. `.env`
2. `.env.local`（覆蓋 `.env`）
3. 同名 process environment variables（優先級最高）

這和 Vylux 本機開發時的常見習慣一致：

- `.env` 通常放 container-to-container 位址，例如 `postgres`、`redis`、`otel-collector`
- `.env.local` 通常放 host-to-container 覆蓋值，例如 `localhost:5434`、`localhost:6381`、`localhost:9002`

## 入口

在 `gh-pages` 分支根目錄執行：

```bash showLineNumbers
python3 tools/vylux-manual/run.py --help
```

## 支援的指令

目前支援：

- 上傳檔案到 `source` 或 `media` bucket
- 列出 `source` 或 `media` bucket 中的 object
- 用 `head` 查物件 metadata
- 從 `source` 或 `media` bucket 刪除 object
- 建立 audio job
- 建立 video transcode job
- 建立 video full job
- 查詢 job status
- retry failed job
- 依 content hash 刪除衍生媒體
- 產生已簽名的 `/img`、`/original`、`/thumb` URL
- 產生 `/api/key/{id}` 的 URL 與 Bearer token

## 常見流程

### 上傳來源檔案

```bash showLineNumbers
python3 tools/vylux-manual/run.py upload source uploads/demo.flac /path/to/demo.flac
python3 tools/vylux-manual/run.py upload source uploads/demo.mp4 /path/to/demo.mp4
```

### 建立加密 audio HLS 工作

```bash showLineNumbers
python3 tools/vylux-manual/run.py create-audio a0b1c2d3 uploads/demo.flac --encrypt --waveform
```

### 建立加密 video transcode 工作

```bash showLineNumbers
python3 tools/vylux-manual/run.py create-video-transcode deadbeef uploads/demo.mp4 --encrypt
```

### 查詢 job 狀態

```bash showLineNumbers
python3 tools/vylux-manual/run.py job-status <job-id>
```

### 依 hash 刪除衍生媒體

```bash showLineNumbers
python3 tools/vylux-manual/run.py delete-media deadbeef
```

## 簽名與播放輔助

這套工具也能產生你的應用平常會對外提供的那些 route。

### 已簽名圖片 URL

```bash showLineNumbers
python3 tools/vylux-manual/run.py image-url uploads/sample.png webp --options w320_h180
```

### 已簽名原檔 URL

```bash showLineNumbers
python3 tools/vylux-manual/run.py original-url uploads/sample.png
```

### 已簽名縮圖 URL

```bash showLineNumbers
python3 tools/vylux-manual/run.py thumb-url videos/mo/movie-2026-04-01/cover.jpg
```

### Key endpoint URL 與 Bearer token

```bash showLineNumbers
python3 tools/vylux-manual/run.py key-url <key-id> <content-hash> --ttl 3600
```

## 安全提醒

- 不要把 `API_KEY`、`HMAC_SECRET`、`KEY_TOKEN_SECRET` 暴露到瀏覽器
- 請把這套工具視為可信任的本機 helper，而不是公開 client
- 若你會分享 shell history 或截圖，記得其中可能會帶到敏感值

## 相關頁面

- [快速開始](../getting-started)
- [工作 API](../api/jobs)
- [播放 API](../api/playback)
- [加密串流](../media/encrypted-streaming)
