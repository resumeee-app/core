package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// Jobs manages the unified persistent worker queue, leases, fairness, admission limits,
// branch epoch fencing, and recovery across all asynchronous job kinds.
type Jobs struct {
	store *Store
}

// Job retrieves a queued background job owned by userID.
func (j *Jobs) Job(ctx context.Context, userID, jobID string) (*core.Job, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(jobID) == "" {
		return nil, fmt.Errorf("%w: user id and job id cannot be empty", core.ErrValidation)
	}
	var out *core.Job
	err := j.store.backend.Read(ctx, func(tx ReadTx) error {
		job, err := tx.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
		if job.UserID != userID {
			return core.ErrNotFound
		}
		out = cloneValue(job)
		return nil
	})
	return out, err
}

// SubmitBranchTailor atomically creates a pending BranchVersion and queued branch_tailor Job,
// failing any expired active lease on the branch, superseding prior pending jobs on the branch,
// enforcing single-active-per-branch exclusivity and tenant/global pending caps.
func (j *Jobs) SubmitBranchTailor(
	ctx context.Context,
	job *core.Job,
	version *core.BranchVersion,
	opts core.PendingJobOptions,
) ([]core.ReleasedReservation, error) {
	if job == nil || version == nil {
		return nil, fmt.Errorf("%w: job and version cannot be nil", core.ErrValidation)
	}
	if job.Status != "" && job.Status != core.JobStatusPending {
		return nil, fmt.Errorf("%w: pending job cannot start in %s state", core.ErrValidation, job.Status)
	}
	if version.Status != "" && version.Status != core.VersionStatusPending {
		return nil, fmt.Errorf("%w: pending version cannot start in %s state", core.ErrValidation, version.Status)
	}
	branchID := resolveTailorBranchID(job, version)
	if branchID == "" {
		return nil, fmt.Errorf("%w: branch id is required", core.ErrValidation)
	}

	var released []core.ReleasedReservation
	err := j.store.backend.Write(ctx, func(tx Tx) error {
		released = nil
		// Execute all reads before writing.
		br, err := tx.GetBranch(ctx, branchID)
		if err != nil {
			return err
		}
		userID := job.UserID
		if userID == "" {
			userID = version.UserID
		}
		if userID == "" {
			userID = br.UserID
		} else if br.UserID != "" && br.UserID != userID {
			return core.ErrNotFound
		}
		if userID == "" {
			return fmt.Errorf("%w: user id is required", core.ErrValidation)
		}

		activeJobs, err := tx.ListActiveJobs(ctx)
		if err != nil {
			return err
		}
		versions, err := tx.ListVersionsByBranch(ctx, branchID)
		if err != nil {
			return err
		}
		versionMap := make(map[string]*core.BranchVersion, len(versions))
		for i := range versions {
			versionMap[versions[i].ID] = &versions[i]
		}

		nextJob := cloneValue(job)
		if nextJob.ID == "" {
			nextJob.ID = core.GenerateID("job")
		}
		if _, exists := findJobInSlice(activeJobs, nextJob.ID); exists {
			return core.ErrConflict
		}
		if existing, getErr := tx.GetJob(ctx, nextJob.ID); getErr == nil && existing != nil {
			return core.ErrConflict
		} else if getErr != nil && !errors.Is(getErr, core.ErrNotFound) {
			return getErr
		}

		nextVer := cloneValue(version)
		if nextVer.ID == "" {
			nextVer.ID = core.GenerateID("ver")
		}
		if _, exists := versionMap[nextVer.ID]; exists {
			return core.ErrConflict
		}
		if existing, getErr := tx.GetVersion(ctx, nextVer.ID); getErr == nil && existing != nil {
			return core.ErrConflict
		} else if getErr != nil && !errors.Is(getErr, core.ErrNotFound) {
			return getErr
		}

		now := j.store.resolveNow(opts.Now)
		for _, aj := range activeJobs {
			if isJobForBranch(&aj, branchID) && aj.Status == core.JobStatusProcessing && isLeaseUnexpired(aj.LeaseExpiresAt, now) {
				return fmt.Errorf("%w: branch %s already has an active processing job", core.ErrConflict, branchID)
			}
		}
		if err := checkPendingAdmission(activeJobs, userID, branchID, opts, now); err != nil {
			return err
		}

		// Transition expired processing jobs and prior pending jobs on branchID, synced with linked versions.
		var modifiedVersions []*core.BranchVersion
		var modifiedJobs []*core.Job
		for i := range activeJobs {
			aj := &activeJobs[i]
			if !isJobForBranch(aj, branchID) {
				continue
			}
			var linkedVer *core.BranchVersion
			if aj.Tailor != nil {
				linkedVer = versionMap[aj.Tailor.VersionID]
			}
			if aj.Status == core.JobStatusProcessing && !isLeaseUnexpired(aj.LeaseExpiresAt, now) {
				cp := cloneValue(*aj)
				rel := failJobAndVersion(&cp, linkedVer, core.JobStatusFailed, core.VersionStatusFailed, "worker lease expired", now, false)
				if rel != nil {
					released = append(released, *rel)
				}
				modifiedJobs = append(modifiedJobs, &cp)
				if linkedVer != nil {
					modifiedVersions = append(modifiedVersions, linkedVer)
				}
			} else if aj.Status == core.JobStatusPending {
				cp := cloneValue(*aj)
				rel := failJobAndVersion(&cp, linkedVer, core.JobStatusCancelled, core.VersionStatusCancelled, "superseded by newer pending version", now, true)
				if rel != nil {
					released = append(released, *rel)
				}
				modifiedJobs = append(modifiedJobs, &cp)
				if linkedVer != nil {
					modifiedVersions = append(modifiedVersions, linkedVer)
				}
			}
		}

		_, _, ttl := resolvePendingLimits(opts)
		epoch := br.Epoch
		if epoch <= 0 {
			epoch = 1
		}

		nextVer.UserID = userID
		nextVer.BranchID = branchID
		nextVer.JobID = nextJob.ID
		nextVer.VersionNumber = nextVersionNumber(versions)
		nextVer.BranchEpoch = epoch
		nextVer.Status = core.VersionStatusPending
		if nextVer.CreatedAt.IsZero() {
			nextVer.CreatedAt = now
		}
		nextVer.UpdatedAt = now
		nextVer.CompletedAt = nil

		nextJob.Kind = core.JobKindBranchTailor
		nextJob.UserID = userID
		nextJob.Status = core.JobStatusPending
		nextJob.Tailor = &core.BranchTailorSpec{
			BranchID:    branchID,
			VersionID:   nextVer.ID,
			BranchEpoch: epoch,
		}
		nextJob.PDF = nil
		if nextJob.CreatedAt.IsZero() {
			nextJob.CreatedAt = now
		}
		nextJob.UpdatedAt = now
		if nextJob.ExpiresAt == nil {
			exp := now.Add(ttl)
			nextJob.ExpiresAt = &exp
		}
		clearJobLeaseFields(nextJob)
		nextJob.NextRetryAt = nil

		for _, mv := range modifiedVersions {
			if err := tx.PutVersion(ctx, mv); err != nil {
				return err
			}
		}
		for _, mj := range modifiedJobs {
			if err := tx.PutJob(ctx, mj); err != nil {
				return err
			}
		}
		if err := tx.PutVersion(ctx, nextVer); err != nil {
			return err
		}
		if err := tx.PutJob(ctx, nextJob); err != nil {
			return err
		}

		*version = *cloneValue(nextVer)
		*job = *cloneValue(nextJob)
		return nil
	})
	return released, err
}

// SubmitPDFParse atomically checks raw-text size and pending admission caps and inserts a pdf_parse Job.
func (j *Jobs) SubmitPDFParse(ctx context.Context, job *core.Job, opts core.PendingJobOptions) error {
	if job == nil {
		return fmt.Errorf("%w: job cannot be nil", core.ErrValidation)
	}
	if job.Status != "" && job.Status != core.JobStatusPending {
		return fmt.Errorf("%w: pending job cannot start in %s state", core.ErrValidation, job.Status)
	}
	if strings.TrimSpace(job.UserID) == "" {
		return fmt.Errorf("%w: user id is required", core.ErrValidation)
	}
	if job.PDF == nil {
		return fmt.Errorf("%w: pdf parse payload is required", core.ErrValidation)
	}
	maxRawBytes := opts.MaxRawTextBytes
	if maxRawBytes <= 0 {
		maxRawBytes = core.DefaultMaxPDFRawTextBytes
	}
	if len(job.PDF.RawText) > maxRawBytes {
		return fmt.Errorf("%w: raw text exceeds maximum size of %d bytes", core.ErrValidation, maxRawBytes)
	}

	return j.store.backend.Write(ctx, func(tx Tx) error {
		toSave := cloneValue(job)
		if toSave.ID == "" {
			toSave.ID = core.GenerateID("pdf")
		}
		if existing, err := tx.GetJob(ctx, toSave.ID); err == nil && existing != nil {
			return core.ErrConflict
		} else if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		activeJobs, err := tx.ListActiveJobs(ctx)
		if err != nil {
			return err
		}

		now := j.store.resolveNow(opts.Now)
		if err := checkPendingAdmission(activeJobs, toSave.UserID, "", opts, now); err != nil {
			return err
		}

		_, _, ttl := resolvePendingLimits(opts)
		toSave.Kind = core.JobKindPDFParse
		toSave.Status = core.JobStatusPending
		toSave.Tailor = nil
		if toSave.CreatedAt.IsZero() {
			toSave.CreatedAt = now
		}
		toSave.UpdatedAt = now
		if toSave.ExpiresAt == nil {
			exp := now.Add(ttl)
			toSave.ExpiresAt = &exp
		}
		clearJobLeaseFields(toSave)
		toSave.NextRetryAt = nil

		if err := tx.PutJob(ctx, toSave); err != nil {
			return err
		}
		*job = *cloneValue(toSave)
		return nil
	})
}

// SupersedeBranchTailor cancels all pending branch_tailor jobs for branchID and returns released reservations.
func (j *Jobs) SupersedeBranchTailor(ctx context.Context, branchID, reason string) ([]core.ReleasedReservation, error) {
	if strings.TrimSpace(branchID) == "" {
		return nil, fmt.Errorf("%w: branch id cannot be empty", core.ErrValidation)
	}

	var released []core.ReleasedReservation
	err := j.store.backend.Write(ctx, func(tx Tx) error {
		released = nil
		activeJobs, err := tx.ListActiveJobs(ctx)
		if err != nil {
			return err
		}
		versions, err := tx.ListVersionsByBranch(ctx, branchID)
		if err != nil {
			return err
		}
		versionMap := make(map[string]*core.BranchVersion, len(versions))
		for i := range versions {
			versionMap[versions[i].ID] = &versions[i]
		}

		now := j.store.now()
		return cancelBranchJobsTx(ctx, tx, activeJobs, versionMap, branchID, false, reason, now, &released)
	})
	return released, err
}

// AbortBranchJobs cancels both pending and processing branch_tailor jobs for branchID and returns released reservations.
func (j *Jobs) AbortBranchJobs(ctx context.Context, branchID, reason string) ([]core.ReleasedReservation, error) {
	if strings.TrimSpace(branchID) == "" {
		return nil, fmt.Errorf("%w: branch id cannot be empty", core.ErrValidation)
	}

	var released []core.ReleasedReservation
	err := j.store.backend.Write(ctx, func(tx Tx) error {
		released = nil
		activeJobs, err := tx.ListActiveJobs(ctx)
		if err != nil {
			return err
		}
		versions, err := tx.ListVersionsByBranch(ctx, branchID)
		if err != nil {
			return err
		}
		versionMap := make(map[string]*core.BranchVersion, len(versions))
		for i := range versions {
			versionMap[versions[i].ID] = &versions[i]
		}

		now := j.store.now()
		return cancelBranchJobsTx(ctx, tx, activeJobs, versionMap, branchID, true, reason, now, &released)
	})
	return released, err
}

// CancelJob cancels an owned pending job by ID, pruning transient PDF input and syncing any linked BranchVersion.
func (j *Jobs) CancelJob(ctx context.Context, userID, jobID, reason string) (*core.Job, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(jobID) == "" {
		return nil, fmt.Errorf("%w: user id and job id are required", core.ErrValidation)
	}

	var out *core.Job
	err := j.store.backend.Write(ctx, func(tx Tx) error {
		job, err := tx.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
		if job.UserID != userID {
			return core.ErrNotFound
		}
		if job.Status != core.JobStatusPending {
			return fmt.Errorf("%w: cannot cancel job in %s state", core.ErrConflict, job.Status)
		}

		var ver *core.BranchVersion
		if job.Kind == core.JobKindBranchTailor && job.Tailor != nil && job.Tailor.VersionID != "" {
			ver, err = tx.GetVersion(ctx, job.Tailor.VersionID)
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				return err
			}
		}

		now := j.store.now()
		failJobAndVersion(job, ver, core.JobStatusCancelled, core.VersionStatusCancelled, reason, now, true)
		if ver != nil {
			if err := tx.PutVersion(ctx, ver); err != nil {
				return err
			}
		}
		if err := tx.PutJob(ctx, job); err != nil {
			return err
		}
		out = cloneValue(job)
		return nil
	})
	return out, err
}

// CancelBranchVersion cancels an owned pending BranchVersion along with its linked queue Job.
func (j *Jobs) CancelBranchVersion(ctx context.Context, userID, branchID, versionID, reason string) (*core.BranchVersion, *core.Job, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(versionID) == "" {
		return nil, nil, fmt.Errorf("%w: user id and version id are required", core.ErrValidation)
	}

	var (
		outVer *core.BranchVersion
		outJob *core.Job
	)
	err := j.store.backend.Write(ctx, func(tx Tx) error {
		ver, err := tx.GetVersion(ctx, versionID)
		if err != nil {
			return err
		}
		if ver.UserID != userID || (branchID != "" && ver.BranchID != branchID) {
			return core.ErrNotFound
		}
		var job *core.Job
		if ver.JobID != "" {
			job, err = tx.GetJob(ctx, ver.JobID)
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				return err
			}
		}

		if ver.Status != core.VersionStatusPending {
			return fmt.Errorf("%w: cannot cancel version in %s state", core.ErrConflict, ver.Status)
		}
		if job != nil && job.Status != core.JobStatusPending {
			return fmt.Errorf("%w: cannot cancel job in %s state", core.ErrConflict, job.Status)
		}

		now := j.store.now()
		if job != nil {
			failJobAndVersion(job, ver, core.JobStatusCancelled, core.VersionStatusCancelled, reason, now, true)
			if err := tx.PutJob(ctx, job); err != nil {
				return err
			}
			outJob = cloneValue(job)
		} else {
			ver.Status = core.VersionStatusCancelled
			ver.ErrorMessage = reason
			completedAt := now
			ver.CompletedAt = &completedAt
			ver.UpdatedAt = now
		}
		if err := tx.PutVersion(ctx, ver); err != nil {
			return err
		}
		outVer = cloneValue(ver)
		return nil
	})
	return outVer, outJob, err
}

// ClaimNext atomically selects and claims the fairest eligible pending job across all workloads.
func (j *Jobs) ClaimNext(ctx context.Context, req core.ClaimJobRequest) (*core.Job, error) {
	if strings.TrimSpace(req.WorkerID) == "" {
		return nil, fmt.Errorf("%w: worker id cannot be empty", core.ErrValidation)
	}

	var claimed *core.Job
	err := j.store.backend.Write(ctx, func(tx Tx) error {
		claimed = nil
		activeJobs, err := tx.ListActiveJobs(ctx)
		if err != nil {
			return err
		}

		now := j.store.resolveNow(req.Now)
		leaseDur := req.LeaseDuration
		if leaseDur <= 0 {
			leaseDur = core.DefaultLeaseDuration
		}

		activeByTenant, activeBranches := activeCountsAt(activeJobs, now)
		var candidates []core.Job
		for _, item := range activeJobs {
			if item.Status != core.JobStatusPending || !isPendingUnexpired(item.ExpiresAt, now) || !isRetryReady(item.NextRetryAt, now) {
				continue
			}
			if item.Kind == core.JobKindBranchTailor && item.Tailor != nil && activeBranches[item.Tailor.BranchID] {
				continue
			}
			if req.MaxConcurrentPerTenant > 0 && activeByTenant[item.UserID] >= req.MaxConcurrentPerTenant {
				continue
			}
			candidates = append(candidates, item)
		}

		winner, ok := selectFairestJob(candidates, req.LastServedSeq, req.PreferredKind)
		if !ok {
			return nil
		}

		var (
			br  *core.Branch
			ver *core.BranchVersion
		)
		if winner.Kind == core.JobKindBranchTailor && winner.Tailor != nil {
			br, err = tx.GetBranch(ctx, winner.Tailor.BranchID)
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				return err
			}
			ver, err = tx.GetVersion(ctx, winner.Tailor.VersionID)
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				return err
			}
		}

		leaseExp := now.Add(leaseDur)
		winner.Status = core.JobStatusProcessing
		winner.ClaimedBy = req.WorkerID
		winner.ClaimedAt = &now
		winner.LeaseExpiresAt = &leaseExp
		winner.NextRetryAt = nil
		winner.AttemptCount++
		winner.UpdatedAt = now
		if winner.Kind == core.JobKindBranchTailor && winner.Tailor != nil && br != nil {
			epoch := br.Epoch
			if epoch <= 0 {
				epoch = 1
			}
			winner.Tailor.BranchEpoch = epoch
		}

		if err := tx.PutJob(ctx, &winner); err != nil {
			return err
		}
		if ver != nil {
			ver.Status = core.VersionStatusProcessing
			if winner.Tailor != nil {
				ver.BranchEpoch = winner.Tailor.BranchEpoch
			}
			ver.UpdatedAt = now
			if err := tx.PutVersion(ctx, ver); err != nil {
				return err
			}
		}

		cp := cloneValue(winner)
		claimed = &cp
		return nil
	})
	return claimed, err
}

// RenewLease extends an active processing job lease owned by claimedBy.
func (j *Jobs) RenewLease(ctx context.Context, jobID, claimedBy string, leaseExpiresAt time.Time) error {
	if strings.TrimSpace(jobID) == "" || strings.TrimSpace(claimedBy) == "" || leaseExpiresAt.IsZero() {
		return fmt.Errorf("%w: job id, claimed_by, and lease_expires_at are required", core.ErrValidation)
	}

	return j.store.backend.Write(ctx, func(tx Tx) error {
		job, err := tx.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
		if job.Status != core.JobStatusProcessing || job.ClaimedBy != claimedBy {
			return fmt.Errorf("%w: job %s is not actively claimed by %q", core.ErrLeaseExpired, jobID, claimedBy)
		}
		exp := leaseExpiresAt.UTC()
		job.LeaseExpiresAt = &exp
		job.UpdatedAt = j.store.now()
		return tx.PutJob(ctx, job)
	})
}

// RequeueForRetry returns a claimed processing job back to pending for scheduled retry prior to ExpiresAt.
func (j *Jobs) RequeueForRetry(ctx context.Context, jobID, claimedBy string, nextRetryAt time.Time, errMsg string) error {
	if strings.TrimSpace(jobID) == "" || strings.TrimSpace(claimedBy) == "" || nextRetryAt.IsZero() {
		return fmt.Errorf("%w: job id, claimed_by, and next_retry_at are required", core.ErrValidation)
	}

	return j.store.backend.Write(ctx, func(tx Tx) error {
		job, err := tx.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
		if job.Status != core.JobStatusProcessing || job.ClaimedBy != claimedBy {
			return fmt.Errorf("%w: job %s is not actively claimed by %q", core.ErrLeaseExpired, jobID, claimedBy)
		}
		retryAt := nextRetryAt.UTC()
		if job.ExpiresAt != nil && !retryAt.Before(*job.ExpiresAt) {
			return core.ErrJobExpired
		}

		var ver *core.BranchVersion
		if job.Kind == core.JobKindBranchTailor && job.Tailor != nil && job.Tailor.VersionID != "" {
			ver, err = tx.GetVersion(ctx, job.Tailor.VersionID)
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				return err
			}
		}

		now := j.store.now()
		job.Status = core.JobStatusPending
		job.ErrorMessage = errMsg
		clearJobLeaseFields(job)
		job.NextRetryAt = &retryAt
		job.UpdatedAt = now
		if err := tx.PutJob(ctx, job); err != nil {
			return err
		}
		if ver != nil {
			ver.Status = core.VersionStatusPending
			ver.ErrorMessage = errMsg
			ver.UpdatedAt = now
			return tx.PutVersion(ctx, ver)
		}
		return nil
	})
}

// CompleteBranchTailor atomically completes a claimed branch_tailor job after verifying lease ownership,
// branch epoch fencing, and monotonic version promotion, then promotes the completed version on the branch.
func (j *Jobs) CompleteBranchTailor(ctx context.Context, jobID, claimedBy string, completed *core.BranchVersion) error {
	if strings.TrimSpace(jobID) == "" || strings.TrimSpace(claimedBy) == "" || completed == nil {
		return fmt.Errorf("%w: job id, claimed_by, and completed version are required", core.ErrValidation)
	}

	return j.store.backend.Write(ctx, func(tx Tx) error {
		job, err := tx.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
		if job.Kind != core.JobKindBranchTailor || job.Tailor == nil {
			return fmt.Errorf("%w: job %s is not a branch tailor job", core.ErrValidation, jobID)
		}
		if job.Status != core.JobStatusProcessing || job.ClaimedBy != claimedBy {
			return fmt.Errorf("%w: job %s is not actively claimed by %q", core.ErrLeaseExpired, jobID, claimedBy)
		}

		br, err := tx.GetBranch(ctx, job.Tailor.BranchID)
		if err != nil {
			return err
		}
		if job.Tailor.BranchEpoch != br.Epoch {
			return fmt.Errorf("%w: branch configuration or active version changed while job %s was processing", core.ErrConflict, jobID)
		}
		existingVer, err := tx.GetVersion(ctx, job.Tailor.VersionID)
		if err != nil {
			return err
		}
		if existingVer.Status == core.VersionStatusFailed || existingVer.Status == core.VersionStatusCancelled {
			return fmt.Errorf("%w: cannot complete terminal version %s in %s state", core.ErrConflict, existingVer.ID, existingVer.Status)
		}
		if br.ActiveVersionID != nil && *br.ActiveVersionID != "" && *br.ActiveVersionID != existingVer.ID {
			activeVer, aErr := tx.GetVersion(ctx, *br.ActiveVersionID)
			if aErr != nil && !errors.Is(aErr, core.ErrNotFound) {
				return aErr
			}
			if activeVer != nil && activeVer.VersionNumber > existingVer.VersionNumber {
				return fmt.Errorf("%w: stale version %d cannot overwrite active version %d", core.ErrConflict, existingVer.VersionNumber, activeVer.VersionNumber)
			}
		}

		now := j.store.now()
		finalVer := cloneValue(existingVer)
		finalVer.JobID = job.ID
		finalVer.BranchEpoch = br.Epoch
		finalVer.Status = completed.Status
		if !finalVer.Status.IsCompleted() {
			finalVer.Status = core.VersionStatusCompleted
		}
		finalVer.MainRevisionUsed = completed.MainRevisionUsed
		finalVer.TemplateSetIDUsed = completed.TemplateSetIDUsed
		finalVer.ProviderUsed = completed.ProviderUsed
		finalVer.ModelUsed = completed.ModelUsed
		finalVer.TailoredContent = cloneValue(completed.TailoredContent)
		finalVer.GapReport = cloneValue(completed.GapReport)
		finalVer.ValidationWarnings = cloneValue(completed.ValidationWarnings)
		finalVer.ErrorMessage = ""
		finalVer.HTMLOutput = completed.HTMLOutput
		finalVer.MarkdownOutput = completed.MarkdownOutput
		finalVer.PlainTextOutput = completed.PlainTextOutput
		completedAt := now
		finalVer.CompletedAt = &completedAt
		finalVer.UpdatedAt = now

		job.Status = core.JobStatusCompleted
		job.ErrorMessage = ""
		job.CompletedAt = &completedAt
		job.UpdatedAt = now
		clearJobLeaseFields(job)
		job.NextRetryAt = nil

		nextBranch := cloneValue(br)
		syncBranchFromVersion(nextBranch, finalVer, now)

		if err := tx.PutVersion(ctx, finalVer); err != nil {
			return err
		}
		if err := tx.PutJob(ctx, job); err != nil {
			return err
		}
		return tx.PutBranch(ctx, nextBranch)
	})
}

// CompletePDFParse completes a claimed pdf_parse job with its extracted JSON Resume and prunes RawText.
func (j *Jobs) CompletePDFParse(ctx context.Context, jobID, claimedBy string, result *core.JSONResume) error {
	if strings.TrimSpace(jobID) == "" || strings.TrimSpace(claimedBy) == "" {
		return fmt.Errorf("%w: job id and claimed_by are required", core.ErrValidation)
	}

	return j.store.backend.Write(ctx, func(tx Tx) error {
		job, err := tx.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
		if job.Kind != core.JobKindPDFParse || job.PDF == nil {
			return fmt.Errorf("%w: job %s is not a pdf parse job", core.ErrValidation, jobID)
		}
		if job.Status != core.JobStatusProcessing || job.ClaimedBy != claimedBy {
			return fmt.Errorf("%w: job %s is not actively claimed by %q", core.ErrLeaseExpired, jobID, claimedBy)
		}

		now := j.store.now()
		if result != nil {
			job.PDF.Result = cloneValue(result)
		}
		job.ErrorMessage = ""
		failJobAndVersion(job, nil, core.JobStatusCompleted, "", "", now, false)
		return tx.PutJob(ctx, job)
	})
}

// FailClaimed transitions an actively claimed processing job to failed, syncs any linked
// BranchVersion to failed, and prunes transient PDF RawText.
func (j *Jobs) FailClaimed(ctx context.Context, jobID, claimedBy, errMsg string) error {
	if strings.TrimSpace(jobID) == "" || strings.TrimSpace(claimedBy) == "" {
		return fmt.Errorf("%w: job id and claimed_by are required", core.ErrValidation)
	}

	return j.store.backend.Write(ctx, func(tx Tx) error {
		job, err := tx.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
		if job.Status != core.JobStatusProcessing || job.ClaimedBy != claimedBy {
			return fmt.Errorf("%w: job %s is not actively claimed by %q", core.ErrLeaseExpired, jobID, claimedBy)
		}
		var ver *core.BranchVersion
		if job.Kind == core.JobKindBranchTailor && job.Tailor != nil && job.Tailor.VersionID != "" {
			ver, err = tx.GetVersion(ctx, job.Tailor.VersionID)
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				return err
			}
		}

		failJobAndVersion(job, ver, core.JobStatusFailed, core.VersionStatusFailed, errMsg, j.store.now(), false)
		if ver != nil {
			if err := tx.PutVersion(ctx, ver); err != nil {
				return err
			}
		}
		return tx.PutJob(ctx, job)
	})
}

// ExpirePending fails pending jobs whose ExpiresAt deadline has passed at now and returns released reservations.
func (j *Jobs) ExpirePending(ctx context.Context, now time.Time, reason string) ([]core.ReleasedReservation, error) {
	return j.sweepJobs(ctx, now, reason, true, func(item *core.Job, ts time.Time) bool {
		return item.Status == core.JobStatusPending && !isPendingUnexpired(item.ExpiresAt, ts)
	})
}

// FailExpiredLeases fails processing jobs whose worker lease has expired at now and returns released reservations.
func (j *Jobs) FailExpiredLeases(ctx context.Context, now time.Time, reason string) ([]core.ReleasedReservation, error) {
	return j.sweepJobs(ctx, now, reason, false, func(item *core.Job, ts time.Time) bool {
		return item.Status == core.JobStatusProcessing && !isLeaseUnexpired(item.LeaseExpiresAt, ts)
	})
}

// RecoverInterrupted fails all in-flight processing jobs and expired pending jobs following restart,
// preserving unexpired pending work and returning released quota reservations.
func (j *Jobs) RecoverInterrupted(ctx context.Context, reason string) ([]core.ReleasedReservation, error) {
	return j.sweepJobs(ctx, time.Time{}, reason, false, func(item *core.Job, ts time.Time) bool {
		return item.Status == core.JobStatusProcessing || (item.Status == core.JobStatusPending && !isPendingUnexpired(item.ExpiresAt, ts))
	})
}

// CleanupCompletedJobs deletes terminal jobs older than olderThan for userID while keeping artifacts intact.
func (j *Jobs) CleanupCompletedJobs(ctx context.Context, userID string, olderThan time.Duration) error {
	if strings.TrimSpace(userID) == "" {
		return fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}

	return j.store.backend.Write(ctx, func(tx Tx) error {
		cutoff := j.store.now().Add(-olderThan)
		jobs, err := tx.ListTerminalJobsBefore(ctx, userID, cutoff)
		if err != nil {
			return err
		}
		for _, item := range jobs {
			if !item.Status.IsTerminal() {
				continue
			}
			ts := item.UpdatedAt
			if item.CompletedAt != nil {
				ts = *item.CompletedAt
			}
			if ts.Before(cutoff) {
				if err := tx.DeleteJob(ctx, item.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
					return err
				}
			}
		}
		return nil
	})
}

// sweepJobs atomically transitions every active job matching predicate to failed and returns released reservations.
func (j *Jobs) sweepJobs(
	ctx context.Context,
	explicitNow time.Time,
	reason string,
	clearClaim bool,
	match func(*core.Job, time.Time) bool,
) ([]core.ReleasedReservation, error) {
	var released []core.ReleasedReservation
	err := j.store.backend.Write(ctx, func(tx Tx) error {
		released = nil
		ts := j.store.resolveNow(explicitNow)
		activeJobs, err := tx.ListActiveJobs(ctx)
		if err != nil {
			return err
		}

		// Read every referenced BranchVersion before performing writes.
		versionsByID := make(map[string]*core.BranchVersion)
		var matched []*core.Job
		for i := range activeJobs {
			if !match(&activeJobs[i], ts) {
				continue
			}
			cp := cloneValue(activeJobs[i])
			matched = append(matched, &cp)
			if cp.Kind == core.JobKindBranchTailor && cp.Tailor != nil && cp.Tailor.VersionID != "" {
				ver, err := tx.GetVersion(ctx, cp.Tailor.VersionID)
				if err != nil && !errors.Is(err, core.ErrNotFound) {
					return err
				}
				if ver != nil {
					versionsByID[ver.ID] = ver
				}
			}
		}

		for _, mj := range matched {
			var linkedVer *core.BranchVersion
			if mj.Kind == core.JobKindBranchTailor && mj.Tailor != nil {
				linkedVer = versionsByID[mj.Tailor.VersionID]
			}
			if rel := failJobAndVersion(mj, linkedVer, core.JobStatusFailed, core.VersionStatusFailed, reason, ts, clearClaim); rel != nil {
				released = append(released, *rel)
			}
			if linkedVer != nil {
				if err := tx.PutVersion(ctx, linkedVer); err != nil {
					return err
				}
			}
			if err := tx.PutJob(ctx, mj); err != nil {
				return err
			}
		}
		return nil
	})
	return released, err
}

// cancelBranchJobsTx cancels active branch_tailor jobs for branchID matching pending or all non-terminal states.
func cancelBranchJobsTx(
	ctx context.Context,
	tx Tx,
	activeJobs []core.Job,
	versionMap map[string]*core.BranchVersion,
	branchID string,
	includeProcessing bool,
	reason string,
	now time.Time,
	released *[]core.ReleasedReservation,
) error {
	for i := range activeJobs {
		aj := &activeJobs[i]
		if !isJobForBranch(aj, branchID) {
			continue
		}
		if !includeProcessing && aj.Status != core.JobStatusPending {
			continue
		}
		if includeProcessing && aj.Status.IsTerminal() {
			continue
		}
		var ver *core.BranchVersion
		if aj.Tailor != nil {
			ver = versionMap[aj.Tailor.VersionID]
		}
		cp := cloneValue(*aj)
		if rel := failJobAndVersion(&cp, ver, core.JobStatusCancelled, core.VersionStatusCancelled, reason, now, true); rel != nil {
			*released = append(*released, *rel)
		}
		if ver != nil {
			if err := tx.PutVersion(ctx, ver); err != nil {
				return err
			}
		}
		if err := tx.PutJob(ctx, &cp); err != nil {
			return err
		}
	}
	return nil
}

// resolveTailorBranchID extracts the branch ID specified on job or version.
func resolveTailorBranchID(job *core.Job, version *core.BranchVersion) string {
	if job != nil && job.Tailor != nil && job.Tailor.BranchID != "" {
		return job.Tailor.BranchID
	}
	if version != nil {
		return version.BranchID
	}
	return ""
}

// findJobInSlice finds a job matching id inside jobs.
func findJobInSlice(jobs []core.Job, id string) (core.Job, bool) {
	for _, j := range jobs {
		if j.ID == id {
			return j, true
		}
	}
	return core.Job{}, false
}

// isJobForBranch reports whether j is a branch_tailor job targeting branchID.
func isJobForBranch(j *core.Job, branchID string) bool {
	return j != nil && j.Kind == core.JobKindBranchTailor && j.Tailor != nil && j.Tailor.BranchID == branchID
}

// clearJobLeaseFields clears worker lease ownership fields after completion, requeue, or cancellation.
func clearJobLeaseFields(job *core.Job) {
	job.ClaimedBy = ""
	job.ClaimedAt = nil
	job.LeaseExpiresAt = nil
}

// failJobAndVersion marks job and optional linked BranchVersion terminal, prunes PDF RawText,
// and emits any Held ReservationID release record.
func failJobAndVersion(
	job *core.Job,
	ver *core.BranchVersion,
	jobStatus core.JobStatus,
	verStatus core.VersionStatus,
	reason string,
	now time.Time,
	clearClaim bool,
) *core.ReleasedReservation {
	job.Status = jobStatus
	job.ErrorMessage = reason
	job.UpdatedAt = now
	completedAt := now
	job.CompletedAt = &completedAt
	job.LeaseExpiresAt = nil
	job.NextRetryAt = nil
	if clearClaim {
		job.ClaimedBy = ""
		job.ClaimedAt = nil
	}
	if job.Kind == core.JobKindPDFParse && job.PDF != nil {
		job.PDF.RawText = ""
	}

	if ver != nil && verStatus != "" {
		ver.Status = verStatus
		ver.ErrorMessage = reason
		ver.CompletedAt = &completedAt
		ver.UpdatedAt = now
	}

	if job.ReservationID == "" || jobStatus == core.JobStatusCompleted {
		return nil
	}
	return &core.ReleasedReservation{
		JobID:         job.ID,
		UserID:        job.UserID,
		ReservationID: job.ReservationID,
		Reason:        reason,
	}
}

// isPendingUnexpired reports whether an optional pending expiration deadline is strictly in the future relative to now.
func isPendingUnexpired(expiresAt *time.Time, now time.Time) bool {
	return expiresAt == nil || expiresAt.After(now)
}

// isLeaseUnexpired reports whether an optional worker lease expiry deadline is strictly in the future relative to now.
func isLeaseUnexpired(leaseExpiresAt *time.Time, now time.Time) bool {
	return leaseExpiresAt == nil || leaseExpiresAt.After(now)
}

// isRetryReady reports whether a pending job has no backoff delay or NextRetryAt <= now.
func isRetryReady(nextRetryAt *time.Time, now time.Time) bool {
	return nextRetryAt == nil || !nextRetryAt.After(now)
}

// resolvePendingLimits returns effective per-tenant cap, global cap, and TTL defaults from opts.
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

// checkPendingAdmission enforces MaxPendingPerTenant and MaxGlobalPending across all active unexpired pending jobs,
// excluding prior pending tailor jobs on excludeBranchID that are atomically superseded in the same submit.
func checkPendingAdmission(activeJobs []core.Job, userID, excludeBranchID string, opts core.PendingJobOptions, now time.Time) error {
	var tenantCount, globalCount int
	for _, j := range activeJobs {
		if j.Status != core.JobStatusPending || !isPendingUnexpired(j.ExpiresAt, now) {
			continue
		}
		if excludeBranchID != "" && isJobForBranch(&j, excludeBranchID) {
			continue
		}
		globalCount++
		if j.UserID == userID {
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

// activeCountsAt counts active unexpired processing leases by tenant ID and active branches.
func activeCountsAt(activeJobs []core.Job, now time.Time) (map[string]int, map[string]bool) {
	byTenant := make(map[string]int)
	byBranch := make(map[string]bool)
	for _, j := range activeJobs {
		if j.Status == core.JobStatusProcessing && isLeaseUnexpired(j.LeaseExpiresAt, now) {
			byTenant[j.UserID]++
			if j.Kind == core.JobKindBranchTailor && j.Tailor != nil && j.Tailor.BranchID != "" {
				byBranch[j.Tailor.BranchID] = true
			}
		}
	}
	return byTenant, byBranch
}

// selectFairestJob picks the highest-priority schedulable candidate ordered by LastServedSeq LRU,
// optional PreferredKind tie-break, oldest CreatedAt, and lexicographical ID.
func selectFairestJob(candidates []core.Job, lastServedSeq map[string]uint64, preferredKind core.JobKind) (core.Job, bool) {
	if len(candidates) == 0 {
		return core.Job{}, false
	}
	best := candidates[0]
	for _, c := range candidates[1:] {
		if jobPrecedes(c, best, lastServedSeq, preferredKind) {
			best = c
		}
	}
	return best, true
}

// jobPrecedes reports whether a should be scheduled strictly before b.
func jobPrecedes(a, b core.Job, lastServedSeq map[string]uint64, preferredKind core.JobKind) bool {
	aSeq := lastServedSeq[a.UserID]
	bSeq := lastServedSeq[b.UserID]
	if aSeq != bSeq {
		return aSeq < bSeq
	}
	if preferredKind != "" && (a.Kind == preferredKind) != (b.Kind == preferredKind) {
		return a.Kind == preferredKind
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}
