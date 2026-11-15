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

// processClaimedVersion coordinates end-to-end execution of a claimed resume generation version.
func (p *Pool) processClaimedVersion(ver *core.BranchVersion) {
	if p.branches == nil || ver == nil {
		return
	}

	attemptCtx, attemptCancel := context.WithCancel(p.jobCtx)
	defer attemptCancel()

	claimedBy := ver.ClaimedBy
	if claimedBy == "" {
		claimedBy = p.workerID
	}

	stopHeartbeat := p.startLeaseHeartbeat(attemptCtx, attemptCancel, ver.ID, func(ctx context.Context, leaseExp time.Time) error {
		return p.branches.RenewVersionLease(ctx, ver.ID, claimedBy, leaseExp)
	}, "branch_version")
	defer stopHeartbeat()

	if ver.ExpiresAt != nil && !ver.ExpiresAt.After(p.now()) {
		stopHeartbeat()
		p.failClaimedJob(p.versionJobDescriptor(ver, claimedBy), core.ErrJobExpired.Error(), true, core.LLMTelemetry{})
		return
	}

	inputs, err := p.loadGenerationInputs(attemptCtx, ver)
	if err != nil {
		stopHeartbeat()
		if p.logger != nil {
			p.logger.Error("failed to load generation inputs",
				"version_id", ver.ID,
				"worker_id", claimedBy,
				"error", err,
			)
		}
		p.failClaimedJob(p.versionJobDescriptor(ver, claimedBy), "Failed to load generation inputs.", true, core.LLMTelemetry{})
		return
	}

	if !p.verifyVersionRunnableBeforeLLM(attemptCtx, ver, claimedBy, inputs) {
		stopHeartbeat()
		return
	}

	resp, telemetry, ok := p.runTailoringGeneration(attemptCtx, ver, claimedBy, inputs, stopHeartbeat)
	if !ok {
		return
	}

	if p.validator == nil {
		stopHeartbeat()
		p.failClaimedJob(p.versionJobDescriptor(ver, claimedBy), "Resume provenance validation is unavailable.", false, core.LLMTelemetry{})
		return
	}
	valRes, err := p.validator.Validate(&resp.TailoredContent, inputs.snapshot, inputs.branch)
	if err != nil || valRes == nil {
		stopHeartbeat()
		if p.logger != nil {
			p.logger.Error("provenance validation failed",
				"version_id", ver.ID,
				"worker_id", claimedBy,
				"error", err,
			)
		}
		p.failClaimedJob(p.versionJobDescriptor(ver, claimedBy), "Resume validation failed after generation.", false, core.LLMTelemetry{})
		return
	}

	if p.renderer == nil {
		stopHeartbeat()
		p.failClaimedJob(p.versionJobDescriptor(ver, claimedBy), "Resume template rendering is unavailable.", false, core.LLMTelemetry{})
		return
	}
	rendered, err := p.renderer.Render(attemptCtx, inputs.template, inputs.profile, valRes.SanitizedContent)
	if err != nil || rendered == nil {
		stopHeartbeat()
		if p.logger != nil {
			p.logger.Error("template render failed",
				"version_id", ver.ID,
				"worker_id", claimedBy,
				"error", err,
			)
		}
		p.failClaimedJob(p.versionJobDescriptor(ver, claimedBy), "Failed to render generated resume template.", false, core.LLMTelemetry{})
		return
	}

	stopHeartbeat()
	p.persistCompletedVersion(attemptCtx, ver, claimedBy, inputs, resp, valRes, rendered, telemetry)
}

// verifyVersionRunnableBeforeLLM confirms the claimed version is still owned, unexpired, and not superseded.
func (p *Pool) verifyVersionRunnableBeforeLLM(
	ctx context.Context,
	ver *core.BranchVersion,
	claimedBy string,
	inputs *generationInputs,
) bool {
	latest, err := p.branches.Version(ctx, inputs.branch.ID, ver.ID)
	if err != nil {
		if p.logger != nil {
			p.logger.Error("failed to verify version state before llm",
				"version_id", ver.ID,
				"worker_id", claimedBy,
				"error", err,
			)
		}
		p.failClaimedJob(p.versionJobDescriptor(ver, claimedBy), "Failed to verify generation job state.", true, core.LLMTelemetry{})
		return false
	}
	if latest.Status != core.VersionStatusProcessing || latest.ClaimedBy != claimedBy {
		return false
	}
	if latest.ExpiresAt != nil && !latest.ExpiresAt.After(p.now()) {
		p.failClaimedJob(p.versionJobDescriptor(ver, claimedBy), core.ErrJobExpired.Error(), true, core.LLMTelemetry{})
		return false
	}
	if ver.ExpiresAt == nil && latest.ExpiresAt != nil {
		ver.ExpiresAt = latest.ExpiresAt
	}
	if ver.ReservationID == "" {
		ver.ReservationID = latest.ReservationID
	}
	if ver.VersionNumber == 0 {
		ver.VersionNumber = latest.VersionNumber
	}
	if inputs.activeVersionNumber > latest.VersionNumber {
		p.failClaimedJob(p.versionJobDescriptor(ver, claimedBy), "Version superseded by newer active version.", true, core.LLMTelemetry{})
		return false
	}
	return true
}

// loadGenerationInputs resolves branch, profile, snapshot, optional preset/prior version, and template entities.
func (p *Pool) loadGenerationInputs(ctx context.Context, ver *core.BranchVersion) (*generationInputs, error) {
	branchID := ver.BranchID
	if branchID == "" {
		branchID = ver.ResumeID
	}
	if branchID == "" {
		loaded, err := p.branches.Version(ctx, "", ver.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to load version: %w", err)
		}
		if loaded == nil {
			return nil, errors.New("failed to load version: empty version record")
		}
		branchID = loaded.BranchID
		if branchID == "" {
			branchID = loaded.ResumeID
		}
	}

	br, err := p.branches.Branch(ctx, ver.UserID, branchID)
	if err != nil {
		return nil, fmt.Errorf("failed to load branch: %w", err)
	}
	userID := ver.UserID
	if userID == "" {
		userID = br.UserID
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
	presetID := br.PresetID
	if presetID == nil && br.TailoringStrategyID != nil {
		presetID = br.TailoringStrategyID
	}
	if presetID != nil && *presetID != "" && p.presets != nil {
		if pr, prErr := p.presets.Preset(ctx, userID, *presetID); prErr == nil {
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
		return nil, errors.New("no template reader configured")
	}
	tmpl, err := p.templates.Template(ctx, userID, br.TemplateSetID)
	if err != nil {
		return nil, fmt.Errorf("failed to load template set: %w", err)
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

// runTailoringGeneration builds the sanitized request, executes Generator with timeout, handles errors/retries, and commits quota.
func (p *Pool) runTailoringGeneration(
	attemptCtx context.Context,
	ver *core.BranchVersion,
	claimedBy string,
	inputs *generationInputs,
	stopHeartbeat func(),
) (*core.GenerationResponse, core.LLMTelemetry, bool) {
	if p.generator == nil {
		stopHeartbeat()
		p.failClaimedJob(p.versionJobDescriptor(ver, claimedBy), "Resume generation service is unavailable.", true, core.LLMTelemetry{})
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
			p.versionJobDescriptor(ver, claimedBy),
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
			p.failClaimedJob(p.versionJobDescriptor(ver, claimedBy), "Generation interrupted by worker shutdown.", true, core.LLMTelemetry{})
		} else {
			p.failClaimedJob(p.versionJobDescriptor(ver, claimedBy), core.ErrLeaseExpired.Error(), true, core.LLMTelemetry{})
		}
		return nil, core.LLMTelemetry{}, false
	}
	if genResp == nil {
		stopHeartbeat()
		if p.logger != nil {
			p.logger.Error("llm returned empty generation response", "version_id", ver.ID, "worker_id", claimedBy)
		}
		p.failClaimedJob(p.versionJobDescriptor(ver, claimedBy), "Resume generation returned an empty response.", true, telemetry)
		return nil, core.LLMTelemetry{}, false
	}

	// Commit consumed LLM usage immediately after generation succeeds so downstream failures cannot refund it.
	telemetry.Usage = telemetry.Usage.WithFallback(defaultTailoringFallbackTokens)
	p.commitUsageDetached(ver.ReservationID, telemetry)
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
	claimedVer *core.BranchVersion,
	claimedBy string,
	inputs *generationInputs,
	resp *core.GenerationResponse,
	valRes *core.ValidationResult,
	rendered *core.RenderOutput,
	telemetry core.LLMTelemetry,
) {
	ver, err := p.branches.Version(ctx, inputs.branch.ID, claimedVer.ID)
	if err != nil {
		if p.logger != nil {
			p.logger.Error("failed to reload claimed version before completion",
				"version_id", claimedVer.ID,
				"worker_id", claimedBy,
				"error", err,
			)
		}
		p.failClaimedJob(p.versionJobDescriptor(claimedVer, claimedBy), "Failed to save generated resume version.", false, core.LLMTelemetry{})
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
	if ver.ReservationID == "" {
		ver.ReservationID = claimedVer.ReservationID
	}
	ver.ClaimedBy = claimedBy
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
		if p.logger != nil {
			p.logger.Error("failed to persist completed version",
				"version_id", claimedVer.ID,
				"worker_id", claimedBy,
				"error", err,
			)
		}
		p.failClaimedJob(p.versionJobDescriptor(claimedVer, claimedBy), "Failed to save generated resume version.", false, core.LLMTelemetry{})
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

// versionJobDescriptor adapts a claimed BranchVersion into a claimedJobDescriptor for shared lifecycle management.
func (p *Pool) versionJobDescriptor(ver *core.BranchVersion, claimedBy string) claimedJobDescriptor {
	if ver == nil {
		return claimedJobDescriptor{}
	}
	if claimedBy == "" {
		claimedBy = ver.ClaimedBy
	}
	if claimedBy == "" {
		claimedBy = p.workerID
	}
	var requeueFn func(context.Context, time.Time, string) error
	var failFn func(context.Context, string) error
	if p.branches != nil {
		requeueFn = func(ctx context.Context, nextRetryAt time.Time, errMsg string) error {
			return p.branches.RequeueVersionForRetry(ctx, ver.ID, claimedBy, nextRetryAt, errMsg)
		}
		failFn = func(ctx context.Context, errMsg string) error {
			return p.branches.FailClaimedVersion(ctx, ver.ID, claimedBy, errMsg)
		}
	}
	return claimedJobDescriptor{
		kind:          "branch_version",
		id:            ver.ID,
		claimedBy:     claimedBy,
		reservationID: ver.ReservationID,
		attemptCount:  ver.AttemptCount,
		expiresAt:     ver.ExpiresAt,
		requeueFn:     requeueFn,
		failFn:        failFn,
	}
}
