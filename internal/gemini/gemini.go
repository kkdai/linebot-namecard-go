package gemini

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"

	"google.golang.org/genai"

	"github.com/kkdai/linebot-namecard-go/internal/config"
)

// VisionClient abstracts Gemini image-to-JSON extraction for production and testing.
type VisionClient interface {
	GenerateJSONFromImage(ctx context.Context, imgBytes []byte, prompt string) (string, error)
	GenerateJSONFromTwoImages(ctx context.Context, frontBytes, backBytes []byte, prompt string) (string, error)
}

// NamecardSchema returns the structured output schema matching Python's NAMECARD_SCHEMA.
func NamecardSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"name": {
				Type:        genai.TypeString,
				Description: "聯絡人姓名，如果看不出來，請填寫 N/A",
			},
			"title": {
				Type:        genai.TypeString,
				Description: "職稱或頭銜，如果看不出來，請填寫 N/A",
			},
			"company": {
				Type:        genai.TypeString,
				Description: "公司名稱，如果看不出來，請填寫 N/A",
			},
			"address": {
				Type:        genai.TypeString,
				Description: "公司或聯絡地址，如果看不出來，請填寫 N/A",
			},
			"phone": {
				Type:        genai.TypeString,
				Description: "電話號碼，格式為 #886-0123-456-789,1234。沒有分機就忽略 ,1234。如果看不出來，請填寫 N/A",
			},
			"email": {
				Type:        genai.TypeString,
				Description: "電子郵件信箱，如果看不出來，請填寫 N/A",
			},
		},
		Required: []string{"name", "title", "company", "address", "phone", "email"},
	}
}

// NormalizeToJPEG decodes an image (JPEG/PNG) and re-encodes it as JPEG bytes (matching pil_to_bytes in Python).
func NormalizeToJPEG(raw []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid image bytes: %w", err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, fmt.Errorf("failed to encode JPEG: %w", err)
	}
	return buf.Bytes(), nil
}

// GenAIClient implements VisionClient using Google's official google.golang.org/genai SDK on Vertex AI.
type GenAIClient struct {
	Client    *genai.Client
	ModelName string
	// GenerateFunc allows unit tests to intercept the underlying GenerateContent call and inspect parts/config.
	GenerateFunc func(ctx context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
}

// NewGenAIClient creates a Vertex AI backed GenAIClient.
func NewGenAIClient(ctx context.Context, projectID, location string) (*GenAIClient, error) {
	if location == "" {
		location = "global"
	}
	cli, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  projectID,
		Location: location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		return nil, err
	}
	return &GenAIClient{
		Client:    cli,
		ModelName: config.GeminiModelName,
	}, nil
}

func (g *GenAIClient) generate(ctx context.Context, parts []*genai.Part) (string, error) {
	modelName := g.ModelName
	if modelName == "" {
		modelName = config.GeminiModelName
	}

	genCfg := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   NamecardSchema(),
		Labels:           map[string]string{"client_id": "namecard"},
	}

	contents := []*genai.Content{
		{
			Role:  genai.RoleUser,
			Parts: parts,
		},
	}

	var resp *genai.GenerateContentResponse
	var err error
	if g.GenerateFunc != nil {
		resp, err = g.GenerateFunc(ctx, modelName, contents, genCfg)
	} else {
		resp, err = g.Client.Models.GenerateContent(ctx, modelName, contents, genCfg)
	}
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", fmt.Errorf("empty response from Gemini")
	}
	return resp.Text(), nil
}

// GenerateJSONFromImage extracts structured namecard JSON from a single card image.
func (g *GenAIClient) GenerateJSONFromImage(ctx context.Context, imgBytes []byte, prompt string) (string, error) {
	jpegBytes, err := NormalizeToJPEG(imgBytes)
	if err != nil {
		return "", err
	}
	parts := []*genai.Part{
		genai.NewPartFromText(prompt),
		genai.NewPartFromBytes(jpegBytes, "image/jpeg"),
	}
	return g.generate(ctx, parts)
}

// GenerateJSONFromTwoImages extracts and merges structured namecard JSON from front and back card images.
func (g *GenAIClient) GenerateJSONFromTwoImages(ctx context.Context, frontBytes, backBytes []byte, prompt string) (string, error) {
	frontJPEG, err := NormalizeToJPEG(frontBytes)
	if err != nil {
		return "", fmt.Errorf("front image error: %w", err)
	}
	backJPEG, err := NormalizeToJPEG(backBytes)
	if err != nil {
		return "", fmt.Errorf("back image error: %w", err)
	}
	parts := []*genai.Part{
		genai.NewPartFromText(prompt),
		genai.NewPartFromBytes(frontJPEG, "image/jpeg"),
		genai.NewPartFromBytes(backJPEG, "image/jpeg"),
	}
	return g.generate(ctx, parts)
}
