package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/resumeee-app/core"
	"github.com/resumeee-app/core/worker"
)

// CompletionRequest bundles the parameters for a capability-aware Completer invocation.
type CompletionRequest struct {
	Operation    string
	SystemPrompt string
	UserPrompt   string
	Provider     string
	Model        string
	SchemaName   string
	Schema       *Schema
}

// Completer abstracts model catalog discovery and capability-aware completion for resume extraction.
type Completer interface {
	AvailableModels() []core.ModelOption
	SupportsStructuredCompletion(provider, model string) bool
	Complete(ctx context.Context, req CompletionRequest) (string, core.LLMTelemetry, error)
}

// CompleterFunc adapts a simple text completion function to satisfy Completer.
type CompleterFunc func(ctx context.Context, systemPrompt, userPrompt, provider, model string) (string, error)

// AvailableModels returns nil because a bare CompleterFunc carries no model catalog.
func (f CompleterFunc) AvailableModels() []core.ModelOption {
	return nil
}

// SupportsStructuredCompletion returns false because a bare CompleterFunc relies on inline schemas.
func (f CompleterFunc) SupportsStructuredCompletion(_, _ string) bool {
	return false
}

// Complete invokes the wrapped function and returns zero telemetry unless error telemetry is attached.
func (f CompleterFunc) Complete(ctx context.Context, req CompletionRequest) (string, core.LLMTelemetry, error) {
	if f == nil {
		return "", core.LLMTelemetry{}, fmt.Errorf("%w: no completer configured", core.ErrValidation)
	}
	text, err := f(ctx, req.SystemPrompt, req.UserPrompt, req.Provider, req.Model)
	if err != nil {
		return "", core.ResolveTelemetry(core.LLMTelemetry{}, err), err
	}
	return text, core.LLMTelemetry{}, nil
}

// ExtractResumeWithTelemetry extracts a structured resume using extractor and applies defaultTokens
// when a successful extraction reports zero usage. A nil extractor fails closed with core.ErrValidation.
func ExtractResumeWithTelemetry(ctx context.Context, extractor worker.ResumeExtractor, text, provider, model string, defaultTokens int) (*core.JSONResume, core.LLMTelemetry, error) {
	if extractor == nil {
		return nil, core.LLMTelemetry{}, fmt.Errorf("%w: no resume extractor configured", core.ErrValidation)
	}
	extracted, telemetry, err := extractor.ExtractResume(ctx, text, provider, model)
	telemetry = core.ResolveTelemetry(telemetry, err)
	if err != nil {
		return nil, telemetry, core.AttachLLMTelemetry(err, telemetry)
	}
	telemetry.Usage = telemetry.Usage.WithFallback(defaultTokens)
	return extracted, telemetry, nil
}

// Extractor implements worker.ResumeExtractor and core.ModelEnumerator using an injected Completer.
type Extractor struct {
	completer Completer
}

// NewExtractor constructs an Extractor backed by completer.
func NewExtractor(completer Completer) *Extractor {
	return &Extractor{completer: completer}
}

// NewRegistryExtractor constructs an Extractor backed by a provider Registry.
func NewRegistryExtractor(registry *Registry) *Extractor {
	return &Extractor{completer: NewRegistryCompleter(registry)}
}

// AvailableModels returns the configured model catalog exposed by the underlying Completer.
func (e *Extractor) AvailableModels() []core.ModelOption {
	if e == nil || e.completer == nil {
		return nil
	}
	return e.completer.AvailableModels()
}

// ExtractResume parses unstructured resume text into a structured JSONResume and reports call telemetry.
func (e *Extractor) ExtractResume(ctx context.Context, text, provider, model string) (*core.JSONResume, core.LLMTelemetry, error) {
	if err := ctx.Err(); err != nil {
		return nil, core.LLMTelemetry{}, err
	}

	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return nil, core.LLMTelemetry{}, fmt.Errorf("%w: empty resume text", core.ErrValidation)
	}
	if e == nil || e.completer == nil {
		return nil, core.LLMTelemetry{}, fmt.Errorf("%w: no completer configured", core.ErrValidation)
	}

	supportsStructured := e.completer.SupportsStructuredCompletion(provider, model)
	prompts, err := BuildResumeExtractPromptWithOptions(trimmedText, PromptOptions{
		NativeStructuredOutput: supportsStructured,
	})
	if err != nil {
		return nil, core.LLMTelemetry{}, fmt.Errorf("failed to build resume extraction prompt: %w", err)
	}

	var schema *Schema
	var schemaName string
	if supportsStructured {
		schema = ResumeExtractResponseSchema()
		schemaName = "resume_extract_response"
	}

	rawResponse, telemetry, err := e.completer.Complete(ctx, CompletionRequest{
		Operation:    "ExtractResume",
		SystemPrompt: prompts.SystemPrompt,
		UserPrompt:   prompts.UserPrompt,
		Provider:     provider,
		Model:        model,
		SchemaName:   schemaName,
		Schema:       schema,
	})
	telemetry = core.ResolveTelemetry(telemetry, err)
	if err != nil {
		return nil, telemetry, core.AttachLLMTelemetry(err, telemetry)
	}
	if err := ctx.Err(); err != nil {
		return nil, telemetry, core.AttachLLMTelemetry(err, telemetry)
	}

	cleanedJSON := ExtractJSON(rawResponse)
	if strings.TrimSpace(cleanedJSON) == "" {
		parseErr := fmt.Errorf("%w: empty or missing JSON in LLM response", core.ErrValidation)
		return nil, telemetry, core.AttachLLMTelemetry(parseErr, telemetry)
	}

	var resume core.JSONResume
	if err := json.Unmarshal([]byte(cleanedJSON), &resume); err != nil {
		parseErr := fmt.Errorf("%w: failed to parse structured LLM response: %v", core.ErrValidation, err)
		return nil, telemetry, core.AttachLLMTelemetry(parseErr, telemetry)
	}
	return &resume, telemetry, nil
}

// RegistryCompleter adapts a Registry into a capability-aware Completer.
type RegistryCompleter struct {
	registry *Registry
}

// NewRegistryCompleter constructs a RegistryCompleter delegating to registry.
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

// resolveClientAndModel resolves the target Client and model identifier from the Registry.
func (rc *RegistryCompleter) resolveClientAndModel(provider, model string) (Client, string, error) {
	if rc == nil || rc.registry == nil {
		return nil, "", ErrNoProviders
	}
	return rc.registry.Resolve(core.GenerationRequest{
		Model:         model,
		BranchContext: core.Branch{ProviderOverride: provider},
	}, nil, nil)
}

// SupportsStructuredCompletion reports whether the resolved client and model support native structured outputs.
func (rc *RegistryCompleter) SupportsStructuredCompletion(provider, model string) bool {
	client, resolvedModel, err := rc.resolveClientAndModel(provider, model)
	if err != nil || client == nil {
		return false
	}
	return client.SupportsStructuredOutput(resolvedModel)
}

// Complete resolves the target client and dispatches a capability-aware completion request.
func (rc *RegistryCompleter) Complete(ctx context.Context, req CompletionRequest) (string, core.LLMTelemetry, error) {
	client, targetModel, err := rc.resolveClientAndModel(req.Provider, req.Model)
	if err != nil {
		return "", core.LLMTelemetry{}, err
	}
	return rc.registry.CompleteStructured(ctx, client, req.Operation, Request{
		Model:          targetModel,
		SystemPrompt:   req.SystemPrompt,
		UserPrompt:     req.UserPrompt,
		ResponseSchema: req.Schema,
		SchemaName:     req.SchemaName,
	})
}

// Compile-time interface assertions verifying Extractor and Completer implementations.
var (
	_ worker.ResumeExtractor = (*Extractor)(nil)
	_ core.ModelEnumerator   = (*Extractor)(nil)
	_ Completer              = (*RegistryCompleter)(nil)
	_ Completer              = (CompleterFunc)(nil)
)
