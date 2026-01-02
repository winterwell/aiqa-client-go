package aiqa

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	tracerProvider *sdktrace.TracerProvider
	tracer         trace.Tracer
	exporter       *AIQAExporter
	samplingRate   float64 = 1.0 // Default: sample all traces
	componentTag   string  = ""  // Component tag to add to all spans
	tracingEnabled bool    = true // Whether tracing is enabled (set to false if env vars missing)
)

func init() {
	// Read component tag from environment variable
	if envTag := os.Getenv("AIQA_COMPONENT_TAG"); envTag != "" {
		componentTag = envTag
	}
}

const (
	tracerName = "aiqa-tracer"
)

// traceIDSampler implements deterministic sampling based on trace-id
type traceIDSampler struct {
	rate float64
}

func (s *traceIDSampler) ShouldSample(params sdktrace.SamplingParameters) sdktrace.SamplingResult {
	if s.rate <= 0 {
		return sdktrace.SamplingResult{Decision: sdktrace.Drop}
	}
	if s.rate >= 1 {
		return sdktrace.SamplingResult{Decision: sdktrace.RecordAndSample}
	}

	// Use trace ID for deterministic sampling
	traceID := params.TraceID
	hash := fnv.New64a()
	hash.Write(traceID[:])
	hashValue := hash.Sum64()

	// Convert hash to a value in [0, 1)
	// hashValue is already a uint64, so normalize it by dividing by max uint64 (2^64 - 1)
	const maxUint64 = float64(^uint64(0))
	sampleValue := float64(hashValue) / maxUint64

	if sampleValue < s.rate {
		return sdktrace.SamplingResult{Decision: sdktrace.RecordAndSample}
	}
	return sdktrace.SamplingResult{Decision: sdktrace.Drop}
}

func (s *traceIDSampler) Description() string {
	return fmt.Sprintf("TraceIDSampler{rate=%.4f}", s.rate)
}

// TracingOptions contains options for tracing functions
type TracingOptions struct {
	Name         string
	IgnoreInput  []string
	IgnoreOutput []string
	FilterInput  func(interface{}) interface{}
	FilterOutput func(interface{}) interface{}
}

// InitTracing initializes the OpenTelemetry tracer provider with AIQA exporter
// samplingRate: value between 0 and 1, where 0 = tracing is off, 1 = trace all
// If not provided, reads from AIQA_SAMPLING_RATE environment variable (default: 1.0)
// If a TracerProvider already exists, it will add the AIQA exporter to it instead of creating a new one.
// If AIQA_SERVER_URL or AIQA_API_KEY are not set, tracing will be gracefully disabled
// (functions will execute normally without tracing overhead).
func InitTracing(serverURL, apiKey string, samplingRateArg ...float64) error {
	if serverURL == "" {
		serverURL = os.Getenv("AIQA_SERVER_URL")
	}
	if apiKey == "" {
		apiKey = os.Getenv("AIQA_API_KEY")
	}

	// Gracefully disable if required environment variables are not set
	if serverURL == "" || apiKey == "" {
		var missingVars []string
		if serverURL == "" {
			missingVars = append(missingVars, "AIQA_SERVER_URL")
		}
		if apiKey == "" {
			missingVars = append(missingVars, "AIQA_API_KEY")
		}
		fmt.Printf("AIQA: WARNING: Tracing is disabled: missing required environment variables: %s\n", strings.Join(missingVars, ", "))
		fmt.Printf("AIQA: Your application will continue to run without tracing.\n")
		tracingEnabled = false
		return nil
	}

	tracingEnabled = true

	// Set sampling rate
	if len(samplingRateArg) > 0 {
		samplingRate = samplingRateArg[0]
	} else {
		if envRate := os.Getenv("AIQA_SAMPLING_RATE"); envRate != "" {
			if rate, err := strconv.ParseFloat(envRate, 64); err == nil {
				samplingRate = rate
			}
		}
	}

	// Clamp sampling rate to [0, 1]
	if samplingRate < 0 {
		samplingRate = 0
	} else if samplingRate > 1 {
		samplingRate = 1
	}

	exporter = NewAIQAExporter(serverURL, apiKey, 5)

	// Check if a TracerProvider is already set
	existingProvider := otel.GetTracerProvider()

	// Try to cast to SDK TracerProvider to see if it's a real provider
	if sdkProvider, ok := existingProvider.(*sdktrace.TracerProvider); ok {
		// Real provider already exists, add our span processor to it
		bsp := sdktrace.NewBatchSpanProcessor(exporter)
		sdkProvider.RegisterSpanProcessor(bsp)
		tracerProvider = sdkProvider
		tracer = otel.Tracer(tracerName)
		return nil
	}

	// No real provider exists, create a new one
	res, err := resource.New(
		context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String("example-service"),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create resource: %w", err)
	}

	// Create a batch span processor with the exporter
	bsp := sdktrace.NewBatchSpanProcessor(exporter)

	// Create custom sampler based on trace-id
	sampler := &traceIDSampler{rate: samplingRate}

	tracerProvider = sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(bsp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	otel.SetTracerProvider(tracerProvider)
	tracer = otel.Tracer(tracerName)

	return nil
}

// FlushSpans flushes all pending spans to the server
func FlushSpans(ctx context.Context) error {
	if tracerProvider != nil {
		if err := tracerProvider.ForceFlush(ctx); err != nil {
			return err
		}
	}
	if exporter != nil {
		return exporter.Flush(ctx)
	}
	return nil
}

// ShutdownTracing shuts down the tracer provider and exporter.
// Note: If InitTracing detected and used an existing TracerProvider, calling this
// will shutdown the entire provider, which may affect other tracing systems. Use with caution.
func ShutdownTracing(ctx context.Context) error {
	if tracerProvider != nil {
		if err := tracerProvider.Shutdown(ctx); err != nil {
			return err
		}
	}
	if exporter != nil {
		return exporter.Shutdown(ctx)
	}
	return nil
}

// WithTracing wraps a function to automatically create spans
func WithTracing(fn interface{}, options ...TracingOptions) interface{} {
	opt := TracingOptions{}
	if len(options) > 0 {
		opt = options[0]
	}

	fnValue := reflect.ValueOf(fn)
	fnType := fnValue.Type()

	if fnType.Kind() != reflect.Func {
		panic("WithTracing: argument must be a function")
	}

	// Get function name
	fnName := opt.Name
	if fnName == "" {
		fnName = runtime.FuncForPC(fnValue.Pointer()).Name()
		if idx := strings.LastIndex(fnName, "."); idx >= 0 {
			fnName = fnName[idx+1:]
		}
	}

	// Check if already traced
	if fnValue.Kind() == reflect.Func {
		// Check for _isTraced field (not possible in Go, but we can track it differently)
		// For now, we'll just wrap it
	}

	// Determine if function is async (returns error or context)
	isAsync := false
	if fnType.NumOut() > 0 {
		lastOut := fnType.Out(fnType.NumOut() - 1)
		if lastOut.String() == "error" {
			isAsync = true
		}
	}

	if isAsync {
		return wrapAsyncFunction(fnValue, fnType, fnName, opt)
	}
	return wrapSyncFunction(fnValue, fnType, fnName, opt)
}

// wrapSyncFunction wraps a synchronous function
func wrapSyncFunction(fnValue reflect.Value, fnType reflect.Type, fnName string, opt TracingOptions) interface{} {
	wrapper := reflect.MakeFunc(fnType, func(args []reflect.Value) []reflect.Value {
		// If tracing is disabled, just execute the function without creating spans
		if !tracingEnabled || tracer == nil {
			return fnValue.Call(args)
		}

		ctx := context.Background()
		if len(args) > 0 {
			if ctxVal := args[0]; ctxVal.Type().String() == "context.Context" {
				ctx = ctxVal.Interface().(context.Context)
			}
		}

		// Start a new span (always create a child span for the traced function)
		ctx, span := tracer.Start(ctx, fnName)
		defer span.End()

		// Set component tag if configured
		setComponentTagIfSet(span)

		// Prepare input
		input := prepareInput(args, opt)
		if input != nil {
			span.SetAttributes(attribute.String("input", serializeValue(input)))
		}

		// Execute function
		results := fnValue.Call(args)

		// Handle results
		if len(results) > 0 {
			output := prepareOutput(results, opt)
			if output != nil {
				// Extract and set token usage before setting output
				extractAndSetTokenUsage(span, output)
				// Extract and set provider/model before setting output
				extractAndSetProviderAndModel(span, output)
				span.SetAttributes(attribute.String("output", serializeValue(output)))
			}

			// Check for error
			lastResult := results[len(results)-1]
			if lastResult.Type().String() == "error" && !lastResult.IsNil() {
				err := lastResult.Interface().(error)
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			} else {
				span.SetStatus(codes.Ok, "")
			}
		} else {
			span.SetStatus(codes.Ok, "")
		}

		return results
	})

	return wrapper.Interface()
}

// wrapAsyncFunction wraps an asynchronous function (one that returns error)
func wrapAsyncFunction(fnValue reflect.Value, fnType reflect.Type, fnName string, opt TracingOptions) interface{} {
	wrapper := reflect.MakeFunc(fnType, func(args []reflect.Value) []reflect.Value {
		// If tracing is disabled, just execute the function without creating spans
		if !tracingEnabled || tracer == nil {
			return fnValue.Call(args)
		}

		ctx := context.Background()
		if len(args) > 0 {
			if ctxVal := args[0]; ctxVal.Type().String() == "context.Context" {
				ctx = ctxVal.Interface().(context.Context)
			}
		}

		ctx, span := tracer.Start(ctx, fnName)
		defer span.End()

		// Set component tag if configured
		setComponentTagIfSet(span)

		// Prepare input
		input := prepareInput(args, opt)
		if input != nil {
			span.SetAttributes(attribute.String("input", serializeValue(input)))
		}

		// Execute function
		results := fnValue.Call(args)

		// Handle results
		if len(results) > 0 {
			output := prepareOutput(results, opt)
			if output != nil {
				// Extract and set token usage before setting output
				extractAndSetTokenUsage(span, output)
				// Extract and set provider/model before setting output
				extractAndSetProviderAndModel(span, output)
				span.SetAttributes(attribute.String("output", serializeValue(output)))
			}

			// Check for error (last return value)
			lastResult := results[len(results)-1]
			if lastResult.Type().String() == "error" {
				if !lastResult.IsNil() {
					err := lastResult.Interface().(error)
					span.RecordError(err)
					span.SetStatus(codes.Error, err.Error())
				} else {
					span.SetStatus(codes.Ok, "")
				}
			}
		}

		return results
	})

	return wrapper.Interface()
}

// prepareInput prepares function input for span attributes
func prepareInput(args []reflect.Value, opt TracingOptions) interface{} {
	if len(args) == 0 {
		return nil
	}

	// Filter out context if present
	filteredArgs := make([]reflect.Value, 0, len(args))
	for _, arg := range args {
		if arg.Type().String() != "context.Context" {
			filteredArgs = append(filteredArgs, arg)
		}
	}

	if len(filteredArgs) == 0 {
		return nil
	}

	if len(filteredArgs) == 1 {
		result := filteredArgs[0].Interface()
		if opt.FilterInput != nil {
			result = opt.FilterInput(result)
		}
		return result
	}

	// Multiple args - combine into map
	result := make(map[string]interface{})
	for i, arg := range filteredArgs {
		key := fmt.Sprintf("arg%d", i)
		result[key] = arg.Interface()
	}

	if opt.FilterInput != nil {
		result = opt.FilterInput(result).(map[string]interface{})
	}

	return result
}

// prepareOutput prepares function output for span attributes
func prepareOutput(results []reflect.Value, opt TracingOptions) interface{} {
	if len(results) == 0 {
		return nil
	}

	// Filter out error if present
	filteredResults := make([]reflect.Value, 0, len(results))
	for _, result := range results {
		if result.Type().String() != "error" {
			filteredResults = append(filteredResults, result)
		}
	}

	if len(filteredResults) == 0 {
		return nil
	}

	if len(filteredResults) == 1 {
		result := filteredResults[0].Interface()
		if opt.FilterOutput != nil {
			result = opt.FilterOutput(result)
		}
		return result
	}

	// Multiple results - combine into map
	result := make(map[string]interface{})
	for i, res := range filteredResults {
		key := fmt.Sprintf("result%d", i)
		result[key] = res.Interface()
	}

	if opt.FilterOutput != nil {
		result = opt.FilterOutput(result).(map[string]interface{})
	}

	return result
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
func isJWTToken(value interface{}) bool {
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
func isAPIKey(value interface{}) bool {
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
func applyDataFilters(key string, value interface{}) interface{} {
	// Don't filter falsy values (nil, empty string, zero, false)
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
	// For other types, proceed with filtering

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

// filterDataRecursive recursively applies data filters to nested structures
func filterDataRecursive(data interface{}) interface{} {
	if data == nil {
		return data
	}

	switch v := data.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for k, val := range v {
			filteredVal := applyDataFilters(k, val)
			result[k] = filterDataRecursive(filteredVal)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
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
		var jsonData interface{}
		if err := json.Unmarshal(jsonBytes, &jsonData); err != nil {
			return applyDataFilters("", v)
		}
		return filterDataRecursive(jsonData)
	}
}

// serializeValue serializes a value to JSON string for span attributes
func serializeValue(value interface{}) string {
	// Apply data filters before serialization
	filteredValue := filterDataRecursive(value)

	// Try JSON serialization first
	jsonBytes, err := json.Marshal(filteredValue)
	if err != nil {
		// Fallback to string representation
		return fmt.Sprintf("%v", filteredValue)
	}
	return string(jsonBytes)
}

// SetSpanAttribute sets an attribute on the active span
func SetSpanAttribute(ctx context.Context, attributeName string, attributeValue interface{}) bool {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.SetAttributes(attribute.String(attributeName, serializeValue(attributeValue)))
		return true
	}
	return false
}

// isAttributeSet checks if an attribute is already set on a span.
// Returns true if the attribute exists, false otherwise.
// Safe against exceptions.
func isAttributeSet(span trace.Span, attributeName string) bool {
	// OpenTelemetry Go SDK doesn't expose a direct way to check if an attribute is set.
	// We'll use a conservative approach: try to access internal attributes if possible,
	// otherwise assume not set to allow setting.
	defer func() {
		// Recover from any panics
		if r := recover(); r != nil {
			// If anything goes wrong, assume not set (conservative approach)
		}
	}()

	// Check if span is recording first
	if !span.IsRecording() {
		return false
	}

	// Try to access span's internal attributes if available
	// This is SDK-specific and may not work for all span implementations
	if sdkSpan, ok := span.(interface{ Attributes() []attribute.KeyValue }); ok {
		attrs := sdkSpan.Attributes()
		for _, kv := range attrs {
			if string(kv.Key) == attributeName {
				return true
			}
		}
	}

	// Try alternative method: check if span has a way to get attributes
	// Some SDK implementations may store attributes differently
	if attrSpan, ok := span.(interface{ GetAttributes() map[string]interface{} }); ok {
		attrs := attrSpan.GetAttributes()
		if attrs != nil {
			if _, exists := attrs[attributeName]; exists {
				return true
			}
		}
	}

	return false
}

// extractAndSetTokenUsage extracts OpenAI API style token usage from result and adds to span attributes
// using OpenTelemetry semantic conventions for gen_ai.
// Only sets attributes that are not already set.
//
// This function detects token usage from OpenAI API response patterns:
//   - OpenAI Chat Completions API: The 'usage' object contains 'prompt_tokens', 'completion_tokens', and 'total_tokens'.
//     See https://platform.openai.com/docs/api-reference/chat/object (usage field)
//   - OpenAI Completions API: The 'usage' object contains 'prompt_tokens', 'completion_tokens', and 'total_tokens'.
//     See https://platform.openai.com/docs/api-reference/completions/object (usage field)
//
// This function is safe against exceptions and will not derail tracing or program execution.
func extractAndSetTokenUsage(span trace.Span, result interface{}) {
	defer func() {
		// Catch any panics to ensure this never derails tracing
		if r := recover(); r != nil {
			// Silently ignore errors
		}
	}()

	if !span.IsRecording() {
		return
	}

	var usage map[string]interface{}

	// Check if result is a map with 'usage' key
	if resultMap, ok := result.(map[string]interface{}); ok {
		if usageVal, exists := resultMap["usage"]; exists {
			if usageMap, ok := usageVal.(map[string]interface{}); ok {
				usage = usageMap
			}
		} else {
			// Check if result itself is a usage dict (OpenAI format)
			if _, hasPrompt := resultMap["prompt_tokens"]; hasPrompt {
				if _, hasCompletion := resultMap["completion_tokens"]; hasCompletion {
					if _, hasTotal := resultMap["total_tokens"]; hasTotal {
						usage = resultMap
					}
				}
			} else if _, hasInput := resultMap["input_tokens"]; hasInput {
				// Bedrock format
				if _, hasOutput := resultMap["output_tokens"]; hasOutput {
					usage = resultMap
				}
			}
		}
	}

	// Check if result has a 'Usage' field (struct with Usage field, e.g., OpenAI response object)
	if usage == nil {
		resultVal := reflect.ValueOf(result)
		if resultVal.Kind() == reflect.Ptr {
			resultVal = resultVal.Elem()
		}
		if resultVal.Kind() == reflect.Struct {
			usageField := resultVal.FieldByName("Usage")
			if !usageField.IsValid() {
				usageField = resultVal.FieldByName("usage")
			}
			if usageField.IsValid() && usageField.CanInterface() {
				if usageMap, ok := usageField.Interface().(map[string]interface{}); ok {
					usage = usageMap
				} else if usageField.Kind() == reflect.Struct {
					// Convert struct to map
					usage = make(map[string]interface{})
					usageType := usageField.Type()
					for i := 0; i < usageField.NumField(); i++ {
						field := usageField.Field(i)
						if field.CanInterface() {
							fieldName := usageType.Field(i).Name
							usage[fieldName] = field.Interface()
						}
					}
				}
			}
		}
	}

	// Extract token usage if found
	if usage != nil {
		// Get token values safely
		// Support both OpenAI format (prompt_tokens/completion_tokens) and Bedrock format (input_tokens/output_tokens)
		var promptTokens, completionTokens, totalTokens interface{}
		if val, ok := usage["prompt_tokens"]; ok {
			promptTokens = val
		} else if val, ok := usage["PromptTokens"]; ok {
			promptTokens = val
		} else if val, ok := usage["input_tokens"]; ok {
			// Bedrock format
			promptTokens = val
		} else if val, ok := usage["InputTokens"]; ok {
			// Bedrock format (capitalized)
			promptTokens = val
		}

		if val, ok := usage["completion_tokens"]; ok {
			completionTokens = val
		} else if val, ok := usage["CompletionTokens"]; ok {
			completionTokens = val
		} else if val, ok := usage["output_tokens"]; ok {
			// Bedrock format
			completionTokens = val
		} else if val, ok := usage["OutputTokens"]; ok {
			// Bedrock format (capitalized)
			completionTokens = val
		}

		if val, ok := usage["total_tokens"]; ok {
			totalTokens = val
		} else if val, ok := usage["TotalTokens"]; ok {
			totalTokens = val
		}

		// Calculate total_tokens if not provided but we have input and output
		if totalTokens == nil && promptTokens != nil && completionTokens != nil {
			// Try to calculate total
			var inputVal, outputVal float64
			if inputInt, ok := promptTokens.(int); ok {
				inputVal = float64(inputInt)
			} else if inputInt64, ok := promptTokens.(int64); ok {
				inputVal = float64(inputInt64)
			} else if inputFloat, ok := promptTokens.(float64); ok {
				inputVal = inputFloat
			}
			if outputInt, ok := completionTokens.(int); ok {
				outputVal = float64(outputInt)
			} else if outputInt64, ok := completionTokens.(int64); ok {
				outputVal = float64(outputInt64)
			} else if outputFloat, ok := completionTokens.(float64); ok {
				outputVal = outputFloat
			}
			if inputVal > 0 && outputVal > 0 {
				totalTokens = int(inputVal + outputVal)
			}
		}

		// Only set attributes that are not already set
		if promptTokens != nil && !isAttributeSet(span, "gen_ai.usage.input_tokens") {
			if tokens, ok := promptTokens.(int); ok {
				span.SetAttributes(attribute.Int("gen_ai.usage.input_tokens", tokens))
			} else if tokens, ok := promptTokens.(int64); ok {
				span.SetAttributes(attribute.Int64("gen_ai.usage.input_tokens", tokens))
			} else if tokens, ok := promptTokens.(float64); ok {
				span.SetAttributes(attribute.Int("gen_ai.usage.input_tokens", int(tokens)))
			}
		}

		if completionTokens != nil && !isAttributeSet(span, "gen_ai.usage.output_tokens") {
			if tokens, ok := completionTokens.(int); ok {
				span.SetAttributes(attribute.Int("gen_ai.usage.output_tokens", tokens))
			} else if tokens, ok := completionTokens.(int64); ok {
				span.SetAttributes(attribute.Int64("gen_ai.usage.output_tokens", tokens))
			} else if tokens, ok := completionTokens.(float64); ok {
				span.SetAttributes(attribute.Int("gen_ai.usage.output_tokens", int(tokens)))
			}
		}

		if totalTokens != nil && !isAttributeSet(span, "gen_ai.usage.total_tokens") {
			if tokens, ok := totalTokens.(int); ok {
				span.SetAttributes(attribute.Int("gen_ai.usage.total_tokens", tokens))
			} else if tokens, ok := totalTokens.(int64); ok {
				span.SetAttributes(attribute.Int64("gen_ai.usage.total_tokens", tokens))
			} else if tokens, ok := totalTokens.(float64); ok {
				span.SetAttributes(attribute.Int("gen_ai.usage.total_tokens", int(tokens)))
			}
		}
	}
}

// extractAndSetProviderAndModel extracts provider and model information from result and adds to span attributes
// using OpenTelemetry semantic conventions for gen_ai.
// Only sets attributes that are not already set.
//
// This function detects model information from common API response patterns:
//   - OpenAI Chat Completions API: The 'model' field is at the top level of the response.
//     See https://platform.openai.com/docs/api-reference/chat/object
//   - OpenAI Completions API: The 'model' field is at the top level of the response.
//     See https://platform.openai.com/docs/api-reference/completions/object
//
// This function is safe against exceptions and will not derail tracing or program execution.
func extractAndSetProviderAndModel(span trace.Span, result interface{}) {
	defer func() {
		// Catch any panics to ensure this never derails tracing
		if r := recover(); r != nil {
			// Silently ignore errors
		}
	}()

	if !span.IsRecording() {
		return
	}

	var model, provider interface{}

	// Check if result is a map
	if resultMap, ok := result.(map[string]interface{}); ok {
		model = resultMap["model"]
		if model == nil {
			model = resultMap["Model"]
		}
		provider = resultMap["provider"]
		if provider == nil {
			provider = resultMap["Provider"]
		}
		if provider == nil {
			provider = resultMap["provider_name"]
		}
		if provider == nil {
			provider = resultMap["providerName"]
		}

		// Check for model in choices (OpenAI pattern)
		if model == nil {
			if choices, ok := resultMap["choices"].([]interface{}); ok && len(choices) > 0 {
				if firstChoice, ok := choices[0].(map[string]interface{}); ok {
					model = firstChoice["model"]
					if model == nil {
						model = firstChoice["Model"]
					}
				}
			}
		}
	}

	// Check if result has Model/Provider fields (struct, e.g., OpenAI response object)
	if model == nil || provider == nil {
		resultVal := reflect.ValueOf(result)
		if resultVal.Kind() == reflect.Ptr {
			resultVal = resultVal.Elem()
		}
		if resultVal.Kind() == reflect.Struct {
			if model == nil {
				modelField := resultVal.FieldByName("Model")
				if !modelField.IsValid() {
					modelField = resultVal.FieldByName("model")
				}
				if modelField.IsValid() && modelField.CanInterface() {
					model = modelField.Interface()
				}
			}
			if provider == nil {
				providerField := resultVal.FieldByName("Provider")
				if !providerField.IsValid() {
					providerField = resultVal.FieldByName("provider")
				}
				if !providerField.IsValid() {
					providerField = resultVal.FieldByName("ProviderName")
				}
				if !providerField.IsValid() {
					providerField = resultVal.FieldByName("provider_name")
				}
				if providerField.IsValid() && providerField.CanInterface() {
					provider = providerField.Interface()
				}
			}
		}
	}

	// Set attributes if found and not already set
	if model != nil && !isAttributeSet(span, "gen_ai.request.model") {
		if modelStr, ok := model.(string); ok && modelStr != "" {
			span.SetAttributes(attribute.String("gen_ai.request.model", modelStr))
		} else {
			// Convert to string if needed
			span.SetAttributes(attribute.String("gen_ai.request.model", fmt.Sprintf("%v", model)))
		}
	}

	if provider != nil && !isAttributeSet(span, "gen_ai.provider.name") {
		if providerStr, ok := provider.(string); ok && providerStr != "" {
			span.SetAttributes(attribute.String("gen_ai.provider.name", providerStr))
		} else {
			// Convert to string if needed
			span.SetAttributes(attribute.String("gen_ai.provider.name", fmt.Sprintf("%v", provider)))
		}
	}
}

// setComponentTagIfSet sets the component tag on a span if it's configured
// Uses OpenTelemetry semantic convention gen_ai.component.id
func setComponentTagIfSet(span trace.Span) {
	if componentTag != "" {
		span.SetAttributes(attribute.String("gen_ai.component.id", componentTag))
	}
}

// GetActiveSpan returns the active span from context
func GetActiveSpan(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// SetConversationId sets the gen_ai.conversation.id attribute on the active span.
// This allows you to group multiple traces together that are part of the same conversation.
// See https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-events/ for more details.
//
// conversationId: A unique identifier for the conversation (e.g., user session ID, chat ID, etc.)
// Returns: True if gen_ai.conversation.id was set, False if no active span found
func SetConversationId(ctx context.Context, conversationId string) bool {
	return SetSpanAttribute(ctx, "gen_ai.conversation.id", conversationId)
}

// SetTokenUsage sets token usage attributes on the active span using OpenTelemetry semantic conventions for gen_ai.
// This allows you to explicitly record token usage information.
// See https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-spans/ for more details.
//
// inputTokens: Number of input tokens used (maps to gen_ai.usage.input_tokens)
// outputTokens: Number of output tokens generated (maps to gen_ai.usage.output_tokens)
// totalTokens: Total number of tokens used (maps to gen_ai.usage.total_tokens)
// Returns: True if at least one token usage attribute was set, False if no active span found
func SetTokenUsage(ctx context.Context, inputTokens *int, outputTokens *int, totalTokens *int) bool {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return false
	}

	setCount := 0
	defer func() {
		// Recover from any panics
		if r := recover(); r != nil {
			// Silently ignore errors
		}
	}()

	if inputTokens != nil {
		span.SetAttributes(attribute.Int("gen_ai.usage.input_tokens", *inputTokens))
		setCount++
	}
	if outputTokens != nil {
		span.SetAttributes(attribute.Int("gen_ai.usage.output_tokens", *outputTokens))
		setCount++
	}
	if totalTokens != nil {
		span.SetAttributes(attribute.Int("gen_ai.usage.total_tokens", *totalTokens))
		setCount++
	}

	return setCount > 0
}

// SetProviderAndModel sets provider and model attributes on the active span using OpenTelemetry semantic conventions for gen_ai.
// This allows you to explicitly record provider and model information.
// See https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-spans/ for more details.
//
// provider: Name of the AI provider (e.g., "openai", "anthropic", "google") (maps to gen_ai.provider.name)
// model: Name of the model used (e.g., "gpt-4", "claude-3-5-sonnet") (maps to gen_ai.request.model)
// Returns: True if at least one attribute was set, False if no active span found
func SetProviderAndModel(ctx context.Context, provider *string, model *string) bool {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return false
	}

	setCount := 0
	defer func() {
		// Recover from any panics
		if r := recover(); r != nil {
			// Silently ignore errors
		}
	}()

	if provider != nil && *provider != "" {
		span.SetAttributes(attribute.String("gen_ai.provider.name", *provider))
		setCount++
	}
	if model != nil && *model != "" {
		span.SetAttributes(attribute.String("gen_ai.request.model", *model))
		setCount++
	}

	return setCount > 0
}

// SetComponentTag sets a custom component tag that will be added to all spans created by AIQA.
// This can also be set via the AIQA_COMPONENT_TAG environment variable.
// The component tag allows you to identify which component/system generated the spans - e.g. in the AIQA Traces view.
//
// tag: A component identifier (e.g., "mynamespace.mysystem", "backend.api", etc.)
func SetComponentTag(tag string) {
	componentTag = tag
}

// IsTracingEnabled returns whether tracing is currently enabled.
// Tracing is disabled if AIQA_SERVER_URL or AIQA_API_KEY are not set.
func IsTracingEnabled() bool {
	return tracingEnabled
}

// GetTraceId gets the current trace ID as a hexadecimal string (32 characters).
// Returns: The trace ID as a hex string, or empty string if no active span exists.
func GetTraceId(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		spanContext := span.SpanContext()
		traceID := spanContext.TraceID()
		if traceID.IsValid() {
			return traceID.String()
		}
	}
	return ""
}

// GetSpanId gets the current span ID as a hexadecimal string (16 characters).
// Returns: The span ID as a hex string, or empty string if no active span exists.
func GetSpanId(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		spanContext := span.SpanContext()
		spanID := spanContext.SpanID()
		if spanID.IsValid() {
			return spanID.String()
		}
	}
	return ""
}

// CreateSpanFromTraceId creates a new span that continues from an existing trace ID.
// This is useful for linking traces across different services or agents.
//
// traceId: The trace ID as a hexadecimal string (32 characters)
// parentSpanId: Optional parent span ID as a hexadecimal string (16 characters).
//
//	If provided, the new span will be a child of this span.
//
// spanName: Name for the new span (default: "continued_span")
// Returns: A context with the new span and the span itself. Use it with defer span.End().
func CreateSpanFromTraceId(ctx context.Context, traceId string, parentSpanId string, spanName string) (context.Context, trace.Span) {
	if spanName == "" {
		spanName = "continued_span"
	}

	// Parse trace ID
	traceID, err := trace.TraceIDFromHex(traceId)
	if err != nil {
		// Fallback: create a new span
		ctx, span := tracer.Start(ctx, spanName)
		setComponentTagIfSet(span)
		return ctx, span
	}

	// Parse parent span ID if provided
	var spanID trace.SpanID
	if parentSpanId != "" {
		spanID, err = trace.SpanIDFromHex(parentSpanId)
		if err != nil {
			// If parent span ID is invalid, use zero span ID
			spanID = trace.SpanID{}
		}
	}

	// Create a span context
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})

	// Create a context with this span context as the parent
	ctx = trace.ContextWithRemoteSpanContext(ctx, spanContext)

	// Start a new span in this context (it will be a child of the parent span)
	ctx, span := tracer.Start(ctx, spanName)
	setComponentTagIfSet(span)
	return ctx, span
}

// InjectTraceContext injects the current trace context into a carrier (e.g., HTTP headers).
// This allows you to pass trace context to another service.
//
// carrier: Map to inject trace context into (e.g., HTTP headers map)
func InjectTraceContext(ctx context.Context, carrier map[string]string) {
	prop := otel.GetTextMapPropagator()
	prop.Inject(ctx, propagation.MapCarrier(carrier))
}

// ExtractTraceContext extracts trace context from a carrier (e.g., HTTP headers).
// Use this to continue a trace that was started in another service.
//
// carrier: Map containing trace context (e.g., HTTP headers map)
// Returns: A context object that can be used with tracer.Start()
func ExtractTraceContext(ctx context.Context, carrier map[string]string) context.Context {
	prop := otel.GetTextMapPropagator()
	return prop.Extract(ctx, propagation.MapCarrier(carrier))
}

// FeedbackOptions contains options for submitting feedback
type FeedbackOptions struct {
	ThumbsUp *bool  // true for positive, false for negative, nil for neutral
	Comment  string // Optional text comment
}

// GetSpan gets a span by its ID from the AIQA server.
//
// spanId: The span ID as a hexadecimal string (16 characters) or client span ID
// organisationId: Optional organisation ID. If empty, will try to get from AIQA_ORGANISATION_ID
//
//	environment variable. The organisation is typically extracted from the API key during
//	authentication, but the API requires it as a query parameter.
//
// Returns: The span data as a map, or nil if not found, and an error if the request failed
//
// Example:
//
//	span, err := GetSpan(ctx, "abc123...", "")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if span != nil {
//	    log.Printf("Found span: %v", span["name"])
//	}
func GetSpan(ctx context.Context, spanId string, organisationId string) (map[string]interface{}, error) {
	serverURL := os.Getenv("AIQA_SERVER_URL")
	apiKey := os.Getenv("AIQA_API_KEY")
	orgID := organisationId
	if orgID == "" {
		orgID = os.Getenv("AIQA_ORGANISATION_ID")
	}

	if serverURL == "" {
		return nil, fmt.Errorf("AIQA_SERVER_URL is not set. Cannot retrieve span")
	}

	if orgID == "" {
		return nil, fmt.Errorf("Organisation ID is required. Provide it as parameter or set AIQA_ORGANISATION_ID environment variable")
	}

	// Remove trailing slash
	serverURL = strings.TrimSuffix(serverURL, "/")

	// Try both spanId and clientSpanId queries
	queryFields := []string{"spanId", "clientSpanId"}
	for _, queryField := range queryFields {
		url := fmt.Sprintf("%s/span?q=%s:%s&organisation=%s&limit=1", serverURL, queryField, spanId, orgID)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			req.Header.Set("Authorization", fmt.Sprintf("ApiKey %s", apiKey))
		}

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to send request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == 200 {
			var result struct {
				Hits  []map[string]interface{} `json:"hits"`
				Total int                      `json:"total"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return nil, fmt.Errorf("failed to decode response: %w", err)
			}
			if len(result.Hits) > 0 {
				return result.Hits[0], nil
			}
		} else if resp.StatusCode == 400 {
			// Try next query field
			continue
		} else {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("failed to get span: %d %s - %s", resp.StatusCode, resp.Status, string(body))
		}
	}

	return nil, nil
}

// SubmitFeedback submits feedback for a trace by creating a new span with the same trace ID.
// This allows you to add feedback (thumbs-up, thumbs-down, comment) to a trace after it has completed.
//
// traceId: The trace ID as a hexadecimal string (32 characters)
// feedback: Feedback options with ThumbsUp and Comment
// Returns: Error if feedback could not be submitted
//
// Example:
//
//	// Submit positive feedback
//	thumbsUp := true
//	err := SubmitFeedback(ctx, "abc123...", FeedbackOptions{
//	    ThumbsUp: &thumbsUp,
//	    Comment:  "Great response!",
//	})
//
//	// Submit negative feedback
//	thumbsDown := false
//	err := SubmitFeedback(ctx, "abc123...", FeedbackOptions{
//	    ThumbsUp: &thumbsDown,
//	    Comment:  "Incorrect answer",
//	})
func SubmitFeedback(ctx context.Context, traceId string, feedback FeedbackOptions) error {
	if len(traceId) != 32 {
		return fmt.Errorf("invalid trace ID: must be 32 hexadecimal characters")
	}

	// Create a span for feedback with the same trace ID
	ctx, span := CreateSpanFromTraceId(ctx, traceId, "", "feedback")
	defer span.End()

	// Set feedback attributes
	if feedback.ThumbsUp != nil {
		span.SetAttributes(attribute.Bool("feedback.thumbs_up", *feedback.ThumbsUp))
		if *feedback.ThumbsUp {
			span.SetAttributes(attribute.String("feedback.type", "positive"))
		} else {
			span.SetAttributes(attribute.String("feedback.type", "negative"))
		}
	} else {
		span.SetAttributes(attribute.String("feedback.type", "neutral"))
	}

	if feedback.Comment != "" {
		span.SetAttributes(attribute.String("feedback.comment", feedback.Comment))
	}

	// Mark as feedback span
	span.SetAttributes(attribute.String("aiqa.span_type", "feedback"))

	// Flush to ensure it's sent immediately
	return FlushSpans(ctx)
}
