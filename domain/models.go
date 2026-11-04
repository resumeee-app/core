package domain

import (
	"strings"
	"time"
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
	Category        string         `json:"category"` // e.g., "Languages", "Distributed Systems", "Cloud"
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
	EmploymentType    EmploymentType `json:"employment_type"` // Full-time, Contract, Part-time, Internship
	StartDate         string         `json:"start_date" gorm:"not null"` // ISO-8601 YYYY-MM-DD
	EndDate           *string        `json:"end_date,omitempty"` // nil if current
	IsCurrent         bool           `json:"is_current"`
	Location          Location       `json:"location" gorm:"serializer:json"`
	CompanyLocation   Location       `json:"company_location,omitempty" gorm:"serializer:json"` // Location of the employing company (government/visa)
	CompanyContact    *ContactDetail `json:"company_contact,omitempty" gorm:"serializer:json"` // Contact detail for employment verification
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
	Statement string    `json:"statement" gorm:"not null"`        // Raw factual description
	Impact    string    `json:"impact,omitempty"`                 // Verifiable outcome / metric
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

// Preset represents Tier-2 reusable LLM and formatting configurations.
type Preset struct {
	ID                 string    `json:"id" gorm:"primaryKey;type:text"`
	UserID             string    `json:"user_id" gorm:"index;not null"`
	Name               string    `json:"name" gorm:"not null"`
	Description        string    `json:"description,omitempty"`
	SystemInstructions string    `json:"system_instructions" gorm:"not null"` // Tier 2 LLM instructions
	TemplateSetID      string    `json:"template_set_id" gorm:"index;not null"`
	ProviderOverride   string    `json:"provider_override,omitempty"`
	ModelOverride      string    `json:"model_override,omitempty"`
	IncludeAdminFields bool      `json:"include_admin_fields"` // True for immigration/government presets
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// IsBuiltIn returns true if the preset is a system-provided built-in preset.
func (p Preset) IsBuiltIn() bool {
	return strings.HasPrefix(p.ID, "builtin-")
}

// TemplateSetName returns an empty string on domain.Preset, allowing view models to override it.
func (p Preset) TemplateSetName() string {
	return ""
}

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

// Canonical default branch constants and helpers.
const DefaultBranchName = "Master Profile (Default)"

// DefaultBranchID returns the deterministic branch ID for a user's default untailored branch.
func DefaultBranchID(userID string) string {
	return "branch-default-" + userID
}

// Branch represents a tailored resume line (Tier-3 context + rules + version pointer).
type Branch struct {
	ID                       string    `json:"id" gorm:"primaryKey;type:text"`
	UserID                   string    `json:"user_id" gorm:"index;not null"`
	Name                     string    `json:"name" gorm:"not null"` // e.g., "Stripe - Staff Backend Engineer"
	IsDefault                bool      `json:"is_default" gorm:"default:false;index"`
	TargetRole               string    `json:"target_role,omitempty"`
	TargetCompany            string    `json:"target_company,omitempty"`
	TargetRequirements       string    `json:"target_requirements,omitempty"` // Job description or immigration criteria
	BranchInstructions       string    `json:"branch_instructions,omitempty"` // Tier 3 custom LLM instructions
	PresetID                 *string   `json:"preset_id,omitempty" gorm:"index"`
	TemplateSetID            string    `json:"template_set_id" gorm:"index;not null"`
	ProviderOverride         string    `json:"provider_override,omitempty"`
	ModelOverride            string    `json:"model_override,omitempty"`
	PinnedEntityIDs          []string  `json:"pinned_entity_ids,omitempty" gorm:"serializer:json"`   // Force-included Master IDs
	ExcludedEntityIDs        []string  `json:"excluded_entity_ids,omitempty" gorm:"serializer:json"` // Force-excluded Master IDs
	ActiveVersionID          *string   `json:"active_version_id,omitempty" gorm:"index"`
	LastSyncedMasterRevision int64     `json:"last_synced_master_revision"`
	IsStale                  bool      `json:"is_stale" gorm:"-"` // Computed: LastSyncedMasterRevision < Profile.MasterRevision
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`
}

// CanTailor returns true if the branch can be tailored via LLM or manual bullet edits.
// Default branches directly render the master profile and cannot be tailored.
func (b *Branch) CanTailor() bool {
	return !b.IsDefault
}

// IsStaleRevision returns true if the branch's last synced master revision is behind the given master revision.
func (b *Branch) IsStaleRevision(currentMasterRevision int64) bool {
	return b.LastSyncedMasterRevision < currentMasterRevision
}

// PresetName returns an empty string on domain.Branch, allowing view models to override it.
func (b Branch) PresetName() string {
	return ""
}

// VersionStatus represents the lifecycle state of a BranchVersion.
type VersionStatus string

const (
	VersionStatusPending               VersionStatus = "pending"
	VersionStatusProcessing            VersionStatus = "processing"
	VersionStatusCompleted             VersionStatus = "completed"
	VersionStatusCompletedWithWarnings VersionStatus = "completed_with_warnings"
	VersionStatusFailed                VersionStatus = "failed"
)

// BranchVersion is an immutable snapshot tracking its own async generation lifecycle and rendered outputs.
type BranchVersion struct {
	ID                 string           `json:"id" gorm:"primaryKey;type:text"`
	BranchID           string           `json:"branch_id" gorm:"uniqueIndex:idx_branch_version;not null"`
	VersionNumber      int              `json:"version_number" gorm:"uniqueIndex:idx_branch_version;not null"` // Sequential per branch: 1, 2, 3...
	Status             VersionStatus    `json:"status" gorm:"index;not null"`
	MasterRevisionUsed int64            `json:"master_revision_used"`
	TemplateSetIDUsed  string           `json:"template_set_id_used"`
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

