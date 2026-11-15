package worker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// claimedJobDescriptor normalizes store lifecycle callbacks and metadata across claimed BranchVersion and PDFParseJob items.
type claimedJobDescriptor struct {
	kind          string
	id            string
	claimedBy     string
	reservationID string
	attemptCount  int
	expiresAt     *time.Time
	requeueFn     func(ctx context.Context, nextRetryAt time.Time, errMsg string) error
	failFn        func(ctx context.Context, errMsg string) error
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

	if errClass == core.ErrorClassTransient && job.requeueFn != nil {
		attempt := job.attemptCount
		if attempt <= 0 {
			attempt = 1
		}
		if attempt < p.maxAttempts {
			delay := computeRetryDelay(attempt, p.retryBaseDelay, p.maxRetryDelay)
			nextRetryAt := p.now().Add(delay)
			if job.expiresAt == nil || nextRetryAt.Before(*job.expiresAt) {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), p.cleanupDuration())
				reqErr := job.requeueFn(cleanupCtx, nextRetryAt, userErrMsg)
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
	if job.id == "" || job.failFn == nil {
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

	if err := job.failFn(cleanupCtx, errMsg); err != nil {
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
	id string,
	renewFn func(ctx context.Context, leaseExpiresAt time.Time) error,
	kind string,
) func() {
	interval := p.leaseRenewInterval
	if interval <= 0 {
		interval = p.leaseDuration / 3
	}
	if interval <= 0 || renewFn == nil {
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
				err := renewFn(renewCtx, p.now().Add(p.leaseDuration))
				cancel()
				if err != nil {
					if isLeaseLossOrMissing(err) {
						if p.logger != nil {
							p.logger.Warn("lost worker lease during execution; cancelling attempt",
								"kind", kind,
								"id", id,
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
							"id", id,
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
