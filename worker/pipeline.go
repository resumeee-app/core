package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// Timeout bounds and fallback token budgets applied to LLM worker stages and failure persistence.
const (
	// defaultLLMJobTimeout caps individual LLM generation calls when Config.LLMTimeout is unset.
	defaultLLMJobTimeout = 10 * time.Minute
	// failJobStatusTimeout bounds detached failure writes when the parent context has been cancelled.
	failJobStatusTimeout = 5 * time.Second
	// defaultTailoringFallbackTokens is committed when a tailoring generation call succeeds with zero reported token usage.
	defaultTailoringFallbackTokens = 2000
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
		p.releaseJobQuota(ctx, job.ReservationID, "no branch store configured", core.LLMTelemetry{})
		return
	}

	if err := p.branches.UpdateVersionStatus(ctx, job.VersionID, core.VersionStatusProcessing, ""); err != nil {
		if p.logger != nil {
			p.logger.Error("failed to update version status to processing", "version_id", job.VersionID, "err", err)
		}
		p.releaseJobQuota(ctx, job.ReservationID, err.Error(), core.LLMTelemetry{})
		return
	}

	inputs, err := p.loadGenerationInputs(ctx, job)
	if err != nil {
		p.failJobWithQuota(ctx, job.VersionID, job.ReservationID, err.Error(), core.LLMTelemetry{})
		return
	}

	resp, telemetry, ok := p.runTailoringGeneration(ctx, job, inputs)
	if !ok {
		return
	}

	valRes, rendered, err := p.validateAndRenderVersion(ctx, inputs, resp)
	if err != nil {
		p.failJobWithQuota(ctx, job.VersionID, job.ReservationID, err.Error(), telemetry)
		return
	}

	if err := p.persistCompletedVersion(ctx, job, inputs, resp, valRes, rendered, telemetry); err != nil {
		p.failJobWithQuota(ctx, job.VersionID, job.ReservationID, err.Error(), telemetry)
		return
	}

	// Apply fallback estimation only after generation, validation, rendering, and persistence succeed.
	telemetry.Usage = telemetry.Usage.WithFallback(defaultTailoringFallbackTokens)
	p.commitJobQuota(ctx, job.ReservationID, telemetry)
}

// loadGenerationInputs resolves branch, profile, snapshot, optional preset/prior version, and template entities.
func (p *Pool) loadGenerationInputs(ctx context.Context, job core.GenerationJob) (*generationInputs, error) {
	branchID := job.BranchID
	if branchID == "" {
		v, err := p.branches.Version(ctx, "", job.VersionID)
		if err != nil {
			return nil, fmt.Errorf("failed to load version: %w", err)
		}
		if v == nil {
			return nil, errors.New("failed to load version: empty version record")
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

	if p.profiles == nil {
		return nil, errors.New("no profile reader configured")
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

	if p.templates == nil {
		return nil, errors.New("no template reader configured")
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

// withLLMTimeout derives a context bounded by Config.LLMTimeout or defaultLLMJobTimeout.
func (p *Pool) withLLMTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := p.llmTimeout
	if timeout <= 0 {
		timeout = defaultLLMJobTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// runTailoringGeneration builds the sanitized request, executes the Generator with timeout, and resolves call telemetry.
func (p *Pool) runTailoringGeneration(ctx context.Context, job core.GenerationJob, inputs *generationInputs) (*core.GenerationResponse, core.LLMTelemetry, bool) {
	if p.generator == nil {
		p.failJobWithQuota(ctx, job.VersionID, job.ReservationID, "no LLM service configured", core.LLMTelemetry{})
		return nil, core.LLMTelemetry{}, false
	}

	presetInstructions := ""
	if inputs.preset != nil {
		presetInstructions = inputs.preset.SystemInstructions
	}

	genReq := core.GenerationRequest{
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

	genCtx, cancel := p.withLLMTimeout(ctx)
	defer cancel()

	genResp, err := p.generator.GenerateTailoredResume(genCtx, genReq)
	telemetry := core.ResolveTelemetry(genResp.EffectiveTelemetry(), err)
	if err != nil {
		if p.logger != nil {
			p.logger.Error("llm generation failed", "version_id", job.VersionID, "error", err)
		}
		errMsg := "An error occurred during resume tailoring. Please try again."
		if errors.Is(err, context.DeadlineExceeded) {
			errMsg = "Resume tailoring timed out after 10 minutes."
		}
		p.failJobWithQuota(ctx, job.VersionID, job.ReservationID, errMsg, telemetry)
		return nil, core.LLMTelemetry{}, false
	}
	if genResp == nil {
		p.failJobWithQuota(ctx, job.VersionID, job.ReservationID, "llm returned empty generation response", telemetry)
		return nil, core.LLMTelemetry{}, false
	}

	return genResp, telemetry, true
}

// validateAndRenderVersion validates generated content against source provenance and renders final outputs.
func (p *Pool) validateAndRenderVersion(ctx context.Context, inputs *generationInputs, resp *core.GenerationResponse) (*core.ValidationResult, *core.RenderOutput, error) {
	if p.validator == nil {
		return nil, nil, errors.New("no provenance validator configured")
	}
	valRes, err := p.validator.Validate(&resp.TailoredContent, inputs.snapshot, inputs.branch)
	if err != nil {
		return nil, nil, fmt.Errorf("provenance validation error: %w", err)
	}
	if valRes == nil {
		return nil, nil, errors.New("provenance validation error: empty validation result")
	}
	if p.renderer == nil {
		return nil, nil, errors.New("no template renderer configured")
	}
	rendered, err := p.renderer.Render(ctx, inputs.template, inputs.profile, valRes.SanitizedContent)
	if err != nil {
		return nil, nil, fmt.Errorf("template render error: %w", err)
	}
	if rendered == nil {
		return nil, nil, errors.New("template render error: empty render output")
	}
	return valRes, rendered, nil
}

// detachedQuotaContext ensures quota settlement runs under an uncancelled bounded context when ctx is done.
func detachedQuotaContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), failJobStatusTimeout)
	}
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), failJobStatusTimeout)
}

// commitJobQuota commits normalized call telemetry for a successfully completed job reservation.
func (p *Pool) commitJobQuota(ctx context.Context, reservationID string, telemetry core.LLMTelemetry) {
	if p.quotas == nil || reservationID == "" {
		return
	}
	quotaCtx, cancel := detachedQuotaContext(ctx)
	defer cancel()
	if err := p.quotas.CommitUsage(quotaCtx, reservationID, telemetry.Normalize()); err != nil && p.logger != nil {
		p.logger.Error("failed to commit job quota usage", "reservation_id", reservationID, "err", err)
	}
}

// releaseJobQuota releases reserved tenant quota units alongside call telemetry when a job fails prior to commitment.
func (p *Pool) releaseJobQuota(ctx context.Context, reservationID, reason string, telemetry core.LLMTelemetry) {
	if p.quotas == nil || reservationID == "" {
		return
	}
	quotaCtx, cancel := detachedQuotaContext(ctx)
	defer cancel()
	if err := p.quotas.ReleaseQuota(quotaCtx, reservationID, reason, telemetry.Normalize()); err != nil && p.logger != nil {
		p.logger.Error("failed to release job quota", "reservation_id", reservationID, "err", err)
	}
}

// failJobWithQuota releases any pending quota reservation with telemetry before marking the version failed.
func (p *Pool) failJobWithQuota(ctx context.Context, versionID, reservationID, errMsg string, telemetry core.LLMTelemetry) {
	p.releaseJobQuota(ctx, reservationID, errMsg, telemetry)
	p.failJob(ctx, versionID, errMsg)
}

// resolveVersionProviderModel prefers non-empty resolved call telemetry over branch overrides and existing metadata.
func resolveVersionProviderModel(ver *core.BranchVersion, b *core.Branch, telemetry core.LLMTelemetry) (string, string) {
	provider := strings.TrimSpace(telemetry.Provider)
	if provider == "" && b != nil {
		provider = strings.TrimSpace(b.ProviderOverride)
	}
	if provider == "" && ver != nil {
		provider = ver.ProviderUsed
	}

	model := strings.TrimSpace(telemetry.Model)
	if model == "" && b != nil {
		model = strings.TrimSpace(b.ModelOverride)
	}
	if model == "" && ver != nil {
		model = ver.ModelUsed
	}
	return provider, model
}

// persistCompletedVersion updates version fields with sanitized content, rendered outputs, and resolved model metadata.
func (p *Pool) persistCompletedVersion(ctx context.Context, job core.GenerationJob, inputs *generationInputs, resp *core.GenerationResponse, valRes *core.ValidationResult, rendered *core.RenderOutput, telemetry core.LLMTelemetry) error {
	ver, err := p.branches.Version(ctx, inputs.branch.ID, job.VersionID)
	if err != nil {
		return fmt.Errorf("failed to get version: %w", err)
	}

	status := core.VersionStatusCompleted
	if len(valRes.Warnings) > 0 {
		status = core.VersionStatusCompletedWithWarnings
	}

	ver.Status = status
	ver.MainRevisionUsed = inputs.snapshot.MainRevision
	ver.TemplateSetIDUsed = inputs.branch.TemplateSetID
	ver.ProviderUsed, ver.ModelUsed = resolveVersionProviderModel(ver, inputs.branch, telemetry)
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
		return fmt.Errorf("failed to complete version: %w", err)
	}

	if p.logger != nil {
		p.logger.Info("generation job completed successfully",
			"version_id", ver.ID,
			"branch_id", inputs.branch.ID,
			"status", ver.Status,
			"warnings_count", len(valRes.Warnings),
		)
	}
	return nil
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
