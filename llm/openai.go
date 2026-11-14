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

const defaultOpenAIBaseURL = "https://api.openai.com"

// OpenAIProvider implements core.LLMClient for OpenAI models.
type OpenAIProvider struct {
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	defaultModel string
}

// NewOpenAIProvider creates an OpenAI transport client with the specified API key.
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

// DefaultModel returns the configured default model for OpenAI.
func (p *OpenAIProvider) DefaultModel() string {
	if strings.TrimSpace(p.defaultModel) != "" {
		return p.defaultModel
	}
	return defaultOpenAIModel
}

// Models returns the default model option for OpenAI.
func (p *OpenAIProvider) Models() []core.ModelOption {
	def := p.DefaultModel()
	return []core.ModelOption{
		{
			Provider:    ProviderOpenAI,
			Model:       def,
			DisplayName: fmt.Sprintf("OpenAI — %s", def),
			IsDefault:   true,
		},
	}
}

// SupportsStructuredOutput reports that OpenAI natively enforces JSON schemas via the Responses API text.format.
func (p *OpenAIProvider) SupportsStructuredOutput() bool {
	return true
}

// Complete executes a completion request against the OpenAI v1/responses API.
func (p *OpenAIProvider) Complete(ctx context.Context, req core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.DefaultModel()
	}

	type reasoningConfig struct {
		Effort string `json:"effort"`
	}

	type textFormat struct {
		Type   string         `json:"type"`
		Name   string         `json:"name"`
		Strict bool           `json:"strict"`
		Schema map[string]any `json:"schema"`
	}

	type textConfig struct {
		Format *textFormat `json:"format,omitempty"`
	}

	type requestBody struct {
		Model        string           `json:"model"`
		Instructions string           `json:"instructions,omitempty"`
		Input        string           `json:"input"`
		Reasoning    *reasoningConfig `json:"reasoning,omitempty"`
		Text         *textConfig      `json:"text,omitempty"`
	}

	payload := requestBody{
		Model:        model,
		Instructions: req.SystemPrompt,
		Input:        req.UserPrompt,
	}

	if effort := strings.ToLower(strings.TrimSpace(req.ReasoningLevel)); effort != "" && effort != "none" {
		payload.Reasoning = &reasoningConfig{Effort: effort}
	}

	if req.ResponseSchema != nil {
		schemaName := strings.TrimSpace(req.SchemaName)
		if schemaName == "" {
			schemaName = "structured_response"
		}
		payload.Text = &textConfig{
			Format: &textFormat{
				Type:   "json_schema",
				Name:   schemaName,
				Strict: true,
				Schema: SanitizeOpenAISchema(req.ResponseSchema),
			},
		}
	}

	reqBodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal openai request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/responses", strings.TrimRight(p.baseURL, "/"))
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

	bodyBytes, err := readBoundedBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	rawBody := string(bodyBytes)
	if err := checkStatusError("openai", resp.StatusCode, bodyBytes); err != nil {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, err
	}

	type outputContent struct {
		Type    string `json:"type"`
		Text    string `json:"text,omitempty"`
		Refusal string `json:"refusal,omitempty"`
	}

	type outputItem struct {
		Type    string          `json:"type"`
		Role    string          `json:"role,omitempty"`
		Status  string          `json:"status,omitempty"`
		Content []outputContent `json:"content,omitempty"`
	}

	type openAIResponse struct {
		OutputText string       `json:"output_text,omitempty"`
		Output     []outputItem `json:"output"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}

	var openAIResp openAIResponse
	if err := json.Unmarshal(bodyBytes, &openAIResp); err != nil {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, fmt.Errorf("%w: malformed openai response: %v", ErrInvalidResponse, err)
	}

	usage := core.TokenUsage{
		PromptTokens:     openAIResp.Usage.InputTokens,
		CompletionTokens: openAIResp.Usage.OutputTokens,
		TotalTokens:      openAIResp.Usage.TotalTokens,
	}
	if usage.TotalTokens == 0 && (usage.PromptTokens > 0 || usage.CompletionTokens > 0) {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	var extractedText string
	for _, item := range openAIResp.Output {
		for _, content := range item.Content {
			if content.Type == "refusal" || strings.TrimSpace(content.Refusal) != "" {
				return &core.LLMCompletionResponse{
					RawBody:    rawBody,
					StatusCode: resp.StatusCode,
					Usage:      usage,
				}, fmt.Errorf("%w: openai model refused structured output request: %s", ErrInvalidResponse, strings.TrimSpace(content.Refusal))
			}
			if (content.Type == "output_text" || content.Type == "text") && strings.TrimSpace(content.Text) != "" && extractedText == "" {
				extractedText = strings.TrimSpace(content.Text)
			}
		}
	}
	if extractedText == "" {
		extractedText = strings.TrimSpace(openAIResp.OutputText)
	}
	if extractedText == "" {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, fmt.Errorf("%w: no output content returned by openai", ErrInvalidResponse)
	}

	return &core.LLMCompletionResponse{
		Text:       extractedText,
		RawBody:    rawBody,
		StatusCode: resp.StatusCode,
		Usage:      usage,
	}, nil
}
