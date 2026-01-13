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

func TestWithTracing_LazyInitialization(t *testing.T) {
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

	// Create a traced function - should trigger lazy initialization
	testFunc := func(x int) int {
		return x * 2
	}

	tracedFunc := WithTracing(testFunc, TracingOptions{Name: "test_func"})

	// Call the traced function - should initialize tracing lazily
	result := tracedFunc.(func(int) int)(5)

	if result != 10 {
		t.Errorf("Expected result 10, got %d", result)
	}

	// Should be initialized now
	if !initialized {
		t.Error("WithTracing should trigger lazy initialization")
	}
}

