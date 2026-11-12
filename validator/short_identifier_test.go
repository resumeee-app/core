package validator_test

import (
	"strings"
	"testing"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/validator"
)

// snapshotWithShortIDs constructs a sample MainProfileSnapshot populated with both canonical UUIDs
// and compact short IDs across skills, experiences, projects, and facts for provenance testing.
func snapshotWithShortIDs() *core.MainProfileSnapshot {
	endDate := core.NewDate(2023, 12, 31)
	projStart := core.NewDate(2021, 6, 1)
	return &core.MainProfileSnapshot{
		Profile: core.Profile{
			ID:       "prof-uuid-12345678-abcd-1111-2222-333344445555",
			FullName: "Grace Hopper",
		},
		Skills: []core.Skill{
			{
				ID:       "skill-uuid-0001-abcd-1111-2222-333344445555",
				ShortID:  "skill-go",
				Name:     "Go",
				Category: "Backend",
			},
			{
				ID:       "skill-uuid-0002-abcd-1111-2222-333344445555",
				ShortID:  "skill-k8s",
				Name:     "Kubernetes",
				Category: "Infrastructure",
			},
		},
		Experiences: []core.WorkExperience{
			{
				ID:        "exp-uuid-1111-abcd-1111-2222-333344445555",
				ShortID:   "exp-1",
				Company:   "Acme Systems",
				RoleTitle: "Staff Engineer",
				StartDate: core.NewDate(2020, 1, 1),
				EndDate:   &endDate,
				Facts: []core.Fact{
					{
						ID:        "fact-uuid-1001-abcd-1111-2222-333344445555",
						ShortID:   "fact-1",
						OwnerID:   "exp-uuid-1111-abcd-1111-2222-333344445555",
						OwnerType: core.OwnerTypeWorkExperience,
						Statement: "Designed distributed consensus engine",
					},
					{
						ID:        "fact-uuid-1002-abcd-1111-2222-333344445555",
						ShortID:   "fact-2",
						OwnerID:   "exp-uuid-1111-abcd-1111-2222-333344445555",
						OwnerType: core.OwnerTypeWorkExperience,
						Statement: "Reduced p99 latency by 45%",
					},
				},
			},
			{
				ID:        "exp-uuid-2222-abcd-1111-2222-333344445555",
				ShortID:   "exp-2",
				Company:   "Globex Corp",
				RoleTitle: "Senior Developer",
				StartDate: core.NewDate(2017, 1, 1),
				Facts: []core.Fact{
					{
						ID:        "fact-uuid-2001-abcd-1111-2222-333344445555",
						ShortID:   "fact-3",
						OwnerID:   "exp-uuid-2222-abcd-1111-2222-333344445555",
						OwnerType: core.OwnerTypeWorkExperience,
						Statement: "Led payment processing pipeline overhaul",
					},
				},
			},
		},
		Projects: []core.Project{
			{
				ID:        "proj-uuid-3333-abcd-1111-2222-333344445555",
				ShortID:   "proj-1",
				Name:      "Distributed KV Store",
				StartDate: &projStart,
				Facts: []core.Fact{
					{
						ID:        "fact-uuid-3001-abcd-1111-2222-333344445555",
						ShortID:   "fact-proj-1",
						OwnerID:   "proj-uuid-3333-abcd-1111-2222-333344445555",
						OwnerType: core.OwnerTypeProject,
						Statement: "Achieved 2M ops/sec with Raft consensus",
					},
				},
			},
		},
	}
}

// TestValidate_ShortIDCitations verifies that candidate content exclusively using short IDs
// for experiences, skills, and fact citations passes provenance validation cleanly without warnings.
func TestValidate_ShortIDCitations(t *testing.T) {
	snap := snapshotWithShortIDs()
	content := &core.TailoredContent{
		Summary: "Systems engineer specializing in high-throughput architectures.",
		SkillGroups: []core.TailoredSkillGroup{
			{
				Category:       "Backend",
				Skills:         []string{"Go"},
				SourceSkillIDs: []string{"skill-go"},
			},
		},
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Acme Systems",
				RoleTitle:      "Staff Engineer",
				StartDate:      core.NewDate(2020, 1, 1),
				Bullets: []core.TailoredBullet{
					{
						Text:           "Designed distributed consensus engine handling enterprise workloads.",
						SourceFactIDs:  []string{"fact-1"},
						SourceSkillIDs: []string{"skill-go"},
					},
				},
			},
		},
	}

	res, err := validator.Validate(content, snap, nil)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if len(res.Warnings) > 0 {
		t.Fatalf("expected 0 warnings, got %v", res.Warnings)
	}
	if len(res.SanitizedContent.Experiences) != 1 {
		t.Fatalf("expected 1 experience, got %d", len(res.SanitizedContent.Experiences))
	}
	exp := res.SanitizedContent.Experiences[0]
	if len(exp.Bullets) != 1 {
		t.Fatalf("expected 1 bullet, got %d", len(exp.Bullets))
	}
}

// TestValidate_MixedCitations verifies dual-key resolution when some entities/bullets use canonical
// UUIDs while others use compact short IDs in the same tailored resume payload.
func TestValidate_MixedCitations(t *testing.T) {
	snap := snapshotWithShortIDs()
	content := &core.TailoredContent{
		Summary: "Hybrid citation test.",
		Experiences: []core.TailoredExperience{
			{
				// exp-1 cited by ShortID, bullet fact cited by canonical UUID
				SourceEntityID: "exp-1",
				Company:        "Acme Systems",
				RoleTitle:      "Staff Engineer",
				StartDate:      core.NewDate(2020, 1, 1),
				Bullets: []core.TailoredBullet{
					{
						Text:          "Optimized latency.",
						SourceFactIDs: []string{"fact-uuid-1002-abcd-1111-2222-333344445555"},
					},
				},
			},
			{
				// exp-2 cited by canonical UUID, bullet fact cited by ShortID
				SourceEntityID: "exp-uuid-2222-abcd-1111-2222-333344445555",
				Company:        "Globex Corp",
				RoleTitle:      "Senior Developer",
				StartDate:      core.NewDate(2017, 1, 1),
				Bullets: []core.TailoredBullet{
					{
						Text:          "Led payment processing rewrite.",
						SourceFactIDs: []string{"fact-3"},
					},
				},
			},
		},
	}

	res, err := validator.Validate(content, snap, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Warnings) > 0 {
		t.Fatalf("expected 0 warnings for mixed citations, got %v", res.Warnings)
	}
	if len(res.SanitizedContent.Experiences) != 2 {
		t.Fatalf("expected 2 experiences, got %d", len(res.SanitizedContent.Experiences))
	}
}

// TestValidate_CrossEntitySpoof_ShortIDs verifies that citing a fact belonging to a different parent
// experience using short IDs triggers a [CROSS_ENTITY_SPOOF] warning and prunes the fraudulent bullet.
func TestValidate_CrossEntitySpoof_ShortIDs(t *testing.T) {
	snap := snapshotWithShortIDs()
	content := &core.TailoredContent{
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Acme Systems",
				RoleTitle:      "Staff Engineer",
				StartDate:      core.NewDate(2020, 1, 1),
				Bullets: []core.TailoredBullet{
					{
						// fact-3 belongs to exp-2, not exp-1!
						Text:          "Claiming payment processing pipeline bullet under Acme.",
						SourceFactIDs: []string{"fact-3"},
					},
				},
			},
		},
	}

	res, err := validator.Validate(content, snap, nil)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Fatalf("expected [CROSS_ENTITY_SPOOF] warning, got none")
	}
	foundSpoof := false
	for _, w := range res.Warnings {
		if containsSubstr(w, "[CROSS_ENTITY_SPOOF]") {
			foundSpoof = true
			break
		}
	}
	if !foundSpoof {
		t.Fatalf("expected [CROSS_ENTITY_SPOOF] warning, got %v", res.Warnings)
	}
	if len(res.SanitizedContent.Experiences[0].Bullets) != 0 {
		t.Fatalf("expected spoofed bullet to be pruned, got %d bullets", len(res.SanitizedContent.Experiences[0].Bullets))
	}
}

// TestValidate_FabricatedFact_ShortID verifies that citing a non-existent short fact ID (e.g. 'fact-999')
// triggers a [FABRICATED_FACT] warning and prunes the bullet.
func TestValidate_FabricatedFact_ShortID(t *testing.T) {
	snap := snapshotWithShortIDs()
	content := &core.TailoredContent{
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Acme Systems",
				RoleTitle:      "Staff Engineer",
				StartDate:      core.NewDate(2020, 1, 1),
				Bullets: []core.TailoredBullet{
					{
						Text:          "Invented breakthrough AI chip.",
						SourceFactIDs: []string{"fact-999"},
					},
				},
			},
		},
	}

	res, err := validator.Validate(content, snap, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	foundFabricated := false
	for _, w := range res.Warnings {
		if containsSubstr(w, "[FABRICATED_FACT]") {
			foundFabricated = true
			break
		}
	}
	if !foundFabricated {
		t.Fatalf("expected [FABRICATED_FACT] warning, got %v", res.Warnings)
	}
	if len(res.SanitizedContent.Experiences[0].Bullets) != 0 {
		t.Fatalf("expected fabricated bullet to be pruned")
	}
}

// TestValidate_BranchPinning_ShortID asserts that pinning an experience by short ID ('exp-1')
// is recognized properly without emitting false-positive [OMITTED_PINNED_ENTITY] warnings.
func TestValidate_BranchPinning_ShortID(t *testing.T) {
	snap := snapshotWithShortIDs()
	branch := &core.Branch{
		// Pinned using short ID
		PinnedEntityIDs: []string{"exp-1"},
	}
	content := &core.TailoredContent{
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Acme Systems",
				RoleTitle:      "Staff Engineer",
				StartDate:      core.NewDate(2020, 1, 1),
				Bullets: []core.TailoredBullet{
					{
						Text:          "Designed distributed consensus engine.",
						SourceFactIDs: []string{"fact-1"},
					},
				},
			},
		},
	}

	res, err := validator.Validate(content, snap, branch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, w := range res.Warnings {
		if containsSubstr(w, "[OMITTED_PINNED_ENTITY]") {
			t.Fatalf("unexpected [OMITTED_PINNED_ENTITY] warning when pinned short ID was included: %s", w)
		}
	}
}

// TestValidate_BranchExclusion_ShortID asserts that excluding an entity by short ID ('exp-1')
// properly prunes the entity and emits an [EXCLUDED_ENTITY] warning.
func TestValidate_BranchExclusion_ShortID(t *testing.T) {
	snap := snapshotWithShortIDs()
	branch := &core.Branch{
		// Excluded using short ID
		ExcludedEntityIDs: []string{"exp-1"},
	}
	content := &core.TailoredContent{
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Acme Systems",
				RoleTitle:      "Staff Engineer",
				StartDate:      core.NewDate(2020, 1, 1),
				Bullets: []core.TailoredBullet{
					{
						Text:          "Designed distributed consensus engine.",
						SourceFactIDs: []string{"fact-1"},
					},
				},
			},
			{
				SourceEntityID: "exp-2",
				Company:        "Globex Corp",
				RoleTitle:      "Senior Developer",
				StartDate:      core.NewDate(2017, 1, 1),
				Bullets: []core.TailoredBullet{
					{
						Text:          "Led payment processing overhaul.",
						SourceFactIDs: []string{"fact-3"},
					},
				},
			},
		},
	}

	res, err := validator.Validate(content, snap, branch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	foundExcluded := false
	for _, w := range res.Warnings {
		if containsSubstr(w, "[EXCLUDED_ENTITY]") {
			foundExcluded = true
			break
		}
	}
	if !foundExcluded {
		t.Fatalf("expected [EXCLUDED_ENTITY] warning, got %v", res.Warnings)
	}
	if len(res.SanitizedContent.Experiences) != 1 {
		t.Fatalf("expected 1 experience remaining after exclusion, got %d", len(res.SanitizedContent.Experiences))
	}
	if res.SanitizedContent.Experiences[0].Company != "Globex Corp" {
		t.Fatalf("expected Globex Corp to remain, got %s", res.SanitizedContent.Experiences[0].Company)
	}
}

// containsSubstr checks if string s contains substring substr.
func containsSubstr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && (strings.Contains(s, substr)))
}
