// Package memory provides an in-memory, thread-safe implementation of Resumeee persistence repositories.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// clone produces a detached deep copy of an entity or slice via JSON serialization.
func clone[T any](v T) T {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}

// Store implements domain repository interfaces and repository.Store in memory.
type Store struct {
	mu sync.RWMutex

	profiles       map[string]*core.Profile
	skills         map[string]*core.Skill
	experiences    map[string]*core.WorkExperience
	projects       map[string]*core.Project
	education      map[string]*core.Education
	certifications map[string]*core.Certification
	facts          map[string]*core.Fact
	presets        map[string]*core.Preset
	templates      map[string]*core.TemplateSet
	branches       map[string]*core.Branch
	versions       map[string]*core.BranchVersion
	pdfJobs        map[string]*core.PDFParseJob
	quotas         map[string]*core.TenantQuota
	usageRecords   map[string]*core.LLMUsageRecord
	users          map[string]*core.User
}

// New constructs an empty, thread-safe in-memory store.
func New() *Store {
	return &Store{
		profiles:       make(map[string]*core.Profile),
		skills:         make(map[string]*core.Skill),
		experiences:    make(map[string]*core.WorkExperience),
		projects:       make(map[string]*core.Project),
		education:      make(map[string]*core.Education),
		certifications: make(map[string]*core.Certification),
		facts:          make(map[string]*core.Fact),
		presets:        make(map[string]*core.Preset),
		templates:      make(map[string]*core.TemplateSet),
		branches:       make(map[string]*core.Branch),
		versions:       make(map[string]*core.BranchVersion),
		pdfJobs:        make(map[string]*core.PDFParseJob),
		quotas:         make(map[string]*core.TenantQuota),
		usageRecords:   make(map[string]*core.LLMUsageRecord),
		users:          make(map[string]*core.User),
	}
}

// NewSeeded constructs an in-memory store and populates default user, built-in templates, and presets.
func NewSeeded(ctx context.Context, defaultUserID ...string) (*Store, error) {
	s := New()
	targetUserID := "default"
	if len(defaultUserID) > 0 && defaultUserID[0] != "" {
		targetUserID = defaultUserID[0]
	}
	if err := core.SeedDefaultData(ctx, s, s, s, s, targetUserID); err != nil {
		return nil, fmt.Errorf("seeding in-memory store: %w", err)
	}
	return s, nil
}

// Profile returns this store as core.ProfileRepository.
func (s *Store) Profile() core.ProfileRepository { return s }

// Branch returns this store as core.BranchRepository.
func (s *Store) Branch() core.BranchRepository { return s }

// Preset returns this store as core.PresetRepository.
func (s *Store) Preset() core.PresetRepository { return s }

// Template returns this store as core.TemplateRepository.
func (s *Store) Template() core.TemplateRepository { return s }

// PDFJob returns this store as core.PDFParseJobRepository.
func (s *Store) PDFJob() core.PDFParseJobRepository { return s }

// Quota returns this store as core.QuotaRepository.
func (s *Store) Quota() core.QuotaRepository { return s }

// User returns this store as core.UserRepository.
func (s *Store) User() core.UserRepository { return s }

// Close releases any allocated store resources.
func (s *Store) Close() error { return nil }

// StoreBundle converts the in-memory store into a core.StoreBundle.
func (s *Store) StoreBundle() *core.StoreBundle {
	return &core.StoreBundle{
		ProfileRepo:  s,
		BranchRepo:   s,
		PresetRepo:   s,
		TemplateRepo: s,
		PDFJobRepo:   s,
		QuotaRepo:    s,
		UserRepo:     s,
		Closer:       s.Close,
	}
}

var _ core.Store = (*Store)(nil)

func (s *Store) incrementRevisionLocked(userID string) int64 {
	prof, ok := s.profiles[userID]
	if !ok {
		prof = &core.Profile{
			ID:           "profile-" + userID,
			UserID:       userID,
			FullName:     userID,
			MainRevision: 1,
			CreatedAt:    time.Now().UTC(),
			UpdatedAt:    time.Now().UTC(),
		}
		s.profiles[userID] = prof
		return prof.MainRevision
	}
	prof.MainRevision++
	prof.UpdatedAt = time.Now().UTC()
	return prof.MainRevision
}

// -----------------------------------------------------------------------------
// ProfileRepository
// -----------------------------------------------------------------------------

func (s *Store) GetProfile(_ context.Context, userID string) (*core.Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prof, ok := s.profiles[userID]
	if !ok {
		return nil, core.ErrNotFound
	}
	return clone(prof), nil
}

func (s *Store) UpsertProfile(_ context.Context, profile *core.Profile) error {
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

func (s *Store) IncrementMainRevision(_ context.Context, userID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.incrementRevisionLocked(userID), nil
}

func (s *Store) GetSnapshot(_ context.Context, userID string) (*core.MainProfileSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prof := s.profiles[userID]
	if prof == nil {
		prof = &core.Profile{ID: "profile-" + userID, UserID: userID}
	}

	snap := &core.MainProfileSnapshot{
		Profile:      *clone(prof),
		MainRevision: prof.MainRevision,
		ExportedAt:   time.Now().UTC(),
	}

	for _, sk := range s.skills {
		if sk.UserID == userID {
			snap.Skills = append(snap.Skills, *clone(sk))
		}
	}
	for _, exp := range s.experiences {
		if exp.UserID == userID {
			expCopy := *clone(exp)
			for _, f := range s.facts {
				if f.UserID == userID && f.OwnerID == exp.ID && f.OwnerType == core.OwnerTypeWorkExperience {
					expCopy.Facts = append(expCopy.Facts, *clone(f))
				}
			}
			snap.Experiences = append(snap.Experiences, expCopy)
		}
	}
	for _, proj := range s.projects {
		if proj.UserID == userID {
			projCopy := *clone(proj)
			for _, f := range s.facts {
				if f.UserID == userID && f.OwnerID == proj.ID && f.OwnerType == core.OwnerTypeProject {
					projCopy.Facts = append(projCopy.Facts, *clone(f))
				}
			}
			snap.Projects = append(snap.Projects, projCopy)
		}
	}
	for _, edu := range s.education {
		if edu.UserID == userID {
			eduCopy := *clone(edu)
			for _, f := range s.facts {
				if f.UserID == userID && f.OwnerID == edu.ID && f.OwnerType == core.OwnerTypeEducation {
					eduCopy.Facts = append(eduCopy.Facts, *clone(f))
				}
			}
			snap.Education = append(snap.Education, eduCopy)
		}
	}
	for _, cert := range s.certifications {
		if cert.UserID == userID {
			snap.Certifications = append(snap.Certifications, *clone(cert))
		}
	}
	for _, f := range s.facts {
		if f.UserID == userID {
			snap.Facts = append(snap.Facts, *clone(f))
		}
	}

	return clone(snap.ConsolidateSkills()), nil
}

func (s *Store) ReplaceCareerGraph(_ context.Context, userID string, snapshot *core.MainProfileSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	for _, sk := range snapshot.AllSkills() {
		if !sk.IsGlobal && sk.UserID != core.SystemUserID {
			sk.UserID = userID
			s.skills[sk.ID] = clone(&sk)
		}
	}
	for _, exp := range snapshot.Experiences {
		exp.UserID = userID
		s.experiences[exp.ID] = clone(&exp)
		for _, f := range exp.Facts {
			f.UserID = userID
			f.OwnerID = exp.ID
			f.OwnerType = core.OwnerTypeWorkExperience
			s.facts[f.ID] = clone(&f)
		}
	}
	for _, proj := range snapshot.Projects {
		proj.UserID = userID
		s.projects[proj.ID] = clone(&proj)
		for _, f := range proj.Facts {
			f.UserID = userID
			f.OwnerID = proj.ID
			f.OwnerType = core.OwnerTypeProject
			s.facts[f.ID] = clone(&f)
		}
	}
	for _, edu := range snapshot.Education {
		edu.UserID = userID
		s.education[edu.ID] = clone(&edu)
		for _, f := range edu.Facts {
			f.UserID = userID
			f.OwnerID = edu.ID
			f.OwnerType = core.OwnerTypeEducation
			s.facts[f.ID] = clone(&f)
		}
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
	return nil
}

// -----------------------------------------------------------------------------
// Skills
// -----------------------------------------------------------------------------

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

func (s *Store) GetSkill(_ context.Context, userID, id string) (*core.Skill, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, sk := range s.skills {
		if (sk.UserID == userID || sk.IsGlobal) && (sk.ID == id || sk.ShortID == id) {
			return clone(sk), nil
		}
	}
	return nil, core.ErrNotFound
}

func (s *Store) CreateSkill(_ context.Context, skill *core.Skill) error {
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

func (s *Store) UpdateSkill(_ context.Context, skill *core.Skill) error {
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

func (s *Store) registerSkillsLocked(userID string, skills []core.Skill) []core.Skill {
	resolved := make([]core.Skill, len(skills))
	for i, sk := range skills {
		if sk.UserID == "" {
			sk.UserID = userID
		}
		if sk.ID == "" {
			sk.ID = core.GenerateID("skill")
		}
		var found *core.Skill
		for _, sSk := range s.skills {
			if (sSk.UserID == userID || sSk.IsGlobal) && (sSk.ID == sk.ID || (sk.Name != "" && strings.EqualFold(sSk.Name, sk.Name))) {
				found = sSk
				break
			}
		}
		if found != nil {
			resolved[i] = *clone(found)
		} else {
			s.skills[sk.ID] = clone(&sk)
			resolved[i] = sk
		}
	}
	return resolved
}

// -----------------------------------------------------------------------------
// Work Experience
// -----------------------------------------------------------------------------

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

func (s *Store) GetExperience(_ context.Context, userID, id string) (*core.WorkExperience, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, exp := range s.experiences {
		if exp.UserID == userID && (exp.ID == id || exp.ShortID == id) {
			return clone(exp), nil
		}
	}
	return nil, core.ErrNotFound
}

func (s *Store) CreateExperience(_ context.Context, exp *core.WorkExperience) error {
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

func (s *Store) UpdateExperience(_ context.Context, exp *core.WorkExperience) error {
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

// -----------------------------------------------------------------------------
// Projects
// -----------------------------------------------------------------------------

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

func (s *Store) GetProject(_ context.Context, userID, id string) (*core.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, proj := range s.projects {
		if proj.UserID == userID && (proj.ID == id || proj.ShortID == id) {
			return clone(proj), nil
		}
	}
	return nil, core.ErrNotFound
}

func (s *Store) CreateProject(_ context.Context, proj *core.Project) error {
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

func (s *Store) UpdateProject(_ context.Context, proj *core.Project) error {
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

// -----------------------------------------------------------------------------
// Education
// -----------------------------------------------------------------------------

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

func (s *Store) GetEducation(_ context.Context, userID, id string) (*core.Education, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, edu := range s.education {
		if edu.UserID == userID && (edu.ID == id || edu.ShortID == id) {
			return clone(edu), nil
		}
	}
	return nil, core.ErrNotFound
}

func (s *Store) CreateEducation(_ context.Context, edu *core.Education) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.education[edu.ID]; exists {
		return core.ErrConflict
	}
	s.education[edu.ID] = clone(edu)
	s.incrementRevisionLocked(edu.UserID)
	return nil
}

func (s *Store) UpdateEducation(_ context.Context, edu *core.Education) error {
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

// -----------------------------------------------------------------------------
// Certifications
// -----------------------------------------------------------------------------

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

func (s *Store) GetCertification(_ context.Context, userID, id string) (*core.Certification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, cert := range s.certifications {
		if cert.UserID == userID && (cert.ID == id || cert.ShortID == id) {
			return clone(cert), nil
		}
	}
	return nil, core.ErrNotFound
}

func (s *Store) CreateCertification(_ context.Context, cert *core.Certification) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.certifications[cert.ID]; exists {
		return core.ErrConflict
	}
	s.certifications[cert.ID] = clone(cert)
	s.incrementRevisionLocked(cert.UserID)
	return nil
}

func (s *Store) UpdateCertification(_ context.Context, cert *core.Certification) error {
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

// -----------------------------------------------------------------------------
// Facts
// -----------------------------------------------------------------------------

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

func (s *Store) GetFact(_ context.Context, userID, id string) (*core.Fact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, f := range s.facts {
		if f.UserID == userID && (f.ID == id || f.ShortID == id) {
			return clone(f), nil
		}
	}
	return nil, core.ErrNotFound
}

func (s *Store) CreateFact(_ context.Context, fact *core.Fact) error {
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

func (s *Store) UpdateFact(_ context.Context, fact *core.Fact) error {
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

// -----------------------------------------------------------------------------
// Presets
// -----------------------------------------------------------------------------

func (s *Store) ListPresets(_ context.Context, userID string) ([]core.Preset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []core.Preset
	for _, pr := range s.presets {
		if pr.UserID == userID || pr.UserID == core.SystemUserID || strings.HasPrefix(pr.ID, "builtin-") || pr.IsBuiltIn() {
			list = append(list, *clone(pr))
		}
	}
	return list, nil
}

func (s *Store) GetPreset(_ context.Context, userID, id string) (*core.Preset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pr, ok := s.presets[id]
	if !ok || (pr.UserID != userID && pr.UserID != core.SystemUserID && !strings.HasPrefix(pr.ID, "builtin-") && !pr.IsBuiltIn()) {
		return nil, core.ErrNotFound
	}
	return clone(pr), nil
}

func (s *Store) CreatePreset(_ context.Context, preset *core.Preset) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if preset == nil {
		return core.ErrValidation
	}
	if preset.Language == "" {
		preset.Language = core.DefaultLanguage
	}
	if _, exists := s.presets[preset.ID]; exists {
		return core.ErrConflict
	}
	now := time.Now().UTC()
	if preset.CreatedAt.IsZero() {
		preset.CreatedAt = now
	}
	preset.UpdatedAt = now
	s.presets[preset.ID] = clone(preset)
	return nil
}

func (s *Store) UpdatePreset(_ context.Context, preset *core.Preset) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if preset == nil {
		return core.ErrValidation
	}
	if preset.IsBuiltIn() || strings.HasPrefix(preset.ID, "builtin-") {
		return core.ErrStrategyImmutable
	}
	existing, exists := s.presets[preset.ID]
	if !exists || existing.UserID != preset.UserID {
		return core.ErrNotFound
	}
	if existing.IsBuiltIn() || strings.HasPrefix(existing.ID, "builtin-") {
		return core.ErrStrategyImmutable
	}
	if preset.Language == "" {
		preset.Language = core.DefaultLanguage
	}
	preset.UpdatedAt = time.Now().UTC()
	s.presets[preset.ID] = clone(preset)
	return nil
}

func (s *Store) DeletePreset(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.HasPrefix(id, "builtin-") || userID == core.SystemUserID {
		return core.ErrStrategyImmutable
	}
	existing, exists := s.presets[id]
	if !exists || existing.UserID != userID {
		return core.ErrNotFound
	}
	if existing.IsBuiltIn() || strings.HasPrefix(existing.ID, "builtin-") {
		return core.ErrStrategyImmutable
	}
	delete(s.presets, id)
	return nil
}

// -----------------------------------------------------------------------------
// Templates
// -----------------------------------------------------------------------------

func (s *Store) ListTemplates(_ context.Context, userID string) ([]core.TemplateSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []core.TemplateSet
	for _, tmpl := range s.templates {
		if tmpl.UserID == userID || tmpl.IsBuiltIn || tmpl.UserID == core.SystemUserID {
			list = append(list, *clone(tmpl))
		}
	}
	return list, nil
}

func (s *Store) GetTemplate(_ context.Context, userID, id string) (*core.TemplateSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tmpl, ok := s.templates[id]
	if !ok {
		return nil, core.ErrNotFound
	}
	if tmpl.UserID != userID && !tmpl.IsBuiltIn && tmpl.UserID != core.SystemUserID {
		return nil, core.ErrNotFound
	}
	return clone(tmpl), nil
}

func (s *Store) CreateTemplate(_ context.Context, tmpl *core.TemplateSet) error {
	if tmpl == nil {
		return fmt.Errorf("%w: template set cannot be nil", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.templates[tmpl.ID]; exists {
		return core.ErrConflict
	}
	now := time.Now().UTC()
	if tmpl.CreatedAt.IsZero() {
		tmpl.CreatedAt = now
	}
	tmpl.UpdatedAt = now
	s.templates[tmpl.ID] = clone(tmpl)
	return nil
}

func (s *Store) UpdateTemplate(_ context.Context, tmpl *core.TemplateSet) error {
	if tmpl == nil {
		return fmt.Errorf("%w: template set cannot be nil", core.ErrValidation)
	}
	if tmpl.IsBuiltIn || strings.HasPrefix(tmpl.ID, "builtin-") || tmpl.UserID == core.SystemUserID {
		return core.ErrStrategyImmutable
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.templates[tmpl.ID]
	if !exists {
		return core.ErrNotFound
	}
	if existing.IsBuiltIn || strings.HasPrefix(existing.ID, "builtin-") || existing.UserID == core.SystemUserID {
		return core.ErrStrategyImmutable
	}
	if existing.UserID != tmpl.UserID {
		return core.ErrForbidden
	}
	tmpl.UpdatedAt = time.Now().UTC()
	s.templates[tmpl.ID] = clone(tmpl)
	return nil
}

func (s *Store) DeleteTemplate(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.templates[id]
	if !exists {
		return core.ErrNotFound
	}
	if existing.IsBuiltIn || strings.HasPrefix(existing.ID, "builtin-") || existing.UserID == core.SystemUserID {
		return core.ErrStrategyImmutable
	}
	if existing.UserID != userID {
		return core.ErrForbidden
	}
	delete(s.templates, id)
	return nil
}

// -----------------------------------------------------------------------------
// Branches
// -----------------------------------------------------------------------------

func (s *Store) ListBranches(_ context.Context, userID string) ([]core.Branch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var currentMainRev int64
	if prof, ok := s.profiles[userID]; ok {
		currentMainRev = prof.MainRevision
	}

	var list []core.Branch
	for _, br := range s.branches {
		if br.UserID == userID {
			bCopy := clone(br)
			bCopy.IsStale = bCopy.IsStaleRevision(currentMainRev)
			list = append(list, *bCopy)
		}
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].IsDefault != list[j].IsDefault {
			return list[i].IsDefault
		}
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})

	return list, nil
}

func (s *Store) GetBranch(_ context.Context, userID, id string) (*core.Branch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	br, ok := s.branches[id]
	if !ok || (userID != "" && br.UserID != userID) {
		return nil, core.ErrNotFound
	}

	var currentMainRev int64
	if prof, ok := s.profiles[br.UserID]; ok {
		currentMainRev = prof.MainRevision
	}

	bCopy := clone(br)
	bCopy.IsStale = bCopy.IsStaleRevision(currentMainRev)
	return bCopy, nil
}

func (s *Store) CreateBranch(_ context.Context, branch *core.Branch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.branches[branch.ID]; exists {
		return core.ErrConflict
	}
	now := time.Now().UTC()
	if branch.CreatedAt.IsZero() {
		branch.CreatedAt = now
	}
	branch.UpdatedAt = now
	s.branches[branch.ID] = clone(branch)
	return nil
}

func (s *Store) UpdateBranch(_ context.Context, branch *core.Branch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.branches[branch.ID]; !exists {
		return core.ErrNotFound
	}
	branch.UpdatedAt = time.Now().UTC()
	s.branches[branch.ID] = clone(branch)
	return nil
}

func (s *Store) DeleteBranch(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	br, ok := s.branches[id]
	if !ok || br.UserID != userID {
		return core.ErrNotFound
	}
	if br.IsDefault {
		return fmt.Errorf("%w: cannot delete default branch", core.ErrDefaultBranchImmutable)
	}

	for vID, v := range s.versions {
		if v.ResumeID == id || v.BranchID == id {
			delete(s.versions, vID)
		}
	}
	delete(s.branches, id)
	return nil
}

// -----------------------------------------------------------------------------
// Versions
// -----------------------------------------------------------------------------

func syncVersionIDs(v *core.BranchVersion) {
	if v == nil {
		return
	}
	if v.ResumeID == "" && v.BranchID != "" {
		v.ResumeID = v.BranchID
	}
	if v.BranchID == "" && v.ResumeID != "" {
		v.BranchID = v.ResumeID
	}
}

func (s *Store) CreateVersion(_ context.Context, version *core.BranchVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	targetID := version.ResumeID
	if targetID == "" {
		targetID = version.BranchID
	}
	version.ResumeID = targetID
	version.BranchID = targetID
	if version.UserID == "" && targetID != "" {
		if b, ok := s.branches[targetID]; ok && b.UserID != "" {
			version.UserID = b.UserID
		}
	}

	var maxVer int
	for _, v := range s.versions {
		if v.ResumeID == targetID || v.BranchID == targetID {
			if v.VersionNumber > maxVer {
				maxVer = v.VersionNumber
			}
		}
	}

	version.VersionNumber = maxVer + 1
	if version.Status == "" {
		version.Status = core.VersionStatusPending
	}
	if version.CreatedAt.IsZero() {
		version.CreatedAt = time.Now().UTC()
	}

	s.versions[version.ID] = clone(version)
	return nil
}

func (s *Store) GetVersion(_ context.Context, branchID, versionID string) (*core.BranchVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	v, ok := s.versions[versionID]
	if !ok {
		return nil, core.ErrNotFound
	}
	if branchID != "" && v.ResumeID != branchID && v.BranchID != branchID {
		return nil, core.ErrNotFound
	}

	copyV := clone(v)
	syncVersionIDs(copyV)
	return copyV, nil
}

func (s *Store) GetVersionByNumber(_ context.Context, branchID string, versionNumber int) (*core.BranchVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, v := range s.versions {
		if (v.ResumeID == branchID || v.BranchID == branchID) && v.VersionNumber == versionNumber {
			copyV := clone(v)
			syncVersionIDs(copyV)
			return copyV, nil
		}
	}
	return nil, core.ErrNotFound
}

func (s *Store) GetVersionsByIDs(_ context.Context, versionIDs []string) ([]core.BranchVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []core.BranchVersion
	for _, id := range versionIDs {
		if v, ok := s.versions[id]; ok {
			copyV := clone(v)
			syncVersionIDs(copyV)
			result = append(result, *copyV)
		}
	}
	return result, nil
}

func (s *Store) ListVersions(_ context.Context, branchID string) ([]core.BranchVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []core.BranchVersion
	for _, v := range s.versions {
		if v.ResumeID == branchID || v.BranchID == branchID {
			copyV := clone(v)
			syncVersionIDs(copyV)
			list = append(list, *copyV)
		}
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].VersionNumber < list[j].VersionNumber
	})

	return list, nil
}

func (s *Store) UpdateVersionStatus(_ context.Context, versionID string, status core.VersionStatus, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.versions[versionID]
	if !ok {
		return core.ErrNotFound
	}
	v.Status = status
	v.ErrorMessage = errMsg
	if status == core.VersionStatusCompleted || status == core.VersionStatusCompletedWithWarnings || status == core.VersionStatusFailed {
		now := time.Now().UTC()
		v.CompletedAt = &now
	}
	return nil
}

func (s *Store) CompleteVersion(_ context.Context, version *core.BranchVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	parentID := version.ResumeID
	if parentID == "" {
		parentID = version.BranchID
	}
	version.ResumeID = parentID
	version.BranchID = parentID

	now := time.Now().UTC()
	version.CompletedAt = &now
	if version.Status == "" {
		version.Status = core.VersionStatusCompleted
	}

	s.versions[version.ID] = clone(version)

	br, ok := s.branches[parentID]
	if !ok {
		return core.ErrNotFound
	}
	br.ActiveVersionID = &version.ID
	br.LastSyncedMainRevision = version.MainRevisionUsed
	if version.GapReport != nil {
		br.GapReport = clone(version.GapReport)
	} else {
		br.GapReport = nil
	}
	if version.ATSScore != nil {
		br.ATSScore = clone(version.ATSScore)
	} else {
		br.ATSScore = nil
	}
	br.UpdatedAt = now
	return nil
}

func (s *Store) UpdateVersionATSScore(_ context.Context, versionID string, atsScore *core.ATSScore) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.versions[versionID]
	if !ok {
		return core.ErrNotFound
	}
	if atsScore != nil && v.ATSScore != nil {
		return fmt.Errorf("%w: ATS score already exists for version %s", core.ErrConflict, versionID)
	}
	if atsScore != nil {
		scoreCopy := clone(atsScore)
		scoreCopy.Normalize()
		v.ATSScore = scoreCopy
	} else {
		v.ATSScore = nil
	}

	parentID := v.ResumeID
	if parentID == "" {
		parentID = v.BranchID
	}
	if br, exists := s.branches[parentID]; exists && br.ActiveVersionID != nil && *br.ActiveVersionID == versionID {
		if v.ATSScore != nil {
			br.ATSScore = clone(v.ATSScore)
		} else {
			br.ATSScore = nil
		}
		br.UpdatedAt = time.Now().UTC()
	}
	return nil
}

func (s *Store) FailInterruptedVersions(_ context.Context, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	for _, v := range s.versions {
		if v.Status == core.VersionStatusPending || v.Status == core.VersionStatusProcessing {
			v.Status = core.VersionStatusFailed
			v.ErrorMessage = reason
			v.CompletedAt = &now
		}
	}
	return nil
}

// -----------------------------------------------------------------------------
// PDFParseJobRepository
// -----------------------------------------------------------------------------

func (s *Store) CreateJob(_ context.Context, job *core.PDFParseJob) error {
	if job == nil {
		return fmt.Errorf("%w: job cannot be nil", core.ErrValidation)
	}
	if job.ID == "" || job.UserID == "" {
		return fmt.Errorf("%w: job id and user_id are required", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	}
	s.pdfJobs[job.ID] = clone(job)
	return nil
}

func (s *Store) GetJob(_ context.Context, userID, jobID string) (*core.PDFParseJob, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}
	if jobID == "" {
		return nil, fmt.Errorf("%w: job id cannot be empty", core.ErrValidation)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	job, ok := s.pdfJobs[jobID]
	if !ok || job.UserID != userID {
		return nil, core.ErrNotFound
	}
	return clone(job), nil
}

func (s *Store) UpdateJobStatus(_ context.Context, jobID string, status core.PDFParseJobStatus, result any, errMsg string) error {
	if jobID == "" {
		return fmt.Errorf("%w: job id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.pdfJobs[jobID]
	if !ok {
		return core.ErrNotFound
	}

	now := time.Now().UTC()
	job.Status = status
	job.UpdatedAt = now
	if errMsg != "" {
		job.ErrorMessage = errMsg
	}
	if result != nil {
		if b, err := json.Marshal(result); err == nil {
			var jsonResult any
			if err := json.Unmarshal(b, &jsonResult); err == nil {
				job.Result = jsonResult
			} else {
				job.Result = result
			}
		} else {
			job.Result = result
		}
	}
	if status == core.PDFParseJobStatusCompleted || status == core.PDFParseJobStatusFailed {
		job.CompletedAt = &now
	}
	return nil
}

func (s *Store) FailInterruptedJobs(_ context.Context, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	for _, job := range s.pdfJobs {
		if job.Status == core.PDFParseJobStatusPending || job.Status == core.PDFParseJobStatusProcessing {
			job.Status = core.PDFParseJobStatusFailed
			job.ErrorMessage = reason
			job.UpdatedAt = now
			job.CompletedAt = &now
		}
	}
	return nil
}

func (s *Store) CleanupCompletedJobs(_ context.Context, userID string, olderThan time.Duration) error {
	if userID == "" {
		return fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().UTC().Add(-olderThan)
	for id, job := range s.pdfJobs {
		if job.UserID == userID && (job.Status == core.PDFParseJobStatusCompleted || job.Status == core.PDFParseJobStatusFailed) {
			if (job.CompletedAt != nil && job.CompletedAt.Before(cutoff)) || (job.CompletedAt == nil && job.UpdatedAt.Before(cutoff)) {
				delete(s.pdfJobs, id)
			}
		}
	}
	return nil
}

// -----------------------------------------------------------------------------
// Quota
// -----------------------------------------------------------------------------

func (s *Store) GetQuota(_ context.Context, userID string) (*core.TenantQuota, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	quota, ok := s.quotas[userID]
	if !ok {
		quota = &core.TenantQuota{
			UserID:             userID,
			DailyRequestsUsed:  0,
			DailyRequestLimit:  50,
			DailyWindowStart:   now,
			MonthlyTokensUsed:  0,
			MonthlyTokenLimit:  1000000,
			MonthlyWindowStart: now,
			UpdatedAt:          now,
		}
		s.quotas[userID] = quota
		return clone(quota), nil
	}

	if quota.ResetWindowsIfExpired(now) {
		quota.UpdatedAt = now
	}

	return clone(quota), nil
}

func (s *Store) ReserveQuota(
	_ context.Context,
	userID string,
	op core.LLMOperationType,
	estimatedTokens int,
	dailyLimit int,
	monthlyLimit int,
) (*core.LLMUsageRecord, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	quota, ok := s.quotas[userID]
	if !ok {
		dLimit := 50
		if dailyLimit > 0 {
			dLimit = dailyLimit
		}
		mLimit := 1000000
		if monthlyLimit > 0 {
			mLimit = monthlyLimit
		}
		quota = &core.TenantQuota{
			UserID:             userID,
			DailyRequestsUsed:  0,
			DailyRequestLimit:  dLimit,
			DailyWindowStart:   now,
			MonthlyTokensUsed:  0,
			MonthlyTokenLimit:  mLimit,
			MonthlyWindowStart: now,
			UpdatedAt:          now,
		}
		s.quotas[userID] = quota
	}

	if quota.ResetWindowsIfExpired(now) {
		quota.UpdatedAt = now
	}

	if dailyLimit > 0 && quota.DailyRequestLimit != dailyLimit {
		quota.DailyRequestLimit = dailyLimit
	}
	if monthlyLimit > 0 && quota.MonthlyTokenLimit != monthlyLimit {
		quota.MonthlyTokenLimit = monthlyLimit
	}

	if quota.DailyRequestsUsed+1 > quota.DailyRequestLimit {
		return nil, core.ErrQuotaExceeded
	}
	if quota.MonthlyTokensUsed+estimatedTokens > quota.MonthlyTokenLimit {
		return nil, core.ErrQuotaExceeded
	}

	quota.DailyRequestsUsed++
	quota.MonthlyTokensUsed += estimatedTokens
	quota.UpdatedAt = now

	record := &core.LLMUsageRecord{
		ID:              fmt.Sprintf("usage-%d", time.Now().UnixNano()),
		UserID:          userID,
		Operation:       op,
		EstimatedTokens: estimatedTokens,
		Status:          core.ReservationStatusPending,
		CreatedAt:       now,
	}
	s.usageRecords[record.ID] = record

	return clone(record), nil
}

func (s *Store) CommitUsage(_ context.Context, reservationID string, usage core.TokenUsage) error {
	if reservationID == "" {
		return fmt.Errorf("%w: reservation id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.usageRecords[reservationID]
	if !ok {
		return core.ErrNotFound
	}
	if record.Status != core.ReservationStatusPending {
		return nil
	}

	if quota, ok := s.quotas[record.UserID]; ok {
		delta := usage.TotalTokens - record.EstimatedTokens
		quota.AdjustMonthlyTokens(delta)
		quota.UpdatedAt = time.Now().UTC()
	}

	now := time.Now().UTC()
	record.Status = core.ReservationStatusCommitted
	record.PromptTokens = usage.PromptTokens
	record.CompletionTokens = usage.CompletionTokens
	record.TotalTokens = usage.TotalTokens
	record.CommittedAt = &now
	return nil
}

func (s *Store) ReleaseQuota(_ context.Context, reservationID string, errMsg string) error {
	if reservationID == "" {
		return fmt.Errorf("%w: reservation id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.usageRecords[reservationID]
	if !ok {
		return core.ErrNotFound
	}
	if record.Status != core.ReservationStatusPending {
		return nil
	}

	if quota, ok := s.quotas[record.UserID]; ok {
		quota.ReleaseReservation(record.EstimatedTokens)
		quota.UpdatedAt = time.Now().UTC()
	}

	record.Status = core.ReservationStatusReleased
	record.ErrorMessage = errMsg
	return nil
}

func (s *Store) ListUsageRecords(_ context.Context, userID string, limit int) ([]core.LLMUsageRecord, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}
	if limit <= 0 {
		limit = 50
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []core.LLMUsageRecord
	for _, rec := range s.usageRecords {
		if rec.UserID == userID {
			list = append(list, *clone(rec))
		}
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})

	if len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

// -----------------------------------------------------------------------------
// Users
// -----------------------------------------------------------------------------

func (s *Store) GetUser(_ context.Context, id string) (*core.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	u, ok := s.users[id]
	if !ok {
		return nil, core.ErrNotFound
	}
	return clone(u), nil
}

func (s *Store) UpsertUser(_ context.Context, user *core.User) error {
	if user == nil || user.ID == "" {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	existing, ok := s.users[user.ID]
	if !ok {
		u := clone(user)
		if u.CreatedAt.IsZero() {
			u.CreatedAt = now
		}
		u.UpdatedAt = now
		s.users[user.ID] = u
		return nil
	}

	existing.Email = user.Email
	existing.FullName = user.FullName
	// Preserve existing admin role unless explicitly promoted
	if existing.Role == core.UserRoleAdmin && user.Role != core.UserRoleAdmin {
		existing.Role = core.UserRoleAdmin
	} else if user.Role != "" {
		existing.Role = user.Role
	}
	if user.Tier != "" {
		existing.Tier = user.Tier
	}
	existing.IsActive = user.IsActive
	existing.UpdatedAt = now
	s.users[user.ID] = existing
	return nil
}
