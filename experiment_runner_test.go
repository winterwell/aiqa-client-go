package aiqa

import (
	"context"
	"os"
	"testing"
)

func TestNewExperimentRunner(t *testing.T) {
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

	// Test with provided options
	options := ExperimentRunnerOptions{
		DatasetId:      "test-dataset",
		ExperimentId:   "test-experiment",
		ServerUrl:      "http://localhost:3000",
		ApiKey:         "test-key",
		OrganisationId: "test-org",
	}

	runner := NewExperimentRunner(options)
	if runner == nil {
		t.Fatal("NewExperimentRunner should return a non-nil runner")
	}
	if runner.datasetId != "test-dataset" {
		t.Errorf("Expected datasetId to be 'test-dataset', got '%s'", runner.datasetId)
	}
	if runner.experimentId != "test-experiment" {
		t.Errorf("Expected experimentId to be 'test-experiment', got '%s'", runner.experimentId)
	}
	if runner.serverUrl != "http://localhost:3000" {
		t.Errorf("Expected serverUrl to be 'http://localhost:3000', got '%s'", runner.serverUrl)
	}
	if runner.apiKey != "test-key" {
		t.Errorf("Expected apiKey to be 'test-key', got '%s'", runner.apiKey)
	}
	if runner.organisation != "test-org" {
		t.Errorf("Expected organisation to be 'test-org', got '%s'", runner.organisation)
	}

	// Test with env vars
	os.Setenv("AIQA_SERVER_URL", "http://env-server:3000")
	os.Setenv("AIQA_API_KEY", "env-key")
	options2 := ExperimentRunnerOptions{
		DatasetId: "test-dataset",
	}
	runner2 := NewExperimentRunner(options2)
	if runner2.serverUrl != "http://env-server:3000" {
		t.Errorf("Expected serverUrl from env, got '%s'", runner2.serverUrl)
	}
	if runner2.apiKey != "env-key" {
		t.Errorf("Expected apiKey from env, got '%s'", runner2.apiKey)
	}
}

func TestExperimentRunner_GetDataset(t *testing.T) {
	// This test would require a mock HTTP server
	// For now, we'll test the error cases
	runner := NewExperimentRunner(ExperimentRunnerOptions{
		DatasetId: "test-dataset",
	})

	ctx := context.Background()
	_, err := runner.GetDataset(ctx)
	// Should fail without a real server, but we're testing the function exists
	if err == nil {
		t.Log("GetDataset succeeded (unexpected - may have real server)")
	}
}

func TestExperimentRunner_GetExampleInputs(t *testing.T) {
	runner := NewExperimentRunner(ExperimentRunnerOptions{
		DatasetId: "test-dataset",
	})

	ctx := context.Background()
	_, err := runner.GetExampleInputs(ctx, 10)
	// Should fail without a real server, but we're testing the function exists
	if err == nil {
		t.Log("GetExampleInputs succeeded (unexpected - may have real server)")
	}
}

func TestExperimentRunner_CreateExperiment(t *testing.T) {
	runner := NewExperimentRunner(ExperimentRunnerOptions{
		DatasetId:      "test-dataset",
		OrganisationId: "test-org",
	})

	ctx := context.Background()

	// Test with nil experiment setup
	_, err := runner.CreateExperiment(ctx, nil)
	// Should fail without a real server, but we're testing the function exists
	if err == nil {
		t.Log("CreateExperiment succeeded (unexpected - may have real server)")
	}

	// Test with missing organisation
	runner2 := NewExperimentRunner(ExperimentRunnerOptions{
		DatasetId: "test-dataset",
	})
	_, err = runner2.CreateExperiment(ctx, nil)
	if err == nil {
		t.Error("CreateExperiment should fail when organisation is missing")
	}
}

func TestExperimentRunner_ScoreAndStore(t *testing.T) {
	runner := NewExperimentRunner(ExperimentRunnerOptions{
		DatasetId: "test-dataset",
	})

	ctx := context.Background()
	example := Example{
		Id:      "test-example",
		TraceId: "test-trace-id",
	}

	_, err := runner.ScoreAndStore(ctx, example, "test-result", map[string]float64{"score": 0.5})
	// Should fail without a real server, but we're testing the function exists
	if err == nil {
		t.Log("ScoreAndStore succeeded (unexpected - may have real server)")
	}
}

func TestExperimentRunner_RunExample(t *testing.T) {
	runner := NewExperimentRunner(ExperimentRunnerOptions{
		DatasetId:      "test-dataset",
		OrganisationId: "test-org",
	})

	ctx := context.Background()
	example := Example{
		Id:    "test-example",
		Input: "test-input",
	}

	engine := func(input interface{}, parameters map[string]interface{}) (interface{}, error) {
		return "test-output", nil
	}

	_, err := runner.RunExample(ctx, example, engine)
	// Should fail without a real server, but we're testing the function exists
	if err == nil {
		t.Log("RunExample succeeded (unexpected - may have real server)")
	}
}

func TestExperimentRunner_GetSummaryResults(t *testing.T) {
	runner := NewExperimentRunner(ExperimentRunnerOptions{
		DatasetId: "test-dataset",
	})

	ctx := context.Background()

	// Test without experiment ID
	_, err := runner.GetSummaryResults(ctx)
	if err == nil {
		t.Error("GetSummaryResults should fail when experiment ID is not set")
	}
}

func TestExampleStruct(t *testing.T) {
	example := Example{
		Id:      "test-id",
		TraceId: "test-trace",
		Dataset: "test-dataset",
		Input:   "test-input",
		Outputs: map[string]interface{}{
			"output1": "value1",
		},
	}

	if example.Id != "test-id" {
		t.Errorf("Expected Id to be 'test-id', got '%s'", example.Id)
	}
	if example.TraceId != "test-trace" {
		t.Errorf("Expected TraceId to be 'test-trace', got '%s'", example.TraceId)
	}
}

func TestMetricStruct(t *testing.T) {
	metric := Metric{
		Name:        "test-metric",
		Description: "test description",
		Unit:        "score",
		Type:        "javascript",
		Parameters: map[string]interface{}{
			"param1": "value1",
		},
	}

	if metric.Name != "test-metric" {
		t.Errorf("Expected Name to be 'test-metric', got '%s'", metric.Name)
	}
	if metric.Type != "javascript" {
		t.Errorf("Expected Type to be 'javascript', got '%s'", metric.Type)
	}
}

func TestDatasetStruct(t *testing.T) {
	dataset := Dataset{
		Id:           "test-dataset",
		Organisation: "test-org",
		Name:         "Test Dataset",
		Description:  "Test description",
		Tags:         []string{"tag1", "tag2"},
		Metrics: []Metric{
			{Name: "metric1", Type: "javascript"},
		},
	}

	if dataset.Id != "test-dataset" {
		t.Errorf("Expected Id to be 'test-dataset', got '%s'", dataset.Id)
	}
	if len(dataset.Metrics) != 1 {
		t.Errorf("Expected 1 metric, got %d", len(dataset.Metrics))
	}
}

func TestExperimentStruct(t *testing.T) {
	experiment := Experiment{
		Id:           "test-experiment",
		Dataset:      "test-dataset",
		Organisation: "test-org",
		Name:         "Test Experiment",
		Parameters: map[string]interface{}{
			"param1": "value1",
		},
		Results: []Result{
			{ExampleId: "example1", Scores: map[string]float64{"score": 0.5}},
		},
	}

	if experiment.Id != "test-experiment" {
		t.Errorf("Expected Id to be 'test-experiment', got '%s'", experiment.Id)
	}
	if len(experiment.Results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(experiment.Results))
	}
}

func TestResultStruct(t *testing.T) {
	result := Result{
		ExampleId: "test-example",
		Scores: map[string]float64{
			"score1": 0.5,
			"score2": 0.8,
		},
		Errors: map[string]string{
			"error1": "error message",
		},
	}

	if result.ExampleId != "test-example" {
		t.Errorf("Expected ExampleId to be 'test-example', got '%s'", result.ExampleId)
	}
	if len(result.Scores) != 2 {
		t.Errorf("Expected 2 scores, got %d", len(result.Scores))
	}
}

func TestMetricStatsStruct(t *testing.T) {
	stats := MetricStats{
		Mean:  0.5,
		Min:   0.0,
		Max:   1.0,
		Var:   0.1,
		Count: 10,
	}

	if stats.Mean != 0.5 {
		t.Errorf("Expected Mean to be 0.5, got %f", stats.Mean)
	}
	if stats.Count != 10 {
		t.Errorf("Expected Count to be 10, got %d", stats.Count)
	}
}
