package memory

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// ListBranches returns all branches for userID ordered default-first, then newest-first.
func (s *Store) ListBranches(_ context.Context, userID string) ([]core.Branch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	currentMainRev := s.currentMainRevisionLocked(userID)

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

// Branch retrieves a single branch by ID, enforcing ownership when userID is non-empty.
func (s *Store) Branch(_ context.Context, userID, id string) (*core.Branch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	br, ok := s.branches[id]
	if !ok || (userID != "" && br.UserID != userID) {
		return nil, core.ErrNotFound
	}

	bCopy := clone(br)
	bCopy.IsStale = bCopy.IsStaleRevision(s.currentMainRevisionLocked(br.UserID))
	return bCopy, nil
}

// CreateBranch persists a new resume branch.
func (s *Store) CreateBranch(_ context.Context, branch *core.Branch) error {
	if branch == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.branches[branch.ID]; exists {
		return core.ErrConflict
	}
	now := s.now()
	if branch.CreatedAt.IsZero() {
		branch.CreatedAt = now
	}
	branch.UpdatedAt = now
	s.branches[branch.ID] = clone(branch)
	return nil
}

// UpdateBranch replaces a user-owned resume branch record.
func (s *Store) UpdateBranch(_ context.Context, branch *core.Branch) error {
	if branch == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.branches[branch.ID]
	if !exists || existing.UserID != branch.UserID {
		return core.ErrNotFound
	}
	if branchGenerationOrActiveChanged(existing, branch) {
		s.branchEpoch[branch.ID]++
	}
	branch.UpdatedAt = s.now()
	s.branches[branch.ID] = clone(branch)
	return nil
}

// DeleteBranch removes a non-default branch and all of its versions.
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
		if versionMatchesBranch(v, id) {
			delete(s.versions, vID)
			delete(s.versionEpoch, vID)
		}
	}
	delete(s.branches, id)
	delete(s.branchEpoch, id)
	return nil
}

// CreateVersion assigns the next monotonic version number and stores a pending version.
func (s *Store) CreateVersion(_ context.Context, version *core.BranchVersion) error {
	if version == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	targetID := resolveVersionBranchID(version)
	if version.UserID == "" && targetID != "" {
		if b, ok := s.branches[targetID]; ok && b.UserID != "" {
			version.UserID = b.UserID
		}
	}

	now := s.now()
	version.VersionNumber = s.nextVersionNumberLocked(targetID)
	if version.Status == "" {
		version.Status = core.VersionStatusPending
	}
	if version.CreatedAt.IsZero() {
		version.CreatedAt = now
	}
	if version.UpdatedAt.IsZero() {
		version.UpdatedAt = version.CreatedAt
	}

	s.versions[version.ID] = clone(version)
	if targetID != "" {
		s.versionEpoch[version.ID] = s.branchEpoch[targetID]
	}
	return nil
}

// SubmitPendingVersion atomically checks branch exclusivity and pending limits, supersedes older pending
// versions, fails expired processing leases on the branch, and inserts a new pending version.
func (s *Store) SubmitPendingVersion(_ context.Context, version *core.BranchVersion, opts core.PendingJobOptions) ([]core.ReleasedReservation, error) {
	if version == nil {
		return nil, core.ErrValidation
	}
	if version.Status != "" && version.Status != core.VersionStatusPending {
		return nil, fmt.Errorf("%w: pending version cannot start in %s state", core.ErrValidation, version.Status)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	branchID := resolveVersionBranchID(version)
	if branchID == "" {
		return nil, fmt.Errorf("%w: branch id is required", core.ErrValidation)
	}
	br, branchExists := s.branches[branchID]
	if !branchExists {
		return nil, core.ErrNotFound
	}
	if version.UserID == "" {
		version.UserID = br.UserID
	} else if br.UserID != "" && br.UserID != version.UserID {
		return nil, core.ErrNotFound
	}
	if version.UserID == "" {
		return nil, fmt.Errorf("%w: user id is required", core.ErrValidation)
	}
	if version.ID == "" {
		version.ID = core.GenerateID("ver")
	}
	if _, exists := s.versions[version.ID]; exists {
		return nil, core.ErrConflict
	}

	now := s.resolveNow(opts.Now)
	for _, existing := range s.versions {
		if versionMatchesBranch(existing, branchID) && existing.Status == core.VersionStatusProcessing && isLeaseUnexpired(existing.LeaseExpiresAt, now) {
			return nil, fmt.Errorf("%w: branch %s already has an active processing version", core.ErrConflict, branchID)
		}
	}

	if err := s.checkPendingAdmissionLocked(version.UserID, branchID, opts, now); err != nil {
		return nil, err
	}

	var released []core.ReleasedReservation
	for _, existing := range s.versions {
		if versionMatchesBranch(existing, branchID) && existing.Status == core.VersionStatusProcessing && !isLeaseUnexpired(existing.LeaseExpiresAt, now) {
			if rel, ok := s.transitionVersionTerminalLocked(existing, core.VersionStatusFailed, "worker lease expired", now, false); ok {
				released = append(released, rel)
			}
		}
	}
	released = append(released, s.supersedePendingVersionsLocked(branchID, "superseded by newer pending version", now)...)

	_, _, ttl := resolvePendingLimits(opts)
	version.VersionNumber = s.nextVersionNumberLocked(branchID)
	version.Status = core.VersionStatusPending
	if version.CreatedAt.IsZero() {
		version.CreatedAt = now
	}
	version.UpdatedAt = now
	if version.ExpiresAt == nil {
		exp := now.Add(ttl)
		version.ExpiresAt = &exp
	}
	version.ClaimedBy = ""
	version.ClaimedAt = nil
	version.LeaseExpiresAt = nil
	version.CompletedAt = nil

	s.versions[version.ID] = clone(version)
	s.versionEpoch[version.ID] = s.branchEpoch[branchID]
	return released, nil
}

// SupersedePendingVersions cancels all pending versions on branchID and returns released quota reservations.
func (s *Store) SupersedePendingVersions(_ context.Context, branchID, reason string) ([]core.ReleasedReservation, error) {
	if branchID == "" {
		return nil, fmt.Errorf("%w: branch id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.supersedePendingVersionsLocked(branchID, reason, s.now()), nil
}

// AbortBranchVersions cancels all pending and processing versions on branchID and returns released reservations.
func (s *Store) AbortBranchVersions(_ context.Context, branchID, reason string) ([]core.ReleasedReservation, error) {
	if branchID == "" {
		return nil, fmt.Errorf("%w: branch id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	var released []core.ReleasedReservation
	for _, v := range s.versions {
		if versionMatchesBranch(v, branchID) && !v.Status.IsTerminal() {
			if rel, ok := s.transitionVersionTerminalLocked(v, core.VersionStatusCancelled, reason, now, true); ok {
				released = append(released, rel)
			}
		}
	}
	return released, nil
}

// CancelVersion cancels an owned pending branch version or returns ErrNotFound/ErrConflict.
func (s *Store) CancelVersion(_ context.Context, userID, branchID, versionID, reason string) (*core.BranchVersion, error) {
	if userID == "" || versionID == "" {
		return nil, core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.versions[versionID]
	if !ok || (branchID != "" && !versionMatchesBranch(v, branchID)) || s.resolveVersionUserIDLocked(v) != userID {
		return nil, core.ErrNotFound
	}
	if v.Status != core.VersionStatusPending {
		return nil, fmt.Errorf("%w: cannot cancel version in %s state", core.ErrConflict, v.Status)
	}

	s.transitionVersionTerminalLocked(v, core.VersionStatusCancelled, reason, s.now(), true)
	copyV := clone(v)
	_ = copyV.Normalize()
	return copyV, nil
}

// ClaimNextVersion atomically claims the fairest eligible pending branch version for a worker lease.
func (s *Store) ClaimNextVersion(_ context.Context, req core.ClaimJobRequest) (*core.BranchVersion, error) {
	if req.WorkerID == "" {
		return nil, fmt.Errorf("%w: worker id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.resolveNow(req.Now)
	leaseDur := req.LeaseDuration
	if leaseDur <= 0 {
		leaseDur = core.DefaultLeaseDuration
	}

	activeByTenant, activeBranches := s.activeVersionCountsLocked(now)
	otherByTenant := s.siblingActiveCountLocked(req, false, now)

	var candidates []claimCandidate
	for _, v := range s.versions {
		if v.Status != core.VersionStatusPending || !isPendingUnexpired(v.ExpiresAt, now) || !isRetryReady(v.NextRetryAt, now) {
			continue
		}
		branchID := peekVersionBranchID(v)
		if activeBranches[branchID] {
			continue
		}
		tenantID := s.resolveVersionUserIDLocked(v)
		if req.MaxConcurrentPerTenant > 0 && activeByTenant[tenantID]+otherByTenant[tenantID] >= req.MaxConcurrentPerTenant {
			continue
		}
		candidates = append(candidates, claimCandidate{
			id:        v.ID,
			tenantID:  tenantID,
			createdAt: v.CreatedAt,
		})
	}

	winnerID, ok := selectFairestCandidate(candidates, req.LastServedSeq)
	if !ok {
		return nil, nil
	}

	v := s.versions[winnerID]
	leaseExp := now.Add(leaseDur)
	v.Status = core.VersionStatusProcessing
	v.ClaimedBy = req.WorkerID
	v.ClaimedAt = &now
	v.LeaseExpiresAt = &leaseExp
	v.NextRetryAt = nil
	v.AttemptCount++
	v.UpdatedAt = now
	if branchID := peekVersionBranchID(v); branchID != "" {
		s.versionEpoch[v.ID] = s.branchEpoch[branchID]
	}

	copyV := clone(v)
	if copyV.UserID == "" {
		copyV.UserID = s.resolveVersionUserIDLocked(v)
	}
	_ = copyV.Normalize()
	return copyV, nil
}

// RenewVersionLease extends an active processing version lease owned by claimedBy.
func (s *Store) RenewVersionLease(_ context.Context, versionID, claimedBy string, leaseExpiresAt time.Time) error {
	if versionID == "" || claimedBy == "" || leaseExpiresAt.IsZero() {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	v, err := s.claimedVersionLocked(versionID, claimedBy)
	if err != nil {
		return err
	}

	exp := leaseExpiresAt.UTC()
	v.LeaseExpiresAt = &exp
	v.UpdatedAt = s.now()
	return nil
}

// RequeueVersionForRetry returns a claimed processing version to pending for future retry prior to expiration.
func (s *Store) RequeueVersionForRetry(_ context.Context, versionID, claimedBy string, nextRetryAt time.Time, errMsg string) error {
	if versionID == "" || claimedBy == "" || nextRetryAt.IsZero() {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	v, err := s.claimedVersionLocked(versionID, claimedBy)
	if err != nil {
		return err
	}

	retryAt := nextRetryAt.UTC()
	if v.ExpiresAt != nil && !retryAt.Before(*v.ExpiresAt) {
		return core.ErrJobExpired
	}

	v.Status = core.VersionStatusPending
	v.ErrorMessage = errMsg
	v.NextRetryAt = &retryAt
	v.ClaimedBy = ""
	v.ClaimedAt = nil
	v.LeaseExpiresAt = nil
	v.CompletedAt = nil
	v.UpdatedAt = s.now()
	return nil
}

// FailClaimedVersion transitions an active claimed version owned by claimedBy to failed.
func (s *Store) FailClaimedVersion(_ context.Context, versionID, claimedBy, errMsg string) error {
	if versionID == "" || claimedBy == "" {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	v, err := s.claimedVersionLocked(versionID, claimedBy)
	if err != nil {
		return err
	}

	s.transitionVersionTerminalLocked(v, core.VersionStatusFailed, errMsg, s.now(), false)
	return nil
}

// ExpirePendingVersions fails pending versions whose ExpiresAt has passed and returns released reservations.
func (s *Store) ExpirePendingVersions(_ context.Context, now time.Time, reason string) ([]core.ReleasedReservation, error) {
	ts := s.resolveNow(now)

	s.mu.Lock()
	defer s.mu.Unlock()

	var released []core.ReleasedReservation
	for _, v := range s.versions {
		if v.Status == core.VersionStatusPending && !isPendingUnexpired(v.ExpiresAt, ts) {
			if rel, ok := s.transitionVersionTerminalLocked(v, core.VersionStatusFailed, reason, ts, true); ok {
				released = append(released, rel)
			}
		}
	}
	return released, nil
}

// FailExpiredVersionLeases fails processing versions whose worker lease has expired and returns released reservations.
func (s *Store) FailExpiredVersionLeases(_ context.Context, now time.Time, reason string) ([]core.ReleasedReservation, error) {
	ts := s.resolveNow(now)

	s.mu.Lock()
	defer s.mu.Unlock()

	var released []core.ReleasedReservation
	for _, v := range s.versions {
		if v.Status == core.VersionStatusProcessing && !isLeaseUnexpired(v.LeaseExpiresAt, ts) {
			if rel, ok := s.transitionVersionTerminalLocked(v, core.VersionStatusFailed, reason, ts, false); ok {
				released = append(released, rel)
			}
		}
	}
	return released, nil
}

// RecoverInterruptedVersions fails interrupted processing versions and expired pending versions while preserving unexpired pending work.
func (s *Store) RecoverInterruptedVersions(_ context.Context, reason string) ([]core.ReleasedReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ts := s.now()
	var released []core.ReleasedReservation
	for _, v := range s.versions {
		if v.Status == core.VersionStatusProcessing || (v.Status == core.VersionStatusPending && !isPendingUnexpired(v.ExpiresAt, ts)) {
			if rel, ok := s.transitionVersionTerminalLocked(v, core.VersionStatusFailed, reason, ts, false); ok {
				released = append(released, rel)
			}
		}
	}
	return released, nil
}

// Version fetches a branch version by versionID, optionally verifying its branchID.
func (s *Store) Version(_ context.Context, branchID, versionID string) (*core.BranchVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	v, ok := s.versions[versionID]
	if !ok {
		return nil, core.ErrNotFound
	}
	if branchID != "" && !versionMatchesBranch(v, branchID) {
		return nil, core.ErrNotFound
	}

	copyV := clone(v)
	_ = copyV.Normalize()
	return copyV, nil
}

// VersionByNumber fetches a branch version by its 1-based sequence number.
func (s *Store) VersionByNumber(_ context.Context, branchID string, versionNumber int) (*core.BranchVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, v := range s.versions {
		if versionMatchesBranch(v, branchID) && v.VersionNumber == versionNumber {
			copyV := clone(v)
			_ = copyV.Normalize()
			return copyV, nil
		}
	}
	return nil, core.ErrNotFound
}

// VersionsByIDs fetches matching branch versions in requested ID order.
func (s *Store) VersionsByIDs(_ context.Context, versionIDs []string) ([]core.BranchVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []core.BranchVersion
	for _, id := range versionIDs {
		if v, ok := s.versions[id]; ok {
			copyV := clone(v)
			_ = copyV.Normalize()
			result = append(result, *copyV)
		}
	}
	return result, nil
}

// ListVersions returns all versions belonging to branchID sorted by VersionNumber ascending.
func (s *Store) ListVersions(_ context.Context, branchID string) ([]core.BranchVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []core.BranchVersion
	for _, v := range s.versions {
		if versionMatchesBranch(v, branchID) {
			copyV := clone(v)
			_ = copyV.Normalize()
			list = append(list, *copyV)
		}
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].VersionNumber < list[j].VersionNumber
	})

	return list, nil
}

// UpdateVersionStatus updates the lifecycle status and error message on a branch version.
func (s *Store) UpdateVersionStatus(_ context.Context, versionID string, status core.VersionStatus, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.versions[versionID]
	if !ok {
		return core.ErrNotFound
	}
	if v.Status.IsTerminal() {
		return fmt.Errorf("%w: cannot transition terminal version from %s to %s", core.ErrConflict, v.Status, status)
	}

	now := s.now()
	v.Status = status
	v.ErrorMessage = errMsg
	v.UpdatedAt = now
	if status.IsTerminal() {
		v.CompletedAt = &now
		v.LeaseExpiresAt = nil
		v.NextRetryAt = nil
	}
	return nil
}

// CompleteVersion marks a version terminal, stores its payload, and promotes it to active on its branch.
func (s *Store) CompleteVersion(_ context.Context, version *core.BranchVersion) error {
	if version == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	parentID := resolveVersionBranchID(version)
	br, branchExists := s.branches[parentID]
	if !branchExists {
		return core.ErrNotFound
	}

	existing, exists := s.versions[version.ID]
	if err := s.validateVersionCompletionLocked(existing, exists, br, version); err != nil {
		return err
	}

	now := s.now()
	if exists {
		if version.VersionNumber == 0 {
			version.VersionNumber = existing.VersionNumber
		}
		if version.UserID == "" {
			version.UserID = existing.UserID
		}
		if version.CreatedAt.IsZero() {
			version.CreatedAt = existing.CreatedAt
		}
	}
	if version.UserID == "" {
		version.UserID = br.UserID
	}
	if version.CreatedAt.IsZero() {
		version.CreatedAt = now
	}
	if !version.Status.IsCompleted() {
		version.Status = core.VersionStatusCompleted
	}
	if version.CompletedAt == nil || !exists || !existing.Status.IsCompleted() {
		completedAt := now
		version.CompletedAt = &completedAt
	}
	version.UpdatedAt = now
	version.ClaimedBy = ""
	version.LeaseExpiresAt = nil
	version.NextRetryAt = nil

	s.versions[version.ID] = clone(version)
	return s.syncBranchFromVersionLocked(parentID, version, now)
}

// UpdateVersionATSScore writes an immutable initial ATS score on a version and mirrors it on the active branch.
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

	parentID := resolveVersionBranchID(v)
	s.mirrorATSScoreIfActiveLocked(parentID, versionID, v.ATSScore)
	return nil
}

// FailInterruptedVersions transitions interrupted processing and expired pending versions to failed.
func (s *Store) FailInterruptedVersions(ctx context.Context, reason string) error {
	_, err := s.RecoverInterruptedVersions(ctx, reason)
	return err
}

// currentMainRevisionLocked returns the user's MainRevision or 0 if no profile exists yet.
func (s *Store) currentMainRevisionLocked(userID string) int64 {
	if prof, ok := s.profiles[userID]; ok {
		return prof.MainRevision
	}
	return 0
}

// peekVersionBranchID returns the branch ID for v without mutating v.
func peekVersionBranchID(v *core.BranchVersion) string {
	if v.ResumeID != "" {
		return v.ResumeID
	}
	return v.BranchID
}

// resolveVersionUserIDLocked resolves the owning userID from v or its parent branch.
func (s *Store) resolveVersionUserIDLocked(v *core.BranchVersion) string {
	if v.UserID != "" {
		return v.UserID
	}
	if br, ok := s.branches[peekVersionBranchID(v)]; ok {
		return br.UserID
	}
	return ""
}

// supersedePendingVersionsLocked cancels all pending versions on branchID and returns released reservations.
func (s *Store) supersedePendingVersionsLocked(branchID, reason string, now time.Time) []core.ReleasedReservation {
	var released []core.ReleasedReservation
	for _, v := range s.versions {
		if versionMatchesBranch(v, branchID) && v.Status == core.VersionStatusPending {
			if rel, ok := s.transitionVersionTerminalLocked(v, core.VersionStatusCancelled, reason, now, true); ok {
				released = append(released, rel)
			}
		}
	}
	return released
}

// transitionVersionTerminalLocked marks v terminal and returns a ReleasedReservation when v holds a ReservationID.
func (s *Store) transitionVersionTerminalLocked(v *core.BranchVersion, status core.VersionStatus, reason string, now time.Time, clearClaim bool) (core.ReleasedReservation, bool) {
	v.Status = status
	v.ErrorMessage = reason
	v.UpdatedAt = now
	completedAt := now
	v.CompletedAt = &completedAt
	v.LeaseExpiresAt = nil
	v.NextRetryAt = nil
	if clearClaim {
		v.ClaimedBy = ""
		v.ClaimedAt = nil
	}
	if v.ReservationID == "" {
		return core.ReleasedReservation{}, false
	}
	return core.ReleasedReservation{
		JobID:         v.ID,
		UserID:        s.resolveVersionUserIDLocked(v),
		ReservationID: v.ReservationID,
		Reason:        reason,
	}, true
}

// claimedVersionLocked validates that versionID exists and is actively claimed by claimedBy.
func (s *Store) claimedVersionLocked(versionID, claimedBy string) (*core.BranchVersion, error) {
	v, ok := s.versions[versionID]
	if !ok {
		return nil, core.ErrNotFound
	}
	if v.Status != core.VersionStatusProcessing || v.ClaimedBy != claimedBy {
		return nil, fmt.Errorf("%w: version %s is not actively claimed by %q", core.ErrLeaseExpired, versionID, claimedBy)
	}
	return v, nil
}

// validateVersionCompletionLocked enforces non-resurrection, lease ownership, branch epoch, and monotonic branch promotion checks.
func (s *Store) validateVersionCompletionLocked(existing *core.BranchVersion, exists bool, br *core.Branch, incoming *core.BranchVersion) error {
	if exists && (existing.Status == core.VersionStatusFailed || existing.Status == core.VersionStatusCancelled) {
		return fmt.Errorf("%w: cannot complete terminal version %s in %s state", core.ErrConflict, incoming.ID, existing.Status)
	}
	if incoming.ClaimedBy != "" {
		if !exists || existing.Status != core.VersionStatusProcessing || existing.ClaimedBy != incoming.ClaimedBy {
			return fmt.Errorf("%w: version %s is not actively claimed by %q", core.ErrLeaseExpired, incoming.ID, incoming.ClaimedBy)
		}
	} else if exists && existing.Status == core.VersionStatusProcessing && existing.ClaimedBy != "" {
		return fmt.Errorf("%w: version %s is actively claimed by %q", core.ErrConflict, incoming.ID, existing.ClaimedBy)
	}
	if exists && existing.Status == core.VersionStatusProcessing && s.versionEpoch[incoming.ID] != s.branchEpoch[br.ID] {
		return fmt.Errorf("%w: branch configuration or active version changed while version %s was processing", core.ErrConflict, incoming.ID)
	}

	verNum := incoming.VersionNumber
	if verNum == 0 && exists {
		verNum = existing.VersionNumber
	}
	if br.ActiveVersionID != nil && *br.ActiveVersionID != "" {
		if activeVer, ok := s.versions[*br.ActiveVersionID]; ok && activeVer.VersionNumber > verNum {
			return fmt.Errorf("%w: stale version %d cannot overwrite active version %d", core.ErrConflict, verNum, activeVer.VersionNumber)
		}
	}
	return nil
}

// branchGenerationOrActiveChanged reports whether generation-defining configuration or ActiveVersionID changed between a and b.
func branchGenerationOrActiveChanged(a, b *core.Branch) bool {
	if a == nil || b == nil {
		return a != b
	}
	return !equalStringPtr(a.ActiveVersionID, b.ActiveVersionID) ||
		a.TargetRole != b.TargetRole ||
		a.TargetCompany != b.TargetCompany ||
		a.TargetRequirements != b.TargetRequirements ||
		a.CustomDirectives != b.CustomDirectives ||
		a.BranchInstructions != b.BranchInstructions ||
		!equalStringPtr(a.TailoringStrategyID, b.TailoringStrategyID) ||
		!equalStringPtr(a.PresetID, b.PresetID) ||
		a.TemplateSetID != b.TemplateSetID ||
		a.ProviderOverride != b.ProviderOverride ||
		a.ModelOverride != b.ModelOverride ||
		a.ReasoningLevel != b.ReasoningLevel ||
		!slices.Equal(a.PinnedEntityIDs, b.PinnedEntityIDs) ||
		!slices.Equal(a.ExcludedEntityIDs, b.ExcludedEntityIDs)
}

// equalStringPtr reports whether two optional string pointers hold equal values.
func equalStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// resolveVersionBranchID canonicalizes ResumeID and BranchID on version and returns the branch ID.
func resolveVersionBranchID(v *core.BranchVersion) string {
	parentID := v.ResumeID
	if parentID == "" {
		parentID = v.BranchID
	}
	v.ResumeID = parentID
	v.BranchID = parentID
	return parentID
}

// versionMatchesBranch reports whether a version belongs to branchID via either alias field.
func versionMatchesBranch(v *core.BranchVersion, branchID string) bool {
	return v.ResumeID == branchID || v.BranchID == branchID
}

// nextVersionNumberLocked computes one past the highest existing VersionNumber on branchID.
func (s *Store) nextVersionNumberLocked(branchID string) int {
	var maxVer int
	for _, v := range s.versions {
		if versionMatchesBranch(v, branchID) && v.VersionNumber > maxVer {
			maxVer = v.VersionNumber
		}
	}
	return maxVer + 1
}

// syncBranchFromVersionLocked promotes version to active on its parent branch and mirrors its reports.
func (s *Store) syncBranchFromVersionLocked(branchID string, version *core.BranchVersion, now time.Time) error {
	br, ok := s.branches[branchID]
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

// mirrorATSScoreIfActiveLocked propagates a newly computed ATS score to the parent branch when versionID is active.
func (s *Store) mirrorATSScoreIfActiveLocked(branchID, versionID string, score *core.ATSScore) {
	br, exists := s.branches[branchID]
	if !exists || br.ActiveVersionID == nil || *br.ActiveVersionID != versionID {
		return
	}
	if score != nil {
		br.ATSScore = clone(score)
	} else {
		br.ATSScore = nil
	}
	br.UpdatedAt = s.now()
}
