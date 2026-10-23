package branch_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/google/uuid"

	"resumeee/internal/branch"
	"resumeee/internal/domain"
	"resumeee/internal/renderer"
	"resumeee/internal/repository/sqlite"
	"resumeee/internal/validator"
	"resumeee/internal/worker"
)

type stubEnqueuer struct {
	mu       sync.Mutex
	capacity int
	jobs     []worker.GenerationJob
	closed   bool
}

func newStubEnqueuer(capacity int) *stubEnqueuer {
	return &stubEnqueuer{capacity: capacity}
}

func (e *stubEnqueuer) Enqueue(job worker.GenerationJob) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed || (e.capacity > 0 && len(e.jobs) >= e.capacity) {
		return domain.ErrQueueFull
	}
	e.jobs = append(e.jobs, job)
	return nil
}

type testBranchEnv struct {
	db          any
	svc         branch.Service
	branchRepo  domain.BranchRepository
	profileRepo domain.ProfileRepository
	enqueuer    *stubEnqueuer
	defaultUser string
	branchID    string
	v1ID        string
	validFactID string
	validExpID  string
	validSkillID string
}

func setupTestBranchEnv(t *testing.T, enqueuerCap int) *testBranchEnv {
	t.Helper()

	db, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("failed to init sqlite: %v", err)
	}

	ctx := context.Background()
	defaultUser := "default"
	if err := sqlite.SeedDefaultData(ctx, db, defaultUser); err != nil {
		t.Fatalf("failed to seed: %v", err)
	}

	profileRepo := sqlite.NewProfileRepository(db)
	presetRepo := sqlite.NewPresetRepository(db)
	templateRepo := sqlite.NewTemplateRepository(db)
	branchRepo := sqlite.NewBranchRepository(db)
	val := validator.New()
	rend := renderer.NewTemplateRenderer()
	enqueuer := newStubEnqueuer(enqueuerCap)

	svc := branch.NewService(branch.Config{
		BranchRepo:   branchRepo,
		ProfileRepo:  profileRepo,
		PresetRepo:   presetRepo,
		TemplateRepo: templateRepo,
		JobEnqueuer:  enqueuer,
		Validator:    val,
		Renderer:     rend,
	})

	b := &domain.Branch{
		ID:            "branch-" + uuid.New().String(),
		UserID:        defaultUser,
		Name:          "Senior Cloud Architect",
		TemplateSetID: renderer.TemplateStandardCleanID,
	}
	created, err := svc.CreateBranch(ctx, b)
	if err != nil {
		t.Fatalf("failed to create branch: %v", err)
	}

	// Create initial v1
	v1 := &domain.BranchVersion{
		ID:                 "ver-init-1",
		BranchID:           created.ID,
		Status:             domain.VersionStatusCompleted,
		MasterRevisionUsed: 1,
		TemplateSetIDUsed:  renderer.TemplateStandardCleanID,
		SourceType:         domain.SourceTypeLLMGeneration,
	}
	if err := branchRepo.CreateVersion(ctx, v1); err != nil {
		t.Fatalf("failed to create v1: %v", err)
	}
	if err := branchRepo.CompleteVersion(ctx, v1); err != nil {
		t.Fatalf("failed to complete v1: %v", err)
	}

	// Seed an experience and fact so manual edits have valid provenance
	exp := &domain.WorkExperience{
		ID:        "exp-test-1",
		UserID:    defaultUser,
		Company:   "Acme Corp",
		RoleTitle: "Lead Systems Architect",
		StartDate: "2020-01-01",
	}
	if err := profileRepo.CreateExperience(ctx, exp); err != nil {
		t.Fatalf("failed to create experience: %v", err)
	}

	fact := &domain.Fact{
		ID:        "fact-test-1",
		UserID:    defaultUser,
		OwnerID:   exp.ID,
		OwnerType: domain.OwnerTypeWorkExperience,
		Statement: "Optimized distributed consensus throughput by 40%.",
	}
	if err := profileRepo.CreateFact(ctx, fact); err != nil {
		t.Fatalf("failed to create fact: %v", err)
	}

	snap, err := profileRepo.GetSnapshot(ctx, defaultUser)
	if err != nil {
		t.Fatalf("failed to get snapshot: %v", err)
	}

	var factID, expID, skillID string
	if len(snap.Skills) > 0 {
		skillID = snap.Skills[0].ID
	}
	if len(snap.Experiences) > 0 {
		expID = snap.Experiences[0].ID
		if len(snap.Experiences[0].Facts) > 0 {
			factID = snap.Experiences[0].Facts[0].ID
		}
	}
	if factID == "" {
		factID = fact.ID
	}
	if expID == "" {
		expID = exp.ID
	}

	return &testBranchEnv{
		db:           db,
		svc:          svc,
		branchRepo:   branchRepo,
		profileRepo:  profileRepo,
		enqueuer:     enqueuer,
		defaultUser:  defaultUser,
		branchID:     created.ID,
		v1ID:         v1.ID,
		validFactID:  factID,
		validExpID:   expID,
		validSkillID: skillID,
	}
}

// 1. Concurrent Manual Edit Versioning (Testing race safety and sequential vN+1 ordering)
func TestService_ConcurrentManualEditVersioning(t *testing.T) {
	env := setupTestBranchEnv(t, 100)
	ctx := context.Background()

	const concurrency = 8
	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)
	results := make([]*domain.BranchVersion, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			content := domain.TailoredContent{
				Summary: fmt.Sprintf("Summary updated by thread %d", idx),
				Experiences: []domain.TailoredExperience{
					{
						SourceEntityID: env.validExpID,
						Company:        "Acme Corp",
						RoleTitle:      "Engineer",
						StartDate:      "2021-01-01",
						Bullets: []domain.TailoredBullet{
							{
								Text:          fmt.Sprintf("Bullet point %d", idx),
								SourceFactIDs: []string{env.validFactID},
							},
						},
					},
				},
			}

			ver, err := env.svc.EditVersion(ctx, env.defaultUser, env.branchID, "1", content)
			if err != nil {
				errCh <- fmt.Errorf("thread %d edit failed: %w", idx, err)
				return
			}
			results[idx] = ver
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent edit error: %v", err)
	}

	// Fetch all versions for branch
	versions, err := env.svc.ListVersions(ctx, env.branchID)
	if err != nil {
		t.Fatalf("failed to list versions: %v", err)
	}

	// We expect 1 initial version + 8 edited versions = 9 total versions
	expectedTotal := concurrency + 1
	if len(versions) != expectedTotal {
		t.Fatalf("expected %d total versions, got %d", expectedTotal, len(versions))
	}

	// Verify strict sequential version numbers: 1, 2, 3, 4, 5, 6, 7, 8, 9 with 0 duplicates
	nums := make([]int, len(versions))
	for i, v := range versions {
		nums[i] = v.VersionNumber
	}
	sort.Ints(nums)

	for i := 0; i < len(nums); i++ {
		expectedNum := i + 1
		if nums[i] != expectedNum {
			t.Errorf("version index %d has version_number %d, expected %d (numbers: %v)", i, nums[i], expectedNum, nums)
		}
	}
}

// 2. Provenance Error on Manual Edit
func TestService_EditVersionProvenanceFailure(t *testing.T) {
	env := setupTestBranchEnv(t, 10)
	ctx := context.Background()

	// Content citing a non-existent hallucinated fact ID
	badContent := domain.TailoredContent{
		Summary: "Fake accomplishments",
		Experiences: []domain.TailoredExperience{
			{
				SourceEntityID: env.validExpID,
				Company:        "Acme Corp",
				RoleTitle:      "Engineer",
				StartDate:      "2021-01-01",
				Bullets: []domain.TailoredBullet{
					{
						Text:          "Claiming ungrounded achievement",
						SourceFactIDs: []string{"fabricated-fact-id-12345"},
					},
				},
			},
		},
	}

	_, err := env.svc.EditVersion(ctx, env.defaultUser, env.branchID, env.v1ID, badContent)
	if err == nil {
		t.Fatalf("expected provenance error, got nil")
	}
	if !errors.Is(err, domain.ErrProvenance) {
		t.Fatalf("expected errors.Is(err, domain.ErrProvenance), got %v", err)
	}
}

// 3. Batch Sync and Backpressure Saturation
func TestService_BatchSyncAndBackpressure(t *testing.T) {
	env := setupTestBranchEnv(t, 1) // Capacity of 1 to test backpressure saturation
	ctx := context.Background()

	// Create a second branch
	b2 := &domain.Branch{
		ID:            "branch-2-" + uuid.New().String(),
		UserID:        env.defaultUser,
		Name:          "Frontend Architect",
		TemplateSetID: renderer.TemplateStandardCleanID,
	}
	_, err := env.svc.CreateBranch(ctx, b2)
	if err != nil {
		t.Fatalf("failed to create branch 2: %v", err)
	}

	// Increment master revision to make branches stale
	_, err = env.profileRepo.IncrementMasterRevision(ctx, env.defaultUser)
	if err != nil {
		t.Fatalf("failed to increment master revision: %v", err)
	}

	// Verify staleness
	staleBranches, err := env.svc.ListBranches(ctx, env.defaultUser, true)
	if err != nil {
		t.Fatalf("failed to list stale branches: %v", err)
	}
	if len(staleBranches) < 2 {
		t.Fatalf("expected at least 2 stale branches, got %d", len(staleBranches))
	}

	// Batch sync all stale branches: queue capacity is 1, so 1 should enqueue and 2nd hit ErrQueueFull
	enqueuedIDs, err := env.svc.SyncBranches(ctx, env.defaultUser, []string{env.branchID, b2.ID})
	if !errors.Is(err, domain.ErrQueueFull) {
		t.Fatalf("expected domain.ErrQueueFull on saturated batch sync, got %v", err)
	}
	if len(enqueuedIDs) != 1 {
		t.Fatalf("expected exactly 1 enqueued version ID before saturation, got %d", len(enqueuedIDs))
	}
}

// 4. GenerateVersion Async Enqueuing
func TestService_GenerateVersion(t *testing.T) {
	env := setupTestBranchEnv(t, 10)
	ctx := context.Background()

	ver, err := env.svc.GenerateVersion(ctx, env.defaultUser, env.branchID)
	if err != nil {
		t.Fatalf("GenerateVersion failed: %v", err)
	}

	if ver.Status != domain.VersionStatusPending {
		t.Errorf("expected pending status, got %s", ver.Status)
	}
	if ver.VersionNumber != 2 {
		t.Errorf("expected version number 2 (after initial v1), got %d", ver.VersionNumber)
	}
	if len(env.enqueuer.jobs) != 1 {
		t.Errorf("expected 1 enqueued job, got %d", len(env.enqueuer.jobs))
	}
	if env.enqueuer.jobs[0].VersionID != ver.ID {
		t.Errorf("expected enqueued version ID %s, got %s", ver.ID, env.enqueuer.jobs[0].VersionID)
	}
}
