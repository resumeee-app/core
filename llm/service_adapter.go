package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core/domain"
	"github.com/resumeee-app/resumeee-core/privacy"
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
// executing resume tailoring against the candidate's main profile.
//
// Parameters:
//   - ctx: Request context with cancellation and timeout propagation.
//   - req: GenerationRequest containing main profile, branch context, and target requirements.
//
// Returns:
//   - GenerationResponse containing tailored content, citations, and gap report, or an error.
func (a *ServiceAdapter) GenerateTailoredResume(ctx context.Context, req domain.GenerationRequest) (*domain.GenerationResponse, error) {
	var profile *domain.Profile
	if req.MainProfile != nil {
		profile = &req.MainProfile.Profile
	}
	provider, model, err := a.registry.Resolve(req, req.Preset, profile)
	if err != nil {
		return nil, err
	}
	req.Model = model
	resp, err := provider.GenerateTailoredResume(ctx, req)
	if err != nil {
		return nil, err
	}
	session := privacy.NewSessionFromRequest(req)
	session.DeobfuscateGenerationResponse(resp)
	return resp, nil
}

// GenerateGapAnalysis performs a lightweight, decoupled ATS qualification and keyword gap analysis
// between the candidate profile and target job requirements without incurring full resume generation overhead.
//
// Parameters:
//   - ctx: Request context with cancellation and timeout propagation.
//   - req: GenerationRequest containing main profile and job requirements.
//
// Returns:
//   - Populated and normalized *domain.GapReport, or an error.
func (a *ServiceAdapter) GenerateGapAnalysis(ctx context.Context, req domain.GenerationRequest) (*domain.GapReport, error) {
	var profile *domain.Profile
	if req.MainProfile != nil {
		profile = &req.MainProfile.Profile
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
					session := privacy.NewSessionFromRequest(req)
					session.DeobfuscateGapReport(wrapper.GapReport)
					wrapper.GapReport.Normalize()
					return wrapper.GapReport, nil
				}
				var direct domain.GapReport
				if err := json.Unmarshal([]byte(cleaned), &direct); err == nil && (direct.OverallMatchSummary != "" || len(direct.MissingRequirements) > 0 || len(direct.IdentifiedGaps) > 0) {
					session := privacy.NewSessionFromRequest(req)
					session.DeobfuscateGapReport(&direct)
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
	session := privacy.NewSessionFromRequest(req)
	session.DeobfuscateGenerationResponse(resp)
	resp.GapReport.Normalize()
	return &resp.GapReport, nil
}

type atsExtractionLLMOutput struct {
	ExtractionConfidenceScore float64                      `json:"extraction_confidence_score"`
	ExtractionSummary         string                       `json:"extraction_summary"`
	ExtractedCandidate        domain.ATSExtractedCandidate `json:"extracted_candidate"`
}

type atsMatchingLLMOutput struct {
	JobMatchingScore float64 `json:"job_matching_score"`
	MatchingSummary  string  `json:"matching_summary"`
}

// SimulateATS executes the two-stage ATS perception simulation for a rendered resume version:
//  1. Stage 1 extracts and categorizes content from the text-based resume (HTML, Markdown, or plain text),
//     producing an Extraction Confidence Score from 0 to 1 (converted to 0..100%).
//  2. Stage 2 evaluates only the extracted candidate profile from Stage 1 against the job description,
//     producing a Job Matching Score from 0 to 1 (converted to 0..100%).
func (a *ServiceAdapter) SimulateATS(ctx context.Context, req domain.ATSSimulationRequest) (*domain.ATSSimulationResponse, error) {
	formatUsed, err := domain.NormalizeATSFormat(req.FormatUsed)
	if err != nil {
		return nil, err
	}
	req.FormatUsed = formatUsed

	var profile *domain.Profile
	if req.MainProfile != nil {
		profile = &req.MainProfile.Profile
	}

	genReq := domain.GenerationRequest{
		Model:          req.Model,
		ReasoningLevel: req.ReasoningLevel,
		MainProfile:    req.MainProfile,
		BranchContext:  req.BranchContext,
	}

	provider, model, err := a.registry.Resolve(genReq, nil, profile)
	if err != nil {
		return nil, err
	}

	completeStep := func(systemPrompt, userPrompt string) (string, domain.TokenUsage, error) {
		if rawUsage, ok := provider.(RawUsageCompleter); ok {
			return rawUsage.CompleteTextWithUsage(ctx, systemPrompt, userPrompt, model)
		}
		if raw, ok := provider.(RawCompleter); ok {
			text, cErr := raw.CompleteText(ctx, systemPrompt, userPrompt, model)
			return text, domain.TokenUsage{}, cErr
		}
		return "", domain.TokenUsage{}, fmt.Errorf("%w: provider %s does not support raw completion for ATS simulation", ErrInvalidResponse, provider.Name())
	}

	// Stage 1: ATS Content Extraction & Extraction Confidence Score
	extractPrompts, session, err := BuildATSExtractionPrompt(req)
	if err != nil {
		return nil, err
	}

	rawExtract, usage1, err := completeStep(extractPrompts.SystemPrompt, extractPrompts.UserPrompt)
	if err != nil {
		return nil, fmt.Errorf("ats extraction failed: %w", err)
	}

	cleanedExtract := ExtractJSON(rawExtract)
	if strings.TrimSpace(cleanedExtract) == "" {
		return nil, fmt.Errorf("%w: empty JSON in ATS extraction response", ErrInvalidResponse)
	}

	var parsedExtract atsExtractionLLMOutput
	if err := json.Unmarshal([]byte(cleanedExtract), &parsedExtract); err != nil {
		return nil, fmt.Errorf("%w: failed to parse ATS extraction response: %v", ErrInvalidResponse, err)
	}
	parsedExtract.ExtractedCandidate.Normalize()

	// Stage 2: Perceived Job Matching Score using ONLY the extracted candidate data
	matchPrompts, err := BuildATSMatchingPrompt(req.BranchContext, &parsedExtract.ExtractedCandidate, session)
	if err != nil {
		return nil, err
	}

	rawMatch, usage2, err := completeStep(matchPrompts.SystemPrompt, matchPrompts.UserPrompt)
	if err != nil {
		return nil, fmt.Errorf("ats matching failed: %w", err)
	}

	cleanedMatch := ExtractJSON(rawMatch)
	if strings.TrimSpace(cleanedMatch) == "" {
		return nil, fmt.Errorf("%w: empty JSON in ATS matching response", ErrInvalidResponse)
	}

	var parsedMatch atsMatchingLLMOutput
	if err := json.Unmarshal([]byte(cleanedMatch), &parsedMatch); err != nil {
		return nil, fmt.Errorf("%w: failed to parse ATS matching response: %v", ErrInvalidResponse, err)
	}

	now := time.Now().UTC()
	candidateCopy := parsedExtract.ExtractedCandidate
	atsScore := domain.ATSScore{
		ExtractionConfidenceScore: domain.ConvertATSRatioToPercent(parsedExtract.ExtractionConfidenceScore),
		JobMatchingScore:          domain.ConvertATSRatioToPercent(parsedMatch.JobMatchingScore),
		FormatUsed:                formatUsed,
		ExtractionSummary:         strings.TrimSpace(parsedExtract.ExtractionSummary),
		MatchingSummary:           strings.TrimSpace(parsedMatch.MatchingSummary),
		ExtractedCandidate:        &candidateCopy,
		EvaluatedAt:               &now,
	}

	if session != nil {
		session.DeobfuscateATSScore(&atsScore)
	}
	atsScore.Normalize()

	totalUsage := domain.TokenUsage{
		PromptTokens:     usage1.PromptTokens + usage2.PromptTokens,
		CompletionTokens: usage1.CompletionTokens + usage2.CompletionTokens,
		TotalTokens:      usage1.TotalTokens + usage2.TotalTokens,
	}

	return &domain.ATSSimulationResponse{
		Score: atsScore,
		Usage: totalUsage,
	}, nil
}

