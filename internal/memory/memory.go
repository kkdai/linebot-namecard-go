package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"

	"github.com/kkdai/linebot-namecard-go/internal/config"
	"github.com/kkdai/linebot-namecard-go/internal/models"
)

// DocumentStore abstracts document persistence (implemented by Firestore and by test fakes).
type DocumentStore interface {
	SaveDoc(ctx context.Context, collection, docID string, data map[string]any) error
	LoadDoc(ctx context.Context, collection, docID string) (map[string]any, error)
	DeleteDoc(ctx context.Context, collection, docID string) error
	ListDocs(ctx context.Context, collection string) (map[string]map[string]any, error)
}

// FirestoreDocStore implements DocumentStore backed by Google Cloud Firestore.
type FirestoreDocStore struct {
	Client *firestore.Client
}

func NewFirestoreDocStore(client *firestore.Client) *FirestoreDocStore {
	if client == nil {
		return nil
	}
	return &FirestoreDocStore{Client: client}
}

func (f *FirestoreDocStore) SaveDoc(ctx context.Context, collection, docID string, data map[string]any) error {
	if f == nil || f.Client == nil {
		return nil
	}
	_, err := f.Client.Collection(collection).Doc(docID).Set(ctx, data)
	return err
}

func (f *FirestoreDocStore) LoadDoc(ctx context.Context, collection, docID string) (map[string]any, error) {
	if f == nil || f.Client == nil {
		return nil, nil
	}
	snap, err := f.Client.Collection(collection).Doc(docID).Get(ctx)
	if err != nil {
		return nil, err
	}
	return snap.Data(), nil
}

func (f *FirestoreDocStore) DeleteDoc(ctx context.Context, collection, docID string) error {
	if f == nil || f.Client == nil {
		return nil
	}
	_, err := f.Client.Collection(collection).Doc(docID).Delete(ctx)
	return err
}

func (f *FirestoreDocStore) ListDocs(ctx context.Context, collection string) (map[string]map[string]any, error) {
	if f == nil || f.Client == nil {
		return nil, nil
	}
	snaps, err := f.Client.Collection(collection).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	res := make(map[string]map[string]any, len(snaps))
	for _, s := range snaps {
		res[s.Ref.ID] = s.Data()
	}
	return res, nil
}

// ============================================================================
// 1. Shared UserStateStore (Shared In-Memory Cache + Firestore Persistence)
// ============================================================================

// UserStateStore manages per-user multi-step interaction states (e.g., pending backside confirm,
// awaiting backside image, adding memo, editing field, pending update confirmation)
// in shared memory and persists them to Firestore.
type UserStateStore struct {
	mu    sync.RWMutex
	cache map[string]models.UserState
	store DocumentStore
}

// NewUserStateStore creates a shared UserStateStore backed by memory and optional Firestore DocumentStore.
func NewUserStateStore(store DocumentStore) *UserStateStore {
	return &UserStateStore{
		cache: make(map[string]models.UserState),
		store: store,
	}
}

// Get retrieves the current state for userID from shared memory, falling back to Firestore if not cached.
func (s *UserStateStore) Get(ctx context.Context, userID string) (models.UserState, bool) {
	s.mu.RLock()
	st, ok := s.cache[userID]
	s.mu.RUnlock()
	if ok {
		return st, true
	}

	if s.store != nil {
		data, err := s.store.LoadDoc(ctx, config.FirestoreUserStateCollection, userID)
		if err == nil && len(data) > 0 {
			if rawJSON, ok := data["payload_json"].(string); ok && rawJSON != "" {
				var loaded models.UserState
				if json.Unmarshal([]byte(rawJSON), &loaded) == nil && loaded.Action != "" {
					s.mu.Lock()
					s.cache[userID] = loaded
					s.mu.Unlock()
					return loaded, true
				}
			}
		}
	}
	return models.UserState{}, false
}

// Set updates the user's state in shared memory and persists it to Firestore.
func (s *UserStateStore) Set(ctx context.Context, userID string, state models.UserState) {
	s.mu.Lock()
	s.cache[userID] = state
	s.mu.Unlock()

	if s.store != nil {
		if rawBytes, err := json.Marshal(state); err == nil {
			_ = s.store.SaveDoc(ctx, config.FirestoreUserStateCollection, userID, map[string]any{
				"user_id":      userID,
				"action":       string(state.Action),
				"expires_at":   state.ExpiresAt,
				"updated_at":   time.Now().UTC().Format(time.RFC3339),
				"payload_json": string(rawBytes),
			})
		}
	}
}

// Delete removes the user's state from shared memory and Firestore.
func (s *UserStateStore) Delete(ctx context.Context, userID string) {
	s.mu.Lock()
	delete(s.cache, userID)
	s.mu.Unlock()

	if s.store != nil {
		_ = s.store.DeleteDoc(ctx, config.FirestoreUserStateCollection, userID)
	}
}

// ClearAll clears all states in memory (used in testing).
func (s *UserStateStore) ClearAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = make(map[string]models.UserState)
}

// SweepExpiredStates removes all expired states (ExpiresAt > 0 && ExpiresAt <= now) from shared memory and Firestore.
func (s *UserStateStore) SweepExpiredStates(ctx context.Context, now float64) {
	var expiredUsers []string

	s.mu.Lock()
	for uid, st := range s.cache {
		if st.ExpiresAt > 0 && st.ExpiresAt <= now {
			delete(s.cache, uid)
			expiredUsers = append(expiredUsers, uid)
		}
	}
	s.mu.Unlock()

	if s.store != nil {
		for _, uid := range expiredUsers {
			_ = s.store.DeleteDoc(ctx, config.FirestoreUserStateCollection, uid)
		}
	}
}

// ============================================================================
// 2. Shared FirestoreSessionService (implements google.golang.org/adk/session.Service)
// ============================================================================

type persistedSessionDoc struct {
	AppName   string           `json:"app_name"`
	UserID    string           `json:"user_id"`
	SessionID string           `json:"session_id"`
	State     map[string]any   `json:"state"`
	Events    []*session.Event `json:"events"`
	UpdatedAt time.Time        `json:"updated_at"`
}

// FirestoreSessionService implements ADK's session.Service using a shared in-memory session service
// synchronized with Cloud Firestore so conversation sessions and state survive restarts and scale across instances.
type FirestoreSessionService struct {
	mu       sync.Mutex
	inMemory session.Service
	store    DocumentStore
}

// NewFirestoreSessionService creates a shared ADK session.Service backed by in-memory state + Firestore.
func NewFirestoreSessionService(store DocumentStore) *FirestoreSessionService {
	return &FirestoreSessionService{
		inMemory: session.InMemoryService(),
		store:    store,
	}
}

func sessionDocKey(appName, userID, sessionID string) string {
	return fmt.Sprintf("%s__%s__%s", appName, userID, sessionID)
}

func (s *FirestoreSessionService) persistSession(ctx context.Context, sess session.Session) {
	if s.store == nil || sess == nil {
		return
	}
	stateMap := make(map[string]any)
	if sess.State() != nil {
		for k, v := range sess.State().All() {
			stateMap[k] = v
		}
	}
	var events []*session.Event
	if sess.Events() != nil {
		for ev := range sess.Events().All() {
			events = append(events, ev)
		}
	}
	doc := persistedSessionDoc{
		AppName:   sess.AppName(),
		UserID:    sess.UserID(),
		SessionID: sess.ID(),
		State:     stateMap,
		Events:    events,
		UpdatedAt: time.Now().UTC(),
	}
	rawBytes, err := json.Marshal(doc)
	if err != nil {
		return
	}
	docID := sessionDocKey(sess.AppName(), sess.UserID(), sess.ID())
	_ = s.store.SaveDoc(ctx, config.FirestoreADKSessionCollection, docID, map[string]any{
		"app_name":     sess.AppName(),
		"user_id":      sess.UserID(),
		"session_id":   sess.ID(),
		"updated_at":   doc.UpdatedAt.Format(time.RFC3339),
		"payload_json": string(rawBytes),
	})
}

func (s *FirestoreSessionService) hydrateFromFirestoreIfNeeded(ctx context.Context, appName, userID, sessionID string) {
	if s.store == nil {
		return
	}
	docID := sessionDocKey(appName, userID, sessionID)
	data, err := s.store.LoadDoc(ctx, config.FirestoreADKSessionCollection, docID)
	if err != nil || len(data) == 0 {
		return
	}
	rawJSON, ok := data["payload_json"].(string)
	if !ok || rawJSON == "" {
		return
	}
	var persisted persistedSessionDoc
	if err := json.Unmarshal([]byte(rawJSON), &persisted); err != nil {
		return
	}

	createResp, err := s.inMemory.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		State:     persisted.State,
	})
	if err != nil || createResp == nil || createResp.Session == nil {
		return
	}
	for _, ev := range persisted.Events {
		if ev != nil {
			_ = s.inMemory.AppendEvent(ctx, createResp.Session, ev)
		}
	}
}

func (s *FirestoreSessionService) Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	resp, err := s.inMemory.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	s.persistSession(ctx, resp.Session)
	return resp, nil
}

func (s *FirestoreSessionService) Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	resp, err := s.inMemory.Get(ctx, req)
	if err == nil {
		return resp, nil
	}
	// Try hydrating from Firestore
	s.hydrateFromFirestoreIfNeeded(ctx, req.AppName, req.UserID, req.SessionID)
	return s.inMemory.Get(ctx, req)
}

func (s *FirestoreSessionService) List(ctx context.Context, req *session.ListRequest) (*session.ListResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inMemory.List(ctx, req)
}

func (s *FirestoreSessionService) Delete(ctx context.Context, req *session.DeleteRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.inMemory.Delete(ctx, req)
	if s.store != nil {
		docID := sessionDocKey(req.AppName, req.UserID, req.SessionID)
		_ = s.store.DeleteDoc(ctx, config.FirestoreADKSessionCollection, docID)
	}
	return err
}

func (s *FirestoreSessionService) AppendEvent(ctx context.Context, sess session.Session, ev *session.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.inMemory.AppendEvent(ctx, sess, ev); err != nil {
		return err
	}
	// Reload updated session from in-memory service and persist to Firestore
	if updated, err := s.inMemory.Get(ctx, &session.GetRequest{
		AppName:   sess.AppName(),
		UserID:    sess.UserID(),
		SessionID: sess.ID(),
	}); err == nil && updated != nil {
		s.persistSession(ctx, updated.Session)
	}
	return nil
}

// ============================================================================
// 3. Shared FirestoreMemoryService (implements google.golang.org/adk/memory.Service)
// ============================================================================

// FirestoreMemoryService implements ADK's memory.Service using a shared in-memory service
// backed by Cloud Firestore for long-term cross-session memory persistence.
type FirestoreMemoryService struct {
	mu       sync.Mutex
	inMemory memory.Service
	store    DocumentStore
}

// NewFirestoreMemoryService creates a shared ADK memory.Service backed by in-memory search + Firestore.
func NewFirestoreMemoryService(store DocumentStore) *FirestoreMemoryService {
	return &FirestoreMemoryService{
		inMemory: memory.InMemoryService(),
		store:    store,
	}
}

func (m *FirestoreMemoryService) AddSessionToMemory(ctx context.Context, sess session.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	err := m.inMemory.AddSessionToMemory(ctx, sess)
	if m.store != nil && sess != nil {
		var texts []string
		if sess.Events() != nil {
			for ev := range sess.Events().All() {
				if ev != nil && ev.Content != nil {
					for _, p := range ev.Content.Parts {
						if p != nil && p.Text != "" {
							texts = append(texts, p.Text)
						}
					}
				}
			}
		}
		docID := sessionDocKey(sess.AppName(), sess.UserID(), sess.ID())
		_ = m.store.SaveDoc(ctx, config.FirestoreADKMemoryCollection, docID, map[string]any{
			"app_name":   sess.AppName(),
			"user_id":    sess.UserID(),
			"session_id": sess.ID(),
			"summary":    strings.Join(texts, "\n"),
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		})
	}
	return err
}

func (m *FirestoreMemoryService) SearchMemory(ctx context.Context, req *memory.SearchRequest) (*memory.SearchResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inMemory.SearchMemory(ctx, req)
}
