package core

import "maps"

// CloneMetadata returns a defensive shallow copy of an entity metadata map, or nil if m is nil.
func CloneMetadata(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	return maps.Clone(m)
}

// TailoredContent is the canonical structured payload produced by the LLM (or manual edit) and passed to Go templates.
type TailoredContent struct {
	Summary        string               `json:"summary"`
	SkillGroups    []TailoredSkillGroup `json:"skill_groups,omitempty"`
	Experiences    []TailoredExperience `json:"experiences,omitempty"`
	Projects       []TailoredProject    `json:"projects,omitempty"`
	Education      []TailoredEducation  `json:"education,omitempty"`
	Certifications []TailoredCert       `json:"certifications,omitempty"`
}

// TailoredSkillGroup groups categorized skills with citations to source Skill.IDs.
type TailoredSkillGroup struct {
	Category       string   `json:"category"`
	Skills         []string `json:"skills"`
	SourceSkillIDs []string `json:"source_skill_ids,omitempty"` // Provenance citations
}

// TailoredExperience contains tailored bullets for a specific WorkExperience entity.
type TailoredExperience struct {
	SourceEntityID    string           `json:"source_entity_id"` // Must match a WorkExperience.ID
	Company           string           `json:"company"`
	RoleTitle         string           `json:"role_title"`
	EmploymentType    EmploymentType   `json:"employment_type,omitempty"`
	StartDate         Date             `json:"start_date"`
	EndDate           *Date            `json:"end_date,omitempty"`
	IsCurrent         bool             `json:"is_current,omitempty"`
	Location          Location         `json:"location,omitempty,omitzero"`
	CompanyLocation   Location         `json:"company_location,omitempty,omitzero"`
	CompanyContact    *ContactDetail   `json:"company_contact,omitempty"`
	HoursPerWeek      *int             `json:"hours_per_week,omitempty"`
	SupervisorName    string           `json:"supervisor_name,omitempty"`
	SupervisorContact string           `json:"supervisor_contact,omitempty"`
	DutiesSummary     string           `json:"duties_summary,omitempty"`
	Bullets           []TailoredBullet `json:"bullets"`
	Metadata          map[string]any   `json:"metadata,omitempty"`
}

// BulletPoints provides backward-compatibility for template property access.
func (e TailoredExperience) BulletPoints() []TailoredBullet { return e.Bullets }

// TailoredProject contains tailored bullets for a specific Project entity.
type TailoredProject struct {
	SourceEntityID string           `json:"source_entity_id"` // Must match a Project.ID
	Name           string           `json:"name"`
	Role           string           `json:"role,omitempty"`
	URL            string           `json:"url,omitempty"`
	StartDate      *Date            `json:"start_date,omitempty"`
	EndDate        *Date            `json:"end_date,omitempty"`
	Summary        string           `json:"summary,omitempty"`
	Bullets        []TailoredBullet `json:"bullets"`
	Metadata       map[string]any   `json:"metadata,omitempty"`
}

// BulletPoints provides backward-compatibility for template property access.
func (p TailoredProject) BulletPoints() []TailoredBullet { return p.Bullets }

// TailoredEducation captures tailored academic achievements.
type TailoredEducation struct {
	SourceEntityID string           `json:"source_entity_id"` // Must match an Education.ID
	Institution    string           `json:"institution"`
	Degree         string           `json:"degree"`
	Field          string           `json:"field"`
	StartDate      *Date            `json:"start_date,omitempty"`
	EndDate        *Date            `json:"end_date,omitempty"`
	Location       Location         `json:"location,omitempty,omitzero"`
	Score          string           `json:"score,omitempty"`
	Bullets        []TailoredBullet `json:"bullets,omitempty"`
	Metadata       map[string]any   `json:"metadata,omitempty"`
}

// BulletPoints provides backward-compatibility for template property access.
func (d TailoredEducation) BulletPoints() []TailoredBullet { return d.Bullets }

// TailoredCert captures tailored professional credentials.
type TailoredCert struct {
	SourceEntityID string         `json:"source_entity_id"` // Must match a Certification.ID
	Name           string         `json:"name"`
	Issuer         string         `json:"issuer"`
	Date           *Date          `json:"date,omitempty"`
	URL            string         `json:"url,omitempty"`
	Summary        string         `json:"summary,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

// TailoredBullet represents an individual resume bullet with explicit citations to source Facts and Skills.
type TailoredBullet struct {
	Text           string   `json:"text"`
	SourceFactIDs  []string `json:"source_fact_ids"`            // Must match Fact.IDs belonging to SourceEntityID
	SourceSkillIDs []string `json:"source_skill_ids,omitempty"` // Optional cited Skill.IDs
}

// TokenUsage captures token consumption metrics returned by upstream LLM providers.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
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

// GenerationResponse wraps structured tailored content and gap analysis returned by an LLM.
type GenerationResponse struct {
	TailoredContent TailoredContent `json:"tailored_content"`
	GapReport       GapReport       `json:"gap_report"`
	Usage           TokenUsage      `json:"usage"`
}

// ValidationResult contains the sanitized tailored content and structured warnings.
type ValidationResult struct {
	SanitizedContent *TailoredContent
	Warnings         []string
}
