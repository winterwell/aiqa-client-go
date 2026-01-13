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
// Note: net/http automatically handles gzip/deflate decompression when Accept-Encoding header is set
func BuildHeaders(apiKey string) map[string]string {
	headers := map[string]string{
		"Content-Type":    "application/json",
		"Accept-Encoding": "gzip, deflate, br", // Request compression (net/http handles decompression automatically)
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
		if serverURL == "" {
			serverURL = "https://server-aiqa.winterwell.com"
		}
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
// Note: net/http automatically handles gzip/deflate decompression when Accept-Encoding header is set
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

// FormatHTTPError formats an HTTP error message from a response object
func FormatHTTPError(resp *http.Response, operation string) string {
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	errorText := string(body)
	if errorText == "" {
		errorText = "Unknown error"
	}
	return fmt.Sprintf("Failed to %s: %d %s - %s", operation, resp.StatusCode, resp.Status, errorText)
}

// GetOrganisation gets organisation information based on API key via an API call
func GetOrganisation(ctx context.Context, organisationID string, serverURL, apiKey string) (map[string]interface{}, error) {
	url := GetServerURL(serverURL)
	key := GetAPIKey(apiKey)

	apiURL := fmt.Sprintf("%s/organisation/%s", url, organisationID)
	resp, err := makeRequest(ctx, "GET", apiURL, nil, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get organisation: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", FormatHTTPError(resp, "get organisation"))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode organisation response: %w", err)
	}

	return result, nil
}

// GetAPIKeyInfo gets API key information via an API call
func GetAPIKeyInfo(ctx context.Context, apiKeyID string, serverURL, apiKey string) (map[string]interface{}, error) {
	url := GetServerURL(serverURL)
	key := GetAPIKey(apiKey)

	apiURL := fmt.Sprintf("%s/api-key/%s", url, apiKeyID)
	resp, err := makeRequest(ctx, "GET", apiURL, nil, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get api key info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", FormatHTTPError(resp, "get api key info"))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode api key info response: %w", err)
	}

	return result, nil
}
