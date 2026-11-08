package domain

import (
	"context"
	"strings"
	"time"
)

// ProfileRepository defines persistence operations for Tier-1 career data and atomic revision increments.
type ProfileRepository interface {
	GetProfile(ctx context.Context, userID string) (*Profile, error)
	UpsertProfile(ctx context.Context, profile *Profile) error
	IncrementMainRevision(ctx context.Context, userID string) (int64, error)
	GetSnapshot(ctx context.Context, userID string) (*MainProfileSnapshot, error)
	ReplaceCareerGraph(ctx context.Context, userID string, snapshot *MainProfileSnapshot) error

	// Skills
	ListSkills(ctx context.Context, userID string) ([]Skill, error)
	ListGlobalSkills(ctx context.Context) ([]Skill, error)
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
	GetVersionsByIDs(ctx context.Context, versionIDs []string) ([]BranchVersion, error)
	ListVersions(ctx context.Context, branchID string) ([]BranchVersion, error)
	UpdateVersionStatus(ctx context.Context, versionID string, status VersionStatus, errMsg string) error
	CompleteVersion(ctx context.Context, version *BranchVersion) error
	FailInterruptedVersions(ctx context.Context, reason string) error
}

// GenerationRequest contains full context required by an LLM provider to tailor a resume.
type GenerationRequest struct {
	Model              string
	ReasoningLevel     string           // Reasoning effort constraint ("none", "low", "medium", "high")
	MainProfile        *MainProfileSnapshot
	GlobalRules        string           // Tier 1
	PresetInstructions string           // Tier 2
	Preset             *Preset          // Tier 2 Preset metadata & overrides
	TargetLanguage     string           // Target output language for contextual translation (default: "English")
	BranchContext      Branch           // Tier 3
	PriorVersion       *TailoredContent // For iterative refinements
}

// TokenUsage captures token consumption metrics returned by upstream LLM providers.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// GenerationResponse wraps structured tailored content and gap analysis returned by an LLM.
type GenerationResponse struct {
	TailoredContent TailoredContent `json:"tailored_content"`
	GapReport       GapReport       `json:"gap_report"`
	Usage           TokenUsage      `json:"usage"`
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

// PDFParseJobRepository defines persistence operations for asynchronous PDF parsing jobs.
type PDFParseJobRepository interface {
	// CreateJob inserts a new PDF parse job record in pending state.
	CreateJob(ctx context.Context, job *PDFParseJob) error

	// GetJob retrieves a PDF parse job by ID scoped to the authenticated user.
	GetJob(ctx context.Context, userID, jobID string) (*PDFParseJob, error)

	// UpdateJobStatus updates the status, result, and error message of a PDF parse job.
	UpdateJobStatus(ctx context.Context, jobID string, status PDFParseJobStatus, result any, errMsg string) error

	// FailInterruptedJobs marks all pending or processing jobs as failed following a server restart.
	FailInterruptedJobs(ctx context.Context, reason string) error

	// CleanupCompletedJobs purges completed or failed PDF parse jobs older than the retention threshold.
	CleanupCompletedJobs(ctx context.Context, userID string, olderThan time.Duration) error
}

// TokenInfo contains identity attributes verified by a TokenVerifier.
type TokenInfo struct {
	UID   string `json:"uid"`
	Email string `json:"email"`
	Admin bool   `json:"admin,omitempty"`
	Role  string `json:"role,omitempty"`
}

// IsAdmin returns true if the TokenInfo carries administrative privileges via the Admin boolean flag
// or an administrative UserRole.
func (t *TokenInfo) IsAdmin() bool {
	if t == nil {
		return false
	}
	return t.Admin || strings.EqualFold(t.Role, string(UserRoleAdmin))
}

// UserRepository defines persistence operations for tenant user accounts.
type UserRepository interface {
	GetUser(ctx context.Context, id string) (*User, error)
	UpsertUser(ctx context.Context, user *User) error
}

// TokenVerifier validates bearer identity tokens from an external authentication provider.
type TokenVerifier interface {
	VerifyIDToken(ctx context.Context, idToken string) (*TokenInfo, error)
}

// QuotaRepository provides atomic accounting and persistence for tenant LLM quotas and usage records.
type QuotaRepository interface {
	GetQuota(ctx context.Context, userID string) (*TenantQuota, error)
	ReserveQuota(ctx context.Context, userID string, op LLMOperationType, estimatedTokens, dailyLimit, monthlyLimit int) (*LLMUsageRecord, error)
	CommitUsage(ctx context.Context, reservationID string, usage TokenUsage) error
	ReleaseQuota(ctx context.Context, reservationID string, errMsg string) error
	ListUsageRecords(ctx context.Context, userID string, limit int) ([]LLMUsageRecord, error)
}

// QuotaService orchestrates multi-tenant LLM rate limiting, quota reservation, and usage accounting.
type QuotaService interface {
	Reserve(ctx context.Context, userID string, op LLMOperationType) (*LLMUsageRecord, error)
	Commit(ctx context.Context, reservationID string, usage TokenUsage) error
	Release(ctx context.Context, reservationID string, errMsg string) error
	GetStatus(ctx context.Context, userID string) (*TenantQuota, error)
}

