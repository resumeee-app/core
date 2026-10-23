package domain

// GapImportance represents the severity or tier of an identified gap.
type GapImportance string

const (
	GapImportanceCritical   GapImportance = "critical"
	GapImportancePreferred  GapImportance = "preferred"
	GapImportanceNiceToHave GapImportance = "nice_to_have"
)

// TailoredContent is the canonical structured payload produced by the LLM (or manual edit) and passed to Go templates.
type TailoredContent struct {
	Summary        string               `json:"summary"`
	SkillGroups    []TailoredSkillGroup `json:"skill_groups"`
	Experiences    []TailoredExperience `json:"experiences"`
	Projects       []TailoredProject    `json:"projects"`
	Education      []TailoredEducation  `json:"education"`
	Certifications []TailoredCert       `json:"certifications"`
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
	StartDate         string           `json:"start_date"`
	EndDate           *string          `json:"end_date,omitempty"`
	Location          Location         `json:"location"`
	CompanyLocation   Location         `json:"company_location,omitempty"`
	CompanyContact    *ContactDetail   `json:"company_contact,omitempty"`
	HoursPerWeek      *int             `json:"hours_per_week,omitempty"`
	SupervisorName    string           `json:"supervisor_name,omitempty"`
	SupervisorContact string           `json:"supervisor_contact,omitempty"`
	DutiesSummary     string           `json:"duties_summary,omitempty"`
	Bullets           []TailoredBullet `json:"bullets"`
}

// TailoredProject contains tailored bullets for a specific Project entity.
type TailoredProject struct {
	SourceEntityID string           `json:"source_entity_id"` // Must match a Project.ID
	Name           string           `json:"name"`
	Role           string           `json:"role,omitempty"`
	URL            string           `json:"url,omitempty"`
	StartDate      string           `json:"start_date,omitempty"`
	EndDate        *string          `json:"end_date,omitempty"`
	Summary        string           `json:"summary,omitempty"`
	Bullets        []TailoredBullet `json:"bullets"`
}

// TailoredEducation captures tailored academic achievements.
type TailoredEducation struct {
	SourceEntityID string           `json:"source_entity_id"` // Must match an Education.ID
	Institution    string           `json:"institution"`
	Degree         string           `json:"degree"`
	Field          string           `json:"field"`
	StartDate      string           `json:"start_date"`
	EndDate        *string          `json:"end_date,omitempty"`
	Score          string           `json:"score,omitempty"`
	Bullets        []TailoredBullet `json:"bullets,omitempty"`
}

// TailoredCert captures tailored professional credentials.
type TailoredCert struct {
	SourceEntityID string `json:"source_entity_id"` // Must match a Certification.ID
	Name           string `json:"name"`
	Issuer         string `json:"issuer"`
	Date           string `json:"date"`
	URL            string `json:"url,omitempty"`
}

// TailoredBullet represents an individual resume bullet with explicit citations to source Facts and Skills.
type TailoredBullet struct {
	Text           string   `json:"text"`
	SourceFactIDs  []string `json:"source_fact_ids"`            // Must match Fact.IDs belonging to SourceEntityID
	SourceSkillIDs []string `json:"source_skill_ids,omitempty"` // Optional cited Skill.IDs
}

// GapReport is an informational analysis attached to a BranchVersion identifying missing target requirements.
type GapReport struct {
	OverallMatchSummary string    `json:"overall_match_summary"`
	MissingRequirements []GapItem `json:"missing_requirements"`
}

// GapItem represents an identified gap in candidate experience relative to target requirements.
type GapItem struct {
	Requirement      string        `json:"requirement"`
	Importance       GapImportance `json:"importance"` // "critical", "preferred", "nice_to_have"
	ClarifyingPrompt string        `json:"clarifying_prompt"`
}
