package aiqa

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joho/godotenv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var (
	tracerProvider         *sdktrace.TracerProvider
	tracer                 trace.Tracer
	exporter               sdktrace.SpanExporter
	samplingRate           float64                                   = 1.0            // Default: sample all traces
	componentTag           string                                    = ""             // Component tag to add to all spans
	tracingEnabled         bool                                      = true           // Whether tracing is enabled (set to false if env vars missing)
	initialized            bool                                      = false          // Whether tracing has been initialized (lazy initialization)
	initMutex              sync.Mutex                                                 // Mutex for thread-safe lazy initialization
	defaultIgnorePatterns  []string                                  = []string{"_*"} // Default ignore patterns (filters properties starting with '_')
	ignoreRecursive        bool                                      = true           // Whether to apply ignore patterns recursively
	clientMutex            sync.RWMutex                                               // Mutex for thread-safe access to client state
	attachedProviders      = make(map[*sdktrace.TracerProvider]bool)                  // Track which providers we've already attached processors to (idempotency)
	attachedProvidersMutex sync.RWMutex                                               // Mutex for attachedProviders map
	usingExternalProvider  bool                                                       // Whether we attached to an existing TracerProvider
)

func init() {
	// Load .env file if it exists (optional - won't error if missing)
	// This allows local development with .env files
	_ = godotenv.Load()

	// Read component tag from environment variable
	if envTag := os.Getenv("AIQA_COMPONENT_TAG"); envTag != "" {
		componentTag = envTag
	}
}

type gatingExporter struct {
	inner   sdktrace.SpanExporter
	enabled atomic.Bool
}

func newGatingExporter(inner sdktrace.SpanExporter, enabled bool) *gatingExporter {
	ge := &gatingExporter{inner: inner}
	ge.enabled.Store(enabled)
	return ge
}

func (g *gatingExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if !g.enabled.Load() {
		return nil
	}
	return g.inner.ExportSpans(ctx, spans)
}

func (g *gatingExporter) Shutdown(ctx context.Context) error {
	return g.inner.Shutdown(ctx)
}

// func (g *gatingExporter) ForceFlush(ctx context.Context) error {
// 	// return g.inner.ForceFlush(ctx) compiler doesnt like this
// 	return nil
// }

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
	FilterInput  func(any) any
	FilterOutput func(any) any
}

// GetAIQAClient initializes and returns the AIQA client singleton.
// This function is called automatically when WithTracing is first used, so you typically
// don't need to call it explicitly. However, you can call it manually if you want to:
// - Check if tracing is enabled (IsTracingEnabled())
// - Initialize before the first WithTracing usage
// - Access the client state for advanced usage
//
// The function loads environment variables (AIQA_SERVER_URL, AIQA_API_KEY, AIQA_COMPONENT_TAG)
// and initializes the tracing system.
//
// The function is idempotent - calling it multiple times is safe and will only initialize once.
func GetAIQAClient() error {
	return ensureTracingInitialized("", "", nil)
}

// InitTracing initializes the OpenTelemetry tracer provider with AIQA exporter
// samplingRate: value between 0 and 1, where 0 = tracing is off, 1 = trace all
// If not provided, reads from AIQA_SAMPLING_RATE environment variable (default: 1.0)
// If a TracerProvider already exists, it will add the AIQA exporter to it instead of creating a new one.
// If AIQA_SERVER_URL or AIQA_API_KEY are not set, tracing will be gracefully disabled
// (functions will execute normally without tracing overhead).
//
// Note: This function is now a wrapper around ensureTracingInitialized for backward compatibility.
// For lazy initialization, use GetAIQAClient() or just use WithTracing (which calls it automatically).
func InitTracing(serverURL, apiKey string, samplingRateArg ...float64) error {
	var rate *float64
	if len(samplingRateArg) > 0 {
		rate = &samplingRateArg[0]
	}
	return ensureTracingInitialized(serverURL, apiKey, rate)
}

// ensureTracingInitialized ensures tracing is initialized (lazy initialization).
// Thread-safe. When already initialized, returns early unless explicit serverURL and apiKey
// are provided (allows re-enabling after a previous graceful disable).
func ensureTracingInitialized(serverURL, apiKey string, samplingRateArg *float64) error {
	initMutex.Lock()
	defer initMutex.Unlock()

	if initialized {
		// Allow re-init when caller provides explicit enabling args (e.g. test: disable then enable)
		if serverURL == "" || apiKey == "" {
			return nil
		}
		initialized = false
	}

	// Mark as initialized before actual initialization to prevent retry loops on error
	initialized = true

	// Now do the actual initialization
	if err := doInitTracing(serverURL, apiKey, samplingRateArg); err != nil {
		initialized = false
		return err
	}
	return nil
}

// doInitTracing performs the actual tracing initialization
// We trust the caller to provide the correct serverURL, which normally excludes the /v1/traces path.
func doInitTracing(serverURL, apiKey string, samplingRateArg *float64) error {
	// Use GetServerURL helper which checks AIQA_SERVER_URL, then OTEL_EXPORTER_OTLP_ENDPOINT, then default
	serverURL = GetServerURL(serverURL)
	if apiKey == "" {
		apiKey = GetAPIKey(apiKey)
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
		if ge, ok := exporter.(*gatingExporter); ok {
			ge.enabled.Store(false)
		}

		// If we created our own provider, shutdown to prevent spans from being sent.
		// If we attached to an external provider, keep it intact and just gate exports.
		if !usingExternalProvider {
			if tracerProvider != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = tracerProvider.Shutdown(ctx)
				tracerProvider = nil
			}
			if exporter != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = exporter.Shutdown(ctx)
				exporter = nil
			}
			tracer = nil
		}
		return nil
	}

	// Clean up any existing tracing infrastructure before creating new one
	// This ensures we can safely toggle between enabled/disabled states
	if tracerProvider != nil || exporter != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if tracerProvider != nil && !usingExternalProvider {
			_ = tracerProvider.Shutdown(ctx)
			tracerProvider = nil
		}
		if exporter != nil && !usingExternalProvider {
			_ = exporter.Shutdown(ctx)
			exporter = nil
		}
		if !usingExternalProvider {
			tracer = nil
		}
	}

	tracingEnabled = true

	// Set sampling rate
	if samplingRateArg != nil {
		samplingRate = *samplingRateArg
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

	// Get timeout from environment variable (in seconds)
	// Supports OTEL_EXPORTER_OTLP_TIMEOUT (standard) or AIQA_EXPORT_TIMEOUT (custom)
	// Default is 30 seconds (more generous than OTLP default of 10s)
	timeout := 30 * time.Second
	if otlpTimeout := os.Getenv("OTEL_EXPORTER_OTLP_TIMEOUT"); otlpTimeout != "" {
		if timeoutSec, err := strconv.ParseFloat(otlpTimeout, 64); err == nil {
			timeout = time.Duration(timeoutSec * float64(time.Second))
		}
	}

	// Build headers for authentication
	headers := map[string]string{
		"Authorization": fmt.Sprintf("ApiKey %s", apiKey),
	}

	// Check if a TracerProvider is already set (e.g. by the application's own OTEL init).
	existingProvider := otel.GetTracerProvider()

	// If user has their own OpenTelemetry initialization, not AIQA:
	// First check the env var AIQA_EXPORTER_FLAG - if set (true/false) this decides whether to add our AIQA exporter.
	// Otherwise our logic is: If that exporter is not pointing to the same endpoint as AIQA,
	// then we add our AIQA exporter as an additional span processor. This means spans will be sent to both:
	// 1. The user's existing exporter(s) (if any)
	// 2. The AIQA exporter (to AIQA server)
	// This allows AIQA tracing to work alongside existing OTEL setups without conflicts.
	// Output logging messages for what happens.
	if sdkProvider, ok := existingProvider.(*sdktrace.TracerProvider); ok && sdkProvider != nil {
		// Real provider already exists - user has their own OTEL initialization

		// Idempotency check: avoid adding duplicate processors if we've already attached to this provider
		attachedProvidersMutex.RLock()
		alreadyAttached := attachedProviders[sdkProvider]
		attachedProvidersMutex.RUnlock()

		if alreadyAttached {
			// Already attached to this provider, skip to avoid duplicates
			if ge, ok := exporter.(*gatingExporter); ok {
				ge.enabled.Store(true)
			}
			tracer = otel.Tracer(AIQATracerName)
			return nil
		}

		// First check AIQA_EXPORTER_FLAG env var - if set, use it to decide
		aiqaExporterFlag := os.Getenv("AIQA_EXPORTER_FLAG")
		shouldSkip := false

		if aiqaExporterFlag != "" {
			// Parse the flag (case-insensitive, accepts "true", "false", "1", "0", "yes", "no")
			aiqaExporterFlagLower := strings.ToLower(strings.TrimSpace(aiqaExporterFlag))
			if aiqaExporterFlagLower == "false" || aiqaExporterFlagLower == "0" || aiqaExporterFlagLower == "no" {
				// Explicitly disabled - skip adding our exporter
				fmt.Printf("AIQA: (skip add) AIQA_EXPORTER_FLAG is set to false, not adding AIQA exporter\n")
				shouldSkip = true
			} else if aiqaExporterFlagLower == "true" || aiqaExporterFlagLower == "1" || aiqaExporterFlagLower == "yes" {
				// Explicitly enabled - add our exporter
				fmt.Printf("AIQA: AIQA_EXPORTER_FLAG is set to true, adding AIQA exporter: %s\n", serverURL)
			} else {
				// Invalid value - log warning and fall through to default logic
				fmt.Printf("AIQA: WARNING: Invalid AIQA_EXPORTER_FLAG value '%s' (expected true/false), using default logic\n", aiqaExporterFlag)
			}
		}

		// If AIQA_EXPORTER_FLAG wasn't set or had invalid value, use default logic:
		// Check if user's OTEL init is already pointing to the same endpoint
		// This can happen if they set OTEL_EXPORTER_OTLP_ENDPOINT to AIQA_SERVER_URL
		if !shouldSkip && aiqaExporterFlag == "" {
			otelEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")

			if otelEndpoint != "" {
				// Normalize the OTEL endpoint for comparison
				otelEndpointNormalized := strings.TrimRight(otelEndpoint, "/")
				otelParsedURL, err := url.Parse(otelEndpointNormalized)
				if err == nil && otelParsedURL.Scheme != "" {
					otelEndpointNormalized = fmt.Sprintf("%s://%s", otelParsedURL.Scheme, otelParsedURL.Host)
					serverURLNormalized := strings.TrimRight(serverURL, "/")
					if otelEndpointNormalized == serverURLNormalized {
						// User's OTEL init is already pointing to the same endpoint
						fmt.Printf("AIQA: (skip add) User's OTEL init is already pointing to the same endpoint: %s\n", otelEndpointNormalized)
						shouldSkip = true
					}
				}
			}
		}

		if shouldSkip {
			// Skip adding our exporter (either explicitly disabled or already pointing to same endpoint)
			// Mark as attached so we don't try again
			attachedProvidersMutex.Lock()
			attachedProviders[sdkProvider] = true
			attachedProvidersMutex.Unlock()
			tracer = otel.Tracer(AIQATracerName)
			return nil
		}

		otlpExporter, err := otlptracehttp.New(
			context.Background(),
			otlptracehttp.WithEndpoint(serverURL),
			otlptracehttp.WithHeaders(headers),
			otlptracehttp.WithTimeout(timeout),
		)
		if err != nil {
			return fmt.Errorf("failed to create OTLP exporter: %w", err)
		}
		exporter = newGatingExporter(otlpExporter, true)

		// Add our span processor to the existing provider
		// Our exporter ensures the API key header is set correctly for AIQA authentication
		if aiqaExporterFlag == "" {
			// Only log this message if we're using default logic (not when explicitly enabled)
			fmt.Printf("AIQA: Adding span processor to existing provider: %s\n", serverURL)
		}
		bsp := sdktrace.NewBatchSpanProcessor(exporter)
		sdkProvider.RegisterSpanProcessor(bsp)

		// Mark as attached for idempotency
		attachedProvidersMutex.Lock()
		attachedProviders[sdkProvider] = true
		attachedProvidersMutex.Unlock()

		usingExternalProvider = true
		tracer = otel.Tracer(AIQATracerName)
		return nil
	}

	otlpExporter, err := otlptracehttp.New(
		context.Background(),
		otlptracehttp.WithEndpoint(serverURL),
		otlptracehttp.WithHeaders(headers),
		otlptracehttp.WithTimeout(timeout),
	)
	if err != nil {
		return fmt.Errorf("failed to create OTLP exporter: %w", err)
	}
	exporter = newGatingExporter(otlpExporter, true)
	usingExternalProvider = false

	// No real provider exists, create a new one
	res, err := resource.New(
		context.Background(),
		resource.WithAttributes(
			attribute.String("service.name", "example-service"),
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
	tracer = otel.Tracer(AIQATracerName)
	fmt.Printf("AIQA: New tracer provider created: %s\n", serverURL)
	return nil
}

// FlushSpans flushes all pending spans to the server
func FlushSpans(ctx context.Context) error {
	if tracerProvider != nil {
		return tracerProvider.ForceFlush(ctx)
	}
	return nil
}

// ShutdownTracing shuts down the tracer provider and exporter.
// Note: If InitTracing attached to an existing TracerProvider, this will only
// gate exports rather than shutting down the shared provider.
// Resets initialized so a subsequent InitTracing/GetAIQAClient can re-initialize.
func ShutdownTracing(ctx context.Context) error {
	if usingExternalProvider {
		if ge, ok := exporter.(*gatingExporter); ok {
			ge.enabled.Store(false)
		}
		tracingEnabled = false
	} else {
		if tracerProvider != nil {
			if err := tracerProvider.Shutdown(ctx); err != nil {
				return err
			}
			tracerProvider = nil
			// Reset global so next InitTracing doesn't reuse a shut-down provider (which would yield non-recording spans)
			otel.SetTracerProvider(sdktrace.NewTracerProvider())
		}
		if exporter != nil {
			_ = exporter.Shutdown(ctx)
			exporter = nil
		}
		tracer = nil
		tracingEnabled = false
		usingExternalProvider = false
	}
	initMutex.Lock()
	initialized = false
	initMutex.Unlock()
	return nil
}

// serializeValue serializes a value to JSON string for span attributes
// Filter functions (getEnabledFilters, applyDataFilters, filterDataRecursive, etc.) are now in object_serialiser.go
// This is kept for backward compatibility but delegates to the new implementation
func serializeValue(value any) string {
	return SerializeValue(value)
}

// SetSpanAttribute sets an attribute on the active span
func SetSpanAttribute(ctx context.Context, attributeName string, attributeValue any) bool {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return false
	}

	if attributeValue == nil {
		return false
	}

	value := reflect.ValueOf(attributeValue)
	for value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	attributeValue = value.Interface()

	if s, ok := attributeValue.(string); ok && s == "" {
		return false
	}

	span.SetAttributes(attribute.String(attributeName, serializeValue(attributeValue)))
	return true
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
// All arguments may be nil. They can be *int, *int32, int, int32, int64, or float64.
// inputTokens: Number of input tokens used (maps to gen_ai.usage.input_tokens)
// outputTokens: Number of output tokens generated (maps to gen_ai.usage.output_tokens)
// totalTokens: Total number of tokens used (maps to gen_ai.usage.total_tokens)
// cachedInputTokens: Number of cached input tokens used (maps to gen_ai.usage.cached_input_tokens)
// Returns: True if at least one token usage attribute was set, False if no active span found
func SetTokenUsage(ctx context.Context, inputTokens any, outputTokens any, totalTokens any, cachedInputTokens any) bool {
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

	if v := normalizeTokenCount(inputTokens); v != nil {
		span.SetAttributes(attribute.Int("gen_ai.usage.input_tokens", *v))
		setCount++
	}
	if v := normalizeTokenCount(outputTokens); v != nil {
		span.SetAttributes(attribute.Int("gen_ai.usage.output_tokens", *v))
		setCount++
	}
	if v := normalizeTokenCount(totalTokens); v != nil {
		span.SetAttributes(attribute.Int("gen_ai.usage.total_tokens", *v))
		setCount++
	}
	if v := normalizeTokenCount(cachedInputTokens); v != nil {
		span.SetAttributes(attribute.Int("gen_ai.usage.cached_input_tokens", *v))
		setCount++
	}

	return setCount > 0
}

func normalizeTokenCount(value any) *int {
	if value == nil {
		return nil
	}

	v := reflect.ValueOf(value)
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := int(v.Int())
		return &n
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n := int(v.Uint())
		return &n
	case reflect.Float32, reflect.Float64:
		n := int(v.Float())
		return &n
	default:
		return nil
	}
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

// GetDefaultIgnorePatterns returns the default ignore patterns applied to all traced inputs and outputs.
// Default: ["_*"] (filters properties starting with '_')
// Returns a copy of the patterns to prevent external modification
func GetDefaultIgnorePatterns() []string {
	clientMutex.RLock()
	defer clientMutex.RUnlock()
	result := make([]string, len(defaultIgnorePatterns))
	copy(result, defaultIgnorePatterns)
	return result
}

// SetDefaultIgnorePatterns sets the default ignore patterns applied to all traced inputs and outputs.
// Set to nil or empty slice to disable default ignore patterns.
// Supports wildcards (e.g., "_*" matches "_apple", "_fruit").
func SetDefaultIgnorePatterns(patterns []string) {
	clientMutex.Lock()
	defer clientMutex.Unlock()
	if patterns == nil {
		defaultIgnorePatterns = []string{}
	} else {
		defaultIgnorePatterns = make([]string, len(patterns))
		copy(defaultIgnorePatterns, patterns)
	}
}

// GetIgnoreRecursive returns whether ignore patterns are applied recursively to nested objects.
// Default: true (recursive filtering enabled)
func GetIgnoreRecursive() bool {
	clientMutex.RLock()
	defer clientMutex.RUnlock()
	return ignoreRecursive
}

// SetIgnoreRecursive sets whether ignore patterns are applied recursively to nested objects.
// When true (default), ignore patterns are applied at all nesting levels.
// When false, ignore patterns are only applied to top-level keys.
func SetIgnoreRecursive(recursive bool) {
	clientMutex.Lock()
	defer clientMutex.Unlock()
	ignoreRecursive = recursive
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
	// Use global tracer when ours is nil (tracing disabled) so we get a noop span and avoid nil dereference
	t := tracer
	if t == nil {
		t = otel.Tracer(AIQATracerName)
	}

	// Parse trace ID
	traceID, err := trace.TraceIDFromHex(traceId)
	if err != nil {
		// Fallback: create a new span
		ctx, span := t.Start(ctx, spanName)
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
	ctx, span := t.Start(ctx, spanName)
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
func GetSpan(ctx context.Context, spanId string, organisationId string) (map[string]any, error) {
	serverURL := GetServerURL("")
	apiKey := GetAPIKey("")
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

	url := fmt.Sprintf("%s/span/%s?limit=1", serverURL, spanId)
	resp, err := makeRequest(ctx, "GET", url, nil, apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get span: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get span: %d %s - %s", resp.StatusCode, resp.Status, string(body))
	}
	var result struct {
		Hits  []map[string]any `json:"hits"`
		Total int              `json:"total"`
	}
	if err := parseJSONResponse(resp, &result); err != nil {
		return nil, err
	}
	if len(result.Hits) == 0 {
		return nil, nil
	}
	return result.Hits[0], nil
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
		if *feedback.ThumbsUp {
			span.SetAttributes(attribute.String("feedback.value", "positive"))
		} else {
			span.SetAttributes(attribute.String("feedback.value", "negative"))
		}
	} else {
		span.SetAttributes(attribute.String("feedback.value", "neutral"))
	}

	if feedback.Comment != "" {
		span.SetAttributes(attribute.String("feedback.comment", feedback.Comment))
	}

	// Mark as feedback span
	span.SetAttributes(attribute.String("aiqa.span_type", "feedback"))

	// Flush to ensure it's sent immediately
	return FlushSpans(ctx)
}
