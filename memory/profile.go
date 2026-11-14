package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// Profile returns a deep copy of the user's profile or core.ErrNotFound.
func (s *Store) Profile(_ context.Context, userID string) (*core.Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prof, ok := s.profiles[userID]
	if !ok {
		return nil, core.ErrNotFound
	}
	return clone(prof), nil
}

// UpsertProfile creates or updates the user's profile, advancing MainRevision.
func (s *Store) UpsertProfile(_ context.Context, profile *core.Profile) error {
	if profile == nil {
		return fmt.Errorf("%w: profile cannot be nil", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	existing, ok := s.profiles[profile.UserID]
	if !ok {
		if profile.MainRevision == 0 {
			profile.MainRevision = 1
		}
		profile.CreatedAt = now
		profile.UpdatedAt = now
		s.profiles[profile.UserID] = clone(profile)
		return nil
	}

	profile.MainRevision = existing.MainRevision + 1
	profile.UpdatedAt = now
	s.profiles[profile.UserID] = clone(profile)
	return nil
}

// IncrementMainRevision atomically bumps the user's profile revision counter.
func (s *Store) IncrementMainRevision(_ context.Context, userID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.incrementRevisionLocked(userID), nil
}

// Snapshot assembles an immutable view of the user's profile and full career graph.
func (s *Store) Snapshot(_ context.Context, userID string) (*core.MainProfileSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prof := s.profiles[userID]
	if prof == nil {
		prof = &core.Profile{ID: "profile-" + userID, UserID: userID}
	}

	snap := &core.MainProfileSnapshot{
		Profile:        *clone(prof),
		MainRevision:   prof.MainRevision,
		ExportedAt:     time.Now().UTC(),
		Skills:         s.collectUserSkillsLocked(userID),
		Experiences:    s.collectExperiencesLocked(userID),
		Projects:       s.collectProjectsLocked(userID),
		Education:      s.collectEducationLocked(userID),
		Certifications: s.collectCertificationsLocked(userID),
		Facts:          s.collectAllUserFactsLocked(userID),
	}

	return clone(snap.ConsolidateSkills()), nil
}

// ReplaceCareerGraph atomically replaces the user's career entities with snapshot contents.
func (s *Store) ReplaceCareerGraph(_ context.Context, userID string, snapshot *core.MainProfileSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("%w: snapshot cannot be nil", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.clearCareerGraphLocked(userID)
	s.insertCareerGraphLocked(userID, snapshot)
	return nil
}

// collectUserSkillsLocked gathers cloned copies of non-global skills owned by userID.
func (s *Store) collectUserSkillsLocked(userID string) []core.Skill {
	var out []core.Skill
	for _, sk := range s.skills {
		if sk.UserID == userID {
			out = append(out, *clone(sk))
		}
	}
	return out
}

// collectOwnerFactsLocked returns cloned facts attached to a specific career entity.
func (s *Store) collectOwnerFactsLocked(userID, ownerID string, ownerType core.OwnerType) []core.Fact {
	var out []core.Fact
	for _, f := range s.facts {
		if f.UserID == userID && f.OwnerID == ownerID && f.OwnerType == ownerType {
			out = append(out, *clone(f))
		}
	}
	return out
}

// collectExperiencesLocked gathers cloned work experiences with preloaded facts.
func (s *Store) collectExperiencesLocked(userID string) []core.WorkExperience {
	var out []core.WorkExperience
	for _, exp := range s.experiences {
		if exp.UserID == userID {
			expCopy := *clone(exp)
			expCopy.Facts = s.collectOwnerFactsLocked(userID, exp.ID, core.OwnerTypeWorkExperience)
			out = append(out, expCopy)
		}
	}
	return out
}

// collectProjectsLocked gathers cloned projects with preloaded facts.
func (s *Store) collectProjectsLocked(userID string) []core.Project {
	var out []core.Project
	for _, proj := range s.projects {
		if proj.UserID == userID {
			projCopy := *clone(proj)
			projCopy.Facts = s.collectOwnerFactsLocked(userID, proj.ID, core.OwnerTypeProject)
			out = append(out, projCopy)
		}
	}
	return out
}

// collectEducationLocked gathers cloned education records with preloaded facts.
func (s *Store) collectEducationLocked(userID string) []core.Education {
	var out []core.Education
	for _, edu := range s.education {
		if edu.UserID == userID {
			eduCopy := *clone(edu)
			eduCopy.Facts = s.collectOwnerFactsLocked(userID, edu.ID, core.OwnerTypeEducation)
			out = append(out, eduCopy)
		}
	}
	return out
}

// collectCertificationsLocked gathers cloned certifications owned by userID.
func (s *Store) collectCertificationsLocked(userID string) []core.Certification {
	var out []core.Certification
	for _, cert := range s.certifications {
		if cert.UserID == userID {
			out = append(out, *clone(cert))
		}
	}
	return out
}

// collectAllUserFactsLocked gathers all cloned facts owned by userID.
func (s *Store) collectAllUserFactsLocked(userID string) []core.Fact {
	var out []core.Fact
	for _, f := range s.facts {
		if f.UserID == userID {
			out = append(out, *clone(f))
		}
	}
	return out
}

// clearCareerGraphLocked deletes all mutable user-owned career entities while preserving global skills.
func (s *Store) clearCareerGraphLocked(userID string) {
	for id, f := range s.facts {
		if f.UserID == userID {
			delete(s.facts, id)
		}
	}
	for id, exp := range s.experiences {
		if exp.UserID == userID {
			delete(s.experiences, id)
		}
	}
	for id, proj := range s.projects {
		if proj.UserID == userID {
			delete(s.projects, id)
		}
	}
	for id, edu := range s.education {
		if edu.UserID == userID {
			delete(s.education, id)
		}
	}
	for id, cert := range s.certifications {
		if cert.UserID == userID {
			delete(s.certifications, id)
		}
	}
	for id, sk := range s.skills {
		if sk.UserID == userID && !sk.IsGlobal {
			delete(s.skills, id)
		}
	}
}

// insertCareerGraphLocked stores a snapshot's career graph and synchronized profile revision.
func (s *Store) insertCareerGraphLocked(userID string, snapshot *core.MainProfileSnapshot) {
	for _, sk := range snapshot.AllSkills() {
		if !sk.IsGlobal && sk.UserID != core.SystemUserID {
			sk.UserID = userID
			s.skills[sk.ID] = clone(&sk)
		}
	}
	for _, exp := range snapshot.Experiences {
		exp.UserID = userID
		s.experiences[exp.ID] = clone(&exp)
		s.storeOwnerFactsLocked(userID, exp.ID, core.OwnerTypeWorkExperience, exp.Facts)
	}
	for _, proj := range snapshot.Projects {
		proj.UserID = userID
		s.projects[proj.ID] = clone(&proj)
		s.storeOwnerFactsLocked(userID, proj.ID, core.OwnerTypeProject, proj.Facts)
	}
	for _, edu := range snapshot.Education {
		edu.UserID = userID
		s.education[edu.ID] = clone(&edu)
		s.storeOwnerFactsLocked(userID, edu.ID, core.OwnerTypeEducation, edu.Facts)
	}
	for _, cert := range snapshot.Certifications {
		cert.UserID = userID
		s.certifications[cert.ID] = clone(&cert)
	}
	for _, f := range snapshot.Facts {
		f.UserID = userID
		s.facts[f.ID] = clone(&f)
	}

	prof := snapshot.Profile
	prof.UserID = userID
	prof.MainRevision = snapshot.MainRevision
	if prof.ID == "" {
		prof.ID = "profile-" + userID
	}
	prof.UpdatedAt = time.Now().UTC()
	s.profiles[userID] = clone(&prof)
}

// storeOwnerFactsLocked writes child facts bound to a parent career entity.
func (s *Store) storeOwnerFactsLocked(userID, ownerID string, ownerType core.OwnerType, facts []core.Fact) {
	for _, f := range facts {
		f.UserID = userID
		f.OwnerID = ownerID
		f.OwnerType = ownerType
		s.facts[f.ID] = clone(&f)
	}
}
