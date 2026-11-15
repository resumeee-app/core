package worker_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/llm"
	"github.com/resumeee-app/resumeee-core/memory"
	"github.com/resumeee-app/resumeee-core/provenance"
	"github.com/resumeee-app/resumeee-core/renderer"
	"github.com/resumeee-app/resumeee-core/seed"
	"github.com/resumeee-app/resumeee-core/worker"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t.UTC()}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

type mockLLM struct {
	mu           sync.Mutex
	delay        time.Duration
	fail         bool
	failErr      error
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
	failErr := m.failErr
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

	if failErr != nil {
		return nil, failErr
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
	store        *memory.Store
	db           any
	profileRepo  core.ProfileStore
	careerRepo   core.CareerStore
	presetRepo   core.PresetStore
	templateRepo core.TemplateStore
	branchRepo   core.BranchStore
	pdfRepo      core.PDFJobStore
	quotaRepo    core.QuotaStore
	validator    *provenance.Validator
	renderer     worker.TemplateRenderer
	llm          *mockLLM
	defaultUser  string
	branchID     string
}

func setupTestWorkerEnv(t *testing.T) *testWorkerEnv {
	t.Helper()
	return setupTestWorkerEnvWithClock(t, nil)
}

func setupTestWorkerEnvWithClock(t *testing.T, clock func() time.Time) *testWorkerEnv {
	t.Helper()

	ctx := context.Background()
	defaultUser := "default"
	store := memory.NewWithClock(clock)
	if err := seed.DefaultData(ctx, store, defaultUser); err != nil {
		t.Fatalf("failed to seed store: %v", err)
	}

	val := provenance.New()
	rend := renderer.New()
	llmService := &mockLLM{}

	branch := core.Branch{
		ID:            "branch-" + uuid.New().String(),
		UserID:        defaultUser,
		Name:          "Backend Staff Engineer",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	if err := store.CreateBranch(ctx, &branch); err != nil {
		t.Fatalf("failed to create branch: %v", err)
	}

	return &testWorkerEnv{
		store:        store,
		db:           store,
		profileRepo:  store,
		careerRepo:   store,
		presetRepo:   store,
		templateRepo: store,
		branchRepo:   store,
		pdfRepo:      store,
		quotaRepo:    store,
		validator:    val,
		renderer:     rend,
		llm:          llmService,
		defaultUser:  defaultUser,
		branchID:     branch.ID,
	}
}

func submitPendingVersionWithQuota(
	ctx context.Context,
	t *testing.T,
	store *memory.Store,
	userID, branchID, versionID string,
	opts core.PendingJobOptions,
) *core.BranchVersion {
	t.Helper()
	rec, err := store.ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 100, 1000000)
	if err != nil {
		t.Fatalf("ReserveQuota failed: %v", err)
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = core.DefaultJobTTL
	}
	exp := now.Add(ttl)
	ver := &core.BranchVersion{
		ID:                versionID,
		UserID:            userID,
		BranchID:          branchID,
		ResumeID:          branchID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		SourceType:        core.SourceTypeLLMGeneration,
		ReservationID:     rec.ID,
		ExpiresAt:         &exp,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if _, err := store.SubmitPendingVersion(ctx, ver, opts); err != nil {
		_ = store.ReleaseQuota(ctx, rec.ID, err.Error())
		t.Fatalf("SubmitPendingVersion failed: %v", err)
	}
	return ver
}

func submitPendingPDFWithQuota(
	ctx context.Context,
	t *testing.T,
	store *memory.Store,
	notifier core.JobNotifier,
	userID, rawText, provider, model string,
	ttl time.Duration,
	clock func() time.Time,
) *core.PDFParseJob {
	t.Helper()
	job, err := worker.SubmitPDFJob(ctx, worker.PDFSubmitConfig{
		PDFJobs:        store,
		Quotas:         store,
		Notifier:       notifier,
		JobTTL:         ttl,
		CleanupTimeout: 2 * time.Second,
		Clock:          clock,
	}, worker.PDFJobSubmission{
		UserID:       userID,
		Filename:     "resume.pdf",
		Provider:     provider,
		Model:        model,
		RawText:      rawText,
		DailyLimit:   100,
		MonthlyLimit: 1000000,
	})
	if err != nil {
		t.Fatalf("SubmitPDFJob failed: %v", err)
	}
	return job
}

func waitForVersionStatus(
	ctx context.Context,
	t *testing.T,
	store worker.BranchStore,
	branchID, versionID string,
	timeout time.Duration,
	pred func(*core.BranchVersion) bool,
) *core.BranchVersion {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		v, err := store.Version(ctx, branchID, versionID)
		if err == nil && pred(v) {
			return v
		}
		if time.Now().After(deadline) {
			status := core.VersionStatus("")
			errMsg := ""
			if v != nil {
				status = v.Status
				errMsg = v.ErrorMessage
			}
			t.Fatalf("timed out waiting for version %s (last status=%q err=%q, fetchErr=%v)", versionID, status, errMsg, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForPDFStatus(
	ctx context.Context,
	t *testing.T,
	store worker.PDFJobStore,
	userID, jobID string,
	timeout time.Duration,
	pred func(*core.PDFParseJob) bool,
) *core.PDFParseJob {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		j, err := store.Job(ctx, userID, jobID)
		if err == nil && pred(j) {
			return j
		}
		if time.Now().After(deadline) {
			status := core.PDFParseJobStatus("")
			errMsg := ""
			if j != nil {
				status = j.Status
				errMsg = j.ErrorMessage
			}
			t.Fatalf("timed out waiting for pdf job %s (last status=%q err=%q, fetchErr=%v)", jobID, status, errMsg, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func reservationStatus(ctx context.Context, t *testing.T, store *memory.Store, userID, reservationID string) core.ReservationStatus {
	t.Helper()
	records, err := store.ListUsageRecords(ctx, userID, 100)
	if err != nil {
		t.Fatalf("ListUsageRecords failed: %v", err)
	}
	for _, r := range records {
		if r.ID == reservationID {
			return r.Status
		}
	}
	t.Fatalf("reservation %s not found for user %s", reservationID, userID)
	return ""
}

// 1. Parallel Execution Test
func TestPool_ParallelExecution(t *testing.T) {
	env := setupTestWorkerEnv(t)
	env.llm.delay = 15 * time.Millisecond

	pool := worker.New(worker.Config{
		Concurrency:  4,
		PollInterval: 50 * time.Millisecond,
		Branches:     env.branchRepo,
		Profiles:     env.profileRepo,
		Presets:      env.presetRepo,
		Templates:    env.templateRepo,
		Quotas:       env.quotaRepo,
		Generator:    env.llm,
		Validator:    env.validator,
		Renderer:     env.renderer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	const numJobs = 16
	type branchVerPair struct {
		branchID  string
		versionID string
	}
	pairs := make([]branchVerPair, numJobs)

	for i := 0; i < numJobs; i++ {
		br := core.Branch{
			ID:            fmt.Sprintf("branch-parallel-%d", i),
			UserID:        env.defaultUser,
			Name:          fmt.Sprintf("Parallel Branch %d", i),
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateBranch(ctx, &br); err != nil {
			t.Fatalf("failed to create branch %d: %v", i, err)
		}
		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, br.ID, fmt.Sprintf("ver-parallel-%d", i), core.PendingJobOptions{
			MaxPendingPerTenant: 25,
		})

		pairs[i] = branchVerPair{branchID: br.ID, versionID: ver.ID}
		pool.Notify()
	}

	for _, pair := range pairs {
		ver := waitForVersionStatus(ctx, t, env.branchRepo, pair.branchID, pair.versionID, 5*time.Second, func(v *core.BranchVersion) bool {
			return v.Status.IsTerminal()
		})

		if ver.Status != core.VersionStatusCompleted {
			t.Fatalf("job %s unexpectedly terminated in %s: %s", pair.versionID, ver.Status, ver.ErrorMessage)
		}
		if ver.HTMLOutput == "" || ver.MarkdownOutput == "" || ver.PlainTextOutput == "" {
			t.Fatalf("expected rendered outputs on version %s, got empty", pair.versionID)
		}

		b, err := env.branchRepo.Branch(ctx, env.defaultUser, pair.branchID)
		if err != nil {
			t.Fatalf("failed to get branch %s: %v", pair.branchID, err)
		}
		if b.ActiveVersionID == nil || *b.ActiveVersionID != pair.versionID {
			t.Errorf("expected branch.ActiveVersionID == %q, got %v", pair.versionID, b.ActiveVersionID)
		}
		if b.LastSyncedMainRevision != 1 {
			t.Errorf("expected LastSyncedMainRevision 1, got %d", b.LastSyncedMainRevision)
		}
	}
}

// 2. Backpressure & Saturation Test (Persisted MaxPendingPerTenant + Claiming Concurrency Saturation)
func TestPool_BackpressureAndSaturation(t *testing.T) {
	env := setupTestWorkerEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create 3 branches so each can hold a pending version
	branches := make([]string, 3)
	for i := range branches {
		b := core.Branch{
			ID:            fmt.Sprintf("branch-sat-%d", i),
			UserID:        env.defaultUser,
			Name:          fmt.Sprintf("Sat Branch %d", i),
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateBranch(ctx, &b); err != nil {
			t.Fatalf("CreateBranch %d failed: %v", i, err)
		}
		branches[i] = b.ID
	}

	// Verify persisted MaxPendingPerTenant rejects 3rd pending job with ErrPendingLimitExceeded / ErrQueueFull
	_ = submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, branches[0], "ver-sat-0", core.PendingJobOptions{
		MaxPendingPerTenant: 2,
	})

	_ = submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, branches[1], "ver-sat-1", core.PendingJobOptions{
		MaxPendingPerTenant: 2,
	})

	recOver, err := env.store.ReserveQuota(ctx, env.defaultUser, core.OperationResumeTailoring, 2000, 100, 1000000)
	if err != nil {
		t.Fatalf("ReserveQuota failed: %v", err)
	}
	exp := time.Now().UTC().Add(core.DefaultJobTTL)
	verOver := &core.BranchVersion{
		ID:                "ver-sat-overflow",
		UserID:            env.defaultUser,
		BranchID:          branches[2],
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		ReservationID:     recOver.ID,
		ExpiresAt:         &exp,
	}
	_, submitErr := env.store.SubmitPendingVersion(ctx, verOver, core.PendingJobOptions{MaxPendingPerTenant: 2})
	if !errors.Is(submitErr, core.ErrPendingLimitExceeded) || !errors.Is(submitErr, core.ErrQueueFull) {
		t.Fatalf("expected ErrPendingLimitExceeded / ErrQueueFull, got %v", submitErr)
	}
	_ = env.store.ReleaseQuota(ctx, recOver.ID, "rejected by pending limit")

	// Also verify PDF SubmitPDFJob enforces MaxPendingPerTenant and releases quota on rejection
	_, pdfErr := worker.SubmitPDFJob(ctx, worker.PDFSubmitConfig{
		PDFJobs:             env.store,
		Quotas:              env.store,
		MaxPendingPerTenant: 2,
	}, worker.PDFJobSubmission{
		UserID:       env.defaultUser,
		RawText:      "overflow pdf text",
		DailyLimit:   100,
		MonthlyLimit: 1000000,
	})
	if !errors.Is(pdfErr, core.ErrPendingLimitExceeded) || !errors.Is(pdfErr, core.ErrQueueFull) {
		t.Fatalf("expected SubmitPDFJob ErrPendingLimitExceeded / ErrQueueFull, got %v", pdfErr)
	}

	// Verify direct claiming saturation with Concurrency=1 leaves remaining pending jobs in store until slot frees
	unblock := make(chan struct{})
	started := make(chan string, 2)
	blockingLLM := &blockingMockLLM{unblock: unblock, started: started}

	pool := worker.New(worker.Config{
		Concurrency:  1,
		PollInterval: 50 * time.Millisecond,
		Branches:     env.branchRepo,
		Profiles:     env.profileRepo,
		Presets:      env.presetRepo,
		Templates:    env.templateRepo,
		Quotas:       env.quotaRepo,
		Generator:    blockingLLM,
		Validator:    env.validator,
		Renderer:     env.renderer,
	})

	pool.Start(ctx)
	defer func() {
		select {
		case <-unblock:
		default:
			close(unblock)
		}
		pool.Stop()
	}()

	pool.Notify()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for 1st job to start processing")
	}

	// Give dispatcher a brief moment; 2nd job MUST stay pending while slot is saturated
	time.Sleep(40 * time.Millisecond)
	v0, _ := env.branchRepo.Version(ctx, branches[0], "ver-sat-0")
	v1, _ := env.branchRepo.Version(ctx, branches[1], "ver-sat-1")
	processingCount := 0
	pendingCount := 0
	for _, v := range []*core.BranchVersion{v0, v1} {
		switch v.Status {
		case core.VersionStatusProcessing:
			processingCount++
		case core.VersionStatusPending:
			pendingCount++
		}
	}
	if processingCount != 1 || pendingCount != 1 {
		t.Fatalf("expected 1 processing and 1 pending under Concurrency=1 saturation, got v0=%s v1=%s", v0.Status, v1.Status)
	}

	close(unblock)

	for i, vID := range []string{"ver-sat-0", "ver-sat-1"} {
		done := waitForVersionStatus(ctx, t, env.branchRepo, branches[i], vID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusCompleted
		})

		if done.Status != core.VersionStatusCompleted {
			t.Fatalf("expected %s completed, got %s", vID, done.Status)
		}
	}
}

type blockingMockLLM struct {
	unblock chan struct{}
	started chan string
}

func (m *blockingMockLLM) GenerateTailoredResume(ctx context.Context, req core.GenerationRequest) (*core.GenerationResponse, error) {
	if m.started != nil {
		select {
		case m.started <- req.BranchContext.ID:
		default:
		}
	}
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
	ctx := context.Background()

	unblock := make(chan struct{})
	started := make(chan string, 4)
	llmMock := &blockingMockLLM{unblock: unblock, started: started}

	pool := worker.New(worker.Config{
		Concurrency:            2,
		MaxConcurrentPerTenant: 2,
		PollInterval:           50 * time.Millisecond,
		Branches:               env.branchRepo,
		Profiles:               env.profileRepo,
		Presets:                env.presetRepo,
		Templates:              env.templateRepo,
		Quotas:                 env.quotaRepo,
		Generator:              llmMock,
		Validator:              env.validator,
		Renderer:               env.renderer,
	})

	pool.Start(ctx)

	// Create 4 branches: 2 will be claimed immediately and active, 2 will stay pending in store
	branches := make([]string, 4)
	versions := make([]*core.BranchVersion, 4)
	for i := 0; i < 4; i++ {
		b := core.Branch{
			ID:            fmt.Sprintf("branch-drain-%d", i),
			UserID:        env.defaultUser,
			Name:          fmt.Sprintf("Drain Branch %d", i),
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateBranch(ctx, &b); err != nil {
			t.Fatalf("CreateBranch %d failed: %v", i, err)
		}
		branches[i] = b.ID
	}

	for i := 0; i < 2; i++ {
		versions[i] = submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, branches[i], fmt.Sprintf("ver-drain-active-%d", i), core.PendingJobOptions{})
	}
	pool.Notify()

	// Wait until both concurrency slots are actively in-flight
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for active job %d to enter LLM", i)
		}
	}

	// Persist 2 more pending versions while slots are saturated
	for i := 2; i < 4; i++ {
		versions[i] = submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, branches[i], fmt.Sprintf("ver-drain-pending-%d", i), core.PendingJobOptions{})
	}

	// Trigger Stop while 2 jobs are in-flight and 2 are unclaimed pending
	stopDone := make(chan struct{})
	go func() {
		pool.Stop()
		close(stopDone)
	}()

	// Give Stop a moment to cancel claimCtx, then unblock active jobs
	time.Sleep(30 * time.Millisecond)
	close(unblock)

	select {
	case <-stopDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("pool.Stop() timed out")
	}

	// Active jobs must have drained to completed
	for i := 0; i < 2; i++ {
		ver, err := env.branchRepo.Version(ctx, branches[i], versions[i].ID)
		if err != nil {
			t.Fatalf("failed to get active version %d: %v", i, err)
		}
		if ver.Status != core.VersionStatusCompleted {
			t.Errorf("expected active version %s completed after drain, got %s", ver.ID, ver.Status)
		}
	}

	// Unclaimed jobs must remain safely persisted in pending state (with reservations intact)
	for i := 2; i < 4; i++ {
		ver, err := env.branchRepo.Version(ctx, branches[i], versions[i].ID)
		if err != nil {
			t.Fatalf("failed to get pending version %d: %v", i, err)
		}
		if ver.Status != core.VersionStatusPending {
			t.Errorf("expected unclaimed version %s to remain pending after Stop, got %s", ver.ID, ver.Status)
		}
		if got := reservationStatus(ctx, t, env.store, env.defaultUser, ver.ReservationID); got != core.ReservationStatusPending {
			t.Errorf("expected unclaimed reservation %s to stay pending, got %s", ver.ReservationID, got)
		}
	}

	// Enqueue/Notify adapters remain safe and non-blocking even after Stop
	if err := pool.Enqueue(core.GenerationJob{UserID: env.defaultUser, BranchID: env.branchID, VersionID: "ver-post-stop"}); err != nil {
		t.Errorf("expected Enqueue wakeup adapter to return nil after stop, got %v", err)
	}
}

// 4. Startup Crash Recovery Test
func TestPool_StartupCrashRecovery(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC))
	env := setupTestWorkerEnvWithClock(t, clk.Now)
	ctx := context.Background()

	// Create 3 branches for: expired pending, interrupted processing, unexpired pending
	var branches []string
	for i := 0; i < 3; i++ {
		b := core.Branch{
			ID:            fmt.Sprintf("branch-crash-%d", i),
			UserID:        env.defaultUser,
			Name:          fmt.Sprintf("Crash Branch %d", i),
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateBranch(ctx, &b); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}
		branches = append(branches, b.ID)
	}

	resExpired, _ := env.store.ReserveQuota(ctx, env.defaultUser, core.OperationResumeTailoring, 2000, 100, 1000000)
	resProcessing, _ := env.store.ReserveQuota(ctx, env.defaultUser, core.OperationResumeTailoring, 2000, 100, 1000000)
	resUnexpired, _ := env.store.ReserveQuota(ctx, env.defaultUser, core.OperationResumeTailoring, 2000, 100, 1000000)

	pastExp := clk.Now().Add(-5 * time.Minute)
	futureExp := clk.Now().Add(20 * time.Minute)

	vExpired := &core.BranchVersion{
		ID:                "ver-crash-expired",
		UserID:            env.defaultUser,
		BranchID:          branches[0],
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		ReservationID:     resExpired.ID,
		ExpiresAt:         &pastExp,
	}
	vProcessing := &core.BranchVersion{
		ID:                "ver-crash-processing",
		UserID:            env.defaultUser,
		BranchID:          branches[1],
		Status:            core.VersionStatusProcessing,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		ReservationID:     resProcessing.ID,
		ExpiresAt:         &futureExp,
	}
	vUnexpired := &core.BranchVersion{
		ID:                "ver-crash-unexpired",
		UserID:            env.defaultUser,
		BranchID:          branches[2],
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		ReservationID:     resUnexpired.ID,
		ExpiresAt:         &futureExp,
	}
	for _, v := range []*core.BranchVersion{vExpired, vProcessing, vUnexpired} {
		if err := env.branchRepo.CreateVersion(ctx, v); err != nil {
			t.Fatalf("CreateVersion %s failed: %v", v.ID, err)
		}
	}

	pool := worker.New(worker.Config{
		Concurrency:  2,
		PollInterval: 20 * time.Millisecond,
		Clock:        clk.Now,
		Branches:     env.branchRepo,
		Profiles:     env.profileRepo,
		Presets:      env.presetRepo,
		Templates:    env.templateRepo,
		PDFJobs:      env.pdfRepo,
		Quotas:       env.quotaRepo,
		Generator:    env.llm,
		Validator:    env.validator,
		Renderer:     env.renderer,
	})

	const crashReason = "server restarted while generation job was in flight"
	if err := pool.RecoverStaleJobs(ctx, crashReason); err != nil {
		t.Fatalf("RecoverStaleJobs failed: %v", err)
	}

	// Expired pending and processing jobs must be failed with quota released
	for idx, v := range []*core.BranchVersion{vExpired, vProcessing} {
		reloaded, err := env.branchRepo.Version(ctx, branches[idx], v.ID)
		if err != nil {
			t.Fatalf("Version %s failed: %v", v.ID, err)
		}
		if reloaded.Status != core.VersionStatusFailed {
			t.Errorf("expected %s failed, got %s", v.ID, reloaded.Status)
		}
		if reloaded.ErrorMessage != crashReason {
			t.Errorf("expected reason %q, got %q", crashReason, reloaded.ErrorMessage)
		}
		if reloaded.CompletedAt == nil {
			t.Errorf("expected CompletedAt on %s", v.ID)
		}
		if st := reservationStatus(ctx, t, env.store, env.defaultUser, v.ReservationID); st != core.ReservationStatusReleased {
			t.Errorf("expected reservation %s released, got %s", v.ReservationID, st)
		}
	}

	// Unexpired pending job must remain pending with reservation intact until pool starts and completes it
	reloadedPending, err := env.branchRepo.Version(ctx, branches[2], vUnexpired.ID)
	if err != nil {
		t.Fatalf("Version %s failed: %v", vUnexpired.ID, err)
	}
	if reloadedPending.Status != core.VersionStatusPending {
		t.Fatalf("expected unexpired pending version preserved, got %s", reloadedPending.Status)
	}
	if st := reservationStatus(ctx, t, env.store, env.defaultUser, resUnexpired.ID); st != core.ReservationStatusPending {
		t.Fatalf("expected unexpired reservation still pending, got %s", st)
	}

	pool.Start(ctx)
	defer pool.Stop()

	completed := waitForVersionStatus(ctx, t, env.branchRepo, branches[2], vUnexpired.ID, 2*time.Second, func(v *core.BranchVersion) bool {
		return v.Status.IsTerminal()
	})

	if completed.Status != core.VersionStatusCompleted {
		t.Fatalf("expected preserved pending version to complete after Start, got %s (%s)", completed.Status, completed.ErrorMessage)
	}
	if st := reservationStatus(ctx, t, env.store, env.defaultUser, resUnexpired.ID); st != core.ReservationStatusCommitted {
		t.Errorf("expected preserved job reservation committed after completion, got %s", st)
	}
}

// 5. LLM Error Transition Test
func TestPool_LLMErrorTransition(t *testing.T) {
	env := setupTestWorkerEnv(t)
	env.llm.fail = true
	env.llm.failErrMsg = "invalid prompt or bad request"

	pool := worker.New(worker.Config{
		Concurrency:  2,
		PollInterval: 50 * time.Millisecond,
		Branches:     env.branchRepo,
		Profiles:     env.profileRepo,
		Presets:      env.presetRepo,
		Templates:    env.templateRepo,
		Quotas:       env.quotaRepo,
		Generator:    env.llm,
		Validator:    env.validator,
		Renderer:     env.renderer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-llm-fail", core.PendingJobOptions{})
	pool.Notify()

	failed := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 2*time.Second, func(v *core.BranchVersion) bool {
		return v.Status == core.VersionStatusFailed
	})

	if failed.ErrorMessage == "" {
		t.Errorf("expected error message on failed version")
	}
	if st := reservationStatus(ctx, t, env.store, env.defaultUser, ver.ReservationID); st != core.ReservationStatusReleased {
		t.Errorf("expected reservation released on LLM failure, got %s", st)
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
		Concurrency:  2,
		PollInterval: 50 * time.Millisecond,
		Branches:     env.branchRepo,
		Profiles:     env.profileRepo,
		Presets:      env.presetRepo,
		Templates:    env.templateRepo,
		Quotas:       env.quotaRepo,
		Generator:    env.llm,
		Validator:    env.validator,
		Renderer:     env.renderer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-warn-1", core.PendingJobOptions{})
	pool.Notify()

	done := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 2*time.Second, func(v *core.BranchVersion) bool {
		return v.Status.IsTerminal()
	})

	if done.Status != core.VersionStatusCompletedWithWarnings {
		t.Fatalf("expected completed_with_warnings, got %s (%s)", done.Status, done.ErrorMessage)
	}
	if len(done.ValidationWarnings) == 0 {
		t.Errorf("expected validation warnings, got none")
	}
}

// 7. RecoverStaleJobs across versions + PDF jobs
func TestPool_RecoverStaleJobs(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 11, 15, 11, 0, 0, 0, time.UTC))
	env := setupTestWorkerEnvWithClock(t, clk.Now)
	ctx := context.Background()

	b2 := core.Branch{
		ID:            "branch-stale-2",
		UserID:        env.defaultUser,
		Name:          "Stale Branch 2",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateBranch(ctx, &b2); err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}

	resV1, _ := env.store.ReserveQuota(ctx, env.defaultUser, core.OperationResumeTailoring, 2000, 100, 1000000)
	resV2, _ := env.store.ReserveQuota(ctx, env.defaultUser, core.OperationResumeTailoring, 2000, 100, 1000000)
	resP1, _ := env.store.ReserveQuota(ctx, env.defaultUser, core.OperationPDFParse, 1000, 100, 1000000)
	resP2, _ := env.store.ReserveQuota(ctx, env.defaultUser, core.OperationPDFParse, 1000, 100, 1000000)

	futureExp := clk.Now().Add(15 * time.Minute)
	vPending := &core.BranchVersion{
		ID:                "ver-stale-pending",
		UserID:            env.defaultUser,
		BranchID:          env.branchID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		ReservationID:     resV1.ID,
		ExpiresAt:         &futureExp,
	}
	vProcessing := &core.BranchVersion{
		ID:                "ver-stale-processing",
		UserID:            env.defaultUser,
		BranchID:          b2.ID,
		Status:            core.VersionStatusProcessing,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		ReservationID:     resV2.ID,
		ExpiresAt:         &futureExp,
	}
	_ = env.branchRepo.CreateVersion(ctx, vPending)
	_ = env.branchRepo.CreateVersion(ctx, vProcessing)

	pdfPending := &core.PDFParseJob{
		ID:            "pdf-stale-pending",
		UserID:        env.defaultUser,
		Status:        core.PDFParseJobStatusPending,
		RawText:       "Unexpired PDF raw text",
		ReservationID: resP1.ID,
		ExpiresAt:     &futureExp,
	}
	pdfProcessing := &core.PDFParseJob{
		ID:            "pdf-stale-processing",
		UserID:        env.defaultUser,
		Status:        core.PDFParseJobStatusProcessing,
		RawText:       "Interrupted PDF raw text",
		ReservationID: resP2.ID,
		ExpiresAt:     &futureExp,
	}
	_ = env.pdfRepo.CreateJob(ctx, pdfPending)
	_ = env.pdfRepo.CreateJob(ctx, pdfProcessing)

	ext := &mockResumeExtractor{result: &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Recovered PDF"}}}
	pool := worker.New(worker.Config{
		Concurrency:     2,
		PollInterval:    20 * time.Millisecond,
		Clock:           clk.Now,
		Branches:        env.branchRepo,
		Profiles:        env.profileRepo,
		Presets:         env.presetRepo,
		Templates:       env.templateRepo,
		PDFJobs:         env.pdfRepo,
		Quotas:          env.quotaRepo,
		ResumeExtractor: ext,
		Generator:       env.llm,
		Validator:       env.validator,
		Renderer:        env.renderer,
	})

	const reason = "server restart recovery test"
	if err := pool.RecoverStaleJobs(ctx, reason); err != nil {
		t.Fatalf("RecoverStaleJobs failed: %v", err)
	}

	// Processing jobs must fail and release quota
	refV2, _ := env.branchRepo.Version(ctx, b2.ID, vProcessing.ID)
	if refV2.Status != core.VersionStatusFailed || refV2.ErrorMessage != reason {
		t.Errorf("expected processing version failed with %q, got %s (%q)", reason, refV2.Status, refV2.ErrorMessage)
	}
	if st := reservationStatus(ctx, t, env.store, env.defaultUser, resV2.ID); st != core.ReservationStatusReleased {
		t.Errorf("expected version processing quota released, got %s", st)
	}

	refP2, _ := env.pdfRepo.Job(ctx, env.defaultUser, pdfProcessing.ID)
	if refP2.Status != core.PDFParseJobStatusFailed || refP2.ErrorMessage != reason {
		t.Errorf("expected processing pdf job failed with %q, got %s (%q)", reason, refP2.Status, refP2.ErrorMessage)
	}
	if refP2.RawText != "" {
		t.Errorf("expected processing pdf RawText pruned on recovery failure, got %q", refP2.RawText)
	}
	if st := reservationStatus(ctx, t, env.store, env.defaultUser, resP2.ID); st != core.ReservationStatusReleased {
		t.Errorf("expected pdf processing quota released, got %s", st)
	}

	// Unexpired pending jobs must remain pending and execute once pool starts
	pool.Start(ctx)
	defer pool.Stop()

	doneV1 := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, vPending.ID, 2*time.Second, func(v *core.BranchVersion) bool {
		return v.Status == core.VersionStatusCompleted
	})

	if doneV1.Status != core.VersionStatusCompleted {
		t.Fatalf("expected preserved pending version completed, got %s", doneV1.Status)
	}
	doneP1 := waitForPDFStatus(ctx, t, env.pdfRepo, env.defaultUser, pdfPending.ID, 2*time.Second, func(j *core.PDFParseJob) bool {
		return j.Status == core.PDFParseJobStatusCompleted
	})

	if doneP1.Result == nil || doneP1.Result.Basics.Name != "Recovered PDF" {
		t.Fatalf("expected preserved pending pdf job completed cleanly, got %+v", doneP1)
	}
}

// 8. Panic Recovery Test
func TestPool_WorkerPanicRecovery(t *testing.T) {
	env := setupTestWorkerEnv(t)
	env.llm.mu.Lock()
	env.llm.shouldPanic = true
	env.llm.panicVal = "unexpected runtime nil pointer during model invocation"
	env.llm.mu.Unlock()

	pool := worker.New(worker.Config{
		Concurrency:  1,
		PollInterval: 50 * time.Millisecond,
		Branches:     env.branchRepo,
		Profiles:     env.profileRepo,
		Presets:      env.presetRepo,
		Templates:    env.templateRepo,
		Quotas:       env.quotaRepo,
		Generator:    env.llm,
		Validator:    env.validator,
		Renderer:     env.renderer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	verPanic := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-panic-1", core.PendingJobOptions{})
	pool.Notify()

	failed := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, verPanic.ID, 2*time.Second, func(v *core.BranchVersion) bool {
		return v.Status == core.VersionStatusFailed
	})

	if failed.ErrorMessage == "" {
		t.Errorf("expected non-empty error message on panic failure")
	}
	if st := reservationStatus(ctx, t, env.store, env.defaultUser, verPanic.ReservationID); st != core.ReservationStatusReleased {
		t.Errorf("expected quota released on worker panic, got %s", st)
	}

	env.llm.mu.Lock()
	env.llm.shouldPanic = false
	env.llm.panicVal = nil
	env.llm.mu.Unlock()

	verSuccess := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-success-2", core.PendingJobOptions{})
	pool.Notify()

	completed := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, verSuccess.ID, 2*time.Second, func(v *core.BranchVersion) bool {
		return v.Status.IsTerminal()
	})

	if completed.Status != core.VersionStatusCompleted {
		t.Fatalf("second job unexpectedly terminated in %s: %s", completed.Status, completed.ErrorMessage)
	}
}

type mockResumeExtractor struct {
	mu           sync.Mutex
	delay        time.Duration
	fail         bool
	failErr      error
	failErrMsg   string
	result       *core.JSONResume
	usage        core.TokenUsage
	requestCount int
}

func (m *mockResumeExtractor) ExtractResume(ctx context.Context, _, _, _ string) (*core.JSONResume, core.TokenUsage, error) {
	m.mu.Lock()
	m.requestCount++
	delay := m.delay
	fail := m.fail
	failErr := m.failErr
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
	if failErr != nil {
		return nil, core.TokenUsage{}, failErr
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

// 9. PDF Parse Job Success
func TestWorkerPool_PDFParseJob_Success(t *testing.T) {
	store := memory.New()
	var capturedText, capturedProvider, capturedModel string
	mockExt := &callbackResumeExtractor{
		fn: func(_ context.Context, text, provider, model string) (*core.JSONResume, error) {
			capturedText = text
			capturedProvider = provider
			capturedModel = model
			return &core.JSONResume{
				Basics: core.JSONResumeBasics{Name: "Grace Hopper"},
			}, nil
		},
	}

	pool := worker.New(worker.Config{
		Concurrency:     2,
		PollInterval:    50 * time.Millisecond,
		PDFJobs:         store,
		Quotas:          store,
		ResumeExtractor: mockExt,
		LLMTimeout:      5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	job := submitPendingPDFWithQuota(ctx, t, store, pool, "user-1", "Admiral Grace Hopper resume text", "mock", "mock-model", core.DefaultJobTTL, nil)

	done := waitForPDFStatus(ctx, t, store, "user-1", job.ID, 2*time.Second, func(j *core.PDFParseJob) bool {
		return j.Status.IsTerminal()
	})

	if done.Status != core.PDFParseJobStatusCompleted {
		t.Fatalf("expected completed, got %s (%s)", done.Status, done.ErrorMessage)
	}
	if done.CompletedAt == nil {
		t.Errorf("expected CompletedAt to be set")
	}
	if done.Result == nil || done.Result.Basics.Name != "Grace Hopper" {
		t.Fatalf("unexpected JSONResume result: %+v", done.Result)
	}
	if done.RawText != "" {
		t.Errorf("expected RawText pruned after completion, got %q", done.RawText)
	}
	if capturedText != "Admiral Grace Hopper resume text" || capturedProvider != "mock" || capturedModel != "mock-model" {
		t.Errorf("extractor received unexpected persisted fields: text=%q provider=%q model=%q", capturedText, capturedProvider, capturedModel)
	}
}

// 10. PDF Parse Job Failure
func TestWorkerPool_PDFParseJob_Failure(t *testing.T) {
	store := memory.New()
	mockExt := &mockResumeExtractor{
		fail:       true,
		failErrMsg: "upstream model capacity reached",
	}

	pool := worker.New(worker.Config{
		Concurrency:     2,
		PollInterval:    50 * time.Millisecond,
		PDFJobs:         store,
		Quotas:          store,
		ResumeExtractor: mockExt,
		LLMTimeout:      5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	job := submitPendingPDFWithQuota(ctx, t, store, pool, "user-1", "Some text", "mock", "mock-model", core.DefaultJobTTL, nil)

	done := waitForPDFStatus(ctx, t, store, "user-1", job.ID, 2*time.Second, func(j *core.PDFParseJob) bool {
		return j.Status.IsTerminal()
	})

	if done.Status != core.PDFParseJobStatusFailed {
		t.Fatalf("expected job to fail, got %s", done.Status)
	}
	if !strings.Contains(done.ErrorMessage, "Failed to extract resume from document") {
		t.Errorf("expected sanitized error message, got %q", done.ErrorMessage)
	}
	if done.RawText != "" {
		t.Errorf("expected RawText pruned on failure, got %q", done.RawText)
	}
	if st := reservationStatus(ctx, t, store, "user-1", job.ReservationID); st != core.ReservationStatusReleased {
		t.Errorf("expected quota reservation released on PDF failure, got %s", st)
	}
}

// 11. LLMTimeout Context Bounding Test
func TestWorkerPool_TailoringPipeline_LLMTimeout(t *testing.T) {
	env := setupTestWorkerEnv(t)
	llmMock := &mockLLM{}

	pool := worker.New(worker.Config{
		Concurrency:  1,
		PollInterval: 50 * time.Millisecond,
		Branches:     env.branchRepo,
		Profiles:     env.profileRepo,
		Presets:      env.presetRepo,
		Templates:    env.templateRepo,
		Quotas:       env.quotaRepo,
		Generator:    llmMock,
		Validator:    env.validator,
		Renderer:     env.renderer,
		LLMTimeout:   7 * time.Minute,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-timeout-check-1", core.PendingJobOptions{})
	pool.Notify()

	done := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 3*time.Second, func(v *core.BranchVersion) bool {
		return v.Status.IsTerminal()
	})

	if done.Status != core.VersionStatusCompleted {
		t.Fatalf("expected completed, got %s (%s)", done.Status, done.ErrorMessage)
	}

	llmMock.mu.Lock()
	capturedCtx := llmMock.capturedCtx
	llmMock.mu.Unlock()

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

// 12. Per-Tenant Concurrency Cap Test (PDF jobs)
func TestPool_PerTenantConcurrencyCap(t *testing.T) {
	store := memory.New()
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
		PollInterval:           20 * time.Millisecond,
		PDFJobs:                store,
		Quotas:                 store,
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
		j := submitPendingPDFWithQuota(ctx, t, store, pool, tenantID, fmt.Sprintf("resume %d", i), "mock", "m", core.DefaultJobTTL, nil)
		jobIDs[i] = j.ID
	}

	for _, id := range jobIDs {
		done := waitForPDFStatus(ctx, t, store, tenantID, id, 5*time.Second, func(j *core.PDFParseJob) bool {
			return j.Status.IsTerminal()
		})

		if done.Status != core.PDFParseJobStatusCompleted {
			t.Fatalf("job %s failed: %s", id, done.ErrorMessage)
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
	store := memory.New()

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
		PollInterval:           20 * time.Millisecond,
		PDFJobs:                store,
		Quotas:                 store,
		ResumeExtractor:        mockExt,
		LLMTimeout:             5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	tenantA := "tenant-a-noisy"
	const tenantAJobs = 8
	tenantAJobIDs := make([]string, tenantAJobs)
	for i := 0; i < tenantAJobs; i++ {
		j := submitPendingPDFWithQuota(ctx, t, store, pool, tenantA, fmt.Sprintf("slow-%d", i), "slow", "m", core.DefaultJobTTL, nil)
		tenantAJobIDs[i] = j.ID
	}

	time.Sleep(15 * time.Millisecond)

	tenantB := "tenant-b-fast"
	jB := submitPendingPDFWithQuota(ctx, t, store, pool, tenantB, "fast-0", "fast", "m", core.DefaultJobTTL, nil)

	doneB := waitForPDFStatus(ctx, t, store, tenantB, jB.ID, 500*time.Millisecond, func(j *core.PDFParseJob) bool {
		return j.Status == core.PDFParseJobStatusCompleted
	})

	if doneB.Status != core.PDFParseJobStatusCompleted {
		t.Fatalf("expected tenant B job completed")
	}

	var remainingA int
	for _, id := range tenantAJobIDs {
		ja, err := store.Job(ctx, tenantA, id)
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
	store := memory.New()
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

	pool := worker.New(worker.Config{
		Concurrency:            1,
		MaxConcurrentPerTenant: 1,
		PollInterval:           50 * time.Millisecond,
		PDFJobs:                store,
		Quotas:                 store,
		ResumeExtractor:        mockExt,
		LLMTimeout:             5 * time.Second,
	})

	ctx := context.Background()
	base := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	tenants := []string{"tenant-a", "tenant-b", "tenant-c"}
	totalJobs := 0
	for idx, tID := range tenants {
		for j := 1; j <= 2; j++ {
			rec, err := store.ReserveQuota(ctx, tID, core.OperationPDFParse, 1000, 100, 1000000)
			if err != nil {
				t.Fatalf("ReserveQuota failed: %v", err)
			}
			ts := base.Add(time.Duration(idx*10+j) * time.Second)
			exp := time.Now().UTC().Add(core.DefaultJobTTL)
			job := &core.PDFParseJob{
				ID:            fmt.Sprintf("%s-job-%d", tID, j),
				UserID:        tID,
				Status:        core.PDFParseJobStatusPending,
				RawText:       tID,
				ReservationID: rec.ID,
				ExpiresAt:     &exp,
				CreatedAt:     ts,
				UpdatedAt:     ts,
			}
			if err := store.CreateJob(ctx, job); err != nil {
				t.Fatalf("create job failed: %v", err)
			}
			totalJobs++
		}
	}

	pool.Start(ctx)

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
	store := memory.New()
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

	pool := worker.New(worker.Config{
		Concurrency:            2,
		MaxConcurrentPerTenant: 1,
		PollInterval:           50 * time.Millisecond,
		PDFJobs:                store,
		Quotas:                 store,
		ResumeExtractor:        mockExt,
		LLMTimeout:             5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	tenantID := "tenant-panic-test"
	j1 := submitPendingPDFWithQuota(ctx, t, store, pool, tenantID, "panic text", "mock", "m", core.DefaultJobTTL, nil)

	done1 := waitForPDFStatus(ctx, t, store, tenantID, j1.ID, 2*time.Second, func(j *core.PDFParseJob) bool {
		return j.Status == core.PDFParseJobStatusFailed
	})

	if done1.RawText != "" {
		t.Errorf("expected RawText pruned on panic failure, got %q", done1.RawText)
	}
	if st := reservationStatus(ctx, t, store, tenantID, j1.ReservationID); st != core.ReservationStatusReleased {
		t.Errorf("expected quota released on PDF panic, got %s", st)
	}

	// Enqueue job 2 for same tenant; must execute since slot was freed on panic
	j2 := submitPendingPDFWithQuota(ctx, t, store, pool, tenantID, "recovery text", "mock", "m", core.DefaultJobTTL, nil)
	done2 := waitForPDFStatus(ctx, t, store, tenantID, j2.ID, 2*time.Second, func(j *core.PDFParseJob) bool {
		return j.Status.IsTerminal()
	})

	if done2.Status != core.PDFParseJobStatusCompleted {
		t.Fatalf("job 2 failed unexpectedly: %s (%s)", done2.Status, done2.ErrorMessage)
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

	llmMock := &callbackLLM{
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
		PollInterval:           20 * time.Millisecond,
		Branches:               env.branchRepo,
		Profiles:               env.profileRepo,
		Presets:                env.presetRepo,
		Templates:              env.templateRepo,
		Quotas:                 env.quotaRepo,
		Generator:              llmMock,
		Validator:              env.validator,
		Renderer:               env.renderer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	const jobCount = 6
	type branchVer struct {
		branchID  string
		versionID string
	}
	items := make([]branchVer, jobCount)
	for i := 0; i < jobCount; i++ {
		b := core.Branch{
			ID:            fmt.Sprintf("branch-gencap-%d", i),
			UserID:        env.defaultUser,
			Name:          fmt.Sprintf("GenCap %d", i),
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateBranch(ctx, &b); err != nil {
			t.Fatalf("CreateBranch %d failed: %v", i, err)
		}
		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, b.ID, fmt.Sprintf("ver-gen-cap-%d", i), core.PendingJobOptions{})
		items[i] = branchVer{branchID: b.ID, versionID: ver.ID}
		pool.Notify()
	}

	for _, it := range items {
		done := waitForVersionStatus(ctx, t, env.branchRepo, it.branchID, it.versionID, 5*time.Second, func(v *core.BranchVersion) bool {
			return v.Status.IsTerminal()
		})

		if done.Status != core.VersionStatusCompleted {
			t.Fatalf("version %s failed: %s", it.versionID, done.ErrorMessage)
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

	llmMock := &callbackLLM{
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
		PollInterval:           20 * time.Millisecond,
		Branches:               env.branchRepo,
		Profiles:               env.profileRepo,
		Presets:                env.presetRepo,
		Templates:              env.templateRepo,
		PDFJobs:                env.pdfRepo,
		Quotas:                 env.quotaRepo,
		ResumeExtractor:        extractor,
		Generator:              llmMock,
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
	type branchVer struct {
		branchID  string
		versionID string
	}
	genItems := make([]branchVer, genCount)
	pdfJobIDs := make([]string, pdfCount)

	for i := 0; i < genCount; i++ {
		b := core.Branch{
			ID:            fmt.Sprintf("branch-mixed-%d", i),
			UserID:        tenantID,
			Name:          fmt.Sprintf("Mixed Branch %d", i),
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateBranch(ctx, &b); err != nil {
			t.Fatalf("CreateBranch %d failed: %v", i, err)
		}
		ver := submitPendingVersionWithQuota(ctx, t, env.store, tenantID, b.ID, fmt.Sprintf("ver-mixed-%d", i), core.PendingJobOptions{})
		genItems[i] = branchVer{branchID: b.ID, versionID: ver.ID}
		pool.Notify()
	}

	for i := 0; i < pdfCount; i++ {
		pj := submitPendingPDFWithQuota(ctx, t, env.store, pool, tenantID, fmt.Sprintf("mixed pdf %d", i), "mock", "m", core.DefaultJobTTL, nil)
		pdfJobIDs[i] = pj.ID
	}

	for _, it := range genItems {
		done := waitForVersionStatus(ctx, t, env.branchRepo, it.branchID, it.versionID, 5*time.Second, func(v *core.BranchVersion) bool {
			return v.Status.IsTerminal()
		})

		if done.Status != core.VersionStatusCompleted {
			t.Fatalf("version %s failed: %s", it.versionID, done.ErrorMessage)
		}
	}

	for _, pID := range pdfJobIDs {
		done := waitForPDFStatus(ctx, t, env.pdfRepo, tenantID, pID, 5*time.Second, func(j *core.PDFParseJob) bool {
			return j.Status.IsTerminal()
		})

		if done.Status != core.PDFParseJobStatusCompleted {
			t.Fatalf("pdf job %s failed: %s", pID, done.ErrorMessage)
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

	llmMock := &callbackLLM{
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
		PollInterval:           20 * time.Millisecond,
		Branches:               env.branchRepo,
		Profiles:               env.profileRepo,
		Presets:                env.presetRepo,
		Templates:              env.templateRepo,
		PDFJobs:                env.pdfRepo,
		Quotas:                 env.quotaRepo,
		ResumeExtractor:        extractor,
		Generator:              llmMock,
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
	branchesA := make([]string, countA)
	versionIDs := make([]string, countA)
	for i := 0; i < countA; i++ {
		b := core.Branch{
			ID:            fmt.Sprintf("branch-slow-gen-%d", i),
			UserID:        tenantA,
			Name:          fmt.Sprintf("Slow Gen %d", i),
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateBranch(ctx, &b); err != nil {
			t.Fatalf("CreateBranch %d failed: %v", i, err)
		}
		ver := submitPendingVersionWithQuota(ctx, t, env.store, tenantA, b.ID, fmt.Sprintf("ver-slow-gen-%d", i), core.PendingJobOptions{})
		branchesA[i] = b.ID
		versionIDs[i] = ver.ID
		pool.Notify()
	}

	time.Sleep(15 * time.Millisecond)

	tenantB := "tenant-b-fast-pdf"
	pj := submitPendingPDFWithQuota(ctx, t, env.store, pool, tenantB, "fast pdf content", "mock", "m", core.DefaultJobTTL, nil)

	doneB := waitForPDFStatus(ctx, t, env.pdfRepo, tenantB, pj.ID, 500*time.Millisecond, func(j *core.PDFParseJob) bool {
		return j.Status == core.PDFParseJobStatusCompleted
	})

	if doneB.Status != core.PDFParseJobStatusCompleted {
		t.Fatalf("expected tenant B fast job completed")
	}

	var remainingA int
	for i, vID := range versionIDs {
		ver, err := env.branchRepo.Version(ctx, branchesA[i], vID)
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

// 19. PDFParseJob Usage Quota Commit Test
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
		PollInterval:    50 * time.Millisecond,
		PDFJobs:         store,
		Quotas:          store,
		ResumeExtractor: mockExt,
		LLMTimeout:      5 * time.Second,
	})
	pool.Start(ctx)
	defer pool.Stop()

	job := submitPendingPDFWithQuota(ctx, t, store, pool, "user-quota-worker", "Candidate resume content", "mock", "mock-model", core.DefaultJobTTL, nil)

	done := waitForPDFStatus(ctx, t, store, "user-quota-worker", job.ID, 2*time.Second, func(j *core.PDFParseJob) bool {
		return j.Status.IsTerminal()
	})

	if done.Status != core.PDFParseJobStatusCompleted {
		t.Fatalf("pdf job failed: %s", done.ErrorMessage)
	}

	quota, err := store.Quota(ctx, "user-quota-worker")
	if err != nil {
		t.Fatalf("failed to get quota: %v", err)
	}
	if quota.MonthlyTokensUsed != 350 {
		t.Errorf("expected monthly_tokens_used 350 (actual usage), got %d", quota.MonthlyTokensUsed)
	}
}

// 20. Reasoning Level Precedence Test
func TestPool_TailoringPipeline_ReasoningLevelPrecedence(t *testing.T) {
	ctx := context.Background()

	t.Run("branch reasoning_level is passed to generation request", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		var capturedReq core.GenerationRequest
		reqReceived := make(chan struct{}, 1)

		llmMock := &callbackLLM{
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
			Concurrency:  1,
			PollInterval: 50 * time.Millisecond,
			Branches:     env.branchRepo,
			Profiles:     env.profileRepo,
			Presets:      env.presetRepo,
			Templates:    env.templateRepo,
			Quotas:       env.quotaRepo,
			Generator:    llmMock,
			Validator:    env.validator,
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

		_ = submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, branch.ID, "ver-reasoning-override", core.PendingJobOptions{})
		pool.Notify()

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

		llmMock := &callbackLLM{
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
			Concurrency:  1,
			PollInterval: 50 * time.Millisecond,
			Branches:     env.branchRepo,
			Profiles:     env.profileRepo,
			Presets:      env.presetRepo,
			Templates:    env.templateRepo,
			Quotas:       env.quotaRepo,
			Generator:    llmMock,
			Validator:    env.validator,
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
			ReasoningLevel:      "",
		}
		if err := env.branchRepo.CreateBranch(ctx, branch); err != nil {
			t.Fatalf("failed to create branch: %v", err)
		}

		_ = submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, branch.ID, "ver-reasoning-fallback", core.PendingJobOptions{})
		pool.Notify()

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

// 21. Direct Store Claiming via Notify() and PollInterval fallback
func TestPool_DirectClaiming_NotifyAndPollFallback(t *testing.T) {
	t.Run("Notify wakes dispatcher immediately without Enqueue even with long PollInterval", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		ext := &mockResumeExtractor{result: &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Notified PDF"}}}

		pool := worker.New(worker.Config{
			Concurrency:     2,
			PollInterval:    10 * time.Second,
			Branches:        env.branchRepo,
			Profiles:        env.profileRepo,
			Presets:         env.presetRepo,
			Templates:       env.templateRepo,
			PDFJobs:         env.pdfRepo,
			Quotas:          env.quotaRepo,
			ResumeExtractor: ext,
			Generator:       env.llm,
			Validator:       env.validator,
			Renderer:        env.renderer,
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.Start(ctx)
		defer pool.Stop()

		// Ensure initial start claim pass settled
		time.Sleep(25 * time.Millisecond)

		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-notify-only", core.PendingJobOptions{})
		pdfJob := submitPendingPDFWithQuota(ctx, t, env.store, nil, "user-notify-pdf", "Notify raw text", "mock", "m", core.DefaultJobTTL, nil)

		// Wake strictly via Notify() (no Enqueue call)
		pool.Notify()

		doneVer := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 500*time.Millisecond, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusCompleted
		})

		if doneVer.Status != core.VersionStatusCompleted {
			t.Fatalf("expected version completed via Notify, got %s", doneVer.Status)
		}
		donePDF := waitForPDFStatus(ctx, t, env.pdfRepo, "user-notify-pdf", pdfJob.ID, 500*time.Millisecond, func(j *core.PDFParseJob) bool {
			return j.Status == core.PDFParseJobStatusCompleted
		})

		if donePDF.Status != core.PDFParseJobStatusCompleted {
			t.Fatalf("expected PDF completed via Notify, got %s", donePDF.Status)
		}
	})

	t.Run("PollInterval discovers persisted jobs without any Notify or Enqueue", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		ext := &mockResumeExtractor{result: &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Polled PDF"}}}

		pool := worker.New(worker.Config{
			Concurrency:     2,
			PollInterval:    20 * time.Millisecond,
			Branches:        env.branchRepo,
			Profiles:        env.profileRepo,
			Presets:         env.presetRepo,
			Templates:       env.templateRepo,
			PDFJobs:         env.pdfRepo,
			Quotas:          env.quotaRepo,
			ResumeExtractor: ext,
			Generator:       env.llm,
			Validator:       env.validator,
			Renderer:        env.renderer,
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.Start(ctx)
		defer pool.Stop()

		time.Sleep(30 * time.Millisecond)

		// Persist directly with nil notifier
		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-poll-only", core.PendingJobOptions{})
		pdfJob := submitPendingPDFWithQuota(ctx, t, env.store, nil, "user-poll-pdf", "Polled raw text", "mock", "m", core.DefaultJobTTL, nil)

		doneVer := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusCompleted
		})

		if doneVer.Status != core.VersionStatusCompleted {
			t.Fatalf("expected polled version completed, got %s", doneVer.Status)
		}
		donePDF := waitForPDFStatus(ctx, t, env.pdfRepo, "user-poll-pdf", pdfJob.ID, 2*time.Second, func(j *core.PDFParseJob) bool {
			return j.Status == core.PDFParseJobStatusCompleted
		})

		if donePDF.Status != core.PDFParseJobStatusCompleted {
			t.Fatalf("expected polled pdf completed, got %s", donePDF.Status)
		}
	})
}

// 22. Alternating Store Preference & Cross-Queue Fairness
func TestPool_MixedWorkload_AlternatingFairness(t *testing.T) {
	env := setupTestWorkerEnv(t)
	ctx := context.Background()

	var (
		orderMu sync.Mutex
		order   []string
	)
	llmMock := &callbackLLM{
		fn: func(_ context.Context, req core.GenerationRequest) (*core.GenerationResponse, error) {
			orderMu.Lock()
			order = append(order, "ver:"+req.BranchContext.UserID)
			orderMu.Unlock()
			return &core.GenerationResponse{TailoredContent: core.TailoredContent{Summary: "OK"}}, nil
		},
	}
	ext := &callbackResumeExtractor{
		fn: func(_ context.Context, text, _, _ string) (*core.JSONResume, error) {
			orderMu.Lock()
			order = append(order, "pdf:"+text)
			orderMu.Unlock()
			return &core.JSONResume{Basics: core.JSONResumeBasics{Name: text}}, nil
		},
	}

	base := time.Date(2026, 11, 15, 13, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		b := core.Branch{
			ID:            fmt.Sprintf("branch-alt-%d", i),
			UserID:        env.defaultUser,
			Name:          fmt.Sprintf("Alt Branch %d", i),
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateBranch(ctx, &b); err != nil {
			t.Fatalf("CreateBranch %d failed: %v", i, err)
		}
		_ = submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, b.ID, fmt.Sprintf("ver-alt-%d", i), core.PendingJobOptions{
			Now: base.Add(time.Duration(i) * time.Second),
		})

		_ = submitPendingPDFWithQuota(ctx, t, env.store, nil, fmt.Sprintf("pdf-tenant-%d", i), fmt.Sprintf("t%d", i), "mock", "m", core.DefaultJobTTL, func() time.Time {
			return base.Add(time.Duration(i) * time.Second)
		})

	}

	pool := worker.New(worker.Config{
		Concurrency:            1,
		MaxConcurrentPerTenant: 1,
		PollInterval:           50 * time.Millisecond,
		Branches:               env.branchRepo,
		Profiles:               env.profileRepo,
		Presets:                env.presetRepo,
		Templates:              env.templateRepo,
		PDFJobs:                env.pdfRepo,
		Quotas:                 env.quotaRepo,
		ResumeExtractor:        ext,
		Generator:              llmMock,
		Validator:              env.validator,
		Renderer:               env.renderer,
	})

	pool.Start(ctx)
	defer pool.Stop()

	deadline := time.Now().Add(3 * time.Second)
	for {
		orderMu.Lock()
		n := len(order)
		orderMu.Unlock()
		if n >= 6 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for alternating dispatch, got %d items", n)
		}
		time.Sleep(5 * time.Millisecond)
	}

	orderMu.Lock()
	defer orderMu.Unlock()
	// With Concurrency=1 and alternating preferPDFNext, first 6 items alternate ver / pdf cleanly!
	for i := 0; i < 6; i += 2 {
		if !strings.HasPrefix(order[i], "ver:") || !strings.HasPrefix(order[i+1], "pdf:") {
			if !strings.HasPrefix(order[i], "pdf:") || !strings.HasPrefix(order[i+1], "ver:") {
				t.Fatalf("expected strict alternating store types without starvation, got %v", order)
			}
		}
	}
	if order[0] == order[1] {
		t.Fatalf("expected alternating store types, got %v", order)
	}
}

// 23. Queue TTL Expiration: Skip on Claim + Periodic Sweep Releases Quota
func TestPool_QueueTTLExpiration_SkipAndSweepReleasesQuota(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC))
	env := setupTestWorkerEnvWithClock(t, clk.Now)
	ctx := context.Background()

	var genCalls atomic.Int32
	var pdfCalls atomic.Int32
	llmMock := &callbackLLM{
		fn: func(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
			genCalls.Add(1)
			return &core.GenerationResponse{TailoredContent: core.TailoredContent{Summary: "Should not run"}}, nil
		},
	}
	ext := &callbackResumeExtractor{
		fn: func(_ context.Context, _, _, _ string) (*core.JSONResume, error) {
			pdfCalls.Add(1)
			return &core.JSONResume{}, nil
		},
	}

	ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-ttl-expire", core.PendingJobOptions{
		TTL: 5 * time.Minute,
		Now: clk.Now(),
	})

	pdfJob := submitPendingPDFWithQuota(ctx, t, env.store, nil, "user-ttl-pdf", "Expired pdf text", "mock", "m", 5*time.Minute, clk.Now)

	// Advance clock past TTL BEFORE starting worker so ClaimNext skips them and sweepLoop expires them
	clk.Advance(6 * time.Minute)

	pool := worker.New(worker.Config{
		Concurrency:     2,
		PollInterval:    20 * time.Millisecond,
		SweepInterval:   25 * time.Millisecond,
		Clock:           clk.Now,
		Branches:        env.branchRepo,
		Profiles:        env.profileRepo,
		Presets:         env.presetRepo,
		Templates:       env.templateRepo,
		PDFJobs:         env.pdfRepo,
		Quotas:          env.quotaRepo,
		ResumeExtractor: ext,
		Generator:       llmMock,
		Validator:       env.validator,
		Renderer:        env.renderer,
	})

	pool.Start(ctx)
	defer pool.Stop()

	failedVer := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 2*time.Second, func(v *core.BranchVersion) bool {
		return v.Status == core.VersionStatusFailed
	})

	if !strings.Contains(failedVer.ErrorMessage, "expired") {
		t.Errorf("expected expiration reason on version, got %q", failedVer.ErrorMessage)
	}
	if st := reservationStatus(ctx, t, env.store, env.defaultUser, ver.ReservationID); st != core.ReservationStatusReleased {
		t.Errorf("expected version quota released on TTL sweep, got %s", st)
	}

	failedPDF := waitForPDFStatus(ctx, t, env.pdfRepo, "user-ttl-pdf", pdfJob.ID, 2*time.Second, func(j *core.PDFParseJob) bool {
		return j.Status == core.PDFParseJobStatusFailed
	})

	if failedPDF.RawText != "" {
		t.Errorf("expected expired PDF RawText pruned, got %q", failedPDF.RawText)
	}
	if st := reservationStatus(ctx, t, env.store, "user-ttl-pdf", pdfJob.ReservationID); st != core.ReservationStatusReleased {
		t.Errorf("expected PDF quota released on TTL sweep, got %s", st)
	}

	if genCalls.Load() != 0 || pdfCalls.Load() != 0 {
		t.Fatalf("expected expired jobs never to invoke LLM/extractor, got gen=%d pdf=%d", genCalls.Load(), pdfCalls.Load())
	}
}

type spyRenewBranchStore struct {
	worker.BranchStore
	renewErr    error
	renewCalls  atomic.Int32
	firstRenew  chan struct{}
	onceFirstRn sync.Once
}

func (s *spyRenewBranchStore) RenewVersionLease(ctx context.Context, versionID, claimedBy string, leaseExpiresAt time.Time) error {
	s.renewCalls.Add(1)
	s.onceFirstRn.Do(func() {
		if s.firstRenew != nil {
			close(s.firstRenew)
		}
	})
	if s.renewErr != nil {
		return s.renewErr
	}
	return s.BranchStore.RenewVersionLease(ctx, versionID, claimedBy, leaseExpiresAt)
}

// 24. Lease Heartbeat Renewal + Stale Lease Sweep + Lease Loss Cancellation
func TestPool_StaleLeaseSweepAndHeartbeatRenewal(t *testing.T) {
	t.Run("heartbeat renews lease so slow LLM call completes without premature lease expiration", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		spyBranches := &spyRenewBranchStore{BranchStore: env.store}

		// LLM sleeps 160ms while LeaseDuration is 70ms and LeaseRenewInterval is 20ms and SweepInterval is 25ms.
		// Without heartbeat renewal, sweepLoop would kill the active lease!
		llmMock := &callbackLLM{
			fn: func(ctx context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
				select {
				case <-time.After(160 * time.Millisecond):
					return &core.GenerationResponse{TailoredContent: core.TailoredContent{Summary: "Slow but healthy"}}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
		}

		ext := &callbackResumeExtractor{
			fn: func(ctx context.Context, _, _, _ string) (*core.JSONResume, error) {
				select {
				case <-time.After(160 * time.Millisecond):
					return &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Slow PDF"}}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
		}

		pool := worker.New(worker.Config{
			Concurrency:        2,
			PollInterval:       20 * time.Millisecond,
			SweepInterval:      25 * time.Millisecond,
			LeaseDuration:      70 * time.Millisecond,
			LeaseRenewInterval: 20 * time.Millisecond,
			Branches:           spyBranches,
			Profiles:           env.profileRepo,
			Presets:            env.presetRepo,
			Templates:          env.templateRepo,
			PDFJobs:            env.pdfRepo,
			Quotas:             env.quotaRepo,
			ResumeExtractor:    ext,
			Generator:          llmMock,
			Validator:          env.validator,
			Renderer:           env.renderer,
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.Start(ctx)
		defer pool.Stop()

		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-heartbeat-1", core.PendingJobOptions{})
		pdfJob := submitPendingPDFWithQuota(ctx, t, env.store, pool, "user-hb-pdf", "Slow PDF content", "mock", "m", core.DefaultJobTTL, nil)
		pool.Notify()

		doneVer := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 3*time.Second, func(v *core.BranchVersion) bool {
			return v.Status.IsTerminal()
		})

		if doneVer.Status != core.VersionStatusCompleted {
			t.Fatalf("expected heartbeat-renewed version completed, got %s (%s)", doneVer.Status, doneVer.ErrorMessage)
		}
		if spyBranches.renewCalls.Load() < 2 {
			t.Errorf("expected at least 2 lease renewals during slow execution, got %d", spyBranches.renewCalls.Load())
		}

		donePDF := waitForPDFStatus(ctx, t, env.pdfRepo, "user-hb-pdf", pdfJob.ID, 3*time.Second, func(j *core.PDFParseJob) bool {
			return j.Status.IsTerminal()
		})

		if donePDF.Status != core.PDFParseJobStatusCompleted {
			t.Fatalf("expected heartbeat-renewed pdf job completed, got %s (%s)", donePDF.Status, donePDF.ErrorMessage)
		}
	})

	t.Run("stale lease sweep fails expired processing leases and releases quota", func(t *testing.T) {
		clk := newFakeClock(time.Date(2026, 11, 15, 15, 0, 0, 0, time.UTC))
		env := setupTestWorkerEnvWithClock(t, clk.Now)
		ctx := context.Background()

		resVer, _ := env.store.ReserveQuota(ctx, env.defaultUser, core.OperationResumeTailoring, 2000, 100, 1000000)
		resPDF, _ := env.store.ReserveQuota(ctx, "user-stale-lease", core.OperationPDFParse, 1000, 100, 1000000)

		staleLease := clk.Now().Add(-1 * time.Minute)
		futureTTL := clk.Now().Add(20 * time.Minute)
		_ = env.branchRepo.CreateVersion(ctx, &core.BranchVersion{
			ID:                "ver-stale-lease",
			UserID:            env.defaultUser,
			BranchID:          env.branchID,
			Status:            core.VersionStatusProcessing,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: core.TemplateStandardCleanID,
			ReservationID:     resVer.ID,
			ClaimedBy:         "dead-worker",
			LeaseExpiresAt:    &staleLease,
			ExpiresAt:         &futureTTL,
		})
		_ = env.pdfRepo.CreateJob(ctx, &core.PDFParseJob{
			ID:             "pdf-stale-lease",
			UserID:         "user-stale-lease",
			Status:         core.PDFParseJobStatusProcessing,
			RawText:        "abandoned text",
			ReservationID:  resPDF.ID,
			ClaimedBy:      "dead-worker",
			LeaseExpiresAt: &staleLease,
			ExpiresAt:      &futureTTL,
		})

		pool := worker.New(worker.Config{
			Concurrency:   1,
			PollInterval:  1 * time.Second,
			SweepInterval: 20 * time.Millisecond,
			Clock:         clk.Now,
			Branches:      env.branchRepo,
			Profiles:      env.profileRepo,
			Presets:       env.presetRepo,
			Templates:     env.templateRepo,
			PDFJobs:       env.pdfRepo,
			Quotas:        env.quotaRepo,
		})
		pool.Start(ctx)
		defer pool.Stop()

		failedVer := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, "ver-stale-lease", 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusFailed
		})

		if !strings.Contains(failedVer.ErrorMessage, "lease") {
			t.Errorf("expected lease expiration message, got %q", failedVer.ErrorMessage)
		}
		if st := reservationStatus(ctx, t, env.store, env.defaultUser, resVer.ID); st != core.ReservationStatusReleased {
			t.Errorf("expected version quota released on lease sweep, got %s", st)
		}

		failedPDF := waitForPDFStatus(ctx, t, env.pdfRepo, "user-stale-lease", "pdf-stale-lease", 2*time.Second, func(j *core.PDFParseJob) bool {
			return j.Status == core.PDFParseJobStatusFailed
		})

		if failedPDF.RawText != "" {
			t.Errorf("expected abandoned PDF RawText pruned on lease sweep, got %q", failedPDF.RawText)
		}
		if st := reservationStatus(ctx, t, env.store, "user-stale-lease", resPDF.ID); st != core.ReservationStatusReleased {
			t.Errorf("expected pdf quota released on lease sweep, got %s", st)
		}
	})

	t.Run("heartbeat losing lease via ErrConflict cancels in-flight attempt context", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		spyBranches := &spyRenewBranchStore{
			BranchStore: env.store,
			renewErr:    core.ErrConflict,
			firstRenew:  make(chan struct{}),
		}

		attemptCancelled := make(chan struct{})
		llmMock := &callbackLLM{
			fn: func(ctx context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
				<-ctx.Done()
				close(attemptCancelled)
				return nil, ctx.Err()
			},
		}

		pool := worker.New(worker.Config{
			Concurrency:        1,
			PollInterval:       50 * time.Millisecond,
			LeaseDuration:      60 * time.Millisecond,
			LeaseRenewInterval: 15 * time.Millisecond,
			Branches:           spyBranches,
			Profiles:           env.profileRepo,
			Presets:            env.presetRepo,
			Templates:          env.templateRepo,
			Quotas:             env.quotaRepo,
			Generator:          llmMock,
			Validator:          env.validator,
			Renderer:           env.renderer,
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.Start(ctx)
		defer pool.Stop()

		_ = submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-lease-lost", core.PendingJobOptions{})
		pool.Notify()

		select {
		case <-attemptCancelled:
		case <-time.After(2 * time.Second):
			t.Fatal("expected heartbeat ErrConflict to cancel in-flight LLM context")
		}
	})
}

// 25. Upstream Error Classification: Fail-Fast + Bounded Retries + TTL Cutoff + Non-Blocking Slot + RawText Preservation
func TestPool_UpstreamErrorClassification_FailFastAndBoundedRetries(t *testing.T) {
	t.Run("permanent and upstream quota errors fail fast in 1 attempt and refund quota", func(t *testing.T) {
		cases := []struct {
			name string
			err  error
		}{
			{name: "llm.ErrBadRequest", err: llm.ErrBadRequest},
			{name: "llm.ErrUnauthorized", err: llm.ErrUnauthorized},
			{name: "llm.ErrUpstreamQuota", err: llm.ErrUpstreamQuota},
			{name: "core.ErrUpstreamQuota", err: core.ErrUpstreamQuota},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				env := setupTestWorkerEnv(t)
				var attempts atomic.Int32
				llmMock := &callbackLLM{
					fn: func(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
						attempts.Add(1)
						return nil, tc.err
					},
				}

				var pdfAttempts atomic.Int32
				ext := &callbackResumeExtractor{
					fn: func(_ context.Context, _, _, _ string) (*core.JSONResume, error) {
						pdfAttempts.Add(1)
						return nil, tc.err
					},
				}

				pool := worker.New(worker.Config{
					Concurrency:     2,
					MaxAttempts:     3,
					RetryBaseDelay:  10 * time.Millisecond,
					PollInterval:    50 * time.Millisecond,
					Branches:        env.branchRepo,
					Profiles:        env.profileRepo,
					Presets:         env.presetRepo,
					Templates:       env.templateRepo,
					PDFJobs:         env.pdfRepo,
					Quotas:          env.quotaRepo,
					ResumeExtractor: ext,
					Generator:       llmMock,
					Validator:       env.validator,
					Renderer:        env.renderer,
				})

				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				pool.Start(ctx)
				defer pool.Stop()

				ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-ff-"+tc.name, core.PendingJobOptions{})
				pdfJob := submitPendingPDFWithQuota(ctx, t, env.store, pool, "user-ff-pdf", "Raw PDF", "mock", "m", core.DefaultJobTTL, nil)
				pool.Notify()

				failedVer := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 2*time.Second, func(v *core.BranchVersion) bool {
					return v.Status == core.VersionStatusFailed
				})

				if attempts.Load() != 1 || failedVer.AttemptCount != 1 {
					t.Fatalf("expected 1 fail-fast version attempt, got calls=%d attemptCount=%d", attempts.Load(), failedVer.AttemptCount)
				}
				if st := reservationStatus(ctx, t, env.store, env.defaultUser, ver.ReservationID); st != core.ReservationStatusReleased {
					t.Errorf("expected version quota released, got %s", st)
				}

				failedPDF := waitForPDFStatus(ctx, t, env.pdfRepo, "user-ff-pdf", pdfJob.ID, 2*time.Second, func(j *core.PDFParseJob) bool {
					return j.Status == core.PDFParseJobStatusFailed
				})

				if pdfAttempts.Load() != 1 || failedPDF.AttemptCount != 1 {
					t.Fatalf("expected 1 fail-fast PDF attempt, got calls=%d attemptCount=%d", pdfAttempts.Load(), failedPDF.AttemptCount)
				}
				if failedPDF.RawText != "" {
					t.Errorf("expected PDF RawText pruned on fail-fast, got %q", failedPDF.RawText)
				}
				if st := reservationStatus(ctx, t, env.store, "user-ff-pdf", pdfJob.ReservationID); st != core.ReservationStatusReleased {
					t.Errorf("expected PDF quota released, got %s", st)
				}
			})
		}
	})

	t.Run("transient errors retry bounded MaxAttempts without releasing quota or blocking worker slot", func(t *testing.T) {
		env := setupTestWorkerEnv(t)

		var tenantAAttempts atomic.Int32
		tenantBCompleted := make(chan struct{})
		llmMock := &callbackLLM{
			fn: func(_ context.Context, req core.GenerationRequest) (*core.GenerationResponse, error) {
				if req.BranchContext.UserID == env.defaultUser {
					n := tenantAAttempts.Add(1)
					if n < 3 {
						return nil, llm.ErrRateLimited
					}
					return &core.GenerationResponse{TailoredContent: core.TailoredContent{Summary: "Recovered on 3rd attempt"}}, nil
				}
				select {
				case <-tenantBCompleted:
				default:
					close(tenantBCompleted)
				}
				return &core.GenerationResponse{TailoredContent: core.TailoredContent{Summary: "Tenant B ran during A backoff"}}, nil
			},
		}

		// Single worker concurrency=1! Tenant B MUST run while Tenant A is in 80ms retry backoff!
		pool := worker.New(worker.Config{
			Concurrency:    1,
			MaxAttempts:    3,
			RetryBaseDelay: 80 * time.Millisecond,
			MaxRetryDelay:  200 * time.Millisecond,
			PollInterval:   1 * time.Second, // proves retry wakeup timer works without PollInterval
			Branches:       env.branchRepo,
			Profiles:       env.profileRepo,
			Presets:        env.presetRepo,
			Templates:      env.templateRepo,
			Quotas:         env.quotaRepo,
			Generator:      llmMock,
			Validator:      env.validator,
			Renderer:       env.renderer,
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.Start(ctx)
		defer pool.Stop()

		// Seed tenant B profile + branch too
		if err := seed.DefaultData(ctx, env.store, "tenant-b-retry"); err != nil {
			t.Fatalf("seed tenant B failed: %v", err)
		}
		branchB := core.Branch{
			ID:            "branch-tenant-b-retry",
			UserID:        "tenant-b-retry",
			Name:          "Tenant B",
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateBranch(ctx, &branchB); err != nil {
			t.Fatalf("CreateBranch B failed: %v", err)
		}

		verA := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-transient-a", core.PendingJobOptions{})
		pool.Notify()

		// Wait until attempt 1 has failed and entered backoff
		deadline := time.Now().Add(1 * time.Second)
		for tenantAAttempts.Load() < 1 {
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for attempt 1")
			}
			time.Sleep(2 * time.Millisecond)
		}

		// Submit tenant B while tenant A is in 80ms backoff: worker slot is free so B completes immediately
		verB := submitPendingVersionWithQuota(ctx, t, env.store, "tenant-b-retry", branchB.ID, "ver-b-free-slot", core.PendingJobOptions{})
		pool.Notify()

		select {
		case <-tenantBCompleted:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("tenant B did not execute while tenant A was in retry backoff; worker slot was blocked!")
		}

		doneB := waitForVersionStatus(ctx, t, env.branchRepo, branchB.ID, verB.ID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusCompleted
		})

		if doneB.Status != core.VersionStatusCompleted {
			t.Fatalf("expected B completed, got %s", doneB.Status)
		}

		// Tenant A must recover on attempt 3 and commit its original reservation
		doneA := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, verA.ID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusCompleted
		})

		if doneA.AttemptCount != 3 || tenantAAttempts.Load() != 3 {
			t.Fatalf("expected 3 attempts, got AttemptCount=%d calls=%d", doneA.AttemptCount, tenantAAttempts.Load())
		}
		if st := reservationStatus(ctx, t, env.store, env.defaultUser, verA.ReservationID); st != core.ReservationStatusCommitted {
			t.Errorf("expected reservation committed after transient retry success, got %s", st)
		}
	})

	t.Run("transient retry exhaustion fails and refunds quota", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		var attempts atomic.Int32
		llmMock := &callbackLLM{
			fn: func(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
				attempts.Add(1)
				return nil, llm.ErrUpstream
			},
		}

		pool := worker.New(worker.Config{
			Concurrency:    1,
			MaxAttempts:    3,
			RetryBaseDelay: 10 * time.Millisecond,
			MaxRetryDelay:  20 * time.Millisecond,
			PollInterval:   50 * time.Millisecond,
			Branches:       env.branchRepo,
			Profiles:       env.profileRepo,
			Presets:        env.presetRepo,
			Templates:      env.templateRepo,
			Quotas:         env.quotaRepo,
			Generator:      llmMock,
			Validator:      env.validator,
			Renderer:       env.renderer,
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.Start(ctx)
		defer pool.Stop()

		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-exhaust", core.PendingJobOptions{})
		pool.Notify()

		failed := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusFailed
		})

		if failed.AttemptCount != 3 || attempts.Load() != 3 {
			t.Fatalf("expected 3 exhausted attempts, got AttemptCount=%d calls=%d", failed.AttemptCount, attempts.Load())
		}
		if st := reservationStatus(ctx, t, env.store, env.defaultUser, ver.ReservationID); st != core.ReservationStatusReleased {
			t.Errorf("expected quota released after retry exhaustion, got %s", st)
		}
	})

	t.Run("transient failure past ExpiresAt fails immediately without requeuing past TTL", func(t *testing.T) {
		clk := newFakeClock(time.Date(2026, 11, 15, 16, 0, 0, 0, time.UTC))
		env := setupTestWorkerEnvWithClock(t, clk.Now)

		var attempts atomic.Int32
		llmMock := &callbackLLM{
			fn: func(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
				attempts.Add(1)
				return nil, core.ErrTransientFailure
			},
		}

		pool := worker.New(worker.Config{
			Concurrency:    1,
			MaxAttempts:    5,
			RetryBaseDelay: 10 * time.Minute, // exceeds remaining 2m TTL!
			MaxRetryDelay:  30 * time.Minute,
			PollInterval:   50 * time.Millisecond,
			Clock:          clk.Now,
			Branches:       env.branchRepo,
			Profiles:       env.profileRepo,
			Presets:        env.presetRepo,
			Templates:      env.templateRepo,
			Quotas:         env.quotaRepo,
			Generator:      llmMock,
			Validator:      env.validator,
			Renderer:       env.renderer,
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.Start(ctx)
		defer pool.Stop()

		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-ttl-cutoff", core.PendingJobOptions{
			TTL: 2 * time.Minute,
			Now: clk.Now(),
		})

		pool.Notify()

		failed := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusFailed
		})

		if attempts.Load() != 1 || failed.AttemptCount != 1 {
			t.Fatalf("expected immediate failure when retry exceeds TTL, got calls=%d attemptCount=%d", attempts.Load(), failed.AttemptCount)
		}
		if st := reservationStatus(ctx, t, env.store, env.defaultUser, ver.ReservationID); st != core.ReservationStatusReleased {
			t.Errorf("expected quota released when retry bounded by TTL, got %s", st)
		}
	})

	t.Run("PDF transient retry preserves RawText across attempts and prunes on completion", func(t *testing.T) {
		store := memory.New()
		var (
			mu    sync.Mutex
			texts []string
		)
		ext := &callbackResumeExtractor{
			fn: func(_ context.Context, text, _, _ string) (*core.JSONResume, error) {
				mu.Lock()
				texts = append(texts, text)
				attempt := len(texts)
				mu.Unlock()
				if attempt == 1 {
					return nil, llm.ErrRateLimited
				}
				return &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Retry Success"}}, nil
			},
		}

		pool := worker.New(worker.Config{
			Concurrency:     1,
			MaxAttempts:     3,
			RetryBaseDelay:  15 * time.Millisecond,
			MaxRetryDelay:   30 * time.Millisecond,
			PollInterval:    50 * time.Millisecond,
			PDFJobs:         store,
			Quotas:          store,
			ResumeExtractor: ext,
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.Start(ctx)
		defer pool.Stop()

		const raw = "Persisted PDF raw text across retry"
		job := submitPendingPDFWithQuota(ctx, t, store, pool, "user-pdf-retry", raw, "mock", "m", core.DefaultJobTTL, nil)

		done := waitForPDFStatus(ctx, t, store, "user-pdf-retry", job.ID, 2*time.Second, func(j *core.PDFParseJob) bool {
			return j.Status == core.PDFParseJobStatusCompleted
		})

		if done.AttemptCount != 2 {
			t.Errorf("expected AttemptCount == 2, got %d", done.AttemptCount)
		}
		if done.RawText != "" {
			t.Errorf("expected RawText pruned after completion, got %q", done.RawText)
		}

		mu.Lock()
		defer mu.Unlock()
		if len(texts) != 2 || texts[0] != raw || texts[1] != raw {
			t.Fatalf("expected RawText delivered on both attempts, got %v", texts)
		}
	})
}

// 26. Cancellation & Manual Edit / Delete Supersedence
func TestPool_CancellationAndSupersedence(t *testing.T) {
	t.Run("cancelled version and PDF job never execute and release quota", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		ctx := context.Background()

		var genCalls atomic.Int32
		var pdfCalls atomic.Int32
		llmMock := &callbackLLM{fn: func(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
			genCalls.Add(1)
			return &core.GenerationResponse{}, nil
		}}
		ext := &callbackResumeExtractor{fn: func(_ context.Context, _, _, _ string) (*core.JSONResume, error) {
			pdfCalls.Add(1)
			return &core.JSONResume{}, nil
		}}

		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-cancel-1", core.PendingJobOptions{})
		cancelledVer, err := env.store.CancelVersion(ctx, env.defaultUser, env.branchID, ver.ID, "cancelled by user")
		if err != nil {
			t.Fatalf("CancelVersion failed: %v", err)
		}
		_ = env.store.ReleaseQuota(ctx, cancelledVer.ReservationID, "cancelled by user")

		pdfJob := submitPendingPDFWithQuota(ctx, t, env.store, nil, "user-cancel-pdf", "Sensitive raw resume text", "mock", "m", core.DefaultJobTTL, nil)
		cancelledPDF, err := worker.CancelPDFJob(ctx, env.store, env.store, "user-cancel-pdf", pdfJob.ID, 2*time.Second)
		if err != nil {
			t.Fatalf("CancelPDFJob failed: %v", err)
		}
		if cancelledPDF.Status != core.PDFParseJobStatusCancelled || cancelledPDF.RawText != "" {
			t.Fatalf("expected cancelled PDF with pruned RawText, got status=%s raw=%q", cancelledPDF.Status, cancelledPDF.RawText)
		}

		pool := worker.New(worker.Config{
			Concurrency:     2,
			PollInterval:    20 * time.Millisecond,
			Branches:        env.branchRepo,
			Profiles:        env.profileRepo,
			Presets:         env.presetRepo,
			Templates:       env.templateRepo,
			PDFJobs:         env.pdfRepo,
			Quotas:          env.quotaRepo,
			ResumeExtractor: ext,
			Generator:       llmMock,
			Validator:       env.validator,
			Renderer:        env.renderer,
		})
		pool.Start(ctx)
		time.Sleep(60 * time.Millisecond)
		pool.Stop()

		if genCalls.Load() != 0 || pdfCalls.Load() != 0 {
			t.Fatalf("cancelled jobs should never execute, got gen=%d pdf=%d", genCalls.Load(), pdfCalls.Load())
		}
		if st := reservationStatus(ctx, t, env.store, env.defaultUser, ver.ReservationID); st != core.ReservationStatusReleased {
			t.Errorf("expected version quota released, got %s", st)
		}
		if st := reservationStatus(ctx, t, env.store, "user-cancel-pdf", pdfJob.ReservationID); st != core.ReservationStatusReleased {
			t.Errorf("expected pdf quota released, got %s", st)
		}
	})

	t.Run("manual edit supersedence prevents stale CompleteVersion overwrite", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		ctx := context.Background()

		inLLM := make(chan struct{})
		unblock := make(chan struct{})
		llmMock := &callbackLLM{
			fn: func(ctx context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
				close(inLLM)
				select {
				case <-unblock:
					return &core.GenerationResponse{TailoredContent: core.TailoredContent{Summary: "Stale LLM output"}}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
		}

		pool := worker.New(worker.Config{
			Concurrency:  1,
			PollInterval: 20 * time.Millisecond,
			Branches:     env.branchRepo,
			Profiles:     env.profileRepo,
			Presets:      env.presetRepo,
			Templates:    env.templateRepo,
			Quotas:       env.quotaRepo,
			Generator:    llmMock,
			Validator:    env.validator,
			Renderer:     env.renderer,
		})
		pool.Start(ctx)
		defer pool.Stop()

		v1 := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-stale-1", core.PendingJobOptions{})
		pool.Notify()

		select {
		case <-inLLM:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for v1 to enter LLM")
		}

		// While v1 is running in LLM, user saves a manual edit v2 as active!
		v2 := &core.BranchVersion{
			ID:                "ver-manual-2",
			UserID:            env.defaultUser,
			BranchID:          env.branchID,
			Status:            core.VersionStatusCompleted,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: core.TemplateStandardCleanID,
			SourceType:        core.SourceTypeManualEdit,
			TailoredContent:   &core.TailoredContent{Summary: "Manual edit wins"},
		}
		if err := env.branchRepo.CreateVersion(ctx, v2); err != nil {
			t.Fatalf("CreateVersion v2 failed: %v", err)
		}
		if err := env.branchRepo.CompleteVersion(ctx, v2); err != nil {
			t.Fatalf("CompleteVersion v2 failed: %v", err)
		}

		close(unblock)

		// v1 must fail cleanly without touching branch ActiveVersionID
		doneV1 := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, v1.ID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status.IsTerminal()
		})

		if doneV1.Status != core.VersionStatusFailed {
			t.Fatalf("expected stale v1 to fail cleanly, got %s", doneV1.Status)
		}

		br, err := env.branchRepo.Branch(ctx, env.defaultUser, env.branchID)
		if err != nil {
			t.Fatalf("Branch failed: %v", err)
		}
		if br.ActiveVersionID == nil || *br.ActiveVersionID != v2.ID {
			t.Fatalf("expected branch ActiveVersionID to remain %q, got %v", v2.ID, br.ActiveVersionID)
		}
	})

	t.Run("pre-LLM branch deletion check aborts before calling Generator", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		ctx := context.Background()

		// Wrap store to delete branch right when Profile is loaded (just before Generator call)
		var genCalls atomic.Int32
		llmMock := &callbackLLM{
			fn: func(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
				genCalls.Add(1)
				return &core.GenerationResponse{TailoredContent: core.TailoredContent{Summary: "Should not run"}}, nil
			},
		}

		deletingProfiles := &hookProfileReader{
			inner: env.store,
			onProfile: func() {
				released, _ := env.store.AbortBranchVersions(ctx, env.branchID, "branch deleted")
				for _, r := range released {
					_ = env.store.ReleaseQuota(ctx, r.ReservationID, r.Reason)
				}
				_ = env.store.DeleteBranch(ctx, env.defaultUser, env.branchID)
			},
		}

		pool := worker.New(worker.Config{
			Concurrency:  1,
			PollInterval: 50 * time.Millisecond,
			Branches:     env.branchRepo,
			Profiles:     deletingProfiles,
			Presets:      env.presetRepo,
			Templates:    env.templateRepo,
			Quotas:       env.quotaRepo,
			Generator:    llmMock,
			Validator:    env.validator,
			Renderer:     env.renderer,
		})
		pool.Start(ctx)
		defer pool.Stop()

		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-deleted-prellm", core.PendingJobOptions{})
		pool.Notify()

		// Wait for branch deletion hook to finish and worker slot to return
		deadline := time.Now().Add(2 * time.Second)
		for {
			_, err := env.branchRepo.Branch(ctx, env.defaultUser, env.branchID)
			if errors.Is(err, core.ErrNotFound) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for branch deletion hook")
			}
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(40 * time.Millisecond)

		if genCalls.Load() != 0 {
			t.Fatalf("expected Generator NOT to be called after branch deletion, got %d calls", genCalls.Load())
		}
		if st := reservationStatus(ctx, t, env.store, env.defaultUser, ver.ReservationID); st != core.ReservationStatusReleased {
			t.Errorf("expected reservation released on branch deletion, got %s", st)
		}
	})
}

type hookProfileReader struct {
	inner     worker.ProfileReader
	onProfile func()
	once      sync.Once
}

func (h *hookProfileReader) Profile(ctx context.Context, userID string) (*core.Profile, error) {
	p, err := h.inner.Profile(ctx, userID)
	if h.onProfile != nil {
		h.once.Do(h.onProfile)
	}
	return p, err
}

func (h *hookProfileReader) Snapshot(ctx context.Context, userID string) (*core.MainProfileSnapshot, error) {
	return h.inner.Snapshot(ctx, userID)
}

type failingPDFSubmitStore struct {
	worker.PDFJobStore
	submitErr error
}

func (f *failingPDFSubmitStore) SubmitPendingJob(_ context.Context, _ *core.PDFParseJob, _ core.PendingJobOptions) error {
	return f.submitErr
}

// 27. PDF Submission & Cancellation Helpers + Empty RawText Validation
func TestPool_PDFSubmitAndCancelHelpers(t *testing.T) {
	t.Run("SubmitPDFJob validates inputs and notifies worker", func(t *testing.T) {
		store := memory.New()
		ctx := context.Background()

		if _, err := worker.SubmitPDFJob(ctx, worker.PDFSubmitConfig{PDFJobs: store}, worker.PDFJobSubmission{
			UserID:  "",
			RawText: "hello",
		}); !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation for empty UserID, got %v", err)
		}

		if _, err := worker.SubmitPDFJob(ctx, worker.PDFSubmitConfig{PDFJobs: store}, worker.PDFJobSubmission{
			UserID:  "user-1",
			RawText: "   ",
		}); !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation for blank RawText, got %v", err)
		}

		var notified atomic.Int32
		job, err := worker.SubmitPDFJob(ctx, worker.PDFSubmitConfig{
			PDFJobs:  store,
			Quotas:   store,
			Notifier: core.JobNotifierFunc(func() { notified.Add(1) }),
		}, worker.PDFJobSubmission{
			UserID:       "user-1",
			Filename:     "cv.pdf",
			Provider:     "openai",
			Model:        "gpt-4o",
			RawText:      "Resume raw text",
			DailyLimit:   10,
			MonthlyLimit: 50000,
		})
		if err != nil {
			t.Fatalf("SubmitPDFJob failed: %v", err)
		}
		if notified.Load() != 1 {
			t.Errorf("expected Notifier called once, got %d", notified.Load())
		}
		if job.ReservationID == "" || job.ExpiresAt == nil {
			t.Errorf("expected ReservationID and ExpiresAt populated, got %+v", job)
		}
	})

	t.Run("SubmitPDFJob releases quota via detached cleanupCtx when caller context is cancelled", func(t *testing.T) {
		store := memory.New()
		callCtx, cancel := context.WithCancel(context.Background())
		cancel()

		// Reserve succeeds? Wait: store.ReserveQuota ignores ctx cancellation, while failingPDFSubmitStore returns ErrPendingLimitExceeded.
		failStore := &failingPDFSubmitStore{
			PDFJobStore: store,
			submitErr:   core.ErrPendingLimitExceeded,
		}

		_, err := worker.SubmitPDFJob(callCtx, worker.PDFSubmitConfig{
			PDFJobs:        failStore,
			Quotas:         store,
			CleanupTimeout: 2 * time.Second,
		}, worker.PDFJobSubmission{
			UserID:       "user-detached",
			RawText:      "Some resume text",
			DailyLimit:   10,
			MonthlyLimit: 50000,
		})
		if !errors.Is(err, core.ErrPendingLimitExceeded) {
			t.Fatalf("expected ErrPendingLimitExceeded, got %v", err)
		}

		quota, err := store.Quota(context.Background(), "user-detached")
		if err != nil {
			t.Fatalf("Quota failed: %v", err)
		}
		if quota.DailyRequestsUsed != 0 || quota.MonthlyTokensUsed != 0 {
			t.Errorf("expected quota fully released despite cancelled ctx, got daily=%d monthly=%d", quota.DailyRequestsUsed, quota.MonthlyTokensUsed)
		}
	})

	t.Run("persisted PDF job with empty RawText fails fast and refunds quota", func(t *testing.T) {
		store := memory.New()
		ctx := context.Background()

		rec, err := store.ReserveQuota(ctx, "user-empty-raw", core.OperationPDFParse, 1000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}
		exp := time.Now().UTC().Add(core.DefaultJobTTL)
		_ = store.CreateJob(ctx, &core.PDFParseJob{
			ID:            "pdf-empty-raw",
			UserID:        "user-empty-raw",
			Status:        core.PDFParseJobStatusPending,
			RawText:       "   ",
			ReservationID: rec.ID,
			ExpiresAt:     &exp,
		})

		pool := worker.New(worker.Config{
			Concurrency:     1,
			PollInterval:    20 * time.Millisecond,
			PDFJobs:         store,
			Quotas:          store,
			ResumeExtractor: &mockResumeExtractor{},
		})
		pool.Start(ctx)
		defer pool.Stop()

		failed := waitForPDFStatus(ctx, t, store, "user-empty-raw", "pdf-empty-raw", 2*time.Second, func(j *core.PDFParseJob) bool {
			return j.Status == core.PDFParseJobStatusFailed
		})

		if failed.ErrorMessage == "" {
			t.Errorf("expected non-empty validation error for empty RawText")
		}
		if st := reservationStatus(ctx, t, store, "user-empty-raw", rec.ID); st != core.ReservationStatusReleased {
			t.Errorf("expected quota released for empty RawText job, got %s", st)
		}
	})
}

// 28. StopWithContext Timeout Cancels In-Flight Jobs and Refunds Quota via Detached Context
func TestPool_StopWithContext_TimeoutCancelsInFlightAndRefundsQuota(t *testing.T) {
	env := setupTestWorkerEnv(t)
	ctx := context.Background()

	b2 := core.Branch{
		ID:            "branch-timeout-pending",
		UserID:        env.defaultUser,
		Name:          "Pending Branch",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateBranch(ctx, &b2); err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}

	inLLM := make(chan string, 1)
	unblock := make(chan struct{})
	llmMock := &blockingMockLLM{unblock: unblock, started: inLLM}

	pool := worker.New(worker.Config{
		Concurrency:    1,
		PollInterval:   50 * time.Millisecond,
		CleanupTimeout: 2 * time.Second,
		Branches:       env.branchRepo,
		Profiles:       env.profileRepo,
		Presets:        env.presetRepo,
		Templates:      env.templateRepo,
		Quotas:         env.quotaRepo,
		Generator:      llmMock,
		Validator:      env.validator,
		Renderer:       env.renderer,
	})
	pool.Start(ctx)

	vActive := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-timeout-active", core.PendingJobOptions{})
	pool.Notify()

	select {
	case <-inLLM:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for active version to enter LLM")
	}

	vPending := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, b2.ID, "ver-timeout-unclaimed", core.PendingJobOptions{})

	stopCtx, stopCancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stopCancel()
	stopErr := pool.StopWithContext(stopCtx)
	if !errors.Is(stopErr, context.DeadlineExceeded) {
		t.Fatalf("expected StopWithContext to return DeadlineExceeded, got %v", stopErr)
	}

	// Active job must be failed and its quota refunded via detached cleanupCtx
	reloadedActive, err := env.branchRepo.Version(ctx, env.branchID, vActive.ID)
	if err != nil {
		t.Fatalf("Version active failed: %v", err)
	}
	if reloadedActive.Status != core.VersionStatusFailed {
		t.Errorf("expected timed-out active job failed, got %s", reloadedActive.Status)
	}
	if st := reservationStatus(ctx, t, env.store, env.defaultUser, vActive.ReservationID); st != core.ReservationStatusReleased {
		t.Errorf("expected timed-out active job quota released, got %s", st)
	}

	// Unclaimed pending job remains safely persisted in store
	reloadedPending, err := env.branchRepo.Version(ctx, b2.ID, vPending.ID)
	if err != nil {
		t.Fatalf("Version pending failed: %v", err)
	}
	if reloadedPending.Status != core.VersionStatusPending {
		t.Errorf("expected unclaimed job to remain pending, got %s", reloadedPending.Status)
	}
	if st := reservationStatus(ctx, t, env.store, env.defaultUser, vPending.ReservationID); st != core.ReservationStatusPending {
		t.Errorf("expected unclaimed pending reservation intact, got %s", st)
	}
}

// 29. Concurrency Race Test: At-Most-Once Execution & Branch Exclusivity
func TestPool_ConcurrentRace_AtMostOnceAndBranchExclusivity(t *testing.T) {
	env := setupTestWorkerEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		mu             sync.Mutex
		branchInFlight = make(map[string]int)
		versionExecs   = make(map[string]int)
		pdfExecs       = make(map[string]int)
		branchOverlap  bool
		duplicateExec  bool
	)

	llmMock := &callbackLLM{
		fn: func(_ context.Context, req core.GenerationRequest) (*core.GenerationResponse, error) {
			bID := req.BranchContext.ID
			mu.Lock()
			branchInFlight[bID]++
			if branchInFlight[bID] > 1 {
				branchOverlap = true
			}
			mu.Unlock()

			time.Sleep(10 * time.Millisecond)

			mu.Lock()
			branchInFlight[bID]--
			mu.Unlock()

			return &core.GenerationResponse{TailoredContent: core.TailoredContent{Summary: "Concurrent OK"}}, nil
		},
	}

	ext := &callbackResumeExtractor{
		fn: func(_ context.Context, text, _, _ string) (*core.JSONResume, error) {
			mu.Lock()
			pdfExecs[text]++
			if pdfExecs[text] > 1 {
				duplicateExec = true
			}
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			return &core.JSONResume{Basics: core.JSONResumeBasics{Name: text}}, nil
		},
	}

	pool := worker.New(worker.Config{
		Concurrency:            8,
		MaxConcurrentPerTenant: 4,
		PollInterval:           15 * time.Millisecond,
		SweepInterval:          25 * time.Millisecond,
		Branches:               env.branchRepo,
		Profiles:               env.profileRepo,
		Presets:                env.presetRepo,
		Templates:              env.templateRepo,
		PDFJobs:                env.pdfRepo,
		Quotas:                 env.quotaRepo,
		ResumeExtractor:        ext,
		Generator:              llmMock,
		Validator:              env.validator,
		Renderer:               env.renderer,
	})
	pool.Start(ctx)
	defer pool.Stop()

	// Create 4 branches with 3 queued versions each (created directly so they all remain pending)
	// plus 8 PDF jobs across 4 tenants, while hammering Notify() concurrently.
	const numBranches = 4
	const versionsPerBranch = 3
	type branchVer struct {
		branchID  string
		versionID string
	}
	var allVersions []branchVer
	for bIdx := 0; bIdx < numBranches; bIdx++ {
		b := core.Branch{
			ID:            fmt.Sprintf("branch-race-%d", bIdx),
			UserID:        env.defaultUser,
			Name:          fmt.Sprintf("Race Branch %d", bIdx),
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := env.branchRepo.CreateBranch(ctx, &b); err != nil {
			t.Fatalf("CreateBranch %d failed: %v", bIdx, err)
		}
		for vIdx := 0; vIdx < versionsPerBranch; vIdx++ {
			rec, err := env.store.ReserveQuota(ctx, env.defaultUser, core.OperationResumeTailoring, 2000, 100, 1000000)
			if err != nil {
				t.Fatalf("ReserveQuota failed: %v", err)
			}
			exp := time.Now().UTC().Add(core.DefaultJobTTL)
			vID := fmt.Sprintf("ver-race-%d-%d", bIdx, vIdx)
			ver := &core.BranchVersion{
				ID:                vID,
				UserID:            env.defaultUser,
				BranchID:          b.ID,
				Status:            core.VersionStatusPending,
				MainRevisionUsed:  1,
				TemplateSetIDUsed: core.TemplateStandardCleanID,
				ReservationID:     rec.ID,
				ExpiresAt:         &exp,
			}
			if err := env.branchRepo.CreateVersion(ctx, ver); err != nil {
				t.Fatalf("CreateVersion %s failed: %v", vID, err)
			}
			allVersions = append(allVersions, branchVer{branchID: b.ID, versionID: vID})
			pool.Notify()
		}
	}

	type pdfRef struct {
		userID string
		jobID  string
	}
	var allPDFs []pdfRef
	for i := 0; i < 8; i++ {
		uID := fmt.Sprintf("race-user-%d", i%4)
		pj := submitPendingPDFWithQuota(ctx, t, env.store, pool, uID, fmt.Sprintf("unique-pdf-%d", i), "mock", "m", core.DefaultJobTTL, nil)
		allPDFs = append(allPDFs, pdfRef{userID: uID, jobID: pj.ID})
	}

	// Hammer Notify concurrently while jobs run
	var notifyWG sync.WaitGroup
	for g := 0; g < 4; g++ {
		notifyWG.Add(1)
		go func() {
			defer notifyWG.Done()
			for i := 0; i < 10; i++ {
				pool.Notify()
				time.Sleep(2 * time.Millisecond)
			}
		}()
	}

	for _, bv := range allVersions {
		done := waitForVersionStatus(ctx, t, env.branchRepo, bv.branchID, bv.versionID, 5*time.Second, func(v *core.BranchVersion) bool {
			return v.Status.IsTerminal()
		})

		if done.Status != core.VersionStatusCompleted {
			t.Fatalf("version %s terminated in %s: %s", bv.versionID, done.Status, done.ErrorMessage)
		}
		mu.Lock()
		versionExecs[bv.versionID] = done.AttemptCount
		if done.AttemptCount != 1 {
			duplicateExec = true
		}
		mu.Unlock()
	}

	for _, pr := range allPDFs {
		done := waitForPDFStatus(ctx, t, env.pdfRepo, pr.userID, pr.jobID, 5*time.Second, func(j *core.PDFParseJob) bool {
			return j.Status.IsTerminal()
		})

		if done.Status != core.PDFParseJobStatusCompleted {
			t.Fatalf("pdf %s terminated in %s: %s", pr.jobID, done.Status, done.ErrorMessage)
		}
		if done.AttemptCount != 1 {
			mu.Lock()
			duplicateExec = true
			mu.Unlock()
		}
	}

	notifyWG.Wait()

	mu.Lock()
	defer mu.Unlock()
	if branchOverlap {
		t.Errorf("detected overlapping concurrent generations on the same branch!")
	}
	if duplicateExec {
		t.Errorf("detected duplicate execution of a job: versions=%v pdfs=%v", versionExecs, pdfExecs)
	}
}
