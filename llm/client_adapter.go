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
	defaultGeminiModel    = "gemini-3.8-flash"
	defaultOpenAIModel    = "gpt-4o"
	defaultAnthropicModel = "claude-sonnet-4-5"
	defaultOllamaModel    = "llama3.1"
	defaultCompatModel    = "default"
)

// StructuredCompleter is an optional interface implemented by LLM providers
// supporting schema-guided structured JSON completions with token usage reporting.
type StructuredCompleter interface {
	CompleteStructuredWithUsage(ctx context.Context, req core.LLMCompletionRequest) (*core.LLMCompletionResponse, error)
}

// ModelCatalogProvider is implemented by providers that expose their default model and model catalog.
type ModelCatalogProvider interface {
	DefaultModel() string
	Models() []core.ModelOption
}

// ClientProvider wraps a pure transport core.LLMClient with prompt compilation,
// JSON schema attachment, structured observability logging, and domain response unmarshaling.
type ClientProvider struct {
	client       core.LLMClient
	defaultModel string
	logger       *slog.Logger
}

// NewClientProvider wraps a transport-only core.LLMClient into a full core.LLMProvider and RawUsageCompleter.
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

// SupportsStructuredOutput reports whether the wrapped LLMClient supports native provider-level structured JSON outputs.
func (p *ClientProvider) SupportsStructuredOutput() bool {
	return p != nil && p.client != nil && p.client.SupportsStructuredOutput()
}

// GenerateTailoredResume compiles the tailoring prompts and output JSON schema in core,
// dispatches the transport request to the underlying LLMClient, and unmarshals the structured domain response.
func (p *ClientProvider) GenerateTailoredResume(ctx context.Context, req core.GenerationRequest) (genResp *core.GenerationResponse, retErr error) {
	if p == nil || p.client == nil {
		return nil, ErrNoProviders
	}

	supportsStructured := p.SupportsStructuredOutput()
	prompts, err := BuildPromptWithOptions(req, PromptOptions{
		NativeStructuredOutput: supportsStructured,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	model := p.resolveModel(req.Model)
	trace := logLLMRequest(ctx, p.logger, p.Name(), model, "GenerateTailoredResume")

	var statusCode int
	defer func() {
		logLLMResponse(ctx, p.logger, trace, statusCode, retErr)
	}()

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

	var compResp *core.LLMCompletionResponse
	statusCode, compResp, err = p.dispatchClientComplete(ctx, compReq)
	if err != nil {
		retErr = err
		return nil, retErr
	}

	rawText := strings.TrimSpace(compResp.Text)
	cleanJSON := stripCodeFences(rawText)
	if cleanJSON == "" {
		retErr = fmt.Errorf("%w: failed to unmarshal generation response: empty JSON output", ErrInvalidResponse)
		return nil, retErr
	}

	var parsed core.GenerationResponse
	if err := json.Unmarshal([]byte(cleanJSON), &parsed); err != nil {
		retErr = fmt.Errorf("%w: failed to unmarshal generation response: %v", ErrInvalidResponse, err)
		return nil, retErr
	}

	if parsed.Usage.TotalTokens == 0 && (compResp.Usage.TotalTokens > 0 || compResp.Usage.PromptTokens > 0 || compResp.Usage.CompletionTokens > 0) {
		parsed.Usage = compResp.Usage
	}

	return &parsed, nil
}

// CompleteStructuredWithUsage dispatches a schema-guided completion request to the underlying LLMClient.
// If the underlying client does not support native structured output, ResponseSchema and SchemaName are cleared
// so providers rely on prompt-embedded fallback schemas.
func (p *ClientProvider) CompleteStructuredWithUsage(ctx context.Context, req core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
	if p == nil || p.client == nil {
		return nil, ErrNoProviders
	}
	if !p.SupportsStructuredOutput() {
		req.ResponseSchema = nil
		req.SchemaName = ""
	}
	return p.completeWithTrace(ctx, "CompleteStructured", req)
}

// CompleteText implements RawCompleter by delegating to CompleteTextWithUsage.
func (p *ClientProvider) CompleteText(ctx context.Context, systemPrompt, userPrompt, model string) (string, error) {
	text, _, err := p.CompleteTextWithUsage(ctx, systemPrompt, userPrompt, model)
	return text, err
}

// CompleteTextWithUsage dispatches an unstructured/raw text completion request to the underlying LLMClient.
func (p *ClientProvider) CompleteTextWithUsage(ctx context.Context, systemPrompt, userPrompt, model string) (text string, usage core.TokenUsage, retErr error) {
	compResp, err := p.completeWithTrace(ctx, "CompleteText", core.LLMCompletionRequest{
		Model:        model,
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
	})
	if err != nil {
		return "", core.TokenUsage{}, err
	}

	return strings.TrimSpace(compResp.Text), compResp.Usage, nil
}

func (p *ClientProvider) resolveModel(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed != "" {
		return trimmed
	}
	return p.DefaultModel()
}

func (p *ClientProvider) completeWithTrace(ctx context.Context, operation string, req core.LLMCompletionRequest) (compResp *core.LLMCompletionResponse, retErr error) {
	if p == nil || p.client == nil {
		return nil, ErrNoProviders
	}

	req.Model = p.resolveModel(req.Model)
	trace := logLLMRequest(ctx, p.logger, p.Name(), req.Model, operation)

	var statusCode int
	defer func() {
		logLLMResponse(ctx, p.logger, trace, statusCode, retErr)
	}()

	statusCode, compResp, retErr = p.dispatchClientComplete(ctx, req)
	if retErr != nil {
		return nil, retErr
	}
	return compResp, nil
}

func (p *ClientProvider) dispatchClientComplete(ctx context.Context, req core.LLMCompletionRequest) (int, *core.LLMCompletionResponse, error) {
	compResp, err := p.client.Complete(ctx, req)
	var statusCode int
	if compResp != nil {
		statusCode = compResp.StatusCode
	}
	if err != nil {
		return statusCode, nil, err
	}
	if compResp == nil {
		return statusCode, nil, fmt.Errorf("%w: empty completion response from provider %s", ErrInvalidResponse, p.Name())
	}
	return statusCode, compResp, nil
}
