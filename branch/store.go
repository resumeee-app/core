package branch

import (
	"context"

	"github.com/resumeee-app/resumeee-core"
)

// Store defines branch and version persistence operations required by the branch service.
type Store interface {
	ListBranches(ctx context.Context, userID string) ([]core.Branch, error)
	Branch(ctx context.Context, userID, id string) (*core.Branch, error)
	CreateBranch(ctx context.Context, branch *core.Branch) error
	UpdateBranch(ctx context.Context, branch *core.Branch) error
	DeleteBranch(ctx context.Context, userID, id string) error
	Version(ctx context.Context, branchID, versionID string) (*core.BranchVersion, error)
	VersionByNumber(ctx context.Context, branchID string, versionNumber int) (*core.BranchVersion, error)
	VersionsByIDs(ctx context.Context, versionIDs []string) ([]core.BranchVersion, error)
	ListVersions(ctx context.Context, branchID string) ([]core.BranchVersion, error)
	SaveCompletedVersion(ctx context.Context, version *core.BranchVersion) error
	SetVersionGapReport(ctx context.Context, versionID string, report *core.GapReport) error
	SetVersionATSScore(ctx context.Context, versionID string, atsScore *core.ATSScore) error
}

// JobStore defines unified background queue operations required by the branch service.
type JobStore interface {
	SubmitBranchTailor(ctx context.Context, job *core.Job, version *core.BranchVersion, opts core.PendingJobOptions) ([]core.ReleasedReservation, error)
	SupersedeBranchTailor(ctx context.Context, branchID, reason string) ([]core.ReleasedReservation, error)
	AbortBranchJobs(ctx context.Context, branchID, reason string) ([]core.ReleasedReservation, error)
	CancelBranchVersion(ctx context.Context, userID, branchID, versionID, reason string) (*core.BranchVersion, *core.Job, error)
}

// ProfileReader retrieves candidate profile roots and snapshots.
type ProfileReader interface {
	Profile(ctx context.Context, userID string) (*core.Profile, error)
	Snapshot(ctx context.Context, userID string) (*core.MainProfileSnapshot, error)
}

// PresetReader retrieves tailoring presets for reference validation and prompt compilation.
type PresetReader interface {
	Preset(ctx context.Context, userID, id string) (*core.Preset, error)
}

// TemplateReader retrieves resume template sets for validation and rendering.
type TemplateReader interface {
	Template(ctx context.Context, userID, id string) (*core.TemplateSet, error)
}

// QuotaManager reserves, commits, and releases tenant LLM quota units and call telemetry.
type QuotaManager interface {
	ReserveQuota(ctx context.Context, userID string, op core.LLMOperationType, estimatedTokens, dailyLimit, monthlyLimit int) (*core.LLMUsageRecord, error)
	CommitUsage(ctx context.Context, reservationID string, telemetry core.LLMTelemetry) error
	ReleaseQuota(ctx context.Context, reservationID string, reason string, telemetry core.LLMTelemetry) error
}

// Generator executes AI-backed resume tailoring, gap analysis, and ATS simulations.
type Generator interface {
	GenerateTailoredResume(ctx context.Context, req core.GenerationRequest) (*core.GenerationResponse, error)
	GenerateGapAnalysis(ctx context.Context, req core.GenerationRequest) (*core.GapAnalysisResponse, error)
	SimulateATS(ctx context.Context, req core.ATSSimulationRequest) (*core.ATSSimulationResponse, error)
}

// PromptCompiler compiles sanitized LLM prompt previews without invoking a provider.
type PromptCompiler interface {
	CompilePrompt(req core.GenerationRequest) (*core.PromptPreview, error)
	CompileGapAnalysisPrompt(req core.GenerationRequest) (*core.PromptPreview, error)
	CompileATSExtractionPrompt(req core.ATSSimulationRequest) (*core.PromptPreview, error)
	CompileATSMatchingPrompt(branch core.Branch, profile *core.MainProfileSnapshot, extracted *core.ATSExtractedCandidate) (*core.PromptPreview, error)
}

// ProvenanceValidator verifies manual edits against the candidate's canonical snapshot.
type ProvenanceValidator interface {
	ValidateStrict(content *core.TailoredContent, snapshot *core.MainProfileSnapshot, branch *core.Branch) error
}

// TemplateRenderer renders tailored resume content into output formats.
type TemplateRenderer interface {
	Render(ctx context.Context, tmpl *core.TemplateSet, profile *core.Profile, content *core.TailoredContent) (*core.RenderOutput, error)
}
