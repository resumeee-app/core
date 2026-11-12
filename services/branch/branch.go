// Package branch provides domain services for managing targeted resume branches,
// version lifecycles, iterative refinements, and asynchronous generation coordination.
//
// A branch represents a tailored variant of a candidate's main career profile
// targeted at a specific job description, company, or target role. Each branch
// maintains an immutable history of generated resume versions.
//
// Key capabilities include:
//   - Branch lifecycle management (Create, Get, List, Update, Archive)
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/services/renderer"
)

// Service defines domain operations for branches, async generation, batch synchronization, and version lifecycle.
type Service interface {
	ListBranches(ctx context.Context, userID string, staleOnly bool) ([]core.Branch, error)
	GetBranch(ctx context.Context, userID, id string) (*core.Branch, error)
	CreateBranch(ctx context.Context, branch *core.Branch) (*core.Branch, error)
	UpdateBranch(ctx context.Context, branch *core.Branch) (*core.Branch, error)
	DeleteBranch(ctx context.Context, userID, id string) error

	// Version Lifecycle
	GenerateVersion(ctx context.Context, userID, branchID string) (*core.BranchVersion, error)
	SyncBranches(ctx context.Context, userID string, branchIDs []string) ([]string, error)
	ListVersions(ctx context.Context, branchID string) ([]core.BranchVersion, error)
	GetVersion(ctx context.Context, branchID, versionNumOrID string) (*core.BranchVersion, error)
	EditVersion(ctx context.Context, userID, branchID, parentVersionNumOrID string, editedContent core.TailoredContent) (*core.BranchVersion, error)
	AnalyzeGaps(ctx context.Context, userID, branchID string) (*core.GapReport, error)
	SimulateATS(ctx context.Context, userID, branchID string, opts *core.ATSSimulationOptions) (*core.ATSScore, error)
	GetPromptPreview(ctx context.Context, userID, branchID string, opts *core.PromptPreviewOptions) (*core.PromptPreview, error)
}

// Config defines the dependencies required by the branch domain service.
type Config struct {
	BranchRepo        core.BranchRepository
	ProfileRepo       core.ProfileRepository
	PresetRepo        core.PresetRepository
	TemplateRepo      core.TemplateRepository
	QuotaRepo         core.QuotaRepository
	JobEnqueuer       core.JobEnqueuer
	Validator         core.ProvenanceValidator
	Renderer          core.TemplateRenderer
	LLMService        core.LLMService
	PromptCompiler    core.PromptCompiler
	Logger            *slog.Logger
	DailyRequestLimit int
	MonthlyTokenLimit int
}

type service struct {
	branchRepo        core.BranchRepository
	profileRepo       core.ProfileRepository
	presetRepo        core.PresetRepository
	templateRepo      core.TemplateRepository
	quotaRepo         core.QuotaRepository
	jobEnqueuer       core.JobEnqueuer
	validator         core.ProvenanceValidator
	renderer          core.TemplateRenderer
	llmService        core.LLMService
	promptCompiler    core.PromptCompiler
	logger            *slog.Logger
	dailyRequestLimit int
	monthlyTokenLimit int
	atsInFlightMu     sync.Mutex
	atsInFlight       map[string]bool
}

// NewService constructs a new Branch service.
func NewService(cfg Config) Service {
	dailyLimit := cfg.DailyRequestLimit
	if dailyLimit <= 0 {
		dailyLimit = 50
	}
	monthlyLimit := cfg.MonthlyTokenLimit
	if monthlyLimit <= 0 {
		monthlyLimit = 1000000
	}

	return &service{
		branchRepo:        cfg.BranchRepo,
		profileRepo:       cfg.ProfileRepo,
		presetRepo:        cfg.PresetRepo,
		templateRepo:      cfg.TemplateRepo,
		quotaRepo:         cfg.QuotaRepo,
		jobEnqueuer:       cfg.JobEnqueuer,
		validator:         cfg.Validator,
		renderer:          cfg.Renderer,
		llmService:        cfg.LLMService,
		promptCompiler:    cfg.PromptCompiler,
		logger:            cfg.Logger,
		dailyRequestLimit: dailyLimit,
		monthlyTokenLimit: monthlyLimit,
		atsInFlight:       make(map[string]bool),
	}
}

func (s *service) validateBranchReferences(ctx context.Context, branch *core.Branch) error {
	if s.templateRepo != nil && branch.TemplateSetID != "" {
		tmpl, err := s.templateRepo.GetTemplate(ctx, branch.UserID, branch.TemplateSetID)
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
	if s.presetRepo != nil && presetID != nil && *presetID != "" {
		preset, err := s.presetRepo.GetPreset(ctx, branch.UserID, *presetID)
		if err != nil {
			return fmt.Errorf("%w: referenced preset %s: %v", core.ErrValidation, *presetID, err)
		}
		if preset != nil && preset.UserID != branch.UserID && !preset.IsBuiltIn() {
			return fmt.Errorf("%w: preset %s belongs to another tenant", core.ErrForbidden, *presetID)
		}
	}
	return nil
}

func isGapReportEmpty(g *core.GapReport) bool {
	if g == nil {
		return true
	}
	return g.MatchScore == nil && g.OverallMatchSummary == "" && len(g.MissingRequirements) == 0 && len(g.IdentifiedGaps) == 0
}

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

func (s *service) populateBranchActiveMetrics(ctx context.Context, branch *core.Branch) {
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
	if (hasGapReport && branch.ATSScore != nil) || branch.ActiveVersionID == nil || *branch.ActiveVersionID == "" || s.branchRepo == nil {
		return
	}
	ver, err := s.branchRepo.GetVersion(ctx, branch.ID, *branch.ActiveVersionID)
	if err == nil && ver != nil {
		hydrateBranchFromActiveVersion(branch, ver, false)
	}
}

// ListBranches returns all branches for the user, optionally filtering for stale branches whose synced revision is behind main.
func (s *service) ListBranches(ctx context.Context, userID string, staleOnly bool) ([]core.Branch, error) {
	branches, err := s.branchRepo.ListBranches(ctx, userID)
	if err != nil {
		return nil, err
	}

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

	if len(missingActiveVersionIDs) > 0 && s.branchRepo != nil {
		activeVersions, err := s.branchRepo.GetVersionsByIDs(ctx, missingActiveVersionIDs)
		if err == nil {
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
	}

	if staleOnly {
		var filtered []core.Branch
		for _, b := range branches {
			if b.IsStale {
				filtered = append(filtered, b)
			}
		}
		if filtered == nil {
			filtered = []core.Branch{}
		}
		return filtered, nil
	}

	if branches == nil {
		branches = []core.Branch{}
	}
	return branches, nil
}

// GetBranch retrieves a branch record by its identifier.
func (s *service) GetBranch(ctx context.Context, userID, id string) (*core.Branch, error) {
	branch, err := s.branchRepo.GetBranch(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	s.populateBranchActiveMetrics(ctx, branch)
	return branch, nil
}

// CreateBranch initializes and saves a new resume branch with main revision synchronization.
func (s *service) CreateBranch(ctx context.Context, branch *core.Branch) (*core.Branch, error) {
	if err := s.validateBranchReferences(ctx, branch); err != nil {
		return nil, err
	}

	if branch.ID == "" {
		branch.ID = "branch-" + uuid.New().String()
	}

	if s.profileRepo != nil {
		prof, err := s.profileRepo.GetProfile(ctx, branch.UserID)
		if err == nil && prof != nil {
			branch.LastSyncedMainRevision = prof.MainRevision
		}
	}

	if err := s.branchRepo.CreateBranch(ctx, branch); err != nil {
		return nil, err
	}

	return s.branchRepo.GetBranch(ctx, branch.UserID, branch.ID)
}

// UpdateBranch modifies branch configuration while preserving default branch immutability invariants.
func (s *service) UpdateBranch(ctx context.Context, branch *core.Branch) (*core.Branch, error) {
	if err := s.validateBranchReferences(ctx, branch); err != nil {
		return nil, err
	}

	existing, err := s.branchRepo.GetBranch(ctx, branch.UserID, branch.ID)
	if err != nil {
		return nil, err
	}

	if existing.IsDefault {
		// Enforce default branch immutability: disallow tailor fields
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
		if templateChanged {
			if s.templateRepo != nil {
				if _, err := s.templateRepo.GetTemplate(ctx, branch.UserID, branch.TemplateSetID); err != nil {
					return nil, err
				}
			}
		}

		if err := s.branchRepo.UpdateBranch(ctx, branch); err != nil {
			return nil, err
		}

		if templateChanged {
			if _, err := s.renderDefaultBranchVersion(ctx, branch.UserID, branch); err != nil {
				return nil, err
			}
		}

		return s.GetBranch(ctx, branch.UserID, branch.ID)
	}

	if branch.ActiveVersionID != nil && *branch.ActiveVersionID != "" {
		ver, err := s.branchRepo.GetVersion(ctx, "", *branch.ActiveVersionID)
		if err != nil {
			if errors.Is(err, core.ErrNotFound) {
				return nil, fmt.Errorf("%w: active version %s not found", core.ErrValidation, *branch.ActiveVersionID)
			}
			return nil, err
		}
		if ver.BranchID != branch.ID {
			return nil, fmt.Errorf("%w: version %s belongs to branch %s, not %s", core.ErrValidation, ver.ID, ver.BranchID, branch.ID)
		}
		if ver.Status != core.VersionStatusCompleted && ver.Status != core.VersionStatusCompletedWithWarnings {
			return nil, fmt.Errorf("%w: active version %s must be completed (got %s)", core.ErrValidation, ver.ID, ver.Status)
		}
		hydrateBranchFromActiveVersion(branch, ver, true)
	}

	if err := s.branchRepo.UpdateBranch(ctx, branch); err != nil {
		return nil, err
	}
	return s.GetBranch(ctx, branch.UserID, branch.ID)
}

// DeleteBranch removes a branch if it is not the immutable default branch.
func (s *service) DeleteBranch(ctx context.Context, userID, id string) error {
	branch, err := s.branchRepo.GetBranch(ctx, userID, id)
	if err == nil && branch != nil && branch.IsDefault {
		return fmt.Errorf("%w: cannot delete default branch", core.ErrDefaultBranchImmutable)
	}
	return s.branchRepo.DeleteBranch(ctx, userID, id)
}

// ListVersions returns all generated and edited versions belonging to a branch.
func (s *service) ListVersions(ctx context.Context, branchID string) ([]core.BranchVersion, error) {
	versions, err := s.branchRepo.ListVersions(ctx, branchID)
	if err != nil {
		return nil, err
	}
	if versions == nil {
		versions = []core.BranchVersion{}
	}
	return versions, nil
}

// GetVersion resolves a version by either sequential version number (1, 2, ...) or version UUID.
func (s *service) GetVersion(ctx context.Context, branchID, versionNumOrID string) (*core.BranchVersion, error) {
	if num, err := strconv.Atoi(versionNumOrID); err == nil && num > 0 {
		ver, err := s.branchRepo.GetVersionByNumber(ctx, branchID, num)
		if err == nil {
			return ver, nil
		}
	}
	return s.branchRepo.GetVersion(ctx, branchID, versionNumOrID)
}

// GenerateVersion enqueues an asynchronous generation job or synchronously renders default branch versions.
func (s *service) GenerateVersion(ctx context.Context, userID, branchID string) (*core.BranchVersion, error) {
	branch, err := s.branchRepo.GetBranch(ctx, userID, branchID)
	if err != nil {
		return nil, err
	}

	if branch.IsDefault {
		return s.renderDefaultBranchVersion(ctx, userID, branch)
	}

	var mainRevision int64 = 1
	provider := branch.ProviderOverride
	model := branch.ModelOverride

	if s.profileRepo != nil {
		prof, err := s.profileRepo.GetProfile(ctx, userID)
		if err == nil && prof != nil {
			mainRevision = prof.MainRevision
		}
	}

	var reservationID string
	if s.quotaRepo != nil {
		rec, qErr := s.quotaRepo.ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, s.dailyRequestLimit, s.monthlyTokenLimit)
		if qErr != nil {
			return nil, qErr
		}
		reservationID = rec.ID
	}

	ver := &core.BranchVersion{
		ID:                "ver-" + uuid.New().String(),
		UserID:            userID,
		BranchID:          branch.ID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  mainRevision,
		TemplateSetIDUsed: branch.TemplateSetID,
		ProviderUsed:      provider,
		ModelUsed:         model,
		SourceType:        core.SourceTypeLLMGeneration,
		ParentVersionID:   branch.ActiveVersionID,
	}

	if err := s.branchRepo.CreateVersion(ctx, ver); err != nil {
		if reservationID != "" && s.quotaRepo != nil {
			_ = s.quotaRepo.ReleaseQuota(ctx, reservationID, err.Error())
		}
		return nil, err
	}

	if s.jobEnqueuer != nil {
		job := core.GenerationJob{
			UserID:        userID,
			BranchID:      branch.ID,
			VersionID:     ver.ID,
			ReservationID: reservationID,
		}
		if err := s.jobEnqueuer.Enqueue(job); err != nil {
			if reservationID != "" && s.quotaRepo != nil {
				_ = s.quotaRepo.ReleaseQuota(ctx, reservationID, "generation queue full or error")
			}
			if errors.Is(err, core.ErrQueueFull) {
				_ = s.branchRepo.UpdateVersionStatus(ctx, ver.ID, core.VersionStatusFailed, "generation queue full")
				return nil, core.ErrQueueFull
			}
			return nil, err
		}
	}

	return ver, nil
}

// SyncBranches regenerates specified branches or all stale branches when branchIDs is empty.
func (s *service) SyncBranches(ctx context.Context, userID string, branchIDs []string) ([]string, error) {
	var mainRevision int64 = 1

	if s.profileRepo != nil {
		prof, err := s.profileRepo.GetProfile(ctx, userID)
		if err != nil {
			return nil, err
		}
		mainRevision = prof.MainRevision
	}

	var targetBranches []core.Branch
	if len(branchIDs) > 0 {
		for _, bID := range branchIDs {
			b, err := s.branchRepo.GetBranch(ctx, userID, bID)
			if err != nil {
				return nil, err
			}
			targetBranches = append(targetBranches, *b)
		}
	} else {
		allBranches, err := s.branchRepo.ListBranches(ctx, userID)
		if err != nil {
			return nil, err
		}
		for _, b := range allBranches {
			if b.IsStale {
				targetBranches = append(targetBranches, b)
			}
		}
	}

	var enqueuedIDs []string
	for _, b := range targetBranches {
		if b.IsDefault {
			ver, err := s.renderDefaultBranchVersion(ctx, userID, &b)
			if err != nil {
				return enqueuedIDs, err
			}
			enqueuedIDs = append(enqueuedIDs, ver.ID)
			continue
		}

		provider := b.ProviderOverride
		model := b.ModelOverride

		var reservationID string
		if s.quotaRepo != nil {
			rec, qErr := s.quotaRepo.ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, s.dailyRequestLimit, s.monthlyTokenLimit)
			if qErr != nil {
				return enqueuedIDs, qErr
			}
			reservationID = rec.ID
		}

		ver := &core.BranchVersion{
			ID:                "ver-" + uuid.New().String(),
			UserID:            userID,
			BranchID:          b.ID,
			Status:            core.VersionStatusPending,
			MainRevisionUsed:  mainRevision,
			TemplateSetIDUsed: b.TemplateSetID,
			ProviderUsed:      provider,
			ModelUsed:         model,
			SourceType:        core.SourceTypeLLMGeneration,
			ParentVersionID:   b.ActiveVersionID,
		}

		if err := s.branchRepo.CreateVersion(ctx, ver); err != nil {
			if reservationID != "" && s.quotaRepo != nil {
				_ = s.quotaRepo.ReleaseQuota(ctx, reservationID, err.Error())
			}
			return enqueuedIDs, err
		}

		if s.jobEnqueuer != nil {
			job := core.GenerationJob{
				UserID:        userID,
				BranchID:      b.ID,
				VersionID:     ver.ID,
				ReservationID: reservationID,
			}
			if err := s.jobEnqueuer.Enqueue(job); err != nil {
				if s.quotaRepo != nil && reservationID != "" {
					_ = s.quotaRepo.ReleaseQuota(ctx, reservationID, "failed to enqueue generation job")
				}
				if errors.Is(err, core.ErrQueueFull) {
					_ = s.branchRepo.UpdateVersionStatus(ctx, ver.ID, core.VersionStatusFailed, "generation queue full")
					return enqueuedIDs, core.ErrQueueFull
				}
				return enqueuedIDs, err
			}
		}
		enqueuedIDs = append(enqueuedIDs, ver.ID)
	}

	if enqueuedIDs == nil {
		enqueuedIDs = []string{}
	}

	return enqueuedIDs, nil
}

// EditVersion applies provenance validation to manual edits, renders templates, and appends a new immutable version.
func (s *service) EditVersion(ctx context.Context, userID, branchID, parentVersionNumOrID string, editedContent core.TailoredContent) (*core.BranchVersion, error) {
	branch, err := s.branchRepo.GetBranch(ctx, userID, branchID)
	if err != nil {
		return nil, err
	}

	if branch.IsDefault {
		return nil, fmt.Errorf("%w: default branch cannot be manually edited", core.ErrDefaultBranchImmutable)
	}

	parentVer, err := s.GetVersion(ctx, branchID, parentVersionNumOrID)
	if err != nil {
		return nil, err
	}

	snapshot, err := s.profileRepo.GetSnapshot(ctx, userID)
	if err != nil {
		return nil, err
	}

	prof, err := s.profileRepo.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}

	tmpl, err := s.templateRepo.GetTemplate(ctx, userID, branch.TemplateSetID)
	if err != nil {
		return nil, err
	}

	// 1. Provenance validation ("No Lies" invariant)
	if s.validator != nil {
		if err := s.validator.ValidateStrict(&editedContent, snapshot, branch); err != nil {
			return nil, fmt.Errorf("%w: %v", core.ErrProvenance, err)
		}
	}

	// 2. Synchronous Go template rendering
	var rendered *core.RenderOutput
	if s.renderer != nil {
		r, err := s.renderer.Render(ctx, tmpl, prof, &editedContent)
		if err != nil {
			return nil, fmt.Errorf("template rendering failed: %w", err)
		}
		rendered = r
	} else {
		rendered = &core.RenderOutput{}
	}

	// 3. Construct immutable sequential version vN+1
	newVer := &core.BranchVersion{
		ID:                "ver-" + uuid.New().String(),
		UserID:            branch.UserID,
		BranchID:          branch.ID,
		Status:            core.VersionStatusCompleted,
		MainRevisionUsed:  snapshot.MainRevision,
		TemplateSetIDUsed: branch.TemplateSetID,
		ProviderUsed:      "manual",
		ModelUsed:         "manual",
		SourceType:        core.SourceTypeManualEdit,
		ParentVersionID:   &parentVer.ID,
		TailoredContent:   &editedContent,
		GapReport:         parentVer.GapReport,
		HTMLOutput:        rendered.HTML,
		MarkdownOutput:    rendered.Markdown,
		PlainTextOutput:   rendered.PlainText,
		CreatedAt:         time.Now().UTC(),
	}

	if err := s.branchRepo.CreateVersion(ctx, newVer); err != nil {
		return nil, err
	}

	if err := s.branchRepo.CompleteVersion(ctx, newVer); err != nil {
		return nil, err
	}

	return newVer, nil
}

// renderDefaultBranchVersion synchronously renders a resume version for the default branch directly from the main profile.
func (s *service) renderDefaultBranchVersion(ctx context.Context, userID string, branch *core.Branch) (*core.BranchVersion, error) {
	if s.profileRepo == nil {
		return nil, errors.New("profile repository required for default branch rendering")
	}
	snapshot, err := s.profileRepo.GetSnapshot(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("getting profile snapshot for default branch: %w", err)
	}
	prof, err := s.profileRepo.GetProfile(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("getting profile for default branch: %w", err)
	}

	tmplID := branch.TemplateSetID
	if tmplID == "" {
		tmplID = renderer.TemplateStandardCleanID
		branch.TemplateSetID = tmplID
	}

	if s.templateRepo == nil {
		return nil, errors.New("template repository required for default branch rendering")
	}
	tmpl, err := s.templateRepo.GetTemplate(ctx, userID, tmplID)
	if err != nil {
		return nil, fmt.Errorf("getting template %s for default branch: %w", tmplID, err)
	}

	tailoredContent := core.SnapshotToTailoredContent(snapshot)

	var rendered *core.RenderOutput
	if s.renderer != nil {
		r, err := s.renderer.Render(ctx, tmpl, prof, tailoredContent)
		if err != nil {
			return nil, fmt.Errorf("rendering default branch: %w", err)
		}
		rendered = r
	} else {
		rendered = &core.RenderOutput{}
	}

	now := time.Now().UTC()
	ver := &core.BranchVersion{
		ID:                "ver-" + uuid.New().String(),
		UserID:            userID,
		BranchID:          branch.ID,
		Status:            core.VersionStatusCompleted,
		MainRevisionUsed:  snapshot.MainRevision,
		TemplateSetIDUsed: branch.TemplateSetID,
		ProviderUsed:      "none",
		ModelUsed:         "none",
		SourceType:        core.SourceTypeMainProfile,
		ParentVersionID:   branch.ActiveVersionID,
		TailoredContent:   tailoredContent,
		HTMLOutput:        rendered.HTML,
		MarkdownOutput:    rendered.Markdown,
		PlainTextOutput:   rendered.PlainText,
		CreatedAt:         now,
		CompletedAt:       &now,
	}

	if err := s.branchRepo.CreateVersion(ctx, ver); err != nil {
		return nil, fmt.Errorf("creating default branch version: %w", err)
	}

	if err := s.branchRepo.CompleteVersion(ctx, ver); err != nil {
		return nil, fmt.Errorf("completing default branch version: %w", err)
	}

	branch.ActiveVersionID = &ver.ID
	branch.LastSyncedMainRevision = snapshot.MainRevision

	return ver, nil
}

func (s *service) reserveOperationQuota(ctx context.Context, userID string, op core.LLMOperationType, estimatedTokens, fallbackTokens int) (func(core.TokenUsage, error), error) {
	if s.llmService == nil || s.quotaRepo == nil {
		return func(core.TokenUsage, error) {}, nil
	}
	rec, err := s.quotaRepo.ReserveQuota(ctx, userID, op, estimatedTokens, s.dailyRequestLimit, s.monthlyTokenLimit)
	if err != nil {
		return nil, err
	}
	return func(usage core.TokenUsage, retErr error) {
		if rec == nil {
			return
		}
		if retErr != nil {
			_ = s.quotaRepo.ReleaseQuota(ctx, rec.ID, retErr.Error())
			return
		}
		if usage.TotalTokens == 0 {
			usage.TotalTokens = fallbackTokens
		}
		_ = s.quotaRepo.CommitUsage(ctx, rec.ID, usage)
	}, nil
}

func (s *service) tryAcquireATSInFlight(versionID string) bool {
	s.atsInFlightMu.Lock()
	defer s.atsInFlightMu.Unlock()
	if s.atsInFlight[versionID] {
		return false
	}
	s.atsInFlight[versionID] = true
	return true
}

func (s *service) releaseATSInFlight(versionID string) {
	s.atsInFlightMu.Lock()
	defer s.atsInFlightMu.Unlock()
	delete(s.atsInFlight, versionID)
}

// AnalyzeGaps performs fast decoupled gap analysis for a branch against the main profile snapshot.
func (s *service) AnalyzeGaps(ctx context.Context, userID, branchID string) (*core.GapReport, error) {
	var (
		retErr error
		usage  core.TokenUsage
	)
	finishQuota, err := s.reserveOperationQuota(ctx, userID, core.OperationGapAnalysis, 1000, 500)
	if err != nil {
		return nil, err
	}
	defer func() {
		finishQuota(usage, retErr)
	}()

	branch, err := s.branchRepo.GetBranch(ctx, userID, branchID)
	if err != nil {
		retErr = err
		return nil, retErr
	}

	snapshot, err := s.profileRepo.GetSnapshot(ctx, userID)
	if err != nil {
		retErr = err
		return nil, retErr
	}

	// Fetch active version once if present
	var activeVer *core.BranchVersion
	if branch.ActiveVersionID != nil && *branch.ActiveVersionID != "" {
		v, vErr := s.branchRepo.GetVersion(ctx, branch.ID, *branch.ActiveVersionID)
		if vErr != nil && s.logger != nil {
			s.logger.Warn("could not fetch active version for gap analysis", "branch_id", branch.ID, "version_id", *branch.ActiveVersionID, "err", vErr)
		} else {
			activeVer = v
		}
	}

	var report *core.GapReport
	if s.llmService != nil {
		req := core.GenerationRequest{
			Model:          branch.ModelOverride,
			ReasoningLevel: branch.ReasoningLevel,
			MainProfile:    snapshot,
			BranchContext:  *branch,
		}
		if analyzer, ok := s.llmService.(interface {
			GenerateGapAnalysis(ctx context.Context, req core.GenerationRequest) (*core.GapReport, error)
		}); ok {
			r, err := analyzer.GenerateGapAnalysis(ctx, req)
			if err != nil {
				retErr = fmt.Errorf("gap analysis generation failed: %w", err)
				return nil, retErr
			}
			report = r
		} else {
			resp, err := s.llmService.GenerateTailoredResume(ctx, req)
			if err != nil {
				retErr = fmt.Errorf("gap analysis fallback generation failed: %w", err)
				return nil, retErr
			}
			report = &resp.GapReport
			usage = resp.Usage
		}
	} else {
		// Fallback to active version gap report if present
		if activeVer != nil && activeVer.GapReport != nil {
			report = activeVer.GapReport
		}
		if report == nil {
			report = &core.GapReport{
				OverallMatchSummary: "No qualification gaps detected or LLM service unconfigured.",
				MissingRequirements: []core.GapItem{},
			}
		}
	}

	// Normalization MUST occur before scoring so identified_gaps populate missing_requirements
	report.Normalize()

	if report.MatchScore == nil {
		score := core.CalculateDeterministicMatchScore(report.MissingRequirements)
		report.MatchScore = &score
	}

	if report.SkillCoverageRatio == nil {
		ratio := core.CalculateSkillCoverageRatio(snapshot.AllSkills(), report.MissingRequirements, branch.TargetRequirements)
		report.SkillCoverageRatio = &ratio
	}

	// Persist gap report directly on branch record
	branch.GapReport = report
	if err := s.branchRepo.UpdateBranch(ctx, branch); err != nil && s.logger != nil {
		s.logger.Error("failed to persist gap report on branch", "branch_id", branch.ID, "err", err)
	}

	// Update active version in-place if branch has one
	if activeVer != nil {
		activeVer.GapReport = report
		if err := s.branchRepo.CompleteVersion(ctx, activeVer); err != nil && s.logger != nil {
			s.logger.Error("failed to persist gap report on active version", "branch_id", branch.ID, "version_id", activeVer.ID, "err", err)
		}
	}

	return report, nil
}

// GetPromptPreview compiles the system and user prompt templates against the branch's candidate profile,
// rules hierarchy, and target requirements without dispatching any request to an LLM provider.
func (s *service) GetPromptPreview(ctx context.Context, userID, branchID string, opts *core.PromptPreviewOptions) (*core.PromptPreview, error) {
	branch, err := s.branchRepo.GetBranch(ctx, userID, branchID)
	if err != nil {
		return nil, err
	}

	snapshot, err := s.profileRepo.GetSnapshot(ctx, userID)
	if err != nil {
		return nil, err
	}

	prof, err := s.profileRepo.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Apply optional live draft overrides
	b := *branch
	if opts != nil {
		if opts.TargetRole != nil {
			b.TargetRole = *opts.TargetRole
		}
		if opts.TargetCompany != nil {
			b.TargetCompany = *opts.TargetCompany
		}
		if opts.TargetRequirements != nil {
			b.TargetRequirements = *opts.TargetRequirements
		}
		if opts.BranchInstructions != nil {
			b.BranchInstructions = *opts.BranchInstructions
		}
		if opts.PresetID != nil {
			b.PresetID = opts.PresetID
		}
		if opts.PinnedEntityIDs != nil {
			b.PinnedEntityIDs = opts.PinnedEntityIDs
		}
		if opts.ExcludedEntityIDs != nil {
			b.ExcludedEntityIDs = opts.ExcludedEntityIDs
		}
	}

	var preset *core.Preset
	var presetInstructions string
	if b.PresetID != nil && *b.PresetID != "" {
		p, pErr := s.presetRepo.GetPreset(ctx, userID, *b.PresetID)
		if pErr == nil && p != nil {
			preset = p
			presetInstructions = p.SystemInstructions
		} else if pErr != nil && s.logger != nil {
			s.logger.Warn("failed to load preset for prompt preview", "branch_id", b.ID, "preset_id", *b.PresetID, "err", pErr)
		}
	}

	var priorContent *core.TailoredContent
	if b.ActiveVersionID != nil && *b.ActiveVersionID != "" {
		activeVer, vErr := s.branchRepo.GetVersion(ctx, b.ID, *b.ActiveVersionID)
		if vErr == nil && activeVer != nil {
			priorContent = activeVer.TailoredContent
		} else if vErr != nil && s.logger != nil {
			s.logger.Warn("failed to load active version for prompt preview", "branch_id", b.ID, "version_id", *b.ActiveVersionID, "err", vErr)
		}
	}

	model := b.ModelOverride
	reasoningLevel := b.ReasoningLevel

	targetLang := preset.EffectiveLanguage()

	req := core.GenerationRequest{
		Model:              model,
		ReasoningLevel:     reasoningLevel,
		MainProfile:        snapshot,
		GlobalRules:        prof.GlobalWritingRules,
		PresetInstructions: presetInstructions,
		Preset:             preset,
		TargetLanguage:     targetLang,
		BranchContext:      b,
		PriorVersion:       priorContent,
	}

	if s.promptCompiler == nil {
		return nil, fmt.Errorf("%w: prompt compiler not configured", core.ErrValidation)
	}

	preview, err := s.promptCompiler.CompilePrompt(req)
	if err != nil {
		return nil, fmt.Errorf("failed to compile prompt preview: %w", err)
	}

	return preview, nil
}

func formatTailoredContentAsText(tc *core.TailoredContent) string {
	if tc == nil {
		return ""
	}
	var sb strings.Builder
	if tc.Summary != "" {
		sb.WriteString("## Summary\n")
		sb.WriteString(tc.Summary)
		sb.WriteString("\n\n")
	}
	if len(tc.SkillGroups) > 0 {
		sb.WriteString("## Skills\n")
		for _, sg := range tc.SkillGroups {
			sb.WriteString("- ")
			if sg.Category != "" {
				sb.WriteString(sg.Category + ": ")
			}
			sb.WriteString(strings.Join(sg.Skills, ", "))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	if len(tc.Experiences) > 0 {
		sb.WriteString("## Experience\n")
		for _, exp := range tc.Experiences {
			sb.WriteString("### " + exp.RoleTitle + " - " + exp.Company + "\n")
			for _, b := range exp.Bullets {
				sb.WriteString("- " + b.Text + "\n")
			}
		}
		sb.WriteString("\n")
	}
	if len(tc.Projects) > 0 {
		sb.WriteString("## Projects\n")
		for _, proj := range tc.Projects {
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
	if len(tc.Education) > 0 {
		sb.WriteString("## Education\n")
		for _, edu := range tc.Education {
			sb.WriteString("- " + edu.Degree + " " + edu.Field + " - " + edu.Institution + "\n")
		}
		sb.WriteString("\n")
	}
	if len(tc.Certifications) > 0 {
		sb.WriteString("## Certifications\n")
		for _, cert := range tc.Certifications {
			line := "- " + cert.Name
			if cert.Issuer != "" {
				line += " (" + cert.Issuer + ")"
			}
			sb.WriteString(line + "\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

func (s *service) resolveVersionResumeText(ctx context.Context, userID string, branch *core.Branch, ver *core.BranchVersion, snapshot *core.MainProfileSnapshot, requestedFormat string) (string, string, error) {
	htmlOut := ver.HTMLOutput
	mdOut := ver.MarkdownOutput
	txtOut := ver.PlainTextOutput
	if (htmlOut == "" || mdOut == "" || txtOut == "") && ver.TailoredContent != nil && s.renderer != nil && s.templateRepo != nil {
		tmplID := ver.TemplateSetIDUsed
		if tmplID == "" {
			tmplID = branch.TemplateSetID
		}
		if tmplID != "" {
			if tmpl, tErr := s.templateRepo.GetTemplate(ctx, userID, tmplID); tErr == nil && tmpl != nil {
				var prof *core.Profile
				if snapshot != nil {
					prof = &snapshot.Profile
				}
				if rendered, rErr := s.renderer.Render(ctx, tmpl, prof, ver.TailoredContent); rErr == nil && rendered != nil {
					if htmlOut == "" {
						htmlOut = rendered.HTML
					}
					if mdOut == "" {
						mdOut = rendered.Markdown
					}
					if txtOut == "" {
						txtOut = rendered.PlainText
					}
				}
			}
		}
	}

	var resumeText, formatUsed string
	switch requestedFormat {
	case string(core.OutputFormatHTML):
		if strings.TrimSpace(htmlOut) != "" {
			resumeText = htmlOut
			formatUsed = string(core.OutputFormatHTML)
		}
	case string(core.OutputFormatPlainText):
		if strings.TrimSpace(txtOut) != "" {
			resumeText = txtOut
			formatUsed = string(core.OutputFormatPlainText)
		}
	}
	if strings.TrimSpace(resumeText) == "" {
		if strings.TrimSpace(mdOut) != "" {
			resumeText = mdOut
			formatUsed = string(core.OutputFormatMarkdown)
		} else if strings.TrimSpace(txtOut) != "" {
			resumeText = txtOut
			formatUsed = string(core.OutputFormatPlainText)
		} else if strings.TrimSpace(htmlOut) != "" {
			resumeText = htmlOut
			formatUsed = string(core.OutputFormatHTML)
		} else if ver.TailoredContent != nil {
			resumeText = formatTailoredContentAsText(ver.TailoredContent)
			formatUsed = string(core.OutputFormatMarkdown)
		}
	}

	if strings.TrimSpace(resumeText) == "" {
		return "", "", fmt.Errorf("%w: tailored version contains no resume text to evaluate", core.ErrValidation)
	}
	return resumeText, formatUsed, nil
}

// SimulateATS executes on-demand ATS extraction and job matching score calculation for a completed tailored version.
// It enforces:
//   - Disabled for Default Main Branch (returns core.ErrDefaultBranchImmutable).
//   - Disabled when Job Description (TargetRequirements) is not provided (returns core.ErrValidation).
//   - Triggered once per successfully tailored version (returns core.ErrConflict if already calculated for that version).
func (s *service) SimulateATS(ctx context.Context, userID, branchID string, opts *core.ATSSimulationOptions) (*core.ATSScore, error) {
	rawFormat := ""
	if opts != nil {
		rawFormat = opts.Format
	}
	requestedFormat, err := core.NormalizeATSFormat(rawFormat)
	if err != nil {
		return nil, err
	}

	branch, err := s.branchRepo.GetBranch(ctx, userID, branchID)
	if err != nil {
		return nil, err
	}

	if branch.IsDefault {
		return nil, fmt.Errorf("%w: ATS score simulation is disabled for the default main branch", core.ErrDefaultBranchImmutable)
	}

	if strings.TrimSpace(branch.TargetRequirements) == "" {
		return nil, fmt.Errorf("%w: job description (target_requirements) is required to calculate ATS score", core.ErrValidation)
	}

	targetVersionID := ""
	if opts != nil && strings.TrimSpace(opts.VersionID) != "" {
		targetVersionID = strings.TrimSpace(opts.VersionID)
	} else if branch.ActiveVersionID != nil && strings.TrimSpace(*branch.ActiveVersionID) != "" {
		targetVersionID = strings.TrimSpace(*branch.ActiveVersionID)
	}
	if targetVersionID == "" {
		return nil, fmt.Errorf("%w: branch has no completed tailored version to evaluate", core.ErrValidation)
	}

	ver, err := s.GetVersion(ctx, branch.ID, targetVersionID)
	if err != nil {
		return nil, err
	}
	if ver.Status != core.VersionStatusCompleted && ver.Status != core.VersionStatusCompletedWithWarnings {
		return nil, fmt.Errorf("%w: ATS score can only be generated for a completed tailored version (status is %s)", core.ErrValidation, ver.Status)
	}

	if ver.ATSScore != nil {
		return nil, fmt.Errorf("%w: ATS score has already been generated for version %s", core.ErrConflict, ver.ID)
	}

	if !s.tryAcquireATSInFlight(ver.ID) {
		return nil, fmt.Errorf("%w: ATS score calculation already in progress or completed for version %s", core.ErrConflict, ver.ID)
	}
	defer s.releaseATSInFlight(ver.ID)

	var snapshot *core.MainProfileSnapshot
	if s.profileRepo != nil {
		snap, snapErr := s.profileRepo.GetSnapshot(ctx, userID)
		if snapErr != nil {
			return nil, fmt.Errorf("failed to load profile snapshot for ATS privacy session: %w", snapErr)
		}
		snapshot = snap
	}

	resumeText, formatUsed, err := s.resolveVersionResumeText(ctx, userID, branch, ver, snapshot, requestedFormat)
	if err != nil {
		return nil, err
	}

	var (
		retErr error
		usage  core.TokenUsage
	)
	finishQuota, err := s.reserveOperationQuota(ctx, userID, core.OperationATSSimulation, 1500, 800)
	if err != nil {
		return nil, err
	}
	defer func() {
		finishQuota(usage, retErr)
	}()

	var atsScore *core.ATSScore
	if s.llmService != nil {
		if simulator, ok := s.llmService.(interface {
			SimulateATS(ctx context.Context, req core.ATSSimulationRequest) (*core.ATSSimulationResponse, error)
		}); ok {
			model := branch.ModelOverride
			simResp, simErr := simulator.SimulateATS(ctx, core.ATSSimulationRequest{
				Model:          model,
				ReasoningLevel: branch.ReasoningLevel,
				ResumeText:     resumeText,
				FormatUsed:     formatUsed,
				BranchContext:  *branch,
				MainProfile:    snapshot,
			})
			if simErr != nil {
				retErr = fmt.Errorf("ats simulation failed: %w", simErr)
				return nil, retErr
			}
			scoreCopy := simResp.Score
			atsScore = &scoreCopy
			usage = simResp.Usage
		}
	}
	if atsScore == nil {
		atsScore = core.CalculateDeterministicATSScore(resumeText, formatUsed, branch.TargetRequirements)
	}

	if atsScore.EvaluatedAt == nil {
		now := time.Now().UTC()
		atsScore.EvaluatedAt = &now
	}
	atsScore.Normalize()

	if err := s.branchRepo.UpdateVersionATSScore(ctx, ver.ID, atsScore); err != nil {
		retErr = err
		return nil, retErr
	}

	return atsScore, nil
}
