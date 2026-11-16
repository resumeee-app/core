package core_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/resumeee-app/core"
)

func TestGapReport_JSONHarmonization(t *testing.T) {
	t.Run("unmarshals missing_requirements and populates identified_gaps alias", func(t *testing.T) {
		input := `{
			"overall_match_summary": "Strong backend fit with minor cloud gaps.",
			"match_score": 85.0,
			"missing_requirements": [
				{
					"requirement": "Kubernetes CRD development",
					"missing_area": "Container Orchestration",
					"importance": "critical",
					"clarifying_prompt": "Have you written custom Kubernetes controllers in Go?",
					"recommended_action": "Highlight controller-runtime projects."
				}
			],
			"strategic_recommendations": ["Emphasize Go concurrency patterns."]
		}`

		var report core.GapReport
		if err := json.Unmarshal([]byte(input), &report); err != nil {
			t.Fatalf("failed to unmarshal GapReport: %v", err)
		}

		if report.OverallMatchSummary != "Strong backend fit with minor cloud gaps." {
			t.Errorf("expected summary, got %q", report.OverallMatchSummary)
		}
		if report.MatchScore == nil || *report.MatchScore != 85.0 {
			t.Errorf("expected match_score 85.0, got %v", report.MatchScore)
		}
		if len(report.MissingRequirements) != 1 {
			t.Fatalf("expected 1 missing requirement, got %d", len(report.MissingRequirements))
		}
		if len(report.IdentifiedGaps) != 1 {
			t.Fatalf("expected 1 identified gap alias, got %d", len(report.IdentifiedGaps))
		}
		if report.IdentifiedGaps[0].Requirement != "Kubernetes CRD development" {
			t.Errorf("expected requirement 'Kubernetes CRD development', got %q", report.IdentifiedGaps[0].Requirement)
		}
		if report.MissingRequirements[0].MissingArea != "Container Orchestration" {
			t.Errorf("expected missing area 'Container Orchestration', got %q", report.MissingRequirements[0].MissingArea)
		}
		if report.MissingRequirements[0].Importance != core.GapImportanceCritical {
			t.Errorf("expected importance 'critical', got %q", report.MissingRequirements[0].Importance)
		}
	})

	t.Run("unmarshals identified_gaps and populates missing_requirements alias", func(t *testing.T) {
		input := `{
			"match_score": 92.5,
			"identified_gaps": [
				{
					"requirement": "Raft consensus protocol",
					"missing_area": "Distributed Systems",
					"importance": "preferred",
					"recommended_action": "Cite distributed systems coursework or projects."
				}
			]
		}`

		var report core.GapReport
		if err := json.Unmarshal([]byte(input), &report); err != nil {
			t.Fatalf("failed to unmarshal GapReport: %v", err)
		}

		if len(report.IdentifiedGaps) != 1 {
			t.Fatalf("expected 1 identified gap, got %d", len(report.IdentifiedGaps))
		}
		if len(report.MissingRequirements) != 1 {
			t.Fatalf("expected 1 missing requirement alias, got %d", len(report.MissingRequirements))
		}
		// RecommendedAction fallback for ClarifyingPrompt if omitted
		if report.MissingRequirements[0].ClarifyingPrompt == "" {
			t.Errorf("expected clarifying prompt fallback from recommended action, got empty")
		}
	})

	t.Run("marshaling includes both missing_requirements and identified_gaps", func(t *testing.T) {
		score := 90.0
		report := core.GapReport{
			OverallMatchSummary: "Solid fit",
			MatchScore:          &score,
			MissingRequirements: []core.GapItem{
				{
					Requirement:      "Kafka stream processing",
					MissingArea:      "Messaging",
					Importance:       core.GapImportancePreferred,
					ClarifyingPrompt: "Have you operated Kafka consumers at high throughput?",
				},
			},
		}

		data, err := json.Marshal(report)
		if err != nil {
			t.Fatalf("failed to marshal report: %v", err)
		}

		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("failed to unmarshal into map: %v", err)
		}

		if _, ok := raw["missing_requirements"]; !ok {
			t.Errorf("missing_requirements key missing from marshaled JSON: %s", string(data))
		}
		if _, ok := raw["identified_gaps"]; !ok {
			t.Errorf("identified_gaps key missing from marshaled JSON: %s", string(data))
		}
		if _, ok := raw["match_score"]; !ok {
			t.Errorf("match_score key missing from marshaled JSON: %s", string(data))
		}
	})
}

func TestCalculateDeterministicMatchScore(t *testing.T) {
	tests := []struct {
		name     string
		gaps     []core.GapItem
		expected float64
	}{
		{
			name:     "empty gaps returns 100",
			gaps:     []core.GapItem{},
			expected: 100.0,
		},
		{
			name: "single critical gap deducts 15",
			gaps: []core.GapItem{
				{Requirement: "G1", Importance: core.GapImportanceCritical},
			},
			expected: 85.0,
		},
		{
			name: "single preferred gap deducts 7",
			gaps: []core.GapItem{
				{Requirement: "G1", Importance: core.GapImportancePreferred},
			},
			expected: 93.0,
		},
		{
			name: "single nice to have gap deducts 3",
			gaps: []core.GapItem{
				{Requirement: "G1", Importance: core.GapImportanceNiceToHave},
			},
			expected: 97.0,
		},
		{
			name: "suggested gap acts as nice to have alias (-3)",
			gaps: []core.GapItem{
				{Requirement: "G1", Importance: core.GapImportanceSuggested},
			},
			expected: 97.0,
		},
		{
			name: "combined gaps: 1 critical, 1 preferred, 1 nice_to_have = -25",
			gaps: []core.GapItem{
				{Requirement: "G1", Importance: core.GapImportanceCritical},
				{Requirement: "G2", Importance: core.GapImportancePreferred},
				{Requirement: "G3", Importance: core.GapImportanceNiceToHave},
			},
			expected: 75.0,
		},
		{
			name: "many critical gaps clamp at 0.0",
			gaps: []core.GapItem{
				{Requirement: "G1", Importance: core.GapImportanceCritical},
				{Requirement: "G2", Importance: core.GapImportanceCritical},
				{Requirement: "G3", Importance: core.GapImportanceCritical},
				{Requirement: "G4", Importance: core.GapImportanceCritical},
				{Requirement: "G5", Importance: core.GapImportanceCritical},
				{Requirement: "G6", Importance: core.GapImportanceCritical},
				{Requirement: "G7", Importance: core.GapImportanceCritical},
			},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := core.CalculateDeterministicMatchScore(tt.gaps)
			if score != tt.expected {
				t.Errorf("CalculateDeterministicMatchScore() = %v, expected %v", score, tt.expected)
			}
		})
	}
}

func TestCalculateSkillCoverageRatio(t *testing.T) {
	candidateSkills := []core.Skill{
		{Name: "Go"},
		{Name: "Kubernetes"},
		{Name: "PostgreSQL"},
		{Name: "Kafka"},
	}

	t.Run("calculates ratio when candidate has matching skills", func(t *testing.T) {
		gaps := []core.GapItem{
			{Requirement: "Raft consensus protocol"},
		}
		target := "Must be experienced in Go and Kubernetes and Raft"

		// Go and Kubernetes match (2 matched). 1 gap.
		// total = 2 + 1 = 3 -> ratio = 2/3 = 0.67
		ratio := core.CalculateSkillCoverageRatio(candidateSkills, gaps, target)
		if ratio != 0.67 {
			t.Errorf("expected 0.67, got %v", ratio)
		}
	})

	t.Run("returns 1.0 when candidate has no gaps", func(t *testing.T) {
		gaps := []core.GapItem{}
		target := "Experienced in Go and PostgreSQL"
		ratio := core.CalculateSkillCoverageRatio(candidateSkills, gaps, target)
		if ratio != 1.0 {
			t.Errorf("expected 1.0, got %v", ratio)
		}
	})

	t.Run("returns 0.0 when candidate has zero matching skills and multiple gaps", func(t *testing.T) {
		gaps := []core.GapItem{
			{Requirement: "Cobol"},
			{Requirement: "Mainframe"},
		}
		target := "Cobol and Mainframe systems"
		ratio := core.CalculateSkillCoverageRatio(candidateSkills, gaps, target)
		if ratio != 0.0 {
			t.Errorf("expected 0.0, got %v", ratio)
		}
	})
}

func TestTailoredContent_OmitZeroNonTailoringFields(t *testing.T) {
	t.Run("Location.IsZero correctly identifies empty locations", func(t *testing.T) {
		emptyLoc := core.Location{}
		if !emptyLoc.IsZero() {
			t.Errorf("expected empty Location to be zero")
		}

		populatedLoc := core.Location{City: "San Francisco"}
		if populatedLoc.IsZero() {
			t.Errorf("expected populated Location to not be zero")
		}
	})

	t.Run("omitzero and omitempty omit unpopulated location and contact fields", func(t *testing.T) {
		content := core.TailoredContent{
			Summary: "Lead developer summary.",
			Experiences: []core.TailoredExperience{
				{
					SourceEntityID: "exp-1",
					Company:        "Stripe",
					RoleTitle:      "Staff Engineer",
					StartDate:      core.NewDate(2022, 1, 1),
					Bullets: []core.TailoredBullet{
						{
							Text:          "Scaled payment engine.",
							SourceFactIDs: []string{"fact-1"},
						},
					},
				},
			},
			Education: []core.TailoredEducation{
				{
					SourceEntityID: "edu-1",
					Institution:    "Stanford",
					Degree:         "B.S.",
					Field:          "CS",
				},
			},
		}

		bytes, err := json.Marshal(content)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}

		jsonStr := string(bytes)
		forbiddenKeys := []string{
			`"location"`,
			`"company_location"`,
			`"company_contact"`,
			`"hours_per_week"`,
			`"supervisor_name"`,
			`"supervisor_contact"`,
			`"skill_groups"`,
			`"projects"`,
			`"certifications"`,
		}

		for _, key := range forbiddenKeys {
			if strings.Contains(jsonStr, key) {
				t.Errorf("serialized TailoredContent unexpectedly contained %s: %s", key, jsonStr)
			}
		}

		expectedKeys := []string{
			`"summary"`,
			`"experiences"`,
			`"education"`,
			`"source_entity_id"`,
			`"company"`,
			`"role_title"`,
			`"start_date"`,
			`"bullets"`,
		}

		for _, key := range expectedKeys {
			if !strings.Contains(jsonStr, key) {
				t.Errorf("serialized TailoredContent missing expected key %s: %s", key, jsonStr)
			}
		}
	})
}

func TestConvertATSRatioToPercent(t *testing.T) {
	tests := []struct {
		name     string
		input    float64
		expected float64
	}{
		{name: "negative clamped to 0", input: -0.25, expected: 0},
		{name: "zero remains 0", input: 0, expected: 0},
		{name: "ratio 0.5 converts to 50", input: 0.5, expected: 50},
		{name: "ratio 0.875 converts to 87.5", input: 0.875, expected: 87.5},
		{name: "ratio 1.0 converts to 100", input: 1.0, expected: 100},
		{name: "percentage 85 remains 85", input: 85.0, expected: 85},
		{name: "percentage above 100 clamped to 100", input: 120.0, expected: 100},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := core.ConvertATSRatioToPercent(tc.input)
			if got != tc.expected {
				t.Errorf("ConvertATSRatioToPercent(%v) = %v; want %v", tc.input, got, tc.expected)
			}
		})
	}
}

func TestATSScore_Normalize(t *testing.T) {
	score := &core.ATSScore{
		ExtractionConfidenceScore: 105.0,
		JobMatchingScore:          -5.0,
		ExtractedCandidate: &core.ATSExtractedCandidate{
			Summary: "Backend engineer",
			Experiences: []core.ATSExtractedExperience{
				{Company: "Acme", Title: "Senior Dev"},
			},
		},
	}
	score.Normalize()

	if score.ExtractionConfidenceScore != 100.0 {
		t.Errorf("expected ExtractionConfidenceScore clamped to 100, got %v", score.ExtractionConfidenceScore)
	}
	if score.JobMatchingScore != 0.0 {
		t.Errorf("expected JobMatchingScore clamped to 0, got %v", score.JobMatchingScore)
	}
	if score.ExtractedCandidate.Skills == nil {
		t.Errorf("expected ExtractedCandidate.Skills initialized to non-nil slice")
	}
	if score.ExtractedCandidate.Experiences[0].Highlights == nil {
		t.Errorf("expected Highlights initialized to non-nil slice")
	}
}

func TestCalculateDeterministicATSScore(t *testing.T) {
	resumeText := "# Jane Doe\n## Summary\nDistributed systems engineer skilled in Go, Kubernetes, and PostgreSQL.\n## Experience\n- Built high-throughput microservices in Go and Kubernetes."
	jobDesc := "Looking for a Senior Go Engineer with Kubernetes, PostgreSQL, and AWS experience."

	score := core.CalculateDeterministicATSScore(resumeText, "markdown", jobDesc)
	if score == nil {
		t.Fatal("expected non-nil ATSScore")
	}
	if score.ExtractionConfidenceScore <= 0 || score.ExtractionConfidenceScore > 100 {
		t.Errorf("expected ExtractionConfidenceScore in (0, 100], got %v", score.ExtractionConfidenceScore)
	}
	if score.JobMatchingScore <= 0 || score.JobMatchingScore > 100 {
		t.Errorf("expected JobMatchingScore in (0, 100], got %v", score.JobMatchingScore)
	}
	if score.FormatUsed != "markdown" {
		t.Errorf("expected FormatUsed 'markdown', got %q", score.FormatUsed)
	}
}
