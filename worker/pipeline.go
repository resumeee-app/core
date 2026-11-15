package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// Timeout bounds and fallback token budgets applied to LLM worker stages.
const (
	// defaultLLMJobTimeout caps individual LLM generation calls when Config.LLMTimeout is unset.
	defaultLLMJobTimeout = 10 * time.Minute
	// defaultTailoringFallbackTokens is committed when a tailoring generation call succeeds with zero reported token usage.
	defaultTailoringFallbackTokens = 2000
)

// generationInputs holds all domain entities resolved prior to invoking resume tailoring.
type generationInputs struct {
	userID              string
	branch              *core.Branch
	profile             *core.Profile
	snapshot            *core.MainProfileSnapshot
	preset              *core.Preset
	priorContent        *core.TailoredContent
	activeVersionNumber int
	template            *core.TemplateSet
}

// safeProcessBranchJob runs a claimed branch tailoring job with panic recovery so worker slots and quotas never leak.
func (p *Pool) safeProcessBranchJob(job *core.Job) {
	defer func() {
		if r := recover(); r != nil {
			jobID := ""
			if job != nil {
				jobID = job.ID
			}
			if p.logger != nil {
				p.logger.Error("panic recovered during branch tailoring execution",
					"job_id", jobID,
					"worker_id", p.workerID,
					"panic", r,
				)
			}
			p.failClaimedJob(p.jobDescriptor(job, ""), "An unexpected internal error occurred during resume generation.", true, core.LLMTelemetry{})
		}
	}()

	p.processClaimedBranchJob(job)
}

// processClaimedBranchJob coordinates end-to-end execution of a claimed branch tailoring job.
func (p *Pool) processClaimedBranchJob(job *core.Job) {
	if p.jobs == nil || job == nil {
		return
	}

	attemptCtx, attemptCancel := context.WithCancel(p.jobCtx)
	defer attemptCancel()

	claimedBy := job.ClaimedBy
	if claimedBy == "" {
		claimedBy = p.workerID
	}
	desc := p.jobDescriptor(job, claimedBy)

	stopHeartbeat := p.startLeaseHeartbeat(attemptCtx, attemptCancel, job.ID, claimedBy, core.JobKindBranchTailor)
	defer stopHeartbeat()

	if job.ExpiresAt != nil && !job.ExpiresAt.After(p.now()) {
		stopHeartbeat()
		p.failClaimedJob(desc, core.ErrJobExpired.Error(), true, core.LLMTelemetry{})
		return
	}

	if job.Tailor == nil || strings.TrimSpace(job.Tailor.BranchID) == "" || strings.TrimSpace(job.Tailor.VersionID) == "" {
		stopHeartbeat()
		p.failClaimedJob(desc, "Invalid branch tailor job specification.", true, core.LLMTelemetry{})
		return
	}

	inputs, ver, err := p.loadGenerationInputs(attemptCtx, job)
	if err != nil {
		stopHeartbeat()
		if p.logger != nil {
			p.logger.Error("failed to load generation inputs",
				"job_id", job.ID,
				"version_id", job.Tailor.VersionID,
				"worker_id", claimedBy,
				"error", err,
			)
		}
		p.failClaimedJob(desc, "Failed to load generation inputs.", true, core.LLMTelemetry{})
		return
	}

	if !p.verifyVersionRunnableBeforeLLM(attemptCtx, job, ver, claimedBy, inputs) {
		stopHeartbeat()
		return
	}

	resp, telemetry, ok := p.runTailoringGeneration(attemptCtx, job, claimedBy, inputs, stopHeartbeat)
	if !ok {
		return
	}

	if p.validator == nil {
		stopHeartbeat()
		p.failClaimedJob(desc, "Resume provenance validation is unavailable.", false, core.LLMTelemetry{})
		return
	}
	valRes, err := p.validator.Validate(&resp.TailoredContent, inputs.snapshot, inputs.branch)
	if err != nil || valRes == nil {
		stopHeartbeat()
		if p.logger != nil {
			p.logger.Error("provenance validation failed",
				"job_id", job.ID,
				"version_id", ver.ID,
				"worker_id", claimedBy,
				"error", err,
			)
		}
		p.failClaimedJob(desc, "Resume validation failed after generation.", false, core.LLMTelemetry{})
		return
	}

	if p.renderer == nil {
		stopHeartbeat()
		p.failClaimedJob(desc, "Resume template rendering is unavailable.", false, core.LLMTelemetry{})
		return
	}
	rendered, err := p.renderer.Render(attemptCtx, inputs.template, inputs.profile, valRes.SanitizedContent)
	if err != nil || rendered == nil {
		stopHeartbeat()
		if p.logger != nil {
			p.logger.Error("template render failed",
				"job_id", job.ID,
				"version_id", ver.ID,
				"worker_id", claimedBy,
				"error", err,
			)
		}
		p.failClaimedJob(desc, "Failed to render generated resume template.", false, core.LLMTelemetry{})
		return
	}

	stopHeartbeat()
	p.persistCompletedVersion(attemptCtx, job, ver, claimedBy, inputs, resp, valRes, rendered, telemetry)
}

// verifyVersionRunnableBeforeLLM confirms the claimed job and target version are still owned, unexpired, and not superseded.
func (p *Pool) verifyVersionRunnableBeforeLLM(
	ctx context.Context,
	job *core.Job,
	ver *core.BranchVersion,
	claimedBy string,
	inputs *generationInputs,
) bool {
	desc := p.jobDescriptor(job, claimedBy)
	if job.Tailor.BranchEpoch != 0 && inputs.branch.Epoch != job.Tailor.BranchEpoch {
		p.failClaimedJob(desc, "Branch superseded before generation.", true, core.LLMTelemetry{})
		return false
	}

	latestVer, err := p.branches.Version(ctx, inputs.branch.ID, ver.ID)
	if err != nil {
		if p.logger != nil {
			p.logger.Error("failed to verify version state before llm",
				"job_id", job.ID,
				"version_id", ver.ID,
				"worker_id", claimedBy,
				"error", err,
			)
		}
		p.failClaimedJob(desc, "Failed to verify generation job state.", true, core.LLMTelemetry{})
		return false
	}
	if latestVer.Status != core.VersionStatusProcessing || (latestVer.JobID != "" && latestVer.JobID != job.ID) {
		return false
	}

	latestJob, jErr := p.jobs.Job(ctx, inputs.userID, job.ID)
	if jErr == nil && latestJob != nil {
		if latestJob.Status != core.JobStatusProcessing || latestJob.ClaimedBy != claimedBy {
			return false
		}
		if latestJob.ExpiresAt != nil && !latestJob.ExpiresAt.After(p.now()) {
			p.failClaimedJob(desc, core.ErrJobExpired.Error(), true, core.LLMTelemetry{})
			return false
		}
		if job.ExpiresAt == nil && latestJob.ExpiresAt != nil {
			job.ExpiresAt = latestJob.ExpiresAt
		}
		if job.ReservationID == "" {
			job.ReservationID = latestJob.ReservationID
		}
	}

	if inputs.activeVersionNumber > latestVer.VersionNumber {
		p.failClaimedJob(desc, "Version superseded by newer active version.", true, core.LLMTelemetry{})
		return false
	}
	return true
}

// loadGenerationInputs resolves branch, version, profile, snapshot, optional preset/prior version, and template entities.
func (p *Pool) loadGenerationInputs(ctx context.Context, job *core.Job) (*generationInputs, *core.BranchVersion, error) {
	if p.branches == nil {
		return nil, nil, errors.New("no branch reader configured")
	}

	br, err := p.branches.Branch(ctx, job.UserID, job.Tailor.BranchID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load branch: %w", err)
	}
	userID := job.UserID
	if userID == "" {
		userID = br.UserID
	}

	ver, err := p.branches.Version(ctx, br.ID, job.Tailor.VersionID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load version: %w", err)
	}

	if p.profiles == nil {
		return nil, nil, errors.New("no profile reader configured")
	}
	prof, err := p.profiles.Profile(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load profile: %w", err)
	}
	snapshot, err := p.profiles.Snapshot(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load profile snapshot: %w", err)
	}

	var preset *core.Preset
	if br.PresetID != nil && *br.PresetID != "" && p.presets != nil {
		if pr, prErr := p.presets.Preset(ctx, userID, *br.PresetID); prErr == nil {
			preset = pr
		}
	}

	var priorContent *core.TailoredContent
	var activeVersionNumber int
	if br.ActiveVersionID != nil && *br.ActiveVersionID != "" {
		if activeVer, avErr := p.branches.Version(ctx, br.ID, *br.ActiveVersionID); avErr == nil && activeVer != nil {
			priorContent = activeVer.TailoredContent
			activeVersionNumber = activeVer.VersionNumber
		}
	}

	if p.templates == nil {
		return nil, nil, errors.New("no template reader configured")
	}
	tmpl, err := p.templates.Template(ctx, userID, br.TemplateSetID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load template set: %w", err)
	}

	return &generationInputs{
		userID:              userID,
		branch:              br,
		profile:             prof,
		snapshot:            snapshot,
		preset:              preset,
		priorContent:        priorContent,
		activeVersionNumber: activeVersionNumber,
		template:            tmpl,
	}, ver, nil
}

// withLLMTimeout derives a context bounded by Config.LLMTimeout or defaultLLMJobTimeout.
func (p *Pool) withLLMTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := p.llmTimeout
	if timeout <= 0 {
		timeout = defaultLLMJobTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// runTailoringGeneration builds the sanitized request, executes Generator with timeout, handles errors/retries, and commits quota.
func (p *Pool) runTailoringGeneration(
	attemptCtx context.Context,
	job *core.Job,
	claimedBy string,
	inputs *generationInputs,
	stopHeartbeat func(),
) (*core.GenerationResponse, core.LLMTelemetry, bool) {
	desc := p.jobDescriptor(job, claimedBy)
	if p.generator == nil {
		stopHeartbeat()
		p.failClaimedJob(desc, "Resume generation service is unavailable.", true, core.LLMTelemetry{})
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

	llmCtx, llmCancel := p.withLLMTimeout(attemptCtx)
	genResp, err := p.generator.GenerateTailoredResume(llmCtx, genReq)
	llmCancel()
	telemetry := core.ResolveTelemetry(genResp.EffectiveTelemetry(), err)

	if err != nil {
		stopHeartbeat()
		p.routeAttemptError(
			attemptCtx,
			desc,
			err,
			telemetry,
			"Generation interrupted by worker shutdown.",
			userFacingTailoringError,
		)
		return nil, core.LLMTelemetry{}, false
	}
	if attemptCtx.Err() != nil {
		stopHeartbeat()
		if p.isShuttingDown() {
			p.failClaimedJob(desc, "Generation interrupted by worker shutdown.", true, core.LLMTelemetry{})
		} else {
			p.failClaimedJob(desc, core.ErrLeaseExpired.Error(), true, core.LLMTelemetry{})
		}
		return nil, core.LLMTelemetry{}, false
	}
	if genResp == nil {
		stopHeartbeat()
		if p.logger != nil {
			p.logger.Error("llm returned empty generation response", "job_id", job.ID, "worker_id", claimedBy)
		}
		p.failClaimedJob(desc, "Resume generation returned an empty response.", true, telemetry)
		return nil, core.LLMTelemetry{}, false
	}

	// Commit consumed LLM usage immediately after generation succeeds so downstream failures cannot refund it.
	telemetry.Usage = telemetry.Usage.WithFallback(defaultTailoringFallbackTokens)
	p.commitUsageDetached(job.ReservationID, telemetry)
	return genResp, telemetry, true
}

// userFacingTailoringError translates an LLM tailoring error and class into a sanitized user-facing message.
func userFacingTailoringError(err error, class core.ErrorClass) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "Resume tailoring timed out. Please try again."
	case class == core.ErrorClassUpstreamQuota:
		return "Resume tailoring could not run because upstream provider billing or quota is exhausted."
	default:
		return "An error occurred during resume tailoring. Please try again."
	}
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
func (p *Pool) persistCompletedVersion(
	ctx context.Context,
	job *core.Job,
	claimedVer *core.BranchVersion,
	claimedBy string,
	inputs *generationInputs,
	resp *core.GenerationResponse,
	valRes *core.ValidationResult,
	rendered *core.RenderOutput,
	telemetry core.LLMTelemetry,
) {
	desc := p.jobDescriptor(job, claimedBy)
	ver, err := p.branches.Version(ctx, inputs.branch.ID, claimedVer.ID)
	if err != nil {
		if p.logger != nil {
			p.logger.Error("failed to reload claimed version before completion",
				"job_id", job.ID,
				"version_id", claimedVer.ID,
				"worker_id", claimedBy,
				"error", err,
			)
		}
		p.failClaimedJob(desc, "Failed to save generated resume version.", false, core.LLMTelemetry{})
		return
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

	if err := p.jobs.CompleteBranchTailor(ctx, job.ID, claimedBy, ver); err != nil {
		if p.logger != nil {
			p.logger.Error("failed to persist completed version",
				"job_id", job.ID,
				"version_id", claimedVer.ID,
				"worker_id", claimedBy,
				"error", err,
			)
		}
		p.failClaimedJob(desc, "Failed to save generated resume version.", false, core.LLMTelemetry{})
		return
	}

	if p.logger != nil {
		p.logger.Info("generation job completed successfully",
			"job_id", job.ID,
			"version_id", ver.ID,
			"branch_id", inputs.branch.ID,
			"status", ver.Status,
			"warnings_count", len(valRes.Warnings),
		)
	}
}
