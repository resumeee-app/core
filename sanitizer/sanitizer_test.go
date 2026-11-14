package sanitizer_test

import (
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/sanitizer"
)

func sampleCandidateProfile() core.Profile {
	return core.Profile{
		ID:       "prof-123",
		FullName: "Alice Walker",
		Email:    "alice.walker@example.org",
		Phone:    "+1 (555) 234-5678",
		Headline: "Staff Cloud Engineer (Alice Walker)",
		Summary:  "Alice Walker is an experienced systems architect. Reach me at alice.walker@example.org or +1 (555) 234-5678. Portfolio: https://alicewalker.dev.",
		Profiles: []core.SocialLink{
			{Network: "LinkedIn", Username: "alicewalker", URL: "https://linkedin.com/in/alicewalker"},
			{Network: "GitHub", Username: "alicewalker-dev", URL: "https://github.com/alicewalker-dev"},
		},
		Metadata: map[string]any{
			"website": "https://alicewalker.dev",
		},
	}
}

func sampleCandidateSnapshot() *core.MainProfileSnapshot {
	prof := sampleCandidateProfile()
	endDate := core.DatePtr(core.NewDate(2023, 12, 31))
	return &core.MainProfileSnapshot{
		Profile: prof,
		Experiences: []core.WorkExperience{
			{
				ID:                "exp-1",
				Company:           "Acme Cloud (Alice Walker)",
				RoleTitle:         "Lead Engineer (Alice Walker)",
				StartDate:         core.NewDate(2021, 1, 1),
				EndDate:           endDate,
				DutiesSummary:     "Alice Walker led distributed cache migration.",
				SupervisorName:    "Alice Walker",
				SupervisorContact: "alice.walker@example.org",
				CompanyContact: &core.ContactDetail{
					Type:  core.ContactTypeEmail,
					Value: "alice.walker@example.org",
				},
				Facts: []core.Fact{
					{
						ID:        "fact-1",
						Statement: "Alice Walker engineered Raft consensus layer saving $1M annually.",
						Impact:    "Contact alice.walker@example.org for verification.",
					},
				},
			},
		},
		Projects: []core.Project{
			{
				ID:      "proj-1",
				Name:    "Raft-Go by Alice Walker",
				Role:    "Creator (Alice Walker)",
				URL:     "https://github.com/alicewalker-dev/raft-go",
				Summary: "Authored by Alice Walker (alicewalker-dev).",
				Facts: []core.Fact{
					{
						ID:        "fact-proj-1",
						Statement: "Alice Walker benchmarked consensus latency.",
						Impact:    "See https://alicewalker.dev for benchmarks.",
					},
				},
			},
		},
		Education: []core.Education{
			{
				ID:          "edu-1",
				Institution: "MIT (Alice Walker Fellow)",
				Degree:      "M.S. (Alice Walker)",
				Field:       "CS",
				Facts: []core.Fact{
					{
						ID:        "fact-edu-1",
						Statement: "Alice Walker authored thesis on Paxos variants.",
					},
				},
			},
		},
		Certifications: []core.Certification{
			{
				ID:      "cert-1",
				Name:    "CKA (Alice Walker)",
				Issuer:  "CNCF",
				URL:     "https://alicewalker.dev/certs/cka",
				Summary: "Issued to Alice Walker.",
			},
		},
		Facts: []core.Fact{
			{
				ID:        "fact-root-1",
				Statement: "Alice Walker holds 3 patents.",
				Impact:    "Verified at alice.walker@example.org.",
			},
		},
		ExportedAt: time.Now(),
	}
}

func assertContainsNoPII(t *testing.T, fieldName, text string) {
	t.Helper()
	piiTokens := []struct {
		label string
		val   string
	}{
		{"FullName", "Alice Walker"},
		{"Email", "alice.walker@example.org"},
		{"Phone", "+1 (555) 234-5678"},
		{"Website", "https://alicewalker.dev"},
		{"GitHub URL", "https://github.com/alicewalker-dev"},
		{"LinkedIn URL", "https://linkedin.com/in/alicewalker"},
		{"Social Username", "alicewalker-dev"},
	}
	for _, tok := range piiTokens {
		if strings.Contains(text, tok.val) {
			t.Errorf("field %s leaked candidate %s (%q): %s", fieldName, tok.label, tok.val, text)
		}
	}
}

func TestSession_ObfuscateSnapshot_StrippedAndLLMFields(t *testing.T) {
	snap := sampleCandidateSnapshot()
	session := sanitizer.NewSession(&snap.Profile)

	if session.ObfuscateSnapshot(nil) != nil {
		t.Fatal("expected nil ObfuscateSnapshot result for nil input")
	}

	obf := session.ObfuscateSnapshot(snap)

	t.Run("non-LLM contact and supervisor fields are left untouched", func(t *testing.T) {
		cases := []struct {
			name string
			got  string
			want string
		}{
			{"Profile.FullName", obf.Profile.FullName, "Alice Walker"},
			{"Profile.Email", obf.Profile.Email, "alice.walker@example.org"},
			{"Profile.Phone", obf.Profile.Phone, "+1 (555) 234-5678"},
			{"Profile.Profiles[0].URL", obf.Profile.Profiles[0].URL, "https://linkedin.com/in/alicewalker"},
			{"Profile.Profiles[0].Username", obf.Profile.Profiles[0].Username, "alicewalker"},
			{"Profile.Profiles[1].URL", obf.Profile.Profiles[1].URL, "https://github.com/alicewalker-dev"},
			{"Profile.Profiles[1].Username", obf.Profile.Profiles[1].Username, "alicewalker-dev"},
			{"Experiences[0].SupervisorName", obf.Experiences[0].SupervisorName, "Alice Walker"},
			{"Experiences[0].SupervisorContact", obf.Experiences[0].SupervisorContact, "alice.walker@example.org"},
			{"Experiences[0].CompanyContact.Value", obf.Experiences[0].CompanyContact.Value, "alice.walker@example.org"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if tc.got != tc.want {
					t.Errorf("%s was unexpectedly modified: got %q, want %q", tc.name, tc.got, tc.want)
				}
			})
		}

		if got := obf.Profile.Metadata["website"]; got != "https://alicewalker.dev" {
			t.Errorf("Profile.Metadata[website] was unexpectedly modified: got %v", got)
		}
	})

	t.Run("LLM-bound snapshot fields scrub PII", func(t *testing.T) {
		llmFields := []struct {
			name string
			val  string
		}{
			{"Profile.Headline", obf.Profile.Headline},
			{"Profile.Summary", obf.Profile.Summary},
			{"Experiences[0].Company", obf.Experiences[0].Company},
			{"Experiences[0].RoleTitle", obf.Experiences[0].RoleTitle},
			{"Experiences[0].DutiesSummary", obf.Experiences[0].DutiesSummary},
			{"Experiences[0].Facts[0].Statement", obf.Experiences[0].Facts[0].Statement},
			{"Experiences[0].Facts[0].Impact", obf.Experiences[0].Facts[0].Impact},
			{"Projects[0].Name", obf.Projects[0].Name},
			{"Projects[0].Role", obf.Projects[0].Role},
			{"Projects[0].URL", obf.Projects[0].URL},
			{"Projects[0].Summary", obf.Projects[0].Summary},
			{"Projects[0].Facts[0].Statement", obf.Projects[0].Facts[0].Statement},
			{"Projects[0].Facts[0].Impact", obf.Projects[0].Facts[0].Impact},
			{"Education[0].Institution", obf.Education[0].Institution},
			{"Education[0].Degree", obf.Education[0].Degree},
			{"Education[0].Facts[0].Statement", obf.Education[0].Facts[0].Statement},
			{"Certifications[0].Name", obf.Certifications[0].Name},
			{"Certifications[0].URL", obf.Certifications[0].URL},
			{"Certifications[0].Summary", obf.Certifications[0].Summary},
			{"Facts[0].Statement", obf.Facts[0].Statement},
			{"Facts[0].Impact", obf.Facts[0].Impact},
		}
		for _, tc := range llmFields {
			t.Run(tc.name, func(t *testing.T) {
				assertContainsNoPII(t, tc.name, tc.val)
			})
		}

		if !strings.Contains(obf.Profile.Summary, sanitizer.PlaceholderFullName) {
			t.Errorf("expected Profile.Summary to contain %q, got %q", sanitizer.PlaceholderFullName, obf.Profile.Summary)
		}
		if !strings.Contains(obf.Profile.Summary, sanitizer.PlaceholderEmail) {
			t.Errorf("expected Profile.Summary to contain %q, got %q", sanitizer.PlaceholderEmail, obf.Profile.Summary)
		}
		if !strings.Contains(obf.Experiences[0].Facts[0].Statement, sanitizer.PlaceholderFullName) {
			t.Errorf("expected Fact.Statement to contain %q, got %q", sanitizer.PlaceholderFullName, obf.Experiences[0].Facts[0].Statement)
		}
	})

	t.Run("original snapshot is not mutated in-place", func(t *testing.T) {
		if !strings.Contains(snap.Profile.Summary, "Alice Walker") {
			t.Errorf("original snap.Profile.Summary was mutated: %q", snap.Profile.Summary)
		}
		if !strings.Contains(snap.Experiences[0].DutiesSummary, "Alice Walker") {
			t.Errorf("original snap.Experiences[0].DutiesSummary was mutated: %q", snap.Experiences[0].DutiesSummary)
		}
		if !strings.Contains(snap.Experiences[0].Facts[0].Statement, "Alice Walker") {
			t.Errorf("original snap.Experiences[0].Facts[0].Statement was mutated: %q", snap.Experiences[0].Facts[0].Statement)
		}
		if !strings.Contains(snap.Projects[0].Summary, "Alice Walker") {
			t.Errorf("original snap.Projects[0].Summary was mutated: %q", snap.Projects[0].Summary)
		}
		if !strings.Contains(snap.Education[0].Facts[0].Statement, "Alice Walker") {
			t.Errorf("original snap.Education[0].Facts[0].Statement was mutated: %q", snap.Education[0].Facts[0].Statement)
		}
		if !strings.Contains(snap.Certifications[0].Summary, "Alice Walker") {
			t.Errorf("original snap.Certifications[0].Summary was mutated: %q", snap.Certifications[0].Summary)
		}
		if !strings.Contains(snap.Facts[0].Statement, "Alice Walker") {
			t.Errorf("original snap.Facts[0].Statement was mutated: %q", snap.Facts[0].Statement)
		}
	})
}

func TestSession_ObfuscateRequest(t *testing.T) {
	snap := sampleCandidateSnapshot()
	session := sanitizer.NewSession(&snap.Profile)

	req := core.GenerationRequest{
		GlobalRules:        "Always highlight Alice Walker's leadership at alice.walker@example.org.",
		PresetInstructions: "Format concise bullets for Alice Walker.",
		MainProfile:        snap,
		BranchContext: core.Branch{
			TargetRole:         "Principal Architect for Alice Walker",
			TargetCompany:      "Acme (Alice Walker)",
			TargetRequirements: "Looking for an engineer like Alice Walker (contact: alice.walker@example.org).",
			BranchInstructions: "Emphasize Alice Walker's cloud background.",
			CustomDirectives:   "Legacy directives mentioning Alice Walker (not sent directly).",
		},
		PriorVersion: &core.TailoredContent{
			Summary: "Prior version summary for Alice Walker.",
			Experiences: []core.TailoredExperience{
				{
					SourceEntityID: "exp-1",
					Company:        "Acme",
					Bullets: []core.TailoredBullet{
						{Text: "Alice Walker scaled infrastructure by 300%."},
					},
				},
			},
			Education: []core.TailoredEducation{
				{
					SourceEntityID: "edu-1",
					Institution:    "MIT",
					Bullets: []core.TailoredBullet{
						{Text: "Alice Walker graduated with honors."},
					},
				},
			},
		},
	}

	obfReq := session.ObfuscateRequest(req)

	scrubbedFields := []struct {
		name string
		val  string
	}{
		{"GlobalRules", obfReq.GlobalRules},
		{"PresetInstructions", obfReq.PresetInstructions},
		{"BranchContext.TargetRole", obfReq.BranchContext.TargetRole},
		{"BranchContext.TargetCompany", obfReq.BranchContext.TargetCompany},
		{"BranchContext.TargetRequirements", obfReq.BranchContext.TargetRequirements},
		{"BranchContext.BranchInstructions", obfReq.BranchContext.BranchInstructions},
		{"PriorVersion.Summary", obfReq.PriorVersion.Summary},
		{"PriorVersion.Experiences[0].Bullets[0].Text", obfReq.PriorVersion.Experiences[0].Bullets[0].Text},
		{"PriorVersion.Education[0].Bullets[0].Text", obfReq.PriorVersion.Education[0].Bullets[0].Text},
	}
	for _, tc := range scrubbedFields {
		t.Run(tc.name, func(t *testing.T) {
			assertContainsNoPII(t, tc.name, tc.val)
		})
	}

	if obfReq.BranchContext.CustomDirectives != req.BranchContext.CustomDirectives {
		t.Errorf("expected unused BranchContext.CustomDirectives to remain untouched, got %q", obfReq.BranchContext.CustomDirectives)
	}

	// Verify original request objects were not mutated
	if !strings.Contains(req.GlobalRules, "Alice Walker") {
		t.Errorf("original req.GlobalRules was mutated")
	}
	if !strings.Contains(req.PriorVersion.Summary, "Alice Walker") {
		t.Errorf("original req.PriorVersion was mutated")
	}
}

func TestSession_DeobfuscationRestoresOriginalValues(t *testing.T) {
	snap := sampleCandidateSnapshot()
	session := sanitizer.NewSession(&snap.Profile)

	tailored := core.TailoredContent{
		Summary: "Results-driven cloud architect " + sanitizer.PlaceholderFullName + " with expertise in Kubernetes. Contact: " + sanitizer.PlaceholderEmail + " or " + sanitizer.PlaceholderPhone + ". See " + sanitizer.PlaceholderWebsite + " and https://social.example.com/profile-1.",
		SkillGroups: []core.TailoredSkillGroup{
			{Category: "Cloud", Skills: []string{"Go", "Kubernetes"}},
		},
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID:    "exp-1",
				Company:           "Acme (" + sanitizer.PlaceholderFullName + ")",
				RoleTitle:         "Staff Architect (" + sanitizer.PlaceholderFullName + ")",
				DutiesSummary:     "Managed team; contact at " + sanitizer.PlaceholderEmail,
				SupervisorName:    sanitizer.PlaceholderFullName,
				SupervisorContact: sanitizer.PlaceholderEmail,
				CompanyContact: &core.ContactDetail{
					Type:  core.ContactTypeEmail,
					Value: sanitizer.PlaceholderEmail,
				},
				Bullets: []core.TailoredBullet{
					{Text: sanitizer.PlaceholderFullName + " designed zero-trust networking reducing attack surface."},
					{Text: "Direct inquiries to " + sanitizer.PlaceholderEmail + " or candidate profile candidate-social-1."},
				},
			},
		},
		Projects: []core.TailoredProject{
			{
				SourceEntityID: "proj-1",
				Name:           "Raft-Go by " + sanitizer.PlaceholderFullName,
				Role:           "Lead (" + sanitizer.PlaceholderFullName + ")",
				URL:            "https://social.example.com/profile-2",
				Summary:        "Maintained by " + sanitizer.PlaceholderFullName + " (candidate-social-2).",
				Bullets: []core.TailoredBullet{
					{Text: sanitizer.PlaceholderFullName + " benchmarked cluster failover."},
				},
			},
		},
		Education: []core.TailoredEducation{
			{
				SourceEntityID: "edu-1",
				Institution:    "MIT (" + sanitizer.PlaceholderFullName + ")",
				Degree:         "M.S.",
				Field:          "CS",
				Bullets: []core.TailoredBullet{
					{Text: sanitizer.PlaceholderFullName + " non-schema bullet."},
				},
			},
		},
		Certifications: []core.TailoredCert{
			{
				SourceEntityID: "cert-1",
				Name:           "CKA (" + sanitizer.PlaceholderFullName + ")",
				Issuer:         "CNCF",
				URL:            sanitizer.PlaceholderWebsite + "/cka",
			},
		},
	}

	gapReport := core.GapReport{
		OverallMatchSummary: sanitizer.PlaceholderFullName + " strongly matches 90% of requirements.",
		StrategicRecommendations: []string{
			sanitizer.PlaceholderFullName + " should highlight enterprise scaling.",
			"Add portfolio link " + sanitizer.PlaceholderWebsite,
		},
		MissingRequirements: []core.GapItem{
			{
				Requirement:       "Experience with AWS for " + sanitizer.PlaceholderFullName,
				MissingArea:       "Cloud (" + sanitizer.PlaceholderFullName + ")",
				ClarifyingPrompt:  "Does " + sanitizer.PlaceholderFullName + " have AWS production experience?",
				RecommendedAction: "Clarify with " + sanitizer.PlaceholderFullName,
			},
		},
		IdentifiedGaps: []core.GapItem{
			{
				Requirement:       "Terraform",
				MissingArea:       "IaC",
				ClarifyingPrompt:  "Has " + sanitizer.PlaceholderFullName + " used Terraform?",
				RecommendedAction: "Add details for " + sanitizer.PlaceholderFullName,
			},
		},
	}

	atsScore := core.ATSScore{
		ExtractionSummary: "Parsed resume for " + sanitizer.PlaceholderFullName,
		MatchingSummary:   "Matched " + sanitizer.PlaceholderFullName + " against role",
		ExtractedCandidate: &core.ATSExtractedCandidate{
			Summary:        sanitizer.PlaceholderFullName + " (" + sanitizer.PlaceholderEmail + ")",
			Skills:         []string{"Go", sanitizer.PlaceholderFullName},
			Certifications: []string{"CKA for " + sanitizer.PlaceholderFullName},
			Experiences: []core.ATSExtractedExperience{
				{
					Company:    "Acme",
					Title:      "Engineer (" + sanitizer.PlaceholderFullName + ")",
					DateRange:  "2021-2023",
					Highlights: []string{sanitizer.PlaceholderFullName + " led migrations"},
				},
			},
			Educations: []core.ATSExtractedEducation{
				{
					Institution: "MIT (" + sanitizer.PlaceholderFullName + ")",
					Degree:      "M.S.",
					Field:       "CS",
					DateRange:   "2020",
				},
			},
		},
	}

	genResp := core.GenerationResponse{
		TailoredContent: tailored,
		GapReport:       gapReport,
	}

	session.DeobfuscateGenerationResponse(&genResp)
	session.DeobfuscateATSScore(&atsScore)

	t.Run("TailoredContent schema fields are restored", func(t *testing.T) {
		if !strings.Contains(genResp.TailoredContent.Summary, "Alice Walker") ||
			!strings.Contains(genResp.TailoredContent.Summary, "alice.walker@example.org") ||
			!strings.Contains(genResp.TailoredContent.Summary, "+1 (555) 234-5678") ||
			!strings.Contains(genResp.TailoredContent.Summary, "https://alicewalker.dev") ||
			!strings.Contains(genResp.TailoredContent.Summary, "https://linkedin.com/in/alicewalker") {
			t.Errorf("Summary failed to restore contact PII: %s", genResp.TailoredContent.Summary)
		}

		exp := genResp.TailoredContent.Experiences[0]
		if !strings.Contains(exp.Company, "Alice Walker") || !strings.Contains(exp.RoleTitle, "Alice Walker") {
			t.Errorf("Experience Company/RoleTitle failed to restore: %+v", exp)
		}
		if !strings.HasPrefix(exp.Bullets[0].Text, "Alice Walker designed") {
			t.Errorf("bullet 0 failed to deobfuscate: %s", exp.Bullets[0].Text)
		}
		if !strings.Contains(exp.Bullets[1].Text, "alice.walker@example.org") || !strings.Contains(exp.Bullets[1].Text, "alicewalker") {
			t.Errorf("bullet 1 failed to deobfuscate: %s", exp.Bullets[1].Text)
		}

		proj := genResp.TailoredContent.Projects[0]
		if proj.URL != "https://github.com/alicewalker-dev" || !strings.Contains(proj.Summary, "Alice Walker (alicewalker-dev)") {
			t.Errorf("project failed to deobfuscate: %+v", proj)
		}
		if !strings.HasPrefix(proj.Bullets[0].Text, "Alice Walker benchmarked") {
			t.Errorf("project bullet failed to deobfuscate: %s", proj.Bullets[0].Text)
		}

		if !strings.Contains(genResp.TailoredContent.Education[0].Institution, "Alice Walker") {
			t.Errorf("education institution failed to deobfuscate: %s", genResp.TailoredContent.Education[0].Institution)
		}
		cert := genResp.TailoredContent.Certifications[0]
		if !strings.Contains(cert.Name, "Alice Walker") || cert.URL != "https://alicewalker.dev/cka" {
			t.Errorf("certification failed to deobfuscate: %+v", cert)
		}
	})

	t.Run("TailoredContent non-schema fields are left untouched", func(t *testing.T) {
		exp := genResp.TailoredContent.Experiences[0]
		if exp.SupervisorName != sanitizer.PlaceholderFullName {
			t.Errorf("SupervisorName should remain untouched, got %q", exp.SupervisorName)
		}
		if exp.SupervisorContact != sanitizer.PlaceholderEmail {
			t.Errorf("SupervisorContact should remain untouched, got %q", exp.SupervisorContact)
		}
		if exp.CompanyContact.Value != sanitizer.PlaceholderEmail {
			t.Errorf("CompanyContact should remain untouched, got %q", exp.CompanyContact.Value)
		}
		if genResp.TailoredContent.Education[0].Bullets[0].Text != sanitizer.PlaceholderFullName+" non-schema bullet." {
			t.Errorf("Education.Bullets should remain untouched, got %q", genResp.TailoredContent.Education[0].Bullets[0].Text)
		}
	})

	t.Run("GapReport and ATSScore are restored", func(t *testing.T) {
		if !strings.Contains(genResp.GapReport.OverallMatchSummary, "Alice Walker strongly matches") {
			t.Errorf("GapReport OverallMatchSummary failed: %s", genResp.GapReport.OverallMatchSummary)
		}
		if !strings.Contains(genResp.GapReport.StrategicRecommendations[1], "https://alicewalker.dev") {
			t.Errorf("GapReport StrategicRecommendations failed: %v", genResp.GapReport.StrategicRecommendations)
		}
		if !strings.Contains(genResp.GapReport.MissingRequirements[0].ClarifyingPrompt, "Does Alice Walker have") {
			t.Errorf("GapReport MissingRequirements failed: %+v", genResp.GapReport.MissingRequirements)
		}
		if !strings.Contains(genResp.GapReport.IdentifiedGaps[0].RecommendedAction, "Alice Walker") {
			t.Errorf("GapReport IdentifiedGaps failed: %+v", genResp.GapReport.IdentifiedGaps)
		}
		if !strings.Contains(atsScore.ExtractionSummary, "Alice Walker") ||
			!strings.Contains(atsScore.MatchingSummary, "Alice Walker") ||
			!strings.Contains(atsScore.ExtractedCandidate.Summary, "alice.walker@example.org") ||
			!strings.Contains(atsScore.ExtractedCandidate.Experiences[0].Highlights[0], "Alice Walker") ||
			!strings.Contains(atsScore.ExtractedCandidate.Educations[0].Institution, "Alice Walker") ||
			!strings.Contains(atsScore.ExtractedCandidate.Certifications[0], "Alice Walker") {
			t.Errorf("ATSScore failed to restore placeholders: %+v", atsScore)
		}
	})
}

func TestSession_IdempotenceAndEmptySafety(t *testing.T) {
	emptySession := sanitizer.NewSession(&core.Profile{})
	raw := "Hello world"
	if emptySession.ObfuscateString(raw) != raw {
		t.Errorf("empty session corrupted raw string")
	}
	if emptySession.DeobfuscateString(raw) != raw {
		t.Errorf("empty session corrupted raw string in deobfuscate")
	}

	var nilResp *core.GenerationResponse
	emptySession.DeobfuscateGenerationResponse(nilResp)

	var nilContent *core.TailoredContent
	emptySession.DeobfuscateTailoredContent(nilContent)

	var nilReport *core.GapReport
	emptySession.DeobfuscateGapReport(nilReport)

	var nilScore *core.ATSScore
	emptySession.DeobfuscateATSScore(nilScore)

	snap := sampleCandidateSnapshot()
	session := sanitizer.NewSession(&snap.Profile)

	text := "Hello " + sanitizer.PlaceholderFullName
	deobf1 := session.DeobfuscateString(text)
	if deobf1 != "Hello Alice Walker" {
		t.Errorf("expected 'Hello Alice Walker', got %q", deobf1)
	}
	deobf2 := session.DeobfuscateString(deobf1)
	if deobf2 != "Hello Alice Walker" {
		t.Errorf("expected idempotent deobfuscation, got %q", deobf2)
	}
}

func TestSession_ShortUsernames_DoNotCorruptTechnicalTerms(t *testing.T) {
	prof := &core.Profile{
		FullName: "Test Candidate",
		Email:    "test@example.com",
		Profiles: []core.SocialLink{
			{Network: "GitHub", Username: "go", URL: "https://github.com/go"},
			{Network: "Twitter", Username: "ai", URL: "https://twitter.com/ai"},
			{Network: "Custom", Username: "c", URL: "https://custom.com/c"},
		},
	}

	session := sanitizer.NewSession(prof)

	text := "Senior Go engineer with 5 years Go experience and AI expertise. Wrote C drivers for real-time systems."
	obfText := session.ObfuscateString(text)
	if obfText != text {
		t.Errorf("short username corrupted technical terms: got %q, want %q", obfText, text)
	}

	echoed := "The candidate candidate-social-1 expert designed candidate-social-2 agents."
	deobf := session.DeobfuscateString(echoed)
	expected := "The candidate go expert designed ai agents."
	if deobf != expected {
		t.Errorf("deobfuscation of short username placeholder failed: got %q, want %q", deobf, expected)
	}
}

func TestSession_PhoneBoundary_AndMetricsProtection(t *testing.T) {
	shortProf := &core.Profile{
		FullName: "Metric Master",
		Phone:    "+1",
	}
	shortSession := sanitizer.NewSession(shortProf)
	metricText := "Managed +15 engineers and increased revenue by +100% with +1.5M ARR."
	if obf := shortSession.ObfuscateString(metricText); obf != metricText {
		t.Errorf("short phone '+1' corrupted metrics: got %q, want %q", obf, metricText)
	}

	fullProf := &core.Profile{
		FullName: "Alice Walker",
		Phone:    "+1 555 432 1098",
	}
	fullSession := sanitizer.NewSession(fullProf)

	statement := "Contact +1 555 432 1098 or email. Increased revenue by +100% and led +15 engineers."
	obfStatement := fullSession.ObfuscateString(statement)

	if strings.Contains(obfStatement, "+1 555 432 1098") {
		t.Errorf("statement leaked authentic phone: %s", obfStatement)
	}
	if !strings.Contains(obfStatement, sanitizer.PlaceholderPhone) {
		t.Errorf("statement missing PlaceholderPhone: %s", obfStatement)
	}
	if !strings.Contains(obfStatement, "Contact +1 111 111 1111 or email.") {
		t.Errorf("whitespace or delimiter corrupted around phone: %s", obfStatement)
	}
	if !strings.Contains(obfStatement, "+100%") || !strings.Contains(obfStatement, "+15 engineers") {
		t.Errorf("metrics were corrupted by phone regex: %s", obfStatement)
	}

	deobfStatement := fullSession.DeobfuscateString(obfStatement)
	if deobfStatement != statement {
		t.Errorf("deobfuscation of phone statement failed: got %q, want %q", deobfStatement, statement)
	}

	parenProf := &core.Profile{
		FullName: "Bob Smith",
		Phone:    "(555) 432-1098",
	}
	parenSession := sanitizer.NewSession(parenProf)
	parenText := "Call (555) 432-1098 today."
	obfParen := parenSession.ObfuscateString(parenText)
	if strings.Contains(obfParen, "(555) 432-1098") {
		t.Errorf("paren phone leaked: %s", obfParen)
	}
	if !strings.Contains(obfParen, "Call +1 111 111 1111 today.") {
		t.Errorf("paren phone masking failed: %s", obfParen)
	}
}

func TestSession_InvalidURLs_DoNotPolluteOrCorrupt(t *testing.T) {
	prof := &core.Profile{
		FullName: "URL Candidate",
		Metadata: map[string]any{
			"website": "/",
		},
		Profiles: []core.SocialLink{
			{Network: "Short", URL: "co"},
			{Network: "Incomplete", URL: "http://"},
		},
	}

	session := sanitizer.NewSession(prof)
	text := "Collaborated with co-workers on /dev/null and visited http://example.com/path."
	obf := session.ObfuscateString(text)
	if obf != text {
		t.Errorf("invalid short URLs corrupted text: got %q, want %q", obf, text)
	}
}
