package domain_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"resumeee/internal/domain"
)

func TestProfileJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	profile := domain.Profile{
		ID:       "prof-1",
		UserID:   "user-1",
		FullName: "Jane Doe",
		Headline: "Staff Distributed Systems Engineer",
		Email:    "jane@example.com",
		Phone:    "+1-555-0199",
		Location: domain.Location{
			Address:     "123 Tech Way",
			PostalCode:  "94107",
			City:        "San Francisco",
			CountryCode: "US",
			Region:      "CA",
		},
		Profiles: []domain.SocialLink{
			{Network: "GitHub", Username: "janedoe", URL: "https://github.com/janedoe"},
		},
		Summary:            "Experienced systems engineer.",
		GlobalWritingRules: "Active verbs only.",
		DefaultProvider:    "gemini",
		DefaultModel:       "gemini-3.8-flash",
		MasterRevision:     1,
		Metadata:           map[string]any{"source": "test"},
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("failed to marshal Profile: %v", err)
	}

	var decoded domain.Profile
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal Profile: %v", err)
	}

	if decoded.ID != profile.ID || decoded.FullName != profile.FullName || decoded.Location.City != "San Francisco" {
		t.Errorf("decoded Profile does not match original: %+v", decoded)
	}
	if len(decoded.Profiles) != 1 || decoded.Profiles[0].Network != "GitHub" {
		t.Errorf("decoded Profiles slice mismatch: %+v", decoded.Profiles)
	}
}

func TestCareerEntitiesAndFactPolymorphism(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	hours := 40
	endDate := "2024-01-01"

	skill := domain.Skill{
		ID:              "skill-1",
		UserID:          "user-1",
		Name:            "Go",
		Category:        "Languages",
		Proficiency:     "Expert",
		YearsExperience: 6.5,
		Keywords:        []string{"concurrency", "goroutines"},
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	fact := domain.Fact{
		ID:        "fact-1",
		UserID:    "user-1",
		OwnerID:   "exp-1",
		OwnerType: domain.OwnerTypeWorkExperience,
		Statement: "Reduced p99 latency from 120ms to 8ms by introducing worker pools.",
		Impact:    "93% latency reduction",
		Skills:    []domain.Skill{skill},
		CreatedAt: now,
		UpdatedAt: now,
	}

	exp := domain.WorkExperience{
		ID:             "exp-1",
		UserID:         "user-1",
		Company:        "Acme Corp",
		RoleTitle:      "Lead Backend Engineer",
		EmploymentType: domain.EmploymentTypeFullTime,
		StartDate:      "2020-01-01",
		EndDate:        &endDate,
		IsCurrent:      false,
		Location: domain.Location{
			City:        "Austin",
			CountryCode: "US",
		},
		CompanyLocation: domain.Location{
			Address:     "100 Corporate Blvd",
			City:        "Austin",
			CountryCode: "US",
		},
		CompanyContact: &domain.ContactDetail{
			Type:  domain.ContactTypeEmail,
			Value: "hr@acmeworks.example.com",
		},
		HoursPerWeek:   &hours,
		Facts:          []domain.Fact{fact},
		Skills:         []domain.Skill{skill},
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	proj := domain.Project{
		ID:        "proj-1",
		UserID:    "user-1",
		Name:      "Resumeee",
		Role:      "Author",
		URL:       "https://github.com/rrivera/resumeee",
		StartDate: "2026-01-01",
		Summary:   "Self-hosted resume tailoring engine",
		Facts: []domain.Fact{
			{
				ID:        "fact-2",
				UserID:    "user-1",
				OwnerID:   "proj-1",
				OwnerType: domain.OwnerTypeProject,
				Statement: "Designed dual-axis peer review agent pipeline.",
			},
		},
		Skills:    []domain.Skill{skill},
		CreatedAt: now,
		UpdatedAt: now,
	}

	edu := domain.Education{
		ID:          "edu-1",
		UserID:      "user-1",
		Institution: "MIT",
		Degree:      "Bachelor of Science",
		Field:       "Computer Science",
		StartDate:   "2016-09-01",
		EndDate:     &endDate,
		Score:       "3.9",
		Location: domain.Location{
			City:        "Cambridge",
			CountryCode: "US",
		},
		Facts: []domain.Fact{
			{
				ID:        "fact-3",
				UserID:    "user-1",
				OwnerID:   "edu-1",
				OwnerType: domain.OwnerTypeEducation,
				Statement: "Graduated with honors in systems research.",
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	cert := domain.Certification{
		ID:        "cert-1",
		UserID:    "user-1",
		Name:      "AWS Solutions Architect Professional",
		Issuer:    "Amazon Web Services",
		Date:      "2023-05-15",
		URL:       "https://aws.amazon.com/verify",
		Summary:   "Cloud architecture certification",
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Verify JSON roundtrip for all entities
	for _, entity := range []any{skill, fact, exp, proj, edu, cert} {
		data, err := json.Marshal(entity)
		if err != nil {
			t.Fatalf("failed to marshal entity %T: %v", entity, err)
		}
		if len(data) == 0 {
			t.Fatalf("expected non-empty JSON data for %T", entity)
		}
	}

	// Verify company details in marshaled WorkExperience
	expBytes, err := json.Marshal(exp)
	if err != nil {
		t.Fatalf("failed to marshal WorkExperience: %v", err)
	}
	var decodedExp domain.WorkExperience
	if err := json.Unmarshal(expBytes, &decodedExp); err != nil {
		t.Fatalf("failed to unmarshal WorkExperience: %v", err)
	}
	if decodedExp.CompanyLocation.City != "Austin" || decodedExp.CompanyContact == nil || decodedExp.CompanyContact.Value != "hr@acmeworks.example.com" || decodedExp.CompanyContact.Type != domain.ContactTypeEmail {
		t.Errorf("company location or contact not preserved: %+v", decodedExp)
	}
	if decodedExp.EmploymentType != domain.EmploymentTypeFullTime {
		t.Errorf("expected EmploymentType %v, got %v", domain.EmploymentTypeFullTime, decodedExp.EmploymentType)
	}
}

func TestPresetsAndBranches(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	preset := domain.Preset{
		ID:                 "preset-1",
		UserID:             "user-1",
		Name:               "1-Page Tech Resume",
		Description:        "Ultra-concise engineering resume",
		SystemInstructions: "Focus on metrics and active verbs.",
		TemplateSetID:      "tmpl-1",
		IncludeAdminFields: false,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	tmpl := domain.TemplateSet{
		ID:               "tmpl-1",
		UserID:           "user-1",
		Name:             "Standard Clean",
		Description:      "Standard HTML/CSS and Markdown template",
		IsBuiltIn:        true,
		HTMLTemplate:     "<html>{{.Summary}}</html>",
		CSSStyles:        "body { font-family: sans-serif; }",
		MarkdownTemplate: "# {{.Summary}}",
		TextTemplate:     "{{.Summary}}",
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if preset.IsBuiltIn() {
		t.Errorf("expected preset-1 not to be built-in")
	}
	builtinPreset := domain.Preset{ID: "builtin-1-page-tech-resume"}
	if !builtinPreset.IsBuiltIn() {
		t.Errorf("expected builtin-1-page-tech-resume to be built-in")
	}

	presetID := preset.ID
	branch := domain.Branch{
		ID:                       "branch-1",
		UserID:                   "user-1",
		Name:                     "Stripe Backend",
		TargetRole:               "Staff Backend Engineer",
		TargetCompany:            "Stripe",
		TargetRequirements:       "Go, distributed systems, high throughput",
		BranchInstructions:       "Emphasize financial ledger experience.",
		PresetID:                 &presetID,
		TemplateSetID:            tmpl.ID,
		PinnedEntityIDs:          []string{"exp-1"},
		ExcludedEntityIDs:        []string{"proj-old"},
		LastSyncedMasterRevision: 1,
		CreatedAt:                now,
		UpdatedAt:                now,
	}

	if branch.IsStaleRevision(2) != true {
		t.Errorf("expected branch to be stale when master revision is higher")
	}
	if branch.IsStaleRevision(1) != false {
		t.Errorf("expected branch not to be stale when master revision matches")
	}

	version := domain.BranchVersion{
		ID:                 "ver-1",
		BranchID:           branch.ID,
		VersionNumber:      1,
		Status:             domain.VersionStatusCompleted,
		MasterRevisionUsed: 1,
		TemplateSetIDUsed:  tmpl.ID,
		ProviderUsed:       "gemini",
		ModelUsed:          "gemini-2.5-pro",
		SourceType:         domain.SourceTypeLLMGeneration,
		HTMLOutput:         "<html>...</html>",
		MarkdownOutput:     "# ...",
		PlainTextOutput:    "...",
		CreatedAt:          now,
	}

	data, err := json.Marshal(version)
	if err != nil {
		t.Fatalf("failed to marshal BranchVersion: %v", err)
	}
	var decodedVersion domain.BranchVersion
	if err := json.Unmarshal(data, &decodedVersion); err != nil {
		t.Fatalf("failed to unmarshal BranchVersion: %v", err)
	}
	if decodedVersion.Status != domain.VersionStatusCompleted {
		t.Errorf("expected status %v, got %v", domain.VersionStatusCompleted, decodedVersion.Status)
	}
	if decodedVersion.SourceType != domain.SourceTypeLLMGeneration {
		t.Errorf("expected SourceType %v, got %v", domain.SourceTypeLLMGeneration, decodedVersion.SourceType)
	}
}

func TestTailoredContentAndProvenance(t *testing.T) {
	tailored := domain.TailoredContent{
		Summary: "Backend specialist with deep experience in distributed systems.",
		SkillGroups: []domain.TailoredSkillGroup{
			{
				Category:       "Languages & Runtimes",
				Skills:         []string{"Go", "Rust", "C++"},
				SourceSkillIDs: []string{"skill-1", "skill-2"},
			},
		},
		Experiences: []domain.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Acme Corp",
				RoleTitle:      "Lead Engineer",
				StartDate:      "2020-01-01",
				CompanyLocation: domain.Location{
					City:        "Austin",
					CountryCode: "US",
				},
				CompanyContact: &domain.ContactDetail{
					Type:  domain.ContactTypePhone,
					Value: "+1-555-0100",
				},
				Bullets: []domain.TailoredBullet{
					{
						Text:           "Architected event-driven streaming pipeline handling 50k eps.",
						SourceFactIDs:  []string{"fact-1"},
						SourceSkillIDs: []string{"skill-1"},
					},
				},
			},
		},
	}

	gapReport := domain.GapReport{
		OverallMatchSummary: "Strong match (90%) for distributed systems role.",
		MissingRequirements: []domain.GapItem{
			{
				Requirement:      "Kubernetes operator development",
				Importance:       domain.GapImportancePreferred,
				ClarifyingPrompt: "Have you written custom CRDs or Kubernetes controllers?",
			},
		},
	}

	data, err := json.Marshal(tailored)
	if err != nil {
		t.Fatalf("failed to marshal TailoredContent: %v", err)
	}
	var decodedContent domain.TailoredContent
	if err := json.Unmarshal(data, &decodedContent); err != nil {
		t.Fatalf("failed to unmarshal TailoredContent: %v", err)
	}
	if len(decodedContent.Experiences) != 1 || len(decodedContent.Experiences[0].Bullets) != 1 {
		t.Fatalf("decoded TailoredContent structure mismatch: %+v", decodedContent)
	}
	if decodedContent.Experiences[0].Bullets[0].SourceFactIDs[0] != "fact-1" {
		t.Errorf("expected bullet SourceFactIDs 'fact-1', got %v", decodedContent.Experiences[0].Bullets[0].SourceFactIDs)
	}
	if decodedContent.Experiences[0].CompanyContact == nil || decodedContent.Experiences[0].CompanyContact.Value != "+1-555-0100" || decodedContent.Experiences[0].CompanyContact.Type != domain.ContactTypePhone {
		t.Errorf("expected CompanyContact preserved in TailoredExperience, got %q", decodedContent.Experiences[0].CompanyContact)
	}

	gapData, err := json.Marshal(gapReport)
	if err != nil {
		t.Fatalf("failed to marshal GapReport: %v", err)
	}
	var decodedGap domain.GapReport
	if err := json.Unmarshal(gapData, &decodedGap); err != nil {
		t.Fatalf("failed to unmarshal GapReport: %v", err)
	}
	if len(decodedGap.MissingRequirements) != 1 || decodedGap.MissingRequirements[0].Importance != domain.GapImportancePreferred {
		t.Errorf("decoded GapReport mismatch: %+v", decodedGap)
	}
}

func TestMasterProfileSnapshot(t *testing.T) {
	profile := domain.Profile{ID: "prof-1", UserID: "user-1", FullName: "Jane Doe", MasterRevision: 3}
	snapshot := domain.MasterProfileSnapshot{
		Profile:        profile,
		MasterRevision: 3,
		Skills:         []domain.Skill{{ID: "s-1", Name: "Go"}},
		Experiences:    []domain.WorkExperience{{ID: "e-1", Company: "Acme"}},
		Projects:       []domain.Project{{ID: "p-1", Name: "Resumeee"}},
		Education:      []domain.Education{{ID: "ed-1", Institution: "MIT"}},
		Certifications: []domain.Certification{{ID: "c-1", Name: "AWS"}},
		Facts:          []domain.Fact{{ID: "f-1", Statement: "Did something great"}},
		ExportedAt:     time.Now(),
	}

	if snapshot.MasterRevision != 3 {
		t.Errorf("expected snapshot revision 3, got %d", snapshot.MasterRevision)
	}
	if len(snapshot.Skills) != 1 || len(snapshot.Facts) != 1 {
		t.Errorf("expected populated snapshot slices, got %+v", snapshot)
	}
}

func TestDomainErrors(t *testing.T) {
	tests := []struct {
		err      error
		target   error
		expected bool
	}{
		{err: domain.ErrNotFound, target: domain.ErrNotFound, expected: true},
		{err: domain.ErrValidation, target: domain.ErrValidation, expected: true},
		{err: domain.ErrProvenance, target: domain.ErrProvenance, expected: true},
		{err: domain.ErrTemplateSyntax, target: domain.ErrTemplateSyntax, expected: true},
		{err: domain.ErrQueueFull, target: domain.ErrQueueFull, expected: true},
		{err: domain.ErrNotFound, target: domain.ErrValidation, expected: false},
	}

	for _, tc := range tests {
		if errors.Is(tc.err, tc.target) != tc.expected {
			t.Errorf("errors.Is(%v, %v) expected %v", tc.err, tc.target, tc.expected)
		}
	}
}

func TestTailoringStrategyAndTargetResumeRFC005(t *testing.T) {
	// 1. Assert TailoringStrategy.IsBuiltIn()
	builtinStrat := domain.TailoringStrategy{ID: "builtin-1-page-swe"}
	customStrat := domain.TailoringStrategy{ID: "strat-custom-1"}
	if !builtinStrat.IsBuiltIn() {
		t.Errorf("expected builtin-1-page-swe to be built-in")
	}
	if customStrat.IsBuiltIn() {
		t.Errorf("expected strat-custom-1 to NOT be built-in")
	}

	// 2. Assert SuggestedThemeID serializes to null in JSON when omitted
	stratNoTheme := domain.TailoringStrategy{
		ID:                 "strat-1",
		UserID:             "user-1",
		Name:               "1-Page SWE",
		SystemInstructions: "Focus on latency",
		SuggestedThemeID:   nil,
	}
	bytesNoTheme, err := json.Marshal(stratNoTheme)
	if err != nil {
		t.Fatalf("failed to marshal strategy: %v", err)
	}
	if !strings.Contains(string(bytesNoTheme), `"suggested_theme_id":null`) {
		t.Errorf("expected suggested_theme_id to serialize to null, got: %s", string(bytesNoTheme))
	}

	themeID := "builtin-standard-clean"
	stratWithTheme := domain.TailoringStrategy{
		ID:                 "strat-2",
		UserID:             "user-1",
		Name:               "1-Page SWE",
		SystemInstructions: "Focus on latency",
		SuggestedThemeID:   &themeID,
	}
	bytesWithTheme, err := json.Marshal(stratWithTheme)
	if err != nil {
		t.Fatalf("failed to marshal strategy with theme: %v", err)
	}
	if !strings.Contains(string(bytesWithTheme), `"suggested_theme_id":"builtin-standard-clean"`) {
		t.Errorf("expected suggested_theme_id in JSON, got: %s", string(bytesWithTheme))
	}

	// 3. Assert TargetResume directly owns TemplateSetID
	targetResume := domain.TargetResume{
		ID:            "resume-1",
		UserID:        "user-1",
		Name:          "Stripe - Staff Backend",
		TemplateSetID: "builtin-standard-clean",
	}
	if targetResume.TemplateSetID != "builtin-standard-clean" {
		t.Errorf("expected TargetResume to directly own TemplateSetID, got: %s", targetResume.TemplateSetID)
	}
}

