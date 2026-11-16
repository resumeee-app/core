package worker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/resumeee-app/core"
)

// claimedJobDescriptor normalizes lifecycle metadata for an active claimed core.Job.
type claimedJobDescriptor struct {
	kind          core.JobKind
	id            string
	claimedBy     string
	reservationID string
	attemptCount  int
	expiresAt     *time.Time
}

// jobDescriptor extracts normalized lifecycle metadata and ownership for job.
func (p *Pool) jobDescriptor(job *core.Job, claimedBy string) claimedJobDescriptor {
	if job == nil {
		return claimedJobDescriptor{}
	}
	if claimedBy == "" {
		claimedBy = job.ClaimedBy
	}
	if claimedBy == "" {
		claimedBy = p.workerID
	}
	return claimedJobDescriptor{
		kind:          job.Kind,
		id:            job.ID,
		claimedBy:     claimedBy,
		reservationID: job.ReservationID,
		attemptCount:  job.AttemptCount,
		expiresAt:     job.ExpiresAt,
	}
}

// isLeaseLossOrMissing reports whether err indicates the worker lost lease ownership or the target record no longer exists.
func isLeaseLossOrMissing(err error) bool {
	return errors.Is(err, core.ErrLeaseExpired) ||
		errors.Is(err, core.ErrConflict) ||
		errors.Is(err, core.ErrNotFound)
}

// routeAttemptError handles shutdown abort, lease-loss cancellation, bounded transient retry requeuing, or terminal failure with quota refund.
func (p *Pool) routeAttemptError(
	attemptCtx context.Context,
	job claimedJobDescriptor,
	err error,
	telemetry core.LLMTelemetry,
	shutdownMsg string,
	mapUserErr func(error, core.ErrorClass) string,
) {
	if p.logger != nil {
		p.logger.Error("claimed job attempt failed",
			"kind", job.kind,
			"job_id", job.id,
			"worker_id", job.claimedBy,
			"attempt", job.attemptCount,
			"error", err,
		)
	}

	if p.isShuttingDown() {
		p.failClaimedJob(job, shutdownMsg, true, core.LLMTelemetry{})
		return
	}
	if attemptCtx.Err() != nil {
		p.failClaimedJob(job, core.ErrLeaseExpired.Error(), true, core.LLMTelemetry{})
		return
	}

	errClass := core.ClassifyError(err)
	userErrMsg := mapUserErr(err, errClass)

	if errClass == core.ErrorClassTransient && p.jobs != nil {
		attempt := job.attemptCount
		if attempt <= 0 {
			attempt = 1
		}
		if attempt < p.maxAttempts {
			delay := computeRetryDelay(attempt, p.retryBaseDelay, p.maxRetryDelay)
			nextRetryAt := p.now().Add(delay)
			if job.expiresAt == nil || nextRetryAt.Before(*job.expiresAt) {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), p.cleanupDuration())
				reqErr := p.jobs.RequeueForRetry(cleanupCtx, job.id, job.claimedBy, nextRetryAt, userErrMsg)
				cancel()

				if reqErr == nil {
					p.scheduleRetryTimer(delay)
					return
				}
				if isLeaseLossOrMissing(reqErr) {
					if p.logger != nil {
						p.logger.Warn("skipping retry requeue after lease loss",
							"kind", job.kind,
							"job_id", job.id,
							"err", reqErr,
						)
					}
					return
				}
				if p.logger != nil {
					p.logger.Warn("failed to requeue claimed job for retry",
						"kind", job.kind,
						"job_id", job.id,
						"err", reqErr,
					)
				}
			}
		}
	}

	p.failClaimedJob(job, userErrMsg, true, telemetry)
}

// failClaimedJob records terminal failure for a claimed job using a bounded detached context and optionally releases reserved quota.
func (p *Pool) failClaimedJob(job claimedJobDescriptor, errMsg string, refundQuota bool, telemetry core.LLMTelemetry) {
	if job.id == "" || p.jobs == nil {
		return
	}
	if p.logger != nil {
		p.logger.Error("claimed job failed",
			"kind", job.kind,
			"job_id", job.id,
			"worker_id", job.claimedBy,
			"refund_quota", refundQuota,
			"error", errMsg,
		)
	}

	cleanupCtx, cancel := context.WithTimeout(context.Background(), p.cleanupDuration())
	defer cancel()

	if err := p.jobs.FailClaimed(cleanupCtx, job.id, job.claimedBy, errMsg); err != nil {
		if p.logger != nil && !isLeaseLossOrMissing(err) {
			p.logger.Error("failed to record claimed job failure in store",
				"kind", job.kind,
				"job_id", job.id,
				"error", err,
			)
		}
		return
	}
	if refundQuota {
		p.releaseJobQuota(cleanupCtx, job.reservationID, errMsg, telemetry)
	}
}

// commitUsageDetached commits token usage and LLM call telemetry for reservationID using a bounded detached cleanup context.
func (p *Pool) commitUsageDetached(reservationID string, telemetry core.LLMTelemetry) {
	if p.quotas == nil || reservationID == "" {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), p.cleanupDuration())
	defer cancel()
	if err := p.quotas.CommitUsage(cleanupCtx, reservationID, telemetry.Normalize()); err != nil && p.logger != nil {
		p.logger.Warn("failed to commit quota usage", "reservation_id", reservationID, "err", err)
	}
}

// releaseJobQuota releases reserved tenant quota units alongside optional failure telemetry when a job fails prior to commitment.
func (p *Pool) releaseJobQuota(ctx context.Context, reservationID, reason string, telemetry core.LLMTelemetry) {
	if p.quotas == nil || reservationID == "" {
		return
	}
	if err := p.quotas.ReleaseQuota(ctx, reservationID, reason, telemetry.Normalize()); err != nil && p.logger != nil {
		p.logger.Warn("failed to release job quota", "reservation_id", reservationID, "reason", reason, "err", err)
	}
}

// startLeaseHeartbeat periodically renews a claimed job lease and cancels attemptCancel if ownership is lost.
func (p *Pool) startLeaseHeartbeat(
	parentCtx context.Context,
	attemptCancel context.CancelFunc,
	jobID string,
	claimedBy string,
	kind core.JobKind,
) func() {
	interval := p.leaseRenewInterval
	if interval <= 0 {
		interval = p.leaseDuration / 3
	}
	if interval <= 0 || p.jobs == nil {
		return func() {}
	}

	stopCh := make(chan struct{})
	done := make(chan struct{})
	var stopOnce sync.Once

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-parentCtx.Done():
				return
			case <-ticker.C:
				renewCtx, cancel := context.WithTimeout(parentCtx, p.cleanupDuration())
				err := p.jobs.RenewLease(renewCtx, jobID, claimedBy, p.now().Add(p.leaseDuration))
				cancel()
				if err != nil {
					if isLeaseLossOrMissing(err) {
						if p.logger != nil {
							p.logger.Warn("lost worker lease during execution; cancelling attempt",
								"kind", kind,
								"id", jobID,
								"worker_id", p.workerID,
								"err", err,
							)
						}
						if attemptCancel != nil {
							attemptCancel()
						}
						return
					}
					if p.logger != nil && parentCtx.Err() == nil {
						p.logger.Warn("failed to renew worker lease",
							"kind", kind,
							"id", jobID,
							"worker_id", p.workerID,
							"err", err,
						)
					}
				}
			}
		}
	}()

	return func() {
		stopOnce.Do(func() {
			close(stopCh)
		})
		<-done
	}
}

// computeRetryDelay calculates overflow-safe exponential backoff capped at maxDelay for 1-based attempt.
func computeRetryDelay(attempt int, base, maxDelay time.Duration) time.Duration {
	if base <= 0 {
		base = defaultRetryBaseDelay
	}
	if maxDelay <= 0 {
		maxDelay = defaultMaxRetryDelay
	}
	delay := base
	for i := 1; i < attempt; i++ {
		if delay >= maxDelay || delay > maxDelay/2 {
			return maxDelay
		}
		delay *= 2
	}
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}
