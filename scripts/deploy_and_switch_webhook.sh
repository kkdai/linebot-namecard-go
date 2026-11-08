#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# 自動從舊版 Python Cloud Run 複製環境變數、部署新版 Go Cloud Run 並切換 LINE Webhook
# ==============================================================================
PROJECT_ID="${PROJECT_ID:?Please set PROJECT_ID (e.g. export PROJECT_ID=YOUR_GCP_PROJECT_ID)}"
REGION="${REGION:?Please set REGION (e.g. export REGION=YOUR_GCP_REGION)}"
OLD_SERVICE="${OLD_SERVICE:?Please set OLD_SERVICE (legacy Python Cloud Run service name)}"
NEW_SERVICE="${NEW_SERVICE:?Please set NEW_SERVICE (new Go Cloud Run service name)}"

if [[ "${1:-}" == "--rollback" ]]; then
  echo "🔄 正在執行 Rollback：將 LINE Webhook 切回舊版服務 (${OLD_SERVICE})..."
  OLD_URL=$(gcloud run services describe "${OLD_SERVICE}" \
    --project="${PROJECT_ID}" \
    --region="${REGION}" \
    --format="value(status.url)")
  CHANNEL_ACCESS_TOKEN=$(gcloud run services describe "${OLD_SERVICE}" \
    --project="${PROJECT_ID}" \
    --region="${REGION}" \
    --format="json" | python3 -c '
import sys, json
data = json.load(sys.stdin)
envs = data["spec"]["template"]["spec"]["containers"][0].get("env", [])
for e in envs:
    if e.get("name") == "ChannelAccessToken":
        print(e.get("value", ""))
        break
')
  WEBHOOK_URL="${OLD_URL%/}/"
  curl -sS -X PUT https://api.line.me/v2/bot/channel/webhook/endpoint \
    -H "Authorization: Bearer ${CHANNEL_ACCESS_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "{\"endpoint\":\"${WEBHOOK_URL}\"}"
  echo ""
  echo "✅ 已成功將 LINE Webhook 切回: ${WEBHOOK_URL}"
  exit 0
fi

echo "🔍 [1/5] 正在從 GCP (${PROJECT_ID} / ${REGION}) 讀取舊服務 ${OLD_SERVICE} 的環境變數設定..."
SERVICE_JSON=$(gcloud run services describe "${OLD_SERVICE}" \
  --project="${PROJECT_ID}" \
  --region="${REGION}" \
  --format="json")

ENV_FILE=$(mktemp /tmp/cloudrun_env.XXXXXX.yaml)
trap 'rm -f "${ENV_FILE}"' EXIT

CHANNEL_ACCESS_TOKEN=$(printf "%s" "${SERVICE_JSON}" | python3 -c '
import sys, json

data = json.load(sys.stdin)
containers = data.get("spec", {}).get("template", {}).get("spec", {}).get("containers", [])
if not containers:
    sys.exit("找不到 container spec")

envs = containers[0].get("env", [])
env_dict = {}
for item in envs:
    name = item.get("name")
    val = item.get("value")
    if name and val is not None:
        env_dict[name] = val

# 確保 PROJECT_ID 與 LOCATION 存在
env_dict.setdefault("PROJECT_ID", "'"${PROJECT_ID}"'")
env_dict.setdefault("LOCATION", "global")

# 輸出 gcloud --env-vars-file 支援的 YAML 格式
with open("'"${ENV_FILE}"'", "w", encoding="utf-8") as f:
    for k, v in env_dict.items():
        f.write(f"{k}: {json.dumps(v, ensure_ascii=False)}\n")

print(env_dict.get("ChannelAccessToken", ""))
')

if [[ -z "${CHANNEL_ACCESS_TOKEN}" ]]; then
  echo "❌ 無法從 ${OLD_SERVICE} 取得 ChannelAccessToken，請確認 gcloud 權限與服務名稱。"
  exit 1
fi

echo "✅ 成功擷取環境變數（包含 ChannelSecret、ChannelAccessToken、FIREBASE_URL、GOOGLE_APPLICATION_CREDENTIALS_JSON 等）。"

echo "🚀 [2/5] 正在建置並部署新版 Go + ADK Cloud Run 服務 (${NEW_SERVICE})..."
gcloud run deploy "${NEW_SERVICE}" \
  --source . \
  --project="${PROJECT_ID}" \
  --region="${REGION}" \
  --platform=managed \
  --allow-unauthenticated \
  --env-vars-file="${ENV_FILE}"

NEW_SERVICE_URL=$(gcloud run services describe "${NEW_SERVICE}" \
  --project="${PROJECT_ID}" \
  --region="${REGION}" \
  --format="value(status.url)")

WEBHOOK_ENDPOINT="${NEW_SERVICE_URL%/}/"
echo "✅ 新版 Cloud Run 部署完成！服務網址: ${NEW_SERVICE_URL}"

echo "🩺 [3/5] 正在檢查新服務 Health Check (${WEBHOOK_ENDPOINT})..."
curl -fsS "${WEBHOOK_ENDPOINT}"
echo ""

echo "🔗 [4/5] 正在呼叫 LINE Messaging API 更新 Webhook Endpoint 至 ${WEBHOOK_ENDPOINT} ..."
PUT_RESP=$(curl -sS -X PUT https://api.line.me/v2/bot/channel/webhook/endpoint \
  -H "Authorization: Bearer ${CHANNEL_ACCESS_TOKEN}" \
  -H "Content-Type: application/json" \
  -d "{\"endpoint\":\"${WEBHOOK_ENDPOINT}\"}")
echo "LINE Set Webhook API 回應: ${PUT_RESP}"

echo "🧪 [5/5] 正在呼叫 LINE Messaging API 測試 Webhook 連線..."
TEST_RESP=$(curl -sS -X POST https://api.line.me/v2/bot/channel/webhook/test \
  -H "Authorization: Bearer ${CHANNEL_ACCESS_TOKEN}" \
  -H "Content-Type: application/json" \
  -d "{\"endpoint\":\"${WEBHOOK_ENDPOINT}\"}")
echo "LINE Test Webhook API 回應: ${TEST_RESP}"

echo "🎉 全部完成！LINE Bot 已成功切換至新版 Go + ADK 機器 (${WEBHOOK_ENDPOINT})。"
echo "💡 若需隨時切回舊版 Python 機器，可執行： ./scripts/deploy_and_switch_webhook.sh --rollback"
