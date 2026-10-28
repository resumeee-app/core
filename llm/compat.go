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
	defaultCompatModel = "default"
	ProviderCompat     = "compat"
	ProviderCompatAlt  = "openai-compat"
)

// CompatProvider implements domain.LLMProvider for generic OpenAI-compatible HTTP endpoints
// such as Ollama, vLLM, llama.cpp, LocalAI, and LM Studio.
type CompatProvider struct {
	baseURL      string
	apiKey       string
	httpClient   *http.Client
	defaultModel string
}

// NewCompatProvider creates an OpenAI-compatible adapter targeting baseURL with optional API key.
func NewCompatProvider(baseURL string, apiKey string, opts ...Option) *CompatProvider {
	cfg := applyOptions(opts, baseURL, defaultCompatModel)
	return &CompatProvider{
		baseURL:      cfg.baseURL,
		apiKey:       apiKey,
		httpClient:   cfg.httpClient,
		defaultModel: cfg.defaultModel,
	}
}

// Name returns the provider identifier.
func (p *CompatProvider) Name() string {
	return ProviderCompat
}

// GenerateTailoredResume calls the OpenAI-compatible chat completions endpoint.
func (p *CompatProvider) GenerateTailoredResume(ctx context.Context, req domain.GenerationRequest) (*domain.GenerationResponse, error) {
	prompts, err := BuildPrompt(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	model := req.Model
	if strings.TrimSpace(model) == "" {
		model = p.defaultModel
	}

	// For compatibility with models lacking strict schema engines, embed schema instructions in system prompt
	schemaBytes, err := json.Marshal(GenerationResponseSchema())
	if err != nil {
		return nil, fmt.Errorf("failed to serialize schema: %w", err)
	}

	systemPromptWithSchema := fmt.Sprintf("%s\n\n<json_output_schema>\n%s\n</json_output_schema>",
		prompts.SystemPrompt, string(schemaBytes))

	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	type responseFormat struct {
		Type string `json:"type"`
	}

	type requestBody struct {
		Model          string          `json:"model"`
		Messages       []chatMessage   `json:"messages"`
		ResponseFormat *responseFormat `json:"response_format,omitempty"`
	}

	payload := requestBody{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPromptWithSchema},
			{Role: "user", Content: prompts.UserPrompt},
		},
		ResponseFormat: &responseFormat{
			Type: "json_object",
		},
	}

	reqBodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal compat request: %w", err)
	}

	url := buildCompatURL(p.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(p.apiKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("compat http request failed: %w", err)
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
		return nil, fmt.Errorf("compat api returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	type compatResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	var compatResp compatResponse
	if err := json.Unmarshal(bodyBytes, &compatResp); err != nil {
		return nil, fmt.Errorf("%w: malformed compat response: %v", ErrInvalidResponse, err)
	}

	if len(compatResp.Choices) == 0 {
		return nil, fmt.Errorf("%w: no choices returned by compat endpoint", ErrInvalidResponse)
	}

	rawText := strings.TrimSpace(compatResp.Choices[0].Message.Content)
	cleanJSON := stripCodeFences(rawText)

	var genResp domain.GenerationResponse
	if err := json.Unmarshal([]byte(cleanJSON), &genResp); err != nil {
		return nil, fmt.Errorf("%w: failed to unmarshal generation response: %v", ErrInvalidResponse, err)
	}

	return &genResp, nil
}

// CompleteText implements RawCompleter for OpenAI-compatible endpoints.
func (p *CompatProvider) CompleteText(ctx context.Context, systemPrompt, userPrompt, model string) (string, error) {
	if strings.TrimSpace(model) == "" {
		model = p.defaultModel
	}

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
		return "", fmt.Errorf("failed to marshal compat request: %w", err)
	}

	url := buildCompatURL(p.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(p.apiKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("compat http request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("%w: status %d: %s", ErrUnauthorized, resp.StatusCode, string(bodyBytes))
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("%w: status 429: %s", ErrRateLimited, string(bodyBytes))
	}
	if resp.StatusCode >= 500 {
		return "", fmt.Errorf("%w: status %d: %s", ErrUpstream, resp.StatusCode, string(bodyBytes))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("compat api returned status %d: %s", resp.StatusCode, string(bodyBytes))
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
		return "", fmt.Errorf("%w: malformed compat response: %v", ErrInvalidResponse, err)
	}

	if len(openAIResp.Choices) == 0 {
		return "", fmt.Errorf("%w: no choices returned by compat endpoint", ErrInvalidResponse)
	}

	return strings.TrimSpace(openAIResp.Choices[0].Message.Content), nil
}

func buildCompatURL(baseURL string) string {
	cleaned := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(cleaned, "/chat/completions") {
		return cleaned
	}
	return cleaned + "/chat/completions"
}
