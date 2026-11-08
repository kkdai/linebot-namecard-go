package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kkdai/linebot-namecard-go/internal/models"
)

func TestStoreRepository_CRUDAndStatistics(t *testing.T) {
	ctx := context.Background()
	repo := NewStoreRepository(nil, nil, nil, "test-bucket")

	card1 := models.Namecard{
		Name:      "王大明",
		Title:     "工程師",
		Company:   "Google",
		Email:     "daming@google.com",
		Phone:     "#886-02-1234-5678",
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	cid1 := repo.AddNamecard(ctx, card1, "user-1")
	if cid1 == "" {
		t.Fatalf("expected non-empty cardID")
	}

	// Duplicate detection by email
	if existingID := repo.CheckIfCardExists(ctx, card1, "user-1"); existingID != cid1 {
		t.Errorf("expected existing card ID %q, got %q", cid1, existingID)
	}

	// Update field & memo
	if !repo.UpdateNamecardField(ctx, "user-1", cid1, "title", "資深工程師") {
		t.Errorf("expected UpdateNamecardField to succeed")
	}
	if !repo.UpdateNamecardMemo(ctx, cid1, "user-1", "在 GDG 活動認識") {
		t.Errorf("expected UpdateNamecardMemo to succeed")
	}

	updated := repo.GetCardByID(ctx, "user-1", cid1)
	if updated == nil || updated.Title != "資深工程師" || updated.Memo != "在 GDG 活動認識" {
		t.Errorf("unexpected updated card: %+v", updated)
	}

	// Add duplicate email card and test RemoveRedundantData
	repo.AddNamecard(ctx, card1, "user-1")
	if len(repo.GetAllCards(ctx, "user-1")) != 2 {
		t.Fatalf("expected 2 cards before deduplication")
	}
	repo.RemoveRedundantData(ctx, "user-1")
	if len(repo.GetAllCards(ctx, "user-1")) != 1 {
		t.Errorf("expected 1 card after RemoveRedundantData, got %d", len(repo.GetAllCards(ctx, "user-1")))
	}

	stats := repo.GetNamecardStatistics(ctx, "user-1")
	if stats.Total != 1 || stats.ThisMonth != 1 || !strings.Contains(stats.TopCompany, "Google") {
		t.Errorf("unexpected statistics: %+v", stats)
	}
}
