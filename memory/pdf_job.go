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

	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	}
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

	now := time.Now().UTC()
	job.Status = status
	job.UpdatedAt = now
	if errMsg != "" {
		job.ErrorMessage = errMsg
	}
	if result != nil {
		job.Result = clone(result)
	}
	if status == core.PDFParseJobStatusCompleted || status == core.PDFParseJobStatusFailed {
		job.CompletedAt = &now
	}
	return nil
}

// FailInterruptedJobs marks pending or processing PDF jobs failed during startup crash recovery.
func (s *Store) FailInterruptedJobs(_ context.Context, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	for _, job := range s.pdfJobs {
		if job.Status == core.PDFParseJobStatusPending || job.Status == core.PDFParseJobStatusProcessing {
			job.Status = core.PDFParseJobStatusFailed
			job.ErrorMessage = reason
			job.UpdatedAt = now
			job.CompletedAt = &now
		}
	}
	return nil
}

// CleanupCompletedJobs removes terminal PDF parse jobs older than olderThan for userID.
func (s *Store) CleanupCompletedJobs(_ context.Context, userID string, olderThan time.Duration) error {
	if userID == "" {
		return fmt.Errorf("%w: user id cannot be empty", core.ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().UTC().Add(-olderThan)
	for id, job := range s.pdfJobs {
		if job.UserID != userID {
			continue
		}
		if job.Status != core.PDFParseJobStatusCompleted && job.Status != core.PDFParseJobStatusFailed {
			continue
		}
		if (job.CompletedAt != nil && job.CompletedAt.Before(cutoff)) || (job.CompletedAt == nil && job.UpdatedAt.Before(cutoff)) {
			delete(s.pdfJobs, id)
		}
	}
	return nil
}
