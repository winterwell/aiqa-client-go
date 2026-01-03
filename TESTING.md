# Testing the Go Client Package

## Prerequisites

1. Ensure you have Go 1.21+ installed
2. Install dependencies:
```bash
cd aiqa-client-go
go mod download
```

## Running Unit Tests

The package includes unit tests using Go's built-in `testing` package. To run them:

### Running All Tests

```bash
# Run all tests
go test

# Run with verbose output
go test -v

# Run with coverage report
go test -cover

# Generate HTML coverage report
go test -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### Running Specific Test Files

```bash
# Run tests in a specific file
go test -v tracing_test.go tracing.go

# Run tests in a specific package
go test ./...
```

### Running Specific Tests

```bash
# Run a specific test function
go test -v -run TestInitTracing

# Run tests matching a pattern
go test -v -run "TestWithTracing.*"
```

### Test Output Options

```bash
# Verbose output (shows each test name)
go test -v

# Show test coverage
go test -cover

# Show coverage for specific functions
go test -cover -coverprofile=coverage.out
go tool cover -func=coverage.out

# Generate HTML coverage report
go test -coverprofile=coverage.out
go tool cover -html=coverage.out -o coverage.html
```

## Test Structure

The test files follow Go conventions:
- Test files end with `_test.go`
- Test functions start with `Test`
- Tests are in the same package as the code being tested

### Test Files

- `tracing_test.go` - Tests for tracing functions (InitTracing, WithTracing, data filters, etc.)
- `exporter_test.go` - Tests for exporter functions (AIQAExporter, span serialization, etc.)
- `experiment_runner_test.go` - Tests for experiment runner functions

## Test Categories

### Unit Tests

These tests focus on individual functions and don't require a running server:

- **Tracing initialization**: Tests for `InitTracing` with various configurations
- **Function wrapping**: Tests for `WithTracing` with different function signatures
- **Data filtering**: Tests for password/JWT/API key filtering
- **Span attributes**: Tests for setting span attributes, token usage, provider/model
- **Trace/span IDs**: Tests for getting trace and span IDs
- **Struct validation**: Tests for data structure creation and validation

### Integration Tests

Some tests may require a running AIQA server. These tests will fail gracefully if the server is not available:

- **Exporter tests**: Tests that send spans to the server
- **Experiment runner tests**: Tests that interact with the server API

## Running Tests Without a Server

Most tests are designed to work without a running server. The package gracefully handles missing server configuration:

```bash
# Tests will pass even without AIQA_SERVER_URL set
unset AIQA_SERVER_URL
unset AIQA_API_KEY
go test
```

When environment variables are missing, tracing is disabled but functions continue to work normally.

## Test Environment Variables

Tests automatically clean up environment variables, but you can set them for integration testing:

```bash
export AIQA_SERVER_URL="http://localhost:3000"
export AIQA_API_KEY="test-key"
export AIQA_ORGANISATION_ID="test-org"
go test -v
```

## Example Test Run

```bash
$ go test -v

=== RUN   TestInitTracing_WithMissingEnvVars
--- PASS: TestInitTracing_WithMissingEnvVars (0.00s)
=== RUN   TestInitTracing_WithProvidedArgs
--- PASS: TestInitTracing_WithProvidedArgs (0.01s)
=== RUN   TestTraceIDSampler
--- PASS: TestTraceIDSampler (0.00s)
=== RUN   TestWithTracing_SyncFunction
--- PASS: TestWithTracing_SyncFunction (0.00s)
...
PASS
ok      github.com/winterwell/aiqa-client-go    0.123s
```

## Coverage Goals

Aim for high test coverage, especially for:
- Core tracing functions
- Data filtering logic
- Error handling paths
- Edge cases

Check coverage:
```bash
go test -coverprofile=coverage.out
go tool cover -func=coverage.out
```

## Writing New Tests

When adding new functionality, add corresponding tests:

1. Create a test function: `func TestFunctionName(t *testing.T)`
2. Use table-driven tests for multiple test cases
3. Clean up environment variables and resources in `defer` blocks
4. Test both success and error cases
5. Test edge cases (nil values, empty strings, etc.)

Example:
```go
func TestNewFunction(t *testing.T) {
    // Setup
    originalValue := os.Getenv("SOME_VAR")
    defer func() {
        if originalValue != "" {
            os.Setenv("SOME_VAR", originalValue)
        } else {
            os.Unsetenv("SOME_VAR")
        }
    }()

    // Test cases
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {"valid input", "test", "result", false},
        {"empty input", "", "", true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := NewFunction(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("NewFunction() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if got != tt.want {
                t.Errorf("NewFunction() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

## Continuous Integration

Tests should be run in CI/CD pipelines:

```yaml
# Example GitHub Actions workflow
- name: Run tests
  run: go test -v -coverprofile=coverage.out

- name: Upload coverage
  uses: codecov/codecov-action@v3
  with:
    file: ./coverage.out
```

## Troubleshooting

### Import Errors
- Ensure you're in the correct directory: `cd aiqa-client-go`
- Run `go mod download` to fetch dependencies
- Check that `go.mod` is up to date

### Test Failures
- Check that environment variables are properly cleaned up
- Verify that tracing is properly initialized in tests
- Ensure that deferred cleanup functions are called

### Coverage Issues
- Some code paths may be hard to test (e.g., network errors)
- Focus on testing business logic and error handling
- Use mocks for external dependencies when appropriate

### Performance
- Tests should run quickly (< 1 second for unit tests)
- Use `-short` flag to skip long-running tests: `go test -short`
- Avoid network calls in unit tests when possible

## Best Practices

1. **Isolation**: Each test should be independent and not rely on other tests
2. **Cleanup**: Always clean up resources (env vars, spans, etc.) in defer blocks
3. **Naming**: Use descriptive test names that explain what is being tested
4. **Table-driven tests**: Use table-driven tests for multiple similar test cases
5. **Error messages**: Include helpful error messages that explain what went wrong
6. **Coverage**: Aim for high coverage but focus on testing important logic
7. **Speed**: Keep tests fast - unit tests should complete in milliseconds

## Mocking

For integration tests that require a server, consider using:
- HTTP test servers: `net/http/httptest`
- Mock implementations of interfaces
- Test fixtures for complex data structures

Example with httptest:
```go
func TestWithMockServer(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok"})
    }))
    defer server.Close()

    // Use server.URL in your test
    exporter := NewAIQAExporter(server.URL, "test-key", 5)
    // ... test code ...
}
```

