// Package openaicompat implements llm.Client for generic OpenAI-compatible
// /chat/completions HTTP endpoints such as vLLM, llama.cpp, LocalAI, and LM Studio.
package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/llm"
	"github.com/resumeee-app/resumeee-core/llm/internal/httpx"
)

// Option configures an OpenAI-compatible Client.
type Option func(*Client)

// WithBaseURL overrides the endpoint base URL.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		c.settings.SetBaseURL(baseURL)
	}
}

// WithHTTPClient overrides the HTTP client used for API requests.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		c.settings.SetHTTPClient(client)
	}
}

// WithDefaultModel overrides the default model identifier when Request.Model is blank.
func WithDefaultModel(model string) Option {
	return func(c *Client) {
		c.settings.SetDefaultModel(model)
	}
}

// WithTimeout overrides the request timeout applied to API requests.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		c.settings.SetTimeout(timeout)
	}
}

// Client implements llm.Client against an OpenAI-compatible chat completions endpoint.
type Client struct {
	apiKey   string
	settings httpx.Settings
}

// New creates an OpenAI-compatible Client targeting baseURL with an optional Bearer apiKey.
func New(baseURL, apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:   apiKey,
		settings: httpx.DefaultSettings(baseURL, llm.DefaultModelForProvider(llm.ProviderCompat)),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	c.settings.Finalize()
	return c
}

// Name returns the canonical compat provider identifier.
func (c *Client) Name() string {
	return llm.ProviderCompat
}

// DefaultModel returns the configured default model identifier for this endpoint.
func (c *Client) DefaultModel() string {
	if c != nil && strings.TrimSpace(c.settings.DefaultModel) != "" {
		return c.settings.DefaultModel
	}
	return llm.DefaultModelForProvider(llm.ProviderCompat)
}

// Models returns the default model catalog entry exposed by this OpenAI-compatible Client.
func (c *Client) Models() []core.ModelOption {
	def := c.DefaultModel()
	return []core.ModelOption{
		{
			Provider:    llm.ProviderCompat,
			Model:       def,
			DisplayName: fmt.Sprintf("OpenAI Compatible — %s", def),
			IsDefault:   true,
		},
	}
}

// SupportsStructuredOutput reports false because generic compatible servers cannot guarantee
// strict JSON Schema enforcement at decode time.
func (c *Client) SupportsStructuredOutput(_ string) bool {
	return false
}

// chatMessage encodes a single OpenAI chat completions message turn.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// responseFormat selects JSON object mode on compatible servers.
type responseFormat struct {
	Type string `json:"type"`
}

// requestBody represents an OpenAI-compatible /chat/completions HTTP request payload.
type requestBody struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

// compatResponse decodes an OpenAI-compatible /chat/completions HTTP response payload.
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

// Complete executes a chat completion request against an OpenAI-compatible HTTP endpoint.
func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: compat client is not initialized", llm.ErrNoProviders)
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = c.DefaultModel()
	}

	payload, err := buildRequestPayload(model, req)
	if err != nil {
		return nil, err
	}

	var headers map[string]string
	if strings.TrimSpace(c.apiKey) != "" {
		headers = map[string]string{"Authorization": "Bearer " + c.apiKey}
	}

	bodyBytes, statusCode, err := httpx.PostJSON(ctx, c.settings.HTTPClient, buildEndpointURL(c.settings.BaseURL), payload, headers, llm.ProviderCompat)
	if err != nil {
		if bodyBytes == nil && statusCode == 0 {
			return nil, err
		}
		return &llm.Response{RawBody: string(bodyBytes), StatusCode: statusCode}, err
	}

	return parseCompatResponse(bodyBytes, statusCode)
}

// buildRequestPayload translates req into a chat completions payload and injects inline schema
// instructions when Request.ResponseSchema is supplied without an existing directive.
func buildRequestPayload(model string, req llm.Request) (requestBody, error) {
	systemPrompt := req.SystemPrompt
	var respFmt *responseFormat

	if req.ResponseSchema != nil {
		if !llm.ContainsResponseSchemaDirective(systemPrompt) {
			schemaBytes, err := json.Marshal(req.ResponseSchema)
			if err != nil {
				return requestBody{}, fmt.Errorf("failed to serialize schema: %w", err)
			}
			systemPrompt = fmt.Sprintf("%s\n\n<json_output_schema>\n%s\n</json_output_schema>",
				req.SystemPrompt, string(schemaBytes))
		}
		respFmt = &responseFormat{Type: "json_object"}
	}

	return requestBody{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: req.UserPrompt},
		},
		ResponseFormat: respFmt,
	}, nil
}

// parseCompatResponse decodes bodyBytes and enforces choice availability and usage backfill.
func parseCompatResponse(bodyBytes []byte, statusCode int) (*llm.Response, error) {
	rawBody := string(bodyBytes)
	var parsed compatResponse
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		return &llm.Response{RawBody: rawBody, StatusCode: statusCode},
			fmt.Errorf("%w: malformed compat response: %v", llm.ErrInvalidResponse, err)
	}
	if len(parsed.Choices) == 0 {
		return &llm.Response{RawBody: rawBody, StatusCode: statusCode},
			fmt.Errorf("%w: no choices returned by compat endpoint", llm.ErrInvalidResponse)
	}

	usage := core.TokenUsage{
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
		TotalTokens:      parsed.Usage.TotalTokens,
	}
	if usage.TotalTokens == 0 && (usage.PromptTokens > 0 || usage.CompletionTokens > 0) {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	return &llm.Response{
		Text:       strings.TrimSpace(parsed.Choices[0].Message.Content),
		RawBody:    rawBody,
		StatusCode: statusCode,
		Usage:      usage,
	}, nil
}

// buildEndpointURL normalizes baseURL and ensures the /chat/completions path suffix is present.
func buildEndpointURL(baseURL string) string {
	cleaned := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(cleaned, "/chat/completions") {
		return cleaned
	}
	return cleaned + "/chat/completions"
}

// _ enforces at compile time that Client implements llm.Client.
var _ llm.Client = (*Client)(nil)
