package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"resumeee/internal/domain"
)

const (
	defaultCompatModel = "default"
	ProviderCompat     = "compat"
	ProviderCompatAlt  = "openai-compat"
)

// CompatProvider implements domain.LLMProvider for generic OpenAI-compatible HTTP endpoints
// such as Ollama, vLLM, llama.cpp, LocalAI, and LM Studio.
type CompatProvider struct {
	baseURL        string
	apiKey         string
	httpClient     *http.Client
	defaultModel   string
	logger         *slog.Logger
	promptDebugger PromptDebugger
}

// NewCompatProvider creates an OpenAI-compatible adapter targeting baseURL with optional API key.
func NewCompatProvider(baseURL string, apiKey string, opts ...Option) *CompatProvider {
	cfg := applyOptions(opts, baseURL, defaultCompatModel)
	return &CompatProvider{
		baseURL:        cfg.baseURL,
		apiKey:         apiKey,
		httpClient:     cfg.httpClient,
		defaultModel:   cfg.defaultModel,
		logger:         cfg.logger,
		promptDebugger: cfg.promptDebugger,
	}
}

// Name returns the provider identifier.
func (p *CompatProvider) Name() string {
	return ProviderCompat
}

// GenerateTailoredResume calls the OpenAI-compatible chat completions endpoint.
func (p *CompatProvider) GenerateTailoredResume(ctx context.Context, req domain.GenerationRequest) (genResp *domain.GenerationResponse, retErr error) {
	prompts, err := BuildPrompt(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	model := req.Model
	if strings.TrimSpace(model) == "" {
		model = p.defaultModel
	}

	// For compatibility with models lacking strict schema engines, embed schema instructions in system prompt
	schemaBytes, err := json.Marshal(GenerationResponseSchema())
	if err != nil {
		return nil, fmt.Errorf("failed to serialize schema: %w", err)
	}

	systemPromptWithSchema := fmt.Sprintf("%s\n\n<json_output_schema>\n%s\n</json_output_schema>",
		prompts.SystemPrompt, string(schemaBytes))

	trace := logLLMRequest(ctx, p.logger, p.promptDebugger, p.Name(), model, "GenerateTailoredResume", systemPromptWithSchema, prompts.UserPrompt)

	var statusCode int
	var rawResp string
	defer func() {
		logLLMResponse(ctx, p.logger, trace, statusCode, rawResp, retErr)
	}()

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

	payload := requestBody{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPromptWithSchema},
			{Role: "user", Content: prompts.UserPrompt},
		},
		ResponseFormat: &responseFormat{
			Type: "json_object",
		},
	}

	reqBodyBytes, err := json.Marshal(payload)
	if err != nil {
		retErr = fmt.Errorf("failed to marshal compat request: %w", err)
		return nil, retErr
	}

	url := buildCompatURL(p.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		retErr = fmt.Errorf("failed to create http request: %w", err)
		return nil, retErr
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(p.apiKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		retErr = fmt.Errorf("compat http request failed: %w", err)
		return nil, retErr
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := readBoundedBody(resp.Body)
	if err != nil {
		retErr = fmt.Errorf("failed to read response body: %w", err)
		return nil, retErr
	}

	statusCode = resp.StatusCode
	rawResp = string(bodyBytes)

	if err := checkStatusError("compat", resp.StatusCode, bodyBytes); err != nil {
		retErr = err
		return nil, retErr
	}

	type compatResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	var compatResp compatResponse
	if err := json.Unmarshal(bodyBytes, &compatResp); err != nil {
		retErr = fmt.Errorf("%w: malformed compat response: %v", ErrInvalidResponse, err)
		return nil, retErr
	}

	if len(compatResp.Choices) == 0 {
		retErr = fmt.Errorf("%w: no choices returned by compat endpoint", ErrInvalidResponse)
		return nil, retErr
	}

	rawText := strings.TrimSpace(compatResp.Choices[0].Message.Content)
	cleanJSON := stripCodeFences(rawText)

	var parsed domain.GenerationResponse
	if err := json.Unmarshal([]byte(cleanJSON), &parsed); err != nil {
		retErr = fmt.Errorf("%w: failed to unmarshal generation response: %v", ErrInvalidResponse, err)
		return nil, retErr
	}

	return &parsed, nil
}

// CompleteText implements RawCompleter for OpenAI-compatible endpoints.
func (p *CompatProvider) CompleteText(ctx context.Context, systemPrompt, userPrompt, model string) (text string, retErr error) {
	if strings.TrimSpace(model) == "" {
		model = p.defaultModel
	}

	trace := logLLMRequest(ctx, p.logger, p.promptDebugger, p.Name(), model, "CompleteText", systemPrompt, userPrompt)

	var statusCode int
	var rawResp string
	defer func() {
		logLLMResponse(ctx, p.logger, trace, statusCode, rawResp, retErr)
	}()

	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	type requestBody struct {
		Model    string        `json:"model"`
		Messages []chatMessage `json:"messages"`
	}

	payload := requestBody{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}

	reqBodyBytes, err := json.Marshal(payload)
	if err != nil {
		retErr = fmt.Errorf("failed to marshal compat request: %w", err)
		return "", retErr
	}

	url := buildCompatURL(p.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		retErr = fmt.Errorf("failed to create http request: %w", err)
		return "", retErr
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(p.apiKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		retErr = fmt.Errorf("compat http request failed: %w", err)
		return "", retErr
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := readBoundedBody(resp.Body)
	if err != nil {
		retErr = fmt.Errorf("failed to read response body: %w", err)
		return "", retErr
	}

	statusCode = resp.StatusCode
	rawResp = string(bodyBytes)

	if err := checkStatusError("compat", resp.StatusCode, bodyBytes); err != nil {
		retErr = err
		return "", retErr
	}

	type openAIResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	var openAIResp openAIResponse
	if err := json.Unmarshal(bodyBytes, &openAIResp); err != nil {
		retErr = fmt.Errorf("%w: malformed compat response: %v", ErrInvalidResponse, err)
		return "", retErr
	}

	if len(openAIResp.Choices) == 0 {
		retErr = fmt.Errorf("%w: no choices returned by compat endpoint", ErrInvalidResponse)
		return "", retErr
	}

	return strings.TrimSpace(openAIResp.Choices[0].Message.Content), nil
}

// buildCompatURL normalizes baseURL and ensures the /chat/completions path suffix is present.
func buildCompatURL(baseURL string) string {
	cleaned := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(cleaned, "/chat/completions") {
		return cleaned
	}
	return cleaned + "/chat/completions"
}
