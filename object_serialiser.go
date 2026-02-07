package aiqa

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// toNumber converts string to number, handling units like g, m, k (also mb, kb, gb though these should be avoided)
func toNumber(value string) int {
	if value == "" {
		return 0
	}

	// Remove trailing 'b' if present (e.g., "100mb" -> "100m")
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, "b")

	// Handle units
	if strings.HasSuffix(value, "g") {
		numStr := value[:len(value)-1]
		num, err := strconv.Atoi(numStr)
		if err != nil {
			return 0
		}
		return num * 1024 * 1024 * 1024
	} else if strings.HasSuffix(value, "m") {
		numStr := value[:len(value)-1]
		num, err := strconv.Atoi(numStr)
		if err != nil {
			return 0
		}
		return num * 1024 * 1024
	} else if strings.HasSuffix(value, "k") {
		numStr := value[:len(value)-1]
		num, err := strconv.Atoi(numStr)
		if err != nil {
			return 0
		}
		return num * 1024
	}

	// No unit, just parse as int
	num, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return num
}

// GetMaxObjectStrChars returns the maximum object string representation size in characters
// Configurable via AIQA_MAX_OBJECT_STR_CHARS environment variable (default: 1MB)
func GetMaxObjectStrChars() int {
	envValue := os.Getenv("AIQA_MAX_OBJECT_STR_CHARS")
	if envValue == "" {
		envValue = "1m"
	}
	return toNumber(envValue)
}

// sanitizeStringForUTF8 sanitizes a string to remove surrogate characters that can't be encoded to UTF-8
// Surrogate characters (U+D800 to U+DFFF) are invalid in UTF-8 and can cause encoding errors
func sanitizeStringForUTF8(text string) string {
	if text == "" {
		return text
	}

	// Check if string is valid UTF-8 and has no surrogates
	hasSurrogates := false
	for _, r := range text {
		if r >= 0xD800 && r <= 0xDFFF {
			hasSurrogates = true
			break
		}
	}

	if utf8.ValidString(text) && !hasSurrogates {
		return text
	}

	// Replace invalid UTF-8 sequences and surrogate pairs with replacement character
	var result strings.Builder
	for _, r := range text {
		if r == utf8.RuneError {
			result.WriteRune('\uFFFD') // Unicode replacement character
		} else if r >= 0xD800 && r <= 0xDFFF {
			// Surrogate pair range - replace with replacement character
			result.WriteRune('\uFFFD')
		} else {
			result.WriteRune(r)
		}
	}

	return result.String()
}

// getEnabledFilters returns a set of enabled filter names from AIQA_DATA_FILTERS env var
// Default filters: RemovePasswords, RemoveJWT, RemoveAuthHeaders, RemoveAPIKeys
func getEnabledFilters() map[string]bool {
	filtersEnv := os.Getenv("AIQA_DATA_FILTERS")
	if filtersEnv == "" {
		filtersEnv = "RemovePasswords, RemoveJWT, RemoveAuthHeaders, RemoveAPIKeys"
	}
	// Check if explicitly disabled
	if strings.ToLower(filtersEnv) == "false" {
		return make(map[string]bool)
	}
	enabled := make(map[string]bool)
	for _, f := range strings.Split(filtersEnv, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			enabled[f] = true
		}
	}
	return enabled
}

// isJWTToken checks if a value looks like a JWT token (starts with "eyJ" and has 3 parts separated by dots)
func isJWTToken(value any) bool {
	str, ok := value.(string)
	if !ok {
		return false
	}
	// JWT tokens have format: header.payload.signature (3 parts separated by dots)
	// They typically start with "eyJ" (base64 encoded '{"')
	parts := strings.Split(str, ".")
	return len(parts) == 3 && strings.HasPrefix(str, "eyJ") && len(parts[0]) > 0 && len(parts[1]) > 0 && len(parts[2]) > 0
}

// isAPIKey checks if a value looks like an API key based on common patterns
func isAPIKey(value any) bool {
	str, ok := value.(string)
	if !ok {
		return false
	}
	str = strings.TrimSpace(str)
	// Common API key prefixes
	apiKeyPrefixes := []string{"sk-", "pk-", "AKIA", "ghp_", "gho_", "ghu_", "ghs_", "ghr_"}
	for _, prefix := range apiKeyPrefixes {
		if strings.HasPrefix(str, prefix) {
			return true
		}
	}
	return false
}

// applyDataFilters applies data filters to a key-value pair based on enabled filters
func applyDataFilters(key string, value any) any {
	// Don't filter falsy values
	if value == nil {
		return value
	}

	// Check if value is falsy - only filter non-falsy values
	switch v := value.(type) {
	case string:
		if v == "" {
			return value
		}
	case int:
		if v == 0 {
			return value
		}
	case int64:
		if v == 0 {
			return value
		}
	case float64:
		if v == 0 {
			return value
		}
	case bool:
		if !v {
			return value
		}
	}

	enabledFilters := getEnabledFilters()
	keyLower := strings.ToLower(key)

	// RemovePasswords filter: if key contains "password", replace value with "****"
	if enabledFilters["RemovePasswords"] && strings.Contains(keyLower, "password") {
		return "****"
	}

	// RemoveJWT filter: if value looks like a JWT token, replace with "****"
	if enabledFilters["RemoveJWT"] && isJWTToken(value) {
		return "****"
	}

	// RemoveAuthHeaders filter: if key is "authorization" (case-insensitive), replace value with "****"
	if enabledFilters["RemoveAuthHeaders"] && keyLower == "authorization" {
		return "****"
	}

	// RemoveAPIKeys filter: if key contains API key patterns or value looks like an API key
	if enabledFilters["RemoveAPIKeys"] {
		// Check key patterns
		apiKeyKeyPatterns := []string{"api_key", "apikey", "api-key"}
		for _, pattern := range apiKeyKeyPatterns {
			if strings.Contains(keyLower, pattern) {
				return "****"
			}
		}
		// Check value patterns
		if isAPIKey(value) {
			return "****"
		}
	}

	return value
}

// safeStrRepr safely converts a value to string representation
// Handles objects that might raise exceptions during string conversion
// Uses AIQA_MAX_OBJECT_STR_CHARS environment variable to limit length
// Also sanitizes surrogate characters to prevent UTF-8 encoding errors
func safeStrRepr(value any) string {
	maxChars := GetMaxObjectStrChars()

	// Try to get string representation
	var reprStr string
	if value == nil {
		return "nil"
	}

	// Use reflection to get a reasonable string representation
	val := reflect.ValueOf(value)
	switch val.Kind() {
	case reflect.String:
		reprStr = val.String()
	case reflect.Ptr:
		if val.IsNil() {
			return "nil"
		}
		reprStr = fmt.Sprintf("%v", val.Elem().Interface())
	default:
		reprStr = fmt.Sprintf("%v", value)
	}

	// Sanitize surrogate characters
	reprStr = sanitizeStringForUTF8(reprStr)

	// Limit length to avoid huge strings
	if len(reprStr) > maxChars {
		return reprStr[:maxChars] + "... (truncated)"
	}
	return reprStr
}

// SerializeForSpan serializes a value for span attributes
// OpenTelemetry only accepts primitives (bool, string, bytes, int, float) or sequences of those
// Complex types (maps, structs) are converted to JSON strings
func SerializeForSpan(value any) any {
	if value == nil {
		return nil
	}

	// Keep primitives as is
	switch v := value.(type) {
	case string, int, int64, float64, bool:
		return v
	case []byte:
		return v
	}

	// For slices, check if all elements are primitives
	if reflect.TypeOf(value).Kind() == reflect.Slice {
		val := reflect.ValueOf(value)
		allPrimitives := true
		for i := 0; i < val.Len(); i++ {
			elem := val.Index(i).Interface()
			switch elem.(type) {
			case string, int, int64, float64, bool, []byte, nil:
				// Primitive
			default:
				allPrimitives = false
			}
		}
		if allPrimitives {
			return value
		}
		// Found non-primitive, serialize to JSON string
		jsonBytes, err := json.Marshal(value)
		if err != nil {
			return safeStrRepr(value)
		}
		return string(jsonBytes)
	}

	// For maps and other complex types, serialize to JSON string
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return safeStrRepr(value)
	}
	return string(jsonBytes)
}

// filterDataRecursive recursively applies data filters to nested structures
func filterDataRecursive(data any) any {
	if data == nil {
		return data
	}

	switch v := data.(type) {
	case map[string]any:
		result := make(map[string]any)
		for k, val := range v {
			filteredVal := applyDataFilters(k, val)
			result[k] = filterDataRecursive(filteredVal)
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = filterDataRecursive(item)
		}
		return result
	default:
		// For other types, try to convert to map if possible
		// This handles structs and other complex types
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return applyDataFilters("", v)
		}
		var jsonData any
		if err := json.Unmarshal(jsonBytes, &jsonData); err != nil {
			return applyDataFilters("", v)
		}
		// Check if unmarshaled data is still a primitive type to avoid infinite recursion
		switch jsonData.(type) {
		case map[string]any, []any:
			return filterDataRecursive(jsonData)
		default:
			// Primitive type, just apply filters and return
			return applyDataFilters("", jsonData)
		}
	}
}

// SerializeValue serializes a value to JSON string for span attributes
// Applies data filters before serialization
func SerializeValue(value any) string {
	// Apply data filters before serialization
	filteredValue := filterDataRecursive(value)

	// Try JSON serialization first
	jsonBytes, err := json.Marshal(filteredValue)
	if err != nil {
		// Fallback to string representation
		return safeStrRepr(filteredValue)
	}
	return string(jsonBytes)
}
