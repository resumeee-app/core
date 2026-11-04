// Package domain defines the core domain models, interfaces, entities, error sentinels,
// and invariants for the Resumeee resume tailoring engine.
package domain

import "context"

// ProfileRepository defines persistence operations for Tier-1 career data and atomic revision increments.
type ProfileRepository interface {
	GetProfile(ctx context.Context, userID string) (*Profile, error)
	UpsertProfile(ctx context.Context, profile *Profile) error
	IncrementMasterRevision(ctx context.Context, userID string) (int64, error)
	GetSnapshot(ctx context.Context, userID string) (*MasterProfileSnapshot, error)
	ReplaceCareerGraph(ctx context.Context, userID string, snapshot *MasterProfileSnapshot) error

	// Skills
	ListSkills(ctx context.Context, userID string) ([]Skill, error)
	GetSkill(ctx context.Context, userID, id string) (*Skill, error)
	CreateSkill(ctx context.Context, skill *Skill) error
	UpdateSkill(ctx context.Context, skill *Skill) error
	DeleteSkill(ctx context.Context, userID, id string) error

	// Work Experience
	ListExperiences(ctx context.Context, userID string) ([]WorkExperience, error)
	GetExperience(ctx context.Context, userID, id string) (*WorkExperience, error)
	CreateExperience(ctx context.Context, exp *WorkExperience) error
	UpdateExperience(ctx context.Context, exp *WorkExperience) error
	DeleteExperience(ctx context.Context, userID, id string) error

	// Projects
	ListProjects(ctx context.Context, userID string) ([]Project, error)
	GetProject(ctx context.Context, userID, id string) (*Project, error)
	CreateProject(ctx context.Context, proj *Project) error
	UpdateProject(ctx context.Context, proj *Project) error
	DeleteProject(ctx context.Context, userID, id string) error

	// Education
	ListEducation(ctx context.Context, userID string) ([]Education, error)
	GetEducation(ctx context.Context, userID, id string) (*Education, error)
	CreateEducation(ctx context.Context, edu *Education) error
	UpdateEducation(ctx context.Context, edu *Education) error
	DeleteEducation(ctx context.Context, userID, id string) error

	// Certifications
	ListCertifications(ctx context.Context, userID string) ([]Certification, error)
	GetCertification(ctx context.Context, userID, id string) (*Certification, error)
	CreateCertification(ctx context.Context, cert *Certification) error
	UpdateCertification(ctx context.Context, cert *Certification) error
	DeleteCertification(ctx context.Context, userID, id string) error

	// Facts
	ListFactsByOwner(ctx context.Context, userID, ownerType, ownerID string) ([]Fact, error)
	GetFact(ctx context.Context, userID, id string) (*Fact, error)
	CreateFact(ctx context.Context, fact *Fact) error
	UpdateFact(ctx context.Context, fact *Fact) error
	DeleteFact(ctx context.Context, userID, id string) error
}

// PresetRepository defines persistence operations for Tier-2 reusable instructions.
type PresetRepository interface {
	ListPresets(ctx context.Context, userID string) ([]Preset, error)
	GetPreset(ctx context.Context, userID, id string) (*Preset, error)
	CreatePreset(ctx context.Context, preset *Preset) error
	UpdatePreset(ctx context.Context, preset *Preset) error
	DeletePreset(ctx context.Context, userID, id string) error
}

// TemplateRepository defines persistence operations for multi-format resume templates.
type TemplateRepository interface {
	ListTemplates(ctx context.Context, userID string) ([]TemplateSet, error)
	GetTemplate(ctx context.Context, userID, id string) (*TemplateSet, error)
	CreateTemplate(ctx context.Context, tmpl *TemplateSet) error
	UpdateTemplate(ctx context.Context, tmpl *TemplateSet) error
	DeleteTemplate(ctx context.Context, userID, id string) error
}

// BranchRepository defines persistence operations for Tier-3 targeted resumes and immutable versions.
type BranchRepository interface {
	ListBranches(ctx context.Context, userID string) ([]Branch, error)
	GetBranch(ctx context.Context, userID, id string) (*Branch, error)
	CreateBranch(ctx context.Context, branch *Branch) error
	UpdateBranch(ctx context.Context, branch *Branch) error
	DeleteBranch(ctx context.Context, userID, id string) error

	// Versioning
	CreateVersion(ctx context.Context, version *BranchVersion) error
	GetVersion(ctx context.Context, branchID, versionID string) (*BranchVersion, error)
	GetVersionByNumber(ctx context.Context, branchID string, versionNumber int) (*BranchVersion, error)
	ListVersions(ctx context.Context, branchID string) ([]BranchVersion, error)
	UpdateVersionStatus(ctx context.Context, versionID string, status VersionStatus, errMsg string) error
	CompleteVersion(ctx context.Context, version *BranchVersion) error
	FailInterruptedVersions(ctx context.Context, reason string) error
}

// GenerationRequest contains full context required by an LLM provider to tailor a resume.
type GenerationRequest struct {
	Model              string
	MasterProfile      *MasterProfileSnapshot
	GlobalRules        string           // Tier 1
	PresetInstructions string           // Tier 2
	Preset             *Preset          // Tier 2 Preset metadata & overrides
	BranchContext      Branch           // Tier 3
	PriorVersion       *TailoredContent // For iterative refinements
}

// GenerationResponse wraps structured tailored content and gap analysis returned by an LLM.
type GenerationResponse struct {
	TailoredContent TailoredContent `json:"tailored_content"`
	GapReport       GapReport       `json:"gap_report"`
}

// LLMProvider abstracts interactions with generative AI model providers.
type LLMProvider interface {
	Name() string
	GenerateTailoredResume(ctx context.Context, req GenerationRequest) (*GenerationResponse, error)
}

// OutputFormat designates the targeted rendering medium.
type OutputFormat string

const (
	OutputFormatHTML      OutputFormat = "html"
	OutputFormatMarkdown  OutputFormat = "markdown"
	OutputFormatPlainText OutputFormat = "text"
)

// RenderOutput contains the rendered text artifacts across all three supported formats.
type RenderOutput struct {
	HTML      string `json:"html"`
	Markdown  string `json:"markdown"`
	PlainText string `json:"plain_text"`
}

// TemplateRenderer defines the external testing and execution seam for resume rendering.
type TemplateRenderer interface {
	// Render compiles and executes all three formats in a TemplateSet against candidate profile and tailored content.
	Render(ctx context.Context, tmpl *TemplateSet, profile *Profile, content *TailoredContent) (*RenderOutput, error)

	// RenderFormat executes a single template string targeting the designated format.
	RenderFormat(format OutputFormat, tmplStr string, data any) (string, error)

	// Validate checks that all templates within a TemplateSet parse cleanly and cite only whitelisted functions.
	Validate(tmpl *TemplateSet) error
}
