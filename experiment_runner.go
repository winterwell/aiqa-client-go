package aiqa

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// ExperimentRunnerOptions contains options for creating an ExperimentRunner
type ExperimentRunnerOptions struct {
	DatasetId      string
	ExperimentId   string
	ServerUrl      string
	ApiKey         string
	OrganisationId string
}

// Example represents an example from a dataset
type Example struct {
	Id           string                 `json:"id"`
	TraceId      string                 `json:"traceId,omitempty"`
	Dataset      string                 `json:"dataset"`
	Organisation string                 `json:"organisation"`
	Spans        []interface{}          `json:"spans,omitempty"`
	Input        interface{}            `json:"input,omitempty"`
	Outputs      map[string]interface{} `json:"outputs"`
	Created      time.Time              `json:"created"`
	Updated      time.Time              `json:"updated"`
	Metrics      []Metric               `json:"metrics,omitempty"`
}

// Metric represents a metric for scoring
type Metric struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Unit        string                 `json:"unit,omitempty"`
	Type        string                 `json:"type"` // "javascript", "llm", or "number"
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// Dataset represents a dataset
type Dataset struct {
	Id          string                 `json:"id"`
	Organisation string                `json:"organisation"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
	InputSchema  interface{}           `json:"input_schema,omitempty"`
	OutputSchema interface{}           `json:"output_schema,omitempty"`
	Metrics     []Metric               `json:"metrics,omitempty"`
	Created     time.Time              `json:"created"`
	Updated     time.Time              `json:"updated"`
}

// Experiment represents an experiment
type Experiment struct {
	Id                  string                   `json:"id"`
	Dataset             string                   `json:"dataset"`
	Organisation        string                   `json:"organisation"`
	Name                string                   `json:"name,omitempty"`
	Parameters          map[string]interface{}    `json:"parameters,omitempty"`
	ComparisonParameters []map[string]interface{} `json:"comparison_parameters,omitempty"`
	SummaryResults      map[string]interface{}   `json:"summary_results,omitempty"`
	Created             time.Time                 `json:"created"`
	Updated             time.Time                 `json:"updated"`
	Results             []Result                  `json:"results,omitempty"`
}

// Result represents a result for an example
type Result struct {
	ExampleId string            `json:"exampleId"`
	Scores    map[string]float64 `json:"scores"`
	Errors    map[string]string  `json:"errors,omitempty"`
}

// ScoreResult represents the result of scoring
type ScoreResult map[string]interface{}

// MetricStats represents statistics for a metric
type MetricStats struct {
	Mean  float64 `json:"mean"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Var   float64 `json:"var"`
	Count int     `json:"count"`
}

// ExperimentRunner is the main class for running experiments on datasets.
// It can create an experiment, run it, and score the results.
// Handles setting up environment variables and passing parameters to the engine function.
type ExperimentRunner struct {
	datasetId      string
	serverUrl      string
	apiKey         string
	organisation   string
	experimentId   string
	experiment     *Experiment
	scores         []struct {
		example Example
		result  interface{}
		scores  ScoreResult
	}
	summaryResults map[string]MetricStats
}

// NewExperimentRunner creates a new ExperimentRunner
func NewExperimentRunner(options ExperimentRunnerOptions) *ExperimentRunner {
	serverUrl := options.ServerUrl
	if serverUrl == "" {
		serverUrl = os.Getenv("AIQA_SERVER_URL")
	}
	// Remove trailing slash
	serverUrl = strings.TrimSuffix(serverUrl, "/")

	apiKey := options.ApiKey
	if apiKey == "" {
		apiKey = os.Getenv("AIQA_API_KEY")
	}

	return &ExperimentRunner{
		datasetId:      options.DatasetId,
		serverUrl:      serverUrl,
		apiKey:         apiKey,
		organisation:   options.OrganisationId,
		experimentId:   options.ExperimentId,
		summaryResults: make(map[string]MetricStats),
	}
}

// GetDataset fetches the dataset to get its metrics
func (er *ExperimentRunner) GetDataset(ctx context.Context) (*Dataset, error) {
	url := fmt.Sprintf("%s/dataset/%s", er.serverUrl, er.datasetId)
	resp, err := makeRequest(ctx, "GET", url, nil, er.apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch dataset: %w", err)
	}

	var dataset Dataset
	if err := parseJSONResponse(resp, &dataset); err != nil {
		return nil, fmt.Errorf("failed to fetch dataset: %w", err)
	}

	return &dataset, nil
}

// GetExampleInputs fetches example inputs from the dataset
func (er *ExperimentRunner) GetExampleInputs(ctx context.Context, limit int) ([]Example, error) {
	if limit == 0 {
		limit = 10000
	}

	url := fmt.Sprintf("%s/example?dataset_id=%s&limit=%d", er.serverUrl, er.datasetId, limit)
	if er.organisation != "" {
		url += fmt.Sprintf("&organisation=%s", er.organisation)
	}

	resp, err := makeRequest(ctx, "GET", url, nil, er.apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch example inputs: %w", err)
	}

	var data struct {
		Hits   []Example `json:"hits"`
		Total  int       `json:"total,omitempty"`
		Limit  int       `json:"limit,omitempty"`
		Offset int       `json:"offset,omitempty"`
	}

	if err := parseJSONResponse(resp, &data); err != nil {
		return nil, fmt.Errorf("failed to fetch example inputs: %w", err)
	}

	return data.Hits, nil
}

// CreateExperiment creates an experiment if one does not exist
func (er *ExperimentRunner) CreateExperiment(ctx context.Context, experimentSetup *Experiment) (*Experiment, error) {
	if er.organisation == "" || er.datasetId == "" {
		return nil, fmt.Errorf("organisation and dataset ID are required to create an experiment")
	}

	if experimentSetup == nil {
		experimentSetup = &Experiment{}
	}

	// Fill in if not set
	if experimentSetup.Organisation == "" {
		experimentSetup.Organisation = er.organisation
	}
	if experimentSetup.Dataset == "" {
		experimentSetup.Dataset = er.datasetId
	}
	if experimentSetup.Results == nil {
		experimentSetup.Results = []Result{}
	}
	if experimentSetup.SummaryResults == nil {
		experimentSetup.SummaryResults = make(map[string]interface{})
	}

	fmt.Println("AIQA: Creating experiment")
	url := fmt.Sprintf("%s/experiment", er.serverUrl)
	resp, err := makeRequest(ctx, "POST", url, experimentSetup, er.apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create experiment: %w", err)
	}

	var experiment Experiment
	if err := parseJSONResponse(resp, &experiment); err != nil {
		return nil, fmt.Errorf("failed to create experiment: %w", err)
	}

	er.experimentId = experiment.Id
	er.experiment = &experiment
	return &experiment, nil
}

// ScoreAndStore asks the server to score an example result. Stores the score for later summary calculation.
func (er *ExperimentRunner) ScoreAndStore(ctx context.Context, example Example, result interface{}, scores map[string]float64) (ScoreResult, error) {
	// Do we have an experiment ID? If not, we need to create the experiment first
	if er.experimentId == "" {
		if _, err := er.CreateExperiment(ctx, nil); err != nil {
			return nil, fmt.Errorf("failed to create experiment: %w", err)
		}
	}

	fmt.Printf("AIQA: Scoring and storing example: %s\n", example.Id)
	fmt.Printf("AIQA: Scores: %v\n", scores)
	requestBody := map[string]interface{}{
		"output":  result,
		"traceId": example.TraceId,
		"scores":  scores,
	}

	url := fmt.Sprintf("%s/experiment/%s/example/%s/scoreAndStore", er.serverUrl, er.experimentId, example.Id)
	resp, err := makeRequest(ctx, "POST", url, requestBody, er.apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to score and store: %w", err)
	}

	var scoreResult ScoreResult
	if err := parseJSONResponse(resp, &scoreResult); err != nil {
		return nil, fmt.Errorf("failed to score and store: %w", err)
	}

	fmt.Printf("AIQA: scoreAndStore response: %v\n", scoreResult)
	return scoreResult, nil
}

// Run runs an engine function on all examples and scores the results
// engine: function that takes input and parameters and returns output
// scorer: optional function that scores the output given the example
func (er *ExperimentRunner) Run(ctx context.Context, engine func(input interface{}, parameters map[string]interface{}) (interface{}, error), scorer func(output interface{}, example Example, parameters map[string]interface{}) (map[string]float64, error)) error {
	examples, err := er.GetExampleInputs(ctx, 0)
	if err != nil {
		return fmt.Errorf("failed to get examples: %w", err)
	}

	for _, example := range examples {
		scores, err := er.RunExample(ctx, example, engine, scorer)
		if err != nil {
			return fmt.Errorf("failed to run example %s: %w", example.Id, err)
		}
		if len(scores) > 0 {
			er.scores = append(er.scores, struct {
				example Example
				result  interface{}
				scores  ScoreResult
			}{
				example: example,
				result:  scores[0],
				scores:  scores[0],
			})
		}
	}

	return nil
}

// RunExample runs the engine on an example with the given parameters (looping over comparison parameters), and scores the result.
// Also calls ScoreAndStore to store the result in the server.
// Returns one set of scores for each comparison parameter set. If no comparison parameters, returns an array of one.
func (er *ExperimentRunner) RunExample(ctx context.Context, example Example, callMyCode func(input interface{}, parameters map[string]interface{}) (interface{}, error), scoreThisOutput func(output interface{}, example Example, parameters map[string]interface{}) (map[string]float64, error)) ([]ScoreResult, error) {
	// Ensure experiment exists
	if er.experiment == nil {
		if _, err := er.CreateExperiment(ctx, nil); err != nil {
			return nil, fmt.Errorf("failed to create experiment: %w", err)
		}
	}
	if er.experiment == nil {
		return nil, fmt.Errorf("failed to create experiment")
	}

	// Make the parameters
	parametersFixed := er.experiment.Parameters
	if parametersFixed == nil {
		parametersFixed = make(map[string]interface{})
	}

	// If comparison_parameters is empty/undefined, default to [{}] so we run at least once
	parametersLoop := er.experiment.ComparisonParameters
	if len(parametersLoop) == 0 {
		parametersLoop = []map[string]interface{}{{}}
	}

	// Handle both spans array and input field
	input := example.Input
	if input == nil && len(example.Spans) > 0 {
		if spanMap, ok := example.Spans[0].(map[string]interface{}); ok {
			if attributes, ok := spanMap["attributes"].(map[string]interface{}); ok {
				input = attributes["input"]
			}
		}
	}
	if input == nil {
		fmt.Printf("AIQA: Warning: Example has no input field or spans with input attribute: %v\n", example)
		// Run engine anyway -- this could make sense if it's all about the parameters
	}

	var allScores []ScoreResult

	// This loop should not be parallelized - it should run sequentially, one after the other - to avoid creating interference between the runs.
	for _, parameters := range parametersLoop {
		parametersHere := make(map[string]interface{})
		for k, v := range parametersFixed {
			parametersHere[k] = v
		}
		for k, v := range parameters {
			parametersHere[k] = v
		}

		fmt.Printf("AIQA: Running with parameters: %v\n", parametersHere)

		// Note: Parameters are passed directly to the engine function.
		// Engine functions should read from the parameters map, not from environment variables,
		// to ensure thread-safety and avoid global state mutation.
		start := time.Now()
		output, err := callMyCode(input, parametersHere)
		if err != nil {
			return nil, fmt.Errorf("engine function failed: %w", err)
		}
		duration := time.Since(start)

		fmt.Printf("AIQA: Output: %v\n", output)

		scores := make(map[string]float64)
		if scoreThisOutput != nil {
			scored, err := scoreThisOutput(output, example, parametersHere)
			if err != nil {
				return nil, fmt.Errorf("scorer function failed: %w", err)
			}
			for k, v := range scored {
				scores[k] = v
			}
		}
		scores["duration"] = float64(duration.Milliseconds())

		fmt.Printf("AIQA: Call scoreAndStore ... for example: %s with scores: %v\n", example.Id, scores)
		result, err := er.ScoreAndStore(ctx, example, output, scores)
		if err != nil {
			return nil, fmt.Errorf("failed to score and store: %w", err)
		}
		fmt.Printf("AIQA: scoreAndStore returned: %v\n", result)

		allScores = append(allScores, result)
	}

	return allScores, nil
}

// GetSummaryResults fetches summary results from the server
func (er *ExperimentRunner) GetSummaryResults(ctx context.Context) (map[string]MetricStats, error) {
	if er.experimentId == "" {
		return nil, fmt.Errorf("no experiment ID set")
	}

	url := fmt.Sprintf("%s/experiment/%s", er.serverUrl, er.experimentId)
	resp, err := makeRequest(ctx, "GET", url, nil, er.apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch summary results: %w", err)
	}

	var experiment Experiment
	if err := parseJSONResponse(resp, &experiment); err != nil {
		return nil, fmt.Errorf("failed to fetch summary results: %w", err)
	}

	// Convert summary_results to MetricStats
	summaryResults := make(map[string]MetricStats)
	if experiment.SummaryResults != nil {
		for key, value := range experiment.SummaryResults {
			if statsMap, ok := value.(map[string]interface{}); ok {
				stats := MetricStats{}
				if mean, ok := statsMap["mean"].(float64); ok {
					stats.Mean = mean
				}
				if min, ok := statsMap["min"].(float64); ok {
					stats.Min = min
				}
				if max, ok := statsMap["max"].(float64); ok {
					stats.Max = max
				}
				if v, ok := statsMap["var"].(float64); ok {
					stats.Var = v
				}
				if count, ok := statsMap["count"].(float64); ok {
					stats.Count = int(count)
				}
				summaryResults[key] = stats
			}
		}
	}

	return summaryResults, nil
}

