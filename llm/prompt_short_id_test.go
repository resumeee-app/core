package llm_test

import (
	"strings"
	"testing"
	"time"

	"resumeee/internal/domain"
	"resumeee/internal/llm"
)

// TestBuildPrompt_EntitiesUseShortIDs asserts that serialized master_profile_data contains
// short IDs instead of raw 40-character UUIDs across skills, experiences, facts, and projects.
func TestBuildPrompt_EntitiesUseShortIDs(t *testing.T) {
	uuidExp := "exp-9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d"
	uuidSkill := "skill-12345678-3b7d-4bad-9bdd-2b0d7b3dcb6d"
	uuidFact := "fact-3db884b2-f155-442b-9e0a-ef5f8b96d997"
	uuidProj := "proj-55556666-3b7d-4bad-9bdd-2b0d7b3dcb6d"

	req := domain.GenerationRequest{
		MasterProfile: &domain.MasterProfileSnapshot{
			Profile: domain.Profile{FullName: "Test Candidate"},
			Skills: []domain.Skill{
				{
					ID:      uuidSkill,
					ShortID: "skill-go",
					Name:    "Go",
				},
			},
			Experiences: []domain.WorkExperience{
				{
					ID:        uuidExp,
					ShortID:   "exp-1",
					Company:   "Acme Corp",
					RoleTitle: "Engineer",
					Facts: []domain.Fact{
						{
							ID:        uuidFact,
							ShortID:   "fact-1",
							OwnerID:   uuidExp,
							Statement: "Built high-throughput distributed system",
						},
					},
				},
			},
			Projects: []domain.Project{
				{
					ID:      uuidProj,
					ShortID: "proj-1",
					Name:    "Resumeee Engine",
				},
			},
			ExportedAt: time.Now(),
		},
		BranchContext: domain.Branch{
			TargetRole: "Senior Engineer",
		},
	}

	payload, err := llm.BuildPrompt(req)
	if err != nil {
		t.Fatalf("unexpected BuildPrompt error: %v", err)
	}

	// Verify UserPrompt master_profile_data does NOT contain the raw 40-char UUIDs
	if strings.Contains(payload.UserPrompt, uuidExp) {
		t.Errorf("UserPrompt master_profile_data unexpectedly leaked raw experience UUID: %s", uuidExp)
	}
	if strings.Contains(payload.UserPrompt, uuidSkill) {
		t.Errorf("UserPrompt master_profile_data unexpectedly leaked raw skill UUID: %s", uuidSkill)
	}
	if strings.Contains(payload.UserPrompt, uuidFact) {
		t.Errorf("UserPrompt master_profile_data unexpectedly leaked raw fact UUID: %s", uuidFact)
	}
	if strings.Contains(payload.UserPrompt, uuidProj) {
		t.Errorf("UserPrompt master_profile_data unexpectedly leaked raw project UUID: %s", uuidProj)
	}

	// Verify short IDs ARE present in master_profile_data
	if !strings.Contains(payload.UserPrompt, `"id": "exp-1"`) {
		t.Errorf("UserPrompt master_profile_data missing projected short ID 'exp-1'")
	}
	if !strings.Contains(payload.UserPrompt, `"id": "skill-go"`) {
		t.Errorf("UserPrompt master_profile_data missing projected short ID 'skill-go'")
	}
	if !strings.Contains(payload.UserPrompt, `"id": "fact-1"`) {
		t.Errorf("UserPrompt master_profile_data missing projected short ID 'fact-1'")
	}
	if !strings.Contains(payload.UserPrompt, `"id": "proj-1"`) {
		t.Errorf("UserPrompt master_profile_data missing projected short ID 'proj-1'")
	}
}

// TestBuildPrompt_FactOwnerUsesShortID asserts that child fact owner_id references within
// serialized master profile data are projected to the parent entity's short identifier.
func TestBuildPrompt_FactOwnerUsesShortID(t *testing.T) {
	uuidExp := "exp-9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d"
	uuidFact := "fact-3db884b2-f155-442b-9e0a-ef5f8b96d997"

	req := domain.GenerationRequest{
		MasterProfile: &domain.MasterProfileSnapshot{
			Profile: domain.Profile{FullName: "Test Candidate"},
			Experiences: []domain.WorkExperience{
				{
					ID:        uuidExp,
					ShortID:   "exp-1",
					Company:   "Acme Corp",
					RoleTitle: "Engineer",
					Facts: []domain.Fact{
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
		BranchContext: domain.Branch{
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

	req := domain.GenerationRequest{
		MasterProfile: &domain.MasterProfileSnapshot{
			Profile: domain.Profile{FullName: "Test Candidate"},
			Experiences: []domain.WorkExperience{
				{
					ID:        uuidExp,
					ShortID:   "exp-1",
					Company:   "Acme Corp",
					RoleTitle: "Engineer",
				},
			},
			Projects: []domain.Project{
				{
					ID:      uuidProj,
					ShortID: "proj-1",
					Name:    "Resumeee Engine",
				},
			},
		},
		BranchContext: domain.Branch{
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

	req := domain.GenerationRequest{
		MasterProfile: &domain.MasterProfileSnapshot{
			Profile: domain.Profile{FullName: "Test Candidate"},
			Skills: []domain.Skill{
				{ID: uuidSkill, ShortID: "skill-go", Name: "Go"},
			},
			Experiences: []domain.WorkExperience{
				{
					ID:        uuidExp,
					ShortID:   "exp-1",
					Company:   "Acme Corp",
					RoleTitle: "Engineer",
					Facts: []domain.Fact{
						{ID: uuidFact, ShortID: "fact-1", OwnerID: uuidExp, Statement: "Built distributed system"},
					},
				},
			},
		},
		BranchContext: domain.Branch{TargetRole: "Senior Engineer"},
		PriorVersion: &domain.TailoredContent{
			Experiences: []domain.TailoredExperience{
				{
					SourceEntityID: uuidExp,
					Company:        "Acme Corp",
					RoleTitle:      "Engineer",
					Bullets: []domain.TailoredBullet{
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
	req := domain.GenerationRequest{
		BranchContext: domain.Branch{TargetRole: "Staff Engineer"},
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
