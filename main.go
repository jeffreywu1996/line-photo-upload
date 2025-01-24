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

// Add this new type for group stats
type GroupStats struct {
	TotalUploads int
	LastUpload   time.Time
	mu           sync.RWMutex
}

type GroupCache struct {
	stats map[string]*GroupStats // groupID -> stats
	mu    sync.RWMutex
}

func NewGroupCache() *GroupCache {
	return &GroupCache{
		stats: make(map[string]*GroupStats),
	}
}

func (c *GroupCache) IncrementUploads(groupID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.stats[groupID]; !exists {
		c.stats[groupID] = &GroupStats{}
	}

	stats := c.stats[groupID]
	stats.mu.Lock()
	defer stats.mu.Unlock()

	stats.TotalUploads++
	stats.LastUpload = time.Now()
}

func (c *GroupCache) GetStats(groupID string) (int, time.Time) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if stats, exists := c.stats[groupID]; exists {
		stats.mu.RLock()
		defer stats.mu.RUnlock()
		return stats.TotalUploads, stats.LastUpload
	}
	return 0, time.Time{}
}

// Add configuration struct
type Config struct {
	LineChannelSecret   string
	LineChannelToken    string
	GoogleCredentials   string
	GoogleDriveFolderID string
	Port               string
	AdminUsers         []string // List of user IDs who have admin privileges
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

	// Load admin users from env var (comma-separated list)
	adminUsersStr := os.Getenv("ADMIN_USERS")
	if adminUsersStr != "" {
		config.AdminUsers = strings.Split(adminUsersStr, ",")
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

	// Initialize group cache
	groupCache := NewGroupCache()

	// Create main router
	router := http.NewServeMux()

	// Add callback handler with group cache
	router.HandleFunc("/callback", callbackHandler(bot, driveService, messageCache, groupCache, config))

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

// Update the callbackHandler function to only handle commands
func callbackHandler(bot *messaging_api.MessagingApiAPI, driveService DriveService,
	messageCache *MessageCache, groupCache *GroupCache, config *Config) http.HandlerFunc {
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
					// Get user ID and group ID if applicable
					var userID, groupID string
					switch source := e.Source.(type) {
					case *webhook.UserSource:
						userID = source.UserId
					case *webhook.GroupSource:
						userID = source.UserId
						groupID = source.GroupId
					case *webhook.RoomSource:
						userID = source.UserId
					}

					if !isAllowedUser(userID, config) {
						sendMessage(bot, e.ReplyToken, "Sorry, you don't have permission to use this bot.")
						continue
					}

					switch message := e.Message.(type) {
					case webhook.TextMessageContent:
						// Handle commands for both group and direct messages
						if strings.HasPrefix(message.Text, "/") {
							handleCommand(bot, message.Text, groupID, e.ReplyToken, groupCache)
							continue
						}
						// Ignore non-command text messages

					case webhook.ImageMessageContent, webhook.FileMessageContent,
						webhook.VideoMessageContent, webhook.AudioMessageContent:
						// Create group-specific folder structure if needed
						folderID := config.GoogleDriveFolderID
						if groupID != "" {
							folderID = getOrCreateGroupFolder(driveService, groupID, config.GoogleDriveFolderID)
							defer func() {
								groupCache.IncrementUploads(groupID)
							}()
						}

						// Handle the file upload with the appropriate folder
						handleFileMessage(bot, driveService, e.Message, getFileExtension(e.Message),
							e.ReplyToken, messageCache, folderID, config)
					}
				}
			}
		}()
	}
}

type MessageSender interface {
	ReplyMessage(request *messaging_api.ReplyMessageRequest) (*messaging_api.ReplyMessageResponse, error)
}

// Rename handleGroupCommand to handleCommand and update it to work for both group and direct messages
func handleCommand(bot MessageSender, text, groupID, replyToken string, groupCache *GroupCache) {
	switch text {
	case "/help":
		sendMessage(bot, replyToken, `Available commands:
/help - Show this help message
/stats - Show upload statistics (group only)
/upload - Show upload instructions`)

	case "/stats":
		if groupID == "" {
			sendMessage(bot, replyToken, "Stats command is only available in groups")
			return
		}
		uploads, lastUpload := groupCache.GetStats(groupID)
		var lastUploadStr string
		if !lastUpload.IsZero() {
			lastUploadStr = lastUpload.Format("2006-01-02 15:04:05")
		} else {
			lastUploadStr = "Never"
		}

		msg := fmt.Sprintf("Group Statistics:\nTotal uploads: %d\nLast upload: %s",
			uploads, lastUploadStr)
		sendMessage(bot, replyToken, msg)

	case "/upload":
		sendMessage(bot, replyToken, "To upload files:\n1. Send an image, video, audio, or file\n2. I will upload it to Google Drive\n3. I will reply with the file ID")

	default:
		if strings.HasPrefix(text, "/") {
			sendMessage(bot, replyToken, "Unknown command. Type /help for available commands.")
		}
	}
}

func sendMessage(bot MessageSender, replyToken, text string) {
	if bot == nil {
		log.Printf("Error: bot is nil in sendMessage")
		return
	}

	if replyToken == "" {
		log.Printf("Error: empty reply token in sendMessage")
		return
	}

	if _, err := bot.ReplyMessage(&messaging_api.ReplyMessageRequest{
		ReplyToken: replyToken,
		Messages: []messaging_api.MessageInterface{
			&messaging_api.TextMessage{Text: text},
		},
	}); err != nil {
		log.Printf("Error sending message: %v", err)
	}
}

func getOrCreateGroupFolder(driveService DriveService, groupID, parentFolderID string) string {
	folderName := fmt.Sprintf("LINE-Group-%s", groupID)
	folder := &drive.File{
		Name:     folderName,
		Parents:  []string{parentFolderID},
		MimeType: "application/vnd.google-apps.folder",
	}

	createdFolder, err := driveService.Files().CreateFile(folder, nil)
	if err != nil {
		log.Printf("Error creating group folder: %v", err)
		return parentFolderID
	}
	return createdFolder.Id
}

func getFileExtension(message webhook.MessageContentInterface) string {
	switch m := message.(type) {
	case webhook.ImageMessageContent:
		return ".jpg"
	case webhook.VideoMessageContent:
		return ".mp4"
	case webhook.AudioMessageContent:
		return ".m4a"
	case webhook.FileMessageContent:
		return filepath.Ext(m.FileName)
	default:
		return ""
	}
}

func handleFileMessage(bot *messaging_api.MessagingApiAPI, driveService DriveService,
	message webhook.MessageContentInterface, fileExt string, replyToken string,
	messageCache *MessageCache, folderID string, config *Config) {
	// Get messageID based on message type
	var messageID string
	switch m := message.(type) {
	case webhook.ImageMessageContent:
		messageID = m.Id
	case webhook.VideoMessageContent:
		messageID = m.Id
	case webhook.AudioMessageContent:
		messageID = m.Id
	case webhook.FileMessageContent:
		messageID = m.Id
	default:
		log.Printf("Unsupported message type: %T", message)
		return
	}

	// Check if we've already processed this message
	if messageCache.IsProcessed(messageID) {
		log.Printf("Skipping already processed message ID: %s", messageID)
		return
	}

	log.Printf("File message received (Message ID: %s)", messageID)
	if err := handleFile(bot, driveService, message, messageID, fileExt, replyToken, config); err != nil {
		log.Printf("Error handling file: %v", err)
	}
	// Mark as processed after successful handling
	messageCache.MarkProcessed(messageID)
}

type DriveService interface {
	Files() FilesService
}

type FilesService interface {
	CreateFile(file *drive.File, media io.Reader) (*drive.File, error)
}

// Wrapper for the real Drive service
type driveServiceWrapper struct {
	*drive.Service
}

func (d *driveServiceWrapper) Files() FilesService {
	return &filesServiceWrapper{d.Service.Files}
}

type filesServiceWrapper struct {
	*drive.FilesService
}

func (f *filesServiceWrapper) CreateFile(file *drive.File, media io.Reader) (*drive.File, error) {
	call := f.FilesService.Create(file)
	if media != nil {
		call.Media(media)
	}
	return call.Do()
}

func handleFile(bot *messaging_api.MessagingApiAPI, driveService DriveService,
	message webhook.MessageContentInterface, messageID string, fileExt string, replyToken string, config *Config) error {
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
	uploadedFile, err := driveService.Files().CreateFile(driveFile, file)
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

// Update the initialization function
func initializeDriveClient(config *Config) (DriveService, error) {
	ctx := context.Background()
	credentials := option.WithCredentialsFile(config.GoogleCredentials)

	service, err := drive.NewService(ctx, credentials)
	if err != nil {
		return nil, err
	}
	return &driveServiceWrapper{service}, nil
}

func isAllowedUser(userID string, config *Config) bool {
	for _, adminID := range config.AdminUsers {
		if userID == adminID {
			return true
		}
	}
	return true // Allow all users by default
}
