// Package branch provides domain services for managing targeted resume branches,
// version lifecycles, iterative refinements, and asynchronous generation coordination.
package branch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"

	"resumeee/internal/domain"
	"resumeee/internal/llm"
	"resumeee/internal/renderer"
	"resumeee/internal/validator"
	"resumeee/internal/worker"
)

// Service defines domain operations for branches, async generation, batch synchronization, and version lifecycle.
type Service interface {
	ListBranches(ctx context.Context, userID string, staleOnly bool) ([]domain.Branch, error)
	GetBranch(ctx context.Context, userID, id string) (*domain.Branch, error)
	CreateBranch(ctx context.Context, branch *domain.Branch) (*domain.Branch, error)
	UpdateBranch(ctx context.Context, branch *domain.Branch) (*domain.Branch, error)
	DeleteBranch(ctx context.Context, userID, id string) error

	// Version Lifecycle
	GenerateVersion(ctx context.Context, userID, branchID string) (*domain.BranchVersion, error)
	SyncBranches(ctx context.Context, userID string, branchIDs []string) ([]string, error)
	ListVersions(ctx context.Context, branchID string) ([]domain.BranchVersion, error)
	GetVersion(ctx context.Context, branchID, versionNumOrID string) (*domain.BranchVersion, error)
	EditVersion(ctx context.Context, userID, branchID, parentVersionNumOrID string, editedContent domain.TailoredContent) (*domain.BranchVersion, error)
	AnalyzeGaps(ctx context.Context, userID, branchID string) (*domain.GapReport, error)
	GetPromptPreview(ctx context.Context, userID, branchID string, opts *domain.PromptPreviewOptions) (*domain.PromptPreview, error)
}

// Config defines the dependencies required by the branch domain service.
type Config struct {
	BranchRepo   domain.BranchRepository
	ProfileRepo  domain.ProfileRepository
	PresetRepo   domain.PresetRepository
	TemplateRepo domain.TemplateRepository
	JobEnqueuer  worker.JobEnqueuer
	Validator    validator.ProvenanceValidator
	Renderer     domain.TemplateRenderer
	LLMService   worker.LLMService
	Logger       *slog.Logger
}

type service struct {
	branchRepo   domain.BranchRepository
	profileRepo  domain.ProfileRepository
	presetRepo   domain.PresetRepository
	templateRepo domain.TemplateRepository
	jobEnqueuer  worker.JobEnqueuer
	validator    validator.ProvenanceValidator
	renderer     domain.TemplateRenderer
	llmService   worker.LLMService
	logger       *slog.Logger
}

// NewService constructs a new Branch service.
func NewService(cfg Config) Service {
	return &service{
		branchRepo:   cfg.BranchRepo,
		profileRepo:  cfg.ProfileRepo,
		presetRepo:   cfg.PresetRepo,
		templateRepo: cfg.TemplateRepo,
		jobEnqueuer:  cfg.JobEnqueuer,
		validator:    cfg.Validator,
		renderer:     cfg.Renderer,
		llmService:   cfg.LLMService,
		logger:       cfg.Logger,
	}
}

// ListBranches returns all branches for the user, optionally filtering for stale branches whose synced revision is behind master.
func (s *service) ListBranches(ctx context.Context, userID string, staleOnly bool) ([]domain.Branch, error) {
	branches, err := s.branchRepo.ListBranches(ctx, userID)
	if err != nil {
		return nil, err
	}

	if staleOnly {
		var filtered []domain.Branch
		for _, b := range branches {
			if b.IsStale {
				filtered = append(filtered, b)
			}
		}
		if filtered == nil {
			filtered = []domain.Branch{}
		}
		return filtered, nil
	}

	if branches == nil {
		branches = []domain.Branch{}
	}
	return branches, nil
}

// GetBranch retrieves a branch record by its identifier.
func (s *service) GetBranch(ctx context.Context, userID, id string) (*domain.Branch, error) {
	return s.branchRepo.GetBranch(ctx, userID, id)
}

// CreateBranch initializes and saves a new resume branch with master revision synchronization.
func (s *service) CreateBranch(ctx context.Context, branch *domain.Branch) (*domain.Branch, error) {
	if branch.ID == "" {
		branch.ID = "branch-" + uuid.New().String()
	}

	if s.profileRepo != nil {
		prof, err := s.profileRepo.GetProfile(ctx, branch.UserID)
		if err == nil && prof != nil {
			branch.LastSyncedMasterRevision = prof.MasterRevision
		}
	}

	if err := s.branchRepo.CreateBranch(ctx, branch); err != nil {
		return nil, err
	}

	return s.branchRepo.GetBranch(ctx, branch.UserID, branch.ID)
}

// UpdateBranch modifies branch configuration while preserving default branch immutability invariants.
func (s *service) UpdateBranch(ctx context.Context, branch *domain.Branch) (*domain.Branch, error) {
	existing, err := s.branchRepo.GetBranch(ctx, branch.UserID, branch.ID)
	if err != nil {
		return nil, err
	}

	if existing.IsDefault {
		// Enforce default branch immutability: disallow tailor fields
		if branch.TargetRole != "" || branch.TargetCompany != "" || branch.TargetRequirements != "" ||
			branch.BranchInstructions != "" || branch.PresetID != nil || branch.ProviderOverride != "" ||
			branch.ModelOverride != "" || len(branch.PinnedEntityIDs) > 0 || len(branch.ExcludedEntityIDs) > 0 {
			return nil, fmt.Errorf("%w: default branch cannot be tailored with custom roles, presets, or instructions", domain.ErrDefaultBranchImmutable)
		}
		if branch.Name != "" && branch.Name != domain.DefaultBranchName {
			return nil, fmt.Errorf("%w: default branch name cannot be altered", domain.ErrDefaultBranchImmutable)
		}

		branch.IsDefault = true
		branch.Name = domain.DefaultBranchName

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

		return s.branchRepo.GetBranch(ctx, branch.UserID, branch.ID)
	}

	if branch.ActiveVersionID != nil && *branch.ActiveVersionID != "" {
		ver, err := s.branchRepo.GetVersion(ctx, "", *branch.ActiveVersionID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil, fmt.Errorf("%w: active version %s not found", domain.ErrValidation, *branch.ActiveVersionID)
			}
			return nil, err
		}
		if ver.BranchID != branch.ID {
			return nil, fmt.Errorf("%w: version %s belongs to branch %s, not %s", domain.ErrValidation, ver.ID, ver.BranchID, branch.ID)
		}
		if ver.Status != domain.VersionStatusCompleted && ver.Status != domain.VersionStatusCompletedWithWarnings {
			return nil, fmt.Errorf("%w: active version %s must be completed (got %s)", domain.ErrValidation, ver.ID, ver.Status)
		}
		branch.LastSyncedMasterRevision = ver.MasterRevisionUsed
	}

	if err := s.branchRepo.UpdateBranch(ctx, branch); err != nil {
		return nil, err
	}
	return s.branchRepo.GetBranch(ctx, branch.UserID, branch.ID)
}

// DeleteBranch removes a branch if it is not the immutable default branch.
func (s *service) DeleteBranch(ctx context.Context, userID, id string) error {
	branch, err := s.branchRepo.GetBranch(ctx, userID, id)
	if err == nil && branch != nil && branch.IsDefault {
		return fmt.Errorf("%w: cannot delete default branch", domain.ErrDefaultBranchImmutable)
	}
	return s.branchRepo.DeleteBranch(ctx, userID, id)
}

// ListVersions returns all generated and edited versions belonging to a branch.
func (s *service) ListVersions(ctx context.Context, branchID string) ([]domain.BranchVersion, error) {
	versions, err := s.branchRepo.ListVersions(ctx, branchID)
	if err != nil {
		return nil, err
	}
	if versions == nil {
		versions = []domain.BranchVersion{}
	}
	return versions, nil
}

// GetVersion resolves a version by either sequential version number (1, 2, ...) or version UUID.
func (s *service) GetVersion(ctx context.Context, branchID, versionNumOrID string) (*domain.BranchVersion, error) {
	if num, err := strconv.Atoi(versionNumOrID); err == nil && num > 0 {
		ver, err := s.branchRepo.GetVersionByNumber(ctx, branchID, num)
		if err == nil {
			return ver, nil
		}
	}
	return s.branchRepo.GetVersion(ctx, branchID, versionNumOrID)
}

// GenerateVersion enqueues an asynchronous generation job or synchronously renders default branch versions.
func (s *service) GenerateVersion(ctx context.Context, userID, branchID string) (*domain.BranchVersion, error) {
	branch, err := s.branchRepo.GetBranch(ctx, userID, branchID)
	if err != nil {
		return nil, err
	}

	if branch.IsDefault {
		return s.renderDefaultBranchVersion(ctx, userID, branch)
	}

	var masterRevision int64 = 1
	provider := branch.ProviderOverride
	model := branch.ModelOverride

	if s.profileRepo != nil {
		prof, err := s.profileRepo.GetProfile(ctx, userID)
		if err == nil && prof != nil {
			masterRevision = prof.MasterRevision
			if provider == "" {
				provider = prof.DefaultProvider
			}
			if model == "" {
				model = prof.DefaultModel
			}
		}
	}

	ver := &domain.BranchVersion{
		ID:                 "ver-" + uuid.New().String(),
		BranchID:           branch.ID,
		Status:             domain.VersionStatusPending,
		MasterRevisionUsed: masterRevision,
		TemplateSetIDUsed:  branch.TemplateSetID,
		ProviderUsed:       provider,
		ModelUsed:          model,
		SourceType:         domain.SourceTypeLLMGeneration,
		ParentVersionID:    branch.ActiveVersionID,
	}

	if err := s.branchRepo.CreateVersion(ctx, ver); err != nil {
		return nil, err
	}

	if s.jobEnqueuer != nil {
		job := worker.GenerationJob{
			UserID:    userID,
			BranchID:  branch.ID,
			VersionID: ver.ID,
		}
		if err := s.jobEnqueuer.Enqueue(job); err != nil {
			if errors.Is(err, domain.ErrQueueFull) {
				_ = s.branchRepo.UpdateVersionStatus(ctx, ver.ID, domain.VersionStatusFailed, "generation queue full")
				return nil, domain.ErrQueueFull
			}
			return nil, err
		}
	}

	return ver, nil
}

// SyncBranches regenerates specified branches or all stale branches when branchIDs is empty.
func (s *service) SyncBranches(ctx context.Context, userID string, branchIDs []string) ([]string, error) {
	var masterRevision int64 = 1
	var defaultProvider, defaultModel string

	if s.profileRepo != nil {
		prof, err := s.profileRepo.GetProfile(ctx, userID)
		if err != nil {
			return nil, err
		}
		masterRevision = prof.MasterRevision
		defaultProvider = prof.DefaultProvider
		defaultModel = prof.DefaultModel
	}

	var targetBranches []domain.Branch
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
		if provider == "" {
			provider = defaultProvider
		}
		model := b.ModelOverride
		if model == "" {
			model = defaultModel
		}

		ver := &domain.BranchVersion{
			ID:                 "ver-" + uuid.New().String(),
			BranchID:           b.ID,
			Status:             domain.VersionStatusPending,
			MasterRevisionUsed: masterRevision,
			TemplateSetIDUsed:  b.TemplateSetID,
			ProviderUsed:       provider,
			ModelUsed:          model,
			SourceType:         domain.SourceTypeLLMGeneration,
			ParentVersionID:    b.ActiveVersionID,
		}

		if err := s.branchRepo.CreateVersion(ctx, ver); err != nil {
			return enqueuedIDs, err
		}

		if s.jobEnqueuer != nil {
			job := worker.GenerationJob{
				UserID:    userID,
				BranchID:  b.ID,
				VersionID: ver.ID,
			}
			if err := s.jobEnqueuer.Enqueue(job); err != nil {
				if errors.Is(err, domain.ErrQueueFull) {
					_ = s.branchRepo.UpdateVersionStatus(ctx, ver.ID, domain.VersionStatusFailed, "generation queue full")
					return enqueuedIDs, domain.ErrQueueFull
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
func (s *service) EditVersion(ctx context.Context, userID, branchID, parentVersionNumOrID string, editedContent domain.TailoredContent) (*domain.BranchVersion, error) {
	branch, err := s.branchRepo.GetBranch(ctx, userID, branchID)
	if err != nil {
		return nil, err
	}

	if branch.IsDefault {
		return nil, fmt.Errorf("%w: default branch cannot be manually edited", domain.ErrDefaultBranchImmutable)
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
			return nil, fmt.Errorf("%w: %v", domain.ErrProvenance, err)
		}
	}

	// 2. Synchronous Go template rendering
	var rendered *domain.RenderOutput
	if s.renderer != nil {
		r, err := s.renderer.Render(ctx, tmpl, prof, &editedContent)
		if err != nil {
			return nil, fmt.Errorf("template rendering failed: %w", err)
		}
		rendered = r
	} else {
		rendered = &domain.RenderOutput{}
	}

	// 3. Construct immutable sequential version vN+1
	newVer := &domain.BranchVersion{
		ID:                 "ver-" + uuid.New().String(),
		BranchID:           branch.ID,
		Status:             domain.VersionStatusCompleted,
		MasterRevisionUsed: snapshot.MasterRevision,
		TemplateSetIDUsed:  branch.TemplateSetID,
		ProviderUsed:       "manual",
		ModelUsed:          "manual",
		SourceType:         domain.SourceTypeManualEdit,
		ParentVersionID:    &parentVer.ID,
		TailoredContent:    &editedContent,
		GapReport:          parentVer.GapReport,
		HTMLOutput:         rendered.HTML,
		MarkdownOutput:     rendered.Markdown,
		PlainTextOutput:    rendered.PlainText,
		CreatedAt:          time.Now().UTC(),
	}

	if err := s.branchRepo.CreateVersion(ctx, newVer); err != nil {
		return nil, err
	}

	if err := s.branchRepo.CompleteVersion(ctx, newVer); err != nil {
		return nil, err
	}

	return newVer, nil
}

// renderDefaultBranchVersion synchronously renders a resume version for the default branch directly from the master profile.
func (s *service) renderDefaultBranchVersion(ctx context.Context, userID string, branch *domain.Branch) (*domain.BranchVersion, error) {
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

	tailoredContent := domain.SnapshotToTailoredContent(snapshot)

	var rendered *domain.RenderOutput
	if s.renderer != nil {
		r, err := s.renderer.Render(ctx, tmpl, prof, tailoredContent)
		if err != nil {
			return nil, fmt.Errorf("rendering default branch: %w", err)
		}
		rendered = r
	} else {
		rendered = &domain.RenderOutput{}
	}

	now := time.Now().UTC()
	ver := &domain.BranchVersion{
		ID:                 "ver-" + uuid.New().String(),
		BranchID:           branch.ID,
		Status:             domain.VersionStatusCompleted,
		MasterRevisionUsed: snapshot.MasterRevision,
		TemplateSetIDUsed:  branch.TemplateSetID,
		ProviderUsed:       "none",
		ModelUsed:          "none",
		SourceType:         domain.SourceTypeMasterProfile,
		ParentVersionID:    branch.ActiveVersionID,
		TailoredContent:    tailoredContent,
		HTMLOutput:         rendered.HTML,
		MarkdownOutput:     rendered.Markdown,
		PlainTextOutput:    rendered.PlainText,
		CreatedAt:          now,
		CompletedAt:        &now,
	}

	if err := s.branchRepo.CreateVersion(ctx, ver); err != nil {
		return nil, fmt.Errorf("creating default branch version: %w", err)
	}

	if err := s.branchRepo.CompleteVersion(ctx, ver); err != nil {
		return nil, fmt.Errorf("completing default branch version: %w", err)
	}

	branch.ActiveVersionID = &ver.ID
	branch.LastSyncedMasterRevision = snapshot.MasterRevision

	return ver, nil
}

// AnalyzeGaps performs fast decoupled gap analysis for a branch against the master profile snapshot.
func (s *service) AnalyzeGaps(ctx context.Context, userID, branchID string) (*domain.GapReport, error) {
	branch, err := s.branchRepo.GetBranch(ctx, userID, branchID)
	if err != nil {
		return nil, err
	}

	snapshot, err := s.profileRepo.GetSnapshot(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Fetch active version once if present
	var activeVer *domain.BranchVersion
	if branch.ActiveVersionID != nil && *branch.ActiveVersionID != "" {
		v, vErr := s.branchRepo.GetVersion(ctx, branch.ID, *branch.ActiveVersionID)
		if vErr != nil && s.logger != nil {
			s.logger.Warn("could not fetch active version for gap analysis", "branch_id", branch.ID, "version_id", *branch.ActiveVersionID, "err", vErr)
		} else {
			activeVer = v
		}
	}

	var report *domain.GapReport
	if s.llmService != nil {
		req := domain.GenerationRequest{
			MasterProfile: snapshot,
			BranchContext: *branch,
		}
		if analyzer, ok := s.llmService.(interface {
			GenerateGapAnalysis(ctx context.Context, req domain.GenerationRequest) (*domain.GapReport, error)
		}); ok {
			r, err := analyzer.GenerateGapAnalysis(ctx, req)
			if err != nil {
				return nil, fmt.Errorf("gap analysis generation failed: %w", err)
			}
			report = r
		} else {
			resp, err := s.llmService.GenerateTailoredResume(ctx, req)
			if err != nil {
				return nil, fmt.Errorf("gap analysis fallback generation failed: %w", err)
			}
			report = &resp.GapReport
		}
	} else {
		// Fallback to active version gap report if present
		if activeVer != nil && activeVer.GapReport != nil {
			report = activeVer.GapReport
		}
		if report == nil {
			report = &domain.GapReport{
				OverallMatchSummary: "No qualification gaps detected or LLM service unconfigured.",
				MissingRequirements: []domain.GapItem{},
			}
		}
	}

	// Normalization MUST occur before scoring so identified_gaps populate missing_requirements
	report.Normalize()

	if report.MatchScore == nil {
		score := domain.CalculateDeterministicMatchScore(report.MissingRequirements)
		report.MatchScore = &score
	}

	if report.SkillCoverageRatio == nil {
		ratio := domain.CalculateSkillCoverageRatio(snapshot.AllSkills(), report.MissingRequirements, branch.TargetRequirements)
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
func (s *service) GetPromptPreview(ctx context.Context, userID, branchID string, opts *domain.PromptPreviewOptions) (*domain.PromptPreview, error) {
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

	var preset *domain.Preset
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

	var priorContent *domain.TailoredContent
	if b.ActiveVersionID != nil && *b.ActiveVersionID != "" {
		activeVer, vErr := s.branchRepo.GetVersion(ctx, b.ID, *b.ActiveVersionID)
		if vErr == nil && activeVer != nil {
			priorContent = activeVer.TailoredContent
		} else if vErr != nil && s.logger != nil {
			s.logger.Warn("failed to load active version for prompt preview", "branch_id", b.ID, "version_id", *b.ActiveVersionID, "err", vErr)
		}
	}

	model := b.ModelOverride
	if model == "" && preset != nil {
		model = preset.ModelOverride
	}
	if model == "" {
		model = prof.DefaultModel
	}

	req := domain.GenerationRequest{
		Model:              model,
		MasterProfile:      snapshot,
		GlobalRules:        prof.GlobalWritingRules,
		PresetInstructions: presetInstructions,
		Preset:             preset,
		BranchContext:      b,
		PriorVersion:       priorContent,
	}

	payload, err := llm.BuildPrompt(req)
	if err != nil {
		return nil, fmt.Errorf("failed to compile prompt preview: %w", err)
	}

	return &domain.PromptPreview{
		SystemPrompt: payload.SystemPrompt,
		UserPrompt:   payload.UserPrompt,
	}, nil
}

