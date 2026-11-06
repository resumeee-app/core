package llm_test

import (
	"strings"
	"testing"
	"time"

	"resumeee/internal/domain"
	"resumeee/internal/llm"
)

func TestPromptTemplates_EmbeddedFilesExist(t *testing.T) {
	requiredFiles := []string{
		"tailor_system.tmpl",
		"tailor_user.tmpl",
		"gap_analysis_system.tmpl",
		"gap_analysis_user.tmpl",
	}

	for _, file := range requiredFiles {
		content, err := llm.ReadTemplateFile(file)
		if err != nil {
			t.Errorf("expected embedded template file %q to be readable, got error: %v", file, err)
			continue
		}
		if len(strings.TrimSpace(content)) == 0 {
			t.Errorf("embedded template file %q is unexpectedly empty", file)
		}
	}
}

func TestBuildGapAnalysisPrompt_Composition(t *testing.T) {
	req := domain.GenerationRequest{
		MasterProfile: &domain.MasterProfileSnapshot{
			Profile: domain.Profile{FullName: "Jane Doe"},
			Skills: []domain.Skill{
				{ID: "skill-1", ShortID: "skill-1", Name: "Go"},
			},
			ExportedAt: time.Now(),
		},
		BranchContext: domain.Branch{
			TargetRole:         "Senior Backend Engineer",
			TargetCompany:      "Acme Cloud",
			TargetRequirements: "5+ years Go experience and distributed systems architecture.",
			BranchInstructions: "Focus on cloud-native patterns.",
		},
	}

	payload, err := llm.BuildGapAnalysisPrompt(req)
	if err != nil {
		t.Fatalf("unexpected error building gap analysis prompt: %v", err)
	}

	// System prompt checks
	if !strings.Contains(payload.SystemPrompt, "You are Resumeee AI, an expert career strategist and technical recruiter.") {
		t.Errorf("gap analysis system prompt missing introductory header")
	}
	if !strings.Contains(payload.SystemPrompt, "Anti-Hallucination (\"No Lies\" Invariant)") {
		t.Errorf("gap analysis system prompt missing anti-hallucination directive")
	}
	if !strings.Contains(payload.SystemPrompt, "Do NOT flag a qualification as missing if it is present or directly implied") {
		t.Errorf("gap analysis system prompt missing gap grounding rule")
	}

	// User prompt checks
	if !strings.Contains(payload.UserPrompt, "<target_context>") {
		t.Errorf("gap analysis user prompt missing target_context tag")
	}
	if !strings.Contains(payload.UserPrompt, "Target Role: Senior Backend Engineer") {
		t.Errorf("gap analysis user prompt missing target role")
	}
	if !strings.Contains(payload.UserPrompt, "Target Company: Acme Cloud") {
		t.Errorf("gap analysis user prompt missing target company")
	}
	if !strings.Contains(payload.UserPrompt, "5+ years Go experience and distributed systems architecture.") {
		t.Errorf("gap analysis user prompt missing target requirements")
	}
	if !strings.Contains(payload.UserPrompt, "Focus on cloud-native patterns.") {
		t.Errorf("gap analysis user prompt missing branch instructions")
	}
	if !strings.Contains(payload.UserPrompt, "<master_profile_data>") {
		t.Errorf("gap analysis user prompt missing master_profile_data tag")
	}
	if !strings.Contains(payload.UserPrompt, "Jane Doe") {
		t.Errorf("gap analysis user prompt missing candidate full name")
	}
}

func TestBuildPrompt_Composition(t *testing.T) {
	req := domain.GenerationRequest{
		GlobalRules:        "Tier 1: Never write passive voice sentences.",
		PresetInstructions: "Tier 2: Emphasize metrics and quantified business impact.",
		MasterProfile: &domain.MasterProfileSnapshot{
			Profile: domain.Profile{FullName: "Alex Mercer"},
			Experiences: []domain.WorkExperience{
				{
					ID:        "exp-1111-2222",
					ShortID:   "exp-1",
					Company:   "TechCorp",
					RoleTitle: "Staff Engineer",
					StartDate: "2020-01",
					Facts: []domain.Fact{
						{ID: "fact-1111-2222", ShortID: "f-1", Statement: "Architected event-driven microservices platform."},
					},
				},
			},
			Projects: []domain.Project{
				{
					ID:      "proj-3333-4444",
					ShortID: "proj-1",
					Name:    "OpenTelemetry Collector Plugin",
				},
			},
			Skills: []domain.Skill{
				{ID: "skill-5555-6666", ShortID: "sk-1", Name: "Go"},
			},
			ExportedAt: time.Now(),
		},
		BranchContext: domain.Branch{
			TargetRole:         "Principal Distributed Systems Engineer",
			TargetCompany:      "Stripe",
			TargetRequirements: "Deep expertise in consensus algorithms and high availability.",
			BranchInstructions: "Tier 3: Tailor for enterprise cloud scale.",
			PinnedEntityIDs:    []string{"exp-1111-2222"},
			ExcludedEntityIDs:  []string{"proj-3333-4444"},
		},
		PriorVersion: &domain.TailoredContent{
			Summary: "Prior active summary highlighting distributed systems.",
		},
	}

	payload, err := llm.BuildPrompt(req)
	if err != nil {
		t.Fatalf("unexpected error building prompt: %v", err)
	}

	// 1. System Prompt Assertions
	if !strings.Contains(payload.SystemPrompt, "<grounding_and_provenance_rules>") {
		t.Errorf("system prompt missing grounding & citation provenance rules tag")
	}
	if !strings.Contains(payload.SystemPrompt, "Anti-Hallucination (\"No Lies\" Invariant)") {
		t.Errorf("system prompt missing anti-hallucination directive")
	}
	if !strings.Contains(payload.SystemPrompt, "source_fact_ids") {
		t.Errorf("system prompt missing source_fact_ids requirement")
	}

	// 2. User Prompt - 3-Tier Rules Hierarchy
	if !strings.Contains(payload.UserPrompt, "<rules_hierarchy>") {
		t.Errorf("user prompt missing rules_hierarchy tag")
	}
	if !strings.Contains(payload.UserPrompt, "Tier 1: Global Writing Rules:\nTier 1: Never write passive voice sentences.") {
		t.Errorf("user prompt missing Tier 1 Global Rules")
	}
	if !strings.Contains(payload.UserPrompt, "Tier 2: Preset Formatting Instructions:\nTier 2: Emphasize metrics and quantified business impact.") {
		t.Errorf("user prompt missing Tier 2 Preset Instructions")
	}
	if !strings.Contains(payload.UserPrompt, "Custom Branch Instructions:\nTier 3: Tailor for enterprise cloud scale.") {
		t.Errorf("user prompt missing Tier 3 Branch Instructions")
	}

	// 3. User Prompt - Target Context & Entities
	if !strings.Contains(payload.UserPrompt, "Target Role: Principal Distributed Systems Engineer") {
		t.Errorf("user prompt missing target role")
	}
	if !strings.Contains(payload.UserPrompt, "Target Company: Stripe") {
		t.Errorf("user prompt missing target company")
	}
	if !strings.Contains(payload.UserPrompt, "Pinned Entity IDs (Must Include): exp-1") {
		t.Errorf("user prompt missing projected short ID for pinned entities")
	}
	if !strings.Contains(payload.UserPrompt, "Excluded Entity IDs (Must Omit): proj-1") {
		t.Errorf("user prompt missing projected short ID for excluded entities")
	}

	// 4. User Prompt - Prior Active Version & Master Profile Data
	if !strings.Contains(payload.UserPrompt, "<prior_active_version>") {
		t.Errorf("user prompt missing prior_active_version tag")
	}
	if !strings.Contains(payload.UserPrompt, "Prior active summary highlighting distributed systems.") {
		t.Errorf("user prompt missing prior version content")
	}
	if !strings.Contains(payload.UserPrompt, "<master_profile_data>") {
		t.Errorf("user prompt missing master_profile_data tag")
	}
	if !strings.Contains(payload.UserPrompt, "Alex Mercer") {
		t.Errorf("user prompt missing candidate full name")
	}
	if !strings.Contains(payload.UserPrompt, "f-1") {
		t.Errorf("user prompt missing projected short ID in profile data")
	}
}
