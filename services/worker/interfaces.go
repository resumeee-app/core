package worker

import (
	"context"

	"resumeee/internal/domain"
	"resumeee/internal/profile"
	"resumeee/internal/validator"
)

// GenerationJob encapsulates the parameters of an asynchronous resume generation task.
type GenerationJob struct {
	UserID    string `json:"user_id"`
	BranchID  string `json:"branch_id"`
	VersionID string `json:"version_id"`
}

// JobEnqueuer defines the contract for enqueuing generation jobs into the worker pool.
type JobEnqueuer interface {
	Enqueue(job GenerationJob) error
}

// PDFParseJobTask encapsulates the parameters of an asynchronous PDF resume parsing task.
type PDFParseJobTask struct {
	JobID    string `json:"job_id"`
	UserID   string `json:"user_id"`
	RawText  string `json:"raw_text"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// PDFJobEnqueuer defines the contract for enqueuing PDF parse tasks into the worker pool.
type PDFJobEnqueuer interface {
	EnqueuePDFJob(task PDFParseJobTask) error
}

// ResumeExtractor defines the contract for structured resume data extraction from raw text via an LLM.
type ResumeExtractor interface {
	ExtractResume(ctx context.Context, text string, provider string, model string) (*profile.JSONResume, error)
}

// LLMService abstracts multi-provider prompt generation and structured response.
type LLMService interface {
	GenerateTailoredResume(ctx context.Context, req domain.GenerationRequest) (*domain.GenerationResponse, error)
	GenerateGapAnalysis(ctx context.Context, req domain.GenerationRequest) (*domain.GapReport, error)
}

// ProvenanceValidator audits tailored content against career snapshots and exclusion lists.
type ProvenanceValidator interface {
	Validate(content *domain.TailoredContent, snapshot *domain.MasterProfileSnapshot, branch *domain.Branch) (*validator.ValidationResult, error)
}

// TemplateRenderer executes multi-format Go templates.
type TemplateRenderer interface {
	Render(ctx context.Context, tmpl *domain.TemplateSet, profile *domain.Profile, content *domain.TailoredContent) (*domain.RenderOutput, error)
}
