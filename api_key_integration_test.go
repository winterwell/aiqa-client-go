package aiqa

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/joho/godotenv"
)

func init() {
	// Load .env file if it exists (optional - won't error if missing)
	// This allows local development with .env files
	_ = godotenv.Load()
}

// getAPIKeyIDFromList gets an API key ID by listing API keys.
// Requires AIQA_ORGANISATION_ID to be set in environment.
// Returns the first API key ID found, or empty string if none found or organisation ID not set.
func getAPIKeyIDFromList(ctx context.Context) string {
	organisationID := os.Getenv("AIQA_ORGANISATION_ID")
	if organisationID == "" {
		return ""
	}

	serverURL := GetServerURL("")
	apiKey := GetAPIKey("")

	url := fmt.Sprintf("%s/api-key?organisation=%s", serverURL, organisationID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return ""
	}

	headers := BuildHeaders(apiKey)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var apiKeys []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&apiKeys); err != nil {
		return ""
	}

	if len(apiKeys) > 0 {
		if id, ok := apiKeys[0]["id"].(string); ok {
			return id
		}
	}

	return ""
}

// checkServerAvailable checks if the server is available and skips the test if not
func checkServerAvailableForAPIKey(t *testing.T) {
	serverURL := os.Getenv("AIQA_SERVER_URL")
	apiKey := os.Getenv("AIQA_API_KEY")

	if serverURL == "" || apiKey == "" {
		t.Skip("AIQA_SERVER_URL and AIQA_API_KEY environment variables must be set")
	}

	// Try to connect to server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/span", serverURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		t.Skipf("Cannot create request: %v", err)
	}

	params := req.URL.Query()
	params.Set("q", "name:nonexistent")
	params.Set("limit", "1")
	req.URL.RawQuery = params.Encode()

	headers := BuildHeaders(apiKey)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("Cannot connect to server: %v", err)
	}
	defer resp.Body.Close()

	// 200 means server is up (even if no results)
	// 401/403 means server is up but auth failed
	// Other errors might mean server is down
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Skipf("Server appears to be down (status %d)", resp.StatusCode)
	}
}

func TestIntegration_GetAPIKeyInfo(t *testing.T) {
	checkServerAvailableForAPIKey(t)

	// Get an API key ID to test with
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	apiKeyID := getAPIKeyIDFromList(ctx)
	if apiKeyID == "" {
		t.Skip("AIQA_ORGANISATION_ID must be set to test GetAPIKeyInfo, or no API keys found")
	}

	// Test GetAPIKeyInfo - it should load serverURL and apiKey from .env (via GetServerURL and GetAPIKey)
	result, err := GetAPIKeyInfo(ctx, apiKeyID, "", "")
	if err != nil {
		t.Fatalf("GetAPIKeyInfo failed: %v", err)
	}

	// Verify the response structure
	if result == nil {
		t.Fatal("API key info should not be nil")
	}

	id, ok := result["id"].(string)
	if !ok {
		t.Fatal("API key info should have 'id' field")
	}
	if id != apiKeyID {
		t.Errorf("API key ID should match: expected %s, got %s", apiKeyID, id)
	}

	// Verify other expected fields
	if _, ok := result["organisation"].(string); !ok {
		t.Error("API key info should have 'organisation' field")
	}

	role, ok := result["role"].(string)
	if !ok {
		t.Error("API key info should have 'role' field")
	}
	if role != "trace" && role != "developer" && role != "admin" {
		t.Errorf("API key role should be valid, got %s", role)
	}
}
