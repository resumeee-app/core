package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"resumeee/internal/domain"
	"resumeee/internal/profile"
)

// ResumeExtractor abstracts LLM-driven unstructured text extraction into standard JSONResume.
type ResumeExtractor interface {
	ExtractResume(ctx context.Context, text string, provider string, model string) (*profile.JSONResume, error)
}

// UsageResumeExtractor extends ResumeExtractor with token usage reporting.
type UsageResumeExtractor interface {
	ResumeExtractor
	ExtractResumeWithUsage(ctx context.Context, text string, provider string, model string) (*profile.JSONResume, domain.TokenUsage, error)
}

// Completer abstracts text-based completion for LLM extraction tasks.
type Completer interface {
	Complete(ctx context.Context, systemPrompt, userPrompt, provider, model string) (string, error)
}

// UsageCompleter extends Completer with token usage reporting.
type UsageCompleter interface {
	Completer
	CompleteWithUsage(ctx context.Context, systemPrompt, userPrompt, provider, model string) (string, domain.TokenUsage, error)
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
	CompleteTextWithUsage(ctx context.Context, systemPrompt, userPrompt, model string) (string, domain.TokenUsage, error)
}

// ExtractResumeWithTokenUsage extracts a structured resume using the extractor and returns the parsed resume,
// token usage (defaulting to defaultTokens if reported as 0), and any error encountered.
func ExtractResumeWithTokenUsage(ctx context.Context, extractor ResumeExtractor, text, provider, model string, defaultTokens int) (*profile.JSONResume, domain.TokenUsage, error) {
	var (
		extracted *profile.JSONResume
		usage     domain.TokenUsage
		err       error
	)
	if ue, ok := extractor.(UsageResumeExtractor); ok {
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
func NewResumeExtractor(completer Completer) ResumeExtractor {
	return &resumeExtractor{
		completer: completer,
	}
}

// NewDefaultResumeExtractor creates a ResumeExtractor backed by the provider Registry.
func NewDefaultResumeExtractor(registry *Registry) ResumeExtractor {
	return &resumeExtractor{
		completer: NewRegistryCompleter(registry),
	}
}

// AvailableModels returns available configured models if the completer is backed by a Registry.
func (e *resumeExtractor) AvailableModels() []domain.ModelOption {
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
func (rc *registryCompleter) CompleteWithUsage(ctx context.Context, systemPrompt, userPrompt, provider, model string) (string, domain.TokenUsage, error) {
	if rc.registry == nil {
		return "", domain.TokenUsage{}, ErrNoProviders
	}

	targetProvider := provider
	if strings.TrimSpace(targetProvider) == "" {
		targetProvider = rc.registry.DefaultProvider()
	}
	if strings.TrimSpace(targetProvider) == "" {
		return "", domain.TokenUsage{}, ErrNoProviders
	}

	p, err := rc.registry.Get(targetProvider)
	if err != nil {
		return "", domain.TokenUsage{}, err
	}

	targetModel := model
	if strings.TrimSpace(targetModel) == "" {
		if strings.EqualFold(targetProvider, rc.registry.DefaultProvider()) {
			targetModel = rc.registry.DefaultModel()
		} else {
			targetModel = defaultModelForProvider(targetProvider)
		}
	}

	if rawUsage, ok := p.(RawUsageCompleter); ok {
		return rawUsage.CompleteTextWithUsage(ctx, systemPrompt, userPrompt, targetModel)
	}

	if raw, ok := p.(RawCompleter); ok {
		text, err := raw.CompleteText(ctx, systemPrompt, userPrompt, targetModel)
		return text, domain.TokenUsage{}, err
	}

	return "", domain.TokenUsage{}, fmt.Errorf("%w: provider %s does not support raw completion", ErrInvalidResponse, p.Name())
}

// ExtractResume parses unstructured resume text into a structured JSONResume document.
func (e *resumeExtractor) ExtractResume(ctx context.Context, text string, provider string, model string) (*profile.JSONResume, error) {
	res, _, err := e.ExtractResumeWithUsage(ctx, text, provider, model)
	return res, err
}

// ExtractResumeWithUsage parses unstructured resume text into a structured JSONResume document and reports token usage.
func (e *resumeExtractor) ExtractResumeWithUsage(ctx context.Context, text string, provider string, model string) (*profile.JSONResume, domain.TokenUsage, error) {
	if err := ctx.Err(); err != nil {
		return nil, domain.TokenUsage{}, err
	}

	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return nil, domain.TokenUsage{}, fmt.Errorf("%w: empty resume text", domain.ErrValidation)
	}

	if e.completer == nil {
		return nil, domain.TokenUsage{}, fmt.Errorf("%w: no completer configured", domain.ErrValidation)
	}

	prompts, err := BuildResumeExtractPrompt(trimmedText)
	if err != nil {
		return nil, domain.TokenUsage{}, fmt.Errorf("failed to build resume extraction prompt: %w", err)
	}

	var rawResponse string
	var usage domain.TokenUsage
	if uc, ok := e.completer.(UsageCompleter); ok {
		rawResponse, usage, err = uc.CompleteWithUsage(ctx, prompts.SystemPrompt, prompts.UserPrompt, provider, model)
	} else {
		rawResponse, err = e.completer.Complete(ctx, prompts.SystemPrompt, prompts.UserPrompt, provider, model)
	}
	if err != nil {
		return nil, domain.TokenUsage{}, err
	}

	if err := ctx.Err(); err != nil {
		return nil, domain.TokenUsage{}, err
	}

	cleanedJSON := ExtractJSON(rawResponse)
	if strings.TrimSpace(cleanedJSON) == "" {
		return nil, domain.TokenUsage{}, fmt.Errorf("%w: empty or missing JSON in LLM response", domain.ErrValidation)
	}

	var resume profile.JSONResume
	if err := json.Unmarshal([]byte(cleanedJSON), &resume); err != nil {
		return nil, domain.TokenUsage{}, fmt.Errorf("%w: failed to parse structured LLM response: %v", domain.ErrValidation, err)
	}

	return &resume, usage, nil
}
