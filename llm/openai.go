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
	defaultOpenAIBaseURL    = "https://api.openai.com"
	defaultOpenAISchemaName = "structured_response"
)

// OpenAIProvider implements core.LLMClient for OpenAI models via the Responses API (/v1/responses).
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
	if p != nil && strings.TrimSpace(p.defaultModel) != "" {
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

// SupportsStructuredOutput reports whether the target OpenAI model supports strict JSON Schema
// structured outputs via the /v1/responses text.format parameter.
func (p *OpenAIProvider) SupportsStructuredOutput(model string) bool {
	resolved := strings.TrimSpace(model)
	if resolved == "" {
		resolved = p.DefaultModel()
	}
	return OpenAIModelSupportsStructuredOutput(resolved)
}

// OpenAIModelSupportsStructuredOutput returns false for legacy OpenAI snapshots that predate
// strict JSON Schema structured output support (such as gpt-3.5-*, gpt-4-turbo*, gpt-4o-2024-05-13,
// o1-mini*, o1-preview*) and true for supported modern models.
func OpenAIModelSupportsStructuredOutput(model string) bool {
	norm := strings.ToLower(strings.TrimSpace(model))
	if norm == "" {
		return true
	}
	switch {
	case strings.HasPrefix(norm, "gpt-3.5"),
		strings.HasPrefix(norm, "gpt-4-turbo"),
		norm == "gpt-4",
		strings.HasPrefix(norm, "gpt-4-0"),
		strings.HasPrefix(norm, "gpt-4-1106"),
		strings.HasPrefix(norm, "gpt-4-32k"),
		strings.HasPrefix(norm, "gpt-4-vision"),
		norm == "gpt-4o-2024-05-13",
		strings.HasPrefix(norm, "o1-mini"),
		strings.HasPrefix(norm, "o1-preview"):
		return false
	default:
		return true
	}
}

func openAIModelSupportsReasoning(model string) bool {
	norm := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(norm, "o1-mini"), strings.HasPrefix(norm, "o1-preview"):
		return false
	case norm == "o1" || strings.HasPrefix(norm, "o1-"),
		norm == "o3" || strings.HasPrefix(norm, "o3-"),
		norm == "o4" || strings.HasPrefix(norm, "o4-"),
		norm == "gpt-5" || strings.HasPrefix(norm, "gpt-5-") || strings.HasPrefix(norm, "gpt-5."):
		return true
	default:
		return false
	}
}

// Complete executes a completion request against the OpenAI /v1/responses API.
func (p *OpenAIProvider) Complete(ctx context.Context, req core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
	if p == nil || strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("%w: openai api key is required", ErrUnauthorized)
	}

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

	instructions := req.SystemPrompt
	payload := requestBody{
		Model: model,
		Input: req.UserPrompt,
	}

	if effort := normalizeReasoningEffort(req.ReasoningLevel); effort != "" && openAIModelSupportsReasoning(model) {
		payload.Reasoning = &reasoningConfig{Effort: effort}
	}

	if req.ResponseSchema != nil {
		if p.SupportsStructuredOutput(model) {
			payload.Text = &textConfig{
				Format: &textFormat{
					Type:   "json_schema",
					Name:   sanitizeOpenAISchemaName(req.SchemaName),
					Strict: true,
					Schema: SanitizeOpenAISchema(req.ResponseSchema),
				},
			}
		} else {
			var err error
			instructions, err = appendFallbackSchemaIfNeeded(instructions, req.ResponseSchema)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal fallback openai schema: %w", err)
			}
		}
	}
	payload.Instructions = instructions

	reqBodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal openai request: %w", err)
	}

	url := buildOpenAIResponsesURL(p.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := resolveHTTPClient(p.httpClient).Do(httpReq)
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

	type outputContentItem struct {
		Type    string `json:"type"`
		Text    string `json:"text,omitempty"`
		Refusal string `json:"refusal,omitempty"`
	}

	type outputItem struct {
		Type    string              `json:"type"`
		Role    string              `json:"role,omitempty"`
		Content []outputContentItem `json:"content,omitempty"`
	}

	type openAIResponsesPayload struct {
		Status            string `json:"status"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details,omitempty"`
		Output     []outputItem `json:"output"`
		OutputText string       `json:"output_text,omitempty"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}

	var openAIResp openAIResponsesPayload
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

	if strings.EqualFold(openAIResp.Status, "incomplete") {
		reason := "incomplete"
		if openAIResp.IncompleteDetails != nil && strings.TrimSpace(openAIResp.IncompleteDetails.Reason) != "" {
			reason = openAIResp.IncompleteDetails.Reason
		}
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
			Usage:      usage,
		}, fmt.Errorf("%w: openai response incomplete (%s)", ErrInvalidResponse, reason)
	}

	var textParts []string
	for _, item := range openAIResp.Output {
		for _, content := range item.Content {
			if content.Type == "refusal" {
				refusalMsg := strings.TrimSpace(content.Refusal)
				if refusalMsg == "" {
					refusalMsg = "model refused structured completion request"
				}
				return &core.LLMCompletionResponse{
					RawBody:    rawBody,
					StatusCode: resp.StatusCode,
					Usage:      usage,
				}, fmt.Errorf("%w: openai refusal: %s", ErrInvalidResponse, refusalMsg)
			}
			if (content.Type == "output_text" || content.Type == "text") && strings.TrimSpace(content.Text) != "" {
				textParts = append(textParts, strings.TrimSpace(content.Text))
			}
		}
	}

	finalText := strings.TrimSpace(strings.Join(textParts, "\n"))
	if finalText == "" {
		finalText = strings.TrimSpace(openAIResp.OutputText)
	}
	if finalText == "" {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
			Usage:      usage,
		}, fmt.Errorf("%w: no output text returned by openai", ErrInvalidResponse)
	}

	return &core.LLMCompletionResponse{
		Text:       finalText,
		RawBody:    rawBody,
		StatusCode: resp.StatusCode,
		Usage:      usage,
	}, nil
}

func buildOpenAIResponsesURL(baseURL string) string {
	cleaned := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if cleaned == "" {
		cleaned = defaultOpenAIBaseURL
	}
	switch {
	case strings.HasSuffix(cleaned, "/v1/responses"):
		return cleaned
	case strings.HasSuffix(cleaned, "/v1"):
		return cleaned + "/responses"
	default:
		return cleaned + "/v1/responses"
	}
}

func normalizeReasoningEffort(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(level))
	default:
		return ""
	}
}

func sanitizeOpenAISchemaName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return defaultOpenAISchemaName
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return defaultOpenAISchemaName
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
