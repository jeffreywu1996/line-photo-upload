package main

import (
	"fmt"
	"io"
	"os"
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
		got      string
		want     string
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
	uploads, lastUpload := cache.GetStats(groupID)
	if uploads != 0 {
		t.Errorf("Initial uploads = %d, want 0", uploads)
	}
	if !lastUpload.IsZero() {
		t.Error("Initial lastUpload should be zero time")
	}

	// Test increment
	cache.IncrementUploads(groupID)
	uploads, lastUpload = cache.GetStats(groupID)
	if uploads != 1 {
		t.Errorf("Uploads after increment = %d, want 1", uploads)
	}
	if lastUpload.IsZero() {
		t.Error("LastUpload should not be zero after increment")
	}

	// Test multiple increments
	cache.IncrementUploads(groupID)
	uploads, _ = cache.GetStats(groupID)
	if uploads != 2 {
		t.Errorf("Uploads after second increment = %d, want 2", uploads)
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
	// Create a mock bot that doesn't make real API calls
	mockBot := &mockBotWithoutAPI{
		sentMessages: make([]string, 0),
	}
	groupCache := NewGroupCache()
	groupID := "test-group-123"
	replyToken := "test-reply-token"

	// Add some test data
	groupCache.IncrementUploads(groupID)
	time.Sleep(time.Millisecond)
	groupCache.IncrementUploads(groupID)

	tests := []struct {
		name          string
		command       string
		groupID       string // Add groupID to test both group and direct messages
		expectMessage bool
	}{
		{
			name:          "Help command",
			command:       "/help",
			groupID:       groupID,
			expectMessage: true,
		},
		{
			name:          "Stats command in group",
			command:       "/stats",
			groupID:       groupID,
			expectMessage: true,
		},
		{
			name:          "Stats command in direct message",
			command:       "/stats",
			groupID:       "", // Empty groupID for direct message
			expectMessage: true,
		},
		{
			name:          "Upload command",
			command:       "/upload",
			groupID:       groupID,
			expectMessage: true,
		},
		{
			name:          "Invalid command",
			command:       "/invalid",
			groupID:       groupID,
			expectMessage: true, // Now returns "Unknown command" message
		},
		{
			name:          "Non-command message",
			command:       "hello",
			groupID:       groupID,
			expectMessage: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockBot.sentMessages = make([]string, 0) // Reset messages
			handleCommand(mockBot, tt.command, tt.groupID, replyToken, groupCache)

			if tt.expectMessage && len(mockBot.sentMessages) == 0 {
				t.Error("Expected a message to be sent, but none was sent")
			}
			if !tt.expectMessage && len(mockBot.sentMessages) > 0 {
				t.Error("Expected no message to be sent, but one was sent")
			}

			// Additional checks for specific commands
			if tt.command == "/stats" && tt.groupID == "" {
				// Verify stats command in direct message shows the correct error
				if len(mockBot.sentMessages) > 0 && mockBot.sentMessages[0] != "Stats command is only available in groups" {
					t.Error("Expected 'Stats command is only available in groups' message")
				}
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
	*messaging_api.MessagingApiAPI
}

func newMockBot() *messaging_api.MessagingApiAPI {
	// Create a properly initialized mock bot
	bot, err := messaging_api.NewMessagingApiAPI("mock-token")
	if err != nil {
		// In a test environment, this shouldn't fail
		panic(fmt.Sprintf("Failed to create mock bot: %v", err))
	}
	return bot
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

func TestHandleFileMessage(t *testing.T) {
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
			bot := newMockBot()
			driveService := newMockDriveService()
			messageCache := NewMessageCache()
			config := newTestConfig()
			replyToken := "test-reply-token"

			// Call handleFileMessage with all required parameters
			handleFileMessage(bot, driveService, tt.message, tt.fileExt, replyToken,
				messageCache, config.GoogleDriveFolderID, config)

			// Verify message was processed (or not) as expected
			messageID := ""
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

			if !tt.shouldError && !messageCache.IsProcessed(messageID) {
				t.Errorf("message %s should have been processed", messageID)
			}
			if tt.shouldError && messageCache.IsProcessed(messageID) {
				t.Errorf("message %s should not have been processed", messageID)
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
