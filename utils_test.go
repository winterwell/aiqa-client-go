package aiqa

import (
	"os"
	"testing"
)

func TestBuildHeaders(t *testing.T) {
	// Save original env var
	originalAPIKey := os.Getenv("AIQA_API_KEY")
	defer func() {
		if originalAPIKey != "" {
			os.Setenv("AIQA_API_KEY", originalAPIKey)
		} else {
			os.Unsetenv("AIQA_API_KEY")
		}
	}()

	// Test with provided API key
	headers := BuildHeaders("test-key-123")
	if headers["Content-Type"] != "application/json" {
		t.Errorf("Expected Content-Type to be 'application/json', got %q", headers["Content-Type"])
	}
	expectedAuth := "ApiKey test-key-123"
	if headers["Authorization"] != expectedAuth {
		t.Errorf("Expected Authorization to be %q, got %q", expectedAuth, headers["Authorization"])
	}

	// Test with empty API key but env var set
	os.Setenv("AIQA_API_KEY", "env-key-456")
	headers = BuildHeaders("")
	if headers["Authorization"] != "ApiKey env-key-456" {
		t.Errorf("Expected Authorization from env var, got %q", headers["Authorization"])
	}

	// Test with no API key
	os.Unsetenv("AIQA_API_KEY")
	headers = BuildHeaders("")
	if _, ok := headers["Authorization"]; ok {
		t.Error("Expected no Authorization header when no API key is set")
	}
	if headers["Content-Type"] != "application/json" {
		t.Errorf("Expected Content-Type to always be present, got %q", headers["Content-Type"])
	}
}

func TestGetServerURL(t *testing.T) {
	// Save original env var
	originalServerURL := os.Getenv("AIQA_SERVER_URL")
	defer func() {
		if originalServerURL != "" {
			os.Setenv("AIQA_SERVER_URL", originalServerURL)
		} else {
			os.Unsetenv("AIQA_SERVER_URL")
		}
	}()

	// Test with provided URL
	result := GetServerURL("http://localhost:3000")
	if result != "http://localhost:3000" {
		t.Errorf("Expected 'http://localhost:3000', got %q", result)
	}

	// Test with trailing slash
	result = GetServerURL("http://localhost:3000/")
	if result != "http://localhost:3000" {
		t.Errorf("Expected trailing slash to be removed, got %q", result)
	}

	// Test with multiple trailing slashes
	result = GetServerURL("http://localhost:3000///")
	if result != "http://localhost:3000" {
		t.Errorf("Expected multiple trailing slashes to be removed, got %q", result)
	}

	// Test with empty string, should use env var
	os.Setenv("AIQA_SERVER_URL", "http://env-server:4000")
	result = GetServerURL("")
	if result != "http://env-server:4000" {
		t.Errorf("Expected URL from env var, got %q", result)
	}

	// Test with empty string and env var with trailing slash
	os.Setenv("AIQA_SERVER_URL", "http://env-server:4000/")
	result = GetServerURL("")
	if result != "http://env-server:4000" {
		t.Errorf("Expected trailing slash to be removed from env var, got %q", result)
	}

	// Test with no env var
	os.Unsetenv("AIQA_SERVER_URL")
	result = GetServerURL("")
	if result != "" {
		t.Errorf("Expected empty string when no URL is set, got %q", result)
	}
}

func TestGetAPIKey(t *testing.T) {
	// Save original env var
	originalAPIKey := os.Getenv("AIQA_API_KEY")
	defer func() {
		if originalAPIKey != "" {
			os.Setenv("AIQA_API_KEY", originalAPIKey)
		} else {
			os.Unsetenv("AIQA_API_KEY")
		}
	}()

	// Test with provided API key
	result := GetAPIKey("test-key-123")
	if result != "test-key-123" {
		t.Errorf("Expected 'test-key-123', got %q", result)
	}

	// Test with empty string, should use env var
	os.Setenv("AIQA_API_KEY", "env-key-456")
	result = GetAPIKey("")
	if result != "env-key-456" {
		t.Errorf("Expected API key from env var, got %q", result)
	}

	// Test with no env var
	os.Unsetenv("AIQA_API_KEY")
	result = GetAPIKey("")
	if result != "" {
		t.Errorf("Expected empty string when no API key is set, got %q", result)
	}

	// Test that provided key takes precedence over env var
	os.Setenv("AIQA_API_KEY", "env-key-456")
	result = GetAPIKey("provided-key-789")
	if result != "provided-key-789" {
		t.Errorf("Expected provided key to take precedence, got %q", result)
	}
}

func TestBuildHeadersEdgeCases(t *testing.T) {
	// Save original env var
	originalAPIKey := os.Getenv("AIQA_API_KEY")
	defer func() {
		if originalAPIKey != "" {
			os.Setenv("AIQA_API_KEY", originalAPIKey)
		} else {
			os.Unsetenv("AIQA_API_KEY")
		}
	}()

	// Test with special characters in API key
	specialKey := "key-with-special-chars-!@#$%^&*()"
	headers := BuildHeaders(specialKey)
	expectedAuth := "ApiKey " + specialKey
	if headers["Authorization"] != expectedAuth {
		t.Errorf("Expected Authorization with special chars, got %q", headers["Authorization"])
	}

	// Test with very long API key
	longKey := "key-" + string(make([]byte, 1000))
	headers = BuildHeaders(longKey)
	if len(headers["Authorization"]) != len("ApiKey ")+len(longKey) {
		t.Errorf("Expected Authorization header to handle long key")
	}
}

func TestGetServerURLEdgeCases(t *testing.T) {
	// Save original env var
	originalServerURL := os.Getenv("AIQA_SERVER_URL")
	defer func() {
		if originalServerURL != "" {
			os.Setenv("AIQA_SERVER_URL", originalServerURL)
		} else {
			os.Unsetenv("AIQA_SERVER_URL")
		}
	}()

	// Test with URL containing path
	result := GetServerURL("http://localhost:3000/api/v1/")
	if result != "http://localhost:3000/api/v1" {
		t.Errorf("Expected path to be preserved, got %q", result)
	}

	// Test with URL containing query params
	result = GetServerURL("http://localhost:3000?param=value")
	if result != "http://localhost:3000?param=value" {
		t.Errorf("Expected query params to be preserved, got %q", result)
	}

	// Test with HTTPS
	result = GetServerURL("https://api.example.com/")
	if result != "https://api.example.com" {
		t.Errorf("Expected HTTPS URL, got %q", result)
	}

	// Test with just domain
	result = GetServerURL("example.com/")
	if result != "example.com" {
		t.Errorf("Expected domain without trailing slash, got %q", result)
	}
}

func TestGetAPIKeyEdgeCases(t *testing.T) {
	// Save original env var
	originalAPIKey := os.Getenv("AIQA_API_KEY")
	defer func() {
		if originalAPIKey != "" {
			os.Setenv("AIQA_API_KEY", originalAPIKey)
		} else {
			os.Unsetenv("AIQA_API_KEY")
		}
	}()

	// Test with whitespace
	result := GetAPIKey("  test-key  ")
	if result != "  test-key  " {
		t.Errorf("Expected whitespace to be preserved, got %q", result)
	}

	// Test with empty env var
	os.Setenv("AIQA_API_KEY", "")
	result = GetAPIKey("")
	if result != "" {
		t.Errorf("Expected empty string for empty env var, got %q", result)
	}
}

func TestUtilsIntegration(t *testing.T) {
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
	}()

	// Test integration: BuildHeaders with GetAPIKey
	os.Setenv("AIQA_API_KEY", "integration-test-key")
	apiKey := GetAPIKey("")
	headers := BuildHeaders(apiKey)
	if headers["Authorization"] != "ApiKey integration-test-key" {
		t.Errorf("Expected integration to work, got %q", headers["Authorization"])
	}

	// Test integration: GetServerURL with trailing slash handling
	serverURL := GetServerURL("http://test-server:3000/")
	if serverURL != "http://test-server:3000" {
		t.Errorf("Expected server URL without trailing slash, got %q", serverURL)
	}
}

