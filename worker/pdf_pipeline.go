package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// defaultPDFExtractionTokens is committed when an extractor succeeds with zero reported token usage.
const defaultPDFExtractionTokens = 1000

// safeProcessPDFJob executes a claimed PDF parse job with panic recovery to prevent worker goroutines
// from terminating and to guarantee slot and quota cleanup.
func (p *Pool) safeProcessPDFJob(job *core.PDFParseJob) {
	defer func() {
		if r := recover(); r != nil {
			if p.logger != nil {
				p.logger.Error("panic recovered during pdf job execution",
					"job_id", job.ID,
					"worker_id", p.workerID,
					"panic", r,
				)
			}
			p.failAndRefundPDFJob(job, "An unexpected internal error occurred while parsing the resume")
		}
	}()

	p.processClaimedPDFJob(job)
}

// processClaimedPDFJob coordinates asynchronous LLM parsing of persisted raw resume text into JSONResume.
func (p *Pool) processClaimedPDFJob(job *core.PDFParseJob) {
	if p.pdfJobs == nil || job == nil {
		return
	}

	attemptCtx, attemptCancel := context.WithCancel(p.jobCtx)
	defer attemptCancel()

	claimedBy := job.ClaimedBy
	if claimedBy == "" {
		claimedBy = p.workerID
	}

	stopHeartbeat := p.startLeaseHeartbeat(attemptCtx, attemptCancel, job.ID, func(ctx context.Context, leaseExp time.Time) error {
		return p.pdfJobs.RenewJobLease(ctx, job.ID, claimedBy, leaseExp)
	}, "pdf_parse_job")
	defer stopHeartbeat()

	if job.ExpiresAt != nil && !job.ExpiresAt.After(p.now()) {
		stopHeartbeat()
		p.failAndRefundPDFJob(job, core.ErrJobExpired.Error())
		return
	}

	if strings.TrimSpace(job.RawText) == "" {
		stopHeartbeat()
		p.failAndRefundPDFJob(job, "Document validation failed: resume raw text cannot be empty")
		return
	}

	if p.resumeExtractor == nil {
		stopHeartbeat()
		p.failAndRefundPDFJob(job, "no resume extractor configured")
		return
	}

	timeout := p.llmTimeout
	if timeout <= 0 {
		timeout = defaultLLMJobTimeout
	}
	extractCtx, extractCancel := context.WithTimeout(attemptCtx, timeout)
	extracted, usage, err := p.resumeExtractor.ExtractResume(extractCtx, job.RawText, job.Provider, job.Model)
	extractCancel()

	if err != nil {
		stopHeartbeat()
		p.handlePDFExtractionError(attemptCtx, job, claimedBy, err)
		return
	}
	if attemptCtx.Err() != nil {
		stopHeartbeat()
		if p.isShuttingDown() {
			p.failAndRefundPDFJob(job, "Resume extraction interrupted by worker shutdown.")
		} else {
			p.failAndRefundPDFJob(job, core.ErrLeaseExpired.Error())
		}
		return
	}

	stopHeartbeat()
	if err := p.pdfJobs.CompleteClaimedJob(attemptCtx, job.ID, claimedBy, extracted); err != nil {
		p.failAndRefundPDFJob(job, fmt.Sprintf("failed to complete claimed pdf job: %v", err))
		return
	}

	if usage.TotalTokens == 0 {
		usage.TotalTokens = defaultPDFExtractionTokens
	}
	p.commitUsageDetached(job.ReservationID, usage)
}

// handlePDFExtractionError routes extraction failures to shutdown abort, fail-fast refund, or bounded retry requeue.
func (p *Pool) handlePDFExtractionError(attemptCtx context.Context, job *core.PDFParseJob, claimedBy string, err error) {
	if p.logger != nil {
		p.logger.Error("pdf resume extraction failed",
			"job_id", job.ID,
			"worker_id", claimedBy,
			"attempt", job.AttemptCount,
			"error", err,
		)
	}

	if p.isShuttingDown() {
		p.failAndRefundPDFJob(job, "Resume extraction interrupted by worker shutdown.")
		return
	}
	if attemptCtx.Err() != nil {
		p.failAndRefundPDFJob(job, core.ErrLeaseExpired.Error())
		return
	}

	errClass := core.ClassifyError(err)
	userErrMsg := classifyPDFExtractionError(err, errClass)

	if errClass == core.ErrorClassTransient {
		attempt := job.AttemptCount
		if attempt <= 0 {
			attempt = 1
		}
		if attempt < p.maxAttempts {
			delay := computeRetryDelay(attempt, p.retryBaseDelay, p.maxRetryDelay)
			nextRetryAt := p.now().Add(delay)
			if job.ExpiresAt == nil || nextRetryAt.Before(*job.ExpiresAt) {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), p.cleanupDuration())
				reqErr := p.pdfJobs.RequeueJobForRetry(cleanupCtx, job.ID, claimedBy, nextRetryAt, userErrMsg)
				cancel()

				if reqErr == nil {
					p.scheduleRetryTimer(delay)
					return
				}
				if errors.Is(reqErr, core.ErrConflict) || errors.Is(reqErr, core.ErrNotFound) {
					if p.logger != nil {
						p.logger.Warn("skipping pdf retry requeue after lease loss", "job_id", job.ID, "err", reqErr)
					}
					return
				}
			}
		}
	}

	p.failAndRefundPDFJob(job, userErrMsg)
}

// classifyPDFExtractionError maps an extraction error and class to a sanitized user-facing failure message.
func classifyPDFExtractionError(err error, class core.ErrorClass) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "Resume extraction timed out. Please try again with a shorter document."
	case errors.Is(err, core.ErrValidation):
		return "Document validation failed during extraction: " + err.Error()
	case class == core.ErrorClassUpstreamQuota:
		return "Resume extraction could not run because upstream provider billing or quota is exhausted."
	default:
		return "Failed to extract resume from document. Please ensure the PDF contains valid text."
	}
}

// failAndRefundPDFJob marks an actively claimed PDF job failed via bounded detached context and releases its quota.
func (p *Pool) failAndRefundPDFJob(job *core.PDFParseJob, reason string) {
	if job == nil || p.pdfJobs == nil {
		return
	}
	claimedBy := job.ClaimedBy
	if claimedBy == "" {
		claimedBy = p.workerID
	}

	cleanupCtx, cancel := context.WithTimeout(context.Background(), p.cleanupDuration())
	defer cancel()

	if err := p.pdfJobs.FailClaimedJob(cleanupCtx, job.ID, claimedBy, reason); err != nil {
		if p.logger != nil && !errors.Is(err, core.ErrConflict) && !errors.Is(err, core.ErrNotFound) {
			p.logger.Error("failed to record claimed pdf job failure", "job_id", job.ID, "err", err)
		}
		return
	}
	p.releaseJobQuota(cleanupCtx, job.ReservationID, reason)
}
