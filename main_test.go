package main

import (
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

// Mock implementations
type mockBot struct {
	*messaging_api.MessagingApiAPI
}

func newMockBot() *messaging_api.MessagingApiAPI {
	return &messaging_api.MessagingApiAPI{}
}

type mockDriveService struct {
	files *mockFilesService
}

func newMockDriveService() *mockDriveService {
	return &mockDriveService{
		files: &mockFilesService{},
	}
}

// Add this method to implement the DriveService interface
func (m *mockDriveService) Files() FilesService {
	return m.files
}

type mockFilesService struct {
	*drive.FilesCreateCall
}

func (m *mockFilesService) Create(file *drive.File) *drive.FilesCreateCall {
	call := &drive.FilesCreateCall{}
	return call
}

// Add these methods to complete the chain
type mockFilesCreateCall struct {
	*drive.FilesCreateCall
}

func (m *mockFilesService) Media(r io.Reader) *drive.FilesCreateCall {
	return &drive.FilesCreateCall{}
}

func (m *mockFilesService) Do() (*drive.File, error) {
	return &drive.File{Id: "mock-file-id"}, nil
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
			config := &Config{}

			// Call handleFileMessage
			handleFileMessage(bot, driveService, tt.message, tt.fileExt, "reply-token", messageCache, config)

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
