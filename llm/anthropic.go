package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/resumeee-app/resumeee-core/domain"
)

const (
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	anthropicAPIVersion     = "2023-06-01"
	defaultAnthropicTokens  = 8192
	anthropicToolName       = "generate_tailored_resume"
)

// AnthropicProvider implements domain.LLMClient for Anthropic Claude models via the Messages API.
type AnthropicProvider struct {
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	defaultModel string
}

// NewAnthropicProvider creates an Anthropic transport client with the specified API key.
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

// DefaultModel returns the configured default model for Anthropic.
func (p *AnthropicProvider) DefaultModel() string {
	if strings.TrimSpace(p.defaultModel) != "" {
		return p.defaultModel
	}
	return defaultAnthropicModel
}

// Models returns the default model option for Anthropic.
func (p *AnthropicProvider) Models() []domain.ModelOption {
	def := p.DefaultModel()
	return []domain.ModelOption{
		{
			Provider:    ProviderAnthropic,
			Model:       def,
			DisplayName: fmt.Sprintf("Anthropic — %s", def),
			IsDefault:   true,
		},
	}
}

// Complete executes a completion request against the Anthropic Messages API.
func (p *AnthropicProvider) Complete(ctx context.Context, req domain.LLMCompletionRequest) (*domain.LLMCompletionResponse, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.DefaultModel()
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
		Model      string               `json:"model"`
		MaxTokens  int                  `json:"max_tokens"`
		System     string               `json:"system,omitempty"`
		Messages   []anthropicMessage   `json:"messages"`
		Tools      []anthropicTool      `json:"tools,omitempty"`
		ToolChoice *anthropicToolChoice `json:"tool_choice,omitempty"`
	}

	payload := requestBody{
		Model:     model,
		MaxTokens: defaultAnthropicTokens,
		System:    req.SystemPrompt,
		Messages: []anthropicMessage{
			{Role: "user", Content: req.UserPrompt},
		},
	}

	if req.ResponseSchema != nil {
		payload.Tools = []anthropicTool{
			{
				Name:        anthropicToolName,
				Description: "Output tailored resume and gap analysis adhering strictly to candidate profile",
				InputSchema: req.ResponseSchema,
			},
		}
		payload.ToolChoice = &anthropicToolChoice{
			Type: "tool",
			Name: anthropicToolName,
		}
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

	bodyBytes, err := readBoundedBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	rawBody := string(bodyBytes)
	if err := checkStatusError("anthropic", resp.StatusCode, bodyBytes); err != nil {
		return &domain.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, err
	}

	type contentBlock struct {
		Type  string          `json:"type"`
		Name  string          `json:"name,omitempty"`
		Text  string          `json:"text,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	}

	type anthropicResponse struct {
		Content []contentBlock `json:"content"`
		Usage   struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	var anthropicResp anthropicResponse
	if err := json.Unmarshal(bodyBytes, &anthropicResp); err != nil {
		return &domain.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, fmt.Errorf("%w: malformed anthropic response: %v", ErrInvalidResponse, err)
	}

	usage := domain.TokenUsage{
		PromptTokens:     anthropicResp.Usage.InputTokens,
		CompletionTokens: anthropicResp.Usage.OutputTokens,
		TotalTokens:      anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens,
	}

	if req.ResponseSchema != nil {
		var targetInput json.RawMessage
		for _, block := range anthropicResp.Content {
			if block.Type == "tool_use" && block.Name == anthropicToolName {
				targetInput = block.Input
				break
			}
		}
		if len(targetInput) == 0 {
			return &domain.LLMCompletionResponse{
				RawBody:    rawBody,
				StatusCode: resp.StatusCode,
			}, fmt.Errorf("%w: no %s tool call in anthropic response", ErrInvalidResponse, anthropicToolName)
		}
		return &domain.LLMCompletionResponse{
			Text:       string(targetInput),
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
			Usage:      usage,
		}, nil
	}

	for _, block := range anthropicResp.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			return &domain.LLMCompletionResponse{
				Text:       strings.TrimSpace(block.Text),
				RawBody:    rawBody,
				StatusCode: resp.StatusCode,
				Usage:      usage,
			}, nil
		}
	}

	return &domain.LLMCompletionResponse{
		RawBody:    rawBody,
		StatusCode: resp.StatusCode,
	}, fmt.Errorf("%w: no text content returned by anthropic", ErrInvalidResponse)
}
