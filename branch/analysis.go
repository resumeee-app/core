package branch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// Quota token budgets, settlement timeouts, and error truncation limits for analysis operations.
const (
	// gapAnalysisEstimatedTokens is reserved before dispatching gap analysis to the LLM.
	gapAnalysisEstimatedTokens = 1000
	// gapAnalysisFallbackTokens is committed when gap analysis reports zero token usage.
	gapAnalysisFallbackTokens = 500
	// atsSimulationEstimatedTokens is reserved before dispatching ATS simulation to the LLM.
	atsSimulationEstimatedTokens = 1500
	// atsSimulationFallbackTokens is committed when ATS simulation reports zero token usage.
	atsSimulationFallbackTokens = 800
	// quotaSettleTimeout bounds quota settlement writes when the caller context is already done.
	quotaSettleTimeout = 5 * time.Second
	// maxQuotaErrorMessageRunes bounds persisted failure diagnostics on released quota records.
	maxQuotaErrorMessageRunes = 256
)

// detachedQuotaContext ensures quota settlement runs under an uncancelled bounded context when ctx is done.
func detachedQuotaContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), quotaSettleTimeout)
	}
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), quotaSettleTimeout)
}

// formatQuotaErrorMessage collapses line breaks and bounds failure error messages persisted on quota records.
func formatQuotaErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	clean := strings.Join(strings.Fields(err.Error()), " ")
	runes := []rune(clean)
	if len(runes) > maxQuotaErrorMessageRunes {
		return string(runes[:maxQuotaErrorMessageRunes]) + "..."
	}
	return clean
}

// reserveOperationQuota reserves quota for op and returns a completion callback to commit or release it.
func (s *Service) reserveOperationQuota(ctx context.Context, userID string, op core.LLMOperationType, estimatedTokens, fallbackTokens int) (func(core.LLMTelemetry, error), error) {
	if s.generator == nil || s.quotas == nil {
		return func(core.LLMTelemetry, error) {}, nil
	}
	rec, err := s.quotas.ReserveQuota(ctx, userID, op, estimatedTokens, s.dailyRequestLimit, s.monthlyTokenLimit)
	if err != nil {
		return nil, err
	}
	return s.newQuotaFinalizer(ctx, rec, fallbackTokens), nil
}

// newQuotaFinalizer builds a deferred callback that releases or commits a quota reservation with normalized call telemetry.
func (s *Service) newQuotaFinalizer(ctx context.Context, rec *core.LLMUsageRecord, fallbackTokens int) func(core.LLMTelemetry, error) {
	return func(telemetry core.LLMTelemetry, retErr error) {
		if rec == nil {
			return
		}
		telemetry = core.ResolveTelemetry(telemetry, retErr)
		quotaCtx, cancel := detachedQuotaContext(ctx)
		defer cancel()
		if retErr != nil {
			if err := s.quotas.ReleaseQuota(quotaCtx, rec.ID, formatQuotaErrorMessage(retErr), telemetry); err != nil && s.logger != nil {
				s.logger.Error("failed to release quota reservation", "reservation_id", rec.ID, "err", err)
			}
			return
		}
		telemetry.Usage = telemetry.Usage.WithFallback(fallbackTokens)
		if err := s.quotas.CommitUsage(quotaCtx, rec.ID, telemetry); err != nil && s.logger != nil {
			s.logger.Error("failed to commit quota usage", "reservation_id", rec.ID, "err", err)
		}
	}
}

// tryAcquireATSInFlight records an active ATS simulation for versionID unless one is already running.
func (s *Service) tryAcquireATSInFlight(versionID string) bool {
	s.atsInFlightMu.Lock()
	defer s.atsInFlightMu.Unlock()
	if s.atsInFlight[versionID] {
		return false
	}
	s.atsInFlight[versionID] = true
	return true
}

// releaseATSInFlight clears the active ATS simulation guard for versionID.
func (s *Service) releaseATSInFlight(versionID string) {
	s.atsInFlightMu.Lock()
	defer s.atsInFlightMu.Unlock()
	delete(s.atsInFlight, versionID)
}

// AnalyzeGaps performs fast decoupled gap analysis for a branch against the main profile snapshot.
func (s *Service) AnalyzeGaps(ctx context.Context, userID, branchID string) (*core.GapReport, error) {
	if err := requireUserID(userID); err != nil {
		return nil, err
	}
	branch, err := s.branches.Branch(ctx, userID, branchID)
	if err != nil {
		return nil, err
	}
	if branch.UserID != userID {
		return nil, core.ErrNotFound
	}
	snapshot, err := s.profiles.Snapshot(ctx, userID)
	if err != nil {
		return nil, err
	}

	var (
		retErr    error
		telemetry core.LLMTelemetry
	)
	finishQuota, err := s.reserveOperationQuota(ctx, userID, core.OperationGapAnalysis, gapAnalysisEstimatedTokens, gapAnalysisFallbackTokens)
	if err != nil {
		return nil, err
	}
	defer func() {
		finishQuota(telemetry, retErr)
	}()

	activeVer := s.loadOptionalActiveVersion(ctx, branch, "could not fetch active version for gap analysis")
	report, callTel, err := s.computeGapReport(ctx, branch, snapshot, activeVer)
	telemetry = callTel
	if err != nil {
		retErr = err
		return nil, retErr
	}

	// Normalization MUST occur before scoring so identified_gaps populate missing_requirements.
	report.Normalize()
	if report.MatchScore == nil {
		score := core.CalculateDeterministicMatchScore(report.MissingRequirements)
		report.MatchScore = &score
	}
	if report.SkillCoverageRatio == nil {
		ratio := core.CalculateSkillCoverageRatio(snapshot.AllSkills(), report.MissingRequirements, branch.TargetRequirements)
		report.SkillCoverageRatio = &ratio
	}

	s.persistGapReport(ctx, branch, activeVer, report)
	return report, nil
}

// loadOptionalActiveVersion loads branch.ActiveVersionID when present, logging and ignoring lookup errors.
func (s *Service) loadOptionalActiveVersion(ctx context.Context, branch *core.Branch, warnMsg string) *core.BranchVersion {
	if branch.ActiveVersionID == nil || *branch.ActiveVersionID == "" {
		return nil
	}
	v, err := s.branches.Version(ctx, branch.ID, *branch.ActiveVersionID)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn(warnMsg, "branch_id", branch.ID, "version_id", *branch.ActiveVersionID, "err", err)
		}
		return nil
	}
	return v
}

// computeGapReport delegates gap analysis to the configured Generator or falls back to cached/empty data.
func (s *Service) computeGapReport(ctx context.Context, branch *core.Branch, snapshot *core.MainProfileSnapshot, activeVer *core.BranchVersion) (*core.GapReport, core.LLMTelemetry, error) {
	if s.generator != nil {
		req := core.GenerationRequest{
			Model:          branch.ModelOverride,
			ReasoningLevel: branch.ReasoningLevel,
			MainProfile:    snapshot,
			BranchContext:  *branch,
		}
		resp, err := s.generator.GenerateGapAnalysis(ctx, req)
		telemetry := core.ResolveTelemetry(resp.EffectiveTelemetry(), err)
		if err != nil {
			return nil, telemetry, fmt.Errorf("gap analysis generation failed: %w", err)
		}
		// Reject (nil, nil) explicitly so AnalyzeGaps releases the quota reservation instead of dereferencing nil.
		if resp == nil {
			return nil, telemetry, errors.New("gap analysis generation failed: llm service returned no gap report")
		}
		report := resp.GapReport
		return &report, telemetry, nil
	}

	if activeVer != nil && activeVer.GapReport != nil {
		return activeVer.GapReport, core.LLMTelemetry{}, nil
	}
	return &core.GapReport{
		OverallMatchSummary: "No qualification gaps detected or LLM service unconfigured.",
		MissingRequirements: []core.GapItem{},
	}, core.LLMTelemetry{}, nil
}

// persistGapReport saves report onto both branch and its active version when present.
func (s *Service) persistGapReport(ctx context.Context, branch *core.Branch, activeVer *core.BranchVersion, report *core.GapReport) {
	branch.GapReport = report
	if err := s.branches.UpdateBranch(ctx, branch); err != nil && s.logger != nil {
		s.logger.Error("failed to persist gap report on branch", "branch_id", branch.ID, "err", err)
	}
	if activeVer != nil {
		activeVer.GapReport = report
		if err := s.branches.CompleteVersion(ctx, activeVer); err != nil && s.logger != nil {
			s.logger.Error("failed to persist gap report on active version", "branch_id", branch.ID, "version_id", activeVer.ID, "err", err)
		}
	}
}

// SimulateATS executes on-demand ATS extraction and job matching score calculation for a completed tailored version.
// It is disabled for the default main branch (core.ErrDefaultBranchImmutable), requires target requirements
// (core.ErrValidation), and runs at most once per version (core.ErrConflict).
func (s *Service) SimulateATS(ctx context.Context, userID, branchID string, opts *core.ATSSimulationOptions) (*core.ATSScore, error) {
	if err := requireUserID(userID); err != nil {
		return nil, err
	}
	requestedFormat, err := normalizeATSOptionFormat(opts)
	if err != nil {
		return nil, err
	}

	branch, ver, err := s.resolveATSTarget(ctx, userID, branchID, opts)
	if err != nil {
		return nil, err
	}

	if !s.tryAcquireATSInFlight(ver.ID) {
		return nil, fmt.Errorf("%w: ATS score calculation already in progress or completed for version %s", core.ErrConflict, ver.ID)
	}
	defer s.releaseATSInFlight(ver.ID)

	snapshot, resumeText, formatUsed, err := s.loadATSSimulationInputs(ctx, userID, branch, ver, requestedFormat)
	if err != nil {
		return nil, err
	}

	var (
		retErr    error
		telemetry core.LLMTelemetry
	)
	finishQuota, err := s.reserveOperationQuota(ctx, userID, core.OperationATSSimulation, atsSimulationEstimatedTokens, atsSimulationFallbackTokens)
	if err != nil {
		return nil, err
	}
	defer func() {
		finishQuota(telemetry, retErr)
	}()

	atsScore, callTel, err := s.executeATSSimulation(ctx, branch, snapshot, resumeText, formatUsed)
	telemetry = callTel
	if err != nil {
		retErr = err
		return nil, retErr
	}

	if err := s.branches.UpdateVersionATSScore(ctx, ver.ID, atsScore); err != nil {
		retErr = err
		return nil, retErr
	}
	return atsScore, nil
}

// normalizeATSOptionFormat extracts and validates the requested resume output format from opts.
func normalizeATSOptionFormat(opts *core.ATSSimulationOptions) (string, error) {
	if opts == nil {
		return core.NormalizeATSFormat("")
	}
	return core.NormalizeATSFormat(opts.Format)
}

// resolveATSTarget validates branch preconditions and returns the eligible completed version for ATS scoring.
func (s *Service) resolveATSTarget(ctx context.Context, userID, branchID string, opts *core.ATSSimulationOptions) (*core.Branch, *core.BranchVersion, error) {
	branch, err := s.branches.Branch(ctx, userID, branchID)
	if err != nil {
		return nil, nil, err
	}
	if branch.UserID != userID {
		return nil, nil, core.ErrNotFound
	}
	if branch.IsDefault {
		return nil, nil, fmt.Errorf("%w: ATS score simulation is disabled for the default main branch", core.ErrDefaultBranchImmutable)
	}
	if strings.TrimSpace(branch.TargetRequirements) == "" {
		return nil, nil, fmt.Errorf("%w: job description (target_requirements) is required to calculate ATS score", core.ErrValidation)
	}

	var explicitVersionID string
	if opts != nil {
		explicitVersionID = opts.VersionID
	}
	ver, err := s.resolveCompletedVersion(
		ctx,
		branch,
		explicitVersionID,
		"branch has no completed tailored version to evaluate",
		"ATS score can only be generated for a completed tailored version",
	)
	if err != nil {
		return nil, nil, err
	}
	if ver.ATSScore != nil {
		return nil, nil, fmt.Errorf("%w: ATS score has already been generated for version %s", core.ErrConflict, ver.ID)
	}
	return branch, ver, nil
}

// resolveCompletedVersion resolves either explicitVersionID or b.ActiveVersionID and verifies
// the referenced version exists and has completed generation successfully.
func (s *Service) resolveCompletedVersion(ctx context.Context, b *core.Branch, explicitVersionID, missingMsg, incompletePrefix string) (*core.BranchVersion, error) {
	targetVersionID := strings.TrimSpace(explicitVersionID)
	if targetVersionID == "" && b.ActiveVersionID != nil {
		targetVersionID = strings.TrimSpace(*b.ActiveVersionID)
	}
	if targetVersionID == "" {
		return nil, fmt.Errorf("%w: %s", core.ErrValidation, missingMsg)
	}

	ver, err := s.Version(ctx, b.ID, targetVersionID)
	if err != nil {
		return nil, err
	}
	if ver.Status != core.VersionStatusCompleted && ver.Status != core.VersionStatusCompletedWithWarnings {
		return nil, fmt.Errorf("%w: %s (status is %s)", core.ErrValidation, incompletePrefix, ver.Status)
	}
	return ver, nil
}

// loadATSSimulationInputs loads the candidate snapshot (failing closed if configured) and resolves resume text.
func (s *Service) loadATSSimulationInputs(ctx context.Context, userID string, branch *core.Branch, ver *core.BranchVersion, requestedFormat string) (*core.MainProfileSnapshot, string, string, error) {
	var snapshot *core.MainProfileSnapshot
	if s.profiles != nil {
		snap, err := s.profiles.Snapshot(ctx, userID)
		if err != nil {
			return nil, "", "", fmt.Errorf("failed to load profile snapshot for ATS sanitizer session: %w", err)
		}
		snapshot = snap
	}
	resumeText, formatUsed, err := s.resolveVersionResumeText(ctx, userID, branch, ver, snapshot, requestedFormat)
	if err != nil {
		return nil, "", "", err
	}
	return snapshot, resumeText, formatUsed, nil
}

// executeATSSimulation computes an ATS score via Generator or deterministic fallback and normalizes it.
func (s *Service) executeATSSimulation(ctx context.Context, branch *core.Branch, snapshot *core.MainProfileSnapshot, resumeText, formatUsed string) (*core.ATSScore, core.LLMTelemetry, error) {
	var (
		atsScore  *core.ATSScore
		telemetry core.LLMTelemetry
	)
	if s.generator != nil {
		simResp, err := s.generator.SimulateATS(ctx, core.ATSSimulationRequest{
			Model:          branch.ModelOverride,
			ReasoningLevel: branch.ReasoningLevel,
			ResumeText:     resumeText,
			FormatUsed:     formatUsed,
			BranchContext:  *branch,
			MainProfile:    snapshot,
		})
		telemetry = core.ResolveTelemetry(simResp.EffectiveTelemetry(), err)
		if err != nil {
			return nil, telemetry, fmt.Errorf("ats simulation failed: %w", err)
		}
		if simResp != nil {
			scoreCopy := simResp.Score
			atsScore = &scoreCopy
		}
	}
	if atsScore == nil {
		atsScore = core.CalculateDeterministicATSScore(resumeText, formatUsed, branch.TargetRequirements)
		atsScore.EvaluatedAt = nil
	}
	if atsScore.EvaluatedAt == nil {
		now := s.now()
		atsScore.EvaluatedAt = &now
	}
	atsScore.Normalize()
	return atsScore, telemetry, nil
}

// resolveVersionResumeText returns rendered resume text and its effective format for ver.
func (s *Service) resolveVersionResumeText(ctx context.Context, userID string, branch *core.Branch, ver *core.BranchVersion, snapshot *core.MainProfileSnapshot, requestedFormat string) (string, string, error) {
	htmlOut, mdOut, txtOut := s.renderMissingVersionOutputs(ctx, userID, branch, ver, snapshot)
	resumeText, formatUsed := selectResumeFormatText(requestedFormat, htmlOut, mdOut, txtOut, ver.TailoredContent)
	if strings.TrimSpace(resumeText) == "" {
		return "", "", fmt.Errorf("%w: tailored version contains no resume text to evaluate", core.ErrValidation)
	}
	return resumeText, formatUsed, nil
}

// renderMissingVersionOutputs backfills blank HTML/Markdown/Text outputs on ver using the configured renderer.
func (s *Service) renderMissingVersionOutputs(ctx context.Context, userID string, branch *core.Branch, ver *core.BranchVersion, snapshot *core.MainProfileSnapshot) (string, string, string) {
	htmlOut := ver.HTMLOutput
	mdOut := ver.MarkdownOutput
	txtOut := ver.PlainTextOutput
	if (htmlOut != "" && mdOut != "" && txtOut != "") || ver.TailoredContent == nil || s.renderer == nil || s.templates == nil {
		return htmlOut, mdOut, txtOut
	}

	tmplID := ver.TemplateSetIDUsed
	if tmplID == "" {
		tmplID = branch.TemplateSetID
	}
	if tmplID == "" {
		return htmlOut, mdOut, txtOut
	}
	tmpl, err := s.templates.Template(ctx, userID, tmplID)
	if err != nil || tmpl == nil {
		return htmlOut, mdOut, txtOut
	}
	var prof *core.Profile
	if snapshot != nil {
		prof = &snapshot.Profile
	}
	rendered, err := s.renderer.Render(ctx, tmpl, prof, ver.TailoredContent)
	if err != nil || rendered == nil {
		return htmlOut, mdOut, txtOut
	}
	if htmlOut == "" {
		htmlOut = rendered.HTML
	}
	if mdOut == "" {
		mdOut = rendered.Markdown
	}
	if txtOut == "" {
		txtOut = rendered.PlainText
	}
	return htmlOut, mdOut, txtOut
}

// selectResumeFormatText picks the preferred non-empty rendered format, falling back to formatted TailoredContent.
func selectResumeFormatText(requestedFormat, htmlOut, mdOut, txtOut string, tc *core.TailoredContent) (string, string) {
	switch requestedFormat {
	case string(core.OutputFormatHTML):
		if strings.TrimSpace(htmlOut) != "" {
			return htmlOut, string(core.OutputFormatHTML)
		}
	case string(core.OutputFormatPlainText):
		if strings.TrimSpace(txtOut) != "" {
			return txtOut, string(core.OutputFormatPlainText)
		}
	}

	switch {
	case strings.TrimSpace(mdOut) != "":
		return mdOut, string(core.OutputFormatMarkdown)
	case strings.TrimSpace(txtOut) != "":
		return txtOut, string(core.OutputFormatPlainText)
	case strings.TrimSpace(htmlOut) != "":
		return htmlOut, string(core.OutputFormatHTML)
	case tc != nil:
		return formatTailoredContentAsText(tc), string(core.OutputFormatMarkdown)
	default:
		return "", ""
	}
}

// formatTailoredContentAsText serializes structured tailored resume content into Markdown text.
func formatTailoredContentAsText(tc *core.TailoredContent) string {
	if tc == nil {
		return ""
	}
	var sb strings.Builder
	if tc.Summary != "" {
		sb.WriteString("## Summary\n" + tc.Summary + "\n\n")
	}
	appendTailoredSkills(&sb, tc.SkillGroups)
	appendTailoredExperiences(&sb, tc.Experiences)
	appendTailoredProjects(&sb, tc.Projects)
	appendTailoredCredentials(&sb, tc.Education, tc.Certifications)
	return strings.TrimSpace(sb.String())
}

// appendTailoredSkills writes formatted skill groups into sb when non-empty.
func appendTailoredSkills(sb *strings.Builder, groups []core.TailoredSkillGroup) {
	if len(groups) == 0 {
		return
	}
	sb.WriteString("## Skills\n")
	for _, sg := range groups {
		sb.WriteString("- ")
		if sg.Category != "" {
			sb.WriteString(sg.Category + ": ")
		}
		sb.WriteString(strings.Join(sg.Skills, ", ") + "\n")
	}
	sb.WriteString("\n")
}

// appendTailoredExperiences writes formatted work experiences and bullets into sb when non-empty.
func appendTailoredExperiences(sb *strings.Builder, exps []core.TailoredExperience) {
	if len(exps) == 0 {
		return
	}
	sb.WriteString("## Experience\n")
	for _, exp := range exps {
		sb.WriteString("### " + exp.RoleTitle + " - " + exp.Company + "\n")
		for _, b := range exp.Bullets {
			sb.WriteString("- " + b.Text + "\n")
		}
	}
	sb.WriteString("\n")
}

// appendTailoredProjects writes formatted projects and bullets into sb when non-empty.
func appendTailoredProjects(sb *strings.Builder, projs []core.TailoredProject) {
	if len(projs) == 0 {
		return
	}
	sb.WriteString("## Projects\n")
	for _, proj := range projs {
		header := "### " + proj.Name
		if proj.Role != "" {
			header += " (" + proj.Role + ")"
		}
		sb.WriteString(header + "\n")
		if proj.Summary != "" {
			sb.WriteString(proj.Summary + "\n")
		}
		for _, b := range proj.Bullets {
			sb.WriteString("- " + b.Text + "\n")
		}
	}
	sb.WriteString("\n")
}

// appendTailoredCredentials writes formatted education and certifications into sb when non-empty.
func appendTailoredCredentials(sb *strings.Builder, edu []core.TailoredEducation, certs []core.TailoredCert) {
	if len(edu) > 0 {
		sb.WriteString("## Education\n")
		for _, e := range edu {
			sb.WriteString("- " + e.Degree + " " + e.Field + " - " + e.Institution + "\n")
		}
		sb.WriteString("\n")
	}
	if len(certs) > 0 {
		sb.WriteString("## Certifications\n")
		for _, c := range certs {
			line := "- " + c.Name
			if c.Issuer != "" {
				line += " (" + c.Issuer + ")"
			}
			sb.WriteString(line + "\n")
		}
	}
}
