package privacy_test

import (
	"strings"
	"testing"
	"time"

	"resumeee/internal/domain"
	"resumeee/internal/privacy"
)

func sampleCandidateProfile() domain.Profile {
	return domain.Profile{
		ID:       "prof-123",
		FullName: "Alice Walker",
		Email:    "alice.walker@example.org",
		Phone:    "+1 (555) 234-5678",
		Headline: "Staff Cloud Engineer (Alice Walker)",
		Summary:  "Alice Walker is an experienced systems architect. Reach me at alice.walker@example.org or +1 (555) 234-5678. Portfolio: https://alicewalker.dev.",
		Profiles: []domain.SocialLink{
			{Network: "LinkedIn", Username: "alicewalker", URL: "https://linkedin.com/in/alicewalker"},
			{Network: "GitHub", Username: "alicewalker-dev", URL: "https://github.com/alicewalker-dev"},
		},
		Metadata: map[string]any{
			"website": "https://alicewalker.dev",
		},
	}
}

func sampleCandidateSnapshot() *domain.MasterProfileSnapshot {
	prof := sampleCandidateProfile()
	endDate := domain.DatePtr(domain.NewDate(2023, 12, 31))
	return &domain.MasterProfileSnapshot{
		Profile: prof,
		Experiences: []domain.WorkExperience{
			{
				ID:                "exp-1",
				Company:           "Acme Cloud",
				RoleTitle:         "Lead Engineer",
				StartDate:         domain.NewDate(2021, 1, 1),
				EndDate:           endDate,
				DutiesSummary:     "Alice Walker led distributed cache migration.",
				SupervisorName:    "Bob Boss",
				SupervisorContact: "alice.walker@example.org", // Candidate's email placed in verification field
				Facts: []domain.Fact{
					{
						ID:        "fact-1",
						Statement: "Alice Walker engineered Raft consensus layer saving $1M annually.",
						Impact:    "Contact alice.walker@example.org for verification.",
					},
				},
			},
		},
		Projects: []domain.Project{
			{
				ID:      "proj-1",
				Name:    "Raft-Go",
				Role:    "Creator",
				URL:     "https://github.com/alicewalker-dev/raft-go",
				Summary: "Authored by Alice Walker (alicewalker-dev).",
			},
		},
		Education: []domain.Education{
			{
				ID:          "edu-1",
				Institution: "MIT",
				Degree:      "M.S.",
				Field:       "CS",
				Facts: []domain.Fact{
					{
						ID:        "fact-edu-1",
						Statement: "Alice Walker authored thesis on Paxos variants.",
					},
				},
			},
		},
		Certifications: []domain.Certification{
			{
				ID:      "cert-1",
				Name:    "CKA",
				Issuer:  "CNCF",
				Summary: "Issued to Alice Walker.",
			},
		},
		Facts: []domain.Fact{
			{
				ID:        "fact-root-1",
				Statement: "Alice Walker holds 3 patents.",
			},
		},
		ExportedAt: time.Now(),
	}
}

func TestSession_ObfuscateSnapshot_Basics(t *testing.T) {
	snap := sampleCandidateSnapshot()
	session := privacy.NewSession(&snap.Profile)

	obf := session.ObfuscateSnapshot(snap)

	// 1. Profile contact endpoints are masked
	if obf.Profile.FullName != privacy.PlaceholderFullName {
		t.Errorf("expected FullName %q, got %q", privacy.PlaceholderFullName, obf.Profile.FullName)
	}
	if obf.Profile.Email != privacy.PlaceholderEmail {
		t.Errorf("expected Email %q, got %q", privacy.PlaceholderEmail, obf.Profile.Email)
	}
	if obf.Profile.Phone != privacy.PlaceholderPhone {
		t.Errorf("expected Phone %q, got %q", privacy.PlaceholderPhone, obf.Profile.Phone)
	}

	// 2. Profiles/Social links are deterministically masked
	if len(obf.Profile.Profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(obf.Profile.Profiles))
	}
	if obf.Profile.Profiles[0].URL != "https://social.example.com/profile-1" {
		t.Errorf("expected profile 0 URL placeholder, got %q", obf.Profile.Profiles[0].URL)
	}
	if obf.Profile.Profiles[0].Username != "candidate-social-1" {
		t.Errorf("expected profile 0 Username placeholder, got %q", obf.Profile.Profiles[0].Username)
	}
	if obf.Profile.Profiles[1].URL != "https://social.example.com/profile-2" {
		t.Errorf("expected profile 1 URL placeholder, got %q", obf.Profile.Profiles[1].URL)
	}
	if obf.Profile.Profiles[1].Username != "candidate-social-2" {
		t.Errorf("expected profile 1 Username placeholder, got %q", obf.Profile.Profiles[1].Username)
	}

	// 3. Metadata website is masked
	if obf.Profile.Metadata["website"] != privacy.PlaceholderWebsite {
		t.Errorf("expected metadata website placeholder, got %v", obf.Profile.Metadata["website"])
	}

	// 4. Invariant: Original snapshot must NOT be mutated in-place
	if snap.Profile.FullName != "Alice Walker" {
		t.Errorf("original snapshot FullName was mutated: %q", snap.Profile.FullName)
	}
	if snap.Profile.Email != "alice.walker@example.org" {
		t.Errorf("original snapshot Email was mutated: %q", snap.Profile.Email)
	}
}

func TestSession_TextScrubbingAcrossSnapshot(t *testing.T) {
	snap := sampleCandidateSnapshot()
	session := privacy.NewSession(&snap.Profile)

	obf := session.ObfuscateSnapshot(snap)

	// Verify no real PII exists in any serialized or nested text field
	assertNoPII := func(t *testing.T, fieldName, text string) {
		t.Helper()
		if strings.Contains(text, "Alice Walker") {
			t.Errorf("field %s leaked candidate FullName: %s", fieldName, text)
		}
		if strings.Contains(text, "alice.walker@example.org") {
			t.Errorf("field %s leaked candidate Email: %s", fieldName, text)
		}
		if strings.Contains(text, "+1 (555) 234-5678") {
			t.Errorf("field %s leaked candidate Phone: %s", fieldName, text)
		}
		if strings.Contains(text, "https://alicewalker.dev") {
			t.Errorf("field %s leaked candidate Website: %s", fieldName, text)
		}
		if strings.Contains(text, "https://github.com/alicewalker-dev") {
			t.Errorf("field %s leaked candidate GitHub URL: %s", fieldName, text)
		}
		if strings.Contains(text, "https://linkedin.com/in/alicewalker") {
			t.Errorf("field %s leaked candidate LinkedIn URL: %s", fieldName, text)
		}
	}

	assertNoPII(t, "Profile.Summary", obf.Profile.Summary)
	assertNoPII(t, "Profile.Headline", obf.Profile.Headline)
	assertNoPII(t, "Experience.DutiesSummary", obf.Experiences[0].DutiesSummary)
	assertNoPII(t, "Experience.SupervisorContact", obf.Experiences[0].SupervisorContact)
	assertNoPII(t, "Fact.Statement", obf.Experiences[0].Facts[0].Statement)
	assertNoPII(t, "Fact.Impact", obf.Experiences[0].Facts[0].Impact)
	assertNoPII(t, "Project.Summary", obf.Projects[0].Summary)
	assertNoPII(t, "Project.URL", obf.Projects[0].URL)
	assertNoPII(t, "Education.Fact", obf.Education[0].Facts[0].Statement)
	assertNoPII(t, "Certifications.Summary", obf.Certifications[0].Summary)
	assertNoPII(t, "Root.Fact", obf.Facts[0].Statement)

	// Verify placeholders were correctly inserted into text
	if !strings.Contains(obf.Profile.Summary, privacy.PlaceholderFullName) {
		t.Errorf("expected Profile.Summary to contain %q", privacy.PlaceholderFullName)
	}
	if !strings.Contains(obf.Profile.Summary, privacy.PlaceholderEmail) {
		t.Errorf("expected Profile.Summary to contain %q", privacy.PlaceholderEmail)
	}
	if !strings.Contains(obf.Experiences[0].Facts[0].Statement, privacy.PlaceholderFullName) {
		t.Errorf("expected Fact.Statement to contain %q", privacy.PlaceholderFullName)
	}
}

func TestSession_ObfuscateRequest(t *testing.T) {
	snap := sampleCandidateSnapshot()
	session := privacy.NewSession(&snap.Profile)

	req := domain.GenerationRequest{
		GlobalRules:        "Always highlight Alice Walker's leadership at alice.walker@example.org.",
		PresetInstructions: "Format concise bullets for Alice Walker.",
		MasterProfile:      snap,
		BranchContext: domain.Branch{
			TargetRequirements: "Looking for an engineer like Alice Walker (contact: alice.walker@example.org).",
			BranchInstructions: "Emphasize Alice Walker's cloud background.",
		},
		PriorVersion: &domain.TailoredContent{
			Summary: "Prior version summary for Alice Walker.",
			Experiences: []domain.TailoredExperience{
				{
					SourceEntityID: "exp-1",
					Company:        "Acme",
					Bullets: []domain.TailoredBullet{
						{Text: "Alice Walker scaled infrastructure by 300%."},
					},
				},
			},
		},
	}

	obfReq := session.ObfuscateRequest(req)

	if strings.Contains(obfReq.GlobalRules, "Alice Walker") || strings.Contains(obfReq.GlobalRules, "alice.walker@example.org") {
		t.Errorf("leaked PII in GlobalRules: %s", obfReq.GlobalRules)
	}
	if strings.Contains(obfReq.PresetInstructions, "Alice Walker") {
		t.Errorf("leaked PII in PresetInstructions: %s", obfReq.PresetInstructions)
	}
	if strings.Contains(obfReq.BranchContext.TargetRequirements, "Alice Walker") {
		t.Errorf("leaked PII in TargetRequirements: %s", obfReq.BranchContext.TargetRequirements)
	}
	if strings.Contains(obfReq.BranchContext.BranchInstructions, "Alice Walker") {
		t.Errorf("leaked PII in BranchInstructions: %s", obfReq.BranchContext.BranchInstructions)
	}
	if strings.Contains(obfReq.PriorVersion.Summary, "Alice Walker") {
		t.Errorf("leaked PII in PriorVersion.Summary: %s", obfReq.PriorVersion.Summary)
	}
	if strings.Contains(obfReq.PriorVersion.Experiences[0].Bullets[0].Text, "Alice Walker") {
		t.Errorf("leaked PII in PriorVersion bullet: %s", obfReq.PriorVersion.Experiences[0].Bullets[0].Text)
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
	session := privacy.NewSession(&snap.Profile)

	// Synthetic LLM output echoing placeholders
	tailored := domain.TailoredContent{
		Summary: "Results-driven cloud architect " + privacy.PlaceholderFullName + " with expertise in Kubernetes. Contact: " + privacy.PlaceholderEmail + " or " + privacy.PlaceholderPhone + ". See " + privacy.PlaceholderWebsite + " and " + "https://social.example.com/profile-1" + ".",
		SkillGroups: []domain.TailoredSkillGroup{
			{Category: "Cloud", Skills: []string{"Go", "Kubernetes"}},
		},
		Experiences: []domain.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Acme",
				RoleTitle:      "Staff Architect (" + privacy.PlaceholderFullName + ")",
				DutiesSummary:  "Managed team; contact at " + privacy.PlaceholderEmail,
				Bullets: []domain.TailoredBullet{
					{Text: privacy.PlaceholderFullName + " designed zero-trust networking reducing attack surface."},
					{Text: "Direct inquiries to " + privacy.PlaceholderEmail + " or candidate profile candidate-social-1."},
				},
			},
		},
		Projects: []domain.TailoredProject{
			{
				SourceEntityID: "proj-1",
				Name:           "Raft-Go",
				URL:            "https://social.example.com/profile-2",
				Summary:        "Maintained by " + privacy.PlaceholderFullName + " (candidate-social-2).",
			},
		},
	}

	gapReport := domain.GapReport{
		OverallMatchSummary: privacy.PlaceholderFullName + " strongly matches 90% of requirements.",
		StrategicRecommendations: []string{
			privacy.PlaceholderFullName + " should highlight enterprise scaling.",
			"Add portfolio link " + privacy.PlaceholderWebsite,
		},
		MissingRequirements: []domain.GapItem{
			{
				Requirement:       "Experience with AWS",
				MissingArea:       "Cloud Provider",
				ClarifyingPrompt:  "Does " + privacy.PlaceholderFullName + " have AWS production experience?",
				RecommendedAction: "Clarify with " + privacy.PlaceholderFullName,
			},
		},
	}

	genResp := domain.GenerationResponse{
		TailoredContent: tailored,
		GapReport:       gapReport,
	}

	session.DeobfuscateGenerationResponse(&genResp)

	// 1. Verify TailoredContent deobfuscation
	if strings.Contains(genResp.TailoredContent.Summary, privacy.PlaceholderFullName) ||
		!strings.Contains(genResp.TailoredContent.Summary, "Alice Walker") {
		t.Errorf("Summary failed to deobfuscate FullName: %s", genResp.TailoredContent.Summary)
	}
	if strings.Contains(genResp.TailoredContent.Summary, privacy.PlaceholderEmail) ||
		!strings.Contains(genResp.TailoredContent.Summary, "alice.walker@example.org") {
		t.Errorf("Summary failed to deobfuscate Email: %s", genResp.TailoredContent.Summary)
	}
	if strings.Contains(genResp.TailoredContent.Summary, privacy.PlaceholderPhone) ||
		!strings.Contains(genResp.TailoredContent.Summary, "+1 (555) 234-5678") {
		t.Errorf("Summary failed to deobfuscate Phone: %s", genResp.TailoredContent.Summary)
	}
	if strings.Contains(genResp.TailoredContent.Summary, privacy.PlaceholderWebsite) ||
		!strings.Contains(genResp.TailoredContent.Summary, "https://alicewalker.dev") {
		t.Errorf("Summary failed to deobfuscate Website: %s", genResp.TailoredContent.Summary)
	}
	if strings.Contains(genResp.TailoredContent.Summary, "https://social.example.com/profile-1") ||
		!strings.Contains(genResp.TailoredContent.Summary, "https://linkedin.com/in/alicewalker") {
		t.Errorf("Summary failed to deobfuscate LinkedIn URL: %s", genResp.TailoredContent.Summary)
	}

	// Bullet deobfuscation
	bullet0 := genResp.TailoredContent.Experiences[0].Bullets[0].Text
	if !strings.HasPrefix(bullet0, "Alice Walker designed") {
		t.Errorf("bullet 0 failed to deobfuscate: %s", bullet0)
	}
	bullet1 := genResp.TailoredContent.Experiences[0].Bullets[1].Text
	if !strings.Contains(bullet1, "alice.walker@example.org") || !strings.Contains(bullet1, "alicewalker") {
		t.Errorf("bullet 1 failed to deobfuscate email and username: %s", bullet1)
	}

	// Project URL & Summary
	proj := genResp.TailoredContent.Projects[0]
	if proj.URL != "https://github.com/alicewalker-dev" {
		t.Errorf("project URL failed to deobfuscate: %s", proj.URL)
	}
	if !strings.Contains(proj.Summary, "Alice Walker (alicewalker-dev)") {
		t.Errorf("project summary failed to deobfuscate: %s", proj.Summary)
	}

	// 2. Verify GapReport deobfuscation
	if !strings.Contains(genResp.GapReport.OverallMatchSummary, "Alice Walker strongly matches") {
		t.Errorf("GapReport OverallMatchSummary failed to deobfuscate: %s", genResp.GapReport.OverallMatchSummary)
	}
	if !strings.Contains(genResp.GapReport.StrategicRecommendations[0], "Alice Walker should highlight") {
		t.Errorf("GapReport StrategicRecommendations failed to deobfuscate: %s", genResp.GapReport.StrategicRecommendations[0])
	}
	if !strings.Contains(genResp.GapReport.StrategicRecommendations[1], "https://alicewalker.dev") {
		t.Errorf("GapReport StrategicRecommendations failed to deobfuscate website: %s", genResp.GapReport.StrategicRecommendations[1])
	}
	if !strings.Contains(genResp.GapReport.MissingRequirements[0].ClarifyingPrompt, "Does Alice Walker have") {
		t.Errorf("GapReport MissingRequirements ClarifyingPrompt failed to deobfuscate: %s", genResp.GapReport.MissingRequirements[0].ClarifyingPrompt)
	}
	if !strings.Contains(genResp.GapReport.MissingRequirements[0].RecommendedAction, "Clarify with Alice Walker") {
		t.Errorf("GapReport MissingRequirements RecommendedAction failed to deobfuscate: %s", genResp.GapReport.MissingRequirements[0].RecommendedAction)
	}
}

func TestSession_IdempotenceAndEmptySafety(t *testing.T) {
	// 1. Empty profile should not panic or corrupt
	emptySession := privacy.NewSession(&domain.Profile{})
	raw := "Hello world"
	if emptySession.ObfuscateString(raw) != raw {
		t.Errorf("empty session corrupted raw string")
	}
	if emptySession.DeobfuscateString(raw) != raw {
		t.Errorf("empty session corrupted raw string in deobfuscate")
	}

	// 2. Nil request / response safety
	var nilResp *domain.GenerationResponse
	emptySession.DeobfuscateGenerationResponse(nilResp) // must not panic

	var nilContent *domain.TailoredContent
	emptySession.DeobfuscateTailoredContent(nilContent) // must not panic

	var nilReport *domain.GapReport
	emptySession.DeobfuscateGapReport(nilReport) // must not panic

	// 3. Repeated deobfuscation is idempotent
	snap := sampleCandidateSnapshot()
	session := privacy.NewSession(&snap.Profile)

	text := "Hello " + privacy.PlaceholderFullName
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
	prof := &domain.Profile{
		FullName: "Test Candidate",
		Email:    "test@example.com",
		Profiles: []domain.SocialLink{
			{Network: "GitHub", Username: "go", URL: "https://github.com/go"},
			{Network: "Twitter", Username: "ai", URL: "https://twitter.com/ai"},
			{Network: "Custom", Username: "c", URL: "https://custom.com/c"},
		},
	}

	session := privacy.NewSession(prof)
	snap := &domain.MasterProfileSnapshot{Profile: *prof}
	obf := session.ObfuscateSnapshot(snap)

	// 1. Structured usernames are masked to synthetic placeholders
	if obf.Profile.Profiles[0].Username != "candidate-social-1" {
		t.Errorf("expected Profiles[0].Username candidate-social-1, got %s", obf.Profile.Profiles[0].Username)
	}
	if obf.Profile.Profiles[1].Username != "candidate-social-2" {
		t.Errorf("expected Profiles[1].Username candidate-social-2, got %s", obf.Profile.Profiles[1].Username)
	}
	if obf.Profile.Profiles[2].Username != "candidate-social-3" {
		t.Errorf("expected Profiles[2].Username candidate-social-3, got %s", obf.Profile.Profiles[2].Username)
	}

	// 2. Freeform text with technical terms "Go", "AI", "C" is NOT corrupted
	text := "Senior Go engineer with 5 years Go experience and AI expertise. Wrote C drivers for real-time systems."
	obfText := session.ObfuscateString(text)
	if obfText != text {
		t.Errorf("short username corrupted technical terms: got %q, want %q", obfText, text)
	}

	// 3. Deobfuscation restores synthetic placeholder if echoed
	echoed := "The candidate candidate-social-1 expert designed candidate-social-2 agents."
	deobf := session.DeobfuscateString(echoed)
	expected := "The candidate go expert designed ai agents."
	if deobf != expected {
		t.Errorf("deobfuscation of short username placeholder failed: got %q, want %q", deobf, expected)
	}
}

func TestSession_PhoneBoundary_AndMetricsProtection(t *testing.T) {
	// Case A: Partial/short phone like "+1" (substantive digits < 7)
	shortProf := &domain.Profile{
		FullName: "Metric Master",
		Phone:    "+1",
	}
	shortSession := privacy.NewSession(shortProf)
	metricText := "Managed +15 engineers and increased revenue by +100% with +1.5M ARR."
	if obf := shortSession.ObfuscateString(metricText); obf != metricText {
		t.Errorf("short phone '+1' corrupted metrics: got %q, want %q", obf, metricText)
	}

	// Case B: Full phone number starting with '+'
	fullProf := &domain.Profile{
		FullName: "Alice Walker",
		Phone:    "+1 555 432 1098",
	}
	fullSession := privacy.NewSession(fullProf)

	statement := "Contact +1 555 432 1098 or email. Increased revenue by +100% and led +15 engineers."
	obfStatement := fullSession.ObfuscateString(statement)

	// Phone must be replaced
	if strings.Contains(obfStatement, "+1 555 432 1098") {
		t.Errorf("statement leaked authentic phone: %s", obfStatement)
	}
	if !strings.Contains(obfStatement, privacy.PlaceholderPhone) {
		t.Errorf("statement missing PlaceholderPhone: %s", obfStatement)
	}
	// Whitespace before phone and metrics must be intact
	if !strings.Contains(obfStatement, "Contact +1 111 111 1111 or email.") {
		t.Errorf("whitespace or delimiter corrupted around phone: %s", obfStatement)
	}
	if !strings.Contains(obfStatement, "+100%") || !strings.Contains(obfStatement, "+15 engineers") {
		t.Errorf("metrics were corrupted by phone regex: %s", obfStatement)
	}

	// Deobfuscation restores authentic phone
	deobfStatement := fullSession.DeobfuscateString(obfStatement)
	if deobfStatement != statement {
		t.Errorf("deobfuscation of phone statement failed: got %q, want %q", deobfStatement, statement)
	}

	// Case C: Phone with parentheses like (555) 432-1098
	parenProf := &domain.Profile{
		FullName: "Bob Smith",
		Phone:    "(555) 432-1098",
	}
	parenSession := privacy.NewSession(parenProf)
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
	prof := &domain.Profile{
		FullName: "URL Candidate",
		Metadata: map[string]any{
			"website": "/",
		},
		Profiles: []domain.SocialLink{
			{Network: "Short", URL: "co"},
			{Network: "Incomplete", URL: "http://"},
		},
	}

	session := privacy.NewSession(prof)
	text := "Collaborated with co-workers on /dev/null and visited http://example.com/path."
	obf := session.ObfuscateString(text)
	if obf != text {
		t.Errorf("invalid short URLs corrupted text: got %q, want %q", obf, text)
	}
}
