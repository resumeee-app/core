package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// CreateJob persists a new asynchronous PDF parse job record.
func (s *Store) CreateJob(_ context.Context, job *core.PDFParseJob) error {
	if job == nil {
		return fmt.Errorf("%w: job cannot be nil", core.ErrValidation)
	}
	if job.ID == "" || job.UserID == "" {
		return fmt.Errorf("%w: job id and user_id are required", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	}
	s.pdfJobs[job.ID] = clone(job)
	return nil
}

// SubmitPendingJob atomically checks tenant/global pending caps, applies default expiration metadata, and stores a pending PDF job.
func (s *Store) SubmitPendingJob(_ context.Context, job *core.PDFParseJob, opts core.PendingJobOptions) error {
	if job == nil {
		return fmt.Errorf("%w: job cannot be nil", core.ErrValidation)
	}
	if job.Status != "" && job.Status != core.PDFParseJobStatusPending {
		return fmt.Errorf("%w: pending job cannot start in %s state", core.ErrValidation, job.Status)
	}
	if job.UserID == "" {
		return fmt.Errorf("%w: user id is required", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if job.ID == "" {
		job.ID = core.GenerateID("pdf")
	}
	if _, exists := s.pdfJobs[job.ID]; exists {
		return core.ErrConflict
	}

	now := s.resolveNow(opts.Now)
	if err := s.checkPendingAdmissionLocked(job.UserID, "", opts, now); err != nil {
		return err
	}

	_, _, ttl := resolvePendingLimits(opts)
	job.Status = core.PDFParseJobStatusPending
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now
	if job.ExpiresAt == nil {
		exp := now.Add(ttl)
		job.ExpiresAt = &exp
	}
	job.ClaimedBy = ""
	job.ClaimedAt = nil
	job.LeaseExpiresAt = nil
	job.CompletedAt = nil

	s.pdfJobs[job.ID] = clone(job)
	return nil
}

// Job fetches a PDF parse job belonging to userID by jobID.
func (s *Store) Job(_ context.Context, userID, jobID string) (*core.PDFParseJob, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}
	if jobID == "" {
		return nil, fmt.Errorf("%w: job id cannot be empty", core.ErrValidation)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	job, ok := s.pdfJobs[jobID]
	if !ok || job.UserID != userID {
		return nil, core.ErrNotFound
	}
	return clone(job), nil
}

// UpdateJobStatus transitions a PDF parse job's status and records any extracted JSON Resume or error.
func (s *Store) UpdateJobStatus(_ context.Context, jobID string, status core.PDFParseJobStatus, result *core.JSONResume, errMsg string) error {
	if jobID == "" {
		return fmt.Errorf("%w: job id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.pdfJobs[jobID]
	if !ok {
		return core.ErrNotFound
	}
	if job.Status.IsTerminal() {
		return fmt.Errorf("%w: cannot transition terminal job %s from %s to %s", core.ErrConflict, jobID, job.Status, status)
	}

	now := s.now()
	job.Status = status
	job.UpdatedAt = now
	if errMsg != "" {
		job.ErrorMessage = errMsg
	}
	if result != nil {
		job.Result = clone(result)
	}
	if status.IsTerminal() {
		job.RawText = ""
		job.CompletedAt = &now
		job.LeaseExpiresAt = nil
		job.NextRetryAt = nil
	}
	return nil
}

// CancelJob cancels an owned pending PDF parse job, prunes transient input text, and returns a deep copy.
func (s *Store) CancelJob(_ context.Context, userID, jobID, reason string) (*core.PDFParseJob, error) {
	if userID == "" || jobID == "" {
		return nil, fmt.Errorf("%w: user id and job id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.pdfJobs[jobID]
	if !ok || job.UserID != userID {
		return nil, core.ErrNotFound
	}
	if job.Status != core.PDFParseJobStatusPending {
		return nil, fmt.Errorf("%w: cannot cancel job in %s state", core.ErrConflict, job.Status)
	}

	s.transitionPDFJobTerminalLocked(job, core.PDFParseJobStatusCancelled, reason, s.now(), true)
	return clone(job), nil
}

// ClaimNextJob atomically claims the fairest eligible pending PDF parse job for a worker lease.
func (s *Store) ClaimNextJob(_ context.Context, req core.ClaimJobRequest) (*core.PDFParseJob, error) {
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

	activeByTenant := s.activePDFJobCountsLocked(now)
	otherByTenant := s.siblingActiveCountLocked(req, true, now)

	var candidates []claimCandidate
	for _, job := range s.pdfJobs {
		if job.Status != core.PDFParseJobStatusPending || !isPendingUnexpired(job.ExpiresAt, now) || !isRetryReady(job.NextRetryAt, now) {
			continue
		}
		if req.MaxConcurrentPerTenant > 0 && activeByTenant[job.UserID]+otherByTenant[job.UserID] >= req.MaxConcurrentPerTenant {
			continue
		}
		candidates = append(candidates, claimCandidate{
			id:        job.ID,
			tenantID:  job.UserID,
			createdAt: job.CreatedAt,
		})
	}

	winnerID, ok := selectFairestCandidate(candidates, req.LastServedSeq)
	if !ok {
		return nil, nil
	}

	job := s.pdfJobs[winnerID]
	leaseExp := now.Add(leaseDur)
	job.Status = core.PDFParseJobStatusProcessing
	job.ClaimedBy = req.WorkerID
	job.ClaimedAt = &now
	job.LeaseExpiresAt = &leaseExp
	job.NextRetryAt = nil
	job.AttemptCount++
	job.UpdatedAt = now

	return clone(job), nil
}

// RenewJobLease extends an active processing job lease owned by claimedBy.
func (s *Store) RenewJobLease(_ context.Context, jobID, claimedBy string, leaseExpiresAt time.Time) error {
	if jobID == "" || claimedBy == "" || leaseExpiresAt.IsZero() {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.claimedPDFJobLocked(jobID, claimedBy)
	if err != nil {
		return err
	}

	exp := leaseExpiresAt.UTC()
	job.LeaseExpiresAt = &exp
	job.UpdatedAt = s.now()
	return nil
}

// RequeueJobForRetry transitions an actively claimed PDF job back to pending for retry prior to expiration.
func (s *Store) RequeueJobForRetry(_ context.Context, jobID, claimedBy string, nextRetryAt time.Time, errMsg string) error {
	if jobID == "" || claimedBy == "" || nextRetryAt.IsZero() {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.claimedPDFJobLocked(jobID, claimedBy)
	if err != nil {
		return err
	}

	retryAt := nextRetryAt.UTC()
	if job.ExpiresAt != nil && !retryAt.Before(*job.ExpiresAt) {
		return core.ErrJobExpired
	}

	job.Status = core.PDFParseJobStatusPending
	job.ErrorMessage = errMsg
	job.NextRetryAt = &retryAt
	job.ClaimedBy = ""
	job.ClaimedAt = nil
	job.LeaseExpiresAt = nil
	job.CompletedAt = nil
	job.UpdatedAt = s.now()
	return nil
}

// CompleteClaimedJob finalizes a claimed processing PDF job with its extracted JSON Resume and prunes raw input text.
func (s *Store) CompleteClaimedJob(_ context.Context, jobID, claimedBy string, result *core.JSONResume) error {
	if jobID == "" || claimedBy == "" {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.claimedPDFJobLocked(jobID, claimedBy)
	if err != nil {
		return err
	}

	if result != nil {
		job.Result = clone(result)
	}
	s.transitionPDFJobTerminalLocked(job, core.PDFParseJobStatusCompleted, "", s.now(), false)
	return nil
}

// FailClaimedJob marks an active claimed PDF job as failed and prunes transient raw input text.
func (s *Store) FailClaimedJob(_ context.Context, jobID, claimedBy, errMsg string) error {
	if jobID == "" || claimedBy == "" {
		return core.ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, err := s.claimedPDFJobLocked(jobID, claimedBy)
	if err != nil {
		return err
	}

	s.transitionPDFJobTerminalLocked(job, core.PDFParseJobStatusFailed, errMsg, s.now(), false)
	return nil
}

// ExpirePendingJobs fails pending PDF parse jobs whose ExpiresAt has passed and returns released reservations.
func (s *Store) ExpirePendingJobs(_ context.Context, now time.Time, reason string) ([]core.ReleasedReservation, error) {
	ts := s.resolveNow(now)

	s.mu.Lock()
	defer s.mu.Unlock()

	var released []core.ReleasedReservation
	for _, job := range s.pdfJobs {
		if job.Status == core.PDFParseJobStatusPending && !isPendingUnexpired(job.ExpiresAt, ts) {
			if rel, ok := s.transitionPDFJobTerminalLocked(job, core.PDFParseJobStatusFailed, reason, ts, true); ok {
				released = append(released, rel)
			}
		}
	}
	return released, nil
}

// FailExpiredJobLeases fails processing PDF parse jobs whose worker lease has expired and returns released reservations.
func (s *Store) FailExpiredJobLeases(_ context.Context, now time.Time, reason string) ([]core.ReleasedReservation, error) {
	ts := s.resolveNow(now)

	s.mu.Lock()
	defer s.mu.Unlock()

	var released []core.ReleasedReservation
	for _, job := range s.pdfJobs {
		if job.Status == core.PDFParseJobStatusProcessing && !isLeaseUnexpired(job.LeaseExpiresAt, ts) {
			if rel, ok := s.transitionPDFJobTerminalLocked(job, core.PDFParseJobStatusFailed, reason, ts, false); ok {
				released = append(released, rel)
			}
		}
	}
	return released, nil
}

// RecoverInterruptedJobs fails interrupted processing jobs and expired pending jobs while preserving unexpired pending work.
func (s *Store) RecoverInterruptedJobs(_ context.Context, reason string) ([]core.ReleasedReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ts := s.now()
	var released []core.ReleasedReservation
	for _, job := range s.pdfJobs {
		if job.Status == core.PDFParseJobStatusProcessing || (job.Status == core.PDFParseJobStatusPending && !isPendingUnexpired(job.ExpiresAt, ts)) {
			if rel, ok := s.transitionPDFJobTerminalLocked(job, core.PDFParseJobStatusFailed, reason, ts, false); ok {
				released = append(released, rel)
			}
		}
	}
	return released, nil
}

// FailInterruptedJobs marks interrupted processing jobs and expired pending jobs as failed during startup crash recovery.
func (s *Store) FailInterruptedJobs(ctx context.Context, reason string) error {
	_, err := s.RecoverInterruptedJobs(ctx, reason)
	return err
}

// CleanupCompletedJobs removes terminal PDF parse jobs older than olderThan for userID.
func (s *Store) CleanupCompletedJobs(_ context.Context, userID string, olderThan time.Duration) error {
	if userID == "" {
		return fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := s.now().Add(-olderThan)
	for id, job := range s.pdfJobs {
		if job.UserID != userID || !job.Status.IsTerminal() {
			continue
		}
		if (job.CompletedAt != nil && job.CompletedAt.Before(cutoff)) || (job.CompletedAt == nil && job.UpdatedAt.Before(cutoff)) {
			delete(s.pdfJobs, id)
		}
	}
	return nil
}

// claimedPDFJobLocked validates that jobID exists and is actively claimed by claimedBy.
func (s *Store) claimedPDFJobLocked(jobID, claimedBy string) (*core.PDFParseJob, error) {
	job, ok := s.pdfJobs[jobID]
	if !ok {
		return nil, core.ErrNotFound
	}
	if job.Status != core.PDFParseJobStatusProcessing || job.ClaimedBy != claimedBy {
		return nil, fmt.Errorf("%w: job %s is not actively claimed by %q", core.ErrConflict, jobID, claimedBy)
	}
	return job, nil
}

// transitionPDFJobTerminalLocked marks job terminal, prunes RawText, clears lease state, and emits any ReleasedReservation.
func (s *Store) transitionPDFJobTerminalLocked(job *core.PDFParseJob, status core.PDFParseJobStatus, reason string, ts time.Time, clearClaim bool) (core.ReleasedReservation, bool) {
	job.Status = status
	job.ErrorMessage = reason
	job.RawText = ""
	job.UpdatedAt = ts
	job.CompletedAt = &ts
	job.LeaseExpiresAt = nil
	job.NextRetryAt = nil
	if clearClaim {
		job.ClaimedBy = ""
		job.ClaimedAt = nil
	}
	if job.ReservationID == "" {
		return core.ReleasedReservation{}, false
	}
	return core.ReleasedReservation{
		JobID:         job.ID,
		UserID:        job.UserID,
		ReservationID: job.ReservationID,
		Reason:        reason,
	}, true
}
