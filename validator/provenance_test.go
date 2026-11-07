package validator_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"resumeee/internal/domain"
	"resumeee/internal/validator"
)

func sampleSnapshot() *domain.MasterProfileSnapshot {
	endDate := domain.NewDate(2023, 12, 31)
	hours := 40
	contact := domain.ContactDetail{Type: domain.ContactTypePhone, Value: "+1-555-0199"}
	projEnd := domain.NewDate(2022, 12, 31)
	eduEnd := domain.NewDate(2018, 5, 31)
	certDate := domain.NewDate(2022, 5, 1)

	return &domain.MasterProfileSnapshot{
		Profile: domain.Profile{
			ID:       "prof-1",
			FullName: "Ada Lovelace",
		},
		Skills: []domain.Skill{
			{ID: "skill-go", Name: "Go", Category: "Backend"},
			{ID: "skill-grpc", Name: "gRPC", Category: "Backend"},
			{ID: "skill-k8s", Name: "Kubernetes", Category: "Infrastructure"},
		},
		Experiences: []domain.WorkExperience{
			{
				ID:             "exp-1",
				Company:        "Acme Corp",
				RoleTitle:      "Principal Engineer",
				StartDate:      domain.NewDate(2021, 1, 1),
				EndDate:        &endDate,
				CompanyContact: &contact,
				HoursPerWeek:   &hours,
				Skills: []domain.Skill{
					{ID: "skill-exp-nested", Name: "Postgres", Category: "Databases"},
				},
				Facts: []domain.Fact{
					{
						ID:        "fact-exp-1",
						Statement: "Built event streaming pipeline",
						Skills: []domain.Skill{
							{ID: "skill-fact-nested", Name: "Kafka", Category: "Streaming"},
						},
					},
					{ID: "fact-exp-2", OwnerID: "exp-1", OwnerType: domain.OwnerTypeWorkExperience, Statement: "Scaled Postgres cluster to 100k QPS"},
				},
			},
			{
				ID:        "exp-2",
				Company:   "Beta LLC",
				RoleTitle: "Senior Engineer",
				StartDate: domain.NewDate(2018, 1, 1),
				Facts: []domain.Fact{
					{ID: "fact-exp-beta", OwnerID: "exp-2", OwnerType: domain.OwnerTypeWorkExperience, Statement: "Led rewrite of auth service"},
				},
			},
		},
		Projects: []domain.Project{
			{
				ID:      "proj-1",
				Name:    "OpenTelemetry Collector Exporter",
				EndDate: &projEnd,
				Skills: []domain.Skill{
					{ID: "skill-proj-nested", Name: "OTel", Category: "Observability"},
				},
				Facts: []domain.Fact{
					{
						ID:        "fact-proj-1",
						Statement: "Maintained custom exporter plugin",
						Skills: []domain.Skill{
							{ID: "skill-proj-fact-nested", Name: "Go Collector", Category: "Observability"},
						},
					},
				},
			},
		},
		Education: []domain.Education{
			{
				ID:          "edu-1",
				Institution: "MIT",
				Degree:      "B.S.",
				Field:       "Computer Science",
				StartDate:   domain.DatePtr(domain.NewDate(2014, 9, 1)),
				EndDate:     &eduEnd,
				Facts: []domain.Fact{
					{
						ID:        "fact-edu-1",
						Statement: "Published research on distributed consensus",
						Skills: []domain.Skill{
							{ID: "skill-edu-fact-nested", Name: "Paxos", Category: "Distributed"},
						},
					},
				},
			},
		},
		Certifications: []domain.Certification{
			{
				ID:     "cert-1",
				Name:   "Certified Kubernetes Administrator",
				Issuer: "CNCF",
				Date:   &certDate,
			},
		},
		Facts: []domain.Fact{
			{
				ID:        "fact-top-level",
				OwnerID:   "exp-1",
				OwnerType: domain.OwnerTypeWorkExperience,
				Statement: "Top-level indexed fact",
				Skills: []domain.Skill{
					{ID: "skill-top-fact-nested", Name: "Docker", Category: "Containers"},
				},
			},
		},
	}
}

func sampleValidContent() *domain.TailoredContent {
	endDate := domain.NewDate(2023, 12, 31)
	hours := 40
	contact := domain.ContactDetail{Type: domain.ContactTypePhone, Value: "+1-555-0199"}
	projEnd := domain.NewDate(2022, 12, 31)
	eduEnd := domain.NewDate(2018, 5, 31)
	certDate := domain.NewDate(2022, 5, 1)

	return &domain.TailoredContent{
		Summary: "Experienced systems engineer focused on distributed systems.",
		SkillGroups: []domain.TailoredSkillGroup{
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
		Experiences: []domain.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Acme Corp",
				RoleTitle:      "Principal Engineer",
				StartDate:      domain.NewDate(2021, 1, 1),
				EndDate:        &endDate,
				CompanyContact: &contact,
				HoursPerWeek:   &hours,
				Bullets: []domain.TailoredBullet{
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
				StartDate:      domain.NewDate(2018, 1, 1),
				Bullets: []domain.TailoredBullet{
					{
						Text:          "Spearheaded auth rewrite with zero downtime.",
						SourceFactIDs: []string{"fact-exp-beta"},
					},
				},
			},
		},
		Projects: []domain.TailoredProject{
			{
				SourceEntityID: "proj-1",
				Name:           "OpenTelemetry Collector Exporter",
				EndDate:        &projEnd,
				Bullets: []domain.TailoredBullet{
					{
						Text:          "Maintained and released high-throughput plugin.",
						SourceFactIDs: []string{"fact-proj-1"},
					},
				},
			},
		},
		Education: []domain.TailoredEducation{
			{
				SourceEntityID: "edu-1",
				Institution:    "MIT",
				Degree:         "B.S.",
				Field:          "Computer Science",
				StartDate:      domain.DatePtr(domain.NewDate(2014, 9, 1)),
				EndDate:        &eduEnd,
				Bullets: []domain.TailoredBullet{
					{
						Text:          "Published distributed consensus research in ACM.",
						SourceFactIDs: []string{"fact-edu-1"},
					},
				},
			},
		},
		Certifications: []domain.TailoredCert{
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
	content.Experiences = append(content.Experiences, domain.TailoredExperience{
		SourceEntityID: "fabricated-exp-uuid",
		Company:        "Fake Tech",
		RoleTitle:      "Ghost Engineer",
		Bullets: []domain.TailoredBullet{
			{Text: "Invented warp drive", SourceFactIDs: []string{"fact-exp-1"}},
		},
	})
	content.Projects = append(content.Projects, domain.TailoredProject{
		SourceEntityID: "fabricated-proj-uuid",
		Name:           "Fake Blockchain",
		Bullets: []domain.TailoredBullet{
			{Text: "Minted fake coins", SourceFactIDs: []string{"fact-proj-1"}},
		},
	})
	content.Education = append(content.Education, domain.TailoredEducation{
		SourceEntityID: "fabricated-edu-uuid",
		Institution:    "Fake University",
	})
	content.Certifications = append(content.Certifications, domain.TailoredCert{
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
	content.Experiences[0].Bullets = append(content.Experiences[0].Bullets, domain.TailoredBullet{
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

	content.Experiences[0].Bullets = append(content.Experiences[0].Bullets, domain.TailoredBullet{
		Text:          "Uncited bullet with no source fact ids.",
		SourceFactIDs: []string{},
	})
	content.Experiences[0].Bullets = append(content.Experiences[0].Bullets, domain.TailoredBullet{
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
	content.Experiences[0].Bullets = append(content.Experiences[0].Bullets, domain.TailoredBullet{
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
	content.Experiences[0].Bullets = append(content.Experiences[0].Bullets, domain.TailoredBullet{
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

	branch := &domain.Branch{
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
	content := &domain.TailoredContent{
		Experiences: []domain.TailoredExperience{
			{SourceEntityID: "exp-1"}, // Empty Company
		},
		Projects: []domain.TailoredProject{
			{SourceEntityID: "proj-1"}, // Empty Name
		},
		Education: []domain.TailoredEducation{
			{SourceEntityID: "edu-1"}, // Empty Institution
		},
		Certifications: []domain.TailoredCert{
			{SourceEntityID: "cert-1"}, // Empty Name
		},
	}

	branch := &domain.Branch{
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

	snapshot.Projects = append(snapshot.Projects, domain.Project{
		ID:   "proj-pinned",
		Name: "Critical Open Source Project",
	})

	branch := &domain.Branch{
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
	content := &domain.TailoredContent{
		SkillGroups: []domain.TailoredSkillGroup{
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

	content.SkillGroups = append(content.SkillGroups, domain.TailoredSkillGroup{
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
	content := &domain.TailoredContent{
		SkillGroups: []domain.TailoredSkillGroup{
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
	content.Experiences[0].Bullets = append(content.Experiences[0].Bullets, domain.TailoredBullet{
		Text:          "Bad bullet",
		SourceFactIDs: []string{"fake-fact"},
	})
	original.Experiences[0].Bullets = append(original.Experiences[0].Bullets, domain.TailoredBullet{
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
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation for nil snapshot, got %v", err)
	}

	// Nil content
	_, err = validator.Validate(nil, snapshot, nil)
	if err == nil {
		t.Fatalf("expected error when content is nil")
	}
	if !errors.Is(err, domain.ErrValidation) {
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
	emptyContent := &domain.TailoredContent{}
	resEmpty, err := validator.Validate(emptyContent, snapshot, nil)
	if err != nil {
		t.Fatalf("unexpected error with empty content: %v", err)
	}
	if resEmpty.SanitizedContent == nil {
		t.Fatalf("expected non-nil SanitizedContent for empty input")
	}

	// Content with nil bullets
	nilBulletsContent := &domain.TailoredContent{
		Experiences: []domain.TailoredExperience{
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
	dirtyContent.Experiences[0].Bullets = append(dirtyContent.Experiences[0].Bullets, domain.TailoredBullet{
		Text:          "Uncited bullet",
		SourceFactIDs: []string{},
	})

	err = validator.ValidateStrict(dirtyContent, snapshot, nil)
	if err == nil {
		t.Fatalf("expected error on dirty content in ValidateStrict")
	}
	if !errors.Is(err, domain.ErrProvenance) {
		t.Fatalf("expected ErrProvenance, got %v", err)
	}

	// Strict validate on nil arguments
	err = validator.ValidateStrict(dirtyContent, nil, nil)
	if err == nil {
		t.Fatalf("expected error on nil snapshot in ValidateStrict")
	}
	if !errors.Is(err, domain.ErrValidation) {
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
	snapshot.Experiences[0].Location = domain.Location{City: "San Francisco", Region: "CA", CountryCode: "USA"}

	adversarialEndDate := domain.NewDate(2099, 1, 1)
	advProjStart := domain.NewDate(1991, 1, 1)
	advEduStart := domain.NewDate(1980, 1, 1)
	advCertDate := domain.NewDate(1901, 1, 1)
	adversarialContent := &domain.TailoredContent{
		Summary: "Adversarial LLM content",
		Experiences: []domain.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Hallucinated Stripe Inc",
				RoleTitle:      "Chief Executive Officer",
				StartDate:      domain.NewDate(1990, 1, 1),
				EndDate:        &adversarialEndDate,
				Location:       domain.Location{City: "Atlantis", CountryCode: "XX"},
				Bullets: []domain.TailoredBullet{
					{
						Text:          "Architected streaming pipeline handling 100k eps.",
						SourceFactIDs: []string{"fact-exp-1"},
					},
				},
			},
		},
		Projects: []domain.TailoredProject{
			{
				SourceEntityID: "proj-1",
				Name:           "Hallucinated Linux Kernel",
				Role:           "Lead Architect",
				URL:            "https://fake.example.com",
				StartDate:      &advProjStart,
				Bullets: []domain.TailoredBullet{
					{
						Text:          "Maintained custom exporter plugin",
						SourceFactIDs: []string{"fact-proj-1"},
					},
				},
			},
		},
		Education: []domain.TailoredEducation{
			{
				SourceEntityID: "edu-1",
				Institution:    "Hallucinated Oxford University",
				Degree:         "Ph.D.",
				Field:          "Astrophysics",
				StartDate:      &advEduStart,
				Bullets: []domain.TailoredBullet{
					{
						Text:          "Published research on distributed consensus",
						SourceFactIDs: []string{"fact-edu-1"},
					},
				},
			},
		},
		Certifications: []domain.TailoredCert{
			{
				SourceEntityID: "cert-1",
				Name:           "Hallucinated Nobel Prize",
				Issuer:         "Nobel Committee",
				Date:           &advCertDate,
				URL:            "https://fake-nobel.com",
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
	if exp.StartDate.String() != "2021-01-01" {
		t.Errorf("expected StartDate '2021-01-01', got '%s'", exp.StartDate)
	}
	if exp.EndDate == nil || exp.EndDate.String() != "2023-12-31" {
		t.Errorf("expected EndDate '2023-12-31', got '%v'", exp.EndDate)
	}
	if exp.Location.City != "San Francisco" {
		t.Errorf("expected Location.City 'San Francisco', got '%s'", exp.Location.City)
	}

	// 2. Project headers must be strictly reconciled to snapshot values
	proj := res.SanitizedContent.Projects[0]
	if proj.Name != "OpenTelemetry Collector Exporter" {
		t.Errorf("expected Project Name 'OpenTelemetry Collector Exporter', got '%s'", proj.Name)
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
}

