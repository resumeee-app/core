package branch_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/branch"
	"github.com/resumeee-app/resumeee-core/sanitizer"
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

// --- BranchStore ---

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

func (s *fakeBranchStore) Branch(_ context.Context, userID, id string) (*core.Branch, error) {
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

func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
}

func cloneStringPtr(s *string) *string {
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

func cloneVersion(v *core.BranchVersion) *core.BranchVersion {
	if v == nil {
		return nil
	}
	cp := *v
	cp.StrategyIDUsed = cloneStringPtr(v.StrategyIDUsed)
	cp.ParentVersionID = cloneStringPtr(v.ParentVersionID)
	cp.ClaimedAt = cloneTimePtr(v.ClaimedAt)
	cp.LeaseExpiresAt = cloneTimePtr(v.LeaseExpiresAt)
	cp.NextRetryAt = cloneTimePtr(v.NextRetryAt)
	cp.ExpiresAt = cloneTimePtr(v.ExpiresAt)
	cp.CompletedAt = cloneTimePtr(v.CompletedAt)
	if v.ValidationWarnings != nil {
		cp.ValidationWarnings = append([]string(nil), v.ValidationWarnings...)
	}
	if v.GapReport != nil {
		gr := *v.GapReport
		cp.GapReport = &gr
	}
	if v.ATSScore != nil {
		as := *v.ATSScore
		cp.ATSScore = &as
	}
	if v.TailoredContent != nil {
		tc := *v.TailoredContent
		cp.TailoredContent = &tc
	}
	return &cp
}

func resolveFakeVersionBranchID(v *core.BranchVersion) string {
	parentID := v.BranchID
	if parentID == "" {
		parentID = v.ResumeID
	}
	v.BranchID = parentID
	v.ResumeID = parentID
	return parentID
}

func effectiveNow(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t.UTC()
}

func (s *fakeBranchStore) nextVersionNumberLocked(branchID string) int {
	maxNum := 0
	for _, v := range s.versions {
		if (v.BranchID == branchID || v.ResumeID == branchID) && v.VersionNumber > maxNum {
			maxNum = v.VersionNumber
		}
	}
	return maxNum + 1
}

func (s *fakeBranchStore) cancelVersionLocked(v *core.BranchVersion, reason string, now time.Time) {
	v.Status = core.VersionStatusCancelled
	v.ErrorMessage = reason
	v.UpdatedAt = now
	completedAt := now
	v.CompletedAt = &completedAt
	v.ClaimedBy = ""
	v.ClaimedAt = nil
	v.LeaseExpiresAt = nil
	v.NextRetryAt = nil
}

func (s *fakeBranchStore) failVersionLocked(v *core.BranchVersion, reason string, now time.Time) {
	v.Status = core.VersionStatusFailed
	v.ErrorMessage = reason
	v.UpdatedAt = now
	completedAt := now
	v.CompletedAt = &completedAt
	v.ClaimedBy = ""
	v.LeaseExpiresAt = nil
	v.NextRetryAt = nil
}

func (s *fakeBranchStore) CreateVersion(_ context.Context, version *core.BranchVersion) error {
	if version == nil {
		return fmt.Errorf("%w: version cannot be nil", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	parentID := resolveFakeVersionBranchID(version)
	if version.UserID == "" && parentID != "" {
		if br, ok := s.branches[parentID]; ok && br.UserID != "" {
			version.UserID = br.UserID
		}
	}
	if version.ID == "" {
		version.ID = "ver-" + uuid.New().String()
	}
	if version.VersionNumber <= 0 {
		version.VersionNumber = s.nextVersionNumberLocked(parentID)
	}
	if version.Status == "" {
		version.Status = core.VersionStatusPending
	}
	now := time.Now().UTC()
	if version.CreatedAt.IsZero() {
		version.CreatedAt = now
	}
	if version.UpdatedAt.IsZero() {
		version.UpdatedAt = now
	}

	s.versions[version.ID] = cloneVersion(version)
	return nil
}

func (s *fakeBranchStore) SubmitPendingVersion(_ context.Context, version *core.BranchVersion, opts core.PendingJobOptions) ([]core.ReleasedReservation, error) {
	if version == nil {
		return nil, fmt.Errorf("%w: version cannot be nil", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	parentID := resolveFakeVersionBranchID(version)
	if parentID == "" {
		return nil, fmt.Errorf("%w: branch_id is required", core.ErrValidation)
	}
	br, ok := s.branches[parentID]
	if !ok {
		return nil, core.ErrNotFound
	}
	if version.UserID == "" {
		version.UserID = br.UserID
	}
	if br.UserID != "" && version.UserID != br.UserID {
		return nil, core.ErrNotFound
	}

	for _, v := range s.versions {
		if (v.BranchID == parentID || v.ResumeID == parentID) && v.Status == core.VersionStatusProcessing {
			return nil, fmt.Errorf("%w: branch %s already has a generation in progress", core.ErrConflict, parentID)
		}
	}

	tenantPending := 0
	globalPending := 0
	for _, v := range s.versions {
		if v.Status != core.VersionStatusPending || v.BranchID == parentID {
			continue
		}
		globalPending++
		if v.UserID == version.UserID {
			tenantPending++
		}
	}
	if opts.MaxPendingPerTenant > 0 && tenantPending >= opts.MaxPendingPerTenant {
		return nil, fmt.Errorf("%w: tenant %s reached pending limit (%d)", core.ErrPendingLimitExceeded, version.UserID, opts.MaxPendingPerTenant)
	}
	if opts.MaxGlobalPending > 0 && globalPending >= opts.MaxGlobalPending {
		return nil, fmt.Errorf("%w: global pending limit reached (%d)", core.ErrPendingLimitExceeded, opts.MaxGlobalPending)
	}

	now := effectiveNow(opts.Now)
	var superseded []core.ReleasedReservation
	for _, v := range s.versions {
		if (v.BranchID == parentID || v.ResumeID == parentID) && v.Status == core.VersionStatusPending {
			s.cancelVersionLocked(v, "superseded by newer generation", now)
			if v.ReservationID != "" {
				superseded = append(superseded, core.ReleasedReservation{
					JobID:         v.ID,
					UserID:        v.UserID,
					ReservationID: v.ReservationID,
					Reason:        "superseded by newer generation",
				})
			}
		}
	}

	if version.ID == "" {
		version.ID = "ver-" + uuid.New().String()
	}
	if version.VersionNumber <= 0 {
		version.VersionNumber = s.nextVersionNumberLocked(parentID)
	}
	version.Status = core.VersionStatusPending
	if version.CreatedAt.IsZero() {
		version.CreatedAt = now
	}
	version.UpdatedAt = now
	if version.ExpiresAt == nil {
		ttl := opts.TTL
		if ttl <= 0 {
			ttl = core.DefaultJobTTL
		}
		exp := now.Add(ttl)
		version.ExpiresAt = &exp
	}

	s.versions[version.ID] = cloneVersion(version)
	return superseded, nil
}

func (s *fakeBranchStore) SupersedePendingVersions(_ context.Context, branchID, reason string) ([]core.ReleasedReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if reason == "" {
		reason = "superseded"
	}
	now := time.Now().UTC()
	var released []core.ReleasedReservation
	for _, v := range s.versions {
		if (v.BranchID == branchID || v.ResumeID == branchID) && v.Status == core.VersionStatusPending {
			s.cancelVersionLocked(v, reason, now)
			if v.ReservationID != "" {
				released = append(released, core.ReleasedReservation{
					JobID:         v.ID,
					UserID:        v.UserID,
					ReservationID: v.ReservationID,
					Reason:        reason,
				})
			}
		}
	}
	return released, nil
}

func (s *fakeBranchStore) AbortBranchVersions(_ context.Context, branchID, reason string) ([]core.ReleasedReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if reason == "" {
		reason = "branch deleted"
	}
	now := time.Now().UTC()
	var released []core.ReleasedReservation
	for _, v := range s.versions {
		if (v.BranchID == branchID || v.ResumeID == branchID) && (v.Status == core.VersionStatusPending || v.Status == core.VersionStatusProcessing) {
			s.cancelVersionLocked(v, reason, now)
			if v.ReservationID != "" {
				released = append(released, core.ReleasedReservation{
					JobID:         v.ID,
					UserID:        v.UserID,
					ReservationID: v.ReservationID,
					Reason:        reason,
				})
			}
		}
	}
	return released, nil
}

func (s *fakeBranchStore) CancelVersion(_ context.Context, userID, branchID, versionID, reason string) (*core.BranchVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if branchID != "" {
		br, ok := s.branches[branchID]
		if !ok || (userID != "" && br.UserID != userID) {
			return nil, core.ErrNotFound
		}
	}
	v, ok := s.versions[versionID]
	if !ok {
		return nil, core.ErrNotFound
	}
	if branchID != "" && v.BranchID != branchID && v.ResumeID != branchID {
		return nil, core.ErrNotFound
	}
	if userID != "" {
		if v.UserID != "" && v.UserID != userID {
			return nil, core.ErrNotFound
		}
		if br, ok := s.branches[v.BranchID]; ok && br.UserID != userID {
			return nil, core.ErrNotFound
		}
	}
	if v.Status != core.VersionStatusPending {
		return nil, fmt.Errorf("%w: version %s in status %s cannot be cancelled", core.ErrConflict, versionID, v.Status)
	}
	if reason == "" {
		reason = "cancelled by user"
	}
	s.cancelVersionLocked(v, reason, time.Now().UTC())
	return cloneVersion(v), nil
}

func (s *fakeBranchStore) ClaimNextVersion(_ context.Context, req core.ClaimJobRequest) (*core.BranchVersion, error) {
	if req.WorkerID == "" {
		return nil, fmt.Errorf("%w: worker_id is required", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := effectiveNow(req.Now)
	leaseDur := req.LeaseDuration
	if leaseDur <= 0 {
		leaseDur = core.DefaultLeaseDuration
	}

	activeByTenant := make(map[string]int)
	for _, v := range s.versions {
		if v.Status == core.VersionStatusProcessing {
			activeByTenant[v.UserID]++
		}
	}

	var candidates []*core.BranchVersion
	for _, v := range s.versions {
		if v.Status != core.VersionStatusPending {
			continue
		}
		if v.ExpiresAt != nil && !v.ExpiresAt.After(now) {
			continue
		}
		if v.NextRetryAt != nil && v.NextRetryAt.After(now) {
			continue
		}
		if req.MaxConcurrentPerTenant > 0 {
			active := activeByTenant[v.UserID] + req.OtherActiveByTenant[v.UserID]
			if active >= req.MaxConcurrentPerTenant {
				continue
			}
		}
		candidates = append(candidates, v)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		seqI := req.LastServedSeq[candidates[i].UserID]
		seqJ := req.LastServedSeq[candidates[j].UserID]
		if seqI != seqJ {
			return seqI < seqJ
		}
		if !candidates[i].CreatedAt.Equal(candidates[j].CreatedAt) {
			return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
		}
		return candidates[i].ID < candidates[j].ID
	})

	chosen := candidates[0]
	chosen.Status = core.VersionStatusProcessing
	chosen.ClaimedBy = req.WorkerID
	claimedAt := now
	chosen.ClaimedAt = &claimedAt
	leaseExp := now.Add(leaseDur)
	chosen.LeaseExpiresAt = &leaseExp
	chosen.AttemptCount++
	chosen.NextRetryAt = nil
	chosen.UpdatedAt = now
	return cloneVersion(chosen), nil
}

func (s *fakeBranchStore) RenewVersionLease(_ context.Context, versionID, claimedBy string, leaseExpiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.versions[versionID]
	if !ok {
		return core.ErrNotFound
	}
	if v.Status != core.VersionStatusProcessing || v.ClaimedBy != claimedBy {
		return fmt.Errorf("%w: version %s is not actively claimed by %s", core.ErrLeaseExpired, versionID, claimedBy)
	}
	exp := leaseExpiresAt.UTC()
	v.LeaseExpiresAt = &exp
	v.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *fakeBranchStore) RequeueVersionForRetry(_ context.Context, versionID, claimedBy string, nextRetryAt time.Time, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.versions[versionID]
	if !ok {
		return core.ErrNotFound
	}
	if v.Status != core.VersionStatusProcessing || v.ClaimedBy != claimedBy {
		return fmt.Errorf("%w: version %s is not actively claimed by %s", core.ErrLeaseExpired, versionID, claimedBy)
	}
	v.Status = core.VersionStatusPending
	v.ErrorMessage = errMsg
	nra := nextRetryAt.UTC()
	v.NextRetryAt = &nra
	v.ClaimedBy = ""
	v.ClaimedAt = nil
	v.LeaseExpiresAt = nil
	v.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *fakeBranchStore) FailClaimedVersion(_ context.Context, versionID, claimedBy, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.versions[versionID]
	if !ok {
		return core.ErrNotFound
	}
	if v.Status != core.VersionStatusProcessing || v.ClaimedBy != claimedBy {
		return fmt.Errorf("%w: version %s is not actively claimed by %s", core.ErrLeaseExpired, versionID, claimedBy)
	}
	s.failVersionLocked(v, errMsg, time.Now().UTC())
	return nil
}

func (s *fakeBranchStore) ExpirePendingVersions(_ context.Context, now time.Time, reason string) ([]core.ReleasedReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now = effectiveNow(now)
	if reason == "" {
		reason = core.ErrJobExpired.Error()
	}
	var released []core.ReleasedReservation
	for _, v := range s.versions {
		if v.Status == core.VersionStatusPending && v.ExpiresAt != nil && !v.ExpiresAt.After(now) {
			s.failVersionLocked(v, reason, now)
			if v.ReservationID != "" {
				released = append(released, core.ReleasedReservation{
					JobID:         v.ID,
					UserID:        v.UserID,
					ReservationID: v.ReservationID,
					Reason:        reason,
				})
			}
		}
	}
	return released, nil
}

func (s *fakeBranchStore) FailExpiredVersionLeases(_ context.Context, now time.Time, reason string) ([]core.ReleasedReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now = effectiveNow(now)
	if reason == "" {
		reason = core.ErrLeaseExpired.Error()
	}
	var released []core.ReleasedReservation
	for _, v := range s.versions {
		if v.Status == core.VersionStatusProcessing && v.LeaseExpiresAt != nil && !v.LeaseExpiresAt.After(now) {
			s.failVersionLocked(v, reason, now)
			if v.ReservationID != "" {
				released = append(released, core.ReleasedReservation{
					JobID:         v.ID,
					UserID:        v.UserID,
					ReservationID: v.ReservationID,
					Reason:        reason,
				})
			}
		}
	}
	return released, nil
}

func (s *fakeBranchStore) RecoverInterruptedVersions(_ context.Context, reason string) ([]core.ReleasedReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if reason == "" {
		reason = "interrupted by server restart"
	}
	var released []core.ReleasedReservation
	for _, v := range s.versions {
		expiredPending := v.Status == core.VersionStatusPending && v.ExpiresAt != nil && !v.ExpiresAt.After(now)
		if v.Status == core.VersionStatusProcessing || expiredPending {
			s.failVersionLocked(v, reason, now)
			if v.ReservationID != "" {
				released = append(released, core.ReleasedReservation{
					JobID:         v.ID,
					UserID:        v.UserID,
					ReservationID: v.ReservationID,
					Reason:        reason,
				})
			}
		}
	}
	return released, nil
}

func (s *fakeBranchStore) Version(_ context.Context, branchID, versionID string) (*core.BranchVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.versions[versionID]
	if !ok || (branchID != "" && v.BranchID != branchID) {
		return nil, fmt.Errorf("%w: version %s", core.ErrNotFound, versionID)
	}
	return cloneVersion(v), nil
}

func (s *fakeBranchStore) VersionByNumber(_ context.Context, branchID string, versionNumber int) (*core.BranchVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, v := range s.versions {
		if v.BranchID == branchID && v.VersionNumber == versionNumber {
			return cloneVersion(v), nil
		}
	}
	return nil, core.ErrNotFound
}

func (s *fakeBranchStore) VersionsByIDs(_ context.Context, versionIDs []string) ([]core.BranchVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idMap := make(map[string]bool)
	for _, id := range versionIDs {
		idMap[id] = true
	}

	var res []core.BranchVersion
	for _, v := range s.versions {
		if idMap[v.ID] {
			res = append(res, *cloneVersion(v))
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
			res = append(res, *cloneVersion(v))
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
	if v.Status.IsTerminal() {
		return fmt.Errorf("%w: version %s is already in terminal state %s", core.ErrConflict, versionID, v.Status)
	}
	now := time.Now().UTC()
	v.Status = status
	v.ErrorMessage = errMsg
	v.UpdatedAt = now
	if status.IsTerminal() {
		v.CompletedAt = &now
		v.ClaimedBy = ""
		v.LeaseExpiresAt = nil
		v.NextRetryAt = nil
	}
	return nil
}

func (s *fakeBranchStore) CompleteVersion(_ context.Context, version *core.BranchVersion) error {
	if version == nil {
		return fmt.Errorf("%w: version cannot be nil", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.versions[version.ID]
	if !ok {
		return core.ErrNotFound
	}
	if existing.Status == core.VersionStatusFailed || existing.Status == core.VersionStatusCancelled {
		return fmt.Errorf("%w: cannot complete version %s in terminal status %s", core.ErrConflict, version.ID, existing.Status)
	}
	if version.ClaimedBy != "" {
		if existing.Status != core.VersionStatusProcessing || existing.ClaimedBy != version.ClaimedBy {
			return fmt.Errorf("%w: version %s lease is held by %q (status=%s), not %q", core.ErrLeaseExpired, version.ID, existing.ClaimedBy, existing.Status, version.ClaimedBy)
		}
	} else if existing.Status == core.VersionStatusProcessing && existing.ClaimedBy != "" {
		return fmt.Errorf("%w: version %s is actively claimed by %q", core.ErrConflict, version.ID, existing.ClaimedBy)
	}

	parentID := resolveFakeVersionBranchID(version)
	br, ok := s.branches[parentID]
	if !ok {
		return core.ErrNotFound
	}

	if version.VersionNumber <= 0 {
		version.VersionNumber = existing.VersionNumber
	}
	if version.UserID == "" {
		version.UserID = existing.UserID
	}
	if !version.Status.IsCompleted() {
		version.Status = core.VersionStatusCompleted
	}
	now := time.Now().UTC()
	version.CompletedAt = &now
	version.UpdatedAt = now
	version.ClaimedBy = ""
	version.LeaseExpiresAt = nil
	version.NextRetryAt = nil
	s.versions[version.ID] = cloneVersion(version)

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

	now := time.Now().UTC()
	for _, v := range s.versions {
		if v.Status == core.VersionStatusPending || v.Status == core.VersionStatusProcessing {
			s.failVersionLocked(v, reason, now)
		}
	}
	return nil
}

// --- ProfileStore & CareerStore ---

func (s *fakeBranchStore) Profile(_ context.Context, userID string) (*core.Profile, error) {
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

func (s *fakeBranchStore) Snapshot(_ context.Context, userID string) (*core.MainProfileSnapshot, error) {
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

func (s *fakeBranchStore) Experience(_ context.Context, _, id string) (*core.WorkExperience, error) {
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

func (s *fakeBranchStore) Skill(_ context.Context, _, id string) (*core.Skill, error) {
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

func (s *fakeBranchStore) Fact(_ context.Context, _, id string) (*core.Fact, error) {
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
func (s *fakeBranchStore) Project(_ context.Context, _, _ string) (*core.Project, error) {
	return nil, core.ErrNotFound
}
func (s *fakeBranchStore) CreateProject(_ context.Context, _ *core.Project) error { return nil }
func (s *fakeBranchStore) UpdateProject(_ context.Context, _ *core.Project) error { return nil }
func (s *fakeBranchStore) DeleteProject(_ context.Context, _, _ string) error     { return nil }

func (s *fakeBranchStore) ListEducation(_ context.Context, _ string) ([]core.Education, error) {
	return nil, nil
}
func (s *fakeBranchStore) Education(_ context.Context, _, _ string) (*core.Education, error) {
	return nil, core.ErrNotFound
}
func (s *fakeBranchStore) CreateEducation(_ context.Context, _ *core.Education) error { return nil }
func (s *fakeBranchStore) UpdateEducation(_ context.Context, _ *core.Education) error { return nil }
func (s *fakeBranchStore) DeleteEducation(_ context.Context, _, _ string) error       { return nil }

func (s *fakeBranchStore) ListCertifications(_ context.Context, _ string) ([]core.Certification, error) {
	return nil, nil
}
func (s *fakeBranchStore) Certification(_ context.Context, _, _ string) (*core.Certification, error) {
	return nil, core.ErrNotFound
}
func (s *fakeBranchStore) CreateCertification(_ context.Context, _ *core.Certification) error {
	return nil
}
func (s *fakeBranchStore) UpdateCertification(_ context.Context, _ *core.Certification) error {
	return nil
}
func (s *fakeBranchStore) DeleteCertification(_ context.Context, _, _ string) error { return nil }

// --- PresetStore ---

func (s *fakeBranchStore) Preset(_ context.Context, userID, id string) (*core.Preset, error) {
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

// --- TemplateStore ---

func (s *fakeBranchStore) Template(_ context.Context, userID, id string) (*core.TemplateSet, error) {
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

// --- QuotaStore ---

func (s *fakeBranchStore) Quota(_ context.Context, userID string) (*core.TenantQuota, error) {
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
	session := sanitizer.NewSessionFromRequest(req)
	obfReq := session.ObfuscateRequest(req)

	var headline, summary string
	if obfReq.MainProfile != nil {
		headline = obfReq.MainProfile.Profile.Headline
		summary = obfReq.MainProfile.Profile.Summary
	}

	userPrompt := fmt.Sprintf("Headline: %s\nSummary: %s\nTarget Role: %s\nRequirements: %s",
		headline,
		summary,
		obfReq.BranchContext.TargetRole,
		obfReq.BranchContext.TargetRequirements,
	)
	sysPrompt := fmt.Sprintf("Rules: %s\nLanguage: %s",
		obfReq.GlobalRules,
		obfReq.TargetLanguage,
	)

	return &core.PromptPreview{
		SystemPrompt: sysPrompt,
		UserPrompt:   userPrompt,
	}, nil
}

func (f *fakePromptCompiler) CompileGapAnalysisPrompt(req core.GenerationRequest) (*core.PromptPreview, error) {
	session := sanitizer.NewSessionFromRequest(req)
	obfBranch := session.ObfuscateBranchContext(req.BranchContext)
	return &core.PromptPreview{
		SystemPrompt: fmt.Sprintf("Workflow: %s\nLanguage: %s", core.PromptWorkflowGapAnalysis, req.TargetLanguage),
		UserPrompt:   fmt.Sprintf("Target Role: %s\nRequirements: %s", obfBranch.TargetRole, obfBranch.TargetRequirements),
	}, nil
}

func (f *fakePromptCompiler) CompileATSExtractionPrompt(req core.ATSSimulationRequest) (*core.PromptPreview, error) {
	session := sanitizer.NewSessionFromSnapshot(req.MainProfile)
	return &core.PromptPreview{
		SystemPrompt: fmt.Sprintf("Workflow: %s\nFormat: %s", core.PromptWorkflowATSExtraction, req.FormatUsed),
		UserPrompt:   session.ObfuscateString(req.ResumeText),
	}, nil
}

func (f *fakePromptCompiler) CompileATSMatchingPrompt(b core.Branch, profile *core.MainProfileSnapshot, extracted *core.ATSExtractedCandidate) (*core.PromptPreview, error) {
	session := sanitizer.NewSessionFromSnapshot(profile)
	obfBranch := session.ObfuscateBranchContext(b)
	obfExtracted := session.ObfuscateExtractedCandidate(extracted)
	var summary string
	if obfExtracted != nil {
		summary = obfExtracted.Summary
	}
	return &core.PromptPreview{
		SystemPrompt: fmt.Sprintf("Workflow: %s", core.PromptWorkflowATSMatching),
		UserPrompt:   fmt.Sprintf("Target Role: %s\nRequirements: %s\nSummary: %s", obfBranch.TargetRole, obfBranch.TargetRequirements, summary),
	}, nil
}

// Compile-time interface assertions ensuring test fakes satisfy the core store and branch consumer contracts.
var (
	_ core.BranchStore      = (*fakeBranchStore)(nil)
	_ core.CareerStore      = (*fakeBranchStore)(nil)
	_ core.ProfileStore     = (*fakeBranchStore)(nil)
	_ core.PresetStore      = (*fakeBranchStore)(nil)
	_ core.TemplateStore    = (*fakeBranchStore)(nil)
	_ core.QuotaStore       = (*fakeBranchStore)(nil)
	_ branch.Store          = (*fakeBranchStore)(nil)
	_ branch.ProfileReader  = (*fakeBranchStore)(nil)
	_ branch.PresetReader   = (*fakeBranchStore)(nil)
	_ branch.TemplateReader = (*fakeBranchStore)(nil)
	_ branch.QuotaManager   = (*fakeBranchStore)(nil)
	_ branch.PromptCompiler = (*fakePromptCompiler)(nil)
)
