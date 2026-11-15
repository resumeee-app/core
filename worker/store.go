package worker

import (
	"context"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// BranchReader retrieves branch and version context required to execute tailoring jobs.
type BranchReader interface {
	Branch(ctx context.Context, userID, id string) (*core.Branch, error)
	Version(ctx context.Context, branchID, versionID string) (*core.BranchVersion, error)
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

// JobStore persists unified asynchronous queue state, claims, heartbeats, completions, and recovery.
type JobStore interface {
	SubmitPDFParse(ctx context.Context, job *core.Job, opts core.PendingJobOptions) error
	Job(ctx context.Context, userID, jobID string) (*core.Job, error)
	CancelJob(ctx context.Context, userID, jobID, reason string) (*core.Job, error)
	ClaimNext(ctx context.Context, req core.ClaimJobRequest) (*core.Job, error)
	RenewLease(ctx context.Context, jobID, claimedBy string, leaseExpiresAt time.Time) error
	RequeueForRetry(ctx context.Context, jobID, claimedBy string, nextRetryAt time.Time, errMsg string) error
	CompleteBranchTailor(ctx context.Context, jobID, claimedBy string, completed *core.BranchVersion) error
	CompletePDFParse(ctx context.Context, jobID, claimedBy string, result *core.JSONResume) error
	FailClaimed(ctx context.Context, jobID, claimedBy, errMsg string) error
	ExpirePending(ctx context.Context, now time.Time, reason string) ([]core.ReleasedReservation, error)
	FailExpiredLeases(ctx context.Context, now time.Time, reason string) ([]core.ReleasedReservation, error)
	RecoverInterrupted(ctx context.Context, reason string) ([]core.ReleasedReservation, error)
}

// QuotaCommitter finalizes or releases tenant LLM quota reservations alongside call telemetry.
type QuotaCommitter interface {
	CommitUsage(ctx context.Context, reservationID string, telemetry core.LLMTelemetry) error
	ReleaseQuota(ctx context.Context, reservationID string, reason string, telemetry core.LLMTelemetry) error
}

// ResumeExtractor parses raw resume text into a structured JSONResume using an LLM.
type ResumeExtractor interface {
	ExtractResume(ctx context.Context, text, provider, model string) (*core.JSONResume, core.LLMTelemetry, error)
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
