package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/privacy"
)

// ServiceAdapter adapts an LLM Registry into the core.LLMService and branch domain service interfaces,
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
func (a *ServiceAdapter) GenerateTailoredResume(ctx context.Context, req core.GenerationRequest) (*core.GenerationResponse, error) {
	if a == nil || a.registry == nil {
		return nil, ErrNoProviders
	}
	var profile *core.Profile
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
//   - Populated and normalized *core.GapReport, or an error.
func (a *ServiceAdapter) GenerateGapAnalysis(ctx context.Context, req core.GenerationRequest) (*core.GapReport, error) {
	if a == nil || a.registry == nil {
		return nil, ErrNoProviders
	}
	var profile *core.Profile
	if req.MainProfile != nil {
		profile = &req.MainProfile.Profile
	}
	provider, model, err := a.registry.Resolve(req, req.Preset, profile)
	if err != nil {
		return nil, err
	}
	req.Model = model

	if canExecuteProviderCompletion(provider) {
		supportsStructured := supportsNativeStructuredCall(provider, model)
		prompts, pErr := BuildGapAnalysisPromptWithOptions(req, PromptOptions{NativeStructuredOutput: supportsStructured})
		if pErr != nil {
			return nil, pErr
		}
		text, _, cErr := (structuredCall{
			Provider:       provider,
			Operation:      "GenerateGapAnalysis",
			Model:          model,
			Prompts:        prompts,
			Schema:         GapAnalysisResponseSchema(),
			SchemaName:     "gap_analysis_response",
			ReasoningLevel: req.ReasoningLevel,
		}).execute(ctx)
		if cErr != nil {
			return nil, cErr
		}

		cleaned := ExtractJSON(text)
		var wrapper struct {
			GapReport *core.GapReport `json:"gap_report"`
		}
		if err := json.Unmarshal([]byte(cleaned), &wrapper); err == nil && wrapper.GapReport != nil {
			session := privacy.NewSessionFromRequest(req)
			session.DeobfuscateGapReport(wrapper.GapReport)
			wrapper.GapReport.Normalize()
			return wrapper.GapReport, nil
		}
		var direct core.GapReport
		if err := json.Unmarshal([]byte(cleaned), &direct); err == nil && (direct.OverallMatchSummary != "" || len(direct.MissingRequirements) > 0 || len(direct.IdentifiedGaps) > 0) {
			session := privacy.NewSessionFromRequest(req)
			session.DeobfuscateGapReport(&direct)
			direct.Normalize()
			return &direct, nil
		}
	}

	// Fallback to full generation if lightweight analysis is unavailable or returns unrecognized output
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
	ExtractionConfidenceScore float64                    `json:"extraction_confidence_score"`
	ExtractionSummary         string                     `json:"extraction_summary"`
	ExtractedCandidate        core.ATSExtractedCandidate `json:"extracted_candidate"`
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
func (a *ServiceAdapter) SimulateATS(ctx context.Context, req core.ATSSimulationRequest) (*core.ATSSimulationResponse, error) {
	if a == nil || a.registry == nil {
		return nil, ErrNoProviders
	}
	formatUsed, err := core.NormalizeATSFormat(req.FormatUsed)
	if err != nil {
		return nil, err
	}
	req.FormatUsed = formatUsed

	var profile *core.Profile
	if req.MainProfile != nil {
		profile = &req.MainProfile.Profile
	}

	genReq := core.GenerationRequest{
		Model:          req.Model,
		ReasoningLevel: req.ReasoningLevel,
		MainProfile:    req.MainProfile,
		BranchContext:  req.BranchContext,
	}

	provider, model, err := a.registry.Resolve(genReq, nil, profile)
	if err != nil {
		return nil, err
	}

	supportsStructured := supportsNativeStructuredCall(provider, model)
	promptOpts := PromptOptions{NativeStructuredOutput: supportsStructured}

	// Stage 1: ATS Content Extraction & Extraction Confidence Score
	extractPrompts, session, err := BuildATSExtractionPromptWithOptions(req, promptOpts)
	if err != nil {
		return nil, err
	}

	rawExtract, usage1, err := (structuredCall{
		Provider:       provider,
		Operation:      "ATSExtraction",
		Model:          model,
		Prompts:        extractPrompts,
		Schema:         ATSExtractionResponseSchema(),
		SchemaName:     "ats_extraction_response",
		ReasoningLevel: req.ReasoningLevel,
	}).execute(ctx)
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
	matchPrompts, err := BuildATSMatchingPromptWithOptions(req.BranchContext, &parsedExtract.ExtractedCandidate, session, promptOpts)
	if err != nil {
		return nil, err
	}

	rawMatch, usage2, err := (structuredCall{
		Provider:       provider,
		Operation:      "ATSMatching",
		Model:          model,
		Prompts:        matchPrompts,
		Schema:         ATSMatchingResponseSchema(),
		SchemaName:     "ats_matching_response",
		ReasoningLevel: req.ReasoningLevel,
	}).execute(ctx)
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
	atsScore := core.ATSScore{
		ExtractionConfidenceScore: core.ConvertATSRatioToPercent(parsedExtract.ExtractionConfidenceScore),
		JobMatchingScore:          core.ConvertATSRatioToPercent(parsedMatch.JobMatchingScore),
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

	totalUsage := core.TokenUsage{
		PromptTokens:     usage1.PromptTokens + usage2.PromptTokens,
		CompletionTokens: usage1.CompletionTokens + usage2.CompletionTokens,
		TotalTokens:      usage1.TotalTokens + usage2.TotalTokens,
	}

	return &core.ATSSimulationResponse{
		Score: atsScore,
		Usage: totalUsage,
	}, nil
}
