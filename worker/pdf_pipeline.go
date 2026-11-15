package worker

import (
	"context"
	"errors"

	"github.com/resumeee-app/resumeee-core"
)

// defaultPDFExtractionTokens is committed when an extractor succeeds with zero reported token usage.
const defaultPDFExtractionTokens = 1000

// safeProcessPDFJob executes a PDF parse task with panic recovery to prevent worker goroutines from terminating
// and to guarantee that the offending job is transitioned to a failed state.
func (p *Pool) safeProcessPDFJob(ctx context.Context, task core.PDFParseJobTask) {
	defer func() {
		if r := recover(); r != nil {
			if p.logger != nil {
				p.logger.Error("panic recovered during pdf job execution",
					"job_id", task.JobID,
					"panic", r,
				)
			}
			p.failPDFJobWithQuota(ctx, task.JobID, task.ReservationID, "An unexpected internal error occurred while parsing the resume", core.LLMTelemetry{})
		}
	}()

	p.processPDFJob(ctx, task)
}

// processPDFJob coordinates asynchronous LLM parsing of raw resume text into a structured JSONResume.
func (p *Pool) processPDFJob(ctx context.Context, task core.PDFParseJobTask) {
	if p.pdfJobs == nil {
		p.releaseJobQuota(ctx, task.ReservationID, "no PDF job store configured", core.LLMTelemetry{})
		return
	}

	if err := p.pdfJobs.UpdateJobStatus(ctx, task.JobID, core.PDFParseJobStatusProcessing, nil, ""); err != nil {
		if p.logger != nil {
			p.logger.Error("failed to update pdf job status to processing", "job_id", task.JobID, "err", err)
		}
		p.releaseJobQuota(ctx, task.ReservationID, "failed to update pdf job status to processing", core.LLMTelemetry{})
		return
	}
	if p.resumeExtractor == nil {
		p.failPDFJobWithQuota(ctx, task.JobID, task.ReservationID, "no resume extractor configured", core.LLMTelemetry{})
		return
	}

	extractCtx, cancel := p.withLLMTimeout(ctx)
	defer cancel()

	extracted, telemetry, err := p.resumeExtractor.ExtractResume(extractCtx, task.RawText, task.Provider, task.Model)
	telemetry = core.ResolveTelemetry(telemetry, err)
	if err != nil {
		if p.logger != nil {
			p.logger.Error("pdf resume extraction failed", "job_id", task.JobID, "error", err)
		}
		userErrMsg := classifyPDFExtractionError(err)
		p.failPDFJobWithQuota(ctx, task.JobID, task.ReservationID, userErrMsg, telemetry)
		return
	}
	if extracted == nil {
		userErrMsg := "Failed to extract resume from document. Please ensure the PDF contains valid text."
		p.failPDFJobWithQuota(ctx, task.JobID, task.ReservationID, userErrMsg, telemetry)
		return
	}

	if err := p.pdfJobs.UpdateJobStatus(ctx, task.JobID, core.PDFParseJobStatusCompleted, extracted, ""); err != nil {
		if p.logger != nil {
			p.logger.Error("failed to update pdf job status to completed", "job_id", task.JobID, "err", err)
		}
		p.releaseJobQuota(ctx, task.ReservationID, "Failed to save extracted resume.", telemetry)
		p.failPDFJob(ctx, task.JobID, "Failed to save extracted resume.")
		return
	}

	// Apply fallback estimation only after extraction and job completion persistence succeed.
	telemetry.Usage = telemetry.Usage.WithFallback(defaultPDFExtractionTokens)
	p.commitJobQuota(ctx, task.ReservationID, telemetry)
}

// failPDFJobWithQuota releases any pending quota reservation with telemetry before marking the PDF job failed.
func (p *Pool) failPDFJobWithQuota(ctx context.Context, jobID, reservationID, reason string, telemetry core.LLMTelemetry) {
	p.releaseJobQuota(ctx, reservationID, reason, telemetry)
	p.failPDFJob(ctx, jobID, reason)
}

// classifyPDFExtractionError maps an extraction error to a sanitized user-facing failure message.
func classifyPDFExtractionError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "Resume extraction timed out after 10 minutes. Please try again with a shorter document."
	case errors.Is(err, core.ErrValidation):
		return "Document validation failed during extraction."
	default:
		return "Failed to extract resume from document. Please ensure the PDF contains valid text."
	}
}

// failPDFJob records a terminal failure status and diagnostic error message for a PDF parse job.
func (p *Pool) failPDFJob(_ context.Context, jobID, reason string) {
	if p.pdfJobs == nil {
		return
	}
	failCtx, cancel := context.WithTimeout(context.Background(), failJobStatusTimeout)
	defer cancel()
	if err := p.pdfJobs.UpdateJobStatus(failCtx, jobID, core.PDFParseJobStatusFailed, nil, reason); err != nil {
		if p.logger != nil {
			p.logger.Error("failed to record pdf job failure", "job_id", jobID, "err", err)
		}
	}
}
