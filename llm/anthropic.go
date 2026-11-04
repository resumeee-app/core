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
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	defaultAnthropicModel   = "claude-3-7-sonnet-20250219"
	anthropicAPIVersion     = "2023-06-01"
	defaultAnthropicTokens  = 8192
	ProviderAnthropic       = "anthropic"
	anthropicToolName       = "generate_tailored_resume"
)

// AnthropicProvider implements domain.LLMProvider for Anthropic Claude models via Messages API tool use.
type AnthropicProvider struct {
	apiKey         string
	baseURL        string
	httpClient     *http.Client
	defaultModel   string
	logger         *slog.Logger
	promptDebugger PromptDebugger
}

// NewAnthropicProvider creates an Anthropic adapter with the specified API key.
func NewAnthropicProvider(apiKey string, opts ...Option) *AnthropicProvider {
	cfg := applyOptions(opts, defaultAnthropicBaseURL, defaultAnthropicModel)
	return &AnthropicProvider{
		apiKey:         apiKey,
		baseURL:        cfg.baseURL,
		httpClient:     cfg.httpClient,
		defaultModel:   cfg.defaultModel,
		logger:         cfg.logger,
		promptDebugger: cfg.promptDebugger,
	}
}

// Name returns the provider identifier.
func (p *AnthropicProvider) Name() string {
	return ProviderAnthropic
}

// GenerateTailoredResume calls the Anthropic Messages API with forced tool use and parses the structured response.
func (p *AnthropicProvider) GenerateTailoredResume(ctx context.Context, req domain.GenerationRequest) (genResp *domain.GenerationResponse, retErr error) {
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
		retErr = fmt.Errorf("failed to marshal anthropic request: %w", err)
		return nil, retErr
	}

	url := fmt.Sprintf("%s/v1/messages", strings.TrimRight(p.baseURL, "/"))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		retErr = fmt.Errorf("failed to create http request: %w", err)
		return nil, retErr
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		retErr = fmt.Errorf("anthropic http request failed: %w", err)
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

	if err := checkStatusError("anthropic", resp.StatusCode, bodyBytes); err != nil {
		retErr = err
		return nil, retErr
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
		retErr = fmt.Errorf("%w: malformed anthropic response: %v", ErrInvalidResponse, err)
		return nil, retErr
	}

	var targetInput json.RawMessage
	for _, block := range anthropicResp.Content {
		if block.Type == "tool_use" && block.Name == anthropicToolName {
			targetInput = block.Input
			break
		}
	}

	if len(targetInput) == 0 {
		retErr = fmt.Errorf("%w: no %s tool call in anthropic response", ErrInvalidResponse, anthropicToolName)
		return nil, retErr
	}

	var parsed domain.GenerationResponse
	if err := json.Unmarshal(targetInput, &parsed); err != nil {
		retErr = fmt.Errorf("%w: failed to unmarshal generation response from tool input: %v", ErrInvalidResponse, err)
		return nil, retErr
	}

	return &parsed, nil
}

// CompleteText implements RawCompleter for Anthropic Claude.
func (p *AnthropicProvider) CompleteText(ctx context.Context, systemPrompt, userPrompt, model string) (text string, retErr error) {
	if strings.TrimSpace(model) == "" {
		model = p.defaultModel
	}

	trace := logLLMRequest(ctx, p.logger, p.promptDebugger, p.Name(), model, "CompleteText", systemPrompt, userPrompt)

	var statusCode int
	var rawResp string
	defer func() {
		logLLMResponse(ctx, p.logger, trace, statusCode, rawResp, retErr)
	}()

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
		retErr = fmt.Errorf("failed to marshal anthropic request: %w", err)
		return "", retErr
	}

	url := fmt.Sprintf("%s/v1/messages", strings.TrimRight(p.baseURL, "/"))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		retErr = fmt.Errorf("failed to create http request: %w", err)
		return "", retErr
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		retErr = fmt.Errorf("anthropic http request failed: %w", err)
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

	if err := checkStatusError("anthropic", resp.StatusCode, bodyBytes); err != nil {
		retErr = err
		return "", retErr
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
		retErr = fmt.Errorf("%w: malformed anthropic response: %v", ErrInvalidResponse, err)
		return "", retErr
	}

	for _, block := range anthropicResp.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			return strings.TrimSpace(block.Text), nil
		}
	}

	retErr = fmt.Errorf("%w: no text content returned by anthropic", ErrInvalidResponse)
	return "", retErr
}
