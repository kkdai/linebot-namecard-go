package models

import (
	"encoding/json"
	"strings"
)

// Namecard represents a single business card record.
type Namecard struct {
	CardID    string `json:"card_id,omitempty" firestore:"card_id,omitempty"`
	Name      string `json:"name" firestore:"name"`
	Title     string `json:"title" firestore:"title"`
	Company   string `json:"company" firestore:"company"`
	Address   string `json:"address" firestore:"address"`
	Phone     string `json:"phone" firestore:"phone"`
	Email     string `json:"email" firestore:"email"`
	Memo      string `json:"memo,omitempty" firestore:"memo,omitempty"`
	CreatedAt string `json:"created_at,omitempty" firestore:"created_at,omitempty"`
}

// ToMap converts a Namecard struct to a map[string]string compatible with existing logic.
func (n Namecard) ToMap() map[string]string {
	m := map[string]string{
		"name":    n.Name,
		"title":   n.Title,
		"company": n.Company,
		"address": n.Address,
		"phone":   n.Phone,
		"email":   n.Email,
	}
	if n.CardID != "" {
		m["card_id"] = n.CardID
	}
	if n.Memo != "" {
		m["memo"] = n.Memo
	}
	if n.CreatedAt != "" {
		m["created_at"] = n.CreatedAt
	}
	return m
}

// NamecardFromMap converts a generic map into a Namecard struct with normalized lowercase keys.
func NamecardFromMap(raw map[string]any) Namecard {
	getStr := func(key string) string {
		for k, v := range raw {
			if strings.EqualFold(k, key) && v != nil {
				if s, ok := v.(string); ok {
					return s
				}
			}
		}
		return ""
	}
	return Namecard{
		CardID:    getStr("card_id"),
		Name:      getStr("name"),
		Title:     getStr("title"),
		Company:   getStr("company"),
		Address:   getStr("address"),
		Phone:     getStr("phone"),
		Email:     getStr("email"),
		Memo:      getStr("memo"),
		CreatedAt: getStr("created_at"),
	}
}

// ParseGeminiResultToNamecard parses Gemini's JSON response (either a single object or an array of objects).
func ParseGeminiResultToNamecard(rawJSON string) (*Namecard, error) {
	cleaned := strings.TrimSpace(rawJSON)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	if cleaned == "" {
		return nil, ErrEmptyResponse
	}

	// Try object first
	var single map[string]any
	if err := json.Unmarshal([]byte(cleaned), &single); err == nil {
		if len(single) == 0 {
			return nil, ErrEmptyResponse
		}
		card := NamecardFromMap(single)
		return &card, nil
	}

	// Try array of objects (Gemini Vision sometimes returns a list)
	var list []map[string]any
	if err := json.Unmarshal([]byte(cleaned), &list); err == nil {
		if len(list) == 0 {
			return nil, ErrEmptyListResponse
		}
		card := NamecardFromMap(list[0])
		return &card, nil
	} else {
		return nil, err
	}
}

// GenerateSampleNamecard returns the sample card used by the `show_test` action.
func GenerateSampleNamecard() Namecard {
	return Namecard{
		Name:    "Kevin Dai",
		Title:   "Software Engineer",
		Address: "Taipei, Taiwan",
		Email:   "aa@bbb.cc",
		Phone:   "+886-123-456-789",
		Company: "LINE Taiwan",
		Memo:    "This is a test memo.",
	}
}

// Statistics holds aggregated namecard metrics for a user.
type Statistics struct {
	Total      int    `json:"total"`
	ThisMonth  int    `json:"this_month"`
	TopCompany string `json:"top_company"`
}

// UserActionType enumerates the multi-step user state actions.
type UserActionType string

const (
	ActionPendingBacksideConfirm UserActionType = "pending_backside_confirm"
	ActionAwaitingBacksideImage  UserActionType = "awaiting_backside_image"
	ActionAddingMemo             UserActionType = "adding_memo"
	ActionEditingField           UserActionType = "editing_field"
	ActionPendingUpdate          UserActionType = "pending_update"
)

// UserState represents a user's multi-step interactive state stored in shared memory and Firestore.
type UserState struct {
	Action          UserActionType `json:"action" firestore:"action"`
	CardID          string         `json:"card_id,omitempty" firestore:"card_id,omitempty"`
	Field           string         `json:"field,omitempty" firestore:"field,omitempty"`
	Value           string         `json:"value,omitempty" firestore:"value,omitempty"`
	Memo            string         `json:"memo,omitempty" firestore:"memo,omitempty"`
	UpdateType      string         `json:"update_type,omitempty" firestore:"update_type,omitempty"`
	CardObj         *Namecard      `json:"card_obj,omitempty" firestore:"card_obj,omitempty"`
	FrontImageBytes []byte         `json:"front_image_bytes,omitempty" firestore:"front_image_bytes,omitempty"`
	ExpiresAt       float64        `json:"expires_at,omitempty" firestore:"expires_at,omitempty"`
}

// Sentinel errors for parsing
type parseError string

func (e parseError) Error() string { return string(e) }

const (
	ErrEmptyResponse     parseError = "empty JSON response"
	ErrEmptyListResponse parseError = "empty JSON list response"
)
