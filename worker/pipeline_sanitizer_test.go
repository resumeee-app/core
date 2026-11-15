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
				Summary: "Pioneering software executive " + sanitizer.PlaceholderFullName + " with expertise in fault-tolerant asynchronous execution. Contact: " + sanitizer.PlaceholderEmail + " or " + sanitizer.PlaceholderPhone + ". Portfolio: " + sanitizer.PlaceholderWebsite + ". GitHub: " + sanitizer.PlaceholderSocialHandle(2) + ".",
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
		Jobs:        env.jobRepo,
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
		MainRevisionUsed:  2,
		TemplateSetIDUsed: "builtin-standard-clean",
		SourceType:        core.SourceTypeLLMGeneration,
	}
	job := core.Job{
		ID:        "job-sanitizer-test",
		Kind:      core.JobKindBranchTailor,
		UserID:    env.defaultUser,
		ExpiresAt: &expiresAt,
		Tailor: &core.BranchTailorSpec{
			BranchID:  env.branchID,
			VersionID: ver.ID,
		},
	}
	if _, err := env.jobRepo.SubmitBranchTailor(ctx, &job, &ver, core.PendingJobOptions{}); err != nil {
		t.Fatalf("failed to submit pending version: %v", err)
	}
	pool.Notify()

	completedVer := awaitCompletedVersion(ctx, t, env.branchRepo, env.branchID, ver.ID)

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
		sanitizer.PlaceholderSocialHandle(2),
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
		sanitizer.PlaceholderSocialHandle(2),
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
			sanitizer.PlaceholderSocialHandle(2),
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

// awaitCompletedVersion polls store until versionID reaches a terminal completed status or fails the test.
func awaitCompletedVersion(ctx context.Context, t *testing.T, store worker.BranchReader, branchID, versionID string) *core.BranchVersion {
	t.Helper()
	for range 50 {
		time.Sleep(20 * time.Millisecond)
		v, err := store.Version(ctx, branchID, versionID)
		if err == nil && (v.Status == core.VersionStatusCompleted || v.Status == core.VersionStatusCompletedWithWarnings || v.Status == core.VersionStatusFailed) {
			if v.Status == core.VersionStatusFailed {
				t.Fatalf("expected version completed or completed_with_warnings, got failed: %+v", v)
			}
			return v
		}
	}
	t.Fatalf("timed out waiting for version %q to complete", versionID)
	return nil
}

// setupNameEmbeddedCompanyEnv seeds a Spanish-language branch and career records embedding realCompany.
func setupNameEmbeddedCompanyEnv(ctx context.Context, t *testing.T, realName, realCompany string) (*testWorkerEnv, core.WorkExperience, core.Fact) {
	t.Helper()
	env := setupTestWorkerEnv(t)

	preset := &core.Preset{
		ID:                 "preset-es-consulting",
		UserID:             env.defaultUser,
		Name:               "Spanish Executive",
		Language:           "Spanish",
		SystemInstructions: "Redacta logros técnicos con claridad.",
	}
	if err := env.presetRepo.CreatePreset(ctx, preset); err != nil {
		t.Fatalf("failed to create preset: %v", err)
	}
	b, err := env.branchRepo.Branch(ctx, env.defaultUser, env.branchID)
	if err != nil {
		t.Fatalf("failed to load branch: %v", err)
	}
	b.PresetID = &preset.ID
	if err := env.branchRepo.UpdateBranch(ctx, b); err != nil {
		t.Fatalf("failed to attach preset to branch: %v", err)
	}

	realProf := &core.Profile{
		ID:           "profile-" + env.defaultUser,
		UserID:       env.defaultUser,
		FullName:     realName,
		Email:        "jane.doe@example.org",
		Phone:        "+1 (415) 555-0199",
		Headline:     "Arquitecta de Sistemas (" + realName + ")",
		Summary:      realName + " fundó " + realCompany + " para diseñar plataformas resilientes.",
		MainRevision: 2,
	}
	if err := env.profileRepo.UpsertProfile(ctx, realProf); err != nil {
		t.Fatalf("failed to update profile: %v", err)
	}

	consultExp := core.WorkExperience{
		ID:            "exp-jane-consulting",
		UserID:        env.defaultUser,
		Company:       realCompany,
		RoleTitle:     "Arquitecta Principal",
		StartDate:     core.NewDate(2020, 1, 1),
		DutiesSummary: realName + " dirigió proyectos en la nube en " + realCompany + ".",
	}
	if err := env.careerRepo.CreateExperience(ctx, &consultExp); err != nil {
		t.Fatalf("failed to create consulting experience: %v", err)
	}

	consultFact := core.Fact{
		ID:        "fact-jane-consulting-1",
		UserID:    env.defaultUser,
		OwnerID:   consultExp.ID,
		OwnerType: core.OwnerTypeWorkExperience,
		Statement: realName + " diseñó arquitectura tolerante a fallos para empresas en " + realCompany + ".",
	}
	if err := env.careerRepo.CreateFact(ctx, &consultFact); err != nil {
		t.Fatalf("failed to create consulting fact: %v", err)
	}

	return env, consultExp, consultFact
}

// runSanitizerPipelineVersion executes a single tailoring job through worker.Pool and returns the completed version and outbound wire prompt.
func runSanitizerPipelineVersion(ctx context.Context, t *testing.T, env *testWorkerEnv, client *sanitizerPipelineClient, versionID string) (*core.BranchVersion, string) {
	t.Helper()
	reg := llm.NewRegistry()
	reg.Register(client)
	svc := llm.NewService(reg)

	pool := worker.New(worker.Config{
		Concurrency:  1,
		PollInterval: 10 * time.Millisecond,
		Jobs:         env.jobRepo,
		Branches:     env.branchRepo,
		Profiles:     env.profileRepo,
		Presets:      env.presetRepo,
		Templates:    env.templateRepo,
		Generator:    svc,
		Validator:    env.validator,
		Renderer:     env.renderer,
	})

	pool.Start(ctx)
	defer pool.Stop()

	now := time.Now()
	ver := core.BranchVersion{
		ID:                versionID,
		BranchID:          env.branchID,
		UserID:            env.defaultUser,
		MainRevisionUsed:  2,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		SourceType:        core.SourceTypeLLMGeneration,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	job := core.Job{
		ID:     "job-" + versionID,
		Kind:   core.JobKindBranchTailor,
		UserID: env.defaultUser,
		Tailor: &core.BranchTailorSpec{
			BranchID:  env.branchID,
			VersionID: ver.ID,
		},
	}
	if _, err := env.jobRepo.SubmitBranchTailor(ctx, &job, &ver, core.PendingJobOptions{}); err != nil {
		t.Fatalf("failed to submit pending version: %v", err)
	}
	pool.Notify()

	completedVer := awaitCompletedVersion(ctx, t, env.branchRepo, env.branchID, ver.ID)

	client.mu.Lock()
	outboundWire := client.capturedReq.SystemPrompt + "\n" + client.capturedReq.UserPrompt
	client.mu.Unlock()
	return completedVer, outboundWire
}

// assertNameEmbeddedCompanyResult verifies outbound PII scrubbing and end-to-end restoration of realCompany across stored and rendered outputs.
func assertNameEmbeddedCompanyResult(t *testing.T, completedVer *core.BranchVersion, outboundWire, realName, realCompany, email, phone string) {
	t.Helper()
	maskedCompany := sanitizer.PlaceholderFullName + " Consulting"

	for _, leaked := range []string{realName, realCompany, email, phone} {
		if strings.Contains(outboundWire, leaked) {
			t.Errorf("outbound LLM transport prompt leaked authentic PII %q", leaked)
		}
	}
	for _, expected := range []string{"Target Output Language: Spanish", maskedCompany} {
		if !strings.Contains(outboundWire, expected) {
			t.Errorf("outbound LLM transport prompt missing %q", expected)
		}
	}

	wantSummary := "Arquitecta principal en " + realCompany + " especializada en sistemas distribuidos."
	if completedVer.TailoredContent.Summary != wantSummary {
		t.Errorf("TailoredContent.Summary = %q, want %q", completedVer.TailoredContent.Summary, wantSummary)
	}
	if len(completedVer.TailoredContent.Experiences) != 1 {
		t.Fatalf("expected 1 tailored experience, got %d", len(completedVer.TailoredContent.Experiences))
	}
	gotExp := completedVer.TailoredContent.Experiences[0]
	if gotExp.Company != realCompany {
		t.Errorf("Experience.Company = %q, want %q", gotExp.Company, realCompany)
	}
	wantDuties := realName + " lideró entregas críticas en " + realCompany + "."
	if gotExp.DutiesSummary != wantDuties {
		t.Errorf("Experience.DutiesSummary = %q, want %q", gotExp.DutiesSummary, wantDuties)
	}
	if len(gotExp.Bullets) != 1 {
		t.Fatalf("expected 1 tailored bullet, got %d", len(gotExp.Bullets))
	}
	wantBullet := "Diseñó arquitectura tolerante a fallos en " + realCompany + "."
	if gotExp.Bullets[0].Text != wantBullet {
		t.Errorf("Experience.Bullets[0].Text = %q, want %q", gotExp.Bullets[0].Text, wantBullet)
	}

	wantGapSummary := realName + " aporta liderazgo comprobado en " + realCompany + "."
	if completedVer.GapReport.OverallMatchSummary != wantGapSummary {
		t.Errorf("GapReport.OverallMatchSummary = %q, want %q", completedVer.GapReport.OverallMatchSummary, wantGapSummary)
	}
	wantGapRec := "Destacar proyectos de resiliencia ejecutados en " + realCompany + "."
	if len(completedVer.GapReport.StrategicRecommendations) != 1 || completedVer.GapReport.StrategicRecommendations[0] != wantGapRec {
		t.Errorf("GapReport.StrategicRecommendations = %+v, want [%q]", completedVer.GapReport.StrategicRecommendations, wantGapRec)
	}

	for _, rendered := range []struct {
		name    string
		content string
	}{
		{"HTMLOutput", completedVer.HTMLOutput},
		{"MarkdownOutput", completedVer.MarkdownOutput},
		{"PlainTextOutput", completedVer.PlainTextOutput},
	} {
		if strings.Contains(rendered.content, sanitizer.PlaceholderFullName) {
			t.Errorf("%s retained placeholder %q", rendered.name, sanitizer.PlaceholderFullName)
		}
		if !strings.Contains(rendered.content, realCompany) {
			t.Errorf("%s missing restored company %q", rendered.name, realCompany)
		}
		if !strings.Contains(rendered.content, wantBullet) {
			t.Errorf("%s missing restored translated bullet %q", rendered.name, wantBullet)
		}
	}
}

func TestWorkerPool_TailoringPipeline_NameEmbeddedCompanyTranslatedNarrative(t *testing.T) {
	ctx := context.Background()
	realName := "Jane Doe"
	realCompany := "Jane Doe Consulting"
	env, consultExp, consultFact := setupNameEmbeddedCompanyEnv(ctx, t, realName, realCompany)

	maskedCompany := sanitizer.PlaceholderFullName + " Consulting"
	transportClient := &sanitizerPipelineClient{
		resp: core.GenerationResponse{
			TailoredContent: core.TailoredContent{
				Summary: "Arquitecta principal en " + maskedCompany + " especializada en sistemas distribuidos.",
				Experiences: []core.TailoredExperience{
					{
						SourceEntityID: consultExp.EffectiveShortID(),
						Company:        maskedCompany,
						RoleTitle:      "Arquitecta Principal",
						StartDate:      core.NewDate(2020, 1, 1),
						DutiesSummary:  sanitizer.PlaceholderFullName + " lideró entregas críticas en " + maskedCompany + ".",
						Bullets: []core.TailoredBullet{
							{
								Text:          "Diseñó arquitectura tolerante a fallos en " + maskedCompany + ".",
								SourceFactIDs: []string{consultFact.EffectiveShortID()},
							},
						},
					},
				},
			},
			GapReport: core.GapReport{
				OverallMatchSummary: sanitizer.PlaceholderFullName + " aporta liderazgo comprobado en " + maskedCompany + ".",
				StrategicRecommendations: []string{
					"Destacar proyectos de resiliencia ejecutados en " + maskedCompany + ".",
				},
			},
		},
	}

	completedVer, outboundWire := runSanitizerPipelineVersion(ctx, t, env, transportClient, "ver-name-embedded-company")
	assertNameEmbeddedCompanyResult(t, completedVer, outboundWire, realName, realCompany, "jane.doe@example.org", "+1 (415) 555-0199")
}
