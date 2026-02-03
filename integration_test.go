package aiqa

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

func init() {
	// Load .env file if it exists (optional - won't error if missing)
	// This allows local development with .env files
	_ = godotenv.Load()
}

const testMarkerPrefix = "integration_test_"

// getTestMarker generates a unique test marker for this test run
func getTestMarker() string {
	return fmt.Sprintf("%s%s", testMarkerPrefix, uuid.New().String()[:8])
}

// querySpansResponse represents the response from querying spans
type querySpansResponse struct {
	Hits  []map[string]interface{} `json:"hits"`
	Total int                      `json:"total"`
}

// querySpans queries spans from the server API
func querySpans(ctx context.Context, query string, limit int) (*querySpansResponse, error) {
	serverURL := GetServerURL("")
	apiKey := GetAPIKey("")

	url := fmt.Sprintf("%s/span", serverURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	params := req.URL.Query()
	params.Set("q", query)
	params.Set("limit", fmt.Sprintf("%d", limit))
	params.Set("fields", "*")
	req.URL.RawQuery = params.Encode()

	headers := BuildHeaders(apiKey)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query spans: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to query spans: %d - %s", resp.StatusCode, resp.Status)
	}

	var result querySpansResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// waitForSpans waits for spans to appear in the server, with retry logic
func waitForSpans(ctx context.Context, query string, expectedCount int, maxWaitSeconds int, pollInterval time.Duration) ([]map[string]interface{}, error) {
	startTime := time.Now()
	for time.Since(startTime) < time.Duration(maxWaitSeconds)*time.Second {
		result, err := querySpans(ctx, query, 100)
		if err != nil {
			return nil, err
		}

		if len(result.Hits) >= expectedCount {
			return result.Hits, nil
		}

		select {
		case <-ctx.Done():
			return result.Hits, ctx.Err()
		case <-time.After(pollInterval):
			// Continue polling
		}
	}

	// Return whatever we have after timeout
	result, err := querySpans(ctx, query, 100)
	if err != nil {
		return nil, err
	}
	return result.Hits, nil
}

// checkServerAvailable checks if the server is available and skips the test if not
func checkServerAvailable(t *testing.T) {
	serverURL := os.Getenv("AIQA_SERVER_URL")
	apiKey := os.Getenv("AIQA_API_KEY")

	if serverURL == "" || apiKey == "" {
		t.Skip("AIQA_SERVER_URL and AIQA_API_KEY environment variables must be set")
	}

	// Try to connect to server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/span", strings.TrimRight(serverURL, "/"))
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

func TestIntegration_BasicSpanGenerationAndRetrieval(t *testing.T) {
	checkServerAvailable(t)

	// Setup
	serverURL := os.Getenv("AIQA_SERVER_URL")
	apiKey := os.Getenv("AIQA_API_KEY")
	testMarker := getTestMarker()

	cleanup := setupTracingTest(t, serverURL, apiKey, 1.0)
	defer cleanup()

	// Create a traced function with a unique name
	testFunction := func(x, y int) int {
		return x + y
	}
	tracedFunction := WithTracing(testFunction, TracingOptions{
		Name: fmt.Sprintf("test_function_%s", testMarker),
	}).(func(int, int) int)

	// Call the function to generate a span
	result := tracedFunction(5, 3)
	if result != 8 {
		t.Errorf("Expected result 8, got %d", result)
	}

	// Flush spans to ensure they're sent
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := FlushSpans(ctx); err != nil {
		t.Fatalf("Failed to flush spans: %v", err)
	}

	// Wait for span to appear in server
	query := fmt.Sprintf("name:test_function_%s", testMarker)
	hits, err := waitForSpans(ctx, query, 1, 10, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to wait for spans: %v", err)
	}

	if len(hits) < 1 {
		t.Fatalf("Expected at least 1 span, found %d", len(hits))
	}

	// Find our span
	span := hits[0]
	if span["name"] != fmt.Sprintf("test_function_%s", testMarker) {
		t.Errorf("Expected span name %s, got %v", fmt.Sprintf("test_function_%s", testMarker), span["name"])
	}

	// Check status code (1 = OK)
	if status, ok := span["status"].(map[string]interface{}); ok {
		if code, ok := status["code"].(float64); ok {
			if int(code) != 1 {
				t.Errorf("Expected status code 1 (OK), got %d", int(code))
			}
		}
	}

	// Check required fields
	if _, ok := span["trace"]; !ok {
		t.Error("Span missing trace")
	}
	if _, ok := span["id"]; !ok {
		t.Error("Span missing id")
	}
	if _, ok := span["start"]; !ok {
		t.Error("Span missing start")
	}
	if _, ok := span["end"]; !ok {
		t.Error("Span missing end")
	}
}

func TestIntegration_MultipleSpansGeneration(t *testing.T) {
	checkServerAvailable(t)

	// Setup
	serverURL := os.Getenv("AIQA_SERVER_URL")
	apiKey := os.Getenv("AIQA_API_KEY")
	testMarker := getTestMarker()

	cleanup := setupTracingTest(t, serverURL, apiKey, 1.0)
	defer cleanup()

	// Create multiple traced functions
	funcA := func(x int) int {
		return x * 2
	}
	funcB := func(x int) int {
		return x + 10
	}
	funcC := func(x int) int {
		return x - 5
	}

	tracedFuncA := WithTracing(funcA, TracingOptions{
		Name: fmt.Sprintf("test_func_a_%s", testMarker),
	}).(func(int) int)
	tracedFuncB := WithTracing(funcB, TracingOptions{
		Name: fmt.Sprintf("test_func_b_%s", testMarker),
	}).(func(int) int)
	tracedFuncC := WithTracing(funcC, TracingOptions{
		Name: fmt.Sprintf("test_func_c_%s", testMarker),
	}).(func(int) int)

	// Call all functions
	if tracedFuncA(5) != 10 {
		t.Error("funcA(5) should return 10")
	}
	if tracedFuncB(5) != 15 {
		t.Error("funcB(5) should return 15")
	}
	if tracedFuncC(5) != 0 {
		t.Error("funcC(5) should return 0")
	}

	// Flush spans
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := FlushSpans(ctx); err != nil {
		t.Fatalf("Failed to flush spans: %v", err)
	}

	// Query for all spans with our test marker using OR query
	query := fmt.Sprintf("name:test_func_a_%s OR name:test_func_b_%s OR name:test_func_c_%s",
		testMarker, testMarker, testMarker)
	hits, err := waitForSpans(ctx, query, 3, 10, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to wait for spans: %v", err)
	}

	if len(hits) < 3 {
		t.Fatalf("Expected at least 3 spans, found %d", len(hits))
	}

	// Verify we have all three spans
	spanNames := make(map[string]bool)
	for _, hit := range hits {
		if name, ok := hit["name"].(string); ok {
			if strings.Contains(name, testMarker) {
				spanNames[name] = true
			}
		}
	}

	expectedNames := map[string]bool{
		fmt.Sprintf("test_func_a_%s", testMarker): true,
		fmt.Sprintf("test_func_b_%s", testMarker): true,
		fmt.Sprintf("test_func_c_%s", testMarker): true,
	}

	for expectedName := range expectedNames {
		if !spanNames[expectedName] {
			t.Errorf("Missing span: %s. Found: %v", expectedName, spanNames)
		}
	}
}

func TestIntegration_SpanWithAttributes(t *testing.T) {
	checkServerAvailable(t)

	// Setup
	serverURL := os.Getenv("AIQA_SERVER_URL")
	apiKey := os.Getenv("AIQA_API_KEY")
	testMarker := getTestMarker()

	cleanup := setupTracingTest(t, serverURL, apiKey, 1.0)
	defer cleanup()

	// Create a function that will have input/output attributes
	functionWithAttrs := func(data map[string]interface{}) map[string]interface{} {
		value, _ := data["value"].(float64)
		return map[string]interface{}{
			"result":    value * 2,
			"processed": true,
		}
	}
	tracedFunction := WithTracing(functionWithAttrs, TracingOptions{
		Name: fmt.Sprintf("test_attrs_%s", testMarker),
	}).(func(map[string]interface{}) map[string]interface{})

	// Call with specific input
	inputData := map[string]interface{}{
		"value":       42.0,
		"test_marker": testMarker,
	}
	output := tracedFunction(inputData)
	if output["result"] != 84.0 {
		t.Errorf("Expected result 84.0, got %v", output["result"])
	}

	// Flush spans
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := FlushSpans(ctx); err != nil {
		t.Fatalf("Failed to flush spans: %v", err)
	}

	// Query for our span
	query := fmt.Sprintf("name:test_attrs_%s", testMarker)
	hits, err := waitForSpans(ctx, query, 1, 10, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to wait for spans: %v", err)
	}

	if len(hits) < 1 {
		t.Fatalf("Expected at least 1 span, found %d", len(hits))
	}

	span := hits[0]
	if span["name"] != fmt.Sprintf("test_attrs_%s", testMarker) {
		t.Errorf("Expected span name %s, got %v", fmt.Sprintf("test_attrs_%s", testMarker), span["name"])
	}

	// Verify attributes are present (they may be in attributes or unindexed_attributes)
	attributes := make(map[string]interface{})
	if attrs, ok := span["attributes"].(map[string]interface{}); ok {
		for k, v := range attrs {
			attributes[k] = v
		}
	}
	if unindexedAttrs, ok := span["unindexed_attributes"].(map[string]interface{}); ok {
		for k, v := range unindexedAttrs {
			attributes[k] = v
		}
	}

	// Check that input or output are captured (exact structure may vary)
	if _, hasInput := attributes["input"]; !hasInput {
		if _, hasOutput := attributes["output"]; !hasOutput {
			t.Error("Span should have input or output attributes")
		}
	}
}

func TestIntegration_SpanStatusCode(t *testing.T) {
	checkServerAvailable(t)

	// Setup
	serverURL := os.Getenv("AIQA_SERVER_URL")
	apiKey := os.Getenv("AIQA_API_KEY")
	testMarker := getTestMarker()

	cleanup := setupTracingTest(t, serverURL, apiKey, 1.0)
	defer cleanup()

	// Create a function that succeeds
	successfulFunction := func() string {
		return "success"
	}
	tracedSuccess := WithTracing(successfulFunction, TracingOptions{
		Name: fmt.Sprintf("test_success_%s", testMarker),
	}).(func() string)

	// Create a function that returns an error (should have ERROR status)
	errorFunction := func() (string, error) {
		return "", fmt.Errorf("test error")
	}
	tracedError := WithTracing(errorFunction, TracingOptions{
		Name: fmt.Sprintf("test_error_%s", testMarker),
	}).(func() (string, error))

	// Call successful function
	if tracedSuccess() != "success" {
		t.Error("successfulFunction() should return 'success'")
	}

	// Call error function (should return error)
	_, err := tracedError()
	if err == nil {
		t.Error("errorFunction() should return an error")
	}

	// Flush spans
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := FlushSpans(ctx); err != nil {
		t.Fatalf("Failed to flush spans: %v", err)
	}

	// Query for both spans using OR query
	query := fmt.Sprintf("name:test_success_%s OR name:test_error_%s", testMarker, testMarker)
	hits, err := waitForSpans(ctx, query, 2, 10, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to wait for spans: %v", err)
	}

	if len(hits) < 2 {
		t.Fatalf("Expected at least 2 spans, found %d", len(hits))
	}

	// Find the spans
	var successSpan, errorSpan map[string]interface{}
	for _, hit := range hits {
		if name, ok := hit["name"].(string); ok {
			if name == fmt.Sprintf("test_success_%s", testMarker) {
				successSpan = hit
			} else if name == fmt.Sprintf("test_error_%s", testMarker) {
				errorSpan = hit
			}
		}
	}

	if successSpan == nil {
		t.Fatal("Success span not found")
	}
	if errorSpan == nil {
		t.Fatal("Error span not found")
	}

	// Verify status codes
	if status, ok := successSpan["status"].(map[string]interface{}); ok {
		if code, ok := status["code"].(float64); ok {
			if int(code) != 1 {
				t.Errorf("Success span should have OK status (1), got %d", int(code))
			}
		}
	}

	if status, ok := errorSpan["status"].(map[string]interface{}); ok {
		if code, ok := status["code"].(float64); ok {
			if int(code) != 2 {
				t.Errorf("Error span should have ERROR status (2), got %d", int(code))
			}
		}
	}
}
