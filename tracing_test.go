package aiqa

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// setupTracingTest is a helper function that sets up tracing for tests and returns a cleanup function.
// It marks itself as a test helper so failures are reported at the call site.
func setupTracingTest(t *testing.T, serverURL, apiKey string, samplingRate float64) func() {
	t.Helper()

	// Save original env vars
	originalServerURL := os.Getenv("AIQA_SERVER_URL")
	originalAPIKey := os.Getenv("AIQA_API_KEY")

	// Clear env vars
	os.Unsetenv("AIQA_SERVER_URL")
	os.Unsetenv("AIQA_API_KEY")

	// Initialize tracing
	err := InitTracing(serverURL, apiKey, samplingRate)
	if err != nil {
		t.Fatalf("Failed to init tracing: %v", err)
	}

	// Return cleanup function
	return func() {
		// Restore env vars
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
		// Shutdown tracing
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ShutdownTracing(ctx)
	}
}

// restoreEnvVar is a helper to save and restore a single environment variable.
func restoreEnvVar(t *testing.T, key string) func() {
	t.Helper()
	originalValue := os.Getenv(key)
	return func() {
		if originalValue != "" {
			os.Setenv(key, originalValue)
		} else {
			os.Unsetenv(key)
		}
	}
}

func TestInitTracing_WithMissingEnvVars(t *testing.T) {
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

	// Clear env vars
	os.Unsetenv("AIQA_SERVER_URL")
	os.Unsetenv("AIQA_API_KEY")

	err := InitTracing("", "")
	if err != nil {
		t.Fatalf("InitTracing should not return error when env vars are missing: %v", err)
	}

	if IsTracingEnabled() {
		t.Error("Tracing should be disabled when env vars are missing")
	}
}

func TestInitTracing(t *testing.T) {
	tests := []struct {
		name         string
		serverURL    string
		apiKey       string
		samplingRate float64
		wantEnabled  bool
		wantErr      bool
	}{
		{
			name:         "missing env vars",
			serverURL:    "",
			apiKey:       "",
			samplingRate: 1.0,
			wantEnabled:  false,
			wantErr:      false,
		},
		{
			name:         "with provided args",
			serverURL:    "http://127.0.0.1:1", // Use invalid port to fail fast in tests
			apiKey:       "test-key",
			samplingRate: 0.5,
			wantEnabled:  true,
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
				if tt.wantEnabled {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					ShutdownTracing(ctx)
				}
			}()

			// Clear env vars
			os.Unsetenv("AIQA_SERVER_URL")
			os.Unsetenv("AIQA_API_KEY")

			err := InitTracing(tt.serverURL, tt.apiKey, tt.samplingRate)
			if (err != nil) != tt.wantErr {
				t.Errorf("InitTracing() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if IsTracingEnabled() != tt.wantEnabled {
				t.Errorf("IsTracingEnabled() = %v, want %v", IsTracingEnabled(), tt.wantEnabled)
			}
		})
	}
}

func TestInitTracing_SamplingRateClamping(t *testing.T) {
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ShutdownTracing(ctx)
	}()

	os.Unsetenv("AIQA_SERVER_URL")
	os.Unsetenv("AIQA_API_KEY")

	// Test negative sampling rate (should be clamped to 0)
	// Use invalid port to fail fast in tests
	err := InitTracing("http://127.0.0.1:1", "test-key", -1.0)
	if err != nil {
		t.Fatalf("InitTracing should succeed: %v", err)
	}

	// Test sampling rate > 1 (should be clamped to 1)
	err = InitTracing("http://127.0.0.1:1", "test-key", 2.0)
	if err != nil {
		t.Fatalf("InitTracing should succeed: %v", err)
	}
}

func TestTraceIDSampler(t *testing.T) {
	sampler := &traceIDSampler{rate: 0.5}

	// Test with rate 0
	sampler0 := &traceIDSampler{rate: 0}
	result := sampler0.ShouldSample(sdktrace.SamplingParameters{})
	if result.Decision != sdktrace.Drop {
		t.Error("Sampler with rate 0 should drop")
	}

	// Test with rate 1
	sampler1 := &traceIDSampler{rate: 1.0}
	result = sampler1.ShouldSample(sdktrace.SamplingParameters{})
	if result.Decision != sdktrace.RecordAndSample {
		t.Error("Sampler with rate 1 should record and sample")
	}

	// Test with rate 0.5 (should be deterministic)
	result = sampler.ShouldSample(sdktrace.SamplingParameters{})
	// Should be either Drop or RecordAndSample
	if result.Decision != sdktrace.Drop && result.Decision != sdktrace.RecordAndSample {
		t.Error("Sampler should return valid decision")
	}

	// Test description
	desc := sampler.Description()
	if desc == "" {
		t.Error("Description should not be empty")
	}
}

func TestIsJWTToken(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
		want  bool
	}{
		{"Valid JWT", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c", true},
		{"Invalid JWT - too few parts", "eyJ.notenough", false},
		{"Invalid JWT - too many parts", "eyJ.part1.part2.part3", false},
		{"Invalid JWT - doesn't start with eyJ", "not.eyJ.token", false},
		{"Not a string", 123, false},
		{"Empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isJWTToken(tt.value); got != tt.want {
				t.Errorf("isJWTToken() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAPIKey(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
		want  bool
	}{
		{"OpenAI key", "sk-1234567890abcdef", true},
		{"GitHub token", "ghp_1234567890abcdef", true},
		{"AWS key", "AKIAIOSFODNN7EXAMPLE", true},
		{"Not an API key", "regular-string", false},
		{"Not a string", 123, false},
		{"Empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAPIKey(tt.value); got != tt.want {
				t.Errorf("isAPIKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyDataFilters(t *testing.T) {
	tests := []struct {
		name       string
		filtersEnv string
		key        string
		value      interface{}
		want       interface{}
	}{
		{
			name:       "RemovePasswords filter",
			filtersEnv: "RemovePasswords",
			key:        "password",
			value:      "secret123",
			want:       "****",
		},
		{
			name:       "RemoveJWT filter",
			filtersEnv: "RemoveJWT",
			key:        "token",
			value:      "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgI",
			want:       "****",
		},
		{
			name:       "RemoveAuthHeaders filter",
			filtersEnv: "RemoveAuthHeaders",
			key:        "authorization",
			value:      "Bearer token123",
			want:       "****",
		},
		{
			name:       "RemoveAPIKeys filter",
			filtersEnv: "RemoveAPIKeys",
			key:        "api_key",
			value:      "sk-1234567890",
			want:       "****",
		},
		{
			name:       "filters disabled",
			filtersEnv: "false",
			key:        "password",
			value:      "secret123",
			want:       "secret123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer restoreEnvVar(t, "AIQA_DATA_FILTERS")()
			os.Setenv("AIQA_DATA_FILTERS", tt.filtersEnv)
			result := applyDataFilters(tt.key, tt.value)
			if result != tt.want {
				t.Errorf("applyDataFilters(%q, %v) = %v, want %v", tt.key, tt.value, result, tt.want)
			}
		})
	}
}

func TestFilterDataRecursive(t *testing.T) {
	// Save original filter setting
	originalFilters := os.Getenv("AIQA_DATA_FILTERS")
	defer func() {
		if originalFilters != "" {
			os.Setenv("AIQA_DATA_FILTERS", originalFilters)
		} else {
			os.Unsetenv("AIQA_DATA_FILTERS")
		}
	}()

	os.Setenv("AIQA_DATA_FILTERS", "RemovePasswords,RemoveJWT")

	// Test nested map
	data := map[string]interface{}{
		"user":     "john",
		"password": "secret",
		"token":    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgI",
		"nested": map[string]interface{}{
			"password": "nested-secret",
		},
	}

	result := filterDataRecursive(data).(map[string]interface{})
	if result["password"] != "****" {
		t.Errorf("Expected password to be filtered, got %v", result["password"])
	}
	if result["token"] != "****" {
		t.Errorf("Expected token to be filtered, got %v", result["token"])
	}
	if result["user"] != "john" {
		t.Errorf("Expected user to remain unchanged, got %v", result["user"])
	}

	nested := result["nested"].(map[string]interface{})
	if nested["password"] != "****" {
		t.Errorf("Expected nested password to be filtered, got %v", nested["password"])
	}
}

func TestSerializeValue(t *testing.T) {
	// Save original filter setting
	originalFilters := os.Getenv("AIQA_DATA_FILTERS")
	defer func() {
		if originalFilters != "" {
			os.Setenv("AIQA_DATA_FILTERS", originalFilters)
		} else {
			os.Unsetenv("AIQA_DATA_FILTERS")
		}
	}()

	os.Setenv("AIQA_DATA_FILTERS", "RemovePasswords")

	data := map[string]interface{}{
		"username": "john",
		"password": "secret",
	}

	result := serializeValue(data)
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("Failed to unmarshal serialized value: %v", err)
	}

	if decoded["password"] != "****" {
		t.Errorf("Expected password to be filtered in serialized value")
	}
}

func TestSetSpanAttribute(t *testing.T) {
	// Setup tracing
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ShutdownTracing(ctx)
	}()

	os.Unsetenv("AIQA_SERVER_URL")
	os.Unsetenv("AIQA_API_KEY")

	// Use invalid port to fail fast in tests (connection refused, not timeout)
	err := InitTracing("http://127.0.0.1:1", "test-key", 1.0)
	if err != nil {
		t.Fatalf("Failed to init tracing: %v", err)
	}

	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	// Test setting attribute
	success := SetSpanAttribute(ctx, "test.key", "test-value")
	if !success {
		t.Error("SetSpanAttribute should return true when span is recording")
	}

	// Test with no active span
	ctxNoSpan := context.Background()
	success = SetSpanAttribute(ctxNoSpan, "test.key", "test-value")
	if success {
		t.Error("SetSpanAttribute should return false when no active span")
	}
}

func TestSetComponentTag(t *testing.T) {
	originalTag := componentTag
	defer func() {
		componentTag = originalTag
	}()

	SetComponentTag("test-component")
	if componentTag != "test-component" {
		t.Errorf("Expected componentTag to be 'test-component', got '%s'", componentTag)
	}
}

func TestGetTraceId(t *testing.T) {
	// Setup tracing
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ShutdownTracing(ctx)
	}()

	os.Unsetenv("AIQA_SERVER_URL")
	os.Unsetenv("AIQA_API_KEY")

	// Use invalid port to fail fast in tests (connection refused, not timeout)
	err := InitTracing("http://127.0.0.1:1", "test-key", 1.0)
	if err != nil {
		t.Fatalf("Failed to init tracing: %v", err)
	}

	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	traceID := GetTraceId(ctx)
	if traceID == "" {
		t.Error("GetTraceId should return a trace ID when span is active")
	}
	if len(traceID) != 32 {
		t.Errorf("Trace ID should be 32 hex characters, got %d", len(traceID))
	}

	// Test with no active span
	ctxNoSpan := context.Background()
	traceID = GetTraceId(ctxNoSpan)
	if traceID != "" {
		t.Error("GetTraceId should return empty string when no active span")
	}
}

func TestGetSpanId(t *testing.T) {
	// Setup tracing
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ShutdownTracing(ctx)
	}()

	os.Unsetenv("AIQA_SERVER_URL")
	os.Unsetenv("AIQA_API_KEY")

	// Use invalid port to fail fast in tests (connection refused, not timeout)
	err := InitTracing("http://127.0.0.1:1", "test-key", 1.0)
	if err != nil {
		t.Fatalf("Failed to init tracing: %v", err)
	}

	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	spanID := GetSpanId(ctx)
	if spanID == "" {
		t.Error("GetSpanId should return a span ID when span is active")
	}
	if len(spanID) != 16 {
		t.Errorf("Span ID should be 16 hex characters, got %d", len(spanID))
	}

	// Test with no active span
	ctxNoSpan := context.Background()
	spanID = GetSpanId(ctxNoSpan)
	if spanID != "" {
		t.Error("GetSpanId should return empty string when no active span")
	}
}

func TestCreateSpanFromTraceId(t *testing.T) {
	// Setup tracing
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ShutdownTracing(ctx)
	}()

	os.Unsetenv("AIQA_SERVER_URL")
	os.Unsetenv("AIQA_API_KEY")

	// Use invalid port to fail fast in tests (connection refused, not timeout)
	err := InitTracing("http://127.0.0.1:1", "test-key", 0.0)
	if err != nil {
		t.Fatalf("Failed to init tracing: %v", err)
	}

	// Create a valid trace ID (32 hex characters)
	traceID := "12345678901234567890123456789012"
	ctx, span := CreateSpanFromTraceId(context.Background(), traceID, "", "test-span")
	defer span.End()

	if ctx == nil {
		t.Error("CreateSpanFromTraceId should return a valid context")
	}
	if span == nil {
		t.Error("CreateSpanFromTraceId should return a valid span")
	}

	// Test with invalid trace ID
	ctx2, span2 := CreateSpanFromTraceId(context.Background(), "invalid", "", "test-span")
	defer span2.End()
	if ctx2 == nil || span2 == nil {
		t.Error("CreateSpanFromTraceId should still return valid context/span even with invalid trace ID")
	}
}

func TestSetTokenUsage(t *testing.T) {
	// Setup tracing
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ShutdownTracing(ctx)
	}()

	os.Unsetenv("AIQA_SERVER_URL")
	os.Unsetenv("AIQA_API_KEY")

	// Use invalid port to fail fast in tests (connection refused, not timeout)
	err := InitTracing("http://127.0.0.1:1", "test-key", 1.0)
	if err != nil {
		t.Fatalf("Failed to init tracing: %v", err)
	}

	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	inputTokens := 10
	outputTokens := 20
	totalTokens := 30

	success := SetTokenUsage(ctx, &inputTokens, &outputTokens, &totalTokens, nil)
	if !success {
		t.Error("SetTokenUsage should return true when span is recording")
	}

	// Test with no active span
	ctxNoSpan := context.Background()
	success = SetTokenUsage(ctxNoSpan, &inputTokens, &outputTokens, &totalTokens, nil)
	if success {
		t.Error("SetTokenUsage should return false when no active span")
	}
}

func TestSetProviderAndModel(t *testing.T) {
	// Setup tracing
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ShutdownTracing(ctx)
	}()

	os.Unsetenv("AIQA_SERVER_URL")
	os.Unsetenv("AIQA_API_KEY")

	// Use invalid port to fail fast in tests (connection refused, not timeout)
	err := InitTracing("http://127.0.0.1:1", "test-key", 1.0)
	if err != nil {
		t.Fatalf("Failed to init tracing: %v", err)
	}

	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	provider := "openai"
	model := "gpt-4"

	success := SetProviderAndModel(ctx, &provider, &model)
	if !success {
		t.Error("SetProviderAndModel should return true when span is recording")
	}

	// Test with no active span
	ctxNoSpan := context.Background()
	success = SetProviderAndModel(ctxNoSpan, &provider, &model)
	if success {
		t.Error("SetProviderAndModel should return false when no active span")
	}
}

func TestSetConversationId(t *testing.T) {
	// Setup tracing
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ShutdownTracing(ctx)
	}()

	os.Unsetenv("AIQA_SERVER_URL")
	os.Unsetenv("AIQA_API_KEY")

	// Use invalid port to fail fast in tests (connection refused, not timeout)
	err := InitTracing("http://127.0.0.1:1", "test-key", 1.0)
	if err != nil {
		t.Fatalf("Failed to init tracing: %v", err)
	}

	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	success := SetConversationId(ctx, "conv-123")
	if !success {
		t.Error("SetConversationId should return true when span is recording")
	}

	// Test with no active span
	ctxNoSpan := context.Background()
	success = SetConversationId(ctxNoSpan, "conv-123")
	if success {
		t.Error("SetConversationId should return false when no active span")
	}
}
