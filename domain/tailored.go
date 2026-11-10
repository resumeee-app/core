package domain

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"strings"
	"time"
)

// CloneMetadata returns a defensive shallow copy of an entity metadata map, or nil if m is nil.
func CloneMetadata(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	return maps.Clone(m)
}

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

// ATSExtractedExperience represents a work experience entry extracted by simulated ATS parsing.
type ATSExtractedExperience struct {
	Company    string   `json:"company"`
	Title      string   `json:"title"`
	DateRange  string   `json:"date_range,omitempty"`
	Highlights []string `json:"highlights,omitempty"`
}

// ATSExtractedEducation represents an education entry extracted by simulated ATS parsing.
type ATSExtractedEducation struct {
	Institution string `json:"institution"`
	Degree      string `json:"degree"`
	Field       string `json:"field,omitempty"`
	DateRange   string `json:"date_range,omitempty"`
}

// ATSExtractedCandidate represents the categorized candidate content parsed from a text-based resume.
type ATSExtractedCandidate struct {
	Summary        string                   `json:"summary,omitempty"`
	Skills         []string                 `json:"skills"`
	Experiences    []ATSExtractedExperience `json:"experiences"`
	Educations     []ATSExtractedEducation  `json:"educations"`
	Certifications []string                 `json:"certifications,omitempty"`
}

// Normalize ensures slices in ATSExtractedCandidate are non-nil.
func (c *ATSExtractedCandidate) Normalize() {
	if c == nil {
		return
	}
	if c.Skills == nil {
		c.Skills = []string{}
	}
	if c.Experiences == nil {
		c.Experiences = []ATSExtractedExperience{}
	}
	for i := range c.Experiences {
		if c.Experiences[i].Highlights == nil {
			c.Experiences[i].Highlights = []string{}
		}
	}
	if c.Educations == nil {
		c.Educations = []ATSExtractedEducation{}
	}
	if c.Certifications == nil {
		c.Certifications = []string{}
	}
}

// ATSScore represents the two-signal ATS simulation evaluation for a tailored resume version:
// 1. ExtractionConfidenceScore (0..100%): how complete the extracted information is from the text-based resume.
// 2. JobMatchingScore (0..100%): perceived matching score of the extracted information against the job description.
type ATSScore struct {
	ExtractionConfidenceScore float64                `json:"extraction_confidence_score"`
	JobMatchingScore          float64                `json:"job_matching_score"`
	FormatUsed                string                 `json:"format_used,omitempty"`
	ExtractionSummary         string                 `json:"extraction_summary,omitempty"`
	MatchingSummary           string                 `json:"matching_summary,omitempty"`
	ExtractedCandidate        *ATSExtractedCandidate `json:"extracted_candidate,omitempty"`
	EvaluatedAt               *time.Time             `json:"evaluated_at,omitempty"`
}

// NormalizeATSFormat validates and normalizes an ATS simulation format against the allowed values
// ("", "markdown", "md", "html", "text", "txt", "plain_text").
func NormalizeATSFormat(rawFormat string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(rawFormat)) {
	case "", "markdown", "md":
		return string(OutputFormatMarkdown), nil
	case "html":
		return string(OutputFormatHTML), nil
	case "text", "txt", "plain_text":
		return string(OutputFormatPlainText), nil
	default:
		return "", fmt.Errorf("%w: unsupported ATS format %q (expected html, markdown, or text)", ErrValidation, rawFormat)
	}
}

// Normalize clamps score percentages to [0, 100] and normalizes nested extracted candidate slices.
func (s *ATSScore) Normalize() {
	if s == nil {
		return
	}
	if s.ExtractionConfidenceScore < 0 {
		s.ExtractionConfidenceScore = 0
	} else if s.ExtractionConfidenceScore > 100 {
		s.ExtractionConfidenceScore = 100
	} else {
		s.ExtractionConfidenceScore = math.Round(s.ExtractionConfidenceScore*10) / 10
	}

	if s.JobMatchingScore < 0 {
		s.JobMatchingScore = 0
	} else if s.JobMatchingScore > 100 {
		s.JobMatchingScore = 100
	} else {
		s.JobMatchingScore = math.Round(s.JobMatchingScore*10) / 10
	}

	if s.ExtractedCandidate != nil {
		s.ExtractedCandidate.Normalize()
	}
}

// ConvertATSRatioToPercent converts an ATS score ratio in [0, 1] (where 1.0 = 100%) or percentage in (1, 100]
// into a clamped [0, 100] percentage value rounded to one decimal place.
func ConvertATSRatioToPercent(val float64) float64 {
	if math.IsNaN(val) || val <= 0 {
		return 0.0
	}
	if val <= 1.0 {
		return math.Round(val*1000) / 10
	}
	if val > 100.0 {
		return 100.0
	}
	return math.Round(val*10) / 10
}

// CalculateDeterministicATSScore provides a deterministic fallback ATS extraction and matching score
// when no external LLM provider is configured.
func CalculateDeterministicATSScore(resumeText, formatUsed, jobDescription string) *ATSScore {
	trimmedResume := strings.TrimSpace(resumeText)
	trimmedJD := strings.TrimSpace(jobDescription)
	if formatUsed == "" {
		formatUsed = string(OutputFormatMarkdown)
	}

	extractionScore := 0.0
	if len(trimmedResume) > 0 {
		extractionScore = 60.0
		lowerResume := strings.ToLower(trimmedResume)
		if strings.Contains(lowerResume, "summary") || len(trimmedResume) >= 120 {
			extractionScore += 15.0
		}
		if strings.Contains(lowerResume, "experience") || strings.Contains(lowerResume, "work") || strings.Contains(lowerResume, "- ") || strings.Contains(lowerResume, "<li>") {
			extractionScore += 15.0
		}
		if strings.Contains(lowerResume, "skill") || strings.Contains(lowerResume, "education") {
			extractionScore += 10.0
		}
	}

	matchingScore := 0.0
	var matchedTokens []string
	if len(trimmedResume) > 0 && len(trimmedJD) > 0 {
		lowerResume := strings.ToLower(trimmedResume)
		words := strings.FieldsFunc(strings.ToLower(trimmedJD), func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9')
		})
		stopWords := map[string]bool{
			"the": true, "and": true, "for": true, "with": true, "that": true,
			"this": true, "from": true, "your": true, "have": true, "will": true,
			"are": true, "our": true, "you": true, "looking": true, "experience": true,
		}
		seen := make(map[string]bool)
		totalKeywords := 0
		matchedKeywords := 0
		for _, w := range words {
			if len(w) < 2 || stopWords[w] || seen[w] {
				continue
			}
			seen[w] = true
			totalKeywords++
			if strings.Contains(lowerResume, w) {
				matchedKeywords++
				matchedTokens = append(matchedTokens, w)
			}
		}
		if totalKeywords == 0 {
			matchingScore = 80.0
		} else {
			ratio := float64(matchedKeywords) / float64(totalKeywords)
			matchingScore = math.Round(ratio*1000) / 10
			if matchingScore < 15.0 && matchedKeywords > 0 {
				matchingScore = 15.0
			}
		}
	}

	candidate := &ATSExtractedCandidate{
		Summary:     "Deterministically parsed resume content.",
		Skills:      matchedTokens,
		Experiences: []ATSExtractedExperience{},
		Educations:  []ATSExtractedEducation{},
	}
	candidate.Normalize()

	now := time.Now().UTC()
	res := &ATSScore{
		ExtractionConfidenceScore: extractionScore,
		JobMatchingScore:          matchingScore,
		FormatUsed:                formatUsed,
		ExtractionSummary:         "Extracted structured candidate sections from rendered resume text.",
		MatchingSummary:           "Evaluated extracted candidate profile against target job description.",
		ExtractedCandidate:        candidate,
		EvaluatedAt:               &now,
	}
	res.Normalize()
	return res
}


