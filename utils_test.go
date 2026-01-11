package aiqa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetOrganisation(t *testing.T) {
	// Create a mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/organisation/test-org-123" {
			t.Errorf("Expected path /organisation/test-org-123, got %s", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("Expected GET method, got %s", r.Method)
		}
		authHeader := r.Header.Get("Authorization")
		if authHeader != "ApiKey test-key" {
			t.Errorf("Expected Authorization header 'ApiKey test-key', got %s", authHeader)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"test-org-123","name":"Test Organisation"}`))
	}))
	defer server.Close()

	ctx := context.Background()
	result, err := GetOrganisation(ctx, "test-org-123", server.URL, "test-key")
	if err != nil {
		t.Fatalf("GetOrganisation failed: %v", err)
	}

	if result["id"] != "test-org-123" {
		t.Errorf("Expected id 'test-org-123', got %v", result["id"])
	}
	if result["name"] != "Test Organisation" {
		t.Errorf("Expected name 'Test Organisation', got %v", result["name"])
	}
}

func TestGetOrganisation_WithEnvVars(t *testing.T) {
	// Create a mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"test-org-123","name":"Test Organisation"}`))
	}))
	defer server.Close()

	// Set environment variables
	t.Setenv("AIQA_SERVER_URL", server.URL)
	t.Setenv("AIQA_API_KEY", "env-key")

	ctx := context.Background()
	result, err := GetOrganisation(ctx, "test-org-123", "", "")
	if err != nil {
		t.Fatalf("GetOrganisation failed: %v", err)
	}

	if result["id"] != "test-org-123" {
		t.Errorf("Expected id 'test-org-123', got %v", result["id"])
	}
}

func TestGetAPIKeyInfo(t *testing.T) {
	// Create a mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api-key/test-key-123" {
			t.Errorf("Expected path /api-key/test-key-123, got %s", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("Expected GET method, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"test-key-123","name":"Test API Key"}`))
	}))
	defer server.Close()

	ctx := context.Background()
	result, err := GetAPIKeyInfo(ctx, "test-key-123", server.URL, "test-key")
	if err != nil {
		t.Fatalf("GetAPIKeyInfo failed: %v", err)
	}

	if result["id"] != "test-key-123" {
		t.Errorf("Expected id 'test-key-123', got %v", result["id"])
	}
	if result["name"] != "Test API Key" {
		t.Errorf("Expected name 'Test API Key', got %v", result["name"])
	}
}

func TestFormatHTTPError(t *testing.T) {
	// Create a mock response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Not Found"))
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("Failed to create test request: %v", err)
	}
	defer resp.Body.Close()

	errorMsg := FormatHTTPError(resp, "test operation")
	if errorMsg == "" {
		t.Error("FormatHTTPError should return a non-empty error message")
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Expected status code 404, got %d", resp.StatusCode)
	}
}
