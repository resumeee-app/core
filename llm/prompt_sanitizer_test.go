package llm_test

import (
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/llm"
	"github.com/resumeee-app/resumeee-core/sanitizer"
)

func TestBuildPrompt_ContactSanitizerMasking(t *testing.T) {
	realName := "Alex Mercer"
	realEmail := "alex.mercer@cloudsystems.io"
	realPhone := "+1 (555) 789-0123"
	realWebsite := "https://alexmercer.io"
	realLinkedIn := "https://linkedin.com/in/alexmercer"
	realGitHub := "https://github.com/alexmercer-dev"
	realUsername := "alexmercer-dev"

	req := core.GenerationRequest{
		GlobalRules:        "Tier 1: Always highlight " + realName + "'s distributed systems background. Reach at " + realEmail + ".",
		PresetInstructions: "Tier 2: Emphasize " + realName + "'s metrics.",
		MainProfile: &core.MainProfileSnapshot{
			Profile: core.Profile{
				FullName: realName,
				Headline: "Principal Cloud Architect (" + realName + ")",
				Email:    realEmail,
				Phone:    realPhone,
				Summary:  realName + " is a distributed systems expert. Contact: " + realEmail + ", Phone: " + realPhone + ", Web: " + realWebsite + ".",
				Profiles: []core.SocialLink{
					{Network: "LinkedIn", Username: "alexmercer", URL: realLinkedIn},
					{Network: "GitHub", Username: realUsername, URL: realGitHub},
				},
				Metadata: map[string]any{
					"website": realWebsite,
				},
			},
			Experiences: []core.WorkExperience{
				{
					ID:                "exp-1",
					ShortID:           "exp-1",
					Company:           "CloudCorp",
					RoleTitle:         "Lead Architect",
					DutiesSummary:     realName + " oversaw infrastructure engineering.",
					SupervisorName:    "Jane Director",
					SupervisorContact: realEmail,
					Facts: []core.Fact{
						{
							ID:        "fact-1",
							ShortID:   "f-1",
							Statement: realName + " re-architected payment gateway.",
							Impact:    "Inquiries: " + realEmail,
						},
					},
				},
			},
			Projects: []core.Project{
				{
					ID:      "proj-1",
					ShortID: "proj-1",
					Name:    "DistMesh",
					URL:     realGitHub + "/distmesh",
					Summary: "Authored by " + realName + " (" + realUsername + ").",
				},
			},
			ExportedAt: time.Now(),
		},
		BranchContext: core.Branch{
			TargetRole:         "Staff Systems Engineer",
			TargetCompany:      "Stripe",
			TargetRequirements: "Looking for an engineer with " + realName + "'s caliber at " + realEmail + ".",
			BranchInstructions: "Direct attention to " + realName + "'s Raft expertise.",
		},
		PriorVersion: &core.TailoredContent{
			Summary: "Prior active summary for " + realName + " (" + realEmail + ").",
		},
	}

	payload, err := llm.BuildPrompt(req)
	if err != nil {
		t.Fatalf("unexpected error from BuildPrompt: %v", err)
	}

	assertNoPII := func(t *testing.T, sectionName, text string) {
		t.Helper()
		if strings.Contains(text, realName) {
			t.Errorf("%s leaked candidate FullName %q", sectionName, realName)
		}
		if strings.Contains(text, realEmail) {
			t.Errorf("%s leaked candidate Email %q", sectionName, realEmail)
		}
		if strings.Contains(text, realPhone) {
			t.Errorf("%s leaked candidate Phone %q", sectionName, realPhone)
		}
		if strings.Contains(text, realWebsite) {
			t.Errorf("%s leaked candidate Website %q", sectionName, realWebsite)
		}
		if strings.Contains(text, realLinkedIn) {
			t.Errorf("%s leaked candidate LinkedIn URL %q", sectionName, realLinkedIn)
		}
		if strings.Contains(text, realGitHub) {
			t.Errorf("%s leaked candidate GitHub URL %q", sectionName, realGitHub)
		}
	}

	assertNoPII(t, "SystemPrompt", payload.SystemPrompt)
	assertNoPII(t, "UserPrompt", payload.UserPrompt)

	// Verify required obfuscated placeholders are present
	if !strings.Contains(payload.UserPrompt, sanitizer.PlaceholderFullName) {
		t.Errorf("UserPrompt missing PlaceholderFullName %q", sanitizer.PlaceholderFullName)
	}
	if !strings.Contains(payload.UserPrompt, sanitizer.PlaceholderEmail) {
		t.Errorf("UserPrompt missing PlaceholderEmail %q", sanitizer.PlaceholderEmail)
	}
	if !strings.Contains(payload.UserPrompt, sanitizer.PlaceholderPhone) {
		t.Errorf("UserPrompt missing PlaceholderPhone %q", sanitizer.PlaceholderPhone)
	}
	if strings.Contains(payload.UserPrompt, "https://social.example.com/profile-1") {
		t.Errorf("UserPrompt should not contain stripped social profile URL placeholder")
	}

	// Verify original request MainProfile was NOT mutated
	if req.MainProfile.Profile.FullName != realName {
		t.Errorf("BuildPrompt mutated caller's MainProfile FullName")
	}
	if !strings.Contains(req.MainProfile.Profile.Summary, realName) {
		t.Errorf("BuildPrompt mutated caller's MainProfile Summary")
	}
}

func TestBuildGapAnalysisPrompt_ContactSanitizerMasking(t *testing.T) {
	realName := "Samantha Ray"
	realEmail := "samantha.ray@fintech.net"
	realPhone := "+1 (555) 999-8888"

	req := core.GenerationRequest{
		MainProfile: &core.MainProfileSnapshot{
			Profile: core.Profile{
				FullName: realName,
				Email:    realEmail,
				Phone:    realPhone,
				Summary:  realName + " has 10+ years in distributed databases.",
			},
			Skills: []core.Skill{
				{ID: "sk-1", ShortID: "sk-1", Name: "Go"},
			},
			ExportedAt: time.Now(),
		},
		BranchContext: core.Branch{
			TargetRole:         "Lead Database Engineer",
			TargetCompany:      "Cockroach Labs",
			TargetRequirements: "Candidate like " + realName + " with strong Go internals.",
			BranchInstructions: "Highlight " + realName + "'s storage engine experience.",
		},
	}

	payload, err := llm.BuildGapAnalysisPrompt(req)
	if err != nil {
		t.Fatalf("unexpected error from BuildGapAnalysisPrompt: %v", err)
	}

	if strings.Contains(payload.UserPrompt, realName) {
		t.Errorf("BuildGapAnalysisPrompt leaked candidate FullName %q", realName)
	}
	if strings.Contains(payload.UserPrompt, realEmail) {
		t.Errorf("BuildGapAnalysisPrompt leaked candidate Email %q", realEmail)
	}
	if strings.Contains(payload.UserPrompt, realPhone) {
		t.Errorf("BuildGapAnalysisPrompt leaked candidate Phone %q", realPhone)
	}

	if !strings.Contains(payload.UserPrompt, sanitizer.PlaceholderFullName) {
		t.Errorf("BuildGapAnalysisPrompt missing PlaceholderFullName %q", sanitizer.PlaceholderFullName)
	}
}

func TestBuildPrompts_PreservesStructuralLabelsAndScrubsCompanyAndCertPII(t *testing.T) {
	realName := "Jordan Vance"
	realWebsite := "https://jordanvance.dev"
	snap := &core.MainProfileSnapshot{
		Profile: core.Profile{
			FullName: realName,
			Summary:  realName + " builds distributed storage engines.",
			Profiles: []core.SocialLink{
				{Network: "Website", URL: realWebsite},
			},
		},
		Experiences: []core.WorkExperience{
			{
				ID:            "exp-1",
				ShortID:       "exp-1",
				Company:       realName + " Systems",
				RoleTitle:     "Principal Engineer",
				DutiesSummary: realName + " led consensus development.",
			},
		},
		Education: []core.Education{
			{
				ID:          "edu-1",
				ShortID:     "edu-1",
				Institution: realName + " Institute of Technology",
				Degree:      "M.S. (" + realName + " Fellow)",
				Field:       "Computer Science (" + realName + " Track)",
			},
		},
		Certifications: []core.Certification{
			{
				ID:      "cert-1",
				ShortID: "cert-1",
				Name:    "CKA (" + realName + ")",
				Issuer:  "CNCF",
				URL:     realWebsite + "/cka",
				Summary: "Issued to " + realName + ".",
			},
		},
		ExportedAt: time.Now(),
	}

	req := core.GenerationRequest{
		MainProfile: snap,
		BranchContext: core.Branch{
			TargetRole:         "Staff Engineer",
			TargetCompany:      realName + " Corp",
			TargetRequirements: "Seeking engineer like " + realName + ".",
		},
		PriorVersion: &core.TailoredContent{
			Experiences: []core.TailoredExperience{
				{
					SourceEntityID: "exp-1",
					Company:        realName + " Systems",
				},
			},
			Certifications: []core.TailoredCert{
				{
					SourceEntityID: "cert-1",
					URL:            realWebsite + "/cka",
					Summary:        "Unprojected cert summary for " + realName + ".",
				},
			},
		},
	}

	structuralLabels := []string{
		realName + " Corp",
		realName + " Institute of Technology",
		"M.S. (" + realName + " Fellow)",
		"Computer Science (" + realName + " Track)",
		"CKA (" + realName + ")",
		"CNCF",
	}

	assertScrubbedCompanyAndCert := func(promptName, prompt string) {
		t.Helper()
		for _, leaked := range []string{
			realName + " Systems",
			realWebsite + "/cka",
			"Issued to " + realName + ".",
		} {
			if strings.Contains(prompt, leaked) {
				t.Errorf("%s leaked PII %q", promptName, leaked)
			}
		}
		for _, masked := range []string{
			sanitizer.PlaceholderFullName + " Systems",
			sanitizer.PlaceholderWebsite + "/cka",
			"Issued to " + sanitizer.PlaceholderFullName + ".",
		} {
			if !strings.Contains(prompt, masked) {
				t.Errorf("%s missing scrubbed placeholder %q", promptName, masked)
			}
		}
	}

	tailorPayload, err := llm.BuildPrompt(req)
	if err != nil {
		t.Fatalf("BuildPrompt failed: %v", err)
	}
	assertScrubbedCompanyAndCert("BuildPrompt", tailorPayload.UserPrompt)
	for _, want := range structuralLabels {
		if !strings.Contains(tailorPayload.UserPrompt, want) {
			t.Errorf("BuildPrompt unexpectedly obfuscated non-PII structured field %q", want)
		}
	}

	// Verify PriorVersion section in BuildPrompt scrubs Company and Certification.URL while omitting Summary.
	_, afterPriorStart, okStart := strings.Cut(tailorPayload.UserPrompt, "<prior_active_version>")
	if !okStart {
		t.Fatalf("BuildPrompt UserPrompt missing <prior_active_version> marker")
	}
	priorSection, _, okEnd := strings.Cut(afterPriorStart, "</prior_active_version>")
	if !okEnd {
		t.Fatalf("BuildPrompt UserPrompt missing </prior_active_version> marker")
	}
	if strings.Contains(priorSection, realName+" Systems") || !strings.Contains(priorSection, sanitizer.PlaceholderFullName+" Systems") {
		t.Errorf("BuildPrompt PriorVersion failed to scrub Company: %s", priorSection)
	}
	if strings.Contains(priorSection, realWebsite+"/cka") || !strings.Contains(priorSection, sanitizer.PlaceholderWebsite+"/cka") {
		t.Errorf("BuildPrompt PriorVersion failed to scrub Certification.URL: %s", priorSection)
	}
	if strings.Contains(tailorPayload.UserPrompt, "Unprojected cert summary") {
		t.Errorf("BuildPrompt unexpectedly projected PriorVersion Certification.Summary")
	}

	// Verify BuildPrompt did not mutate caller MainProfile or PriorVersion in place.
	if req.MainProfile.Experiences[0].Company != realName+" Systems" {
		t.Errorf("BuildPrompt mutated MainProfile.Experiences[0].Company: %q", req.MainProfile.Experiences[0].Company)
	}
	if req.MainProfile.Certifications[0].URL != realWebsite+"/cka" {
		t.Errorf("BuildPrompt mutated MainProfile.Certifications[0].URL: %q", req.MainProfile.Certifications[0].URL)
	}
	if req.MainProfile.Certifications[0].Summary != "Issued to "+realName+"." {
		t.Errorf("BuildPrompt mutated MainProfile.Certifications[0].Summary: %q", req.MainProfile.Certifications[0].Summary)
	}
	if req.PriorVersion.Experiences[0].Company != realName+" Systems" {
		t.Errorf("BuildPrompt mutated PriorVersion.Experiences[0].Company: %q", req.PriorVersion.Experiences[0].Company)
	}
	if req.PriorVersion.Certifications[0].URL != realWebsite+"/cka" {
		t.Errorf("BuildPrompt mutated PriorVersion.Certifications[0].URL: %q", req.PriorVersion.Certifications[0].URL)
	}
	if req.PriorVersion.Certifications[0].Summary != "Unprojected cert summary for "+realName+"." {
		t.Errorf("BuildPrompt mutated PriorVersion.Certifications[0].Summary: %q", req.PriorVersion.Certifications[0].Summary)
	}

	gapPayload, err := llm.BuildGapAnalysisPrompt(req)
	if err != nil {
		t.Fatalf("BuildGapAnalysisPrompt failed: %v", err)
	}
	assertScrubbedCompanyAndCert("BuildGapAnalysisPrompt", gapPayload.UserPrompt)
	for _, want := range structuralLabels {
		if !strings.Contains(gapPayload.UserPrompt, want) {
			t.Errorf("BuildGapAnalysisPrompt unexpectedly obfuscated non-PII structured field %q", want)
		}
	}

	session := sanitizer.NewSessionFromSnapshot(snap)
	extracted := &core.ATSExtractedCandidate{
		Educations: []core.ATSExtractedEducation{
			{
				Institution: realName + " Institute of Technology",
				Degree:      "M.S. (" + realName + " Fellow)",
				Field:       "Computer Science (" + realName + " Track)",
			},
		},
		Certifications: []string{"CKA (" + realName + ")"},
	}
	atsMatchPayload, err := llm.BuildATSMatchingPrompt(req.BranchContext, extracted, session)
	if err != nil {
		t.Fatalf("BuildATSMatchingPrompt failed: %v", err)
	}
	for _, want := range []string{
		realName + " Corp",
		realName + " Institute of Technology",
		"M.S. (" + realName + " Fellow)",
		"Computer Science (" + realName + " Track)",
		"CKA (" + realName + ")",
	} {
		if !strings.Contains(atsMatchPayload.UserPrompt, want) {
			t.Errorf("BuildATSMatchingPrompt unexpectedly obfuscated non-PII structured field %q", want)
		}
	}
	if strings.Contains(atsMatchPayload.UserPrompt, "Seeking engineer like "+realName) {
		t.Errorf("BuildATSMatchingPrompt failed to obfuscate TargetRequirements")
	}
}
