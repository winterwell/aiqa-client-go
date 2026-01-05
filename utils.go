package aiqa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// BuildHeaders builds HTTP headers for AIQA API requests
func BuildHeaders(apiKey string) map[string]string {
	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if apiKey != "" {
		headers["Authorization"] = fmt.Sprintf("ApiKey %s", apiKey)
	} else if envKey := os.Getenv("AIQA_API_KEY"); envKey != "" {
		headers["Authorization"] = fmt.Sprintf("ApiKey %s", envKey)
	}
	return headers
}

// GetServerURL gets server URL from parameter or environment variable, with trailing slashes removed
func GetServerURL(serverURL string) string {
	if serverURL == "" {
		serverURL = os.Getenv("AIQA_SERVER_URL")
	}
	// Remove all trailing slashes
	return strings.TrimRight(serverURL, "/")
}

// GetAPIKey gets API key from parameter or environment variable
func GetAPIKey(apiKey string) string {
	if apiKey == "" {
		apiKey = os.Getenv("AIQA_API_KEY")
	}
	return apiKey
}

// HTTPClient is a shared HTTP client with timeout
// Using 5 second timeout to balance between production needs and test speed
var defaultHTTPClient = &http.Client{Timeout: 5 * time.Second}

// makeRequest performs an HTTP request with common error handling
func makeRequest(ctx context.Context, method, url string, body interface{}, apiKey string) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	headers := BuildHeaders(apiKey)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	return resp, nil
}

// parseJSONResponse parses a JSON response body into the provided struct
func parseJSONResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed: %d %s - %s", resp.StatusCode, resp.Status, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}
