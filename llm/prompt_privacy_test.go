package llm_test

import (
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/llm"
	"github.com/resumeee-app/resumeee-core/privacy"
)

func TestBuildPrompt_ContactPrivacyMasking(t *testing.T) {
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
	if !strings.Contains(payload.UserPrompt, privacy.PlaceholderFullName) {
		t.Errorf("UserPrompt missing PlaceholderFullName %q", privacy.PlaceholderFullName)
	}
	if !strings.Contains(payload.UserPrompt, privacy.PlaceholderEmail) {
		t.Errorf("UserPrompt missing PlaceholderEmail %q", privacy.PlaceholderEmail)
	}
	if !strings.Contains(payload.UserPrompt, privacy.PlaceholderPhone) {
		t.Errorf("UserPrompt missing PlaceholderPhone %q", privacy.PlaceholderPhone)
	}
	if strings.Contains(payload.UserPrompt, "https://social.example.com/profile-1") {
		t.Errorf("UserPrompt should not contain stripped social profile URL placeholder")
	}

	// Verify original request MainProfile was NOT mutated
	if req.MainProfile.Profile.FullName != realName {
		t.Errorf("BuildPrompt mutated caller's MainProfile FullName")
	}
}

func TestBuildGapAnalysisPrompt_ContactPrivacyMasking(t *testing.T) {
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

	if !strings.Contains(payload.UserPrompt, privacy.PlaceholderFullName) {
		t.Errorf("BuildGapAnalysisPrompt missing PlaceholderFullName %q", privacy.PlaceholderFullName)
	}
}
