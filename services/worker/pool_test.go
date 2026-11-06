package worker_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"resumeee/internal/domain"
	"resumeee/internal/profile"
	"resumeee/internal/renderer"
	"resumeee/internal/repository/sqlite"
	"resumeee/internal/validator"
	"resumeee/internal/worker"
)

type mockLLM struct {
	mu           sync.Mutex
	delay        time.Duration
	fail         bool
	failErrMsg   string
	shouldPanic  bool
	panicVal     any
	customResp   *domain.GenerationResponse
	requestCount int
	capturedCtx  context.Context
}

func (m *mockLLM) GenerateTailoredResume(ctx context.Context, req domain.GenerationRequest) (*domain.GenerationResponse, error) {
	m.mu.Lock()
	m.requestCount++
	m.capturedCtx = ctx
	delay := m.delay
	fail := m.fail
	failErrMsg := m.failErrMsg
	shouldPanic := m.shouldPanic
	panicVal := m.panicVal
	custom := m.customResp
	m.mu.Unlock()

	if shouldPanic {
		panic(panicVal)
	}

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if fail {
		return nil, errors.New(failErrMsg)
	}

	if custom != nil {
		return custom, nil
	}

	// Default valid tailored content grounded in seeded default data
	var skillIDs []string
	var expID, factID string
	if req.MasterProfile != nil {
		if len(req.MasterProfile.Skills) > 0 {
			skillIDs = []string{req.MasterProfile.Skills[0].ID}
		}
		if len(req.MasterProfile.Experiences) > 0 {
			expID = req.MasterProfile.Experiences[0].ID
			if len(req.MasterProfile.Experiences[0].Facts) > 0 {
				factID = req.MasterProfile.Experiences[0].Facts[0].ID
			}
		}
	}

	var bullets []domain.TailoredBullet
	if factID != "" {
		bullets = []domain.TailoredBullet{
			{
				Text:           "Engineered high-throughput streaming systems.",
				SourceFactIDs:  []string{factID},
				SourceSkillIDs: skillIDs,
			},
		}
	}

	var experiences []domain.TailoredExperience
	if expID != "" {
		experiences = []domain.TailoredExperience{
			{
				SourceEntityID: expID,
				Company:        "Test Corp",
				RoleTitle:      "Lead Engineer",
				StartDate:      "2020-01-01",
				Bullets:        bullets,
			},
		}
	}

	return &domain.GenerationResponse{
		TailoredContent: domain.TailoredContent{
			Summary:     "Seasoned engineer specializing in scalable backend systems.",
			Experiences: experiences,
		},
		GapReport: domain.GapReport{
			OverallMatchSummary: "Candidate strongly matches requirements.",
		},
	}, nil
}

func (m *mockLLM) GenerateGapAnalysis(ctx context.Context, req domain.GenerationRequest) (*domain.GapReport, error) {
	resp, err := m.GenerateTailoredResume(ctx, req)
	if err != nil {
		return nil, err
	}
	return &resp.GapReport, nil
}

type testWorkerEnv struct {
	db           any
	profileRepo  domain.ProfileRepository
	presetRepo   domain.PresetRepository
	templateRepo domain.TemplateRepository
	branchRepo   domain.BranchRepository
	validator    *validator.Validator
	renderer     domain.TemplateRenderer
	llm          *mockLLM
	defaultUser  string
	branchID     string
}

func setupTestWorkerEnv(t *testing.T) *testWorkerEnv {
	t.Helper()

	db, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("failed to initialize sqlite: %v", err)
	}

	ctx := context.Background()
	defaultUser := "default"
	if err := sqlite.SeedDefaultData(ctx, db, defaultUser); err != nil {
		t.Fatalf("failed to seed default data: %v", err)
	}

	profileRepo := sqlite.NewProfileRepository(db)
	presetRepo := sqlite.NewPresetRepository(db)
	templateRepo := sqlite.NewTemplateRepository(db)
	branchRepo := sqlite.NewBranchRepository(db)
	val := validator.New()
	rend := renderer.NewTemplateRenderer()
	llmService := &mockLLM{}

	branch := domain.Branch{
		ID:            "branch-" + uuid.New().String(),
		UserID:        defaultUser,
		Name:          "Backend Staff Engineer",
		TemplateSetID: renderer.TemplateStandardCleanID,
	}
	if err := branchRepo.CreateBranch(ctx, &branch); err != nil {
		t.Fatalf("failed to create branch: %v", err)
	}

	return &testWorkerEnv{
		db:           db,
		profileRepo:  profileRepo,
		presetRepo:   presetRepo,
		templateRepo: templateRepo,
		branchRepo:   branchRepo,
		validator:    val,
		renderer:     rend,
		llm:          llmService,
		defaultUser:  defaultUser,
		branchID:     branch.ID,
	}
}

// 1. Parallel Execution Test
func TestPool_ParallelExecution(t *testing.T) {
	env := setupTestWorkerEnv(t)
	env.llm.delay = 15 * time.Millisecond

	pool := worker.NewPool(worker.PoolConfig{
		Concurrency:  4,
		QueueSize:    20,
		BranchRepo:   env.branchRepo,
		ProfileRepo:  env.profileRepo,
		PresetRepo:   env.presetRepo,
		TemplateRepo: env.templateRepo,
		LLMService:   env.llm,
		Validator:    env.validator,
		Renderer:     env.renderer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	const numJobs = 16
	versionIDs := make([]string, numJobs)

	for i := 0; i < numJobs; i++ {
		ver := &domain.BranchVersion{
			ID:                 fmt.Sprintf("ver-parallel-%d", i),
			BranchID:           env.branchID,
			Status:             domain.VersionStatusPending,
			MasterRevisionUsed: 1,
			TemplateSetIDUsed:  renderer.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateVersion(ctx, ver); err != nil {
			t.Fatalf("failed to create version %d: %v", i, err)
		}
		versionIDs[i] = ver.ID

		job := worker.GenerationJob{
			UserID:    env.defaultUser,
			BranchID:  env.branchID,
			VersionID: ver.ID,
		}
		if err := pool.Enqueue(job); err != nil {
			t.Fatalf("failed to enqueue job %d: %v", i, err)
		}
	}

	// Poll until all jobs are completed (or timeout after 5s)
	deadline := time.Now().Add(5 * time.Second)
	for _, vID := range versionIDs {
		for {
			ver, err := env.branchRepo.GetVersion(ctx, env.branchID, vID)
			if err != nil {
				t.Fatalf("failed to get version %s: %v", vID, err)
			}
			if ver.Status == domain.VersionStatusCompleted {
				if ver.HTMLOutput == "" || ver.MarkdownOutput == "" || ver.PlainTextOutput == "" {
					t.Fatalf("expected rendered outputs on version %s, got empty", vID)
				}
				break
			}
			if ver.Status == domain.VersionStatusFailed {
				t.Fatalf("job %s unexpectedly failed: %s", vID, ver.ErrorMessage)
			}
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for version %s to complete, current status: %s", vID, ver.Status)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Assert branch active version and last synced revision were updated
	b, err := env.branchRepo.GetBranch(ctx, env.defaultUser, env.branchID)
	if err != nil {
		t.Fatalf("failed to get branch: %v", err)
	}
	if b.ActiveVersionID == nil || *b.ActiveVersionID == "" {
		t.Errorf("expected branch.ActiveVersionID to be set, got nil")
	}
	if b.LastSyncedMasterRevision != 1 {
		t.Errorf("expected LastSyncedMasterRevision 1, got %d", b.LastSyncedMasterRevision)
	}
}

// 2. Backpressure & Saturation Test
func TestPool_BackpressureAndSaturation(t *testing.T) {
	env := setupTestWorkerEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Seed version 1 so it successfully reaches GenerateTailoredResume and blocks
	v1 := &domain.BranchVersion{
		ID:                 "ver-block-1",
		BranchID:           env.branchID,
		Status:             domain.VersionStatusPending,
		MasterRevisionUsed: 1,
		TemplateSetIDUsed:  renderer.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateVersion(ctx, v1); err != nil {
		t.Fatalf("failed to create version: %v", err)
	}

	// Block the LLM call until test unblocks it
	unblock := make(chan struct{})

	blockingLLM := &blockingMockLLM{unblock: unblock}

	pool := worker.NewPool(worker.PoolConfig{
		Concurrency:  1,
		QueueSize:    2,
		BranchRepo:   env.branchRepo,
		ProfileRepo:  env.profileRepo,
		PresetRepo:   env.presetRepo,
		TemplateRepo: env.templateRepo,
		LLMService:   blockingLLM,
		Validator:    env.validator,
		Renderer:     env.renderer,
	})

	pool.Start(ctx)
	defer func() {
		// Ensure worker is unblocked BEFORE pool.Stop() waits on wg
		select {
		case <-unblock:
		default:
			close(unblock)
		}
		pool.Stop()
	}()

	// 1st job: dequeued by worker and blocked inside LLM
	j1 := worker.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: v1.ID}
	if err := pool.Enqueue(j1); err != nil {
		t.Fatalf("failed to enqueue job 1: %v", err)
	}

	// Wait for worker to enter processing state for job 1
	deadline := time.Now().Add(2 * time.Second)
	for {
		ver, err := env.branchRepo.GetVersion(ctx, env.branchID, v1.ID)
		if err == nil && ver.Status == domain.VersionStatusProcessing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for worker to pick up job 1")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 2nd job: fills slot 1 of queue
	j2 := worker.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: "ver-slot-1"}
	if err := pool.Enqueue(j2); err != nil {
		t.Fatalf("failed to enqueue job 2: %v", err)
	}

	// 3rd job: fills slot 2 of queue
	j3 := worker.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: "ver-slot-2"}
	if err := pool.Enqueue(j3); err != nil {
		t.Fatalf("failed to enqueue job 3: %v", err)
	}

	// 4th job: queue is full, must return domain.ErrQueueFull immediately without blocking
	j4 := worker.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: "ver-overflow"}
	err := pool.Enqueue(j4)
	if !errors.Is(err, domain.ErrQueueFull) {
		t.Fatalf("expected domain.ErrQueueFull, got %v", err)
	}
}

type blockingMockLLM struct {
	unblock chan struct{}
}

func (m *blockingMockLLM) GenerateTailoredResume(ctx context.Context, req domain.GenerationRequest) (*domain.GenerationResponse, error) {
	select {
	case <-m.unblock:
		return &domain.GenerationResponse{
			TailoredContent: domain.TailoredContent{Summary: "Done"},
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *blockingMockLLM) GenerateGapAnalysis(ctx context.Context, req domain.GenerationRequest) (*domain.GapReport, error) {
	resp, err := m.GenerateTailoredResume(ctx, req)
	if err != nil {
		return nil, err
	}
	return &resp.GapReport, nil
}

// 3. Graceful Shutdown & Drain Test
func TestPool_GracefulShutdownAndDrain(t *testing.T) {
	env := setupTestWorkerEnv(t)
	env.llm.delay = 20 * time.Millisecond

	pool := worker.NewPool(worker.PoolConfig{
		Concurrency:  2,
		QueueSize:    10,
		BranchRepo:   env.branchRepo,
		ProfileRepo:  env.profileRepo,
		PresetRepo:   env.presetRepo,
		TemplateRepo: env.templateRepo,
		LLMService:   env.llm,
		Validator:    env.validator,
		Renderer:     env.renderer,
	})

	ctx := context.Background()
	pool.Start(ctx)

	const jobsCount = 4
	versionIDs := make([]string, jobsCount)
	for i := 0; i < jobsCount; i++ {
		ver := &domain.BranchVersion{
			ID:                 fmt.Sprintf("ver-drain-%d", i),
			BranchID:           env.branchID,
			Status:             domain.VersionStatusPending,
			MasterRevisionUsed: 1,
			TemplateSetIDUsed:  renderer.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateVersion(ctx, ver); err != nil {
			t.Fatalf("failed to create version %d: %v", i, err)
		}
		versionIDs[i] = ver.ID

		job := worker.GenerationJob{
			UserID:    env.defaultUser,
			BranchID:  env.branchID,
			VersionID: ver.ID,
		}
		if err := pool.Enqueue(job); err != nil {
			t.Fatalf("failed to enqueue job %d: %v", i, err)
		}
	}

	// Trigger Stop while jobs are in flight and queued
	stopDone := make(chan struct{})
	go func() {
		pool.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("pool.Stop() timed out")
	}

	// Verify all drained jobs completed cleanly
	for _, vID := range versionIDs {
		ver, err := env.branchRepo.GetVersion(ctx, env.branchID, vID)
		if err != nil {
			t.Fatalf("failed to get version %s: %v", vID, err)
		}
		if ver.Status != domain.VersionStatusCompleted {
			t.Errorf("expected version %s to be completed after drain, got %s", vID, ver.Status)
		}
	}

	// Enqueue after Stop should be rejected
	err := pool.Enqueue(worker.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: "ver-post-stop"})
	if !errors.Is(err, domain.ErrQueueFull) {
		t.Errorf("expected domain.ErrQueueFull after pool stop, got %v", err)
	}
}

// 4. Startup Crash Recovery Test
func TestPool_StartupCrashRecovery(t *testing.T) {
	env := setupTestWorkerEnv(t)
	ctx := context.Background()

	// Seed versions lingering in pending and processing states
	vPending := &domain.BranchVersion{
		ID:                 "ver-lingering-pending",
		BranchID:           env.branchID,
		Status:             domain.VersionStatusPending,
		MasterRevisionUsed: 1,
		TemplateSetIDUsed:  renderer.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateVersion(ctx, vPending); err != nil {
		t.Fatalf("failed to create pending version: %v", err)
	}

	vProcessing := &domain.BranchVersion{
		ID:                 "ver-lingering-processing",
		BranchID:           env.branchID,
		Status:             domain.VersionStatusProcessing,
		MasterRevisionUsed: 1,
		TemplateSetIDUsed:  renderer.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateVersion(ctx, vProcessing); err != nil {
		t.Fatalf("failed to create processing version: %v", err)
	}

	// Simulate server startup recovery
	const crashReason = "server restarted while generation job was in flight"
	if err := env.branchRepo.FailInterruptedVersions(ctx, crashReason); err != nil {
		t.Fatalf("FailInterruptedVersions failed: %v", err)
	}

	// Verify both versions transitioned to failed with exact reason and completed_at
	for _, id := range []string{vPending.ID, vProcessing.ID} {
		ver, err := env.branchRepo.GetVersion(ctx, env.branchID, id)
		if err != nil {
			t.Fatalf("failed to get version %s: %v", id, err)
		}
		if ver.Status != domain.VersionStatusFailed {
			t.Errorf("expected version %s to be failed, got %s", id, ver.Status)
		}
		if ver.ErrorMessage != crashReason {
			t.Errorf("expected error message %q, got %q", crashReason, ver.ErrorMessage)
		}
		if ver.CompletedAt == nil {
			t.Errorf("expected completed_at timestamp on recovered version %s", id)
		}
	}
}

// 5. LLM Error Transition Test
func TestPool_LLMErrorTransition(t *testing.T) {
	env := setupTestWorkerEnv(t)
	env.llm.fail = true
	env.llm.failErrMsg = "quota exhausted or network timeout"

	pool := worker.NewPool(worker.PoolConfig{
		Concurrency:  2,
		QueueSize:    5,
		BranchRepo:   env.branchRepo,
		ProfileRepo:  env.profileRepo,
		PresetRepo:   env.presetRepo,
		TemplateRepo: env.templateRepo,
		LLMService:   env.llm,
		Validator:    env.validator,
		Renderer:     env.renderer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	ver := &domain.BranchVersion{
		ID:                 "ver-llm-fail",
		BranchID:           env.branchID,
		Status:             domain.VersionStatusPending,
		MasterRevisionUsed: 1,
		TemplateSetIDUsed:  renderer.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateVersion(ctx, ver); err != nil {
		t.Fatalf("failed to create version: %v", err)
	}

	if err := pool.Enqueue(worker.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: ver.ID}); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	// Poll until version reaches failed state
	deadline := time.Now().Add(2 * time.Second)
	for {
		v, err := env.branchRepo.GetVersion(ctx, env.branchID, ver.ID)
		if err != nil {
			t.Fatalf("failed to get version: %v", err)
		}
		if v.Status == domain.VersionStatusFailed {
			if v.ErrorMessage == "" {
				t.Errorf("expected error message on failed version")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for failure transition, got %s", v.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 6. Provenance Warnings Transition Test
func TestPool_CompletedWithWarnings(t *testing.T) {
	env := setupTestWorkerEnv(t)

	// Inject tailored content citing a fabricated fact ID
	env.llm.customResp = &domain.GenerationResponse{
		TailoredContent: domain.TailoredContent{
			Summary: "Valid summary",
			Experiences: []domain.TailoredExperience{
				{
					SourceEntityID: "non-existent-exp-id",
					Company:        "Fake Corp",
					RoleTitle:      "Phantom Engineer",
					StartDate:      "2020-01-01",
					Bullets: []domain.TailoredBullet{
						{
							Text:          "Did impossible things",
							SourceFactIDs: []string{"fabricated-fact-1"},
						},
					},
				},
			},
		},
		GapReport: domain.GapReport{OverallMatchSummary: "Moderate match"},
	}

	pool := worker.NewPool(worker.PoolConfig{
		Concurrency:  2,
		QueueSize:    5,
		BranchRepo:   env.branchRepo,
		ProfileRepo:  env.profileRepo,
		PresetRepo:   env.presetRepo,
		TemplateRepo: env.templateRepo,
		LLMService:   env.llm,
		Validator:    env.validator,
		Renderer:     env.renderer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	ver := &domain.BranchVersion{
		ID:                 "ver-warn-1",
		BranchID:           env.branchID,
		Status:             domain.VersionStatusPending,
		MasterRevisionUsed: 1,
		TemplateSetIDUsed:  renderer.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateVersion(ctx, ver); err != nil {
		t.Fatalf("failed to create version: %v", err)
	}

	if err := pool.Enqueue(worker.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: ver.ID}); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		v, err := env.branchRepo.GetVersion(ctx, env.branchID, ver.ID)
		if err != nil {
			t.Fatalf("failed to get version: %v", err)
		}
		if v.Status == domain.VersionStatusCompletedWithWarnings {
			if len(v.ValidationWarnings) == 0 {
				t.Errorf("expected validation warnings, got none")
			}
			break
		}
		if v.Status == domain.VersionStatusFailed {
			t.Fatalf("job unexpectedly failed: %s", v.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for completed_with_warnings, current status: %s", v.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPool_RecoverStaleJobs(t *testing.T) {
	env := setupTestWorkerEnv(t)
	ctx := context.Background()

	// Create pending version and processing version
	v1 := &domain.BranchVersion{
		ID:                 "ver-stale-pending",
		BranchID:           env.branchID,
		Status:             domain.VersionStatusPending,
		MasterRevisionUsed: 1,
	}
	if err := env.branchRepo.CreateVersion(ctx, v1); err != nil {
		t.Fatalf("failed to create v1: %v", err)
	}

	v2 := &domain.BranchVersion{
		ID:                 "ver-stale-processing",
		BranchID:           env.branchID,
		Status:             domain.VersionStatusProcessing,
		MasterRevisionUsed: 1,
	}
	if err := env.branchRepo.CreateVersion(ctx, v2); err != nil {
		t.Fatalf("failed to create v2: %v", err)
	}

	pool := worker.NewPool(worker.PoolConfig{
		BranchRepo: env.branchRepo,
	})

	if err := pool.RecoverStaleJobs(ctx, "server restart recovery test"); err != nil {
		t.Fatalf("RecoverStaleJobs failed: %v", err)
	}

	refetched1, err := env.branchRepo.GetVersion(ctx, env.branchID, v1.ID)
	if err != nil {
		t.Fatalf("failed to get v1: %v", err)
	}
	if refetched1.Status != domain.VersionStatusFailed {
		t.Errorf("expected v1 status failed, got %s", refetched1.Status)
	}
	if refetched1.ErrorMessage != "server restart recovery test" {
		t.Errorf("expected error message 'server restart recovery test', got %q", refetched1.ErrorMessage)
	}

	refetched2, err := env.branchRepo.GetVersion(ctx, env.branchID, v2.ID)
	if err != nil {
		t.Fatalf("failed to get v2: %v", err)
	}
	if refetched2.Status != domain.VersionStatusFailed {
		t.Errorf("expected v2 status failed, got %s", refetched2.Status)
	}
}

// TestPool_WorkerPanicRecovery verifies that a panic during job execution is caught,
// records the version as failed with panic diagnostics, and does not crash the worker goroutine,
// allowing subsequent enqueued jobs to process successfully.
func TestPool_WorkerPanicRecovery(t *testing.T) {
	env := setupTestWorkerEnv(t)
	env.llm.mu.Lock()
	env.llm.shouldPanic = true
	env.llm.panicVal = "unexpected runtime nil pointer during model invocation"
	env.llm.mu.Unlock()

	// Single concurrency to prove the single worker survives the panic
	pool := worker.NewPool(worker.PoolConfig{
		Concurrency:  1,
		QueueSize:    5,
		BranchRepo:   env.branchRepo,
		ProfileRepo:  env.profileRepo,
		PresetRepo:   env.presetRepo,
		TemplateRepo: env.templateRepo,
		LLMService:   env.llm,
		Validator:    env.validator,
		Renderer:     env.renderer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	// 1. Enqueue job that panics
	verPanic := &domain.BranchVersion{
		ID:                 "ver-panic-1",
		BranchID:           env.branchID,
		Status:             domain.VersionStatusPending,
		MasterRevisionUsed: 1,
		TemplateSetIDUsed:  renderer.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateVersion(ctx, verPanic); err != nil {
		t.Fatalf("failed to create panic version: %v", err)
	}

	if err := pool.Enqueue(worker.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: verPanic.ID}); err != nil {
		t.Fatalf("failed to enqueue panic job: %v", err)
	}

	// Wait for panic version to fail
	deadline := time.Now().Add(2 * time.Second)
	for {
		v, err := env.branchRepo.GetVersion(ctx, env.branchID, verPanic.ID)
		if err != nil {
			t.Fatalf("failed to get panic version: %v", err)
		}
		if v.Status == domain.VersionStatusFailed {
			if v.ErrorMessage == "" {
				t.Errorf("expected non-empty error message on panic failure")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for panic version to fail, current status: %s", v.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 2. Clear panic condition and enqueue a second job to prove worker survived
	env.llm.mu.Lock()
	env.llm.shouldPanic = false
	env.llm.panicVal = nil
	env.llm.mu.Unlock()

	verSuccess := &domain.BranchVersion{
		ID:                 "ver-success-2",
		BranchID:           env.branchID,
		Status:             domain.VersionStatusPending,
		MasterRevisionUsed: 1,
		TemplateSetIDUsed:  renderer.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateVersion(ctx, verSuccess); err != nil {
		t.Fatalf("failed to create success version: %v", err)
	}

	if err := pool.Enqueue(worker.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: verSuccess.ID}); err != nil {
		t.Fatalf("failed to enqueue second job: %v", err)
	}

	// Wait for second job to complete successfully
	deadline = time.Now().Add(2 * time.Second)
	for {
		v, err := env.branchRepo.GetVersion(ctx, env.branchID, verSuccess.ID)
		if err != nil {
			t.Fatalf("failed to get success version: %v", err)
		}
		if v.Status == domain.VersionStatusCompleted {
			break
		}
		if v.Status == domain.VersionStatusFailed {
			t.Fatalf("second job unexpectedly failed: %s", v.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for second job to complete, current status: %s", v.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type mockResumeExtractor struct {
	mu         sync.Mutex
	delay      time.Duration
	fail       bool
	failErrMsg string
	result     *profile.JSONResume
}

func (m *mockResumeExtractor) ExtractResume(ctx context.Context, text, provider, model string) (*profile.JSONResume, error) {
	m.mu.Lock()
	delay := m.delay
	fail := m.fail
	failErrMsg := m.failErrMsg
	result := m.result
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if fail {
		return nil, errors.New(failErrMsg)
	}
	if result != nil {
		return result, nil
	}
	return &profile.JSONResume{
		Basics: profile.JSONResumeBasics{
			Name:  "Test Candidate",
			Email: "candidate@test.com",
		},
	}, nil
}

func TestWorkerPool_PDFParseJob_Success(t *testing.T) {
	db, err := sqlite.New("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("sqlite init: %v", err)
	}
	pdfRepo := sqlite.NewPDFParseJobRepository(db)
	mockExt := &mockResumeExtractor{
		delay: 10 * time.Millisecond,
		result: &profile.JSONResume{
			Basics: profile.JSONResumeBasics{
				Name: "Grace Hopper",
			},
		},
	}

	pool := worker.NewPool(worker.PoolConfig{
		Concurrency:     2,
		QueueSize:       10,
		PDFJobRepo:      pdfRepo,
		ResumeExtractor: mockExt,
		LLMTimeout:      5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	jobID := "pdf-job-success-1"
	job := &domain.PDFParseJob{
		ID:        jobID,
		UserID:    "user-1",
		Status:    domain.PDFParseJobStatusPending,
		Filename:  "hopper.pdf",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := pdfRepo.CreateJob(ctx, job); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	task := worker.PDFParseJobTask{
		JobID:    jobID,
		UserID:   "user-1",
		RawText:  "Admiral Grace Hopper resume text",
		Provider: "mock",
		Model:    "mock-model",
	}

	if err := pool.EnqueuePDFJob(task); err != nil {
		t.Fatalf("failed to enqueue pdf job: %v", err)
	}

	// Poll until completed
	deadline := time.Now().Add(2 * time.Second)
	for {
		j, err := pdfRepo.GetJob(ctx, "user-1", jobID)
		if err != nil {
			t.Fatalf("get job failed: %v", err)
		}
		if j.Status == domain.PDFParseJobStatusCompleted {
			if j.CompletedAt == nil {
				t.Errorf("expected CompletedAt to be set")
			}
			resMap, ok := j.Result.(map[string]any)
			if !ok {
				t.Fatalf("expected map[string]any result, got %T", j.Result)
			}
			basics, ok := resMap["basics"].(map[string]any)
			if !ok || basics["name"] != "Grace Hopper" {
				t.Errorf("expected basics.name == 'Grace Hopper', got %+v", resMap)
			}
			break
		}
		if j.Status == domain.PDFParseJobStatusFailed {
			t.Fatalf("pdf job unexpectedly failed: %s", j.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for pdf job to complete, status: %s", j.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWorkerPool_PDFParseJob_Failure(t *testing.T) {
	db, err := sqlite.New("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("sqlite init: %v", err)
	}
	pdfRepo := sqlite.NewPDFParseJobRepository(db)
	mockExt := &mockResumeExtractor{
		fail:       true,
		failErrMsg: "upstream model capacity reached",
	}

	pool := worker.NewPool(worker.PoolConfig{
		Concurrency:     2,
		QueueSize:       10,
		PDFJobRepo:      pdfRepo,
		ResumeExtractor: mockExt,
		LLMTimeout:      5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	jobID := "pdf-job-fail-1"
	job := &domain.PDFParseJob{
		ID:        jobID,
		UserID:    "user-1",
		Status:    domain.PDFParseJobStatusPending,
		Filename:  "fail.pdf",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := pdfRepo.CreateJob(ctx, job); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	task := worker.PDFParseJobTask{
		JobID:   jobID,
		UserID:  "user-1",
		RawText: "Some text",
	}

	if err := pool.EnqueuePDFJob(task); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		j, err := pdfRepo.GetJob(ctx, "user-1", jobID)
		if err != nil {
			t.Fatalf("get job failed: %v", err)
		}
		if j.Status == domain.PDFParseJobStatusFailed {
			if !strings.Contains(j.ErrorMessage, "Failed to extract resume from document") {
				t.Errorf("expected sanitized error message, got %q", j.ErrorMessage)
			}
			break
		}
		if j.Status == domain.PDFParseJobStatusCompleted {
			t.Fatalf("expected job to fail, but it completed")
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for failure, status: %s", j.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWorkerPool_TailoringPipeline_LLMTimeout(t *testing.T) {
	env := setupTestWorkerEnv(t)
	mockLLM := &mockLLM{}

	pool := worker.NewPool(worker.PoolConfig{
		Concurrency:  1,
		QueueSize:    5,
		BranchRepo:   env.branchRepo,
		ProfileRepo:  env.profileRepo,
		PresetRepo:   env.presetRepo,
		TemplateRepo: env.templateRepo,
		LLMService:   mockLLM,
		Validator:    env.validator,
		Renderer:     env.renderer,
		LLMTimeout:   7 * time.Minute,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	verID := "ver-timeout-check-1"
	ver := domain.BranchVersion{
		ID:                 verID,
		BranchID:           env.branchID,
		Status:             domain.VersionStatusPending,
		MasterRevisionUsed: 1,
		TemplateSetIDUsed:  renderer.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateVersion(ctx, &ver); err != nil {
		t.Fatalf("failed to create version: %v", err)
	}

	if err := pool.Enqueue(worker.GenerationJob{
		UserID:    env.defaultUser,
		BranchID:  env.branchID,
		VersionID: verID,
	}); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		v, err := env.branchRepo.GetVersion(ctx, env.branchID, verID)
		if err != nil {
			t.Fatalf("get version failed: %v", err)
		}
		if v.Status == domain.VersionStatusCompleted {
			break
		}
		if v.Status == domain.VersionStatusFailed {
			t.Fatalf("job failed: %s", v.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for job completion")
		}
		time.Sleep(10 * time.Millisecond)
	}

	mockLLM.mu.Lock()
	capturedCtx := mockLLM.capturedCtx
	mockLLM.mu.Unlock()

	if capturedCtx == nil {
		t.Fatalf("expected GenerateTailoredResume to be called with context")
	}
	capturedDeadline, hasDeadline := capturedCtx.Deadline()
	if !hasDeadline {
		t.Fatalf("expected GenerateTailoredResume context to have a deadline bounded by LLMTimeout")
	}
	remaining := time.Until(capturedDeadline)
	if remaining < 6*time.Minute+50*time.Second || remaining > 7*time.Minute {
		t.Fatalf("expected remaining timeout around 7 minutes, got %v", remaining)
	}
}
