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

const defaultOllamaBaseURL = "http://localhost:11434"

// OllamaProvider implements core.LLMClient for local or hosted Ollama instances using
// the native /api/chat endpoint with schema-constrained structured outputs via format.
type OllamaProvider struct {
	baseURL      string
	apiKey       string
	httpClient   *http.Client
	defaultModel string
}

// NewOllamaProvider creates an Ollama transport client targeting baseURL (defaulting to
// http://localhost:11434 when empty) with an optional Bearer API key.
func NewOllamaProvider(baseURL string, apiKey string, opts ...Option) *OllamaProvider {
	resolvedBase := strings.TrimSpace(baseURL)
	if resolvedBase == "" {
		resolvedBase = defaultOllamaBaseURL
	}
	cfg := applyOptions(opts, resolvedBase, defaultOllamaModel)
	return &OllamaProvider{
		baseURL:      cfg.baseURL,
		apiKey:       apiKey,
		httpClient:   cfg.httpClient,
		defaultModel: cfg.defaultModel,
	}
}

// Name returns the provider identifier.
func (p *OllamaProvider) Name() string {
	return ProviderOllama
}

// DefaultModel returns the configured default model for Ollama.
func (p *OllamaProvider) DefaultModel() string {
	if strings.TrimSpace(p.defaultModel) != "" {
		return p.defaultModel
	}
	return defaultOllamaModel
}

// Models returns the default model option for Ollama.
func (p *OllamaProvider) Models() []core.ModelOption {
	def := p.DefaultModel()
	return []core.ModelOption{
		{
			Provider:    ProviderOllama,
			Model:       def,
			DisplayName: fmt.Sprintf("Ollama — %s", def),
			IsDefault:   true,
		},
	}
}

// SupportsStructuredOutput reports that Ollama natively enforces JSON schemas via /api/chat format.
func (p *OllamaProvider) SupportsStructuredOutput() bool {
	return true
}

// Complete executes a non-streaming completion request against Ollama's /api/chat endpoint.
func (p *OllamaProvider) Complete(ctx context.Context, req core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.DefaultModel()
	}

	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	type requestBody struct {
		Model    string         `json:"model"`
		Messages []chatMessage  `json:"messages"`
		Stream   bool           `json:"stream"`
		Format   map[string]any `json:"format,omitempty"`
	}

	messages := make([]chatMessage, 0, 2)
	if strings.TrimSpace(req.SystemPrompt) != "" {
		messages = append(messages, chatMessage{Role: "system", Content: req.SystemPrompt})
	}
	messages = append(messages, chatMessage{Role: "user", Content: req.UserPrompt})

	payload := requestBody{
		Model:    model,
		Messages: messages,
		Stream:   false,
		Format:   req.ResponseSchema,
	}

	reqBodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ollama request: %w", err)
	}

	url := buildOllamaURL(p.baseURL)
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
		return nil, fmt.Errorf("ollama http request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := readBoundedBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	rawBody := string(bodyBytes)
	if err := checkStatusError("ollama", resp.StatusCode, bodyBytes); err != nil {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, err
	}

	type ollamaResponse struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		PromptEvalCount int `json:"prompt_eval_count"`
		EvalCount       int `json:"eval_count"`
	}

	var ollamaResp ollamaResponse
	if err := json.Unmarshal(bodyBytes, &ollamaResp); err != nil {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, fmt.Errorf("%w: malformed ollama response: %v", ErrInvalidResponse, err)
	}

	text := strings.TrimSpace(ollamaResp.Message.Content)
	if text == "" {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, fmt.Errorf("%w: empty message content returned by ollama", ErrInvalidResponse)
	}

	usage := core.TokenUsage{
		PromptTokens:     ollamaResp.PromptEvalCount,
		CompletionTokens: ollamaResp.EvalCount,
		TotalTokens:      ollamaResp.PromptEvalCount + ollamaResp.EvalCount,
	}

	return &core.LLMCompletionResponse{
		Text:       text,
		RawBody:    rawBody,
		StatusCode: resp.StatusCode,
		Usage:      usage,
	}, nil
}

func buildOllamaURL(baseURL string) string {
	cleaned := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(cleaned, "/api/chat") {
		return cleaned
	}
	return cleaned + "/api/chat"
}
