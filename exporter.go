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
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
)

// SerializableSpan represents a span in a format that can be sent to the AIQA server
type SerializableSpan struct {
	Name           string                 `json:"name"`
	Kind           int                    `json:"kind"`
	ParentSpanID   string                 `json:"parentSpanId,omitempty"`
	StartTime      [2]int64               `json:"startTime"`
	EndTime        [2]int64               `json:"endTime"`
	Status         SpanStatus             `json:"status"`
	Attributes     map[string]interface{} `json:"attributes"`
	Links          []SpanLink             `json:"links"`
	Events         []SpanEvent            `json:"events"`
	Resource       map[string]interface{} `json:"resource"`
	TraceID        string                 `json:"traceId"`
	SpanID         string                 `json:"spanId"`
	TraceFlags     byte                   `json:"traceFlags"`
	Duration       [2]int64               `json:"duration"`
	Ended          bool                   `json:"ended"`
	InstrumentationLibrary InstrumentationLibrary `json:"instrumentationLibrary"`
}

type SpanStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

type SpanLink struct {
	Context   SpanContext            `json:"context"`
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
	serverURL         string
	apiKey            string
	flushInterval     time.Duration
	maxBatchSizeBytes int
	maxBufferSpans    int // Maximum number of spans to buffer (prevents unbounded growth)
	buffer            []SerializableSpan
	bufferSpanKeys    map[string]bool // Track (traceId, spanId) tuples to prevent duplicates
	bufferMutex       sync.Mutex
	flushMutex        sync.Mutex
	shutdownRequested bool
	flushTimer        *time.Timer
	client            *http.Client
}

// NewAIQAExporter creates a new AIQA exporter
func NewAIQAExporter(serverURL, apiKey string, flushIntervalSeconds int) *AIQAExporter {
	if serverURL == "" {
		serverURL = os.Getenv("AIQA_SERVER_URL")
	}
	if apiKey == "" {
		apiKey = os.Getenv("AIQA_API_KEY")
	}
	
	// Remove trailing slash
	serverURL = strings.TrimSuffix(serverURL, "/")
	
	exporter := &AIQAExporter{
		serverURL:         serverURL,
		apiKey:            apiKey,
		flushInterval:     time.Duration(flushIntervalSeconds) * time.Second,
		maxBatchSizeBytes: 5 * 1024 * 1024, // 5MB default
		maxBufferSpans:    10000,           // Maximum spans to buffer (prevents unbounded growth)
		buffer:            make([]SerializableSpan, 0),
		bufferSpanKeys:    make(map[string]bool),
		client:            &http.Client{Timeout: 30 * time.Second},
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

// addToBuffer adds spans to the buffer in a thread-safe manner
// Deduplicates spans based on (traceId, spanId) to prevent repeated exports.
// Drops spans if buffer exceeds maxBufferSpans to prevent unbounded memory growth.
func (e *AIQAExporter) addToBuffer(spans []trace.ReadOnlySpan) {
	e.bufferMutex.Lock()
	defer e.bufferMutex.Unlock()
	
	duplicatesCount := 0
	droppedCount := 0
	
	for _, span := range spans {
		// Check if buffer is full (prevent unbounded growth)
		if len(e.buffer) >= e.maxBufferSpans {
			droppedCount++
			continue
		}
		
		serialized := e.serializeSpan(span)
		spanKey := serialized.TraceID + ":" + serialized.SpanID
		if !e.bufferSpanKeys[spanKey] {
			e.buffer = append(e.buffer, serialized)
			e.bufferSpanKeys[spanKey] = true
		} else {
			duplicatesCount++
		}
	}
	
	if droppedCount > 0 {
		fmt.Printf("AIQA: WARNING: Buffer full (%d spans), dropped %d span(s). Consider increasing maxBufferSpans or fixing server connectivity.\n",
			len(e.buffer), droppedCount)
	}
	if duplicatesCount > 0 {
		fmt.Printf("AIQA: export() added %d span(s) to buffer, skipped %d duplicate(s). Total buffered: %d\n",
			len(spans)-duplicatesCount-droppedCount, duplicatesCount, len(e.buffer))
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
	span.Resource().Attributes().Range(func(kv attribute.KeyValue) bool {
		key := string(kv.Key)
		value := kv.Value.AsInterface()
		resourceAttrs[key] = applyDataFilters(key, value)
		return true
	})
	
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
		Name:     span.Name(),
		Kind:     int(span.SpanKind()),
		ParentSpanID: parentSpanID,
		StartTime: [2]int64{startUnix, startNano},
		EndTime:   [2]int64{endUnix, endNano},
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

// removeSpanKeysFromTracking removes span keys from tracking set (thread-safe).
// Called after successful send to free memory.
func (e *AIQAExporter) removeSpanKeysFromTracking(spans []SerializableSpan) {
	e.bufferMutex.Lock()
	defer e.bufferMutex.Unlock()
	
	for _, span := range spans {
		spanKey := span.TraceID + ":" + span.SpanID
		delete(e.bufferSpanKeys, spanKey)
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
	
	// Send each batch
	for i, batch := range batches {
		if err := e.sendSpans(ctx, batch); err != nil {
			// If one batch fails, continue with others but return error
			fmt.Printf("AIQA: Error sending batch %d/%d: %v\n", i+1, len(batches), err)
			// Put remaining batches back in buffer for retry
			if i+1 < len(batches) {
				e.bufferMutex.Lock()
				for _, remainingBatch := range batches[i+1:] {
					e.buffer = append(e.buffer, remainingBatch...)
					// Keys are already in bufferSpanKeys, no need to re-add
				}
				e.bufferMutex.Unlock()
			}
			// Clear keys only for successfully sent spans
			if len(successfullySentSpans) > 0 {
				e.removeSpanKeysFromTracking(successfullySentSpans)
			}
			return err
		}
		// Track successfully sent spans
		successfullySentSpans = append(successfullySentSpans, batch...)
	}
	
	// Clear keys for all successfully sent spans
	if len(successfullySentSpans) > 0 {
		e.removeSpanKeysFromTracking(successfullySentSpans)
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
		// Estimate size of this span when serialized
		spanJSON, err := json.Marshal(span)
		if err != nil {
			// If marshaling fails, estimate based on a reasonable default
			spanJSON = []byte("{}")
		}
		spanSize := len(spanJSON)
		
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
	
	jsonData, err := json.Marshal(spans)
	if err != nil {
		return fmt.Errorf("failed to marshal spans: %w", err)
	}
	
	url := fmt.Sprintf("%s/span", e.serverURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("ApiKey %s", e.apiKey))
	}
	
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send spans: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to send spans: %d %s - %s", resp.StatusCode, resp.Status, string(body))
	}
	
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

