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

回應狀態取決於 Vylux 能不能確認該媒體已經真的被清掉。

## 目前清理範圍

- media bucket 中由該 hash 產生的資產
- 已追蹤的同步圖片快取紀錄
- queue 中仍存在的相關 active / retry / scheduled tasks
- encryption key 紀錄與 job 紀錄

## 語義說明

- `204 No Content` 代表 cleanup 已完成，或該 hash 原本就已經被清乾淨
- `503 Service Unavailable` 代表 Vylux 目前無法確認 cleanup 完成，呼叫端應重試
- 重試是安全的：部分失敗之後再次呼叫，仍應最終收斂到 `204`
- 影響 caller 是否可安全忘掉該 hash 的 critical stages 包含 task cancellation、media object deletion、與 tracked cache object deletion
- encryption key 與 job row 等 metadata cleanup 只會在 critical cleanup 成功後才繼續執行
- 建議由內部管理後台、補償任務或保留政策流程呼叫，而不是直接暴露給終端用戶

### cleanup 未完成時的回應範例

```json
{
    "message": "cleanup incomplete for movie-2026-04-01",
    "retryable": true,
    "completed_stages": ["task_cancellation"],
    "failed_stages": ["media_objects"]
}
```

### 呼叫端建議

- 只有在收到 `204` 時，才把該 hash 視為可安全遺忘
- 收到 `503` 時，應把它視為可重試的 cleanup failure
- 在 cleanup 確認完成之前，不要先刪掉自己那邊代表該媒體的 source-of-truth 記錄
