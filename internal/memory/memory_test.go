package memory

import (
	"context"
	"sync"
	"testing"

	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/kkdai/linebot-namecard-go/internal/models"
)

// fakeDocStore is an in-memory fake simulating Cloud Firestore documents to verify persistence.
type fakeDocStore struct {
	mu   sync.Mutex
	docs map[string]map[string]map[string]any
}

func newFakeDocStore() *fakeDocStore {
	return &fakeDocStore{
		docs: make(map[string]map[string]map[string]any),
	}
}

func (f *fakeDocStore) SaveDoc(_ context.Context, collection, docID string, data map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.docs[collection] == nil {
		f.docs[collection] = make(map[string]map[string]any)
	}
	f.docs[collection][docID] = data
	return nil
}

func (f *fakeDocStore) LoadDoc(_ context.Context, collection, docID string) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.docs[collection] == nil {
		return nil, nil
	}
	return f.docs[collection][docID], nil
}

func (f *fakeDocStore) DeleteDoc(_ context.Context, collection, docID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.docs[collection] != nil {
		delete(f.docs[collection], docID)
	}
	return nil
}

func (f *fakeDocStore) ListDocs(_ context.Context, collection string) (map[string]map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.docs[collection], nil
}

func TestUserStateStore_SharedMemoryAndFirestorePersistence(t *testing.T) {
	ctx := context.Background()
	firestoreBacking := newFakeDocStore()

	// Instance 1 writes user state
	store1 := NewUserStateStore(firestoreBacking)
	store1.Set(ctx, "user-1", models.UserState{
		Action:    models.ActionPendingBacksideConfirm,
		CardID:    "card-abc",
		ExpiresAt: 1000,
	})

	// Instance 2 (simulating another Cloud Run instance or restart) reads from Firestore
	store2 := NewUserStateStore(firestoreBacking)
	st, ok := store2.Get(ctx, "user-1")
	if !ok {
		t.Fatalf("expected user-1 state to be hydrated from Firestore")
	}
	if st.Action != models.ActionPendingBacksideConfirm || st.CardID != "card-abc" {
		t.Errorf("unexpected hydrated state: %+v", st)
	}

	// Sweep expired states
	store2.SweepExpiredStates(ctx, 2000)
	if _, ok := store2.Get(ctx, "user-1"); ok {
		t.Errorf("expected expired state to be swept from both memory and Firestore")
	}
}

func TestFirestoreSessionService_PersistenceAndHydration(t *testing.T) {
	ctx := context.Background()
	firestoreBacking := newFakeDocStore()

	svc1 := NewFirestoreSessionService(firestoreBacking)
	createResp, err := svc1.Create(ctx, &session.CreateRequest{
		AppName:   "namecard_bot_app",
		UserID:    "user-1",
		SessionID: "user-1",
		State: map[string]any{
			"last_card_id": "card-999",
		},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ev := session.NewEvent("inv-1")
	ev.Author = "user"
	ev.Content = genai.NewContentFromText("找王大明", genai.RoleUser)
	if err := svc1.AppendEvent(ctx, createResp.Session, ev); err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}

	// Create a new service instance (cold start) backed by the same Firestore store
	svc2 := NewFirestoreSessionService(firestoreBacking)
	getResp, err := svc2.Get(ctx, &session.GetRequest{
		AppName:   "namecard_bot_app",
		UserID:    "user-1",
		SessionID: "user-1",
	})
	if err != nil {
		t.Fatalf("expected session to hydrate from Firestore on cold start, got error: %v", err)
	}
	val, err := getResp.Session.State().Get("last_card_id")
	if err != nil || val != "card-999" {
		t.Errorf("expected state last_card_id='card-999', got %v (err=%v)", val, err)
	}
	if getResp.Session.Events().Len() != 1 {
		t.Errorf("expected 1 hydrated event, got %d", getResp.Session.Events().Len())
	}
}
