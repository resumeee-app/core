package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/resumeee-app/resumeee-core"
)

// matchesEntityID checks whether a non-empty lookup key matches an entity ID or ShortID.
func matchesEntityID(id, shortID, query string) bool {
	return query != "" && (id == query || shortID == query)
}

// ListSkills returns all skills owned by userID plus shared global catalog skills.
func (s *Store) ListSkills(_ context.Context, userID string) ([]core.Skill, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []core.Skill
	for _, sk := range s.skills {
		if sk.UserID == userID || sk.IsGlobal {
			list = append(list, *clone(sk))
		}
	}
	return list, nil
}

// ListGlobalSkills returns all canonical skills from the shared global catalog.
func (s *Store) ListGlobalSkills(_ context.Context) ([]core.Skill, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []core.Skill
	for _, sk := range s.skills {
		if sk.IsGlobal {
			list = append(list, *clone(sk))
		}
	}
	return list, nil
}

// Skill fetches a user or global skill matching ID or ShortID.
func (s *Store) Skill(_ context.Context, userID, id string) (*core.Skill, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, sk := range s.skills {
		if (sk.UserID == userID || sk.IsGlobal) && matchesEntityID(sk.ID, sk.ShortID, id) {
			return clone(sk), nil
		}
	}
	return nil, core.ErrNotFound
}

// CreateSkill persists a new skill, rejecting non-system attempts to create global skills.
func (s *Store) CreateSkill(_ context.Context, skill *core.Skill) error {
	if skill == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if skill.IsGlobal && skill.UserID != core.SystemUserID {
		return fmt.Errorf("%w: only system may create global skills", core.ErrForbidden)
	}
	if _, exists := s.skills[skill.ID]; exists {
		return core.ErrConflict
	}
	s.skills[skill.ID] = clone(skill)
	if skill.UserID != core.SystemUserID {
		s.incrementRevisionLocked(skill.UserID)
	}
	return nil
}

// UpdateSkill replaces a user-owned skill while protecting global catalog entries.
func (s *Store) UpdateSkill(_ context.Context, skill *core.Skill) error {
	if skill == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.skills[skill.ID]
	if !exists {
		return core.ErrNotFound
	}
	if existing.IsGlobal {
		return core.ErrGlobalSkillImmutable
	}
	if existing.UserID != skill.UserID {
		return core.ErrForbidden
	}
	s.skills[skill.ID] = clone(skill)
	s.incrementRevisionLocked(skill.UserID)
	return nil
}

// DeleteSkill removes a user-owned skill while protecting global catalog entries.
func (s *Store) DeleteSkill(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.skills[id]
	if !exists {
		return core.ErrNotFound
	}
	if existing.IsGlobal {
		return core.ErrGlobalSkillImmutable
	}
	if existing.UserID != userID {
		return core.ErrForbidden
	}
	delete(s.skills, id)
	s.incrementRevisionLocked(userID)
	return nil
}

// registerSkillsLocked resolves attached skills against existing user/global entries or registers missing ones.
func (s *Store) registerSkillsLocked(userID string, skills []core.Skill) []core.Skill {
	resolved := make([]core.Skill, len(skills))
	for i, sk := range skills {
		if sk.UserID == "" {
			sk.UserID = userID
		}
		if sk.ID == "" {
			sk.ID = core.GenerateID("skill")
		}
		if found := s.findMatchingSkillLocked(userID, sk); found != nil {
			resolved[i] = *clone(found)
			continue
		}
		s.skills[sk.ID] = clone(&sk)
		resolved[i] = sk
	}
	return resolved
}

// findMatchingSkillLocked looks up an existing user or global skill matching sk by ID or name.
func (s *Store) findMatchingSkillLocked(userID string, sk core.Skill) *core.Skill {
	for _, existing := range s.skills {
		if existing.UserID != userID && !existing.IsGlobal {
			continue
		}
		if existing.ID == sk.ID || (sk.Name != "" && strings.EqualFold(existing.Name, sk.Name)) {
			return existing
		}
	}
	return nil
}

// ListExperiences returns all work experiences owned by userID.
func (s *Store) ListExperiences(_ context.Context, userID string) ([]core.WorkExperience, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []core.WorkExperience
	for _, exp := range s.experiences {
		if exp.UserID == userID {
			list = append(list, *clone(exp))
		}
	}
	return list, nil
}

// Experience fetches a user-owned work experience matching ID or ShortID.
func (s *Store) Experience(_ context.Context, userID, id string) (*core.WorkExperience, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, exp := range s.experiences {
		if exp.UserID == userID && matchesEntityID(exp.ID, exp.ShortID, id) {
			return clone(exp), nil
		}
	}
	return nil, core.ErrNotFound
}

// CreateExperience persists a new work experience and bumps the owner's MainRevision.
func (s *Store) CreateExperience(_ context.Context, exp *core.WorkExperience) error {
	if exp == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.experiences[exp.ID]; exists {
		return core.ErrConflict
	}
	exp.Skills = s.registerSkillsLocked(exp.UserID, exp.Skills)
	s.experiences[exp.ID] = clone(exp)
	s.incrementRevisionLocked(exp.UserID)
	return nil
}

// UpdateExperience replaces a user-owned work experience and bumps MainRevision.
func (s *Store) UpdateExperience(_ context.Context, exp *core.WorkExperience) error {
	if exp == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.experiences[exp.ID]
	if !exists || existing.UserID != exp.UserID {
		return core.ErrNotFound
	}
	exp.Skills = s.registerSkillsLocked(exp.UserID, exp.Skills)
	s.experiences[exp.ID] = clone(exp)
	s.incrementRevisionLocked(exp.UserID)
	return nil
}

// DeleteExperience deletes a user-owned work experience and bumps MainRevision.
func (s *Store) DeleteExperience(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.experiences[id]
	if !exists || existing.UserID != userID {
		return core.ErrNotFound
	}
	delete(s.experiences, id)
	s.incrementRevisionLocked(userID)
	return nil
}

// ListProjects returns all projects owned by userID.
func (s *Store) ListProjects(_ context.Context, userID string) ([]core.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []core.Project
	for _, proj := range s.projects {
		if proj.UserID == userID {
			list = append(list, *clone(proj))
		}
	}
	return list, nil
}

// Project fetches a user-owned project matching ID or ShortID.
func (s *Store) Project(_ context.Context, userID, id string) (*core.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, proj := range s.projects {
		if proj.UserID == userID && matchesEntityID(proj.ID, proj.ShortID, id) {
			return clone(proj), nil
		}
	}
	return nil, core.ErrNotFound
}

// CreateProject persists a new project and bumps the owner's MainRevision.
func (s *Store) CreateProject(_ context.Context, proj *core.Project) error {
	if proj == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.projects[proj.ID]; exists {
		return core.ErrConflict
	}
	proj.Skills = s.registerSkillsLocked(proj.UserID, proj.Skills)
	s.projects[proj.ID] = clone(proj)
	s.incrementRevisionLocked(proj.UserID)
	return nil
}

// UpdateProject replaces a user-owned project and bumps MainRevision.
func (s *Store) UpdateProject(_ context.Context, proj *core.Project) error {
	if proj == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.projects[proj.ID]
	if !exists || existing.UserID != proj.UserID {
		return core.ErrNotFound
	}
	proj.Skills = s.registerSkillsLocked(proj.UserID, proj.Skills)
	s.projects[proj.ID] = clone(proj)
	s.incrementRevisionLocked(proj.UserID)
	return nil
}

// DeleteProject deletes a user-owned project and bumps MainRevision.
func (s *Store) DeleteProject(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.projects[id]
	if !exists || existing.UserID != userID {
		return core.ErrNotFound
	}
	delete(s.projects, id)
	s.incrementRevisionLocked(userID)
	return nil
}

// ListEducation returns all education records owned by userID.
func (s *Store) ListEducation(_ context.Context, userID string) ([]core.Education, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []core.Education
	for _, edu := range s.education {
		if edu.UserID == userID {
			list = append(list, *clone(edu))
		}
	}
	return list, nil
}

// Education fetches a user-owned education entry matching ID or ShortID.
func (s *Store) Education(_ context.Context, userID, id string) (*core.Education, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, edu := range s.education {
		if edu.UserID == userID && matchesEntityID(edu.ID, edu.ShortID, id) {
			return clone(edu), nil
		}
	}
	return nil, core.ErrNotFound
}

// CreateEducation persists a new education record and bumps the owner's MainRevision.
func (s *Store) CreateEducation(_ context.Context, edu *core.Education) error {
	if edu == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.education[edu.ID]; exists {
		return core.ErrConflict
	}
	s.education[edu.ID] = clone(edu)
	s.incrementRevisionLocked(edu.UserID)
	return nil
}

// UpdateEducation replaces a user-owned education record and bumps MainRevision.
func (s *Store) UpdateEducation(_ context.Context, edu *core.Education) error {
	if edu == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.education[edu.ID]
	if !exists || existing.UserID != edu.UserID {
		return core.ErrNotFound
	}
	s.education[edu.ID] = clone(edu)
	s.incrementRevisionLocked(edu.UserID)
	return nil
}

// DeleteEducation deletes a user-owned education record and bumps MainRevision.
func (s *Store) DeleteEducation(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.education[id]
	if !exists || existing.UserID != userID {
		return core.ErrNotFound
	}
	delete(s.education, id)
	s.incrementRevisionLocked(userID)
	return nil
}

// ListCertifications returns all certifications owned by userID.
func (s *Store) ListCertifications(_ context.Context, userID string) ([]core.Certification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []core.Certification
	for _, cert := range s.certifications {
		if cert.UserID == userID {
			list = append(list, *clone(cert))
		}
	}
	return list, nil
}

// Certification fetches a user-owned certification matching ID or ShortID.
func (s *Store) Certification(_ context.Context, userID, id string) (*core.Certification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, cert := range s.certifications {
		if cert.UserID == userID && matchesEntityID(cert.ID, cert.ShortID, id) {
			return clone(cert), nil
		}
	}
	return nil, core.ErrNotFound
}

// CreateCertification persists a new certification and bumps the owner's MainRevision.
func (s *Store) CreateCertification(_ context.Context, cert *core.Certification) error {
	if cert == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.certifications[cert.ID]; exists {
		return core.ErrConflict
	}
	s.certifications[cert.ID] = clone(cert)
	s.incrementRevisionLocked(cert.UserID)
	return nil
}

// UpdateCertification replaces a user-owned certification and bumps MainRevision.
func (s *Store) UpdateCertification(_ context.Context, cert *core.Certification) error {
	if cert == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.certifications[cert.ID]
	if !exists || existing.UserID != cert.UserID {
		return core.ErrNotFound
	}
	s.certifications[cert.ID] = clone(cert)
	s.incrementRevisionLocked(cert.UserID)
	return nil
}

// DeleteCertification deletes a user-owned certification and bumps MainRevision.
func (s *Store) DeleteCertification(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.certifications[id]
	if !exists || existing.UserID != userID {
		return core.ErrNotFound
	}
	delete(s.certifications, id)
	s.incrementRevisionLocked(userID)
	return nil
}

// ListFactsByOwner returns all facts belonging to ownerType and ownerID for userID.
func (s *Store) ListFactsByOwner(_ context.Context, userID, ownerType, ownerID string) ([]core.Fact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []core.Fact
	for _, f := range s.facts {
		if f.UserID == userID && string(f.OwnerType) == ownerType && f.OwnerID == ownerID {
			list = append(list, *clone(f))
		}
	}
	return list, nil
}

// Fact fetches a user-owned fact matching ID or ShortID.
func (s *Store) Fact(_ context.Context, userID, id string) (*core.Fact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, f := range s.facts {
		if f.UserID == userID && matchesEntityID(f.ID, f.ShortID, id) {
			return clone(f), nil
		}
	}
	return nil, core.ErrNotFound
}

// CreateFact persists a new fact and bumps the owner's MainRevision.
func (s *Store) CreateFact(_ context.Context, fact *core.Fact) error {
	if fact == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.facts[fact.ID]; exists {
		return core.ErrConflict
	}
	fact.Skills = s.registerSkillsLocked(fact.UserID, fact.Skills)
	s.facts[fact.ID] = clone(fact)
	s.incrementRevisionLocked(fact.UserID)
	return nil
}

// UpdateFact replaces a user-owned fact and bumps MainRevision.
func (s *Store) UpdateFact(_ context.Context, fact *core.Fact) error {
	if fact == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.facts[fact.ID]
	if !exists || existing.UserID != fact.UserID {
		return core.ErrNotFound
	}
	fact.Skills = s.registerSkillsLocked(fact.UserID, fact.Skills)
	s.facts[fact.ID] = clone(fact)
	s.incrementRevisionLocked(fact.UserID)
	return nil
}

// DeleteFact deletes a user-owned fact and bumps MainRevision.
func (s *Store) DeleteFact(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.facts[id]
	if !exists || existing.UserID != userID {
		return core.ErrNotFound
	}
	delete(s.facts, id)
	s.incrementRevisionLocked(userID)
	return nil
}
