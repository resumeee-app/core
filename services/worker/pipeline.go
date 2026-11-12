package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/resumeee-app/resumeee-core/domain"
)

// processJob coordinates end-to-end execution of a background resume generation pipeline job.
func (p *Pool) processJob(ctx context.Context, job GenerationJob) {
	if p.branchRepo == nil {
		return
	}

	// 1. Transition version status to processing
	if err := p.branchRepo.UpdateVersionStatus(ctx, job.VersionID, domain.VersionStatusProcessing, ""); err != nil {
		if p.logger != nil {
			p.logger.Error("failed to update version status to processing", "version_id", job.VersionID, "err", err)
		}
		return
	}

	// 2. Context Assembly
	if job.BranchID == "" {
		v, err := p.branchRepo.GetVersion(ctx, "", job.VersionID)
		if err != nil {
			p.failJob(ctx, job.VersionID, fmt.Sprintf("failed to load version: %v", err))
			return
		}
		job.BranchID = v.BranchID
	}

	branch, err := p.branchRepo.GetBranch(ctx, job.UserID, job.BranchID)
	if err != nil {
		p.failJob(ctx, job.VersionID, fmt.Sprintf("failed to load branch: %v", err))
		return
	}

	if job.UserID == "" {
		job.UserID = branch.UserID
	}

	prof, err := p.profileRepo.GetProfile(ctx, job.UserID)
	if err != nil {
		p.failJob(ctx, job.VersionID, fmt.Sprintf("failed to load profile: %v", err))
		return
	}

	snapshot, err := p.profileRepo.GetSnapshot(ctx, job.UserID)
	if err != nil {
		p.failJob(ctx, job.VersionID, fmt.Sprintf("failed to load profile snapshot: %v", err))
		return
	}

	var preset *domain.Preset
	if branch.PresetID != nil && *branch.PresetID != "" && p.presetRepo != nil {
		pr, err := p.presetRepo.GetPreset(ctx, job.UserID, *branch.PresetID)
		if err == nil {
			preset = pr
		}
	}

	// Continuity: Prior version context
	var priorContent *domain.TailoredContent
	if branch.ActiveVersionID != nil && *branch.ActiveVersionID != "" {
		activeVer, err := p.branchRepo.GetVersion(ctx, branch.ID, *branch.ActiveVersionID)
		if err == nil && activeVer != nil {
			priorContent = activeVer.TailoredContent
		}
	}

	tmpl, err := p.templateRepo.GetTemplate(ctx, job.UserID, branch.TemplateSetID)
	if err != nil {
		p.failJob(ctx, job.VersionID, fmt.Sprintf("failed to load template set: %v", err))
		return
	}

	// 3. Provider Resolution & LLM Invocation
	presetInstructions := ""
	if preset != nil {
		presetInstructions = preset.SystemInstructions
	}

	provider := branch.ProviderOverride
	model := branch.ModelOverride
	reasoningLevel := branch.ReasoningLevel

	targetLang := preset.EffectiveLanguage()

	req := domain.GenerationRequest{
		Model:              model,
		ReasoningLevel:     reasoningLevel,
		MainProfile:        snapshot,
		GlobalRules:        prof.GlobalWritingRules,
		PresetInstructions: presetInstructions,
		Preset:             preset,
		TargetLanguage:     targetLang,
		BranchContext:      *branch,
		PriorVersion:       priorContent,
	}

	if p.llmService == nil {
		p.failJob(ctx, job.VersionID, "no LLM service configured")
		return
	}

	timeout := p.llmTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	llmCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := p.llmService.GenerateTailoredResume(llmCtx, req)
	if err != nil {
		if p.logger != nil {
			p.logger.Error("llm generation failed", "version_id", job.VersionID, "error", err)
		}
		errMsg := "An error occurred during resume tailoring. Please try again."
		if errors.Is(err, context.DeadlineExceeded) {
			errMsg = "Resume tailoring timed out after 10 minutes."
		}
		if p.quotaRepo != nil && job.ReservationID != "" {
			_ = p.quotaRepo.ReleaseQuota(ctx, job.ReservationID, errMsg)
		}
		p.failJob(ctx, job.VersionID, errMsg)
		return
	}
	if resp == nil {
		if p.quotaRepo != nil && job.ReservationID != "" {
			_ = p.quotaRepo.ReleaseQuota(ctx, job.ReservationID, "llm returned empty generation response")
		}
		p.failJob(ctx, job.VersionID, "llm returned empty generation response")
		return
	}

	if p.quotaRepo != nil && job.ReservationID != "" {
		_ = p.quotaRepo.CommitUsage(ctx, job.ReservationID, resp.Usage)
	}

	// 4. Provenance Validation ("No Lies" invariant)
	if p.validator == nil {
		p.failJob(ctx, job.VersionID, "no provenance validator configured")
		return
	}

	valRes, err := p.validator.Validate(&resp.TailoredContent, snapshot, branch)
	if err != nil {
		p.failJob(ctx, job.VersionID, fmt.Sprintf("provenance validation error: %v", err))
		return
	}
	sanitizedContent := valRes.SanitizedContent
	warnings := valRes.Warnings

	// 5. Template Rendering
	if p.renderer == nil {
		p.failJob(ctx, job.VersionID, "no template renderer configured")
		return
	}

	rendered, err := p.renderer.Render(ctx, tmpl, prof, sanitizedContent)
	if err != nil {
		p.failJob(ctx, job.VersionID, fmt.Sprintf("template render error: %v", err))
		return
	}

	// 6. Dual Persistence & Pointer Progression
	ver, err := p.branchRepo.GetVersion(ctx, branch.ID, job.VersionID)
	if err != nil {
		p.failJob(ctx, job.VersionID, fmt.Sprintf("failed to get version: %v", err))
		return
	}

	status := domain.VersionStatusCompleted
	if len(warnings) > 0 {
		status = domain.VersionStatusCompletedWithWarnings
	}

	ver.Status = status
	ver.MainRevisionUsed = snapshot.MainRevision
	ver.TemplateSetIDUsed = branch.TemplateSetID
	if ver.ProviderUsed == "" {
		ver.ProviderUsed = provider
	}
	if ver.ModelUsed == "" {
		ver.ModelUsed = model
	}
	ver.TailoredContent = sanitizedContent
	if resp.GapReport.MatchScore == nil {
		score := domain.CalculateDeterministicMatchScore(resp.GapReport.MissingRequirements)
		resp.GapReport.MatchScore = &score
	}
	ver.GapReport = &resp.GapReport
	ver.ValidationWarnings = warnings
	ver.HTMLOutput = rendered.HTML
	ver.MarkdownOutput = rendered.Markdown
	ver.PlainTextOutput = rendered.PlainText

	if err := p.branchRepo.CompleteVersion(ctx, ver); err != nil {
		p.failJob(ctx, job.VersionID, fmt.Sprintf("failed to complete version: %v", err))
		return
	}

	if p.logger != nil {
		p.logger.Info("generation job completed successfully",
			"version_id", ver.ID,
			"branch_id", branch.ID,
			"status", ver.Status,
			"warnings_count", len(warnings),
		)
	}
}

// failJob records a failed execution status and error diagnostics for a version.
// It uses a detached, bounded timeout context to ensure persistence even if the parent job context is canceled.
func (p *Pool) failJob(ctx context.Context, versionID, errMsg string) {
	if p.logger != nil {
		p.logger.Error("generation job failed", "version_id", versionID, "error", errMsg)
	}
	if p.branchRepo != nil {
		failCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.branchRepo.UpdateVersionStatus(failCtx, versionID, domain.VersionStatusFailed, errMsg); err != nil {
			if p.logger != nil {
				p.logger.Error("failed to record version failure status in repository", "version_id", versionID, "error", err)
			}
		}
	}
}
