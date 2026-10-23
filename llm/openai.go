package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	defaultModel string
}

// NewOpenAIProvider creates an OpenAI adapter with the specified API key.
func NewOpenAIProvider(apiKey string, opts ...Option) *OpenAIProvider {
	cfg := applyOptions(opts, defaultOpenAIBaseURL, defaultOpenAIModel)
	return &OpenAIProvider{
		apiKey:       apiKey,
		baseURL:      cfg.baseURL,
		httpClient:   cfg.httpClient,
		defaultModel: cfg.defaultModel,
	}
}

// Name returns the provider identifier.
func (p *OpenAIProvider) Name() string {
	return ProviderOpenAI
}

// GenerateTailoredResume calls the OpenAI v1/chat/completions API with strict json_schema response_format.
func (p *OpenAIProvider) GenerateTailoredResume(ctx context.Context, req domain.GenerationRequest) (*domain.GenerationResponse, error) {
	prompts, err := BuildPrompt(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	model := req.Model
	if strings.TrimSpace(model) == "" {
		model = p.defaultModel
	}

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
		return nil, fmt.Errorf("failed to marshal openai request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/chat/completions", strings.TrimRight(p.baseURL, "/"))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai http request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: status %d: %s", ErrUnauthorized, resp.StatusCode, string(bodyBytes))
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%w: status 429: %s", ErrRateLimited, string(bodyBytes))
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: status %d: %s", ErrUpstream, resp.StatusCode, string(bodyBytes))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai api returned status %d: %s", resp.StatusCode, string(bodyBytes))
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
		return nil, fmt.Errorf("%w: malformed openai response: %v", ErrInvalidResponse, err)
	}

	if len(openAIResp.Choices) == 0 {
		return nil, fmt.Errorf("%w: no choices returned by openai", ErrInvalidResponse)
	}

	rawText := strings.TrimSpace(openAIResp.Choices[0].Message.Content)
	cleanJSON := stripCodeFences(rawText)

	var genResp domain.GenerationResponse
	if err := json.Unmarshal([]byte(cleanJSON), &genResp); err != nil {
		return nil, fmt.Errorf("%w: failed to unmarshal generation response: %v", ErrInvalidResponse, err)
	}

	return &genResp, nil
}
