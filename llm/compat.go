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

// CompatProvider implements core.LLMClient for generic OpenAI-compatible HTTP endpoints
// such as Ollama, vLLM, llama.cpp, LocalAI, and LM Studio.
type CompatProvider struct {
	baseURL      string
	apiKey       string
	httpClient   *http.Client
	defaultModel string
}

// NewCompatProvider creates an OpenAI-compatible transport client targeting baseURL with optional API key.
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

// DefaultModel returns the configured default model for the compatible endpoint.
func (p *CompatProvider) DefaultModel() string {
	if strings.TrimSpace(p.defaultModel) != "" {
		return p.defaultModel
	}
	return defaultCompatModel
}

// Models returns the default model option for the compatible endpoint.
func (p *CompatProvider) Models() []core.ModelOption {
	def := p.DefaultModel()
	return []core.ModelOption{
		{
			Provider:    ProviderCompat,
			Model:       def,
			DisplayName: fmt.Sprintf("OpenAI Compatible — %s", def),
			IsDefault:   true,
		},
	}
}

// SupportsStructuredOutput reports false for generic OpenAI-compatible endpoints because
// strict JSON Schema enforcement is not uniformly supported; prompt compilation appends
// the desired schema inline as a fallback.
func (p *CompatProvider) SupportsStructuredOutput() bool {
	return false
}

// Complete executes a completion request against the OpenAI-compatible chat completions endpoint.
func (p *CompatProvider) Complete(ctx context.Context, req core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.DefaultModel()
	}

	systemPrompt := req.SystemPrompt

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

	var respFmt *responseFormat
	if req.ResponseSchema != nil && !containsSchemaDirective(systemPrompt) {
		schemaBytes, err := json.Marshal(req.ResponseSchema)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize schema: %w", err)
		}
		systemPrompt = fmt.Sprintf("%s\n\n<json_output_schema>\n%s\n</json_output_schema>",
			req.SystemPrompt, string(schemaBytes))
		respFmt = &responseFormat{Type: "json_object"}
	}

	payload := requestBody{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: req.UserPrompt},
		},
		ResponseFormat: respFmt,
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

	bodyBytes, err := readBoundedBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	rawBody := string(bodyBytes)
	if err := checkStatusError("compat", resp.StatusCode, bodyBytes); err != nil {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, err
	}

	type compatResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	var compatResp compatResponse
	if err := json.Unmarshal(bodyBytes, &compatResp); err != nil {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, fmt.Errorf("%w: malformed compat response: %v", ErrInvalidResponse, err)
	}

	if len(compatResp.Choices) == 0 {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, fmt.Errorf("%w: no choices returned by compat endpoint", ErrInvalidResponse)
	}

	usage := core.TokenUsage{
		PromptTokens:     compatResp.Usage.PromptTokens,
		CompletionTokens: compatResp.Usage.CompletionTokens,
		TotalTokens:      compatResp.Usage.TotalTokens,
	}
	if usage.TotalTokens == 0 && (usage.PromptTokens > 0 || usage.CompletionTokens > 0) {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	return &core.LLMCompletionResponse{
		Text:       strings.TrimSpace(compatResp.Choices[0].Message.Content),
		RawBody:    rawBody,
		StatusCode: resp.StatusCode,
		Usage:      usage,
	}, nil
}

// buildCompatURL normalizes baseURL and ensures the /chat/completions path suffix is present.
func buildCompatURL(baseURL string) string {
	cleaned := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(cleaned, "/chat/completions") {
		return cleaned
	}
	return cleaned + "/chat/completions"
}

func containsSchemaDirective(systemPrompt string) bool {
	return strings.Contains(systemPrompt, "<response_schema>") || strings.Contains(systemPrompt, "<json_output_schema>")
}
