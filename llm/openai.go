package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"resumeee/internal/domain"
)

const (
	defaultOpenAIBaseURL = "https://api.openai.com"
	defaultOpenAIModel   = "gpt-4o"
	ProviderOpenAI       = "openai"
)

// OpenAIProvider implements domain.LLMProvider for OpenAI models using Structured Outputs.
type OpenAIProvider struct {
	apiKey         string
	baseURL        string
	httpClient     *http.Client
	defaultModel   string
	logger         *slog.Logger
	promptDebugger PromptDebugger
}

// NewOpenAIProvider creates an OpenAI adapter with the specified API key.
func NewOpenAIProvider(apiKey string, opts ...Option) *OpenAIProvider {
	cfg := applyOptions(opts, defaultOpenAIBaseURL, defaultOpenAIModel)
	return &OpenAIProvider{
		apiKey:         apiKey,
		baseURL:        cfg.baseURL,
		httpClient:     cfg.httpClient,
		defaultModel:   cfg.defaultModel,
		logger:         cfg.logger,
		promptDebugger: cfg.promptDebugger,
	}
}

// Name returns the provider identifier.
func (p *OpenAIProvider) Name() string {
	return ProviderOpenAI
}

// GenerateTailoredResume calls the OpenAI v1/chat/completions API with strict json_schema response_format.
func (p *OpenAIProvider) GenerateTailoredResume(ctx context.Context, req domain.GenerationRequest) (genResp *domain.GenerationResponse, retErr error) {
	prompts, err := BuildPrompt(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	model := req.Model
	if strings.TrimSpace(model) == "" {
		model = p.defaultModel
	}

	trace := logLLMRequest(ctx, p.logger, p.promptDebugger, p.Name(), model, "GenerateTailoredResume", prompts.SystemPrompt, prompts.UserPrompt)

	var statusCode int
	var rawResp string
	defer func() {
		logLLMResponse(ctx, p.logger, trace, statusCode, rawResp, retErr)
	}()

	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	type jsonSchemaDefinition struct {
		Name   string         `json:"name"`
		Strict bool           `json:"strict"`
		Schema map[string]any `json:"schema"`
	}

	type responseFormat struct {
		Type       string               `json:"type"`
		JSONSchema jsonSchemaDefinition `json:"json_schema"`
	}

	type requestBody struct {
		Model          string         `json:"model"`
		Messages       []chatMessage  `json:"messages"`
		ResponseFormat responseFormat `json:"response_format"`
	}

	payload := requestBody{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: prompts.SystemPrompt},
			{Role: "user", Content: prompts.UserPrompt},
		},
		ResponseFormat: responseFormat{
			Type: "json_schema",
			JSONSchema: jsonSchemaDefinition{
				Name:   "tailored_resume_response",
				Strict: true,
				Schema: GenerationResponseSchema(),
			},
		},
	}

	reqBodyBytes, err := json.Marshal(payload)
	if err != nil {
		retErr = fmt.Errorf("failed to marshal openai request: %w", err)
		return nil, retErr
	}

	url := fmt.Sprintf("%s/v1/chat/completions", strings.TrimRight(p.baseURL, "/"))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		retErr = fmt.Errorf("failed to create http request: %w", err)
		return nil, retErr
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		retErr = fmt.Errorf("openai http request failed: %w", err)
		return nil, retErr
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := readBoundedBody(resp.Body)
	if err != nil {
		retErr = fmt.Errorf("failed to read response body: %w", err)
		return nil, retErr
	}

	statusCode = resp.StatusCode
	rawResp = string(bodyBytes)

	if err := checkStatusError("openai", resp.StatusCode, bodyBytes); err != nil {
		retErr = err
		return nil, retErr
	}

	type openAIResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	var openAIResp openAIResponse
	if err := json.Unmarshal(bodyBytes, &openAIResp); err != nil {
		retErr = fmt.Errorf("%w: malformed openai response: %v", ErrInvalidResponse, err)
		return nil, retErr
	}

	if len(openAIResp.Choices) == 0 {
		retErr = fmt.Errorf("%w: no choices returned by openai", ErrInvalidResponse)
		return nil, retErr
	}

	rawText := strings.TrimSpace(openAIResp.Choices[0].Message.Content)
	cleanJSON := stripCodeFences(rawText)

	var parsed domain.GenerationResponse
	if err := json.Unmarshal([]byte(cleanJSON), &parsed); err != nil {
		retErr = fmt.Errorf("%w: failed to unmarshal generation response: %v", ErrInvalidResponse, err)
		return nil, retErr
	}

	return &parsed, nil
}

// CompleteText implements RawCompleter for OpenAI.
func (p *OpenAIProvider) CompleteText(ctx context.Context, systemPrompt, userPrompt, model string) (text string, retErr error) {
	if strings.TrimSpace(model) == "" {
		model = p.defaultModel
	}

	trace := logLLMRequest(ctx, p.logger, p.promptDebugger, p.Name(), model, "CompleteText", systemPrompt, userPrompt)

	var statusCode int
	var rawResp string
	defer func() {
		logLLMResponse(ctx, p.logger, trace, statusCode, rawResp, retErr)
	}()

	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	type requestBody struct {
		Model    string        `json:"model"`
		Messages []chatMessage `json:"messages"`
	}

	payload := requestBody{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}

	reqBodyBytes, err := json.Marshal(payload)
	if err != nil {
		retErr = fmt.Errorf("failed to marshal openai request: %w", err)
		return "", retErr
	}

	url := fmt.Sprintf("%s/v1/chat/completions", strings.TrimRight(p.baseURL, "/"))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		retErr = fmt.Errorf("failed to create http request: %w", err)
		return "", retErr
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		retErr = fmt.Errorf("openai http request failed: %w", err)
		return "", retErr
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := readBoundedBody(resp.Body)
	if err != nil {
		retErr = fmt.Errorf("failed to read response body: %w", err)
		return "", retErr
	}

	statusCode = resp.StatusCode
	rawResp = string(bodyBytes)

	if err := checkStatusError("openai", resp.StatusCode, bodyBytes); err != nil {
		retErr = err
		return "", retErr
	}

	type openAIResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	var openAIResp openAIResponse
	if err := json.Unmarshal(bodyBytes, &openAIResp); err != nil {
		retErr = fmt.Errorf("%w: malformed openai response: %v", ErrInvalidResponse, err)
		return "", retErr
	}

	if len(openAIResp.Choices) == 0 {
		retErr = fmt.Errorf("%w: no choices returned by openai", ErrInvalidResponse)
		return "", retErr
	}

	return strings.TrimSpace(openAIResp.Choices[0].Message.Content), nil
}
