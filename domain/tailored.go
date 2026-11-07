package domain

import (
	"encoding/json"
	"math"
	"strings"
)

// GapImportance represents the severity or tier of an identified gap.
type GapImportance string

const (
	GapImportanceCritical   GapImportance = "critical"
	GapImportancePreferred  GapImportance = "preferred"
	GapImportanceNiceToHave GapImportance = "nice_to_have"
	GapImportanceSuggested  GapImportance = "suggested" // Alias for nice_to_have
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
	StartDate         Date             `json:"start_date"`
	EndDate           *Date            `json:"end_date,omitempty"`
	Location          Location         `json:"location"`
	CompanyLocation   Location         `json:"company_location,omitempty"`
	CompanyContact    *ContactDetail   `json:"company_contact,omitempty"`
	HoursPerWeek      *int             `json:"hours_per_week,omitempty"`
	SupervisorName    string           `json:"supervisor_name,omitempty"`
	SupervisorContact string           `json:"supervisor_contact,omitempty"`
	DutiesSummary     string           `json:"duties_summary,omitempty"`
	Bullets           []TailoredBullet `json:"bullets"`
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
	Score          string           `json:"score,omitempty"`
	Bullets        []TailoredBullet `json:"bullets,omitempty"`
}

// BulletPoints provides backward-compatibility for template property access.
func (d TailoredEducation) BulletPoints() []TailoredBullet { return d.Bullets }

// TailoredCert captures tailored professional credentials.
type TailoredCert struct {
	SourceEntityID string `json:"source_entity_id"` // Must match a Certification.ID
	Name           string `json:"name"`
	Issuer         string `json:"issuer"`
	Date           *Date  `json:"date,omitempty"`
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
	OverallMatchSummary      string    `json:"overall_match_summary,omitempty"`
	MatchScore               *float64  `json:"match_score,omitempty"`
	MissingRequirements      []GapItem `json:"missing_requirements,omitempty"`
	IdentifiedGaps           []GapItem `json:"identified_gaps,omitempty"`
	StrategicRecommendations []string  `json:"strategic_recommendations,omitempty"`
	SkillCoverageRatio       *float64  `json:"skill_coverage_ratio,omitempty"`
}

// Normalize ensures missing_requirements, identified_gaps, clarifying_prompt, and recommended_action are populated in-memory.
func (r *GapReport) Normalize() {
	if r == nil {
		return
	}
	items := r.MissingRequirements
	if len(items) == 0 && len(r.IdentifiedGaps) > 0 {
		items = r.IdentifiedGaps
	}

	for i := range items {
		if items[i].ClarifyingPrompt == "" {
			items[i].ClarifyingPrompt = items[i].RecommendedAction
		}
		if items[i].RecommendedAction == "" {
			items[i].RecommendedAction = items[i].ClarifyingPrompt
		}
	}

	r.MissingRequirements = items
	r.IdentifiedGaps = items
}

// MarshalJSON ensures both missing_requirements and identified_gaps are populated on output.
func (r GapReport) MarshalJSON() ([]byte, error) {
	r.Normalize()
	type Alias GapReport
	return json.Marshal(Alias(r))
}

// UnmarshalJSON populates both MissingRequirements and IdentifiedGaps regardless of which field was provided.
func (r *GapReport) UnmarshalJSON(data []byte) error {
	type Alias GapReport
	var aux Alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*r = GapReport(aux)
	r.Normalize()
	return nil
}

// GapItem represents an identified gap in candidate experience relative to target requirements.
type GapItem struct {
	Requirement       string        `json:"requirement"`
	MissingArea       string        `json:"missing_area,omitempty"`
	Importance        GapImportance `json:"importance"` // "critical", "preferred", "nice_to_have", "suggested"
	ClarifyingPrompt  string        `json:"clarifying_prompt,omitempty"`
	RecommendedAction string        `json:"recommended_action,omitempty"`
}

// CalculateDeterministicMatchScore computes an explainable, reproducible match score between 0.0 and 100.0 based on identified gaps.
func CalculateDeterministicMatchScore(gaps []GapItem) float64 {
	if len(gaps) == 0 {
		return 100.0
	}
	deductions := 0.0
	for _, g := range gaps {
		switch g.Importance {
		case GapImportanceCritical:
			deductions += 15.0
		case GapImportancePreferred:
			deductions += 7.0
		case GapImportanceNiceToHave, GapImportanceSuggested:
			deductions += 3.0
		default:
			deductions += 5.0
		}
	}
	score := 100.0 - deductions
	if score < 0.0 {
		return 0.0
	}
	return math.Round(score*10) / 10
}

// CalculateSkillCoverageRatio computes the ratio of candidate skills present in target requirements relative to total requirement keywords and missing gaps.
func CalculateSkillCoverageRatio(candidateSkills []Skill, gaps []GapItem, targetRequirements string) float64 {
	targetLower := strings.ToLower(targetRequirements)
	matched := 0
	seen := make(map[string]bool)
	for _, s := range candidateSkills {
		name := strings.ToLower(strings.TrimSpace(s.Name))
		if name == "" || len(name) < 2 || seen[name] {
			continue
		}
		seen[name] = true
		if strings.Contains(targetLower, name) {
			matched++
		}
	}

	total := matched + len(gaps)
	if total == 0 {
		if len(gaps) == 0 {
			return 1.0
		}
		return 0.0
	}

	ratio := float64(matched) / float64(total)
	return math.Round(ratio*100) / 100
}

