# Agent Handoff Document (`HANDOFF.md`) — `linebot-namecard-go`

> **給 Local `agy` CLI Agent 的交接指令**：
> 本專案已在雲端開發環境完成從 Python (`kkdai/linebot-namecard-python`) 到 **Golang + Google ADK (`google.golang.org/adk`) + Cloud Firestore** 的 100% 程式碼實作與單元測試，並已推送至 `https://github.com/kkdai/linebot-namecard-go`。
> 由於雲端沙盒沒有安裝 `gcloud` CLI 與本機 GCP 憑證，請由本機 `agy` CLI Agent 接手執行 **Cloud Run 部署、環境變數遷移、Firestore 權限確認與 LINE Webhook 自動切換**。

---

## 一、之前已經完成了什麼？（Completed Work）

1. **建立全新 Go 儲存庫並完成架構規劃**：
   - Repo：[`https://github.com/kkdai/linebot-namecard-go`](https://github.com/kkdai/linebot-namecard-go)（分支：`main`）
   - 遷移藍圖：`ROADMAP.md`
2. **完成 100% 功能模組實作與單元測試（`go test -v ./...` 全數通過）**：
   - `internal/config`：環境變數載入（自動設定 `GOOGLE_CLOUD_LOCATION=global`、支援 `ChannelSecret`、`ChannelAccessToken`、`PROJECT_ID`、`LOCATION`、`FIREBASE_URL`、`FIREBASE_STORAGE_BUCKET`、`FIRESTORE_DATABASE_ID`、`GOOGLE_APPLICATION_CREDENTIALS_JSON`）。
   - `internal/memory`（**共通記憶體 + Firestore 持久化**）：
     - `FirestoreSessionService`：實作 ADK `session.Service`，結合共通記憶體 (`session.InMemoryService()`) 與 Firestore `adk_sessions` 集合，支援跨對話記憶與冷啟動自動還原（Hydration）。
     - `FirestoreMemoryService`：實作 ADK `memory.Service`，將長期記憶同步至 Firestore `adk_memories` 集合。
     - `UserStateStore`：管理正反雙面等待狀態 (`pending_backside_confirm` / `awaiting_backside_image`)、欄位修改 (`editing_field` / `adding_memo`) 與 AI 修改二次確認 (`pending_update`)，具備共通記憶體快取、Firestore `user_states` 同步與 300 秒過期清理 (`SweepExpiredStates`)。
   - `internal/agent`（**Google ADK Go Agent + 5 Function Tools**）：
     - 使用 `llmagent.New` (`gemini-3-flash-preview`) 與常駐共用的 `runner.Runner`。
     - 註冊 5 個強型別 `functiontool`：`get_all_namecards`、`get_namecard_by_id`、`display_namecard`、`update_namecard_memo`、`update_namecard_field`。
     - 內建 `FallbackKeywordSearch` 本地端關鍵字備援搜尋。
   - `internal/gemini`（**Gemini Vision 單／雙面名片 OCR**）：
     - 使用 `google.golang.org/genai` (`BackendVertexAI`) + `NamecardSchema` 強制輸出結構化 JSON，並標記 `labels: {"client_id": "namecard"}`。
   - `internal/storage`（**雙資料庫相容 + Cloud Storage**）：
     - 同時支援讀取／寫入舊有 Firebase Realtime Database (`namecard/{user_id}`) 與新 Cloud Firestore (`namecards/{user_id}/cards/{card_id}`)，確保舊有名片資料無痛銜接。
     - 支援上傳 vCard QR Code PNG 至 Firebase Cloud Storage (`qrcodes/{user_id}/{card_id}.png`) 並設為公開讀取。
   - `internal/flex` & `internal/qrcode` & `internal/bot` & `cmd/server`：
     - 完整實作名片 Giga Bubble、編輯選單、多筆消歧義清單（`> 4` 筆）、修改二次確認 Bubble、`tel:` 撥號、LINE 分享連結 1000 字元截斷、vCard 3.0 QR Code 與 LINE Messaging API v8 Webhook 伺服器。
3. **完成一鍵部署與 Webhook 切換腳本**：
   - `scripts/deploy_and_switch_webhook.sh`

---

## 二、接下來 Local `agy` CLI Agent 該怎麼做？（Next Steps）

### Step 1：Clone 專案並確認本機 `gcloud` 狀態
```bash
git clone https://github.com/kkdai/linebot-namecard-go.git
cd linebot-namecard-go

# 確認 gcloud 已登入且可存取專案 <YOUR_GCP_PROJECT_ID>
gcloud config set project <YOUR_GCP_PROJECT_ID>
gcloud run services describe <OLD_CLOUD_RUN_SERVICE> --project=<YOUR_GCP_PROJECT_ID> --region=<YOUR_GCP_REGION> --format="value(status.url)"
```

### Step 2：確認 GCP 專案 `<YOUR_GCP_PROJECT_ID>` 的 Firestore 狀態与權限
1. 檢查 `<YOUR_GCP_PROJECT_ID>` 專案是否已啟用 Cloud Firestore API 與建立 `(default)` 資料庫：
   ```bash
   gcloud services enable firestore.googleapis.com --project=<YOUR_GCP_PROJECT_ID>
   gcloud firestore databases list --project=<YOUR_GCP_PROJECT_ID>
   ```
   - 若尚未建立 `(default)` Firestore 資料庫，請建立 Native 模式資料庫：
     ```bash
     gcloud firestore databases create --location=<YOUR_GCP_REGION> --type=firestore-native --project=<YOUR_GCP_PROJECT_ID>
     ```
2. 若舊服務使用的是 `GOOGLE_APPLICATION_CREDENTIALS_JSON` 內的 Service Account，請確認該 Service Account 具備 `roles/datastore.user`（Cloud Datastore User）與 `roles/aiplatform.user`（Vertex AI User）權限。

### Step 3：執行一鍵部署與 LINE Webhook 切換腳本
直接執行專案內建腳本：
```bash
./scripts/deploy_and_switch_webhook.sh
```
該腳本會自動完成：
1. 從舊服務 `<OLD_CLOUD_RUN_SERVICE>`（`<YOUR_GCP_REGION>`）匯出所有環境變數（`ChannelSecret`、`ChannelAccessToken`、`FIREBASE_URL`、`FIREBASE_STORAGE_BUCKET`、`GOOGLE_APPLICATION_CREDENTIALS_JSON` 等）至暫存 YAML 檔。
2. 部署新 Cloud Run 服務 `<NEW_CLOUD_RUN_SERVICE>`（注意：Cloud Build 會使用專案根目錄的 `Dockerfile`；若正式環境無法拉取 `golang:1.27-bookworm`，請將 `Dockerfile` 第一行改為 `FROM golang:1.26-bookworm AS builder` 或對應版本並調整 `go.mod` 的 `go` 版本號）。
3. 測試新服務 `GET /` Health Check 是否回傳 `{"status":"ok"}`。
4. 呼叫 `PUT https://api.line.me/v2/bot/channel/webhook/endpoint` 將 LINE Bot Webhook 指向新的 Cloud Run 服務網址。
5. 呼叫 `POST https://api.line.me/v2/bot/channel/webhook/test` 確認 LINE Webhook 測試結果為 `"success": true`。

### Step 4：端到端驗收與監控（Verification & Rollback）
1. 查看 Cloud Run 即時日誌，確認 Firestore、Firebase 與 ADK 初始化成功：
   ```bash
   gcloud run services logs read <NEW_CLOUD_RUN_SERVICE> --project=<YOUR_GCP_PROJECT_ID> --region=<YOUR_GCP_REGION> --limit=50
   ```
2. 在 LINE 聊天室實測：
   - 點擊「📊 統計」、「📋 列表」、「🧪 測試」確認舊有名片讀取正常。
   - 輸入自然語言（例如「幫我找王大明」或追問「那他的電話是什麼？」）確認 ADK Agent 與 Firestore 共通記憶正常運作。
   - 上傳一張名片照片測試單面／雙面辨識。
3. **若遇到任何問題需緊急退回舊版 Python 機器**：
   ```bash
   ./scripts/deploy_and_switch_webhook.sh --rollback
   ```
