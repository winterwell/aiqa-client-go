package aiqa

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// debugResponseBody logs response metadata and a safe preview of body when AIQA_DEBUG=1.
// Non-printable bytes are shown as \xNN so escape sequences (e.g. \\x1b) are visible.
func debugResponseBody(resp *http.Response, body []byte, decodeErr error) {
	if os.Getenv("AIQA_DEBUG") == "" {
		return
	}
	url := ""
	if resp.Request != nil && resp.Request.URL != nil {
		url = resp.Request.URL.String()
	}
	const previewLen = 600
	preview := body
	if len(preview) > previewLen {
		preview = preview[:previewLen]
	}
	var sb strings.Builder
	for _, b := range preview {
		if b >= 32 && b < 127 && b != '\\' {
			sb.WriteByte(b)
		} else {
			sb.WriteString(fmt.Sprintf("\\x%02x", b))
		}
	}
	if len(body) > previewLen {
		sb.WriteString("...")
	}
	ct := resp.Header.Get("Content-Type")
	fmt.Fprintf(os.Stderr, "AIQA: DEBUG parseJSONResponse failed url=%q status=%d content_type=%q body_len=%d decode_err=%v\n",
		url, resp.StatusCode, ct, len(body), decodeErr)
	fmt.Fprintf(os.Stderr, "AIQA: DEBUG response body preview: %s\n", sb.String())
}

// BuildHeaders builds HTTP headers for AIQA API requests
// Note: net/http automatically handles gzip/deflate decompression when Accept-Encoding header is set
// Checks AIQA_API_KEY first, then falls back to OTEL_EXPORTER_OTLP_HEADERS if not set
func BuildHeaders(apiKey string) map[string]string {
	headers := map[string]string{
		"Content-Type":    "application/json",
		"Accept-Encoding": "gzip, deflate", // br omitted: stdlib does not decompress Brotli; server may still send gzip
	}
	
	// Check parameter first
	if apiKey != "" {
		headers["Authorization"] = fmt.Sprintf("ApiKey %s", apiKey)
		return headers
	}
	
	// Check AIQA_API_KEY env var
	if envKey := os.Getenv("AIQA_API_KEY"); envKey != "" {
		headers["Authorization"] = fmt.Sprintf("ApiKey %s", envKey)
		return headers
	}
	
	// Fallback to OTLP headers (format: "key1=value1,key2=value2")
	if otlpHeaders := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"); otlpHeaders != "" {
		// Parse comma-separated key=value pairs
		for _, headerPair := range strings.Split(otlpHeaders, ",") {
			headerPair = strings.TrimSpace(headerPair)
			if idx := strings.Index(headerPair, "="); idx > 0 {
				key := strings.TrimSpace(headerPair[:idx])
				value := strings.TrimSpace(headerPair[idx+1:])
				if strings.ToLower(key) == "authorization" {
					headers["Authorization"] = value
				} else {
					headers[key] = value
				}
			}
		}
	}
	
	return headers
}

// GetServerURL gets server URL from parameter or environment variable, with trailing slashes removed
// Checks AIQA_SERVER_URL first, then falls back to OTEL_EXPORTER_OTLP_ENDPOINT if not set
func GetServerURL(serverURL string) string {
	// Check parameter first
	if serverURL != "" {
		return strings.TrimRight(serverURL, "/")
	}
	
	// Check AIQA_SERVER_URL env var
	if url := os.Getenv("AIQA_SERVER_URL"); url != "" {
		return strings.TrimRight(url, "/")
	}
	
	// Fallback to OTLP endpoint
	if url := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); url != "" {
		return strings.TrimRight(url, "/")
	}
	
	// Default fallback
	return "https://server-aiqa.winterwell.com"
}

// GetAPIKey gets API key from parameter or environment variable
// Checks AIQA_API_KEY first, then falls back to OTEL_EXPORTER_OTLP_HEADERS if not set
func GetAPIKey(apiKey string) string {
	// Check parameter first
	if apiKey != "" {
		return apiKey
	}
	
	// Check AIQA_API_KEY env var
	if envKey := os.Getenv("AIQA_API_KEY"); envKey != "" {
		return envKey
	}
	
	// Fallback to OTLP headers (look for Authorization header)
	if otlpHeaders := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"); otlpHeaders != "" {
		for _, headerPair := range strings.Split(otlpHeaders, ",") {
			headerPair = strings.TrimSpace(headerPair)
			if idx := strings.Index(headerPair, "="); idx > 0 {
				key := strings.TrimSpace(headerPair[:idx])
				value := strings.TrimSpace(headerPair[idx+1:])
				if strings.ToLower(key) == "authorization" {
					// Extract API key from "ApiKey <key>" or just return the value
					if strings.HasPrefix(value, "ApiKey ") {
						return value[7:]
					}
					return value
				}
			}
		}
	}
	
	return ""
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

// parseJSONResponse parses a JSON response body into the provided struct.
// When decode fails, set AIQA_DEBUG=1 to log response headers and body preview (non-printables as \xNN).
func parseJSONResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("request failed: %d %s - %s", resp.StatusCode, resp.Status, string(body))
	}

	// Decompress if server sent gzip but did not set Content-Encoding (so net/http didn't decompress)
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gr.Close()
		body, err = io.ReadAll(gr)
		if err != nil {
			return fmt.Errorf("failed to decompress gzip response: %w", err)
		}
	}

	if err := json.Unmarshal(body, result); err != nil {
		debugResponseBody(resp, body, err)
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
