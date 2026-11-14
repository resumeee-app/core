// Package memory provides an in-memory, thread-safe implementation of Resumeee persistence stores.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/branch"
	"github.com/resumeee-app/resumeee-core/profile"
	"github.com/resumeee-app/resumeee-core/renderer"
	"github.com/resumeee-app/resumeee-core/seed"
	"github.com/resumeee-app/resumeee-core/worker"
)

// Compile-time assertions verify Store satisfies root, seed, and domain consumer contracts.
var (
	_ core.Store              = (*Store)(nil)
	_ core.ProfileStore       = (*Store)(nil)
	_ core.CareerStore        = (*Store)(nil)
	_ core.BranchStore        = (*Store)(nil)
	_ core.PresetStore        = (*Store)(nil)
	_ core.TemplateStore      = (*Store)(nil)
	_ core.PDFJobStore        = (*Store)(nil)
	_ core.QuotaStore         = (*Store)(nil)
	_ core.UserStore          = (*Store)(nil)
	_ seed.ProfileSeeder      = (*Store)(nil)
	_ seed.SkillSeeder        = (*Store)(nil)
	_ seed.BranchSeeder       = (*Store)(nil)
	_ seed.TemplateSeeder     = (*Store)(nil)
	_ seed.PresetSeeder       = (*Store)(nil)
	_ seed.UserSeeder         = (*Store)(nil)
	_ branch.Store            = (*Store)(nil)
	_ branch.ProfileReader    = (*Store)(nil)
	_ branch.PresetReader     = (*Store)(nil)
	_ branch.TemplateReader   = (*Store)(nil)
	_ branch.QuotaManager     = (*Store)(nil)
	_ profile.Store           = (*Store)(nil)
	_ profile.SkillCatalog    = (*Store)(nil)
	_ profile.BranchReader    = (*Store)(nil)
	_ renderer.ProfileReader  = (*Store)(nil)
	_ renderer.TemplateReader = (*Store)(nil)
	_ worker.BranchStore      = (*Store)(nil)
	_ worker.ProfileReader    = (*Store)(nil)
	_ worker.PresetReader     = (*Store)(nil)
	_ worker.TemplateReader   = (*Store)(nil)
	_ worker.PDFJobStore      = (*Store)(nil)
	_ worker.QuotaCommitter   = (*Store)(nil)
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

// Store implements domain store interfaces and core.Store in memory.
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
	if err := seed.DefaultData(ctx, s, targetUserID); err != nil {
		return nil, fmt.Errorf("seeding in-memory store: %w", err)
	}
	return s, nil
}

// Profiles returns this store as core.ProfileStore.
func (s *Store) Profiles() core.ProfileStore { return s }

// Career returns this store as core.CareerStore.
func (s *Store) Career() core.CareerStore { return s }

// Branches returns this store as core.BranchStore.
func (s *Store) Branches() core.BranchStore { return s }

// Presets returns this store as core.PresetStore.
func (s *Store) Presets() core.PresetStore { return s }

// Templates returns this store as core.TemplateStore.
func (s *Store) Templates() core.TemplateStore { return s }

// PDFJobs returns this store as core.PDFJobStore.
func (s *Store) PDFJobs() core.PDFJobStore { return s }

// Quotas returns this store as core.QuotaStore.
func (s *Store) Quotas() core.QuotaStore { return s }

// Users returns this store as core.UserStore.
func (s *Store) Users() core.UserStore { return s }

// Close releases any allocated store resources.
func (s *Store) Close() error { return nil }

// StoreBundle converts the in-memory store into a core.StoreBundle.
func (s *Store) StoreBundle() *core.StoreBundle {
	return &core.StoreBundle{
		ProfileStore:  s,
		CareerStore:   s,
		BranchStore:   s,
		PresetStore:   s,
		TemplateStore: s,
		PDFJobStore:   s,
		QuotaStore:    s,
		UserStore:     s,
		Closer:        s.Close,
	}
}

// incrementRevisionLocked increments the user's MainRevision, creating a placeholder profile if absent.
func (s *Store) incrementRevisionLocked(userID string) int64 {
	now := time.Now().UTC()
	prof, ok := s.profiles[userID]
	if !ok {
		prof = &core.Profile{
			ID:           "profile-" + userID,
			UserID:       userID,
			FullName:     userID,
			MainRevision: 1,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		s.profiles[userID] = prof
		return prof.MainRevision
	}
	prof.MainRevision++
	prof.UpdatedAt = now
	return prof.MainRevision
}
