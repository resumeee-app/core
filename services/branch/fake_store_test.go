package branch_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/privacy"
	"github.com/resumeee-app/resumeee-core/seed"
)

type fakeBranchStore struct {
	mu           sync.Mutex
	profiles     map[string]*core.Profile
	experiences  map[string]*core.WorkExperience
	skills       map[string]*core.Skill
	facts        map[string]*core.Fact
	branches     map[string]*core.Branch
	versions     map[string]*core.BranchVersion
	presets      map[string]*core.Preset
	templates    map[string]*core.TemplateSet
	usageRecords map[string]*core.LLMUsageRecord
}

func newSeededBranchStore(_ context.Context, defaultUserID string) *fakeBranchStore {
	s := &fakeBranchStore{
		profiles:     make(map[string]*core.Profile),
		experiences:  make(map[string]*core.WorkExperience),
		skills:       make(map[string]*core.Skill),
		facts:        make(map[string]*core.Fact),
		branches:     make(map[string]*core.Branch),
		versions:     make(map[string]*core.BranchVersion),
		presets:      make(map[string]*core.Preset),
		templates:    make(map[string]*core.TemplateSet),
		usageRecords: make(map[string]*core.LLMUsageRecord),
	}

	// 1. Seed Profile
	s.profiles[defaultUserID] = &core.Profile{
		UserID:       defaultUserID,
		FullName:     "Default User",
		MainRevision: 1,
	}

	// 2. Seed Built-In Templates
	for _, tmpl := range seed.DefaultTemplateSets() {
		cp := tmpl
		s.templates[tmpl.ID] = &cp
	}

	// 3. Seed Default Preset
	s.presets["preset-1"] = &core.Preset{
		ID:                 "preset-1",
		UserID:             defaultUserID,
		Name:               "Default Technical",
		SystemInstructions: "Focus on technical impact.",
	}

	// 4. Seed Default Branch
	defaultBranchID := core.DefaultBranchID(defaultUserID)
	defaultBranch := core.Branch{
		ID:                     defaultBranchID,
		UserID:                 defaultUserID,
		Name:                   core.DefaultBranchName,
		IsDefault:              true,
		TemplateSetID:          core.TemplateStandardCleanID,
		LastSyncedMainRevision: 1,
		CreatedAt:              time.Now().UTC(),
		UpdatedAt:              time.Now().UTC(),
	}
	s.branches[defaultBranchID] = &defaultBranch

	return s
}

// --- BranchRepository ---

func (s *fakeBranchStore) CreateBranch(_ context.Context, branch *core.Branch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.branches {
		if existing.UserID == branch.UserID && existing.Name == branch.Name {
			return fmt.Errorf("%w: branch with name %q already exists", core.ErrConflict, branch.Name)
		}
	}

	if branch.ID == "" {
		branch.ID = "branch-" + uuid.New().String()
	}
	cp := *branch
	s.branches[branch.ID] = &cp
	return nil
}

func (s *fakeBranchStore) GetBranch(_ context.Context, userID, id string) (*core.Branch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.branches[id]
	if !ok || (userID != "" && b.UserID != userID) {
		return nil, core.ErrNotFound
	}
	cp := *b
	var mainRev int64 = 1
	if p, ok := s.profiles[b.UserID]; ok {
		mainRev = p.MainRevision
	}
	cp.IsStale = cp.IsStaleRevision(mainRev)
	return &cp, nil
}

func (s *fakeBranchStore) ListBranches(_ context.Context, userID string) ([]core.Branch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var mainRev int64 = 1
	if p, ok := s.profiles[userID]; ok {
		mainRev = p.MainRevision
	}

	var res []core.Branch
	for _, b := range s.branches {
		if b.UserID == userID {
			cp := *b
			cp.IsStale = cp.IsStaleRevision(mainRev)
			res = append(res, cp)
		}
	}
	return res, nil
}

func (s *fakeBranchStore) UpdateBranch(_ context.Context, branch *core.Branch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.branches {
		if existing.UserID == branch.UserID && existing.Name == branch.Name && existing.ID != branch.ID {
			return fmt.Errorf("%w: branch with name %q already exists", core.ErrConflict, branch.Name)
		}
	}

	cp := *branch
	s.branches[branch.ID] = &cp
	return nil
}

func (s *fakeBranchStore) DeleteBranch(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.branches[id]
	if !ok || b.UserID != userID {
		return core.ErrNotFound
	}
	delete(s.branches, id)
	for vid, v := range s.versions {
		if v.BranchID == id {
			delete(s.versions, vid)
		}
	}
	return nil
}

func (s *fakeBranchStore) CreateVersion(_ context.Context, version *core.BranchVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	parentID := version.BranchID
	if parentID == "" {
		parentID = version.ResumeID
	}
	version.BranchID = parentID
	version.ResumeID = parentID

	if version.ID == "" {
		version.ID = "ver-" + uuid.New().String()
	}
	if version.VersionNumber <= 0 {
		maxNum := 0
		for _, v := range s.versions {
			if v.BranchID == version.BranchID && v.VersionNumber > maxNum {
				maxNum = v.VersionNumber
			}
		}
		version.VersionNumber = maxNum + 1
	}

	cp := *version
	s.versions[version.ID] = &cp
	return nil
}

func (s *fakeBranchStore) GetVersion(_ context.Context, branchID, versionID string) (*core.BranchVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.versions[versionID]
	if !ok || (branchID != "" && v.BranchID != branchID) {
		return nil, fmt.Errorf("%w: version %s", core.ErrNotFound, versionID)
	}
	cp := *v
	return &cp, nil
}

func (s *fakeBranchStore) GetVersionByNumber(_ context.Context, branchID string, versionNumber int) (*core.BranchVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, v := range s.versions {
		if v.BranchID == branchID && v.VersionNumber == versionNumber {
			cp := *v
			return &cp, nil
		}
	}
	return nil, core.ErrNotFound
}

func (s *fakeBranchStore) GetVersionsByIDs(_ context.Context, versionIDs []string) ([]core.BranchVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idMap := make(map[string]bool)
	for _, id := range versionIDs {
		idMap[id] = true
	}

	var res []core.BranchVersion
	for _, v := range s.versions {
		if idMap[v.ID] {
			res = append(res, *v)
		}
	}
	return res, nil
}

func (s *fakeBranchStore) ListVersions(_ context.Context, branchID string) ([]core.BranchVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var res []core.BranchVersion
	for _, v := range s.versions {
		if v.BranchID == branchID {
			res = append(res, *v)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].VersionNumber > res[j].VersionNumber
	})
	return res, nil
}

func (s *fakeBranchStore) UpdateVersionStatus(_ context.Context, versionID string, status core.VersionStatus, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.versions[versionID]
	if !ok {
		return core.ErrNotFound
	}
	v.Status = status
	v.ErrorMessage = errMsg
	return nil
}

func (s *fakeBranchStore) CompleteVersion(_ context.Context, version *core.BranchVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	parentID := version.BranchID
	if parentID == "" {
		parentID = version.ResumeID
	}
	version.BranchID = parentID
	version.ResumeID = parentID
	version.Status = core.VersionStatusCompleted
	now := time.Now().UTC()
	version.CompletedAt = &now
	cp := *version
	s.versions[version.ID] = &cp

	if br, ok := s.branches[parentID]; ok {
		br.ActiveVersionID = &version.ID
		br.LastSyncedMainRevision = version.MainRevisionUsed
		if version.GapReport != nil {
			cpG := *version.GapReport
			br.GapReport = &cpG
		} else {
			br.GapReport = nil
		}
		if version.ATSScore != nil {
			cpA := *version.ATSScore
			br.ATSScore = &cpA
		} else {
			br.ATSScore = nil
		}
		br.UpdatedAt = now
	}
	return nil
}

func (s *fakeBranchStore) UpdateVersionATSScore(_ context.Context, versionID string, atsScore *core.ATSScore) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.versions[versionID]
	if !ok {
		return core.ErrNotFound
	}
	v.ATSScore = atsScore

	if br, ok := s.branches[v.BranchID]; ok {
		if atsScore != nil {
			cpA := *atsScore
			br.ATSScore = &cpA
		}
	}
	return nil
}

func (s *fakeBranchStore) FailInterruptedVersions(_ context.Context, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, v := range s.versions {
		if v.Status == core.VersionStatusPending || v.Status == core.VersionStatusProcessing {
			v.Status = core.VersionStatusFailed
			v.ErrorMessage = reason
		}
	}
	return nil
}

// --- ProfileRepository ---

func (s *fakeBranchStore) GetProfile(_ context.Context, userID string) (*core.Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.profiles[userID]
	if !ok {
		return nil, core.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (s *fakeBranchStore) UpsertProfile(_ context.Context, profile *core.Profile) error {
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

func (s *fakeBranchStore) IncrementMainRevision(_ context.Context, userID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.profiles[userID]
	if !ok {
		return 0, core.ErrNotFound
	}
	p.MainRevision++
	return p.MainRevision, nil
}

func (s *fakeBranchStore) GetSnapshot(_ context.Context, userID string) (*core.MainProfileSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prof, ok := s.profiles[userID]
	if !ok {
		return nil, core.ErrNotFound
	}

	var exps []core.WorkExperience
	for _, e := range s.experiences {
		if e.UserID == userID {
			cp := *e
			var expFacts []core.Fact
			for _, f := range s.facts {
				if f.UserID == userID && f.OwnerID == e.ID {
					expFacts = append(expFacts, *f)
				}
			}
			cp.Facts = expFacts
			exps = append(exps, cp)
		}
	}

	var skills []core.Skill
	for _, sk := range s.skills {
		if sk.UserID == userID {
			skills = append(skills, *sk)
		}
	}

	var facts []core.Fact
	for _, f := range s.facts {
		if f.UserID == userID {
			facts = append(facts, *f)
		}
	}

	return &core.MainProfileSnapshot{
		Profile:      *prof,
		MainRevision: prof.MainRevision,
		Experiences:  exps,
		Skills:       skills,
		Facts:        facts,
		ExportedAt:   time.Now().UTC(),
	}, nil
}

func (s *fakeBranchStore) ReplaceCareerGraph(_ context.Context, _ string, _ *core.MainProfileSnapshot) error {
	return nil
}

func (s *fakeBranchStore) CreateExperience(_ context.Context, exp *core.WorkExperience) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if exp.ID == "" {
		exp.ID = "exp-" + uuid.New().String()
	}
	cp := *exp
	s.experiences[exp.ID] = &cp
	return nil
}

func (s *fakeBranchStore) GetExperience(_ context.Context, _, id string) (*core.WorkExperience, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.experiences[id]
	if !ok {
		return nil, core.ErrNotFound
	}
	cp := *e
	return &cp, nil
}

func (s *fakeBranchStore) UpdateExperience(_ context.Context, exp *core.WorkExperience) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp := *exp
	s.experiences[exp.ID] = &cp
	return nil
}

func (s *fakeBranchStore) DeleteExperience(_ context.Context, _, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.experiences, id)
	return nil
}

func (s *fakeBranchStore) ListExperiences(_ context.Context, userID string) ([]core.WorkExperience, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var res []core.WorkExperience
	for _, e := range s.experiences {
		if e.UserID == userID {
			res = append(res, *e)
		}
	}
	return res, nil
}

func (s *fakeBranchStore) CreateSkill(_ context.Context, skill *core.Skill) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if skill.ID == "" {
		skill.ID = "skill-" + uuid.New().String()
	}
	cp := *skill
	s.skills[skill.ID] = &cp
	return nil
}

func (s *fakeBranchStore) GetSkill(_ context.Context, _, id string) (*core.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sk, ok := s.skills[id]
	if !ok {
		return nil, core.ErrNotFound
	}
	cp := *sk
	return &cp, nil
}

func (s *fakeBranchStore) UpdateSkill(_ context.Context, skill *core.Skill) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp := *skill
	s.skills[skill.ID] = &cp
	return nil
}

func (s *fakeBranchStore) DeleteSkill(_ context.Context, _, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.skills, id)
	return nil
}

func (s *fakeBranchStore) ListSkills(_ context.Context, userID string) ([]core.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var res []core.Skill
	for _, sk := range s.skills {
		if sk.UserID == userID {
			res = append(res, *sk)
		}
	}
	return res, nil
}

func (s *fakeBranchStore) ListGlobalSkills(_ context.Context) ([]core.Skill, error) {
	return nil, nil
}

func (s *fakeBranchStore) CreateFact(_ context.Context, fact *core.Fact) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if fact.ID == "" {
		fact.ID = "fact-" + uuid.New().String()
	}
	cp := *fact
	s.facts[fact.ID] = &cp
	return nil
}

func (s *fakeBranchStore) GetFact(_ context.Context, _, id string) (*core.Fact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, ok := s.facts[id]
	if !ok {
		return nil, core.ErrNotFound
	}
	cp := *f
	return &cp, nil
}

func (s *fakeBranchStore) UpdateFact(_ context.Context, fact *core.Fact) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp := *fact
	s.facts[fact.ID] = &cp
	return nil
}

func (s *fakeBranchStore) DeleteFact(_ context.Context, _, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.facts, id)
	return nil
}

func (s *fakeBranchStore) ListFactsByOwner(_ context.Context, userID, ownerType, ownerID string) ([]core.Fact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var res []core.Fact
	for _, f := range s.facts {
		if f.UserID == userID && string(f.OwnerType) == ownerType && f.OwnerID == ownerID {
			res = append(res, *f)
		}
	}
	return res, nil
}

func (s *fakeBranchStore) ListProjects(_ context.Context, _ string) ([]core.Project, error) {
	return nil, nil
}
func (s *fakeBranchStore) GetProject(_ context.Context, _, _ string) (*core.Project, error) {
	return nil, core.ErrNotFound
}
func (s *fakeBranchStore) CreateProject(_ context.Context, _ *core.Project) error { return nil }
func (s *fakeBranchStore) UpdateProject(_ context.Context, _ *core.Project) error { return nil }
func (s *fakeBranchStore) DeleteProject(_ context.Context, _, _ string) error     { return nil }

func (s *fakeBranchStore) ListEducation(_ context.Context, _ string) ([]core.Education, error) {
	return nil, nil
}
func (s *fakeBranchStore) GetEducation(_ context.Context, _, _ string) (*core.Education, error) {
	return nil, core.ErrNotFound
}
func (s *fakeBranchStore) CreateEducation(_ context.Context, _ *core.Education) error { return nil }
func (s *fakeBranchStore) UpdateEducation(_ context.Context, _ *core.Education) error { return nil }
func (s *fakeBranchStore) DeleteEducation(_ context.Context, _, _ string) error       { return nil }

func (s *fakeBranchStore) ListCertifications(_ context.Context, _ string) ([]core.Certification, error) {
	return nil, nil
}
func (s *fakeBranchStore) GetCertification(_ context.Context, _, _ string) (*core.Certification, error) {
	return nil, core.ErrNotFound
}
func (s *fakeBranchStore) CreateCertification(_ context.Context, _ *core.Certification) error {
	return nil
}
func (s *fakeBranchStore) UpdateCertification(_ context.Context, _ *core.Certification) error {
	return nil
}
func (s *fakeBranchStore) DeleteCertification(_ context.Context, _, _ string) error { return nil }

// --- PresetRepository ---

func (s *fakeBranchStore) GetPreset(_ context.Context, userID, id string) (*core.Preset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.presets[id]
	if !ok || (p.UserID != "" && p.UserID != userID) {
		return nil, core.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (s *fakeBranchStore) CreatePreset(_ context.Context, preset *core.Preset) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if preset.ID == "" {
		preset.ID = "preset-" + uuid.New().String()
	}
	cp := *preset
	s.presets[preset.ID] = &cp
	return nil
}

func (s *fakeBranchStore) ListPresets(_ context.Context, userID string) ([]core.Preset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var res []core.Preset
	for _, p := range s.presets {
		if p.UserID == userID || p.UserID == "" {
			res = append(res, *p)
		}
	}
	return res, nil
}

func (s *fakeBranchStore) UpdatePreset(_ context.Context, preset *core.Preset) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp := *preset
	s.presets[preset.ID] = &cp
	return nil
}

func (s *fakeBranchStore) DeletePreset(_ context.Context, _, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.presets, id)
	return nil
}

// --- TemplateRepository ---

func (s *fakeBranchStore) GetTemplate(_ context.Context, userID, id string) (*core.TemplateSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.templates[id]
	if !ok || (!t.IsBuiltIn && t.UserID != "" && t.UserID != userID) {
		return nil, core.ErrNotFound
	}
	cp := *t
	return &cp, nil
}

func (s *fakeBranchStore) CreateTemplate(_ context.Context, tmpl *core.TemplateSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tmpl.ID == "" {
		tmpl.ID = "tmpl-" + uuid.New().String()
	}
	cp := *tmpl
	s.templates[tmpl.ID] = &cp
	return nil
}

func (s *fakeBranchStore) ListTemplates(_ context.Context, userID string) ([]core.TemplateSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var res []core.TemplateSet
	for _, t := range s.templates {
		if t.IsBuiltIn || t.UserID == userID {
			res = append(res, *t)
		}
	}
	return res, nil
}

func (s *fakeBranchStore) UpdateTemplate(_ context.Context, tmpl *core.TemplateSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp := *tmpl
	s.templates[tmpl.ID] = &cp
	return nil
}

func (s *fakeBranchStore) DeleteTemplate(_ context.Context, _, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.templates, id)
	return nil
}

// --- QuotaRepository ---

func (s *fakeBranchStore) GetQuota(_ context.Context, userID string) (*core.TenantQuota, error) {
	return &core.TenantQuota{
		UserID:            userID,
		DailyRequestLimit: 50,
		MonthlyTokenLimit: 1000000,
	}, nil
}

func (s *fakeBranchStore) ReserveQuota(_ context.Context, userID string, op core.LLMOperationType, estimatedTokens, _, _ int) (*core.LLMUsageRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec := &core.LLMUsageRecord{
		ID:              "res-" + uuid.New().String(),
		UserID:          userID,
		Operation:       op,
		EstimatedTokens: estimatedTokens,
		Status:          core.ReservationStatusPending,
		CreatedAt:       time.Now().UTC(),
	}
	s.usageRecords[rec.ID] = rec
	return rec, nil
}

func (s *fakeBranchStore) CommitUsage(_ context.Context, reservationID string, usage core.TokenUsage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rec, ok := s.usageRecords[reservationID]; ok {
		rec.Status = core.ReservationStatusCommitted
		rec.PromptTokens = usage.PromptTokens
		rec.CompletionTokens = usage.CompletionTokens
		rec.TotalTokens = usage.TotalTokens
	}
	return nil
}

func (s *fakeBranchStore) ReleaseQuota(_ context.Context, reservationID string, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rec, ok := s.usageRecords[reservationID]; ok {
		rec.Status = core.ReservationStatusReleased
		rec.ErrorMessage = errMsg
	}
	return nil
}

func (s *fakeBranchStore) ListUsageRecords(_ context.Context, userID string, _ int) ([]core.LLMUsageRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var res []core.LLMUsageRecord
	for _, r := range s.usageRecords {
		if r.UserID == userID {
			res = append(res, *r)
		}
	}
	return res, nil
}

// --- Fake PromptCompiler ---

type fakePromptCompiler struct{}

func (f *fakePromptCompiler) CompilePrompt(req core.GenerationRequest) (*core.PromptPreview, error) {
	session := privacy.NewSessionFromRequest(req)
	obfReq := session.ObfuscateRequest(req)

	fullName := obfReq.MainProfile.Profile.FullName
	if fullName == "" {
		fullName = privacy.PlaceholderFullName
	}

	userPrompt := fmt.Sprintf("Candidate: %s\nTarget Role: %s\nRequirements: %s",
		fullName,
		req.BranchContext.TargetRole,
		req.BranchContext.TargetRequirements,
	)
	sysPrompt := fmt.Sprintf("Rules: %s\nLanguage: %s",
		req.GlobalRules,
		req.TargetLanguage,
	)

	return &core.PromptPreview{
		SystemPrompt: sysPrompt,
		UserPrompt:   userPrompt,
	}, nil
}

var _ core.BranchRepository = (*fakeBranchStore)(nil)
var _ core.ProfileRepository = (*fakeBranchStore)(nil)
var _ core.PresetRepository = (*fakeBranchStore)(nil)
var _ core.TemplateRepository = (*fakeBranchStore)(nil)
var _ core.QuotaRepository = (*fakeBranchStore)(nil)
var _ core.PromptCompiler = (*fakePromptCompiler)(nil)
