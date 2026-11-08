package qrcode

import (
	"bytes"
	"fmt"
	"strings"

	goqrcode "github.com/skip2/go-qrcode"
)

// GenerateVCardString generates a vCard 3.0 formatted string from namecard fields.
func GenerateVCardString(namecardData map[string]string) string {
	name := namecardData["name"]
	title := namecardData["title"]
	company := namecardData["company"]
	phone := namecardData["phone"]
	email := namecardData["email"]
	address := namecardData["address"]
	memo := namecardData["memo"]

	vcardLines := []string{
		"BEGIN:VCARD",
		"VERSION:3.0",
		fmt.Sprintf("FN:%s", name),
		fmt.Sprintf("N:%s;;;", name),
	}

	if company != "" {
		vcardLines = append(vcardLines, fmt.Sprintf("ORG:%s", company))
	}

	if title != "" {
		vcardLines = append(vcardLines, fmt.Sprintf("TITLE:%s", title))
	}

	if phone != "" {
		cleanPhone := strings.ReplaceAll(phone, "-", "")
		cleanPhone = strings.ReplaceAll(cleanPhone, " ", "")
		vcardLines = append(vcardLines, fmt.Sprintf("TEL;TYPE=WORK,VOICE:%s", cleanPhone))
	}

	if email != "" {
		vcardLines = append(vcardLines, fmt.Sprintf("EMAIL;TYPE=WORK:%s", email))
	}

	if address != "" {
		vcardLines = append(vcardLines, fmt.Sprintf("ADR;TYPE=WORK:;;%s;;;;", address))
	}

	if memo != "" {
		escapedMemo := strings.ReplaceAll(memo, "\n", `\n`)
		escapedMemo = strings.ReplaceAll(escapedMemo, ",", `\,`)
		escapedMemo = strings.ReplaceAll(escapedMemo, ";", `\;`)
		vcardLines = append(vcardLines, fmt.Sprintf("NOTE:%s", escapedMemo))
	}

	vcardLines = append(vcardLines, "END:VCARD")

	return strings.Join(vcardLines, "\n")
}

// GenerateVCardQRCode generates a PNG QR Code containing vCard 3.0 data.
func GenerateVCardQRCode(namecardData map[string]string) (*bytes.Reader, []byte, error) {
	vcardString := GenerateVCardString(namecardData)

	qr, err := goqrcode.New(vcardString, goqrcode.Low)
	if err != nil {
		return nil, nil, err
	}

	pngBytes, err := qr.PNG(256)
	if err != nil {
		return nil, nil, err
	}

	return bytes.NewReader(pngBytes), pngBytes, nil
}

// GetQRCodeUsageInstruction returns the user instruction message for scanning the QR Code.
func GetQRCodeUsageInstruction(name string) string {
	return fmt.Sprintf(`已為「%s」生成聯絡人 QR Code！

📱 使用方式：
1. 用手機相機 App 掃描上方的 QR Code
2. 系統會自動識別聯絡人資訊
3. 點擊「加入聯絡人」即可匯入

✅ 支援 iPhone 和 Android 所有智慧型手機`, name)
}
