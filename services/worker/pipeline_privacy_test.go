package worker_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core/domain"
	"github.com/resumeee-app/resumeee-core/services/worker"
)

func TestWorkerPool_TailoringPipeline_PrivacyDeobfuscation(t *testing.T) {
	ctx := context.Background()
	env := setupTestWorkerEnv(t)

	// Update the profile to have a known authentic candidate identity
	realProf := &domain.Profile{
		ID:                 "profile-" + env.defaultUser,
		UserID:             env.defaultUser,
		FullName:           "Margaret Hamilton",
		Email:              "margaret.hamilton@mit.edu",
		Phone:              "+1 (617) 253-1000",
		Headline:           "Director of Software Engineering",
		Summary:            "Margaret Hamilton pioneered asynchronous executive software.",
		MainRevision:       2,
		GlobalWritingRules: "Prioritize mission-critical systems and zero-bug principles.",
		Profiles: []domain.SocialLink{
			{Network: "Website", URL: "https://margarethamilton.org"},
			{Network: "GitHub", Username: "mhamilton", URL: "https://github.com/mhamilton"},
		},
	}
	if err := env.profileRepo.UpsertProfile(ctx, realProf); err != nil {
		t.Fatalf("failed to update profile: %v", err)
	}

	exp := domain.WorkExperience{
		ID:        "exp-apollo",
		UserID:    env.defaultUser,
		Company:   "MIT Instrumentation Lab",
		RoleTitle: "Director of Software Engineering",
		StartDate: domain.NewDate(1965, 1, 1),
	}
	if err := env.profileRepo.CreateExperience(ctx, &exp); err != nil {
		t.Fatalf("failed to create exp: %v", err)
	}

	fact := domain.Fact{
		ID:        "fact-apollo-1",
		UserID:    env.defaultUser,
		OwnerID:   exp.ID,
		OwnerType: domain.OwnerTypeWorkExperience,
		Statement: "Engineered Apollo guidance flight software.",
	}
	if err := env.profileRepo.CreateFact(ctx, &fact); err != nil {
		t.Fatalf("failed to create fact: %v", err)
	}

	expID := exp.ID
	factID := fact.ID

	// LLM Service returns tailored content and gap report with candidate details
	env.llm.mu.Lock()
	env.llm.customResp = &domain.GenerationResponse{
		TailoredContent: domain.TailoredContent{
			Summary: "Pioneering software executive Margaret Hamilton with expertise in fault-tolerant asynchronous execution. Contact: margaret.hamilton@mit.edu or +1 (617) 253-1000. Portfolio: https://margarethamilton.org.",
			Experiences: []domain.TailoredExperience{
				{
					SourceEntityID: expID,
					Company:        "MIT Instrumentation Lab",
					RoleTitle:      "Director (Margaret Hamilton)",
					StartDate:      domain.NewDate(1965, 1, 1),
					DutiesSummary:  "Margaret Hamilton designed Apollo guidance flight software.",
					Bullets: []domain.TailoredBullet{
						{
							Text:          "Margaret Hamilton engineered priority display and asynchronous recovery.",
							SourceFactIDs: []string{factID},
						},
					},
				},
			},
		},
		GapReport: domain.GapReport{
			OverallMatchSummary: "Margaret Hamilton exceeds all mission-critical reliability criteria.",
			StrategicRecommendations: []string{
				"Margaret Hamilton should highlight real-time operating systems.",
			},
		},
	}
	env.llm.mu.Unlock()

	pool := worker.NewPool(worker.PoolConfig{
		Concurrency:  1,
		QueueSize:    10,
		BranchRepo:   env.branchRepo,
		ProfileRepo:  env.profileRepo,
		PresetRepo:   env.presetRepo,
		TemplateRepo: env.templateRepo,
		LLMService:   env.llm,
		Validator:    env.validator,
		Renderer:     env.renderer,
	})

	pool.Start(ctx)
	defer pool.Stop()

	// Create and enqueue version
	ver := domain.BranchVersion{
		ID:                 "ver-privacy-test",
		BranchID:           env.branchID,
		Status:             domain.VersionStatusPending,
		MainRevisionUsed:   2,
		TemplateSetIDUsed:  "builtin-standard-clean",
		SourceType:         domain.SourceTypeLLMGeneration,
	}
	if err := env.branchRepo.CreateVersion(ctx, &ver); err != nil {
		t.Fatalf("failed to create version: %v", err)
	}

	if err := pool.Enqueue(worker.GenerationJob{
		UserID:    env.defaultUser,
		BranchID:  env.branchID,
		VersionID: ver.ID,
	}); err != nil {
		t.Fatalf("failed to enqueue job: %v", err)
	}

	// Await completion
	var completedVer *domain.BranchVersion
	for range 50 {
		time.Sleep(20 * time.Millisecond)
		v, err := env.branchRepo.GetVersion(ctx, env.branchID, ver.ID)
		if err == nil && (v.Status == domain.VersionStatusCompleted || v.Status == domain.VersionStatusCompletedWithWarnings || v.Status == domain.VersionStatusFailed) {
			completedVer = v
			break
		}
	}

	if completedVer == nil || (completedVer.Status != domain.VersionStatusCompleted && completedVer.Status != domain.VersionStatusCompletedWithWarnings) {
		t.Fatalf("expected version completed or completed_with_warnings, got %+v", completedVer)
	}

	// 1. Verify TailoredContent is stored with authentic details
	if strings.Contains(completedVer.TailoredContent.Summary, "[John Doe]") {
		t.Errorf("version TailoredContent.Summary contains placeholder [John Doe]")
	}
	if !strings.Contains(completedVer.TailoredContent.Summary, "Margaret Hamilton") {
		t.Errorf("version TailoredContent.Summary missing authentic name 'Margaret Hamilton'")
	}
	if !strings.Contains(completedVer.TailoredContent.Summary, "margaret.hamilton@mit.edu") {
		t.Errorf("version TailoredContent.Summary missing authentic email 'margaret.hamilton@mit.edu'")
	}

	bulletText := completedVer.TailoredContent.Experiences[0].Bullets[0].Text
	if !strings.HasPrefix(bulletText, "Margaret Hamilton engineered") {
		t.Errorf("version bullet text unexpected: %q", bulletText)
	}

	// 2. Verify GapReport contains authentic details
	if strings.Contains(completedVer.GapReport.OverallMatchSummary, "[John Doe]") {
		t.Errorf("version GapReport contains placeholder [John Doe]")
	}
	if !strings.Contains(completedVer.GapReport.OverallMatchSummary, "Margaret Hamilton") {
		t.Errorf("version GapReport missing authentic name 'Margaret Hamilton'")
	}

	// 3. Verify Rendered outputs are unobfuscated and show authentic contact details
	assertRendered := func(format, content string) {
		if strings.Contains(content, "[John Doe]") {
			t.Errorf("%s contains placeholder [John Doe]", format)
		}
		if strings.Contains(content, "candidate@email.com") {
			t.Errorf("%s contains placeholder candidate@email.com", format)
		}
		if !strings.Contains(content, "Margaret Hamilton") {
			t.Errorf("%s missing authentic name 'Margaret Hamilton'", format)
		}
		if !strings.Contains(content, "margaret.hamilton@mit.edu") {
			t.Errorf("%s missing authentic email 'margaret.hamilton@mit.edu'", format)
		}
	}

	assertRendered("HTMLOutput", completedVer.HTMLOutput)
	assertRendered("MarkdownOutput", completedVer.MarkdownOutput)
	assertRendered("PlainTextOutput", completedVer.PlainTextOutput)
}
