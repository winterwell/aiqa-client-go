package aiqa

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Any error that starts with this prefix will be treated as a request to stop the experiment.
const ERROR_PREFIX_STOP_EXPERIMENT = "STOP_EXPERIMENT"

// ErrStopExperiment is returned by callMyCode to signal that Run() should stop the experiment Run early.
// This can be used when callMyCode detects issues like connectivity problems.
var ErrStopExperiment = errors.New(ERROR_PREFIX_STOP_EXPERIMENT)

// ExperimentRunnerOptions contains options for creating an ExperimentRunner
type ExperimentRunnerOptions struct {
	Name           string
	DatasetId      string
	ExperimentId   string
	ServerUrl      string
	ApiKey         string
	OrganisationId string
	// LlmCallFn optional: called for LLM-as-judge metrics when type is "llm".
	// If nil, uses a default function that uses OPENAI_API_KEY/ANTHROPIC_API_KEY or model from server.
	// If you want to track your token use on this too - either use the default (which has token tracking,
	// or provide your own with token tracking.
	LlmCallFn LLMCallFn
	// ScorerForMetricId optional: map metric id -> scorer; used in RunExample when scoring metrics. If nil, no per-metric scoring.
	ScorerForMetricId map[string]ScorerForMetricFn
	// False by default. If true, Run and RunSome will re-run examples (once) if they do not have a score for each metric.
	// This is useful if run-example hit a transient error.
	// It can be wasteful if the error is permanent.
	RerunExamplesWithMissingScores bool
}

// Example represents an example from a dataset
type Example struct {
	Id   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	// The source trace, if created from spans. Do not edit this - it is set by the server.
	// For the trace relating to running an example, see Result.Trace.
	Trace        string         `json:"trace,omitempty"`
	Dataset      string         `json:"dataset"`
	Organisation string         `json:"organisation"`
	Spans        []any          `json:"spans,omitempty"`
	Input        any            `json:"input,omitempty"`
	Outputs      map[string]any `json:"outputs"`
	Created      time.Time      `json:"created"`
	Updated      time.Time      `json:"updated"`
	Metrics      []Metric       `json:"metrics,omitempty"`
	Tags         []string       `json:"tags,omitempty"`
}

// Metric represents a metric for scoring
type Metric struct {
	Id             string         `json:"id,omitempty"`
	Name           string         `json:"name"`
	Description    string         `json:"description,omitempty"`
	Unit           string         `json:"unit,omitempty"`
	Type           string         `json:"type"` // "javascript", "llm", or "number"
	Parameters     map[string]any `json:"parameters,omitempty"`
	Prompt         string         `json:"prompt,omitempty"`
	PromptCriteria string         `json:"promptCriteria,omitempty"`
	Code           string         `json:"code,omitempty"`
	Model          string         `json:"model,omitempty"`
	Provider       string         `json:"provider,omitempty"`
	Value          any            `json:"value,omitempty"`
}

// Dataset represents a dataset
type Dataset struct {
	Id           string    `json:"id"`
	Organisation string    `json:"organisation"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	Tags         []string  `json:"tags,omitempty"`
	Metrics      []Metric  `json:"metrics,omitempty"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
}

// Experiment represents an experiment
type Experiment struct {
	Id           string         `json:"id"`
	Dataset      string         `json:"dataset"`
	Organisation string         `json:"organisation"`
	Name         string         `json:"name,omitempty"`
	Parameters   map[string]any `json:"parameters,omitempty"`
	Summaries    map[string]any `json:"summaries,omitempty"`
	Created      time.Time      `json:"created"`
	Updated      time.Time      `json:"updated"`
	Results      []Result       `json:"results,omitempty"`
}

// Result represents the result for an example. See Experiment.ts Result interface.
type Result struct {
	Example     string             `json:"example"`
	Trace       string             `json:"trace,omitempty"`
	RateLimited bool               `json:"rateLimited,omitempty"`
	Scores      map[string]float64 `json:"scores"`
	Messages    map[string]string  `json:"messages,omitempty"`
	Errors      map[string]string  `json:"errors,omitempty"`
}

const (
	AIQA_TRACE_ID   = "aiqa.experiment"
	AIQA_EXAMPLE_ID = "aiqa.example"
)

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
	datasetId                      string
	serverUrl                      string
	apiKey                         string
	organisation                   string
	experimentId                   string
	experiment                     *Experiment
	datasetCache                   *Dataset
	llmCallFn                      LLMCallFn
	scorerForMetricId              map[string]ScorerForMetricFn
	summaryResults                 map[string]MetricStats
	rerunExamplesWithMissingScores bool
}

// CallMyCode is how the experiment runner calls the engine function,
// paasing in the input from an Example, plus any parameters for the experiment.
// Error handling:
//   - Results are not recorded if the error is not nil.
//   - If the error is an instance of ErrStopExperiment, or if the error begins
//     with ERROR_PREFIX_STOP_EXPERIMENT, then the experiment runner will stop the experiment.
type CallMyCodeFunc func(
	ctx context.Context,
	input any,
	parameters map[string]any,
) (any, error)

// NewExperimentRunner creates a new ExperimentRunner
func NewExperimentRunner(options ExperimentRunnerOptions) *ExperimentRunner {
	serverUrl := options.ServerUrl
	if serverUrl == "" {
		serverUrl = os.Getenv("AIQA_SERVER_URL")
		if serverUrl == "" {
			serverUrl = "https://server-aiqa.winterwell.com"
		}
	}
	// Remove trailing slash
	serverUrl = strings.TrimSuffix(serverUrl, "/")

	apiKey := options.ApiKey
	if apiKey == "" {
		apiKey = os.Getenv("AIQA_API_KEY")
	}

	return &ExperimentRunner{
		datasetId:                      options.DatasetId,
		serverUrl:                      serverUrl,
		apiKey:                         apiKey,
		organisation:                   options.OrganisationId,
		experimentId:                   options.ExperimentId,
		llmCallFn:                      options.LlmCallFn,
		scorerForMetricId:              options.ScorerForMetricId,
		summaryResults:                 make(map[string]MetricStats),
		rerunExamplesWithMissingScores: options.RerunExamplesWithMissingScores || false,
	}
}

// setEnvFromMap sets os env vars from the given map (truthy values only). Returns a restore func to call when done.
func setEnvFromMap(m map[string]any) func() {
	saved := make(map[string]string)
	for k, v := range m {
		if v == nil {
			continue
		}
		s := fmt.Sprint(v)
		if s == "" {
			continue
		}
		saved[k] = os.Getenv(k)
		os.Setenv(k, s)
	}
	return func() {
		for k, prev := range saved {
			if prev == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, prev)
			}
		}
	}
}

// GetDataset fetches the dataset to get its metrics. Caches the result and derives organisation from it when not set.
func (er *ExperimentRunner) GetDataset(ctx context.Context) (*Dataset, error) {
	if er.datasetCache != nil {
		return er.datasetCache, nil
	}

	fmt.Printf("AIQA: DEBUG GetDataset fetching dataset_id=%q\n", er.datasetId)
	url := fmt.Sprintf("%s/dataset/%s", er.serverUrl, er.datasetId)
	resp, err := makeRequest(ctx, "GET", url, nil, er.apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch dataset: %w", err)
	}

	var dataset Dataset
	if err := parseJSONResponse(resp, &dataset); err != nil {
		return nil, fmt.Errorf("failed to fetch dataset: %w", err)
	}

	er.datasetCache = &dataset
	if er.organisation == "" && dataset.Organisation != "" {
		er.organisation = dataset.Organisation
	}
	fmt.Printf("AIQA: DEBUG GetDataset ok dataset_id=%q organisation=%q\n", er.datasetId, er.organisation)
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

// GetExample fetches a single example by ID.
func (er *ExperimentRunner) GetExample(ctx context.Context, exampleId string) (Example, error) {
	url := fmt.Sprintf("%s/example/%s", er.serverUrl, exampleId)
	resp, err := makeRequest(ctx, "GET", url, nil, er.apiKey)
	if err != nil {
		return Example{}, fmt.Errorf("failed to fetch example: %w", err)
	}

	var example Example
	if err := parseJSONResponse(resp, &example); err != nil {
		return Example{}, fmt.Errorf("failed to fetch example: %w", err)
	}
	return example, nil
}

// CreateExample creates an example in a dataset. Derives organisation from dataset when not set.
func (er *ExperimentRunner) CreateExample(ctx context.Context, example *Example) (*Example, error) {
	if example == nil {
		return nil, fmt.Errorf("example cannot be nil")
	}

	// Fill in dataset first (needed to derive organisation)
	if example.Dataset == "" {
		if er.datasetId == "" {
			return nil, fmt.Errorf("dataset is required (set via DatasetId in ExperimentRunnerOptions or in the example)")
		}
		example.Dataset = er.datasetId
	}

	// Derive organisation from dataset if not set
	if er.organisation == "" {
		if _, err := er.GetDataset(ctx); err != nil {
			return nil, fmt.Errorf("failed to get dataset to derive organisation: %w", err)
		}
	}

	if example.Organisation == "" {
		if er.organisation == "" {
			return nil, fmt.Errorf("organisation is required (set via OrganisationId in ExperimentRunnerOptions, or it will be derived from the dataset)")
		}
		example.Organisation = er.organisation
	}

	// Set timestamps if not set
	now := time.Now()
	if example.Created.IsZero() {
		example.Created = now
	}
	if example.Updated.IsZero() {
		example.Updated = now
	}

	// Create the example
	url := fmt.Sprintf("%s/example", er.serverUrl)
	resp, err := makeRequest(ctx, "POST", url, example, er.apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create example: %w", err)
	}

	var createdExample Example
	if err := parseJSONResponse(resp, &createdExample); err != nil {
		return nil, fmt.Errorf("failed to create example: %w", err)
	}

	return &createdExample, nil
}

// CreateExperiment creates an experiment if one does not exist. Derives organisation from dataset when not set.
func (er *ExperimentRunner) CreateExperiment(ctx context.Context, experimentSetup *Experiment) (*Experiment, error) {
	fmt.Printf("AIQA: DEBUG CreateExperiment runner.datasetId=%q runner.organisation=%q\n", er.datasetId, er.organisation)
	if er.organisation == "" {
		if _, err := er.GetDataset(ctx); err != nil {
			return nil, fmt.Errorf("failed to get dataset to derive organisation: %w", err)
		}
	}
	if er.organisation == "" || er.datasetId == "" {
		return nil, fmt.Errorf("organisation and dataset ID are required to create an experiment (organisation can be derived from the dataset or set via OrganisationId)")
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
	fmt.Printf("AIQA: DEBUG CreateExperiment body dataset=%q organisation=%q\n", experimentSetup.Dataset, experimentSetup.Organisation)
	if experimentSetup.Results == nil {
		experimentSetup.Results = []Result{}
	}
	if experimentSetup.Summaries == nil {
		experimentSetup.Summaries = make(map[string]any)
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
func (er *ExperimentRunner) ScoreAndStore(ctx context.Context, result *Result, output any, scores map[string]float64) (*Result, error) {
	// Do we have an experiment ID? If not, we need to create the experiment first
	if er.experimentId == "" {
		if _, err := er.CreateExperiment(ctx, nil); err != nil {
			return nil, fmt.Errorf("failed to create experiment: %w", err)
		}
	}

	if result == nil || result.Example == "" {
		return nil, fmt.Errorf("result with example id is required")
	}

	if scores == nil {
		scores = map[string]float64{}
	}

	fmt.Printf("AIQA: Scoring and storing example: %s\n", result.Example)
	fmt.Printf("AIQA: Scores: %v\n", scores)
	requestBody := map[string]any{
		"output": output,
		"trace":  result.Trace,
		"scores": scores,
	}
	fmt.Printf("AIQA: scoreAndStore %s request body: %v\n", result.Example, requestBody)
	url := fmt.Sprintf("%s/experiment/%s/example/%s/scoreAndStore", er.serverUrl, er.experimentId, result.Example)
	resp, err := makeRequest(ctx, "POST", url, requestBody, er.apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to score and store: %w", err)
	}

	var remoteResult Result
	if err := parseJSONResponse(resp, &remoteResult); err != nil {
		return nil, fmt.Errorf("failed to score and store: %w", err)
	}

	if remoteResult.Trace == "" && result.Trace != "" {
		remoteResult.Trace = result.Trace
	}
	fmt.Printf("AIQA: scoreAndStore response: %v\n", remoteResult)
	return &remoteResult, nil
}

// Run runs an engine function on all examples and scores the results
// engine: function that takes context, input and parameters and returns output
// Checks if results already exist for an example before calling RunExample, allowing experiments to be resumed.
func (er *ExperimentRunner) Run(ctx context.Context, engine func(ctx context.Context, input any, parameters map[string]any) (any, error)) (int, error) {
	return er.RunSomeExamples(ctx, engine, "", 0)
}

// RunSomeExamples runs an engine function on all or some of the examples and scores the results
func (er *ExperimentRunner) RunSomeExamples(ctx context.Context, engine func(ctx context.Context, input any, parameters map[string]any) (any, error), tag string, limit int) (int, error) {
	// Ensure experiment is loaded
	if er.experiment == nil {
		if er.experimentId != "" {
			if _, err := er.LoadExperiment(ctx, er.experimentId); err != nil {
				return 0, fmt.Errorf("failed to load experiment: %w", err)
			}
		} else {
			if _, err := er.CreateExperiment(ctx, nil); err != nil {
				return 0, fmt.Errorf("failed to create experiment: %w", err)
			}
		}
	}

	examples, err := er.GetExampleInputs(ctx, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to get examples: %w", err)
	}
	// If tag is set, filter examples by tag
	if tag != "" {
		filteredExamples := make([]Example, 0, len(examples))
		for _, example := range examples {
			if len(example.Tags) > 0 && slices.Contains(example.Tags, tag) {
				filteredExamples = append(filteredExamples, example)
			}
		}
		examples = filteredExamples
	}
	// If limit is set, limit the number of examples
	if limit > 0 {
		if limit < len(examples) {
			examples = examples[:limit]
		}
	}
	var dataset *Dataset
	if er.rerunExamplesWithMissingScores {
		dataset, err = er.GetDataset(ctx)
		if err != nil {
			return 0, fmt.Errorf("failed to get dataset for score completeness checks: %w", err)
		}
	}
	// Build a map of existing results by example ID for quick lookup
	existingResults := make(map[string]Result)
	if er.experiment != nil && er.experiment.Results != nil {
		for _, result := range er.experiment.Results {
			existingResults[result.Example] = result
		}
	}
	// filter out examples that have results already
	doneCnt := 0
	notDoneExamples := make([]Example, 0, len(examples))
	for _, example := range examples {
		existingResult, hasExistingResult := existingResults[example.Id]
		if hasExistingResult {
			if er.rerunExamplesWithMissingScores {
				// Does it have all the scores?
				metrics := getMetricsForExample(dataset, example)
				hasAllScores := true
				for _, metric := range metrics {
					metricId := metric.Id
					if metricId == "" {
						metricId = metric.Name
					}
					if metricId == "" {
						continue
					}
					if _, ok := existingResult.Scores[metricId]; !ok {
						hasAllScores = false
						fmt.Printf("AIQA: Example %s is missing score for metric %s\n", example.Id, metricId)
						break
					}
				}
				if !hasAllScores {
					notDoneExamples = append(notDoneExamples, example)
					continue
				}
				// already done - skip it here
				fmt.Printf("AIQA: Example %s has all scores :) - skipping\n", example.Id)
			}
			// already done - skip it here
			doneCnt++
		} else {
			notDoneExamples = append(notDoneExamples, example)
		}
	}
	fmt.Printf("AIQA: Running %d examples, %d done, %d not done\n", len(examples), doneCnt, len(notDoneExamples))
	examples = notDoneExamples

	// run the experiment
	cnt := 0
	for _, example := range examples {
		_, err := er.RunExample(ctx, example, engine)
		if err != nil {
			// Check if callMyCode requested early termination
			if errors.Is(err, ErrStopExperiment) || strings.HasPrefix(err.Error(), ERROR_PREFIX_STOP_EXPERIMENT) {
				fmt.Printf("AIQA: Stopping early as requested by callMyCode: %v\n", err)
				return cnt, err
			}
			fmt.Printf("AIQA: Error processing example %s: %v\n", example.Id, err)
			continue
		}
		cnt++
	}

	return cnt, nil
}

func (er *ExperimentRunner) getLLMCallFn(model string) LLMCallFn {
	if er.llmCallFn != nil {
		// TODO wrap with tracing
		return er.llmCallFn
	}
	// TODO refactor with llm as judge code to do api key based OpenAI
	return nil
}

func (er *ExperimentRunner) scoreLLMMetric(ctx context.Context, input, output any, example Example, metric Metric) (MetricResult, error) {
	llmCallFn := er.getLLMCallFn(metric.Model)
	if llmCallFn != nil {
		return ScoreLLMMetricLocal(input, output, example, metric, llmCallFn)
	}
	return MetricResult{}, fmt.Errorf("no LLM call function found")
}

// RunExample runs the engine on an example with the given parameters (looping over comparison parameters), and scores the result.
// Also calls ScoreAndStore to store the result in the server.
// If scorerForMetricId is non-nil, metrics are taken from dataset + example; for each metric either the custom scorer is used
// or, when metric.Type == "llm", the built-in LLM-as-judge is used (see LlmCallFn and OPENAI_API_KEY/ANTHROPIC_API_KEY).
// callMyCode receives a context.Context as the first parameter, which can be used to propagate trace context in HTTP calls.
func (er *ExperimentRunner) RunExample(ctx context.Context,
	example Example,
	callMyCode CallMyCodeFunc,
) (*Result, error) {
	fmt.Printf("AIQA: RunExample %s\n", example.Id)
	if er.experiment == nil {
		fmt.Printf("AIQA: DEBUG RunExample creating experiment: example.id=%q example.dataset=%q example.organisation=%q runner.datasetId=%q\n",
			example.Id, example.Dataset, example.Organisation, er.datasetId)
		if _, err := er.CreateExperiment(ctx, nil); err != nil {
			return nil, fmt.Errorf("failed to create experiment: %w", err)
		}
	}
	if er.experiment == nil {
		return nil, fmt.Errorf("failed to create experiment")
	}

	parametersHere := er.experiment.Parameters
	if parametersHere == nil {
		parametersHere = make(map[string]any)
	}
	input := example.Input
	if input == nil && len(example.Spans) > 0 {
		if spanMap, ok := example.Spans[0].(map[string]any); ok {
			if attributes, ok := spanMap["attributes"].(map[string]any); ok {
				input = attributes["input"]
			}
		}
	}
	if input == nil {
		fmt.Printf("AIQA: Warning: Example has no input field or spans with input attribute: %v\n", example)
	}

	result, err := er.runExampleWithParameters(ctx, example, input, callMyCode, parametersHere)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// This function juggles two separate  contexts and spans: The experiment runner, and the example being run.
// The spans and token counts from these must be kept separate.
func (er *ExperimentRunner) runExampleWithParameters(
	runnerCtx context.Context,
	example Example,
	input any,
	callMyCode CallMyCodeFunc,
	parameters map[string]any,
) (*Result, error) {
	parametersFixed := er.experiment.Parameters
	if parametersFixed == nil {
		parametersFixed = make(map[string]any)
	}
	parametersHere := make(map[string]any)
	for k, v := range parametersFixed {
		parametersHere[k] = v
	}
	for k, v := range parameters {
		parametersHere[k] = v
	}

	// Explicit OTEL runExampleSpan with clean attributes (example.id, input, parameters, output)
	// Must be in a fresh otel context so its not counted in with other activity (eg scoring or other examples)
	// Start as a root trace (no parent span) using context.Background()
	var runExampleSpan trace.Span
	runExampleCtx := runnerCtx
	if runExampleCtx == nil {
		runExampleCtx = context.Background()
	}
	if _ = ensureTracingInitialized("", "", nil); tracingEnabled && tracer != nil {
		runExampleCtx, runExampleSpan = tracer.Start(context.Background(), "RunExample")
		defer runExampleSpan.End()
		setComponentTagIfSet(runExampleSpan)
		inputDict := map[string]any{
			"example.id": example.Id,
			"input":      input,
		}
		if len(parametersHere) > 0 {
			inputDict["parameters"] = parametersHere
		}
		runExampleSpan.SetAttributes(attribute.String("input", serializeValue(inputDict)))
		// Set AIQA-specific attributes (used by the server to match spans to experiments, and examples to spans)
		runExampleSpan.SetAttributes(attribute.String(AIQA_TRACE_ID, er.experimentId))
		runExampleSpan.SetAttributes(attribute.String(AIQA_EXAMPLE_ID, example.Id))
	}

	fmt.Printf("AIQA: Running with parameters: %v\n", parametersHere)

	// Set env vars from parameters; restore when done
	restoreEnv := setEnvFromMap(parametersHere)

	// Parameters are also passed directly to the engine; env vars allow legacy code to read from os.Getenv
	start := time.Now()
	// ======================================================
	// Call the engine function - Run the example!!
	// Pass context so callMyCode can propagate trace context in HTTP calls using InjectTraceContext
	output, err := callMyCode(runExampleCtx, input, parametersHere)
	// process the output into scores
	duration := time.Since(start)
	if err != nil {
		// Errors do get traced - but are not saved as experiment results
		if runExampleSpan != nil {
			runExampleSpan.RecordError(err)
			runExampleSpan.SetStatus(codes.Error, err.Error())
		}
		restoreEnv()
		// Preserve ErrStopEarly so Run() can detect it
		if errors.Is(err, ErrStopExperiment) || strings.HasPrefix(err.Error(), ERROR_PREFIX_STOP_EXPERIMENT) {
			return nil, err
		}
		return nil, fmt.Errorf("engine function failed: %w", err)
	}

	if runExampleSpan != nil {
		runExampleSpan.SetAttributes(attribute.String("output", serializeValue(output)))
	}

	fmt.Printf("AIQA: Output: %v\n", output)

	// Score the output using the local scoring functions or LLM-as-judge
	scores, err := er.scoreExampleOutputLocal(runnerCtx, example, input, output, parametersHere)

	if err != nil {
		if runExampleSpan != nil {
			runExampleSpan.RecordError(err)
			runExampleSpan.SetStatus(codes.Error, err.Error())
		}
		restoreEnv()
		return nil, fmt.Errorf("failed to score example output: %w", err)
	}

	// Add the duration to the scores
	scores["duration"] = float64(duration.Milliseconds())

	result := Result{
		Example: example.Id,
		Trace:   GetTraceId(runExampleCtx),
	}

	fmt.Printf("AIQA: Call scoreAndStore ... for example: %s with scores: %v\n", example.Id, scores)
	// Store the scores on the server (and trigger any server-side scoring)
	remoteResult, err := er.ScoreAndStore(runnerCtx, &result, output, scores)
	if err != nil {
		if runExampleSpan != nil {
			runExampleSpan.RecordError(err)
			runExampleSpan.SetStatus(codes.Error, err.Error())
		}
		restoreEnv()
		return nil, fmt.Errorf("failed to score and store: %w", err)
	}
	fmt.Printf("AIQA: scoreAndStore returned: %v\n", remoteResult)

	if runExampleSpan != nil {
		runExampleSpan.SetStatus(codes.Ok, "")
	}
	restoreEnv()
	return remoteResult, nil
}

func getMetricsForExample(dataset *Dataset, example Example) []Metric {
	metrics := []Metric{}
	if dataset != nil && len(dataset.Metrics) > 0 {
		metrics = append(metrics, dataset.Metrics...)
	}
	if len(example.Metrics) > 0 {
		metrics = append(metrics, example.Metrics...)
	}
	return metrics
}

// Run local scoring functions (the server might run more later, depending on where LLM keys are setup)
func (er *ExperimentRunner) scoreExampleOutputLocal(ctx context.Context, example Example, input any, output any, parameters map[string]any) (map[string]float64, error) {
	scores := make(map[string]float64)
	// metrics are from dataset + example (which could be unset)
	dataset, err := er.GetDataset(ctx)
	if err != nil {
		return nil, fmt.Errorf("get dataset for metrics: %w", err)
	}
	metrics := getMetricsForExample(dataset, example)
	// loop over metrics and score them
	for _, metric := range metrics {
		metricId := metric.Id
		if metricId == "" {
			metricId = metric.Name
		}
		if metricId == "" {
			fmt.Printf("AIQA: Warning: metric missing id and name, skipping: %v\n", metric)
			continue
		}
		fmt.Printf("AIQA: Scoring metric %s (type %s)\n", metricId, metric.Type)
		var mr MetricResult
		var scoreErr error
		if fn := er.scorerForMetricId[metricId]; fn != nil {
			mr, scoreErr = fn(input, output, example, metric, parameters)
		} else if metric.Type == "llm" {
			mr, scoreErr = er.scoreLLMMetric(ctx, input, output, example, metric)
		} else {
			fmt.Printf("AIQA: Skipping metric %s (type %s) - no scorer\n", metricId, metric.Type)
			continue
		}
		if scoreErr != nil {
			fmt.Printf("AIQA: Error scoring metric %s: %v\n", metricId, scoreErr)
			continue
		}
		scores[metricId] = mr.Score
	}
	return scores, nil
}

// LoadExperiment loads an existing experiment by ID. This allows the experiment to be resumed.
func (er *ExperimentRunner) LoadExperiment(ctx context.Context, experimentId string) (*Experiment, error) {
	if experimentId == "" {
		return nil, fmt.Errorf("experiment ID is required")
	}

	url := fmt.Sprintf("%s/experiment/%s", er.serverUrl, experimentId)
	resp, err := makeRequest(ctx, "GET", url, nil, er.apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to load experiment: %w", err)
	}

	var experiment Experiment
	if err := parseJSONResponse(resp, &experiment); err != nil {
		return nil, fmt.Errorf("failed to load experiment: %w", err)
	}

	er.experimentId = experiment.Id
	er.experiment = &experiment
	if er.organisation == "" && experiment.Organisation != "" {
		er.organisation = experiment.Organisation
	}
	if er.datasetId == "" && experiment.Dataset != "" {
		er.datasetId = experiment.Dataset
	}

	return &experiment, nil
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

	// Convert summaries to MetricStats
	summaryResults := make(map[string]MetricStats)
	if experiment.Summaries != nil {
		for key, value := range experiment.Summaries {
			if statsMap, ok := value.(map[string]any); ok {
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
