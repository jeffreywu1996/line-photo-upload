package main

import (
	"os"
	"testing"
	"time"
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
