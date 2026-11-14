package branch

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

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
	branch, err := s.branches.Branch(ctx, userID, branchID)
	if err != nil {
		return nil, err
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
		if b.IsStale {
			stale = append(stale, b)
		}
	}
	return stale, nil
}

// enqueueBranchGeneration reserves quota, persists a pending version, and submits its worker job.
func (s *Service) enqueueBranchGeneration(ctx context.Context, userID string, b *core.Branch, mainRevision int64) (*core.BranchVersion, error) {
	var reservationID string
	if s.quotas != nil {
		rec, qErr := s.quotas.ReserveQuota(ctx, userID, core.OperationResumeTailoring, defaultTailoringQuotaTokens, s.dailyRequestLimit, s.monthlyTokenLimit)
		if qErr != nil {
			return nil, qErr
		}
		reservationID = rec.ID
	}

	ver := &core.BranchVersion{
		ID:                "ver-" + uuid.New().String(),
		UserID:            userID,
		BranchID:          b.ID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  mainRevision,
		TemplateSetIDUsed: b.TemplateSetID,
		ProviderUsed:      b.ProviderOverride,
		ModelUsed:         b.ModelOverride,
		SourceType:        core.SourceTypeLLMGeneration,
		ParentVersionID:   b.ActiveVersionID,
	}

	if err := s.branches.CreateVersion(ctx, ver); err != nil {
		if reservationID != "" && s.quotas != nil {
			_ = s.quotas.ReleaseQuota(ctx, reservationID, err.Error())
		}
		return nil, err
	}

	if s.jobEnqueuer != nil {
		job := core.GenerationJob{
			UserID:        userID,
			BranchID:      b.ID,
			VersionID:     ver.ID,
			ReservationID: reservationID,
		}
		if err := s.jobEnqueuer.Enqueue(job); err != nil {
			if reservationID != "" && s.quotas != nil {
				_ = s.quotas.ReleaseQuota(ctx, reservationID, "failed to enqueue generation job")
			}
			if errors.Is(err, core.ErrQueueFull) {
				_ = s.branches.UpdateVersionStatus(ctx, ver.ID, core.VersionStatusFailed, "generation queue full")
				return nil, core.ErrQueueFull
			}
			return nil, err
		}
	}

	return ver, nil
}

// EditVersion applies provenance validation to manual edits, renders templates, and appends a new immutable version.
func (s *Service) EditVersion(ctx context.Context, userID, branchID, parentVersionNumOrID string, editedContent core.TailoredContent) (*core.BranchVersion, error) {
	branch, err := s.branches.Branch(ctx, userID, branchID)
	if err != nil {
		return nil, err
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
		CreatedAt:         time.Now().UTC(),
	}

	if err := s.branches.CreateVersion(ctx, newVer); err != nil {
		return nil, err
	}
	if err := s.branches.CompleteVersion(ctx, newVer); err != nil {
		return nil, err
	}
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

	now := time.Now().UTC()
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

	if err := s.branches.CreateVersion(ctx, ver); err != nil {
		return nil, fmt.Errorf("creating default branch version: %w", err)
	}
	if err := s.branches.CompleteVersion(ctx, ver); err != nil {
		return nil, fmt.Errorf("completing default branch version: %w", err)
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
