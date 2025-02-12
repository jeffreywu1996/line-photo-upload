package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/line/line-bot-sdk-go/v8/linebot/messaging_api"
	"github.com/line/line-bot-sdk-go/v8/linebot/webhook"
	"google.golang.org/api/drive/v3"
)

func TestMessageCache(t *testing.T) {
	cache := NewMessageCache()

	// Test adding and checking a message
	messageID := "test123"
	if cache.IsProcessed(messageID) {
		t.Error("Message should not be processed initially")
	}

	cache.MarkProcessed(messageID)
	if !cache.IsProcessed(messageID) {
		t.Error("Message should be marked as processed")
	}

	// Test cleanup of old messages
	oldMessageID := "old123"
	cache.processed[oldMessageID] = time.Now().Add(-25 * time.Hour)
	cache.MarkProcessed(messageID) // This triggers cleanup
	if cache.IsProcessed(oldMessageID) {
		t.Error("Old message should be cleaned up")
	}
}

func TestLoadConfig(t *testing.T) {
	// Setup test environment variables
	os.Setenv("LINE_CHANNEL_SECRET", "test-secret")
	os.Setenv("LINE_CHANNEL_TOKEN", "test-token")
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "test-creds.json")
	os.Setenv("GOOGLE_DRIVE_FOLDER_ID", "test-folder")
	os.Setenv("PORT", "8080")

	config, err := loadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Test config values
	tests := []struct {
		got       string
		want      string
		fieldName string
	}{
		{config.LineChannelSecret, "test-secret", "LineChannelSecret"},
		{config.LineChannelToken, "test-token", "LineChannelToken"},
		{config.GoogleCredentials, "test-creds.json", "GoogleCredentials"},
		{config.GoogleDriveFolderID, "test-folder", "GoogleDriveFolderID"},
		{config.Port, "8080", "Port"},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("Config %s = %v, want %v", tt.fieldName, tt.got, tt.want)
		}
	}

	// Test default port
	os.Setenv("PORT", "")
	config, err = loadConfig()
	if err != nil {
		t.Fatalf("Failed to load config with empty port: %v", err)
	}
	if config.Port != "3000" {
		t.Errorf("Default port = %v, want 3000", config.Port)
	}
}

func TestGroupCache(t *testing.T) {
	cache := NewGroupCache()
	groupID := "test-group-123"

	// Test initial state
	uploads, lastUpload, files := cache.GetStats(groupID)
	if uploads != 0 {
		t.Errorf("Initial uploads = %d, want 0", uploads)
	}
	if !lastUpload.IsZero() {
		t.Error("Initial lastUpload should be zero time")
	}
	if len(files) != 0 {
		t.Error("Initial files should be empty")
	}

	// Test increment
	cache.AddUploadedFile(groupID, "test.jpg")
	uploads, lastUpload, files = cache.GetStats(groupID)
	if uploads != 1 {
		t.Errorf("Uploads after increment = %d, want 1", uploads)
	}
	if lastUpload.IsZero() {
		t.Error("LastUpload should not be zero after increment")
	}
	if len(files) != 1 {
		t.Error("Should have one file in history")
	}

	// Test multiple files
	cache.AddUploadedFile(groupID, "test2.jpg")
	uploads, _, files = cache.GetStats(groupID)
	if uploads != 2 {
		t.Errorf("Uploads after second increment = %d, want 2", uploads)
	}
	if len(files) != 2 {
		t.Error("Should have two files in history")
	}

	// Test file limit (should keep only last 5)
	for i := 0; i < 5; i++ {
		cache.AddUploadedFile(groupID, fmt.Sprintf("test%d.jpg", i+3))
	}
	_, _, files = cache.GetStats(groupID)
	if len(files) != 5 {
		t.Errorf("Should have 5 files in history, got %d", len(files))
	}
	// Verify it's keeping the most recent files
	if files[0].Name != "test7.jpg" {
		t.Errorf("Most recent file should be test7.jpg, got %s", files[0].Name)
	}
}

func TestLoadConfigWithAdminUsers(t *testing.T) {
	// Setup test environment variables
	os.Setenv("LINE_CHANNEL_SECRET", "test-secret")
	os.Setenv("LINE_CHANNEL_TOKEN", "test-token")
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "test-creds.json")
	os.Setenv("GOOGLE_DRIVE_FOLDER_ID", "test-folder")
	os.Setenv("ADMIN_USERS", "user1,user2,user3")

	config, err := loadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	expectedAdmins := []string{"user1", "user2", "user3"}
	if len(config.AdminUsers) != len(expectedAdmins) {
		t.Errorf("AdminUsers length = %d, want %d", len(config.AdminUsers), len(expectedAdmins))
	}

	for i, admin := range expectedAdmins {
		if config.AdminUsers[i] != admin {
			t.Errorf("AdminUsers[%d] = %s, want %s", i, config.AdminUsers[i], admin)
		}
	}
}

func TestIsAllowedUser(t *testing.T) {
	config := &Config{
		AdminUsers: []string{"admin1", "admin2"},
	}

	tests := []struct {
		name     string
		userID   string
		expected bool
	}{
		{
			name:     "Admin user",
			userID:   "admin1",
			expected: true,
		},
		{
			name:     "Another admin user",
			userID:   "admin2",
			expected: true,
		},
		{
			name:     "Non-admin user",
			userID:   "regular-user",
			expected: true, // Currently all users are allowed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAllowedUser(tt.userID, config); got != tt.expected {
				t.Errorf("isAllowedUser() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHandleCommand(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		groupID   string
		wantText  string
		checkFunc func(string) bool
	}{
		{
			name: "Help command",
			text: "/help",
			wantText: `📸 LINE Photo Bot
This bot automatically saves photos and files shared in this chat to Google Drive for easy access and backup.

Available commands:
/help - Show this help message
/stats - Show last 5 uploads and statistics
/upload - Show upload instructions`,
		},
		{
			name:    "Stats command in group",
			text:    "/stats",
			groupID: "test-group",
			checkFunc: func(msg string) bool {
				return strings.Contains(msg, "📊 Group Statistics") &&
					strings.Contains(msg, "Total uploads: 2") &&
					strings.Contains(msg, "test1.jpg") &&
					strings.Contains(msg, "test2.jpg")
			},
		},
		{
			name: "Stats command in direct message",
			text: "/stats",
			checkFunc: func(msg string) bool {
				return strings.Contains(msg, "📊 Upload Statistics") &&
					strings.Contains(msg, "Total uploads: 2") &&
					strings.Contains(msg, "test1.jpg") &&
					strings.Contains(msg, "test2.jpg")
			},
		},
		{
			name: "Upload command",
			text: "/upload",
			wantText: `📤 How to upload files:

1. Simply share any photo, video, or file in this chat
2. The bot will automatically save it to Google Drive
3. Files are organized by group/chat

Supported file types:
• Photos (JPG)
• Videos (MP4)
• Audio files (M4A)
• Documents (PDF, etc.)`,
		},
		{
			name:     "Invalid command",
			text:     "/invalid",
			wantText: "Unknown command. Type /help for available commands.",
		},
		{
			name:     "Non-command message",
			text:     "Hello",
			wantText: "", // Non-command messages should not trigger any response
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new mock bot for each test case
			bot := newMockBot()
			groupCache := NewGroupCache()

			// Add test data only if needed for stats commands
			if strings.Contains(tt.name, "Stats command") {
				groupCache.AddUploadedFile("test-group", "test1.jpg")
				groupCache.AddUploadedFile("test-group", "test2.jpg")
			}

			handleCommand(bot, tt.text, tt.groupID, "test-reply-token", groupCache)

			// For non-command messages, verify no message was sent
			if tt.text != "" && !strings.HasPrefix(tt.text, "/") {
				if len(bot.sentMessages) > 0 {
					t.Errorf("Non-command message should not trigger a response, got %v", bot.sentMessages)
				}
				return
			}

			// For commands, verify the response
			if len(bot.sentMessages) == 0 {
				t.Error("No message was sent")
				return
			}

			got := bot.sentMessages[len(bot.sentMessages)-1]
			if tt.checkFunc != nil {
				if !tt.checkFunc(got) {
					t.Errorf("Message does not match expected format. Got: %s", got)
				}
			} else if got != tt.wantText {
				t.Errorf("handleCommand() = %v, want %v", got, tt.wantText)
			}
		})
	}
}

func TestGetOrCreateGroupFolder(t *testing.T) {
	driveService := newMockDriveService()
	groupID := "test-group-123"
	parentFolderID := "parent-folder-123"

	folderID := getOrCreateGroupFolder(driveService, groupID, parentFolderID)

	// Verify the result
	if folderID == "" {
		t.Error("Expected folder ID, got empty string")
	}
	if folderID != "mock-file-id" {
		t.Errorf("Expected folder ID 'mock-file-id', got '%s'", folderID)
	}
}

// Helper function to check if a slice contains a string
func contains(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

func TestGetFileExtension(t *testing.T) {
	tests := []struct {
		name    string
		message webhook.MessageContentInterface
		want    string
	}{
		{
			name: "Image message",
			message: webhook.ImageMessageContent{
				Id: "test-image",
			},
			want: ".jpg",
		},
		{
			name: "Video message",
			message: webhook.VideoMessageContent{
				Id: "test-video",
			},
			want: ".mp4",
		},
		{
			name: "Audio message",
			message: webhook.AudioMessageContent{
				Id: "test-audio",
			},
			want: ".m4a",
		},
		{
			name: "File message with extension",
			message: webhook.FileMessageContent{
				Id:       "test-file",
				FileName: "test.pdf",
			},
			want: ".pdf",
		},
		{
			name: "File message without extension",
			message: webhook.FileMessageContent{
				Id:       "test-file",
				FileName: "test",
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getFileExtension(tt.message); got != tt.want {
				t.Errorf("getFileExtension() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Mock implementations
type mockBot struct {
	sentMessages []string
}

func (m *mockBot) ReplyMessage(request *messaging_api.ReplyMessageRequest) (*messaging_api.ReplyMessageResponse, error) {
	for _, msg := range request.Messages {
		if textMsg, ok := msg.(*messaging_api.TextMessage); ok {
			m.sentMessages = append(m.sentMessages, textMsg.Text)
		}
	}
	return &messaging_api.ReplyMessageResponse{}, nil
}

func newMockBot() *mockBot {
	return &mockBot{
		sentMessages: make([]string, 0),
	}
}

// Mock implementations for Google Drive API
type mockDriveService struct {
	files *mockFilesService
}

func newMockDriveService() *mockDriveService {
	return &mockDriveService{
		files: &mockFilesService{},
	}
}

func (m *mockDriveService) Files() FilesService {
	return m.files
}

// Mock FilesService
type mockFilesService struct{}

// In the test, we directly return a dummy drive.File:
func (m *mockFilesService) CreateFile(file *drive.File, media io.Reader) (*drive.File, error) {
	return &drive.File{
		Id:   "mock-file-id",
		Name: file.Name,
	}, nil
}

// Add a helper function to create test config
func newTestConfig() *Config {
	return &Config{
		LineChannelSecret:   "test-secret",
		LineChannelToken:    "test-token",
		GoogleCredentials:   "test-creds.json",
		GoogleDriveFolderID: "test-folder-id",
		AdminUsers:          []string{"admin1", "admin2"},
	}
}

// Update mockBlobAPI to match the interface
type mockBlobAPI struct {
	content []byte
}

func (m *mockBlobAPI) GetMessageContent(messageID string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.content)), nil
}

func TestHandleFileMessage(t *testing.T) {
	// Create some mock content
	mockContent := []byte("fake file content")

	// Save the original function so we can restore it
	originalNewBlobAPI := NewBlobAPI
	defer func() { NewBlobAPI = originalNewBlobAPI }()

	// Override NewBlobAPI to return our mock
	NewBlobAPI = func(channelToken string) (BlobAPI, error) {
		return &mockBlobAPI{content: mockContent}, nil
	}

	tests := []struct {
		name        string
		message     webhook.MessageContentInterface
		fileExt     string
		shouldError bool
	}{
		{
			name: "Image message",
			message: webhook.ImageMessageContent{
				Id: "test-image-id",
			},
			fileExt:     ".jpg",
			shouldError: false,
		},
		{
			name: "Video message",
			message: webhook.VideoMessageContent{
				Id: "test-video-id",
			},
			fileExt:     ".mp4",
			shouldError: false,
		},
		{
			name: "Audio message",
			message: webhook.AudioMessageContent{
				Id: "test-audio-id",
			},
			fileExt:     ".m4a",
			shouldError: false,
		},
		{
			name: "File message",
			message: webhook.FileMessageContent{
				Id:       "test-file-id",
				FileName: "test.pdf",
			},
			fileExt:     ".pdf",
			shouldError: false,
		},
		{
			name:        "Unsupported message type",
			message:     webhook.LocationMessageContent{},
			fileExt:     "",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock dependencies
			bot := &messaging_api.MessagingApiAPI{} // Use real type but mock the calls
			driveService := newMockDriveService()
			messageCache := NewMessageCache()
			config := &Config{
				LineChannelToken:    "mock-token",
				GoogleDriveFolderID: "mock-folder",
			}
			replyToken := "test-reply-token"

			// Create temporary test file
			tmpContent := []byte("test content")
			tmpfile, err := os.CreateTemp("", "test")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(tmpfile.Name())
			if _, err := tmpfile.Write(tmpContent); err != nil {
				t.Fatal(err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatal(err)
			}

			// Call handleFileMessage
			err = handleFileMessage(bot, driveService, tt.message, tt.fileExt, replyToken,
				messageCache, config.GoogleDriveFolderID, config)

			// Verify results
			if tt.shouldError {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Get messageID based on message type
			var messageID string
			switch m := tt.message.(type) {
			case webhook.ImageMessageContent:
				messageID = m.Id
			case webhook.VideoMessageContent:
				messageID = m.Id
			case webhook.AudioMessageContent:
				messageID = m.Id
			case webhook.FileMessageContent:
				messageID = m.Id
			}

			if !messageCache.IsProcessed(messageID) {
				t.Errorf("message %s should have been processed", messageID)
			}
		})
	}
}

// Add a test for sendMessage
func TestSendMessage(t *testing.T) {
	bot := newMockBot()
	replyToken := "test-reply-token"
	message := "Test message"

	// This should not panic
	sendMessage(bot, replyToken, message)
}

// Replace the mockBotWithoutAPI implementation with this simpler version
type mockBotWithoutAPI struct {
	sentMessages []string
}

func (m *mockBotWithoutAPI) ReplyMessage(request *messaging_api.ReplyMessageRequest) (*messaging_api.ReplyMessageResponse, error) {
	for _, msg := range request.Messages {
		if textMsg, ok := msg.(*messaging_api.TextMessage); ok {
			m.sentMessages = append(m.sentMessages, textMsg.Text)
		}
	}
	return &messaging_api.ReplyMessageResponse{}, nil
}

func TestStatsTracking(t *testing.T) {
	// Create dependencies
	groupCache := NewGroupCache()
	bot := newMockBot()

	// Test cases for different upload scenarios
	tests := []struct {
		name    string
		uploads []struct {
			groupID  string
			fileName string
		}
		checkStats []struct {
			groupID     string // empty for direct message stats
			wantUploads int
			wantFiles   []string
		}
	}{
		{
			name: "Group and direct uploads",
			uploads: []struct {
				groupID  string
				fileName string
			}{
				{groupID: "group1", fileName: "group1_file1.jpg"},
				{groupID: "group1", fileName: "group1_file2.jpg"},
				{groupID: "group2", fileName: "group2_file1.jpg"},
				{groupID: "", fileName: "direct_file1.jpg"}, // direct message
				{groupID: "", fileName: "direct_file2.jpg"}, // direct message
			},
			checkStats: []struct {
				groupID     string
				wantUploads int
				wantFiles   []string
			}{
				// Check group1 stats
				{
					groupID:     "group1",
					wantUploads: 2,
					wantFiles:   []string{"group1_file2.jpg", "group1_file1.jpg"},
				},
				// Check group2 stats
				{
					groupID:     "group2",
					wantUploads: 1,
					wantFiles:   []string{"group2_file1.jpg"},
				},
				// Check direct message stats
				{
					groupID:     "direct",
					wantUploads: 2,
					wantFiles:   []string{"direct_file2.jpg", "direct_file1.jpg"},
				},
				// Check global stats (empty groupID)
				{
					groupID:     "",
					wantUploads: 5, // total of all uploads
					wantFiles:   []string{"direct_file2.jpg", "direct_file1.jpg", "group2_file1.jpg", "group1_file2.jpg", "group1_file1.jpg"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate uploads
			for _, upload := range tt.uploads {
				trackingGroupID := upload.groupID
				if trackingGroupID == "" {
					trackingGroupID = "direct"
				}
				groupCache.AddUploadedFile(trackingGroupID, upload.fileName)
			}

			// Check stats for each scenario
			for _, check := range tt.checkStats {
				// Call /stats command
				handleCommand(bot, "/stats", check.groupID, "test-reply-token", groupCache)

				// Get the last sent message
				if len(bot.sentMessages) == 0 {
					t.Fatal("No message was sent")
				}
				lastMessage := bot.sentMessages[len(bot.sentMessages)-1]

				// Verify the message contains expected information
				if !strings.Contains(lastMessage, fmt.Sprintf("Total uploads: %d", check.wantUploads)) {
					t.Errorf("Expected %d uploads, message was: %s", check.wantUploads, lastMessage)
				}

				// Check that all expected files are mentioned in the message
				for _, fileName := range check.wantFiles {
					if !strings.Contains(lastMessage, fileName) {
						t.Errorf("Expected file %s not found in message: %s", fileName, lastMessage)
					}
				}

				// Clear the bot's messages for the next check
				bot.sentMessages = nil
			}
		})
	}
}
