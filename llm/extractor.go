package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/resumeee-app/resumeee-core"
)

// Completer abstracts model catalog discovery and capability-aware completion for resume extraction.
type Completer interface {
	AvailableModels() []core.ModelOption
	SupportsStructuredCompletion(provider, model string) bool
	Complete(ctx context.Context, operation, systemPrompt, userPrompt, provider, model, schemaName string, schema map[string]any) (string, core.TokenUsage, error)
}

// CompleterFunc allows using an ordinary text-completion function as a Completer.
type CompleterFunc func(ctx context.Context, systemPrompt, userPrompt, provider, model string) (string, error)

// AvailableModels returns nil because a bare CompleterFunc carries no model catalog.
func (f CompleterFunc) AvailableModels() []core.ModelOption {
	return nil
}

// SupportsStructuredCompletion returns false because a bare CompleterFunc relies on prompt-embedded schemas.
func (f CompleterFunc) SupportsStructuredCompletion(_, _ string) bool {
	return false
}

// Complete dispatches the completion call to the underlying function with zero reported token usage.
func (f CompleterFunc) Complete(ctx context.Context, _, systemPrompt, userPrompt, provider, model, _ string, _ map[string]any) (string, core.TokenUsage, error) {
	if f == nil {
		return "", core.TokenUsage{}, fmt.Errorf("%w: no completer configured", core.ErrValidation)
	}
	text, err := f(ctx, systemPrompt, userPrompt, provider, model)
	return text, core.TokenUsage{}, err
}

// ExtractResumeWithTokenUsage extracts a structured resume using the extractor and returns the parsed resume,
// token usage (defaulting to defaultTokens if reported as 0), and any error encountered.
func ExtractResumeWithTokenUsage(ctx context.Context, extractor core.ResumeExtractor, text, provider, model string, defaultTokens int) (*core.JSONResume, core.TokenUsage, error) {
	if extractor == nil {
		return nil, core.TokenUsage{}, nil
	}
	extracted, usage, err := extractor.ExtractResumeWithUsage(ctx, text, provider, model)
	if usage.TotalTokens == 0 && defaultTokens > 0 {
		usage.TotalTokens = defaultTokens
	}
	return extracted, usage, err
}

// ResumeExtractor implements core.ResumeExtractor and core.ModelEnumerator using an injected Completer.
type ResumeExtractor struct {
	completer Completer
}

// NewResumeExtractor constructs a ResumeExtractor with the provided Completer seam.
func NewResumeExtractor(completer Completer) *ResumeExtractor {
	return &ResumeExtractor{
		completer: completer,
	}
}

// NewDefaultResumeExtractor creates a ResumeExtractor backed by the provider Registry.
func NewDefaultResumeExtractor(registry *Registry) *ResumeExtractor {
	return &ResumeExtractor{
		completer: NewRegistryCompleter(registry),
	}
}

// AvailableModels returns the configured model options exposed by the underlying Completer.
func (e *ResumeExtractor) AvailableModels() []core.ModelOption {
	if e == nil || e.completer == nil {
		return nil
	}
	return e.completer.AvailableModels()
}

// RegistryCompleter implements Completer by delegating model catalog lookup and completion dispatch to a Registry.
type RegistryCompleter struct {
	registry *Registry
}

// NewRegistryCompleter constructs a RegistryCompleter that delegates to registered LLM providers.
func NewRegistryCompleter(registry *Registry) *RegistryCompleter {
	return &RegistryCompleter{registry: registry}
}

// AvailableModels returns configured model options from the underlying Registry.
func (rc *RegistryCompleter) AvailableModels() []core.ModelOption {
	if rc == nil || rc.registry == nil {
		return nil
	}
	return rc.registry.AvailableModels()
}

// resolveProviderAndModel resolves the target provider and model name from the underlying Registry.
func (rc *RegistryCompleter) resolveProviderAndModel(provider, model string) (core.LLMProvider, string, error) {
	if rc == nil || rc.registry == nil {
		return nil, "", ErrNoProviders
	}
	return rc.registry.Resolve(core.GenerationRequest{
		Model:         model,
		BranchContext: core.Branch{ProviderOverride: provider},
	}, nil, nil)
}

// SupportsStructuredCompletion reports whether the resolved provider and model support native structured outputs.
func (rc *RegistryCompleter) SupportsStructuredCompletion(provider, model string) bool {
	p, resolvedModel, err := rc.resolveProviderAndModel(provider, model)
	if err != nil || p == nil {
		return false
	}
	return p.SupportsStructuredOutput(resolvedModel)
}

// Complete resolves the provider/model and executes a capability-aware completion request with token usage accounting.
func (rc *RegistryCompleter) Complete(ctx context.Context, operation, systemPrompt, userPrompt, provider, model, schemaName string, schema map[string]any) (string, core.TokenUsage, error) {
	p, targetModel, err := rc.resolveProviderAndModel(provider, model)
	if err != nil {
		return "", core.TokenUsage{}, err
	}
	return (structuredCall{
		Provider:   p,
		Operation:  operation,
		Model:      targetModel,
		Schema:     schema,
		SchemaName: schemaName,
		Prompts: &PromptPayload{
			SystemPrompt: systemPrompt,
			UserPrompt:   userPrompt,
		},
	}).execute(ctx)
}

// ExtractResume parses unstructured resume text into a structured JSONResume document.
func (e *ResumeExtractor) ExtractResume(ctx context.Context, text string, provider string, model string) (*core.JSONResume, error) {
	res, _, err := e.ExtractResumeWithUsage(ctx, text, provider, model)
	return res, err
}

// ExtractResumeWithUsage parses unstructured resume text into a structured JSONResume document and reports token usage.
func (e *ResumeExtractor) ExtractResumeWithUsage(ctx context.Context, text string, provider string, model string) (*core.JSONResume, core.TokenUsage, error) {
	if err := ctx.Err(); err != nil {
		return nil, core.TokenUsage{}, err
	}

	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return nil, core.TokenUsage{}, fmt.Errorf("%w: empty resume text", core.ErrValidation)
	}

	if e == nil || e.completer == nil {
		return nil, core.TokenUsage{}, fmt.Errorf("%w: no completer configured", core.ErrValidation)
	}

	supportsStructured := e.completer.SupportsStructuredCompletion(provider, model)
	prompts, pErr := BuildResumeExtractPromptWithOptions(trimmedText, PromptOptions{NativeStructuredOutput: supportsStructured})
	if pErr != nil {
		return nil, core.TokenUsage{}, fmt.Errorf("failed to build resume extraction prompt: %w", pErr)
	}

	var schema map[string]any
	var schemaName string
	if supportsStructured {
		schema = ResumeExtractResponseSchema()
		schemaName = "resume_extract_response"
	}

	rawResponse, usage, err := e.completer.Complete(ctx, "ExtractResume", prompts.SystemPrompt, prompts.UserPrompt, provider, model, schemaName, schema)
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

// Compile-time interface assertions for extraction types and adapters.
var (
	_ core.ResumeExtractor = (*ResumeExtractor)(nil)
	_ core.ModelEnumerator = (*ResumeExtractor)(nil)
	_ Completer            = (*RegistryCompleter)(nil)
	_ Completer            = (CompleterFunc)(nil)
)
