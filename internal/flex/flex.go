package flex

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/line/line-bot-sdk-go/v8/linebot/messaging_api"
)

const (
	TelAllowedChars   = "+-,0123456789"
	ShareURLPrefix    = "https://line.me/R/share?text="
	MaxShareURLLength = 1000
)

// FlexPayload wraps a LINE Flex Message along with its raw dictionary representation for inspection/testing.
type FlexPayload struct {
	AltText  string         `json:"altText"`
	Contents map[string]any `json:"contents"`
}

// ToLineFlexMessage converts FlexPayload into a LINE SDK messaging_api.FlexMessage.
func (f FlexPayload) ToLineFlexMessage(quickReply *messaging_api.QuickReply) (*messaging_api.FlexMessage, error) {
	rawBytes, err := json.Marshal(f.Contents)
	if err != nil {
		return nil, err
	}
	container, err := messaging_api.UnmarshalFlexContainer(rawBytes)
	if err != nil {
		return nil, err
	}
	return &messaging_api.FlexMessage{
		AltText:    f.AltText,
		Contents:   container,
		QuickReply: quickReply,
	}, nil
}

func clean(value string) string {
	text := strings.TrimSpace(value)
	if strings.EqualFold(text, "N/A") {
		return ""
	}
	return text
}

// ToTelURI converts a phone string from the database (e.g. #886-02-1234-5678,1234) into a tel: URI.
// Returns "" if the phone number cannot be dialed.
func ToTelURI(phone string) string {
	text := clean(phone)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, "#") {
		text = "+" + text[1:]
	}

	var b strings.Builder
	hasDigit := false
	for _, r := range text {
		if strings.ContainsRune(TelAllowedChars, r) {
			b.WriteRune(r)
			if unicode.IsDigit(r) {
				hasDigit = true
			}
		}
	}
	if !hasDigit {
		return ""
	}
	return "tel:" + b.String()
}

// IsValidEmail checks whether an email string is usable for sending mail.
func IsValidEmail(email string) bool {
	return strings.Contains(clean(email), "@")
}

// BuildShareText constructs the plain-text summary used when sharing a namecard with friends.
func BuildShareText(cardData map[string]string) string {
	type fieldSpec struct {
		icon string
		key  string
	}
	fields := []fieldSpec{
		{"📇", "name"},
		{"", "title"},
		{"", "company"},
		{"📞", "phone"},
		{"✉️", "email"},
		{"📍", "address"},
	}

	var lines []string
	for _, f := range fields {
		text := clean(cardData[f.key])
		if text == "" {
			continue
		}
		if f.icon != "" {
			lines = append(lines, fmt.Sprintf("%s %s", f.icon, text))
		} else {
			lines = append(lines, text)
		}
	}
	return strings.Join(lines, "\n")
}

// percentEncode encodes all characters using RFC 3986 percent-encoding (matching Python's urllib.parse.quote(s, safe="")).
func percentEncode(s string) string {
	encoded := url.QueryEscape(s)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	return encoded
}

// BuildShareURL builds the LINE native share URL, truncating rune-by-rune if the encoded length exceeds 1000 chars.
func BuildShareURL(cardData map[string]string) string {
	text := BuildShareText(cardData)
	encoded := percentEncode(text)

	for text != "" && len(ShareURLPrefix)+len(encoded) > MaxShareURLLength {
		_, size := utf8.DecodeLastRuneInString(text)
		text = text[:len(text)-size]
		encoded = percentEncode(text)
	}
	return ShareURLPrefix + encoded
}

func buildContactActionButtons(cardData map[string]string, cardID string) []map[string]any {
	var buttons []map[string]any

	if telURI := ToTelURI(cardData["phone"]); telURI != "" {
		buttons = append(buttons, map[string]any{
			"type":   "button",
			"style":  "link",
			"height": "sm",
			"action": map[string]any{
				"type":  "uri",
				"label": "📞 撥打",
				"uri":   telURI,
			},
			"flex": 1,
		})
	}

	if IsValidEmail(cardData["email"]) {
		buttons = append(buttons, map[string]any{
			"type":   "button",
			"style":  "link",
			"height": "sm",
			"action": map[string]any{
				"type":        "postback",
				"label":       "✉️ Email",
				"data":        fmt.Sprintf("action=send_email&card_id=%s", cardID),
				"displayText": "我想寄 Email 給這位聯絡人",
			},
			"flex": 1,
		})
	}

	buttons = append(buttons, map[string]any{
		"type":   "button",
		"style":  "link",
		"height": "sm",
		"action": map[string]any{
			"type":  "uri",
			"label": "📤 分享",
			"uri":   BuildShareURL(cardData),
		},
		"flex": 1,
	})

	return buttons
}

func defaultOrNA(val string) string {
	if strings.TrimSpace(val) == "" {
		return "N/A"
	}
	return val
}

// GetNamecardFlexMsg builds the Giga Bubble Flex Message for displaying a single business card.
func GetNamecardFlexMsg(cardData map[string]string, cardID string) FlexPayload {
	name := defaultOrNA(cardData["name"])
	title := defaultOrNA(cardData["title"])
	company := defaultOrNA(cardData["company"])
	address := defaultOrNA(cardData["address"])
	phone := defaultOrNA(cardData["phone"])
	email := defaultOrNA(cardData["email"])
	memo := cardData["memo"]
	if strings.TrimSpace(memo) == "" {
		memo = "尚無備忘錄"
	}

	contents := map[string]any{
		"type": "bubble",
		"size": "giga",
		"header": map[string]any{
			"type":   "box",
			"layout": "vertical",
			"contents": []map[string]any{
				{
					"type":   "box",
					"layout": "vertical",
					"contents": []map[string]any{
						{"type": "text", "text": company, "color": "#ffffff", "size": "lg"},
						{"type": "text", "text": name, "color": "#ffffff", "size": "xxl", "weight": "bold"},
						{"type": "text", "text": title, "color": "#ffffff", "size": "md"},
					},
				},
			},
			"paddingAll":      "20px",
			"backgroundColor": "#0367D3",
		},
		"body": map[string]any{
			"type":   "box",
			"layout": "vertical",
			"contents": []map[string]any{
				{
					"type": "box", "layout": "horizontal", "margin": "md",
					"contents": []map[string]any{
						{"type": "text", "text": "Phone", "size": "sm", "color": "#555555", "flex": 1},
						{"type": "text", "text": phone, "size": "sm", "color": "#111111", "align": "end", "flex": 3},
					},
				},
				{
					"type": "box", "layout": "horizontal", "margin": "md",
					"contents": []map[string]any{
						{"type": "text", "text": "Email", "size": "sm", "color": "#555555", "flex": 1},
						{"type": "text", "text": email, "size": "sm", "color": "#111111", "align": "end", "flex": 3},
					},
				},
				{
					"type": "box", "layout": "horizontal", "margin": "md",
					"contents": []map[string]any{
						{"type": "text", "text": "Address", "size": "sm", "color": "#555555", "flex": 1},
						{"type": "text", "text": address, "size": "sm", "color": "#111111", "align": "end", "wrap": true, "flex": 3},
					},
				},
				{"type": "separator", "margin": "xxl"},
				{
					"type": "box", "layout": "vertical", "margin": "md",
					"contents": []map[string]any{
						{"type": "text", "text": "備忘錄", "size": "md", "color": "#555555"},
						{"type": "text", "text": memo, "color": "#111111", "size": "sm", "wrap": true, "margin": "md"},
					},
				},
			},
		},
		"footer": map[string]any{
			"type":    "box",
			"layout":  "vertical",
			"spacing": "sm",
			"contents": []map[string]any{
				{
					"type":    "box",
					"layout":  "horizontal",
					"spacing": "sm",
					"contents": []map[string]any{
						{
							"type":   "button",
							"style":  "link",
							"height": "sm",
							"action": map[string]any{
								"type":        "postback",
								"label":       "新增/修改記事",
								"data":        fmt.Sprintf("action=add_memo&card_id=%s", cardID),
								"displayText": fmt.Sprintf("我想為 %s 新增記事", name),
							},
							"flex": 1,
						},
						{
							"type":   "button",
							"style":  "link",
							"height": "sm",
							"action": map[string]any{
								"type":        "postback",
								"label":       "編輯資料",
								"data":        fmt.Sprintf("action=edit_card&card_id=%s", cardID),
								"displayText": fmt.Sprintf("我想編輯 %s 的名片", name),
							},
							"flex": 1,
						},
					},
				},
				{
					"type":     "box",
					"layout":   "horizontal",
					"spacing":  "sm",
					"contents": buildContactActionButtons(cardData, cardID),
				},
				{
					"type":   "button",
					"style":  "primary",
					"height": "sm",
					"action": map[string]any{
						"type":        "postback",
						"label":       "📥 加入通訊錄",
						"data":        fmt.Sprintf("action=download_contact&card_id=%s", cardID),
						"displayText": fmt.Sprintf("下載 %s 的聯絡人資訊", name),
					},
					"margin": "sm",
				},
			},
		},
		"styles": map[string]any{
			"footer": map[string]any{
				"separator": true,
			},
		},
	}

	return FlexPayload{
		AltText:  fmt.Sprintf("%s 的名片", name),
		Contents: contents,
	}
}

// GetEditOptionsFlexMsg builds the Flex Message listing all editable fields for a card.
func GetEditOptionsFlexMsg(cardID, cardName string) FlexPayload {
	type fieldOption struct {
		label string
		key   string
	}
	fields := []fieldOption{
		{"姓名", "name"},
		{"職稱", "title"},
		{"公司", "company"},
		{"地址", "address"},
		{"電話", "phone"},
		{"Email", "email"},
	}

	var buttons []map[string]any
	for _, f := range fields {
		buttons = append(buttons, map[string]any{
			"type": "button",
			"action": map[string]any{
				"type":        "postback",
				"label":       f.label,
				"data":        fmt.Sprintf("action=edit_field&card_id=%s&field=%s", cardID, f.key),
				"displayText": fmt.Sprintf("我想修改 %s 的 %s", cardName, f.label),
			},
			"style":  "primary",
			"margin": "sm",
		})
	}

	contents := map[string]any{
		"type": "bubble",
		"body": map[string]any{
			"type":   "box",
			"layout": "vertical",
			"contents": []map[string]any{
				{
					"type":   "text",
					"text":   fmt.Sprintf("請問您想編輯「%s」的哪個欄位？", cardName),
					"weight": "bold",
					"size":   "lg",
					"wrap":   true,
				},
				{
					"type":     "box",
					"layout":   "vertical",
					"margin":   "lg",
					"spacing":  "sm",
					"contents": buttons,
				},
			},
		},
	}

	return FlexPayload{
		AltText:  fmt.Sprintf("編輯 %s 的資料", cardName),
		Contents: contents,
	}
}

// GetNamecardListFlexMsg builds the disambiguation list Flex Message (up to 8 cards).
func GetNamecardListFlexMsg(cards []map[string]string, titleText string) FlexPayload {
	if titleText == "" {
		titleText = "🔍 找到多個相符的名片"
	}

	var rows []map[string]any
	limit := len(cards)
	if limit > 8 {
		limit = 8
	}

	for _, card := range cards[:limit] {
		cardID := card["card_id"]
		name := defaultOrNA(card["name"])
		company := defaultOrNA(card["company"])
		title := defaultOrNA(card["title"])

		infoText := company
		if title != "" && title != "N/A" {
			infoText = fmt.Sprintf("%s | %s", company, title)
		}

		rows = append(rows, map[string]any{
			"type":       "box",
			"layout":     "horizontal",
			"paddingAll": "10px",
			"action": map[string]any{
				"type":        "postback",
				"label":       fmt.Sprintf("顯示 %s", name),
				"data":        fmt.Sprintf("action=show_card&card_id=%s", cardID),
				"displayText": fmt.Sprintf("我想看 %s 的名片", name),
			},
			"contents": []map[string]any{
				{
					"type":   "box",
					"layout": "vertical",
					"flex":   4,
					"contents": []map[string]any{
						{
							"type":   "text",
							"text":   name,
							"weight": "bold",
							"size":   "md",
							"color":  "#111111",
						},
						{
							"type":   "text",
							"text":   infoText,
							"size":   "xs",
							"color":  "#555555",
							"margin": "xs",
							"wrap":   true,
						},
					},
				},
				{
					"type":           "box",
					"layout":         "vertical",
					"flex":           1,
					"justifyContent": "center",
					"alignItems":     "flex-end",
					"contents": []map[string]any{
						{
							"type":   "text",
							"text":   "查看 ❯",
							"size":   "xs",
							"color":  "#0367D3",
							"weight": "bold",
						},
					},
				},
			},
		})
		rows = append(rows, map[string]any{"type": "separator"})
	}

	if len(rows) > 0 {
		rows = rows[:len(rows)-1]
	}

	contents := map[string]any{
		"type": "bubble",
		"body": map[string]any{
			"type":   "box",
			"layout": "vertical",
			"contents": []map[string]any{
				{
					"type":   "text",
					"text":   titleText,
					"weight": "bold",
					"size":   "lg",
					"color":  "#0367D3",
				},
				{
					"type":   "text",
					"text":   fmt.Sprintf("共找到 %d 筆相符資料，請點選要查看的名片：", len(cards)),
					"size":   "xs",
					"color":  "#888888",
					"margin": "sm",
				},
				{"type": "separator", "margin": "md"},
				{
					"type":     "box",
					"layout":   "vertical",
					"margin":   "md",
					"contents": rows,
				},
			},
		},
	}

	return FlexPayload{
		AltText:  titleText,
		Contents: contents,
	}
}

// GetConfirmUpdateFlexMsg builds the secondary confirmation Flex Message before committing an update.
func GetConfirmUpdateFlexMsg(messageText, confirmData, cancelData string) FlexPayload {
	contents := map[string]any{
		"type": "bubble",
		"body": map[string]any{
			"type":   "box",
			"layout": "vertical",
			"contents": []map[string]any{
				{
					"type":   "text",
					"text":   "⚠️ 確認修改資料",
					"weight": "bold",
					"size":   "lg",
					"color":  "#D32F2F",
				},
				{
					"type":   "text",
					"text":   messageText,
					"size":   "md",
					"margin": "md",
					"wrap":   true,
					"color":  "#333333",
				},
			},
		},
		"footer": map[string]any{
			"type":    "box",
			"layout":  "horizontal",
			"spacing": "sm",
			"contents": []map[string]any{
				{
					"type":  "button",
					"style": "primary",
					"color": "#D32F2F",
					"action": map[string]any{
						"type":        "postback",
						"label":       "確定修改",
						"data":        confirmData,
						"displayText": "確定修改",
					},
				},
				{
					"type":  "button",
					"style": "secondary",
					"action": map[string]any{
						"type":        "postback",
						"label":       "取消",
						"data":        cancelData,
						"displayText": "取消修改",
					},
				},
			},
		},
	}

	return FlexPayload{
		AltText:  "確認修改資料",
		Contents: contents,
	}
}

// GetQuickReplyItems returns the standard QuickReply buttons for common bot actions.
func GetQuickReplyItems() *messaging_api.QuickReply {
	return &messaging_api.QuickReply{
		Items: []messaging_api.QuickReplyItem{
			{
				Action: &messaging_api.PostbackAction{
					Label: "📊 統計",
					Data:  "action=show_stats",
				},
			},
			{
				Action: &messaging_api.PostbackAction{
					Label: "📋 列表",
					Data:  "action=show_list",
				},
			},
			{
				Action: &messaging_api.PostbackAction{
					Label: "🧪 測試",
					Data:  "action=show_test",
				},
			},
			{
				Action: &messaging_api.PostbackAction{
					Label: "ℹ️ 說明",
					Data:  "action=show_help",
				},
			},
		},
	}
}

// GetBacksideConfirmQuickReply returns the QuickReply asking if the card has a backside.
func GetBacksideConfirmQuickReply() *messaging_api.QuickReply {
	return &messaging_api.QuickReply{
		Items: []messaging_api.QuickReplyItem{
			{
				Action: &messaging_api.PostbackAction{
					Label: "有背面",
					Data:  "action=backside_confirm&has_backside=yes",
				},
			},
			{
				Action: &messaging_api.PostbackAction{
					Label: "沒有，直接儲存",
					Data:  "action=backside_confirm&has_backside=no",
				},
			},
		},
	}
}
