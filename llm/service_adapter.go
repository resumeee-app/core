// Package llm provides LLM provider integrations, registry routing, prompt templates, and generation adapters.
package llm

import (
	"context"
	"encoding/json"

	"resumeee/internal/domain"
)

// ServiceAdapter adapts an LLM Registry into the worker.LLMService and branch domain service interfaces,
// providing unified LLM tailoring and decoupled qualification gap analysis capabilities across server and CLI binaries.
type ServiceAdapter struct {
	registry *Registry
}

// NewServiceAdapter constructs a new ServiceAdapter wrapping the provided provider Registry.
//
// Parameters:
//   - registry: Pointer to an initialized LLM provider Registry.
//
// Returns:
//   - Configured *ServiceAdapter.
func NewServiceAdapter(registry *Registry) *ServiceAdapter {
	return &ServiceAdapter{registry: registry}
}

// GenerateTailoredResume resolves the target LLM provider based on request presets and model configuration,
// executing resume tailoring against the candidate's master profile.
//
// Parameters:
//   - ctx: Request context with cancellation and timeout propagation.
//   - req: GenerationRequest containing master profile, branch context, and target requirements.
//
// Returns:
//   - GenerationResponse containing tailored content, citations, and gap report, or an error.
func (a *ServiceAdapter) GenerateTailoredResume(ctx context.Context, req domain.GenerationRequest) (*domain.GenerationResponse, error) {
	var profile *domain.Profile
	if req.MasterProfile != nil {
		profile = &req.MasterProfile.Profile
	}
	provider, model, err := a.registry.Resolve(req, req.Preset, profile)
	if err != nil {
		return nil, err
	}
	req.Model = model
	return provider.GenerateTailoredResume(ctx, req)
}

// GenerateGapAnalysis performs a lightweight, decoupled ATS qualification and keyword gap analysis
// between the candidate profile and target job requirements without incurring full resume generation overhead.
//
// Parameters:
//   - ctx: Request context with cancellation and timeout propagation.
//   - req: GenerationRequest containing master profile and job requirements.
//
// Returns:
//   - Populated and normalized *domain.GapReport, or an error.
func (a *ServiceAdapter) GenerateGapAnalysis(ctx context.Context, req domain.GenerationRequest) (*domain.GapReport, error) {
	var profile *domain.Profile
	if req.MasterProfile != nil {
		profile = &req.MasterProfile.Profile
	}
	provider, model, err := a.registry.Resolve(req, req.Preset, profile)
	if err != nil {
		return nil, err
	}
	req.Model = model

	// If provider implements RawCompleter, run the lightweight gap analysis prompt
	if rawCompleter, ok := provider.(RawCompleter); ok {
		prompts, pErr := BuildGapAnalysisPrompt(req)
		if pErr == nil {
			text, cErr := rawCompleter.CompleteText(ctx, prompts.SystemPrompt, prompts.UserPrompt, model)
			if cErr == nil {
				cleaned := ExtractJSON(text)
				var wrapper struct {
					GapReport *domain.GapReport `json:"gap_report"`
				}
				if err := json.Unmarshal([]byte(cleaned), &wrapper); err == nil && wrapper.GapReport != nil {
					wrapper.GapReport.Normalize()
					return wrapper.GapReport, nil
				}
				var direct domain.GapReport
				if err := json.Unmarshal([]byte(cleaned), &direct); err == nil && (direct.OverallMatchSummary != "" || len(direct.MissingRequirements) > 0 || len(direct.IdentifiedGaps) > 0) {
					direct.Normalize()
					return &direct, nil
				}
			}
		}
	}

	// Fallback to full generation if lightweight analysis is unavailable or fails
	resp, err := provider.GenerateTailoredResume(ctx, req)
	if err != nil {
		return nil, err
	}
	resp.GapReport.Normalize()
	return &resp.GapReport, nil
}
