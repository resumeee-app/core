package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"resumeee/internal/domain"
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

		var report domain.GapReport
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
		if report.MissingRequirements[0].Importance != domain.GapImportanceCritical {
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

		var report domain.GapReport
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
		report := domain.GapReport{
			OverallMatchSummary: "Solid fit",
			MatchScore:          &score,
			MissingRequirements: []domain.GapItem{
				{
					Requirement:      "Kafka stream processing",
					MissingArea:      "Messaging",
					Importance:       domain.GapImportancePreferred,
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
		gaps     []domain.GapItem
		expected float64
	}{
		{
			name:     "empty gaps returns 100",
			gaps:     []domain.GapItem{},
			expected: 100.0,
		},
		{
			name: "single critical gap deducts 15",
			gaps: []domain.GapItem{
				{Requirement: "G1", Importance: domain.GapImportanceCritical},
			},
			expected: 85.0,
		},
		{
			name: "single preferred gap deducts 7",
			gaps: []domain.GapItem{
				{Requirement: "G1", Importance: domain.GapImportancePreferred},
			},
			expected: 93.0,
		},
		{
			name: "single nice to have gap deducts 3",
			gaps: []domain.GapItem{
				{Requirement: "G1", Importance: domain.GapImportanceNiceToHave},
			},
			expected: 97.0,
		},
		{
			name: "suggested gap acts as nice to have alias (-3)",
			gaps: []domain.GapItem{
				{Requirement: "G1", Importance: domain.GapImportanceSuggested},
			},
			expected: 97.0,
		},
		{
			name: "combined gaps: 1 critical, 1 preferred, 1 nice_to_have = -25",
			gaps: []domain.GapItem{
				{Requirement: "G1", Importance: domain.GapImportanceCritical},
				{Requirement: "G2", Importance: domain.GapImportancePreferred},
				{Requirement: "G3", Importance: domain.GapImportanceNiceToHave},
			},
			expected: 75.0,
		},
		{
			name: "many critical gaps clamp at 0.0",
			gaps: []domain.GapItem{
				{Requirement: "G1", Importance: domain.GapImportanceCritical},
				{Requirement: "G2", Importance: domain.GapImportanceCritical},
				{Requirement: "G3", Importance: domain.GapImportanceCritical},
				{Requirement: "G4", Importance: domain.GapImportanceCritical},
				{Requirement: "G5", Importance: domain.GapImportanceCritical},
				{Requirement: "G6", Importance: domain.GapImportanceCritical},
				{Requirement: "G7", Importance: domain.GapImportanceCritical},
			},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := domain.CalculateDeterministicMatchScore(tt.gaps)
			if score != tt.expected {
				t.Errorf("CalculateDeterministicMatchScore() = %v, expected %v", score, tt.expected)
			}
		})
	}
}

func TestCalculateSkillCoverageRatio(t *testing.T) {
	candidateSkills := []domain.Skill{
		{Name: "Go"},
		{Name: "Kubernetes"},
		{Name: "PostgreSQL"},
		{Name: "Kafka"},
	}

	t.Run("calculates ratio when candidate has matching skills", func(t *testing.T) {
		gaps := []domain.GapItem{
			{Requirement: "Raft consensus protocol"},
		}
		target := "Must be experienced in Go and Kubernetes and Raft"

		// Go and Kubernetes match (2 matched). 1 gap.
		// total = 2 + 1 = 3 -> ratio = 2/3 = 0.67
		ratio := domain.CalculateSkillCoverageRatio(candidateSkills, gaps, target)
		if ratio != 0.67 {
			t.Errorf("expected 0.67, got %v", ratio)
		}
	})

	t.Run("returns 1.0 when candidate has no gaps", func(t *testing.T) {
		gaps := []domain.GapItem{}
		target := "Experienced in Go and PostgreSQL"
		ratio := domain.CalculateSkillCoverageRatio(candidateSkills, gaps, target)
		if ratio != 1.0 {
			t.Errorf("expected 1.0, got %v", ratio)
		}
	})

	t.Run("returns 0.0 when candidate has zero matching skills and multiple gaps", func(t *testing.T) {
		gaps := []domain.GapItem{
			{Requirement: "Cobol"},
			{Requirement: "Mainframe"},
		}
		target := "Cobol and Mainframe systems"
		ratio := domain.CalculateSkillCoverageRatio(candidateSkills, gaps, target)
		if ratio != 0.0 {
			t.Errorf("expected 0.0, got %v", ratio)
		}
	})
}

func TestTailoredContent_OmitZeroNonTailoringFields(t *testing.T) {
	t.Run("Location.IsZero correctly identifies empty locations", func(t *testing.T) {
		emptyLoc := domain.Location{}
		if !emptyLoc.IsZero() {
			t.Errorf("expected empty Location to be zero")
		}

		populatedLoc := domain.Location{City: "San Francisco"}
		if populatedLoc.IsZero() {
			t.Errorf("expected populated Location to not be zero")
		}
	})

	t.Run("omitzero and omitempty omit unpopulated location and contact fields", func(t *testing.T) {
		content := domain.TailoredContent{
			Summary: "Lead developer summary.",
			Experiences: []domain.TailoredExperience{
				{
					SourceEntityID: "exp-1",
					Company:        "Stripe",
					RoleTitle:      "Staff Engineer",
					StartDate:      domain.NewDate(2022, 1, 1),
					Bullets: []domain.TailoredBullet{
						{
							Text:          "Scaled payment engine.",
							SourceFactIDs: []string{"fact-1"},
						},
					},
				},
			},
			Education: []domain.TailoredEducation{
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


