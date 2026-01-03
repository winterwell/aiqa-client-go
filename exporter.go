package aiqa

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/sdk/trace"
)

// SerializableSpan represents a span in a format that can be sent to the AIQA server
type SerializableSpan struct {
	Name                   string                 `json:"name"`
	Kind                   int                    `json:"kind"`
	ParentSpanID           string                 `json:"parentSpanId,omitempty"`
	StartTime              [2]int64               `json:"startTime"`
	EndTime                [2]int64               `json:"endTime"`
	Status                 SpanStatus             `json:"status"`
	Attributes             map[string]interface{} `json:"attributes"`
	Links                  []SpanLink             `json:"links"`
	Events                 []SpanEvent            `json:"events"`
	Resource               map[string]interface{} `json:"resource"`
	TraceID                string                 `json:"traceId"`
	SpanID                 string                 `json:"spanId"`
	TraceFlags             byte                   `json:"traceFlags"`
	Duration               [2]int64               `json:"duration"`
	Ended                  bool                   `json:"ended"`
	InstrumentationLibrary InstrumentationLibrary `json:"instrumentationLibrary"`
}

type SpanStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

type SpanLink struct {
	Context    SpanContext            `json:"context"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
}

type SpanContext struct {
	TraceID string `json:"traceId"`
	SpanID  string `json:"spanId"`
}

type SpanEvent struct {
	Name       string                 `json:"name"`
	Time       [2]int64               `json:"time"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
}

type InstrumentationLibrary struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// AIQAExporter exports spans to the AIQA server API.
// Buffers spans and auto-flushes every flushIntervalSeconds.
// Call Shutdown() before process exit to flush remaining spans.
type AIQAExporter struct {
	serverURL          string
	apiKey             string
	flushInterval      time.Duration
	maxBatchSizeBytes  int
	maxBufferSpans     int    // Maximum number of spans to buffer (prevents unbounded growth)
	maxBufferSizeBytes int    // Maximum total buffer size in bytes (prevents unbounded memory growth)
	buffer             []SerializableSpan
	bufferSpanKeys     map[string]bool // Track (traceId, spanId) tuples to prevent duplicates
	bufferSizeBytes    int             // Track total size of buffered spans in bytes
	spanSizeCache      map[string]int  // Cache span sizes to avoid recalculation (maps spanKey -> size_bytes)
	maxCacheSize       int             // Maximum size of span size cache
	bufferMutex        sync.Mutex
	flushMutex         sync.Mutex
	shutdownRequested  bool
	flushTimer         *time.Timer
}

// NewAIQAExporter creates a new AIQA exporter
func NewAIQAExporter(serverURL, apiKey string, flushIntervalSeconds int) *AIQAExporter {
	serverURL = GetServerURL(serverURL)
	apiKey = GetAPIKey(apiKey)

	// Get maxBufferSpans from environment variable or default
	maxBufferSpans := 10000
	if envValue := os.Getenv("AIQA_MAX_BUFFER_SPANS"); envValue != "" {
		if num := toNumber(envValue); num > 0 {
			maxBufferSpans = num
		}
	}

	// Get maxBufferSizeBytes from environment variable or default (100MB)
	maxBufferSizeBytes := toNumber("100m")
	if envValue := os.Getenv("AIQA_MAX_BUFFER_SIZE_BYTES"); envValue != "" {
		if num := toNumber(envValue); num > 0 {
			maxBufferSizeBytes = num
		}
	}

	exporter := &AIQAExporter{
		serverURL:          serverURL,
		apiKey:             apiKey,
		flushInterval:      time.Duration(flushIntervalSeconds) * time.Second,
		maxBatchSizeBytes: 5 * 1024 * 1024, // 5MB default
		maxBufferSpans:      maxBufferSpans,
		maxBufferSizeBytes:  maxBufferSizeBytes,
		buffer:              make([]SerializableSpan, 0),
		bufferSpanKeys:      make(map[string]bool),
		bufferSizeBytes:     0,
		spanSizeCache:       make(map[string]int),
		maxCacheSize:        maxBufferSpans * 2, // Allow cache to be 2x buffer size
	}

	exporter.startAutoFlush()
	return exporter
}

// ExportSpans exports spans to the AIQA server (implements trace.SpanExporter)
func (e *AIQAExporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}

	// Skip export if server URL is not configured (graceful degradation)
	if e.serverURL == "" {
		return nil
	}

	// Add spans to buffer (thread-safe)
	e.addToBuffer(spans)
	return nil
}

// getSpanSizeLocked gets span size from cache or calculates and caches it
// Must be called with bufferMutex already locked
// Limits cache size to prevent unbounded memory growth
func (e *AIQAExporter) getSpanSizeLocked(spanKey string, serialized SerializableSpan) int {
	if size, ok := e.spanSizeCache[spanKey]; ok {
		return size
	}
	spanJSON, err := json.Marshal(serialized)
	if err != nil {
		// If marshaling fails, estimate based on a reasonable default
		return 1024
	}
	spanSize := len(spanJSON)
	// Only cache if we have valid keys and cache isn't too large
	if spanKey != "" && len(e.spanSizeCache) < e.maxCacheSize {
		e.spanSizeCache[spanKey] = spanSize
	}
	return spanSize
}

// getSpanSize gets span size from cache or calculates and caches it
// Thread-safe: locks mutex when accessing cache
// Limits cache size to prevent unbounded memory growth
func (e *AIQAExporter) getSpanSize(spanKey string, serialized SerializableSpan) int {
	e.bufferMutex.Lock()
	defer e.bufferMutex.Unlock()
	return e.getSpanSizeLocked(spanKey, serialized)
}

// checkThresholdsReached checks if buffer thresholds are reached
// Must be called within bufferMutex
func (e *AIQAExporter) checkThresholdsReached() bool {
	if len(e.buffer) >= e.maxBufferSpans {
		return true
	}
	if e.maxBufferSizeBytes > 0 && e.bufferSizeBytes >= e.maxBufferSizeBytes {
		return true
	}
	return false
}

// addToBuffer adds spans to the buffer in a thread-safe manner
// Deduplicates spans based on (traceId, spanId) to prevent repeated exports.
// Drops spans if buffer exceeds maxBufferSpans or maxBufferSizeBytes to prevent unbounded memory growth.
func (e *AIQAExporter) addToBuffer(spans []trace.ReadOnlySpan) {
	e.bufferMutex.Lock()
	defer e.bufferMutex.Unlock()

	duplicatesCount := 0
	droppedCount := 0
	droppedMemoryCount := 0
	flushInProgress := e.flushMutex.TryLock()
	if flushInProgress {
		e.flushMutex.Unlock()
	}

	for _, span := range spans {
		// Check if buffer is full by span count (prevent unbounded growth)
		if len(e.buffer) >= e.maxBufferSpans {
			if flushInProgress {
				// Flush in progress, drop this span
				droppedCount++
				continue
			}
			// Flush not in progress, will trigger flush after adding spans
			// Continue processing remaining spans to add them before flush
		}

		serialized := e.serializeSpan(span)
		spanKey := serialized.TraceID + ":" + serialized.SpanID
		if !e.bufferSpanKeys[spanKey] {
			// Estimate size of this span when serialized (cache for later use)
			// Note: bufferMutex is already locked, so use getSpanSizeLocked
			spanSize := e.getSpanSizeLocked(spanKey, serialized)

			// Check if buffer is full by memory size (prevent unbounded memory growth)
			if e.maxBufferSizeBytes > 0 && e.bufferSizeBytes+spanSize > e.maxBufferSizeBytes {
				if flushInProgress {
					// Flush in progress, drop this span
					// Don't cache size for dropped spans to prevent memory leak
					droppedMemoryCount++
					continue
				}
				// Flush not in progress, will trigger flush after adding spans
				// Continue processing remaining spans to add them before flush
			}

			e.buffer = append(e.buffer, serialized)
			e.bufferSpanKeys[spanKey] = true
			e.bufferSizeBytes += spanSize
		} else {
			duplicatesCount++
		}
	}

	// Check if thresholds are reached after adding spans
	thresholdReached := e.checkThresholdsReached()

	if droppedCount > 0 {
		fmt.Printf("AIQA: WARNING: Buffer full (%d spans), dropped %d span(s) (flush in progress). Consider increasing maxBufferSpans or fixing server connectivity.\n",
			len(e.buffer), droppedCount)
	}
	if droppedMemoryCount > 0 {
		fmt.Printf("AIQA: WARNING: Buffer memory limit reached (%d bytes / %d bytes), dropped %d span(s) (flush in progress). Consider increasing AIQA_MAX_BUFFER_SIZE_BYTES or fixing server connectivity.\n",
			e.bufferSizeBytes, e.maxBufferSizeBytes, droppedMemoryCount)
	}

	// Trigger immediate flush if threshold reached and flush not in progress
	if thresholdReached && !flushInProgress {
		fmt.Printf("AIQA: Buffer threshold reached (%d spans, %d bytes), triggering immediate flush\n",
			len(e.buffer), e.bufferSizeBytes)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = e.Flush(ctx)
		}()
	}

	if duplicatesCount > 0 {
		fmt.Printf("AIQA: export() added %d span(s) to buffer, skipped %d duplicate(s). Total buffered: %d\n",
			len(spans)-duplicatesCount-droppedCount-droppedMemoryCount, duplicatesCount, len(e.buffer))
	}
}

// serializeSpan converts a ReadOnlySpan to a SerializableSpan
func (e *AIQAExporter) serializeSpan(span trace.ReadOnlySpan) SerializableSpan {
	spanContext := span.SpanContext()

	// Convert start/end times to [seconds, nanoseconds] format
	startTime := span.StartTime()
	endTime := span.EndTime()

	// Convert to Unix timestamp with nanoseconds
	startUnix := startTime.Unix()
	startNano := int64(startTime.Nanosecond())
	endUnix := endTime.Unix()
	endNano := int64(endTime.Nanosecond())

	attributes := make(map[string]interface{})
	for _, kv := range span.Attributes() {
		key := string(kv.Key)
		value := kv.Value.AsInterface()
		attributes[key] = applyDataFilters(key, value)
	}

	resourceAttrs := make(map[string]interface{})
	for _, kv := range span.Resource().Attributes() {
		key := string(kv.Key)
		value := kv.Value.AsInterface()
		resourceAttrs[key] = applyDataFilters(key, value)
	}

	links := make([]SpanLink, 0, len(span.Links()))
	for _, link := range span.Links() {
		linkAttrs := make(map[string]interface{})
		for _, kv := range link.Attributes {
			key := string(kv.Key)
			value := kv.Value.AsInterface()
			linkAttrs[key] = applyDataFilters(key, value)
		}
		links = append(links, SpanLink{
			Context: SpanContext{
				TraceID: link.SpanContext.TraceID().String(),
				SpanID:  link.SpanContext.SpanID().String(),
			},
			Attributes: linkAttrs,
		})
	}

	events := make([]SpanEvent, 0, len(span.Events()))
	for _, event := range span.Events() {
		eventAttrs := make(map[string]interface{})
		for _, kv := range event.Attributes {
			key := string(kv.Key)
			value := kv.Value.AsInterface()
			eventAttrs[key] = applyDataFilters(key, value)
		}
		eventUnix := event.Time.Unix()
		eventNano := int64(event.Time.Nanosecond())
		events = append(events, SpanEvent{
			Name:       event.Name,
			Time:       [2]int64{eventUnix, eventNano},
			Attributes: eventAttrs,
		})
	}

	var parentSpanID string
	if span.Parent().SpanID().IsValid() {
		parentSpanID = span.Parent().SpanID().String()
	}

	return SerializableSpan{
		Name:         span.Name(),
		Kind:         int(span.SpanKind()),
		ParentSpanID: parentSpanID,
		StartTime:    [2]int64{startUnix, startNano},
		EndTime:      [2]int64{endUnix, endNano},
		Status: SpanStatus{
			Code:    int(span.Status().Code),
			Message: span.Status().Description,
		},
		Attributes: attributes,
		Links:      links,
		Events:     events,
		Resource:   resourceAttrs,
		TraceID:    spanContext.TraceID().String(),
		SpanID:     spanContext.SpanID().String(),
		TraceFlags: byte(spanContext.TraceFlags()),
		Duration:   [2]int64{endUnix - startUnix, endNano - startNano},
		Ended:      span.EndTime().After(span.StartTime()),
		InstrumentationLibrary: InstrumentationLibrary{
			Name:    span.InstrumentationLibrary().Name,
			Version: span.InstrumentationLibrary().Version,
		},
	}
}

// removeSpanKeysFromTracking removes span keys from tracking set and size cache (thread-safe).
// Called after successful send to free memory.
func (e *AIQAExporter) removeSpanKeysFromTracking(spans []SerializableSpan) {
	e.bufferMutex.Lock()
	defer e.bufferMutex.Unlock()

	for _, span := range spans {
		spanKey := span.TraceID + ":" + span.SpanID
		delete(e.bufferSpanKeys, spanKey)
		// Also remove from size cache to free memory
		delete(e.spanSizeCache, spanKey)
	}
}

// Flush flushes buffered spans to the server. Thread-safe.
func (e *AIQAExporter) Flush(ctx context.Context) error {
	e.flushMutex.Lock()
	defer e.flushMutex.Unlock()

	e.bufferMutex.Lock()
	spansToFlush := make([]SerializableSpan, len(e.buffer))
	copy(spansToFlush, e.buffer)
	e.buffer = e.buffer[:0]
	e.bufferSizeBytes = 0
	// Note: Do NOT clear bufferSpanKeys here - only clear after successful send
	// to avoid unnecessary clearing/rebuilding on failures
	e.bufferMutex.Unlock()

	if len(spansToFlush) == 0 {
		return nil
	}

	if e.serverURL == "" {
		fmt.Printf("AIQA: WARNING: Skipping flush: AIQA_SERVER_URL is not set. %d span(s) will not be sent.\n", len(spansToFlush))
		// Clear keys for spans that won't be sent
		e.removeSpanKeysFromTracking(spansToFlush)
		return nil
	}

	// Split into batches if needed
	batches := e.splitIntoBatches(spansToFlush)
	if len(batches) > 1 {
		fmt.Printf("AIQA: flush() splitting %d spans into %d batches\n", len(spansToFlush), len(batches))
	}

	// Track successfully sent spans to clear their keys
	var successfullySentSpans []SerializableSpan
	var lastError error

	// Send each batch
	for i, batch := range batches {
		if err := e.sendSpans(ctx, batch); err != nil {
			// If one batch fails, put failed batch and remaining batches back in buffer for retry
			fmt.Printf("AIQA: Error sending batch %d/%d: %v\n", i+1, len(batches), err)
			lastError = err

			// Put failed batch and remaining batches back in buffer
			e.bufferMutex.Lock()
			for _, failedBatch := range batches[i:] {
				e.buffer = append(e.buffer, failedBatch...)
				// Recalculate buffer size using cache when available
				for _, span := range failedBatch {
					spanKey := span.TraceID + ":" + span.SpanID
					// Note: bufferMutex is already locked, so use getSpanSizeLocked
					e.bufferSizeBytes += e.getSpanSizeLocked(spanKey, span)
				}
				// Keys are already in bufferSpanKeys, no need to re-add
			}
			e.bufferMutex.Unlock()
			break
		}
		// Track successfully sent spans
		successfullySentSpans = append(successfullySentSpans, batch...)
	}

	// Clear keys for all successfully sent spans
	if len(successfullySentSpans) > 0 {
		e.removeSpanKeysFromTracking(successfullySentSpans)
	}

	if lastError != nil {
		return lastError
	}

	return nil
}

// splitIntoBatches splits spans into batches based on maxBatchSizeBytes.
// Each batch will be as large as possible without exceeding the limit.
// If a single span exceeds the limit, it will be sent in its own batch with a warning.
func (e *AIQAExporter) splitIntoBatches(spans []SerializableSpan) [][]SerializableSpan {
	if len(spans) == 0 {
		return nil
	}

	var batches [][]SerializableSpan
	var currentBatch []SerializableSpan
	currentBatchSize := 0

	for _, span := range spans {
		// Get size from cache if available, otherwise calculate it
		spanKey := span.TraceID + ":" + span.SpanID
		spanSize := e.getSpanSize(spanKey, span)

		// Check if this single span exceeds the limit
		if spanSize > e.maxBatchSizeBytes {
			// If we have a current batch, save it first
			if len(currentBatch) > 0 {
				batches = append(batches, currentBatch)
				currentBatch = nil
				currentBatchSize = 0
			}

			// Log warning about oversized span
			fmt.Printf("AIQA: Span '%s' (traceId=%s) exceeds maxBatchSizeBytes (%d bytes > %d bytes). Will attempt to send it anyway.\n",
				span.Name, span.TraceID, spanSize, e.maxBatchSizeBytes)
			// Still create a batch with just this span - we'll try to send it
			batches = append(batches, []SerializableSpan{span})
			continue
		}

		// If adding this span would exceed the limit, start a new batch
		if len(currentBatch) > 0 && currentBatchSize+spanSize > e.maxBatchSizeBytes {
			batches = append(batches, currentBatch)
			currentBatch = nil
			currentBatchSize = 0
		}

		currentBatch = append(currentBatch, span)
		currentBatchSize += spanSize
	}

	// Add the last batch if it has any spans
	if len(currentBatch) > 0 {
		batches = append(batches, currentBatch)
	}

	return batches
}

// sendSpans sends spans to the server API
func (e *AIQAExporter) sendSpans(ctx context.Context, spans []SerializableSpan) error {
	if e.serverURL == "" {
		return fmt.Errorf("AIQA_SERVER_URL is not set. Cannot send spans to server")
	}

	url := fmt.Sprintf("%s/span", e.serverURL)
	resp, err := makeRequest(ctx, "POST", url, spans, e.apiKey)
	if err != nil {
		return fmt.Errorf("failed to send spans: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return fmt.Errorf("failed to send spans: %d %s - %s", resp.StatusCode, resp.Status, string(body))
	}
	resp.Body.Close()

	return nil
}

// startAutoFlush starts the auto-flush timer
func (e *AIQAExporter) startAutoFlush() {
	e.flushTimer = time.AfterFunc(e.flushInterval, func() {
		if !e.shutdownRequested {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := e.Flush(ctx); err != nil {
				fmt.Printf("AIQA: Error in auto-flush: %v\n", err)
			}
			if !e.shutdownRequested {
				e.startAutoFlush()
			}
		}
	})
}

// Shutdown shuts down the exporter, flushing any remaining spans
func (e *AIQAExporter) Shutdown(ctx context.Context) error {
	e.shutdownRequested = true

	if e.flushTimer != nil {
		e.flushTimer.Stop()
	}

	return e.Flush(ctx)
}

// Disable clears the serverURL to prevent sending spans
func (e *AIQAExporter) Disable() {
	e.serverURL = ""
	e.apiKey = ""
}
