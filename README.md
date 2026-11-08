# LINE Bot Namecard Assistant (Golang + Google ADK + Firestore)

這是一個基於 **Golang**、**Google Agent Development Kit (`google.golang.org/adk`)**、**Gemini Vision (`gemini-3-flash-preview`)** 與 **Cloud Firestore** 打造的 LINE 智慧名片管理機器人，完整移植並升級自 [`kkdai/linebot-namecard-python`](https://github.com/kkdai/linebot-namecard-python)。

詳細遷移評估與架構規劃請參閱 **[ROADMAP.md](./ROADMAP.md)**。

---

## 核心功能與架構升級

1. **名片雙面／單面 OCR 結構化辨識 (`internal/gemini`)**：
   - 採用 `google.golang.org/genai` (`BackendVertexAI`) 搭配強制 JSON Schema (`NamecardSchema`)，支援單張名片辨識與正反雙面名片自動合併（中英文姓名、公司名稱智慧合併）。
2. **Google ADK Go 智慧代理與 Function Tools (`internal/agent`)**：
   - 使用 `google.golang.org/adk/agent/llmagent` 與 5 個強型別 `functiontool`：
     - `get_all_namecards`：讀取使用者所有名片
     - `get_namecard_by_id`：讀取單張名片詳情
     - `display_namecard`：標記名片顯示（$\le 4$ 張顯示 Giga Bubble 卡片、$> 4$ 張自動轉為消歧義清單）
     - `update_namecard_field` / `update_namecard_memo`：搭配二次確認沙盒 Flex Message 保護資料安全
   - 內建本地關鍵字備援搜尋（Fallback Search），當雲端 AI 異常時確保服務不中斷。
3. **共通記憶體 + Cloud Firestore 持久化 (`internal/memory`)**：
   - 實作 `FirestoreSessionService`（符合 `adk/session.Service`）與 `FirestoreMemoryService`（符合 `adk/memory.Service`），讓多輪對話記憶與使用者互動狀態（`UserStateStore`）同時常駐於共通記憶體並同步至 Cloud Firestore (`adk_sessions`、`adk_memories`、`user_states`)。
4. **豐富 LINE 互動體驗 (`internal/flex`, `internal/qrcode`)**：
   - 支援一鍵 `tel:` 撥號、Email 點擊寄信／長按複製、LINE 原生好友分享（自動 UTF-8 編碼與 1000 字元截斷）以及 vCard 3.0 QR Code 匯入通訊錄。

---

## 快速開始與測試

```bash
# 執行全部單元測試
go test -v ./...

# 本地啟動伺服器
export ChannelSecret="YOUR_CHANNEL_SECRET"
export ChannelAccessToken="YOUR_CHANNEL_ACCESS_TOKEN"
export PROJECT_ID="YOUR_GCP_PROJECT_ID"
export LOCATION="global"
export FIREBASE_URL="https://YOUR_PROJECT_ID-default-rtdb.firebaseio.com/"
export FIREBASE_STORAGE_BUCKET="YOUR_PROJECT_ID.firebasestorage.app"

go run ./cmd/server
```

---

## 部署至 Google Cloud Run

```bash
gcloud run deploy YOUR_NEW_CLOUD_RUN_SERVICE \
  --source . \
  --project YOUR_GCP_PROJECT_ID \
  --region YOUR_GCP_REGION \
  --allow-unauthenticated \
  --set-env-vars "ChannelSecret=YOUR_CHANNEL_SECRET,ChannelAccessToken=YOUR_CHANNEL_ACCESS_TOKEN,PROJECT_ID=YOUR_GCP_PROJECT_ID,LOCATION=global,FIREBASE_URL=YOUR_FIREBASE_URL,FIREBASE_STORAGE_BUCKET=YOUR_STORAGE_BUCKET"
```
