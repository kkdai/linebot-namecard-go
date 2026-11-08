package config

import (
	"fmt"
	"os"
)

const (
	NamecardPath                  = "namecard"
	FirestoreNamecardCollection   = "namecards"
	FirestoreUserStateCollection  = "user_states"
	FirestoreADKSessionCollection = "adk_sessions"
	FirestoreADKMemoryCollection  = "adk_memories"
	PendingBacksideTimeoutSeconds = 300
	GeminiModelName               = "gemini-3-flash-preview"
)

const ImagePrompt = `
這是一張名片，你是一個名片秘書。請將以下資訊整理成 json 給我。
如果看不出來的，幫我填寫 N/A
只好 json 就好:
name, title, address, email, phone, company.
其中 phone 的內容格式為 #886-0123-456-789,1234. 沒有分機就忽略 ,1234
`

const DoubleSidedImagePrompt = ImagePrompt + `
這兩張圖片是同一張名片的正面與背面，請整合成一筆完整資料。
若同一欄位中英文都有出現（如姓名、公司），請合併呈現
（例如「王大明 David Wang」）；
若某欄位只有一面出現，直接採用該面的值；忽略明顯重複的資訊。
`

const AgentSystemInstruction = `你是一個聰明且親切的 LINE 名片助理。你的工作是幫助使用者管理名片資料。
你可以使用合適的工具來讀取或修改資料庫中的名片記錄。

【核心操作準則】
1. 【查詢】當使用者查詢某人或某公司的名片時，請先調用 get_all_namecards 取得所有資料，並在背後進行分析比對。
2. 【顯示】只要找到了符合條件的名片，『務必』調用 display_namecard 工具將該名片的 card_id 標記為顯示，以便系統繪製並呈現在 LINE 畫面上。
3. 【修改】如果使用者想修改名片（例如電話、Email、備註），請先比對找出 card_id，然後調用相對應的更新工具（如 update_namecard_field 或 update_namecard_memo）進行修改，修改成功後請『務必』再次調用 display_namecard 顯示更新後的名片，讓使用者進行確認。
4. 【回覆】最後請以親切、精簡的繁體中文口吻向使用者回覆操作結果或搜尋進度。`

var FieldLabels = map[string]string{
	"name":    "姓名",
	"title":   "職稱",
	"company": "公司",
	"address": "地址",
	"phone":   "電話",
	"email":   "Email",
}

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	ChannelSecret          string
	ChannelAccessToken     string
	ProjectID              string
	Location               string
	FirebaseURL            string
	FirebaseStorageBucket  string
	FirestoreDatabaseID    string
	CredentialsJSON        string
	Port                   string
}

// Load reads environment variables and validates required settings.
func Load() (*Config, error) {
	// Force GOOGLE_CLOUD_LOCATION to global so Vertex AI and ADK look for models in global region
	_ = os.Setenv("GOOGLE_CLOUD_LOCATION", "global")

	loc := os.Getenv("LOCATION")
	if loc == "" {
		loc = "global"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	dbID := os.Getenv("FIRESTORE_DATABASE_ID")
	if dbID == "" {
		dbID = "(default)"
	}

	cfg := &Config{
		ChannelSecret:         os.Getenv("ChannelSecret"),
		ChannelAccessToken:    os.Getenv("ChannelAccessToken"),
		ProjectID:             os.Getenv("PROJECT_ID"),
		Location:              loc,
		FirebaseURL:           os.Getenv("FIREBASE_URL"),
		FirebaseStorageBucket: os.Getenv("FIREBASE_STORAGE_BUCKET"),
		FirestoreDatabaseID:   dbID,
		CredentialsJSON:       os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON"),
		Port:                  port,
	}

	if cfg.ChannelSecret == "" {
		return nil, fmt.Errorf("Specify ChannelSecret as environment variable.")
	}
	if cfg.ChannelAccessToken == "" {
		return nil, fmt.Errorf("Specify ChannelAccessToken as environment variable.")
	}
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("Specify PROJECT_ID as environment variable.")
	}

	return cfg, nil
}
