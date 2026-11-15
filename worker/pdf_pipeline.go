package worker

import (
	"context"
	"errors"
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
			jobID := ""
			if job != nil {
				jobID = job.ID
			}
			if p.logger != nil {
				p.logger.Error("panic recovered during pdf job execution",
					"job_id", jobID,
					"worker_id", p.workerID,
					"panic", r,
				)
			}
			p.failClaimedJob(p.pdfJobDescriptor(job, ""), "An unexpected internal error occurred while parsing the resume.", true)
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
	desc := p.pdfJobDescriptor(job, claimedBy)

	stopHeartbeat := p.startLeaseHeartbeat(attemptCtx, attemptCancel, job.ID, func(ctx context.Context, leaseExp time.Time) error {
		return p.pdfJobs.RenewJobLease(ctx, job.ID, claimedBy, leaseExp)
	}, "pdf_parse_job")
	defer stopHeartbeat()

	if job.ExpiresAt != nil && !job.ExpiresAt.After(p.now()) {
		stopHeartbeat()
		p.failClaimedJob(desc, core.ErrJobExpired.Error(), true)
		return
	}

	if strings.TrimSpace(job.RawText) == "" {
		stopHeartbeat()
		p.failClaimedJob(desc, "Document validation failed: resume raw text cannot be empty.", true)
		return
	}

	if p.resumeExtractor == nil {
		stopHeartbeat()
		p.failClaimedJob(desc, "Resume extraction service is unavailable.", true)
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
		p.routeAttemptError(attemptCtx, desc, err, "Resume extraction interrupted by worker shutdown.", classifyPDFExtractionError)
		return
	}
	if attemptCtx.Err() != nil {
		stopHeartbeat()
		if p.isShuttingDown() {
			p.failClaimedJob(desc, "Resume extraction interrupted by worker shutdown.", true)
		} else {
			p.failClaimedJob(desc, core.ErrLeaseExpired.Error(), true)
		}
		return
	}
	if extracted == nil {
		stopHeartbeat()
		if p.logger != nil {
			p.logger.Error("pdf resume extraction returned empty result", "job_id", job.ID, "worker_id", claimedBy)
		}
		p.failClaimedJob(desc, "Resume extraction returned an empty result.", true)
		return
	}

	if usage.TotalTokens == 0 {
		usage.TotalTokens = defaultPDFExtractionTokens
	}
	p.commitUsageDetached(job.ReservationID, usage)

	stopHeartbeat()
	if err := p.pdfJobs.CompleteClaimedJob(attemptCtx, job.ID, claimedBy, extracted); err != nil {
		if p.logger != nil {
			p.logger.Error("failed to complete claimed pdf job",
				"job_id", job.ID,
				"worker_id", claimedBy,
				"error", err,
			)
		}
		p.failClaimedJob(desc, "Failed to save extracted resume.", false)
		return
	}
}

// classifyPDFExtractionError maps an extraction error and class to a sanitized user-facing failure message.
func classifyPDFExtractionError(err error, class core.ErrorClass) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "Resume extraction timed out. Please try again with a shorter document."
	case errors.Is(err, core.ErrValidation):
		return "Document validation failed during extraction."
	case class == core.ErrorClassUpstreamQuota:
		return "Resume extraction could not run because upstream provider billing or quota is exhausted."
	default:
		return "Failed to extract resume from document. Please ensure the PDF contains valid text."
	}
}

// pdfJobDescriptor adapts a claimed PDFParseJob into a claimedJobDescriptor for shared lifecycle management.
func (p *Pool) pdfJobDescriptor(job *core.PDFParseJob, claimedBy string) claimedJobDescriptor {
	if job == nil {
		return claimedJobDescriptor{}
	}
	if claimedBy == "" {
		claimedBy = job.ClaimedBy
	}
	if claimedBy == "" {
		claimedBy = p.workerID
	}
	var requeueFn func(context.Context, time.Time, string) error
	var failFn func(context.Context, string) error
	if p.pdfJobs != nil {
		requeueFn = func(ctx context.Context, nextRetryAt time.Time, errMsg string) error {
			return p.pdfJobs.RequeueJobForRetry(ctx, job.ID, claimedBy, nextRetryAt, errMsg)
		}
		failFn = func(ctx context.Context, errMsg string) error {
			return p.pdfJobs.FailClaimedJob(ctx, job.ID, claimedBy, errMsg)
		}
	}
	return claimedJobDescriptor{
		kind:          "pdf_parse_job",
		id:            job.ID,
		claimedBy:     claimedBy,
		reservationID: job.ReservationID,
		attemptCount:  job.AttemptCount,
		expiresAt:     job.ExpiresAt,
		requeueFn:     requeueFn,
		failFn:        failFn,
	}
}
