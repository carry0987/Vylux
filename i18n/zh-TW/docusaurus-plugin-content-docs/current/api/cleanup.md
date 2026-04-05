---
title: 清理 API
description: "刪除既有媒體資產、工作紀錄與相關快取的管理 API。"
---

# 清理 API

`DELETE /api/media/{hash}`

這個 endpoint 需要 `X-API-Key`，用途是刪除某個 `hash` 對應的媒體資產與相關追蹤資料。

這類內部呼叫所使用的 `API_KEY` 與 base URL 相關設定，請見 [設定](../operations/configuration)。

## curl 範例

```bash showLineNumbers
BASE_URL='http://localhost:3000'
API_KEY='replace-with-api-key'
MEDIA_HASH='movie-2026-04-01'

curl -i \
    -X DELETE \
    -H "X-API-Key: $API_KEY" \
    "$BASE_URL/api/media/$MEDIA_HASH"
```

成功時回 `204 No Content`。

## 目前清理範圍

- media bucket 中由該 hash 產生的資產
- 已追蹤的同步圖片快取紀錄
- queue 中仍存在的相關 active / retry / scheduled tasks
- encryption key 紀錄與 job 紀錄

## 語義說明

- 這個操作設計為 best-effort、可重入、可重複執行
- 若目標已不存在，仍回 `204`，方便上游做 idempotent cleanup
- 建議由內部管理後台、補償任務或保留政策流程呼叫，而不是直接暴露給終端用戶
