package main

import (
	"context"
	"fmt"
	"time"

	"github.com/winterwell/aiqa-client-go"
)

// Example usage of aiqa-client-go
// To run this example:
//
//	First setup .env using env.example as a template
//	go run examples/example.go
//	Then go to https://app-aiqa.winterwell.com/traces (if that is your server) and see the traces
func main() {
	// Initialize tracing
	err := aiqa.InitTracing("", "")
	if err != nil {
		fmt.Printf("Failed to initialize tracing: %v\n", err)
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		aiqa.ShutdownTracing(ctx)
	}()

	// What server and org are we logging to?
	serverUrl := aiqa.GetServerURL("")
	fmt.Printf("Logging to server: %s\n", serverUrl)

	// Example 1: Simple function
	multiply := func(x, y int) int {
		return x * y
	}
	tracedMultiply := aiqa.WithTracing(multiply).(func(int, int) int)
	result := tracedMultiply(5, 3)
	fmt.Printf("Result: %d\n", result)

	// Example 2: Function with error
	divide := func(x, y float64) (float64, error) {
		if y == 0 {
			return 0, fmt.Errorf("division by zero")
		}
		return x / y, nil
	}
	tracedDivide := aiqa.WithTracing(divide).(func(float64, float64) (float64, error))
	result2, err := tracedDivide(10, 2)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("Result: %f\n", result2)
	}

	// Example 3: Function with context
	processData := func(ctx context.Context, data string) (string, error) {
		// Simulate some work
		time.Sleep(100 * time.Millisecond)
		return fmt.Sprintf("Processed: %s", data), nil
	}
	tracedProcess := aiqa.WithTracing(processData).(func(context.Context, string) (string, error))
	result3, err := tracedProcess(context.Background(), "test data")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("Result: %s\n", result3)
	}

	// Example 4: Nested function calls
	outer := func(x int) int {
		return tracedMultiply(x, 2)
	}
	tracedOuter := aiqa.WithTracing(outer).(func(int) int)
	result4 := tracedOuter(10)
	fmt.Printf("Nested result: %d\n", result4)

	// Flush spans before exit
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := aiqa.FlushSpans(ctx); err != nil {
		fmt.Printf("Failed to flush spans: %v\n", err)
	}
}
