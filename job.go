package core

import "time"

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

// JobKind identifies which asynchronous workload a persistent queued Job executes.
type JobKind string

// Supported persistent background job categories.
const (
	// JobKindBranchTailor executes asynchronous resume tailoring for a Branch and BranchVersion.
	JobKindBranchTailor JobKind = "branch_tailor"

	// JobKindPDFParse extracts structured JSON Resume data from uploaded PDF resume text.
	JobKindPDFParse JobKind = "pdf_parse"
)

// JobStatus represents the lifecycle state of a persistent background Job.
type JobStatus string

// Lifecycle states for persistent background jobs.
const (
	// JobStatusPending indicates the job is queued and eligible for worker claiming.
	JobStatusPending JobStatus = "pending"

	// JobStatusProcessing indicates a worker currently holds an active lease on the job.
	JobStatusProcessing JobStatus = "processing"

	// JobStatusCompleted indicates the job finished successfully.
	JobStatusCompleted JobStatus = "completed"

	// JobStatusFailed indicates the job terminated with an unrecoverable or expired error.
	JobStatusFailed JobStatus = "failed"

	// JobStatusCancelled indicates the job was cancelled or superseded prior to completion.
	JobStatusCancelled JobStatus = "cancelled"
)

// IsTerminal reports whether s represents a finished lifecycle state that cannot transition further.
func (s JobStatus) IsTerminal() bool {
	switch s {
	case JobStatusCompleted, JobStatusFailed, JobStatusCancelled:
		return true
	default:
		return false
	}
}

// BranchTailorSpec carries workload-specific inputs and fencing epoch for a branch tailoring job.
type BranchTailorSpec struct {
	BranchID    string `json:"branch_id"`
	VersionID   string `json:"version_id"`
	BranchEpoch int64  `json:"branch_epoch"`
}

// PDFParseSpec carries transient inputs, provider preferences, and extracted result for a PDF parse job.
type PDFParseSpec struct {
	Filename string      `json:"filename,omitempty"`
	Provider string      `json:"provider,omitempty"`
	Model    string      `json:"model,omitempty"`
	RawText  string      `json:"raw_text,omitempty"`
	Result   *JSONResume `json:"result,omitempty"`
}

// Job represents a persistent background execution task across all asynchronous worker workloads.
type Job struct {
	ID             string            `json:"id"`
	Kind           JobKind           `json:"kind"`
	UserID         string            `json:"user_id"`
	Status         JobStatus         `json:"status"`
	AttemptCount   int               `json:"attempt_count,omitempty"`
	ErrorMessage   string            `json:"error_message,omitempty"`
	ReservationID  string            `json:"reservation_id,omitempty"`
	ExpiresAt      *time.Time        `json:"expires_at,omitempty"`
	NextRetryAt    *time.Time        `json:"next_retry_at,omitempty"`
	ClaimedBy      string            `json:"claimed_by,omitempty"`
	ClaimedAt      *time.Time        `json:"claimed_at,omitempty"`
	LeaseExpiresAt *time.Time        `json:"lease_expires_at,omitempty"`
	Tailor         *BranchTailorSpec `json:"tailor,omitempty"`
	PDF            *PDFParseSpec     `json:"pdf,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	CompletedAt    *time.Time        `json:"completed_at,omitempty"`
}

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
type ClaimJobRequest struct {
	WorkerID               string
	Now                    time.Time
	LeaseDuration          time.Duration
	MaxConcurrentPerTenant int
	LastServedSeq          map[string]uint64
	PreferredKind          JobKind
}

// ReleasedReservation identifies a failed, expired, cancelled, or superseded job whose quota reservation should be released.
type ReleasedReservation struct {
	JobID         string
	UserID        string
	ReservationID string
	Reason        string
}
