// Package anthropic implements llm.Client for Anthropic Claude models via the Messages API.
package anthropic

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

// Anthropic Messages API defaults and Claude version parsing bounds.
const (
	// defaultBaseURL is the upstream Anthropic API base endpoint.
	defaultBaseURL = "https://api.anthropic.com"

	// apiVersion specifies the required anthropic-version request header value.
	apiVersion = "2023-06-01"

	// defaultMaxTokens caps completion output tokens per Messages API call.
	defaultMaxTokens = 8192

	// maxVersionSegment bounds parsed major/minor integers against integer overflow.
	maxVersionSegment = 100_000_000
)

// Option configures an Anthropic Client.
type Option func(*Client)

// WithBaseURL overrides the upstream Anthropic API base URL.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		c.settings.SetBaseURL(baseURL)
	}
}

// WithHTTPClient overrides the HTTP client used for Anthropic API requests.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		c.settings.SetHTTPClient(client)
	}
}

// WithDefaultModel overrides the default Claude model identifier used when Request.Model is blank.
func WithDefaultModel(model string) Option {
	return func(c *Client) {
		c.settings.SetDefaultModel(model)
	}
}

// WithTimeout overrides the request timeout applied to Anthropic API requests.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		c.settings.SetTimeout(timeout)
	}
}

// Client implements llm.Client against Anthropic's /v1/messages endpoint.
type Client struct {
	apiKey   string
	settings httpx.Settings
}

// New creates an Anthropic Client configured with apiKey and optional settings.
func New(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:   apiKey,
		settings: httpx.DefaultSettings(defaultBaseURL, llm.DefaultModelForProvider(llm.ProviderAnthropic)),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	c.settings.Finalize()
	return c
}

// Name returns the canonical Anthropic provider identifier.
func (c *Client) Name() string {
	return llm.ProviderAnthropic
}

// DefaultModel returns the configured default Claude model identifier.
func (c *Client) DefaultModel() string {
	if c != nil && strings.TrimSpace(c.settings.DefaultModel) != "" {
		return c.settings.DefaultModel
	}
	return llm.DefaultModelForProvider(llm.ProviderAnthropic)
}

// Models returns the default model catalog entry exposed by this Anthropic Client.
func (c *Client) Models() []core.ModelOption {
	def := c.DefaultModel()
	return []core.ModelOption{
		{
			Provider:    llm.ProviderAnthropic,
			Model:       def,
			DisplayName: fmt.Sprintf("Anthropic — %s", def),
			IsDefault:   true,
		},
	}
}

// SupportsStructuredOutput reports whether model supports Anthropic's native output_config.format parameter.
func (c *Client) SupportsStructuredOutput(model string) bool {
	resolved := strings.TrimSpace(model)
	if resolved == "" {
		resolved = c.DefaultModel()
	}
	return ModelSupportsStructuredOutput(resolved)
}

// ModelSupportsStructuredOutput returns true for Claude 4.5+ models and Claude Opus 4.1+,
// and false for legacy Claude versions lacking output_config.format constrained decoding.
func ModelSupportsStructuredOutput(model string) bool {
	norm := strings.ToLower(strings.TrimSpace(model))
	if !strings.HasPrefix(norm, "claude-") {
		return false
	}

	// Reject Claude 1.x, 2.x, and 3.x families regardless of variant suffix.
	if strings.HasPrefix(norm, "claude-1") || strings.HasPrefix(norm, "claude-2") || strings.HasPrefix(norm, "claude-3") {
		return false
	}

	if hasClaudeVersionAtLeast(norm, "claude-opus-", 4, 1) {
		return true
	}

	for _, prefix := range []string{"claude-sonnet-", "claude-haiku-", "claude-opus-", "claude-"} {
		if hasClaudeVersionAtLeast(norm, prefix, 4, 5) {
			return true
		}
	}
	return false
}

// hasClaudeVersionAtLeast parses a prefixed Claude model string and checks major/minor bounds.
func hasClaudeVersionAtLeast(normModel, prefix string, minMajor, minMinor int) bool {
	if !strings.HasPrefix(normModel, prefix) {
		return false
	}
	rest := strings.TrimPrefix(normModel, prefix)
	if len(rest) == 0 || rest[0] < '0' || rest[0] > '9' {
		return false
	}

	major, restAfterMajor, ok := parseLeadingPositiveInt(rest)
	if !ok {
		return false
	}
	if major > minMajor {
		return true
	}
	if major < minMajor {
		return false
	}

	if len(restAfterMajor) == 0 {
		return minMinor == 0
	}
	sep := restAfterMajor[0]
	if sep != '-' && sep != '.' {
		return false
	}
	minor, _, ok := parseLeadingPositiveInt(restAfterMajor[1:])
	if !ok {
		return false
	}
	// An 8-digit date snapshot immediately after major (e.g. claude-sonnet-4-20250514) means minor 0.
	if minor >= 20200000 {
		minor = 0
	}
	return minor >= minMinor
}

// parseLeadingPositiveInt parses a saturated non-negative leading integer from s.
func parseLeadingPositiveInt(s string) (int, string, bool) {
	i := 0
	val := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		if val < maxVersionSegment {
			val = val*10 + int(s[i]-'0')
			if val > maxVersionSegment {
				val = maxVersionSegment
			}
		}
		i++
	}
	if i == 0 {
		return 0, s, false
	}
	return val, s[i:], true
}

// anthropicMessage encodes a single conversational message for the Messages API.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// outputFormat encodes the native JSON schema configuration under output_config.
type outputFormat struct {
	Type   string      `json:"type"`
	Schema *llm.Schema `json:"schema"`
}

// outputConfig wraps structured output formatting parameters in an Anthropic request.
type outputConfig struct {
	Format *outputFormat `json:"format,omitempty"`
}

// requestBody represents the Anthropic /v1/messages HTTP request payload.
type requestBody struct {
	Model        string             `json:"model"`
	MaxTokens    int                `json:"max_tokens"`
	System       string             `json:"system,omitempty"`
	Messages     []anthropicMessage `json:"messages"`
	OutputConfig *outputConfig      `json:"output_config,omitempty"`
}

// messagesResponse decodes the Anthropic /v1/messages HTTP response payload.
type messagesResponse struct {
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Complete executes a completion request against Anthropic's /v1/messages endpoint.
func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if c == nil || strings.TrimSpace(c.apiKey) == "" {
		return nil, fmt.Errorf("%w: anthropic api key is required", llm.ErrUnauthorized)
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = c.DefaultModel()
	}

	payload, err := c.buildRequestPayload(model, req)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/v1/messages", strings.TrimRight(c.settings.BaseURL, "/"))
	headers := map[string]string{
		"x-api-key":         c.apiKey,
		"anthropic-version": apiVersion,
	}
	start := time.Now()
	bodyBytes, statusCode, err := httpx.PostJSON(ctx, c.settings.HTTPClient, url, payload, headers, llm.ProviderAnthropic)
	latency := time.Since(start)
	if err != nil {
		return (&llm.Response{
			RawBody:    string(bodyBytes),
			StatusCode: statusCode,
		}).PopulateTelemetry(c.Name(), model, latency), err
	}

	resp, err := parseMessagesResponse(bodyBytes, statusCode, payload.OutputConfig != nil)
	return resp.PopulateTelemetry(c.Name(), model, latency), err
}

// buildRequestPayload constructs an Anthropic request payload with capability-gated schema attachment.
func (c *Client) buildRequestPayload(model string, req llm.Request) (requestBody, error) {
	systemPrompt := req.SystemPrompt
	var outCfg *outputConfig

	if req.ResponseSchema != nil {
		if c.SupportsStructuredOutput(model) {
			outCfg = &outputConfig{
				Format: &outputFormat{
					Type:   "json_schema",
					Schema: req.ResponseSchema.Strict(),
				},
			}
		} else {
			var err error
			systemPrompt, err = llm.AppendFallbackSchema(systemPrompt, req.ResponseSchema)
			if err != nil {
				return requestBody{}, fmt.Errorf("failed to marshal fallback anthropic schema: %w", err)
			}
		}
	}

	return requestBody{
		Model:        model,
		MaxTokens:    defaultMaxTokens,
		System:       systemPrompt,
		Messages:     []anthropicMessage{{Role: "user", Content: req.UserPrompt}},
		OutputConfig: outCfg,
	}, nil
}

// parseMessagesResponse decodes bodyBytes and checks for refusals, truncation, and empty blocks.
func parseMessagesResponse(bodyBytes []byte, statusCode int, structured bool) (*llm.Response, error) {
	rawBody := string(bodyBytes)
	var parsed messagesResponse
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		return &llm.Response{RawBody: rawBody, StatusCode: statusCode},
			fmt.Errorf("%w: malformed anthropic response: %v", llm.ErrInvalidResponse, err)
	}

	usage := (core.TokenUsage{
		PromptTokens:     parsed.Usage.InputTokens,
		CompletionTokens: parsed.Usage.OutputTokens,
	}).Normalize()
	if parsed.StopReason == "refusal" {
		return &llm.Response{RawBody: rawBody, StatusCode: statusCode, Usage: usage},
			fmt.Errorf("%w: anthropic model refused request", llm.ErrInvalidResponse)
	}
	if parsed.StopReason == "max_tokens" && structured {
		return &llm.Response{RawBody: rawBody, StatusCode: statusCode, Usage: usage},
			fmt.Errorf("%w: anthropic structured output truncated at max_tokens", llm.ErrInvalidResponse)
	}

	var textBlocks []string
	for _, block := range parsed.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			textBlocks = append(textBlocks, strings.TrimSpace(block.Text))
		}
	}
	if len(textBlocks) == 0 {
		return &llm.Response{RawBody: rawBody, StatusCode: statusCode, Usage: usage},
			fmt.Errorf("%w: no text content returned by anthropic", llm.ErrInvalidResponse)
	}

	return &llm.Response{
		Text:       strings.Join(textBlocks, "\n"),
		RawBody:    rawBody,
		StatusCode: statusCode,
		Usage:      usage,
	}, nil
}

// _ enforces at compile time that Client implements llm.Client.
var _ llm.Client = (*Client)(nil)
