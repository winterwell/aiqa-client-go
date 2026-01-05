package aiqa

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestToNumber(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{"empty string", "", 0},
		{"plain number", "42", 42},
		{"k suffix", "10k", 10 * 1024},
		{"m suffix", "5m", 5 * 1024 * 1024},
		{"g suffix", "2g", 2 * 1024 * 1024 * 1024},
		{"kb suffix", "10kb", 10 * 1024},
		{"mb suffix", "5mb", 5 * 1024 * 1024},
		{"gb suffix", "2gb", 2 * 1024 * 1024 * 1024},
		{"with spaces", " 10k ", 10 * 1024},
		{"zero", "0", 0},
		{"large number", "1000", 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toNumber(tt.input)
			if result != tt.expected {
				t.Errorf("toNumber(%q) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetMaxObjectStrChars(t *testing.T) {
	// Save original env var
	originalValue := os.Getenv("AIQA_MAX_OBJECT_STR_CHARS")
	defer func() {
		if originalValue != "" {
			os.Setenv("AIQA_MAX_OBJECT_STR_CHARS", originalValue)
		} else {
			os.Unsetenv("AIQA_MAX_OBJECT_STR_CHARS")
		}
	}()

	// Test default
	os.Unsetenv("AIQA_MAX_OBJECT_STR_CHARS")
	result := GetMaxObjectStrChars()
	if result != 1*1024*1024 {
		t.Errorf("Expected default 1MB, got %d", result)
	}

	// Test custom value
	os.Setenv("AIQA_MAX_OBJECT_STR_CHARS", "500k")
	result = GetMaxObjectStrChars()
	if result != 500*1024 {
		t.Errorf("Expected 500KB, got %d", result)
	}
}

func TestSanitizeStringForUTF8(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"empty string", "", true},
		{"valid UTF-8", "Hello, world! 你好", true},
		{"valid ASCII", "Hello World", true},
		// Test with invalid UTF-8 bytes - these will be properly sanitized
		{"invalid UTF-8 bytes", "Hello" + string([]byte{0xFF, 0xFE}) + "World", true},
		// Test with surrogate character encoded as bytes (invalid UTF-8 sequence)
		{"surrogate as invalid bytes", "Hello" + string([]byte{0xED, 0xA0, 0x80}) + "World", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeStringForUTF8(tt.input)
			// Check if result is valid UTF-8 using utf8.ValidString
			valid := utf8.ValidString(result)
			// Also try to encode to bytes (should not panic)
			bytes := []byte(result)
			_ = bytes

			if !valid {
				t.Errorf("Result must be valid UTF-8, got invalid")
			}
			// For invalid input cases, verify that invalid sequences were sanitized
			if !utf8.ValidString(tt.input) {
				// Result should be sanitized (may contain replacement characters)
				// and should be safe to encode
				if len(result) == 0 && len(tt.input) > 0 {
					// Empty result is acceptable if all input was invalid
				} else if !utf8.ValidString(result) {
					t.Errorf("Sanitized result should be valid UTF-8")
				}
			}
		})
	}
}

func TestObjectSerialiserIsJWTToken(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected bool
	}{
		{"valid JWT", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c", true},
		{"invalid - wrong start", "not_a_jwt_token", false},
		{"invalid - wrong parts", "eyJ.part1.part2.part3", false},
		{"invalid - two parts", "eyJ.part1", false},
		{"invalid - four parts", "eyJ.part1.part2.part3.part4", false},
		{"non-string", 123, false},
		{"nil", nil, false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isJWTToken(tt.input)
			if result != tt.expected {
				t.Errorf("isJWTToken(%v) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestObjectSerialiserIsAPIKey(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected bool
	}{
		{"OpenAI key", "sk-1234567890abcdef", true},
		{"AWS key", "AKIAIOSFODNN7EXAMPLE", true},
		{"GitHub token ghp", "ghp_1234567890abcdef", true},
		{"GitHub token gho", "gho_1234567890abcdef", true},
		{"GitHub token ghu", "ghu_1234567890abcdef", true},
		{"GitHub token ghs", "ghs_1234567890abcdef", true},
		{"GitHub token ghr", "ghr_1234567890abcdef", true},
		{"not API key", "regular_string", false},
		{"non-string", 123, false},
		{"nil", nil, false},
		{"empty string", "", false},
		{"with spaces", " sk-123 ", true}, // TrimSpace should handle this
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isAPIKey(tt.input)
			if result != tt.expected {
				t.Errorf("isAPIKey(%v) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestObjectSerialiserApplyDataFilters(t *testing.T) {
	// Save original env var
	originalValue := os.Getenv("AIQA_DATA_FILTERS")
	defer func() {
		if originalValue != "" {
			os.Setenv("AIQA_DATA_FILTERS", originalValue)
		} else {
			os.Unsetenv("AIQA_DATA_FILTERS")
		}
	}()

	// Test with filters enabled
	os.Setenv("AIQA_DATA_FILTERS", "RemovePasswords, RemoveJWT, RemoveAuthHeaders, RemoveAPIKeys")

	tests := []struct {
		name     string
		key      string
		value    interface{}
		expected interface{}
	}{
		{"password field", "password", "secret123", "****"},
		{"password in key", "user_password", "secret123", "****"},
		{"JWT token", "token", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.test", "****"},
		{"authorization header", "authorization", "Bearer token123", "****"},
		{"API key in key", "api_key", "sk-123456", "****"},
		{"API key value", "key", "sk-123456", "****"},
		{"normal value", "name", "John Doe", "John Doe"},
		{"nil value", "value", nil, nil},
		{"empty string", "value", "", ""},
		{"zero int", "value", 0, 0},
		{"false bool", "value", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := applyDataFilters(tt.key, tt.value)
			if result != tt.expected {
				t.Errorf("applyDataFilters(%q, %v) = %v, want %v", tt.key, tt.value, result, tt.expected)
			}
		})
	}

	// Test with filters disabled
	os.Setenv("AIQA_DATA_FILTERS", "false")
	result := applyDataFilters("password", "secret123")
	if result != "secret123" {
		t.Errorf("Expected password to pass through when filters disabled, got %v", result)
	}
}

func TestSafeStrRepr(t *testing.T) {
	// Save original env var
	originalValue := os.Getenv("AIQA_MAX_OBJECT_STR_CHARS")
	defer func() {
		if originalValue != "" {
			os.Setenv("AIQA_MAX_OBJECT_STR_CHARS", originalValue)
		} else {
			os.Unsetenv("AIQA_MAX_OBJECT_STR_CHARS")
		}
	}()

	// Test simple object
	result := safeStrRepr([]int{1, 2, 3})
	if !strings.Contains(result, "1") || !strings.Contains(result, "2") {
		t.Errorf("Expected string representation to contain numbers, got %q", result)
	}

	// Test nil
	result = safeStrRepr(nil)
	if result != "nil" {
		t.Errorf("Expected 'nil' for nil input, got %q", result)
	}

	// Test large string truncation
	os.Setenv("AIQA_MAX_OBJECT_STR_CHARS", "100")
	largeStr := strings.Repeat("x", 1000)
	result = safeStrRepr(largeStr)
	if len(result) > 100+len("... (truncated)") {
		t.Errorf("Expected truncated string, got length %d", len(result))
	}
	if !strings.Contains(result, "... (truncated)") {
		t.Errorf("Expected truncation marker, got %q", result)
	}
}

func TestSerializeForSpan(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		check func(interface{}) bool
	}{
		{"nil", nil, func(v interface{}) bool { return v == nil }},
		{"string", "hello", func(v interface{}) bool { return v == "hello" }},
		{"int", 42, func(v interface{}) bool { return v == 42 }},
		{"float64", 3.14, func(v interface{}) bool { return v == 3.14 }},
		{"bool", true, func(v interface{}) bool { return v == true }},
		{"bytes", []byte("hello"), func(v interface{}) bool {
			b, ok := v.([]byte)
			return ok && string(b) == "hello"
		}},
		{"list of primitives", []interface{}{1, 2, 3}, func(v interface{}) bool {
			list, ok := v.([]interface{})
			return ok && len(list) == 3
		}},
		{"list with complex", []interface{}{1, map[string]interface{}{"key": "value"}}, func(v interface{}) bool {
			str, ok := v.(string)
			return ok && strings.Contains(str, "key")
		}},
		{"map", map[string]interface{}{"key": "value"}, func(v interface{}) bool {
			str, ok := v.(string)
			return ok && strings.Contains(str, "key")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SerializeForSpan(tt.input)
			if !tt.check(result) {
				t.Errorf("SerializeForSpan(%v) = %v, check failed", tt.input, result)
			}
		})
	}
}

func TestObjectSerialiserSerializeValue(t *testing.T) {
	// Save original env var
	originalValue := os.Getenv("AIQA_DATA_FILTERS")
	defer func() {
		if originalValue != "" {
			os.Setenv("AIQA_DATA_FILTERS", originalValue)
		} else {
			os.Unsetenv("AIQA_DATA_FILTERS")
		}
	}()

	// Test simple value
	result := SerializeValue(map[string]interface{}{"key": "value"})
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Errorf("Expected valid JSON, got error: %v", err)
	}
	if decoded["key"] != "value" {
		t.Errorf("Expected key=value, got %v", decoded)
	}

	// Test with filters
	os.Setenv("AIQA_DATA_FILTERS", "RemovePasswords")
	result = SerializeValue(map[string]interface{}{"password": "secret"})
	if !strings.Contains(result, "****") {
		t.Errorf("Expected password to be filtered, got %q", result)
	}

	// Test nested structures
	nested := map[string]interface{}{
		"user": map[string]interface{}{
			"name":     "John",
			"password": "secret",
		},
	}
	os.Setenv("AIQA_DATA_FILTERS", "RemovePasswords")
	result = SerializeValue(nested)
	if !strings.Contains(result, "****") {
		t.Errorf("Expected nested password to be filtered, got %q", result)
	}
}

func TestObjectSerialiserFilterDataRecursive(t *testing.T) {
	// Save original env var
	originalValue := os.Getenv("AIQA_DATA_FILTERS")
	defer func() {
		if originalValue != "" {
			os.Setenv("AIQA_DATA_FILTERS", originalValue)
		} else {
			os.Unsetenv("AIQA_DATA_FILTERS")
		}
	}()

	os.Setenv("AIQA_DATA_FILTERS", "RemovePasswords")

	// Test nested map
	input := map[string]interface{}{
		"user": map[string]interface{}{
			"name":     "John",
			"password": "secret",
		},
		"password": "top_secret",
	}
	result := filterDataRecursive(input)
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected map, got %T", result)
	}
	if resultMap["password"] != "****" {
		t.Errorf("Expected top-level password to be filtered, got %v", resultMap["password"])
	}
	userMap, ok := resultMap["user"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected nested map, got %T", resultMap["user"])
	}
	if userMap["password"] != "****" {
		t.Errorf("Expected nested password to be filtered, got %v", userMap["password"])
	}
	if userMap["name"] != "John" {
		t.Errorf("Expected name to be preserved, got %v", userMap["name"])
	}

	// Test list
	inputList := []interface{}{
		map[string]interface{}{"password": "secret1"},
		map[string]interface{}{"password": "secret2"},
	}
	resultList := filterDataRecursive(inputList)
	list, ok := resultList.([]interface{})
	if !ok {
		t.Fatalf("Expected list, got %T", resultList)
	}
	if len(list) != 2 {
		t.Errorf("Expected 2 items, got %d", len(list))
	}
}

func TestGetEnabledFilters(t *testing.T) {
	// Save original env var
	originalValue := os.Getenv("AIQA_DATA_FILTERS")
	defer func() {
		if originalValue != "" {
			os.Setenv("AIQA_DATA_FILTERS", originalValue)
		} else {
			os.Unsetenv("AIQA_DATA_FILTERS")
		}
	}()

	// Test default filters
	os.Unsetenv("AIQA_DATA_FILTERS")
	filters := getEnabledFilters()
	if len(filters) == 0 {
		t.Error("Expected default filters to be enabled")
	}
	if !filters["RemovePasswords"] {
		t.Error("Expected RemovePasswords to be enabled by default")
	}

	// Test custom filters
	os.Setenv("AIQA_DATA_FILTERS", "RemovePasswords, RemoveJWT")
	filters = getEnabledFilters()
	if !filters["RemovePasswords"] || !filters["RemoveJWT"] {
		t.Error("Expected custom filters to be enabled")
	}
	if filters["RemoveAuthHeaders"] {
		t.Error("Expected RemoveAuthHeaders to be disabled")
	}

	// Test disabled
	os.Setenv("AIQA_DATA_FILTERS", "false")
	filters = getEnabledFilters()
	if len(filters) != 0 {
		t.Errorf("Expected no filters when disabled, got %v", filters)
	}
}

func TestSerializeForSpanWithComplexTypes(t *testing.T) {
	// Test struct-like behavior (maps)
	complexMap := map[string]interface{}{
		"nested": map[string]interface{}{
			"value": 42,
		},
		"list": []interface{}{1, 2, 3},
	}
	result := SerializeForSpan(complexMap)
	str, ok := result.(string)
	if !ok {
		t.Fatalf("Expected string for complex map, got %T", result)
	}
	if !strings.Contains(str, "nested") || !strings.Contains(str, "value") {
		t.Errorf("Expected serialized JSON to contain map keys, got %q", str)
	}

	// Test that it's valid JSON
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(str), &decoded); err != nil {
		t.Errorf("Expected valid JSON, got error: %v", err)
	}
}

func TestSerializeValueWithTime(t *testing.T) {
	// Test that time values are handled
	now := time.Now()
	result := SerializeValue(map[string]interface{}{
		"timestamp": now,
		"value":     42,
	})
	// Should be valid JSON
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Errorf("Expected valid JSON with time, got error: %v", err)
	}
}
