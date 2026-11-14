package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/resumeee-app/resumeee-core"
)

const (
	ProviderGemini        = "gemini"
	ProviderOpenAI        = "openai"
	ProviderAnthropic     = "anthropic"
	ProviderOllama        = "ollama"
	ProviderCompat        = "compat"
	ProviderCompatAlt     = "openai-compat"
	defaultGeminiModel    = "gemini-4-hydrogen"
	defaultOpenAIModel    = "gpt-4o"
	defaultAnthropicModel = "claude-sonnet-4-5"
	defaultOllamaModel    = "llama3.1"
	defaultCompatModel    = "default"
)

// ModelCatalogProvider is implemented by providers that expose their default model and model catalog.
type ModelCatalogProvider interface {
	DefaultModel() string
	Models() []core.ModelOption
}

// StructuredCompleter is implemented by providers capable of dispatching transport-level
// completion requests (both native structured output and prompt-schema fallback) with token usage.
type StructuredCompleter interface {
	CompleteStructuredWithUsage(ctx context.Context, operation string, req core.LLMCompletionRequest) (string, core.TokenUsage, error)
}

// ClientProvider wraps a pure transport core.LLMClient with prompt compilation,
// JSON schema attachment, structured observability logging, and domain response unmarshaling.
type ClientProvider struct {
	client       core.LLMClient
	defaultModel string
	logger       *slog.Logger
}

// NewClientProvider wraps a transport-only core.LLMClient into a full core.LLMProvider and StructuredCompleter.
func NewClientProvider(client core.LLMClient, opts ...Option) *ClientProvider {
	fallbackModel := "default"
	if client != nil && strings.TrimSpace(client.DefaultModel()) != "" {
		fallbackModel = client.DefaultModel()
	}
	cfg := applyOptions(opts, "", fallbackModel)
	return &ClientProvider{
		client:       client,
		defaultModel: cfg.defaultModel,
		logger:       cfg.logger,
	}
}

// Name returns the underlying client's provider identifier.
func (p *ClientProvider) Name() string {
	if p == nil || p.client == nil {
		return ""
	}
	return p.client.Name()
}

// DefaultModel returns the configured default model for this provider.
func (p *ClientProvider) DefaultModel() string {
	if p == nil {
		return "default"
	}
	if strings.TrimSpace(p.defaultModel) != "" {
		return p.defaultModel
	}
	if p.client != nil && strings.TrimSpace(p.client.DefaultModel()) != "" {
		return p.client.DefaultModel()
	}
	return defaultModelForProvider(p.Name())
}

// Models returns the model catalog supported by the underlying transport client.
func (p *ClientProvider) Models() []core.ModelOption {
	if p == nil || p.client == nil {
		return nil
	}
	return p.client.Models()
}

// SupportsStructuredOutput reports whether the underlying transport client supports
// provider-native structured outputs for the specified model (or DefaultModel when empty).
func (p *ClientProvider) SupportsStructuredOutput(model string) bool {
	if p == nil || p.client == nil {
		return false
	}
	resolved := strings.TrimSpace(model)
	if resolved == "" {
		resolved = p.DefaultModel()
	}
	return p.client.SupportsStructuredOutput(resolved)
}

func (p *ClientProvider) completeWithTrace(ctx context.Context, operation string, req core.LLMCompletionRequest) (compResp *core.LLMCompletionResponse, retErr error) {
	if p == nil || p.client == nil {
		return nil, ErrNoProviders
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.DefaultModel()
	}
	req.Model = model

	if operation == "" {
		operation = "CompleteText"
	}

	trace := logLLMRequest(ctx, p.logger, p.Name(), model, operation)

	var statusCode int
	defer func() {
		logLLMResponse(ctx, p.logger, trace, statusCode, retErr)
	}()

	resp, err := p.client.Complete(ctx, req)
	if resp != nil {
		statusCode = resp.StatusCode
	}
	if err != nil {
		retErr = err
		return resp, retErr
	}
	if resp == nil {
		retErr = fmt.Errorf("%w: empty completion response from provider %s", ErrInvalidResponse, p.Name())
		return nil, retErr
	}
	return resp, nil
}

// GenerateTailoredResume compiles the tailoring prompts and output JSON schema in core,
// dispatches the transport request to the underlying LLMClient, and unmarshals the structured domain response.
func (p *ClientProvider) GenerateTailoredResume(ctx context.Context, req core.GenerationRequest) (*core.GenerationResponse, error) {
	if p == nil || p.client == nil {
		return nil, ErrNoProviders
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.DefaultModel()
	}

	supportsStructured := p.SupportsStructuredOutput(model)
	prompts, err := BuildPromptWithOptions(req, PromptOptions{NativeStructuredOutput: supportsStructured})
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	compReq := core.LLMCompletionRequest{
		Model:          model,
		SystemPrompt:   prompts.SystemPrompt,
		UserPrompt:     prompts.UserPrompt,
		ReasoningLevel: req.ReasoningLevel,
	}
	if supportsStructured {
		compReq.ResponseSchema = GenerationResponseSchema()
		compReq.SchemaName = "tailored_resume_response"
	}

	compResp, err := p.completeWithTrace(ctx, "GenerateTailoredResume", compReq)
	if err != nil {
		return nil, err
	}

	rawText := strings.TrimSpace(compResp.Text)
	cleanJSON := stripCodeFences(rawText)
	if cleanJSON == "" {
		return nil, fmt.Errorf("%w: failed to unmarshal generation response: empty JSON output", ErrInvalidResponse)
	}

	var parsed core.GenerationResponse
	if err := json.Unmarshal([]byte(cleanJSON), &parsed); err != nil {
		return nil, fmt.Errorf("%w: failed to unmarshal generation response: %v", ErrInvalidResponse, err)
	}

	if parsed.Usage.TotalTokens == 0 && (compResp.Usage.TotalTokens > 0 || compResp.Usage.PromptTokens > 0 || compResp.Usage.CompletionTokens > 0) {
		parsed.Usage = compResp.Usage
	}

	return &parsed, nil
}

// CompleteStructuredWithUsage dispatches a transport completion request with optional native
// structured output schema and returns trimmed text alongside token usage.
func (p *ClientProvider) CompleteStructuredWithUsage(ctx context.Context, operation string, req core.LLMCompletionRequest) (string, core.TokenUsage, error) {
	compResp, err := p.completeWithTrace(ctx, operation, req)
	if err != nil {
		return "", core.TokenUsage{}, err
	}
	return strings.TrimSpace(compResp.Text), compResp.Usage, nil
}

// CompleteText implements RawCompleter by delegating to CompleteTextWithUsage.
func (p *ClientProvider) CompleteText(ctx context.Context, systemPrompt, userPrompt, model string) (string, error) {
	text, _, err := p.CompleteTextWithUsage(ctx, systemPrompt, userPrompt, model)
	return text, err
}

// CompleteTextWithUsage dispatches an unstructured/raw text completion request to the underlying LLMClient.
func (p *ClientProvider) CompleteTextWithUsage(ctx context.Context, systemPrompt, userPrompt, model string) (string, core.TokenUsage, error) {
	return p.CompleteStructuredWithUsage(ctx, "CompleteText", core.LLMCompletionRequest{
		Model:        model,
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
	})
}

// structuredCall encapsulates parameters for capability-aware completion dispatch across
// ServiceAdapter and ResumeExtractor workflows without duplicating fallback ladders.
type structuredCall struct {
	Provider       core.LLMProvider
	Operation      string
	Model          string
	Prompts        *PromptPayload
	Schema         map[string]any
	SchemaName     string
	ReasoningLevel string
}

func canExecuteProviderCompletion(provider core.LLMProvider) bool {
	if provider == nil {
		return false
	}
	if _, ok := provider.(StructuredCompleter); ok {
		return true
	}
	if _, ok := provider.(RawUsageCompleter); ok {
		return true
	}
	if _, ok := provider.(RawCompleter); ok {
		return true
	}
	return false
}

func (c structuredCall) execute(ctx context.Context) (string, core.TokenUsage, error) {
	if c.Provider == nil {
		return "", core.TokenUsage{}, ErrNoProviders
	}
	var sysPrompt, usrPrompt string
	if c.Prompts != nil {
		sysPrompt = c.Prompts.SystemPrompt
		usrPrompt = c.Prompts.UserPrompt
	}

	if sc, ok := c.Provider.(StructuredCompleter); ok {
		compReq := core.LLMCompletionRequest{
			Model:          c.Model,
			SystemPrompt:   sysPrompt,
			UserPrompt:     usrPrompt,
			ReasoningLevel: c.ReasoningLevel,
		}
		if c.Schema != nil && core.SupportsStructuredOutput(c.Provider, c.Model) {
			compReq.ResponseSchema = c.Schema
			compReq.SchemaName = c.SchemaName
		}
		return sc.CompleteStructuredWithUsage(ctx, c.Operation, compReq)
	}

	if rawUsage, ok := c.Provider.(RawUsageCompleter); ok {
		return rawUsage.CompleteTextWithUsage(ctx, sysPrompt, usrPrompt, c.Model)
	}

	if raw, ok := c.Provider.(RawCompleter); ok {
		text, err := raw.CompleteText(ctx, sysPrompt, usrPrompt, c.Model)
		return text, core.TokenUsage{}, err
	}

	opLabel := strings.TrimSpace(c.Operation)
	if opLabel == "" {
		opLabel = "completion"
	}
	return "", core.TokenUsage{}, fmt.Errorf("%w: provider %s does not support completion for %s", ErrInvalidResponse, c.Provider.Name(), opLabel)
}
