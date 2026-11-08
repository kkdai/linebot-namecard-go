package storage

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	gcs "cloud.google.com/go/storage"
	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/db"
	"github.com/google/uuid"
	"google.golang.org/api/option"

	"github.com/kkdai/linebot-namecard-go/internal/config"
	"github.com/kkdai/linebot-namecard-go/internal/models"
)

// CardRepository defines all namecard data and QR storage operations.
type CardRepository interface {
	GetAllCards(ctx context.Context, userID string) map[string]models.Namecard
	GetCardByID(ctx context.Context, userID, cardID string) *models.Namecard
	GetNameFromCard(ctx context.Context, userID, cardID string) string
	AddNamecard(ctx context.Context, card models.Namecard, userID string) string
	UpdateNamecardMemo(ctx context.Context, cardID, userID, memo string) bool
	UpdateNamecardField(ctx context.Context, userID, cardID, field, value string) bool
	CheckIfCardExists(ctx context.Context, card models.Namecard, userID string) string
	RemoveRedundantData(ctx context.Context, userID string)
	GetNamecardStatistics(ctx context.Context, userID string) models.Statistics
	UploadQRCodeToStorage(ctx context.Context, pngBytes []byte, userID, cardID string) string
}

// StoreRepository implements CardRepository using Shared Memory + Cloud Firestore (and optional Firebase RTDB + Cloud Storage).
type StoreRepository struct {
	mu            sync.RWMutex
	memCards      map[string]map[string]models.Namecard
	firestoreCli  *firestore.Client
	rtdbCli       *db.Client
	storageBucket *gcs.BucketHandle
	bucketName    string
	UploadQRFunc  func(ctx context.Context, pngBytes []byte, userID, cardID string) string
}

// NewStoreRepository creates a StoreRepository. If cloud clients are nil, it operates in shared-memory mode.
func NewStoreRepository(fsClient *firestore.Client, rtdbClient *db.Client, bucket *gcs.BucketHandle, bucketName string) *StoreRepository {
	return &StoreRepository{
		memCards:      make(map[string]map[string]models.Namecard),
		firestoreCli:  fsClient,
		rtdbCli:       rtdbClient,
		storageBucket: bucket,
		bucketName:    bucketName,
	}
}

// InitCloudClients initializes Firestore, Firebase RTDB (if FIREBASE_URL is set), and Cloud Storage Bucket.
func InitCloudClients(ctx context.Context, cfg *config.Config) (*firestore.Client, *db.Client, *gcs.BucketHandle, error) {
	var opts []option.ClientOption
	if cfg.CredentialsJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(cfg.CredentialsJSON)))
	}

	var fsClient *firestore.Client
	var err error
	if cfg.FirestoreDatabaseID != "" && cfg.FirestoreDatabaseID != "(default)" {
		fsClient, err = firestore.NewClientWithDatabase(ctx, cfg.ProjectID, cfg.FirestoreDatabaseID, opts...)
	} else {
		fsClient, err = firestore.NewClient(ctx, cfg.ProjectID, opts...)
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to init firestore client: %w", err)
	}

	fbConfig := &firebase.Config{
		ProjectID:     cfg.ProjectID,
		DatabaseURL:   cfg.FirebaseURL,
		StorageBucket: cfg.FirebaseStorageBucket,
	}
	app, err := firebase.NewApp(ctx, fbConfig, opts...)
	if err != nil {
		return fsClient, nil, nil, nil
	}

	var rtdbClient *db.Client
	if cfg.FirebaseURL != "" {
		rtdbClient, _ = app.Database(ctx)
	}

	var bucketHandle *gcs.BucketHandle
	if cfg.FirebaseStorageBucket != "" {
		if stClient, err := app.Storage(ctx); err == nil {
			bucketHandle, _ = stClient.DefaultBucket()
		}
	}

	return fsClient, rtdbClient, bucketHandle, nil
}

func (r *StoreRepository) GetAllCards(ctx context.Context, userID string) map[string]models.Namecard {
	// 1. Try Firebase RTDB if configured (preserves backward compatibility with existing Python data)
	if r.rtdbCli != nil {
		ref := r.rtdbCli.NewRef(fmt.Sprintf("%s/%s", config.NamecardPath, userID))
		var raw map[string]map[string]any
		if err := ref.Get(ctx, &raw); err == nil && len(raw) > 0 {
			res := make(map[string]models.Namecard, len(raw))
			for cid, data := range raw {
				card := models.NamecardFromMap(data)
				card.CardID = cid
				res[cid] = card
			}
			r.cacheUserCards(userID, res)
			return res
		}
	}

	// 2. Try Cloud Firestore
	if r.firestoreCli != nil {
		col := r.firestoreCli.Collection(config.FirestoreNamecardCollection).Doc(userID).Collection("cards")
		snaps, err := col.Documents(ctx).GetAll()
		if err == nil && len(snaps) > 0 {
			res := make(map[string]models.Namecard, len(snaps))
			for _, s := range snaps {
				card := models.NamecardFromMap(s.Data())
				card.CardID = s.Ref.ID
				res[s.Ref.ID] = card
			}
			r.cacheUserCards(userID, res)
			return res
		}
	}

	// 3. Fallback to shared in-memory cache
	r.mu.RLock()
	defer r.mu.RUnlock()
	userMap := r.memCards[userID]
	if userMap == nil {
		return map[string]models.Namecard{}
	}
	copied := make(map[string]models.Namecard, len(userMap))
	for k, v := range userMap {
		copied[k] = v
	}
	return copied
}

func (r *StoreRepository) cacheUserCards(userID string, cards map[string]models.Namecard) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.memCards[userID] = make(map[string]models.Namecard, len(cards))
	for k, v := range cards {
		r.memCards[userID][k] = v
	}
}

func (r *StoreRepository) GetCardByID(ctx context.Context, userID, cardID string) *models.Namecard {
	if cardID == "" {
		return nil
	}
	all := r.GetAllCards(ctx, userID)
	if c, ok := all[cardID]; ok {
		copied := c
		return &copied
	}
	return nil
}

func (r *StoreRepository) GetNameFromCard(ctx context.Context, userID, cardID string) string {
	c := r.GetCardByID(ctx, userID, cardID)
	if c == nil {
		return ""
	}
	if strings.TrimSpace(c.Name) == "" {
		return "這位聯絡人"
	}
	return c.Name
}

func (r *StoreRepository) AddNamecard(ctx context.Context, card models.Namecard, userID string) string {
	if card.CreatedAt == "" {
		card.CreatedAt = time.Now().Format(time.RFC3339)
	}
	cardID := card.CardID
	if cardID == "" {
		cardID = "-" + strings.ReplaceAll(uuid.NewString()[:16], "-", "")
	}
	card.CardID = cardID

	// Save to RTDB if configured
	if r.rtdbCli != nil {
		ref := r.rtdbCli.NewRef(fmt.Sprintf("%s/%s", config.NamecardPath, userID))
		if pushRef, err := ref.Push(ctx, card.ToMap()); err == nil && pushRef.Key != "" {
			cardID = pushRef.Key
			card.CardID = cardID
		}
	}

	// Save to Firestore if configured
	if r.firestoreCli != nil {
		docRef := r.firestoreCli.Collection(config.FirestoreNamecardCollection).Doc(userID).Collection("cards").Doc(cardID)
		_, _ = docRef.Set(ctx, card.ToMap())
	}

	// Save to shared memory
	r.mu.Lock()
	if r.memCards[userID] == nil {
		r.memCards[userID] = make(map[string]models.Namecard)
	}
	r.memCards[userID][cardID] = card
	r.mu.Unlock()

	return cardID
}

func (r *StoreRepository) UpdateNamecardMemo(ctx context.Context, cardID, userID, memo string) bool {
	return r.UpdateNamecardField(ctx, userID, cardID, "memo", memo)
}

func (r *StoreRepository) UpdateNamecardField(ctx context.Context, userID, cardID, field, value string) bool {
	card := r.GetCardByID(ctx, userID, cardID)
	if card == nil {
		return false
	}
	switch strings.ToLower(field) {
	case "name":
		card.Name = value
	case "title":
		card.Title = value
	case "company":
		card.Company = value
	case "address":
		card.Address = value
	case "phone":
		card.Phone = value
	case "email":
		card.Email = value
	case "memo":
		card.Memo = value
	default:
		return false
	}

	if r.rtdbCli != nil {
		ref := r.rtdbCli.NewRef(fmt.Sprintf("%s/%s/%s", config.NamecardPath, userID, cardID))
		_ = ref.Update(ctx, map[string]any{field: value})
	}
	if r.firestoreCli != nil {
		docRef := r.firestoreCli.Collection(config.FirestoreNamecardCollection).Doc(userID).Collection("cards").Doc(cardID)
		_, _ = docRef.Set(ctx, card.ToMap())
	}

	r.mu.Lock()
	if r.memCards[userID] == nil {
		r.memCards[userID] = make(map[string]models.Namecard)
	}
	r.memCards[userID][cardID] = *card
	r.mu.Unlock()
	return true
}

func (r *StoreRepository) CheckIfCardExists(ctx context.Context, card models.Namecard, userID string) string {
	email := strings.TrimSpace(card.Email)
	if email == "" || strings.EqualFold(email, "N/A") {
		return ""
	}
	all := r.GetAllCards(ctx, userID)
	for cid, existing := range all {
		if existing.Email == email {
			return cid
		}
	}
	return ""
}

func (r *StoreRepository) RemoveRedundantData(ctx context.Context, userID string) {
	all := r.GetAllCards(ctx, userID)
	emailMap := make(map[string]string)

	for cid, card := range all {
		email := strings.TrimSpace(card.Email)
		if email == "" {
			continue
		}
		if _, exists := emailMap[email]; exists {
			// Delete duplicate
			if r.rtdbCli != nil {
				ref := r.rtdbCli.NewRef(fmt.Sprintf("%s/%s/%s", config.NamecardPath, userID, cid))
				_ = ref.Delete(ctx)
			}
			if r.firestoreCli != nil {
				docRef := r.firestoreCli.Collection(config.FirestoreNamecardCollection).Doc(userID).Collection("cards").Doc(cid)
				_, _ = docRef.Delete(ctx)
			}
			r.mu.Lock()
			if r.memCards[userID] != nil {
				delete(r.memCards[userID], cid)
			}
			r.mu.Unlock()
		} else {
			emailMap[email] = cid
		}
	}
}

func (r *StoreRepository) GetNamecardStatistics(ctx context.Context, userID string) models.Statistics {
	all := r.GetAllCards(ctx, userID)
	if len(all) == 0 {
		return models.Statistics{
			Total:      0,
			ThisMonth:  0,
			TopCompany: "無",
		}
	}

	total := len(all)
	now := time.Now()
	thisMonthCount := 0
	companyCounts := make(map[string]int)

	for _, card := range all {
		if card.CreatedAt != "" {
			if t, err := parseISO(card.CreatedAt); err == nil {
				if t.Year() == now.Year() && t.Month() == now.Month() {
					thisMonthCount++
				}
			}
		}
		comp := strings.TrimSpace(card.Company)
		if comp != "" && !strings.EqualFold(comp, "N/A") {
			companyCounts[comp]++
		}
	}

	topCompany := "無"
	maxCount := 0
	for comp, count := range companyCounts {
		if count > maxCount {
			maxCount = count
			topCompany = fmt.Sprintf("%s (%d張)", comp, count)
		}
	}

	return models.Statistics{
		Total:      total,
		ThisMonth:  thisMonthCount,
		TopCompany: topCompany,
	}
}

func parseISO(s string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time: %s", s)
}

func (r *StoreRepository) UploadQRCodeToStorage(ctx context.Context, pngBytes []byte, userID, cardID string) string {
	if r.UploadQRFunc != nil {
		return r.UploadQRFunc(ctx, pngBytes, userID, cardID)
	}
	if r.storageBucket == nil {
		return ""
	}

	blobName := fmt.Sprintf("qrcodes/%s/%s.png", userID, cardID)
	obj := r.storageBucket.Object(blobName)
	w := obj.NewWriter(ctx)
	w.ContentType = "image/png"
	if _, err := w.Write(pngBytes); err != nil {
		_ = w.Close()
		return ""
	}
	if err := w.Close(); err != nil {
		return ""
	}

	// Make publicly readable (matching blob.make_public() in Python)
	_ = obj.ACL().Set(ctx, gcs.AllUsers, gcs.RoleReader)

	return fmt.Sprintf("https://storage.googleapis.com/%s/%s", r.bucketName, blobName)
}
