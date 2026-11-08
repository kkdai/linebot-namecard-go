package flex

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

var sampleCardObj = map[string]string{
	"name":    "王大明",
	"title":   "工程師",
	"company": "測試公司",
	"address": "台北市信義區",
	"phone":   "#886-02-1234-5678,1234",
	"email":   "david@example.com",
}

func collectActions(node any) []map[string]any {
	var actions []map[string]any
	switch v := node.(type) {
	case map[string]any:
		if act, ok := v["action"].(map[string]any); ok {
			actions = append(actions, act)
		}
		for _, child := range v {
			actions = append(actions, collectActions(child)...)
		}
	case []map[string]any:
		for _, item := range v {
			actions = append(actions, collectActions(item)...)
		}
	case []any:
		for _, item := range v {
			actions = append(actions, collectActions(item)...)
		}
	}
	return actions
}

func TestToTelURI(t *testing.T) {
	if got := ToTelURI("#886-02-1234-5678"); got != "tel:+886-02-1234-5678" {
		t.Errorf("expected tel:+886-02-1234-5678, got %q", got)
	}
	if got := ToTelURI("#886-02-1234-5678,1234"); got != "tel:+886-02-1234-5678,1234" {
		t.Errorf("expected tel:+886-02-1234-5678,1234, got %q", got)
	}
	if got := ToTelURI("(02) 1234 5678"); got != "tel:0212345678" {
		t.Errorf("expected tel:0212345678, got %q", got)
	}
	if got := ToTelURI("N/A"); got != "" {
		t.Errorf("expected empty string for N/A, got %q", got)
	}
	if got := ToTelURI(""); got != "" {
		t.Errorf("expected empty string for empty input, got %q", got)
	}
	if got := ToTelURI("---"); got != "" {
		t.Errorf("expected empty string when no digits, got %q", got)
	}
}

func TestIsValidEmail(t *testing.T) {
	if !IsValidEmail("david@example.com") {
		t.Errorf("expected david@example.com to be valid")
	}
	if IsValidEmail("N/A") || IsValidEmail("") || IsValidEmail("david.example.com") {
		t.Errorf("expected invalid emails to return false")
	}
}

func TestBuildShareTextAndURL(t *testing.T) {
	text := BuildShareText(sampleCardObj)
	for _, want := range []string{"王大明", "工程師", "測試公司", "#886-02-1234-5678,1234", "david@example.com"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected share text to contain %q, got %q", want, text)
		}
	}

	naCard := map[string]string{
		"name":    "李小華",
		"title":   "N/A",
		"company": "某公司",
		"address": "N/A",
		"phone":   "N/A",
		"email":   "N/A",
	}
	naText := BuildShareText(naCard)
	if !strings.Contains(naText, "李小華") || !strings.Contains(naText, "某公司") || strings.Contains(naText, "N/A") {
		t.Errorf("unexpected share text for N/A fields: %q", naText)
	}

	longCard := map[string]string{
		"name":    "王大明",
		"address": strings.Repeat("台北市信義區信義路五段七號", 40),
	}
	shareURL := BuildShareURL(longCard)
	if len(shareURL) > MaxShareURLLength {
		t.Errorf("expected shareURL length <= %d, got %d", MaxShareURLLength, len(shareURL))
	}
	if !strings.HasPrefix(shareURL, ShareURLPrefix) {
		t.Errorf("expected shareURL prefix %q, got %q", ShareURLPrefix, shareURL)
	}
}

func TestNamecardFlexMsgActions(t *testing.T) {
	msg := GetNamecardFlexMsg(sampleCardObj, "card-1")
	rawBytes, err := json.Marshal(msg.Contents)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(rawBytes, &generic); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	actions := collectActions(generic)
	var uris, datas []string
	for _, a := range actions {
		if a["type"] == "uri" {
			if u, ok := a["uri"].(string); ok {
				uris = append(uris, u)
			}
		} else if a["type"] == "postback" {
			if d, ok := a["data"].(string); ok {
				datas = append(datas, d)
			}
		}
	}

	if !slices.Contains(uris, "tel:+886-02-1234-5678,1234") {
		t.Errorf("expected tel URI in %v", uris)
	}
	hasShare := false
	for _, u := range uris {
		if strings.HasPrefix(u, ShareURLPrefix) {
			hasShare = true
		}
	}
	if !hasShare {
		t.Errorf("expected share URI in %v", uris)
	}

	for _, expectedData := range []string{
		"action=send_email&card_id=card-1",
		"action=add_memo&card_id=card-1",
		"action=edit_card&card_id=card-1",
		"action=download_contact&card_id=card-1",
	} {
		if !slices.Contains(datas, expectedData) {
			t.Errorf("expected postback data %q in %v", expectedData, datas)
		}
	}

	// Verify conversion to LINE SDK FlexMessage succeeds
	lineFlex, err := msg.ToLineFlexMessage(GetQuickReplyItems())
	if err != nil || lineFlex == nil {
		t.Fatalf("expected valid LINE SDK FlexMessage conversion, got err=%v", err)
	}
}
