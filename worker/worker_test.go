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

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/memory"
	"github.com/resumeee-app/resumeee-core/provenance"
	"github.com/resumeee-app/resumeee-core/renderer"
	"github.com/resumeee-app/resumeee-core/worker"
)

type mockLLM struct {
	mu           sync.Mutex
	delay        time.Duration
	fail         bool
	failErrMsg   string
	shouldPanic  bool
	panicVal     any
	customResp   *core.GenerationResponse
	requestCount int
	capturedCtx  context.Context
}

func (m *mockLLM) GenerateTailoredResume(ctx context.Context, req core.GenerationRequest) (*core.GenerationResponse, error) {
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
	if req.MainProfile != nil {
		if len(req.MainProfile.Skills) > 0 {
			skillIDs = []string{req.MainProfile.Skills[0].ID}
		}
		if len(req.MainProfile.Experiences) > 0 {
			expID = req.MainProfile.Experiences[0].ID
			if len(req.MainProfile.Experiences[0].Facts) > 0 {
				factID = req.MainProfile.Experiences[0].Facts[0].ID
			}
		}
	}

	var bullets []core.TailoredBullet
	if factID != "" {
		bullets = []core.TailoredBullet{
			{
				Text:           "Engineered high-throughput streaming systems.",
				SourceFactIDs:  []string{factID},
				SourceSkillIDs: skillIDs,
			},
		}
	}

	var experiences []core.TailoredExperience
	if expID != "" {
		experiences = []core.TailoredExperience{
			{
				SourceEntityID: expID,
				Company:        "Test Corp",
				RoleTitle:      "Lead Engineer",
				StartDate:      core.NewDate(2020, 1, 1),
				Bullets:        bullets,
			},
		}
	}

	return &core.GenerationResponse{
		TailoredContent: core.TailoredContent{
			Summary:     "Seasoned engineer specializing in scalable backend systems.",
			Experiences: experiences,
		},
		GapReport: core.GapReport{
			OverallMatchSummary: "Candidate strongly matches requirements.",
		},
	}, nil
}

func (m *mockLLM) GenerateGapAnalysis(ctx context.Context, req core.GenerationRequest) (*core.GapReport, error) {
	resp, err := m.GenerateTailoredResume(ctx, req)
	if err != nil {
		return nil, err
	}
	return &resp.GapReport, nil
}

func (m *mockLLM) SimulateATS(_ context.Context, _ core.ATSSimulationRequest) (*core.ATSSimulationResponse, error) {
	return &core.ATSSimulationResponse{}, nil
}

type testWorkerEnv struct {
	db           any
	profileRepo  core.ProfileStore
	careerRepo   core.CareerStore
	presetRepo   core.PresetStore
	templateRepo core.TemplateStore
	branchRepo   core.BranchStore
	validator    *provenance.Validator
	renderer     worker.TemplateRenderer
	llm          *mockLLM
	defaultUser  string
	branchID     string
}

func setupTestWorkerEnv(t *testing.T) *testWorkerEnv {
	t.Helper()

	ctx := context.Background()
	defaultUser := "default"
	store, err := memory.NewSeeded(ctx, defaultUser)
	if err != nil {
		t.Fatalf("failed to seed store: %v", err)
	}

	profileRepo := store
	presetRepo := store
	templateRepo := store
	branchRepo := store
	val := provenance.New()
	rend := renderer.New()
	llmService := &mockLLM{}

	branch := core.Branch{
		ID:            "branch-" + uuid.New().String(),
		UserID:        defaultUser,
		Name:          "Backend Staff Engineer",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	if err := branchRepo.CreateBranch(ctx, &branch); err != nil {
		t.Fatalf("failed to create branch: %v", err)
	}

	return &testWorkerEnv{
		db:           store,
		profileRepo:  profileRepo,
		careerRepo:   store,
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

	pool := worker.New(worker.Config{
		Concurrency: 4,
		QueueSize:   20,
		Branches:    env.branchRepo,
		Profiles:    env.profileRepo,
		Presets:     env.presetRepo,
		Templates:   env.templateRepo,
		Generator:   env.llm,
		Validator:   env.validator,
		Renderer:    env.renderer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	const numJobs = 16
	versionIDs := make([]string, numJobs)

	for i := 0; i < numJobs; i++ {
		ver := &core.BranchVersion{
			ID:                fmt.Sprintf("ver-parallel-%d", i),
			BranchID:          env.branchID,
			Status:            core.VersionStatusPending,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: core.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateVersion(ctx, ver); err != nil {
			t.Fatalf("failed to create version %d: %v", i, err)
		}
		versionIDs[i] = ver.ID

		job := core.GenerationJob{
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
			ver, err := env.branchRepo.Version(ctx, env.branchID, vID)
			if err != nil {
				t.Fatalf("failed to get version %s: %v", vID, err)
			}
			if ver.Status == core.VersionStatusCompleted {
				if ver.HTMLOutput == "" || ver.MarkdownOutput == "" || ver.PlainTextOutput == "" {
					t.Fatalf("expected rendered outputs on version %s, got empty", vID)
				}
				break
			}
			if ver.Status == core.VersionStatusFailed {
				t.Fatalf("job %s unexpectedly failed: %s", vID, ver.ErrorMessage)
			}
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for version %s to complete, current status: %s", vID, ver.Status)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Assert branch active version and last synced revision were updated
	b, err := env.branchRepo.Branch(ctx, env.defaultUser, env.branchID)
	if err != nil {
		t.Fatalf("failed to get branch: %v", err)
	}
	if b.ActiveVersionID == nil || *b.ActiveVersionID == "" {
		t.Errorf("expected branch.ActiveVersionID to be set, got nil")
	}
	if b.LastSyncedMainRevision != 1 {
		t.Errorf("expected LastSyncedMainRevision 1, got %d", b.LastSyncedMainRevision)
	}
}

// 2. Backpressure & Saturation Test
func TestPool_BackpressureAndSaturation(t *testing.T) {
	env := setupTestWorkerEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Seed version 1 so it successfully reaches GenerateTailoredResume and blocks
	v1 := &core.BranchVersion{
		ID:                "ver-block-1",
		BranchID:          env.branchID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateVersion(ctx, v1); err != nil {
		t.Fatalf("failed to create version: %v", err)
	}

	// Block the LLM call until test unblocks it
	unblock := make(chan struct{})

	blockingLLM := &blockingMockLLM{unblock: unblock}

	pool := worker.New(worker.Config{
		Concurrency: 1,
		QueueSize:   2,
		Branches:    env.branchRepo,
		Profiles:    env.profileRepo,
		Presets:     env.presetRepo,
		Templates:   env.templateRepo,
		Generator:   blockingLLM,
		Validator:   env.validator,
		Renderer:    env.renderer,
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
	j1 := core.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: v1.ID}
	if err := pool.Enqueue(j1); err != nil {
		t.Fatalf("failed to enqueue job 1: %v", err)
	}

	// Wait for worker to enter processing state for job 1
	deadline := time.Now().Add(2 * time.Second)
	for {
		ver, err := env.branchRepo.Version(ctx, env.branchID, v1.ID)
		if err == nil && ver.Status == core.VersionStatusProcessing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for worker to pick up job 1")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 2nd job: fills slot 1 of queue
	j2 := core.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: "ver-slot-1"}
	if err := pool.Enqueue(j2); err != nil {
		t.Fatalf("failed to enqueue job 2: %v", err)
	}

	// 3rd job: fills slot 2 of queue
	j3 := core.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: "ver-slot-2"}
	if err := pool.Enqueue(j3); err != nil {
		t.Fatalf("failed to enqueue job 3: %v", err)
	}

	// 4th job: queue is full, must return core.ErrQueueFull immediately without blocking
	j4 := core.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: "ver-overflow"}
	err := pool.Enqueue(j4)
	if !errors.Is(err, core.ErrQueueFull) {
		t.Fatalf("expected core.ErrQueueFull, got %v", err)
	}
}

type blockingMockLLM struct {
	unblock chan struct{}
}

func (m *blockingMockLLM) GenerateTailoredResume(ctx context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
	select {
	case <-m.unblock:
		return &core.GenerationResponse{
			TailoredContent: core.TailoredContent{Summary: "Done"},
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *blockingMockLLM) GenerateGapAnalysis(ctx context.Context, req core.GenerationRequest) (*core.GapReport, error) {
	resp, err := m.GenerateTailoredResume(ctx, req)
	if err != nil {
		return nil, err
	}
	return &resp.GapReport, nil
}

func (m *blockingMockLLM) SimulateATS(_ context.Context, _ core.ATSSimulationRequest) (*core.ATSSimulationResponse, error) {
	return &core.ATSSimulationResponse{}, nil
}

// 3. Graceful Shutdown & Drain Test
func TestPool_GracefulShutdownAndDrain(t *testing.T) {
	env := setupTestWorkerEnv(t)
	env.llm.delay = 20 * time.Millisecond

	pool := worker.New(worker.Config{
		Concurrency: 2,
		QueueSize:   10,
		Branches:    env.branchRepo,
		Profiles:    env.profileRepo,
		Presets:     env.presetRepo,
		Templates:   env.templateRepo,
		Generator:   env.llm,
		Validator:   env.validator,
		Renderer:    env.renderer,
	})

	ctx := context.Background()
	pool.Start(ctx)

	const jobsCount = 4
	versionIDs := make([]string, jobsCount)
	for i := 0; i < jobsCount; i++ {
		ver := &core.BranchVersion{
			ID:                fmt.Sprintf("ver-drain-%d", i),
			BranchID:          env.branchID,
			Status:            core.VersionStatusPending,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: core.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateVersion(ctx, ver); err != nil {
			t.Fatalf("failed to create version %d: %v", i, err)
		}
		versionIDs[i] = ver.ID

		job := core.GenerationJob{
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
		ver, err := env.branchRepo.Version(ctx, env.branchID, vID)
		if err != nil {
			t.Fatalf("failed to get version %s: %v", vID, err)
		}
		if ver.Status != core.VersionStatusCompleted {
			t.Errorf("expected version %s to be completed after drain, got %s", vID, ver.Status)
		}
	}

	// Enqueue after Stop should be rejected
	err := pool.Enqueue(core.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: "ver-post-stop"})
	if !errors.Is(err, core.ErrQueueFull) {
		t.Errorf("expected core.ErrQueueFull after pool stop, got %v", err)
	}
}

// 4. Startup Crash Recovery Test
func TestPool_StartupCrashRecovery(t *testing.T) {
	env := setupTestWorkerEnv(t)
	ctx := context.Background()

	// Seed versions lingering in pending and processing states
	vPending := &core.BranchVersion{
		ID:                "ver-lingering-pending",
		BranchID:          env.branchID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateVersion(ctx, vPending); err != nil {
		t.Fatalf("failed to create pending version: %v", err)
	}

	vProcessing := &core.BranchVersion{
		ID:                "ver-lingering-processing",
		BranchID:          env.branchID,
		Status:            core.VersionStatusProcessing,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
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
		ver, err := env.branchRepo.Version(ctx, env.branchID, id)
		if err != nil {
			t.Fatalf("failed to get version %s: %v", id, err)
		}
		if ver.Status != core.VersionStatusFailed {
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

	pool := worker.New(worker.Config{
		Concurrency: 2,
		QueueSize:   5,
		Branches:    env.branchRepo,
		Profiles:    env.profileRepo,
		Presets:     env.presetRepo,
		Templates:   env.templateRepo,
		Generator:   env.llm,
		Validator:   env.validator,
		Renderer:    env.renderer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	ver := &core.BranchVersion{
		ID:                "ver-llm-fail",
		BranchID:          env.branchID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateVersion(ctx, ver); err != nil {
		t.Fatalf("failed to create version: %v", err)
	}

	if err := pool.Enqueue(core.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: ver.ID}); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	// Poll until version reaches failed state
	deadline := time.Now().Add(2 * time.Second)
	for {
		v, err := env.branchRepo.Version(ctx, env.branchID, ver.ID)
		if err != nil {
			t.Fatalf("failed to get version: %v", err)
		}
		if v.Status == core.VersionStatusFailed {
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
	env.llm.customResp = &core.GenerationResponse{
		TailoredContent: core.TailoredContent{
			Summary: "Valid summary",
			Experiences: []core.TailoredExperience{
				{
					SourceEntityID: "non-existent-exp-id",
					Company:        "Fake Corp",
					RoleTitle:      "Phantom Engineer",
					StartDate:      core.NewDate(2020, 1, 1),
					Bullets: []core.TailoredBullet{
						{
							Text:          "Did impossible things",
							SourceFactIDs: []string{"fabricated-fact-1"},
						},
					},
				},
			},
		},
		GapReport: core.GapReport{OverallMatchSummary: "Moderate match"},
	}

	pool := worker.New(worker.Config{
		Concurrency: 2,
		QueueSize:   5,
		Branches:    env.branchRepo,
		Profiles:    env.profileRepo,
		Presets:     env.presetRepo,
		Templates:   env.templateRepo,
		Generator:   env.llm,
		Validator:   env.validator,
		Renderer:    env.renderer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	ver := &core.BranchVersion{
		ID:                "ver-warn-1",
		BranchID:          env.branchID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateVersion(ctx, ver); err != nil {
		t.Fatalf("failed to create version: %v", err)
	}

	if err := pool.Enqueue(core.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: ver.ID}); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		v, err := env.branchRepo.Version(ctx, env.branchID, ver.ID)
		if err != nil {
			t.Fatalf("failed to get version: %v", err)
		}
		if v.Status == core.VersionStatusCompletedWithWarnings {
			if len(v.ValidationWarnings) == 0 {
				t.Errorf("expected validation warnings, got none")
			}
			break
		}
		if v.Status == core.VersionStatusFailed {
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
	v1 := &core.BranchVersion{
		ID:               "ver-stale-pending",
		BranchID:         env.branchID,
		Status:           core.VersionStatusPending,
		MainRevisionUsed: 1,
	}
	if err := env.branchRepo.CreateVersion(ctx, v1); err != nil {
		t.Fatalf("failed to create v1: %v", err)
	}

	v2 := &core.BranchVersion{
		ID:               "ver-stale-processing",
		BranchID:         env.branchID,
		Status:           core.VersionStatusProcessing,
		MainRevisionUsed: 1,
	}
	if err := env.branchRepo.CreateVersion(ctx, v2); err != nil {
		t.Fatalf("failed to create v2: %v", err)
	}

	pool := worker.New(worker.Config{
		Branches: env.branchRepo,
	})

	if err := pool.RecoverStaleJobs(ctx, "server restart recovery test"); err != nil {
		t.Fatalf("RecoverStaleJobs failed: %v", err)
	}

	refetched1, err := env.branchRepo.Version(ctx, env.branchID, v1.ID)
	if err != nil {
		t.Fatalf("failed to get v1: %v", err)
	}
	if refetched1.Status != core.VersionStatusFailed {
		t.Errorf("expected v1 status failed, got %s", refetched1.Status)
	}
	if refetched1.ErrorMessage != "server restart recovery test" {
		t.Errorf("expected error message 'server restart recovery test', got %q", refetched1.ErrorMessage)
	}

	refetched2, err := env.branchRepo.Version(ctx, env.branchID, v2.ID)
	if err != nil {
		t.Fatalf("failed to get v2: %v", err)
	}
	if refetched2.Status != core.VersionStatusFailed {
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
	pool := worker.New(worker.Config{
		Concurrency: 1,
		QueueSize:   5,
		Branches:    env.branchRepo,
		Profiles:    env.profileRepo,
		Presets:     env.presetRepo,
		Templates:   env.templateRepo,
		Generator:   env.llm,
		Validator:   env.validator,
		Renderer:    env.renderer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	// 1. Enqueue job that panics
	verPanic := &core.BranchVersion{
		ID:                "ver-panic-1",
		BranchID:          env.branchID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateVersion(ctx, verPanic); err != nil {
		t.Fatalf("failed to create panic version: %v", err)
	}

	if err := pool.Enqueue(core.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: verPanic.ID}); err != nil {
		t.Fatalf("failed to enqueue panic job: %v", err)
	}

	// Wait for panic version to fail
	deadline := time.Now().Add(2 * time.Second)
	for {
		v, err := env.branchRepo.Version(ctx, env.branchID, verPanic.ID)
		if err != nil {
			t.Fatalf("failed to get panic version: %v", err)
		}
		if v.Status == core.VersionStatusFailed {
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

	verSuccess := &core.BranchVersion{
		ID:                "ver-success-2",
		BranchID:          env.branchID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateVersion(ctx, verSuccess); err != nil {
		t.Fatalf("failed to create success version: %v", err)
	}

	if err := pool.Enqueue(core.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: verSuccess.ID}); err != nil {
		t.Fatalf("failed to enqueue second job: %v", err)
	}

	// Wait for second job to complete successfully
	deadline = time.Now().Add(2 * time.Second)
	for {
		v, err := env.branchRepo.Version(ctx, env.branchID, verSuccess.ID)
		if err != nil {
			t.Fatalf("failed to get success version: %v", err)
		}
		if v.Status == core.VersionStatusCompleted {
			break
		}
		if v.Status == core.VersionStatusFailed {
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
	result     *core.JSONResume
	usage      core.TokenUsage
}

func (m *mockResumeExtractor) ExtractResume(ctx context.Context, _, _, _ string) (*core.JSONResume, core.TokenUsage, error) {
	m.mu.Lock()
	delay := m.delay
	fail := m.fail
	failErrMsg := m.failErrMsg
	result := m.result
	usage := m.usage
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, core.TokenUsage{}, ctx.Err()
		}
	}
	if fail {
		return nil, core.TokenUsage{}, errors.New(failErrMsg)
	}
	if result != nil {
		return result, usage, nil
	}
	return &core.JSONResume{
		Basics: core.JSONResumeBasics{
			Name:  "Test Candidate",
			Email: "candidate@test.com",
		},
	}, usage, nil
}

func TestWorkerPool_PDFParseJob_Success(t *testing.T) {
	pdfRepo := memory.New()
	mockExt := &mockResumeExtractor{
		delay: 10 * time.Millisecond,
		result: &core.JSONResume{
			Basics: core.JSONResumeBasics{
				Name: "Grace Hopper",
			},
		},
	}

	pool := worker.New(worker.Config{
		Concurrency:     2,
		QueueSize:       10,
		PDFJobs:         pdfRepo,
		ResumeExtractor: mockExt,
		LLMTimeout:      5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	jobID := "pdf-job-success-1"
	job := &core.PDFParseJob{
		ID:        jobID,
		UserID:    "user-1",
		Status:    core.PDFParseJobStatusPending,
		Filename:  "hopper.pdf",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := pdfRepo.CreateJob(ctx, job); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	task := core.PDFParseJobTask{
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
		j, err := pdfRepo.Job(ctx, "user-1", jobID)
		if err != nil {
			t.Fatalf("get job failed: %v", err)
		}
		if j.Status == core.PDFParseJobStatusCompleted {
			if j.CompletedAt == nil {
				t.Errorf("expected CompletedAt to be set")
			}
			if j.Result == nil {
				t.Fatalf("expected non-nil JSONResume result")
			}
			if j.Result.Basics.Name != "Grace Hopper" {
				t.Errorf("expected Basics.Name == 'Grace Hopper', got %q", j.Result.Basics.Name)
			}
			break
		}
		if j.Status == core.PDFParseJobStatusFailed {
			t.Fatalf("pdf job unexpectedly failed: %s", j.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for pdf job to complete, status: %s", j.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWorkerPool_PDFParseJob_Failure(t *testing.T) {
	pdfRepo := memory.New()
	mockExt := &mockResumeExtractor{
		fail:       true,
		failErrMsg: "upstream model capacity reached",
	}

	pool := worker.New(worker.Config{
		Concurrency:     2,
		QueueSize:       10,
		PDFJobs:         pdfRepo,
		ResumeExtractor: mockExt,
		LLMTimeout:      5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	jobID := "pdf-job-fail-1"
	job := &core.PDFParseJob{
		ID:        jobID,
		UserID:    "user-1",
		Status:    core.PDFParseJobStatusPending,
		Filename:  "fail.pdf",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := pdfRepo.CreateJob(ctx, job); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	task := core.PDFParseJobTask{
		JobID:   jobID,
		UserID:  "user-1",
		RawText: "Some text",
	}

	if err := pool.EnqueuePDFJob(task); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		j, err := pdfRepo.Job(ctx, "user-1", jobID)
		if err != nil {
			t.Fatalf("get job failed: %v", err)
		}
		if j.Status == core.PDFParseJobStatusFailed {
			if !strings.Contains(j.ErrorMessage, "Failed to extract resume from document") {
				t.Errorf("expected sanitized error message, got %q", j.ErrorMessage)
			}
			break
		}
		if j.Status == core.PDFParseJobStatusCompleted {
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

	pool := worker.New(worker.Config{
		Concurrency: 1,
		QueueSize:   5,
		Branches:    env.branchRepo,
		Profiles:    env.profileRepo,
		Presets:     env.presetRepo,
		Templates:   env.templateRepo,
		Generator:   mockLLM,
		Validator:   env.validator,
		Renderer:    env.renderer,
		LLMTimeout:  7 * time.Minute,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	verID := "ver-timeout-check-1"
	ver := core.BranchVersion{
		ID:                verID,
		BranchID:          env.branchID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateVersion(ctx, &ver); err != nil {
		t.Fatalf("failed to create version: %v", err)
	}

	if err := pool.Enqueue(core.GenerationJob{
		UserID:    env.defaultUser,
		BranchID:  env.branchID,
		VersionID: verID,
	}); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		v, err := env.branchRepo.Version(ctx, env.branchID, verID)
		if err != nil {
			t.Fatalf("get version failed: %v", err)
		}
		if v.Status == core.VersionStatusCompleted {
			break
		}
		if v.Status == core.VersionStatusFailed {
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

// 12. Per-Tenant Concurrency Cap Test
func TestPool_PerTenantConcurrencyCap(t *testing.T) {
	pdfRepo := memory.New()
	var (
		activeCount sync.Mutex
		current     int
		peak        int
	)

	mockExt := &callbackResumeExtractor{
		fn: func(_ context.Context, _, _, _ string) (*core.JSONResume, error) {
			activeCount.Lock()
			current++
			if current > peak {
				peak = current
			}
			activeCount.Unlock()

			time.Sleep(30 * time.Millisecond)

			activeCount.Lock()
			current--
			activeCount.Unlock()

			return &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Alpha"}}, nil
		},
	}

	pool := worker.New(worker.Config{
		Concurrency:            4,
		MaxConcurrentPerTenant: 2,
		QueueSize:              20,
		PDFJobs:                pdfRepo,
		ResumeExtractor:        mockExt,
		LLMTimeout:             5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	const jobCount = 6
	tenantID := "tenant-alpha"
	jobIDs := make([]string, jobCount)
	for i := 0; i < jobCount; i++ {
		jobID := fmt.Sprintf("job-cap-%d", i)
		jobIDs[i] = jobID
		job := &core.PDFParseJob{
			ID:        jobID,
			UserID:    tenantID,
			Status:    core.PDFParseJobStatusPending,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := pdfRepo.CreateJob(ctx, job); err != nil {
			t.Fatalf("failed to create pdf job %d: %v", i, err)
		}
		if err := pool.EnqueuePDFJob(core.PDFParseJobTask{JobID: jobID, UserID: tenantID}); err != nil {
			t.Fatalf("failed to enqueue job %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for _, id := range jobIDs {
		for {
			j, err := pdfRepo.Job(ctx, tenantID, id)
			if err != nil {
				t.Fatalf("failed to get job %s: %v", id, err)
			}
			if j.Status == core.PDFParseJobStatusCompleted {
				break
			}
			if j.Status == core.PDFParseJobStatusFailed {
				t.Fatalf("job %s unexpectedly failed: %s", id, j.ErrorMessage)
			}
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for job %s to complete, got %s", id, j.Status)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	activeCount.Lock()
	maxObserved := peak
	activeCount.Unlock()

	if maxObserved > 2 {
		t.Fatalf("expected peak concurrent jobs for tenant <= 2, got %d", maxObserved)
	}
	if maxObserved < 2 {
		t.Errorf("expected peak concurrent jobs to reach cap of 2, got %d", maxObserved)
	}
}

// 13. Noisy-Neighbor Fair Share Anti-Starvation Test
func TestPool_NoisyNeighborFairShareAntiStarvation(t *testing.T) {
	pdfRepo := memory.New()

	mockExt := &callbackResumeExtractor{
		fn: func(_ context.Context, text, provider, _ string) (*core.JSONResume, error) {
			if provider == "slow" {
				time.Sleep(120 * time.Millisecond)
			} else {
				time.Sleep(10 * time.Millisecond)
			}
			return &core.JSONResume{Basics: core.JSONResumeBasics{Name: text}}, nil
		},
	}

	pool := worker.New(worker.Config{
		Concurrency:            2,
		MaxConcurrentPerTenant: 1,
		QueueSize:              20,
		PDFJobs:                pdfRepo,
		ResumeExtractor:        mockExt,
		LLMTimeout:             5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	// Tenant A floods with 8 slow jobs
	tenantA := "tenant-a-noisy"
	const tenantAJobs = 8
	tenantAJobIDs := make([]string, tenantAJobs)
	for i := 0; i < tenantAJobs; i++ {
		jobID := fmt.Sprintf("job-tenant-a-%d", i)
		tenantAJobIDs[i] = jobID
		j := &core.PDFParseJob{
			ID:        jobID,
			UserID:    tenantA,
			Status:    core.PDFParseJobStatusPending,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := pdfRepo.CreateJob(ctx, j); err != nil {
			t.Fatalf("failed to create tenant A job %d: %v", i, err)
		}
		if err := pool.EnqueuePDFJob(core.PDFParseJobTask{JobID: jobID, UserID: tenantA, Provider: "slow"}); err != nil {
			t.Fatalf("failed to enqueue tenant A job %d: %v", i, err)
		}
	}

	// Wait briefly for Tenant A's first job to be picked up
	time.Sleep(15 * time.Millisecond)

	// Tenant B enqueues 1 fast job
	tenantB := "tenant-b-fast"
	tenantBJobID := "job-tenant-b-0"
	jB := &core.PDFParseJob{
		ID:        tenantBJobID,
		UserID:    tenantB,
		Status:    core.PDFParseJobStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := pdfRepo.CreateJob(ctx, jB); err != nil {
		t.Fatalf("failed to create tenant B job: %v", err)
	}
	if err := pool.EnqueuePDFJob(core.PDFParseJobTask{JobID: tenantBJobID, UserID: tenantB, Provider: "fast"}); err != nil {
		t.Fatalf("failed to enqueue tenant B job: %v", err)
	}

	// Tenant B's job should complete promptly (< 60ms) while Tenant A's jobs are still executing
	deadlineB := time.Now().Add(500 * time.Millisecond)
	var tenantBCompleted bool
	for {
		jb, err := pdfRepo.Job(ctx, tenantB, tenantBJobID)
		if err != nil {
			t.Fatalf("failed to get tenant B job: %v", err)
		}
		if jb.Status == core.PDFParseJobStatusCompleted {
			tenantBCompleted = true
			break
		}
		if time.Now().After(deadlineB) {
			t.Fatalf("tenant B fast job timed out waiting for completion; noisy neighbor starved it!")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if !tenantBCompleted {
		t.Fatalf("expected tenant B job to be completed")
	}

	// Verify Tenant A still has incomplete jobs when Tenant B finished
	var remainingA int
	for _, id := range tenantAJobIDs {
		ja, err := pdfRepo.Job(ctx, tenantA, id)
		if err != nil {
			t.Fatalf("failed to get tenant A job %s: %v", id, err)
		}
		if ja.Status != core.PDFParseJobStatusCompleted {
			remainingA++
		}
	}
	if remainingA < 4 {
		t.Fatalf("expected at least 4 of tenant A's jobs to still be incomplete when B finished, got remaining %d", remainingA)
	}
}

// 14. Multi-Tenant Round-Robin Ordering Test
func TestPool_MultiTenantRoundRobinOrdering(t *testing.T) {
	pdfRepo := memory.New()
	var (
		mu              sync.Mutex
		dispatchHistory []string
	)

	mockExt := &callbackResumeExtractor{
		fn: func(_ context.Context, text, _, _ string) (*core.JSONResume, error) {
			mu.Lock()
			dispatchHistory = append(dispatchHistory, text)
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			return &core.JSONResume{Basics: core.JSONResumeBasics{Name: text}}, nil
		},
	}

	// 1 worker, max 1 concurrent per tenant
	pool := worker.New(worker.Config{
		Concurrency:            1,
		MaxConcurrentPerTenant: 1,
		QueueSize:              20,
		PDFJobs:                pdfRepo,
		ResumeExtractor:        mockExt,
		LLMTimeout:             5 * time.Second,
	})

	ctx := context.Background()

	// Enqueue before Start so all jobs are queued before dispatch begins:
	// A1, A2, B1, B2, C1, C2
	tenants := []string{"tenant-a", "tenant-b", "tenant-c"}
	totalJobs := 0
	for _, tID := range tenants {
		for j := 1; j <= 2; j++ {
			jobID := fmt.Sprintf("%s-job-%d", tID, j)
			job := &core.PDFParseJob{
				ID:        jobID,
				UserID:    tID,
				Status:    core.PDFParseJobStatusPending,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			if err := pdfRepo.CreateJob(ctx, job); err != nil {
				t.Fatalf("create job failed: %v", err)
			}
			if err := pool.EnqueuePDFJob(core.PDFParseJobTask{JobID: jobID, UserID: tID, RawText: tID}); err != nil {
				t.Fatalf("enqueue failed: %v", err)
			}
			totalJobs++
		}
	}

	pool.Start(ctx)

	// Wait for all 6 jobs to finish
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		count := len(dispatchHistory)
		mu.Unlock()
		if count >= totalJobs {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for all jobs to complete, got %d of %d", count, totalJobs)
		}
		time.Sleep(5 * time.Millisecond)
	}

	pool.Stop()

	mu.Lock()
	defer mu.Unlock()

	expectedOrder := []string{"tenant-a", "tenant-b", "tenant-c", "tenant-a", "tenant-b", "tenant-c"}
	if len(dispatchHistory) != len(expectedOrder) {
		t.Fatalf("expected %d dispatches, got %d: %v", len(expectedOrder), len(dispatchHistory), dispatchHistory)
	}
	for i, exp := range expectedOrder {
		if dispatchHistory[i] != exp {
			t.Errorf("turn %d: expected %s, got %s (full history: %v)", i, exp, dispatchHistory[i], dispatchHistory)
		}
	}
}

// 15. Panic Recovery Slot Release Test
func TestPool_PanicSlotRelease(t *testing.T) {
	pdfRepo := memory.New()
	var (
		callCount int
		mu        sync.Mutex
	)

	mockExt := &callbackResumeExtractor{
		fn: func(_ context.Context, _, _, _ string) (*core.JSONResume, error) {
			mu.Lock()
			callCount++
			currentCall := callCount
			mu.Unlock()

			if currentCall == 1 {
				panic("simulated fatal crash in model parsing")
			}
			return &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Recovered"}}, nil
		},
	}

	// Concurrency 2, MaxConcurrentPerTenant: 1
	pool := worker.New(worker.Config{
		Concurrency:            2,
		MaxConcurrentPerTenant: 1,
		QueueSize:              10,
		PDFJobs:                pdfRepo,
		ResumeExtractor:        mockExt,
		LLMTimeout:             5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	tenantID := "tenant-panic-test"

	// 1. Enqueue job 1 which will panic
	job1ID := "panic-job-1"
	j1 := &core.PDFParseJob{
		ID:        job1ID,
		UserID:    tenantID,
		Status:    core.PDFParseJobStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := pdfRepo.CreateJob(ctx, j1); err != nil {
		t.Fatalf("failed to create job 1: %v", err)
	}
	if err := pool.EnqueuePDFJob(core.PDFParseJobTask{JobID: job1ID, UserID: tenantID}); err != nil {
		t.Fatalf("failed to enqueue job 1: %v", err)
	}

	// Wait for job 1 to fail via panic recovery
	deadline := time.Now().Add(2 * time.Second)
	for {
		job1, err := pdfRepo.Job(ctx, tenantID, job1ID)
		if err != nil {
			t.Fatalf("failed to get job 1: %v", err)
		}
		if job1.Status == core.PDFParseJobStatusFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for job 1 to fail, got %s", job1.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 2. Enqueue job 2 for the same tenant. If slot was leaked, this job would hang forever because MaxConcurrentPerTenant is 1!
	job2ID := "panic-job-2"
	j2 := &core.PDFParseJob{
		ID:        job2ID,
		UserID:    tenantID,
		Status:    core.PDFParseJobStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := pdfRepo.CreateJob(ctx, j2); err != nil {
		t.Fatalf("failed to create job 2: %v", err)
	}
	if err := pool.EnqueuePDFJob(core.PDFParseJobTask{JobID: job2ID, UserID: tenantID}); err != nil {
		t.Fatalf("failed to enqueue job 2: %v", err)
	}

	// Wait for job 2 to complete successfully
	deadline = time.Now().Add(2 * time.Second)
	for {
		job2, err := pdfRepo.Job(ctx, tenantID, job2ID)
		if err != nil {
			t.Fatalf("failed to get job 2: %v", err)
		}
		if job2.Status == core.PDFParseJobStatusCompleted {
			break
		}
		if job2.Status == core.PDFParseJobStatusFailed {
			t.Fatalf("job 2 failed unexpectedly: %s", job2.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("job 2 timed out waiting for completion; tenant concurrency slot was leaked on panic!")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type callbackResumeExtractor struct {
	fn func(ctx context.Context, text, provider, model string) (*core.JSONResume, error)
}

func (c *callbackResumeExtractor) ExtractResume(ctx context.Context, text, provider, model string) (*core.JSONResume, core.TokenUsage, error) {
	if c.fn != nil {
		res, err := c.fn(ctx, text, provider, model)
		return res, core.TokenUsage{}, err
	}
	return &core.JSONResume{}, core.TokenUsage{}, nil
}

type callbackLLM struct {
	fn func(ctx context.Context, req core.GenerationRequest) (*core.GenerationResponse, error)
}

func (m *callbackLLM) GenerateTailoredResume(ctx context.Context, req core.GenerationRequest) (*core.GenerationResponse, error) {
	if m.fn != nil {
		return m.fn(ctx, req)
	}
	return &core.GenerationResponse{TailoredContent: core.TailoredContent{Summary: "OK"}}, nil
}

func (m *callbackLLM) GenerateGapAnalysis(ctx context.Context, req core.GenerationRequest) (*core.GapReport, error) {
	resp, err := m.GenerateTailoredResume(ctx, req)
	if err != nil {
		return nil, err
	}
	return &resp.GapReport, nil
}

func (m *callbackLLM) SimulateATS(_ context.Context, _ core.ATSSimulationRequest) (*core.ATSSimulationResponse, error) {
	return &core.ATSSimulationResponse{}, nil
}

// 16. GenerationJob Per-Tenant Concurrency Cap Test
func TestPool_GenerationJob_PerTenantConcurrencyCap(t *testing.T) {
	env := setupTestWorkerEnv(t)
	var (
		activeMu sync.Mutex
		current  int
		peak     int
	)

	llm := &callbackLLM{
		fn: func(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
			activeMu.Lock()
			current++
			if current > peak {
				peak = current
			}
			activeMu.Unlock()

			time.Sleep(30 * time.Millisecond)

			activeMu.Lock()
			current--
			activeMu.Unlock()

			return &core.GenerationResponse{
				TailoredContent: core.TailoredContent{
					Summary: "Test summary",
				},
			}, nil
		},
	}

	pool := worker.New(worker.Config{
		Concurrency:            4,
		MaxConcurrentPerTenant: 2,
		QueueSize:              20,
		Branches:               env.branchRepo,
		Profiles:               env.profileRepo,
		Presets:                env.presetRepo,
		Templates:              env.templateRepo,
		Generator:              llm,
		Validator:              env.validator,
		Renderer:               env.renderer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	const jobCount = 6
	versionIDs := make([]string, jobCount)
	for i := 0; i < jobCount; i++ {
		vID := fmt.Sprintf("ver-gen-cap-%d", i)
		versionIDs[i] = vID
		ver := &core.BranchVersion{
			ID:                vID,
			BranchID:          env.branchID,
			Status:            core.VersionStatusPending,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: core.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateVersion(ctx, ver); err != nil {
			t.Fatalf("failed to create version %d: %v", i, err)
		}
		if err := pool.Enqueue(core.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: vID}); err != nil {
			t.Fatalf("failed to enqueue generation job %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for _, vID := range versionIDs {
		for {
			ver, err := env.branchRepo.Version(ctx, env.branchID, vID)
			if err != nil {
				t.Fatalf("failed to get version %s: %v", vID, err)
			}
			if ver.Status == core.VersionStatusCompleted {
				break
			}
			if ver.Status == core.VersionStatusFailed {
				t.Fatalf("version %s failed: %s", vID, ver.ErrorMessage)
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for version %s", vID)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	activeMu.Lock()
	maxObserved := peak
	activeMu.Unlock()

	if maxObserved > 2 {
		t.Fatalf("expected peak GenerationJob concurrency <= 2, got %d", maxObserved)
	}
	if maxObserved < 2 {
		t.Errorf("expected peak GenerationJob concurrency to reach cap of 2, got %d", maxObserved)
	}
}

// 17. Mixed Workload Shared Tenant Concurrency Cap Test
func TestPool_MixedWorkload_SharedTenantConcurrencyCap(t *testing.T) {
	env := setupTestWorkerEnv(t)
	pdfRepo := memory.New()

	var (
		activeMu sync.Mutex
		current  int
		peak     int
	)

	trackStart := func() {
		activeMu.Lock()
		current++
		if current > peak {
			peak = current
		}
		activeMu.Unlock()
	}

	trackEnd := func() {
		activeMu.Lock()
		current--
		activeMu.Unlock()
	}

	llm := &callbackLLM{
		fn: func(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
			trackStart()
			time.Sleep(30 * time.Millisecond)
			trackEnd()
			return &core.GenerationResponse{
				TailoredContent: core.TailoredContent{Summary: "Mixed gen"},
			}, nil
		},
	}

	extractor := &callbackResumeExtractor{
		fn: func(_ context.Context, _, _, _ string) (*core.JSONResume, error) {
			trackStart()
			time.Sleep(30 * time.Millisecond)
			trackEnd()
			return &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Mixed pdf"}}, nil
		},
	}

	pool := worker.New(worker.Config{
		Concurrency:            4,
		MaxConcurrentPerTenant: 2,
		QueueSize:              20,
		Branches:               env.branchRepo,
		Profiles:               env.profileRepo,
		Presets:                env.presetRepo,
		Templates:              env.templateRepo,
		PDFJobs:                pdfRepo,
		ResumeExtractor:        extractor,
		Generator:              llm,
		Validator:              env.validator,
		Renderer:               env.renderer,
		LLMTimeout:             5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	tenantID := env.defaultUser

	const genCount = 3
	const pdfCount = 3
	versionIDs := make([]string, genCount)
	pdfJobIDs := make([]string, pdfCount)

	for i := 0; i < genCount; i++ {
		vID := fmt.Sprintf("ver-mixed-%d", i)
		versionIDs[i] = vID
		ver := &core.BranchVersion{
			ID:                vID,
			BranchID:          env.branchID,
			Status:            core.VersionStatusPending,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: core.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateVersion(ctx, ver); err != nil {
			t.Fatalf("failed to create version %d: %v", i, err)
		}
		if err := pool.Enqueue(core.GenerationJob{UserID: tenantID, BranchID: env.branchID, VersionID: vID}); err != nil {
			t.Fatalf("failed to enqueue gen job %d: %v", i, err)
		}
	}

	for i := 0; i < pdfCount; i++ {
		pID := fmt.Sprintf("pdf-mixed-%d", i)
		pdfJobIDs[i] = pID
		pj := &core.PDFParseJob{
			ID:        pID,
			UserID:    tenantID,
			Status:    core.PDFParseJobStatusPending,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := pdfRepo.CreateJob(ctx, pj); err != nil {
			t.Fatalf("failed to create pdf job %d: %v", i, err)
		}
		if err := pool.EnqueuePDFJob(core.PDFParseJobTask{JobID: pID, UserID: tenantID}); err != nil {
			t.Fatalf("failed to enqueue pdf task %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for _, vID := range versionIDs {
		for {
			ver, err := env.branchRepo.Version(ctx, env.branchID, vID)
			if err != nil {
				t.Fatalf("failed to get version %s: %v", vID, err)
			}
			if ver.Status == core.VersionStatusCompleted {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for version %s", vID)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	for _, pID := range pdfJobIDs {
		for {
			pj, err := pdfRepo.Job(ctx, tenantID, pID)
			if err != nil {
				t.Fatalf("failed to get pdf job %s: %v", pID, err)
			}
			if pj.Status == core.PDFParseJobStatusCompleted {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for pdf job %s", pID)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	activeMu.Lock()
	maxObserved := peak
	activeMu.Unlock()

	if maxObserved > 2 {
		t.Fatalf("expected combined peak concurrency across mixed workloads <= 2, got %d", maxObserved)
	}
	if maxObserved < 2 {
		t.Errorf("expected combined peak concurrency to reach 2, got %d", maxObserved)
	}
}

// 18. Mixed Workload Fair-Share Multi-Tenant Anti-Starvation Test
func TestPool_MixedWorkload_FairShareMultiTenant(t *testing.T) {
	env := setupTestWorkerEnv(t)
	pdfRepo := memory.New()

	llm := &callbackLLM{
		fn: func(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
			time.Sleep(120 * time.Millisecond)
			return &core.GenerationResponse{
				TailoredContent: core.TailoredContent{Summary: "Slow gen"},
			}, nil
		},
	}

	extractor := &callbackResumeExtractor{
		fn: func(_ context.Context, _, _, _ string) (*core.JSONResume, error) {
			time.Sleep(10 * time.Millisecond)
			return &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Fast pdf"}}, nil
		},
	}

	pool := worker.New(worker.Config{
		Concurrency:            2,
		MaxConcurrentPerTenant: 1,
		QueueSize:              20,
		Branches:               env.branchRepo,
		Profiles:               env.profileRepo,
		Presets:                env.presetRepo,
		Templates:              env.templateRepo,
		PDFJobs:                pdfRepo,
		ResumeExtractor:        extractor,
		Generator:              llm,
		Validator:              env.validator,
		Renderer:               env.renderer,
		LLMTimeout:             5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	tenantA := env.defaultUser
	const countA = 4
	versionIDs := make([]string, countA)
	for i := 0; i < countA; i++ {
		vID := fmt.Sprintf("ver-slow-gen-%d", i)
		versionIDs[i] = vID
		ver := &core.BranchVersion{
			ID:                vID,
			BranchID:          env.branchID,
			Status:            core.VersionStatusPending,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: core.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateVersion(ctx, ver); err != nil {
			t.Fatalf("failed to create version %d: %v", i, err)
		}
		if err := pool.Enqueue(core.GenerationJob{UserID: tenantA, BranchID: env.branchID, VersionID: vID}); err != nil {
			t.Fatalf("failed to enqueue gen job %d: %v", i, err)
		}
	}

	time.Sleep(15 * time.Millisecond)

	tenantB := "tenant-b-fast-pdf"
	fastJobID := "pdf-fast-job-1"
	pj := &core.PDFParseJob{
		ID:        fastJobID,
		UserID:    tenantB,
		Status:    core.PDFParseJobStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := pdfRepo.CreateJob(ctx, pj); err != nil {
		t.Fatalf("failed to create pdf job: %v", err)
	}
	if err := pool.EnqueuePDFJob(core.PDFParseJobTask{JobID: fastJobID, UserID: tenantB}); err != nil {
		t.Fatalf("failed to enqueue fast pdf job: %v", err)
	}

	deadlineB := time.Now().Add(500 * time.Millisecond)
	for {
		jobB, err := pdfRepo.Job(ctx, tenantB, fastJobID)
		if err != nil {
			t.Fatalf("failed to get fast job: %v", err)
		}
		if jobB.Status == core.PDFParseJobStatusCompleted {
			break
		}
		if time.Now().After(deadlineB) {
			t.Fatalf("tenant B fast job timed out waiting for completion")
		}
		time.Sleep(5 * time.Millisecond)
	}

	var remainingA int
	for _, vID := range versionIDs {
		ver, err := env.branchRepo.Version(ctx, env.branchID, vID)
		if err != nil {
			t.Fatalf("failed to get version %s: %v", vID, err)
		}
		if ver.Status != core.VersionStatusCompleted {
			remainingA++
		}
	}
	if remainingA < 2 {
		t.Fatalf("expected at least 2 of tenant A's generation jobs to still be incomplete when B finished, got remaining %d", remainingA)
	}
}

func TestWorkerPool_PDFParseJob_UsageQuotaCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store, err := memory.NewSeeded(ctx, "user-quota-worker")
	if err != nil {
		t.Fatalf("failed to seed store: %v", err)
	}

	mockExt := &mockResumeExtractor{
		delay: 5 * time.Millisecond,
		result: &core.JSONResume{
			Basics: core.JSONResumeBasics{
				Name: "Usage Candidate",
			},
		},
		usage: core.TokenUsage{
			PromptTokens:     100,
			CompletionTokens: 250,
			TotalTokens:      350,
		},
	}

	pool := worker.New(worker.Config{
		Concurrency:     2,
		QueueSize:       10,
		PDFJobs:         store,
		Quotas:          store,
		ResumeExtractor: mockExt,
		LLMTimeout:      5 * time.Second,
	})
	pool.Start(ctx)
	defer pool.Stop()

	// Reserve quota first
	rec, err := store.ReserveQuota(ctx, "user-quota-worker", core.OperationPDFParse, 1000, 50, 1000000)
	if err != nil {
		t.Fatalf("failed to reserve quota: %v", err)
	}

	jobID := "pdf-job-usage-1"
	job := &core.PDFParseJob{
		ID:        jobID,
		UserID:    "user-quota-worker",
		Status:    core.PDFParseJobStatusPending,
		Filename:  "usage.pdf",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	task := core.PDFParseJobTask{
		JobID:         jobID,
		UserID:        "user-quota-worker",
		RawText:       "Candidate resume content",
		Provider:      "mock",
		Model:         "mock-model",
		ReservationID: rec.ID,
	}
	if err := pool.EnqueuePDFJob(task); err != nil {
		t.Fatalf("failed to enqueue pdf job: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		j, err := store.Job(ctx, "user-quota-worker", jobID)
		if err != nil {
			t.Fatalf("get job failed: %v", err)
		}
		if j.Status == core.PDFParseJobStatusCompleted {
			break
		}
		if j.Status == core.PDFParseJobStatusFailed {
			t.Fatalf("pdf job unexpectedly failed: %s", j.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for pdf job to complete")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Verify committed quota: monthly tokens should reflect actual 350 tokens rather than 1000 estimate
	quota, err := store.Quota(ctx, "user-quota-worker")
	if err != nil {
		t.Fatalf("failed to get quota: %v", err)
	}
	if quota.MonthlyTokensUsed != 350 {
		t.Errorf("expected monthly_tokens_used 350 (actual usage), got %d", quota.MonthlyTokensUsed)
	}
}

func TestPool_TailoringPipeline_ReasoningLevelPrecedence(t *testing.T) {
	ctx := context.Background()

	t.Run("branch reasoning_level is passed to generation request", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		var capturedReq core.GenerationRequest
		reqReceived := make(chan struct{}, 1)

		llm := &callbackLLM{
			fn: func(_ context.Context, req core.GenerationRequest) (*core.GenerationResponse, error) {
				capturedReq = req
				select {
				case reqReceived <- struct{}{}:
				default:
				}
				return &core.GenerationResponse{
					TailoredContent: core.TailoredContent{Summary: "Tailored"},
				}, nil
			},
		}

		pool := worker.New(worker.Config{
			Concurrency: 1,
			QueueSize:   5,
			Branches:    env.branchRepo,
			Profiles:    env.profileRepo,
			Presets:     env.presetRepo,
			Templates:   env.templateRepo,
			Generator:   llm,
			Validator:   env.validator,
		})
		pool.Start(ctx)
		defer pool.Stop()

		preset := &core.Preset{
			ID:                 "preset-reasoning-test",
			UserID:             env.defaultUser,
			Name:               "Test Preset",
			SystemInstructions: "Test instructions",
		}
		if err := env.presetRepo.CreatePreset(ctx, preset); err != nil {
			t.Fatalf("failed to create preset: %v", err)
		}

		// Seed branch with reasoning_level "high"
		presetID := preset.ID
		branch := &core.Branch{
			ID:                  "branch-reasoning-override",
			UserID:              env.defaultUser,
			Name:                "Tailored Branch",
			TemplateSetID:       "builtin-standard-clean",
			TailoringStrategyID: &presetID,
			PresetID:            &presetID,
			ReasoningLevel:      "high",
		}
		if err := env.branchRepo.CreateBranch(ctx, branch); err != nil {
			t.Fatalf("failed to create branch: %v", err)
		}

		versionID := "ver-reasoning-override"
		ver := &core.BranchVersion{
			ID:                versionID,
			UserID:            env.defaultUser,
			BranchID:          branch.ID,
			Status:            core.VersionStatusPending,
			TemplateSetIDUsed: "builtin-standard-clean",
		}
		if err := env.branchRepo.CreateVersion(ctx, ver); err != nil {
			t.Fatalf("failed to create version: %v", err)
		}

		if err := pool.Enqueue(core.GenerationJob{
			VersionID: versionID,
			BranchID:  branch.ID,
			UserID:    env.defaultUser,
		}); err != nil {
			t.Fatalf("failed to enqueue job: %v", err)
		}

		select {
		case <-reqReceived:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for LLM invocation")
		}

		if capturedReq.ReasoningLevel != "high" {
			t.Errorf("expected ReasoningLevel %q (from branch), got %q", "high", capturedReq.ReasoningLevel)
		}
	})

	t.Run("branch reasoning_level is used directly and empty when unset on branch", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		var capturedReq core.GenerationRequest
		reqReceived := make(chan struct{}, 1)

		llm := &callbackLLM{
			fn: func(_ context.Context, req core.GenerationRequest) (*core.GenerationResponse, error) {
				capturedReq = req
				select {
				case reqReceived <- struct{}{}:
				default:
				}
				return &core.GenerationResponse{
					TailoredContent: core.TailoredContent{Summary: "Tailored"},
				}, nil
			},
		}

		pool := worker.New(worker.Config{
			Concurrency: 1,
			QueueSize:   5,
			Branches:    env.branchRepo,
			Profiles:    env.profileRepo,
			Presets:     env.presetRepo,
			Templates:   env.templateRepo,
			Generator:   llm,
			Validator:   env.validator,
		})
		pool.Start(ctx)
		defer pool.Stop()

		preset := &core.Preset{
			ID:                 "preset-reasoning-fallback",
			UserID:             env.defaultUser,
			Name:               "Fallback Preset",
			SystemInstructions: "Test instructions",
		}
		if err := env.presetRepo.CreatePreset(ctx, preset); err != nil {
			t.Fatalf("failed to create preset: %v", err)
		}

		presetID := preset.ID
		branch := &core.Branch{
			ID:                  "branch-reasoning-fallback",
			UserID:              env.defaultUser,
			Name:                "Fallback Branch",
			TemplateSetID:       "builtin-standard-clean",
			TailoringStrategyID: &presetID,
			PresetID:            &presetID,
			ReasoningLevel:      "", // Unset on branch
		}
		if err := env.branchRepo.CreateBranch(ctx, branch); err != nil {
			t.Fatalf("failed to create branch: %v", err)
		}

		versionID := "ver-reasoning-fallback"
		ver := &core.BranchVersion{
			ID:                versionID,
			UserID:            env.defaultUser,
			BranchID:          branch.ID,
			Status:            core.VersionStatusPending,
			TemplateSetIDUsed: "builtin-standard-clean",
		}
		if err := env.branchRepo.CreateVersion(ctx, ver); err != nil {
			t.Fatalf("failed to create version: %v", err)
		}

		if err := pool.Enqueue(core.GenerationJob{
			VersionID: versionID,
			BranchID:  branch.ID,
			UserID:    env.defaultUser,
		}); err != nil {
			t.Fatalf("failed to enqueue job: %v", err)
		}

		select {
		case <-reqReceived:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for LLM invocation")
		}

		if capturedReq.ReasoningLevel != "" {
			t.Errorf("expected empty ReasoningLevel when unset on branch, got %q", capturedReq.ReasoningLevel)
		}
	})
}
