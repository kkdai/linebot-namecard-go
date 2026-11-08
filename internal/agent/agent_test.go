package agent

import (
	"context"
	"iter"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"

	internalmem "github.com/kkdai/linebot-namecard-go/internal/memory"
	"github.com/kkdai/linebot-namecard-go/internal/models"
	"github.com/kkdai/linebot-namecard-go/internal/storage"
)

// mockADKModel implements google.golang.org/adk/model.LLM to test the full ADK Runner + FunctionTools loop deterministically.
type mockADKModel struct {
	turn int
}

func (m *mockADKModel) Name() string {
	return "mock-gemini-3-flash"
}

func (m *mockADKModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.turn++
		switch m.turn {
		case 1:
			// Step 1: Model calls get_all_namecards
			yield(&model.LLMResponse{
				Content: &genai.Content{
					Role: genai.RoleModel,
					Parts: []*genai.Part{
						genai.NewPartFromFunctionCall("get_all_namecards", map[string]any{}),
					},
				},
			}, nil)
		case 2:
			// Step 2: Model calls display_namecard for card_123
			yield(&model.LLMResponse{
				Content: &genai.Content{
					Role: genai.RoleModel,
					Parts: []*genai.Part{
						genai.NewPartFromFunctionCall("display_namecard", map[string]any{
							"card_id": "card_123",
						}),
					},
				},
			}, nil)
		default:
			// Step 3: Model returns final text response
			yield(&model.LLMResponse{
				Content: genai.NewContentFromText("為您找到王大明的名片！", genai.RoleModel),
			}, nil)
		}
	}
}

func TestADKService_RunQueryWithFunctionToolsAndFirestoreSession(t *testing.T) {
	ctx := context.Background()
	repo := storage.NewStoreRepository(nil, nil, nil, "")
	repo.AddNamecard(ctx, models.Namecard{
		CardID:  "card_123",
		Name:    "王大明",
		Company: "谷歌",
		Phone:   "0912-345-678",
		Email:   "daming@google.com",
	}, "user_abc")

	stateStore := internalmem.NewUserStateStore(nil)
	sessionSvc := internalmem.NewFirestoreSessionService(nil)
	memorySvc := internalmem.NewFirestoreMemoryService(nil)

	adkSvc, err := NewADKService(
		ctx,
		"test-project",
		"global",
		repo,
		stateStore,
		sessionSvc,
		memorySvc,
		&mockADKModel{},
	)
	if err != nil {
		t.Fatalf("NewADKService failed: %v", err)
	}

	res, err := adkSvc.RunQuery(ctx, "user_abc", "找王大明的名片")
	if err != nil {
		t.Fatalf("RunQuery failed: %v", err)
	}

	if res.ReplyText != "為您找到王大明的名片！" {
		t.Errorf("unexpected reply text: %q", res.ReplyText)
	}
	if len(res.FoundCardIDs) != 1 || res.FoundCardIDs[0] != "card_123" {
		t.Errorf("expected FoundCardIDs=[card_123], got %v", res.FoundCardIDs)
	}
}
