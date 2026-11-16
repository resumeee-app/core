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

	"github.com/resumeee-app/core"
)

// Store defines the narrow profile and career graph persistence contract required by Service.
type Store interface {
	Profile(ctx context.Context, userID string) (*core.Profile, error)
	UpsertProfile(ctx context.Context, profile *core.Profile) error
	Snapshot(ctx context.Context, userID string) (*core.MainProfileSnapshot, error)
	ReplaceCareerGraph(ctx context.Context, userID string, snapshot *core.MainProfileSnapshot) error
}

// SkillCatalog supplies shared canonical global skills for normalization during JSON Resume import.
type SkillCatalog interface {
	ListGlobalSkills(ctx context.Context) ([]core.Skill, error)
}

// BranchReader defines the narrow branch and version lookup contract required to export tailored versions.
type BranchReader interface {
	Version(ctx context.Context, branchID, versionID string) (*core.BranchVersion, error)
	Branch(ctx context.Context, userID, id string) (*core.Branch, error)
}

// Service manages main profile operations, career graph replacement, and JSON Resume conversions.
type Service struct {
	profiles Store
	skills   SkillCatalog
	branches BranchReader
}

// New constructs a profile Service backed by narrow consumer store contracts.
func New(profiles Store, skills SkillCatalog, branches BranchReader) *Service {
	return &Service{
		profiles: profiles,
		skills:   skills,
		branches: branches,
	}
}

// Profile retrieves the candidate's base Profile for userID.
func (s *Service) Profile(ctx context.Context, userID string) (*core.Profile, error) {
	if s == nil || s.profiles == nil {
		return nil, fmt.Errorf("%w: profile store not configured", core.ErrValidation)
	}
	return s.profiles.Profile(ctx, userID)
}

// UpdateProfile inserts or updates the candidate's base Profile.
func (s *Service) UpdateProfile(ctx context.Context, profile *core.Profile) error {
	if s == nil || s.profiles == nil {
		return fmt.Errorf("%w: profile store not configured", core.ErrValidation)
	}
	return s.profiles.UpsertProfile(ctx, profile)
}

// Snapshot retrieves the full aggregated MainProfileSnapshot for userID.
func (s *Service) Snapshot(ctx context.Context, userID string) (*core.MainProfileSnapshot, error) {
	if s == nil || s.profiles == nil {
		return nil, fmt.Errorf("%w: profile store not configured", core.ErrValidation)
	}
	return s.profiles.Snapshot(ctx, userID)
}

// ImportJSONResume parses JSON Resume bytes and atomically replaces the user's main career graph.
func (s *Service) ImportJSONResume(ctx context.Context, userID string, payload []byte) (*core.MainProfileSnapshot, error) {
	if s == nil || s.profiles == nil {
		return nil, fmt.Errorf("%w: profile store not configured", core.ErrValidation)
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: empty JSON payload", core.ErrValidation)
	}

	var resume core.JSONResume
	if err := json.Unmarshal(payload, &resume); err != nil {
		return nil, fmt.Errorf("%w: malformed JSON Resume: %v", core.ErrValidation, err)
	}

	existingProfile, err := s.profiles.Profile(ctx, userID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}

	revision := int64(1)
	if existingProfile != nil && existingProfile.MainRevision > 0 {
		revision = existingProfile.MainRevision + 1
	}

	var globalSkills []core.Skill
	if s.skills != nil {
		if gs, gsErr := s.skills.ListGlobalSkills(ctx); gsErr == nil {
			globalSkills = gs
		}
	}

	snapshot, err := ToSnapshotWithGlobalSkills(&resume, userID, existingProfile, revision, globalSkills)
	if err != nil {
		return nil, err
	}

	if err := s.profiles.ReplaceCareerGraph(ctx, userID, snapshot); err != nil {
		return nil, err
	}

	return s.profiles.Snapshot(ctx, userID)
}

// ExportJSONResume exports either the main profile snapshot or a specific branch version into JSON Resume format.
func (s *Service) ExportJSONResume(ctx context.Context, userID string, versionID *string) (*core.JSONResume, error) {
	if s == nil || s.profiles == nil {
		return nil, fmt.Errorf("%w: profile store not configured", core.ErrValidation)
	}
	if versionID == nil || *versionID == "" {
		snapshot, err := s.profiles.Snapshot(ctx, userID)
		if err != nil {
			return nil, err
		}
		return s.ExportFromSnapshot(snapshot)
	}

	if s.branches == nil {
		return nil, fmt.Errorf("%w: branch store not configured", core.ErrNotFound)
	}

	targetVersion, err := s.branches.Version(ctx, "", *versionID)
	if err != nil {
		return nil, err
	}

	targetBranch, err := s.branches.Branch(ctx, userID, targetVersion.BranchID)
	if err != nil {
		return nil, err
	}
	if targetBranch.UserID != userID {
		return nil, core.ErrNotFound
	}

	if targetVersion.TailoredContent == nil {
		return nil, fmt.Errorf("%w: version %s has no tailored content", core.ErrValidation, *versionID)
	}

	prof, err := s.profiles.Profile(ctx, userID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}

	return s.ExportFromTailored(prof, targetVersion.TailoredContent)
}

// ExportFromSnapshot converts a MainProfileSnapshot into a JSONResume document.
func (s *Service) ExportFromSnapshot(snapshot *core.MainProfileSnapshot) (*core.JSONResume, error) {
	return FromSnapshot(snapshot)
}

// ExportFromTailored converts TailoredContent paired with a base Profile into a JSONResume document.
func (s *Service) ExportFromTailored(profile *core.Profile, tailored *core.TailoredContent) (*core.JSONResume, error) {
	return FromTailored(profile, tailored)
}
