# AIQA Go Client

OpenTelemetry-based client for AIQA that logs traces to the server.

Repository: [https://github.com/winterwell/aiqa-client-go](https://github.com/winterwell/aiqa-client-go)

## Installation

### From Go Modules (recommended)

Add the module to your Go project:

```bash
go get github.com/winterwell/aiqa-client-go
```

Or specify a version:

```bash
go get github.com/winterwell/aiqa-client-go@v0.6.2
```

### From Source

```bash
git clone https://github.com/winterwell/aiqa-client-go.git
cd aiqa-client-go
go mod download
```

## Setup

1. Set environment variables (or pass them to `InitTracing`):
```bash
export AIQA_SERVER_URL=http://localhost:4318
export AIQA_API_KEY=your-api-key
export AIQA_COMPONENT_TAG=mynamespace.mysystem  # Optional: component tag for all spans
```

See `env.example` for a complete list of all available environment variables.

**Note:** If `AIQA_SERVER_URL` or `AIQA_API_KEY` are not set, tracing will be automatically disabled. You'll see one warning message at the start, and your application will continue to run without tracing. You can check if tracing is enabled via `aiqa.IsTracingEnabled()`.

## Usage

### Basic Example

```go
package main

import (
    "context"
    "fmt"
    "time"
    "github.com/winterwell/aiqa-client-go"
)

func main() {
    // Initialize tracing - usually done automatically

    // Wrap a function with tracing
    multiply := func(x, y int) int {
        return x * y
    }
    tracedMultiply := aiqa.WithTracing(multiply).(func(int, int) int)
    
    result := tracedMultiply(5, 3)
    fmt.Println(result)

    // On a server or long-running process, the traces will be flushed and sent
    // Here we will explicitly flush them
    // Flush spans before exit (for short-lived processes)
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := aiqa.FlushSpans(ctx); err != nil {
        fmt.Printf("Failed to flush spans: %v\n", err)
    }
}
```

### With Error Handling

```go
divide := func(x, y float64) (float64, error) {
    if y == 0 {
        return 0, fmt.Errorf("division by zero")
    }
    return x / y, nil
}
tracedDivide := aiqa.WithTracing(divide).(func(float64, float64) (float64, error))
result, err := tracedDivide(10, 2)
```

### With Context

```go
processData := func(ctx context.Context, data string) (string, error) {
    // Your function logic here
    return fmt.Sprintf("Processed: %s", data), nil
}
tracedProcess := aiqa.WithTracing(processData).(func(context.Context, string) (string, error))
result, err := tracedProcess(context.Background(), "test")
```

### Custom Span Name

```go
options := aiqa.TracingOptions{
    Name: "custom-function-name",
}
tracedFn := aiqa.WithTracing(myFunction, options)
```

### Setting Span Attributes

```go
ctx := context.Background()
aiqa.SetSpanAttribute(ctx, "custom.attribute", "value")
```

### Setting Component Tag

The component tag allows you to identify which component/system generated the spans. It can be set programmatically or via the `AIQA_COMPONENT_TAG` environment variable. The component tag is set as `gen_ai.component.id` following OpenTelemetry semantic conventions:

```go
// Set component tag programmatically
aiqa.SetComponentTag("mynamespace.mysystem")

// Or set via environment variable:
// export AIQA_COMPONENT_TAG="mynamespace.mysystem"
```

### Checking if Tracing is Enabled

You can check if tracing is enabled (useful for debugging):

```go
if aiqa.IsTracingEnabled() {
    fmt.Println("Tracing is enabled")
} else {
    fmt.Println("Tracing is disabled (check AIQA_SERVER_URL and AIQA_API_KEY)")
}
```

### Getting Server URL

You can retrieve the configured server URL:

```go
serverUrl := aiqa.GetServerURL("")
fmt.Printf("Logging to server: %s\n", serverUrl)
```

## Configuration

The client can be configured via environment variables or by passing parameters to `InitTracing`:

### Required
- `AIQA_SERVER_URL`: URL of the AIQA server (default: empty, must be set)
- `AIQA_API_KEY`: API key for authentication (default: empty). Get your API key from the AIQA Webapp: click on your organisation, then click on "API Keys".

### Optional
- `AIQA_COMPONENT_TAG`: Component tag to add to all spans (e.g., "mynamespace.mysystem"). Allows filtering by which component/system generated the spans.
- `AIQA_ORGANISATION_ID`: Your organisation ID within AIQA. Used when retrieving spans from the server. Not needed for tracing, which uses the API key to authenticate.
- `AIQA_SAMPLING_RATE`: Sampling rate between 0 and 1 (default: 1.0 = sample all). Set to a value between 0.0 and 1.0 to sample a fraction of traces (e.g., 0.1 = 10%). Sampling is done based on the trace-id, so a whole trace is either sampled or not sampled.
- `AIQA_DATA_FILTERS`: Comma-separated list of data filters to apply. Default: "RemovePasswords, RemoveJWT, RemoveAuthHeaders, RemoveAPIKeys". Set to "false" to disable all filters.
- `AIQA_MAX_OBJECT_STR_CHARS`: Maximum size for an object string representation in characters (i.e. max input/output size for a span). Default: 1m (1,048,576 characters). Supports units: k (kilo), m (mega), g (giga), or plain number.
- `AIQA_MAX_BUFFER_SPANS`: Maximum number of spans to buffer in memory before sending to server. Default: 10000. When the buffer is full, new spans will be dropped and a warning will be logged.
- `AIQA_FLUSH_INTERVAL_SECONDS`: How often to flush spans to the server in seconds. Default: 5.
- `AIQA_MAX_BUFFER_SIZE_BYTES`: Maximum total buffer size in bytes (memory limit). Default: 100m. When the buffer size exceeds this limit, the buffer will be flushed and a warning will be logged. Supports units: k (kilo), m (mega), g (giga), or plain number.

### OTLP Compatibility

The client also supports standard OpenTelemetry environment variables for compatibility:
- `OTEL_EXPORTER_OTLP_ENDPOINT`: Alternative to `AIQA_SERVER_URL`
- `OTEL_EXPORTER_OTLP_HEADERS`: Alternative to `AIQA_API_KEY` (format: "Authorization=ApiKey <key>")

## Flushing Spans

Spans are automatically flushed every 5 seconds. To flush immediately:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
aiqa.FlushSpans(ctx)
```

## Shutting Down

Always call `ShutdownTracing` before your program exits to ensure all spans are sent:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
aiqa.ShutdownTracing(ctx)
```

## Advanced Features

### Getting Trace and Span IDs

You can retrieve the current trace ID and span ID:

```go
ctx := context.Background()
traceId := aiqa.GetTraceId(ctx)
spanId := aiqa.GetSpanId(ctx)
fmt.Printf("Trace ID: %s, Span ID: %s\n", traceId, spanId)
```

### Setting Conversation ID

Group multiple traces together that are part of the same conversation:

```go
ctx := context.Background()
aiqa.SetConversationId(ctx, "user-session-123")
```

### Manually Setting Token Usage

If your AI provider doesn't follow standard patterns, you can manually set token usage:

```go
ctx := context.Background()
inputTokens := 100
outputTokens := 50
totalTokens := 150
aiqa.SetTokenUsage(ctx, &inputTokens, &outputTokens, &totalTokens, nil)
```

### Manually Setting Provider and Model

Explicitly set provider and model information:

```go
ctx := context.Background()
provider := "openai"
model := "gpt-4"
aiqa.SetProviderAndModel(ctx, &provider, &model)
```

### Continuing Traces Across Services

Create a span that continues from an existing trace ID:

```go
ctx := context.Background()
traceId := "abc123..." // 32 character hex string
parentSpanId := "def456..." // 16 character hex string (optional)
ctx, span := aiqa.CreateSpanFromTraceId(ctx, traceId, parentSpanId, "my-span-name")
defer span.End()
```

### Injecting/Extracting Trace Context

Pass trace context between services via HTTP headers or other carriers:

```go
// Inject trace context into HTTP headers
headers := make(map[string]string)
aiqa.InjectTraceContext(ctx, headers)
// Now use headers in your HTTP request

// Extract trace context from HTTP headers
ctx = aiqa.ExtractTraceContext(ctx, headers)
```

### Getting and Submitting Feedback

Retrieve a span and submit feedback for a trace:

```go
// Get a span by ID
ctx := context.Background()
span, err := aiqa.GetSpan(ctx, "span-id-here", "organisation-id")
if err != nil {
    log.Fatal(err)
}

// Submit feedback for a trace
thumbsUp := true
err = aiqa.SubmitFeedback(ctx, "trace-id-here", aiqa.FeedbackOptions{
    ThumbsUp: &thumbsUp,
    Comment:  "Great response!",
})
```

## Experiment Runner

The `ExperimentRunner` allows you to run experiments on datasets, scoring results against metrics:

```go
import (
    "context"
    "github.com/winterwell/aiqa-client-go"
)

// Create an experiment runner
runner := aiqa.NewExperimentRunner(aiqa.ExperimentRunnerOptions{
    DatasetId:      "dataset-id",
    ExperimentId:   "", // Optional: will create if empty
    ServerUrl:      "", // Optional: uses AIQA_SERVER_URL env var
    ApiKey:         "", // Optional: uses AIQA_API_KEY env var
    OrganisationId: "", // Optional: required for creating experiments
})

// Get the dataset
dataset, err := runner.GetDataset(ctx)
if err != nil {
    log.Fatal(err)
}

// Define your engine function
engine := func(input any, parameters map[string]any) (any, error) {
    // Your AI logic here
    return "output", nil
}

// Optional: define a scorer function
scorer := func(output any, example aiqa.Example, parameters map[string]any) (map[string]float64, error) {
    scores := make(map[string]float64)
    // Calculate scores based on output and example
    scores["accuracy"] = 0.95
    return scores, nil
}

// Run the experiment
err = runner.Run(ctx, engine, scorer)
if err != nil {
    log.Fatal(err)
}

// Get summary results
summary, err := runner.GetSummaryResults(ctx)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Results: %+v\n", summary)
```

## Data Filtering

The client automatically filters sensitive data from spans. By default, it removes:
- Passwords (keys containing "password")
- JWT tokens (values matching JWT format)
- Authorization headers
- API keys (common patterns like "sk-", "ghp_", etc.)

You can configure filters via the `AIQA_DATA_FILTERS` environment variable:

```bash
# Use default filters
export AIQA_DATA_FILTERS="RemovePasswords, RemoveJWT, RemoveAuthHeaders, RemoveAPIKeys"

# Disable all filters
export AIQA_DATA_FILTERS="false"

# Use only specific filters
export AIQA_DATA_FILTERS="RemovePasswords, RemoveAPIKeys"
```

## Running the Example

```bash
# First setup .env using env.example as a template
cp env.example .env
# Edit .env with your server URL and API key

# Then run the example
go run examples/example.go
```

The example will show which server it's logging to and demonstrate various tracing features. After running, check your AIQA server's traces view to see the generated spans.

## Versioning and Publishing

### Version Management

The client uses semantic versioning. Version information is tracked in:
- `version.json` - Contains version, git commit, and date (managed by `set-version-json.sh` in the repo root)
- Git tags - Used by Go modules for version resolution (e.g., `v0.6.2`)

### Publishing a New Version

1. **Update version in the repo:**
   ```bash
   # From the repo root, run the version script
   ./set-version-json.sh
   ```
   This updates `version.json` files across all components.

2. **Publish using the publish script:**
   ```bash
   # The publish.sh script will:
   # - Read the version from version.json
   # - Create and push a git tag
   # - Trigger Go module indexing
   ./publish.sh
   ```

   Or manually:
   ```bash
   # Tag the current commit with the new version
   git tag v0.6.2
   git push origin v0.6.2
   ```

3. **Verify the release:**
   - Check that the tag appears on GitHub: https://github.com/winterwell/aiqa-client-go/tags
   - Go modules will automatically pick up the new version via the Go proxy
   - Users can then install with: `go get github.com/winterwell/aiqa-client-go@v0.6.2`
   - Check https://pkg.go.dev/github.com/winterwell/aiqa-client-go for the updated module (may take a few minutes)

### Version Format

- Follow semantic versioning: `MAJOR.MINOR.PATCH` (e.g., `0.6.2`)
- Pre-release versions: `v0.6.3-alpha.1`, `v0.6.3-beta.1`
- Git tags must start with `v` (e.g., `v0.6.2`)

### Checking Current Version

The version is available in `version.json`:

```bash
cat version.json
```

Or programmatically in Go (if you embed version.json):

```go
import "encoding/json"
import "os"

type VersionInfo struct {
    VERSION   string `json:"VERSION"`
    GIT_COMMIT string `json:"GIT_COMMIT"`
    DATE      string `json:"DATE"`
}

func getVersion() (*VersionInfo, error) {
    data, err := os.ReadFile("version.json")
    if err != nil {
        return nil, err
    }
    var v VersionInfo
    err = json.Unmarshal(data, &v)
    return &v, err
}
```

## Development

### Running Tests

```bash
go test ./...
```

### Building

```bash
go build
```

### Using Locally in Other Projects

When developing the client and testing it in another Go project, use the `replace` directive. This is the standard Go way to use local code without publishing:

1. In your consuming project's `go.mod`, add:
   ```bash
   replace github.com/winterwell/aiqa-client-go => /absolute/path/to/aiqa-client-go
   ```

2. Run `go mod tidy` in your consuming project

3. Your project will now use the local version - any changes you make will be immediately available

4. When done, remove the `replace` line to go back to using the published version

### Contributing

Contributions are welcome! Please open an issue or pull request on [GitHub](https://github.com/winterwell/aiqa-client-go).
