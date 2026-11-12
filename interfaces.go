package core

import (
	"context"
	"io"
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
	UpdateVersionATSScore(ctx context.Context, versionID string, atsScore *ATSScore) error
	FailInterruptedVersions(ctx context.Context, reason string) error
}

// ATSSimulationOptions encapsulates optional parameters when triggering an on-demand ATS score simulation.
type ATSSimulationOptions struct {
	VersionID string `json:"version_id,omitempty"`
	Format    string `json:"format,omitempty"` // "markdown", "html", or "text"
}

// ATSSimulationRequest contains the inputs required by the LLM service to execute two-stage ATS extraction and matching.
type ATSSimulationRequest struct {
	Model          string
	ReasoningLevel string
	ResumeText     string
	FormatUsed     string
	BranchContext  Branch
	MainProfile    *MainProfileSnapshot
}

// ATSSimulationResponse wraps the two-stage ATS score evaluation and token consumption metrics.
type ATSSimulationResponse struct {
	Score ATSScore   `json:"score"`
	Usage TokenUsage `json:"usage"`
}

// GenerationRequest contains full context required by an LLM provider to tailor a resume.
type GenerationRequest struct {
	Model              string
	ReasoningLevel     string // Reasoning effort constraint ("none", "low", "medium", "high")
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

// LLMCompletionRequest represents a transport-level completion request sent to an LLM provider.
// All prompt compilation and schema definition is performed by resumeee-core prior to invocation.
type LLMCompletionRequest struct {
	Model          string
	SystemPrompt   string
	UserPrompt     string
	ResponseSchema map[string]any
	ReasoningLevel string
}

// LLMCompletionResponse represents the raw text/JSON completion and token usage returned by an LLM provider.
type LLMCompletionResponse struct {
	Text       string
	RawBody    string
	StatusCode int
	Usage      TokenUsage
}

// LLMClient abstracts pure transport communication with an external LLM vendor API.
// Implementations must only handle wire serialization, HTTP transport, and status error mapping.
type LLMClient interface {
	Name() string
	DefaultModel() string
	Models() []ModelOption
	Complete(ctx context.Context, req LLMCompletionRequest) (*LLMCompletionResponse, error)
}

// LLMProvider abstracts interactions with generative AI model providers.
type LLMProvider interface {
	Name() string
	GenerateTailoredResume(ctx context.Context, req GenerationRequest) (*GenerationResponse, error)
}

// GenerationJob encapsulates the parameters of an asynchronous resume generation task.
type GenerationJob struct {
	UserID        string `json:"user_id"`
	BranchID      string `json:"branch_id"`
	VersionID     string `json:"version_id"`
	ReservationID string `json:"reservation_id,omitempty"`
}

// JobEnqueuer defines the contract for enqueuing generation jobs into the worker pool.
type JobEnqueuer interface {
	Enqueue(job GenerationJob) error
}

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

// LLMService abstracts multi-provider prompt generation and structured response.
type LLMService interface {
	GenerateTailoredResume(ctx context.Context, req GenerationRequest) (*GenerationResponse, error)
	GenerateGapAnalysis(ctx context.Context, req GenerationRequest) (*GapReport, error)
}

// PromptCompiler abstracts compilation of structured generation prompts for preview.
type PromptCompiler interface {
	CompilePrompt(req GenerationRequest) (*PromptPreview, error)
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

// TemplateValidator defines the abstraction for validating template syntax and integrity prior to persistence.
type TemplateValidator interface {
	Validate(tmpl *TemplateSet) error
}

// TemplateValidatorFunc is a functional adapter that implements TemplateValidator.
type TemplateValidatorFunc func(tmpl *TemplateSet) error

// Validate calls f(tmpl).
func (f TemplateValidatorFunc) Validate(tmpl *TemplateSet) error {
	return f(tmpl)
}

// TemplateRenderer defines the external testing and execution seam for resume rendering.
type TemplateRenderer interface {
	TemplateValidator

	// Render compiles and executes all three formats in a TemplateSet against candidate profile and tailored content.
	Render(ctx context.Context, tmpl *TemplateSet, profile *Profile, content *TailoredContent) (*RenderOutput, error)

	// RenderFormat executes a single template string targeting the designated format.
	RenderFormat(format OutputFormat, tmplStr string, data any) (string, error)
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

// AuthProvider abstracts identity credential validation and provider naming.
type AuthProvider interface {
	TokenVerifier
	Name() string
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

// Store aggregates all domain repository interfaces into a unified persistence bundle.
type Store interface {
	Profile() ProfileRepository
	Branch() BranchRepository
	Preset() PresetRepository
	Template() TemplateRepository
	User() UserRepository
	Quota() QuotaRepository
	PDFJob() PDFParseJobRepository
	Close() error
}

// StoreBundle provides a concrete container for domain repository instances and lifecycle hooks.
type StoreBundle struct {
	ProfileRepo  ProfileRepository
	BranchRepo   BranchRepository
	PresetRepo   PresetRepository
	TemplateRepo TemplateRepository
	PDFJobRepo   PDFParseJobRepository
	QuotaRepo    QuotaRepository
	UserRepo     UserRepository
	Closer       func() error
}

// Profile returns the underlying ProfileRepository.
func (b *StoreBundle) Profile() ProfileRepository { return b.ProfileRepo }

// Branch returns the underlying BranchRepository.
func (b *StoreBundle) Branch() BranchRepository { return b.BranchRepo }

// Preset returns the underlying PresetRepository.
func (b *StoreBundle) Preset() PresetRepository { return b.PresetRepo }

// Template returns the underlying TemplateRepository.
func (b *StoreBundle) Template() TemplateRepository { return b.TemplateRepo }

// PDFJob returns the underlying PDFParseJobRepository.
func (b *StoreBundle) PDFJob() PDFParseJobRepository { return b.PDFJobRepo }

// Quota returns the underlying QuotaRepository.
func (b *StoreBundle) Quota() QuotaRepository { return b.QuotaRepo }

// User returns the underlying UserRepository.
func (b *StoreBundle) User() UserRepository { return b.UserRepo }

// Close executes the configured Closer hook if present.
func (b *StoreBundle) Close() error {
	if b.Closer != nil {
		return b.Closer()
	}
	return nil
}

// StoreFactory defines a constructor function that opens and initializes a Store instance.
type StoreFactory func(ctx context.Context) (Store, error)

// PDFTextExtractor abstracts extracting textual content from PDF binary streams.
type PDFTextExtractor interface {
	ExtractText(ctx context.Context, r io.ReaderAt, size int64) (string, error)
}

// ModelEnumerator defines the interface for enumerating available configured LLM models.
type ModelEnumerator interface {
	AvailableModels() []ModelOption
}

// ValidationResult contains the sanitized tailored content and structured warnings.
type ValidationResult struct {
	SanitizedContent *TailoredContent
	Warnings         []string
}

// ProvenanceValidator audits and sanitizes TailoredContent against MainProfileSnapshot and Branch rules.
type ProvenanceValidator interface {
	Validate(content *TailoredContent, snapshot *MainProfileSnapshot, branch *Branch) (*ValidationResult, error)
	ValidateStrict(content *TailoredContent, snapshot *MainProfileSnapshot, branch *Branch) error
}

// ResumeExtractor abstracts LLM-driven unstructured text extraction into standard JSONResume.
type ResumeExtractor interface {
	ExtractResume(ctx context.Context, text string, provider string, model string) (*JSONResume, error)
}

// UsageResumeExtractor extends ResumeExtractor with token usage reporting.
type UsageResumeExtractor interface {
	ResumeExtractor
	ExtractResumeWithUsage(ctx context.Context, text string, provider string, model string) (*JSONResume, TokenUsage, error)
}
