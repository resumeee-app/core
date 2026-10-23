package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"resumeee/internal/domain"
)

// Service defines high-level profile management, career graph operations, and JSON Resume interoperability.
type Service interface {
	GetProfile(ctx context.Context, userID string) (*domain.Profile, error)
	UpdateProfile(ctx context.Context, profile *domain.Profile) error
	GetSnapshot(ctx context.Context, userID string) (*domain.MasterProfileSnapshot, error)

	// JSON Resume Operations
	ImportJSONResume(ctx context.Context, userID string, payload []byte) (*domain.MasterProfileSnapshot, error)
	ExportJSONResume(ctx context.Context, userID string, versionID *string) (*JSONResume, error)
	ExportFromSnapshot(snapshot *domain.MasterProfileSnapshot) (*JSONResume, error)
	ExportFromTailored(profile *domain.Profile, tailored *domain.TailoredContent) (*JSONResume, error)
}

type profileService struct {
	profileRepo domain.ProfileRepository
	branchRepo  domain.BranchRepository
}

// NewService constructs a new Profile service instance.
func NewService(profileRepo domain.ProfileRepository, branchRepo domain.BranchRepository) Service {
	return &profileService{
		profileRepo: profileRepo,
		branchRepo:  branchRepo,
	}
}

func (s *profileService) GetProfile(ctx context.Context, userID string) (*domain.Profile, error) {
	return s.profileRepo.GetProfile(ctx, userID)
}

func (s *profileService) UpdateProfile(ctx context.Context, profile *domain.Profile) error {
	return s.profileRepo.UpsertProfile(ctx, profile)
}

func (s *profileService) GetSnapshot(ctx context.Context, userID string) (*domain.MasterProfileSnapshot, error) {
	return s.profileRepo.GetSnapshot(ctx, userID)
}

func (s *profileService) ImportJSONResume(ctx context.Context, userID string, payload []byte) (*domain.MasterProfileSnapshot, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: empty JSON payload", domain.ErrValidation)
	}

	var resume JSONResume
	if err := json.Unmarshal(payload, &resume); err != nil {
		return nil, fmt.Errorf("%w: malformed JSON Resume: %v", domain.ErrValidation, err)
	}

	existingProfile, err := s.profileRepo.GetProfile(ctx, userID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	revision := int64(1)
	if existingProfile != nil && existingProfile.MasterRevision > 0 {
		revision = existingProfile.MasterRevision + 1
	}

	snapshot, err := ToSnapshot(&resume, userID, existingProfile, revision)
	if err != nil {
		return nil, err
	}

	if err := s.profileRepo.ReplaceCareerGraph(ctx, userID, snapshot); err != nil {
		return nil, err
	}

	return s.profileRepo.GetSnapshot(ctx, userID)
}

func (s *profileService) ExportJSONResume(ctx context.Context, userID string, versionID *string) (*JSONResume, error) {
	if versionID == nil || *versionID == "" {
		snapshot, err := s.profileRepo.GetSnapshot(ctx, userID)
		if err != nil {
			return nil, err
		}
		return s.ExportFromSnapshot(snapshot)
	}

	if s.branchRepo == nil {
		return nil, fmt.Errorf("%w: branch repository not configured", domain.ErrNotFound)
	}

	branches, err := s.branchRepo.ListBranches(ctx, userID)
	if err != nil {
		return nil, err
	}

	var targetVersion *domain.BranchVersion
	for _, b := range branches {
		v, err := s.branchRepo.GetVersion(ctx, b.ID, *versionID)
		if err == nil && v != nil {
			targetVersion = v
			break
		}
	}

	if targetVersion == nil {
		return nil, domain.ErrNotFound
	}

	if targetVersion.TailoredContent == nil {
		return nil, fmt.Errorf("%w: version %s has no tailored content", domain.ErrValidation, *versionID)
	}

	prof, err := s.profileRepo.GetProfile(ctx, userID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	return s.ExportFromTailored(prof, targetVersion.TailoredContent)
}

func (s *profileService) ExportFromSnapshot(snapshot *domain.MasterProfileSnapshot) (*JSONResume, error) {
	return FromSnapshot(snapshot)
}

func (s *profileService) ExportFromTailored(profile *domain.Profile, tailored *domain.TailoredContent) (*JSONResume, error) {
	return FromTailored(profile, tailored)
}
