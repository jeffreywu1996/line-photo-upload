package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jeffreywu1996/line-photo-bot/middleware"
	"github.com/joho/godotenv"
	"github.com/line/line-bot-sdk-go/v8/linebot/messaging_api"
	"github.com/line/line-bot-sdk-go/v8/linebot/webhook"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
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

// Update handleCommand function
func handleCommand(bot MessageSender, text, groupID, replyToken string, groupCache *GroupCache) {
	switch text {
	case "/help":
		sendMessage(bot, replyToken, `📸 LINE Photo Bot
This bot automatically saves photos and files shared in this chat to Google Drive for easy access and backup.

Available commands:
/help - Show this help message
/stats - Show last 5 uploads and statistics
/upload - Show upload instructions`)

	case "/stats":
		var uploads int
		var lastUpload time.Time
		var recentFiles []FileInfo

		if groupID != "" {
			// Get group stats
			uploads, lastUpload, recentFiles = groupCache.GetStats(groupID)
		} else {
			// Get global stats (all uploads)
			uploads, lastUpload, recentFiles = groupCache.GetGlobalStats()
		}

		// Format recent files list
		var recentFilesList string
		if len(recentFiles) > 0 {
			recentFilesList = "\n\nRecent uploads:"
			for _, file := range recentFiles {
				recentFilesList += fmt.Sprintf("\n%s - %s",
					file.Timestamp.Format("2006-01-02 15:04:05"),
					file.Name)
			}
		} else {
			recentFilesList = "\n\nNo recent uploads found."
		}

		var statsTitle string
		if groupID != "" {
			statsTitle = "📊 Group Statistics"
		} else {
			statsTitle = "📊 Upload Statistics"
		}

		msg := fmt.Sprintf("%s\nTotal uploads: %d\nLast upload: %s%s",
			statsTitle,
			uploads,
			lastUpload.Format("2006-01-02 15:04:05"),
			recentFilesList)
		sendMessage(bot, replyToken, msg)

	case "/upload":
		sendMessage(bot, replyToken, `📤 How to upload files:

1. Simply share any photo, video, or file in this chat
2. The bot will automatically save it to Google Drive
3. Files are organized by group/chat

Supported file types:
• Photos (JPG)
• Videos (MP4)
• Audio files (M4A)
• Documents (PDF, etc.)`)

	default:
		if strings.HasPrefix(text, "/") {
			sendMessage(bot, replyToken, "Unknown command. Type /help for available commands.")
		}
	}
}

// Update GroupStats struct to track recent files
type FileInfo struct {
	Name      string
	Timestamp time.Time
}

type GroupStats struct {
	TotalUploads int
	LastUpload   time.Time
	RecentFiles  []FileInfo // Keep track of recent files
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

func (c *GroupCache) AddUploadedFile(groupID, fileName string) {
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

	// Add new file to recent files
	newFile := FileInfo{
		Name:      fileName,
		Timestamp: time.Now(),
	}

	// Keep only last 5 files
	stats.RecentFiles = append([]FileInfo{newFile}, stats.RecentFiles...)
	if len(stats.RecentFiles) > 5 {
		stats.RecentFiles = stats.RecentFiles[:5]
	}
}

func (c *GroupCache) GetStats(groupID string) (int, time.Time, []FileInfo) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if stats, exists := c.stats[groupID]; exists {
		stats.mu.RLock()
		defer stats.mu.RUnlock()
		return stats.TotalUploads, stats.LastUpload, stats.RecentFiles
	}
	return 0, time.Time{}, nil
}

// Add method to get global stats
func (c *GroupCache) GetGlobalStats() (int, time.Time, []FileInfo) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	totalUploads := 0
	var lastUpload time.Time
	var allFiles []FileInfo

	// Collect stats from all groups
	for _, stats := range c.stats {
		stats.mu.RLock()
		totalUploads += stats.TotalUploads
		if stats.LastUpload.After(lastUpload) {
			lastUpload = stats.LastUpload
		}
		allFiles = append(allFiles, stats.RecentFiles...)
		stats.mu.RUnlock()
	}

	// Sort files by timestamp (newest first)
	sort.Slice(allFiles, func(i, j int) bool {
		return allFiles[i].Timestamp.After(allFiles[j].Timestamp)
	})

	// Return only the last 5 files
	if len(allFiles) > 5 {
		allFiles = allFiles[:5]
	}

	return totalUploads, lastUpload, allFiles
}

// Add configuration struct
type Config struct {
	LineChannelSecret   string
	LineChannelToken    string
	GoogleCredentials   string
	GoogleDriveFolderID string
	Port                string
	AdminUsers          []string // List of user IDs who have admin privileges
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
		Port:                os.Getenv("PORT"),
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

// Update the interface to match the SDK
type BlobAPI interface {
	GetMessageContent(messageID string) (io.ReadCloser, error)
}

// Update the real implementation
type realBlobAPI struct {
	api *messaging_api.MessagingApiBlobAPI
}

func (r *realBlobAPI) GetMessageContent(messageID string) (io.ReadCloser, error) {
	resp, err := r.api.GetMessageContent(messageID)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// Provide a function we can override in tests:
var NewBlobAPI = func(channelToken string) (BlobAPI, error) {
	api, err := messaging_api.NewMessagingApiBlobAPI(channelToken)
	if err != nil {
		return nil, err
	}
	return &realBlobAPI{api: api}, nil
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
	handler := middleware.ErrorHandler(
		middleware.SecurityHeaders(
			middleware.RequestLogger(router),
		),
	)

	// Create server with timeouts
	server := &http.Server{
		Addr:         ":" + config.Port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("Server is running at :%s", config.Port)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

type MessageSender interface {
	ReplyMessage(request *messaging_api.ReplyMessageRequest) (*messaging_api.ReplyMessageResponse, error)
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
	messageCache *MessageCache, folderID string, config *Config) error {
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
		return fmt.Errorf("unsupported message type: %T", message)
	}

	// Check if we've already processed this message
	if messageCache.IsProcessed(messageID) {
		log.Printf("Skipping already processed message ID: %s", messageID)
		return nil
	}

	log.Printf("File message received (Message ID: %s)", messageID)
	if err := handleFile(bot, driveService, message, messageID, fileExt, replyToken, config); err != nil {
		log.Printf("Error handling file: %v", err)
		return err
	}
	// Mark as processed after successful handling
	messageCache.MarkProcessed(messageID)
	return nil
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

// Update handleFile to use the variable
func handleFile(bot *messaging_api.MessagingApiAPI, driveService DriveService,
	message webhook.MessageContentInterface, messageID string, fileExt string, replyToken string, config *Config) error {
	log.Printf("Processing file message ID: %s", messageID)

	// Get the file content from LINE
	blob, err := NewBlobAPI(config.LineChannelToken)
	if err != nil {
		return fmt.Errorf("failed to create blob client: %v", err)
	}

	content, err := blob.GetMessageContent(messageID)
	if err != nil {
		return fmt.Errorf("failed to get content: %v", err)
	}
	defer content.Close()

	// Create a temporary file with timestamp
	timestamp := time.Now().Format("20060102-150405")
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("line-file-%s-*%s", timestamp, fileExt))
	if err != nil {
		return fmt.Errorf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Copy the file content and track size
	written, err := io.Copy(tmpFile, content)
	if err != nil {
		return fmt.Errorf("failed to copy content: %v", err)
	}
	log.Printf("File size: %.2f MB", float64(written)/(1024*1024))

	// Get original filename for file messages
	fileName := filepath.Base(tmpFile.Name())
	if fileMsg, ok := message.(webhook.FileMessageContent); ok {
		fileName = fileMsg.FileName
	}

	// Upload to Google Drive
	driveFile := &drive.File{
		Name:    fileName,
		Parents: []string{config.GoogleDriveFolderID},
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

	// Remove the reply message code
	// The function should just return nil after successful upload
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

// Add the callbackHandler function
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
						}

						// Get filename for tracking
						var fileName string
						if fileMsg, ok := message.(webhook.FileMessageContent); ok {
							fileName = fileMsg.FileName
						} else {
							fileName = fmt.Sprintf("file%s", getFileExtension(e.Message))
						}

						// Handle the file upload
						if err := handleFileMessage(bot, driveService, e.Message, getFileExtension(e.Message),
							e.ReplyToken, messageCache, folderID, config); err != nil {
							log.Printf("Error handling file: %v", err)
							continue
						}

						// Track all uploads, using "direct" as groupID for direct messages
						trackingGroupID := groupID
						if trackingGroupID == "" {
							trackingGroupID = "direct"
						}
						groupCache.AddUploadedFile(trackingGroupID, fileName)
					}
				}
			}
		}()
	}
}
