package memory

import (
	"context"
	"fmt"
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
	now := time.Now().UTC()
	if branch.CreatedAt.IsZero() {
		branch.CreatedAt = now
	}
	branch.UpdatedAt = now
	s.branches[branch.ID] = clone(branch)
	return nil
}

// UpdateBranch replaces an existing resume branch record.
func (s *Store) UpdateBranch(_ context.Context, branch *core.Branch) error {
	if branch == nil {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.branches[branch.ID]; !exists {
		return core.ErrNotFound
	}
	branch.UpdatedAt = time.Now().UTC()
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
		}
	}
	delete(s.branches, id)
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

	version.VersionNumber = s.nextVersionNumberLocked(targetID)
	if version.Status == "" {
		version.Status = core.VersionStatusPending
	}
	if version.CreatedAt.IsZero() {
		version.CreatedAt = time.Now().UTC()
	}

	s.versions[version.ID] = clone(version)
	return nil
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
	syncVersionIDs(copyV)
	return copyV, nil
}

// VersionByNumber fetches a branch version by its 1-based sequence number.
func (s *Store) VersionByNumber(_ context.Context, branchID string, versionNumber int) (*core.BranchVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, v := range s.versions {
		if versionMatchesBranch(v, branchID) && v.VersionNumber == versionNumber {
			copyV := clone(v)
			syncVersionIDs(copyV)
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
			syncVersionIDs(copyV)
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
			syncVersionIDs(copyV)
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
	v.Status = status
	v.ErrorMessage = errMsg
	if status == core.VersionStatusCompleted || status == core.VersionStatusCompletedWithWarnings || status == core.VersionStatusFailed {
		now := time.Now().UTC()
		v.CompletedAt = &now
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
	now := time.Now().UTC()
	version.CompletedAt = &now
	if version.Status == "" {
		version.Status = core.VersionStatusCompleted
	}

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

// FailInterruptedVersions transitions in-flight versions to failed after an ungraceful shutdown.
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

// currentMainRevisionLocked returns the user's MainRevision or 0 if no profile exists yet.
func (s *Store) currentMainRevisionLocked(userID string) int64 {
	if prof, ok := s.profiles[userID]; ok {
		return prof.MainRevision
	}
	return 0
}

// syncVersionIDs mirrors non-empty ResumeID/BranchID aliases on a copied BranchVersion.
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
	br.UpdatedAt = time.Now().UTC()
}
