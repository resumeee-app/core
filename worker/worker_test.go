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

	"github.com/resumeee-app/core"
	"github.com/resumeee-app/core/llm"
	"github.com/resumeee-app/core/memory"
	"github.com/resumeee-app/core/provenance"
	"github.com/resumeee-app/core/renderer"
	"github.com/resumeee-app/core/seed"
	"github.com/resumeee-app/core/store"
	"github.com/resumeee-app/core/worker"
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
	telemetry    core.LLMTelemetry
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
	telemetry := m.telemetry
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
		Telemetry: telemetry,
	}, nil
}

func (m *mockLLM) GenerateGapAnalysis(ctx context.Context, req core.GenerationRequest) (*core.GapAnalysisResponse, error) {
	resp, err := m.GenerateTailoredResume(ctx, req)
	if err != nil {
		return nil, err
	}
	return &core.GapAnalysisResponse{
		GapReport: resp.GapReport,
		Usage:     resp.Usage,
		Telemetry: resp.Telemetry,
	}, nil
}

func (m *mockLLM) SimulateATS(_ context.Context, _ core.ATSSimulationRequest) (*core.ATSSimulationResponse, error) {
	return &core.ATSSimulationResponse{}, nil
}

type testWorkerEnv struct {
	store        *store.Store
	db           *store.Store
	profileRepo  *store.Profiles
	careerRepo   *store.Career
	presetRepo   *store.Presets
	templateRepo *store.Templates
	branchRepo   *store.Branches
	jobRepo      *store.Jobs
	quotaRepo    *store.Quotas
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
	st := memory.NewWithClock(clock)
	if err := seed.DefaultData(ctx, st, defaultUser); err != nil {
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
	if err := st.Branches().CreateBranch(ctx, &branch); err != nil {
		t.Fatalf("failed to create branch: %v", err)
	}

	return &testWorkerEnv{
		store:        st,
		db:           st,
		profileRepo:  st.Profiles(),
		careerRepo:   st.Career(),
		presetRepo:   st.Presets(),
		templateRepo: st.Templates(),
		branchRepo:   st.Branches(),
		jobRepo:      st.Jobs(),
		quotaRepo:    st.Quotas(),
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
	st *store.Store,
	userID, branchID, versionID string,
	opts core.PendingJobOptions,
) *core.BranchVersion {
	t.Helper()
	rec, err := st.Quotas().ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 100, 1000000)
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
	job := &core.Job{
		ID:            "job-" + versionID,
		Kind:          core.JobKindBranchTailor,
		UserID:        userID,
		Status:        core.JobStatusPending,
		ReservationID: rec.ID,
		ExpiresAt:     &exp,
		CreatedAt:     now,
		UpdatedAt:     now,
		Tailor: &core.BranchTailorSpec{
			BranchID:  branchID,
			VersionID: versionID,
		},
	}
	ver := &core.BranchVersion{
		ID:                versionID,
		UserID:            userID,
		BranchID:          branchID,
		JobID:             job.ID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		SourceType:        core.SourceTypeLLMGeneration,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if _, err := st.Jobs().SubmitBranchTailor(ctx, job, ver, opts); err != nil {
		_ = st.Quotas().ReleaseQuota(ctx, rec.ID, err.Error(), core.LLMTelemetry{})
		t.Fatalf("SubmitBranchTailor failed: %v", err)
	}
	return ver
}

func jobForVersion(ctx context.Context, t *testing.T, jobs worker.JobStore, userID string, ver *core.BranchVersion) *core.Job {
	t.Helper()
	job, err := jobs.Job(ctx, userID, ver.JobID)
	if err != nil {
		t.Fatalf("Job %s for version %s failed: %v", ver.JobID, ver.ID, err)
	}
	return job
}

func submitPendingPDFWithQuota(
	ctx context.Context,
	t *testing.T,
	st *store.Store,
	notifier core.JobNotifier,
	userID, rawText, provider, model string,
	ttl time.Duration,
	clock func() time.Time,
) *core.Job {
	t.Helper()
	job, err := worker.SubmitPDFJob(ctx, st.Jobs(), worker.PDFJobSubmission{
		UserID:       userID,
		Filename:     "resume.pdf",
		Provider:     provider,
		Model:        model,
		RawText:      rawText,
		DailyLimit:   100,
		MonthlyLimit: 1000000,
	},
		worker.WithPDFQuotas(st.Quotas()),
		worker.WithPDFNotifier(notifier),
		worker.WithPDFJobTTL(ttl),
		worker.WithPDFCleanupTimeout(2*time.Second),
		worker.WithPDFClock(clock),
	)
	if err != nil {
		t.Fatalf("SubmitPDFJob failed: %v", err)
	}
	return job
}

func waitForVersionStatus(
	ctx context.Context,
	t *testing.T,
	branches worker.BranchReader,
	branchID, versionID string,
	timeout time.Duration,
	pred func(*core.BranchVersion) bool,
) *core.BranchVersion {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		v, err := branches.Version(ctx, branchID, versionID)
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
	jobs worker.JobStore,
	userID, jobID string,
	timeout time.Duration,
	pred func(*core.Job) bool,
) *core.Job {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		j, err := jobs.Job(ctx, userID, jobID)
		if err == nil && pred(j) {
			return j
		}
		if time.Now().After(deadline) {
			status := core.JobStatus("")
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

func reservationStatus(ctx context.Context, t *testing.T, st *store.Store, userID, reservationID string) core.ReservationStatus {
	t.Helper()
	records, err := st.Quotas().ListUsageRecords(ctx, userID, 100)
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

func waitForReservationStatus(
	ctx context.Context,
	t *testing.T,
	st *store.Store,
	userID, reservationID string,
	timeout time.Duration,
	want core.ReservationStatus,
) core.ReservationStatus {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last core.ReservationStatus
	for {
		last = reservationStatus(ctx, t, st, userID, reservationID)
		if last == want {
			return last
		}
		if time.Now().After(deadline) {
			return last
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// 1. Parallel Execution Test
func TestPool_ParallelExecution(t *testing.T) {
	env := setupTestWorkerEnv(t)
	env.llm.delay = 15 * time.Millisecond

	pool := worker.New(
		worker.WithConcurrency(4),
		worker.WithPollInterval(50*time.Millisecond),
		worker.WithJobs(env.jobRepo),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithGenerator(env.llm),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
	)

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

	recOver, err := env.quotaRepo.ReserveQuota(ctx, env.defaultUser, core.OperationResumeTailoring, 2000, 100, 1000000)
	if err != nil {
		t.Fatalf("ReserveQuota failed: %v", err)
	}
	exp := time.Now().UTC().Add(core.DefaultJobTTL)
	jobOver := &core.Job{
		ID:            "job-sat-overflow",
		Kind:          core.JobKindBranchTailor,
		UserID:        env.defaultUser,
		Status:        core.JobStatusPending,
		ReservationID: recOver.ID,
		ExpiresAt:     &exp,
		Tailor: &core.BranchTailorSpec{
			BranchID:  branches[2],
			VersionID: "ver-sat-overflow",
		},
	}
	verOver := &core.BranchVersion{
		ID:                "ver-sat-overflow",
		UserID:            env.defaultUser,
		BranchID:          branches[2],
		JobID:             jobOver.ID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
	}
	_, submitErr := env.jobRepo.SubmitBranchTailor(ctx, jobOver, verOver, core.PendingJobOptions{MaxPendingPerTenant: 2})
	if !errors.Is(submitErr, core.ErrPendingLimitExceeded) || !errors.Is(submitErr, core.ErrQueueFull) {
		t.Fatalf("expected ErrPendingLimitExceeded / ErrQueueFull, got %v", submitErr)
	}
	_ = env.quotaRepo.ReleaseQuota(ctx, recOver.ID, "rejected by pending limit", core.LLMTelemetry{})

	// Also verify PDF SubmitPDFJob enforces MaxPendingPerTenant and releases quota on rejection
	_, pdfErr := worker.SubmitPDFJob(ctx, env.jobRepo, worker.PDFJobSubmission{
		UserID:       env.defaultUser,
		RawText:      "overflow pdf text",
		DailyLimit:   100,
		MonthlyLimit: 1000000,
	},
		worker.WithPDFQuotas(env.quotaRepo),
		worker.WithPDFMaxPendingPerTenant(2),
	)
	if !errors.Is(pdfErr, core.ErrPendingLimitExceeded) || !errors.Is(pdfErr, core.ErrQueueFull) {
		t.Fatalf("expected SubmitPDFJob ErrPendingLimitExceeded / ErrQueueFull, got %v", pdfErr)
	}

	// Verify direct claiming saturation with Concurrency=1 leaves remaining pending jobs in store until slot frees
	unblock := make(chan struct{})
	started := make(chan string, 2)
	blockingLLM := &blockingMockLLM{unblock: unblock, started: started}

	pool := worker.New(
		worker.WithConcurrency(1),
		worker.WithPollInterval(50*time.Millisecond),
		worker.WithJobs(env.jobRepo),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithGenerator(blockingLLM),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
	)

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

func (m *blockingMockLLM) GenerateGapAnalysis(ctx context.Context, req core.GenerationRequest) (*core.GapAnalysisResponse, error) {
	resp, err := m.GenerateTailoredResume(ctx, req)
	if err != nil {
		return nil, err
	}
	return &core.GapAnalysisResponse{
		GapReport: resp.GapReport,
		Usage:     resp.Usage,
		Telemetry: resp.Telemetry,
	}, nil
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

	pool := worker.New(
		worker.WithConcurrency(2),
		worker.WithMaxConcurrentPerTenant(2),
		worker.WithPollInterval(50*time.Millisecond),
		worker.WithJobs(env.jobRepo),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithGenerator(llmMock),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
	)

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
		job := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, ver)
		if got := reservationStatus(ctx, t, env.store, env.defaultUser, job.ReservationID); got != core.ReservationStatusPending {
			t.Errorf("expected unclaimed reservation %s to stay pending, got %s", job.ReservationID, got)
		}
	}

	// Notify remains safe and non-blocking even after Stop.
	pool.Notify()
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

	vExpired := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, branches[0], "ver-crash-expired", core.PendingJobOptions{
		Now: clk.Now().Add(-35 * time.Minute),
		TTL: 30 * time.Minute,
	})
	vProcessing := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, branches[1], "ver-crash-processing", core.PendingJobOptions{
		Now: clk.Now(),
		TTL: 20 * time.Minute,
	})
	// Claim the unexpired job so it transitions to processing (vExpired is already past ExpiresAt and skipped).
	claimedProc, err := env.jobRepo.ClaimNext(ctx, core.ClaimJobRequest{
		WorkerID:      "dead-worker",
		Now:           clk.Now(),
		LeaseDuration: 20 * time.Minute,
	})
	if err != nil || claimedProc == nil || claimedProc.ID != vProcessing.JobID {
		t.Fatalf("expected ClaimNext to claim %s, got %+v err=%v", vProcessing.JobID, claimedProc, err)
	}
	vUnexpired := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, branches[2], "ver-crash-unexpired", core.PendingJobOptions{
		Now: clk.Now(),
		TTL: 20 * time.Minute,
	})

	pool := worker.New(
		worker.WithConcurrency(2),
		worker.WithPollInterval(20*time.Millisecond),
		worker.WithClock(clk.Now),
		worker.WithJobs(env.jobRepo),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithGenerator(env.llm),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
	)

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
		job := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, v)
		if st := reservationStatus(ctx, t, env.store, env.defaultUser, job.ReservationID); st != core.ReservationStatusReleased {
			t.Errorf("expected reservation %s released, got %s", job.ReservationID, st)
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
	jobUnexpired := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, vUnexpired)
	if st := reservationStatus(ctx, t, env.store, env.defaultUser, jobUnexpired.ReservationID); st != core.ReservationStatusPending {
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
	if st := reservationStatus(ctx, t, env.store, env.defaultUser, jobUnexpired.ReservationID); st != core.ReservationStatusCommitted {
		t.Errorf("expected preserved job reservation committed after completion, got %s", st)
	}
}

// 5. LLM Error Transition Test
func TestPool_LLMErrorTransition(t *testing.T) {
	env := setupTestWorkerEnv(t)
	env.llm.fail = true
	env.llm.failErrMsg = "invalid prompt or bad request"

	pool := worker.New(
		worker.WithConcurrency(2),
		worker.WithPollInterval(50*time.Millisecond),
		worker.WithJobs(env.jobRepo),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithGenerator(env.llm),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
	)

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
	job := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, ver)
	if st := waitForReservationStatus(ctx, t, env.store, env.defaultUser, job.ReservationID, 2*time.Second, core.ReservationStatusReleased); st != core.ReservationStatusReleased {
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

	pool := worker.New(
		worker.WithConcurrency(2),
		worker.WithPollInterval(50*time.Millisecond),
		worker.WithJobs(env.jobRepo),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithGenerator(env.llm),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
	)

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

	vProcessing := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, b2.ID, "ver-stale-processing", core.PendingJobOptions{
		Now: clk.Now(),
		TTL: 15 * time.Minute,
	})
	pdfProcessing := submitPendingPDFWithQuota(ctx, t, env.store, nil, env.defaultUser, "Interrupted PDF raw text", "mock", "m", 15*time.Minute, clk.Now)

	claimedV, err := env.jobRepo.ClaimNext(ctx, core.ClaimJobRequest{
		WorkerID:      "dead-worker",
		Now:           clk.Now(),
		LeaseDuration: 10 * time.Minute,
		PreferredKind: core.JobKindBranchTailor,
	})
	if err != nil || claimedV == nil || claimedV.ID != vProcessing.JobID {
		t.Fatalf("expected ClaimNext to claim %s, got %+v err=%v", vProcessing.JobID, claimedV, err)
	}
	claimedP, err := env.jobRepo.ClaimNext(ctx, core.ClaimJobRequest{
		WorkerID:      "dead-worker",
		Now:           clk.Now(),
		LeaseDuration: 10 * time.Minute,
		PreferredKind: core.JobKindPDFParse,
	})
	if err != nil || claimedP == nil || claimedP.ID != pdfProcessing.ID {
		t.Fatalf("expected ClaimNext to claim %s, got %+v err=%v", pdfProcessing.ID, claimedP, err)
	}

	vPending := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-stale-pending", core.PendingJobOptions{
		Now: clk.Now(),
		TTL: 15 * time.Minute,
	})
	pdfPending := submitPendingPDFWithQuota(ctx, t, env.store, nil, env.defaultUser, "Unexpired PDF raw text", "mock", "m", 15*time.Minute, clk.Now)

	ext := &mockResumeExtractor{result: &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Recovered PDF"}}}
	pool := worker.New(
		worker.WithConcurrency(2),
		worker.WithPollInterval(20*time.Millisecond),
		worker.WithClock(clk.Now),
		worker.WithJobs(env.jobRepo),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithResumeExtractor(ext),
		worker.WithGenerator(env.llm),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
	)

	const reason = "server restart recovery test"
	if err := pool.RecoverStaleJobs(ctx, reason); err != nil {
		t.Fatalf("RecoverStaleJobs failed: %v", err)
	}

	// Processing jobs must fail and release quota
	refV2, _ := env.branchRepo.Version(ctx, b2.ID, vProcessing.ID)
	if refV2.Status != core.VersionStatusFailed || refV2.ErrorMessage != reason {
		t.Errorf("expected processing version failed with %q, got %s (%q)", reason, refV2.Status, refV2.ErrorMessage)
	}
	if st := reservationStatus(ctx, t, env.store, env.defaultUser, claimedV.ReservationID); st != core.ReservationStatusReleased {
		t.Errorf("expected version processing quota released, got %s", st)
	}

	refP2, _ := env.jobRepo.Job(ctx, env.defaultUser, pdfProcessing.ID)
	if refP2.Status != core.JobStatusFailed || refP2.ErrorMessage != reason {
		t.Errorf("expected processing pdf job failed with %q, got %s (%q)", reason, refP2.Status, refP2.ErrorMessage)
	}
	if refP2.PDF == nil || refP2.PDF.RawText != "" {
		t.Errorf("expected processing pdf RawText pruned on recovery failure, got %+v", refP2.PDF)
	}
	if st := reservationStatus(ctx, t, env.store, env.defaultUser, pdfProcessing.ReservationID); st != core.ReservationStatusReleased {
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
	doneP1 := waitForPDFStatus(ctx, t, env.jobRepo, env.defaultUser, pdfPending.ID, 2*time.Second, func(j *core.Job) bool {
		return j.Status == core.JobStatusCompleted
	})

	if doneP1.PDF == nil || doneP1.PDF.Result == nil || doneP1.PDF.Result.Basics.Name != "Recovered PDF" {
		t.Fatalf("expected preserved pending pdf job completed cleanly, got %+v", doneP1)
	}
}

// TestPool_WorkerPanicRecovery verifies that a panic during job execution is caught,
// transitions the version to failed with a sanitized message, releases any pending quota
// reservation, and keeps the worker goroutine alive for subsequent jobs.
func TestPool_WorkerPanicRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	env := setupTestWorkerEnv(t)
	env.llm.mu.Lock()
	env.llm.shouldPanic = true
	env.llm.panicVal = "unexpected runtime nil pointer during model invocation"
	env.llm.mu.Unlock()

	pool := worker.New(
		worker.WithConcurrency(1),
		worker.WithPollInterval(50*time.Millisecond),
		worker.WithJobs(env.jobRepo),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithGenerator(env.llm),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
	)

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
	if strings.Contains(failed.ErrorMessage, "unexpected runtime nil pointer") {
		t.Errorf("expected raw panic details redacted from persisted ErrorMessage, got %q", failed.ErrorMessage)
	}
	panicJob := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, verPanic)
	if st := waitForReservationStatus(ctx, t, env.store, env.defaultUser, panicJob.ReservationID, 2*time.Second, core.ReservationStatusReleased); st != core.ReservationStatusReleased {
		t.Errorf("expected quota released on worker panic, got %s", st)
	}
	quotaAfterPanic, err := env.quotaRepo.Quota(ctx, env.defaultUser)
	if err != nil {
		t.Fatalf("Quota failed: %v", err)
	}
	if quotaAfterPanic.MonthlyTokensUsed != 0 || quotaAfterPanic.DailyRequestsUsed != 0 {
		t.Errorf("expected refunded quota (0, 0) after panic, got (%d, %d)",
			quotaAfterPanic.MonthlyTokensUsed, quotaAfterPanic.DailyRequestsUsed)
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
	telemetry    core.LLMTelemetry
	requestCount int
}

func (m *mockResumeExtractor) ExtractResume(ctx context.Context, _, _, _ string) (*core.JSONResume, core.LLMTelemetry, error) {
	m.mu.Lock()
	m.requestCount++
	delay := m.delay
	fail := m.fail
	failErr := m.failErr
	failErrMsg := m.failErrMsg
	result := m.result
	telemetry := m.telemetry
	if telemetry.Usage.IsZero() && !m.usage.IsZero() {
		telemetry.Usage = m.usage
	}
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, telemetry, ctx.Err()
		}
	}
	if failErr != nil {
		return nil, telemetry, failErr
	}
	if fail {
		return nil, telemetry, errors.New(failErrMsg)
	}
	if result != nil {
		return result, telemetry, nil
	}
	return &core.JSONResume{
		Basics: core.JSONResumeBasics{
			Name:  "Test Candidate",
			Email: "candidate@test.com",
		},
	}, telemetry, nil
}

// 9. PDF Parse Job Success
func TestWorkerPool_PDFParseJob_Success(t *testing.T) {
	st := memory.New()
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

	pool := worker.New(
		worker.WithConcurrency(2),
		worker.WithPollInterval(50*time.Millisecond),
		worker.WithJobs(st.Jobs()),
		worker.WithQuotas(st.Quotas()),
		worker.WithResumeExtractor(mockExt),
		worker.WithLLMTimeout(5*time.Second),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	job := submitPendingPDFWithQuota(ctx, t, st, pool, "user-1", "Admiral Grace Hopper resume text", "mock", "mock-model", core.DefaultJobTTL, nil)

	done := waitForPDFStatus(ctx, t, st.Jobs(), "user-1", job.ID, 2*time.Second, func(j *core.Job) bool {
		return j.Status.IsTerminal()
	})

	if done.Status != core.JobStatusCompleted {
		t.Fatalf("expected completed, got %s (%s)", done.Status, done.ErrorMessage)
	}
	if done.CompletedAt == nil {
		t.Errorf("expected CompletedAt to be set")
	}
	if done.PDF == nil || done.PDF.Result == nil || done.PDF.Result.Basics.Name != "Grace Hopper" {
		t.Fatalf("unexpected JSONResume result: %+v", done.PDF)
	}
	if done.PDF.RawText != "" {
		t.Errorf("expected RawText pruned after completion, got %q", done.PDF.RawText)
	}
	if capturedText != "Admiral Grace Hopper resume text" || capturedProvider != "mock" || capturedModel != "mock-model" {
		t.Errorf("extractor received unexpected persisted fields: text=%q provider=%q model=%q", capturedText, capturedProvider, capturedModel)
	}
}

// 10. PDF Parse Job Failure
func TestWorkerPool_PDFParseJob_Failure(t *testing.T) {
	st := memory.New()
	mockExt := &mockResumeExtractor{
		fail:       true,
		failErrMsg: "upstream model capacity reached",
	}

	pool := worker.New(
		worker.WithConcurrency(2),
		worker.WithPollInterval(50*time.Millisecond),
		worker.WithJobs(st.Jobs()),
		worker.WithQuotas(st.Quotas()),
		worker.WithResumeExtractor(mockExt),
		worker.WithLLMTimeout(5*time.Second),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	job := submitPendingPDFWithQuota(ctx, t, st, pool, "user-1", "Some text", "mock", "mock-model", core.DefaultJobTTL, nil)

	done := waitForPDFStatus(ctx, t, st.Jobs(), "user-1", job.ID, 2*time.Second, func(j *core.Job) bool {
		return j.Status.IsTerminal()
	})

	if done.Status != core.JobStatusFailed {
		t.Fatalf("expected job to fail, got %s", done.Status)
	}
	if !strings.Contains(done.ErrorMessage, "Failed to extract resume from document") {
		t.Errorf("expected sanitized error message, got %q", done.ErrorMessage)
	}
	if done.PDF == nil || done.PDF.RawText != "" {
		t.Errorf("expected RawText pruned on failure, got %+v", done.PDF)
	}
	if status := reservationStatus(ctx, t, st, "user-1", job.ReservationID); status != core.ReservationStatusReleased {
		t.Errorf("expected quota reservation released on PDF failure, got %s", status)
	}
}

// 11. LLMTimeout Context Bounding Test
func TestWorkerPool_TailoringPipeline_LLMTimeout(t *testing.T) {
	env := setupTestWorkerEnv(t)
	llmMock := &mockLLM{}

	pool := worker.New(
		worker.WithConcurrency(1),
		worker.WithPollInterval(50*time.Millisecond),
		worker.WithJobs(env.jobRepo),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithGenerator(llmMock),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
		worker.WithLLMTimeout(7*time.Minute),
	)

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
	st := memory.New()
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

	pool := worker.New(
		worker.WithConcurrency(4),
		worker.WithMaxConcurrentPerTenant(2),
		worker.WithPollInterval(20*time.Millisecond),
		worker.WithJobs(st.Jobs()),
		worker.WithQuotas(st.Quotas()),
		worker.WithResumeExtractor(mockExt),
		worker.WithLLMTimeout(5*time.Second),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	const jobCount = 6
	tenantID := "tenant-alpha"
	jobIDs := make([]string, jobCount)
	for i := 0; i < jobCount; i++ {
		j := submitPendingPDFWithQuota(ctx, t, st, pool, tenantID, fmt.Sprintf("resume %d", i), "mock", "m", core.DefaultJobTTL, nil)
		jobIDs[i] = j.ID
	}

	for _, id := range jobIDs {
		done := waitForPDFStatus(ctx, t, st.Jobs(), tenantID, id, 5*time.Second, func(j *core.Job) bool {
			return j.Status.IsTerminal()
		})

		if done.Status != core.JobStatusCompleted {
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
	st := memory.New()

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

	pool := worker.New(
		worker.WithConcurrency(2),
		worker.WithMaxConcurrentPerTenant(1),
		worker.WithPollInterval(20*time.Millisecond),
		worker.WithJobs(st.Jobs()),
		worker.WithQuotas(st.Quotas()),
		worker.WithResumeExtractor(mockExt),
		worker.WithLLMTimeout(5*time.Second),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	tenantA := "tenant-a-noisy"
	const tenantAJobs = 8
	tenantAJobIDs := make([]string, tenantAJobs)
	for i := 0; i < tenantAJobs; i++ {
		j := submitPendingPDFWithQuota(ctx, t, st, pool, tenantA, fmt.Sprintf("slow-%d", i), "slow", "m", core.DefaultJobTTL, nil)
		tenantAJobIDs[i] = j.ID
	}

	time.Sleep(15 * time.Millisecond)

	tenantB := "tenant-b-fast"
	jB := submitPendingPDFWithQuota(ctx, t, st, pool, tenantB, "fast-0", "fast", "m", core.DefaultJobTTL, nil)

	doneB := waitForPDFStatus(ctx, t, st.Jobs(), tenantB, jB.ID, 500*time.Millisecond, func(j *core.Job) bool {
		return j.Status == core.JobStatusCompleted
	})

	if doneB.Status != core.JobStatusCompleted {
		t.Fatalf("expected tenant B job completed")
	}

	var remainingA int
	for _, id := range tenantAJobIDs {
		ja, err := st.Jobs().Job(ctx, tenantA, id)
		if err != nil {
			t.Fatalf("failed to get tenant A job %s: %v", id, err)
		}
		if ja.Status != core.JobStatusCompleted {
			remainingA++
		}
	}
	if remainingA < 4 {
		t.Fatalf("expected at least 4 of tenant A's jobs to still be incomplete when B finished, got remaining %d", remainingA)
	}
}

// 14. Multi-Tenant Round-Robin Ordering Test
func TestPool_MultiTenantRoundRobinOrdering(t *testing.T) {
	base := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	clk := newFakeClock(base.Add(30 * time.Second))
	st := memory.NewWithClock(clk.Now)
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

	pool := worker.New(
		worker.WithConcurrency(1),
		worker.WithMaxConcurrentPerTenant(1),
		worker.WithPollInterval(50*time.Millisecond),
		worker.WithClock(clk.Now),
		worker.WithJobs(st.Jobs()),
		worker.WithQuotas(st.Quotas()),
		worker.WithResumeExtractor(mockExt),
		worker.WithLLMTimeout(5*time.Second),
	)

	ctx := context.Background()
	tenants := []string{"tenant-a", "tenant-b", "tenant-c"}
	totalJobs := 0
	for idx, tID := range tenants {
		for j := 1; j <= 2; j++ {
			rec, err := st.Quotas().ReserveQuota(ctx, tID, core.OperationPDFParse, 1000, 100, 1000000)
			if err != nil {
				t.Fatalf("ReserveQuota failed: %v", err)
			}
			ts := base.Add(time.Duration(idx*10+j) * time.Second)
			exp := base.Add(core.DefaultJobTTL)
			job := &core.Job{
				ID:            fmt.Sprintf("%s-job-%d", tID, j),
				Kind:          core.JobKindPDFParse,
				UserID:        tID,
				Status:        core.JobStatusPending,
				ReservationID: rec.ID,
				ExpiresAt:     &exp,
				CreatedAt:     ts,
				UpdatedAt:     ts,
				PDF: &core.PDFParseSpec{
					RawText: tID,
				},
			}
			if err := st.Jobs().SubmitPDFParse(ctx, job, core.PendingJobOptions{Now: ts}); err != nil {
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
	st := memory.New()
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

	pool := worker.New(
		worker.WithConcurrency(2),
		worker.WithMaxConcurrentPerTenant(1),
		worker.WithPollInterval(50*time.Millisecond),
		worker.WithJobs(st.Jobs()),
		worker.WithQuotas(st.Quotas()),
		worker.WithResumeExtractor(mockExt),
		worker.WithLLMTimeout(5*time.Second),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	tenantID := "tenant-panic-test"
	j1 := submitPendingPDFWithQuota(ctx, t, st, pool, tenantID, "panic text", "mock", "m", core.DefaultJobTTL, nil)

	done1 := waitForPDFStatus(ctx, t, st.Jobs(), tenantID, j1.ID, 2*time.Second, func(j *core.Job) bool {
		return j.Status == core.JobStatusFailed
	})

	if done1.PDF == nil || done1.PDF.RawText != "" {
		t.Errorf("expected RawText pruned on panic failure, got %+v", done1.PDF)
	}
	if status := waitForReservationStatus(ctx, t, st, tenantID, j1.ReservationID, 2*time.Second, core.ReservationStatusReleased); status != core.ReservationStatusReleased {
		t.Errorf("expected quota released on PDF panic, got %s", status)
	}

	// Enqueue job 2 for same tenant; must execute since slot was freed on panic
	j2 := submitPendingPDFWithQuota(ctx, t, st, pool, tenantID, "recovery text", "mock", "m", core.DefaultJobTTL, nil)
	done2 := waitForPDFStatus(ctx, t, st.Jobs(), tenantID, j2.ID, 2*time.Second, func(j *core.Job) bool {
		return j.Status.IsTerminal()
	})

	if done2.Status != core.JobStatusCompleted {
		t.Fatalf("job 2 failed unexpectedly: %s (%s)", done2.Status, done2.ErrorMessage)
	}
}

type callbackResumeExtractor struct {
	fn    func(ctx context.Context, text, provider, model string) (*core.JSONResume, error)
	fnTel func(ctx context.Context, text, provider, model string) (*core.JSONResume, core.LLMTelemetry, error)
}

func (c *callbackResumeExtractor) ExtractResume(ctx context.Context, text, provider, model string) (*core.JSONResume, core.LLMTelemetry, error) {
	if c.fnTel != nil {
		return c.fnTel(ctx, text, provider, model)
	}
	if c.fn != nil {
		res, err := c.fn(ctx, text, provider, model)
		return res, core.LLMTelemetry{}, err
	}
	return &core.JSONResume{}, core.LLMTelemetry{}, nil
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

func (m *callbackLLM) GenerateGapAnalysis(ctx context.Context, req core.GenerationRequest) (*core.GapAnalysisResponse, error) {
	resp, err := m.GenerateTailoredResume(ctx, req)
	if err != nil {
		return nil, err
	}
	return &core.GapAnalysisResponse{
		GapReport: resp.GapReport,
		Usage:     resp.Usage,
		Telemetry: resp.Telemetry,
	}, nil
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

	pool := worker.New(
		worker.WithConcurrency(4),
		worker.WithMaxConcurrentPerTenant(2),
		worker.WithPollInterval(20*time.Millisecond),
		worker.WithJobs(env.jobRepo),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithGenerator(llmMock),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
	)

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

	pool := worker.New(
		worker.WithConcurrency(4),
		worker.WithMaxConcurrentPerTenant(2),
		worker.WithPollInterval(20*time.Millisecond),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithJobs(env.jobRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithResumeExtractor(extractor),
		worker.WithGenerator(llmMock),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
		worker.WithLLMTimeout(5*time.Second),
	)

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
		done := waitForPDFStatus(ctx, t, env.jobRepo, tenantID, pID, 5*time.Second, func(j *core.Job) bool {
			return j.Status.IsTerminal()
		})

		if done.Status != core.JobStatusCompleted {
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

	pool := worker.New(
		worker.WithConcurrency(2),
		worker.WithMaxConcurrentPerTenant(1),
		worker.WithPollInterval(20*time.Millisecond),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithJobs(env.jobRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithResumeExtractor(extractor),
		worker.WithGenerator(llmMock),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
		worker.WithLLMTimeout(5*time.Second),
	)

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

	doneB := waitForPDFStatus(ctx, t, env.jobRepo, tenantB, pj.ID, 500*time.Millisecond, func(j *core.Job) bool {
		return j.Status == core.JobStatusCompleted
	})

	if doneB.Status != core.JobStatusCompleted {
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

	st, err := memory.NewSeeded(ctx, "user-quota-worker")
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

	pool := worker.New(
		worker.WithConcurrency(2),
		worker.WithPollInterval(50*time.Millisecond),
		worker.WithJobs(st.Jobs()),
		worker.WithQuotas(st.Quotas()),
		worker.WithResumeExtractor(mockExt),
		worker.WithLLMTimeout(5*time.Second),
	)
	pool.Start(ctx)
	defer pool.Stop()

	job := submitPendingPDFWithQuota(ctx, t, st, pool, "user-quota-worker", "Candidate resume content", "mock", "mock-model", core.DefaultJobTTL, nil)

	done := waitForPDFStatus(ctx, t, st.Jobs(), "user-quota-worker", job.ID, 2*time.Second, func(j *core.Job) bool {
		return j.Status.IsTerminal()
	})

	if done.Status != core.JobStatusCompleted {
		t.Fatalf("pdf job failed: %s", done.ErrorMessage)
	}

	quota, err := st.Quotas().Quota(ctx, "user-quota-worker")
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

		pool := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(50*time.Millisecond),
			worker.WithJobs(env.jobRepo),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithGenerator(llmMock),
			worker.WithValidator(env.validator),
		)
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
			ID:             "branch-reasoning-override",
			UserID:         env.defaultUser,
			Name:           "Tailored Branch",
			TemplateSetID:  "builtin-standard-clean",
			PresetID:       &presetID,
			ReasoningLevel: "high",
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

		pool := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(50*time.Millisecond),
			worker.WithJobs(env.jobRepo),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithGenerator(llmMock),
			worker.WithValidator(env.validator),
		)
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
			ID:             "branch-reasoning-fallback",
			UserID:         env.defaultUser,
			Name:           "Fallback Branch",
			TemplateSetID:  "builtin-standard-clean",
			PresetID:       &presetID,
			ReasoningLevel: "",
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

		pool := worker.New(
			worker.WithConcurrency(2),
			worker.WithPollInterval(10*time.Second),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithJobs(env.jobRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithResumeExtractor(ext),
			worker.WithGenerator(env.llm),
			worker.WithValidator(env.validator),
			worker.WithRenderer(env.renderer),
		)

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
		donePDF := waitForPDFStatus(ctx, t, env.jobRepo, "user-notify-pdf", pdfJob.ID, 500*time.Millisecond, func(j *core.Job) bool {
			return j.Status == core.JobStatusCompleted
		})

		if donePDF.Status != core.JobStatusCompleted {
			t.Fatalf("expected PDF completed via Notify, got %s", donePDF.Status)
		}
	})

	t.Run("PollInterval discovers persisted jobs without any Notify or Enqueue", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		ext := &mockResumeExtractor{result: &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Polled PDF"}}}

		pool := worker.New(
			worker.WithConcurrency(2),
			worker.WithPollInterval(20*time.Millisecond),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithJobs(env.jobRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithResumeExtractor(ext),
			worker.WithGenerator(env.llm),
			worker.WithValidator(env.validator),
			worker.WithRenderer(env.renderer),
		)

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
		donePDF := waitForPDFStatus(ctx, t, env.jobRepo, "user-poll-pdf", pdfJob.ID, 2*time.Second, func(j *core.Job) bool {
			return j.Status == core.JobStatusCompleted
		})

		if donePDF.Status != core.JobStatusCompleted {
			t.Fatalf("expected polled pdf completed, got %s", donePDF.Status)
		}
	})
}

// 22. Alternating Store Preference & Cross-Queue Fairness
func TestPool_MixedWorkload_AlternatingFairness(t *testing.T) {
	base := time.Date(2026, 11, 15, 13, 0, 0, 0, time.UTC)
	clk := newFakeClock(base)
	env := setupTestWorkerEnvWithClock(t, clk.Now)
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
		ts := base.Add(time.Duration(i) * time.Second)
		_ = submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, b.ID, fmt.Sprintf("ver-alt-%d", i), core.PendingJobOptions{
			MaxPendingPerTenant: 10,
			Now:                 ts,
		})

		if _, err := worker.SubmitPDFJob(ctx, env.jobRepo, worker.PDFJobSubmission{
			UserID:       env.defaultUser,
			Filename:     "resume.pdf",
			Provider:     "mock",
			Model:        "m",
			RawText:      fmt.Sprintf("t%d", i),
			DailyLimit:   100,
			MonthlyLimit: 1000000,
		},
			worker.WithPDFQuotas(env.quotaRepo),
			worker.WithPDFMaxPendingPerTenant(10),
			worker.WithPDFJobTTL(core.DefaultJobTTL),
			worker.WithPDFCleanupTimeout(2*time.Second),
			worker.WithPDFClock(func() time.Time { return ts }),
		); err != nil {
			t.Fatalf("SubmitPDFJob %d failed: %v", i, err)
		}
	}

	pool := worker.New(
		worker.WithConcurrency(1),
		worker.WithMaxConcurrentPerTenant(1),
		worker.WithPollInterval(50*time.Millisecond),
		worker.WithClock(clk.Now),
		worker.WithJobs(env.jobRepo),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithResumeExtractor(ext),
		worker.WithGenerator(llmMock),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
	)

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

	pool := worker.New(
		worker.WithConcurrency(2),
		worker.WithPollInterval(20*time.Millisecond),
		worker.WithSweepInterval(25*time.Millisecond),
		worker.WithClock(clk.Now),
		worker.WithJobs(env.jobRepo),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithResumeExtractor(ext),
		worker.WithGenerator(llmMock),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
	)

	pool.Start(ctx)
	defer pool.Stop()

	failedVer := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 2*time.Second, func(v *core.BranchVersion) bool {
		return v.Status == core.VersionStatusFailed
	})

	if !strings.Contains(failedVer.ErrorMessage, "expired") {
		t.Errorf("expected expiration reason on version, got %q", failedVer.ErrorMessage)
	}
	verJob := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, ver)
	if st := waitForReservationStatus(ctx, t, env.store, env.defaultUser, verJob.ReservationID, 2*time.Second, core.ReservationStatusReleased); st != core.ReservationStatusReleased {
		t.Errorf("expected version quota released on TTL sweep, got %s", st)
	}

	failedPDF := waitForPDFStatus(ctx, t, env.jobRepo, "user-ttl-pdf", pdfJob.ID, 2*time.Second, func(j *core.Job) bool {
		return j.Status == core.JobStatusFailed
	})

	if failedPDF.PDF == nil || failedPDF.PDF.RawText != "" {
		t.Errorf("expected expired PDF RawText pruned, got %+v", failedPDF.PDF)
	}
	if st := waitForReservationStatus(ctx, t, env.store, "user-ttl-pdf", pdfJob.ReservationID, 2*time.Second, core.ReservationStatusReleased); st != core.ReservationStatusReleased {
		t.Errorf("expected PDF quota released on TTL sweep, got %s", st)
	}

	if genCalls.Load() != 0 || pdfCalls.Load() != 0 {
		t.Fatalf("expected expired jobs never to invoke LLM/extractor, got gen=%d pdf=%d", genCalls.Load(), pdfCalls.Load())
	}
}

type spyRenewJobStore struct {
	worker.JobStore
	renewErr    error
	renewCalls  atomic.Int32
	firstRenew  chan struct{}
	onceFirstRn sync.Once
}

func (s *spyRenewJobStore) RenewLease(ctx context.Context, jobID, claimedBy string, leaseExpiresAt time.Time) error {
	s.renewCalls.Add(1)
	s.onceFirstRn.Do(func() {
		if s.firstRenew != nil {
			close(s.firstRenew)
		}
	})
	if s.renewErr != nil {
		return s.renewErr
	}
	return s.JobStore.RenewLease(ctx, jobID, claimedBy, leaseExpiresAt)
}

// 24. Lease Heartbeat Renewal + Stale Lease Sweep + Lease Loss Cancellation
func TestPool_StaleLeaseSweepAndHeartbeatRenewal(t *testing.T) {
	t.Run("heartbeat renews lease so slow LLM call completes without premature lease expiration", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		spyJobs := &spyRenewJobStore{JobStore: env.jobRepo}

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

		pool := worker.New(
			worker.WithConcurrency(2),
			worker.WithPollInterval(20*time.Millisecond),
			worker.WithSweepInterval(25*time.Millisecond),
			worker.WithLeaseDuration(70*time.Millisecond),
			worker.WithLeaseRenewInterval(20*time.Millisecond),
			worker.WithJobs(spyJobs),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithResumeExtractor(ext),
			worker.WithGenerator(llmMock),
			worker.WithValidator(env.validator),
			worker.WithRenderer(env.renderer),
		)

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
		if spyJobs.renewCalls.Load() < 2 {
			t.Errorf("expected at least 2 lease renewals during slow execution, got %d", spyJobs.renewCalls.Load())
		}

		donePDF := waitForPDFStatus(ctx, t, env.jobRepo, "user-hb-pdf", pdfJob.ID, 3*time.Second, func(j *core.Job) bool {
			return j.Status.IsTerminal()
		})

		if donePDF.Status != core.JobStatusCompleted {
			t.Fatalf("expected heartbeat-renewed pdf job completed, got %s (%s)", donePDF.Status, donePDF.ErrorMessage)
		}
	})

	t.Run("stale lease sweep fails expired processing leases and releases quota", func(t *testing.T) {
		clk := newFakeClock(time.Date(2026, 11, 15, 15, 0, 0, 0, time.UTC))
		env := setupTestWorkerEnvWithClock(t, clk.Now)
		ctx := context.Background()

		tStart := clk.Now().Add(-21 * time.Minute)
		verStale := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-stale-lease", core.PendingJobOptions{
			TTL: 40 * time.Minute,
			Now: tStart,
		})
		pdfStale := submitPendingPDFWithQuota(ctx, t, env.store, nil, "user-stale-lease", "abandoned text", "mock", "m", 40*time.Minute, func() time.Time { return tStart })

		c1, err := env.jobRepo.ClaimNext(ctx, core.ClaimJobRequest{
			WorkerID:               "dead-worker",
			LeaseDuration:          20 * time.Minute,
			MaxConcurrentPerTenant: 1,
			Now:                    tStart,
		})
		if err != nil || c1 == nil {
			t.Fatalf("ClaimNext 1 failed: job=%v err=%v", c1, err)
		}
		c2, err := env.jobRepo.ClaimNext(ctx, core.ClaimJobRequest{
			WorkerID:               "dead-worker",
			LeaseDuration:          20 * time.Minute,
			MaxConcurrentPerTenant: 1,
			Now:                    tStart,
		})
		if err != nil || c2 == nil {
			t.Fatalf("ClaimNext 2 failed: job=%v err=%v", c2, err)
		}
		verJob := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, verStale)

		pool := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(1*time.Second),
			worker.WithSweepInterval(20*time.Millisecond),
			worker.WithClock(clk.Now),
			worker.WithJobs(env.jobRepo),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
		)
		pool.Start(ctx)
		defer pool.Stop()

		failedVer := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, verStale.ID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusFailed
		})

		if !strings.Contains(failedVer.ErrorMessage, "lease") {
			t.Errorf("expected lease expiration message, got %q", failedVer.ErrorMessage)
		}
		if st := waitForReservationStatus(ctx, t, env.store, env.defaultUser, verJob.ReservationID, 2*time.Second, core.ReservationStatusReleased); st != core.ReservationStatusReleased {
			t.Errorf("expected version quota released on lease sweep, got %s", st)
		}

		failedPDF := waitForPDFStatus(ctx, t, env.jobRepo, "user-stale-lease", pdfStale.ID, 2*time.Second, func(j *core.Job) bool {
			return j.Status == core.JobStatusFailed
		})

		if failedPDF.PDF == nil || failedPDF.PDF.RawText != "" {
			t.Errorf("expected abandoned PDF RawText pruned on lease sweep, got %+v", failedPDF.PDF)
		}
		if st := waitForReservationStatus(ctx, t, env.store, "user-stale-lease", pdfStale.ReservationID, 2*time.Second, core.ReservationStatusReleased); st != core.ReservationStatusReleased {
			t.Errorf("expected pdf quota released on lease sweep, got %s", st)
		}
	})

	t.Run("heartbeat losing lease via ErrConflict cancels in-flight attempt context", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		spyJobs := &spyRenewJobStore{
			JobStore:   env.jobRepo,
			renewErr:   core.ErrConflict,
			firstRenew: make(chan struct{}),
		}

		attemptCancelled := make(chan struct{})
		llmMock := &callbackLLM{
			fn: func(ctx context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
				<-ctx.Done()
				close(attemptCancelled)
				return nil, ctx.Err()
			},
		}

		pool := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(50*time.Millisecond),
			worker.WithLeaseDuration(60*time.Millisecond),
			worker.WithLeaseRenewInterval(15*time.Millisecond),
			worker.WithJobs(spyJobs),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithGenerator(llmMock),
			worker.WithValidator(env.validator),
			worker.WithRenderer(env.renderer),
		)

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

				pool := worker.New(
					worker.WithConcurrency(2),
					worker.WithMaxAttempts(3),
					worker.WithRetryBaseDelay(10*time.Millisecond),
					worker.WithPollInterval(50*time.Millisecond),
					worker.WithJobs(env.jobRepo),
					worker.WithBranches(env.branchRepo),
					worker.WithProfiles(env.profileRepo),
					worker.WithPresets(env.presetRepo),
					worker.WithTemplates(env.templateRepo),
					worker.WithQuotas(env.quotaRepo),
					worker.WithResumeExtractor(ext),
					worker.WithGenerator(llmMock),
					worker.WithValidator(env.validator),
					worker.WithRenderer(env.renderer),
				)

				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				pool.Start(ctx)
				defer pool.Stop()

				ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-ff-"+tc.name, core.PendingJobOptions{})
				pdfJob := submitPendingPDFWithQuota(ctx, t, env.store, pool, "user-ff-pdf", "Raw PDF", "mock", "m", core.DefaultJobTTL, nil)
				pool.Notify()

				_ = waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 2*time.Second, func(v *core.BranchVersion) bool {
					return v.Status == core.VersionStatusFailed
				})
				verJob := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, ver)

				if attempts.Load() != 1 || verJob.AttemptCount != 1 {
					t.Fatalf("expected 1 fail-fast version attempt, got calls=%d attemptCount=%d", attempts.Load(), verJob.AttemptCount)
				}
				if st := waitForReservationStatus(ctx, t, env.store, env.defaultUser, verJob.ReservationID, 2*time.Second, core.ReservationStatusReleased); st != core.ReservationStatusReleased {
					t.Errorf("expected version quota released, got %s", st)
				}

				failedPDF := waitForPDFStatus(ctx, t, env.jobRepo, "user-ff-pdf", pdfJob.ID, 2*time.Second, func(j *core.Job) bool {
					return j.Status == core.JobStatusFailed
				})

				if pdfAttempts.Load() != 1 || failedPDF.AttemptCount != 1 {
					t.Fatalf("expected 1 fail-fast PDF attempt, got calls=%d attemptCount=%d", pdfAttempts.Load(), failedPDF.AttemptCount)
				}
				if failedPDF.PDF == nil || failedPDF.PDF.RawText != "" {
					t.Errorf("expected PDF RawText pruned on fail-fast, got %+v", failedPDF.PDF)
				}
				if st := waitForReservationStatus(ctx, t, env.store, "user-ff-pdf", pdfJob.ReservationID, 2*time.Second, core.ReservationStatusReleased); st != core.ReservationStatusReleased {
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
		pool := worker.New(
			worker.WithConcurrency(1),
			worker.WithMaxAttempts(3),
			worker.WithRetryBaseDelay(80*time.Millisecond),
			worker.WithMaxRetryDelay(200*time.Millisecond),
			worker.WithPollInterval(1*time.Second), // proves retry wakeup timer works without PollInterval
			worker.WithJobs(env.jobRepo),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithGenerator(llmMock),
			worker.WithValidator(env.validator),
			worker.WithRenderer(env.renderer),
		)

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
		_ = waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, verA.ID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusCompleted
		})
		verAJob := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, verA)

		if verAJob.AttemptCount != 3 || tenantAAttempts.Load() != 3 {
			t.Fatalf("expected 3 attempts, got AttemptCount=%d calls=%d", verAJob.AttemptCount, tenantAAttempts.Load())
		}
		if st := reservationStatus(ctx, t, env.store, env.defaultUser, verAJob.ReservationID); st != core.ReservationStatusCommitted {
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

		pool := worker.New(
			worker.WithConcurrency(1),
			worker.WithMaxAttempts(3),
			worker.WithRetryBaseDelay(10*time.Millisecond),
			worker.WithMaxRetryDelay(20*time.Millisecond),
			worker.WithPollInterval(50*time.Millisecond),
			worker.WithJobs(env.jobRepo),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithGenerator(llmMock),
			worker.WithValidator(env.validator),
			worker.WithRenderer(env.renderer),
		)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.Start(ctx)
		defer pool.Stop()

		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-exhaust", core.PendingJobOptions{})
		pool.Notify()

		_ = waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusFailed
		})
		verJob := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, ver)

		if verJob.AttemptCount != 3 || attempts.Load() != 3 {
			t.Fatalf("expected 3 exhausted attempts, got AttemptCount=%d calls=%d", verJob.AttemptCount, attempts.Load())
		}
		if st := waitForReservationStatus(ctx, t, env.store, env.defaultUser, verJob.ReservationID, 2*time.Second, core.ReservationStatusReleased); st != core.ReservationStatusReleased {
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

		pool := worker.New(
			worker.WithConcurrency(1),
			worker.WithMaxAttempts(5),
			worker.WithRetryBaseDelay(10*time.Minute), // exceeds remaining 2m TTL!
			worker.WithMaxRetryDelay(30*time.Minute),
			worker.WithPollInterval(50*time.Millisecond),
			worker.WithClock(clk.Now),
			worker.WithJobs(env.jobRepo),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithGenerator(llmMock),
			worker.WithValidator(env.validator),
			worker.WithRenderer(env.renderer),
		)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.Start(ctx)
		defer pool.Stop()

		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-ttl-cutoff", core.PendingJobOptions{
			TTL: 2 * time.Minute,
			Now: clk.Now(),
		})

		pool.Notify()

		_ = waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusFailed
		})
		verJob := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, ver)

		if attempts.Load() != 1 || verJob.AttemptCount != 1 {
			t.Fatalf("expected immediate failure when retry exceeds TTL, got calls=%d attemptCount=%d", attempts.Load(), verJob.AttemptCount)
		}
		if st := waitForReservationStatus(ctx, t, env.store, env.defaultUser, verJob.ReservationID, 2*time.Second, core.ReservationStatusReleased); st != core.ReservationStatusReleased {
			t.Errorf("expected quota released when retry bounded by TTL, got %s", st)
		}
	})

	t.Run("PDF transient retry preserves RawText across attempts and prunes on completion", func(t *testing.T) {
		st := memory.New()
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

		pool := worker.New(
			worker.WithConcurrency(1),
			worker.WithMaxAttempts(3),
			worker.WithRetryBaseDelay(15*time.Millisecond),
			worker.WithMaxRetryDelay(30*time.Millisecond),
			worker.WithPollInterval(50*time.Millisecond),
			worker.WithJobs(st.Jobs()),
			worker.WithQuotas(st.Quotas()),
			worker.WithResumeExtractor(ext),
		)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.Start(ctx)
		defer pool.Stop()

		const raw = "Persisted PDF raw text across retry"
		job := submitPendingPDFWithQuota(ctx, t, st, pool, "user-pdf-retry", raw, "mock", "m", core.DefaultJobTTL, nil)

		done := waitForPDFStatus(ctx, t, st.Jobs(), "user-pdf-retry", job.ID, 2*time.Second, func(j *core.Job) bool {
			return j.Status == core.JobStatusCompleted
		})

		if done.AttemptCount != 2 {
			t.Errorf("expected AttemptCount == 2, got %d", done.AttemptCount)
		}
		if done.PDF == nil || done.PDF.RawText != "" {
			t.Errorf("expected RawText pruned after completion, got %+v", done.PDF)
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
		cancelledVer, cancelledJob, err := env.jobRepo.CancelBranchVersion(ctx, env.defaultUser, env.branchID, ver.ID, "cancelled by user")
		if err != nil {
			t.Fatalf("CancelBranchVersion failed: %v", err)
		}
		if cancelledJob != nil && cancelledJob.ReservationID != "" {
			_ = env.quotaRepo.ReleaseQuota(ctx, cancelledJob.ReservationID, "cancelled by user", core.LLMTelemetry{})
		}

		pdfJob := submitPendingPDFWithQuota(ctx, t, env.store, nil, "user-cancel-pdf", "Sensitive raw resume text", "mock", "m", core.DefaultJobTTL, nil)
		cancelledPDF, err := worker.CancelPDFJob(ctx, env.jobRepo, env.quotaRepo, "user-cancel-pdf", pdfJob.ID, 2*time.Second)
		if err != nil {
			t.Fatalf("CancelPDFJob failed: %v", err)
		}
		if cancelledPDF.Status != core.JobStatusCancelled || cancelledPDF.PDF == nil || cancelledPDF.PDF.RawText != "" {
			t.Fatalf("expected cancelled PDF with pruned RawText, got status=%s pdf=%+v", cancelledPDF.Status, cancelledPDF.PDF)
		}

		pool := worker.New(
			worker.WithConcurrency(2),
			worker.WithPollInterval(20*time.Millisecond),
			worker.WithJobs(env.jobRepo),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithResumeExtractor(ext),
			worker.WithGenerator(llmMock),
			worker.WithValidator(env.validator),
			worker.WithRenderer(env.renderer),
		)
		pool.Start(ctx)
		time.Sleep(60 * time.Millisecond)
		pool.Stop()

		if genCalls.Load() != 0 || pdfCalls.Load() != 0 {
			t.Fatalf("cancelled jobs should never execute, got gen=%d pdf=%d", genCalls.Load(), pdfCalls.Load())
		}
		verJob := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, cancelledVer)
		if st := reservationStatus(ctx, t, env.store, env.defaultUser, verJob.ReservationID); st != core.ReservationStatusReleased {
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

		pool := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(20*time.Millisecond),
			worker.WithJobs(env.jobRepo),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithGenerator(llmMock),
			worker.WithValidator(env.validator),
			worker.WithRenderer(env.renderer),
		)
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
		if err := env.branchRepo.SaveCompletedVersion(ctx, v2); err != nil {
			t.Fatalf("SaveCompletedVersion v2 failed: %v", err)
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
			inner: env.profileRepo,
			onProfile: func() {
				released, _ := env.jobRepo.AbortBranchJobs(ctx, env.branchID, "branch deleted")
				for _, r := range released {
					_ = env.quotaRepo.ReleaseQuota(ctx, r.ReservationID, r.Reason, core.LLMTelemetry{})
				}
				_ = env.branchRepo.DeleteBranch(ctx, env.defaultUser, env.branchID)
			},
		}

		pool := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(50*time.Millisecond),
			worker.WithJobs(env.jobRepo),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(deletingProfiles),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithGenerator(llmMock),
			worker.WithValidator(env.validator),
			worker.WithRenderer(env.renderer),
		)
		pool.Start(ctx)
		defer pool.Stop()

		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-deleted-prellm", core.PendingJobOptions{})
		verJob := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, ver)
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
		if st := reservationStatus(ctx, t, env.store, env.defaultUser, verJob.ReservationID); st != core.ReservationStatusReleased {
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
	worker.JobStore
	submitErr error
}

func (f *failingPDFSubmitStore) SubmitPDFParse(_ context.Context, _ *core.Job, _ core.PendingJobOptions) error {
	return f.submitErr
}

type cancelAfterReserveQuota struct {
	inner  worker.PDFQuotaReserver
	cancel context.CancelFunc
}

func (c *cancelAfterReserveQuota) ReserveQuota(ctx context.Context, userID string, op core.LLMOperationType, estimatedTokens, dailyLimit, monthlyLimit int) (*core.LLMUsageRecord, error) {
	rec, err := c.inner.ReserveQuota(ctx, userID, op, estimatedTokens, dailyLimit, monthlyLimit)
	if c.cancel != nil {
		c.cancel()
	}
	return rec, err
}

func (c *cancelAfterReserveQuota) ReleaseQuota(ctx context.Context, reservationID, reason string, telemetry core.LLMTelemetry) error {
	return c.inner.ReleaseQuota(ctx, reservationID, reason, telemetry)
}

// 27. PDF Submission & Cancellation Helpers + Empty RawText Validation
func TestPool_PDFSubmitAndCancelHelpers(t *testing.T) {
	t.Run("SubmitPDFJob validates inputs and notifies worker", func(t *testing.T) {
		st := memory.New()
		ctx := context.Background()

		if _, err := worker.SubmitPDFJob(ctx, st.Jobs(), worker.PDFJobSubmission{
			UserID:  "",
			RawText: "hello",
		}); !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation for empty UserID, got %v", err)
		}

		if _, err := worker.SubmitPDFJob(ctx, st.Jobs(), worker.PDFJobSubmission{
			UserID:  "user-1",
			RawText: "   ",
		}); !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation for blank RawText, got %v", err)
		}

		var notified atomic.Int32
		job, err := worker.SubmitPDFJob(ctx, st.Jobs(), worker.PDFJobSubmission{
			UserID:       "user-1",
			Filename:     "cv.pdf",
			Provider:     "openai",
			Model:        "gpt-4o",
			RawText:      "Resume raw text",
			DailyLimit:   10,
			MonthlyLimit: 50000,
		},
			worker.WithPDFQuotas(st.Quotas()),
			worker.WithPDFNotifier(core.JobNotifierFunc(func() { notified.Add(1) })),
		)
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
		st := memory.New()
		callCtx, cancel := context.WithCancel(context.Background())

		failStore := &failingPDFSubmitStore{
			JobStore:  st.Jobs(),
			submitErr: core.ErrPendingLimitExceeded,
		}
		quotas := &cancelAfterReserveQuota{
			inner:  st.Quotas(),
			cancel: cancel,
		}

		_, err := worker.SubmitPDFJob(callCtx, failStore, worker.PDFJobSubmission{
			UserID:       "user-detached",
			RawText:      "Some resume text",
			DailyLimit:   10,
			MonthlyLimit: 50000,
		},
			worker.WithPDFQuotas(quotas),
			worker.WithPDFCleanupTimeout(2*time.Second),
		)
		if !errors.Is(err, core.ErrPendingLimitExceeded) {
			t.Fatalf("expected ErrPendingLimitExceeded, got %v", err)
		}

		quota, err := st.Quotas().Quota(context.Background(), "user-detached")
		if err != nil {
			t.Fatalf("Quota failed: %v", err)
		}
		if quota.DailyRequestsUsed != 0 || quota.MonthlyTokensUsed != 0 {
			t.Errorf("expected quota fully released despite cancelled ctx, got daily=%d monthly=%d", quota.DailyRequestsUsed, quota.MonthlyTokensUsed)
		}
	})

	t.Run("persisted PDF job with empty RawText fails fast and refunds quota", func(t *testing.T) {
		st := memory.New()
		ctx := context.Background()

		rec, err := st.Quotas().ReserveQuota(ctx, "user-empty-raw", core.OperationPDFParse, 1000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}
		now := time.Now().UTC()
		exp := now.Add(core.DefaultJobTTL)
		if err := st.Jobs().SubmitPDFParse(ctx, &core.Job{
			ID:            "pdf-empty-raw",
			Kind:          core.JobKindPDFParse,
			UserID:        "user-empty-raw",
			Status:        core.JobStatusPending,
			ReservationID: rec.ID,
			ExpiresAt:     &exp,
			PDF: &core.PDFParseSpec{
				RawText: "   ",
			},
			CreatedAt: now,
			UpdatedAt: now,
		}, core.PendingJobOptions{
			TTL: core.DefaultJobTTL,
			Now: now,
		}); err != nil {
			t.Fatalf("SubmitPDFParse failed: %v", err)
		}

		pool := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(20*time.Millisecond),
			worker.WithJobs(st.Jobs()),
			worker.WithQuotas(st.Quotas()),
			worker.WithResumeExtractor(&mockResumeExtractor{}),
		)
		pool.Start(ctx)
		defer pool.Stop()

		failed := waitForPDFStatus(ctx, t, st.Jobs(), "user-empty-raw", "pdf-empty-raw", 2*time.Second, func(j *core.Job) bool {
			return j.Status == core.JobStatusFailed
		})

		if failed.ErrorMessage == "" {
			t.Errorf("expected non-empty validation error for empty RawText")
		}
		if status := reservationStatus(ctx, t, st, "user-empty-raw", rec.ID); status != core.ReservationStatusReleased {
			t.Errorf("expected quota released for empty RawText job, got %s", status)
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

	pool := worker.New(
		worker.WithConcurrency(1),
		worker.WithPollInterval(50*time.Millisecond),
		worker.WithCleanupTimeout(2*time.Second),
		worker.WithJobs(env.jobRepo),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithGenerator(llmMock),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
	)
	pool.Start(ctx)

	vActive := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-timeout-active", core.PendingJobOptions{})
	vActiveJob := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, vActive)
	pool.Notify()

	select {
	case <-inLLM:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for active version to enter LLM")
	}

	vPending := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, b2.ID, "ver-timeout-unclaimed", core.PendingJobOptions{})
	vPendingJob := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, vPending)

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
	if st := reservationStatus(ctx, t, env.store, env.defaultUser, vActiveJob.ReservationID); st != core.ReservationStatusReleased {
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
	if st := reservationStatus(ctx, t, env.store, env.defaultUser, vPendingJob.ReservationID); st != core.ReservationStatusPending {
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

	pool := worker.New(
		worker.WithConcurrency(8),
		worker.WithMaxConcurrentPerTenant(4),
		worker.WithPollInterval(15*time.Millisecond),
		worker.WithSweepInterval(25*time.Millisecond),
		worker.WithBranches(env.branchRepo),
		worker.WithProfiles(env.profileRepo),
		worker.WithPresets(env.presetRepo),
		worker.WithTemplates(env.templateRepo),
		worker.WithJobs(env.jobRepo),
		worker.WithQuotas(env.quotaRepo),
		worker.WithResumeExtractor(ext),
		worker.WithGenerator(llmMock),
		worker.WithValidator(env.validator),
		worker.WithRenderer(env.renderer),
	)
	pool.Start(ctx)
	defer pool.Stop()

	// Create 12 branches with 1 queued version each
	// plus 8 PDF jobs across 4 tenants, while hammering Notify() concurrently.
	const numBranches = 12
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
		vID := fmt.Sprintf("ver-race-%d", bIdx)
		_ = submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, b.ID, vID, core.PendingJobOptions{
			MaxPendingPerTenant: 25,
			MaxGlobalPending:    50,
		})
		allVersions = append(allVersions, branchVer{branchID: b.ID, versionID: vID})
		pool.Notify()
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
		verJob := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, done)
		mu.Lock()
		versionExecs[bv.versionID] = verJob.AttemptCount
		if verJob.AttemptCount != 1 {
			duplicateExec = true
		}
		mu.Unlock()
	}

	for _, pr := range allPDFs {
		done := waitForPDFStatus(ctx, t, env.jobRepo, pr.userID, pr.jobID, 5*time.Second, func(j *core.Job) bool {
			return j.Status.IsTerminal()
		})

		if done.Status != core.JobStatusCompleted {
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

type redactFailingValidator struct {
	err error
}

func (v *redactFailingValidator) Validate(_ *core.TailoredContent, _ *core.MainProfileSnapshot, _ *core.Branch) (*core.ValidationResult, error) {
	return nil, v.err
}

type failingRenderer struct {
	err error
}

func (r *failingRenderer) Render(_ context.Context, _ *core.TemplateSet, _ *core.Profile, _ *core.TailoredContent) (*core.RenderOutput, error) {
	return nil, r.err
}

type completeVersionFailStore struct {
	worker.JobStore
	completeErr error
}

func (s *completeVersionFailStore) CompleteBranchTailor(_ context.Context, _, _ string, _ *core.BranchVersion) error {
	return s.completeErr
}

type completePDFFailStore struct {
	worker.JobStore
	completeErr error
}

func (s *completePDFFailStore) CompletePDFParse(_ context.Context, _, _ string, _ *core.JSONResume) error {
	return s.completeErr
}

type tokenReportingExtractor struct {
	res   *core.JSONResume
	usage core.TokenUsage
	err   error
}

func (e *tokenReportingExtractor) ExtractResume(_ context.Context, _, _, _ string) (*core.JSONResume, core.LLMTelemetry, error) {
	return e.res, core.LLMTelemetry{Usage: e.usage}, e.err
}

type spyQuotaReserver struct {
	inner         worker.PDFQuotaReserver
	reserveCalls  atomic.Int32
	releaseMu     sync.Mutex
	releaseReason string
}

func (s *spyQuotaReserver) ReserveQuota(ctx context.Context, userID string, op core.LLMOperationType, estimatedTokens, dailyLimit, monthlyLimit int) (*core.LLMUsageRecord, error) {
	s.reserveCalls.Add(1)
	return s.inner.ReserveQuota(ctx, userID, op, estimatedTokens, dailyLimit, monthlyLimit)
}

func (s *spyQuotaReserver) ReleaseQuota(ctx context.Context, reservationID, reason string, telemetry core.LLMTelemetry) error {
	s.releaseMu.Lock()
	s.releaseReason = reason
	s.releaseMu.Unlock()
	return s.inner.ReleaseQuota(ctx, reservationID, reason, telemetry)
}

func TestPool_RedactsInternalErrorsAndPersistsPDFQuotaBeforeComplete(t *testing.T) {
	ctx := context.Background()

	t.Run("redacts validator, renderer, and CompleteBranchTailor internal errors", func(t *testing.T) {
		secretErr := "sql: connection reset on internal-db.prod:5432"

		// 1. Validator error redacted
		env1 := setupTestWorkerEnv(t)
		pool1 := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(20*time.Millisecond),
			worker.WithJobs(env1.jobRepo),
			worker.WithBranches(env1.branchRepo),
			worker.WithProfiles(env1.profileRepo),
			worker.WithPresets(env1.presetRepo),
			worker.WithTemplates(env1.templateRepo),
			worker.WithQuotas(env1.quotaRepo),
			worker.WithGenerator(env1.llm),
			worker.WithValidator(&redactFailingValidator{err: errors.New(secretErr)}),
			worker.WithRenderer(env1.renderer),
		)
		pool1.Start(ctx)
		ver1 := submitPendingVersionWithQuota(ctx, t, env1.store, env1.defaultUser, env1.branchID, "ver-val-redact", core.PendingJobOptions{})
		pool1.Notify()
		done1 := waitForVersionStatus(ctx, t, env1.branchRepo, env1.branchID, ver1.ID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusFailed
		})
		pool1.Stop()
		if strings.Contains(done1.ErrorMessage, "internal-db.prod") || strings.Contains(done1.ErrorMessage, "5432") {
			t.Errorf("expected validator internal error redacted, got %q", done1.ErrorMessage)
		}

		// 2. Renderer error redacted
		env2 := setupTestWorkerEnv(t)
		pool2 := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(20*time.Millisecond),
			worker.WithJobs(env2.jobRepo),
			worker.WithBranches(env2.branchRepo),
			worker.WithProfiles(env2.profileRepo),
			worker.WithPresets(env2.presetRepo),
			worker.WithTemplates(env2.templateRepo),
			worker.WithQuotas(env2.quotaRepo),
			worker.WithGenerator(env2.llm),
			worker.WithValidator(env2.validator),
			worker.WithRenderer(&failingRenderer{err: errors.New(secretErr)}),
		)
		pool2.Start(ctx)
		ver2 := submitPendingVersionWithQuota(ctx, t, env2.store, env2.defaultUser, env2.branchID, "ver-rnd-redact", core.PendingJobOptions{})
		pool2.Notify()
		done2 := waitForVersionStatus(ctx, t, env2.branchRepo, env2.branchID, ver2.ID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusFailed
		})
		pool2.Stop()
		if strings.Contains(done2.ErrorMessage, "internal-db.prod") || strings.Contains(done2.ErrorMessage, "5432") {
			t.Errorf("expected renderer internal error redacted, got %q", done2.ErrorMessage)
		}

		// 3. CompleteBranchTailor store error redacted
		env3 := setupTestWorkerEnv(t)
		pool3 := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(20*time.Millisecond),
			worker.WithJobs(&completeVersionFailStore{JobStore: env3.jobRepo, completeErr: errors.New(secretErr)}),
			worker.WithBranches(env3.branchRepo),
			worker.WithProfiles(env3.profileRepo),
			worker.WithPresets(env3.presetRepo),
			worker.WithTemplates(env3.templateRepo),
			worker.WithQuotas(env3.quotaRepo),
			worker.WithGenerator(env3.llm),
			worker.WithValidator(env3.validator),
			worker.WithRenderer(env3.renderer),
		)
		pool3.Start(ctx)
		ver3 := submitPendingVersionWithQuota(ctx, t, env3.store, env3.defaultUser, env3.branchID, "ver-cmp-redact", core.PendingJobOptions{})
		pool3.Notify()
		done3 := waitForVersionStatus(ctx, t, env3.branchRepo, env3.branchID, ver3.ID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusFailed
		})
		pool3.Stop()
		if strings.Contains(done3.ErrorMessage, "internal-db.prod") || strings.Contains(done3.ErrorMessage, "5432") {
			t.Errorf("expected CompleteBranchTailor internal error redacted, got %q", done3.ErrorMessage)
		}
	})

	t.Run("redacts ErrValidation details in PDF extraction and commits quota even if CompletePDFParse fails", func(t *testing.T) {
		st1 := memory.New()
		pool1 := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(20*time.Millisecond),
			worker.WithJobs(st1.Jobs()),
			worker.WithQuotas(st1.Quotas()),
			worker.WithResumeExtractor(&tokenReportingExtractor{
				err: fmt.Errorf("%w: parse table overflow at internal offset 0xdeadbeef", core.ErrValidation),
			}),
		)
		pool1.Start(ctx)
		pj1 := submitPendingPDFWithQuota(ctx, t, st1, pool1, "user-pdf-val", "Resume content", "mock", "m", core.DefaultJobTTL, nil)
		done1 := waitForPDFStatus(ctx, t, st1.Jobs(), "user-pdf-val", pj1.ID, 2*time.Second, func(j *core.Job) bool {
			return j.Status == core.JobStatusFailed
		})
		pool1.Stop()
		if strings.Contains(done1.ErrorMessage, "0xdeadbeef") {
			t.Errorf("expected validation internal details redacted, got %q", done1.ErrorMessage)
		}

		// Now test extraction succeeding + CompletePDFParse failing -> commits quota (never refunds) & redacts store error.
		st2 := memory.New()
		wrappedPDF := &completePDFFailStore{
			JobStore:    st2.Jobs(),
			completeErr: errors.New("pq: deadlock detected on relation pdf_jobs_prod"),
		}
		pool2 := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(20*time.Millisecond),
			worker.WithJobs(wrappedPDF),
			worker.WithQuotas(st2.Quotas()),
			worker.WithResumeExtractor(&tokenReportingExtractor{
				res:   &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Ada"}},
				usage: core.TokenUsage{TotalTokens: 777},
			}),
		)
		pool2.Start(ctx)
		pj2 := submitPendingPDFWithQuota(ctx, t, st2, pool2, "user-pdf-cmp", "Resume content", "mock", "m", core.DefaultJobTTL, nil)
		done2 := waitForPDFStatus(ctx, t, st2.Jobs(), "user-pdf-cmp", pj2.ID, 2*time.Second, func(j *core.Job) bool {
			return j.Status == core.JobStatusFailed
		})
		pool2.Stop()

		if strings.Contains(done2.ErrorMessage, "deadlock") || strings.Contains(done2.ErrorMessage, "pdf_jobs_prod") {
			t.Errorf("expected CompletePDFParse internal error redacted, got %q", done2.ErrorMessage)
		}
		if status := reservationStatus(ctx, t, st2, "user-pdf-cmp", pj2.ReservationID); status != core.ReservationStatusCommitted {
			t.Errorf("expected quota committed right after extraction despite CompletePDFParse failure, got %s", status)
		}
		q, err := st2.Quotas().Quota(ctx, "user-pdf-cmp")
		if err != nil {
			t.Fatalf("Quota failed: %v", err)
		}
		if q.MonthlyTokensUsed != 777 {
			t.Errorf("expected MonthlyTokensUsed=777 committed after extraction, got %d", q.MonthlyTokensUsed)
		}
	})

	t.Run("sanitizes SubmitPDFJob ReleaseQuota reason on store failure", func(t *testing.T) {
		st := memory.New()
		spyQuota := &spyQuotaReserver{inner: st.Quotas()}
		failStore := &failingPDFSubmitStore{
			JobStore:  st.Jobs(),
			submitErr: errors.New("postgres://admin:pass@db.internal:5432/resumeee connection refused"),
		}
		_, err := worker.SubmitPDFJob(ctx, failStore, worker.PDFJobSubmission{
			UserID:       "user-sanitize-reason",
			RawText:      "Resume raw text",
			DailyLimit:   10,
			MonthlyLimit: 50000,
		},
			worker.WithPDFQuotas(spyQuota),
		)
		if err == nil {
			t.Fatal("expected SubmitPDFJob error")
		}
		spyQuota.releaseMu.Lock()
		reason := spyQuota.releaseReason
		spyQuota.releaseMu.Unlock()
		if reason == "" || strings.Contains(reason, "postgres://") || strings.Contains(reason, "db.internal") {
			t.Errorf("expected sanitized ReleaseQuota reason, got %q", reason)
		}
	})
}

type leaseErrJobStore struct {
	worker.JobStore
	renewErr      error
	requeueErr    error
	failErr       error
	renewNotified chan struct{}
	onceRenew     sync.Once
	requeueCalled chan struct{}
	onceRequeue   sync.Once
	failCalls     atomic.Int32
}

func (s *leaseErrJobStore) RenewLease(ctx context.Context, jobID, claimedBy string, leaseExpiresAt time.Time) error {
	s.onceRenew.Do(func() {
		if s.renewNotified != nil {
			close(s.renewNotified)
		}
	})
	if s.renewErr != nil {
		return s.renewErr
	}
	return s.JobStore.RenewLease(ctx, jobID, claimedBy, leaseExpiresAt)
}

func (s *leaseErrJobStore) RequeueForRetry(ctx context.Context, jobID, claimedBy string, nextRetryAt time.Time, errMsg string) error {
	defer s.onceRequeue.Do(func() {
		if s.requeueCalled != nil {
			close(s.requeueCalled)
		}
	})
	if s.requeueErr != nil {
		return s.requeueErr
	}
	return s.JobStore.RequeueForRetry(ctx, jobID, claimedBy, nextRetryAt, errMsg)
}

func (s *leaseErrJobStore) FailClaimed(ctx context.Context, jobID, claimedBy, errMsg string) error {
	s.failCalls.Add(1)
	if s.failErr != nil {
		return s.failErr
	}
	return s.JobStore.FailClaimed(ctx, jobID, claimedBy, errMsg)
}

func TestPool_ErrLeaseExpired_HeartbeatAndRequeueSkip(t *testing.T) {
	ctx := context.Background()

	t.Run("heartbeat ErrLeaseExpired cancels version attempt context and suppresses stale fail refund", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		leaseStore := &leaseErrJobStore{
			JobStore:      env.jobRepo,
			renewErr:      core.ErrLeaseExpired,
			failErr:       core.ErrLeaseExpired,
			renewNotified: make(chan struct{}),
		}
		attemptCancelled := make(chan struct{})
		pool := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(30*time.Millisecond),
			worker.WithLeaseDuration(60*time.Millisecond),
			worker.WithLeaseRenewInterval(15*time.Millisecond),
			worker.WithJobs(leaseStore),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithValidator(env.validator),
			worker.WithRenderer(env.renderer),
			worker.WithGenerator(&callbackLLM{
				fn: func(c context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
					<-c.Done()
					close(attemptCancelled)
					return nil, c.Err()
				},
			}),
		)
		pool.Start(ctx)
		defer pool.Stop()

		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-lease-exp-hb", core.PendingJobOptions{})
		verJob := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, ver)
		pool.Notify()

		select {
		case <-attemptCancelled:
		case <-time.After(2 * time.Second):
			t.Fatal("expected heartbeat ErrLeaseExpired to cancel version attempt context")
		}
		pool.Stop()

		if st := reservationStatus(ctx, t, env.store, env.defaultUser, verJob.ReservationID); st != core.ReservationStatusPending {
			t.Errorf("expected reservation untouched (pending) when FailClaimed returns ErrLeaseExpired, got %s", st)
		}
	})

	t.Run("heartbeat ErrLeaseExpired cancels pdf attempt context", func(t *testing.T) {
		st := memory.New()
		leaseStore := &leaseErrJobStore{
			JobStore:      st.Jobs(),
			renewErr:      core.ErrLeaseExpired,
			failErr:       core.ErrLeaseExpired,
			renewNotified: make(chan struct{}),
		}
		attemptCancelled := make(chan struct{})
		pool := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(30*time.Millisecond),
			worker.WithLeaseDuration(60*time.Millisecond),
			worker.WithLeaseRenewInterval(15*time.Millisecond),
			worker.WithJobs(leaseStore),
			worker.WithQuotas(st.Quotas()),
			worker.WithResumeExtractor(&callbackResumeExtractor{
				fn: func(c context.Context, _, _, _ string) (*core.JSONResume, error) {
					<-c.Done()
					close(attemptCancelled)
					return nil, c.Err()
				},
			}),
		)
		pool.Start(ctx)
		defer pool.Stop()

		pj := submitPendingPDFWithQuota(ctx, t, st, pool, "user-pdf-hb-exp", "Resume text", "mock", "m", core.DefaultJobTTL, nil)

		select {
		case <-attemptCancelled:
		case <-time.After(2 * time.Second):
			t.Fatal("expected heartbeat ErrLeaseExpired to cancel pdf attempt context")
		}
		pool.Stop()

		if status := reservationStatus(ctx, t, st, "user-pdf-hb-exp", pj.ReservationID); status != core.ReservationStatusPending {
			t.Errorf("expected reservation untouched when FailClaimed returns ErrLeaseExpired, got %s", status)
		}
	})

	t.Run("RequeueForRetry returning ErrLeaseExpired skips failure and quota refund", func(t *testing.T) {
		env := setupTestWorkerEnv(t)
		verStore := &leaseErrJobStore{
			JobStore:      env.jobRepo,
			requeueErr:    core.ErrLeaseExpired,
			requeueCalled: make(chan struct{}),
		}
		var verCalls atomic.Int32
		poolVer := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(30*time.Millisecond),
			worker.WithMaxAttempts(3),
			worker.WithRetryBaseDelay(20*time.Millisecond),
			worker.WithJobs(verStore),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithValidator(env.validator),
			worker.WithRenderer(env.renderer),
			worker.WithGenerator(&callbackLLM{
				fn: func(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
					verCalls.Add(1)
					return nil, core.ErrTransientFailure
				},
			}),
		)
		poolVer.Start(ctx)
		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-req-exp", core.PendingJobOptions{})
		verJob := jobForVersion(ctx, t, env.jobRepo, env.defaultUser, ver)
		poolVer.Notify()
		select {
		case <-verStore.requeueCalled:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for RequeueForRetry")
		}
		poolVer.Stop()

		if verCalls.Load() != 1 {
			t.Errorf("expected 1 version attempt before lease loss on requeue, got %d", verCalls.Load())
		}
		if verStore.failCalls.Load() != 0 {
			t.Errorf("expected FailClaimed not called when RequeueForRetry returns ErrLeaseExpired, got %d", verStore.failCalls.Load())
		}
		if st := reservationStatus(ctx, t, env.store, env.defaultUser, verJob.ReservationID); st != core.ReservationStatusPending {
			t.Errorf("expected version reservation untouched on requeue lease loss, got %s", st)
		}

		pdfStore := memory.New()
		wrappedPDF := &leaseErrJobStore{
			JobStore:      pdfStore.Jobs(),
			requeueErr:    core.ErrLeaseExpired,
			requeueCalled: make(chan struct{}),
		}
		var pdfCalls atomic.Int32
		poolPDF := worker.New(
			worker.WithConcurrency(1),
			worker.WithPollInterval(30*time.Millisecond),
			worker.WithMaxAttempts(3),
			worker.WithRetryBaseDelay(20*time.Millisecond),
			worker.WithJobs(wrappedPDF),
			worker.WithQuotas(pdfStore.Quotas()),
			worker.WithResumeExtractor(&callbackResumeExtractor{
				fn: func(_ context.Context, _, _, _ string) (*core.JSONResume, error) {
					pdfCalls.Add(1)
					return nil, core.ErrTransientFailure
				},
			}),
		)
		poolPDF.Start(ctx)
		pj := submitPendingPDFWithQuota(ctx, t, pdfStore, poolPDF, "user-pdf-req-exp", "Resume text", "mock", "m", core.DefaultJobTTL, nil)
		select {
		case <-wrappedPDF.requeueCalled:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for RequeueForRetry")
		}
		poolPDF.Stop()

		if pdfCalls.Load() != 1 {
			t.Errorf("expected 1 pdf attempt before lease loss on requeue, got %d", pdfCalls.Load())
		}
		if wrappedPDF.failCalls.Load() != 0 {
			t.Errorf("expected FailClaimed not called when RequeueForRetry returns ErrLeaseExpired, got %d", wrappedPDF.failCalls.Load())
		}
		if st := reservationStatus(ctx, t, pdfStore, "user-pdf-req-exp", pj.ReservationID); st != core.ReservationStatusPending {
			t.Errorf("expected pdf reservation untouched on requeue lease loss, got %s", st)
		}
	})
}

func TestPool_SharedStoreMultiPoolConcurrency(t *testing.T) {
	ctx := context.Background()

	t.Run("two pools sharing one job store enforce tenant cap across workloads", func(t *testing.T) {
		env := setupTestWorkerEnv(t)

		var (
			mu      sync.Mutex
			active  int
			peak    int
			started = make(chan struct{}, 2)
		)
		release := make(chan struct{})

		gen := &callbackLLM{
			fn: func(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
				mu.Lock()
				active++
				if active > peak {
					peak = active
				}
				mu.Unlock()
				started <- struct{}{}
				<-release
				mu.Lock()
				active--
				mu.Unlock()
				return &core.GenerationResponse{TailoredContent: core.TailoredContent{Summary: "OK"}}, nil
			},
		}
		ext := &callbackResumeExtractor{
			fn: func(_ context.Context, _, _, _ string) (*core.JSONResume, error) {
				mu.Lock()
				active++
				if active > peak {
					peak = active
				}
				mu.Unlock()
				started <- struct{}{}
				<-release
				mu.Lock()
				active--
				mu.Unlock()
				return &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Shared"}}, nil
			},
		}

		poolA := worker.New(
			worker.WithConcurrency(2),
			worker.WithMaxConcurrentPerTenant(1),
			worker.WithPollInterval(15*time.Millisecond),
			worker.WithWorkerID("pool-shared-a"),
			worker.WithJobs(env.jobRepo),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithValidator(env.validator),
			worker.WithRenderer(env.renderer),
			worker.WithGenerator(gen),
			worker.WithResumeExtractor(ext),
		)
		poolB := worker.New(
			worker.WithConcurrency(2),
			worker.WithMaxConcurrentPerTenant(1),
			worker.WithPollInterval(15*time.Millisecond),
			worker.WithWorkerID("pool-shared-b"),
			worker.WithJobs(env.jobRepo),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithValidator(env.validator),
			worker.WithRenderer(env.renderer),
			worker.WithGenerator(gen),
			worker.WithResumeExtractor(ext),
		)

		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-shared-pool", core.PendingJobOptions{})
		pj := submitPendingPDFWithQuota(ctx, t, env.store, nil, env.defaultUser, "Shared PDF text", "mock", "m", core.DefaultJobTTL, nil)

		poolA.Start(ctx)
		poolB.Start(ctx)
		poolA.Notify()
		poolB.Notify()

		<-started
		// Wait long enough for several poll ticks across both pools while the first job is held open.
		time.Sleep(80 * time.Millisecond)

		mu.Lock()
		duringFirstPeak := peak
		mu.Unlock()
		if duringFirstPeak != 1 {
			close(release)
			poolA.Stop()
			poolB.Stop()
			t.Fatalf("expected mutual exclusion across shared-store pools (peak=1), got %d", duringFirstPeak)
		}

		close(release)
		waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusCompleted
		})
		waitForPDFStatus(ctx, t, env.jobRepo, env.defaultUser, pj.ID, 2*time.Second, func(j *core.Job) bool {
			return j.Status == core.JobStatusCompleted
		})
		poolA.Stop()
		poolB.Stop()
	})

	t.Run("pre-existing unexpired processing lease in shared store blocks pool claim", func(t *testing.T) {
		clk := newFakeClock(time.Date(2026, 11, 15, 17, 0, 0, 0, time.UTC))
		env := setupTestWorkerEnvWithClock(t, clk.Now)
		now := clk.Now()

		pj := submitPendingPDFWithQuota(ctx, t, env.store, nil, env.defaultUser, "Held by external worker", "mock", "m", 10*time.Minute, clk.Now)
		claimed, err := env.jobRepo.ClaimNext(ctx, core.ClaimJobRequest{
			WorkerID:               "external-worker",
			LeaseDuration:          5 * time.Minute,
			MaxConcurrentPerTenant: 1,
			Now:                    now,
		})
		if err != nil || claimed == nil || claimed.ID != pj.ID {
			t.Fatalf("ClaimNext external lease failed: claimed=%v err=%v", claimed, err)
		}

		ver := submitPendingVersionWithQuota(ctx, t, env.store, env.defaultUser, env.branchID, "ver-blocked-by-sibling", core.PendingJobOptions{
			Now: now,
		})

		pool := worker.New(
			worker.WithConcurrency(2),
			worker.WithMaxConcurrentPerTenant(1),
			worker.WithPollInterval(15*time.Millisecond),
			worker.WithClock(clk.Now),
			worker.WithJobs(env.jobRepo),
			worker.WithBranches(env.branchRepo),
			worker.WithProfiles(env.profileRepo),
			worker.WithPresets(env.presetRepo),
			worker.WithTemplates(env.templateRepo),
			worker.WithQuotas(env.quotaRepo),
			worker.WithValidator(env.validator),
			worker.WithRenderer(env.renderer),
			worker.WithGenerator(env.llm),
		)
		pool.Start(ctx)
		defer pool.Stop()
		pool.Notify()

		time.Sleep(80 * time.Millisecond)
		loaded, err := env.branchRepo.Version(ctx, env.branchID, ver.ID)
		if err != nil {
			t.Fatalf("Version failed: %v", err)
		}
		if loaded.Status != core.VersionStatusPending {
			t.Fatalf("expected version to stay pending while sibling lease is active, got %s", loaded.Status)
		}

		// Complete external lease and verify pending version now runs.
		if err := env.jobRepo.CompletePDFParse(ctx, claimed.ID, "external-worker", &core.JSONResume{}); err != nil {
			t.Fatalf("CompletePDFParse failed: %v", err)
		}
		if pj.ReservationID != "" {
			_ = env.quotaRepo.CommitUsage(ctx, pj.ReservationID, core.LLMTelemetry{})
		}
		pool.Notify()

		done := waitForVersionStatus(ctx, t, env.branchRepo, env.branchID, ver.ID, 2*time.Second, func(v *core.BranchVersion) bool {
			return v.Status == core.VersionStatusCompleted
		})
		if done.Status != core.VersionStatusCompleted {
			t.Fatalf("expected version completed after sibling lease released, got %s", done.Status)
		}
	})
}

func TestPool_PDFSubmitMaxRawTextBytes(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects payload above default DefaultMaxPDFRawTextBytes before reserving quota", func(t *testing.T) {
		st := memory.New()
		spyQuota := &spyQuotaReserver{inner: st.Quotas()}
		oversized := strings.Repeat("a", core.DefaultMaxPDFRawTextBytes+1)

		_, err := worker.SubmitPDFJob(ctx, st.Jobs(), worker.PDFJobSubmission{
			UserID:       "user-raw-default-max",
			RawText:      oversized,
			DailyLimit:   10,
			MonthlyLimit: 100000,
		},
			worker.WithPDFQuotas(spyQuota),
		)
		if !errors.Is(err, core.ErrValidation) {
			t.Fatalf("expected ErrValidation for oversized RawText, got %v", err)
		}
		if spyQuota.reserveCalls.Load() != 0 {
			t.Errorf("expected ReserveQuota not called on RawText validation failure, got %d", spyQuota.reserveCalls.Load())
		}
	})

	t.Run("enforces custom MaxRawTextBytes limit and allows exact cap", func(t *testing.T) {
		st := memory.New()
		spyQuota := &spyQuotaReserver{inner: st.Quotas()}

		_, err := worker.SubmitPDFJob(ctx, st.Jobs(), worker.PDFJobSubmission{
			UserID:       "user-raw-custom-max",
			RawText:      strings.Repeat("x", 33),
			DailyLimit:   10,
			MonthlyLimit: 100000,
		},
			worker.WithPDFQuotas(spyQuota),
			worker.WithPDFMaxRawTextBytes(32),
		)
		if !errors.Is(err, core.ErrValidation) {
			t.Fatalf("expected ErrValidation for RawText > custom MaxRawTextBytes, got %v", err)
		}
		if spyQuota.reserveCalls.Load() != 0 {
			t.Errorf("expected ReserveQuota skipped on oversized payload, got %d calls", spyQuota.reserveCalls.Load())
		}

		job, err := worker.SubmitPDFJob(ctx, st.Jobs(), worker.PDFJobSubmission{
			UserID:       "user-raw-custom-max",
			RawText:      strings.Repeat("x", 32),
			DailyLimit:   10,
			MonthlyLimit: 100000,
		},
			worker.WithPDFQuotas(spyQuota),
			worker.WithPDFMaxRawTextBytes(32),
		)
		if err != nil {
			t.Fatalf("expected exact MaxRawTextBytes payload accepted, got %v", err)
		}
		if job.PDF == nil || len(job.PDF.RawText) != 32 {
			t.Errorf("expected persisted RawText length 32, got %+v", job.PDF)
		}
	})
}
