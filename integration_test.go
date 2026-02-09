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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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
	Hits  []map[string]any `json:"hits"`
	Total int              `json:"total"`
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
func waitForSpans(ctx context.Context, query string, expectedCount int, maxWaitSeconds int, pollInterval time.Duration) ([]map[string]any, error) {
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

	// Create one span with a unique name
	spanName := fmt.Sprintf("test_function_%s", testMarker)
	spanCtx, otelSpan := otel.Tracer(AIQATracerName).Start(context.Background(), spanName)
	otelSpan.SetAttributes(attribute.String("input", serializeValue(map[string]any{"x": 5, "y": 3})))
	result := 5 + 3
	otelSpan.SetAttributes(attribute.String("output", serializeValue(result)))
	otelSpan.SetStatus(codes.Ok, "")
	otelSpan.End()
	if GetTraceId(spanCtx) == "" {
		t.Error("Expected a valid trace id in span context")
	}
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
	if status, ok := span["status"].(map[string]any); ok {
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

	spanA := fmt.Sprintf("test_func_a_%s", testMarker)
	spanB := fmt.Sprintf("test_func_b_%s", testMarker)
	spanC := fmt.Sprintf("test_func_c_%s", testMarker)

	_, s1 := otel.Tracer(AIQATracerName).Start(context.Background(), spanA)
	resultA := 5 * 2
	s1.SetStatus(codes.Ok, "")
	s1.End()
	_, s2 := otel.Tracer(AIQATracerName).Start(context.Background(), spanB)
	resultB := 5 + 10
	s2.SetStatus(codes.Ok, "")
	s2.End()
	_, s3 := otel.Tracer(AIQATracerName).Start(context.Background(), spanC)
	resultC := 5 - 5
	s3.SetStatus(codes.Ok, "")
	s3.End()

	// Check all functions
	if resultA != 10 {
		t.Error("funcA(5) should return 10")
	}
	if resultB != 15 {
		t.Error("funcB(5) should return 15")
	}
	if resultC != 0 {
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

	spanName := fmt.Sprintf("test_attrs_%s", testMarker)
	inputData := map[string]any{
		"value":       42.0,
		"test_marker": testMarker,
	}
	_, otelSpan := otel.Tracer(AIQATracerName).Start(context.Background(), spanName)
	otelSpan.SetAttributes(attribute.String("input", serializeValue(inputData)))
	value, _ := inputData["value"].(float64)
	output := map[string]any{
		"result":    value * 2,
		"processed": true,
	}
	otelSpan.SetAttributes(attribute.String("output", serializeValue(output)))
	otelSpan.SetStatus(codes.Ok, "")
	otelSpan.End()
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
	attributes := make(map[string]any)
	if attrs, ok := span["attributes"].(map[string]any); ok {
		for k, v := range attrs {
			attributes[k] = v
		}
	}
	if unindexedAttrs, ok := span["unindexed_attributes"].(map[string]any); ok {
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

	// Create success span
	successName := fmt.Sprintf("test_success_%s", testMarker)
	_, successOtelSpan := otel.Tracer(AIQATracerName).Start(context.Background(), successName)
	successResult := "success"
	successOtelSpan.SetAttributes(attribute.String("output", serializeValue(successResult)))
	successOtelSpan.SetStatus(codes.Ok, "")
	successOtelSpan.End()
	if successResult != "success" {
		t.Error("successfulFunction() should return 'success'")
	}

	// Create error span
	errorName := fmt.Sprintf("test_error_%s", testMarker)
	_, errorOtelSpan := otel.Tracer(AIQATracerName).Start(context.Background(), errorName)
	err := fmt.Errorf("test error")
	errorOtelSpan.RecordError(err)
	errorOtelSpan.SetStatus(codes.Error, err.Error())
	errorOtelSpan.End()
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
	var successSpan, errorSpan map[string]any
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
	if status, ok := successSpan["status"].(map[string]any); ok {
		if code, ok := status["code"].(float64); ok {
			if int(code) != 1 {
				t.Errorf("Success span should have OK status (1), got %d", int(code))
			}
		}
	}

	if status, ok := errorSpan["status"].(map[string]any); ok {
		if code, ok := status["code"].(float64); ok {
			if int(code) != 2 {
				t.Errorf("Error span should have ERROR status (2), got %d", int(code))
			}
		}
	}
}
