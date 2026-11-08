package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"

	"github.com/kkdai/linebot-namecard-go/internal/config"
	 internalmem "github.com/kkdai/linebot-namecard-go/internal/memory"
	"github.com/kkdai/linebot-namecard-go/internal/models"
	"github.com/kkdai/linebot-namecard-go/internal/storage"
)

const AppName = "namecard_bot_app"

// EmptyArgs represents an empty input struct for parameterless function tools.
type EmptyArgs struct{}

// GetAllNamecardsResult wraps the list of namecards returned by get_all_namecards.
type GetAllNamecardsResult struct {
	Cards []models.Namecard `json:"cards"`
}

// CardIDArgs represents arguments requiring a card_id.
type CardIDArgs struct {
	CardID string `json:"card_id" jsonschema:"名片的唯一識別碼 card_id"`
}

// GetNamecardByIDResult wraps a single namecard returned by get_namecard_by_id.
type GetNamecardByIDResult struct {
	Card *models.Namecard `json:"card,omitempty"`
}

// DisplayNamecardResult wraps the status message returned by display_namecard.
type DisplayNamecardResult struct {
	Message string `json:"message"`
}

// UpdateMemoArgs represents arguments for update_namecard_memo.
type UpdateMemoArgs struct {
	CardID string `json:"card_id" jsonschema:"名片的唯一識別碼 card_id"`
	Memo   string `json:"memo" jsonschema:"要更新的備忘錄／記事內容"`
}

// UpdateFieldArgs represents arguments for update_namecard_field.
type UpdateFieldArgs struct {
	CardID string `json:"card_id" jsonschema:"名片的唯一識別碼 card_id"`
	Field  string `json:"field" jsonschema:"要修改的指定欄位（可選欄位有：name、title、company、address、phone、email）"`
	Value  string `json:"value" jsonschema:"新的欄位內容"`
}

// UpdateResult wraps the boolean status returned by update tools.
type UpdateResult struct {
	Success bool `json:"success"`
}

// SmartQueryResult holds the output of an ADK smart query turn.
type SmartQueryResult struct {
	ReplyText    string
	FoundCardIDs []string
}

// SmartQueryEngine defines the interface for running natural language namecard queries.
type SmartQueryEngine interface {
	RunQuery(ctx context.Context, userID, msg string) (*SmartQueryResult, error)
}

// ADKService manages the shared ADK Runner, shared Firestore SessionService, shared Firestore MemoryService,
// and the 5 namecard FunctionTools.
type ADKService struct {
	repo           storage.CardRepository
	stateStore     *internalmem.UserStateStore
	sessionService session.Service
	memoryService  memory.Service
	llmModel       model.LLM
	runner         *runner.Runner

	turnMu       sync.Mutex
	turnFoundIDs map[string][]string
}

// NewADKService initializes the shared ADK Agent and Runner backed by the shared Firestore SessionService and MemoryService.
// If llmModel is nil, it initializes a Vertex AI Gemini model (`gemini-3-flash-preview`).
func NewADKService(
	ctx context.Context,
	projectID, location string,
	repo storage.CardRepository,
	stateStore *internalmem.UserStateStore,
	sessionSvc session.Service,
	memorySvc memory.Service,
	customLLM model.LLM,
) (*ADKService, error) {
	if sessionSvc == nil {
		sessionSvc = session.InMemoryService()
	}
	if memorySvc == nil {
		memorySvc = memory.InMemoryService()
	}

	llm := customLLM
	if llm == nil {
		if location == "" {
			location = "global"
		}
		var err error
		llm, err = gemini.NewModel(ctx, config.GeminiModelName, &genai.ClientConfig{
			Project:  projectID,
			Location: location,
			Backend:  genai.BackendVertexAI,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create ADK Gemini model: %w", err)
		}
	}

	svc := &ADKService{
		repo:           repo,
		stateStore:     stateStore,
		sessionService: sessionSvc,
		memoryService:  memorySvc,
		llmModel:       llm,
		turnFoundIDs:   make(map[string][]string),
	}

	tools, err := svc.buildSharedTools()
	if err != nil {
		return nil, err
	}

	ag, err := llmagent.New(llmagent.Config{
		Name:        "namecard_agent",
		Description: "LINE 名片管理與智慧查詢助理",
		Model:       llm,
		Instruction: config.AgentSystemInstruction,
		Tools:       tools,
		GenerateContentConfig: &genai.GenerateContentConfig{
			Labels: map[string]string{"client_id": "namecard"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create ADK llmagent: %w", err)
	}

	r, err := runner.New(runner.Config{
		AppName:           AppName,
		Agent:             ag,
		SessionService:    sessionSvc,
		MemoryService:     memorySvc,
		AutoCreateSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create ADK runner: %w", err)
	}
	svc.runner = r
	return svc, nil
}

func (s *ADKService) recordFoundCard(userID, cardID string) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if !slices.Contains(s.turnFoundIDs[userID], cardID) {
		s.turnFoundIDs[userID] = append(s.turnFoundIDs[userID], cardID)
	}
}

func (s *ADKService) resetFoundCards(userID string) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	s.turnFoundIDs[userID] = nil
}

func (s *ADKService) consumeFoundCards(userID string) []string {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	ids := slices.Clone(s.turnFoundIDs[userID])
	delete(s.turnFoundIDs, userID)
	return ids
}

func (s *ADKService) buildSharedTools() ([]tool.Tool, error) {
	tGetAll, err := functiontool.New(
		functiontool.Config{
			Name:        "get_all_namecards",
			Description: "取得當前使用者在資料庫中所有的名片資料列表。每張名片資料都包含唯一的 card_id 欄位。",
		},
		func(tctx adkagent.ToolContext, _ EmptyArgs) (GetAllNamecardsResult, error) {
			uid := tctx.UserID()
			cardsMap := s.repo.GetAllCards(tctx, uid)
			list := make([]models.Namecard, 0, len(cardsMap))
			for cid, card := range cardsMap {
				c := card
				c.CardID = cid
				list = append(list, c)
			}
			return GetAllNamecardsResult{Cards: list}, nil
		},
	)
	if err != nil {
		return nil, err
	}

	tGetByID, err := functiontool.New(
		functiontool.Config{
			Name:        "get_namecard_by_id",
			Description: "透過特定的 card_id 取得單張名片的詳細欄位與資料。",
		},
		func(tctx adkagent.ToolContext, args CardIDArgs) (GetNamecardByIDResult, error) {
			card := s.repo.GetCardByID(tctx, tctx.UserID(), args.CardID)
			return GetNamecardByIDResult{Card: card}, nil
		},
	)
	if err != nil {
		return nil, err
	}

	tDisplay, err := functiontool.New(
		functiontool.Config{
			Name:        "display_namecard",
			Description: "顯示特定名片給使用者看。當找到與搜尋相匹配的名片時，務必調用此工具。",
		},
		func(tctx adkagent.ToolContext, args CardIDArgs) (DisplayNamecardResult, error) {
			if args.CardID != "" {
				s.recordFoundCard(tctx.UserID(), args.CardID)
			}
			return DisplayNamecardResult{
				Message: fmt.Sprintf("已將名片 ID 標記為顯示：%s", args.CardID),
			}, nil
		},
	)
	if err != nil {
		return nil, err
	}

	tUpdateMemo, err := functiontool.New(
		functiontool.Config{
			Name:        "update_namecard_memo",
			Description: "更新特定名片的備忘錄／記事資訊。",
		},
		func(tctx adkagent.ToolContext, args UpdateMemoArgs) (UpdateResult, error) {
			s.stateStore.Set(tctx, tctx.UserID(), models.UserState{
				Action:     models.ActionPendingUpdate,
				UpdateType: "memo",
				CardID:     args.CardID,
				Memo:       args.Memo,
			})
			return UpdateResult{Success: true}, nil
		},
	)
	if err != nil {
		return nil, err
	}

	tUpdateField, err := functiontool.New(
		functiontool.Config{
			Name:        "update_namecard_field",
			Description: "更新特定名片的指定欄位（可選欄位有：name、title、company、address、phone、email）。",
		},
		func(tctx adkagent.ToolContext, args UpdateFieldArgs) (UpdateResult, error) {
			s.stateStore.Set(tctx, tctx.UserID(), models.UserState{
				Action:     models.ActionPendingUpdate,
				UpdateType: "field",
				CardID:     args.CardID,
				Field:      args.Field,
				Value:      args.Value,
			})
			return UpdateResult{Success: true}, nil
		},
	)
	if err != nil {
		return nil, err
	}

	return []tool.Tool{tGetAll, tGetByID, tDisplay, tUpdateMemo, tUpdateField}, nil
}

// RunQuery executes a conversational turn with the shared ADK Runner and persists the session to Firestore.
func (s *ADKService) RunQuery(ctx context.Context, userID, msg string) (*SmartQueryResult, error) {
	s.resetFoundCards(userID)

	userContent := genai.NewContentFromText(msg, genai.RoleUser)
	var finalText strings.Builder

	for ev, err := range s.runner.Run(ctx, userID, userID, userContent, adkagent.RunConfig{}) {
		if err != nil {
			return nil, err
		}
		if ev != nil && ev.Content != nil {
			for _, part := range ev.Content.Parts {
				if part != nil && part.Text != "" {
					finalText.WriteString(part.Text)
				}
			}
		}
	}

	// Also ingest updated session into long-term memory service
	if getResp, err := s.sessionService.Get(ctx, &session.GetRequest{
		AppName:   AppName,
		UserID:    userID,
		SessionID: userID,
	}); err == nil && getResp != nil && getResp.Session != nil && s.memoryService != nil {
		_ = s.memoryService.AddSessionToMemory(ctx, getResp.Session)
	}

	reply := strings.TrimSpace(finalText.String())
	if reply == "" {
		reply = "為您完成處理。"
	}

	return &SmartQueryResult{
		ReplyText:    reply,
		FoundCardIDs: s.consumeFoundCards(userID),
	}, nil
}

// FallbackKeywordSearch performs local substring matching over name and company when ADK / Vertex AI is unavailable.
func FallbackKeywordSearch(ctx context.Context, repo storage.CardRepository, userID, query string) []models.Namecard {
	allCards := repo.GetAllCards(ctx, userID)
	if len(allCards) == 0 {
		return nil
	}
	qLower := strings.ToLower(strings.TrimSpace(query))
	var matches []models.Namecard
	for cid, card := range allCards {
		nameLower := strings.ToLower(card.Name)
		compLower := strings.ToLower(card.Company)
		if strings.Contains(nameLower, qLower) || strings.Contains(compLower, qLower) {
			c := card
			c.CardID = cid
			matches = append(matches, c)
		}
	}
	return matches
}
