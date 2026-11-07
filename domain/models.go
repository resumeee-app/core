package domain

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

// OwnerType represents the polymorphic owner entity type of a Fact.
type OwnerType string

const (
	OwnerTypeWorkExperience OwnerType = "work_experience"
	OwnerTypeProject        OwnerType = "project"
	OwnerTypeEducation      OwnerType = "education"
)

// EmploymentType represents standard employment arrangements.
type EmploymentType string

const (
	EmploymentTypeFullTime   EmploymentType = "Full-time"
	EmploymentTypePartTime   EmploymentType = "Part-time"
	EmploymentTypeContract   EmploymentType = "Contract"
	EmploymentTypeInternship EmploymentType = "Internship"
)

// ContactType represents the communication medium for a contact detail.
type ContactType string

const (
	ContactTypeEmail ContactType = "Email"
	ContactTypePhone ContactType = "Phone"
)

// ContactDetail represents a structured, typed contact endpoint.
type ContactDetail struct {
	Type  ContactType `json:"type"`
	Value string      `json:"value"`
}

// SourceType represents whether a branch version was produced by LLM or manual edit.
type SourceType string

const (
	SourceTypeLLMGeneration SourceType = "llm_generation"
	SourceTypeManualEdit    SourceType = "manual_edit"
	SourceTypeMasterProfile SourceType = "master_profile"
)

// Location captures standard postal and geographical location attributes.
type Location struct {
	Address     string `json:"address,omitempty"`
	PostalCode  string `json:"postal_code,omitempty"`
	City        string `json:"city,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
	Region      string `json:"region,omitempty"`
}

// SocialLink represents an external digital presence or portfolio URL.
type SocialLink struct {
	Network  string `json:"network"`
	Username string `json:"username"`
	URL      string `json:"url"`
}

// Profile represents the user's Master Profile root and Tier-1 global LLM configuration.
type Profile struct {
	ID                 string         `json:"id" gorm:"primaryKey;type:text"`
	UserID             string         `json:"user_id" gorm:"index;not null;default:'default'"`
	FullName           string         `json:"full_name" gorm:"not null"`
	Headline           string         `json:"headline"`
	Email              string         `json:"email"`
	Phone              string         `json:"phone"`
	Location           Location       `json:"location" gorm:"serializer:json"`
	Profiles           []SocialLink   `json:"profiles" gorm:"serializer:json"`
	Summary            string         `json:"summary"`
	GlobalWritingRules string         `json:"global_writing_rules"` // Tier 1 LLM instructions
	DefaultProvider    string         `json:"default_provider" gorm:"default:'gemini'"`
	DefaultModel       string         `json:"default_model" gorm:"default:'gemini-3.8-flash'"`
	MasterRevision     int64          `json:"master_revision" gorm:"not null;default:1"`
	Metadata           map[string]any `json:"metadata,omitempty" gorm:"serializer:json"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

// ShortIdentifiable is implemented by career domain entities that provide a compact short identifier.
// Types implementing this interface can be rendered into LLM prompts or UI badges using EffectiveShortID.
type ShortIdentifiable interface {
	// EffectiveShortID returns the clean short identifier, falling back to deterministic shortening if empty.
	EffectiveShortID() string
}

// Skill is a first-class entity linked across experiences, projects, and individual facts.
type Skill struct {
	ID              string         `json:"id" gorm:"primaryKey;type:text"`
	ShortID         string         `json:"short_id,omitempty" gorm:"index"`
	UserID          string         `json:"user_id" gorm:"index;not null"`
	Name            string         `json:"name" gorm:"not null;index"`
	Category        string         `json:"category"`    // e.g., "Languages", "Distributed Systems", "Cloud"
	Proficiency     string         `json:"proficiency"` // e.g., "Expert", "Proficient", "Familiar"
	YearsExperience float64        `json:"years_experience"`
	Keywords        []string       `json:"keywords,omitempty" gorm:"serializer:json"`
	Metadata        map[string]any `json:"metadata,omitempty" gorm:"serializer:json"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// EffectiveShortID returns the explicit ShortID if populated; otherwise it deterministically
// derives a compact representation from the canonical ID via ShortenID.
//
// Returns:
//   - A compact identifier string for the skill.
func (s Skill) EffectiveShortID() string {
	if s.ShortID != "" {
		return s.ShortID
	}
	return ShortenID(s.ID)
}

// WorkExperience captures both standard resume fields and detailed administrative fields.
type WorkExperience struct {
	ID                string         `json:"id" gorm:"primaryKey;type:text"`
	ShortID           string         `json:"short_id,omitempty" gorm:"index"`
	UserID            string         `json:"user_id" gorm:"index;not null"`
	Company           string         `json:"company" gorm:"not null"`
	RoleTitle         string         `json:"role_title" gorm:"not null"`
	EmploymentType    EmploymentType `json:"employment_type"`            // Full-time, Contract, Part-time, Internship
	StartDate         string         `json:"start_date" gorm:"not null"` // ISO-8601 YYYY-MM-DD
	EndDate           *string        `json:"end_date,omitempty"`         // nil if current
	IsCurrent         bool           `json:"is_current"`
	Location          Location       `json:"location" gorm:"serializer:json"`
	CompanyLocation   Location       `json:"company_location,omitempty" gorm:"serializer:json"` // Location of the employing company (government/visa)
	CompanyContact    *ContactDetail `json:"company_contact,omitempty" gorm:"serializer:json"`  // Contact detail for employment verification
	HoursPerWeek      *int           `json:"hours_per_week,omitempty"`                          // For immigration/government CVs
	SupervisorName    string         `json:"supervisor_name,omitempty"`
	SupervisorContact string         `json:"supervisor_contact,omitempty"`
	DutiesSummary     string         `json:"duties_summary,omitempty"`
	Facts             []Fact         `json:"facts,omitempty" gorm:"polymorphic:Owner;polymorphicValue:work_experience"`
	Skills            []Skill        `json:"skills,omitempty" gorm:"many2many:experience_skills;"`
	Metadata          map[string]any `json:"metadata,omitempty" gorm:"serializer:json"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

// EffectiveShortID returns the explicit ShortID if populated; otherwise it deterministically
// derives a compact representation from the canonical ID via ShortenID.
//
// Returns:
//   - A compact identifier string for the work experience.
func (e WorkExperience) EffectiveShortID() string {
	if e.ShortID != "" {
		return e.ShortID
	}
	return ShortenID(e.ID)
}

// Project represents a professional, open-source, or academic project.
type Project struct {
	ID        string         `json:"id" gorm:"primaryKey;type:text"`
	ShortID   string         `json:"short_id,omitempty" gorm:"index"`
	UserID    string         `json:"user_id" gorm:"index;not null"`
	Name      string         `json:"name" gorm:"not null"`
	Role      string         `json:"role,omitempty"`
	URL       string         `json:"url,omitempty"`
	StartDate string         `json:"start_date,omitempty"`
	EndDate   *string        `json:"end_date,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Facts     []Fact         `json:"facts,omitempty" gorm:"polymorphic:Owner;polymorphicValue:project"`
	Skills    []Skill        `json:"skills,omitempty" gorm:"many2many:project_skills;"`
	Metadata  map[string]any `json:"metadata,omitempty" gorm:"serializer:json"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// EffectiveShortID returns the explicit ShortID if populated; otherwise it deterministically
// derives a compact representation from the canonical ID via ShortenID.
//
// Returns:
//   - A compact identifier string for the project.
func (p Project) EffectiveShortID() string {
	if p.ShortID != "" {
		return p.ShortID
	}
	return ShortenID(p.ID)
}

// Education captures academic credentials without excessive/unrequired course lists.
type Education struct {
	ID          string         `json:"id" gorm:"primaryKey;type:text"`
	ShortID     string         `json:"short_id,omitempty" gorm:"index"`
	UserID      string         `json:"user_id" gorm:"index;not null"`
	Institution string         `json:"institution" gorm:"not null"`
	Degree      string         `json:"degree"` // StudyType in JSONResume
	Field       string         `json:"field"`  // Area in JSONResume
	StartDate   string         `json:"start_date,omitempty"`
	EndDate     *string        `json:"end_date,omitempty"`
	Score       string         `json:"score,omitempty"` // GPA / Honors
	Location    Location       `json:"location" gorm:"serializer:json"`
	Facts       []Fact         `json:"facts,omitempty" gorm:"polymorphic:Owner;polymorphicValue:education"`
	Metadata    map[string]any `json:"metadata,omitempty" gorm:"serializer:json"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// EffectiveShortID returns the explicit ShortID if populated; otherwise it deterministically
// derives a compact representation from the canonical ID via ShortenID.
//
// Returns:
//   - A compact identifier string for the education record.
func (d Education) EffectiveShortID() string {
	if d.ShortID != "" {
		return d.ShortID
	}
	return ShortenID(d.ID)
}

// Certification represents professional licenses, awards, or publications.
type Certification struct {
	ID        string         `json:"id" gorm:"primaryKey;type:text"`
	ShortID   string         `json:"short_id,omitempty" gorm:"index"`
	UserID    string         `json:"user_id" gorm:"index;not null"`
	Name      string         `json:"name" gorm:"not null"`
	Issuer    string         `json:"issuer,omitempty"`
	Date      string         `json:"date,omitempty"`
	URL       string         `json:"url,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty" gorm:"serializer:json"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// EffectiveShortID returns the explicit ShortID if populated; otherwise it deterministically
// derives a compact representation from the canonical ID via ShortenID.
//
// Returns:
//   - A compact identifier string for the certification.
func (c Certification) EffectiveShortID() string {
	if c.ShortID != "" {
		return c.ShortID
	}
	return ShortenID(c.ID)
}

// Fact is an atomic, verifiable achievement, responsibility, or metric belonging to an entity.
type Fact struct {
	ID        string    `json:"id" gorm:"primaryKey;type:text"`
	ShortID   string    `json:"short_id,omitempty" gorm:"index"`
	UserID    string    `json:"user_id" gorm:"index;not null"`
	OwnerID   string    `json:"owner_id" gorm:"index;index:idx_fact_owner;not null"`
	OwnerType OwnerType `json:"owner_type" gorm:"index;index:idx_fact_owner;not null"` // work_experience, project, education
	Statement string    `json:"statement" gorm:"not null"`                             // Raw factual description
	Impact    string    `json:"impact,omitempty"`                                      // Verifiable outcome / metric
	Skills    []Skill   `json:"skills,omitempty" gorm:"many2many:fact_skills;"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EffectiveShortID returns the explicit ShortID if populated; otherwise it deterministically
// derives a compact representation from the canonical ID via ShortenID.
//
// Returns:
//   - A compact identifier string for the fact.
func (f Fact) EffectiveShortID() string {
	if f.ShortID != "" {
		return f.ShortID
	}
	return ShortenID(f.ID)
}

// Built-in Preset and Tailoring Strategy IDs.
const (
	// Preset1PageTechResumeID defines the identifier for the built-in 1-Page Tech Resume preset.
	Preset1PageTechResumeID = "builtin-1-page-tech-resume"

	// PresetImmigrationFullHistoryCVID defines the identifier for the built-in Immigration Full History CV preset.
	PresetImmigrationFullHistoryCVID = "builtin-immigration-full-history-cv"
)

// TailoringStrategy defines reusable prompt engineering directives, reasoning depth,
// and density rules for LLM resume tailoring.
//
// Invariant: A TailoringStrategy defines how content is synthesized and prioritized;
// it has zero mandatory coupling to visual templates or CSS layouts.
type TailoringStrategy struct {
	ID                 string    `json:"id" gorm:"primaryKey;type:text"`
	UserID             string    `json:"user_id" gorm:"index;not null"`
	Name               string    `json:"name" gorm:"not null"` // e.g., "1-Page SWE", "Executive Leadership"
	Description        string    `json:"description,omitempty"`
	SystemInstructions string    `json:"system_instructions" gorm:"not null"` // Tier 2 LLM instructions

	// SuggestedThemeID optionally provides a recommended visual layout in creation wizards.
	// Nullable; when nil, the application defaults to the system standard theme.
	SuggestedThemeID   *string   `json:"suggested_theme_id" gorm:"index"`

	// TemplateSetID is an alias retained for backwards compatibility with legacy UI/forms.
	TemplateSetID      string    `json:"template_set_id,omitempty" gorm:"column:template_set_id"`

	ProviderOverride   string    `json:"provider_override,omitempty"`
	ModelOverride      string    `json:"model_override,omitempty"`
	ReasoningLevel     string    `json:"reasoning_level,omitempty"` // "none", "low", "medium", "high"
	IncludeAdminFields bool      `json:"include_admin_fields"`      // Government / visa CV admin fields
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// TableName overrides the GORM table name to tailoring_strategies.
func (TailoringStrategy) TableName() string {
	return "tailoring_strategies"
}

// BeforeCreate synchronizes aliases prior to insertion.
func (s *TailoringStrategy) BeforeCreate(_ *gorm.DB) error {
	if s.SuggestedThemeID == nil && s.TemplateSetID != "" {
		themeID := s.TemplateSetID
		s.SuggestedThemeID = &themeID
	}
	if s.TemplateSetID == "" && s.SuggestedThemeID != nil {
		s.TemplateSetID = *s.SuggestedThemeID
	}
	return nil
}

// BeforeSave synchronizes aliases prior to saving.
func (s *TailoringStrategy) BeforeSave(_ *gorm.DB) error {
	if s.SuggestedThemeID == nil && s.TemplateSetID != "" {
		themeID := s.TemplateSetID
		s.SuggestedThemeID = &themeID
	}
	if s.TemplateSetID == "" && s.SuggestedThemeID != nil {
		s.TemplateSetID = *s.SuggestedThemeID
	}
	return nil
}

// IsBuiltIn returns true if the strategy is a system-provided built-in strategy.
// Built-in strategies are immutable and protected against update or deletion.
func (s TailoringStrategy) IsBuiltIn() bool {
	return strings.HasPrefix(s.ID, "builtin-")
}

// TemplateSetName returns an empty string on domain.TailoringStrategy, allowing view models to override it.
func (s TailoringStrategy) TemplateSetName() string {
	return ""
}

// Preset is an alias to TailoringStrategy for backwards compatibility.
type Preset = TailoringStrategy

// TemplateSet bundles HTML, Markdown, and Plain Text Go templates stored in the database.
type TemplateSet struct {
	ID               string    `json:"id" gorm:"primaryKey;type:text"`
	UserID           string    `json:"user_id" gorm:"index;not null"`
	Name             string    `json:"name" gorm:"not null"`
	Description      string    `json:"description,omitempty"`
	IsBuiltIn        bool      `json:"is_built_in"`
	HTMLTemplate     string    `json:"html_template" gorm:"not null"`     // Executed via html/template
	CSSStyles        string    `json:"css_styles,omitempty"`              // Embedded into HTML output
	MarkdownTemplate string    `json:"markdown_template" gorm:"not null"` // Executed via text/template
	TextTemplate     string    `json:"text_template" gorm:"not null"`     // Executed via text/template
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// TableName overrides the GORM table name to template_sets.
func (TemplateSet) TableName() string {
	return "template_sets"
}

// Canonical default branch constants and helpers.
const DefaultBranchName = "Master Profile (Default)"

// DefaultBranchID returns the deterministic branch ID for a user's default untailored branch.
func DefaultBranchID(userID string) string {
	return "branch-default-" + userID
}

// TargetResume (Branch) represents a targeted resume variant tailored for a specific opportunity.
type TargetResume struct {
	ID                       string     `json:"id" gorm:"primaryKey;type:text"`
	UserID                   string     `json:"user_id" gorm:"index;not null"`
	Name                     string     `json:"name" gorm:"not null"` // e.g., "Stripe - Staff Backend Engineer"
	IsDefault                bool       `json:"is_default" gorm:"default:false;index"`
	TargetRole               string     `json:"target_role,omitempty"`
	TargetCompany            string     `json:"target_company,omitempty"`
	TargetRequirements       string     `json:"target_requirements,omitempty"` // Job description text
	CustomDirectives         string     `json:"custom_directives,omitempty"`   // Tier 3 custom prompt directives
	BranchInstructions       string     `json:"branch_instructions,omitempty" gorm:"column:branch_instructions"` // Compatibility alias
	TailoringStrategyID      *string    `json:"tailoring_strategy_id,omitempty" gorm:"index"`
	PresetID                 *string    `json:"preset_id,omitempty" gorm:"column:preset_id;index"` // Compatibility alias
	TemplateSetID            string     `json:"template_set_id" gorm:"index;not null"`
	ProviderOverride         string     `json:"provider_override,omitempty"`
	ModelOverride            string     `json:"model_override,omitempty"`
	PinnedEntityIDs          []string   `json:"pinned_entity_ids,omitempty" gorm:"serializer:json"`
	ExcludedEntityIDs        []string   `json:"excluded_entity_ids,omitempty" gorm:"serializer:json"`
	ActiveVersionID          *string    `json:"active_version_id,omitempty" gorm:"index"`
	GapReport                *GapReport `json:"gap_report,omitempty" gorm:"serializer:json"`
	LastSyncedMasterRevision int64      `json:"last_synced_master_revision"`
	IsStale                  bool       `json:"is_stale" gorm:"-"` // Computed: LastSyncedMasterRevision < Profile.MasterRevision
	CreatedAt                time.Time  `json:"created_at"`
	UpdatedAt                time.Time  `json:"updated_at"`
}

// TableName overrides the GORM table name to target_resumes.
func (TargetResume) TableName() string {
	return "target_resumes"
}

// syncAliases harmonizes TailoringStrategyID <-> PresetID and CustomDirectives <-> BranchInstructions.
func (b *TargetResume) syncAliases() {
	if b.TailoringStrategyID == nil && b.PresetID != nil {
		b.TailoringStrategyID = b.PresetID
	}
	if b.PresetID == nil && b.TailoringStrategyID != nil {
		b.PresetID = b.TailoringStrategyID
	}
	if b.CustomDirectives == "" && b.BranchInstructions != "" {
		b.CustomDirectives = b.BranchInstructions
	}
	if b.BranchInstructions == "" && b.CustomDirectives != "" {
		b.BranchInstructions = b.CustomDirectives
	}
}

// BeforeCreate synchronizes aliases prior to insertion.
func (b *TargetResume) BeforeCreate(_ *gorm.DB) error {
	b.syncAliases()
	return nil
}

// BeforeSave synchronizes aliases prior to saving.
func (b *TargetResume) BeforeSave(_ *gorm.DB) error {
	b.syncAliases()
	return nil
}

// CanTailor returns true if the branch/resume can be tailored via LLM or manual bullet edits.
// Default branches directly render the master profile and cannot be tailored.
func (b *TargetResume) CanTailor() bool {
	return !b.IsDefault
}

// IsStaleRevision returns true if the last synced master revision is behind the given master revision.
func (b *TargetResume) IsStaleRevision(currentMasterRevision int64) bool {
	return b.LastSyncedMasterRevision < currentMasterRevision
}

// PresetName returns an empty string on domain.TargetResume, allowing view models to override it.
func (b TargetResume) PresetName() string {
	return ""
}

// Branch is an alias to TargetResume.
type Branch = TargetResume

// VersionStatus represents the lifecycle state of a ResumeVersion / BranchVersion.
type VersionStatus string

const (
	VersionStatusPending               VersionStatus = "pending"
	VersionStatusProcessing            VersionStatus = "processing"
	VersionStatusCompleted             VersionStatus = "completed"
	VersionStatusCompletedWithWarnings VersionStatus = "completed_with_warnings"
	VersionStatusFailed                VersionStatus = "failed"
)

// ResumeVersion (BranchVersion) is an immutable snapshot tracking its generation lifecycle and outputs.
type ResumeVersion struct {
	ID                 string           `json:"id" gorm:"primaryKey;type:text"`
	ResumeID           string           `json:"resume_id" gorm:"uniqueIndex:idx_resume_version,priority:1;column:resume_id;not null"`
	BranchID           string           `json:"branch_id,omitempty" gorm:"column:branch_id;index"` // Compatibility alias
	VersionNumber      int              `json:"version_number" gorm:"uniqueIndex:idx_resume_version,priority:2;column:version_number;not null"`
	Status             VersionStatus    `json:"status" gorm:"index;not null"`
	MasterRevisionUsed int64            `json:"master_revision_used"`
	TemplateSetIDUsed  string           `json:"template_set_id_used"`
	StrategyIDUsed     *string          `json:"strategy_id_used,omitempty"`
	ProviderUsed       string           `json:"provider_used"`
	ModelUsed          string           `json:"model_used"`
	SourceType         SourceType       `json:"source_type"` // "llm_generation" or "manual_edit"
	ParentVersionID    *string          `json:"parent_version_id,omitempty" gorm:"index"`
	TailoredContent    *TailoredContent `json:"tailored_content,omitempty" gorm:"serializer:json"`
	GapReport          *GapReport       `json:"gap_report,omitempty" gorm:"serializer:json"`
	ValidationWarnings []string         `json:"validation_warnings,omitempty" gorm:"serializer:json"`
	ErrorMessage       string           `json:"error_message,omitempty"`
	HTMLOutput         string           `json:"html_output,omitempty"`
	MarkdownOutput     string           `json:"markdown_output,omitempty"`
	PlainTextOutput    string           `json:"plain_text_output,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	CompletedAt        *time.Time       `json:"completed_at,omitempty"`
}

// TableName overrides the GORM table name to resume_versions.
func (ResumeVersion) TableName() string {
	return "resume_versions"
}

// syncAliases harmonizes resume_id and branch_id.
func (v *ResumeVersion) syncAliases() {
	if v.ResumeID == "" && v.BranchID != "" {
		v.ResumeID = v.BranchID
	}
	if v.BranchID == "" && v.ResumeID != "" {
		v.BranchID = v.ResumeID
	}
}

// BeforeCreate synchronizes resume_id and branch_id prior to insertion.
func (v *ResumeVersion) BeforeCreate(_ *gorm.DB) error {
	v.syncAliases()
	return nil
}

// BeforeSave synchronizes resume_id and branch_id prior to saving.
func (v *ResumeVersion) BeforeSave(_ *gorm.DB) error {
	v.syncAliases()
	return nil
}

// BranchVersion is an alias to ResumeVersion.
type BranchVersion = ResumeVersion

// MasterProfileSnapshot is an aggregate capture of the entire career graph at a specific MasterRevision.
type MasterProfileSnapshot struct {
	Profile        Profile          `json:"profile"`
	MasterRevision int64            `json:"master_revision"`
	Skills         []Skill          `json:"skills"`
	Experiences    []WorkExperience `json:"experiences"`
	Projects       []Project        `json:"projects"`
	Education      []Education      `json:"education"`
	Certifications []Certification  `json:"certifications"`
	Facts          []Fact           `json:"facts"`
	ExportedAt     time.Time        `json:"exported_at"`
}

// ModelOption represents a configured LLM provider and model pairing safe for public enumeration.
type ModelOption struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	DisplayName string `json:"display_name"`
	IsDefault   bool   `json:"is_default"`
}

// PDFParseJobStatus represents the asynchronous lifecycle state of a PDF resume parse operation.
type PDFParseJobStatus string

const (
	// PDFParseJobStatusPending indicates the job is queued for parsing.
	PDFParseJobStatusPending PDFParseJobStatus = "pending"
	// PDFParseJobStatusProcessing indicates the job is actively being processed by an LLM worker.
	PDFParseJobStatusProcessing PDFParseJobStatus = "processing"
	// PDFParseJobStatusCompleted indicates parsing finished successfully and candidate result is available.
	PDFParseJobStatusCompleted PDFParseJobStatus = "completed"
	// PDFParseJobStatusFailed indicates parsing terminated with an error.
	PDFParseJobStatusFailed PDFParseJobStatus = "failed"
)

// PDFParseJob tracks the status and candidate extraction result of an asynchronous PDF resume parse task.
type PDFParseJob struct {
	ID           string            `json:"id" gorm:"primaryKey;type:text"`
	UserID       string            `json:"user_id" gorm:"index;not null"`
	Status       PDFParseJobStatus `json:"status" gorm:"index;not null"`
	Filename     string            `json:"filename,omitempty"`
	Provider     string            `json:"provider,omitempty"`
	Model        string            `json:"model,omitempty"`
	Result       any               `json:"result,omitempty" gorm:"serializer:json"`
	ErrorMessage string            `json:"error_message,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	CompletedAt  *time.Time        `json:"completed_at,omitempty"`
}

// PromptPreview wraps compiled system and user prompt strings ready for LLM consumption.
type PromptPreview struct {
	SystemPrompt string `json:"system_prompt"`
	UserPrompt   string `json:"user_prompt"`
}

// PromptPreviewOptions encapsulates optional draft modifications when previewing prompt compilation.
type PromptPreviewOptions struct {
	TargetRole         *string  `json:"target_role,omitempty"`
	TargetCompany      *string  `json:"target_company,omitempty"`
	TargetRequirements *string  `json:"target_requirements,omitempty"`
	BranchInstructions *string  `json:"branch_instructions,omitempty"`
	PresetID           *string  `json:"preset_id,omitempty"`
	PinnedEntityIDs    []string `json:"pinned_entity_ids,omitempty"`
	ExcludedEntityIDs  []string `json:"excluded_entity_ids,omitempty"`
}

