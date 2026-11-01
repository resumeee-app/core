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

// Completer abstracts text-based completion for LLM extraction tasks.
type Completer interface {
	Complete(ctx context.Context, systemPrompt, userPrompt, provider, model string) (string, error)
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

type registryCompleter struct {
	registry *Registry
}

// NewRegistryCompleter constructs a Completer that delegates to registered LLM providers.
func NewRegistryCompleter(registry *Registry) Completer {
	return &registryCompleter{registry: registry}
}

// Complete delegates raw text completion to the resolved LLM provider from the registry.
func (rc *registryCompleter) Complete(ctx context.Context, systemPrompt, userPrompt, provider, model string) (string, error) {
	if rc.registry == nil {
		return "", ErrNoProviders
	}

	targetProvider := provider
	if strings.TrimSpace(targetProvider) == "" {
		targetProvider = rc.registry.DefaultProvider()
	}
	if strings.TrimSpace(targetProvider) == "" {
		return "", ErrNoProviders
	}

	p, err := rc.registry.Get(targetProvider)
	if err != nil {
		return "", err
	}

	targetModel := model
	if strings.TrimSpace(targetModel) == "" {
		targetModel = rc.registry.DefaultModel()
	}

	if raw, ok := p.(RawCompleter); ok {
		return raw.CompleteText(ctx, systemPrompt, userPrompt, targetModel)
	}

	return "", fmt.Errorf("%w: provider %s does not support raw completion", ErrInvalidResponse, p.Name())
}

const resumeExtractionSystemPrompt = `You are an expert resume parsing engine. Your task is to extract all structured career information from the provided resume text into standard JSON Resume format (v1.0.0). Output strict, valid JSON matching the schema with basics (name, label, email, phone, url, summary, location), work (name/company, position, startDate, endDate, summary, highlights), education (institution, area, studyType, startDate, endDate, courses), skills (name, level, keywords), projects (name, description, highlights, keywords), certificates (name, issuer, date, url). Do not include any explanations or commentary outside of the JSON structure.`

// ExtractResume parses unstructured resume text into a structured JSONResume document.
func (e *resumeExtractor) ExtractResume(ctx context.Context, text string, provider string, model string) (*profile.JSONResume, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return nil, fmt.Errorf("%w: empty resume text", domain.ErrValidation)
	}

	if e.completer == nil {
		return nil, fmt.Errorf("%w: no completer configured", domain.ErrValidation)
	}

	userPrompt := "Extract the following resume text into standard JSON Resume format:\n\n" + trimmedText

	rawResponse, err := e.completer.Complete(ctx, resumeExtractionSystemPrompt, userPrompt, provider, model)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cleanedJSON := ExtractJSON(rawResponse)
	if strings.TrimSpace(cleanedJSON) == "" {
		return nil, fmt.Errorf("%w: empty or missing JSON in LLM response", domain.ErrValidation)
	}

	var resume profile.JSONResume
	if err := json.Unmarshal([]byte(cleanedJSON), &resume); err != nil {
		return nil, fmt.Errorf("%w: failed to parse structured LLM response: %v", domain.ErrValidation, err)
	}

	return &resume, nil
}
