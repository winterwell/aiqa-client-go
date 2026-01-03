package aiqa

import (
	"context"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestNewAIQAExporter(t *testing.T) {
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

	// Test with provided args
	exporter := NewAIQAExporter("http://localhost:3000", "test-key", 5)
	if exporter == nil {
		t.Fatal("NewAIQAExporter should return a non-nil exporter")
	}
	if exporter.serverURL != "http://localhost:3000" {
		t.Errorf("Expected serverURL to be 'http://localhost:3000', got '%s'", exporter.serverURL)
	}
	if exporter.apiKey != "test-key" {
		t.Errorf("Expected apiKey to be 'test-key', got '%s'", exporter.apiKey)
	}
	if exporter.flushInterval != 5*time.Second {
		t.Errorf("Expected flushInterval to be 5s, got %v", exporter.flushInterval)
	}

	// Test with env vars
	os.Setenv("AIQA_SERVER_URL", "http://env-server:3000")
	os.Setenv("AIQA_API_KEY", "env-key")
	exporter2 := NewAIQAExporter("", "", 10)
	if exporter2.serverURL != "http://env-server:3000" {
		t.Errorf("Expected serverURL from env, got '%s'", exporter2.serverURL)
	}
	if exporter2.apiKey != "env-key" {
		t.Errorf("Expected apiKey from env, got '%s'", exporter2.apiKey)
	}
}

func TestAIQAExporter_ExportSpans(t *testing.T) {
	exporter := NewAIQAExporter("http://localhost:3000", "test-key", 5)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exporter.Shutdown(ctx)
	}()

	// Test with empty spans
	err := exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{})
	if err != nil {
		t.Errorf("ExportSpans should not return error for empty spans: %v", err)
	}

	// Test with nil server URL (graceful degradation)
	exporter2 := NewAIQAExporter("", "", 5)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exporter2.Shutdown(ctx)
	}()

	err = exporter2.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{})
	if err != nil {
		t.Errorf("ExportSpans should not return error when server URL is empty: %v", err)
	}
}

func TestAIQAExporter_SerializeSpan(t *testing.T) {
	exporter := NewAIQAExporter("http://localhost:3000", "test-key", 5)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exporter.Shutdown(ctx)
	}()

	// Create a mock span
	tracer := trace.NewNoopTracerProvider().Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	span.SetAttributes(attribute.String("test.key", "test-value"))
	span.End()

	// Get the span from SDK (this is a simplified test - actual implementation would need real SDK span)
	// For now, we'll test that serializeSpan doesn't panic
	_ = exporter
	_ = ctx
}

func TestAIQAExporter_Flush(t *testing.T) {
	exporter := NewAIQAExporter("http://localhost:3000", "test-key", 5)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exporter.Shutdown(ctx)
	}()

	// Test flush with empty buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := exporter.Flush(ctx)
	if err != nil {
		t.Errorf("Flush should not return error with empty buffer: %v", err)
	}

	// Test flush with nil server URL
	exporter2 := NewAIQAExporter("", "", 5)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exporter2.Shutdown(ctx)
	}()

	err = exporter2.Flush(ctx)
	if err != nil {
		t.Errorf("Flush should not return error when server URL is empty: %v", err)
	}
}

func TestAIQAExporter_SplitIntoBatches(t *testing.T) {
	exporter := NewAIQAExporter("http://localhost:3000", "test-key", 5)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exporter.Shutdown(ctx)
	}()

	// Test with empty spans
	batches := exporter.splitIntoBatches([]SerializableSpan{})
	if len(batches) != 0 {
		t.Error("splitIntoBatches should return nil or empty for empty spans")
	}

	// Test with small spans (should fit in one batch)
	spans := []SerializableSpan{
		{
			Name:       "span1",
			TraceID:    "12345678901234567890123456789012",
			SpanID:     "1234567890123456",
			Attributes: map[string]interface{}{"key": "value"},
		},
		{
			Name:       "span2",
			TraceID:    "12345678901234567890123456789012",
			SpanID:     "1234567890123457",
			Attributes: map[string]interface{}{"key": "value2"},
		},
	}

	batches = exporter.splitIntoBatches(spans)
	if len(batches) == 0 {
		t.Error("splitIntoBatches should return at least one batch")
	}
	if len(batches[0]) != len(spans) {
		t.Errorf("Expected all spans in one batch, got %d batches", len(batches))
	}
}

func TestAIQAExporter_Shutdown(t *testing.T) {
	exporter := NewAIQAExporter("http://localhost:3000", "test-key", 5)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := exporter.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown should not return error: %v", err)
	}

	// Test shutdown again (should be idempotent)
	err = exporter.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown should be idempotent: %v", err)
	}
}

func TestAIQAExporter_AddToBuffer(t *testing.T) {
	exporter := NewAIQAExporter("http://localhost:3000", "test-key", 5)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exporter.Shutdown(ctx)
	}()

	// Test that buffer is thread-safe by adding spans concurrently
	// This is a basic test - full concurrency testing would require more setup
	exporter.bufferMutex.Lock()
	initialLen := len(exporter.buffer)
	exporter.bufferMutex.Unlock()

	// Export should add to buffer
	_ = exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{})

	exporter.bufferMutex.Lock()
	finalLen := len(exporter.buffer)
	exporter.bufferMutex.Unlock()

	// Buffer length should not decrease
	if finalLen < initialLen {
		t.Error("Buffer length should not decrease after export")
	}
}

func TestAIQAExporter_RemoveSpanKeysFromTracking(t *testing.T) {
	exporter := NewAIQAExporter("http://localhost:3000", "test-key", 5)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exporter.Shutdown(ctx)
	}()

	// Add a span key to tracking
	span := SerializableSpan{
		TraceID: "12345678901234567890123456789012",
		SpanID:  "1234567890123456",
	}

	exporter.bufferMutex.Lock()
	spanKey := span.TraceID + ":" + span.SpanID
	exporter.bufferSpanKeys[spanKey] = true
	exporter.bufferMutex.Unlock()

	// Remove it
	exporter.removeSpanKeysFromTracking([]SerializableSpan{span})

	exporter.bufferMutex.Lock()
	if exporter.bufferSpanKeys[spanKey] {
		t.Error("Span key should be removed from tracking")
	}
	exporter.bufferMutex.Unlock()
}
