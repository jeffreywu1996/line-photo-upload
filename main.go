package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"github.com/line/line-bot-sdk-go/v8/linebot/messaging_api"
	"github.com/line/line-bot-sdk-go/v8/linebot/webhook"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"github.com/jeffreywu1996/line-photo-bot/middleware"
)

type MessageCache struct {
	processed map[string]time.Time
	mu        sync.RWMutex
}

func NewMessageCache() *MessageCache {
	return &MessageCache{
		processed: make(map[string]time.Time),
	}
}

func (c *MessageCache) IsProcessed(messageID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, exists := c.processed[messageID]
	return exists
}

func (c *MessageCache) MarkProcessed(messageID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.processed[messageID] = time.Now()

	// Cleanup old entries (optional)
	for id, t := range c.processed {
		if time.Since(t) > 24*time.Hour {
			delete(c.processed, id)
		}
	}
}

// Add configuration struct
type Config struct {
	LineChannelSecret   string
	LineChannelToken    string
	GoogleCredentials   string
	GoogleDriveFolderID string
	Port               string
}

func loadConfig() (*Config, error) {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	config := &Config{
		LineChannelSecret:   os.Getenv("LINE_CHANNEL_SECRET"),
		LineChannelToken:    os.Getenv("LINE_CHANNEL_TOKEN"),
		GoogleCredentials:   os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
		GoogleDriveFolderID: os.Getenv("GOOGLE_DRIVE_FOLDER_ID"),
		Port:               os.Getenv("PORT"),
	}

	// Validate required fields
	if config.LineChannelSecret == "" || config.LineChannelToken == "" ||
		config.GoogleCredentials == "" || config.GoogleDriveFolderID == "" {
		return nil, fmt.Errorf("missing required environment variables")
	}

	if config.Port == "" {
		config.Port = "3000"
	}

	return config, nil
}

func main() {
	// Configure logging with timestamp
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Println("Starting LINE bot server...")

	// Load configuration
	config, err := loadConfig()
	if err != nil {
		log.Fatal("Failed to load configuration:", err)
	}

	// Initialize LINE bot clients
	bot, err := messaging_api.NewMessagingApiAPI(config.LineChannelToken)
	if err != nil {
		log.Fatal("Error initializing bot:", err)
	}

	// Initialize Google Drive client
	driveService, err := initializeDriveClient(config)
	if err != nil {
		log.Fatal("Failed to initialize Drive client:", err)
	}
	log.Println("Successfully initialized Google Drive client")

	// Initialize message cache
	messageCache := NewMessageCache()

	// Create main router
	router := http.NewServeMux()

	// Add callback handler
	router.HandleFunc("/callback", callbackHandler(bot, driveService, messageCache, config))

	// Add health check endpoint
	router.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Wrap router with middleware
	handler := middleware.ErrorHandler(middleware.RequestLogger(router))

	// Start server with middleware
	log.Printf("Server is running at :%s", config.Port)
	if err := http.ListenAndServe(":"+config.Port, handler); err != nil {
		log.Fatal(err)
	}
}

// Move callback handler to separate function
func callbackHandler(bot *messaging_api.MessagingApiAPI, driveService *drive.Service,
	messageCache *MessageCache, config *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		log.Printf("Received %s request to %s", req.Method, req.URL.Path)

		cb, err := webhook.ParseRequest(config.LineChannelSecret, req)
		if err != nil {
			if err == webhook.ErrInvalidSignature {
				log.Printf("Invalid signature error: %v", err)
				w.WriteHeader(400)
			} else {
				log.Printf("Parse request error: %v", err)
				w.WriteHeader(500)
			}
			return
		}

		// Send 200 OK immediately after validation
		w.WriteHeader(http.StatusOK)

		// Process events asynchronously
		go func() {
			for _, event := range cb.Events {
				switch e := event.(type) {
				case webhook.MessageEvent:
					// Get user ID based on source type
					var userID string
					switch source := e.Source.(type) {
					case *webhook.UserSource:
						userID = source.UserId
					case *webhook.GroupSource:
						userID = source.UserId
					case *webhook.RoomSource:
						userID = source.UserId
					}
					log.Printf("Message event received from user %s", userID)

					switch message := e.Message.(type) {
					case webhook.ImageMessageContent:
						handleFileMessage(bot, driveService, message, ".jpg", e.ReplyToken, messageCache, config)
					case webhook.FileMessageContent:
						handleFileMessage(bot, driveService, message, filepath.Ext(message.FileName), e.ReplyToken, messageCache, config)
					case webhook.VideoMessageContent:
						handleFileMessage(bot, driveService, message, ".mp4", e.ReplyToken, messageCache, config)
					case webhook.AudioMessageContent:
						handleFileMessage(bot, driveService, message, ".m4a", e.ReplyToken, messageCache, config)
					case webhook.TextMessageContent:
						messageID := message.Id
						if messageCache.IsProcessed(messageID) {
							log.Printf("Skipping already processed message ID: %s", messageID)
							continue
						}
						log.Printf("Text message received: %s", message.Text)
						if _, err = bot.ReplyMessage(&messaging_api.ReplyMessageRequest{
							ReplyToken: e.ReplyToken,
							Messages: []messaging_api.MessageInterface{
								&messaging_api.TextMessage{
									Text: message.Text,
								},
							},
						}); err != nil {
							log.Printf("Error sending reply: %v", err)
						} else {
							log.Println("Reply sent successfully")
						}
						messageCache.MarkProcessed(messageID)
					}
				}
			}
		}()
	}
}

func handleFileMessage(bot *messaging_api.MessagingApiAPI, driveService *drive.Service,
	message webhook.MessageContentInterface, fileExt string, replyToken string,
	messageCache *MessageCache, config *Config) {
	messageID := message.(interface{ GetId() string }).GetId()

	// Check if we've already processed this message
	if messageCache.IsProcessed(messageID) {
		log.Printf("Skipping already processed message ID: %s", messageID)
		return
	}

	log.Printf("File message received (Message ID: %s)", messageID)
	if err := handleFile(bot, driveService, message, fileExt, replyToken, config); err != nil {
		log.Printf("Error handling file: %v", err)
	}
	// Mark as processed after successful handling
	messageCache.MarkProcessed(messageID)
}

func handleFile(bot *messaging_api.MessagingApiAPI, driveService *drive.Service,
	message webhook.MessageContentInterface, fileExt string, replyToken string, config *Config) error {
	messageID := message.(interface{ GetId() string }).GetId()
	log.Printf("Processing file message ID: %s", messageID)

	// Get the file content from LINE
	blob, err := messaging_api.NewMessagingApiBlobAPI(config.LineChannelToken)
	if err != nil {
		return fmt.Errorf("failed to create blob client: %v", err)
	}

	content, err := blob.GetMessageContent(messageID)
	if err != nil {
		return fmt.Errorf("failed to get content: %v", err)
	}
	defer content.Body.Close()

	// Create a temporary file with timestamp
	timestamp := time.Now().Format("20060102-150405")
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("line-file-%s-*%s", timestamp, fileExt))
	if err != nil {
		return fmt.Errorf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	log.Printf("Created temporary file: %s", tmpFile.Name())

	// Copy the file content to the temporary file
	if _, err := io.Copy(tmpFile, content.Body); err != nil {
		return fmt.Errorf("failed to copy content: %v", err)
	}

	// Get original filename for file messages
	fileName := filepath.Base(tmpFile.Name())
	if fileMsg, ok := message.(webhook.FileMessageContent); ok {
		fileName = fileMsg.FileName
	}

	// Upload to Google Drive
	driveFile := &drive.File{
		Name:     fileName,
		Parents:  []string{config.GoogleDriveFolderID},
	}

	file, err := os.Open(tmpFile.Name())
	if err != nil {
		return fmt.Errorf("failed to open temp file: %v", err)
	}
	defer file.Close()

	log.Println("Uploading file to Google Drive...")
	uploadedFile, err := driveService.Files.Create(driveFile).Media(file).Do()
	if err != nil {
		return fmt.Errorf("failed to upload to Drive: %v", err)
	}
	log.Printf("File uploaded successfully to Drive with ID: %s", uploadedFile.Id)

	// Send reply
	messageText := fmt.Sprintf("File uploaded to Drive! ID: %s", uploadedFile.Id)
	if fileMsg, ok := message.(webhook.FileMessageContent); ok {
		messageText = fmt.Sprintf("File '%s' uploaded to Drive! ID: %s", fileMsg.FileName, uploadedFile.Id)
	}

	if _, err = bot.ReplyMessage(&messaging_api.ReplyMessageRequest{
		ReplyToken: replyToken,
		Messages: []messaging_api.MessageInterface{
			&messaging_api.TextMessage{
				Text: messageText,
			},
		},
	}); err != nil {
		if strings.Contains(err.Error(), "Invalid reply token") {
			log.Println("Warning: Reply token expired or already used")
			return nil
		}
		return fmt.Errorf("failed to send reply: %v", err)
	}
	log.Println("Success reply sent to user")

	return nil
}

func initializeDriveClient(config *Config) (*drive.Service, error) {
	ctx := context.Background()
	credentials := option.WithCredentialsFile(config.GoogleCredentials)

	return drive.NewService(ctx, credentials)
}
