// Package ollama implements llm.Client for native Ollama servers via POST /api/chat.
package ollama

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

// defaultBaseURL is the local Ollama daemon URL used when no explicit base URL is supplied.
const defaultBaseURL = "http://localhost:11434"

// Option configures an Ollama Client.
type Option func(*Client)

// WithBaseURL overrides the Ollama server base URL.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		c.settings.SetBaseURL(baseURL)
	}
}

// WithHTTPClient overrides the HTTP client used for Ollama API requests.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		c.settings.SetHTTPClient(client)
	}
}

// WithDefaultModel overrides the default Ollama model identifier when Request.Model is blank.
func WithDefaultModel(model string) Option {
	return func(c *Client) {
		c.settings.SetDefaultModel(model)
	}
}

// WithTimeout overrides the request timeout applied to Ollama API requests.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		c.settings.SetTimeout(timeout)
	}
}

// Client implements llm.Client against Ollama's native POST /api/chat endpoint.
type Client struct {
	apiKey   string
	settings httpx.Settings
}

// New creates an Ollama Client targeting baseURL (defaulting to http://localhost:11434)
// with an optional Bearer apiKey and additional configuration options.
func New(baseURL, apiKey string, opts ...Option) *Client {
	resolvedBase := strings.TrimSpace(baseURL)
	if resolvedBase == "" {
		resolvedBase = defaultBaseURL
	}
	c := &Client{
		apiKey:   apiKey,
		settings: httpx.DefaultSettings(resolvedBase, llm.DefaultModelForProvider(llm.ProviderOllama)),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	c.settings.Finalize()
	return c
}

// Name returns the canonical Ollama provider identifier.
func (c *Client) Name() string {
	return llm.ProviderOllama
}

// DefaultModel returns the configured default Ollama model identifier.
func (c *Client) DefaultModel() string {
	if c != nil && strings.TrimSpace(c.settings.DefaultModel) != "" {
		return c.settings.DefaultModel
	}
	return llm.DefaultModelForProvider(llm.ProviderOllama)
}

// Models returns the default model catalog entry exposed by this Ollama Client.
func (c *Client) Models() []core.ModelOption {
	def := c.DefaultModel()
	return []core.ModelOption{
		{
			Provider:    llm.ProviderOllama,
			Model:       def,
			DisplayName: fmt.Sprintf("Ollama — %s", def),
			IsDefault:   true,
		},
	}
}

// SupportsStructuredOutput reports that Ollama /api/chat supports native JSON Schema formatting.
func (c *Client) SupportsStructuredOutput(_ string) bool {
	return true
}

// ollamaMessage encodes a role/content chat turn for /api/chat.
type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// requestBody represents the Ollama /api/chat HTTP request payload.
type requestBody struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Format   *llm.Schema     `json:"format,omitempty"`
}

// chatResponse decodes the Ollama /api/chat HTTP response payload.
type chatResponse struct {
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	PromptEvalCount int `json:"prompt_eval_count"`
	EvalCount       int `json:"eval_count"`
}

// Complete executes a non-streaming completion request against Ollama's /api/chat endpoint.
func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: ollama client is not initialized", llm.ErrNoProviders)
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = c.DefaultModel()
	}

	messages := make([]ollamaMessage, 0, 2)
	if strings.TrimSpace(req.SystemPrompt) != "" {
		messages = append(messages, ollamaMessage{Role: "system", Content: req.SystemPrompt})
	}
	messages = append(messages, ollamaMessage{Role: "user", Content: req.UserPrompt})

	payload := requestBody{
		Model:    model,
		Messages: messages,
		Stream:   false,
	}
	if req.ResponseSchema != nil {
		payload.Format = req.ResponseSchema.Clone()
	}

	var headers map[string]string
	if strings.TrimSpace(c.apiKey) != "" {
		headers = map[string]string{"Authorization": "Bearer " + c.apiKey}
	}

	start := time.Now()
	bodyBytes, statusCode, err := httpx.PostJSON(ctx, c.settings.HTTPClient, buildChatURL(c.settings.BaseURL), payload, headers, llm.ProviderOllama)
	latency := time.Since(start)
	if err != nil {
		return (&llm.Response{
			RawBody:    string(bodyBytes),
			StatusCode: statusCode,
		}).PopulateTelemetry(c.Name(), model, latency), err
	}

	resp, err := parseChatResponse(bodyBytes, statusCode)
	return resp.PopulateTelemetry(c.Name(), model, latency), err
}

// parseChatResponse unmarshals bodyBytes from /api/chat and checks for non-empty content.
func parseChatResponse(bodyBytes []byte, statusCode int) (*llm.Response, error) {
	rawBody := string(bodyBytes)
	var parsed chatResponse
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		return &llm.Response{RawBody: rawBody, StatusCode: statusCode},
			fmt.Errorf("%w: malformed ollama response: %v", llm.ErrInvalidResponse, err)
	}

	usage := (core.TokenUsage{
		PromptTokens:     parsed.PromptEvalCount,
		CompletionTokens: parsed.EvalCount,
	}).Normalize()

	content := strings.TrimSpace(parsed.Message.Content)
	if content == "" {
		return &llm.Response{RawBody: rawBody, StatusCode: statusCode, Usage: usage},
			fmt.Errorf("%w: empty message content returned by ollama", llm.ErrInvalidResponse)
	}

	return &llm.Response{
		Text:       content,
		RawBody:    rawBody,
		StatusCode: statusCode,
		Usage:      usage,
	}, nil
}

// buildChatURL normalizes baseURL and appends the native /api/chat suffix when missing.
func buildChatURL(baseURL string) string {
	cleaned := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if cleaned == "" {
		cleaned = defaultBaseURL
	}
	switch {
	case strings.HasSuffix(cleaned, "/api/chat"):
		return cleaned
	case strings.HasSuffix(cleaned, "/api"):
		return cleaned + "/chat"
	default:
		return cleaned + "/api/chat"
	}
}

// _ enforces at compile time that Client implements llm.Client.
var _ llm.Client = (*Client)(nil)
