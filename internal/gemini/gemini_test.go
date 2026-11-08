package gemini

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"google.golang.org/genai"

	"github.com/kkdai/linebot-namecard-go/internal/config"
)

func makeJPEGBytes(c color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, nil)
	return buf.Bytes()
}

func TestGenerateJSONFromTwoImages_SendsPromptAndBothImagesWithSchema(t *testing.T) {
	front := makeJPEGBytes(color.White)
	back := makeJPEGBytes(color.Black)

	var capturedModel string
	var capturedContents []*genai.Content
	var capturedCfg *genai.GenerateContentConfig

	client := &GenAIClient{
		ModelName: config.GeminiModelName,
		GenerateFunc: func(_ context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			capturedModel = model
			capturedContents = contents
			capturedCfg = cfg
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{
						Content: genai.NewContentFromText(`{"name":"王大明 David Wang"}`, genai.RoleModel),
					},
				},
			}, nil
		},
	}

	out, err := client.GenerateJSONFromTwoImages(context.Background(), front, back, config.DoubleSidedImagePrompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != `{"name":"王大明 David Wang"}` {
		t.Errorf("unexpected output: %q", out)
	}
	if capturedModel != "gemini-3-flash-preview" {
		t.Errorf("expected model gemini-3-flash-preview, got %q", capturedModel)
	}
	if len(capturedContents) != 1 || len(capturedContents[0].Parts) != 3 {
		t.Fatalf("expected 1 content with 3 parts (prompt + front + back), got %v", capturedContents)
	}
	if capturedCfg.ResponseMIMEType != "application/json" || capturedCfg.ResponseSchema == nil {
		t.Errorf("expected application/json and non-nil ResponseSchema")
	}
	if capturedCfg.Labels["client_id"] != "namecard" {
		t.Errorf("expected label client_id=namecard, got %v", capturedCfg.Labels)
	}
}
