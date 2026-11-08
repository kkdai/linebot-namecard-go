package qrcode

import (
	"strings"
	"testing"
)

func TestGenerateVCardString(t *testing.T) {
	data := map[string]string{
		"name":    "王大明",
		"title":   "工程師",
		"company": "測試公司",
		"phone":   "#886-02-1234-5678",
		"email":   "david@example.com",
		"address": "台北市信義區",
		"memo":    "第一行\n第二行,號;分號",
	}

	vcard := GenerateVCardString(data)

	expectedSubstrings := []string{
		"BEGIN:VCARD",
		"VERSION:3.0",
		"FN:王大明",
		"N:王大明;;;",
		"ORG:測試公司",
		"TITLE:工程師",
		"TEL;TYPE=WORK,VOICE:#8860212345678",
		"EMAIL;TYPE=WORK:david@example.com",
		"ADR;TYPE=WORK:;;台北市信義區;;;;",
		`NOTE:第一行\n第二行\,號\;分號`,
		"END:VCARD",
	}

	for _, exp := range expectedSubstrings {
		if !strings.Contains(vcard, exp) {
			t.Errorf("expected vCard to contain %q, got:\n%s", exp, vcard)
		}
	}
}

func TestGenerateVCardQRCode(t *testing.T) {
	data := map[string]string{
		"name":  "王大明",
		"email": "david@example.com",
	}

	reader, pngBytes, err := GenerateVCardQRCode(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pngBytes) == 0 || reader.Len() == 0 {
		t.Fatalf("expected non-empty PNG bytes")
	}
	// Verify PNG magic header
	if len(pngBytes) < 8 || string(pngBytes[1:4]) != "PNG" {
		t.Errorf("expected valid PNG header")
	}
}
