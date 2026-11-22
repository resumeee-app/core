package profile_test

import (
	"context"
	"fmt"
	"sync"

	"github.com/resumeee-app/core"
	"github.com/resumeee-app/core/profile"
)

type fakeProfileStore struct {
	mu           sync.Mutex
	profiles     map[string]*core.Profile
	snapshots    map[string]*core.MainProfileSnapshot
	branches     map[string]*core.Branch
	versions     map[string]*core.BranchVersion
	globalSkills []core.Skill
}

func newFakeProfileStore() *fakeProfileStore {
	return &fakeProfileStore{
		profiles:  make(map[string]*core.Profile),
		snapshots: make(map[string]*core.MainProfileSnapshot),
		branches:  make(map[string]*core.Branch),
		versions:  make(map[string]*core.BranchVersion),
	}
}

// ProfileStore methods

func (s *fakeProfileStore) Profile(_ context.Context, userID string) (*core.Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.profiles[userID]
	if !ok {
		return nil, core.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (s *fakeProfileStore) UpsertProfile(_ context.Context, profile *core.Profile) error {
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
		return 0, core.ErrNotFound
	}
	p.MainRevision++
	return p.MainRevision, nil
}

func (s *fakeProfileStore) Snapshot(_ context.Context, userID string) (*core.MainProfileSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.snapshots[userID]
	if !ok {
		return nil, core.ErrNotFound
	}
	return snap, nil
}

func (s *fakeProfileStore) ReplaceCareerGraph(_ context.Context, userID string, snapshot *core.MainProfileSnapshot) error {
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

func (s *fakeProfileStore) ListGlobalSkills(_ context.Context) ([]core.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := make([]core.Skill, len(s.globalSkills))
	copy(res, s.globalSkills)
	return res, nil
}

// BranchStore methods

func (s *fakeProfileStore) ListBranches(_ context.Context, userID string) ([]core.Branch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res []core.Branch
	for _, b := range s.branches {
		if b.UserID == userID {
			res = append(res, *b)
		}
	}
	return res, nil
}

func (s *fakeProfileStore) Branch(_ context.Context, userID, id string) (*core.Branch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.branches[id]
	if !ok || (userID != "" && b.UserID != userID) {
		return nil, core.ErrNotFound
	}
	return b, nil
}

func (s *fakeProfileStore) CreateBranch(_ context.Context, branch *core.Branch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.branches[branch.ID] = branch
	return nil
}

func (s *fakeProfileStore) UpdateBranch(_ context.Context, branch *core.Branch) error {
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
		return core.ErrNotFound
	}
	delete(s.branches, id)
	return nil
}

func (s *fakeProfileStore) Version(_ context.Context, _, versionID string) (*core.BranchVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.versions[versionID]
	if !ok {
		return nil, fmt.Errorf("%w: version %s", core.ErrNotFound, versionID)
	}
	return v, nil
}

// Compile-time interface assertions ensuring fakeProfileStore satisfies the store contracts required by profile.Service tests.
var (
	_ profile.Store        = (*fakeProfileStore)(nil)
	_ profile.SkillCatalog = (*fakeProfileStore)(nil)
	_ profile.BranchReader = (*fakeProfileStore)(nil)
)
