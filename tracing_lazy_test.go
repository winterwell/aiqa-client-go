package aiqa

import (
	"os"
	"testing"
)

func TestGetAIQAClient_LazyInitialization(t *testing.T) {
	// Save original env vars
	originalServerURL := os.Getenv("AIQA_SERVER_URL")
	originalAPIKey := os.Getenv("AIQA_API_KEY")
	defer func() {
		if originalServerURL != "" {
			os.Setenv("AIQA_SERVER_URL", originalServerURL)
		} else {
			os.Unsetenv("AIQA_SERVER_URL")
		}
		if originalAPIKey != "" {
			os.Setenv("AIQA_API_KEY", originalAPIKey)
		} else {
			os.Unsetenv("AIQA_API_KEY")
		}
		// Reset initialization state
		initialized = false
	}()

	// Clear env vars
	os.Unsetenv("AIQA_SERVER_URL")
	os.Unsetenv("AIQA_API_KEY")

	// Reset initialization state
	initialized = false

	// Call GetAIQAClient - should initialize lazily
	err := GetAIQAClient()
	if err != nil {
		t.Fatalf("GetAIQAClient should not return error: %v", err)
	}

	// Should be initialized now
	if !initialized {
		t.Error("GetAIQAClient should set initialized to true")
	}

	// Call again - should be idempotent
	err = GetAIQAClient()
	if err != nil {
		t.Fatalf("GetAIQAClient should be idempotent: %v", err)
	}
}
