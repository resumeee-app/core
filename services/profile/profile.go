// Package profile implements career graph domain services, main profile management,
// and JSON Resume schema import/export interoperability.
//
// It serves as the primary manager of the Tier-1 Main Career Profile ("Source of Truth"),
// coordinating career facts, experiences, skills, projects, education, and certifications.
// It also provides bidirectional mapping between Resumeee's internal domain models
// and the open JSON Resume (v1.0.0) standard schema.
package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/resumeee-app/resumeee-core"
)

// Service defines high-level profile management, career graph operations, and JSON Resume interoperability.
type Service interface {
	GetProfile(ctx context.Context, userID string) (*core.Profile, error)
	UpdateProfile(ctx context.Context, profile *core.Profile) error
	GetSnapshot(ctx context.Context, userID string) (*core.MainProfileSnapshot, error)

	// JSON Resume Operations
	ImportJSONResume(ctx context.Context, userID string, payload []byte) (*core.MainProfileSnapshot, error)
	ExportJSONResume(ctx context.Context, userID string, versionID *string) (*core.JSONResume, error)
	ExportFromSnapshot(snapshot *core.MainProfileSnapshot) (*core.JSONResume, error)
	ExportFromTailored(profile *core.Profile, tailored *core.TailoredContent) (*core.JSONResume, error)
}

type profileService struct {
	profileRepo core.ProfileRepository
	branchRepo  core.BranchRepository
}

// NewService constructs a new Profile service instance.
func NewService(profileRepo core.ProfileRepository, branchRepo core.BranchRepository) Service {
	return &profileService{
		profileRepo: profileRepo,
		branchRepo:  branchRepo,
	}
}

func (s *profileService) GetProfile(ctx context.Context, userID string) (*core.Profile, error) {
	return s.profileRepo.GetProfile(ctx, userID)
}

func (s *profileService) UpdateProfile(ctx context.Context, profile *core.Profile) error {
	return s.profileRepo.UpsertProfile(ctx, profile)
}

func (s *profileService) GetSnapshot(ctx context.Context, userID string) (*core.MainProfileSnapshot, error) {
	return s.profileRepo.GetSnapshot(ctx, userID)
}

// ImportJSONResume parses JSON Resume bytes and atomically replaces the user's main career graph.
func (s *profileService) ImportJSONResume(ctx context.Context, userID string, payload []byte) (*core.MainProfileSnapshot, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: empty JSON payload", core.ErrValidation)
	}

	var resume core.JSONResume
	if err := json.Unmarshal(payload, &resume); err != nil {
		return nil, fmt.Errorf("%w: malformed JSON Resume: %v", core.ErrValidation, err)
	}

	existingProfile, err := s.profileRepo.GetProfile(ctx, userID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}

	revision := int64(1)
	if existingProfile != nil && existingProfile.MainRevision > 0 {
		revision = existingProfile.MainRevision + 1
	}

	var globalSkills []core.Skill
	if gs, err := s.profileRepo.ListGlobalSkills(ctx); err == nil {
		globalSkills = gs
	}

	snapshot, err := ToSnapshotWithGlobalSkills(&resume, userID, existingProfile, revision, globalSkills)
	if err != nil {
		return nil, err
	}

	if err := s.profileRepo.ReplaceCareerGraph(ctx, userID, snapshot); err != nil {
		return nil, err
	}

	return s.profileRepo.GetSnapshot(ctx, userID)
}

// ExportJSONResume exports either the main profile snapshot or a specific branch version into JSON Resume format.
func (s *profileService) ExportJSONResume(ctx context.Context, userID string, versionID *string) (*core.JSONResume, error) {
	if versionID == nil || *versionID == "" {
		snapshot, err := s.profileRepo.GetSnapshot(ctx, userID)
		if err != nil {
			return nil, err
		}
		return s.ExportFromSnapshot(snapshot)
	}

	if s.branchRepo == nil {
		return nil, fmt.Errorf("%w: branch repository not configured", core.ErrNotFound)
	}

	targetVersion, err := s.branchRepo.GetVersion(ctx, "", *versionID)
	if err != nil {
		return nil, err
	}

	targetBranch, err := s.branchRepo.GetBranch(ctx, userID, targetVersion.BranchID)
	if err != nil {
		return nil, err
	}
	if targetBranch.UserID != userID {
		return nil, core.ErrNotFound
	}

	if targetVersion.TailoredContent == nil {
		return nil, fmt.Errorf("%w: version %s has no tailored content", core.ErrValidation, *versionID)
	}

	prof, err := s.profileRepo.GetProfile(ctx, userID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}

	return s.ExportFromTailored(prof, targetVersion.TailoredContent)
}

func (s *profileService) ExportFromSnapshot(snapshot *core.MainProfileSnapshot) (*core.JSONResume, error) {
	return FromSnapshot(snapshot)
}

func (s *profileService) ExportFromTailored(profile *core.Profile, tailored *core.TailoredContent) (*core.JSONResume, error) {
	return FromTailored(profile, tailored)
}
