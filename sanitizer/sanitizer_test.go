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
	hours := 40
	return &core.MainProfileSnapshot{
		Profile: prof,
		Skills: []core.Skill{
			{ID: "sk-1", Name: "Go", Category: "Languages"},
		},
		Experiences: []core.WorkExperience{
			{
				ID:                "exp-1",
				Company:           "Alice Walker Consulting",
				RoleTitle:         "Lead Engineer (Alice Walker)",
				StartDate:         core.NewDate(2021, 1, 1),
				EndDate:           endDate,
				HoursPerWeek:      &hours,
				DutiesSummary:     "Alice Walker led distributed cache migration.",
				SupervisorName:    "Alice Walker Supervisor",
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
						Statement: "Alice Walker published benchmarks at https://alicewalker.dev.",
						Impact:    "Adopted by 50 teams; maintainer alicewalker-dev.",
					},
				},
			},
		},
		Education: []core.Education{
			{
				ID:          "edu-1",
				Institution: "MIT (Alice Walker Lab)",
				Degree:      "M.S. (Alice Walker Fellow)",
				Field:       "CS - Alice Walker Track",
				Facts: []core.Fact{
					{
						ID:        "fact-edu-1",
						Statement: "Alice Walker authored thesis on Paxos variants.",
						Impact:    "Published at https://alicewalker.dev/thesis.",
					},
				},
			},
		},
		Certifications: []core.Certification{
			{
				ID:      "cert-1",
				Name:    "CKA (Alice Walker)",
				Issuer:  "CNCF / alicewalker",
				URL:     "https://alicewalker.dev/cka",
				Summary: "Issued to Alice Walker.",
			},
		},
		Facts: []core.Fact{
			{
				ID:        "fact-root-1",
				Statement: "Alice Walker holds 3 patents.",
				Impact:    "Correspondence: alice.walker@example.org.",
			},
		},
		ExportedAt: time.Now(),
	}
}

func TestSession_ObfuscateSnapshot_NonLLMFieldsUntouched(t *testing.T) {
	snap := sampleCandidateSnapshot()
	session := sanitizer.NewSession(&snap.Profile)

	obf := session.ObfuscateSnapshot(snap)

	nonLLMChecks := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "Profile.FullName stripped by LeanProfile",
			got:  obf.Profile.FullName,
			want: "Alice Walker",
		},
		{
			name: "Profile.Email stripped by LeanProfile",
			got:  obf.Profile.Email,
			want: "alice.walker@example.org",
		},
		{
			name: "Profile.Phone stripped by LeanProfile",
			got:  obf.Profile.Phone,
			want: "+1 (555) 234-5678",
		},
		{
			name: "Profile.Profiles[0].URL stripped by LeanProfile",
			got:  obf.Profile.Profiles[0].URL,
			want: "https://linkedin.com/in/alicewalker",
		},
		{
			name: "Profile.Profiles[0].Username stripped by LeanProfile",
			got:  obf.Profile.Profiles[0].Username,
			want: "alicewalker",
		},
		{
			name: "Profile.Profiles[1].URL stripped by LeanProfile",
			got:  obf.Profile.Profiles[1].URL,
			want: "https://github.com/alicewalker-dev",
		},
		{
			name: "Profile.Profiles[1].Username stripped by LeanProfile",
			got:  obf.Profile.Profiles[1].Username,
			want: "alicewalker-dev",
		},
		{
			name: "Profile.Metadata[website] stripped by LeanProfile",
			got:  obf.Profile.Metadata["website"].(string),
			want: "https://alicewalker.dev",
		},
		{
			name: "Experience.SupervisorName stripped by LeanExperience",
			got:  obf.Experiences[0].SupervisorName,
			want: "Alice Walker Supervisor",
		},
		{
			name: "Experience.SupervisorContact stripped by LeanExperience",
			got:  obf.Experiences[0].SupervisorContact,
			want: "alice.walker@example.org",
		},
		{
			name: "Experience.CompanyContact.Value stripped by LeanExperience",
			got:  obf.Experiences[0].CompanyContact.Value,
			want: "alice.walker@example.org",
		},
		{
			name: "Education.Institution structured details untouched",
			got:  obf.Education[0].Institution,
			want: "MIT (Alice Walker Lab)",
		},
		{
			name: "Education.Degree structured details untouched",
			got:  obf.Education[0].Degree,
			want: "M.S. (Alice Walker Fellow)",
		},
		{
			name: "Education.Field structured details untouched",
			got:  obf.Education[0].Field,
			want: "CS - Alice Walker Track",
		},
		{
			name: "Certification.Name structured details untouched",
			got:  obf.Certifications[0].Name,
			want: "CKA (Alice Walker)",
		},
		{
			name: "Certification.Issuer structured details untouched",
			got:  obf.Certifications[0].Issuer,
			want: "CNCF / alicewalker",
		},
	}

	for _, tc := range nonLLMChecks {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("expected non-obfuscated field %s to remain untouched %q, got %q", tc.name, tc.want, tc.got)
			}
		})
	}

	if obf.Skills[0].Name != "Go" {
		t.Errorf("expected Skill.Name untouched, got %q", obf.Skills[0].Name)
	}

	// Verify caller's original snapshot LLM-bound fields were NOT mutated in-place
	if !strings.Contains(snap.Profile.Summary, "Alice Walker") {
		t.Errorf("original snapshot Profile.Summary was mutated: %q", snap.Profile.Summary)
	}
	if !strings.Contains(snap.Experiences[0].Facts[0].Statement, "Alice Walker") {
		t.Errorf("original snapshot Experience.Fact.Statement was mutated: %q", snap.Experiences[0].Facts[0].Statement)
	}
	if !strings.Contains(snap.Projects[0].Facts[0].Statement, "Alice Walker") {
		t.Errorf("original snapshot Project.Fact.Statement was mutated: %q", snap.Projects[0].Facts[0].Statement)
	}
	if !strings.Contains(snap.Education[0].Facts[0].Statement, "Alice Walker") {
		t.Errorf("original snapshot Education.Fact.Statement was mutated: %q", snap.Education[0].Facts[0].Statement)
	}
	if !strings.Contains(snap.Facts[0].Statement, "Alice Walker") {
		t.Errorf("original snapshot root Fact.Statement was mutated: %q", snap.Facts[0].Statement)
	}
	if snap.Experiences[0].Company != "Alice Walker Consulting" {
		t.Errorf("original snapshot Experience.Company was mutated: %q", snap.Experiences[0].Company)
	}
	if snap.Certifications[0].URL != "https://alicewalker.dev/cka" {
		t.Errorf("original snapshot Certification.URL was mutated: %q", snap.Certifications[0].URL)
	}
	if snap.Certifications[0].Summary != "Issued to Alice Walker." {
		t.Errorf("original snapshot Certification.Summary was mutated: %q", snap.Certifications[0].Summary)
	}
}

func TestSession_ObfuscateSnapshot_LLMBoundFieldsScrubbed(t *testing.T) {
	snap := sampleCandidateSnapshot()
	session := sanitizer.NewSession(&snap.Profile)

	obf := session.ObfuscateSnapshot(snap)

	forbiddenPII := []string{
		"Alice Walker",
		"alice.walker@example.org",
		"+1 (555) 234-5678",
		"https://alicewalker.dev",
		"https://github.com/alicewalker-dev",
		"https://linkedin.com/in/alicewalker",
		"alicewalker-dev",
		"alicewalker",
	}

	llmFields := []struct {
		name string
		val  string
		want string
	}{
		{"Profile.Headline", obf.Profile.Headline, sanitizer.PlaceholderFullName},
		{"Profile.Summary", obf.Profile.Summary, sanitizer.PlaceholderEmail},
		{"Experience.Company", obf.Experiences[0].Company, sanitizer.PlaceholderFullName + " Consulting"},
		{"Experience.RoleTitle", obf.Experiences[0].RoleTitle, sanitizer.PlaceholderFullName},
		{"Experience.DutiesSummary", obf.Experiences[0].DutiesSummary, sanitizer.PlaceholderFullName},
		{"Experience.Fact.Statement", obf.Experiences[0].Facts[0].Statement, sanitizer.PlaceholderFullName},
		{"Experience.Fact.Impact", obf.Experiences[0].Facts[0].Impact, sanitizer.PlaceholderEmail},
		{"Project.Name", obf.Projects[0].Name, sanitizer.PlaceholderFullName},
		{"Project.Role", obf.Projects[0].Role, sanitizer.PlaceholderFullName},
		{"Project.URL", obf.Projects[0].URL, sanitizer.PlaceholderSocialURLPrefix + "2/raft-go"},
		{"Project.Summary", obf.Projects[0].Summary, sanitizer.PlaceholderSocialUserPrefix + "2"},
		{"Project.Fact.Statement", obf.Projects[0].Facts[0].Statement, sanitizer.PlaceholderWebsite},
		{"Project.Fact.Impact", obf.Projects[0].Facts[0].Impact, sanitizer.PlaceholderSocialUserPrefix + "2"},
		{"Education.Fact.Statement", obf.Education[0].Facts[0].Statement, sanitizer.PlaceholderFullName},
		{"Education.Fact.Impact", obf.Education[0].Facts[0].Impact, sanitizer.PlaceholderWebsite},
		{"Certification.URL", obf.Certifications[0].URL, sanitizer.PlaceholderWebsite + "/cka"},
		{"Certification.Summary", obf.Certifications[0].Summary, sanitizer.PlaceholderFullName},
		{"Root.Fact.Statement", obf.Facts[0].Statement, sanitizer.PlaceholderFullName},
		{"Root.Fact.Impact", obf.Facts[0].Impact, sanitizer.PlaceholderEmail},
	}

	for _, tc := range llmFields {
		t.Run(tc.name, func(t *testing.T) {
			for _, pii := range forbiddenPII {
				if strings.Contains(tc.val, pii) {
					t.Errorf("LLM-bound field %s leaked PII %q in %q", tc.name, pii, tc.val)
				}
			}
			if !strings.Contains(tc.val, tc.want) {
				t.Errorf("LLM-bound field %s expected placeholder %q in %q", tc.name, tc.want, tc.val)
			}
		})
	}
}

func TestSession_ObfuscateRequest_ScrubsProjectedAndSkipsNonLLMFields(t *testing.T) {
	snap := sampleCandidateSnapshot()
	session := sanitizer.NewSessionFromSnapshot(snap)

	req := core.GenerationRequest{
		GlobalRules:        "Always highlight Alice Walker's leadership at alice.walker@example.org.",
		PresetInstructions: "Format concise bullets for Alice Walker.",
		MainProfile:        snap,
		BranchContext: core.Branch{
			TargetRole:         "Principal Architect for Alice Walker",
			TargetCompany:      "Alice Walker Ventures",
			TargetRequirements: "Looking for an engineer like Alice Walker (contact: alice.walker@example.org).",
			BranchInstructions: "Emphasize Alice Walker's cloud background.",
			CustomDirectives:   "Unused alias mentioning Alice Walker.",
		},
		PriorVersion: &core.TailoredContent{
			Summary: "Prior version summary for Alice Walker.",
			SkillGroups: []core.TailoredSkillGroup{
				{Category: "Backend", Skills: []string{"Go"}, SourceSkillIDs: []string{"sk-1"}},
			},
			Experiences: []core.TailoredExperience{
				{
					SourceEntityID: "exp-1",
					Company:        "Alice Walker Consulting",
					RoleTitle:      "Lead (Alice Walker)",
					DutiesSummary:  "Alice Walker oversaw platform.",
					Bullets: []core.TailoredBullet{
						{Text: "Alice Walker scaled infrastructure by 300%.", SourceFactIDs: []string{"fact-1"}},
					},
				},
			},
			Projects: []core.TailoredProject{
				{
					SourceEntityID: "proj-1",
					Name:           "Raft-Go (Alice Walker)",
					Role:           "Maintainer (Alice Walker)",
					URL:            "https://github.com/alicewalker-dev/raft-go",
					Summary:        "Built by Alice Walker.",
					Bullets: []core.TailoredBullet{
						{Text: "Alice Walker cut consensus tail latency."},
					},
				},
			},
			Education: []core.TailoredEducation{
				{
					SourceEntityID: "edu-1",
					Institution:    "MIT (Alice Walker)",
					Degree:         "M.S. (Alice Walker)",
					Field:          "Systems (Alice Walker)",
					Bullets: []core.TailoredBullet{
						{Text: "Alice Walker graduate research."},
					},
				},
			},
			Certifications: []core.TailoredCert{
				{
					SourceEntityID: "cert-1",
					Name:           "CKA - Alice Walker",
					Issuer:         "CNCF (alicewalker)",
					URL:            "https://alicewalker.dev/cka",
					Summary:        "Non-projected cert summary for Alice Walker.",
				},
			},
		},
	}

	obfReq := session.ObfuscateRequest(req)

	scrubbedChecks := []struct {
		name string
		val  string
	}{
		{"GlobalRules", obfReq.GlobalRules},
		{"PresetInstructions", obfReq.PresetInstructions},
		{"BranchContext.TargetRole", obfReq.BranchContext.TargetRole},
		{"BranchContext.TargetRequirements", obfReq.BranchContext.TargetRequirements},
		{"BranchContext.BranchInstructions", obfReq.BranchContext.BranchInstructions},
		{"PriorVersion.Summary", obfReq.PriorVersion.Summary},
		{"PriorVersion.Experience.Company", obfReq.PriorVersion.Experiences[0].Company},
		{"PriorVersion.Experience.RoleTitle", obfReq.PriorVersion.Experiences[0].RoleTitle},
		{"PriorVersion.Experience.DutiesSummary", obfReq.PriorVersion.Experiences[0].DutiesSummary},
		{"PriorVersion.Experience.Bullet", obfReq.PriorVersion.Experiences[0].Bullets[0].Text},
		{"PriorVersion.Project.Name", obfReq.PriorVersion.Projects[0].Name},
		{"PriorVersion.Project.Role", obfReq.PriorVersion.Projects[0].Role},
		{"PriorVersion.Project.URL", obfReq.PriorVersion.Projects[0].URL},
		{"PriorVersion.Project.Summary", obfReq.PriorVersion.Projects[0].Summary},
		{"PriorVersion.Project.Bullet", obfReq.PriorVersion.Projects[0].Bullets[0].Text},
		{"PriorVersion.Education.Bullet", obfReq.PriorVersion.Education[0].Bullets[0].Text},
		{"PriorVersion.Certification.URL", obfReq.PriorVersion.Certifications[0].URL},
	}
	for _, tc := range scrubbedChecks {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.val, "Alice Walker") ||
				strings.Contains(tc.val, "alice.walker@example.org") ||
				strings.Contains(tc.val, "https://alicewalker.dev") ||
				strings.Contains(tc.val, "https://github.com/alicewalker-dev") {
				t.Errorf("field %s leaked PII: %q", tc.name, tc.val)
			}
		})
	}

	// Non-obfuscated structured / non-LLM fields on request and prior version must remain untouched
	untouchedChecks := []struct {
		name string
		got  string
		want string
	}{
		{"BranchContext.TargetCompany", obfReq.BranchContext.TargetCompany, "Alice Walker Ventures"},
		{"BranchContext.CustomDirectives", obfReq.BranchContext.CustomDirectives, "Unused alias mentioning Alice Walker."},
		{"PriorVersion.Education.Institution", obfReq.PriorVersion.Education[0].Institution, "MIT (Alice Walker)"},
		{"PriorVersion.Education.Degree", obfReq.PriorVersion.Education[0].Degree, "M.S. (Alice Walker)"},
		{"PriorVersion.Education.Field", obfReq.PriorVersion.Education[0].Field, "Systems (Alice Walker)"},
		{"PriorVersion.Certification.Name", obfReq.PriorVersion.Certifications[0].Name, "CKA - Alice Walker"},
		{"PriorVersion.Certification.Issuer", obfReq.PriorVersion.Certifications[0].Issuer, "CNCF (alicewalker)"},
		{"PriorVersion.Certification.Summary", obfReq.PriorVersion.Certifications[0].Summary, "Non-projected cert summary for Alice Walker."},
	}
	for _, tc := range untouchedChecks {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("expected %s untouched %q, got %q", tc.name, tc.want, tc.got)
			}
		})
	}

	// Verify caller's original request and prior version were NOT mutated
	if !strings.Contains(req.GlobalRules, "Alice Walker") {
		t.Errorf("original req.GlobalRules was mutated")
	}
	if !strings.Contains(req.PriorVersion.Summary, "Alice Walker") {
		t.Errorf("original req.PriorVersion.Summary was mutated")
	}
	if !strings.Contains(req.PriorVersion.Experiences[0].Bullets[0].Text, "Alice Walker") {
		t.Errorf("original req.PriorVersion bullet was mutated")
	}
	if !strings.Contains(req.PriorVersion.Education[0].Bullets[0].Text, "Alice Walker") {
		t.Errorf("original req.PriorVersion education bullet was mutated")
	}
	if req.PriorVersion.Experiences[0].Company != "Alice Walker Consulting" {
		t.Errorf("original req.PriorVersion experience company was mutated: %q", req.PriorVersion.Experiences[0].Company)
	}
	if req.PriorVersion.Certifications[0].URL != "https://alicewalker.dev/cka" {
		t.Errorf("original req.PriorVersion certification URL was mutated: %q", req.PriorVersion.Certifications[0].URL)
	}
}

func TestSession_DeobfuscationRestoresSchemaFieldsAndSkipsNonSchemaFields(t *testing.T) {
	snap := sampleCandidateSnapshot()
	session := sanitizer.NewSessionFromRequest(core.GenerationRequest{MainProfile: snap})

	tailored := core.TailoredContent{
		Summary: "Results-driven cloud architect " + sanitizer.PlaceholderFullName + " (also John Doe) with expertise in Kubernetes. Contact: " + sanitizer.PlaceholderEmail + " or " + sanitizer.PlaceholderPhone + ". See " + sanitizer.PlaceholderWebsite + " and https://social.example.com/profile-1.",
		SkillGroups: []core.TailoredSkillGroup{
			{Category: "Cloud", Skills: []string{"Go", "Kubernetes"}},
		},
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID:    "exp-1",
				Company:           sanitizer.PlaceholderFullName + " Consulting",
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
				Name:           "Raft-Go (" + sanitizer.PlaceholderFullName + ")",
				Role:           "Creator (" + sanitizer.PlaceholderFullName + ")",
				URL:            "https://social.example.com/profile-2",
				Summary:        "Maintained by " + sanitizer.PlaceholderFullName + " (candidate-social-2).",
				Bullets: []core.TailoredBullet{
					{Text: "Published by " + sanitizer.PlaceholderFullName + "."},
				},
			},
		},
		Education: []core.TailoredEducation{
			{
				SourceEntityID: "edu-1",
				Institution:    "MIT (" + sanitizer.PlaceholderFullName + ")",
				Degree:         "M.S. (" + sanitizer.PlaceholderFullName + ")",
				Field:          "CS (" + sanitizer.PlaceholderFullName + ")",
				Bullets: []core.TailoredBullet{
					{Text: "Researched Paxos variants with " + sanitizer.PlaceholderFullName + " at " + sanitizer.PlaceholderWebsite + "."},
				},
			},
		},
		Certifications: []core.TailoredCert{
			{
				SourceEntityID: "cert-1",
				Name:           "CKA (" + sanitizer.PlaceholderFullName + ")",
				Issuer:         "CNCF / candidate-social-1",
				URL:            sanitizer.PlaceholderWebsite + "/cka",
				Summary:        "Credential issued to " + sanitizer.PlaceholderFullName + " (" + sanitizer.PlaceholderEmail + ").",
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
				MissingArea:       "Cloud Provider (" + sanitizer.PlaceholderFullName + ")",
				ClarifyingPrompt:  "Does " + sanitizer.PlaceholderFullName + " have AWS production experience?",
				RecommendedAction: "Clarify with " + sanitizer.PlaceholderFullName,
			},
		},
		IdentifiedGaps: []core.GapItem{
			{
				Requirement:       "Distinct gap item for " + sanitizer.PlaceholderFullName,
				MissingArea:       "Infra (" + sanitizer.PlaceholderFullName + ")",
				ClarifyingPrompt:  "Ask " + sanitizer.PlaceholderFullName,
				RecommendedAction: "Follow up at " + sanitizer.PlaceholderEmail,
			},
		},
	}

	genResp := core.GenerationResponse{
		TailoredContent: tailored,
		GapReport:       gapReport,
	}

	session.DeobfuscateGenerationResponse(&genResp)

	// 1. Verify LLM-generated free-text TailoredContent fields are deobfuscated
	if strings.Contains(genResp.TailoredContent.Summary, sanitizer.PlaceholderFullName) ||
		strings.Contains(genResp.TailoredContent.Summary, "John Doe") ||
		!strings.Contains(genResp.TailoredContent.Summary, "Alice Walker (also Alice Walker)") {
		t.Errorf("Summary failed to deobfuscate FullName: %s", genResp.TailoredContent.Summary)
	}
	if !strings.Contains(genResp.TailoredContent.Summary, "alice.walker@example.org") ||
		!strings.Contains(genResp.TailoredContent.Summary, "+1 (555) 234-5678") ||
		!strings.Contains(genResp.TailoredContent.Summary, "https://alicewalker.dev") ||
		!strings.Contains(genResp.TailoredContent.Summary, "https://linkedin.com/in/alicewalker") {
		t.Errorf("Summary failed to deobfuscate contact endpoints: %s", genResp.TailoredContent.Summary)
	}

	exp := genResp.TailoredContent.Experiences[0]
	if !strings.Contains(exp.DutiesSummary, "alice.walker@example.org") {
		t.Errorf("Experience DutiesSummary failed deobfuscation: %s", exp.DutiesSummary)
	}
	if !strings.HasPrefix(exp.Bullets[0].Text, "Alice Walker designed") {
		t.Errorf("bullet 0 failed to deobfuscate: %s", exp.Bullets[0].Text)
	}
	if !strings.Contains(exp.Bullets[1].Text, "alice.walker@example.org") || !strings.Contains(exp.Bullets[1].Text, "alicewalker") {
		t.Errorf("bullet 1 failed to deobfuscate: %s", exp.Bullets[1].Text)
	}

	proj := genResp.TailoredContent.Projects[0]
	if !strings.Contains(proj.Summary, "Alice Walker (alicewalker-dev)") {
		t.Errorf("project Summary failed deobfuscation: %+v", proj)
	}
	if proj.Bullets[0].Text != "Published by Alice Walker." {
		t.Errorf("project Bullets[0].Text failed deobfuscation: got %q", proj.Bullets[0].Text)
	}

	eduBullet := genResp.TailoredContent.Education[0].Bullets[0].Text
	if eduBullet != "Researched Paxos variants with Alice Walker at https://alicewalker.dev." {
		t.Errorf("education Bullets[0].Text failed deobfuscation: got %q", eduBullet)
	}
	certSummary := genResp.TailoredContent.Certifications[0].Summary
	if certSummary != "Credential issued to Alice Walker (alice.walker@example.org)." {
		t.Errorf("certification Summary failed deobfuscation: got %q", certSummary)
	}

	// Non-schema and validator-overwritten structured identity fields must NOT be deobfuscated
	untouchedTailored := []struct {
		name string
		got  string
		want string
	}{
		{"Experience.Company", exp.Company, sanitizer.PlaceholderFullName + " Consulting"},
		{"Experience.RoleTitle", exp.RoleTitle, "Staff Architect (" + sanitizer.PlaceholderFullName + ")"},
		{"Experience.SupervisorName", exp.SupervisorName, sanitizer.PlaceholderFullName},
		{"Experience.SupervisorContact", exp.SupervisorContact, sanitizer.PlaceholderEmail},
		{"Experience.CompanyContact.Value", exp.CompanyContact.Value, sanitizer.PlaceholderEmail},
		{"Project.Name", proj.Name, "Raft-Go (" + sanitizer.PlaceholderFullName + ")"},
		{"Project.Role", proj.Role, "Creator (" + sanitizer.PlaceholderFullName + ")"},
		{"Project.URL", proj.URL, "https://social.example.com/profile-2"},
		{"Education.Institution", genResp.TailoredContent.Education[0].Institution, "MIT (" + sanitizer.PlaceholderFullName + ")"},
		{"Education.Degree", genResp.TailoredContent.Education[0].Degree, "M.S. (" + sanitizer.PlaceholderFullName + ")"},
		{"Education.Field", genResp.TailoredContent.Education[0].Field, "CS (" + sanitizer.PlaceholderFullName + ")"},
		{"Certification.Name", genResp.TailoredContent.Certifications[0].Name, "CKA (" + sanitizer.PlaceholderFullName + ")"},
		{"Certification.Issuer", genResp.TailoredContent.Certifications[0].Issuer, "CNCF / candidate-social-1"},
		{"Certification.URL", genResp.TailoredContent.Certifications[0].URL, sanitizer.PlaceholderWebsite + "/cka"},
	}
	for _, tc := range untouchedTailored {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("expected %s untouched %q, got %q", tc.name, tc.want, tc.got)
			}
		})
	}

	// 2. Verify GapReport deobfuscation across all fields
	if genResp.GapReport.OverallMatchSummary != "Alice Walker strongly matches 90% of requirements." {
		t.Errorf("GapReport OverallMatchSummary failed: %q", genResp.GapReport.OverallMatchSummary)
	}
	if genResp.GapReport.StrategicRecommendations[0] != "Alice Walker should highlight enterprise scaling." ||
		genResp.GapReport.StrategicRecommendations[1] != "Add portfolio link https://alicewalker.dev" {
		t.Errorf("GapReport StrategicRecommendations failed: %+v", genResp.GapReport.StrategicRecommendations)
	}
	mr := genResp.GapReport.MissingRequirements[0]
	if mr.Requirement != "Experience with AWS for Alice Walker" ||
		mr.MissingArea != "Cloud Provider (Alice Walker)" ||
		mr.ClarifyingPrompt != "Does Alice Walker have AWS production experience?" ||
		mr.RecommendedAction != "Clarify with Alice Walker" {
		t.Errorf("GapReport MissingRequirements[0] failed: %+v", mr)
	}
	ig := genResp.GapReport.IdentifiedGaps[0]
	if ig.Requirement != "Distinct gap item for Alice Walker" ||
		ig.MissingArea != "Infra (Alice Walker)" ||
		ig.ClarifyingPrompt != "Ask Alice Walker" ||
		ig.RecommendedAction != "Follow up at alice.walker@example.org" {
		t.Errorf("GapReport distinct IdentifiedGaps[0] failed: %+v", ig)
	}

	// 3. Verify normalized (equal-length aliased backing slice) GapReport deobfuscates cleanly
	normalizedReport := core.GapReport{
		MissingRequirements: []core.GapItem{
			{Requirement: "Req for " + sanitizer.PlaceholderFullName, ClarifyingPrompt: "Prompt " + sanitizer.PlaceholderFullName},
		},
	}
	normalizedReport.Normalize()
	session.DeobfuscateGapReport(&normalizedReport)
	if normalizedReport.MissingRequirements[0].Requirement != "Req for Alice Walker" ||
		normalizedReport.IdentifiedGaps[0].Requirement != "Req for Alice Walker" {
		t.Errorf("normalized GapReport failed deobfuscation: %+v", normalizedReport)
	}

	// 4. Verify unequal-length prefix-aliased GapReport still deobfuscates trailing elements
	backing := []core.GapItem{
		{Requirement: "Shared item for " + sanitizer.PlaceholderFullName},
		{Requirement: "Trailing item for " + sanitizer.PlaceholderFullName},
	}
	prefixAliased := core.GapReport{
		MissingRequirements: backing[:1],
		IdentifiedGaps:      backing,
	}
	session.DeobfuscateGapReport(&prefixAliased)
	if prefixAliased.IdentifiedGaps[0].Requirement != "Shared item for Alice Walker" ||
		prefixAliased.IdentifiedGaps[1].Requirement != "Trailing item for Alice Walker" {
		t.Errorf("prefix-aliased GapReport failed trailing deobfuscation: %+v", prefixAliased.IdentifiedGaps)
	}
}

func TestSession_DeobfuscateATSScore(t *testing.T) {
	snap := sampleCandidateSnapshot()
	session := sanitizer.NewSession(&snap.Profile)

	score := &core.ATSScore{
		ExtractionSummary: "Parsed resume for " + sanitizer.PlaceholderFullName,
		MatchingSummary:   sanitizer.PlaceholderFullName + " aligns well.",
		ExtractedCandidate: &core.ATSExtractedCandidate{
			Summary: sanitizer.PlaceholderFullName + " summary (" + sanitizer.PlaceholderEmail + ")",
			Skills:  []string{"Go (" + sanitizer.PlaceholderFullName + ")"},
			Experiences: []core.ATSExtractedExperience{
				{
					Company:    sanitizer.PlaceholderFullName + " LLC",
					Title:      "Engineer (" + sanitizer.PlaceholderFullName + ")",
					DateRange:  "2021 - " + sanitizer.PlaceholderFullName,
					Highlights: []string{"Contact " + sanitizer.PlaceholderPhone},
				},
			},
			Educations: []core.ATSExtractedEducation{
				{
					Institution: "MIT (" + sanitizer.PlaceholderFullName + ")",
					Degree:      "M.S. (" + sanitizer.PlaceholderFullName + ")",
					Field:       "CS (" + sanitizer.PlaceholderFullName + ")",
					DateRange:   "2019 - 2021",
				},
			},
			Certifications: []string{"CKA - " + sanitizer.PlaceholderFullName},
		},
	}

	session.DeobfuscateATSScore(score)

	if score.ExtractionSummary != "Parsed resume for Alice Walker" || score.MatchingSummary != "Alice Walker aligns well." {
		t.Errorf("ATSScore summaries failed deobfuscation: %+v", score)
	}
	c := score.ExtractedCandidate
	if !strings.Contains(c.Summary, "Alice Walker") || !strings.Contains(c.Summary, "alice.walker@example.org") {
		t.Errorf("ATSExtractedCandidate.Summary failed: %s", c.Summary)
	}
	if c.Experiences[0].Title != "Engineer (Alice Walker)" {
		t.Errorf("ATSExtractedCandidate.Experiences[0].Title failed: %q", c.Experiences[0].Title)
	}
	if c.Experiences[0].Highlights[0] != "Contact +1 (555) 234-5678" {
		t.Errorf("ATSExtractedCandidate.Experiences[0].Highlights[0] failed: %q", c.Experiences[0].Highlights[0])
	}

	// Structured skills, company, date range, education, and certification details must NOT be deobfuscated
	if c.Skills[0] != "Go ("+sanitizer.PlaceholderFullName+")" {
		t.Errorf("expected Skills[0] untouched, got %q", c.Skills[0])
	}
	if c.Experiences[0].Company != sanitizer.PlaceholderFullName+" LLC" {
		t.Errorf("expected Experiences[0].Company untouched, got %q", c.Experiences[0].Company)
	}
	if c.Experiences[0].DateRange != "2021 - "+sanitizer.PlaceholderFullName {
		t.Errorf("expected Experiences[0].DateRange untouched, got %q", c.Experiences[0].DateRange)
	}
	if c.Educations[0].Institution != "MIT ("+sanitizer.PlaceholderFullName+")" ||
		c.Educations[0].Degree != "M.S. ("+sanitizer.PlaceholderFullName+")" ||
		c.Educations[0].Field != "CS ("+sanitizer.PlaceholderFullName+")" {
		t.Errorf("expected Educations[0] untouched, got %+v", c.Educations[0])
	}
	if c.Certifications[0] != "CKA - "+sanitizer.PlaceholderFullName {
		t.Errorf("expected Certifications[0] untouched, got %q", c.Certifications[0])
	}
}

func TestSession_IdempotenceAndNilSafety(t *testing.T) {
	// 1. Nil *Session receiver safety across all methods
	var nilSession *sanitizer.Session
	if nilSession.ObfuscateString("Alice") != "Alice" {
		t.Errorf("nil session mutated ObfuscateString")
	}
	if nilSession.DeobfuscateString("[John Doe]") != "[John Doe]" {
		t.Errorf("nil session mutated DeobfuscateString")
	}
	if nilSession.ObfuscateSnapshot(nil) != nil {
		t.Errorf("expected nil from ObfuscateSnapshot(nil)")
	}
	if nilSession.ObfuscatePriorVersion(nil) != nil {
		t.Errorf("expected nil from ObfuscatePriorVersion(nil)")
	}
	obfNilReq := nilSession.ObfuscateRequest(core.GenerationRequest{GlobalRules: "Rule"})
	if obfNilReq.GlobalRules != "Rule" {
		t.Errorf("nil session mutated ObfuscateRequest.GlobalRules: got %q", obfNilReq.GlobalRules)
	}
	nilSession.DeobfuscateTailoredContent(&core.TailoredContent{Summary: "x"})
	nilSession.DeobfuscateGapReport(&core.GapReport{OverallMatchSummary: "x"})
	nilSession.DeobfuscateGenerationResponse(&core.GenerationResponse{})
	nilSession.DeobfuscateATSScore(&core.ATSScore{})

	// 2. Empty / nil constructors & nil pointers
	emptySession := sanitizer.NewSession(nil)
	snapNilSession := sanitizer.NewSessionFromSnapshot(nil)
	reqNilSession := sanitizer.NewSessionFromRequest(core.GenerationRequest{})
	if snapNilSession == nil || reqNilSession == nil {
		t.Fatalf("expected non-nil empty sessions from nil snapshot/request constructors")
	}
	raw := "Hello [John Doe]"
	for name, sess := range map[string]*sanitizer.Session{
		"NewSession(nil)":             emptySession,
		"NewSessionFromSnapshot(nil)": snapNilSession,
		"NewSessionFromRequest({})":   reqNilSession,
	} {
		if sess.ObfuscateString(raw) != raw {
			t.Errorf("%s corrupted raw string in ObfuscateString", name)
		}
		if sess.DeobfuscateString(raw) != raw {
			t.Errorf("%s corrupted raw string in DeobfuscateString", name)
		}
	}

	emptySession.DeobfuscateGenerationResponse(nil)
	emptySession.DeobfuscateTailoredContent(nil)
	emptySession.DeobfuscateGapReport(nil)
	emptySession.DeobfuscateATSScore(nil)
	emptySession.DeobfuscateATSScore(&core.ATSScore{ExtractedCandidate: nil})

	// 3. Repeated deobfuscation is idempotent
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
	snap := &core.MainProfileSnapshot{Profile: *prof}
	obf := session.ObfuscateSnapshot(snap)

	// 1. Non-LLM structured profile social links remain untouched
	if obf.Profile.Profiles[0].Username != "go" {
		t.Errorf("expected Profiles[0].Username to remain 'go', got %s", obf.Profile.Profiles[0].Username)
	}

	// 2. Freeform text with technical terms "Go", "AI", "C" is NOT corrupted
	text := "Senior Go engineer with 5 years Go experience and AI expertise. Wrote C drivers for real-time systems."
	obfText := session.ObfuscateString(text)
	if obfText != text {
		t.Errorf("short username corrupted technical terms: got %q, want %q", obfText, text)
	}

	// 3. Skipped short usernames (< 3 chars) register neither forward nor one-way reverse mappings
	literalPlaceholderText := "The candidate candidate-social-1 expert designed candidate-social-2 agents."
	deobf := session.DeobfuscateString(literalPlaceholderText)
	if deobf != literalPlaceholderText {
		t.Errorf("expected skipped short username to register no reverse mapping: got %q, want %q", deobf, literalPlaceholderText)
	}
}

func TestSession_PhoneBoundary_AndMetricsProtection(t *testing.T) {
	cases := []struct {
		name      string
		phone     string
		input     string
		wantObf   string
		wantDeobf string
	}{
		{
			name:      "short phone under 7 digits preserves metrics",
			phone:     "+1",
			input:     "Managed +15 engineers and increased revenue by +100% with +1.5M ARR.",
			wantObf:   "Managed +15 engineers and increased revenue by +100% with +1.5M ARR.",
			wantDeobf: "Managed +15 engineers and increased revenue by +100% with +1.5M ARR.",
		},
		{
			name:      "short phone under 7 digits registers no one-way reverse rule",
			phone:     "+1",
			input:     "Literal placeholder " + sanitizer.PlaceholderPhone + " stays intact.",
			wantObf:   "Literal placeholder " + sanitizer.PlaceholderPhone + " stays intact.",
			wantDeobf: "Literal placeholder " + sanitizer.PlaceholderPhone + " stays intact.",
		},
		{
			name:      "international phone with leading plus preserves whitespace and metrics",
			phone:     "+1 555 432 1098",
			input:     "Contact +1 555 432 1098 or email. Increased revenue by +100% and led +15 engineers.",
			wantObf:   "Contact " + sanitizer.PlaceholderPhone + " or email. Increased revenue by +100% and led +15 engineers.",
			wantDeobf: "Contact +1 555 432 1098 or email. Increased revenue by +100% and led +15 engineers.",
		},
		{
			name:      "parenthesized area code phone",
			phone:     "(555) 432-1098",
			input:     "Call (555) 432-1098 today.",
			wantObf:   "Call " + sanitizer.PlaceholderPhone + " today.",
			wantDeobf: "Call (555) 432-1098 today.",
		},
		{
			name:      "word-bounded plain digits phone",
			phone:     "555-432-1098",
			input:     "Direct line: 555-432-1098.",
			wantObf:   "Direct line: " + sanitizer.PlaceholderPhone + ".",
			wantDeobf: "Direct line: 555-432-1098.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := sanitizer.NewSession(&core.Profile{FullName: "Phone Tester", Phone: tc.phone})
			gotObf := session.ObfuscateString(tc.input)
			if gotObf != tc.wantObf {
				t.Errorf("ObfuscateString = %q, want %q", gotObf, tc.wantObf)
			}
			gotDeobf := session.DeobfuscateString(gotObf)
			if gotDeobf != tc.wantDeobf {
				t.Errorf("DeobfuscateString = %q, want %q", gotDeobf, tc.wantDeobf)
			}
		})
	}
}

func TestSession_URLValidation_ValidAndInvalidBoundaries(t *testing.T) {
	invalidProf := &core.Profile{
		FullName: "URL Candidate",
		Metadata: map[string]any{
			"website": "/",
		},
		Profiles: []core.SocialLink{
			{Network: "Short", URL: "co"},
			{Network: "IncompleteHTTP", URL: "http://"},
			{Network: "IncompleteHTTPS", URL: "https://"},
			{Network: "LeadingDot", URL: "https://.bad"},
			{Network: "TrailingDot", URL: "https://bad."},
		},
	}

	invalidSession := sanitizer.NewSession(invalidProf)
	text := "Collaborated with co-workers on /dev/null and visited https://example.com/path."
	if obf := invalidSession.ObfuscateString(text); obf != text {
		t.Errorf("invalid URLs corrupted text: got %q, want %q", obf, text)
	}
	placeholderText := "Visit " + sanitizer.PlaceholderWebsite + " or https://social.example.com/profile-1."
	if deobf := invalidSession.DeobfuscateString(placeholderText); deobf != placeholderText {
		t.Errorf("invalid URLs registered unexpected reverse mapping: got %q, want %q", deobf, placeholderText)
	}

	bareDomainProf := &core.Profile{
		Profiles: []core.SocialLink{
			{Network: "Portfolio", URL: "alicewalker.dev/portfolio"},
		},
	}
	bareSession := sanitizer.NewSession(bareDomainProf)
	bareText := "See alicewalker.dev/portfolio for projects."
	obfBare := bareSession.ObfuscateString(bareText)
	if obfBare != "See "+sanitizer.PlaceholderWebsite+" for projects." {
		t.Errorf("bare domain URL failed obfuscation: got %q", obfBare)
	}
	if deobf := bareSession.DeobfuscateString(obfBare); deobf != bareText {
		t.Errorf("bare domain URL failed deobfuscation: got %q", deobf)
	}
}

func TestSession_BlankProfileWebsiteFallsBackToMetadata(t *testing.T) {
	prof := &core.Profile{
		FullName: "Fallback Candidate",
		Profiles: []core.SocialLink{
			{Network: "website", URL: "   "},
			{Network: "portfolio", URL: ""},
		},
		Metadata: map[string]any{
			"website": "https://fallback-candidate.dev",
		},
	}

	session := sanitizer.NewSession(prof)
	input := "Portfolio hosted at https://fallback-candidate.dev/work."
	obf := session.ObfuscateString(input)
	wantObf := "Portfolio hosted at " + sanitizer.PlaceholderWebsite + "/work."
	if obf != wantObf {
		t.Fatalf("expected metadata website fallback obfuscation %q, got %q", wantObf, obf)
	}
	if deobf := session.DeobfuscateString(obf); deobf != input {
		t.Errorf("expected round-trip deobfuscation %q, got %q", input, deobf)
	}
}
