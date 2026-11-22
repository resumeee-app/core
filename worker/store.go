package worker

import (
	"context"
	"time"

	"github.com/resumeee-app/core"
)

// Store defines the cohesive persistence and quota contract required by worker Pool and PDFCoordinator.
type Store interface {
	core.QuotaManager
	Branch(ctx context.Context, userID, id string) (*core.Branch, error)
	Version(ctx context.Context, branchID, versionID string) (*core.BranchVersion, error)
	Profile(ctx context.Context, userID string) (*core.Profile, error)
	Snapshot(ctx context.Context, userID string) (*core.MainProfileSnapshot, error)
	Preset(ctx context.Context, userID, id string) (*core.Preset, error)
	Template(ctx context.Context, userID, id string) (*core.TemplateSet, error)
	SubmitPDFParse(ctx context.Context, job *core.Job, opts core.PendingJobOptions) error
	Job(ctx context.Context, userID, jobID string) (*core.Job, error)
	ClaimNext(ctx context.Context, req core.ClaimJobRequest) (*core.Job, error)
	RenewLease(ctx context.Context, jobID, claimedBy string, leaseExpiresAt time.Time) error
	RequeueForRetry(ctx context.Context, jobID, claimedBy string, nextRetryAt time.Time, errMsg string) error
	CompleteBranchTailor(ctx context.Context, jobID, claimedBy string, completed *core.BranchVersion) error
	CompletePDFParse(ctx context.Context, jobID, claimedBy string, result *core.JSONResume) error
	FailClaimed(ctx context.Context, req core.FailClaimedRequest) error
	ExpirePending(ctx context.Context, now time.Time, reason string) (int, error)
	FailExpiredLeases(ctx context.Context, now time.Time, reason string) (int, error)
	RecoverInterrupted(ctx context.Context, reason string) (int, error)
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
