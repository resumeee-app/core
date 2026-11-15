package worker

import (
	"context"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// BranchStore defines branch and version persistence operations needed by the worker pool.
type BranchStore interface {
	Branch(ctx context.Context, userID, id string) (*core.Branch, error)
	Version(ctx context.Context, branchID, versionID string) (*core.BranchVersion, error)
	UpdateVersionStatus(ctx context.Context, versionID string, status core.VersionStatus, errMsg string) error
	CompleteVersion(ctx context.Context, version *core.BranchVersion) error
	FailInterruptedVersions(ctx context.Context, reason string) error
	ClaimNextVersion(ctx context.Context, req core.ClaimJobRequest) (*core.BranchVersion, error)
	RenewVersionLease(ctx context.Context, versionID, claimedBy string, leaseExpiresAt time.Time) error
	RequeueVersionForRetry(ctx context.Context, versionID, claimedBy string, nextRetryAt time.Time, errMsg string) error
	FailClaimedVersion(ctx context.Context, versionID, claimedBy, errMsg string) error
	ExpirePendingVersions(ctx context.Context, now time.Time, reason string) ([]core.ReleasedReservation, error)
	FailExpiredVersionLeases(ctx context.Context, now time.Time, reason string) ([]core.ReleasedReservation, error)
	RecoverInterruptedVersions(ctx context.Context, reason string) ([]core.ReleasedReservation, error)
}

// ProfileReader retrieves candidate profile roots and snapshots for job execution.
type ProfileReader interface {
	Profile(ctx context.Context, userID string) (*core.Profile, error)
	Snapshot(ctx context.Context, userID string) (*core.MainProfileSnapshot, error)
}

// PresetReader loads optional preset instructions referenced by a branch.
type PresetReader interface {
	Preset(ctx context.Context, userID, id string) (*core.Preset, error)
}

// TemplateReader loads template sets required to render completed versions.
type TemplateReader interface {
	Template(ctx context.Context, userID, id string) (*core.TemplateSet, error)
}

// PDFJobStore persists asynchronous PDF parse job status updates and recovery state.
type PDFJobStore interface {
	SubmitPendingJob(ctx context.Context, job *core.PDFParseJob, opts core.PendingJobOptions) error
	Job(ctx context.Context, userID, jobID string) (*core.PDFParseJob, error)
	CancelJob(ctx context.Context, userID, jobID, reason string) (*core.PDFParseJob, error)
	UpdateJobStatus(ctx context.Context, jobID string, status core.PDFParseJobStatus, result *core.JSONResume, errMsg string) error
	FailInterruptedJobs(ctx context.Context, reason string) error
	ClaimNextJob(ctx context.Context, req core.ClaimJobRequest) (*core.PDFParseJob, error)
	RenewJobLease(ctx context.Context, jobID, claimedBy string, leaseExpiresAt time.Time) error
	RequeueJobForRetry(ctx context.Context, jobID, claimedBy string, nextRetryAt time.Time, errMsg string) error
	CompleteClaimedJob(ctx context.Context, jobID, claimedBy string, result *core.JSONResume) error
	FailClaimedJob(ctx context.Context, jobID, claimedBy, errMsg string) error
	ExpirePendingJobs(ctx context.Context, now time.Time, reason string) ([]core.ReleasedReservation, error)
	FailExpiredJobLeases(ctx context.Context, now time.Time, reason string) ([]core.ReleasedReservation, error)
	RecoverInterruptedJobs(ctx context.Context, reason string) ([]core.ReleasedReservation, error)
}

// QuotaCommitter finalizes or releases tenant LLM quota reservations after job execution.
type QuotaCommitter interface {
	CommitUsage(ctx context.Context, reservationID string, usage core.TokenUsage) error
	ReleaseQuota(ctx context.Context, reservationID string, reason string) error
}

// ResumeExtractor parses raw resume text into a structured JSONResume using an LLM.
type ResumeExtractor interface {
	ExtractResume(ctx context.Context, text, provider, model string) (*core.JSONResume, core.TokenUsage, error)
}

// Generator executes AI-backed resume tailoring for a branch generation job.
type Generator interface {
	GenerateTailoredResume(ctx context.Context, req core.GenerationRequest) (*core.GenerationResponse, error)
}

// ProvenanceValidator validates and sanitizes generated resume content against source facts.
type ProvenanceValidator interface {
	Validate(content *core.TailoredContent, snapshot *core.MainProfileSnapshot, branch *core.Branch) (*core.ValidationResult, error)
}

// TemplateRenderer renders tailored resume content into output formats.
type TemplateRenderer interface {
	Render(ctx context.Context, tmpl *core.TemplateSet, profile *core.Profile, content *core.TailoredContent) (*core.RenderOutput, error)
}
