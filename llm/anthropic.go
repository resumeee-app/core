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
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	defaultAnthropicModel   = "claude-3-7-sonnet-20250219"
	anthropicAPIVersion     = "2023-06-01"
	defaultAnthropicTokens  = 8192
	ProviderAnthropic       = "anthropic"
	anthropicToolName       = "generate_tailored_resume"
)

// AnthropicProvider implements domain.LLMProvider for Anthropic Claude models via Messages API tool use.
type AnthropicProvider struct {
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	defaultModel string
}

// NewAnthropicProvider creates an Anthropic adapter with the specified API key.
func NewAnthropicProvider(apiKey string, opts ...Option) *AnthropicProvider {
	cfg := applyOptions(opts, defaultAnthropicBaseURL, defaultAnthropicModel)
	return &AnthropicProvider{
		apiKey:       apiKey,
		baseURL:      cfg.baseURL,
		httpClient:   cfg.httpClient,
		defaultModel: cfg.defaultModel,
	}
}

// Name returns the provider identifier.
func (p *AnthropicProvider) Name() string {
	return ProviderAnthropic
}

// GenerateTailoredResume calls the Anthropic Messages API with forced tool use and parses the structured response.
func (p *AnthropicProvider) GenerateTailoredResume(ctx context.Context, req domain.GenerationRequest) (*domain.GenerationResponse, error) {
	prompts, err := BuildPrompt(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	model := req.Model
	if strings.TrimSpace(model) == "" {
		model = p.defaultModel
	}

	type anthropicMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	type anthropicTool struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"input_schema"`
	}

	type anthropicToolChoice struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}

	type requestBody struct {
		Model      string              `json:"model"`
		MaxTokens  int                 `json:"max_tokens"`
		System     string              `json:"system,omitempty"`
		Messages   []anthropicMessage  `json:"messages"`
		Tools      []anthropicTool     `json:"tools"`
		ToolChoice anthropicToolChoice `json:"tool_choice"`
	}

	payload := requestBody{
		Model:     model,
		MaxTokens: defaultAnthropicTokens,
		System:    prompts.SystemPrompt,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompts.UserPrompt},
		},
		Tools: []anthropicTool{
			{
				Name:        anthropicToolName,
				Description: "Output tailored resume and gap analysis adhering strictly to candidate profile",
				InputSchema: GenerationResponseSchema(),
			},
		},
		ToolChoice: anthropicToolChoice{
			Type: "tool",
			Name: anthropicToolName,
		},
	}

	reqBodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal anthropic request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/messages", strings.TrimRight(p.baseURL, "/"))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic http request failed: %w", err)
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
		return nil, fmt.Errorf("anthropic api returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	type contentBlock struct {
		Type  string          `json:"type"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	}

	type anthropicResponse struct {
		Content []contentBlock `json:"content"`
	}

	var anthropicResp anthropicResponse
	if err := json.Unmarshal(bodyBytes, &anthropicResp); err != nil {
		return nil, fmt.Errorf("%w: malformed anthropic response: %v", ErrInvalidResponse, err)
	}

	var targetInput json.RawMessage
	for _, block := range anthropicResp.Content {
		if block.Type == "tool_use" && block.Name == anthropicToolName {
			targetInput = block.Input
			break
		}
	}

	if len(targetInput) == 0 {
		return nil, fmt.Errorf("%w: no %s tool call in anthropic response", ErrInvalidResponse, anthropicToolName)
	}

	var genResp domain.GenerationResponse
	if err := json.Unmarshal(targetInput, &genResp); err != nil {
		return nil, fmt.Errorf("%w: failed to unmarshal generation response from tool input: %v", ErrInvalidResponse, err)
	}

	return &genResp, nil
}

// CompleteText implements RawCompleter for Anthropic Claude.
func (p *AnthropicProvider) CompleteText(ctx context.Context, systemPrompt, userPrompt, model string) (string, error) {
	if strings.TrimSpace(model) == "" {
		model = p.defaultModel
	}

	type anthropicMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	type requestBody struct {
		Model     string             `json:"model"`
		System    string             `json:"system,omitempty"`
		MaxTokens int                `json:"max_tokens"`
		Messages  []anthropicMessage `json:"messages"`
	}

	payload := requestBody{
		Model:     model,
		System:    systemPrompt,
		MaxTokens: defaultAnthropicTokens,
		Messages: []anthropicMessage{
			{Role: "user", Content: userPrompt},
		},
	}

	reqBodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal anthropic request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/messages", strings.TrimRight(p.baseURL, "/"))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("anthropic http request failed: %w", err)
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
		return "", fmt.Errorf("anthropic api returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	type contentBlock struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}

	type anthropicResponse struct {
		Content []contentBlock `json:"content"`
	}

	var anthropicResp anthropicResponse
	if err := json.Unmarshal(bodyBytes, &anthropicResp); err != nil {
		return "", fmt.Errorf("%w: malformed anthropic response: %v", ErrInvalidResponse, err)
	}

	for _, block := range anthropicResp.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			return strings.TrimSpace(block.Text), nil
		}
	}

	return "", fmt.Errorf("%w: no text content returned by anthropic", ErrInvalidResponse)
}
