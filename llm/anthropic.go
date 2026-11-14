package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/resumeee-app/resumeee-core"
)

const (
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	anthropicAPIVersion     = "2023-06-01"
	defaultAnthropicTokens  = 8192
)

// AnthropicProvider implements core.LLMClient for Anthropic Claude models via the Messages API.
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
func (p *AnthropicProvider) Models() []core.ModelOption {
	def := p.DefaultModel()
	return []core.ModelOption{
		{
			Provider:    ProviderAnthropic,
			Model:       def,
			DisplayName: fmt.Sprintf("Anthropic — %s", def),
			IsDefault:   true,
		},
	}
}

// SupportsStructuredOutput reports that Anthropic natively enforces JSON schemas via output_config.format.
func (p *AnthropicProvider) SupportsStructuredOutput() bool {
	return true
}

// Complete executes a completion request against the Anthropic Messages API.
func (p *AnthropicProvider) Complete(ctx context.Context, req core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.DefaultModel()
	}

	type anthropicMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	type outputFormat struct {
		Type   string         `json:"type"`
		Schema map[string]any `json:"schema"`
	}

	type outputConfig struct {
		Format *outputFormat `json:"format,omitempty"`
	}

	type requestBody struct {
		Model        string             `json:"model"`
		MaxTokens    int                `json:"max_tokens"`
		System       string             `json:"system,omitempty"`
		Messages     []anthropicMessage `json:"messages"`
		OutputConfig *outputConfig      `json:"output_config,omitempty"`
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
		payload.OutputConfig = &outputConfig{
			Format: &outputFormat{
				Type:   "json_schema",
				Schema: SanitizeAnthropicSchema(req.ResponseSchema),
			},
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
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, err
	}

	type contentBlock struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}

	type anthropicResponse struct {
		StopReason string         `json:"stop_reason,omitempty"`
		Content    []contentBlock `json:"content"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	var anthropicResp anthropicResponse
	if err := json.Unmarshal(bodyBytes, &anthropicResp); err != nil {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, fmt.Errorf("%w: malformed anthropic response: %v", ErrInvalidResponse, err)
	}

	usage := core.TokenUsage{
		PromptTokens:     anthropicResp.Usage.InputTokens,
		CompletionTokens: anthropicResp.Usage.OutputTokens,
		TotalTokens:      anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens,
	}

	if anthropicResp.StopReason == "refusal" {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
			Usage:      usage,
		}, fmt.Errorf("%w: anthropic refused structured output request", ErrInvalidResponse)
	}

	for _, block := range anthropicResp.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			return &core.LLMCompletionResponse{
				Text:       strings.TrimSpace(block.Text),
				RawBody:    rawBody,
				StatusCode: resp.StatusCode,
				Usage:      usage,
			}, nil
		}
	}

	return &core.LLMCompletionResponse{
		RawBody:    rawBody,
		StatusCode: resp.StatusCode,
	}, fmt.Errorf("%w: no text content returned by anthropic", ErrInvalidResponse)
}
