package llm_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/llm"
	"github.com/resumeee-app/resumeee-core/sanitizer"
)

type mockSanitizerClient struct {
	name         string
	defaultModel string
	tailoredResp *core.GenerationResponse
	completeText string
	completeFn   func(req llm.Request) (*llm.Response, error)
	requests     []llm.Request
}

func (m *mockSanitizerClient) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock-sanitizer"
}

func (m *mockSanitizerClient) DefaultModel() string {
	if m.defaultModel != "" {
		return m.defaultModel
	}
	return "mock-model"
}

func (m *mockSanitizerClient) Models() []core.ModelOption {
	return nil
}

func (m *mockSanitizerClient) SupportsStructuredOutput(_ string) bool {
	return false
}

func (m *mockSanitizerClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	m.requests = append(m.requests, req)
	if m.completeFn != nil {
		return m.completeFn(req)
	}
	if m.tailoredResp != nil && (req.SchemaName == "tailored_resume_response" || strings.Contains(req.SystemPrompt, "\"tailored_content\"")) {
		copyResp := *m.tailoredResp
		raw, err := json.Marshal(&copyResp)
		if err != nil {
			return nil, err
		}
		return &llm.Response{Text: string(raw), StatusCode: 200}, nil
	}
	if m.completeText != "" {
		return &llm.Response{Text: m.completeText, StatusCode: 200}, nil
	}
	return &llm.Response{Text: `{"gap_report":{"overall_match_summary":"Candidate matches criteria","missing_requirements":[]}}`, StatusCode: 200}, nil
}

func TestService_GenerateTailoredResume_DeobfuscationSeam(t *testing.T) {
	mockProv := &mockSanitizerClient{
		name:         "openai",
		defaultModel: "gpt-4o",
		tailoredResp: &core.GenerationResponse{
			TailoredContent: core.TailoredContent{
				Summary: "Results-driven engineer " + sanitizer.PlaceholderFullName + " reachable at " + sanitizer.PlaceholderEmail + " or " + sanitizer.PlaceholderPhone + ". Portfolio: " + sanitizer.PlaceholderWebsite + ". Handle: " + sanitizer.PlaceholderSocialHandle(1) + ".",
				Experiences: []core.TailoredExperience{
					{
						Company:       "Acme",
						DutiesSummary: sanitizer.PlaceholderFullName + " led distributed infrastructure.",
						Bullets: []core.TailoredBullet{
							{Text: sanitizer.PlaceholderFullName + " scaled throughput by 300%."},
						},
					},
				},
				Education: []core.TailoredEducation{
					{
						SourceEntityID: "edu-1",
						Institution:    "MIT",
						Bullets: []core.TailoredBullet{
							{Text: sanitizer.PlaceholderFullName + " authored distributed systems research at " + sanitizer.PlaceholderWebsite + "."},
						},
					},
				},
				Certifications: []core.TailoredCert{
					{
						SourceEntityID: "cert-1",
						Name:           "CKA",
						Summary:        "Awarded to " + sanitizer.PlaceholderFullName + ".",
					},
				},
			},
			GapReport: core.GapReport{
				OverallMatchSummary: sanitizer.PlaceholderFullName + " satisfies all requirements.",
			},
		},
	}

	reg := llm.NewRegistry()
	reg.Register(mockProv)
	svc := llm.NewService(reg)

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
			Education: []core.Education{
				{
					ID:          "edu-1",
					ShortID:     "edu-1",
					Institution: "MIT",
					Degree:      "M.S.",
					Field:       "Computer Science",
				},
			},
			Certifications: []core.Certification{
				{
					ID:      "cert-1",
					ShortID: "cert-1",
					Name:    "CKA",
					Issuer:  "CNCF",
				},
			},
		},
	}

	resp, err := svc.GenerateTailoredResume(context.Background(), req)
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
	if len(resp.TailoredContent.Education) == 0 || len(resp.TailoredContent.Education[0].Bullets) == 0 {
		t.Fatalf("expected education bullet in tailored response, got %+v", resp.TailoredContent.Education)
	}
	eduBullet := resp.TailoredContent.Education[0].Bullets[0].Text
	if strings.Contains(eduBullet, sanitizer.PlaceholderFullName) || strings.Contains(eduBullet, sanitizer.PlaceholderWebsite) {
		t.Errorf("education bullet retained placeholder: %s", eduBullet)
	}
	wantEduBullet := "Alice Walker authored distributed systems research at https://alice.dev."
	if eduBullet != wantEduBullet {
		t.Errorf("education bullet failed deobfuscation: got %q, want %q", eduBullet, wantEduBullet)
	}
	if len(resp.TailoredContent.Certifications) == 0 {
		t.Fatalf("expected certification in tailored response, got %+v", resp.TailoredContent.Certifications)
	}
	certSummary := resp.TailoredContent.Certifications[0].Summary
	wantCertSummary := "Awarded to Alice Walker."
	if certSummary != wantCertSummary {
		t.Errorf("certification summary failed deobfuscation: got %q, want %q", certSummary, wantCertSummary)
	}
	if !strings.Contains(resp.GapReport.OverallMatchSummary, "Alice Walker satisfies") {
		t.Errorf("gap report failed deobfuscation: %s", resp.GapReport.OverallMatchSummary)
	}
}

func TestService_GenerateGapAnalysis_DeobfuscationSeam(t *testing.T) {
	mockProv := &mockSanitizerClient{
		name:         "openai",
		defaultModel: "gpt-4o",
		completeText: `{"gap_report":{"overall_match_summary":"` + sanitizer.PlaceholderFullName + ` demonstrates strong capability.","missing_requirements":[{"requirement":"Kubernetes","missing_area":"Cloud","clarifying_prompt":"Does ` + sanitizer.PlaceholderFullName + ` have experience?","recommended_action":"Review ` + sanitizer.PlaceholderFullName + `'s GitHub ` + sanitizer.PlaceholderSocialHandle(1) + `"}]}}`,
	}

	reg := llm.NewRegistry()
	reg.Register(mockProv)
	svc := llm.NewService(reg)

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

	resp, err := svc.GenerateGapAnalysis(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil gap analysis response")
	}
	report := resp.GapReport

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

func TestService_SimulateATS_DeobfuscationSeam(t *testing.T) {
	mockProv := &mockSanitizerClient{
		name:         "openai",
		defaultModel: "gpt-4o",
	}
	mockProv.completeFn = func(req llm.Request) (*llm.Response, error) {
		if strings.Contains(req.SystemPrompt, `"extraction_confidence_score"`) {
			return &llm.Response{
				Text: `{
					"extraction_confidence_score": 0.94,
					"extraction_summary": "Parsed profile for ` + sanitizer.PlaceholderFullName + `.",
					"extracted_candidate": {
						"summary": "` + sanitizer.PlaceholderFullName + ` leads Go platform teams.",
						"skills": ["Go", "Kubernetes"],
						"experiences": [
							{
								"company": "` + sanitizer.PlaceholderFullName + ` Consulting",
								"title": "Principal Consultant",
								"date_range": "2021 - Present",
								"highlights": ["Built systems for ` + sanitizer.PlaceholderFullName + ` clients."]
							},
							{
								"company": "CloudScale",
								"title": "Staff Engineer",
								"date_range": "2018 - 2021",
								"highlights": ["Scaled distributed storage."]
							}
						],
						"educations": []
					}
				}`,
				StatusCode: 200,
			}, nil
		}
		return &llm.Response{
			Text: `{
				"job_matching_score": 0.88,
				"matching_summary": "` + sanitizer.PlaceholderFullName + ` matches Go and Kubernetes requirements."
			}`,
			StatusCode: 200,
		}, nil
	}

	reg := llm.NewRegistry()
	reg.Register(mockProv)
	svc := llm.NewService(reg)

	req := core.ATSSimulationRequest{
		FormatUsed: string(core.OutputFormatMarkdown),
		ResumeText: "# Alice Walker\n## Experience\n- Alice Walker Consulting (2021 - Present)\n- CloudScale (2018 - 2021)",
		BranchContext: core.Branch{
			ProviderOverride:   "openai",
			ModelOverride:      "gpt-4o",
			TargetRole:         "Staff Go Engineer",
			TargetCompany:      "CloudScale",
			TargetRequirements: "Go and Kubernetes production systems.",
		},
		MainProfile: &core.MainProfileSnapshot{
			Profile: core.Profile{
				FullName: "Alice Walker",
				Email:    "alice.walker@example.org",
			},
		},
	}

	resp, err := svc.SimulateATS(context.Background(), req)
	if err != nil {
		t.Fatalf("SimulateATS failed: %v", err)
	}
	if len(mockProv.requests) != 2 {
		t.Fatalf("expected 2 LLM requests (extract + match), got %d", len(mockProv.requests))
	}
	for i, sent := range mockProv.requests {
		if strings.Contains(sent.UserPrompt, "Alice Walker") {
			t.Errorf("outbound ATS request %d leaked candidate name:\n%s", i, sent.UserPrompt)
		}
	}
	if !strings.Contains(mockProv.requests[1].UserPrompt, sanitizer.PlaceholderFullName+" Consulting") {
		t.Errorf("Stage 2 prompt missing scrubbed company placeholder:\n%s", mockProv.requests[1].UserPrompt)
	}

	if resp.Score.ExtractedCandidate == nil || len(resp.Score.ExtractedCandidate.Experiences) != 2 {
		t.Fatalf("expected 2 extracted experiences, got %+v", resp.Score.ExtractedCandidate)
	}
	if got := resp.Score.ExtractedCandidate.Experiences[0].Company; got != "Alice Walker Consulting" {
		t.Errorf("ExtractedCandidate.Experiences[0].Company = %q, want %q", got, "Alice Walker Consulting")
	}
	if got := resp.Score.ExtractedCandidate.Experiences[1].Company; got != "CloudScale" {
		t.Errorf("ExtractedCandidate.Experiences[1].Company = %q, want %q", got, "CloudScale")
	}
	if resp.Score.ExtractionSummary != "Parsed profile for Alice Walker." {
		t.Errorf("ExtractionSummary = %q, want restored candidate name", resp.Score.ExtractionSummary)
	}
	if resp.Score.MatchingSummary != "Alice Walker matches Go and Kubernetes requirements." {
		t.Errorf("MatchingSummary = %q, want restored candidate name", resp.Score.MatchingSummary)
	}
}
