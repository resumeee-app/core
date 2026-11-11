package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/resumeee-app/resumeee-core/domain"
)

const (
	ProviderGemini        = "gemini"
	ProviderOpenAI        = "openai"
	ProviderAnthropic     = "anthropic"
	ProviderCompat        = "compat"
	ProviderCompatAlt     = "openai-compat"
	defaultGeminiModel    = "gemini-3.8-flash"
	defaultOpenAIModel    = "gpt-4o"
	defaultAnthropicModel = "claude-3-7-sonnet-20250219"
	defaultCompatModel    = "default"
)

// ModelCatalogProvider is implemented by providers that expose their default model and model catalog.
type ModelCatalogProvider interface {
	DefaultModel() string
	Models() []domain.ModelOption
}

// ClientProvider wraps a pure transport domain.LLMClient with prompt compilation,
// JSON schema attachment, structured observability logging, prompt debugging, and domain response unmarshaling.
type ClientProvider struct {
	client         domain.LLMClient
	defaultModel   string
	logger         *slog.Logger
	promptDebugger PromptDebugger
}

// NewClientProvider wraps a transport-only domain.LLMClient into a full domain.LLMProvider and RawUsageCompleter.
func NewClientProvider(client domain.LLMClient, opts ...Option) *ClientProvider {
	fallbackModel := "default"
	if client != nil && strings.TrimSpace(client.DefaultModel()) != "" {
		fallbackModel = client.DefaultModel()
	}
	cfg := applyOptions(opts, "", fallbackModel)
	return &ClientProvider{
		client:         client,
		defaultModel:   cfg.defaultModel,
		logger:         cfg.logger,
		promptDebugger: cfg.promptDebugger,
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
func (p *ClientProvider) Models() []domain.ModelOption {
	if p == nil || p.client == nil {
		return nil
	}
	return p.client.Models()
}

// GenerateTailoredResume compiles the tailoring prompts and output JSON schema in core,
// dispatches the transport request to the underlying LLMClient, and unmarshals the structured domain response.
func (p *ClientProvider) GenerateTailoredResume(ctx context.Context, req domain.GenerationRequest) (genResp *domain.GenerationResponse, retErr error) {
	if p == nil || p.client == nil {
		return nil, ErrNoProviders
	}

	prompts, err := BuildPrompt(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.DefaultModel()
	}

	trace := logLLMRequest(ctx, p.logger, p.promptDebugger, p.Name(), model, "GenerateTailoredResume", prompts.SystemPrompt, prompts.UserPrompt)

	var statusCode int
	var rawResp string
	defer func() {
		logLLMResponse(ctx, p.logger, trace, statusCode, rawResp, retErr)
	}()

	compResp, err := p.client.Complete(ctx, domain.LLMCompletionRequest{
		Model:          model,
		SystemPrompt:   prompts.SystemPrompt,
		UserPrompt:     prompts.UserPrompt,
		ResponseSchema: GenerationResponseSchema(),
		ReasoningLevel: req.ReasoningLevel,
	})
	if compResp != nil {
		statusCode = compResp.StatusCode
		rawResp = compResp.RawBody
		if rawResp == "" {
			rawResp = compResp.Text
		}
	}
	if err != nil {
		retErr = err
		return nil, retErr
	}
	if compResp == nil {
		retErr = fmt.Errorf("%w: empty completion response from provider %s", ErrInvalidResponse, p.Name())
		return nil, retErr
	}

	rawText := strings.TrimSpace(compResp.Text)
	cleanJSON := stripCodeFences(rawText)
	if cleanJSON == "" {
		retErr = fmt.Errorf("%w: failed to unmarshal generation response: empty JSON output", ErrInvalidResponse)
		return nil, retErr
	}

	var parsed domain.GenerationResponse
	if err := json.Unmarshal([]byte(cleanJSON), &parsed); err != nil {
		retErr = fmt.Errorf("%w: failed to unmarshal generation response: %v", ErrInvalidResponse, err)
		return nil, retErr
	}

	if parsed.Usage.TotalTokens == 0 && (compResp.Usage.TotalTokens > 0 || compResp.Usage.PromptTokens > 0 || compResp.Usage.CompletionTokens > 0) {
		parsed.Usage = compResp.Usage
	}

	return &parsed, nil
}

// CompleteText implements RawCompleter by delegating to CompleteTextWithUsage.
func (p *ClientProvider) CompleteText(ctx context.Context, systemPrompt, userPrompt, model string) (string, error) {
	text, _, err := p.CompleteTextWithUsage(ctx, systemPrompt, userPrompt, model)
	return text, err
}

// CompleteTextWithUsage dispatches an unstructured/raw text completion request to the underlying LLMClient.
func (p *ClientProvider) CompleteTextWithUsage(ctx context.Context, systemPrompt, userPrompt, model string) (text string, usage domain.TokenUsage, retErr error) {
	if p == nil || p.client == nil {
		return "", domain.TokenUsage{}, ErrNoProviders
	}

	if strings.TrimSpace(model) == "" {
		model = p.DefaultModel()
	}

	trace := logLLMRequest(ctx, p.logger, p.promptDebugger, p.Name(), model, "CompleteText", systemPrompt, userPrompt)

	var statusCode int
	var rawResp string
	defer func() {
		logLLMResponse(ctx, p.logger, trace, statusCode, rawResp, retErr)
	}()

	compResp, err := p.client.Complete(ctx, domain.LLMCompletionRequest{
		Model:        model,
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
	})
	if compResp != nil {
		statusCode = compResp.StatusCode
		rawResp = compResp.RawBody
		if rawResp == "" {
			rawResp = compResp.Text
		}
	}
	if err != nil {
		retErr = err
		return "", domain.TokenUsage{}, retErr
	}
	if compResp == nil {
		retErr = fmt.Errorf("%w: empty completion response from provider %s", ErrInvalidResponse, p.Name())
		return "", domain.TokenUsage{}, retErr
	}

	return strings.TrimSpace(compResp.Text), compResp.Usage, nil
}
