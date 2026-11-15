package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// defaultLLMJobTimeout caps individual LLM generation calls when Config.LLMTimeout is unset.
const defaultLLMJobTimeout = 10 * time.Minute

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
		p.failAndRefundVersion(ver, core.ErrJobExpired.Error())
		return
	}

	inputs, err := p.loadGenerationInputs(attemptCtx, ver)
	if err != nil {
		stopHeartbeat()
		p.failAndRefundVersion(ver, err.Error())
		return
	}

	if !p.verifyVersionRunnableBeforeLLM(attemptCtx, ver, claimedBy, inputs) {
		stopHeartbeat()
		return
	}

	resp, ok := p.runTailoringGeneration(attemptCtx, ver, claimedBy, inputs, stopHeartbeat)
	if !ok {
		return
	}

	if p.validator == nil {
		stopHeartbeat()
		p.failClaimedVersionOnly(ver, claimedBy, "no provenance validator configured")
		return
	}
	valRes, err := p.validator.Validate(&resp.TailoredContent, inputs.snapshot, inputs.branch)
	if err != nil {
		stopHeartbeat()
		p.failClaimedVersionOnly(ver, claimedBy, fmt.Sprintf("provenance validation error: %v", err))
		return
	}

	if p.renderer == nil {
		stopHeartbeat()
		p.failClaimedVersionOnly(ver, claimedBy, "no template renderer configured")
		return
	}
	rendered, err := p.renderer.Render(attemptCtx, inputs.template, inputs.profile, valRes.SanitizedContent)
	if err != nil {
		stopHeartbeat()
		p.failClaimedVersionOnly(ver, claimedBy, fmt.Sprintf("template render error: %v", err))
		return
	}

	stopHeartbeat()
	p.persistCompletedVersion(attemptCtx, ver, claimedBy, inputs, resp, valRes, rendered)
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
		p.failAndRefundVersion(ver, fmt.Sprintf("failed to verify version state: %v", err))
		return false
	}
	if latest.Status != core.VersionStatusProcessing || latest.ClaimedBy != claimedBy {
		return false
	}
	if latest.ExpiresAt != nil && !latest.ExpiresAt.After(p.now()) {
		p.failAndRefundVersion(ver, core.ErrJobExpired.Error())
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
		p.failAndRefundVersion(ver, "version superseded by newer active version")
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

// runTailoringGeneration builds the sanitized request, executes Generator with timeout, handles errors/retries, and commits quota.
func (p *Pool) runTailoringGeneration(
	attemptCtx context.Context,
	ver *core.BranchVersion,
	claimedBy string,
	inputs *generationInputs,
	stopHeartbeat func(),
) (*core.GenerationResponse, bool) {
	if p.generator == nil {
		stopHeartbeat()
		p.failAndRefundVersion(ver, "no LLM service configured")
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
	llmCtx, llmCancel := context.WithTimeout(attemptCtx, timeout)
	resp, err := p.generator.GenerateTailoredResume(llmCtx, req)
	llmCancel()

	if err != nil {
		stopHeartbeat()
		p.handleVersionGenerationError(attemptCtx, ver, claimedBy, err)
		return nil, false
	}
	if attemptCtx.Err() != nil {
		stopHeartbeat()
		if p.isShuttingDown() {
			p.failAndRefundVersion(ver, "Generation interrupted by worker shutdown.")
		} else {
			p.failAndRefundVersion(ver, core.ErrLeaseExpired.Error())
		}
		return nil, false
	}
	if resp == nil {
		stopHeartbeat()
		p.failAndRefundVersion(ver, "llm returned empty generation response")
		return nil, false
	}

	p.commitUsageDetached(ver.ReservationID, resp.Usage)
	return resp, true
}

// handleVersionGenerationError routes Generator failures to shutdown abort, fail-fast refund, or bounded retry requeue.
func (p *Pool) handleVersionGenerationError(attemptCtx context.Context, ver *core.BranchVersion, claimedBy string, err error) {
	if p.logger != nil {
		p.logger.Error("llm generation failed",
			"version_id", ver.ID,
			"worker_id", claimedBy,
			"attempt", ver.AttemptCount,
			"error", err,
		)
	}

	if p.isShuttingDown() {
		p.failAndRefundVersion(ver, "Generation interrupted by worker shutdown.")
		return
	}
	if attemptCtx.Err() != nil {
		p.failAndRefundVersion(ver, core.ErrLeaseExpired.Error())
		return
	}

	errClass := core.ClassifyError(err)
	errMsg := userFacingTailoringError(err, errClass)

	if errClass == core.ErrorClassTransient {
		attempt := ver.AttemptCount
		if attempt <= 0 {
			attempt = 1
		}
		if attempt < p.maxAttempts {
			delay := computeRetryDelay(attempt, p.retryBaseDelay, p.maxRetryDelay)
			nextRetryAt := p.now().Add(delay)
			if ver.ExpiresAt == nil || nextRetryAt.Before(*ver.ExpiresAt) {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), p.cleanupDuration())
				reqErr := p.branches.RequeueVersionForRetry(cleanupCtx, ver.ID, claimedBy, nextRetryAt, errMsg)
				cancel()

				if reqErr == nil {
					p.scheduleRetryTimer(delay)
					return
				}
				if errors.Is(reqErr, core.ErrConflict) || errors.Is(reqErr, core.ErrNotFound) {
					if p.logger != nil {
						p.logger.Warn("skipping retry requeue after lease loss", "version_id", ver.ID, "err", reqErr)
					}
					return
				}
			}
		}
	}

	p.failAndRefundVersion(ver, errMsg)
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

// persistCompletedVersion updates version fields with sanitized content and rendered outputs and marks it complete.
func (p *Pool) persistCompletedVersion(
	ctx context.Context,
	claimedVer *core.BranchVersion,
	claimedBy string,
	inputs *generationInputs,
	resp *core.GenerationResponse,
	valRes *core.ValidationResult,
	rendered *core.RenderOutput,
) {
	ver, err := p.branches.Version(ctx, inputs.branch.ID, claimedVer.ID)
	if err != nil {
		p.failClaimedVersionOnly(claimedVer, claimedBy, fmt.Sprintf("failed to get version: %v", err))
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
		p.failClaimedVersionOnly(claimedVer, claimedBy, fmt.Sprintf("failed to complete version: %v", err))
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

// failAndRefundVersion fails an actively claimed version using a bounded detached context and refunds its quota.
func (p *Pool) failAndRefundVersion(ver *core.BranchVersion, errMsg string) {
	if ver == nil {
		return
	}
	claimedBy := ver.ClaimedBy
	if claimedBy == "" {
		claimedBy = p.workerID
	}
	if p.logger != nil {
		p.logger.Error("generation job failed", "version_id", ver.ID, "worker_id", claimedBy, "error", errMsg)
	}
	if p.branches == nil {
		return
	}

	cleanupCtx, cancel := context.WithTimeout(context.Background(), p.cleanupDuration())
	defer cancel()

	if err := p.branches.FailClaimedVersion(cleanupCtx, ver.ID, claimedBy, errMsg); err != nil {
		if p.logger != nil && !errors.Is(err, core.ErrConflict) && !errors.Is(err, core.ErrNotFound) {
			p.logger.Error("failed to record claimed version failure in store", "version_id", ver.ID, "error", err)
		}
		return
	}
	p.releaseJobQuota(cleanupCtx, ver.ReservationID, errMsg)
}

// failClaimedVersionOnly marks a claimed version failed after usage was already committed, leaving active branch intact.
func (p *Pool) failClaimedVersionOnly(ver *core.BranchVersion, claimedBy, errMsg string) {
	if ver == nil || p.branches == nil {
		return
	}
	if claimedBy == "" {
		claimedBy = p.workerID
	}
	if p.logger != nil {
		p.logger.Error("generation job failed post-commit", "version_id", ver.ID, "worker_id", claimedBy, "error", errMsg)
	}

	cleanupCtx, cancel := context.WithTimeout(context.Background(), p.cleanupDuration())
	defer cancel()

	if err := p.branches.FailClaimedVersion(cleanupCtx, ver.ID, claimedBy, errMsg); err != nil && p.logger != nil {
		if !errors.Is(err, core.ErrConflict) && !errors.Is(err, core.ErrNotFound) {
			p.logger.Error("failed to record claimed version failure in store", "version_id", ver.ID, "error", err)
		}
	}
}

// commitUsageDetached commits token usage for reservationID using a bounded detached cleanup context.
func (p *Pool) commitUsageDetached(reservationID string, usage core.TokenUsage) {
	if p.quotas == nil || reservationID == "" {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), p.cleanupDuration())
	defer cancel()
	if err := p.quotas.CommitUsage(cleanupCtx, reservationID, usage); err != nil && p.logger != nil {
		p.logger.Warn("failed to commit quota usage", "reservation_id", reservationID, "err", err)
	}
}

// releaseJobQuota releases reserved tenant quota units when a job fails prior to commitment.
func (p *Pool) releaseJobQuota(ctx context.Context, reservationID, reason string) {
	if p.quotas == nil || reservationID == "" {
		return
	}
	if err := p.quotas.ReleaseQuota(ctx, reservationID, reason); err != nil && p.logger != nil {
		p.logger.Warn("failed to release job quota", "reservation_id", reservationID, "reason", reason, "err", err)
	}
}

// startLeaseHeartbeat periodically renews a claimed job lease and cancels attemptCancel if ownership is lost.
func (p *Pool) startLeaseHeartbeat(
	parentCtx context.Context,
	attemptCancel context.CancelFunc,
	id string,
	renewFn func(ctx context.Context, leaseExpiresAt time.Time) error,
	kind string,
) func() {
	interval := p.leaseRenewInterval
	if interval <= 0 {
		interval = p.leaseDuration / 3
	}
	if interval <= 0 || renewFn == nil {
		return func() {}
	}

	stopCh := make(chan struct{})
	done := make(chan struct{})
	var stopOnce sync.Once

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-parentCtx.Done():
				return
			case <-ticker.C:
				renewCtx, cancel := context.WithTimeout(parentCtx, p.cleanupDuration())
				err := renewFn(renewCtx, p.now().Add(p.leaseDuration))
				cancel()
				if err != nil {
					if errors.Is(err, core.ErrConflict) || errors.Is(err, core.ErrNotFound) {
						if p.logger != nil {
							p.logger.Warn("lost worker lease during execution; cancelling attempt",
								"kind", kind,
								"id", id,
								"worker_id", p.workerID,
								"err", err,
							)
						}
						if attemptCancel != nil {
							attemptCancel()
						}
						return
					}
					if p.logger != nil && parentCtx.Err() == nil {
						p.logger.Warn("failed to renew worker lease",
							"kind", kind,
							"id", id,
							"worker_id", p.workerID,
							"err", err,
						)
					}
				}
			}
		}
	}()

	return func() {
		stopOnce.Do(func() {
			close(stopCh)
		})
		<-done
	}
}

// computeRetryDelay calculates overflow-safe exponential backoff capped at maxDelay for 1-based attempt.
func computeRetryDelay(attempt int, base, maxDelay time.Duration) time.Duration {
	if base <= 0 {
		base = defaultRetryBaseDelay
	}
	if maxDelay <= 0 {
		maxDelay = defaultMaxRetryDelay
	}
	delay := base
	for i := 1; i < attempt; i++ {
		if delay >= maxDelay || delay > maxDelay/2 {
			return maxDelay
		}
		delay *= 2
	}
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}
