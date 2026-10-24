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
		logger:       cfg.Logger,
	}
}

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

func (s *service) GetBranch(ctx context.Context, userID, id string) (*domain.Branch, error) {
	return s.branchRepo.GetBranch(ctx, userID, id)
}

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

func (s *service) UpdateBranch(ctx context.Context, branch *domain.Branch) (*domain.Branch, error) {
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

func (s *service) DeleteBranch(ctx context.Context, userID, id string) error {
	return s.branchRepo.DeleteBranch(ctx, userID, id)
}

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

func (s *service) GetVersion(ctx context.Context, branchID, versionNumOrID string) (*domain.BranchVersion, error) {
	if num, err := strconv.Atoi(versionNumOrID); err == nil && num > 0 {
		ver, err := s.branchRepo.GetVersionByNumber(ctx, branchID, num)
		if err == nil {
			return ver, nil
		}
	}
	return s.branchRepo.GetVersion(ctx, branchID, versionNumOrID)
}

func (s *service) GenerateVersion(ctx context.Context, userID, branchID string) (*domain.BranchVersion, error) {
	branch, err := s.branchRepo.GetBranch(ctx, userID, branchID)
	if err != nil {
		return nil, err
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

func (s *service) EditVersion(ctx context.Context, userID, branchID, parentVersionNumOrID string, editedContent domain.TailoredContent) (*domain.BranchVersion, error) {
	branch, err := s.branchRepo.GetBranch(ctx, userID, branchID)
	if err != nil {
		return nil, err
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
