package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/line/line-bot-sdk-go/v8/linebot/webhook"

	"github.com/kkdai/linebot-namecard-go/internal/agent"
	"github.com/kkdai/linebot-namecard-go/internal/bot"
	"github.com/kkdai/linebot-namecard-go/internal/config"
	"github.com/kkdai/linebot-namecard-go/internal/gemini"
	internalmem "github.com/kkdai/linebot-namecard-go/internal/memory"
	"github.com/kkdai/linebot-namecard-go/internal/storage"
)

func extractUserID(src webhook.SourceInterface) string {
	switch s := src.(type) {
	case webhook.UserSource:
		return s.UserId
	case *webhook.UserSource:
		return s.UserId
	case webhook.GroupSource:
		return s.UserId
	case *webhook.GroupSource:
		return s.UserId
	case webhook.RoomSource:
		return s.UserId
	case *webhook.RoomSource:
		return s.UserId
	default:
		return ""
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	// 1. Initialize Cloud Firestore, Firebase RTDB (optional), and Cloud Storage
	fsClient, rtdbClient, bucketHandle, err := storage.InitCloudClients(ctx, cfg)
	if err != nil {
		log.Printf("Warning: Cloud client initialization encountered error (running with shared memory fallback): %v", err)
	} else {
		log.Println("Cloud Firestore & Firebase Admin SDK initialized successfully.")
	}
	if fsClient != nil {
		defer fsClient.Close()
	}

	// 2. Initialize Shared Memory + Firestore Persistence Services
	docStore := internalmem.NewFirestoreDocStore(fsClient)
	stateStore := internalmem.NewUserStateStore(docStore)
	sessionSvc := internalmem.NewFirestoreSessionService(docStore)
	memorySvc := internalmem.NewFirestoreMemoryService(docStore)

	// 3. Initialize Card Repository (Firestore + Firebase RTDB + Shared Memory Cache)
	repo := storage.NewStoreRepository(fsClient, rtdbClient, bucketHandle, cfg.FirebaseStorageBucket)

	// 4. Initialize Gemini Vision Client (Single & Double-Sided OCR)
	visionCli, err := gemini.NewGenAIClient(ctx, cfg.ProjectID, cfg.Location)
	if err != nil {
		log.Fatalf("Failed to initialize Gemini Vision client: %v", err)
	}

	// 5. Initialize Shared ADK Agent & Runner (backed by Firestore SessionService & MemoryService)
	adkSvc, err := agent.NewADKService(
		ctx,
		cfg.ProjectID,
		cfg.Location,
		repo,
		stateStore,
		sessionSvc,
		memorySvc,
		nil,
	)
	if err != nil {
		log.Fatalf("Failed to initialize ADK Agent service: %v", err)
	}

	// 6. Initialize LINE Messaging API client & BotHandler
	lineMessenger, err := bot.NewSDKLineMessenger(cfg.ChannelAccessToken)
	if err != nil {
		log.Fatalf("Failed to initialize LINE Messaging API client: %v", err)
	}
	handler := bot.NewBotHandler(lineMessenger, repo, stateStore, visionCli, adkSvc)

	// 7. Setup HTTP Routes (matching FastAPI GET / and POST /)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case http.MethodPost:
			cb, err := webhook.ParseRequest(cfg.ChannelSecret, r)
			if err != nil {
				if errors.Is(err, webhook.ErrInvalidSignature) {
					http.Error(w, "Invalid signature", http.StatusBadRequest)
					return
				}
				http.Error(w, "Bad request", http.StatusBadRequest)
				return
			}

			handler.SweepExpiredStates(r.Context())

			for _, ev := range cb.Events {
				switch e := ev.(type) {
				case webhook.MessageEvent:
					userID := extractUserID(e.Source)
					switch m := e.Message.(type) {
					case webhook.TextMessageContent:
						if err := handler.HandleTextEvent(r.Context(), e.ReplyToken, userID, m.Text); err != nil {
							log.Printf("Error handling text event: %v", err)
						}
					case webhook.ImageMessageContent:
						if err := handler.HandleImageEvent(r.Context(), e.ReplyToken, userID, m.Id); err != nil {
							log.Printf("Error handling image event: %v", err)
						}
					}
				case webhook.PostbackEvent:
					userID := extractUserID(e.Source)
					if e.Postback != nil {
						if err := handler.HandlePostbackEvent(r.Context(), e.ReplyToken, userID, e.Postback.Data); err != nil {
							log.Printf("Error handling postback event: %v", err)
						}
					}
				}
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))

		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("Starting LINE Bot Namecard Go + ADK server on port %s...", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Println("Server gracefully stopped.")
}
