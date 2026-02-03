package aiqa

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/template"
)

// MetricResult is the result of evaluating one metric on an output (score 0–1, optional message/error).
type MetricResult struct {
	Score   float64 `json:"score"`
	Message string  `json:"message,omitempty"`
	Error   string  `json:"error,omitempty"`
}

// LLMCallFn is a function that calls an LLM with system and user prompts and returns raw content (typically JSON).
// If nil, the runner uses OPENAI_API_KEY / ANTHROPIC_API_KEY env or fetches model from server.
type LLMCallFn func(systemPrompt, userMessage string) (string, error)

// ScorerForMetricFn scores one metric given input, output, example, metric definition, and parameters.
// Returns MetricResult with score in [0,1], optional message and error.
type ScorerForMetricFn func(input, output interface{}, example Example, metric Metric, params map[string]interface{}) (MetricResult, error)

var (
	//go:embed templates/llm_as_judge_default.txt
	defaultLLMPromptTemplateStr string

	defaultLLMPromptTemplate = template.Must(
		template.New("llm-as-judge-default").Parse(defaultLLMPromptTemplateStr),
	)
)

// ParseLLMResponse parses LLM response content (JSON with "score" and optional "message") into MetricResult.
// Clamps score to [0, 1].
func ParseLLMResponse(content string) (MetricResult, error) {
	var raw struct {
		Score   interface{} `json:"score"`
		Message string      `json:"message"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return MetricResult{}, fmt.Errorf("could not parse LLM response: %w", err)
	}
	if raw.Score == nil {
		return MetricResult{}, fmt.Errorf("LLM response missing score")
	}
	var score float64
	switch v := raw.Score.(type) {
	case float64:
		score = v
	case int:
		score = float64(v)
	default:
		return MetricResult{}, fmt.Errorf("LLM response score is not a number: %T", raw.Score)
	}
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return MetricResult{Score: score, Message: raw.Message}, nil
}

// GetModelFromServer fetches model from the AIQA server (with api_key if permitted). Returns nil if not found or no api_key.
func GetModelFromServer(ctx context.Context, modelId, serverUrl, apiKey string) (apiKeyOut string, provider string, _ error) {
	url := fmt.Sprintf("%s/model/%s?fields=api_key", strings.TrimSuffix(serverUrl, "/"), modelId)
	resp, err := makeRequest(ctx, "GET", url, nil, apiKey)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", nil
	}
	var model struct {
		ApiKey   string `json:"api_key"`
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&model); err != nil {
		return "", "", err
	}
	if model.ApiKey == "" {
		return "", "", nil
	}
	return model.ApiKey, model.Provider, nil
}

// callOpenAI calls the OpenAI chat completions API. Returns raw content string.
func callOpenAI(systemPrompt, userMessage, apiKey string) (string, error) {
	body := map[string]interface{}{
		"model": "gpt-4o-mini",
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userMessage},
		},
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OpenAI API error: %d - %s", resp.StatusCode, string(data))
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("OpenAI API did not return content")
	}
	return result.Choices[0].Message.Content, nil
}

// callAnthropic calls the Anthropic messages API. Returns raw content string.
func callAnthropic(systemPrompt, userMessage, apiKey string) (string, error) {
	body := map[string]interface{}{
		"model":       "claude-3-5-sonnet-20241022",
		"max_tokens":  1024,
		"temperature": 0,
		"system":      systemPrompt,
		"messages":    []map[string]string{{"role": "user", "content": userMessage}},
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Anthropic API error: %d - %s", resp.StatusCode, string(data))
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	if len(result.Content) == 0 || result.Content[0].Text == "" {
		return "", fmt.Errorf("Anthropic API did not return content")
	}
	return result.Content[0].Text, nil
}

// CallLLMFallback calls OpenAI or Anthropic using apiKey and provider, or env OPENAI_API_KEY / ANTHROPIC_API_KEY.
func CallLLMFallback(systemPrompt, userMessage string, apiKey string, provider string) (string, error) {
	if apiKey != "" {
		switch provider {
		case "openai", "":
			return callOpenAI(systemPrompt, userMessage, apiKey)
		case "anthropic":
			return callAnthropic(systemPrompt, userMessage, apiKey)
		default:
			content, err := callOpenAI(systemPrompt, userMessage, apiKey)
			if err == nil {
				return content, nil
			}
			return callAnthropic(systemPrompt, userMessage, apiKey)
		}
	}
	openaiKey := os.Getenv("OPENAI_API_KEY")
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	if openaiKey != "" {
		return callOpenAI(systemPrompt, userMessage, openaiKey)
	}
	if anthropicKey != "" {
		return callAnthropic(systemPrompt, userMessage, anthropicKey)
	}
	return "", fmt.Errorf("no LLM API key: set metric.model (server key), or OPENAI_API_KEY or ANTHROPIC_API_KEY")
}

// ScoreLLMMetricLocal scores one LLM-as-judge metric: builds prompts from metric, calls LLM, parses response.
func ScoreLLMMetricLocal(
	input, output interface{},
	example Example,
	metric Metric,
	llmCallFn LLMCallFn,
) (MetricResult, error) {
	// prompt - provided or build a default
	sysStr := metric.Prompt
	if sysStr == "" && metric.Parameters != nil {
		if p, ok := metric.Parameters["prompt"].(string); ok && p != "" {
			sysStr = p
		}
	}
	if sysStr == "" {
		sysStr = renderDefaultLLMPrompt(metric)
	}

	inputText := toJSONOrString(input)
	outputText := toJSONOrString(output)
	userMessage := fmt.Sprintf("<INPUT>\n%s\n</INPUT>\n<OUTPUT>\n%s\n</OUTPUT>\nEvaluate this OUTPUT and return ONLY a valid JSON object {\"score\": number, \"message\": string}.", inputText, outputText)

	var content string
	var err error
	if llmCallFn != nil {
		content, err = llmCallFn(sysStr, userMessage)
	} else {
		content, err = CallLLMFallback(sysStr, userMessage, "", "")
	}
	if err != nil {
		return MetricResult{}, err
	}
	result, err := ParseLLMResponse(content)
	if err != nil {
		return MetricResult{}, err
	}
	return result, nil
}

func toJSONOrString(v interface{}) string {
	if v == nil {
		return ""
	}
	if m, ok := v.(map[string]interface{}); ok {
		b, _ := json.Marshal(m)
		return string(b)
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func renderDefaultLLMPrompt(metric Metric) string {
	name := metric.Name
	if name == "" {
		name = metric.Id
	}
	if name == "" {
		name = "helpful, honest, and harmless"
	}
	criteria := metric.PromptCriteria
	if criteria == "" {
		criteria = metric.Description
	}
	if criteria == "" {
		criteria = "helpful, honest, and harmless"
	}

	var b strings.Builder
	if err := defaultLLMPromptTemplate.Execute(&b, struct {
		MetricName string
		Criteria   string
	}{
		MetricName: name,
		Criteria:   criteria,
	}); err != nil {
		// Template execution should never fail; fall back to a direct format if it does.
		return fmt.Sprintf(`You are an LLM-as-Judge rating an AI assistant's output on the specific criteria of: %s.

Review the last assistant output, in the light of the conversation and context.
Look for:
%s

Be strict but fair in your evaluation.

Output in json using the format: 
{score:[0,1], message:string}`, name, criteria)
	}
	return b.String()
}
