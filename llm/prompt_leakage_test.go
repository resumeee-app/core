package llm_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core/domain"
	"github.com/resumeee-app/resumeee-core/llm"
	"github.com/resumeee-app/resumeee-core/services/renderer"
	"github.com/resumeee-app/resumeee-core/validator"
)

// makeLeakageTestRequest constructs a generation request containing extensive internal multi-tenancy,
// database timestamp, audit, configuration, and metadata fields.
func makeLeakageTestRequest() domain.GenerationRequest {
	tenantID := "usr-tenant-999"
	profileID := "profile-usr-tenant-999"
	now := time.Date(2026, 3, 15, 10, 30, 0, 0, time.UTC)
	created := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	startDate := domain.NewDate(2020, 1, 1)
	hours := 40

	return domain.GenerationRequest{
		GlobalRules:        "Tier 1 global writing rule.",
		PresetInstructions: "Tier 2 preset instructions.",
		MainProfile: &domain.MainProfileSnapshot{
			Profile: domain.Profile{
				ID:                 profileID,
				UserID:             tenantID,
				FullName:           "Candidate Zero",
				Headline:           "Principal Distributed Systems Architect",
				Email:              "candidate.zero@example.com",
				Phone:              "+1 (555) 000-1111",
				Summary:            "Experienced systems architect.",
				Location:           domain.Location{City: "San Francisco", Region: "CA", CountryCode: "US"},
				Profiles:           []domain.SocialLink{{Network: "GitHub", Username: "candzero", URL: "https://github.com/candzero"}},
				GlobalWritingRules: "Do not use passive voice.",
				DefaultProvider:    "gemini",
				DefaultModel:       "gemini-3.8-flash",
				MainRevision:       42,
				Metadata:           map[string]any{"internal_billing_tier": "enterprise", "system_tag": "vip"},
				CreatedAt:          created,
				UpdatedAt:          updated,
			},
			Skills: []domain.Skill{
				{
					ID:              "uuid-skill-100",
					ShortID:         "sk-1",
					UserID:          tenantID,
					Name:            "Distributed Consensus",
					Slug:            "distributed-consensus",
					IsGlobal:        true,
					Category:        "Systems",
					Proficiency:     "Expert",
					YearsExperience: 7.5,
					Keywords:        []string{"Raft", "Paxos"},
					Metadata:        map[string]any{"indexing_score": 98.6},
					CreatedAt:       created,
					UpdatedAt:       updated,
				},
			},
			Experiences: []domain.WorkExperience{
				{
					ID:                "uuid-exp-200",
					ShortID:           "exp-1",
					UserID:            tenantID,
					Company:           "Stripe",
					RoleTitle:         "Staff Infrastructure Engineer",
					EmploymentType:    domain.EmploymentTypeFullTime,
					StartDate:         startDate,
					Location:          domain.Location{City: "San Francisco", Region: "CA", CountryCode: "US"},
					CompanyLocation:   domain.Location{City: "South San Francisco", Region: "CA", CountryCode: "US"},
					CompanyContact:    &domain.ContactDetail{Type: domain.ContactTypeEmail, Value: "hr@stripe.com"},
					HoursPerWeek:      &hours,
					SupervisorName:    "Jane Supervisor",
					SupervisorContact: "jane.supervisor@stripe.com",
					DutiesSummary:     "Led payment ledger core team.",
					Metadata:          map[string]any{"internal_payroll_code": "ST-999"},
					CreatedAt:         created,
					UpdatedAt:         updated,
					Facts: []domain.Fact{
						{
							ID:        "uuid-fact-300",
							ShortID:   "fact-1",
							UserID:    tenantID,
							OwnerID:   "uuid-exp-200",
							OwnerType: domain.OwnerTypeWorkExperience,
							Statement: "Architected multi-region consensus ledger",
							Impact:    "Reduced global transaction latency by 42%",
							Skills: []domain.Skill{
								{
									ID:       "uuid-skill-100",
									ShortID:  "sk-1",
									UserID:   tenantID,
									Name:     "Distributed Consensus",
									IsGlobal: true,
								},
							},
							CreatedAt: created,
							UpdatedAt: updated,
						},
					},
				},
			},
			Projects: []domain.Project{
				{
					ID:        "uuid-proj-400",
					ShortID:   "proj-1",
					UserID:    tenantID,
					Name:      "RaftCore",
					Role:      "Creator & Lead Maintainer",
					URL:       "https://github.com/candzero/raftcore",
					Summary:   "Minimal consensus library in Go",
					Metadata:  map[string]any{"repo_star_count": 5200},
					CreatedAt: created,
					UpdatedAt: updated,
				},
			},
			Education: []domain.Education{
				{
					ID:          "uuid-edu-500",
					ShortID:     "edu-1",
					UserID:      tenantID,
					Institution: "Stanford University",
					Degree:      "M.S.",
					Field:       "Computer Science",
					StartDate:   &startDate,
					Score:       "3.95 GPA",
					Location:    domain.Location{City: "Stanford", Region: "CA", CountryCode: "US"},
					Metadata:    map[string]any{"student_id_hash": "st-888"},
					CreatedAt:   created,
					UpdatedAt:   updated,
				},
			},
			Certifications: []domain.Certification{
				{
					ID:        "uuid-cert-600",
					ShortID:   "cert-1",
					UserID:    tenantID,
					Name:      "AWS Solutions Architect Professional",
					Issuer:    "Amazon Web Services",
					URL:       "https://aws.amazon.com/verify/12345",
					Metadata:  map[string]any{"badge_id": "b-999"},
					CreatedAt: created,
					UpdatedAt: updated,
				},
			},
			Facts: []domain.Fact{
				{
					ID:        "uuid-fact-standalone-700",
					ShortID:   "fact-2",
					UserID:    tenantID,
					OwnerID:   "uuid-proj-400",
					OwnerType: domain.OwnerTypeProject,
					Statement: "Implemented snapshot compression protocol",
					Impact:    "Decreased log compaction time by 60%",
					CreatedAt: created,
					UpdatedAt: updated,
				},
			},
			MainRevision:  42,
			ExportedAt:    now,
		},
		BranchContext: domain.Branch{
			TargetRole:         "Staff Systems Architect",
			TargetCompany:      "Cloudflare",
			TargetRequirements: "Distributed consensus and high-throughput systems.",
			BranchInstructions: "Emphasize zero-downtime consensus transitions.",
		},
		PriorVersion: &domain.TailoredContent{
			Summary: "Prior active summary with no leaks.",
			Experiences: []domain.TailoredExperience{
				{
					SourceEntityID:    "uuid-exp-200",
					Company:           "Stripe",
					RoleTitle:         "Staff Infrastructure Engineer",
					StartDate:         startDate,
					Location:          domain.Location{City: "CanaryPriorExpCity"},
					CompanyLocation:   domain.Location{City: "CanaryPriorCompanyCity"},
					CompanyContact:    &domain.ContactDetail{Type: domain.ContactTypeEmail, Value: "canary-prior-hr@stripe.com"},
					HoursPerWeek:      &hours,
					SupervisorName:    "Canary Prior Supervisor",
					SupervisorContact: "canary-prior-supervisor@stripe.com",
					Bullets: []domain.TailoredBullet{
						{
							Text:          "Prior tailored bullet",
							SourceFactIDs: []string{"uuid-fact-300"},
						},
					},
				},
			},
			Education: []domain.TailoredEducation{
				{
					SourceEntityID: "uuid-edu-500",
					Institution:    "Stanford University",
					Degree:         "M.S.",
					Field:          "Computer Science",
					Location:       domain.Location{City: "CanaryPriorEduCity"},
				},
			},
		},
	}
}

// forbiddenPromptTokens defines internal database, multi-tenancy, system configuration,
// and non-tailoring candidate PII/contact/administrative tokens that must NEVER be leaked into compiled LLM prompt strings.
var forbiddenPromptTokens = []string{
	// Internal database and tenancy fields
	`"user_id"`,
	`"usr-tenant-999"`,
	`"created_at"`,
	`"updated_at"`,
	`"is_global"`,
	`"slug"`,
	`"metadata"`,
	`"internal_billing_tier"`,
	`"internal_payroll_code"`,
	`"student_id_hash"`,
	`"badge_id"`,
	`"main_revision"`,
	`"exported_at"`,
	`"default_provider"`,
	`"default_model"`,
	`"short_id"`,
	`"profile-usr-tenant-999"`,

	// Profile contact and personal data
	`"Candidate Zero"`,
	`"candidate.zero@example.com"`,
	`"+1 (555) 000-1111"`,
	`"candzero"`,
	`"https://github.com/candzero"`,
	`"email"`,
	`"phone"`,
	`"profiles"`,

	// Experience location, company contact, and supervisor metadata
	`"San Francisco"`,
	`"South San Francisco"`,
	`"hr@stripe.com"`,
	`"+1 (555) 019-9000"`,
	`"Jane Supervisor"`,
	`"jane.supervisor@stripe.com"`,
	`"company_location"`,
	`"company_contact"`,
	`"hours_per_week"`,
	`"supervisor_name"`,
	`"supervisor_contact"`,

	// Education location
	`"Stanford, CA"`,
	`"city": "Stanford"`,

	// PriorVersion non-tailoring canary fields
	`"CanaryPriorExpCity"`,
	`"CanaryPriorCompanyCity"`,
	`"canary-prior-hr@stripe.com"`,
	`"Canary Prior Supervisor"`,
	`"canary-prior-supervisor@stripe.com"`,
	`"CanaryPriorEduCity"`,
}

func TestBuildPrompt_PreventsInternalDataLeakage(t *testing.T) {
	req := makeLeakageTestRequest()

	payload, err := llm.BuildPrompt(req)
	if err != nil {
		t.Fatalf("BuildPrompt returned unexpected error: %v", err)
	}

	combinedPrompts := payload.SystemPrompt + "\n" + payload.UserPrompt

	for _, forbidden := range forbiddenPromptTokens {
		if strings.Contains(combinedPrompts, forbidden) {
			t.Errorf("BuildPrompt leaked forbidden token %s into compiled prompt", forbidden)
		}
	}

	// Verify required public career fields ARE present with projected short IDs
	requiredShortIDs := []string{
		`"id": "sk-1"`,
		`"id": "exp-1"`,
		`"id": "fact-1"`,
		`"id": "proj-1"`,
		`"id": "edu-1"`,
		`"id": "cert-1"`,
		`"id": "fact-2"`,
		`"owner_id": "exp-1"`,
	}
	for _, reqID := range requiredShortIDs {
		if !strings.Contains(payload.UserPrompt, reqID) {
			t.Errorf("BuildPrompt UserPrompt missing required short ID %s", reqID)
		}
	}

	// Verify content fields exist
	if !strings.Contains(payload.UserPrompt, `"company": "Stripe"`) {
		t.Errorf("BuildPrompt UserPrompt missing company 'Stripe'")
	}
	if !strings.Contains(payload.UserPrompt, `"name": "Distributed Consensus"`) {
		t.Errorf("BuildPrompt UserPrompt missing skill name 'Distributed Consensus'")
	}
	if !strings.Contains(payload.UserPrompt, "Architected multi-region consensus ledger") {
		t.Errorf("BuildPrompt UserPrompt missing fact statement")
	}

	// Verify provenance validation round-trip succeeds with short IDs generated in response
	mockLLMResponse := &domain.TailoredContent{
		Summary: "Results-oriented architect with deep distributed consensus expertise.",
		SkillGroups: []domain.TailoredSkillGroup{
			{
				Category:       "Systems",
				Skills:         []string{"Distributed Consensus"},
				SourceSkillIDs: []string{"sk-1"},
			},
		},
		Experiences: []domain.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Stripe",
				RoleTitle:      "Staff Infrastructure Engineer",
				StartDate:      domain.NewDate(2020, 1, 1),
				Bullets: []domain.TailoredBullet{
					{
						Text:           "Architected multi-region consensus ledger reducing latency by 42%.",
						SourceFactIDs:  []string{"fact-1"},
						SourceSkillIDs: []string{"sk-1"},
					},
				},
			},
		},
		Projects: []domain.TailoredProject{
			{
				SourceEntityID: "proj-1",
				Name:           "RaftCore",
				Bullets: []domain.TailoredBullet{
					{
						Text:          "Implemented snapshot compression protocol decreasing compaction by 60%.",
						SourceFactIDs: []string{"fact-2"},
					},
				},
			},
		},
		Education: []domain.TailoredEducation{
			{
				SourceEntityID: "edu-1",
				Institution:    "Stanford University",
				Degree:         "M.S.",
				Field:          "Computer Science",
				StartDate:      &req.MainProfile.Experiences[0].StartDate,
			},
		},
		Certifications: []domain.TailoredCert{
			{
				SourceEntityID: "cert-1",
				Name:           "AWS Solutions Architect Professional",
				Issuer:         "Amazon Web Services",
			},
		},
	}

	valResult, valErr := validator.Validate(mockLLMResponse, req.MainProfile, &req.BranchContext)
	if valErr != nil {
		t.Fatalf("provenance validator failed on short IDs: %v", valErr)
	}
	if len(valResult.Warnings) > 0 {
		t.Errorf("expected zero validation warnings, got: %v", valResult.Warnings)
	}
}

func TestBuildGapAnalysisPrompt_PreventsInternalDataLeakage(t *testing.T) {
	req := makeLeakageTestRequest()

	payload, err := llm.BuildGapAnalysisPrompt(req)
	if err != nil {
		t.Fatalf("BuildGapAnalysisPrompt returned unexpected error: %v", err)
	}

	combinedPrompts := payload.SystemPrompt + "\n" + payload.UserPrompt

	for _, forbidden := range forbiddenPromptTokens {
		if strings.Contains(combinedPrompts, forbidden) {
			t.Errorf("BuildGapAnalysisPrompt leaked forbidden token %s into compiled prompt", forbidden)
		}
	}

	// Verify essential career content is present
	if !strings.Contains(payload.UserPrompt, `"company": "Stripe"`) {
		t.Errorf("BuildGapAnalysisPrompt UserPrompt missing company 'Stripe'")
	}
	if !strings.Contains(payload.UserPrompt, `"name": "Distributed Consensus"`) {
		t.Errorf("BuildGapAnalysisPrompt UserPrompt missing skill name 'Distributed Consensus'")
	}
}

func TestNonTailoringFields_PreservedInRenderedOutput(t *testing.T) {
	req := makeLeakageTestRequest()

	mockLLMResponse := &domain.TailoredContent{
		Summary: "Tailored summary.",
		SkillGroups: []domain.TailoredSkillGroup{
			{
				Category:       "Systems",
				Skills:         []string{"Distributed Consensus"},
				SourceSkillIDs: []string{"sk-1"},
			},
		},
		Experiences: []domain.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Stripe",
				RoleTitle:      "Staff Infrastructure Engineer",
				StartDate:      domain.NewDate(2020, 1, 1),
				Bullets: []domain.TailoredBullet{
					{
						Text:          "Architected multi-region consensus ledger",
						SourceFactIDs: []string{"fact-1"},
					},
				},
			},
		},
		Education: []domain.TailoredEducation{
			{
				SourceEntityID: "edu-1",
				Institution:    "Stanford University",
				Degree:         "M.S.",
				Field:          "Computer Science",
				StartDate:      req.MainProfile.Education[0].StartDate,
			},
		},
	}

	valResult, err := validator.Validate(mockLLMResponse, req.MainProfile, &req.BranchContext)
	if err != nil {
		t.Fatalf("validator.Validate returned unexpected error: %v", err)
	}

	// Verify validator hydrated stripped fields from MainProfileSnapshot
	exp := valResult.SanitizedContent.Experiences[0]
	if exp.Location.City != "San Francisco" {
		t.Errorf("expected experience Location.City %q, got %q", "San Francisco", exp.Location.City)
	}
	if exp.CompanyLocation.City != "South San Francisco" {
		t.Errorf("expected experience CompanyLocation.City %q, got %q", "South San Francisco", exp.CompanyLocation.City)
	}
	if exp.SupervisorName != "Jane Supervisor" {
		t.Errorf("expected SupervisorName %q, got %q", "Jane Supervisor", exp.SupervisorName)
	}
	if exp.SupervisorContact != "jane.supervisor@stripe.com" {
		t.Errorf("expected SupervisorContact %q, got %q", "jane.supervisor@stripe.com", exp.SupervisorContact)
	}
	if exp.CompanyContact == nil || exp.CompanyContact.Value != "hr@stripe.com" {
		t.Errorf("expected CompanyContact.Value %q, got %v", "hr@stripe.com", exp.CompanyContact)
	}
	if exp.HoursPerWeek == nil || *exp.HoursPerWeek != 40 {
		t.Errorf("expected HoursPerWeek 40, got %v", exp.HoursPerWeek)
	}

	edu := valResult.SanitizedContent.Education[0]
	if edu.Location.City != "Stanford" {
		t.Errorf("expected education Location.City %q, got %q", "Stanford", edu.Location.City)
	}

	tmpl := &domain.TemplateSet{
		HTMLTemplate: `<div><h1>{{.FullName}}</h1><p>{{.Email}} | {{.Phone}} | {{.Location.City}}</p><p>{{(index .Profiles 0).Username}}</p>{{range .Experiences}}<p>{{.Company}} - {{.Location.City}} - {{.CompanyLocation.City}} - {{.SupervisorName}} - {{.SupervisorContact}} - {{.CompanyContact.Value}} - {{.HoursPerWeek}}</p>{{end}}{{range .Education}}<p>{{.Institution}} - {{.Location.City}}</p>{{end}}</div>`,
		MarkdownTemplate: `# {{.FullName}}
{{.Email}} | {{.Phone}} | {{.Location.City}} | {{(index .Profiles 0).Username}}
{{range .Experiences}}* {{.Company}} - {{.Location.City}} - {{.CompanyLocation.City}} - {{.SupervisorName}} - {{.SupervisorContact}} - {{.CompanyContact.Value}} - {{.HoursPerWeek}}
{{end}}{{range .Education}}* {{.Institution}} - {{.Location.City}}
{{end}}`,
		TextTemplate: `{{.FullName}}
{{.Email}} | {{.Phone}} | {{.Location.City}} | {{(index .Profiles 0).Username}}
{{range .Experiences}}{{.Company}} - {{.Location.City}} - {{.CompanyLocation.City}} - {{.SupervisorName}} - {{.SupervisorContact}} - {{.CompanyContact.Value}} - {{.HoursPerWeek}}
{{end}}{{range .Education}}{{.Institution}} - {{.Location.City}}
{{end}}`,
	}

	rendered, err := renderer.Render(context.Background(), tmpl, &req.MainProfile.Profile, valResult.SanitizedContent)
	if err != nil {
		t.Fatalf("renderer.Render failed: %v", err)
	}

	expectedRenderedSnippets := []string{
		"Candidate Zero",
		"candidate.zero@example.com",
		"(555) 000-1111",
		"San Francisco",
		"candzero",
		"South San Francisco",
		"Jane Supervisor",
		"jane.supervisor@stripe.com",
		"hr@stripe.com",
		"40",
		"Stanford",
	}

	for _, snippet := range expectedRenderedSnippets {
		if !strings.Contains(rendered.HTML, snippet) {
			t.Errorf("rendered HTML output missing expected snippet %q", snippet)
		}
		if !strings.Contains(rendered.Markdown, snippet) {
			t.Errorf("rendered Markdown output missing expected snippet %q", snippet)
		}
		if !strings.Contains(rendered.PlainText, snippet) {
			t.Errorf("rendered PlainText output missing expected snippet %q", snippet)
		}
	}
}
