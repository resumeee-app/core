package worker

import (
	"context"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/services/profile"
	"github.com/resumeee-app/resumeee-core/validator"
)

// GenerationJob encapsulates the parameters of an asynchronous resume generation task.
type GenerationJob = core.GenerationJob

// JobEnqueuer defines the contract for enqueuing generation jobs into the worker pool.
type JobEnqueuer = core.JobEnqueuer

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

// ResumeExtractor defines the contract for structured resume data extraction from raw text via an LLM.
type ResumeExtractor interface {
	ExtractResume(ctx context.Context, text string, provider string, model string) (*profile.JSONResume, error)
}

// LLMService abstracts multi-provider prompt generation and structured response.
type LLMService = core.LLMService

// ProvenanceValidator audits tailored content against career snapshots and exclusion lists.
type ProvenanceValidator interface {
	Validate(content *core.TailoredContent, snapshot *core.MainProfileSnapshot, branch *core.Branch) (*validator.ValidationResult, error)
}

// TemplateRenderer executes multi-format Go templates.
type TemplateRenderer interface {
	Render(ctx context.Context, tmpl *core.TemplateSet, profile *core.Profile, content *core.TailoredContent) (*core.RenderOutput, error)
}
