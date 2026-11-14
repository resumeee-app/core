package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// Timeout bounds applied to LLM worker stages and detached failure status persistence.
const (
	// defaultLLMJobTimeout caps individual LLM generation calls when Config.LLMTimeout is unset.
	defaultLLMJobTimeout = 10 * time.Minute
	// failJobStatusTimeout bounds detached failure writes when the parent context has been cancelled.
	failJobStatusTimeout = 5 * time.Second
)

// generationInputs holds all domain entities resolved prior to invoking resume tailoring.
type generationInputs struct {
	userID       string
	branch       *core.Branch
	profile      *core.Profile
	snapshot     *core.MainProfileSnapshot
	preset       *core.Preset
	priorContent *core.TailoredContent
	template     *core.TemplateSet
}

// processJob coordinates end-to-end execution of a background resume generation pipeline job.
func (p *Pool) processJob(ctx context.Context, job core.GenerationJob) {
	if p.branches == nil {
		return
	}

	if err := p.branches.UpdateVersionStatus(ctx, job.VersionID, core.VersionStatusProcessing, ""); err != nil {
		if p.logger != nil {
			p.logger.Error("failed to update version status to processing", "version_id", job.VersionID, "err", err)
		}
		return
	}

	inputs, err := p.loadGenerationInputs(ctx, job)
	if err != nil {
		p.failJob(ctx, job.VersionID, err.Error())
		return
	}

	resp, ok := p.runTailoringGeneration(ctx, job, inputs)
	if !ok {
		return
	}

	if p.validator == nil {
		p.failJob(ctx, job.VersionID, "no provenance validator configured")
		return
	}
	valRes, err := p.validator.Validate(&resp.TailoredContent, inputs.snapshot, inputs.branch)
	if err != nil {
		p.failJob(ctx, job.VersionID, fmt.Sprintf("provenance validation error: %v", err))
		return
	}

	if p.renderer == nil {
		p.failJob(ctx, job.VersionID, "no template renderer configured")
		return
	}
	rendered, err := p.renderer.Render(ctx, inputs.template, inputs.profile, valRes.SanitizedContent)
	if err != nil {
		p.failJob(ctx, job.VersionID, fmt.Sprintf("template render error: %v", err))
		return
	}

	p.persistCompletedVersion(ctx, job, inputs, resp, valRes, rendered)
}

// loadGenerationInputs resolves branch, profile, snapshot, optional preset/prior version, and template entities.
func (p *Pool) loadGenerationInputs(ctx context.Context, job core.GenerationJob) (*generationInputs, error) {
	branchID := job.BranchID
	if branchID == "" {
		v, err := p.branches.Version(ctx, "", job.VersionID)
		if err != nil {
			return nil, fmt.Errorf("failed to load version: %w", err)
		}
		branchID = v.BranchID
	}

	branch, err := p.branches.Branch(ctx, job.UserID, branchID)
	if err != nil {
		return nil, fmt.Errorf("failed to load branch: %w", err)
	}
	userID := job.UserID
	if userID == "" {
		userID = branch.UserID
	}

	prof, err := p.profiles.Profile(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to load profile: %w", err)
	}
	snapshot, err := p.profiles.Snapshot(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to load profile snapshot: %w", err)
	}

	var preset *core.Preset
	if branch.PresetID != nil && *branch.PresetID != "" && p.presets != nil {
		if pr, err := p.presets.Preset(ctx, userID, *branch.PresetID); err == nil {
			preset = pr
		}
	}

	var priorContent *core.TailoredContent
	if branch.ActiveVersionID != nil && *branch.ActiveVersionID != "" {
		if activeVer, err := p.branches.Version(ctx, branch.ID, *branch.ActiveVersionID); err == nil && activeVer != nil {
			priorContent = activeVer.TailoredContent
		}
	}

	tmpl, err := p.templates.Template(ctx, userID, branch.TemplateSetID)
	if err != nil {
		return nil, fmt.Errorf("failed to load template set: %w", err)
	}

	return &generationInputs{
		userID:       userID,
		branch:       branch,
		profile:      prof,
		snapshot:     snapshot,
		preset:       preset,
		priorContent: priorContent,
		template:     tmpl,
	}, nil
}

// runTailoringGeneration builds the sanitized request, executes the Generator with timeout, and settles quota.
func (p *Pool) runTailoringGeneration(ctx context.Context, job core.GenerationJob, inputs *generationInputs) (*core.GenerationResponse, bool) {
	if p.generator == nil {
		p.failJob(ctx, job.VersionID, "no LLM service configured")
		return nil, false
	}

	presetInstructions := ""
	if inputs.preset != nil {
		presetInstructions = inputs.preset.SystemInstructions
	}

	req := core.GenerationRequest{
		Model:              inputs.branch.ModelOverride,
		ReasoningLevel:     inputs.branch.ReasoningLevel,
		MainProfile:        inputs.snapshot,
		GlobalRules:        inputs.profile.GlobalWritingRules,
		PresetInstructions: presetInstructions,
		Preset:             inputs.preset,
		TargetLanguage:     inputs.preset.EffectiveLanguage(),
		BranchContext:      *inputs.branch,
		PriorVersion:       inputs.priorContent,
	}

	timeout := p.llmTimeout
	if timeout <= 0 {
		timeout = defaultLLMJobTimeout
	}
	llmCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := p.generator.GenerateTailoredResume(llmCtx, req)
	if err != nil {
		if p.logger != nil {
			p.logger.Error("llm generation failed", "version_id", job.VersionID, "error", err)
		}
		errMsg := "An error occurred during resume tailoring. Please try again."
		if errors.Is(err, context.DeadlineExceeded) {
			errMsg = "Resume tailoring timed out after 10 minutes."
		}
		p.releaseJobQuota(ctx, job.ReservationID, errMsg)
		p.failJob(ctx, job.VersionID, errMsg)
		return nil, false
	}
	if resp == nil {
		errMsg := "llm returned empty generation response"
		p.releaseJobQuota(ctx, job.ReservationID, errMsg)
		p.failJob(ctx, job.VersionID, errMsg)
		return nil, false
	}

	if p.quotas != nil && job.ReservationID != "" {
		_ = p.quotas.CommitUsage(ctx, job.ReservationID, resp.Usage)
	}
	return resp, true
}

// releaseJobQuota releases reserved tenant quota units when a job fails prior to commitment.
func (p *Pool) releaseJobQuota(ctx context.Context, reservationID, reason string) {
	if p.quotas != nil && reservationID != "" {
		_ = p.quotas.ReleaseQuota(ctx, reservationID, reason)
	}
}

// persistCompletedVersion updates version fields with sanitized content and rendered outputs and marks it complete.
func (p *Pool) persistCompletedVersion(ctx context.Context, job core.GenerationJob, inputs *generationInputs, resp *core.GenerationResponse, valRes *core.ValidationResult, rendered *core.RenderOutput) {
	ver, err := p.branches.Version(ctx, inputs.branch.ID, job.VersionID)
	if err != nil {
		p.failJob(ctx, job.VersionID, fmt.Sprintf("failed to get version: %v", err))
		return
	}

	status := core.VersionStatusCompleted
	if len(valRes.Warnings) > 0 {
		status = core.VersionStatusCompletedWithWarnings
	}

	ver.Status = status
	ver.MainRevisionUsed = inputs.snapshot.MainRevision
	ver.TemplateSetIDUsed = inputs.branch.TemplateSetID
	if ver.ProviderUsed == "" {
		ver.ProviderUsed = inputs.branch.ProviderOverride
	}
	if ver.ModelUsed == "" {
		ver.ModelUsed = inputs.branch.ModelOverride
	}
	ver.TailoredContent = valRes.SanitizedContent
	if resp.GapReport.MatchScore == nil {
		score := core.CalculateDeterministicMatchScore(resp.GapReport.MissingRequirements)
		resp.GapReport.MatchScore = &score
	}
	ver.GapReport = &resp.GapReport
	ver.ValidationWarnings = valRes.Warnings
	ver.HTMLOutput = rendered.HTML
	ver.MarkdownOutput = rendered.Markdown
	ver.PlainTextOutput = rendered.PlainText

	if err := p.branches.CompleteVersion(ctx, ver); err != nil {
		p.failJob(ctx, job.VersionID, fmt.Sprintf("failed to complete version: %v", err))
		return
	}

	if p.logger != nil {
		p.logger.Info("generation job completed successfully",
			"version_id", ver.ID,
			"branch_id", inputs.branch.ID,
			"status", ver.Status,
			"warnings_count", len(valRes.Warnings),
		)
	}
}

// failJob records a failed execution status and error diagnostics using a bounded detached timeout context.
func (p *Pool) failJob(_ context.Context, versionID, errMsg string) {
	if p.logger != nil {
		p.logger.Error("generation job failed", "version_id", versionID, "error", errMsg)
	}
	if p.branches != nil {
		failCtx, cancel := context.WithTimeout(context.Background(), failJobStatusTimeout)
		defer cancel()
		if err := p.branches.UpdateVersionStatus(failCtx, versionID, core.VersionStatusFailed, errMsg); err != nil {
			if p.logger != nil {
				p.logger.Error("failed to record version failure status in store", "version_id", versionID, "error", err)
			}
		}
	}
}
