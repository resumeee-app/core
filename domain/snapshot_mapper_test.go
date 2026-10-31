package domain_test

import (
	"reflect"
	"testing"

	"resumeee/internal/domain"
)

func TestBranch_DefaultBranchHelpers(t *testing.T) {
	bDefault := domain.Branch{
		ID:        domain.DefaultBranchID("user-1"),
		UserID:    "user-1",
		IsDefault: true,
	}
	if bDefault.CanTailor() {
		t.Errorf("expected CanTailor() = false for default branch, got true")
	}
	if bDefault.ID != "branch-default-user-1" {
		t.Errorf("expected ID 'branch-default-user-1', got %q", bDefault.ID)
	}

	bTailored := domain.Branch{
		ID:        "branch-custom",
		UserID:    "user-1",
		IsDefault: false,
	}
	if !bTailored.CanTailor() {
		t.Errorf("expected CanTailor() = true for tailored branch, got false")
	}
}

func TestSnapshotToTailoredContent(t *testing.T) {
	t.Run("nil snapshot returns safe non-nil empty slices", func(t *testing.T) {
		res := domain.SnapshotToTailoredContent(nil)
		if res == nil {
			t.Fatal("expected non-nil result")
		}
		if res.Summary != "" {
			t.Errorf("expected empty summary, got %q", res.Summary)
		}
		if res.SkillGroups == nil || len(res.SkillGroups) != 0 {
			t.Errorf("expected empty non-nil SkillGroups, got %v", res.SkillGroups)
		}
		if res.Experiences == nil || len(res.Experiences) != 0 {
			t.Errorf("expected empty non-nil Experiences, got %v", res.Experiences)
		}
		if res.Projects == nil || len(res.Projects) != 0 {
			t.Errorf("expected empty non-nil Projects, got %v", res.Projects)
		}
		if res.Education == nil || len(res.Education) != 0 {
			t.Errorf("expected empty non-nil Education, got %v", res.Education)
		}
		if res.Certifications == nil || len(res.Certifications) != 0 {
			t.Errorf("expected empty non-nil Certifications, got %v", res.Certifications)
		}
	})

	t.Run("empty snapshot returns safe non-nil empty slices", func(t *testing.T) {
		snap := &domain.MasterProfileSnapshot{}
		res := domain.SnapshotToTailoredContent(snap)
		if res == nil {
			t.Fatal("expected non-nil result")
		}
		if res.Summary != "" {
			t.Errorf("expected empty summary, got %q", res.Summary)
		}
		if res.SkillGroups == nil || len(res.SkillGroups) != 0 {
			t.Errorf("expected empty non-nil SkillGroups, got %v", res.SkillGroups)
		}
		if res.Experiences == nil || len(res.Experiences) != 0 {
			t.Errorf("expected empty non-nil Experiences, got %v", res.Experiences)
		}
		if res.Projects == nil || len(res.Projects) != 0 {
			t.Errorf("expected empty non-nil Projects, got %v", res.Projects)
		}
		if res.Education == nil || len(res.Education) != 0 {
			t.Errorf("expected empty non-nil Education, got %v", res.Education)
		}
		if res.Certifications == nil || len(res.Certifications) != 0 {
			t.Errorf("expected empty non-nil Certifications, got %v", res.Certifications)
		}
	})

	t.Run("full career graph projection with 100% fidelity", func(t *testing.T) {
		endDate := "2023-12-31"
		hours := 40
		snap := &domain.MasterProfileSnapshot{
			Profile: domain.Profile{
				Summary: "Experienced distributed systems engineer with deep Go and cloud expertise.",
			},
			Skills: []domain.Skill{
				{ID: "skill-go", Name: "Go", Category: "Languages"},
				{ID: "skill-rust", Name: "Rust", Category: "Languages"},
				{ID: "skill-k8s", Name: "Kubernetes", Category: "Cloud & Ops"},
				{ID: "skill-comm", Name: "Mentorship", Category: ""}, // uncategorized
			},
			Experiences: []domain.WorkExperience{
				{
					ID:                "exp-1",
					Company:           "Acme Corp",
					RoleTitle:         "Principal Engineer",
					StartDate:         "2021-01-01",
					EndDate:           &endDate,
					Location:          domain.Location{City: "San Francisco", Region: "CA"},
					CompanyLocation:   domain.Location{City: "San Francisco", Region: "CA"},
					CompanyContact:    &domain.ContactDetail{Type: domain.ContactTypeEmail, Value: "hr@acme.corp"},
					HoursPerWeek:      &hours,
					SupervisorName:    "Jane Doe",
					SupervisorContact: "jane@acme.corp",
					DutiesSummary:     "Architected distributed platform systems.",
					Facts: []domain.Fact{
						{
							ID:        "fact-1",
							Statement: "Architected distributed raft cluster reducing failover latency by 80%.",
							Skills: []domain.Skill{
								{ID: "skill-go", Name: "Go"},
							},
						},
						{
							ID:        "fact-2",
							Statement: "Mentored 12 junior engineers.",
							Skills: []domain.Skill{
								{ID: "skill-comm", Name: "Mentorship"},
							},
						},
					},
				},
			},
			Projects: []domain.Project{
				{
					ID:        "proj-1",
					Name:      "Resumeee Engine",
					Role:      "Creator",
					URL:       "https://github.com/example/resumeee",
					StartDate: "2024-01-01",
					Summary:   "Open-source resume management system.",
					Facts: []domain.Fact{
						{
							ID:        "fact-3",
							Statement: "Implemented AST template parser with 100% test coverage.",
							Skills: []domain.Skill{
								{ID: "skill-go", Name: "Go"},
							},
						},
					},
				},
			},
			Education: []domain.Education{
				{
					ID:          "edu-1",
					Institution: "State University",
					Degree:      "B.S.",
					Field:       "Computer Science",
					StartDate:   "2016-09-01",
					EndDate:     &endDate,
					Score:       "3.9 GPA",
					Facts: []domain.Fact{
						{
							ID:        "fact-4",
							Statement: "Dean's Honor List all semesters.",
						},
					},
				},
			},
			Certifications: []domain.Certification{
				{
					ID:     "cert-1",
					Name:   "Certified Kubernetes Administrator",
					Issuer: "Cloud Native Computing Foundation",
					Date:   "2022-06-01",
					URL:    "https://cncf.io/cert/cka",
				},
			},
		}

		res := domain.SnapshotToTailoredContent(snap)
		if res == nil {
			t.Fatal("expected non-nil tailored content")
		}
		if res.Summary != snap.Profile.Summary {
			t.Errorf("summary mismatch: got %q, want %q", res.Summary, snap.Profile.Summary)
		}

		// Verify SkillGroups
		if len(res.SkillGroups) != 3 {
			t.Fatalf("expected 3 skill groups, got %d", len(res.SkillGroups))
		}
		if res.SkillGroups[0].Category != "Languages" || !reflect.DeepEqual(res.SkillGroups[0].Skills, []string{"Go", "Rust"}) {
			t.Errorf("skill group 0 mismatch: %+v", res.SkillGroups[0])
		}
		if !reflect.DeepEqual(res.SkillGroups[0].SourceSkillIDs, []string{"skill-go", "skill-rust"}) {
			t.Errorf("skill group 0 source skill IDs mismatch: %+v", res.SkillGroups[0].SourceSkillIDs)
		}

		if res.SkillGroups[1].Category != "Cloud & Ops" || !reflect.DeepEqual(res.SkillGroups[1].Skills, []string{"Kubernetes"}) {
			t.Errorf("skill group 1 mismatch: %+v", res.SkillGroups[1])
		}
		if !reflect.DeepEqual(res.SkillGroups[1].SourceSkillIDs, []string{"skill-k8s"}) {
			t.Errorf("skill group 1 source skill IDs mismatch: %+v", res.SkillGroups[1].SourceSkillIDs)
		}

		// Uncategorized skill fallback
		if res.SkillGroups[2].Category != "General Skills" || !reflect.DeepEqual(res.SkillGroups[2].Skills, []string{"Mentorship"}) {
			t.Errorf("skill group 2 fallback mismatch: %+v", res.SkillGroups[2])
		}
		if !reflect.DeepEqual(res.SkillGroups[2].SourceSkillIDs, []string{"skill-comm"}) {
			t.Errorf("skill group 2 source skill IDs mismatch: %+v", res.SkillGroups[2].SourceSkillIDs)
		}

		// Verify Experience
		if len(res.Experiences) != 1 {
			t.Fatalf("expected 1 experience, got %d", len(res.Experiences))
		}
		exp := res.Experiences[0]
		if exp.SourceEntityID != "exp-1" || exp.Company != "Acme Corp" || exp.RoleTitle != "Principal Engineer" {
			t.Errorf("unexpected experience header: %+v", exp)
		}
		if exp.StartDate != "2021-01-01" || exp.EndDate == nil || *exp.EndDate != endDate {
			t.Errorf("unexpected experience dates: %+v", exp)
		}
		if exp.HoursPerWeek == nil || *exp.HoursPerWeek != hours || exp.SupervisorName != "Jane Doe" {
			t.Errorf("unexpected experience administrative fields: %+v", exp)
		}
		if len(exp.Bullets) != 2 {
			t.Fatalf("expected 2 bullets, got %d", len(exp.Bullets))
		}
		if exp.Bullets[0].Text != "Architected distributed raft cluster reducing failover latency by 80%." {
			t.Errorf("unexpected bullet 0 text: %q", exp.Bullets[0].Text)
		}
		if !reflect.DeepEqual(exp.Bullets[0].SourceFactIDs, []string{"fact-1"}) || !reflect.DeepEqual(exp.Bullets[0].SourceSkillIDs, []string{"skill-go"}) {
			t.Errorf("unexpected bullet 0 provenance: %+v", exp.Bullets[0])
		}
		if exp.Bullets[1].Text != "Mentored 12 junior engineers." {
			t.Errorf("unexpected bullet 1 text: %q", exp.Bullets[1].Text)
		}
		if !reflect.DeepEqual(exp.Bullets[1].SourceFactIDs, []string{"fact-2"}) || !reflect.DeepEqual(exp.Bullets[1].SourceSkillIDs, []string{"skill-comm"}) {
			t.Errorf("unexpected bullet 1 provenance: %+v", exp.Bullets[1])
		}

		// Verify Projects
		if len(res.Projects) != 1 {
			t.Fatalf("expected 1 project, got %d", len(res.Projects))
		}
		proj := res.Projects[0]
		if proj.SourceEntityID != "proj-1" || proj.Name != "Resumeee Engine" || proj.Role != "Creator" {
			t.Errorf("unexpected project: %+v", proj)
		}
		if len(proj.Bullets) != 1 || proj.Bullets[0].Text != "Implemented AST template parser with 100% test coverage." {
			t.Errorf("unexpected project bullets: %+v", proj.Bullets)
		}
		if !reflect.DeepEqual(proj.Bullets[0].SourceFactIDs, []string{"fact-3"}) || !reflect.DeepEqual(proj.Bullets[0].SourceSkillIDs, []string{"skill-go"}) {
			t.Errorf("unexpected project bullet provenance: %+v", proj.Bullets[0])
		}

		// Verify Education
		if len(res.Education) != 1 {
			t.Fatalf("expected 1 education, got %d", len(res.Education))
		}
		edu := res.Education[0]
		if edu.SourceEntityID != "edu-1" || edu.Institution != "State University" || edu.Score != "3.9 GPA" {
			t.Errorf("unexpected education: %+v", edu)
		}
		if len(edu.Bullets) != 1 || edu.Bullets[0].Text != "Dean's Honor List all semesters." {
			t.Errorf("unexpected education bullets: %+v", edu.Bullets)
		}
		if !reflect.DeepEqual(edu.Bullets[0].SourceFactIDs, []string{"fact-4"}) {
			t.Errorf("unexpected education bullet provenance: %+v", edu.Bullets[0])
		}

		// Verify Certifications
		if len(res.Certifications) != 1 {
			t.Fatalf("expected 1 certification, got %d", len(res.Certifications))
		}
		cert := res.Certifications[0]
		if cert.SourceEntityID != "cert-1" || cert.Name != "Certified Kubernetes Administrator" || cert.Issuer != "Cloud Native Computing Foundation" {
			t.Errorf("unexpected certification: %+v", cert)
		}
	})

	t.Run("facts indexed from snapshot.Facts if entity Facts slice is empty", func(t *testing.T) {
		snap := &domain.MasterProfileSnapshot{
			Experiences: []domain.WorkExperience{
				{
					ID:        "exp-orphan-facts",
					Company:   "Beta Inc",
					RoleTitle: "Engineer",
					StartDate: "2020-01-01",
					Facts:     nil, // Empty on entity
				},
			},
			Facts: []domain.Fact{
				{
					ID:        "fact-orphan-1",
					OwnerID:   "exp-orphan-facts",
					OwnerType: domain.OwnerTypeWorkExperience,
					Statement: "Built billing pipeline.",
				},
			},
		}

		res := domain.SnapshotToTailoredContent(snap)
		if res == nil {
			t.Fatal("expected non-nil tailored content")
		}
		if len(res.Experiences) != 1 {
			t.Fatalf("expected 1 experience, got %d", len(res.Experiences))
		}
		if len(res.Experiences[0].Bullets) != 1 {
			t.Fatalf("expected 1 bullet, got %d", len(res.Experiences[0].Bullets))
		}
		if res.Experiences[0].Bullets[0].Text != "Built billing pipeline." {
			t.Errorf("unexpected bullet text: %q", res.Experiences[0].Bullets[0].Text)
		}
		if !reflect.DeepEqual(res.Experiences[0].Bullets[0].SourceFactIDs, []string{"fact-orphan-1"}) {
			t.Errorf("unexpected source fact IDs: %+v", res.Experiences[0].Bullets[0].SourceFactIDs)
		}
	})
}
