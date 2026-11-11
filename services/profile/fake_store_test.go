package profile_test

import (
	"context"
	"fmt"
	"sync"

	"github.com/resumeee-app/resumeee-core/domain"
)

type fakeProfileStore struct {
	mu           sync.Mutex
	profiles     map[string]*domain.Profile
	snapshots    map[string]*domain.MainProfileSnapshot
	branches     map[string]*domain.Branch
	versions     map[string]*domain.BranchVersion
	globalSkills []domain.Skill
}

func newFakeProfileStore() *fakeProfileStore {
	return &fakeProfileStore{
		profiles:  make(map[string]*domain.Profile),
		snapshots: make(map[string]*domain.MainProfileSnapshot),
		branches:  make(map[string]*domain.Branch),
		versions:  make(map[string]*domain.BranchVersion),
	}
}

// ProfileRepository methods

func (s *fakeProfileStore) GetProfile(_ context.Context, userID string) (*domain.Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.profiles[userID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (s *fakeProfileStore) UpsertProfile(_ context.Context, profile *domain.Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *profile
	if existing, ok := s.profiles[profile.UserID]; ok {
		cp.MainRevision = existing.MainRevision + 1
	} else if cp.MainRevision == 0 {
		cp.MainRevision = 1
	}
	s.profiles[profile.UserID] = &cp
	return nil
}

func (s *fakeProfileStore) IncrementMainRevision(_ context.Context, userID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.profiles[userID]
	if !ok {
		return 0, domain.ErrNotFound
	}
	p.MainRevision++
	return p.MainRevision, nil
}

func (s *fakeProfileStore) GetSnapshot(_ context.Context, userID string) (*domain.MainProfileSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.snapshots[userID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return snap, nil
}

func (s *fakeProfileStore) ReplaceCareerGraph(_ context.Context, userID string, snapshot *domain.MainProfileSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[userID] = snapshot
	prof, ok := s.profiles[userID]
	if !ok {
		cp := snapshot.Profile
		cp.UserID = userID
		cp.MainRevision = snapshot.MainRevision
		s.profiles[userID] = &cp
	} else {
		prof.MainRevision = snapshot.MainRevision
		if snapshot.Profile.FullName != "" {
			prof.FullName = snapshot.Profile.FullName
		}
		if snapshot.Profile.Email != "" {
			prof.Email = snapshot.Profile.Email
		}
	}
	return nil
}

func (s *fakeProfileStore) ListSkills(_ context.Context, _ string) ([]domain.Skill, error) {
	return nil, nil
}
func (s *fakeProfileStore) ListGlobalSkills(_ context.Context) ([]domain.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := make([]domain.Skill, len(s.globalSkills))
	copy(res, s.globalSkills)
	return res, nil
}
func (s *fakeProfileStore) GetSkill(_ context.Context, _, _ string) (*domain.Skill, error) {
	return nil, domain.ErrNotFound
}
func (s *fakeProfileStore) CreateSkill(_ context.Context, _ *domain.Skill) error { return nil }
func (s *fakeProfileStore) UpdateSkill(_ context.Context, _ *domain.Skill) error { return nil }
func (s *fakeProfileStore) DeleteSkill(_ context.Context, _, _ string) error    { return nil }

func (s *fakeProfileStore) ListExperiences(_ context.Context, _ string) ([]domain.WorkExperience, error) {
	return nil, nil
}
func (s *fakeProfileStore) GetExperience(_ context.Context, _, _ string) (*domain.WorkExperience, error) {
	return nil, domain.ErrNotFound
}
func (s *fakeProfileStore) CreateExperience(_ context.Context, _ *domain.WorkExperience) error {
	return nil
}
func (s *fakeProfileStore) UpdateExperience(_ context.Context, _ *domain.WorkExperience) error {
	return nil
}
func (s *fakeProfileStore) DeleteExperience(_ context.Context, _, _ string) error { return nil }

func (s *fakeProfileStore) ListProjects(_ context.Context, _ string) ([]domain.Project, error) {
	return nil, nil
}
func (s *fakeProfileStore) GetProject(_ context.Context, _, _ string) (*domain.Project, error) {
	return nil, domain.ErrNotFound
}
func (s *fakeProfileStore) CreateProject(_ context.Context, _ *domain.Project) error { return nil }
func (s *fakeProfileStore) UpdateProject(_ context.Context, _ *domain.Project) error { return nil }
func (s *fakeProfileStore) DeleteProject(_ context.Context, _, _ string) error       { return nil }

func (s *fakeProfileStore) ListEducation(_ context.Context, _ string) ([]domain.Education, error) {
	return nil, nil
}
func (s *fakeProfileStore) GetEducation(_ context.Context, _, _ string) (*domain.Education, error) {
	return nil, domain.ErrNotFound
}
func (s *fakeProfileStore) CreateEducation(_ context.Context, _ *domain.Education) error { return nil }
func (s *fakeProfileStore) UpdateEducation(_ context.Context, _ *domain.Education) error { return nil }
func (s *fakeProfileStore) DeleteEducation(_ context.Context, _, _ string) error         { return nil }

func (s *fakeProfileStore) ListCertifications(_ context.Context, _ string) ([]domain.Certification, error) {
	return nil, nil
}
func (s *fakeProfileStore) GetCertification(_ context.Context, _, _ string) (*domain.Certification, error) {
	return nil, domain.ErrNotFound
}
func (s *fakeProfileStore) CreateCertification(_ context.Context, _ *domain.Certification) error {
	return nil
}
func (s *fakeProfileStore) UpdateCertification(_ context.Context, _ *domain.Certification) error {
	return nil
}
func (s *fakeProfileStore) DeleteCertification(_ context.Context, _, _ string) error { return nil }

func (s *fakeProfileStore) ListFactsByOwner(_ context.Context, _, _, _ string) ([]domain.Fact, error) {
	return nil, nil
}
func (s *fakeProfileStore) GetFact(_ context.Context, _, _ string) (*domain.Fact, error) {
	return nil, domain.ErrNotFound
}
func (s *fakeProfileStore) CreateFact(_ context.Context, _ *domain.Fact) error { return nil }
func (s *fakeProfileStore) UpdateFact(_ context.Context, _ *domain.Fact) error { return nil }
func (s *fakeProfileStore) DeleteFact(_ context.Context, _, _ string) error    { return nil }

// BranchRepository methods

func (s *fakeProfileStore) ListBranches(_ context.Context, userID string) ([]domain.Branch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res []domain.Branch
	for _, b := range s.branches {
		if b.UserID == userID {
			res = append(res, *b)
		}
	}
	return res, nil
}

func (s *fakeProfileStore) GetBranch(_ context.Context, userID, id string) (*domain.Branch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.branches[id]
	if !ok || (userID != "" && b.UserID != userID) {
		return nil, domain.ErrNotFound
	}
	return b, nil
}

func (s *fakeProfileStore) CreateBranch(_ context.Context, branch *domain.Branch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.branches[branch.ID] = branch
	return nil
}

func (s *fakeProfileStore) UpdateBranch(_ context.Context, branch *domain.Branch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.branches[branch.ID] = branch
	return nil
}

func (s *fakeProfileStore) DeleteBranch(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.branches[id]
	if !ok || b.UserID != userID {
		return domain.ErrNotFound
	}
	delete(s.branches, id)
	return nil
}

func (s *fakeProfileStore) CreateVersion(_ context.Context, version *domain.BranchVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions[version.ID] = version
	return nil
}

func (s *fakeProfileStore) GetVersion(_ context.Context, _, versionID string) (*domain.BranchVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.versions[versionID]
	if !ok {
		return nil, fmt.Errorf("%w: version %s", domain.ErrNotFound, versionID)
	}
	return v, nil
}

func (s *fakeProfileStore) GetVersionByNumber(_ context.Context, _ string, _ int) (*domain.BranchVersion, error) {
	return nil, domain.ErrNotFound
}

func (s *fakeProfileStore) GetVersionsByIDs(_ context.Context, _ []string) ([]domain.BranchVersion, error) {
	return nil, nil
}

func (s *fakeProfileStore) ListVersions(_ context.Context, _ string) ([]domain.BranchVersion, error) {
	return nil, nil
}

func (s *fakeProfileStore) UpdateVersionStatus(_ context.Context, _ string, _ domain.VersionStatus, _ string) error {
	return nil
}

func (s *fakeProfileStore) CompleteVersion(_ context.Context, _ *domain.BranchVersion) error {
	return nil
}

func (s *fakeProfileStore) UpdateVersionATSScore(_ context.Context, _ string, _ *domain.ATSScore) error {
	return nil
}

func (s *fakeProfileStore) FailInterruptedVersions(_ context.Context, _ string) error {
	return nil
}

var _ domain.ProfileRepository = (*fakeProfileStore)(nil)
var _ domain.BranchRepository = (*fakeProfileStore)(nil)
