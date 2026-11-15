package branch

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"

	"github.com/resumeee-app/resumeee-core"
)

// defaultTailoringQuotaTokens is reserved upfront when enqueueing an async resume tailoring job.
const defaultTailoringQuotaTokens = 2000

// ListVersions returns all generated and edited versions belonging to branchID.
func (s *Service) ListVersions(ctx context.Context, branchID string) ([]core.BranchVersion, error) {
	versions, err := s.branches.ListVersions(ctx, branchID)
	if err != nil {
		return nil, err
	}
	if versions == nil {
		versions = []core.BranchVersion{}
	}
	return versions, nil
}

// Version resolves a version by either sequential version number (1, 2, ...) or version UUID.
func (s *Service) Version(ctx context.Context, branchID, versionNumOrID string) (*core.BranchVersion, error) {
	if num, err := strconv.Atoi(versionNumOrID); err == nil && num > 0 {
		ver, err := s.branches.VersionByNumber(ctx, branchID, num)
		if err == nil {
			return ver, nil
		}
	}
	return s.branches.Version(ctx, branchID, versionNumOrID)
}

// GenerateVersion enqueues an asynchronous generation job or synchronously renders default branch versions.
func (s *Service) GenerateVersion(ctx context.Context, userID, branchID string) (*core.BranchVersion, error) {
	if err := requireUserID(userID); err != nil {
		return nil, err
	}
	branch, err := s.branches.Branch(ctx, userID, branchID)
	if err != nil {
		return nil, err
	}
	if branch.UserID != userID {
		return nil, core.ErrNotFound
	}
	if branch.IsDefault {
		return s.renderDefaultBranchVersion(ctx, userID, branch)
	}

	var mainRevision int64 = 1
	if s.profiles != nil {
		prof, err := s.profiles.Profile(ctx, userID)
		if err == nil && prof != nil {
			mainRevision = prof.MainRevision
		}
	}

	return s.enqueueBranchGeneration(ctx, userID, branch, mainRevision)
}

// SyncBranches regenerates specified branches or all stale branches when branchIDs is empty.
func (s *Service) SyncBranches(ctx context.Context, userID string, branchIDs []string) ([]string, error) {
	if err := requireUserID(userID); err != nil {
		return nil, err
	}
	var mainRevision int64 = 1
	if s.profiles != nil {
		prof, err := s.profiles.Profile(ctx, userID)
		if err != nil {
			return nil, err
		}
		mainRevision = prof.MainRevision
	}

	targetBranches, err := s.resolveSyncTargets(ctx, userID, branchIDs)
	if err != nil {
		return nil, err
	}

	enqueuedIDs := []string{}
	for i := range targetBranches {
		b := targetBranches[i]
		if b.IsDefault {
			ver, err := s.renderDefaultBranchVersion(ctx, userID, &b)
			if err != nil {
				return enqueuedIDs, err
			}
			enqueuedIDs = append(enqueuedIDs, ver.ID)
			continue
		}
		ver, err := s.enqueueBranchGeneration(ctx, userID, &b, mainRevision)
		if err != nil {
			return enqueuedIDs, err
		}
		enqueuedIDs = append(enqueuedIDs, ver.ID)
	}

	return enqueuedIDs, nil
}

// resolveSyncTargets loads explicit branchIDs or selects all stale branches belonging to userID.
func (s *Service) resolveSyncTargets(ctx context.Context, userID string, branchIDs []string) ([]core.Branch, error) {
	if len(branchIDs) > 0 {
		targets := make([]core.Branch, 0, len(branchIDs))
		for _, bID := range branchIDs {
			b, err := s.branches.Branch(ctx, userID, bID)
			if err != nil {
				return nil, err
			}
			if b.UserID != userID {
				return nil, core.ErrNotFound
			}
			targets = append(targets, *b)
		}
		return targets, nil
	}

	allBranches, err := s.branches.ListBranches(ctx, userID)
	if err != nil {
		return nil, err
	}
	var stale []core.Branch
	for _, b := range allBranches {
		if b.UserID == userID && b.IsStale {
			stale = append(stale, b)
		}
	}
	return stale, nil
}

// enqueueBranchGeneration reserves quota, atomically persists a pending version under admission caps,
// releases any superseded or rejected reservations, and notifies background workers.
func (s *Service) enqueueBranchGeneration(ctx context.Context, userID string, b *core.Branch, mainRevision int64) (*core.BranchVersion, error) {
	if err := requireUserID(userID); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, fmt.Errorf("%w: branch is required", core.ErrValidation)
	}
	if b.UserID != userID {
		return nil, core.ErrNotFound
	}
	if s.jobs == nil {
		return nil, fmt.Errorf("%w: job store is required", core.ErrValidation)
	}

	var reservationID string
	if s.quotas != nil {
		rec, qErr := s.quotas.ReserveQuota(ctx, userID, core.OperationResumeTailoring, defaultTailoringQuotaTokens, s.dailyRequestLimit, s.monthlyTokenLimit)
		if qErr != nil {
			return nil, qErr
		}
		if rec != nil {
			reservationID = rec.ID
		}
	}

	now := s.now()
	ttl := s.jobTTL
	if ttl <= 0 {
		ttl = core.DefaultJobTTL
	}
	maxPending := s.maxPendingPerTenant
	if maxPending <= 0 {
		maxPending = core.DefaultMaxPendingPerTenant
	}
	expiresAt := now.Add(ttl)

	jobID := "job-" + uuid.New().String()
	verID := "ver-" + uuid.New().String()
	job := &core.Job{
		ID:            jobID,
		Kind:          core.JobKindBranchTailor,
		UserID:        userID,
		Status:        core.JobStatusPending,
		ReservationID: reservationID,
		ExpiresAt:     &expiresAt,
		Tailor: &core.BranchTailorSpec{
			BranchID:  b.ID,
			VersionID: verID,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	ver := &core.BranchVersion{
		ID:                verID,
		UserID:            userID,
		BranchID:          b.ID,
		JobID:             jobID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  mainRevision,
		TemplateSetIDUsed: b.TemplateSetID,
		ProviderUsed:      b.ProviderOverride,
		ModelUsed:         b.ModelOverride,
		SourceType:        core.SourceTypeLLMGeneration,
		ParentVersionID:   b.ActiveVersionID,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	superseded, err := s.jobs.SubmitBranchTailor(ctx, job, ver, core.PendingJobOptions{
		MaxPendingPerTenant: maxPending,
		TTL:                 ttl,
		Now:                 now,
	})
	if err != nil {
		s.releaseReservationWithDetachedContext(reservationID, formatQuotaErrorMessage(err))
		return nil, err
	}
	s.releaseReservationsWithDetachedContext(superseded)

	s.notifyJob()

	return ver, nil
}

// CancelVersion cancels a queued pending branch version owned by userID and releases its reserved quota.
func (s *Service) CancelVersion(ctx context.Context, userID, branchID, versionID string) (*core.BranchVersion, error) {
	if err := requireUserID(userID); err != nil {
		return nil, err
	}
	if s.jobs == nil {
		return nil, fmt.Errorf("%w: job store is required", core.ErrValidation)
	}
	br, err := s.branches.Branch(ctx, userID, branchID)
	if err != nil {
		return nil, err
	}
	if br.UserID != userID {
		return nil, core.ErrNotFound
	}
	cancelled, cancelledJob, err := s.jobs.CancelBranchVersion(ctx, userID, branchID, versionID, "cancelled by user")
	if err != nil {
		return nil, err
	}
	if cancelledJob != nil && cancelledJob.ReservationID != "" {
		s.releaseReservationWithDetachedContext(cancelledJob.ReservationID, "cancelled by user")
	}
	return cancelled, nil
}

// EditVersion applies provenance validation to manual edits, renders templates, and appends a new immutable version.
func (s *Service) EditVersion(ctx context.Context, userID, branchID, parentVersionNumOrID string, editedContent core.TailoredContent) (*core.BranchVersion, error) {
	if err := requireUserID(userID); err != nil {
		return nil, err
	}
	branch, err := s.branches.Branch(ctx, userID, branchID)
	if err != nil {
		return nil, err
	}
	if branch.UserID != userID {
		return nil, core.ErrNotFound
	}
	if branch.IsDefault {
		return nil, fmt.Errorf("%w: default branch cannot be manually edited", core.ErrDefaultBranchImmutable)
	}

	parentVer, err := s.Version(ctx, branchID, parentVersionNumOrID)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.profiles.Snapshot(ctx, userID)
	if err != nil {
		return nil, err
	}
	prof, err := s.profiles.Profile(ctx, userID)
	if err != nil {
		return nil, err
	}
	tmpl, err := s.templates.Template(ctx, userID, branch.TemplateSetID)
	if err != nil {
		return nil, err
	}

	// Enforce "No Lies" provenance before rendering or persisting manual edits.
	if s.validator != nil {
		if err := s.validator.ValidateStrict(&editedContent, snapshot, branch); err != nil {
			return nil, fmt.Errorf("%w: %v", core.ErrProvenance, err)
		}
	}

	rendered, err := s.renderWithOptionalRenderer(ctx, tmpl, prof, &editedContent)
	if err != nil {
		return nil, fmt.Errorf("template rendering failed: %w", err)
	}

	newVer := &core.BranchVersion{
		ID:                "ver-" + uuid.New().String(),
		UserID:            branch.UserID,
		BranchID:          branch.ID,
		Status:            core.VersionStatusCompleted,
		MainRevisionUsed:  snapshot.MainRevision,
		TemplateSetIDUsed: branch.TemplateSetID,
		ProviderUsed:      "manual",
		ModelUsed:         "manual",
		SourceType:        core.SourceTypeManualEdit,
		ParentVersionID:   &parentVer.ID,
		TailoredContent:   &editedContent,
		GapReport:         parentVer.GapReport,
		HTMLOutput:        rendered.HTML,
		MarkdownOutput:    rendered.Markdown,
		PlainTextOutput:   rendered.PlainText,
		CreatedAt:         s.now(),
	}

	if err := s.branches.SaveCompletedVersion(ctx, newVer); err != nil {
		return nil, err
	}
	s.supersedePendingVersions(ctx, branchID, "superseded by manual edit")
	return newVer, nil
}

// renderDefaultBranchVersion synchronously renders a resume version for the default branch directly from the main profile.
func (s *Service) renderDefaultBranchVersion(ctx context.Context, userID string, branch *core.Branch) (*core.BranchVersion, error) {
	if s.profiles == nil {
		return nil, errors.New("profile store required for default branch rendering")
	}
	snapshot, err := s.profiles.Snapshot(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("getting profile snapshot for default branch: %w", err)
	}
	prof, err := s.profiles.Profile(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("getting profile for default branch: %w", err)
	}

	tmplID := branch.TemplateSetID
	if tmplID == "" {
		tmplID = core.TemplateStandardCleanID
		branch.TemplateSetID = tmplID
	}
	if s.templates == nil {
		return nil, errors.New("template store required for default branch rendering")
	}
	tmpl, err := s.templates.Template(ctx, userID, tmplID)
	if err != nil {
		return nil, fmt.Errorf("getting template %s for default branch: %w", tmplID, err)
	}

	tailoredContent := core.SnapshotToTailoredContent(snapshot)
	rendered, err := s.renderWithOptionalRenderer(ctx, tmpl, prof, tailoredContent)
	if err != nil {
		return nil, fmt.Errorf("rendering default branch: %w", err)
	}

	now := s.now()
	ver := &core.BranchVersion{
		ID:                "ver-" + uuid.New().String(),
		UserID:            userID,
		BranchID:          branch.ID,
		Status:            core.VersionStatusCompleted,
		MainRevisionUsed:  snapshot.MainRevision,
		TemplateSetIDUsed: branch.TemplateSetID,
		ProviderUsed:      "none",
		ModelUsed:         "none",
		SourceType:        core.SourceTypeMainProfile,
		ParentVersionID:   branch.ActiveVersionID,
		TailoredContent:   tailoredContent,
		HTMLOutput:        rendered.HTML,
		MarkdownOutput:    rendered.Markdown,
		PlainTextOutput:   rendered.PlainText,
		CreatedAt:         now,
		CompletedAt:       &now,
	}

	if err := s.branches.SaveCompletedVersion(ctx, ver); err != nil {
		return nil, fmt.Errorf("saving default branch version: %w", err)
	}

	branch.ActiveVersionID = &ver.ID
	branch.LastSyncedMainRevision = snapshot.MainRevision
	return ver, nil
}

// renderWithOptionalRenderer renders content when a renderer is configured, or returns an empty output.
func (s *Service) renderWithOptionalRenderer(ctx context.Context, tmpl *core.TemplateSet, prof *core.Profile, content *core.TailoredContent) (*core.RenderOutput, error) {
	if s.renderer == nil {
		return &core.RenderOutput{}, nil
	}
	return s.renderer.Render(ctx, tmpl, prof, content)
}
