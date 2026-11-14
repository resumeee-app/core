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
	if p != nil && strings.TrimSpace(p.defaultModel) != "" {
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

// SupportsStructuredOutput reports whether the target Claude model supports Anthropic's
// native output_config.format constrained decoding parameter.
func (p *AnthropicProvider) SupportsStructuredOutput(model string) bool {
	resolved := strings.TrimSpace(model)
	if resolved == "" {
		resolved = p.DefaultModel()
	}
	return AnthropicModelSupportsStructuredOutput(resolved)
}

// AnthropicModelSupportsStructuredOutput returns true for Claude 4.5+ models (Sonnet 4.5+,
// Haiku 4.5+, Opus 4.5+, Claude 4.6+, Claude 5+) and Claude Opus 4.1+, and false for legacy
// Claude 3.x / 3.5 / 3.7 / 4.0 models that do not support output_config.format.
func AnthropicModelSupportsStructuredOutput(model string) bool {
	norm := strings.ToLower(strings.TrimSpace(model))
	if !strings.HasPrefix(norm, "claude-") {
		return false
	}

	// Reject all Claude 1.x, 2.x, and 3.x families (including claude-3-5-haiku, claude-3-7-sonnet, etc.)
	if strings.HasPrefix(norm, "claude-1") || strings.HasPrefix(norm, "claude-2") || strings.HasPrefix(norm, "claude-3") {
		return false
	}

	// Claude Opus 4.1+ supports structured outputs via output_config.format.
	if hasClaudeVersionAtLeast(norm, "claude-opus-", 4, 1) {
		return true
	}

	// Claude Sonnet / Haiku / Opus 4.5+ and Claude 5+ support output_config.format.
	for _, prefix := range []string{"claude-sonnet-", "claude-haiku-", "claude-opus-", "claude-"} {
		if hasClaudeVersionAtLeast(norm, prefix, 4, 5) {
			return true
		}
	}

	return false
}

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

	// Exact major match (e.g. major == 4): inspect optional minor version vs 8-digit date suffix.
	if len(restAfterMajor) == 0 {
		return minMinor == 0
	}
	sep := restAfterMajor[0]
	if sep != '-' && sep != '.' {
		return false
	}
	minorPart := restAfterMajor[1:]
	minor, _, ok := parseLeadingPositiveInt(minorPart)
	if !ok {
		return false
	}
	// An 8-digit date snapshot immediately after major (e.g. claude-sonnet-4-20250514) means minor == 0.
	if minor >= 20200000 {
		minor = 0
	}
	return minor >= minMinor
}

func parseLeadingPositiveInt(s string) (int, string, bool) {
	i := 0
	val := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		val = val*10 + int(s[i]-'0')
		i++
	}
	if i == 0 {
		return 0, s, false
	}
	return val, s[i:], true
}

// Complete executes a completion request against the Anthropic Messages API (/v1/messages).
func (p *AnthropicProvider) Complete(ctx context.Context, req core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
	if p == nil || strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("%w: anthropic api key is required", ErrUnauthorized)
	}

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

	systemPrompt := req.SystemPrompt
	var outCfg *outputConfig

	if req.ResponseSchema != nil {
		if p.SupportsStructuredOutput(model) {
			outCfg = &outputConfig{
				Format: &outputFormat{
					Type:   "json_schema",
					Schema: SanitizeAnthropicSchema(req.ResponseSchema),
				},
			}
		} else if !containsResponseSchemaDirective(systemPrompt) {
			schemaBytes, err := json.MarshalIndent(req.ResponseSchema, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("failed to marshal fallback anthropic schema: %w", err)
			}
			systemPrompt = appendRawSchemaBlock(systemPrompt, string(schemaBytes))
		}
	}

	payload := requestBody{
		Model:     model,
		MaxTokens: defaultAnthropicTokens,
		System:    systemPrompt,
		Messages: []anthropicMessage{
			{Role: "user", Content: req.UserPrompt},
		},
		OutputConfig: outCfg,
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

	resp, err := resolveHTTPClient(p.httpClient).Do(httpReq)
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
		StopReason string         `json:"stop_reason"`
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
		}, fmt.Errorf("%w: anthropic model refused request", ErrInvalidResponse)
	}

	if anthropicResp.StopReason == "max_tokens" && outCfg != nil {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
			Usage:      usage,
		}, fmt.Errorf("%w: anthropic structured output truncated at max_tokens", ErrInvalidResponse)
	}

	var textBlocks []string
	for _, block := range anthropicResp.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			textBlocks = append(textBlocks, strings.TrimSpace(block.Text))
		}
	}

	if len(textBlocks) > 0 {
		return &core.LLMCompletionResponse{
			Text:       strings.Join(textBlocks, "\n"),
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
			Usage:      usage,
		}, nil
	}

	return &core.LLMCompletionResponse{
		RawBody:    rawBody,
		StatusCode: resp.StatusCode,
		Usage:      usage,
	}, fmt.Errorf("%w: no text content returned by anthropic", ErrInvalidResponse)
}
