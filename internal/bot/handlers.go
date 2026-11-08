package bot

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/line/line-bot-sdk-go/v8/linebot/messaging_api"

	"github.com/kkdai/linebot-namecard-go/internal/agent"
	"github.com/kkdai/linebot-namecard-go/internal/config"
	"github.com/kkdai/linebot-namecard-go/internal/flex"
	"github.com/kkdai/linebot-namecard-go/internal/gemini"
	internalmem "github.com/kkdai/linebot-namecard-go/internal/memory"
	"github.com/kkdai/linebot-namecard-go/internal/models"
	"github.com/kkdai/linebot-namecard-go/internal/qrcode"
	"github.com/kkdai/linebot-namecard-go/internal/storage"
)

// ReplyMessage represents a unified outbound message (Text, Image, or Flex) for LINE Messaging API and unit testing.
type ReplyMessage struct {
	Type       string                      `json:"type"` // "text", "image", "flex"
	Text       string                      `json:"text,omitempty"`
	ImageURL   string                      `json:"imageUrl,omitempty"`
	Flex       *flex.FlexPayload           `json:"flex,omitempty"`
	QuickReply *messaging_api.QuickReply   `json:"quickReply,omitempty"`
}

// LineMessenger abstracts LINE Messaging API calls for production and unit tests.
type LineMessenger interface {
	ReplyMessages(ctx context.Context, replyToken string, msgs []ReplyMessage) error
	GetMessageContent(ctx context.Context, messageID string) ([]byte, error)
}

// SDKLineMessenger implements LineMessenger using the official LINE Bot SDK v8.
type SDKLineMessenger struct {
	MessagingApi *messaging_api.MessagingApiAPI
	BlobApi      *messaging_api.MessagingApiBlobAPI
}

// NewSDKLineMessenger initializes the official LINE Messaging API and Blob API clients.
func NewSDKLineMessenger(channelAccessToken string) (*SDKLineMessenger, error) {
	msgAPI, err := messaging_api.NewMessagingApiAPI(channelAccessToken)
	if err != nil {
		return nil, err
	}
	blobAPI, err := messaging_api.NewMessagingApiBlobAPI(channelAccessToken)
	if err != nil {
		return nil, err
	}
	return &SDKLineMessenger{
		MessagingApi: msgAPI,
		BlobApi:      blobAPI,
	}, nil
}

func (m *SDKLineMessenger) ReplyMessages(_ context.Context, replyToken string, msgs []ReplyMessage) error {
	var sdkMsgs []messaging_api.MessageInterface
	for _, rm := range msgs {
		switch rm.Type {
		case "text":
			sdkMsgs = append(sdkMsgs, &messaging_api.TextMessage{
				Text:       rm.Text,
				QuickReply: rm.QuickReply,
			})
		case "image":
			sdkMsgs = append(sdkMsgs, &messaging_api.ImageMessage{
				OriginalContentUrl: rm.ImageURL,
				PreviewImageUrl:    rm.ImageURL,
				QuickReply:         rm.QuickReply,
			})
		case "flex":
			if rm.Flex != nil {
				fm, err := rm.Flex.ToLineFlexMessage(rm.QuickReply)
				if err != nil {
					return err
				}
				sdkMsgs = append(sdkMsgs, fm)
			}
		}
	}
	if len(sdkMsgs) == 0 {
		return nil
	}
	_, err := m.MessagingApi.ReplyMessage(&messaging_api.ReplyMessageRequest{
		ReplyToken: replyToken,
		Messages:   sdkMsgs,
	})
	return err
}

func (m *SDKLineMessenger) GetMessageContent(_ context.Context, messageID string) ([]byte, error) {
	resp, err := m.BlobApi.GetMessageContent(messageID)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// BotHandler coordinates LINE events, shared UserStateStore, Firestore-backed ADK Runner, Gemini Vision, and Storage.
type BotHandler struct {
	Line       LineMessenger
	Repo       storage.CardRepository
	StateStore *internalmem.UserStateStore
	Vision     gemini.VisionClient
	QueryAgent agent.SmartQueryEngine
	NowFunc    func() float64
}

// NewBotHandler constructs a BotHandler with all dependencies.
func NewBotHandler(
	line LineMessenger,
	repo storage.CardRepository,
	stateStore *internalmem.UserStateStore,
	vision gemini.VisionClient,
	queryAgent agent.SmartQueryEngine,
) *BotHandler {
	return &BotHandler{
		Line:       line,
		Repo:       repo,
		StateStore: stateStore,
		Vision:     vision,
		QueryAgent: queryAgent,
		NowFunc: func() float64 {
			return float64(time.Now().UnixNano()) / 1e9
		},
	}
}

func (h *BotHandler) now() float64 {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return float64(time.Now().UnixNano()) / 1e9
}

// SweepExpiredStates cleans up expired user states in shared memory and Firestore.
func (h *BotHandler) SweepExpiredStates(ctx context.Context) {
	h.StateStore.SweepExpiredStates(ctx, h.now())
}

// HandlePostbackEvent processes all LINE PostbackEvent actions.
func (h *BotHandler) HandlePostbackEvent(ctx context.Context, replyToken, userID, rawData string) error {
	values, _ := url.ParseQuery(rawData)
	action := values.Get("action")
	cardID := values.Get("card_id")

	switch action {
	case "show_stats":
		stats := h.Repo.GetNamecardStatistics(ctx, userID)
		statsText := fmt.Sprintf("📊 名片統計資訊\n\n📇 總名片數：%d 張\n📅 本月新增：%d 張\n🏢 最常合作公司：%s",
			stats.Total, stats.ThisMonth, stats.TopCompany)
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{Type: "text", Text: statsText, QuickReply: flex.GetQuickReplyItems()},
		})

	case "show_list":
		allCards := h.Repo.GetAllCards(ctx, userID)
		listText := fmt.Sprintf("📋 總共有 %d 張名片資料。", len(allCards))
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{Type: "text", Text: listText, QuickReply: flex.GetQuickReplyItems()},
		})

	case "show_test":
		sample := models.GenerateSampleNamecard()
		flexMsg := flex.GetNamecardFlexMsg(sample.ToMap(), "test_card_id")
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{Type: "flex", Flex: &flexMsg},
		})

	case "show_help":
		helpText := `ℹ️ 名片管理機器人使用說明

📸 上傳名片圖片 → 自動辨識並儲存
🔍 輸入文字 → 智能搜尋相關名片
📊 統計 → 查看名片統計資訊
📋 列表 → 顯示名片總數
🧪 測試 → 查看範例名片

💡 小提示：
• 點擊名片可以編輯、加入備註
• 使用「加入通訊錄」可下載 QR Code
• 📞 撥打：直接叫起手機撥號盤
• ✉️ Email：回傳 Email，可點擊寄信或長按複製
• 📤 分享：把名片摘要分享給朋友`
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{Type: "text", Text: helpText, QuickReply: flex.GetQuickReplyItems()},
		})

	case "confirm_update":
		st, ok := h.StateStore.Get(ctx, userID)
		if ok && st.Action == models.ActionPendingUpdate {
			cardName := h.Repo.GetNameFromCard(ctx, userID, st.CardID)
			if cardName == "" {
				cardName = "聯絡人"
			}
			success := false
			switch st.UpdateType {
			case "field":
				success = h.Repo.UpdateNamecardField(ctx, userID, st.CardID, st.Field, st.Value)
			case "memo":
				success = h.Repo.UpdateNamecardMemo(ctx, st.CardID, userID, st.Memo)
			}
			h.StateStore.Delete(ctx, userID)

			if success {
				replyMsgs := []ReplyMessage{
					{
						Type:       "text",
						Text:       fmt.Sprintf("「%s」的資料已成功更新！", cardName),
						QuickReply: flex.GetQuickReplyItems(),
					},
				}
				if updatedCard := h.Repo.GetCardByID(ctx, userID, st.CardID); updatedCard != nil {
					fm := flex.GetNamecardFlexMsg(updatedCard.ToMap(), st.CardID)
					replyMsgs = append(replyMsgs, ReplyMessage{Type: "flex", Flex: &fm})
				}
				return h.Line.ReplyMessages(ctx, replyToken, replyMsgs)
			}
			return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
				{
					Type:       "text",
					Text:       "更新資料時發生錯誤，請稍後再試。",
					QuickReply: flex.GetQuickReplyItems(),
				},
			})
		}
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{
				Type:       "text",
				Text:       "沒有待確認的更新操作，或操作已過期。",
				QuickReply: flex.GetQuickReplyItems(),
			},
		})

	case "cancel_update":
		h.StateStore.Delete(ctx, userID)
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{
				Type:       "text",
				Text:       "已取消修改操作。",
				QuickReply: flex.GetQuickReplyItems(),
			},
		})

	case "backside_confirm":
		hasBackside := values.Get("has_backside")
		st, ok := h.StateStore.Get(ctx, userID)
		isValid := ok && st.Action == models.ActionPendingBacksideConfirm && st.ExpiresAt > h.now()
		if !isValid {
			if ok && (st.Action == models.ActionPendingBacksideConfirm || st.Action == models.ActionAwaitingBacksideImage) {
				h.StateStore.Delete(ctx, userID)
			}
			return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
				{
					Type:       "text",
					Text:       "沒有待確認的名片資料，或操作已過期，請重新傳送名片圖片。",
					QuickReply: flex.GetQuickReplyItems(),
				},
			})
		}

		if hasBackside == "yes" {
			h.StateStore.Set(ctx, userID, models.UserState{
				Action:          models.ActionAwaitingBacksideImage,
				FrontImageBytes: st.FrontImageBytes,
				ExpiresAt:       h.now() + config.PendingBacksideTimeoutSeconds,
			})
			return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
				{Type: "text", Text: "請傳送背面照片 📸"},
			})
		}

		h.StateStore.Delete(ctx, userID)
		if st.CardObj == nil {
			return nil
		}
		return h.FinalizeAndSaveCard(ctx, *st.CardObj, replyToken, userID)
	}

	// Actions below require a valid card_id
	cardName := h.Repo.GetNameFromCard(ctx, userID, cardID)
	if cardName == "" {
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{Type: "text", Text: "找不到該名片資料。"},
		})
	}

	switch action {
	case "show_card":
		cardData := h.Repo.GetCardByID(ctx, userID, cardID)
		if cardData == nil {
			return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
				{Type: "text", Text: "找不到該名片資料。"},
			})
		}
		fm := flex.GetNamecardFlexMsg(cardData.ToMap(), cardID)
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{Type: "flex", Flex: &fm},
		})

	case "add_memo":
		h.StateStore.Set(ctx, userID, models.UserState{
			Action: models.ActionAddingMemo,
			CardID: cardID,
		})
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{Type: "text", Text: fmt.Sprintf("請輸入關於「%s」的備忘錄：", cardName)},
		})

	case "edit_card":
		fm := flex.GetEditOptionsFlexMsg(cardID, cardName)
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{Type: "flex", Flex: &fm},
		})

	case "edit_field":
		fieldToEdit := values.Get("field")
		fieldLabel, ok := config.FieldLabels[fieldToEdit]
		if !ok {
			fieldLabel = "資料"
		}
		h.StateStore.Set(ctx, userID, models.UserState{
			Action: models.ActionEditingField,
			CardID: cardID,
			Field:  fieldToEdit,
		})
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{Type: "text", Text: fmt.Sprintf("請輸入「%s」的新「%s」：", cardName, fieldLabel)},
		})

	case "send_email":
		return h.handleSendEmail(ctx, replyToken, userID, cardID, cardName)

	case "download_contact":
		return h.handleDownloadContact(ctx, replyToken, userID, cardID, cardName)
	}

	return nil
}

func (h *BotHandler) handleSendEmail(ctx context.Context, replyToken, userID, cardID, cardName string) error {
	cardData := h.Repo.GetCardByID(ctx, userID, cardID)
	if cardData == nil || !flex.IsValidEmail(cardData.Email) {
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{
				Type:       "text",
				Text:       fmt.Sprintf("「%s」沒有 Email 資料。", cardName),
				QuickReply: flex.GetQuickReplyItems(),
			},
		})
	}

	email := strings.TrimSpace(cardData.Email)
	return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
		{
			Type: "text",
			Text: fmt.Sprintf("✉️ 「%s」的 Email\n點擊下方地址可直接寄信，長按可複製：", cardName),
		},
		{
			Type:       "text",
			Text:       email,
			QuickReply: flex.GetQuickReplyItems(),
		},
	})
}

func (h *BotHandler) handleDownloadContact(ctx context.Context, replyToken, userID, cardID, cardName string) error {
	cardData := h.Repo.GetCardByID(ctx, userID, cardID)
	if cardData == nil {
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{Type: "text", Text: "找不到該名片資料。"},
		})
	}

	_, pngBytes, err := qrcode.GenerateVCardQRCode(cardData.ToMap())
	if err != nil {
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{Type: "text", Text: "處理您的請求時發生錯誤，請稍後再試。"},
		})
	}

	imageURL := h.Repo.UploadQRCodeToStorage(ctx, pngBytes, userID, cardID)
	if imageURL == "" {
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{Type: "text", Text: "生成 QR Code 時發生錯誤，請稍後再試。"},
		})
	}

	instruction := qrcode.GetQRCodeUsageInstruction(cardName)
	return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
		{Type: "image", ImageURL: imageURL},
		{Type: "text", Text: instruction},
	})
}

// HandleTextEvent processes incoming text messages and state transitions.
func (h *BotHandler) HandleTextEvent(ctx context.Context, replyToken, userID, msg string) error {
	st, hasState := h.StateStore.Get(ctx, userID)
	var userAction models.UserActionType
	if hasState {
		userAction = st.Action
	}

	// Clear pending backside state if interrupted by text
	if userAction == models.ActionPendingBacksideConfirm || userAction == models.ActionAwaitingBacksideImage {
		h.StateStore.Delete(ctx, userID)
		userAction = ""
	}

	switch {
	case userAction == models.ActionAddingMemo:
		return h.handleAddMemoState(ctx, replyToken, userID, st, msg)
	case userAction == models.ActionEditingField:
		return h.handleEditFieldState(ctx, replyToken, userID, st, msg)
	case msg == "remove":
		h.Repo.RemoveRedundantData(ctx, userID)
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{
				Type:       "text",
				Text:       "Redundant data removal complete.",
				QuickReply: flex.GetQuickReplyItems(),
			},
		})
	default:
		return h.HandleSmartQuery(ctx, replyToken, userID, msg)
	}
}

func (h *BotHandler) handleAddMemoState(ctx context.Context, replyToken, userID string, st models.UserState, msg string) error {
	defer h.StateStore.Delete(ctx, userID)
	if h.Repo.UpdateNamecardMemo(ctx, st.CardID, userID, msg) {
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{
				Type:       "text",
				Text:       "備忘錄已成功更新！",
				QuickReply: flex.GetQuickReplyItems(),
			},
		})
	}
	return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
		{
			Type:       "text",
			Text:       "新增備忘錄時發生錯誤，請稍後再試。",
			QuickReply: flex.GetQuickReplyItems(),
		},
	})
}

func (h *BotHandler) handleEditFieldState(ctx context.Context, replyToken, userID string, st models.UserState, msg string) error {
	defer h.StateStore.Delete(ctx, userID)
	if h.Repo.UpdateNamecardField(ctx, userID, st.CardID, st.Field, msg) {
		if updatedCard := h.Repo.GetCardByID(ctx, userID, st.CardID); updatedCard != nil {
			fm := flex.GetNamecardFlexMsg(updatedCard.ToMap(), st.CardID)
			return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
				{
					Type:       "text",
					Text:       "資料已成功更新！",
					QuickReply: flex.GetQuickReplyItems(),
				},
				{Type: "flex", Flex: &fm},
			})
		}
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{
				Type:       "text",
				Text:       "資料更新成功，但無法立即顯示。",
				QuickReply: flex.GetQuickReplyItems(),
			},
		})
	}
	return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
		{
			Type:       "text",
			Text:       "更新資料時發生錯誤，請稍後再試。",
			QuickReply: flex.GetQuickReplyItems(),
		},
	})
}

// HandleSmartQuery executes the ADK Agent query, handles secondary update confirmations,
// disambiguates >4 results into a list, and falls back to keyword search on error.
func (h *BotHandler) HandleSmartQuery(ctx context.Context, replyToken, userID, msg string) error {
	if h.QueryAgent == nil {
		return h.runFallbackSearch(ctx, replyToken, userID, msg)
	}

	res, err := h.QueryAgent.RunQuery(ctx, userID, msg)
	if err != nil {
		return h.runFallbackSearch(ctx, replyToken, userID, msg)
	}

	finalText := strings.TrimSpace(res.ReplyText)
	if finalText == "" {
		finalText = "為您完成處理。"
	}

	replyMsgs := []ReplyMessage{
		{
			Type:       "text",
			Text:       finalText,
			QuickReply: flex.GetQuickReplyItems(),
		},
	}

	// 1. Check if the ADK tool queued a pending_update requiring confirmation
	if st, ok := h.StateStore.Get(ctx, userID); ok && st.Action == models.ActionPendingUpdate {
		cardName := h.Repo.GetNameFromCard(ctx, userID, st.CardID)
		if cardName == "" {
			cardName = "聯絡人"
		}
		var msgText string
		if st.UpdateType == "field" {
			fieldLabel, exists := config.FieldLabels[st.Field]
			if !exists {
				fieldLabel = st.Field
			}
			msgText = fmt.Sprintf("請問您是否確定要將「%s」的【%s】修改為：\n\n「%s」？", cardName, fieldLabel, st.Value)
		} else {
			msgText = fmt.Sprintf("請問您是否確定要將「%s」的【備忘錄】修改為：\n\n「%s」？", cardName, st.Memo)
		}
		confirmFlex := flex.GetConfirmUpdateFlexMsg(msgText, "action=confirm_update", "action=cancel_update")
		replyMsgs = append(replyMsgs, ReplyMessage{Type: "flex", Flex: &confirmFlex})
	} else if len(res.FoundCardIDs) > 0 {
		// 2. Otherwise display found cards (<=4 full cards, >4 disambiguation list)
		if len(res.FoundCardIDs) <= 4 {
			for _, cid := range res.FoundCardIDs {
				if cardData := h.Repo.GetCardByID(ctx, userID, cid); cardData != nil {
					fm := flex.GetNamecardFlexMsg(cardData.ToMap(), cid)
					replyMsgs = append(replyMsgs, ReplyMessage{Type: "flex", Flex: &fm})
				}
			}
		} else {
			var cardsList []map[string]string
			for _, cid := range res.FoundCardIDs {
				if cardData := h.Repo.GetCardByID(ctx, userID, cid); cardData != nil {
					m := cardData.ToMap()
					m["card_id"] = cid
					cardsList = append(cardsList, m)
				}
			}
			if len(cardsList) > 0 {
				listFlex := flex.GetNamecardListFlexMsg(cardsList, "🔍 找到多個相符的名片")
				replyMsgs = append(replyMsgs, ReplyMessage{Type: "flex", Flex: &listFlex})
			}
		}
	}

	return h.Line.ReplyMessages(ctx, replyToken, replyMsgs)
}

func (h *BotHandler) runFallbackSearch(ctx context.Context, replyToken, userID, msg string) error {
	matches := agent.FallbackKeywordSearch(ctx, h.Repo, userID, msg)
	if len(matches) > 0 {
		if len(matches) <= 4 {
			replyMsgs := []ReplyMessage{
				{
					Type:       "text",
					Text:       "「智慧搜尋」服務暫時無法取得，已自動啟用「關鍵字備援搜尋」為您找到以下相關名片：",
					QuickReply: flex.GetQuickReplyItems(),
				},
			}
			for _, card := range matches {
				fm := flex.GetNamecardFlexMsg(card.ToMap(), card.CardID)
				replyMsgs = append(replyMsgs, ReplyMessage{Type: "flex", Flex: &fm})
			}
			return h.Line.ReplyMessages(ctx, replyToken, replyMsgs)
		}

		replyMsgs := []ReplyMessage{
			{
				Type:       "text",
				Text:       "「智慧搜尋」服務暫時無法取得，已自動啟用「關鍵字備援搜尋」為您找到以下相關名片清單：",
				QuickReply: flex.GetQuickReplyItems(),
			},
		}
		var cardsList []map[string]string
		for _, card := range matches {
			m := card.ToMap()
			m["card_id"] = card.CardID
			cardsList = append(cardsList, m)
		}
		listFlex := flex.GetNamecardListFlexMsg(cardsList, "🔍 關鍵字備援搜尋結果")
		replyMsgs = append(replyMsgs, ReplyMessage{Type: "flex", Flex: &listFlex})
		return h.Line.ReplyMessages(ctx, replyToken, replyMsgs)
	}

	return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
		{
			Type:       "text",
			Text:       "處理您的查詢時發生錯誤，請稍後再試。",
			QuickReply: flex.GetQuickReplyItems(),
		},
	})
}

// FinalizeAndSaveCard checks for duplicates by email, stores the card, and replies to the user.
func (h *BotHandler) FinalizeAndSaveCard(ctx context.Context, cardObj models.Namecard, replyToken, userID string) error {
	if existingID := h.Repo.CheckIfCardExists(ctx, cardObj, userID); existingID != "" {
		existingCard := h.Repo.GetCardByID(ctx, userID, existingID)
		if existingCard != nil {
			fm := flex.GetNamecardFlexMsg(existingCard.ToMap(), existingID)
			return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
				{
					Type:       "text",
					Text:       "這個名片已經存在資料庫中。",
					QuickReply: flex.GetQuickReplyItems(),
				},
				{Type: "flex", Flex: &fm},
			})
		}
	}

	cardID := h.Repo.AddNamecard(ctx, cardObj, userID)
	if cardID != "" {
		fm := flex.GetNamecardFlexMsg(cardObj.ToMap(), cardID)
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{Type: "flex", Flex: &fm},
			{
				Type:       "text",
				Text:       "名片資料已經成功加入資料庫。",
				QuickReply: flex.GetQuickReplyItems(),
			},
		})
	}

	return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
		{
			Type:       "text",
			Text:       "儲存名片時發生錯誤。",
			QuickReply: flex.GetQuickReplyItems(),
		},
	})
}

// HandleImageEvent processes single and double-sided business card images.
func (h *BotHandler) HandleImageEvent(ctx context.Context, replyToken, userID, messageID string) error {
	imageContent, err := h.Line.GetMessageContent(ctx, messageID)
	if err != nil {
		return err
	}

	st, hasState := h.StateStore.Get(ctx, userID)
	isAwaitingBackside := hasState &&
		st.Action == models.ActionAwaitingBacksideImage &&
		st.ExpiresAt > h.now()

	var rawResult string
	if isAwaitingBackside {
		rawResult, err = h.Vision.GenerateJSONFromTwoImages(ctx, st.FrontImageBytes, imageContent, config.DoubleSidedImagePrompt)
		h.StateStore.Delete(ctx, userID)
	} else {
		if hasState && (st.Action == models.ActionPendingBacksideConfirm || st.Action == models.ActionAwaitingBacksideImage) {
			h.StateStore.Delete(ctx, userID)
		}
		rawResult, err = h.Vision.GenerateJSONFromImage(ctx, imageContent, config.ImagePrompt)
	}

	if err != nil {
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{Type: "text", Text: fmt.Sprintf("無法解析這張名片，請再試一次。 錯誤資訊: %v", err)},
		})
	}

	cardObj, parseErr := models.ParseGeminiResultToNamecard(rawResult)
	if parseErr != nil {
		errMsg := fmt.Sprintf("無法解析這張名片，請再試一次。 錯誤資訊: %s", rawResult)
		if parseErr == models.ErrEmptyListResponse {
			errMsg = fmt.Sprintf("無法解析這張名片，Gemini 回傳了空的資料。 資訊: %s", rawResult)
		}
		return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
			{Type: "text", Text: errMsg},
		})
	}

	if isAwaitingBackside {
		return h.FinalizeAndSaveCard(ctx, *cardObj, replyToken, userID)
	}

	h.StateStore.Set(ctx, userID, models.UserState{
		Action:          models.ActionPendingBacksideConfirm,
		CardObj:         cardObj,
		FrontImageBytes: imageContent,
		ExpiresAt:       h.now() + config.PendingBacksideTimeoutSeconds,
	})

	displayName := cardObj.Name
	if strings.TrimSpace(displayName) == "" {
		displayName = "未命名"
	}

	return h.Line.ReplyMessages(ctx, replyToken, []ReplyMessage{
		{
			Type:       "text",
			Text:       fmt.Sprintf("已辨識正面（%s）。這張名片還有背面嗎？", displayName),
			QuickReply: flex.GetBacksideConfirmQuickReply(),
		},
	})
}
