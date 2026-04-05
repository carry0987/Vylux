---
title: 測試
description: "單元測試、整合測試與發版前手動 smoke test 的建議流程。"
sidebar_position: 2
---

# 測試

## 單元測試

```bash
go test -short ./...
```

## 完整測試套件

```bash
go test ./...
```

## 整合測試

```bash
go test -v ./tests/integration
```

## 手動 smoke test

發布前最重要的三組手動驗證如下：

- `video:preview` with `gif`
- `video:preview` with `webp`
- `video:transcode` with `encrypt=true`

這些 smoke test 會用到的 `BASE_URL`、`API_KEY`、buckets 與 secrets，完整說明請見 [設定](../operations/configuration)。

## 建議的 smoke test 順序

### `video:preview` with `gif`

```bash showLineNumbers
BASE_URL='http://localhost:3000'
API_KEY='replace-with-api-key'

curl -s \
    -X POST "$BASE_URL/api/jobs" \
    -H 'Content-Type: application/json' \
    -H "X-API-Key: $API_KEY" \
    -d '{
        "type": "video:preview",
        "hash": "smoke-preview-gif",
        "source": "uploads/sample.mp4",
        "options": {
            "start_sec": 1,
            "duration": 3,
            "width": 480,
            "fps": 12,
            "format": "gif"
        }
    }'
```

### `video:preview` with `webp`

把 `format` 改成 `webp`，並確認 `results.format` 與輸出 key 對應。

### `video:transcode` with `encrypt=true`

```bash showLineNumbers
curl -s \
    -X POST "$BASE_URL/api/jobs" \
    -H 'Content-Type: application/json' \
    -H "X-API-Key: $API_KEY" \
    -d '{
        "type": "video:transcode",
        "hash": "smoke-transcode-encrypted",
        "source": "uploads/sample.mp4",
        "options": {
            "encrypt": true
        }
    }'
```

完成後至少確認：

- `results.streaming.encrypted == true`
- `results.streaming.master_playlist` 存在
- `results.encryption.scheme == "cbcs"`
- 變體 playlist 內可看到 `#EXT-X-KEY`
- `/api/key/{hash}` 在未帶 token 時回 `401`，有效 token 時回 16 bytes

## 最近驗證過的情境

- portrait HLS ladder 實際播放
- `master.m3u8` 的 `RESOLUTION` 與 API results 對齊
- CBCS key delivery：`401` / `403` / `200 + 16 bytes` 語義
- preview 輸出 `gif` / `webp`

## 測試資料與 fixtures

- 單元測試集中在 `internal/.../*_test.go`
- 整合測試集中在 `tests/integration`
- 共用測試輔助在 `tests/testutil`

## 發布前檢查建議

發布前至少確認下面四件事：

- `go test ./...` 全綠
- 非加密 transcode job 可完成並能播放 `master.m3u8`
- 加密 transcode job 會產生 `#EXT-X-KEY`，且 key endpoint 權限語義正確
- `video:preview` 的 `gif` 與 `webp` 都能產出預期檔案

若你同時驗證 observability，建議把 smoke test 與 [可觀測性](../operations/observability) 頁的 Jaeger 檢查一起跑，確認 HTTP 與 worker spans 有串起來。
