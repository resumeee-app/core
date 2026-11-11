package worker

import (
	"context"
	"errors"
	"time"

	"github.com/resumeee-app/resumeee-core/domain"
	"github.com/resumeee-app/resumeee-core/llm"
)

// safeProcessPDFJob executes a PDF parse task with panic recovery to prevent worker goroutines from terminating
// and to guarantee that the offending job is transitioned to a failed state.
func (p *Pool) safeProcessPDFJob(ctx context.Context, task PDFParseJobTask) {
	defer func() {
		if r := recover(); r != nil {
			if p.logger != nil {
				p.logger.Error("panic recovered during pdf job execution",
					"job_id", task.JobID,
					"panic", r,
				)
			}
			p.failPDFJob(ctx, task.JobID, "An unexpected internal error occurred while parsing the resume")
		}
	}()

	p.processPDFJob(ctx, task)
}

// processPDFJob coordinates the asynchronous LLM parsing of raw extracted resume text into structured JSONResume.
func (p *Pool) processPDFJob(ctx context.Context, task PDFParseJobTask) {
	if p.pdfJobRepo == nil {
		return
	}

	// 1. Transition job status to processing
	if err := p.pdfJobRepo.UpdateJobStatus(ctx, task.JobID, domain.PDFParseJobStatusProcessing, nil, ""); err != nil {
		if p.logger != nil {
			p.logger.Error("failed to update pdf job status to processing", "job_id", task.JobID, "err", err)
		}
		return
	}

	if p.resumeExtractor == nil {
		p.failPDFJob(ctx, task.JobID, "no resume extractor configured")
		return
	}

	// 2. Bound LLM extraction execution by configured LLMTimeout (defaulting to 10 minutes)
	timeout := p.llmTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	extractCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	extracted, usage, err := llm.ExtractResumeWithTokenUsage(extractCtx, p.resumeExtractor, task.RawText, task.Provider, task.Model, 1000)
	if err != nil {
		if p.logger != nil {
			p.logger.Error("pdf resume extraction failed", "job_id", task.JobID, "error", err)
		}
		userErrMsg := "Failed to extract resume from document. Please ensure the PDF contains valid text."
		if errors.Is(err, context.DeadlineExceeded) {
			userErrMsg = "Resume extraction timed out after 10 minutes. Please try again with a shorter document."
		} else if errors.Is(err, domain.ErrValidation) {
			userErrMsg = "Document validation failed during extraction: " + err.Error()
		}
		if p.quotaRepo != nil && task.ReservationID != "" {
			_ = p.quotaRepo.ReleaseQuota(ctx, task.ReservationID, userErrMsg)
		}
		p.failPDFJob(ctx, task.JobID, userErrMsg)
		return
	}

	if p.quotaRepo != nil && task.ReservationID != "" {
		_ = p.quotaRepo.CommitUsage(ctx, task.ReservationID, usage)
	}

	// 3. Mark job completed with candidate JSONResume result
	if err := p.pdfJobRepo.UpdateJobStatus(ctx, task.JobID, domain.PDFParseJobStatusCompleted, extracted, ""); err != nil {
		if p.logger != nil {
			p.logger.Error("failed to update pdf job status to completed", "job_id", task.JobID, "err", err)
		}
	}
}

// failPDFJob records a terminal failure status and diagnostic error message for a PDF parse job.
// Uses a detached timeout context to guarantee persistence even if parent context was cancelled.
func (p *Pool) failPDFJob(ctx context.Context, jobID, reason string) {
	if p.pdfJobRepo == nil {
		return
	}
	failCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.pdfJobRepo.UpdateJobStatus(failCtx, jobID, domain.PDFParseJobStatusFailed, nil, reason); err != nil {
		if p.logger != nil {
			p.logger.Error("failed to record pdf job failure", "job_id", jobID, "err", err)
		}
	}
}
