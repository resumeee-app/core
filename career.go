package core

import (
	"time"
)

// OwnerType represents the polymorphic owner entity type of a Fact.
type OwnerType string

// Supported parent entity categories for polymorphic Fact ownership.
const (
	// OwnerTypeWorkExperience scopes a fact to a WorkExperience record.
	OwnerTypeWorkExperience OwnerType = "work_experience"

	// OwnerTypeProject scopes a fact to a Project record.
	OwnerTypeProject OwnerType = "project"

	// OwnerTypeEducation scopes a fact to an Education record.
	OwnerTypeEducation OwnerType = "education"
)

// EmploymentType represents standard employment arrangements.
type EmploymentType string

// Standard employment engagement classifications for work history records.
const (
	// EmploymentTypeFullTime denotes full-time regular employment.
	EmploymentTypeFullTime EmploymentType = "Full-time"

	// EmploymentTypePartTime denotes part-time employment.
	EmploymentTypePartTime EmploymentType = "Part-time"

	// EmploymentTypeContract denotes fixed-term or consulting contract work.
	EmploymentTypeContract EmploymentType = "Contract"

	// EmploymentTypeInternship denotes student or early-career internship work.
	EmploymentTypeInternship EmploymentType = "Internship"
)

// Skill is a first-class entity linked across experiences, projects, and individual facts.
type Skill struct {
	ID              string         `json:"id"`
	ShortID         string         `json:"short_id,omitempty"`
	UserID          string         `json:"user_id"`
	Name            string         `json:"name"`
	Slug            string         `json:"slug,omitempty"`
	IsGlobal        bool           `json:"is_global"`
	Category        string         `json:"category"`    // e.g., "Languages", "Distributed Systems", "Cloud"
	Proficiency     string         `json:"proficiency"` // e.g., "Expert", "Proficient", "Familiar"
	YearsExperience float64        `json:"years_experience"`
	Keywords        []string       `json:"keywords,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// EffectiveShortID returns the explicit ShortID if populated; otherwise it deterministically
// derives a compact representation from the canonical ID via ShortenID.
func (s Skill) EffectiveShortID() string {
	if s.ShortID != "" {
		return s.ShortID
	}
	return ShortenID(s.ID)
}

// WorkExperience captures both standard resume fields and detailed administrative fields.
type WorkExperience struct {
	ID                string         `json:"id"`
	ShortID           string         `json:"short_id,omitempty"`
	UserID            string         `json:"user_id"`
	Company           string         `json:"company"`
	RoleTitle         string         `json:"role_title"`
	EmploymentType    EmploymentType `json:"employment_type"`    // Full-time, Contract, Part-time, Internship
	StartDate         Date           `json:"start_date"`         // ISO-8601 YYYY-MM-DD
	EndDate           *Date          `json:"end_date,omitempty"` // nil if current
	IsCurrent         bool           `json:"is_current"`
	Location          Location       `json:"location"`
	CompanyLocation   Location       `json:"company_location,omitempty"` // Location of the employing company (government/visa)
	CompanyContact    *ContactDetail `json:"company_contact,omitempty"`  // Contact detail for employment verification
	HoursPerWeek      *int           `json:"hours_per_week,omitempty"`   // For immigration/government CVs
	SupervisorName    string         `json:"supervisor_name,omitempty"`
	SupervisorContact string         `json:"supervisor_contact,omitempty"`
	DutiesSummary     string         `json:"duties_summary,omitempty"`
	Facts             []Fact         `json:"facts,omitempty"`
	Skills            []Skill        `json:"skills,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

// EffectiveShortID returns the explicit ShortID if populated; otherwise it deterministically
// derives a compact representation from the canonical ID via ShortenID.
func (e WorkExperience) EffectiveShortID() string {
	if e.ShortID != "" {
		return e.ShortID
	}
	return ShortenID(e.ID)
}

// Project represents a professional, open-source, or academic project.
type Project struct {
	ID        string         `json:"id"`
	ShortID   string         `json:"short_id,omitempty"`
	UserID    string         `json:"user_id"`
	Name      string         `json:"name"`
	Role      string         `json:"role,omitempty"`
	URL       string         `json:"url,omitempty"`
	StartDate *Date          `json:"start_date,omitempty"`
	EndDate   *Date          `json:"end_date,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Facts     []Fact         `json:"facts,omitempty"`
	Skills    []Skill        `json:"skills,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// EffectiveShortID returns the explicit ShortID if populated; otherwise it deterministically
// derives a compact representation from the canonical ID via ShortenID.
func (p Project) EffectiveShortID() string {
	if p.ShortID != "" {
		return p.ShortID
	}
	return ShortenID(p.ID)
}

// Education captures academic credentials without excessive/unrequired course lists.
type Education struct {
	ID          string         `json:"id"`
	ShortID     string         `json:"short_id,omitempty"`
	UserID      string         `json:"user_id"`
	Institution string         `json:"institution"`
	Degree      string         `json:"degree"` // StudyType in JSONResume
	Field       string         `json:"field"`  // Area in JSONResume
	StartDate   *Date          `json:"start_date,omitempty"`
	EndDate     *Date          `json:"end_date,omitempty"`
	Score       string         `json:"score,omitempty"` // GPA / Honors
	Location    Location       `json:"location"`
	Facts       []Fact         `json:"facts,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// EffectiveShortID returns the explicit ShortID if populated; otherwise it deterministically
// derives a compact representation from the canonical ID via ShortenID.
func (d Education) EffectiveShortID() string {
	if d.ShortID != "" {
		return d.ShortID
	}
	return ShortenID(d.ID)
}

// Certification represents professional licenses, awards, or publications.
type Certification struct {
	ID        string         `json:"id"`
	ShortID   string         `json:"short_id,omitempty"`
	UserID    string         `json:"user_id"`
	Name      string         `json:"name"`
	Issuer    string         `json:"issuer,omitempty"`
	Date      *Date          `json:"date,omitempty"`
	URL       string         `json:"url,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// EffectiveShortID returns the explicit ShortID if populated; otherwise it deterministically
// derives a compact representation from the canonical ID via ShortenID.
func (c Certification) EffectiveShortID() string {
	if c.ShortID != "" {
		return c.ShortID
	}
	return ShortenID(c.ID)
}

// Fact is an atomic, verifiable achievement, responsibility, or metric belonging to an entity.
type Fact struct {
	ID        string    `json:"id"`
	ShortID   string    `json:"short_id,omitempty"`
	UserID    string    `json:"user_id"`
	OwnerID   string    `json:"owner_id"`
	OwnerType OwnerType `json:"owner_type"` // work_experience, project, education
	Statement string    `json:"statement"`  // Raw factual description
	Impact    string    `json:"impact,omitempty"`
	Skills    []Skill   `json:"skills,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EffectiveShortID returns the explicit ShortID if populated; otherwise it deterministically
// derives a compact representation from the canonical ID via ShortenID.
func (f Fact) EffectiveShortID() string {
	if f.ShortID != "" {
		return f.ShortID
	}
	return ShortenID(f.ID)
}
