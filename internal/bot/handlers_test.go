package bot

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"

	"github.com/kkdai/linebot-namecard-go/internal/agent"
	internalmem "github.com/kkdai/linebot-namecard-go/internal/memory"
	"github.com/kkdai/linebot-namecard-go/internal/models"
	"github.com/kkdai/linebot-namecard-go/internal/storage"
)

type fakeLineMessenger struct {
	imageBytes []byte
	lastToken  string
	lastMsgs   []ReplyMessage
	replyCount int
}

func (f *fakeLineMessenger) ReplyMessages(_ context.Context, replyToken string, msgs []ReplyMessage) error {
	f.lastToken = replyToken
	f.lastMsgs = msgs
	f.replyCount++
	return nil
}

func (f *fakeLineMessenger) GetMessageContent(_ context.Context, _ string) ([]byte, error) {
	return f.imageBytes, nil
}

type fakeVisionClient struct {
	singleJSON string
	mergeJSON  string
	singleCall int
	mergeCall  int
}

func (v *fakeVisionClient) GenerateJSONFromImage(_ context.Context, _ []byte, _ string) (string, error) {
	v.singleCall++
	return v.singleJSON, nil
}

func (v *fakeVisionClient) GenerateJSONFromTwoImages(_ context.Context, _, _ []byte, _ string) (string, error) {
	v.mergeCall++
	return v.mergeJSON, nil
}

type fakeQueryEngine struct {
	called  int
	lastUID string
	lastMsg string
	result  *agent.SmartQueryResult
	err     error
}

func (q *fakeQueryEngine) RunQuery(_ context.Context, userID, msg string) (*agent.SmartQueryResult, error) {
	q.called++
	q.lastUID = userID
	q.lastMsg = msg
	return q.result, q.err
}

func makeJPEG(c color.Color) []byte {
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

const (
	testCardJSON   = `{"name":"王大明","title":"工程師","company":"測試公司","address":"台北市","phone":"#886-02-1234-5678","email":"david@example.com"}`
	mergedCardJSON = `{"name":"王大明 David Wang","title":"工程師","company":"測試公司","address":"台北市","phone":"#886-02-1234-5678","email":"david@example.com"}`
)

func TestHandleImageEvent_SingleAndDoubleSidedFlow(t *testing.T) {
	ctx := context.Background()
	frontBytes := makeJPEG(color.White)
	backBytes := makeJPEG(color.Black)

	line := &fakeLineMessenger{imageBytes: frontBytes}
	repo := storage.NewStoreRepository(nil, nil, nil, "")
	stateStore := internalmem.NewUserStateStore(nil)
	vision := &fakeVisionClient{singleJSON: testCardJSON, mergeJSON: mergedCardJSON}
	h := NewBotHandler(line, repo, stateStore, vision, nil)

	// 1. Fresh image asks about backside and does not save yet
	if err := h.HandleImageEvent(ctx, "token-1", "user-1", "msg-1"); err != nil {
		t.Fatalf("HandleImageEvent failed: %v", err)
	}
	if vision.singleCall != 1 || len(repo.GetAllCards(ctx, "user-1")) != 0 {
		t.Fatalf("expected 1 single OCR call and 0 saved cards")
	}
	st, ok := stateStore.Get(ctx, "user-1")
	if !ok || st.Action != models.ActionPendingBacksideConfirm || st.CardObj.Name != "王大明" {
		t.Fatalf("unexpected state after front image: %+v", st)
	}
	if len(line.lastMsgs) != 1 || !strings.Contains(line.lastMsgs[0].Text, "還有背面嗎") {
		t.Errorf("expected backside confirmation prompt, got %+v", line.lastMsgs)
	}

	// 2. User clicks "有背面" (backside_confirm&has_backside=yes)
	if err := h.HandlePostbackEvent(ctx, "token-2", "user-1", "action=backside_confirm&has_backside=yes"); err != nil {
		t.Fatalf("HandlePostbackEvent failed: %v", err)
	}
	st, ok = stateStore.Get(ctx, "user-1")
	if !ok || st.Action != models.ActionAwaitingBacksideImage {
		t.Fatalf("expected awaiting_backside_image state, got %+v", st)
	}
	if !strings.Contains(line.lastMsgs[0].Text, "背面照片") {
		t.Errorf("expected prompt for backside photo, got %+v", line.lastMsgs)
	}

	// 3. User sends backside image -> merges and saves
	line.imageBytes = backBytes
	if err := h.HandleImageEvent(ctx, "token-3", "user-1", "msg-2"); err != nil {
		t.Fatalf("HandleImageEvent backside failed: %v", err)
	}
	if vision.mergeCall != 1 {
		t.Errorf("expected 1 merge call, got %d", vision.mergeCall)
	}
	if _, ok := stateStore.Get(ctx, "user-1"); ok {
		t.Errorf("expected user state to be cleared after merge")
	}
	cards := repo.GetAllCards(ctx, "user-1")
	if len(cards) != 1 {
		t.Fatalf("expected 1 saved card, got %d", len(cards))
	}
	for _, c := range cards {
		if c.Name != "王大明 David Wang" {
			t.Errorf("expected merged name '王大明 David Wang', got %q", c.Name)
		}
	}
}

func TestHandlePostback_StaleBacksideConfirmDoesNotClearUnrelatedState(t *testing.T) {
	ctx := context.Background()
	line := &fakeLineMessenger{}
	repo := storage.NewStoreRepository(nil, nil, nil, "")
	stateStore := internalmem.NewUserStateStore(nil)
	h := NewBotHandler(line, repo, stateStore, nil, nil)

	stateStore.Set(ctx, "user-1", models.UserState{
		Action: models.ActionEditingField,
		CardID: "card-x",
		Field:  "phone",
	})

	if err := h.HandlePostbackEvent(ctx, "token-1", "user-1", "action=backside_confirm&has_backside=yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	st, ok := stateStore.Get(ctx, "user-1")
	if !ok || st.Action != models.ActionEditingField {
		t.Errorf("expected editing_field state to be preserved, got %+v", st)
	}
	if !strings.Contains(line.lastMsgs[0].Text, "過期") {
		t.Errorf("expected expired reply message, got %+v", line.lastMsgs)
	}
}

func TestHandleTextEvent_InterruptsPendingBacksideState(t *testing.T) {
	ctx := context.Background()
	line := &fakeLineMessenger{}
	repo := storage.NewStoreRepository(nil, nil, nil, "")
	stateStore := internalmem.NewUserStateStore(nil)
	qe := &fakeQueryEngine{
		result: &agent.SmartQueryResult{ReplyText: "搜尋結果"},
	}
	h := NewBotHandler(line, repo, stateStore, nil, qe)

	stateStore.Set(ctx, "user-1", models.UserState{
		Action:    models.ActionPendingBacksideConfirm,
		ExpiresAt: 9999999999,
	})

	if err := h.HandleTextEvent(ctx, "token-1", "user-1", "幫我查一下王大明"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := stateStore.Get(ctx, "user-1"); ok {
		t.Errorf("expected pending_backside_confirm state to be cleared on text interrupt")
	}
	if qe.called != 1 || qe.lastMsg != "幫我查一下王大明" {
		t.Errorf("expected SmartQueryEngine to be called with '幫我查一下王大明'")
	}
}

func TestHandleSendEmailPostback(t *testing.T) {
	ctx := context.Background()
	line := &fakeLineMessenger{}
	repo := storage.NewStoreRepository(nil, nil, nil, "")
	cid := repo.AddNamecard(ctx, models.Namecard{
		CardID: "card-1",
		Name:   "王大明",
		Email:  "david@example.com",
	}, "user-1")
	stateStore := internalmem.NewUserStateStore(nil)
	h := NewBotHandler(line, repo, stateStore, nil, nil)

	if err := h.HandlePostbackEvent(ctx, "token-1", "user-1", "action=send_email&card_id="+cid); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(line.lastMsgs) != 2 || line.lastMsgs[1].Text != "david@example.com" {
		t.Errorf("expected second message to be raw email 'david@example.com', got %+v", line.lastMsgs)
	}
}

func TestHandleSmartQuery_DisambiguationListAndPendingUpdateConfirm(t *testing.T) {
	ctx := context.Background()
	line := &fakeLineMessenger{}
	repo := storage.NewStoreRepository(nil, nil, nil, "")

	var ids []string
	for i := 1; i <= 5; i++ {
		cid := repo.AddNamecard(ctx, models.Namecard{
			Name:    "王大明",
			Company: "Google",
			Title:   "Engineer",
		}, "user-1")
		ids = append(ids, cid)
	}

	stateStore := internalmem.NewUserStateStore(nil)
	qe := &fakeQueryEngine{
		result: &agent.SmartQueryResult{
			ReplyText:    "找到 5 筆名片",
			FoundCardIDs: ids,
		},
	}
	h := NewBotHandler(line, repo, stateStore, nil, qe)

	// >4 cards should return 1 text + 1 disambiguation list Flex Message
	if err := h.HandleSmartQuery(ctx, "token-list", "user-1", "找 Google"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(line.lastMsgs) != 2 || line.lastMsgs[1].Flex == nil || line.lastMsgs[1].Flex.AltText != "🔍 找到多個相符的名片" {
		t.Errorf("expected disambiguation list flex message, got %+v", line.lastMsgs)
	}

	// Simulate ADK tool setting pending_update state
	stateStore.Set(ctx, "user-1", models.UserState{
		Action:     models.ActionPendingUpdate,
		UpdateType: "field",
		CardID:     ids[0],
		Field:      "title",
		Value:      "Director",
	})
	qe.result = &agent.SmartQueryResult{ReplyText: "請確認修改"}
	if err := h.HandleSmartQuery(ctx, "token-confirm", "user-1", "把職稱改為 Director"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(line.lastMsgs) != 2 || line.lastMsgs[1].Flex == nil || line.lastMsgs[1].Flex.AltText != "確認修改資料" {
		t.Errorf("expected confirmation flex message, got %+v", line.lastMsgs)
	}

	// User clicks confirm_update
	if err := h.HandlePostbackEvent(ctx, "token-do-confirm", "user-1", "action=confirm_update"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	updated := repo.GetCardByID(ctx, "user-1", ids[0])
	if updated == nil || updated.Title != "Director" {
		t.Errorf("expected title to be updated to 'Director', got %+v", updated)
	}
}

