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
	mu    sync.RWMutex
	clock func() time.Time

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
	return NewWithClock(nil)
}

// NewWithClock constructs an empty, thread-safe in-memory store using clock for current UTC timestamps.
func NewWithClock(clock func() time.Time) *Store {
	return &Store{
		clock:          clock,
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
	now := s.now()
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

// now returns the current UTC timestamp using the configured clock or time.Now.
func (s *Store) now() time.Time {
	if s.clock != nil {
		return s.clock().UTC()
	}
	return time.Now().UTC()
}

// resolveNow returns explicit in UTC when non-zero, otherwise falling back to s.now().
func (s *Store) resolveNow(explicit time.Time) time.Time {
	if !explicit.IsZero() {
		return explicit.UTC()
	}
	return s.now()
}

// claimCandidate pairs a schedulable pending job ID with its tenant and creation timestamp for fair selection.
type claimCandidate struct {
	id        string
	tenantID  string
	createdAt time.Time
}

// selectFairestCandidate picks the winning candidate ordered by LastServedSeq LRU, oldest CreatedAt, then ID.
func selectFairestCandidate(candidates []claimCandidate, lastServedSeq map[string]uint64) (string, bool) {
	if len(candidates) == 0 {
		return "", false
	}
	best := candidates[0]
	bestSeq := lastServedSeq[best.tenantID]
	for _, c := range candidates[1:] {
		cSeq := lastServedSeq[c.tenantID]
		if candidatePrecedes(c, cSeq, best, bestSeq) {
			best = c
			bestSeq = cSeq
		}
	}
	return best.id, true
}

// candidatePrecedes reports whether a should be scheduled strictly before b.
func candidatePrecedes(a claimCandidate, aSeq uint64, b claimCandidate, bSeq uint64) bool {
	if aSeq != bSeq {
		return aSeq < bSeq
	}
	if !a.createdAt.Equal(b.createdAt) {
		return a.createdAt.Before(b.createdAt)
	}
	return a.id < b.id
}

// isPendingUnexpired reports whether a pending job has no expiration deadline or expires strictly after now.
func isPendingUnexpired(expiresAt *time.Time, now time.Time) bool {
	return expiresAt == nil || expiresAt.After(now)
}

// isLeaseUnexpired reports whether a claimed processing lease has no expiry or expires strictly after now.
func isLeaseUnexpired(leaseExpiresAt *time.Time, now time.Time) bool {
	return leaseExpiresAt == nil || leaseExpiresAt.After(now)
}

// isRetryReady reports whether a pending job has no retry backoff or its NextRetryAt is at or before now.
func isRetryReady(nextRetryAt *time.Time, now time.Time) bool {
	return nextRetryAt == nil || !nextRetryAt.After(now)
}

// resolvePendingLimits extracts effective per-tenant cap, global cap, and TTL from opts.
func resolvePendingLimits(opts core.PendingJobOptions) (int, int, time.Duration) {
	maxPerTenant := opts.MaxPendingPerTenant
	if maxPerTenant <= 0 {
		maxPerTenant = core.DefaultMaxPendingPerTenant
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = core.DefaultJobTTL
	}
	return maxPerTenant, opts.MaxGlobalPending, ttl
}

// checkPendingAdmissionLocked verifies MaxPendingPerTenant and MaxGlobalPending across combined versions and PDF jobs.
func (s *Store) checkPendingAdmissionLocked(userID, excludeBranchID string, opts core.PendingJobOptions, now time.Time) error {
	var tenantCount int
	var globalCount int

	for _, v := range s.versions {
		if v.Status != core.VersionStatusPending || !isPendingUnexpired(v.ExpiresAt, now) {
			continue
		}
		if excludeBranchID != "" && versionMatchesBranch(v, excludeBranchID) {
			continue
		}
		globalCount++
		if s.resolveVersionUserIDLocked(v) == userID {
			tenantCount++
		}
	}

	for _, job := range s.pdfJobs {
		if job.Status != core.PDFParseJobStatusPending || !isPendingUnexpired(job.ExpiresAt, now) {
			continue
		}
		globalCount++
		if job.UserID == userID {
			tenantCount++
		}
	}

	maxPerTenant, maxGlobal, _ := resolvePendingLimits(opts)
	if tenantCount+1 > maxPerTenant {
		return core.ErrPendingLimitExceeded
	}
	if maxGlobal > 0 && globalCount+1 > maxGlobal {
		return core.ErrPendingLimitExceeded
	}
	return nil
}

// activeVersionCountsLocked computes per-tenant and per-branch active unexpired processing version counts at now.
func (s *Store) activeVersionCountsLocked(now time.Time) (map[string]int, map[string]bool) {
	byTenant := make(map[string]int)
	byBranch := make(map[string]bool)
	for _, v := range s.versions {
		if v.Status == core.VersionStatusProcessing && isLeaseUnexpired(v.LeaseExpiresAt, now) {
			tenantID := s.resolveVersionUserIDLocked(v)
			byTenant[tenantID]++
			if branchID := peekVersionBranchID(v); branchID != "" {
				byBranch[branchID] = true
			}
		}
	}
	return byTenant, byBranch
}

// activePDFJobCountsLocked computes per-tenant active unexpired processing PDF job counts at now.
func (s *Store) activePDFJobCountsLocked(now time.Time) map[string]int {
	byTenant := make(map[string]int)
	for _, job := range s.pdfJobs {
		if job.Status == core.PDFParseJobStatusProcessing && isLeaseUnexpired(job.LeaseExpiresAt, now) {
			byTenant[job.UserID]++
		}
	}
	return byTenant
}

// siblingActiveCountLocked returns active tenant concurrency counts from req.OtherActiveByTenant or the sibling queue.
func (s *Store) siblingActiveCountLocked(req core.ClaimJobRequest, forPDF bool, now time.Time) map[string]int {
	if req.OtherActiveByTenant != nil {
		return req.OtherActiveByTenant
	}
	if forPDF {
		byTenant, _ := s.activeVersionCountsLocked(now)
		return byTenant
	}
	return s.activePDFJobCountsLocked(now)
}
