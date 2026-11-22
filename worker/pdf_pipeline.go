package worker

import (
	"context"
	"errors"
	"strings"

	"github.com/resumeee-app/core"
)

// defaultPDFExtractionTokens is committed when an extractor succeeds with zero reported token usage.
const defaultPDFExtractionTokens = 1000

// safeProcessPDFJob executes a claimed PDF parse job with panic recovery to prevent worker goroutines
// from terminating and to guarantee slot and quota cleanup.
func (p *Pool) safeProcessPDFJob(job *core.Job) {
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
			p.failClaimedJob(p.jobDescriptor(job, ""), "An unexpected internal error occurred while parsing the resume.", true, core.LLMTelemetry{})
		}
	}()

	p.processClaimedPDFJob(job)
}

// processClaimedPDFJob coordinates asynchronous LLM parsing of persisted raw resume text into JSONResume.
func (p *Pool) processClaimedPDFJob(job *core.Job) {
	if p.store == nil || job == nil {
		return
	}

	attemptCtx, attemptCancel := context.WithCancel(p.jobCtx)
	defer attemptCancel()

	claimedBy := job.ClaimedBy
	if claimedBy == "" {
		claimedBy = p.workerID
	}
	desc := p.jobDescriptor(job, claimedBy)

	stopHeartbeat := p.startLeaseHeartbeat(attemptCtx, attemptCancel, job.ID, claimedBy, core.JobKindPDFParse)
	defer stopHeartbeat()

	if job.ExpiresAt != nil && !job.ExpiresAt.After(p.now()) {
		stopHeartbeat()
		p.failClaimedJob(desc, core.ErrJobExpired.Error(), true, core.LLMTelemetry{})
		return
	}

	if job.PDF == nil || strings.TrimSpace(job.PDF.RawText) == "" {
		stopHeartbeat()
		p.failClaimedJob(desc, "Document validation failed: resume raw text cannot be empty.", true, core.LLMTelemetry{})
		return
	}

	if p.resumeExtractor == nil {
		stopHeartbeat()
		p.failClaimedJob(desc, "Resume extraction service is unavailable.", true, core.LLMTelemetry{})
		return
	}

	extractCtx, extractCancel := p.withLLMTimeout(attemptCtx)
	extracted, telemetry, err := p.resumeExtractor.ExtractResume(extractCtx, job.PDF.RawText, job.PDF.Provider, job.PDF.Model)
	extractCancel()
	telemetry = core.ResolveTelemetry(telemetry, err)

	if err != nil {
		stopHeartbeat()
		p.routeAttemptError(attemptCtx, desc, err, telemetry, "Resume extraction interrupted by worker shutdown.", classifyPDFExtractionError)
		return
	}
	if attemptCtx.Err() != nil {
		stopHeartbeat()
		if p.isShuttingDown() {
			p.failClaimedJob(desc, "Resume extraction interrupted by worker shutdown.", true, core.LLMTelemetry{})
		} else {
			p.failClaimedJob(desc, core.ErrLeaseExpired.Error(), true, core.LLMTelemetry{})
		}
		return
	}
	if extracted == nil {
		stopHeartbeat()
		if p.logger != nil {
			p.logger.Error("pdf resume extraction returned empty result", "job_id", job.ID, "worker_id", claimedBy)
		}
		p.failClaimedJob(desc, "Resume extraction returned an empty result.", true, telemetry)
		return
	}

	// Commit consumed LLM usage immediately upon successful extraction so downstream persistence failures cannot refund it.
	telemetry.Usage = telemetry.Usage.WithFallback(defaultPDFExtractionTokens)
	p.commitUsageDetached(job.ReservationID, telemetry)

	stopHeartbeat()
	if err := p.store.CompletePDFParse(attemptCtx, job.ID, claimedBy, extracted); err != nil {
		if p.logger != nil {
			p.logger.Error("failed to complete claimed pdf job",
				"job_id", job.ID,
				"worker_id", claimedBy,
				"error", err,
			)
		}
		p.failClaimedJob(desc, "Failed to save extracted resume.", false, core.LLMTelemetry{})
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
