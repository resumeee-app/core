package validator_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/validator"
)

func sampleSnapshot() *core.MainProfileSnapshot {
	endDate := core.NewDate(2023, 12, 31)
	hours := 40
	contact := core.ContactDetail{Type: core.ContactTypePhone, Value: "+1-555-0199"}
	projEnd := core.NewDate(2022, 12, 31)
	eduEnd := core.NewDate(2018, 5, 31)
	certDate := core.NewDate(2022, 5, 1)

	return &core.MainProfileSnapshot{
		Profile: core.Profile{
			ID:       "prof-1",
			FullName: "Ada Lovelace",
		},
		Skills: []core.Skill{
			{ID: "skill-go", Name: "Go", Category: "Backend"},
			{ID: "skill-grpc", Name: "gRPC", Category: "Backend"},
			{ID: "skill-k8s", Name: "Kubernetes", Category: "Infrastructure"},
		},
		Experiences: []core.WorkExperience{
			{
				ID:             "exp-1",
				Company:        "Acme Corp",
				RoleTitle:      "Principal Engineer",
				StartDate:      core.NewDate(2021, 1, 1),
				EndDate:        &endDate,
				CompanyContact: &contact,
				HoursPerWeek:   &hours,
				Skills: []core.Skill{
					{ID: "skill-exp-nested", Name: "Postgres", Category: "Databases"},
				},
				Facts: []core.Fact{
					{
						ID:        "fact-exp-1",
						Statement: "Built event streaming pipeline",
						Skills: []core.Skill{
							{ID: "skill-fact-nested", Name: "Kafka", Category: "Streaming"},
						},
					},
					{ID: "fact-exp-2", OwnerID: "exp-1", OwnerType: core.OwnerTypeWorkExperience, Statement: "Scaled Postgres cluster to 100k QPS"},
				},
			},
			{
				ID:        "exp-2",
				Company:   "Beta LLC",
				RoleTitle: "Senior Engineer",
				StartDate: core.NewDate(2018, 1, 1),
				Facts: []core.Fact{
					{ID: "fact-exp-beta", OwnerID: "exp-2", OwnerType: core.OwnerTypeWorkExperience, Statement: "Led rewrite of auth service"},
				},
			},
		},
		Projects: []core.Project{
			{
				ID:      "proj-1",
				Name:    "OpenTelemetry Collector Exporter",
				EndDate: &projEnd,
				Skills: []core.Skill{
					{ID: "skill-proj-nested", Name: "OTel", Category: "Observability"},
				},
				Facts: []core.Fact{
					{
						ID:        "fact-proj-1",
						Statement: "Maintained custom exporter plugin",
						Skills: []core.Skill{
							{ID: "skill-proj-fact-nested", Name: "Go Collector", Category: "Observability"},
						},
					},
				},
			},
		},
		Education: []core.Education{
			{
				ID:          "edu-1",
				Institution: "MIT",
				Degree:      "B.S.",
				Field:       "Computer Science",
				StartDate:   core.DatePtr(core.NewDate(2014, 9, 1)),
				EndDate:     &eduEnd,
				Facts: []core.Fact{
					{
						ID:        "fact-edu-1",
						Statement: "Published research on distributed consensus",
						Skills: []core.Skill{
							{ID: "skill-edu-fact-nested", Name: "Paxos", Category: "Distributed"},
						},
					},
				},
			},
		},
		Certifications: []core.Certification{
			{
				ID:     "cert-1",
				Name:   "Certified Kubernetes Administrator",
				Issuer: "CNCF",
				Date:   &certDate,
			},
		},
		Facts: []core.Fact{
			{
				ID:        "fact-top-level",
				OwnerID:   "exp-1",
				OwnerType: core.OwnerTypeWorkExperience,
				Statement: "Top-level indexed fact",
				Skills: []core.Skill{
					{ID: "skill-top-fact-nested", Name: "Docker", Category: "Containers"},
				},
			},
		},
	}
}

func sampleValidContent() *core.TailoredContent {
	endDate := core.NewDate(2023, 12, 31)
	hours := 40
	contact := core.ContactDetail{Type: core.ContactTypePhone, Value: "+1-555-0199"}
	projEnd := core.NewDate(2022, 12, 31)
	eduEnd := core.NewDate(2018, 5, 31)
	certDate := core.NewDate(2022, 5, 1)

	return &core.TailoredContent{
		Summary: "Experienced systems engineer focused on distributed systems.",
		SkillGroups: []core.TailoredSkillGroup{
			{
				Category:       "Backend",
				Skills:         []string{"Go", "gRPC"},
				SourceSkillIDs: []string{"skill-go", "skill-grpc"},
			},
			{
				Category:       "Infrastructure",
				Skills:         []string{"Kubernetes"},
				SourceSkillIDs: []string{"skill-k8s"},
			},
		},
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Acme Corp",
				RoleTitle:      "Principal Engineer",
				StartDate:      core.NewDate(2021, 1, 1),
				EndDate:        &endDate,
				CompanyContact: &contact,
				HoursPerWeek:   &hours,
				Bullets: []core.TailoredBullet{
					{
						Text:           "Architected streaming pipeline handling 100k eps.",
						SourceFactIDs:  []string{"fact-exp-1"},
						SourceSkillIDs: []string{"skill-go"},
					},
					{
						Text:           "Optimized Postgres cluster to withstand 100k QPS peak.",
						SourceFactIDs:  []string{"fact-exp-2"},
						SourceSkillIDs: []string{"skill-grpc"},
					},
				},
			},
			{
				SourceEntityID: "exp-2",
				Company:        "Beta LLC",
				RoleTitle:      "Senior Engineer",
				StartDate:      core.NewDate(2018, 1, 1),
				Bullets: []core.TailoredBullet{
					{
						Text:          "Spearheaded auth rewrite with zero downtime.",
						SourceFactIDs: []string{"fact-exp-beta"},
					},
				},
			},
		},
		Projects: []core.TailoredProject{
			{
				SourceEntityID: "proj-1",
				Name:           "OpenTelemetry Collector Exporter",
				EndDate:        &projEnd,
				Bullets: []core.TailoredBullet{
					{
						Text:          "Maintained and released high-throughput plugin.",
						SourceFactIDs: []string{"fact-proj-1"},
					},
				},
			},
		},
		Education: []core.TailoredEducation{
			{
				SourceEntityID: "edu-1",
				Institution:    "MIT",
				Degree:         "B.S.",
				Field:          "Computer Science",
				StartDate:      core.DatePtr(core.NewDate(2014, 9, 1)),
				EndDate:        &eduEnd,
				Bullets: []core.TailoredBullet{
					{
						Text:          "Published distributed consensus research in ACM.",
						SourceFactIDs: []string{"fact-edu-1"},
					},
				},
			},
		},
		Certifications: []core.TailoredCert{
			{
				SourceEntityID: "cert-1",
				Name:           "Certified Kubernetes Administrator",
				Issuer:         "CNCF",
				Date:           &certDate,
			},
		},
	}
}

func TestValidate_ValidCitations_Clean(t *testing.T) {
	snapshot := sampleSnapshot()
	content := sampleValidContent()

	res, err := validator.Validate(content, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.Warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(res.Warnings), res.Warnings)
	}

	if !reflect.DeepEqual(res.SanitizedContent, content) {
		t.Fatalf("sanitized content does not match input:\nwant: %+v\ngot:  %+v", content, res.SanitizedContent)
	}
}

func TestValidate_FabricatedEntityIDs(t *testing.T) {
	snapshot := sampleSnapshot()
	content := sampleValidContent()

	// Inject fabricated entity IDs
	content.Experiences = append(content.Experiences, core.TailoredExperience{
		SourceEntityID: "fabricated-exp-uuid",
		Company:        "Fake Tech",
		RoleTitle:      "Ghost Engineer",
		Bullets: []core.TailoredBullet{
			{Text: "Invented warp drive", SourceFactIDs: []string{"fact-exp-1"}},
		},
	})
	content.Projects = append(content.Projects, core.TailoredProject{
		SourceEntityID: "fabricated-proj-uuid",
		Name:           "Fake Blockchain",
		Bullets: []core.TailoredBullet{
			{Text: "Minted fake coins", SourceFactIDs: []string{"fact-proj-1"}},
		},
	})
	content.Education = append(content.Education, core.TailoredEducation{
		SourceEntityID: "fabricated-edu-uuid",
		Institution:    "Fake University",
	})
	content.Certifications = append(content.Certifications, core.TailoredCert{
		SourceEntityID: "fabricated-cert-uuid",
		Name:           "Certified Fake Architect",
	})

	res, err := validator.Validate(content, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all fabricated entities were pruned
	for _, exp := range res.SanitizedContent.Experiences {
		if exp.SourceEntityID == "fabricated-exp-uuid" {
			t.Fatalf("fabricated experience was not pruned")
		}
	}
	for _, proj := range res.SanitizedContent.Projects {
		if proj.SourceEntityID == "fabricated-proj-uuid" {
			t.Fatalf("fabricated project was not pruned")
		}
	}
	for _, edu := range res.SanitizedContent.Education {
		if edu.SourceEntityID == "fabricated-edu-uuid" {
			t.Fatalf("fabricated education was not pruned")
		}
	}
	for _, cert := range res.SanitizedContent.Certifications {
		if cert.SourceEntityID == "fabricated-cert-uuid" {
			t.Fatalf("fabricated cert was not pruned")
		}
	}

	// Verify warnings contain [UNGROUNDED_ENTITY]
	foundExp, foundProj, foundEdu, foundCert := false, false, false, false
	for _, w := range res.Warnings {
		if strings.Contains(w, "[UNGROUNDED_ENTITY]") && strings.Contains(w, "fabricated-exp-uuid") {
			foundExp = true
		}
		if strings.Contains(w, "[UNGROUNDED_ENTITY]") && strings.Contains(w, "fabricated-proj-uuid") {
			foundProj = true
		}
		if strings.Contains(w, "[UNGROUNDED_ENTITY]") && strings.Contains(w, "fabricated-edu-uuid") {
			foundEdu = true
		}
		if strings.Contains(w, "[UNGROUNDED_ENTITY]") && strings.Contains(w, "fabricated-cert-uuid") {
			foundCert = true
		}
	}

	if !foundExp || !foundProj || !foundEdu || !foundCert {
		t.Fatalf("missing expected UNGROUNDED_ENTITY warnings: %v", res.Warnings)
	}
}

func TestValidate_FabricatedFactIDs(t *testing.T) {
	snapshot := sampleSnapshot()
	content := sampleValidContent()

	// Add fabricated fact ID to exp-1
	content.Experiences[0].Bullets = append(content.Experiences[0].Bullets, core.TailoredBullet{
		Text:          "Claimed impossible hallucinated metric.",
		SourceFactIDs: []string{"fact-fabricated-999"},
	})

	res, err := validator.Validate(content, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.SanitizedContent.Experiences[0].Bullets) != 2 {
		t.Fatalf("expected 2 valid bullets in exp-1, got %d", len(res.SanitizedContent.Experiences[0].Bullets))
	}

	foundWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "[FABRICATED_FACT]") && strings.Contains(w, "fact-fabricated-999") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected FABRICATED_FACT warning in %v", res.Warnings)
	}
}

func TestValidate_EmptyFactIDs(t *testing.T) {
	snapshot := sampleSnapshot()
	content := sampleValidContent()

	content.Experiences[0].Bullets = append(content.Experiences[0].Bullets, core.TailoredBullet{
		Text:          "Uncited bullet with no source fact ids.",
		SourceFactIDs: []string{},
	})
	content.Experiences[0].Bullets = append(content.Experiences[0].Bullets, core.TailoredBullet{
		Text:          "Nil source fact ids bullet.",
		SourceFactIDs: nil,
	})

	res, err := validator.Validate(content, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.SanitizedContent.Experiences[0].Bullets) != 2 {
		t.Fatalf("expected uncited bullets to be pruned, got %d bullets", len(res.SanitizedContent.Experiences[0].Bullets))
	}

	count := 0
	for _, w := range res.Warnings {
		if strings.Contains(w, "[UNGROUNDED_BULLET]") {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 UNGROUNDED_BULLET warnings, got %d: %v", count, res.Warnings)
	}
}

func TestValidate_CrossEntityFactSpoofing(t *testing.T) {
	snapshot := sampleSnapshot()
	content := sampleValidContent()

	// Bullet under experience exp-1 cites a fact belonging to Project proj-1
	content.Experiences[0].Bullets = append(content.Experiences[0].Bullets, core.TailoredBullet{
		Text:          "Citing project achievement under work experience.",
		SourceFactIDs: []string{"fact-proj-1"},
	})

	res, err := validator.Validate(content, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.SanitizedContent.Experiences[0].Bullets) != 2 {
		t.Fatalf("expected cross-entity spoof bullet to be pruned, got %d", len(res.SanitizedContent.Experiences[0].Bullets))
	}

	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "[CROSS_ENTITY_SPOOF]") && strings.Contains(w, "fact-proj-1") && strings.Contains(w, "proj-1") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected CROSS_ENTITY_SPOOF warning, got: %v", res.Warnings)
	}
}

func TestValidate_CrossExperienceFactSpoofing(t *testing.T) {
	snapshot := sampleSnapshot()
	content := sampleValidContent()

	// Bullet under experience exp-1 cites fact-exp-beta belonging to exp-2
	content.Experiences[0].Bullets = append(content.Experiences[0].Bullets, core.TailoredBullet{
		Text:          "Attributing Beta LLC achievement to Acme Corp.",
		SourceFactIDs: []string{"fact-exp-beta"},
	})

	res, err := validator.Validate(content, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.SanitizedContent.Experiences[0].Bullets) != 2 {
		t.Fatalf("expected cross-experience spoof bullet to be pruned")
	}

	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "[CROSS_ENTITY_SPOOF]") && strings.Contains(w, "fact-exp-beta") && strings.Contains(w, "exp-2") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected CROSS_ENTITY_SPOOF warning for cross-experience spoof, got: %v", res.Warnings)
	}
}

func TestValidate_BranchExclusions(t *testing.T) {
	snapshot := sampleSnapshot()
	content := sampleValidContent()

	branch := &core.Branch{
		ID:                "branch-1",
		Name:              "Target Role",
		ExcludedEntityIDs: []string{"exp-2", "proj-1", "edu-1", "cert-1"},
	}

	res, err := validator.Validate(content, snapshot, branch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.SanitizedContent.Experiences) != 1 || res.SanitizedContent.Experiences[0].SourceEntityID != "exp-1" {
		t.Fatalf("expected exp-2 to be pruned, got: %+v", res.SanitizedContent.Experiences)
	}
	if len(res.SanitizedContent.Projects) != 0 {
		t.Fatalf("expected proj-1 to be pruned, got: %+v", res.SanitizedContent.Projects)
	}
	if len(res.SanitizedContent.Education) != 0 {
		t.Fatalf("expected edu-1 to be pruned, got: %+v", res.SanitizedContent.Education)
	}
	if len(res.SanitizedContent.Certifications) != 0 {
		t.Fatalf("expected cert-1 to be pruned, got: %+v", res.SanitizedContent.Certifications)
	}

	foundExpExcluded := false
	foundProjExcluded := false
	foundEduExcluded := false
	foundCertExcluded := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "[EXCLUDED_ENTITY]") && strings.Contains(w, "exp-2") {
			foundExpExcluded = true
		}
		if strings.Contains(w, "[EXCLUDED_ENTITY]") && strings.Contains(w, "proj-1") {
			foundProjExcluded = true
		}
		if strings.Contains(w, "[EXCLUDED_ENTITY]") && strings.Contains(w, "edu-1") {
			foundEduExcluded = true
		}
		if strings.Contains(w, "[EXCLUDED_ENTITY]") && strings.Contains(w, "cert-1") {
			foundCertExcluded = true
		}
	}
	if !foundExpExcluded || !foundProjExcluded || !foundEduExcluded || !foundCertExcluded {
		t.Fatalf("expected all EXCLUDED_ENTITY warnings, got: %v", res.Warnings)
	}
}

func TestValidate_BranchExclusions_FallbackNames(t *testing.T) {
	snapshot := sampleSnapshot()
	content := &core.TailoredContent{
		Experiences: []core.TailoredExperience{
			{SourceEntityID: "exp-1"}, // Empty Company
		},
		Projects: []core.TailoredProject{
			{SourceEntityID: "proj-1"}, // Empty Name
		},
		Education: []core.TailoredEducation{
			{SourceEntityID: "edu-1"}, // Empty Institution
		},
		Certifications: []core.TailoredCert{
			{SourceEntityID: "cert-1"}, // Empty Name
		},
	}

	branch := &core.Branch{
		ExcludedEntityIDs: []string{"exp-1", "proj-1", "edu-1", "cert-1"},
	}

	res, err := validator.Validate(content, snapshot, branch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should extract fallback names from snapshot
	warningsJoined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(warningsJoined, "Acme Corp") {
		t.Errorf("missing fallback company Acme Corp: %v", res.Warnings)
	}
	if !strings.Contains(warningsJoined, "OpenTelemetry Collector Exporter") {
		t.Errorf("missing fallback project name: %v", res.Warnings)
	}
	if !strings.Contains(warningsJoined, "MIT") {
		t.Errorf("missing fallback education institution: %v", res.Warnings)
	}
	if !strings.Contains(warningsJoined, "Certified Kubernetes Administrator") {
		t.Errorf("missing fallback cert name: %v", res.Warnings)
	}
}

func TestValidate_BranchPinnedOmission(t *testing.T) {
	snapshot := sampleSnapshot()
	content := sampleValidContent()

	snapshot.Projects = append(snapshot.Projects, core.Project{
		ID:   "proj-pinned",
		Name: "Critical Open Source Project",
	})

	branch := &core.Branch{
		ID:              "branch-1",
		PinnedEntityIDs: []string{"proj-pinned"},
	}

	res, err := validator.Validate(content, snapshot, branch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "[OMITTED_PINNED_ENTITY]") && strings.Contains(w, "proj-pinned") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected OMITTED_PINNED_ENTITY warning, got: %v", res.Warnings)
	}
}

func TestValidate_UngroundedSkills(t *testing.T) {
	snapshot := sampleSnapshot()
	content := sampleValidContent()

	// Add fabricated skill to skill group
	content.SkillGroups[0].Skills = append(content.SkillGroups[0].Skills, "HallucinatedLang")
	content.SkillGroups[0].SourceSkillIDs = append(content.SkillGroups[0].SourceSkillIDs, "skill-fabricated")

	// Add fabricated skill to bullet
	content.Experiences[0].Bullets[0].SourceSkillIDs = append(content.Experiences[0].Bullets[0].SourceSkillIDs, "skill-fake-bullet")

	res, err := validator.Validate(content, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Bullet should still be present, but bullet SourceSkillIDs must not have "skill-fake-bullet"
	bullet := res.SanitizedContent.Experiences[0].Bullets[0]
	for _, sid := range bullet.SourceSkillIDs {
		if sid == "skill-fake-bullet" {
			t.Fatalf("bullet still contains ungrounded skill citation")
		}
	}

	// Skill group should have pruned "HallucinatedLang" and "skill-fabricated"
	group := res.SanitizedContent.SkillGroups[0]
	for _, sid := range group.SourceSkillIDs {
		if sid == "skill-fabricated" {
			t.Fatalf("skill group still contains ungrounded skill ID")
		}
	}
	for _, s := range group.Skills {
		if s == "HallucinatedLang" {
			t.Fatalf("skill group still contains ungrounded skill name")
		}
	}

	foundGroupWarning := false
	foundBulletWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "[UNGROUNDED_SKILL]") && strings.Contains(w, "skill-fabricated") {
			foundGroupWarning = true
		}
		if strings.Contains(w, "[UNGROUNDED_BULLET_SKILL]") && strings.Contains(w, "skill-fake-bullet") {
			foundBulletWarning = true
		}
	}
	if !foundGroupWarning || !foundBulletWarning {
		t.Fatalf("missing expected skill warnings: %v", res.Warnings)
	}
}

func TestValidate_SkillGroupVariations(t *testing.T) {
	snapshot := sampleSnapshot()

	// 1. Unequal length with matching and ungrounded skills
	content := &core.TailoredContent{
		SkillGroups: []core.TailoredSkillGroup{
			{
				Category:       "Data",
				Skills:         []string{"Go", "UnrelatedSkill"},
				SourceSkillIDs: []string{"skill-go", "skill-unknown-999"},
			},
			{
				Category:       "EmptyCitation",
				Skills:         []string{"Python"},
				SourceSkillIDs: []string{}, // empty citations
			},
			{
				Category:       "EmptySkillsPopulatedFromIDs",
				Skills:         nil, // empty skills slice, valid IDs
				SourceSkillIDs: []string{"skill-go", "skill-grpc"},
			},
		},
	}

	res, err := validator.Validate(content, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.SanitizedContent.SkillGroups) != 2 {
		t.Fatalf("expected 2 skill groups, got %d", len(res.SanitizedContent.SkillGroups))
	}

	// Check populated names from IDs
	foundPopulated := false
	for _, g := range res.SanitizedContent.SkillGroups {
		if g.Category == "EmptySkillsPopulatedFromIDs" {
			foundPopulated = true
			if len(g.Skills) != 2 || g.Skills[0] != "Go" || g.Skills[1] != "gRPC" {
				t.Fatalf("expected skills to be populated from IDs, got: %v", g.Skills)
			}
		}
	}
	if !foundPopulated {
		t.Fatalf("missing EmptySkillsPopulatedFromIDs group")
	}
}

func TestValidate_EmptySkillGroupPruning(t *testing.T) {
	snapshot := sampleSnapshot()
	content := sampleValidContent()

	content.SkillGroups = append(content.SkillGroups, core.TailoredSkillGroup{
		Category:       "Voodoo Languages",
		Skills:         []string{"FakeLang1", "FakeLang2"},
		SourceSkillIDs: []string{"skill-fake-1", "skill-fake-2"},
	})

	res, err := validator.Validate(content, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, g := range res.SanitizedContent.SkillGroups {
		if g.Category == "Voodoo Languages" {
			t.Fatalf("expected completely invalid skill group to be pruned")
		}
	}
}

func TestValidate_NestedSkillsResolution(t *testing.T) {
	snapshot := sampleSnapshot()
	content := &core.TailoredContent{
		SkillGroups: []core.TailoredSkillGroup{
			{
				Category:       "ResolvedSkills",
				Skills:         []string{"Postgres", "Kafka", "OTel", "Go Collector", "Paxos", "Docker"},
				SourceSkillIDs: []string{"skill-exp-nested", "skill-fact-nested", "skill-proj-nested", "skill-proj-fact-nested", "skill-edu-fact-nested", "skill-top-fact-nested"},
			},
		},
	}

	res, err := validator.Validate(content, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(res.Warnings) != 0 {
		t.Fatalf("expected 0 warnings for nested skills resolution, got: %v", res.Warnings)
	}
	if len(res.SanitizedContent.SkillGroups[0].Skills) != 6 {
		t.Fatalf("expected all 6 nested skills to be resolved, got: %v", res.SanitizedContent.SkillGroups[0].Skills)
	}
}

func TestValidate_Immutability(t *testing.T) {
	snapshot := sampleSnapshot()
	content := sampleValidContent()

	// Clone original content to compare deeply afterwards
	original := sampleValidContent()

	// Inject items that will be pruned
	content.Experiences[0].Bullets = append(content.Experiences[0].Bullets, core.TailoredBullet{
		Text:          "Bad bullet",
		SourceFactIDs: []string{"fake-fact"},
	})
	original.Experiences[0].Bullets = append(original.Experiences[0].Bullets, core.TailoredBullet{
		Text:          "Bad bullet",
		SourceFactIDs: []string{"fake-fact"},
	})

	res, err := validator.Validate(content, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Validate input was not mutated
	if !reflect.DeepEqual(content, original) {
		t.Fatalf("input TailoredContent was mutated by Validate")
	}

	// Mutating the returned sanitized content must not affect the original input
	if len(res.SanitizedContent.Experiences) > 0 {
		res.SanitizedContent.Experiences[0].Company = "MUTATED"
		if content.Experiences[0].Company == "MUTATED" {
			t.Fatalf("mutating returned SanitizedContent mutated original input")
		}
	}
}

func TestValidate_NilAndBoundaryInputs(t *testing.T) {
	snapshot := sampleSnapshot()
	content := sampleValidContent()

	// Nil snapshot
	_, err := validator.Validate(content, nil, nil)
	if err == nil {
		t.Fatalf("expected error when snapshot is nil")
	}
	if !errors.Is(err, core.ErrValidation) {
		t.Fatalf("expected ErrValidation for nil snapshot, got %v", err)
	}

	// Nil content
	_, err = validator.Validate(nil, snapshot, nil)
	if err == nil {
		t.Fatalf("expected error when content is nil")
	}
	if !errors.Is(err, core.ErrValidation) {
		t.Fatalf("expected ErrValidation for nil content, got %v", err)
	}

	// Nil branch is legal
	res, err := validator.Validate(content, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error with nil branch: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("expected 0 warnings with nil branch, got %d", len(res.Warnings))
	}

	// Empty content struct
	emptyContent := &core.TailoredContent{}
	resEmpty, err := validator.Validate(emptyContent, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error with empty content: %v", err)
	}
	if resEmpty.SanitizedContent == nil {
		t.Fatalf("expected non-nil SanitizedContent for empty input")
	}

	// Content with nil bullets
	nilBulletsContent := &core.TailoredContent{
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Acme Corp",
				Bullets:        nil,
			},
		},
	}
	resNilBullets, err := validator.Validate(nilBulletsContent, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resNilBullets.SanitizedContent.Experiences[0].Bullets != nil {
		t.Fatalf("expected nil bullets preserved")
	}
}

func TestValidateStrict_Behavior(t *testing.T) {
	snapshot := sampleSnapshot()
	cleanContent := sampleValidContent()

	// Strict validate on clean content
	err := validator.ValidateStrict(cleanContent, snapshot, nil)
	if err != nil {
		t.Fatalf("expected nil error on clean content in ValidateStrict, got %v", err)
	}

	// Strict validate on content with warnings
	dirtyContent := sampleValidContent()
	dirtyContent.Experiences[0].Bullets = append(dirtyContent.Experiences[0].Bullets, core.TailoredBullet{
		Text:          "Uncited bullet",
		SourceFactIDs: []string{},
	})

	err = validator.ValidateStrict(dirtyContent, snapshot, nil)
	if err == nil {
		t.Fatalf("expected error on dirty content in ValidateStrict")
	}
	if !errors.Is(err, core.ErrProvenance) {
		t.Fatalf("expected ErrProvenance, got %v", err)
	}

	// Strict validate on nil arguments
	err = validator.ValidateStrict(dirtyContent, nil, nil)
	if err == nil {
		t.Fatalf("expected error on nil snapshot in ValidateStrict")
	}
	if !errors.Is(err, core.ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestValidatorInterfaceAndNew(t *testing.T) {
	var _ validator.ProvenanceValidator = validator.New()

	v := validator.New()
	snapshot := sampleSnapshot()
	content := sampleValidContent()

	res, err := v.Validate(content, snapshot, nil)
	if err != nil || len(res.Warnings) != 0 {
		t.Fatalf("v.Validate failed: err=%v, warnings=%v", err, res.Warnings)
	}

	err = v.ValidateStrict(content, snapshot, nil)
	if err != nil {
		t.Fatalf("v.ValidateStrict failed: err=%v", err)
	}
}

func TestValidate_ReconcilesAuthoritativeEntityMetadata_NoLies(t *testing.T) {
	snapshot := sampleSnapshot()
	snapshot.Experiences[0].Location = core.Location{City: "San Francisco", Region: "CA", CountryCode: "USA"}
	snapshot.Experiences[0].EmploymentType = core.EmploymentTypeContract
	snapshot.Experiences[0].Metadata = map[string]any{"clearance": "Top Secret"}
	snapshot.Projects[0].Metadata = map[string]any{"stars": 120}
	snapshot.Education[0].Metadata = map[string]any{"honors": "cum laude"}
	snapshot.Certifications[0].Metadata = map[string]any{"badge_id": "CKA-1234"}

	adversarialEndDate := core.NewDate(2099, 1, 1)
	advProjStart := core.NewDate(1991, 1, 1)
	advEduStart := core.NewDate(1980, 1, 1)
	advCertDate := core.NewDate(1901, 1, 1)
	adversarialContent := &core.TailoredContent{
		Summary: "Adversarial LLM content",
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Hallucinated Stripe Inc",
				RoleTitle:      "Chief Executive Officer",
				EmploymentType: core.EmploymentTypeFullTime,
				StartDate:      core.NewDate(1990, 1, 1),
				EndDate:        &adversarialEndDate,
				Location:       core.Location{City: "Atlantis", CountryCode: "XX"},
				Metadata:       map[string]any{"clearance": "Public", "injected": "exploit"},
				Bullets: []core.TailoredBullet{
					{
						Text:          "Architected streaming pipeline handling 100k eps.",
						SourceFactIDs: []string{"fact-exp-1"},
					},
				},
			},
		},
		Projects: []core.TailoredProject{
			{
				SourceEntityID: "proj-1",
				Name:           "Hallucinated Linux Kernel",
				Role:           "Lead Architect",
				URL:            "https://fake.example.com",
				StartDate:      &advProjStart,
				Metadata:       map[string]any{"stars": 999999, "injected": "exploit"},
				Bullets: []core.TailoredBullet{
					{
						Text:          "Maintained custom exporter plugin",
						SourceFactIDs: []string{"fact-proj-1"},
					},
				},
			},
		},
		Education: []core.TailoredEducation{
			{
				SourceEntityID: "edu-1",
				Institution:    "Hallucinated Oxford University",
				Degree:         "Ph.D.",
				Field:          "Astrophysics",
				StartDate:      &advEduStart,
				Metadata:       map[string]any{"honors": "summa cum laude", "injected": "exploit"},
				Bullets: []core.TailoredBullet{
					{
						Text:          "Published research on distributed consensus",
						SourceFactIDs: []string{"fact-edu-1"},
					},
				},
			},
		},
		Certifications: []core.TailoredCert{
			{
				SourceEntityID: "cert-1",
				Name:           "Hallucinated Nobel Prize",
				Issuer:         "Nobel Committee",
				Date:           &advCertDate,
				URL:            "https://fake-nobel.com",
				Metadata:       map[string]any{"badge_id": "FORGED", "injected": "exploit"},
			},
		},
	}

	res, err := validator.Validate(adversarialContent, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1. Experience headers must be strictly reconciled to snapshot values
	exp := res.SanitizedContent.Experiences[0]
	if exp.Company != "Acme Corp" {
		t.Errorf("expected Company 'Acme Corp', got '%s'", exp.Company)
	}
	if exp.RoleTitle != "Principal Engineer" {
		t.Errorf("expected RoleTitle 'Principal Engineer', got '%s'", exp.RoleTitle)
	}
	if exp.EmploymentType != core.EmploymentTypeContract {
		t.Errorf("expected EmploymentType %q, got %q", core.EmploymentTypeContract, exp.EmploymentType)
	}
	if exp.StartDate.String() != "2021-01-01" {
		t.Errorf("expected StartDate '2021-01-01', got '%s'", exp.StartDate)
	}
	if exp.EndDate == nil || exp.EndDate.String() != "2023-12-31" {
		t.Errorf("expected EndDate '2023-12-31', got '%v'", exp.EndDate)
	}
	if exp.Location.City != "San Francisco" {
		t.Errorf("expected Location.City 'San Francisco', got '%s'", exp.Location.City)
	}
	if exp.Metadata["clearance"] != "Top Secret" {
		t.Errorf("expected clearance 'Top Secret', got %v", exp.Metadata["clearance"])
	}
	if _, exists := exp.Metadata["injected"]; exists {
		t.Errorf("expected injected metadata key to be omitted, got %v", exp.Metadata["injected"])
	}

	// 2. Project headers must be strictly reconciled to snapshot values
	proj := res.SanitizedContent.Projects[0]
	if proj.Name != "OpenTelemetry Collector Exporter" {
		t.Errorf("expected Project Name 'OpenTelemetry Collector Exporter', got '%s'", proj.Name)
	}
	if proj.Metadata["stars"] != 120 {
		t.Errorf("expected stars 120, got %v", proj.Metadata["stars"])
	}
	if _, exists := proj.Metadata["injected"]; exists {
		t.Errorf("expected injected metadata key on project to be omitted, got %v", proj.Metadata["injected"])
	}

	// 3. Education headers must be strictly reconciled to snapshot values
	edu := res.SanitizedContent.Education[0]
	if edu.Institution != "MIT" {
		t.Errorf("expected Institution 'MIT', got '%s'", edu.Institution)
	}
	if edu.Degree != "B.S." {
		t.Errorf("expected Degree 'B.S.', got '%s'", edu.Degree)
	}
	if edu.Field != "Computer Science" {
		t.Errorf("expected Field 'Computer Science', got '%s'", edu.Field)
	}
	if edu.StartDate == nil || edu.StartDate.String() != "2014-09-01" {
		t.Errorf("expected StartDate '2014-09-01', got '%s'", edu.StartDate)
	}
	if edu.Metadata["honors"] != "cum laude" {
		t.Errorf("expected honors 'cum laude', got %v", edu.Metadata["honors"])
	}
	if _, exists := edu.Metadata["injected"]; exists {
		t.Errorf("expected injected metadata key on education to be omitted, got %v", edu.Metadata["injected"])
	}

	// 4. Certification headers must be strictly reconciled to snapshot values
	cert := res.SanitizedContent.Certifications[0]
	if cert.Name != "Certified Kubernetes Administrator" {
		t.Errorf("expected Cert Name 'Certified Kubernetes Administrator', got '%s'", cert.Name)
	}
	if cert.Issuer != "CNCF" {
		t.Errorf("expected Cert Issuer 'CNCF', got '%s'", cert.Issuer)
	}
	if cert.Date == nil || cert.Date.String() != "2022-05-01" {
		t.Errorf("expected Cert Date '2022-05-01', got '%s'", cert.Date)
	}
	if cert.Metadata["badge_id"] != "CKA-1234" {
		t.Errorf("expected badge_id 'CKA-1234', got %v", cert.Metadata["badge_id"])
	}
	if _, exists := cert.Metadata["injected"]; exists {
		t.Errorf("expected injected metadata key on cert to be omitted, got %v", cert.Metadata["injected"])
	}
}

func TestValidate_Immutability_SnapshotPreserved(t *testing.T) {
	snapshot := sampleSnapshot()
	snapshot.Experiences[0].Metadata = map[string]any{"dept": "Engineering"}
	snapshot.Experiences[0].EndDate = core.DatePtr(core.NewDate(2023, 12, 31))
	snapshot.Projects[0].Metadata = map[string]any{"repo": "github.com/foo/bar"}
	snapshot.Projects[0].StartDate = core.DatePtr(core.NewDate(2021, 5, 1))
	snapshot.Education[0].Metadata = map[string]any{"gpa": "3.9"}
	snapshot.Education[0].StartDate = core.DatePtr(core.NewDate(2014, 9, 1))
	snapshot.Certifications[0].Metadata = map[string]any{"tier": "professional"}
	snapshot.Certifications[0].Date = core.DatePtr(core.NewDate(2022, 5, 1))

	content := sampleValidContent()
	res, err := validator.Validate(content, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	// Mutate sanitized content in-place
	res.SanitizedContent.Experiences[0].Metadata["dept"] = "Tampered Dept"
	res.SanitizedContent.Experiences[0].Metadata["injected"] = "tampered"
	*res.SanitizedContent.Experiences[0].EndDate = core.NewDate(1999, 1, 1)

	res.SanitizedContent.Projects[0].Metadata["repo"] = "tampered"
	*res.SanitizedContent.Projects[0].StartDate = core.NewDate(1999, 1, 1)

	res.SanitizedContent.Education[0].Metadata["gpa"] = "1.0"
	*res.SanitizedContent.Education[0].StartDate = core.NewDate(1999, 1, 1)

	res.SanitizedContent.Certifications[0].Metadata["tier"] = "tampered"
	*res.SanitizedContent.Certifications[0].Date = core.NewDate(1999, 1, 1)

	// Verify snapshot remains completely unmutated
	if snapshot.Experiences[0].Metadata["dept"] != "Engineering" {
		t.Errorf("snapshot experience metadata was mutated: got %v, want 'Engineering'", snapshot.Experiences[0].Metadata["dept"])
	}
	if _, exists := snapshot.Experiences[0].Metadata["injected"]; exists {
		t.Errorf("snapshot experience metadata gained unexpected key 'injected'")
	}
	if snapshot.Experiences[0].EndDate.String() != "2023-12-31" {
		t.Errorf("snapshot experience EndDate was mutated: got %s, want 2023-12-31", snapshot.Experiences[0].EndDate)
	}

	if snapshot.Projects[0].Metadata["repo"] != "github.com/foo/bar" {
		t.Errorf("snapshot project metadata was mutated: got %v, want 'github.com/foo/bar'", snapshot.Projects[0].Metadata["repo"])
	}
	if snapshot.Projects[0].StartDate.String() != "2021-05-01" {
		t.Errorf("snapshot project StartDate was mutated: got %s, want 2021-05-01", snapshot.Projects[0].StartDate)
	}

	if snapshot.Education[0].Metadata["gpa"] != "3.9" {
		t.Errorf("snapshot education metadata was mutated: got %v, want '3.9'", snapshot.Education[0].Metadata["gpa"])
	}
	if snapshot.Education[0].StartDate.String() != "2014-09-01" {
		t.Errorf("snapshot education StartDate was mutated: got %s, want 2014-09-01", snapshot.Education[0].StartDate)
	}

	if snapshot.Certifications[0].Metadata["tier"] != "professional" {
		t.Errorf("snapshot cert metadata was mutated: got %v, want 'professional'", snapshot.Certifications[0].Metadata["tier"])
	}
	if snapshot.Certifications[0].Date.String() != "2022-05-01" {
		t.Errorf("snapshot cert Date was mutated: got %s, want 2022-05-01", snapshot.Certifications[0].Date)
	}
}
