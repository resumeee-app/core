package core

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// GapImportance represents the severity or tier of an identified gap.
type GapImportance string

// Severity levels for qualification gaps surfaced against target requirements.
const (
	// GapImportanceCritical identifies a mandatory or core job requirement gap.
	GapImportanceCritical GapImportance = "critical"

	// GapImportancePreferred identifies an important but non-blocking requirement gap.
	GapImportancePreferred GapImportance = "preferred"

	// GapImportanceNiceToHave identifies an optional bonus qualification gap.
	GapImportanceNiceToHave GapImportance = "nice_to_have"

	// GapImportanceSuggested is a legacy alias for GapImportanceNiceToHave.
	GapImportanceSuggested GapImportance = "suggested"
)

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

// ATSSimulationResponse wraps the two-stage ATS score evaluation, token consumption metrics, and call telemetry.
type ATSSimulationResponse struct {
	Score     ATSScore     `json:"score"`
	Usage     TokenUsage   `json:"usage"`
	Telemetry LLMTelemetry `json:"telemetry,omitempty"`
}

// EffectiveTelemetry returns r.Telemetry with Usage populated from r.Usage when Telemetry.Usage is zero.
func (r *ATSSimulationResponse) EffectiveTelemetry() LLMTelemetry {
	if r == nil {
		return LLMTelemetry{}
	}
	return resolveResponseTelemetry(r.Telemetry, r.Usage)
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

// clampPercent clamps a raw percentage score to [0, 100] and rounds to one decimal place.
func clampPercent(val float64) float64 {
	if val < 0 {
		return 0
	}
	if val > 100 {
		return 100
	}
	return math.Round(val*10) / 10
}

// Normalize clamps score percentages to [0, 100] and normalizes nested extracted candidate slices.
func (s *ATSScore) Normalize() {
	if s == nil {
		return
	}
	s.ExtractionConfidenceScore = clampPercent(s.ExtractionConfidenceScore)
	s.JobMatchingScore = clampPercent(s.JobMatchingScore)
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

// atsStopWords holds common filler tokens excluded during deterministic ATS keyword matching.
var atsStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true,
	"this": true, "from": true, "your": true, "have": true, "will": true,
	"are": true, "our": true, "you": true, "looking": true, "experience": true,
}

// scoreATSExtraction estimates section parse completeness from rendered resume text.
func scoreATSExtraction(trimmedResume string) float64 {
	if trimmedResume == "" {
		return 0.0
	}
	score := 60.0
	lowerResume := strings.ToLower(trimmedResume)
	if strings.Contains(lowerResume, "summary") || len(trimmedResume) >= 120 {
		score += 15.0
	}
	if strings.Contains(lowerResume, "experience") || strings.Contains(lowerResume, "work") || strings.Contains(lowerResume, "- ") || strings.Contains(lowerResume, "<li>") {
		score += 15.0
	}
	if strings.Contains(lowerResume, "skill") || strings.Contains(lowerResume, "education") {
		score += 10.0
	}
	return score
}

// extractJDKeywords tokenizes unique significant terms from a job description.
func extractJDKeywords(trimmedJD string) []string {
	words := strings.FieldsFunc(strings.ToLower(trimmedJD), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	seen := make(map[string]bool, len(words))
	keywords := make([]string, 0, len(words))
	for _, w := range words {
		if len(w) < 2 || atsStopWords[w] || seen[w] {
			continue
		}
		seen[w] = true
		keywords = append(keywords, w)
	}
	return keywords
}

// scoreATSMatching compares job description keywords against rendered resume text.
func scoreATSMatching(trimmedResume, trimmedJD string) (float64, []string) {
	if trimmedResume == "" || trimmedJD == "" {
		return 0.0, nil
	}
	keywords := extractJDKeywords(trimmedJD)
	if len(keywords) == 0 {
		return 80.0, nil
	}
	lowerResume := strings.ToLower(trimmedResume)
	var matchedTokens []string
	for _, w := range keywords {
		if strings.Contains(lowerResume, w) {
			matchedTokens = append(matchedTokens, w)
		}
	}
	score := math.Round((float64(len(matchedTokens))/float64(len(keywords)))*1000) / 10
	if score < 15.0 && len(matchedTokens) > 0 {
		score = 15.0
	}
	return score, matchedTokens
}

// CalculateDeterministicATSScore provides a deterministic fallback ATS extraction and matching score
// when no external LLM provider is configured.
func CalculateDeterministicATSScore(resumeText, formatUsed, jobDescription string) *ATSScore {
	trimmedResume := strings.TrimSpace(resumeText)
	trimmedJD := strings.TrimSpace(jobDescription)
	if formatUsed == "" {
		formatUsed = string(OutputFormatMarkdown)
	}

	extractionScore := scoreATSExtraction(trimmedResume)
	matchingScore, matchedTokens := scoreATSMatching(trimmedResume, trimmedJD)

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
