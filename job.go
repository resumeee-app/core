package core

import (
	"context"
	"time"
)

// GenerationJob encapsulates the parameters of an asynchronous resume generation task.
type GenerationJob struct {
	UserID        string `json:"user_id"`
	BranchID      string `json:"branch_id"`
	VersionID     string `json:"version_id"`
	ReservationID string `json:"reservation_id,omitempty"`
}

// PDFParseJobTask encapsulates the parameters of an asynchronous PDF resume parsing task.
type PDFParseJobTask struct {
	JobID         string `json:"job_id"`
	UserID        string `json:"user_id"`
	RawText       string `json:"raw_text"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	ReservationID string `json:"reservation_id,omitempty"`
}

// PDFJobEnqueuer defines the contract for enqueuing PDF parse tasks into the worker pool.
type PDFJobEnqueuer interface {
	EnqueuePDFJob(task PDFParseJobTask) error
}

// PDFParseJobStatus represents the asynchronous lifecycle state of a PDF resume parse operation.
type PDFParseJobStatus string

// Lifecycle states for asynchronous PDF resume parse jobs.
const (
	// PDFParseJobStatusPending indicates the job is queued for parsing.
	PDFParseJobStatusPending PDFParseJobStatus = "pending"

	// PDFParseJobStatusProcessing indicates the job is actively being processed by an LLM worker.
	PDFParseJobStatusProcessing PDFParseJobStatus = "processing"

	// PDFParseJobStatusCompleted indicates parsing finished successfully and candidate result is available.
	PDFParseJobStatusCompleted PDFParseJobStatus = "completed"

	// PDFParseJobStatusFailed indicates parsing terminated with an error.
	PDFParseJobStatusFailed PDFParseJobStatus = "failed"
)

// PDFParseJob tracks the status and candidate extraction result of an asynchronous PDF resume parse task.
type PDFParseJob struct {
	ID           string            `json:"id"`
	UserID       string            `json:"user_id"`
	Status       PDFParseJobStatus `json:"status"`
	Filename     string            `json:"filename,omitempty"`
	Provider     string            `json:"provider,omitempty"`
	Model        string            `json:"model,omitempty"`
	Result       *JSONResume       `json:"result,omitempty"`
	ErrorMessage string            `json:"error_message,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	CompletedAt  *time.Time        `json:"completed_at,omitempty"`
}

// PDFJobStore defines persistence operations for asynchronous PDF parsing jobs.
type PDFJobStore interface {
	// CreateJob inserts a new PDF parse job record in pending state.
	CreateJob(ctx context.Context, job *PDFParseJob) error

	// Job retrieves a PDF parse job by ID scoped to the authenticated user.
	Job(ctx context.Context, userID, jobID string) (*PDFParseJob, error)

	// UpdateJobStatus updates the status, result, and error message of a PDF parse job.
	UpdateJobStatus(ctx context.Context, jobID string, status PDFParseJobStatus, result *JSONResume, errMsg string) error

	// FailInterruptedJobs marks all pending or processing jobs as failed following a server restart.
	FailInterruptedJobs(ctx context.Context, reason string) error

	// CleanupCompletedJobs purges completed or failed PDF parse jobs older than the retention threshold.
	CleanupCompletedJobs(ctx context.Context, userID string, olderThan time.Duration) error
}
