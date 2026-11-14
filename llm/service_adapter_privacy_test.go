package llm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/llm"
	"github.com/resumeee-app/resumeee-core/sanitizer"
)

type mockPrivacyProvider struct {
	name         string
	defaultModel string
	tailoredResp *core.GenerationResponse
	completeText string
}

func (m *mockPrivacyProvider) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock-privacy"
}

func (m *mockPrivacyProvider) DefaultModel() string {
	if m.defaultModel != "" {
		return m.defaultModel
	}
	return "mock-model"
}

func (m *mockPrivacyProvider) GenerateTailoredResume(ctx context.Context, req core.GenerationRequest) (*core.GenerationResponse, error) {
	if m.tailoredResp != nil {
		copyResp := *m.tailoredResp
		return &copyResp, nil
	}
	return &core.GenerationResponse{}, nil
}

func (m *mockPrivacyProvider) CompleteText(ctx context.Context, systemPrompt, userPrompt, model string) (string, error) {
	if m.completeText != "" {
		return m.completeText, nil
	}
	return `{"gap_report":{"overall_match_summary":"Candidate matches criteria","missing_requirements":[]}}`, nil
}

func TestServiceAdapter_GenerateTailoredResume_DeobfuscationSeam(t *testing.T) {
	mockProv := &mockPrivacyProvider{
		name:         "openai",
		defaultModel: "gpt-4o",
		tailoredResp: &core.GenerationResponse{
			TailoredContent: core.TailoredContent{
				Summary: "Results-driven engineer " + sanitizer.PlaceholderFullName + " reachable at " + sanitizer.PlaceholderEmail + " or " + sanitizer.PlaceholderPhone + ". Portfolio: " + sanitizer.PlaceholderWebsite + ". Handle: candidate-social-1.",
				Experiences: []core.TailoredExperience{
					{
						Company:       "Acme",
						DutiesSummary: sanitizer.PlaceholderFullName + " led distributed infrastructure.",
						Bullets: []core.TailoredBullet{
							{Text: sanitizer.PlaceholderFullName + " scaled throughput by 300%."},
						},
					},
				},
			},
			GapReport: core.GapReport{
				OverallMatchSummary: sanitizer.PlaceholderFullName + " satisfies all requirements.",
			},
		},
	}

	reg := llm.NewRegistry(nil)
	reg.Register(mockProv)
	adapter := llm.NewServiceAdapter(reg)

	candidateProf := &core.Profile{
		FullName: "Alice Walker",
		Email:    "alice.walker@example.org",
		Phone:    "+1 555 432 1098",
		Profiles: []core.SocialLink{
			{Network: "GitHub", Username: "alicewalker", URL: "https://github.com/alicewalker"},
			{Network: "Website", URL: "https://alice.dev"},
		},
	}

	req := core.GenerationRequest{
		BranchContext: core.Branch{
			ProviderOverride: "openai",
			ModelOverride:    "gpt-4o",
		},
		MainProfile: &core.MainProfileSnapshot{
			Profile: *candidateProf,
		},
	}

	resp, err := adapter.GenerateTailoredResume(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify placeholders were restored to authentic candidate details
	if strings.Contains(resp.TailoredContent.Summary, sanitizer.PlaceholderFullName) {
		t.Errorf("summary contains PlaceholderFullName: %s", resp.TailoredContent.Summary)
	}
	if !strings.Contains(resp.TailoredContent.Summary, "Alice Walker") {
		t.Errorf("summary missing authentic name: %s", resp.TailoredContent.Summary)
	}
	if !strings.Contains(resp.TailoredContent.Summary, "alice.walker@example.org") {
		t.Errorf("summary missing authentic email: %s", resp.TailoredContent.Summary)
	}
	if !strings.Contains(resp.TailoredContent.Summary, "+1 555 432 1098") {
		t.Errorf("summary missing authentic phone: %s", resp.TailoredContent.Summary)
	}
	if !strings.Contains(resp.TailoredContent.Summary, "https://alice.dev") {
		t.Errorf("summary missing authentic website: %s", resp.TailoredContent.Summary)
	}
	if !strings.Contains(resp.TailoredContent.Summary, "alicewalker") {
		t.Errorf("summary missing authentic social username: %s", resp.TailoredContent.Summary)
	}

	if !strings.Contains(resp.TailoredContent.Experiences[0].DutiesSummary, "Alice Walker led") {
		t.Errorf("duties summary failed deobfuscation: %s", resp.TailoredContent.Experiences[0].DutiesSummary)
	}
	if !strings.Contains(resp.TailoredContent.Experiences[0].Bullets[0].Text, "Alice Walker scaled") {
		t.Errorf("bullet failed deobfuscation: %s", resp.TailoredContent.Experiences[0].Bullets[0].Text)
	}
	if !strings.Contains(resp.GapReport.OverallMatchSummary, "Alice Walker satisfies") {
		t.Errorf("gap report failed deobfuscation: %s", resp.GapReport.OverallMatchSummary)
	}
}

func TestServiceAdapter_GenerateGapAnalysis_DeobfuscationSeam(t *testing.T) {
	mockProv := &mockPrivacyProvider{
		name:         "openai",
		defaultModel: "gpt-4o",
		completeText: `{"gap_report":{"overall_match_summary":"` + sanitizer.PlaceholderFullName + ` demonstrates strong capability.","missing_requirements":[{"requirement":"Kubernetes","missing_area":"Cloud","clarifying_prompt":"Does ` + sanitizer.PlaceholderFullName + ` have experience?","recommended_action":"Review ` + sanitizer.PlaceholderFullName + `'s GitHub candidate-social-1"}]}}`,
	}

	reg := llm.NewRegistry(nil)
	reg.Register(mockProv)
	adapter := llm.NewServiceAdapter(reg)

	candidateProf := &core.Profile{
		FullName: "Alice Walker",
		Email:    "alice.walker@example.org",
		Profiles: []core.SocialLink{
			{Network: "GitHub", Username: "alicewalker", URL: "https://github.com/alicewalker"},
		},
	}

	req := core.GenerationRequest{
		BranchContext: core.Branch{
			ProviderOverride: "openai",
			ModelOverride:    "gpt-4o",
		},
		MainProfile: &core.MainProfileSnapshot{
			Profile: *candidateProf,
		},
	}

	report, err := adapter.GenerateGapAnalysis(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(report.OverallMatchSummary, sanitizer.PlaceholderFullName) {
		t.Errorf("gap report overall summary contains placeholder: %s", report.OverallMatchSummary)
	}
	if !strings.Contains(report.OverallMatchSummary, "Alice Walker demonstrates") {
		t.Errorf("gap report overall summary missing authentic name: %s", report.OverallMatchSummary)
	}

	if len(report.MissingRequirements) == 0 {
		t.Fatalf("expected missing requirements")
	}
	reqItem := report.MissingRequirements[0]
	if !strings.Contains(reqItem.ClarifyingPrompt, "Does Alice Walker have experience?") {
		t.Errorf("clarifying prompt failed deobfuscation: %s", reqItem.ClarifyingPrompt)
	}
	if !strings.Contains(reqItem.RecommendedAction, "Review Alice Walker's GitHub alicewalker") {
		t.Errorf("recommended action failed deobfuscation: %s", reqItem.RecommendedAction)
	}
}
