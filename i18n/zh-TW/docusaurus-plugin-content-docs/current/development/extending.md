---
title: 擴充 Vylux
description: "當你要新增 job type、擴充 pipeline 或同步文件時，建議遵守的維護方式。"
---

# 擴充 Vylux

當你新增或調整一條 workflow 時，runtime 行為、持久化結構與文件都應同步更新。

## 新增功能時，先找邊界

在 Vylux 中擴充功能時，通常要先決定它屬於哪一層：

- HTTP endpoint
- queue task
- worker handler
- media processing primitive
- storage / persistence shape

## 擴充影片任務的典型切點

1. 在 queue task payload 定義或調整資料結構
2. 在進入系統的 endpoint 或 handler 層補上 request validation
3. 實作 worker handler 與 task wiring
4. 更新 persistence fields、job result schema 與新 artifact 的 cleanup 邏輯
5. 補單元測試與 smoke test
6. 同步更新 docs site 與發版驗證說明

## 文件維護建議

優先保留既有慣例：

- 清楚的 job typing
- 穩定的 artifact layout
- 明確的 retry semantics
- 可透過 API 與 playback path 驗證的輸出

如果文件與實作出現差異，應先以已驗證的實作為準，再回頭修正文案與發版測試流程。
