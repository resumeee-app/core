package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/resumeee-app/core"
)

// Branches enforces branch invariants, epoch increments, monotonic version promotion,
// and immutable output analysis updates.
type Branches struct {
	store *Store
}

// ListBranches returns all branches owned by userID ordered default-first, then newest-first,
// with computed IsStale against the tenant's current MainRevision.
func (b *Branches) ListBranches(ctx context.Context, userID string) ([]core.Branch, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}

	var list []core.Branch
	err := b.store.backend.Read(ctx, func(tx ReadTx) error {
		rev, err := currentMainRevision(ctx, tx, userID)
		if err != nil {
			return err
		}
		raw, err := tx.ListBranches(ctx, userID)
		if err != nil {
			return err
		}
		list = make([]core.Branch, 0, len(raw))
		for _, item := range raw {
			cp := cloneValue(item)
			cp.IsStale = cp.IsStaleRevision(rev)
			list = append(list, cp)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].IsDefault != list[j].IsDefault {
			return list[i].IsDefault
		}
		if !list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].CreatedAt.After(list[j].CreatedAt)
		}
		return list[i].ID < list[j].ID
	})
	return list, nil
}

// Branch retrieves a single branch by id, enforcing ownership when userID is non-empty
// and computing IsStale against the branch owner's current MainRevision.
func (b *Branches) Branch(ctx context.Context, userID, id string) (*core.Branch, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: branch id cannot be empty", core.ErrValidation)
	}

	var out *core.Branch
	err := b.store.backend.Read(ctx, func(tx ReadTx) error {
		br, err := tx.Branch(ctx, id)
		if err != nil {
			return err
		}
		if userID != "" && br.UserID != userID {
			return core.ErrNotFound
		}
		rev, err := currentMainRevision(ctx, tx, br.UserID)
		if err != nil {
			return err
		}
		cp := cloneValue(br)
		cp.IsStale = cp.IsStaleRevision(rev)
		out = cp
		return nil
	})
	return out, err
}

// CreateBranch validates and stores a new branch initialized at Epoch 1.
func (b *Branches) CreateBranch(ctx context.Context, branch *core.Branch) error {
	if branch == nil || strings.TrimSpace(branch.UserID) == "" {
		return fmt.Errorf("%w: branch user id is required", core.ErrValidation)
	}

	return b.store.backend.Write(ctx, func(tx Tx) error {
		toSave := cloneValue(branch)
		if toSave.ID == "" {
			toSave.ID = core.GenerateID("br")
		}
		if existing, err := tx.Branch(ctx, toSave.ID); err == nil && existing != nil {
			return core.ErrConflict
		} else if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}

		now := b.store.now()
		toSave.Epoch = 1
		if toSave.CreatedAt.IsZero() {
			toSave.CreatedAt = now
		}
		toSave.UpdatedAt = now
		if err := tx.PutBranch(ctx, toSave); err != nil {
			return err
		}
		*branch = *cloneValue(toSave)
		return nil
	})
}

// UpdateBranch updates a user-owned branch, protecting default branch tailoring fields and bumping
// Epoch whenever generation-defining configuration or ActiveVersionID changes.
func (b *Branches) UpdateBranch(ctx context.Context, branch *core.Branch) error {
	if branch == nil || strings.TrimSpace(branch.ID) == "" {
		return fmt.Errorf("%w: branch id is required", core.ErrValidation)
	}

	return b.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Branch(ctx, branch.ID)
		if err != nil {
			return err
		}
		if existing.UserID != branch.UserID {
			return core.ErrNotFound
		}
		if existing.IsDefault {
			if err := validateDefaultBranchUpdate(existing, branch); err != nil {
				return err
			}
		}

		toSave := cloneValue(branch)
		toSave.CreatedAt = existing.CreatedAt
		toSave.IsDefault = existing.IsDefault
		epoch := existing.Epoch
		if epoch <= 0 {
			epoch = 1
		}
		if existing.GenerationOrActiveChanged(toSave) {
			epoch++
		}
		toSave.Epoch = epoch
		toSave.UpdatedAt = b.store.now()
		if err := tx.PutBranch(ctx, toSave); err != nil {
			return err
		}
		*branch = *cloneValue(toSave)
		return nil
	})
}

// DeleteBranch removes a non-default branch along with all of its versions and job history.
func (b *Branches) DeleteBranch(ctx context.Context, userID, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: branch id cannot be empty", core.ErrValidation)
	}

	return b.store.backend.Write(ctx, func(tx Tx) error {
		existing, err := tx.Branch(ctx, id)
		if err != nil {
			return err
		}
		if existing.UserID != userID {
			return core.ErrNotFound
		}
		if existing.IsDefault {
			return fmt.Errorf("%w: cannot delete default branch", core.ErrDefaultBranchImmutable)
		}

		versions, err := tx.ListVersionsByBranch(ctx, id)
		if err != nil {
			return err
		}
		jobs, err := tx.ListJobsByBranch(ctx, id)
		if err != nil {
			return err
		}

		for _, j := range jobs {
			if err := tx.DeleteJob(ctx, j.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
				return err
			}
		}
		for _, v := range versions {
			if err := tx.DeleteVersion(ctx, v.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
				return err
			}
		}
		return tx.DeleteBranch(ctx, id)
	})
}

// Version fetches a branch version by versionID, optionally verifying its BranchID.
func (b *Branches) Version(ctx context.Context, branchID, versionID string) (*core.BranchVersion, error) {
	if strings.TrimSpace(versionID) == "" {
		return nil, fmt.Errorf("%w: version id cannot be empty", core.ErrValidation)
	}

	var out *core.BranchVersion
	err := b.store.backend.Read(ctx, func(tx ReadTx) error {
		ver, err := tx.Version(ctx, versionID)
		if err != nil {
			return err
		}
		if branchID != "" && ver.BranchID != branchID {
			return core.ErrNotFound
		}
		out = cloneValue(ver)
		return nil
	})
	return out, err
}

// VersionByNumber fetches a branch version by its 1-based sequence number.
func (b *Branches) VersionByNumber(ctx context.Context, branchID string, versionNumber int) (*core.BranchVersion, error) {
	if strings.TrimSpace(branchID) == "" || versionNumber <= 0 {
		return nil, fmt.Errorf("%w: branch id and positive version number are required", core.ErrValidation)
	}

	var out *core.BranchVersion
	err := b.store.backend.Read(ctx, func(tx ReadTx) error {
		ver, err := tx.VersionByNumber(ctx, branchID, versionNumber)
		if err != nil {
			return err
		}
		out = cloneValue(ver)
		return nil
	})
	return out, err
}

// VersionsByIDs fetches matching branch versions in the requested order.
func (b *Branches) VersionsByIDs(ctx context.Context, versionIDs []string) ([]core.BranchVersion, error) {
	if len(versionIDs) == 0 {
		return nil, nil
	}

	var out []core.BranchVersion
	err := b.store.backend.Read(ctx, func(tx ReadTx) error {
		raw, err := tx.ListVersionsByIDs(ctx, versionIDs)
		if err != nil {
			return err
		}
		byID := make(map[string]core.BranchVersion, len(raw))
		for _, v := range raw {
			byID[v.ID] = cloneValue(v)
		}
		for _, id := range versionIDs {
			if v, ok := byID[id]; ok {
				out = append(out, v)
			}
		}
		return nil
	})
	return out, err
}

// ListVersions returns all versions belonging to branchID sorted by VersionNumber ascending.
func (b *Branches) ListVersions(ctx context.Context, branchID string) ([]core.BranchVersion, error) {
	if strings.TrimSpace(branchID) == "" {
		return nil, fmt.Errorf("%w: branch id cannot be empty", core.ErrValidation)
	}

	var list []core.BranchVersion
	err := b.store.backend.Read(ctx, func(tx ReadTx) error {
		raw, err := tx.ListVersionsByBranch(ctx, branchID)
		if err != nil {
			return err
		}
		list = cloneValue(raw)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].VersionNumber != list[j].VersionNumber {
			return list[i].VersionNumber < list[j].VersionNumber
		}
		return list[i].ID < list[j].ID
	})
	return list, nil
}

// SaveCompletedVersion synchronously stores a completed branch version (e.g. manual edits,
// default-branch snapshots, or seeding) and promotes it to active on its branch.
func (b *Branches) SaveCompletedVersion(ctx context.Context, version *core.BranchVersion) error {
	if version == nil || strings.TrimSpace(version.BranchID) == "" {
		return fmt.Errorf("%w: version branch id is required", core.ErrValidation)
	}

	return b.store.backend.Write(ctx, func(tx Tx) error {
		br, err := tx.Branch(ctx, version.BranchID)
		if err != nil {
			return err
		}
		if version.UserID != "" && br.UserID != version.UserID {
			return core.ErrNotFound
		}
		var existing *core.BranchVersion
		if version.ID != "" {
			existing, err = tx.Version(ctx, version.ID)
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				return err
			}
		}
		versions, err := tx.ListVersionsByBranch(ctx, br.ID)
		if err != nil {
			return err
		}

		var activeVer *core.BranchVersion
		if br.ActiveVersionID != nil && *br.ActiveVersionID != "" {
			for i := range versions {
				if versions[i].ID == *br.ActiveVersionID {
					activeVer = &versions[i]
					break
				}
			}
		}
		branchJobs, err := tx.ListJobsByBranch(ctx, br.ID)
		if err != nil {
			return err
		}
		hasProcessingJob := false
		for _, j := range branchJobs {
			if j.Kind == core.JobKindBranchTailor && j.Tailor != nil && j.Tailor.BranchID == br.ID && j.Status == core.JobStatusProcessing {
				hasProcessingJob = true
				break
			}
		}

		if existing != nil {
			if existing.Status == core.VersionStatusFailed || existing.Status == core.VersionStatusCancelled {
				return fmt.Errorf("%w: cannot complete terminal version %s in %s state", core.ErrConflict, existing.ID, existing.Status)
			}
			if existing.Status == core.VersionStatusProcessing {
				return fmt.Errorf("%w: version %s is actively processing", core.ErrConflict, existing.ID)
			}
		}

		verNum := version.VersionNumber
		if verNum == 0 && existing != nil {
			verNum = existing.VersionNumber
		}
		if verNum == 0 {
			verNum = nextVersionNumber(versions)
		}
		if activeVer != nil && activeVer.VersionNumber > verNum {
			return fmt.Errorf("%w: stale version %d cannot overwrite active version %d", core.ErrConflict, verNum, activeVer.VersionNumber)
		}

		now := b.store.now()
		toSave := cloneValue(version)
		if toSave.ID == "" {
			toSave.ID = core.GenerateID("ver")
		}
		if toSave.UserID == "" {
			toSave.UserID = br.UserID
		}
		toSave.VersionNumber = verNum
		epoch := br.Epoch
		if epoch <= 0 {
			epoch = 1
		}
		if existing == nil && (br.ActiveVersionID != nil || hasProcessingJob) {
			// Promoting a new active version advances branch epoch to fence any stale work.
			epoch++
		} else if existing != nil && (br.ActiveVersionID == nil || *br.ActiveVersionID != existing.ID || hasProcessingJob) {
			epoch++
		}
		toSave.BranchEpoch = epoch
		if !toSave.Status.IsCompleted() {
			toSave.Status = core.VersionStatusCompleted
		}
		if existing != nil && !existing.CreatedAt.IsZero() && toSave.CreatedAt.IsZero() {
			toSave.CreatedAt = existing.CreatedAt
		}
		if toSave.CreatedAt.IsZero() {
			toSave.CreatedAt = now
		}
		if toSave.CompletedAt == nil || existing == nil || !existing.Status.IsCompleted() {
			completedAt := now
			toSave.CompletedAt = &completedAt
		}
		toSave.UpdatedAt = now

		if err := tx.PutVersion(ctx, toSave); err != nil {
			return err
		}

		nextBranch := cloneValue(br)
		nextBranch.Epoch = epoch
		syncBranchFromVersion(nextBranch, toSave, now)
		if err := tx.PutBranch(ctx, nextBranch); err != nil {
			return err
		}
		*version = *cloneValue(toSave)
		return nil
	})
}

// SetVersionGapReport stores an updated GapReport on versionID and mirrors it onto the branch when active.
func (b *Branches) SetVersionGapReport(ctx context.Context, versionID string, report *core.GapReport) error {
	if strings.TrimSpace(versionID) == "" {
		return fmt.Errorf("%w: version id cannot be empty", core.ErrValidation)
	}

	return b.store.backend.Write(ctx, func(tx Tx) error {
		ver, err := tx.Version(ctx, versionID)
		if err != nil {
			return err
		}
		br, err := tx.Branch(ctx, ver.BranchID)
		if err != nil {
			return err
		}

		now := b.store.now()
		ver.GapReport = cloneValue(report)
		ver.UpdatedAt = now
		if err := tx.PutVersion(ctx, ver); err != nil {
			return err
		}
		if br.ActiveVersionID != nil && *br.ActiveVersionID == ver.ID {
			br.GapReport = cloneValue(report)
			br.UpdatedAt = now
			return tx.PutBranch(ctx, br)
		}
		return nil
	})
}

// SetVersionATSScore writes an immutable initial ATS score on versionID and mirrors it onto the active branch.
func (b *Branches) SetVersionATSScore(ctx context.Context, versionID string, atsScore *core.ATSScore) error {
	if strings.TrimSpace(versionID) == "" {
		return fmt.Errorf("%w: version id cannot be empty", core.ErrValidation)
	}

	return b.store.backend.Write(ctx, func(tx Tx) error {
		ver, err := tx.Version(ctx, versionID)
		if err != nil {
			return err
		}
		if atsScore != nil && ver.ATSScore != nil {
			return fmt.Errorf("%w: ATS score already exists for version %s", core.ErrConflict, versionID)
		}
		br, err := tx.Branch(ctx, ver.BranchID)
		if err != nil {
			return err
		}

		var normalized *core.ATSScore
		if atsScore != nil {
			cp := cloneValue(atsScore)
			cp.Normalize()
			normalized = cp
		}

		now := b.store.now()
		ver.ATSScore = normalized
		ver.UpdatedAt = now
		if err := tx.PutVersion(ctx, ver); err != nil {
			return err
		}
		if br.ActiveVersionID != nil && *br.ActiveVersionID == ver.ID {
			br.ATSScore = cloneValue(normalized)
			br.UpdatedAt = now
			return tx.PutBranch(ctx, br)
		}
		return nil
	})
}

// currentMainRevision looks up userID's MainRevision or returns 0 when no profile exists yet.
func currentMainRevision(ctx context.Context, tx ReadTx, userID string) (int64, error) {
	prof, err := tx.Profile(ctx, userID)
	if errors.Is(err, core.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return prof.MainRevision, nil
}

// validateDefaultBranchUpdate protects the default branch against tailoring or rename mutations.
func validateDefaultBranchUpdate(existing, incoming *core.Branch) error {
	if incoming.Name != existing.Name {
		return fmt.Errorf("%w: default branch name cannot be altered", core.ErrDefaultBranchImmutable)
	}
	if incoming.TargetRole != existing.TargetRole ||
		incoming.TargetCompany != existing.TargetCompany ||
		incoming.TargetRequirements != existing.TargetRequirements ||
		incoming.BranchInstructions != existing.BranchInstructions ||
		!equalStrPtr(incoming.PresetID, existing.PresetID) ||
		incoming.ProviderOverride != existing.ProviderOverride ||
		incoming.ModelOverride != existing.ModelOverride ||
		incoming.ReasoningLevel != existing.ReasoningLevel ||
		!slices.Equal(incoming.PinnedEntityIDs, existing.PinnedEntityIDs) ||
		!slices.Equal(incoming.ExcludedEntityIDs, existing.ExcludedEntityIDs) {
		return fmt.Errorf("%w: default branch cannot be tailored with custom roles, presets, or instructions", core.ErrDefaultBranchImmutable)
	}
	return nil
}

// equalStrPtr reports whether two optional string pointers are equal by value.
func equalStrPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// nextVersionNumber computes one past the highest existing VersionNumber in versions.
func nextVersionNumber(versions []core.BranchVersion) int {
	maxVer := 0
	for _, v := range versions {
		if v.VersionNumber > maxVer {
			maxVer = v.VersionNumber
		}
	}
	return maxVer + 1
}

// syncBranchFromVersion promotes version to active on br and mirrors its analysis reports.
func syncBranchFromVersion(br *core.Branch, version *core.BranchVersion, now time.Time) {
	verID := version.ID
	br.ActiveVersionID = &verID
	br.LastSyncedMainRevision = version.MainRevisionUsed
	if version.GapReport != nil {
		br.GapReport = cloneValue(version.GapReport)
	} else {
		br.GapReport = nil
	}
	if version.ATSScore != nil {
		br.ATSScore = cloneValue(version.ATSScore)
	} else {
		br.ATSScore = nil
	}
	br.UpdatedAt = now
}
