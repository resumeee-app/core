package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/resumeee-app/resumeee-core"
)

// Completer abstracts text-based completion for LLM extraction tasks.
type Completer interface {
	Complete(ctx context.Context, systemPrompt, userPrompt, provider, model string) (string, error)
}

// UsageCompleter extends Completer with token usage reporting.
type UsageCompleter interface {
	Completer
	CompleteWithUsage(ctx context.Context, systemPrompt, userPrompt, provider, model string) (string, core.TokenUsage, error)
}

// StructuredUsageCompleter extends Completer with provider capability detection and schema-guided structured completions.
type StructuredUsageCompleter interface {
	SupportsStructuredOutput(provider string) bool
	CompleteStructuredWithUsage(ctx context.Context, systemPrompt, userPrompt, provider, model string, schema map[string]any, schemaName string) (string, core.TokenUsage, error)
}

// CompleterFunc allows using an ordinary function as a Completer.
type CompleterFunc func(ctx context.Context, systemPrompt, userPrompt, provider, model string) (string, error)

// Complete dispatches the completion call to the underlying function.
func (f CompleterFunc) Complete(ctx context.Context, systemPrompt, userPrompt, provider, model string) (string, error) {
	return f(ctx, systemPrompt, userPrompt, provider, model)
}

// RawCompleter is an optional interface implemented by LLM providers supporting raw text/chat completions.
type RawCompleter interface {
	CompleteText(ctx context.Context, systemPrompt, userPrompt, model string) (string, error)
}

// RawUsageCompleter is an optional interface implemented by LLM providers supporting raw text completions with token usage.
type RawUsageCompleter interface {
	RawCompleter
	CompleteTextWithUsage(ctx context.Context, systemPrompt, userPrompt, model string) (string, core.TokenUsage, error)
}

// ExtractResumeWithTokenUsage extracts a structured resume using the extractor and returns the parsed resume,
// token usage (defaulting to defaultTokens if reported as 0), and any error encountered.
func ExtractResumeWithTokenUsage(ctx context.Context, extractor core.ResumeExtractor, text, provider, model string, defaultTokens int) (*core.JSONResume, core.TokenUsage, error) {
	var (
		extracted *core.JSONResume
		usage     core.TokenUsage
		err       error
	)
	if ue, ok := extractor.(core.UsageResumeExtractor); ok {
		extracted, usage, err = ue.ExtractResumeWithUsage(ctx, text, provider, model)
	} else if extractor != nil {
		extracted, err = extractor.ExtractResume(ctx, text, provider, model)
	}
	if usage.TotalTokens == 0 && defaultTokens > 0 {
		usage.TotalTokens = defaultTokens
	}
	return extracted, usage, err
}

type resumeExtractor struct {
	completer Completer
}

// NewResumeExtractor constructs a ResumeExtractor with the provided Completer seam.
func NewResumeExtractor(completer Completer) core.ResumeExtractor {
	return &resumeExtractor{
		completer: completer,
	}
}

// NewDefaultResumeExtractor creates a ResumeExtractor backed by the provider Registry.
func NewDefaultResumeExtractor(registry *Registry) core.ResumeExtractor {
	return &resumeExtractor{
		completer: NewRegistryCompleter(registry),
	}
}

// AvailableModels returns available configured models if the completer is backed by a Registry.
func (e *resumeExtractor) AvailableModels() []core.ModelOption {
	if rc, ok := e.completer.(*registryCompleter); ok && rc.registry != nil {
		return rc.registry.AvailableModels()
	}
	return nil
}

type registryCompleter struct {
	registry *Registry
}

// NewRegistryCompleter constructs a Completer that delegates to registered LLM providers.
func NewRegistryCompleter(registry *Registry) Completer {
	return &registryCompleter{registry: registry}
}

// Complete delegates raw text completion to the resolved LLM provider from the registry.
func (rc *registryCompleter) Complete(ctx context.Context, systemPrompt, userPrompt, provider, model string) (string, error) {
	text, _, err := rc.CompleteWithUsage(ctx, systemPrompt, userPrompt, provider, model)
	return text, err
}

// CompleteWithUsage delegates raw text completion and captures token usage from the resolved LLM provider.
func (rc *registryCompleter) CompleteWithUsage(ctx context.Context, systemPrompt, userPrompt, provider, model string) (string, core.TokenUsage, error) {
	p, targetModel, err := rc.resolveProvider(provider, model)
	if err != nil {
		return "", core.TokenUsage{}, err
	}
	return completeWithRawFallback(ctx, p, systemPrompt, userPrompt, targetModel)
}

// SupportsStructuredOutput reports whether the resolved provider natively enforces structured JSON schemas.
func (rc *registryCompleter) SupportsStructuredOutput(provider string) bool {
	p, _, err := rc.resolveProvider(provider, "")
	if err != nil {
		return false
	}
	return providerSupportsNativeSchemaDelivery(p)
}

// CompleteStructuredWithUsage dispatches a schema-guided completion when supported, falling back to raw completion otherwise.
func (rc *registryCompleter) CompleteStructuredWithUsage(
	ctx context.Context,
	systemPrompt, userPrompt, provider, model string,
	schema map[string]any,
	schemaName string,
) (string, core.TokenUsage, error) {
	p, targetModel, err := rc.resolveProvider(provider, model)
	if err != nil {
		return "", core.TokenUsage{}, err
	}
	if sc, ok := p.(StructuredCompleter); ok {
		compReq := core.LLMCompletionRequest{
			Model:        targetModel,
			SystemPrompt: systemPrompt,
			UserPrompt:   userPrompt,
		}
		if providerSupportsNativeSchemaDelivery(p) {
			compReq.ResponseSchema = schema
			compReq.SchemaName = schemaName
		}
		compResp, err := sc.CompleteStructuredWithUsage(ctx, compReq)
		if err != nil {
			return "", core.TokenUsage{}, err
		}
		if compResp == nil {
			return "", core.TokenUsage{}, fmt.Errorf("%w: empty completion response from provider %s", ErrInvalidResponse, p.Name())
		}
		return strings.TrimSpace(compResp.Text), compResp.Usage, nil
	}
	return completeWithRawFallback(ctx, p, systemPrompt, userPrompt, targetModel)
}

func (rc *registryCompleter) resolveProvider(provider, model string) (core.LLMProvider, string, error) {
	if rc.registry == nil {
		return nil, "", ErrNoProviders
	}

	targetProvider := provider
	if strings.TrimSpace(targetProvider) == "" {
		targetProvider = rc.registry.DefaultProvider()
	}
	if strings.TrimSpace(targetProvider) == "" {
		return nil, "", ErrNoProviders
	}

	p, err := rc.registry.Get(targetProvider)
	if err != nil {
		return nil, "", err
	}

	targetModel := model
	if strings.TrimSpace(targetModel) == "" {
		if strings.EqualFold(targetProvider, rc.registry.DefaultProvider()) {
			targetModel = rc.registry.DefaultModel()
		} else if mcp, ok := p.(ModelCatalogProvider); ok && strings.TrimSpace(mcp.DefaultModel()) != "" {
			targetModel = mcp.DefaultModel()
		} else {
			targetModel = defaultModelForProvider(targetProvider)
		}
	}

	return p, targetModel, nil
}

func completeWithRawFallback(ctx context.Context, p core.LLMProvider, systemPrompt, userPrompt, model string) (string, core.TokenUsage, error) {
	if rawUsage, ok := p.(RawUsageCompleter); ok {
		return rawUsage.CompleteTextWithUsage(ctx, systemPrompt, userPrompt, model)
	}
	if raw, ok := p.(RawCompleter); ok {
		text, err := raw.CompleteText(ctx, systemPrompt, userPrompt, model)
		return text, core.TokenUsage{}, err
	}
	return "", core.TokenUsage{}, fmt.Errorf("%w: provider %s does not support raw completion", ErrInvalidResponse, p.Name())
}

// ExtractResume parses unstructured resume text into a structured JSONResume document.
func (e *resumeExtractor) ExtractResume(ctx context.Context, text string, provider string, model string) (*core.JSONResume, error) {
	res, _, err := e.ExtractResumeWithUsage(ctx, text, provider, model)
	return res, err
}

// ExtractResumeWithUsage parses unstructured resume text into a structured JSONResume document and reports token usage.
func (e *resumeExtractor) ExtractResumeWithUsage(ctx context.Context, text string, provider string, model string) (*core.JSONResume, core.TokenUsage, error) {
	if err := ctx.Err(); err != nil {
		return nil, core.TokenUsage{}, err
	}

	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return nil, core.TokenUsage{}, fmt.Errorf("%w: empty resume text", core.ErrValidation)
	}

	if e.completer == nil {
		return nil, core.TokenUsage{}, fmt.Errorf("%w: no completer configured", core.ErrValidation)
	}

	supportsStructured := false
	structuredCompleter, hasStructured := e.completer.(StructuredUsageCompleter)
	if hasStructured {
		supportsStructured = structuredCompleter.SupportsStructuredOutput(provider)
	}

	prompts, err := BuildResumeExtractPromptWithOptions(trimmedText, PromptOptions{
		NativeStructuredOutput: supportsStructured,
	})
	if err != nil {
		return nil, core.TokenUsage{}, fmt.Errorf("failed to build resume extraction prompt: %w", err)
	}

	var rawResponse string
	var usage core.TokenUsage
	if hasStructured {
		var schema map[string]any
		var schemaName string
		if supportsStructured {
			schema = ResumeExtractResponseSchema()
			schemaName = "resume_extract_response"
		}
		rawResponse, usage, err = structuredCompleter.CompleteStructuredWithUsage(
			ctx, prompts.SystemPrompt, prompts.UserPrompt, provider, model, schema, schemaName,
		)
	} else if uc, ok := e.completer.(UsageCompleter); ok {
		rawResponse, usage, err = uc.CompleteWithUsage(ctx, prompts.SystemPrompt, prompts.UserPrompt, provider, model)
	} else {
		rawResponse, err = e.completer.Complete(ctx, prompts.SystemPrompt, prompts.UserPrompt, provider, model)
	}
	if err != nil {
		return nil, core.TokenUsage{}, err
	}

	if err := ctx.Err(); err != nil {
		return nil, core.TokenUsage{}, err
	}

	cleanedJSON := ExtractJSON(rawResponse)
	if strings.TrimSpace(cleanedJSON) == "" {
		return nil, core.TokenUsage{}, fmt.Errorf("%w: empty or missing JSON in LLM response", core.ErrValidation)
	}

	var resume core.JSONResume
	if err := json.Unmarshal([]byte(cleanedJSON), &resume); err != nil {
		return nil, core.TokenUsage{}, fmt.Errorf("%w: failed to parse structured LLM response: %v", core.ErrValidation, err)
	}

	return &resume, usage, nil
}
