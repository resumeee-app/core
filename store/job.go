package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/resumeee-app/core"
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
		job, err := tx.Job(ctx, jobID)
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

// SubmitBranchTailor atomically checks admission caps, optionally reserves tenant quota,
// creates a pending BranchVersion and queued branch_tailor Job, and supersedes prior
// pending work or expired active leases on the branch alongside their quota reservations.
func (j *Jobs) SubmitBranchTailor(
	ctx context.Context,
	job *core.Job,
	version *core.BranchVersion,
	opts core.PendingJobOptions,
) error {
	branchID, err := validateTailorSubmission(job, version)
	if err != nil {
		return err
	}

	return j.store.backend.Write(ctx, func(tx Tx) error {
		// Execute all reads before writing.
		br, err := tx.Branch(ctx, branchID)
		if err != nil {
			return err
		}
		userID, err := resolveTailorUserID(job, version, br)
		if err != nil {
			return err
		}
		activeJobs, err := tx.ListActiveJobs(ctx)
		if err != nil {
			return err
		}
		versions, err := tx.ListVersionsByBranch(ctx, branchID)
		if err != nil {
			return err
		}
		versionMap := indexBranchVersions(versions)
		nextJob, err := prepareNewTailorJob(ctx, tx, job, activeJobs)
		if err != nil {
			return err
		}
		nextVer, err := prepareNewTailorVersion(ctx, tx, version, versionMap)
		if err != nil {
			return err
		}

		now := j.store.resolveNow(opts.Now)
		if err := ensureNoActiveProcessingJob(activeJobs, branchID, now); err != nil {
			return err
		}
		if err := checkPendingAdmission(activeJobs, userID, branchID, opts, now); err != nil {
			return err
		}

		modVersions, modJobs := collectSupersededTailorWork(branchID, activeJobs, versionMap, now)
		ledger := newQuotaTxLedger(now)
		if err := ledger.loadPendingReleaseTargets(ctx, tx, modJobs); err != nil {
			return err
		}
		if opts.Quota != nil {
			if _, err := ledger.loadQuota(ctx, tx, userID, opts.Quota.DailyLimit, opts.Quota.MonthlyLimit, true); err != nil {
				return err
			}
		}

		for _, mj := range modJobs {
			if mj.ReservationID != "" {
				ledger.release(mj.ReservationID, mj.ErrorMessage, core.LLMTelemetry{})
			}
		}
		if opts.Quota != nil {
			rec, err := ledger.reserve(userID, *opts.Quota)
			if err != nil {
				return err
			}
			nextJob.ReservationID = rec.ID
		}

		if err := ledger.flush(ctx, tx); err != nil {
			return err
		}
		populateTailorSubmission(br, userID, branchID, versions, nextJob, nextVer, opts, now)
		if err := persistTailorSubmission(ctx, tx, modVersions, modJobs, nextVer, nextJob); err != nil {
			return err
		}

		*version = *cloneValue(nextVer)
		*job = *cloneValue(nextJob)
		return nil
	})
}

// validateTailorSubmission validates initial tailor submission arguments and resolves the target branch ID.
func validateTailorSubmission(job *core.Job, version *core.BranchVersion) (string, error) {
	if job == nil || version == nil {
		return "", fmt.Errorf("%w: job and version cannot be nil", core.ErrValidation)
	}
	if job.Status != "" && job.Status != core.JobStatusPending {
		return "", fmt.Errorf("%w: pending job cannot start in %s state", core.ErrValidation, job.Status)
	}
	if version.Status != "" && version.Status != core.VersionStatusPending {
		return "", fmt.Errorf("%w: pending version cannot start in %s state", core.ErrValidation, version.Status)
	}
	branchID := resolveTailorBranchID(job, version)
	if branchID == "" {
		return "", fmt.Errorf("%w: branch id is required", core.ErrValidation)
	}
	return branchID, nil
}

// resolveTailorUserID determines the owner user ID for a tailor submission and verifies branch ownership.
func resolveTailorUserID(job *core.Job, version *core.BranchVersion, br *core.Branch) (string, error) {
	userID := job.UserID
	if userID == "" {
		userID = version.UserID
	}
	if userID == "" {
		userID = br.UserID
	} else if br.UserID != "" && br.UserID != userID {
		return "", core.ErrNotFound
	}
	if userID == "" {
		return "", fmt.Errorf("%w: user id is required", core.ErrValidation)
	}
	return userID, nil
}

// indexBranchVersions indexes branch versions by ID for fast lookup within a transaction.
func indexBranchVersions(versions []core.BranchVersion) map[string]*core.BranchVersion {
	byID := make(map[string]*core.BranchVersion, len(versions))
	for i := range versions {
		byID[versions[i].ID] = &versions[i]
	}
	return byID
}

// prepareNewTailorJob clones job, assigns an ID when missing, and confirms no job exists with that ID.
func prepareNewTailorJob(ctx context.Context, tx ReadTx, job *core.Job, activeJobs []core.Job) (*core.Job, error) {
	nextJob := cloneValue(job)
	if nextJob.ID == "" {
		nextJob.ID = core.GenerateID("job")
	}
	if _, exists := findJobInSlice(activeJobs, nextJob.ID); exists {
		return nil, core.ErrConflict
	}
	if existing, err := tx.Job(ctx, nextJob.ID); err == nil && existing != nil {
		return nil, core.ErrConflict
	} else if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}
	return nextJob, nil
}

// prepareNewTailorVersion clones version, assigns an ID when missing, and confirms no version exists with that ID.
func prepareNewTailorVersion(ctx context.Context, tx ReadTx, version *core.BranchVersion, versionMap map[string]*core.BranchVersion) (*core.BranchVersion, error) {
	nextVer := cloneValue(version)
	if nextVer.ID == "" {
		nextVer.ID = core.GenerateID("ver")
	}
	if _, exists := versionMap[nextVer.ID]; exists {
		return nil, core.ErrConflict
	}
	if existing, err := tx.Version(ctx, nextVer.ID); err == nil && existing != nil {
		return nil, core.ErrConflict
	} else if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}
	return nextVer, nil
}

// ensureNoActiveProcessingJob rejects submission when branchID already has a non-expired processing job.
func ensureNoActiveProcessingJob(activeJobs []core.Job, branchID string, now time.Time) error {
	for _, aj := range activeJobs {
		if isJobForBranch(&aj, branchID) && aj.Status == core.JobStatusProcessing && isLeaseUnexpired(aj.LeaseExpiresAt, now) {
			return fmt.Errorf("%w: branch %s already has an active processing job", core.ErrConflict, branchID)
		}
	}
	return nil
}

// collectSupersededTailorWork fails expired leases and cancels superseded pending jobs on branchID alongside linked versions.
func collectSupersededTailorWork(
	branchID string,
	activeJobs []core.Job,
	versionMap map[string]*core.BranchVersion,
	now time.Time,
) ([]*core.BranchVersion, []*core.Job) {
	var (
		modVersions []*core.BranchVersion
		modJobs     []*core.Job
	)
	for i := range activeJobs {
		aj := &activeJobs[i]
		if !isJobForBranch(aj, branchID) {
			continue
		}
		var (
			jobStatus  core.JobStatus
			verStatus  core.VersionStatus
			reason     string
			clearLease bool
		)
		switch {
		case aj.Status == core.JobStatusProcessing && !isLeaseUnexpired(aj.LeaseExpiresAt, now):
			jobStatus, verStatus, reason, clearLease = core.JobStatusFailed, core.VersionStatusFailed, "worker lease expired", false
		case aj.Status == core.JobStatusPending:
			jobStatus, verStatus, reason, clearLease = core.JobStatusCancelled, core.VersionStatusCancelled, "superseded by newer pending version", true
		default:
			continue
		}
		var linkedVer *core.BranchVersion
		if aj.Tailor != nil {
			linkedVer = versionMap[aj.Tailor.VersionID]
		}
		cp := cloneValue(*aj)
		failJobAndVersion(&cp, linkedVer, jobStatus, verStatus, reason, now, clearLease)
		modJobs = append(modJobs, &cp)
		if linkedVer != nil {
			modVersions = append(modVersions, linkedVer)
		}
	}
	return modVersions, modJobs
}

// populateTailorSubmission fills normalized pending fields on nextVer and nextJob before persistence.
func populateTailorSubmission(
	br *core.Branch,
	userID, branchID string,
	versions []core.BranchVersion,
	nextJob *core.Job,
	nextVer *core.BranchVersion,
	opts core.PendingJobOptions,
	now time.Time,
) {
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
}

// persistTailorSubmission writes superseded records followed by the new pending version and job.
func persistTailorSubmission(
	ctx context.Context,
	tx Tx,
	modVersions []*core.BranchVersion,
	modJobs []*core.Job,
	nextVer *core.BranchVersion,
	nextJob *core.Job,
) error {
	for _, mv := range modVersions {
		if err := tx.PutVersion(ctx, mv); err != nil {
			return err
		}
	}
	for _, mj := range modJobs {
		if err := tx.PutJob(ctx, mj); err != nil {
			return err
		}
	}
	if err := tx.PutVersion(ctx, nextVer); err != nil {
		return err
	}
	return tx.PutJob(ctx, nextJob)
}

// SubmitPDFParse atomically checks raw-text size and pending admission caps, optionally reserves tenant quota,
// and inserts a pdf_parse Job.
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
		if existing, err := tx.Job(ctx, toSave.ID); err == nil && existing != nil {
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

		var ledger *quotaTxLedger
		if opts.Quota != nil {
			ledger = newQuotaTxLedger(now)
			if _, err := ledger.loadQuota(ctx, tx, toSave.UserID, opts.Quota.DailyLimit, opts.Quota.MonthlyLimit, true); err != nil {
				return err
			}
			rec, err := ledger.reserve(toSave.UserID, *opts.Quota)
			if err != nil {
				return err
			}
			toSave.ReservationID = rec.ID
			if err := ledger.flush(ctx, tx); err != nil {
				return err
			}
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

// SupersedeBranchTailor atomically cancels pending branch_tailor jobs for branchID and refunds held quota reservations.
func (j *Jobs) SupersedeBranchTailor(ctx context.Context, branchID, reason string) error {
	return j.cancelBranchJobs(ctx, branchID, false, reason)
}

// AbortBranchJobs atomically cancels both pending and processing branch_tailor jobs for branchID
// and refunds held quota reservations.
func (j *Jobs) AbortBranchJobs(ctx context.Context, branchID, reason string) error {
	return j.cancelBranchJobs(ctx, branchID, true, reason)
}

// cancelBranchJobs atomically cancels matching active branch_tailor jobs and refunds their held quota reservations.
func (j *Jobs) cancelBranchJobs(ctx context.Context, branchID string, includeProcessing bool, reason string) error {
	if strings.TrimSpace(branchID) == "" {
		return fmt.Errorf("%w: branch id cannot be empty", core.ErrValidation)
	}

	return j.store.backend.Write(ctx, func(tx Tx) error {
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

		var matchedJobs []*core.Job
		var matchedVersions []*core.BranchVersion
		now := j.store.now()
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
			cp := cloneValue(*aj)
			var ver *core.BranchVersion
			if cp.Tailor != nil {
				ver = versionMap[cp.Tailor.VersionID]
			}
			failJobAndVersion(&cp, ver, core.JobStatusCancelled, core.VersionStatusCancelled, reason, now, true)
			matchedJobs = append(matchedJobs, &cp)
			if ver != nil {
				matchedVersions = append(matchedVersions, ver)
			}
		}

		ledger := newQuotaTxLedger(now)
		if err := ledger.loadPendingReleaseTargets(ctx, tx, matchedJobs); err != nil {
			return err
		}
		for _, mj := range matchedJobs {
			if mj.ReservationID != "" {
				ledger.release(mj.ReservationID, reason, core.LLMTelemetry{})
			}
		}
		if err := ledger.flush(ctx, tx); err != nil {
			return err
		}
		for _, ver := range matchedVersions {
			if err := tx.PutVersion(ctx, ver); err != nil {
				return err
			}
		}
		for _, mj := range matchedJobs {
			if err := tx.PutJob(ctx, mj); err != nil {
				return err
			}
		}
		return nil
	})
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
			br, err = tx.Branch(ctx, winner.Tailor.BranchID)
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				return err
			}
			ver, err = tx.Version(ctx, winner.Tailor.VersionID)
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
		job, err := tx.Job(ctx, jobID)
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
		job, err := tx.Job(ctx, jobID)
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
			ver, err = tx.Version(ctx, job.Tailor.VersionID)
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
		job, err := tx.Job(ctx, jobID)
		if err != nil {
			return err
		}
		if job.Kind != core.JobKindBranchTailor || job.Tailor == nil {
			return fmt.Errorf("%w: job %s is not a branch tailor job", core.ErrValidation, jobID)
		}
		if job.Status != core.JobStatusProcessing || job.ClaimedBy != claimedBy {
			return fmt.Errorf("%w: job %s is not actively claimed by %q", core.ErrLeaseExpired, jobID, claimedBy)
		}

		br, err := tx.Branch(ctx, job.Tailor.BranchID)
		if err != nil {
			return err
		}
		if job.Tailor.BranchEpoch != br.Epoch {
			return fmt.Errorf("%w: branch configuration or active version changed while job %s was processing", core.ErrConflict, jobID)
		}
		existingVer, err := tx.Version(ctx, job.Tailor.VersionID)
		if err != nil {
			return err
		}
		if existingVer.Status == core.VersionStatusFailed || existingVer.Status == core.VersionStatusCancelled {
			return fmt.Errorf("%w: cannot complete terminal version %s in %s state", core.ErrConflict, existingVer.ID, existingVer.Status)
		}
		if br.ActiveVersionID != nil && *br.ActiveVersionID != "" && *br.ActiveVersionID != existingVer.ID {
			activeVer, aErr := tx.Version(ctx, *br.ActiveVersionID)
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
		job, err := tx.Job(ctx, jobID)
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
// BranchVersion to failed, prunes transient PDF RawText, and idempotently releases any pending quota reservation.
func (j *Jobs) FailClaimed(ctx context.Context, req core.FailClaimedRequest) error {
	if strings.TrimSpace(req.JobID) == "" || strings.TrimSpace(req.ClaimedBy) == "" {
		return fmt.Errorf("%w: job id and claimed_by are required", core.ErrValidation)
	}

	return j.store.backend.Write(ctx, func(tx Tx) error {
		job, err := tx.Job(ctx, req.JobID)
		if err != nil {
			return err
		}
		if job.Status != core.JobStatusProcessing || job.ClaimedBy != req.ClaimedBy {
			return fmt.Errorf("%w: job %s is not actively claimed by %q", core.ErrLeaseExpired, req.JobID, req.ClaimedBy)
		}
		var ver *core.BranchVersion
		if job.Kind == core.JobKindBranchTailor && job.Tailor != nil && job.Tailor.VersionID != "" {
			ver, err = tx.Version(ctx, job.Tailor.VersionID)
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				return err
			}
		}

		now := j.store.now()
		var ledger *quotaTxLedger
		if req.RefundQuota && job.ReservationID != "" {
			ledger = newQuotaTxLedger(now)
			if err := ledger.loadPendingReleaseTargets(ctx, tx, []*core.Job{job}); err != nil {
				return err
			}
			ledger.release(job.ReservationID, req.ErrMsg, req.Telemetry)
			if err := ledger.flush(ctx, tx); err != nil {
				return err
			}
		}

		failJobAndVersion(job, ver, core.JobStatusFailed, core.VersionStatusFailed, req.ErrMsg, now, false)
		if ver != nil {
			if err := tx.PutVersion(ctx, ver); err != nil {
				return err
			}
		}
		return tx.PutJob(ctx, job)
	})
}

// ExpirePending atomically fails expired pending jobs at now, settles their quota reservations,
// and returns the count of transitioned jobs.
func (j *Jobs) ExpirePending(ctx context.Context, now time.Time, reason string) (int, error) {
	return j.sweepJobs(ctx, now, reason, true, func(item *core.Job, ts time.Time) bool {
		return item.Status == core.JobStatusPending && !isPendingUnexpired(item.ExpiresAt, ts)
	})
}

// FailExpiredLeases atomically fails processing jobs whose worker lease has expired at now,
// settles their quota reservations, and returns the count of transitioned jobs.
func (j *Jobs) FailExpiredLeases(ctx context.Context, now time.Time, reason string) (int, error) {
	return j.sweepJobs(ctx, now, reason, false, func(item *core.Job, ts time.Time) bool {
		return item.Status == core.JobStatusProcessing && !isLeaseUnexpired(item.LeaseExpiresAt, ts)
	})
}

// RecoverInterrupted atomically fails in-flight processing jobs and expired pending jobs following restart,
// settles their quota reservations, preserves unexpired pending work, and returns the count of transitioned jobs.
func (j *Jobs) RecoverInterrupted(ctx context.Context, reason string) (int, error) {
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
		statuses := []core.JobStatus{
			core.JobStatusCompleted,
			core.JobStatusFailed,
			core.JobStatusCancelled,
		}
		jobs, err := tx.ListJobsByUser(ctx, userID, statuses)
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

// sweepJobs atomically transitions every active job matching predicate to failed and settles held quota reservations.
func (j *Jobs) sweepJobs(
	ctx context.Context,
	explicitNow time.Time,
	reason string,
	clearClaim bool,
	match func(*core.Job, time.Time) bool,
) (int, error) {
	var transitioned int
	err := j.store.backend.Write(ctx, func(tx Tx) error {
		transitioned = 0
		ts := j.store.resolveNow(explicitNow)
		activeJobs, err := tx.ListActiveJobs(ctx)
		if err != nil {
			return err
		}

		// Read every referenced BranchVersion and pending UsageRecord before performing writes.
		versionsByID := make(map[string]*core.BranchVersion)
		var matched []*core.Job
		for i := range activeJobs {
			if !match(&activeJobs[i], ts) {
				continue
			}
			cp := cloneValue(activeJobs[i])
			matched = append(matched, &cp)
			if cp.Kind == core.JobKindBranchTailor && cp.Tailor != nil && cp.Tailor.VersionID != "" {
				ver, err := tx.Version(ctx, cp.Tailor.VersionID)
				if err != nil && !errors.Is(err, core.ErrNotFound) {
					return err
				}
				if ver != nil {
					versionsByID[ver.ID] = ver
				}
			}
		}

		ledger := newQuotaTxLedger(ts)
		if err := ledger.loadPendingReleaseTargets(ctx, tx, matched); err != nil {
			return err
		}
		for _, mj := range matched {
			var linkedVer *core.BranchVersion
			if mj.Kind == core.JobKindBranchTailor && mj.Tailor != nil {
				linkedVer = versionsByID[mj.Tailor.VersionID]
			}
			failJobAndVersion(mj, linkedVer, core.JobStatusFailed, core.VersionStatusFailed, reason, ts, clearClaim)
			if mj.ReservationID != "" {
				ledger.release(mj.ReservationID, reason, core.LLMTelemetry{})
			}
		}
		if err := ledger.flush(ctx, tx); err != nil {
			return err
		}

		for _, mj := range matched {
			if mj.Kind == core.JobKindBranchTailor && mj.Tailor != nil {
				if linkedVer := versionsByID[mj.Tailor.VersionID]; linkedVer != nil {
					if err := tx.PutVersion(ctx, linkedVer); err != nil {
						return err
					}
				}
			}
			if err := tx.PutJob(ctx, mj); err != nil {
				return err
			}
		}
		transitioned = len(matched)
		return nil
	})
	return transitioned, err
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

// failJobAndVersion marks job and optional linked BranchVersion terminal and prunes PDF RawText.
func failJobAndVersion(
	job *core.Job,
	ver *core.BranchVersion,
	jobStatus core.JobStatus,
	verStatus core.VersionStatus,
	reason string,
	now time.Time,
	clearClaim bool,
) {
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
