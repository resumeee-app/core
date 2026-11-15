package core

import (
	"context"
	"time"
)

// Default backlog, expiration, and worker lease configuration bounds.
const (
	// DefaultJobTTL bounds how long a queued job may remain pending before automatic expiration.
	DefaultJobTTL = 30 * time.Minute

	// DefaultLeaseDuration bounds how long a claimed worker lease stays valid without heartbeat renewal.
	DefaultLeaseDuration = 2 * time.Minute

	// DefaultMaxPendingPerTenant caps persisted pending jobs per tenant when not overridden.
	DefaultMaxPendingPerTenant = 25

	// DefaultMaxPDFRawTextBytes caps transient resume raw text payloads accepted for PDF jobs (1 MiB).
	DefaultMaxPDFRawTextBytes = 1 << 20
)

// JobNotifier signals background workers that new persisted pending work is available.
type JobNotifier interface {
	Notify()
}

// JobNotifierFunc adapts a plain function to satisfy JobNotifier.
type JobNotifierFunc func()

// Notify invokes the underlying notification callback when non-nil.
func (f JobNotifierFunc) Notify() {
	if f != nil {
		f()
	}
}

// PendingJobOptions configures atomic admission control, payload limits, and expiration TTL when queuing pending jobs.
type PendingJobOptions struct {
	MaxPendingPerTenant int
	MaxGlobalPending    int
	MaxRawTextBytes     int
	TTL                 time.Duration
	Now                 time.Time
}

// ClaimJobRequest configures atomic direct-store claiming, lease ownership, fairness, and tenant caps.
// Callers using a shared persistent store across branch versions and PDF jobs—or running single-workload
// pools sharing a store with other pools—should leave OtherActiveByTenant nil so the store enforces
// cross-queue concurrency directly from persisted active leases. Supply a non-nil OtherActiveByTenant
// map only when explicitly overriding sibling counts for separate non-shared stores.
type ClaimJobRequest struct {
	WorkerID               string
	Now                    time.Time
	LeaseDuration          time.Duration
	MaxConcurrentPerTenant int
	OtherActiveByTenant    map[string]int
	LastServedSeq          map[string]uint64
}

// ReleasedReservation identifies a failed, expired, cancelled, or superseded job whose quota reservation should be released.
type ReleasedReservation struct {
	JobID         string
	UserID        string
	ReservationID string
	Reason        string
}

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

	// PDFParseJobStatusCancelled indicates parsing was cancelled before execution.
	PDFParseJobStatusCancelled PDFParseJobStatus = "cancelled"
)

// IsTerminal reports whether s represents a finished PDF job state that cannot transition further.
func (s PDFParseJobStatus) IsTerminal() bool {
	switch s {
	case PDFParseJobStatusCompleted, PDFParseJobStatusFailed, PDFParseJobStatusCancelled:
		return true
	default:
		return false
	}
}

// PDFParseJob tracks the status and candidate extraction result of an asynchronous PDF resume parse task.
type PDFParseJob struct {
	ID             string            `json:"id"`
	UserID         string            `json:"user_id"`
	Status         PDFParseJobStatus `json:"status"`
	Filename       string            `json:"filename,omitempty"`
	Provider       string            `json:"provider,omitempty"`
	Model          string            `json:"model,omitempty"`
	RawText        string            `json:"raw_text,omitempty"`
	ReservationID  string            `json:"reservation_id,omitempty"`
	Result         *JSONResume       `json:"result,omitempty"`
	ErrorMessage   string            `json:"error_message,omitempty"`
	AttemptCount   int               `json:"attempt_count,omitempty"`
	ClaimedBy      string            `json:"claimed_by,omitempty"`
	ClaimedAt      *time.Time        `json:"claimed_at,omitempty"`
	LeaseExpiresAt *time.Time        `json:"lease_expires_at,omitempty"`
	NextRetryAt    *time.Time        `json:"next_retry_at,omitempty"`
	ExpiresAt      *time.Time        `json:"expires_at,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	CompletedAt    *time.Time        `json:"completed_at,omitempty"`
}

// PDFJobStore defines persistence operations for asynchronous PDF parsing jobs.
type PDFJobStore interface {
	// CreateJob inserts a new PDF parse job record in pending state.
	CreateJob(ctx context.Context, job *PDFParseJob) error

	// SubmitPendingJob atomically enforces admission caps, defaults expiration metadata, and inserts a pending PDF parse job.
	SubmitPendingJob(ctx context.Context, job *PDFParseJob, opts PendingJobOptions) error

	// Job retrieves a PDF parse job by ID scoped to the authenticated user.
	Job(ctx context.Context, userID, jobID string) (*PDFParseJob, error)

	// UpdateJobStatus updates the status, result, and error message of a PDF parse job.
	UpdateJobStatus(ctx context.Context, jobID string, status PDFParseJobStatus, result *JSONResume, errMsg string) error

	// CancelJob transitions an owned pending PDF parse job to cancelled, prunes transient input text, and returns the record.
	CancelJob(ctx context.Context, userID, jobID, reason string) (*PDFParseJob, error)

	// ClaimNextJob atomically claims the fairest eligible pending PDF parse job for a worker lease.
	ClaimNextJob(ctx context.Context, req ClaimJobRequest) (*PDFParseJob, error)

	// RenewJobLease extends an active processing job lease owned by claimedBy.
	// Implementations must return ErrNotFound if the job is missing and ErrLeaseExpired (wrapping ErrConflict)
	// or ErrConflict when the job is no longer actively processing under claimedBy.
	RenewJobLease(ctx context.Context, jobID, claimedBy string, leaseExpiresAt time.Time) error

	// RequeueJobForRetry returns a transiently failed claimed job back to pending for future retry prior to expiration.
	// Implementations must return ErrNotFound if missing, ErrLeaseExpired/ErrConflict if lease ownership is lost,
	// or ErrJobExpired when nextRetryAt meets or exceeds ExpiresAt.
	RequeueJobForRetry(ctx context.Context, jobID, claimedBy string, nextRetryAt time.Time, errMsg string) error

	// CompleteClaimedJob finalizes a claimed processing PDF job and prunes transient raw input text.
	// Implementations must return ErrNotFound if missing and ErrLeaseExpired/ErrConflict if lease ownership is lost.
	CompleteClaimedJob(ctx context.Context, jobID, claimedBy string, result *JSONResume) error

	// FailClaimedJob transitions an active claimed job to failed and prunes transient raw input text.
	// Implementations must return ErrNotFound if missing and ErrLeaseExpired/ErrConflict if lease ownership is lost.
	FailClaimedJob(ctx context.Context, jobID, claimedBy, errMsg string) error

	// ExpirePendingJobs fails pending PDF parse jobs whose ExpiresAt deadline has passed and returns released reservations.
	ExpirePendingJobs(ctx context.Context, now time.Time, reason string) ([]ReleasedReservation, error)

	// FailExpiredJobLeases fails processing PDF parse jobs whose lease has expired and returns released reservations.
	FailExpiredJobLeases(ctx context.Context, now time.Time, reason string) ([]ReleasedReservation, error)

	// RecoverInterruptedJobs fails interrupted processing jobs and expired pending jobs, preserving valid pending work.
	RecoverInterruptedJobs(ctx context.Context, reason string) ([]ReleasedReservation, error)

	// FailInterruptedJobs marks interrupted processing and expired pending jobs as failed following a server restart.
	FailInterruptedJobs(ctx context.Context, reason string) error

	// CleanupCompletedJobs purges terminal PDF parse jobs older than the retention threshold.
	CleanupCompletedJobs(ctx context.Context, userID string, olderThan time.Duration) error
}
