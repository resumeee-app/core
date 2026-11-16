// Package openai implements llm.Client for OpenAI models via the Responses API (/v1/responses).
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/resumeee-app/core"
	"github.com/resumeee-app/core/llm"
	"github.com/resumeee-app/core/llm/internal/httpx"
)

// OpenAI Responses API endpoint and default structured schema identifier.
const (
	// defaultBaseURL is the upstream OpenAI API base endpoint.
	defaultBaseURL = "https://api.openai.com"

	// defaultSchemaName supplies the strict format identifier when Request.SchemaName is blank.
	defaultSchemaName = "structured_response"
)

// Option configures an OpenAI Client.
type Option func(*Client)

// WithBaseURL overrides the upstream OpenAI API base URL.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		c.settings.SetBaseURL(baseURL)
	}
}

// WithHTTPClient overrides the HTTP client used for OpenAI API requests.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		c.settings.SetHTTPClient(client)
	}
}

// WithDefaultModel overrides the default OpenAI model identifier when Request.Model is blank.
func WithDefaultModel(model string) Option {
	return func(c *Client) {
		c.settings.SetDefaultModel(model)
	}
}

// WithTimeout overrides the request timeout applied to OpenAI API requests.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		c.settings.SetTimeout(timeout)
	}
}

// Client implements llm.Client against OpenAI's /v1/responses endpoint.
type Client struct {
	apiKey   string
	settings httpx.Settings
}

// New creates an OpenAI Client configured with apiKey and optional settings.
func New(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:   apiKey,
		settings: httpx.DefaultSettings(defaultBaseURL, llm.DefaultModelForProvider(llm.ProviderOpenAI)),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	c.settings.Finalize()
	return c
}

// Name returns the canonical OpenAI provider identifier.
func (c *Client) Name() string {
	return llm.ProviderOpenAI
}

// DefaultModel returns the configured default OpenAI model identifier.
func (c *Client) DefaultModel() string {
	if c != nil && strings.TrimSpace(c.settings.DefaultModel) != "" {
		return c.settings.DefaultModel
	}
	return llm.DefaultModelForProvider(llm.ProviderOpenAI)
}

// Models returns the default model catalog entry exposed by this OpenAI Client.
func (c *Client) Models() []core.ModelOption {
	def := c.DefaultModel()
	return []core.ModelOption{
		{
			Provider:    llm.ProviderOpenAI,
			Model:       def,
			DisplayName: fmt.Sprintf("OpenAI — %s", def),
			IsDefault:   true,
		},
	}
}

// SupportsStructuredOutput reports whether model supports strict JSON Schema text formatting.
func (c *Client) SupportsStructuredOutput(model string) bool {
	resolved := strings.TrimSpace(model)
	if resolved == "" {
		resolved = c.DefaultModel()
	}
	return ModelSupportsStructuredOutput(resolved)
}

// ModelSupportsStructuredOutput returns false for legacy OpenAI snapshots that predate
// strict JSON Schema structured output support and true for modern models.
func ModelSupportsStructuredOutput(model string) bool {
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

// modelSupportsReasoning reports whether model accepts OpenAI's reasoning.effort parameter.
func modelSupportsReasoning(model string) bool {
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

// reasoningConfig encodes OpenAI reasoning effort configuration.
type reasoningConfig struct {
	Effort string `json:"effort"`
}

// textFormat encodes the OpenAI Responses API structured JSON schema format payload.
type textFormat struct {
	Type   string      `json:"type"`
	Name   string      `json:"name"`
	Strict bool        `json:"strict"`
	Schema *llm.Schema `json:"schema"`
}

// textConfig wraps the optional format directive in a Responses API request.
type textConfig struct {
	Format *textFormat `json:"format,omitempty"`
}

// requestBody represents the OpenAI /v1/responses HTTP request payload.
type requestBody struct {
	Model        string           `json:"model"`
	Instructions string           `json:"instructions,omitempty"`
	Input        string           `json:"input"`
	Reasoning    *reasoningConfig `json:"reasoning,omitempty"`
	Text         *textConfig      `json:"text,omitempty"`
}

// responsesPayload decodes the OpenAI /v1/responses HTTP response payload.
type responsesPayload struct {
	Status            string `json:"status"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details,omitempty"`
	Output []struct {
		Type    string `json:"type"`
		Role    string `json:"role,omitempty"`
		Content []struct {
			Type    string `json:"type"`
			Text    string `json:"text,omitempty"`
			Refusal string `json:"refusal,omitempty"`
		} `json:"content,omitempty"`
	} `json:"output"`
	OutputText string `json:"output_text,omitempty"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// Complete executes a completion request against the OpenAI /v1/responses endpoint.
func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if c == nil || strings.TrimSpace(c.apiKey) == "" {
		return nil, fmt.Errorf("%w: openai api key is required", llm.ErrUnauthorized)
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = c.DefaultModel()
	}

	payload, err := c.buildRequestPayload(model, req)
	if err != nil {
		return nil, err
	}

	headers := map[string]string{"Authorization": "Bearer " + c.apiKey}
	start := time.Now()
	bodyBytes, statusCode, err := httpx.PostJSON(ctx, c.settings.HTTPClient, buildResponsesURL(c.settings.BaseURL), payload, headers, llm.ProviderOpenAI)
	latency := time.Since(start)
	if err != nil {
		resp := &llm.Response{
			RawBody:    string(bodyBytes),
			StatusCode: statusCode,
		}
		return resp.PopulateTelemetry(c.Name(), model, latency), err
	}

	resp, parseErr := parseResponsesPayload(bodyBytes, statusCode)
	return resp.PopulateTelemetry(c.Name(), model, latency), parseErr
}

// buildRequestPayload translates req into an OpenAI Responses payload with capability-aware schema handling.
func (c *Client) buildRequestPayload(model string, req llm.Request) (requestBody, error) {
	instructions := req.SystemPrompt
	payload := requestBody{
		Model: model,
		Input: req.UserPrompt,
	}

	if effort := normalizeReasoningEffort(req.ReasoningLevel); effort != "" && modelSupportsReasoning(model) {
		payload.Reasoning = &reasoningConfig{Effort: effort}
	}

	if req.ResponseSchema != nil {
		if c.SupportsStructuredOutput(model) {
			payload.Text = &textConfig{
				Format: &textFormat{
					Type:   "json_schema",
					Name:   sanitizeSchemaName(req.SchemaName),
					Strict: true,
					Schema: req.ResponseSchema.Strict(),
				},
			}
		} else {
			var err error
			instructions, err = llm.AppendFallbackSchema(instructions, req.ResponseSchema)
			if err != nil {
				return requestBody{}, fmt.Errorf("failed to marshal fallback openai schema: %w", err)
			}
		}
	}
	payload.Instructions = instructions
	return payload, nil
}

// parseResponsesPayload decodes bodyBytes from /v1/responses and enforces refusal/incomplete guards.
func parseResponsesPayload(bodyBytes []byte, statusCode int) (*llm.Response, error) {
	rawBody := string(bodyBytes)
	var parsed responsesPayload
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		return &llm.Response{RawBody: rawBody, StatusCode: statusCode},
			fmt.Errorf("%w: malformed openai response: %v", llm.ErrInvalidResponse, err)
	}

	usage := extractResponsesUsage(parsed)
	if strings.EqualFold(parsed.Status, "incomplete") {
		reason := "incomplete"
		if parsed.IncompleteDetails != nil && strings.TrimSpace(parsed.IncompleteDetails.Reason) != "" {
			reason = parsed.IncompleteDetails.Reason
		}
		return &llm.Response{RawBody: rawBody, StatusCode: statusCode, Usage: usage},
			fmt.Errorf("%w: openai response incomplete (%s)", llm.ErrInvalidResponse, reason)
	}

	finalText, err := collectOutputText(parsed)
	if err != nil {
		return &llm.Response{RawBody: rawBody, StatusCode: statusCode, Usage: usage}, err
	}

	return &llm.Response{
		Text:       finalText,
		RawBody:    rawBody,
		StatusCode: statusCode,
		Usage:      usage,
	}, nil
}

// extractResponsesUsage converts OpenAI token counters into normalized core.TokenUsage.
func extractResponsesUsage(parsed responsesPayload) core.TokenUsage {
	return core.TokenUsage{
		PromptTokens:     parsed.Usage.InputTokens,
		CompletionTokens: parsed.Usage.OutputTokens,
		TotalTokens:      parsed.Usage.TotalTokens,
	}.Normalize()
}

// collectOutputText extracts assistant output segments and fails closed on model refusals or empty text.
func collectOutputText(parsed responsesPayload) (string, error) {
	var textParts []string
	for _, item := range parsed.Output {
		for _, content := range item.Content {
			if content.Type == "refusal" {
				refusalMsg := strings.TrimSpace(content.Refusal)
				if refusalMsg == "" {
					refusalMsg = "model refused structured completion request"
				}
				return "", fmt.Errorf("%w: openai refusal: %s", llm.ErrInvalidResponse, refusalMsg)
			}
			if (content.Type == "output_text" || content.Type == "text") && strings.TrimSpace(content.Text) != "" {
				textParts = append(textParts, strings.TrimSpace(content.Text))
			}
		}
	}

	finalText := strings.TrimSpace(strings.Join(textParts, "\n"))
	if finalText == "" {
		finalText = strings.TrimSpace(parsed.OutputText)
	}
	if finalText == "" {
		return "", fmt.Errorf("%w: no output text returned by openai", llm.ErrInvalidResponse)
	}
	return finalText, nil
}

// buildResponsesURL normalizes baseURL and appends the /v1/responses endpoint path when absent.
func buildResponsesURL(baseURL string) string {
	cleaned := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if cleaned == "" {
		cleaned = defaultBaseURL
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

// normalizeReasoningEffort normalizes level to one of OpenAI's supported reasoning effort values.
func normalizeReasoningEffort(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(level))
	default:
		return ""
	}
}

// sanitizeSchemaName coerces name into a <=64 character OpenAI identifier matching [a-zA-Z0-9_-].
func sanitizeSchemaName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return defaultSchemaName
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
		return defaultSchemaName
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// _ enforces at compile time that Client implements llm.Client.
var _ llm.Client = (*Client)(nil)
