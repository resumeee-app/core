package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/branch"
	"github.com/resumeee-app/resumeee-core/sanitizer"
	"github.com/resumeee-app/resumeee-core/worker"
)

// Service orchestrates prompt compilation, PII sanitization/restoration, and structured
// completion execution for tailoring, qualification gap analysis, and ATS simulation.
type Service struct {
	registry *Registry
}

// NewService constructs a Service backed by the provided LLM Client Registry.
func NewService(registry *Registry) *Service {
	return &Service{registry: registry}
}

// GenerateTailoredResume executes an end-to-end resume tailoring pass using the resolved Client.
func (s *Service) GenerateTailoredResume(ctx context.Context, req core.GenerationRequest) (*core.GenerationResponse, error) {
	if s == nil || s.registry == nil {
		return nil, ErrNoProviders
	}
	client, model, err := s.registry.Resolve(req, req.Preset, requestProfile(req))
	if err != nil {
		return nil, err
	}
	req.Model = model
	return s.generateTailoredWithClient(ctx, client, model, req)
}

// generateTailoredWithClient compiles the tailoring prompt, executes completion, and deobfuscates PII placeholders.
func (s *Service) generateTailoredWithClient(ctx context.Context, client Client, model string, req core.GenerationRequest) (*core.GenerationResponse, error) {
	supportsStructured := client.SupportsStructuredOutput(model)
	prompts, err := BuildPromptWithOptions(req, PromptOptions{NativeStructuredOutput: supportsStructured})
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	rawText, usage, err := s.registry.CompleteStructured(ctx, client, "GenerateTailoredResume", Request{
		Model:          model,
		SystemPrompt:   prompts.SystemPrompt,
		UserPrompt:     prompts.UserPrompt,
		ResponseSchema: GenerationResponseSchema(),
		SchemaName:     "tailored_resume_response",
		ReasoningLevel: req.ReasoningLevel,
	})
	if err != nil {
		return nil, err
	}

	parsed, err := parseTailoredGenerationResponse(rawText, usage)
	if err != nil {
		return nil, err
	}

	sanitizer.NewSessionFromRequest(req).DeobfuscateGenerationResponse(parsed)
	return parsed, nil
}

// parseTailoredGenerationResponse unmarshals a tailored resume payload and backfills transport usage metrics.
func parseTailoredGenerationResponse(rawText string, usage core.TokenUsage) (*core.GenerationResponse, error) {
	cleanJSON := ExtractJSON(rawText)
	if strings.TrimSpace(cleanJSON) == "" {
		return nil, fmt.Errorf("%w: failed to unmarshal generation response: empty JSON output", ErrInvalidResponse)
	}

	var parsed core.GenerationResponse
	if err := json.Unmarshal([]byte(cleanJSON), &parsed); err != nil {
		return nil, fmt.Errorf("%w: failed to unmarshal generation response: %v", ErrInvalidResponse, err)
	}

	hasTransportUsage := usage.TotalTokens > 0 || usage.PromptTokens > 0 || usage.CompletionTokens > 0
	if parsed.Usage.TotalTokens == 0 && hasTransportUsage {
		parsed.Usage = usage
	}
	return &parsed, nil
}

// GenerateGapAnalysis performs lightweight qualification gap evaluation before falling back
// to full tailoring if a provider emits empty or unrecognized JSON.
func (s *Service) GenerateGapAnalysis(ctx context.Context, req core.GenerationRequest) (*core.GapReport, error) {
	if s == nil || s.registry == nil {
		return nil, ErrNoProviders
	}
	client, model, err := s.registry.Resolve(req, req.Preset, requestProfile(req))
	if err != nil {
		return nil, err
	}
	req.Model = model

	prompts, err := BuildGapAnalysisPromptWithOptions(req, PromptOptions{
		NativeStructuredOutput: client.SupportsStructuredOutput(model),
	})
	if err != nil {
		return nil, err
	}

	text, _, err := s.registry.CompleteStructured(ctx, client, "GenerateGapAnalysis", Request{
		Model:          model,
		SystemPrompt:   prompts.SystemPrompt,
		UserPrompt:     prompts.UserPrompt,
		ResponseSchema: GapAnalysisResponseSchema(),
		SchemaName:     "gap_analysis_response",
		ReasoningLevel: req.ReasoningLevel,
	})
	if err != nil {
		return nil, err
	}

	session := sanitizer.NewSessionFromRequest(req)
	if report := parseGapReport(text, session); report != nil {
		return report, nil
	}

	// Fallback to full generation only when lightweight analysis produces empty or unrecognized output.
	resp, err := s.generateTailoredWithClient(ctx, client, model, req)
	if err != nil {
		return nil, err
	}
	resp.GapReport.Normalize()
	return &resp.GapReport, nil
}

// parseGapReport attempts to decode either a wrapped or unwrapped GapReport JSON payload.
func parseGapReport(raw string, session *sanitizer.Session) *core.GapReport {
	cleaned := strings.TrimSpace(ExtractJSON(raw))
	if cleaned == "" {
		return nil
	}

	var wrapper struct {
		GapReport *core.GapReport `json:"gap_report"`
	}
	if err := json.Unmarshal([]byte(cleaned), &wrapper); err == nil && wrapper.GapReport != nil {
		deobfuscateAndNormalizeGap(wrapper.GapReport, session)
		return wrapper.GapReport
	}

	var direct core.GapReport
	err := json.Unmarshal([]byte(cleaned), &direct)
	hasReportContent := direct.OverallMatchSummary != "" || len(direct.MissingRequirements) > 0 || len(direct.IdentifiedGaps) > 0
	if err == nil && hasReportContent {
		deobfuscateAndNormalizeGap(&direct, session)
		return &direct
	}
	return nil
}

// deobfuscateAndNormalizeGap restores PII placeholders on report and applies canonical normalization.
func deobfuscateAndNormalizeGap(report *core.GapReport, session *sanitizer.Session) {
	if session != nil {
		session.DeobfuscateGapReport(report)
	}
	report.Normalize()
}

// atsExtractionLLMOutput decodes the Stage 1 ATS extraction response JSON payload.
type atsExtractionLLMOutput struct {
	ExtractionConfidenceScore float64                    `json:"extraction_confidence_score"`
	ExtractionSummary         string                     `json:"extraction_summary"`
	ExtractedCandidate        core.ATSExtractedCandidate `json:"extracted_candidate"`
}

// atsMatchingLLMOutput decodes the Stage 2 ATS job matching response JSON payload.
type atsMatchingLLMOutput struct {
	JobMatchingScore float64 `json:"job_matching_score"`
	MatchingSummary  string  `json:"matching_summary"`
}

// SimulateATS executes the two-stage ATS perception simulation for a rendered resume version.
func (s *Service) SimulateATS(ctx context.Context, req core.ATSSimulationRequest) (*core.ATSSimulationResponse, error) {
	if s == nil || s.registry == nil {
		return nil, ErrNoProviders
	}

	formatUsed, err := core.NormalizeATSFormat(req.FormatUsed)
	if err != nil {
		return nil, err
	}
	req.FormatUsed = formatUsed

	client, model, err := s.resolveATSClient(req)
	if err != nil {
		return nil, err
	}
	promptOpts := PromptOptions{NativeStructuredOutput: client.SupportsStructuredOutput(model)}

	extractOut, session, usage1, err := s.runATSExtractionStage(ctx, client, model, req, promptOpts)
	if err != nil {
		return nil, err
	}

	matchOut, usage2, err := s.runATSMatchingStage(ctx, client, model, req, &extractOut.ExtractedCandidate, session, promptOpts)
	if err != nil {
		return nil, err
	}

	return &core.ATSSimulationResponse{
		Score: buildATSScore(formatUsed, extractOut, matchOut, session),
		Usage: core.TokenUsage{
			PromptTokens:     usage1.PromptTokens + usage2.PromptTokens,
			CompletionTokens: usage1.CompletionTokens + usage2.CompletionTokens,
			TotalTokens:      usage1.TotalTokens + usage2.TotalTokens,
		},
	}, nil
}

// resolveATSClient selects the Client and model identifier for an ATS simulation request.
func (s *Service) resolveATSClient(req core.ATSSimulationRequest) (Client, string, error) {
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
	return s.registry.Resolve(genReq, nil, profile)
}

// runATSExtractionStage compiles Stage 1 prompts and extracts candidate attributes from resume text.
func (s *Service) runATSExtractionStage(ctx context.Context, client Client, model string, req core.ATSSimulationRequest, opts PromptOptions) (*atsExtractionLLMOutput, *sanitizer.Session, core.TokenUsage, error) {
	prompts, session, err := BuildATSExtractionPromptWithOptions(req, opts)
	if err != nil {
		return nil, nil, core.TokenUsage{}, err
	}

	rawExtract, usage, err := s.registry.CompleteStructured(ctx, client, "ATSExtraction", Request{
		Model:          model,
		SystemPrompt:   prompts.SystemPrompt,
		UserPrompt:     prompts.UserPrompt,
		ResponseSchema: ATSExtractionResponseSchema(),
		SchemaName:     "ats_extraction_response",
		ReasoningLevel: req.ReasoningLevel,
	})
	if err != nil {
		return nil, nil, core.TokenUsage{}, fmt.Errorf("ats extraction failed: %w", err)
	}

	cleaned := ExtractJSON(rawExtract)
	if strings.TrimSpace(cleaned) == "" {
		return nil, nil, core.TokenUsage{}, fmt.Errorf("%w: empty JSON in ATS extraction response", ErrInvalidResponse)
	}

	var parsed atsExtractionLLMOutput
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return nil, nil, core.TokenUsage{}, fmt.Errorf("%w: failed to parse ATS extraction response: %v", ErrInvalidResponse, err)
	}
	parsed.ExtractedCandidate.Normalize()
	return &parsed, session, usage, nil
}

// runATSMatchingStage compiles Stage 2 prompts using only the extracted candidate profile from Stage 1.
func (s *Service) runATSMatchingStage(ctx context.Context, client Client, model string, req core.ATSSimulationRequest, candidate *core.ATSExtractedCandidate, session *sanitizer.Session, opts PromptOptions) (*atsMatchingLLMOutput, core.TokenUsage, error) {
	prompts, err := BuildATSMatchingPromptWithOptions(req.BranchContext, candidate, session, opts)
	if err != nil {
		return nil, core.TokenUsage{}, err
	}

	rawMatch, usage, err := s.registry.CompleteStructured(ctx, client, "ATSMatching", Request{
		Model:          model,
		SystemPrompt:   prompts.SystemPrompt,
		UserPrompt:     prompts.UserPrompt,
		ResponseSchema: ATSMatchingResponseSchema(),
		SchemaName:     "ats_matching_response",
		ReasoningLevel: req.ReasoningLevel,
	})
	if err != nil {
		return nil, core.TokenUsage{}, fmt.Errorf("ats matching failed: %w", err)
	}

	cleaned := ExtractJSON(rawMatch)
	if strings.TrimSpace(cleaned) == "" {
		return nil, core.TokenUsage{}, fmt.Errorf("%w: empty JSON in ATS matching response", ErrInvalidResponse)
	}

	var parsed atsMatchingLLMOutput
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return nil, core.TokenUsage{}, fmt.Errorf("%w: failed to parse ATS matching response: %v", ErrInvalidResponse, err)
	}
	return &parsed, usage, nil
}

// buildATSScore combines Stage 1 extraction and Stage 2 matching outputs into a normalized core.ATSScore.
func buildATSScore(formatUsed string, extract *atsExtractionLLMOutput, match *atsMatchingLLMOutput, session *sanitizer.Session) core.ATSScore {
	now := time.Now().UTC()
	candidateCopy := extract.ExtractedCandidate
	score := core.ATSScore{
		ExtractionConfidenceScore: core.ConvertATSRatioToPercent(extract.ExtractionConfidenceScore),
		JobMatchingScore:          core.ConvertATSRatioToPercent(match.JobMatchingScore),
		FormatUsed:                formatUsed,
		ExtractionSummary:         strings.TrimSpace(extract.ExtractionSummary),
		MatchingSummary:           strings.TrimSpace(match.MatchingSummary),
		ExtractedCandidate:        &candidateCopy,
		EvaluatedAt:               &now,
	}
	if session != nil {
		session.DeobfuscateATSScore(&score)
	}
	score.Normalize()
	return score
}

// requestProfile extracts a pointer to req.MainProfile.Profile when a main profile snapshot is present.
func requestProfile(req core.GenerationRequest) *core.Profile {
	if req.MainProfile == nil {
		return nil
	}
	return &req.MainProfile.Profile
}

// Compile-time assertions verifying Service satisfies both branch and worker generation seams.
var (
	_ branch.Generator = (*Service)(nil)
	_ worker.Generator = (*Service)(nil)
)
