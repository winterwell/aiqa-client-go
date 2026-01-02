# AIQA Go Client

OpenTelemetry-based client for AIQA that logs traces to the server.

Repository: [https://github.com/winterstein/aiqa](https://github.com/winterstein/aiqa)

## Installation

### From Go Modules (recommended)

Add the module to your Go project:

```bash
go get github.com/winterstein/aiqa/client-go
```

Or specify a version:

```bash
go get github.com/winterstein/aiqa/client-go@v0.4.1
```

### From Source

```bash
git clone https://github.com/winterstein/aiqa.git
cd aiqa/client-go
go mod download
```

## Setup

1. Set environment variables (or pass them to `InitTracing`):
```bash
export AIQA_SERVER_URL=http://localhost:3000
export AIQA_API_KEY=your-api-key
export AIQA_COMPONENT_TAG=mynamespace.mysystem  # Optional: component tag for all spans
```

**Note:** If `AIQA_SERVER_URL` or `AIQA_API_KEY` are not set, tracing will be automatically disabled. You'll see one warning message at the start, and your application will continue to run without tracing. You can check if tracing is enabled via `aiqa.IsTracingEnabled()`.

## Usage

### Basic Example

```go
package main

import (
    "context"
    "time"
    "github.com/winterstein/aiqa/client-go"
)

func main() {
    // Initialize tracing
    err := aiqa.InitTracing("", "")
    if err != nil {
        panic(err)
    }
    defer func() {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        aiqa.ShutdownTracing(ctx)
    }()

    // Wrap a function with tracing
    multiply := func(x, y int) int {
        return x * y
    }
    tracedMultiply := aiqa.WithTracing(multiply).(func(int, int) int)
    
    result := tracedMultiply(5, 3)
    fmt.Println(result)
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

## Configuration

The client can be configured via environment variables or by passing parameters to `InitTracing`:

- `AIQA_SERVER_URL`: URL of the AIQA server (default: empty, must be set)
- `AIQA_API_KEY`: API key for authentication (default: empty)
- `AIQA_COMPONENT_TAG`: Component tag to add to all spans (e.g., "mynamespace.mysystem"). Optional.
- `AIQA_SAMPLING_RATE`: Sampling rate between 0 and 1 (default: 1.0 = sample all). Optional.

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

## Running the Example

```bash
cd client-go
go run example.go
```

Make sure `AIQA_SERVER_URL` and `AIQA_API_KEY` are set in your environment.

## Versioning and Publishing

### Version Management

The client uses semantic versioning. Version information is tracked in:
- `version.json` - Contains version, git commit, and date (managed by `set-version-json.sh` in the repo root)
- Git tags - Used by Go modules for version resolution (e.g., `v0.4.1`)

### Publishing a New Version

1. **Update version in the repo:**
   ```bash
   # From the repo root, run the version script
   ./set-version-json.sh
   ```
   This updates `version.json` files across all components.

2. **Create and push a git tag:**
   ```bash
   # Tag the current commit with the new version
   git tag v0.4.2
   git push origin v0.4.2
   ```

3. **Verify the release:**
   - Check that the tag appears on GitHub: https://github.com/winterstein/aiqa/tags
   - Go modules will automatically pick up the new version via the Go proxy
   - Users can then install with: `go get github.com/winterstein/aiqa/client-go@v0.4.2`

### Version Format

- Follow semantic versioning: `MAJOR.MINOR.PATCH` (e.g., `0.4.1`)
- Pre-release versions: `v0.4.2-alpha.1`, `v0.4.2-beta.1`
- Git tags must start with `v` (e.g., `v0.4.1`)

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

### Contributing

Contributions are welcome! Please open an issue or pull request on [GitHub](https://github.com/winterstein/aiqa).

