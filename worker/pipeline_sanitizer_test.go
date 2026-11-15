package worker_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/llm"
	"github.com/resumeee-app/resumeee-core/sanitizer"
	"github.com/resumeee-app/resumeee-core/worker"
)

type sanitizerPipelineClient struct {
	mu          sync.Mutex
	capturedReq llm.Request
	resp        core.GenerationResponse
}

func (c *sanitizerPipelineClient) Name() string {
	return llm.ProviderOpenAI
}

func (c *sanitizerPipelineClient) DefaultModel() string {
	return "gpt-4o"
}

func (c *sanitizerPipelineClient) Models() []core.ModelOption {
	return []core.ModelOption{{Provider: llm.ProviderOpenAI, Model: "gpt-4o", DisplayName: "GPT-4o", IsDefault: true}}
}

func (c *sanitizerPipelineClient) SupportsStructuredOutput(_ string) bool {
	return true
}

func (c *sanitizerPipelineClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	c.mu.Lock()
	c.capturedReq = req
	respCopy := c.resp
	c.mu.Unlock()

	raw, err := json.Marshal(respCopy)
	if err != nil {
		return nil, err
	}
	return &llm.Response{
		Text:       string(raw),
		StatusCode: 200,
		Usage:      core.TokenUsage{PromptTokens: 120, CompletionTokens: 80, TotalTokens: 200},
	}, nil
}

func TestWorkerPool_TailoringPipeline_SanitizerDeobfuscation(t *testing.T) {
	ctx := context.Background()
	env := setupTestWorkerEnv(t)

	// Update the profile to have a known authentic candidate identity
	realProf := &core.Profile{
		ID:                 "profile-" + env.defaultUser,
		UserID:             env.defaultUser,
		FullName:           "Margaret Hamilton",
		Email:              "margaret.hamilton@mit.edu",
		Phone:              "+1 (617) 253-1000",
		Headline:           "Director of Software Engineering (Margaret Hamilton)",
		Summary:            "Margaret Hamilton pioneered asynchronous executive software. Reach at margaret.hamilton@mit.edu or +1 (617) 253-1000. Web: https://margarethamilton.org.",
		MainRevision:       2,
		GlobalWritingRules: "Prioritize Margaret Hamilton's mission-critical systems and zero-bug principles.",
		Profiles: []core.SocialLink{
			{Network: "Website", URL: "https://margarethamilton.org"},
			{Network: "GitHub", Username: "mhamilton", URL: "https://github.com/mhamilton"},
		},
	}
	if err := env.profileRepo.UpsertProfile(ctx, realProf); err != nil {
		t.Fatalf("failed to update profile: %v", err)
	}

	exp := core.WorkExperience{
		ID:            "exp-apollo",
		UserID:        env.defaultUser,
		Company:       "MIT Instrumentation Lab",
		RoleTitle:     "Director of Software Engineering",
		StartDate:     core.NewDate(1965, 1, 1),
		DutiesSummary: "Margaret Hamilton led the Apollo flight software division.",
	}
	if err := env.careerRepo.CreateExperience(ctx, &exp); err != nil {
		t.Fatalf("failed to create exp: %v", err)
	}

	fact := core.Fact{
		ID:        "fact-apollo-1",
		UserID:    env.defaultUser,
		OwnerID:   exp.ID,
		OwnerType: core.OwnerTypeWorkExperience,
		Statement: "Margaret Hamilton engineered Apollo guidance flight software.",
	}
	if err := env.careerRepo.CreateFact(ctx, &fact); err != nil {
		t.Fatalf("failed to create fact: %v", err)
	}

	consultExp := core.WorkExperience{
		ID:            "exp-consulting",
		UserID:        env.defaultUser,
		Company:       "Margaret Hamilton Consulting",
		RoleTitle:     "Principal Systems Advisor",
		StartDate:     core.NewDate(1976, 1, 1),
		DutiesSummary: "Margaret Hamilton advised aerospace and fault-tolerant systems clients.",
	}
	if err := env.careerRepo.CreateExperience(ctx, &consultExp); err != nil {
		t.Fatalf("failed to create consulting exp: %v", err)
	}

	consultFact := core.Fact{
		ID:        "fact-consulting-1",
		UserID:    env.defaultUser,
		OwnerID:   consultExp.ID,
		OwnerType: core.OwnerTypeWorkExperience,
		Statement: "Margaret Hamilton designed formal verification tooling for mission software.",
	}
	if err := env.careerRepo.CreateFact(ctx, &consultFact); err != nil {
		t.Fatalf("failed to create consulting fact: %v", err)
	}

	eduStart := core.NewDate(1954, 9, 1)
	eduEnd := core.NewDate(1958, 5, 1)
	edu := core.Education{
		ID:          "edu-earlham",
		UserID:      env.defaultUser,
		Institution: "Earlham College",
		Degree:      "B.A.",
		Field:       "Mathematics",
		StartDate:   &eduStart,
		EndDate:     &eduEnd,
	}
	if err := env.careerRepo.CreateEducation(ctx, &edu); err != nil {
		t.Fatalf("failed to create education: %v", err)
	}

	eduFact := core.Fact{
		ID:        "fact-edu-earlham-1",
		UserID:    env.defaultUser,
		OwnerID:   edu.ID,
		OwnerType: core.OwnerTypeEducation,
		Statement: "Margaret Hamilton authored mathematics research archived at https://margarethamilton.org.",
	}
	if err := env.careerRepo.CreateFact(ctx, &eduFact); err != nil {
		t.Fatalf("failed to create education fact: %v", err)
	}

	cert := core.Certification{
		ID:      "cert-apollo",
		UserID:  env.defaultUser,
		Name:    "Apollo Flight Ops",
		Issuer:  "NASA",
		URL:     "https://margarethamilton.org/cert",
		Summary: "Issued to Margaret Hamilton.",
	}
	if err := env.careerRepo.CreateCertification(ctx, &cert); err != nil {
		t.Fatalf("failed to create certification: %v", err)
	}

	// Transport client returns ONLY synthetic placeholders (simulating an external LLM echoing masked inputs).
	transportClient := &sanitizerPipelineClient{
		resp: core.GenerationResponse{
			TailoredContent: core.TailoredContent{
				Summary: "Pioneering software executive " + sanitizer.PlaceholderFullName + " with expertise in fault-tolerant asynchronous execution. Contact: " + sanitizer.PlaceholderEmail + " or " + sanitizer.PlaceholderPhone + ". Portfolio: " + sanitizer.PlaceholderWebsite + ". GitHub: candidate-social-2.",
				Experiences: []core.TailoredExperience{
					{
						SourceEntityID: exp.EffectiveShortID(),
						Company:        "MIT Instrumentation Lab",
						RoleTitle:      "Director of Software Engineering",
						StartDate:      core.NewDate(1965, 1, 1),
						DutiesSummary:  sanitizer.PlaceholderFullName + " designed Apollo guidance flight software.",
						Bullets: []core.TailoredBullet{
							{
								Text:          sanitizer.PlaceholderFullName + " engineered priority display and asynchronous recovery.",
								SourceFactIDs: []string{fact.EffectiveShortID()},
							},
						},
					},
					{
						SourceEntityID: consultExp.EffectiveShortID(),
						Company:        sanitizer.PlaceholderFullName + " Consulting",
						RoleTitle:      "Principal Systems Advisor",
						StartDate:      core.NewDate(1976, 1, 1),
						DutiesSummary:  sanitizer.PlaceholderFullName + " advised fault-tolerant systems clients.",
						Bullets: []core.TailoredBullet{
							{
								Text:          sanitizer.PlaceholderFullName + " built formal verification tooling.",
								SourceFactIDs: []string{consultFact.EffectiveShortID()},
							},
						},
					},
				},
				Education: []core.TailoredEducation{
					{
						SourceEntityID: edu.EffectiveShortID(),
						Institution:    "Earlham College",
						Degree:         "B.A.",
						Field:          "Mathematics",
						Bullets: []core.TailoredBullet{
							{
								Text:          sanitizer.PlaceholderFullName + " authored mathematics research at " + sanitizer.PlaceholderWebsite + ".",
								SourceFactIDs: []string{eduFact.EffectiveShortID()},
							},
						},
					},
				},
			},
			GapReport: core.GapReport{
				OverallMatchSummary: sanitizer.PlaceholderFullName + " exceeds all mission-critical reliability criteria.",
				StrategicRecommendations: []string{
					sanitizer.PlaceholderFullName + " should highlight real-time operating systems at " + sanitizer.PlaceholderWebsite + ".",
				},
			},
		},
	}

	reg := llm.NewRegistry()
	reg.Register(transportClient)
	svc := llm.NewService(reg)

	pool := worker.New(worker.Config{
		Concurrency: 1,
		Branches:    env.branchRepo,
		Profiles:    env.profileRepo,
		Presets:     env.presetRepo,
		Templates:   env.templateRepo,
		Generator:   svc,
		Validator:   env.validator,
		Renderer:    env.renderer,
	})

	pool.Start(ctx)
	defer pool.Stop()

	// Create and submit version
	expiresAt := time.Now().UTC().Add(core.DefaultJobTTL)
	ver := core.BranchVersion{
		ID:                "ver-sanitizer-test",
		UserID:            env.defaultUser,
		BranchID:          env.branchID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  2,
		TemplateSetIDUsed: "builtin-standard-clean",
		SourceType:        core.SourceTypeLLMGeneration,
		ExpiresAt:         &expiresAt,
	}
	if err := env.branchRepo.CreateVersion(ctx, &ver); err != nil {
		t.Fatalf("failed to create version: %v", err)
	}
	pool.Notify()

	// Await completion
	var completedVer *core.BranchVersion
	for range 50 {
		time.Sleep(20 * time.Millisecond)
		v, err := env.branchRepo.Version(ctx, env.branchID, ver.ID)
		if err == nil && (v.Status == core.VersionStatusCompleted || v.Status == core.VersionStatusCompletedWithWarnings || v.Status == core.VersionStatusFailed) {
			completedVer = v
			break
		}
	}

	if completedVer == nil || (completedVer.Status != core.VersionStatusCompleted && completedVer.Status != core.VersionStatusCompletedWithWarnings) {
		t.Fatalf("expected version completed or completed_with_warnings, got %+v", completedVer)
	}

	// 1. Verify outbound LLM transport wire scrubbed all real candidate PII to deterministic placeholders
	transportClient.mu.Lock()
	outboundWire := transportClient.capturedReq.SystemPrompt + "\n" + transportClient.capturedReq.UserPrompt
	transportClient.mu.Unlock()

	for _, leaked := range []string{
		"Margaret Hamilton",
		"margaret.hamilton@mit.edu",
		"+1 (617) 253-1000",
		"https://margarethamilton.org",
		"https://margarethamilton.org/cert",
		"https://github.com/mhamilton",
		"mhamilton",
	} {
		if strings.Contains(outboundWire, leaked) {
			t.Errorf("outbound LLM transport prompt leaked authentic PII %q", leaked)
		}
	}
	for _, mask := range []string{
		sanitizer.PlaceholderFullName,
		sanitizer.PlaceholderFullName + " Consulting",
		sanitizer.PlaceholderEmail,
		sanitizer.PlaceholderPhone,
		sanitizer.PlaceholderWebsite,
		sanitizer.PlaceholderWebsite + "/cert",
		"Issued to " + sanitizer.PlaceholderFullName,
	} {
		if !strings.Contains(outboundWire, mask) {
			t.Errorf("outbound LLM transport prompt missing expected placeholder %q", mask)
		}
	}

	// 2. Verify stored TailoredContent has all placeholders restored to authentic candidate details
	for _, placeholder := range []string{
		sanitizer.PlaceholderFullName,
		sanitizer.PlaceholderEmail,
		sanitizer.PlaceholderPhone,
		sanitizer.PlaceholderWebsite,
		"candidate-social-2",
	} {
		if strings.Contains(completedVer.TailoredContent.Summary, placeholder) {
			t.Errorf("version TailoredContent.Summary retained placeholder %q: %s", placeholder, completedVer.TailoredContent.Summary)
		}
	}
	for _, authentic := range []string{
		"Margaret Hamilton",
		"margaret.hamilton@mit.edu",
		"+1 (617) 253-1000",
		"https://margarethamilton.org",
		"mhamilton",
	} {
		if !strings.Contains(completedVer.TailoredContent.Summary, authentic) {
			t.Errorf("version TailoredContent.Summary missing authentic value %q: %s", authentic, completedVer.TailoredContent.Summary)
		}
	}

	if !strings.HasPrefix(completedVer.TailoredContent.Experiences[0].DutiesSummary, "Margaret Hamilton designed") {
		t.Errorf("version DutiesSummary failed deobfuscation: %q", completedVer.TailoredContent.Experiences[0].DutiesSummary)
	}
	bulletText := completedVer.TailoredContent.Experiences[0].Bullets[0].Text
	if !strings.HasPrefix(bulletText, "Margaret Hamilton engineered") {
		t.Errorf("version bullet text failed deobfuscation: %q", bulletText)
	}

	var foundConsulting *core.TailoredExperience
	for i := range completedVer.TailoredContent.Experiences {
		if completedVer.TailoredContent.Experiences[i].SourceEntityID == consultExp.ID {
			foundConsulting = &completedVer.TailoredContent.Experiences[i]
			break
		}
	}
	if foundConsulting == nil {
		t.Fatalf("expected consulting experience %q in validated TailoredContent", consultExp.ID)
	}
	if foundConsulting.Company != "Margaret Hamilton Consulting" {
		t.Errorf("consulting Company failed provenance reconciliation: got %q, want %q", foundConsulting.Company, "Margaret Hamilton Consulting")
	}

	if len(completedVer.TailoredContent.Education) == 0 || len(completedVer.TailoredContent.Education[0].Bullets) == 0 {
		t.Fatalf("expected education bullet in validated TailoredContent, got %+v", completedVer.TailoredContent.Education)
	}
	eduBullet := completedVer.TailoredContent.Education[0].Bullets[0].Text
	for _, placeholder := range []string{
		sanitizer.PlaceholderFullName,
		sanitizer.PlaceholderEmail,
		sanitizer.PlaceholderPhone,
		sanitizer.PlaceholderWebsite,
		"candidate-social-2",
	} {
		if strings.Contains(eduBullet, placeholder) {
			t.Errorf("education Bullets[0].Text retained placeholder %q: %q", placeholder, eduBullet)
		}
	}
	if !strings.Contains(eduBullet, "Margaret Hamilton") || !strings.Contains(eduBullet, "https://margarethamilton.org") {
		t.Errorf("education Bullets[0].Text failed deobfuscation: %q", eduBullet)
	}

	// 3. Verify GapReport contains authentic details and zero placeholders
	if strings.Contains(completedVer.GapReport.OverallMatchSummary, sanitizer.PlaceholderFullName) ||
		!strings.Contains(completedVer.GapReport.OverallMatchSummary, "Margaret Hamilton") {
		t.Errorf("version GapReport OverallMatchSummary failed deobfuscation: %q", completedVer.GapReport.OverallMatchSummary)
	}
	if len(completedVer.GapReport.StrategicRecommendations) == 0 ||
		!strings.Contains(completedVer.GapReport.StrategicRecommendations[0], "Margaret Hamilton") ||
		!strings.Contains(completedVer.GapReport.StrategicRecommendations[0], "https://margarethamilton.org") {
		t.Errorf("version GapReport StrategicRecommendations failed deobfuscation: %+v", completedVer.GapReport.StrategicRecommendations)
	}

	// 4. Verify Rendered outputs are unobfuscated and show authentic contact details
	assertRendered := func(format, content string) {
		for _, placeholder := range []string{
			sanitizer.PlaceholderFullName,
			sanitizer.PlaceholderEmail,
			sanitizer.PlaceholderPhone,
			sanitizer.PlaceholderWebsite,
			"candidate-social-2",
		} {
			if strings.Contains(content, placeholder) {
				t.Errorf("%s retained placeholder %q", format, placeholder)
			}
		}
		if !strings.Contains(content, "Margaret Hamilton") {
			t.Errorf("%s missing authentic name 'Margaret Hamilton'", format)
		}
		if !strings.Contains(content, "Margaret Hamilton Consulting") {
			t.Errorf("%s missing canonical consulting company 'Margaret Hamilton Consulting'", format)
		}
		if !strings.Contains(content, "margaret.hamilton@mit.edu") {
			t.Errorf("%s missing authentic email 'margaret.hamilton@mit.edu'", format)
		}
	}

	assertRendered("HTMLOutput", completedVer.HTMLOutput)
	assertRendered("MarkdownOutput", completedVer.MarkdownOutput)
	assertRendered("PlainTextOutput", completedVer.PlainTextOutput)
}
