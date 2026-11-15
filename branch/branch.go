// Package branch provides domain services for managing targeted resume branches,
// version lifecycles, iterative refinements, and asynchronous generation coordination.
//
// A branch represents a tailored variant of a candidate's main career profile
// targeted at a specific job description, company, or target role. Each branch
// maintains an immutable history of generated resume versions.
//
// Key capabilities include:
//   - Branch lifecycle management (Create, Branch, ListBranches, UpdateBranch, DeleteBranch)
//   - Asynchronous generation dispatch to the worker pool
//   - Version creation, inspection, and refinement iteration loops
//   - Synchronization with upstream Main Profile updates
//   - Live template rendering across HTML, Markdown, and Plain Text formats
package branch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/resumeee-app/resumeee-core"
)

// Default tenant quota ceilings and asynchronous job admission bounds applied when Config limits are unset.
const (
	// defaultDailyRequestLimit caps daily LLM requests per user when Config.DailyRequestLimit is zero or negative.
	defaultDailyRequestLimit = 50
	// defaultMonthlyTokenLimit caps monthly LLM tokens per user when Config.MonthlyTokenLimit is zero or negative.
	defaultMonthlyTokenLimit = 1000000
	// defaultCleanupTimeout bounds detached quota-release contexts when Config.CleanupTimeout is zero or negative.
	defaultCleanupTimeout = 10 * time.Second
)

// Config defines the dependencies required to construct a branch Service.
type Config struct {
	Branches            Store
	Profiles            ProfileReader
	Presets             PresetReader
	Templates           TemplateReader
	Quotas              QuotaManager
	JobEnqueuer         JobEnqueuer
	JobNotifier         core.JobNotifier
	Validator           ProvenanceValidator
	Renderer            TemplateRenderer
	Generator           Generator
	PromptCompiler      PromptCompiler
	Logger              *slog.Logger
	DailyRequestLimit   int
	MonthlyTokenLimit   int
	MaxPendingPerTenant int
	JobTTL              time.Duration
	CleanupTimeout      time.Duration
	Clock               func() time.Time
}

// Service manages resume branch lifecycles, version generation, gap analysis, and ATS scoring.
type Service struct {
	branches            Store
	profiles            ProfileReader
	presets             PresetReader
	templates           TemplateReader
	quotas              QuotaManager
	jobEnqueuer         JobEnqueuer
	jobNotifier         core.JobNotifier
	validator           ProvenanceValidator
	renderer            TemplateRenderer
	generator           Generator
	promptCompiler      PromptCompiler
	logger              *slog.Logger
	dailyRequestLimit   int
	monthlyTokenLimit   int
	maxPendingPerTenant int
	jobTTL              time.Duration
	cleanupTimeout      time.Duration
	clock               func() time.Time
	atsInFlightMu       sync.Mutex
	atsInFlight         map[string]bool
}

// New constructs a branch Service with the supplied configuration and dependencies.
func New(cfg Config) *Service {
	dailyLimit := cfg.DailyRequestLimit
	if dailyLimit <= 0 {
		dailyLimit = defaultDailyRequestLimit
	}
	monthlyLimit := cfg.MonthlyTokenLimit
	if monthlyLimit <= 0 {
		monthlyLimit = defaultMonthlyTokenLimit
	}
	maxPending := cfg.MaxPendingPerTenant
	if maxPending <= 0 {
		maxPending = core.DefaultMaxPendingPerTenant
	}
	jobTTL := cfg.JobTTL
	if jobTTL <= 0 {
		jobTTL = core.DefaultJobTTL
	}
	cleanupTimeout := cfg.CleanupTimeout
	if cleanupTimeout <= 0 {
		cleanupTimeout = defaultCleanupTimeout
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}

	return &Service{
		branches:            cfg.Branches,
		profiles:            cfg.Profiles,
		presets:             cfg.Presets,
		templates:           cfg.Templates,
		quotas:              cfg.Quotas,
		jobEnqueuer:         cfg.JobEnqueuer,
		jobNotifier:         cfg.JobNotifier,
		validator:           cfg.Validator,
		renderer:            cfg.Renderer,
		generator:           cfg.Generator,
		promptCompiler:      cfg.PromptCompiler,
		logger:              cfg.Logger,
		dailyRequestLimit:   dailyLimit,
		monthlyTokenLimit:   monthlyLimit,
		maxPendingPerTenant: maxPending,
		jobTTL:              jobTTL,
		cleanupTimeout:      cleanupTimeout,
		clock:               clock,
		atsInFlight:         make(map[string]bool),
	}
}

// now returns the current UTC timestamp from the configured service clock.
func (s *Service) now() time.Time {
	if s.clock != nil {
		return s.clock().UTC()
	}
	return time.Now().UTC()
}

// releaseReservationWithDetachedContext refunds a single quota reservation using a bounded detached context
// so caller context cancellation cannot leak reserved tenant quota.
func (s *Service) releaseReservationWithDetachedContext(reservationID, reason string) {
	if s.quotas == nil || reservationID == "" {
		return
	}
	timeout := s.cleanupTimeout
	if timeout <= 0 {
		timeout = defaultCleanupTimeout
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := s.quotas.ReleaseQuota(cleanupCtx, reservationID, reason); err != nil && s.logger != nil {
		s.logger.Warn("failed to release quota reservation", "reservation_id", reservationID, "reason", reason, "err", err)
	}
}

// releaseReservationsWithDetachedContext refunds quota reservations for superseded, aborted,
// expired, or cancelled versions using bounded detached contexts.
func (s *Service) releaseReservationsWithDetachedContext(released []core.ReleasedReservation) {
	if s.quotas == nil || len(released) == 0 {
		return
	}
	for _, r := range released {
		if r.ReservationID == "" {
			continue
		}
		s.releaseReservationWithDetachedContext(r.ReservationID, r.Reason)
	}
}

// supersedePendingVersions cancels queued pending versions on branchID and releases their quota reservations.
func (s *Service) supersedePendingVersions(ctx context.Context, branchID, reason string) {
	released, err := s.branches.SupersedePendingVersions(ctx, branchID, reason)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("failed to supersede pending versions", "branch_id", branchID, "reason", reason, "err", err)
		}
		return
	}
	s.releaseReservationsWithDetachedContext(released)
}

// abortBranchVersions cancels pending and processing versions on branchID and releases their quota reservations.
func (s *Service) abortBranchVersions(ctx context.Context, branchID, reason string) error {
	released, err := s.branches.AbortBranchVersions(ctx, branchID, reason)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("failed to abort branch versions", "branch_id", branchID, "reason", reason, "err", err)
		}
		return err
	}
	s.releaseReservationsWithDetachedContext(released)
	return nil
}

// equalStringPtr reports whether two optional string pointers are both nil or point to equal values.
func equalStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// branchGenerationOrActiveChanged reports whether updated alters active version selection or tailoring configuration.
func branchGenerationOrActiveChanged(existing, updated *core.Branch) bool {
	if existing == nil || updated == nil {
		return existing != updated
	}
	return !equalStringPtr(existing.ActiveVersionID, updated.ActiveVersionID) ||
		existing.TargetRole != updated.TargetRole ||
		existing.TargetCompany != updated.TargetCompany ||
		existing.TargetRequirements != updated.TargetRequirements ||
		existing.CustomDirectives != updated.CustomDirectives ||
		existing.BranchInstructions != updated.BranchInstructions ||
		!equalStringPtr(existing.TailoringStrategyID, updated.TailoringStrategyID) ||
		!equalStringPtr(existing.PresetID, updated.PresetID) ||
		existing.TemplateSetID != updated.TemplateSetID ||
		existing.ProviderOverride != updated.ProviderOverride ||
		existing.ModelOverride != updated.ModelOverride ||
		existing.ReasoningLevel != updated.ReasoningLevel ||
		!slices.Equal(existing.PinnedEntityIDs, updated.PinnedEntityIDs) ||
		!slices.Equal(existing.ExcludedEntityIDs, updated.ExcludedEntityIDs)
}

// notifyJob wakes background workers via JobNotifier or falls back to JobEnqueuer without
// failing an already-persisted pending version if the wakeup signal cannot be delivered.
func (s *Service) notifyJob(job core.GenerationJob) {
	if s.jobNotifier != nil {
		s.jobNotifier.Notify()
		return
	}
	if s.jobEnqueuer != nil {
		if err := s.jobEnqueuer.Enqueue(job); err != nil && s.logger != nil {
			s.logger.Warn("failed to notify worker pool of pending generation job",
				"version_id", job.VersionID,
				"branch_id", job.BranchID,
				"err", err,
			)
		}
	}
}

// validateBranchReferences verifies that template and preset IDs referenced by branch exist and belong to the tenant.
func (s *Service) validateBranchReferences(ctx context.Context, branch *core.Branch) error {
	if s.templates != nil && branch.TemplateSetID != "" {
		tmpl, err := s.templates.Template(ctx, branch.UserID, branch.TemplateSetID)
		if err != nil {
			return fmt.Errorf("%w: referenced template set %s: %v", core.ErrValidation, branch.TemplateSetID, err)
		}
		if tmpl != nil && tmpl.UserID != branch.UserID && !tmpl.IsBuiltIn {
			return fmt.Errorf("%w: template set %s belongs to another tenant", core.ErrForbidden, branch.TemplateSetID)
		}
	}
	presetID := branch.PresetID
	if presetID == nil && branch.TailoringStrategyID != nil {
		presetID = branch.TailoringStrategyID
	}
	if s.presets != nil && presetID != nil && *presetID != "" {
		preset, err := s.presets.Preset(ctx, branch.UserID, *presetID)
		if err != nil {
			return fmt.Errorf("%w: referenced preset %s: %v", core.ErrValidation, *presetID, err)
		}
		if preset != nil && preset.UserID != branch.UserID && !preset.IsBuiltIn() {
			return fmt.Errorf("%w: preset %s belongs to another tenant", core.ErrForbidden, *presetID)
		}
	}
	return nil
}

// isGapReportEmpty reports whether g has no score, summary, or gap items populated.
func isGapReportEmpty(g *core.GapReport) bool {
	if g == nil {
		return true
	}
	return g.MatchScore == nil && g.OverallMatchSummary == "" && len(g.MissingRequirements) == 0 && len(g.IdentifiedGaps) == 0
}

// ensureGapReportScore normalizes g and computes a deterministic match score when missing.
func ensureGapReportScore(g *core.GapReport) {
	if g == nil {
		return
	}
	g.Normalize()
	if g.MatchScore == nil {
		score := core.CalculateDeterministicMatchScore(g.MissingRequirements)
		g.MatchScore = &score
	}
}

// hydrateBranchFromActiveVersion copies cached gap and ATS metrics from ver onto branch.
func hydrateBranchFromActiveVersion(branch *core.Branch, ver *core.BranchVersion, overwriteExisting bool) {
	if branch == nil || ver == nil {
		return
	}
	if overwriteExisting {
		branch.LastSyncedMainRevision = ver.MainRevisionUsed
		if ver.GapReport != nil {
			ensureGapReportScore(ver.GapReport)
		}
		branch.GapReport = ver.GapReport
		if ver.ATSScore != nil {
			ver.ATSScore.Normalize()
		}
		branch.ATSScore = ver.ATSScore
		return
	}
	if isGapReportEmpty(branch.GapReport) && ver.GapReport != nil {
		ensureGapReportScore(ver.GapReport)
		branch.GapReport = ver.GapReport
	}
	if branch.ATSScore == nil && ver.ATSScore != nil {
		ver.ATSScore.Normalize()
		branch.ATSScore = ver.ATSScore
	}
}

// populateBranchActiveMetrics normalizes metrics on branch and falls back to the active version if incomplete.
func (s *Service) populateBranchActiveMetrics(ctx context.Context, branch *core.Branch) {
	if branch == nil {
		return
	}
	hasGapReport := !isGapReportEmpty(branch.GapReport)
	if hasGapReport {
		ensureGapReportScore(branch.GapReport)
	}
	if branch.ATSScore != nil {
		branch.ATSScore.Normalize()
	}
	if (hasGapReport && branch.ATSScore != nil) || branch.ActiveVersionID == nil || *branch.ActiveVersionID == "" || s.branches == nil {
		return
	}
	ver, err := s.branches.Version(ctx, branch.ID, *branch.ActiveVersionID)
	if err == nil && ver != nil {
		hydrateBranchFromActiveVersion(branch, ver, false)
	}
}

// ListBranches returns all branches for userID, optionally filtering for branches stale against main.
func (s *Service) ListBranches(ctx context.Context, userID string, staleOnly bool) ([]core.Branch, error) {
	branches, err := s.branches.ListBranches(ctx, userID)
	if err != nil {
		return nil, err
	}

	s.hydrateBranchListMetrics(ctx, branches)

	if staleOnly {
		filtered := []core.Branch{}
		for _, b := range branches {
			if b.IsStale {
				filtered = append(filtered, b)
			}
		}
		return filtered, nil
	}

	if branches == nil {
		branches = []core.Branch{}
	}
	return branches, nil
}

// hydrateBranchListMetrics normalizes cached metrics and batch-loads missing metrics from active versions.
func (s *Service) hydrateBranchListMetrics(ctx context.Context, branches []core.Branch) {
	var missingActiveVersionIDs []string
	for i := range branches {
		hasGap := !isGapReportEmpty(branches[i].GapReport)
		if hasGap {
			ensureGapReportScore(branches[i].GapReport)
		}
		if branches[i].ATSScore != nil {
			branches[i].ATSScore.Normalize()
		}
		if (!hasGap || branches[i].ATSScore == nil) && branches[i].ActiveVersionID != nil && *branches[i].ActiveVersionID != "" {
			missingActiveVersionIDs = append(missingActiveVersionIDs, *branches[i].ActiveVersionID)
		}
	}

	if len(missingActiveVersionIDs) == 0 || s.branches == nil {
		return
	}
	activeVersions, err := s.branches.VersionsByIDs(ctx, missingActiveVersionIDs)
	if err != nil {
		return
	}
	versionMap := make(map[string]*core.BranchVersion, len(activeVersions))
	for i := range activeVersions {
		versionMap[activeVersions[i].ID] = &activeVersions[i]
	}
	for i := range branches {
		if branches[i].ActiveVersionID != nil {
			if ver, ok := versionMap[*branches[i].ActiveVersionID]; ok {
				hydrateBranchFromActiveVersion(&branches[i], ver, false)
			}
		}
	}
}

// Branch retrieves a branch record by its identifier and hydrates its active metrics.
func (s *Service) Branch(ctx context.Context, userID, id string) (*core.Branch, error) {
	branch, err := s.branches.Branch(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	s.populateBranchActiveMetrics(ctx, branch)
	return branch, nil
}

// CreateBranch initializes and saves a new resume branch synchronized to the main revision.
func (s *Service) CreateBranch(ctx context.Context, branch *core.Branch) (*core.Branch, error) {
	if err := s.validateBranchReferences(ctx, branch); err != nil {
		return nil, err
	}

	if branch.ID == "" {
		branch.ID = "branch-" + uuid.New().String()
	}

	if s.profiles != nil {
		prof, err := s.profiles.Profile(ctx, branch.UserID)
		if err == nil && prof != nil {
			branch.LastSyncedMainRevision = prof.MainRevision
		}
	}

	if err := s.branches.CreateBranch(ctx, branch); err != nil {
		return nil, err
	}

	return s.branches.Branch(ctx, branch.UserID, branch.ID)
}

// UpdateBranch modifies branch configuration while preserving default branch immutability invariants.
func (s *Service) UpdateBranch(ctx context.Context, branch *core.Branch) (*core.Branch, error) {
	if err := s.validateBranchReferences(ctx, branch); err != nil {
		return nil, err
	}

	existing, err := s.branches.Branch(ctx, branch.UserID, branch.ID)
	if err != nil {
		return nil, err
	}

	if existing.IsDefault {
		return s.updateDefaultBranch(ctx, branch, existing)
	}

	if branch.ActiveVersionID != nil && *branch.ActiveVersionID != "" {
		if err := s.validateActiveVersion(ctx, branch); err != nil {
			return nil, err
		}
	}

	if err := s.branches.UpdateBranch(ctx, branch); err != nil {
		return nil, err
	}
	if branchGenerationOrActiveChanged(existing, branch) {
		_ = s.abortBranchVersions(ctx, branch.ID, "superseded by branch update")
	} else {
		s.supersedePendingVersions(ctx, branch.ID, "superseded by branch update")
	}
	return s.Branch(ctx, branch.UserID, branch.ID)
}

// updateDefaultBranch enforces immutability on the default branch and re-renders if the template changed.
func (s *Service) updateDefaultBranch(ctx context.Context, branch, existing *core.Branch) (*core.Branch, error) {
	if branch.TargetRole != "" || branch.TargetCompany != "" || branch.TargetRequirements != "" ||
		branch.BranchInstructions != "" || branch.PresetID != nil || branch.ProviderOverride != "" ||
		branch.ModelOverride != "" || branch.ReasoningLevel != "" || len(branch.PinnedEntityIDs) > 0 || len(branch.ExcludedEntityIDs) > 0 {
		return nil, fmt.Errorf("%w: default branch cannot be tailored with custom roles, presets, or instructions", core.ErrDefaultBranchImmutable)
	}
	if branch.Name != "" && branch.Name != core.DefaultBranchName {
		return nil, fmt.Errorf("%w: default branch name cannot be altered", core.ErrDefaultBranchImmutable)
	}

	branch.IsDefault = true
	branch.Name = core.DefaultBranchName

	templateChanged := branch.TemplateSetID != "" && branch.TemplateSetID != existing.TemplateSetID
	if templateChanged && s.templates != nil {
		if _, err := s.templates.Template(ctx, branch.UserID, branch.TemplateSetID); err != nil {
			return nil, err
		}
	}

	if err := s.branches.UpdateBranch(ctx, branch); err != nil {
		return nil, err
	}

	if templateChanged {
		if _, err := s.renderDefaultBranchVersion(ctx, branch.UserID, branch); err != nil {
			return nil, err
		}
	}

	return s.Branch(ctx, branch.UserID, branch.ID)
}

// validateActiveVersion verifies that the target active version exists, belongs to branch, and completed.
func (s *Service) validateActiveVersion(ctx context.Context, branch *core.Branch) error {
	ver, err := s.branches.Version(ctx, "", *branch.ActiveVersionID)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return fmt.Errorf("%w: active version %s not found", core.ErrValidation, *branch.ActiveVersionID)
		}
		return err
	}
	if ver.BranchID != branch.ID {
		return fmt.Errorf("%w: version %s belongs to branch %s, not %s", core.ErrValidation, ver.ID, ver.BranchID, branch.ID)
	}
	if ver.Status != core.VersionStatusCompleted && ver.Status != core.VersionStatusCompletedWithWarnings {
		return fmt.Errorf("%w: active version %s must be completed (got %s)", core.ErrValidation, ver.ID, ver.Status)
	}
	hydrateBranchFromActiveVersion(branch, ver, true)
	return nil
}

// DeleteBranch aborts active versions, releases reserved quota, and removes a non-default branch.
func (s *Service) DeleteBranch(ctx context.Context, userID, id string) error {
	branch, err := s.branches.Branch(ctx, userID, id)
	if err != nil {
		return err
	}
	if branch.IsDefault {
		return fmt.Errorf("%w: cannot delete default branch", core.ErrDefaultBranchImmutable)
	}
	if err := s.abortBranchVersions(ctx, id, "branch deleted"); err != nil {
		return err
	}
	return s.branches.DeleteBranch(ctx, userID, id)
}
