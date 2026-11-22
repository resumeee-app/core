package branch

import (
	"context"

	"github.com/resumeee-app/core"
)

// Store defines the cohesive persistence and quota contract required by the branch service.
type Store interface {
	core.QuotaManager
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
	SubmitBranchTailor(ctx context.Context, job *core.Job, version *core.BranchVersion, opts core.PendingJobOptions) error
	SupersedeBranchTailor(ctx context.Context, branchID, reason string) error
	AbortBranchJobs(ctx context.Context, branchID, reason string) error
	Profile(ctx context.Context, userID string) (*core.Profile, error)
	Snapshot(ctx context.Context, userID string) (*core.MainProfileSnapshot, error)
	Preset(ctx context.Context, userID, id string) (*core.Preset, error)
	Template(ctx context.Context, userID, id string) (*core.TemplateSet, error)
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
