package llm_test

import (
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/llm"
)

// TestBuildPrompt_EntitiesUseShortIDs asserts that serialized main_profile_data contains
// short IDs instead of raw 40-character UUIDs across skills, experiences, facts, and projects.
func TestBuildPrompt_EntitiesUseShortIDs(t *testing.T) {
	uuidExp := "exp-9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d"
	uuidSkill := "skill-12345678-3b7d-4bad-9bdd-2b0d7b3dcb6d"
	uuidFact := "fact-3db884b2-f155-442b-9e0a-ef5f8b96d997"
	uuidProj := "proj-55556666-3b7d-4bad-9bdd-2b0d7b3dcb6d"

	req := core.GenerationRequest{
		MainProfile: &core.MainProfileSnapshot{
			Profile: core.Profile{FullName: "Test Candidate"},
			Skills: []core.Skill{
				{
					ID:      uuidSkill,
					ShortID: "skill-go",
					Name:    "Go",
				},
			},
			Experiences: []core.WorkExperience{
				{
					ID:        uuidExp,
					ShortID:   "exp-1",
					Company:   "Acme Corp",
					RoleTitle: "Engineer",
					Facts: []core.Fact{
						{
							ID:        uuidFact,
							ShortID:   "fact-1",
							OwnerID:   uuidExp,
							Statement: "Built high-throughput distributed system",
						},
					},
				},
			},
			Projects: []core.Project{
				{
					ID:      uuidProj,
					ShortID: "proj-1",
					Name:    "Resumeee Engine",
				},
			},
			ExportedAt: time.Now(),
		},
		BranchContext: core.Branch{
			TargetRole: "Senior Engineer",
		},
	}

	payload, err := llm.BuildPrompt(req)
	if err != nil {
		t.Fatalf("unexpected BuildPrompt error: %v", err)
	}

	// Verify UserPrompt main_profile_data does NOT contain the raw 40-char UUIDs
	if strings.Contains(payload.UserPrompt, uuidExp) {
		t.Errorf("UserPrompt main_profile_data unexpectedly leaked raw experience UUID: %s", uuidExp)
	}
	if strings.Contains(payload.UserPrompt, uuidSkill) {
		t.Errorf("UserPrompt main_profile_data unexpectedly leaked raw skill UUID: %s", uuidSkill)
	}
	if strings.Contains(payload.UserPrompt, uuidFact) {
		t.Errorf("UserPrompt main_profile_data unexpectedly leaked raw fact UUID: %s", uuidFact)
	}
	if strings.Contains(payload.UserPrompt, uuidProj) {
		t.Errorf("UserPrompt main_profile_data unexpectedly leaked raw project UUID: %s", uuidProj)
	}

	// Verify short IDs ARE present in main_profile_data
	if !strings.Contains(payload.UserPrompt, `"id": "exp-1"`) {
		t.Errorf("UserPrompt main_profile_data missing projected short ID 'exp-1'")
	}
	if !strings.Contains(payload.UserPrompt, `"id": "skill-go"`) {
		t.Errorf("UserPrompt main_profile_data missing projected short ID 'skill-go'")
	}
	if !strings.Contains(payload.UserPrompt, `"id": "fact-1"`) {
		t.Errorf("UserPrompt main_profile_data missing projected short ID 'fact-1'")
	}
	if !strings.Contains(payload.UserPrompt, `"id": "proj-1"`) {
		t.Errorf("UserPrompt main_profile_data missing projected short ID 'proj-1'")
	}
}

// TestBuildPrompt_FactOwnerUsesShortID asserts that child fact owner_id references within
// serialized main profile data are projected to the parent entity's short identifier.
func TestBuildPrompt_FactOwnerUsesShortID(t *testing.T) {
	uuidExp := "exp-9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d"
	uuidFact := "fact-3db884b2-f155-442b-9e0a-ef5f8b96d997"

	req := core.GenerationRequest{
		MainProfile: &core.MainProfileSnapshot{
			Profile: core.Profile{FullName: "Test Candidate"},
			Experiences: []core.WorkExperience{
				{
					ID:        uuidExp,
					ShortID:   "exp-1",
					Company:   "Acme Corp",
					RoleTitle: "Engineer",
					Facts: []core.Fact{
						{
							ID:        uuidFact,
							ShortID:   "fact-1",
							OwnerID:   uuidExp,
							Statement: "Built high-throughput distributed system",
						},
					},
				},
			},
		},
		BranchContext: core.Branch{
			TargetRole: "Senior Engineer",
		},
	}

	payload, err := llm.BuildPrompt(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The fact owner_id in the serialized JSON must be projected to "exp-1"
	if !strings.Contains(payload.UserPrompt, `"owner_id": "exp-1"`) {
		t.Errorf("expected fact owner_id to be 'exp-1', got prompt:\n%s", payload.UserPrompt)
	}
}

// TestBuildPrompt_PinnedAndExcludedUseShortID asserts that branch pinned and excluded entity
// filters are displayed using their projected short IDs in prompt instructions.
func TestBuildPrompt_PinnedAndExcludedUseShortID(t *testing.T) {
	uuidExp := "exp-9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d"
	uuidProj := "proj-55556666-3b7d-4bad-9bdd-2b0d7b3dcb6d"

	req := core.GenerationRequest{
		MainProfile: &core.MainProfileSnapshot{
			Profile: core.Profile{FullName: "Test Candidate"},
			Experiences: []core.WorkExperience{
				{
					ID:        uuidExp,
					ShortID:   "exp-1",
					Company:   "Acme Corp",
					RoleTitle: "Engineer",
				},
			},
			Projects: []core.Project{
				{
					ID:      uuidProj,
					ShortID: "proj-1",
					Name:    "Resumeee Engine",
				},
			},
		},
		BranchContext: core.Branch{
			PinnedEntityIDs:   []string{uuidExp},
			ExcludedEntityIDs: []string{uuidProj},
		},
	}

	payload, err := llm.BuildPrompt(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(payload.UserPrompt, "Pinned Entity IDs (Must Include): exp-1") {
		t.Errorf("expected Pinned Entity IDs to display 'exp-1', got prompt:\n%s", payload.UserPrompt)
	}
	if !strings.Contains(payload.UserPrompt, "Excluded Entity IDs (Must Omit): proj-1") {
		t.Errorf("expected Excluded Entity IDs to display 'proj-1', got prompt:\n%s", payload.UserPrompt)
	}
}

// TestBuildPrompt_PriorVersionUsesShortID asserts that prior active version tailored content
// projected into the prompt has its experience source_entity_id and citation IDs converted to short IDs.
func TestBuildPrompt_PriorVersionUsesShortID(t *testing.T) {
	uuidExp := "exp-9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d"
	uuidFact := "fact-3db884b2-f155-442b-9e0a-ef5f8b96d997"
	uuidSkill := "skill-12345678-3b7d-4bad-9bdd-2b0d7b3dcb6d"

	req := core.GenerationRequest{
		MainProfile: &core.MainProfileSnapshot{
			Profile: core.Profile{FullName: "Test Candidate"},
			Skills: []core.Skill{
				{ID: uuidSkill, ShortID: "skill-go", Name: "Go"},
			},
			Experiences: []core.WorkExperience{
				{
					ID:        uuidExp,
					ShortID:   "exp-1",
					Company:   "Acme Corp",
					RoleTitle: "Engineer",
					Facts: []core.Fact{
						{ID: uuidFact, ShortID: "fact-1", OwnerID: uuidExp, Statement: "Built distributed system"},
					},
				},
			},
		},
		BranchContext: core.Branch{TargetRole: "Senior Engineer"},
		PriorVersion: &core.TailoredContent{
			Experiences: []core.TailoredExperience{
				{
					SourceEntityID: uuidExp,
					Company:        "Acme Corp",
					RoleTitle:      "Engineer",
					Bullets: []core.TailoredBullet{
						{
							Text:           "Built distributed system with 99.99% uptime.",
							SourceFactIDs:  []string{uuidFact},
							SourceSkillIDs: []string{uuidSkill},
						},
					},
				},
			},
		},
	}

	payload, err := llm.BuildPrompt(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// In <prior_active_version>, citations should be projected into short IDs
	if strings.Contains(payload.UserPrompt, uuidExp) {
		t.Errorf("Prior version leaked raw UUID %s", uuidExp)
	}
	if strings.Contains(payload.UserPrompt, uuidFact) {
		t.Errorf("Prior version leaked raw UUID %s", uuidFact)
	}
	if strings.Contains(payload.UserPrompt, uuidSkill) {
		t.Errorf("Prior version leaked raw UUID %s", uuidSkill)
	}
	if !strings.Contains(payload.UserPrompt, `"source_entity_id": "exp-1"`) {
		t.Errorf("expected prior version source_entity_id to be 'exp-1'")
	}
	if !strings.Contains(payload.UserPrompt, `"source_fact_ids": [
        "fact-1"
      ]`) && !strings.Contains(payload.UserPrompt, `"fact-1"`) {
		t.Errorf("expected prior version source_fact_ids to contain 'fact-1'")
	}
}

// TestBuildPrompt_SystemRulesSpecifyShortIDs asserts that system prompt rules explicitly instruct
// the LLM to output short identifiers and reference examples like 'exp-1' and 'fact-1'.
func TestBuildPrompt_SystemRulesSpecifyShortIDs(t *testing.T) {
	req := core.GenerationRequest{
		BranchContext: core.Branch{TargetRole: "Staff Engineer"},
	}

	payload, err := llm.BuildPrompt(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(payload.SystemPrompt, "short identifiers") {
		t.Errorf("SystemPrompt missing 'short identifiers' specification: %s", payload.SystemPrompt)
	}
	if !strings.Contains(payload.SystemPrompt, "exp-1") || !strings.Contains(payload.SystemPrompt, "fact-1") {
		t.Errorf("SystemPrompt missing short identifier examples (exp-1, fact-1): %s", payload.SystemPrompt)
	}
	if strings.Contains(payload.SystemPrompt, "exact UUIDs") {
		t.Errorf("SystemPrompt should not reference 'exact UUIDs': %s", payload.SystemPrompt)
	}
}
