package branch_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"resumeee/internal/branch"
	"resumeee/internal/domain"
	"resumeee/internal/privacy"
	"resumeee/internal/renderer"
	"resumeee/internal/repository/memory"
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

	ctx := context.Background()
	defaultUser := "default"
	store, err := memory.NewSeeded(ctx, defaultUser)
	if err != nil {
		t.Fatalf("failed to seed: %v", err)
	}

	profileRepo := store
	presetRepo := store
	templateRepo := store
	branchRepo := store
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
		StartDate: domain.NewDate(2020, 1, 1),
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
		db:           store,
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
						StartDate:      domain.NewDate(2021, 1, 1),
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
				StartDate:      domain.NewDate(2021, 1, 1),
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

// 5. Rollback/Promotion updates LastSyncedMasterRevision and reflects staleness
func TestService_UpdateBranchActiveVersionRollbackStaleness(t *testing.T) {
	env := setupTestBranchEnv(t, 10)
	ctx := context.Background()

	// Obtain current profile revision as rev1
	prof, err := env.profileRepo.GetProfile(ctx, env.defaultUser)
	if err != nil {
		t.Fatalf("failed to get profile: %v", err)
	}
	rev1 := prof.MasterRevision

	// Update initial v1 to have MasterRevisionUsed = rev1
	v1, err := env.branchRepo.GetVersion(ctx, env.branchID, env.v1ID)
	if err != nil {
		t.Fatalf("failed to get v1: %v", err)
	}
	v1.MasterRevisionUsed = rev1
	if err := env.branchRepo.CompleteVersion(ctx, v1); err != nil {
		t.Fatalf("failed to complete v1: %v", err)
	}

	// Advance profile master revision to rev2
	rev2, err := env.profileRepo.IncrementMasterRevision(ctx, env.defaultUser)
	if err != nil {
		t.Fatalf("failed to increment master revision: %v", err)
	}

	// Create and complete v2 at MasterRevisionUsed = rev2
	v2 := &domain.BranchVersion{
		ID:                 "ver-v2-test",
		BranchID:           env.branchID,
		Status:             domain.VersionStatusCompleted,
		MasterRevisionUsed: rev2,
		TemplateSetIDUsed:  renderer.TemplateStandardCleanID,
		SourceType:         domain.SourceTypeLLMGeneration,
	}
	if err := env.branchRepo.CreateVersion(ctx, v2); err != nil {
		t.Fatalf("failed to create v2: %v", err)
	}
	if err := env.branchRepo.CompleteVersion(ctx, v2); err != nil {
		t.Fatalf("failed to complete v2: %v", err)
	}

	// Fetch branch - should be active on v2 and not stale (rev2 == profile rev2)
	b, err := env.svc.GetBranch(ctx, env.defaultUser, env.branchID)
	if err != nil {
		t.Fatalf("failed to get branch: %v", err)
	}
	if b.ActiveVersionID == nil || *b.ActiveVersionID != v2.ID {
		t.Fatalf("expected ActiveVersionID=%s, got %v", v2.ID, b.ActiveVersionID)
	}
	if b.IsStale {
		t.Fatalf("expected branch not stale when active on v2 (rev %d), but got is_stale=true", rev2)
	}

	// Rollback active version to v1 (which had MasterRevisionUsed = rev1)
	b.ActiveVersionID = &env.v1ID
	updated, err := env.svc.UpdateBranch(ctx, b)
	if err != nil {
		t.Fatalf("UpdateBranch rollback failed: %v", err)
	}

	if updated.LastSyncedMasterRevision != rev1 {
		t.Errorf("expected LastSyncedMasterRevision to be rolled back to %d, got %d", rev1, updated.LastSyncedMasterRevision)
	}
	if !updated.IsStale {
		t.Errorf("expected branch to be marked stale after rolling back to rev %d (profile is rev %d)", rev1, rev2)
	}

	// Verify persistence: fetching branch anew reports IsStale = true
	refetched, err := env.svc.GetBranch(ctx, env.defaultUser, env.branchID)
	if err != nil {
		t.Fatalf("failed to refetch branch: %v", err)
	}
	if refetched.LastSyncedMasterRevision != rev1 {
		t.Errorf("persisted LastSyncedMasterRevision expected %d, got %d", rev1, refetched.LastSyncedMasterRevision)
	}
	if !refetched.IsStale {
		t.Errorf("persisted branch expected IsStale=true, got false")
	}
}

// 6. Foreign, non-existent, or incomplete ActiveVersionID must be rejected
func TestService_UpdateBranchForeignAndInvalidVersionProtection(t *testing.T) {
	env := setupTestBranchEnv(t, 10)
	ctx := context.Background()

	// Create Branch 2 with version vOther
	b2 := &domain.Branch{
		ID:            "branch-other-" + uuid.New().String(),
		UserID:        env.defaultUser,
		Name:          "Other Branch",
		TemplateSetID: renderer.TemplateStandardCleanID,
	}
	created2, err := env.svc.CreateBranch(ctx, b2)
	if err != nil {
		t.Fatalf("failed to create branch 2: %v", err)
	}

	vOther := &domain.BranchVersion{
		ID:                 "ver-other-branch",
		BranchID:           created2.ID,
		Status:             domain.VersionStatusCompleted,
		MasterRevisionUsed: 1,
		TemplateSetIDUsed:  renderer.TemplateStandardCleanID,
		SourceType:         domain.SourceTypeLLMGeneration,
	}
	if err := env.branchRepo.CreateVersion(ctx, vOther); err != nil {
		t.Fatalf("failed to create vOther: %v", err)
	}
	if err := env.branchRepo.CompleteVersion(ctx, vOther); err != nil {
		t.Fatalf("failed to complete vOther: %v", err)
	}

	b, err := env.svc.GetBranch(ctx, env.defaultUser, env.branchID)
	if err != nil {
		t.Fatalf("failed to get branch: %v", err)
	}

	// 1. Cross-branch version assignment
	b.ActiveVersionID = &vOther.ID
	_, err = env.svc.UpdateBranch(ctx, b)
	if err == nil {
		t.Fatalf("expected error assigning foreign branch version, got nil")
	}
	if !errors.Is(err, domain.ErrValidation) {
		t.Errorf("expected domain.ErrValidation, got: %v", err)
	}

	// 2. Non-existent version assignment
	nonExistent := "ver-does-not-exist"
	b.ActiveVersionID = &nonExistent
	_, err = env.svc.UpdateBranch(ctx, b)
	if err == nil {
		t.Fatalf("expected error assigning non-existent version, got nil")
	}
	if !errors.Is(err, domain.ErrValidation) {
		t.Errorf("expected domain.ErrValidation, got: %v", err)
	}

	// 3. Pending/incomplete version assignment
	vPending := &domain.BranchVersion{
		ID:                 "ver-pending-test",
		BranchID:           env.branchID,
		Status:             domain.VersionStatusPending,
		MasterRevisionUsed: 1,
		TemplateSetIDUsed:  renderer.TemplateStandardCleanID,
		SourceType:         domain.SourceTypeLLMGeneration,
	}
	if err := env.branchRepo.CreateVersion(ctx, vPending); err != nil {
		t.Fatalf("failed to create pending version: %v", err)
	}
	b.ActiveVersionID = &vPending.ID
	_, err = env.svc.UpdateBranch(ctx, b)
	if err == nil {
		t.Fatalf("expected error assigning pending version as active, got nil")
	}
	if !errors.Is(err, domain.ErrValidation) {
		t.Errorf("expected domain.ErrValidation, got: %v", err)
	}
}

func TestBranchService_DefaultBranch(t *testing.T) {
	ctx := context.Background()
	env := setupTestBranchEnv(t, 10)
	defaultBranchID := domain.DefaultBranchID(env.defaultUser)

	// Fetch default branch
	defaultBranch, err := env.svc.GetBranch(ctx, env.defaultUser, defaultBranchID)
	if err != nil {
		t.Fatalf("failed to get default branch: %v", err)
	}
	if !defaultBranch.IsDefault {
		t.Fatalf("expected IsDefault=true on default branch")
	}

	t.Run("GenerateVersion on default branch bypasses LLM and worker pool", func(t *testing.T) {
		initialEnqueued := len(env.enqueuer.jobs)

		ver, err := env.svc.GenerateVersion(ctx, env.defaultUser, defaultBranchID)
		if err != nil {
			t.Fatalf("GenerateVersion failed: %v", err)
		}

		if len(env.enqueuer.jobs) != initialEnqueued {
			t.Errorf("expected enqueuer jobs to remain %d, got %d", initialEnqueued, len(env.enqueuer.jobs))
		}
		if ver.Status != domain.VersionStatusCompleted {
			t.Errorf("expected status completed, got %s", ver.Status)
		}
		if ver.SourceType != domain.SourceTypeMasterProfile {
			t.Errorf("expected SourceType %s, got %s", domain.SourceTypeMasterProfile, ver.SourceType)
		}
		if ver.ProviderUsed != "none" || ver.ModelUsed != "none" {
			t.Errorf("expected provider/model to be none, got %s/%s", ver.ProviderUsed, ver.ModelUsed)
		}
		if ver.HTMLOutput == "" || ver.MarkdownOutput == "" || ver.PlainTextOutput == "" {
			t.Errorf("expected non-empty rendered outputs")
		}

		// Verify branch active_version_id was updated
		bAfter, err := env.svc.GetBranch(ctx, env.defaultUser, defaultBranchID)
		if err != nil {
			t.Fatalf("failed to get branch after generate: %v", err)
		}
		if bAfter.ActiveVersionID == nil || *bAfter.ActiveVersionID != ver.ID {
			t.Errorf("expected active version ID to be updated to %s", ver.ID)
		}
	})

	t.Run("GenerateVersion on tailored branch enqueues job", func(t *testing.T) {
		initialEnqueued := len(env.enqueuer.jobs)

		ver, err := env.svc.GenerateVersion(ctx, env.defaultUser, env.branchID)
		if err != nil {
			t.Fatalf("GenerateVersion failed: %v", err)
		}

		if len(env.enqueuer.jobs) != initialEnqueued+1 {
			t.Errorf("expected enqueuer jobs to increase by 1, got %d (was %d)", len(env.enqueuer.jobs), initialEnqueued)
		}
		if ver.Status != domain.VersionStatusPending {
			t.Errorf("expected status pending for tailored branch, got %s", ver.Status)
		}
	})

	t.Run("UpdateBranch on default branch allows TemplateSetID and triggers direct re-render", func(t *testing.T) {
		b, err := env.svc.GetBranch(ctx, env.defaultUser, defaultBranchID)
		if err != nil {
			t.Fatalf("failed to get default branch: %v", err)
		}

		oldActiveVer := ""
		if b.ActiveVersionID != nil {
			oldActiveVer = *b.ActiveVersionID
		}

		b.TemplateSetID = renderer.TemplateDetailedImmigrationCVID
		updated, err := env.svc.UpdateBranch(ctx, b)
		if err != nil {
			t.Fatalf("failed to update default branch template: %v", err)
		}

		if updated.TemplateSetID != renderer.TemplateDetailedImmigrationCVID {
			t.Errorf("expected template %s, got %s", renderer.TemplateDetailedImmigrationCVID, updated.TemplateSetID)
		}
		if updated.ActiveVersionID == nil || *updated.ActiveVersionID == oldActiveVer {
			t.Errorf("expected new active version rendered after template update")
		}

		newVer, err := env.svc.GetVersion(ctx, defaultBranchID, *updated.ActiveVersionID)
		if err != nil {
			t.Fatalf("failed to get new active version: %v", err)
		}
		if newVer.TemplateSetIDUsed != renderer.TemplateDetailedImmigrationCVID {
			t.Errorf("expected version to use new template, got %s", newVer.TemplateSetIDUsed)
		}
	})

	t.Run("UpdateBranch on default branch rejects tailor fields", func(t *testing.T) {
		b, err := env.svc.GetBranch(ctx, env.defaultUser, defaultBranchID)
		if err != nil {
			t.Fatalf("failed to get default branch: %v", err)
		}

		b.TargetRole = "Staff Engineer"
		_, err = env.svc.UpdateBranch(ctx, b)
		if err == nil {
			t.Fatalf("expected error modifying target role on default branch, got nil")
		}
		if !errors.Is(err, domain.ErrDefaultBranchImmutable) {
			t.Errorf("expected ErrDefaultBranchImmutable, got: %v", err)
		}
	})

	t.Run("DeleteBranch on default branch returns ErrDefaultBranchImmutable", func(t *testing.T) {
		err := env.svc.DeleteBranch(ctx, env.defaultUser, defaultBranchID)
		if err == nil {
			t.Fatalf("expected error deleting default branch, got nil")
		}
		if !errors.Is(err, domain.ErrDefaultBranchImmutable) {
			t.Errorf("expected ErrDefaultBranchImmutable, got: %v", err)
		}
	})

	t.Run("EditVersion on default branch returns ErrDefaultBranchImmutable", func(t *testing.T) {
		b, err := env.svc.GetBranch(ctx, env.defaultUser, defaultBranchID)
		if err != nil {
			t.Fatalf("failed to get default branch: %v", err)
		}
		if b.ActiveVersionID == nil {
			t.Fatalf("expected active version on default branch")
		}

		_, err = env.svc.EditVersion(ctx, env.defaultUser, defaultBranchID, *b.ActiveVersionID, domain.TailoredContent{})
		if err == nil {
			t.Fatalf("expected error editing version on default branch, got nil")
		}
		if !errors.Is(err, domain.ErrDefaultBranchImmutable) {
			t.Errorf("expected ErrDefaultBranchImmutable, got: %v", err)
		}
	})

	t.Run("SyncBranches handles default branch synchronously without queueing", func(t *testing.T) {
		initialEnqueued := len(env.enqueuer.jobs)

		vIDs, err := env.svc.SyncBranches(ctx, env.defaultUser, []string{defaultBranchID, env.branchID})
		if err != nil {
			t.Fatalf("SyncBranches failed: %v", err)
		}
		if len(vIDs) != 2 {
			t.Fatalf("expected 2 version IDs returned, got %d", len(vIDs))
		}

		// Only 1 job enqueued (for env.branchID), default branch was rendered synchronously
		if len(env.enqueuer.jobs) != initialEnqueued+1 {
			t.Errorf("expected exactly 1 job enqueued for tailored branch, got %d", len(env.enqueuer.jobs)-initialEnqueued)
		}

		// Default branch version should be completed
		defVer, err := env.svc.GetVersion(ctx, defaultBranchID, vIDs[0])
		if err != nil {
			t.Fatalf("failed to get default branch version: %v", err)
		}
		if defVer.Status != domain.VersionStatusCompleted {
			t.Errorf("expected default branch version to be completed, got %s", defVer.Status)
		}
	})
}

func TestService_EditVersionPreservesGapReport(t *testing.T) {
	env := setupTestBranchEnv(t, 10)
	ctx := context.Background()

	score := 88.0
	expectedReport := &domain.GapReport{
		OverallMatchSummary: "Solid engineering match with minor distributed gaps.",
		MatchScore:          &score,
		MissingRequirements: []domain.GapItem{
			{
				Requirement:      "Kubernetes Operator SDK",
				MissingArea:      "Orchestration",
				Importance:       domain.GapImportancePreferred,
				ClarifyingPrompt: "Have you written Kubernetes operators in Go?",
			},
		},
		StrategicRecommendations: []string{"Highlight controller loops in summary."},
	}

	// Attach GapReport to parent version (v1)
	v1, err := env.svc.GetVersion(ctx, env.branchID, env.v1ID)
	if err != nil {
		t.Fatalf("failed to get v1: %v", err)
	}
	v1.GapReport = expectedReport
	if err := env.branchRepo.CompleteVersion(ctx, v1); err != nil {
		t.Fatalf("failed to update v1 with gap report: %v", err)
	}

	validContent := domain.TailoredContent{
		Summary: "Updated manual summary for Senior Cloud Architect.",
		Experiences: []domain.TailoredExperience{
			{
				SourceEntityID: env.validExpID,
				Company:        "Acme Corp",
				RoleTitle:      "Lead Systems Architect",
				StartDate:      domain.NewDate(2020, 1, 1),
				Location:       domain.Location{City: "San Francisco"},
				Bullets: []domain.TailoredBullet{
					{
						Text:          "Optimized distributed consensus throughput by 40%.",
						SourceFactIDs: []string{env.validFactID},
					},
				},
			},
		},
	}

	newVer, err := env.svc.EditVersion(ctx, env.defaultUser, env.branchID, env.v1ID, validContent)
	if err != nil {
		t.Fatalf("EditVersion failed: %v", err)
	}

	if newVer.GapReport == nil {
		t.Fatalf("expected newVer.GapReport to be preserved, got nil")
	}
	if newVer.GapReport.OverallMatchSummary != expectedReport.OverallMatchSummary {
		t.Errorf("expected summary %q, got %q", expectedReport.OverallMatchSummary, newVer.GapReport.OverallMatchSummary)
	}
	if newVer.GapReport.MatchScore == nil || *newVer.GapReport.MatchScore != score {
		t.Errorf("expected match score %v, got %v", score, newVer.GapReport.MatchScore)
	}
	if len(newVer.GapReport.MissingRequirements) != 1 {
		t.Errorf("expected 1 missing requirement, got %d", len(newVer.GapReport.MissingRequirements))
	}
}

type stubLLMService struct {
	gapReport *domain.GapReport
	genResp   *domain.GenerationResponse
	err       error
}

func (s *stubLLMService) GenerateTailoredResume(_ context.Context, _ domain.GenerationRequest) (*domain.GenerationResponse, error) {
	return s.genResp, s.err
}

func (s *stubLLMService) GenerateGapAnalysis(_ context.Context, _ domain.GenerationRequest) (*domain.GapReport, error) {
	if s.gapReport != nil {
		return s.gapReport, s.err
	}
	if s.genResp != nil {
		return &s.genResp.GapReport, s.err
	}
	return nil, s.err
}

func TestService_AnalyzeGaps(t *testing.T) {
	ctx := context.Background()

	t.Run("returns ErrNotFound for unknown branch", func(t *testing.T) {
		env := setupTestBranchEnv(t, 10)
		_, err := env.svc.AnalyzeGaps(ctx, env.defaultUser, "non-existent-branch")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("analyzes gaps via LLMService and returns deterministic score", func(t *testing.T) {
		env := setupTestBranchEnv(t, 10)

		stubLLM := &stubLLMService{
			gapReport: &domain.GapReport{
				OverallMatchSummary: "Decoupled fast analysis completed.",
				MissingRequirements: []domain.GapItem{
					{
						Requirement:      "Kubernetes CRD controllers",
						MissingArea:      "Container Orchestration",
						Importance:       domain.GapImportanceCritical,
						ClarifyingPrompt: "Have you authored custom controllers?",
					},
					{
						Requirement:      "Kafka event streams",
						MissingArea:      "Messaging",
						Importance:       domain.GapImportancePreferred,
						ClarifyingPrompt: "Have you worked with high-throughput Kafka?",
					},
				},
				StrategicRecommendations: []string{"Highlight controller loops in summary."},
			},
		}

		// Rebuild svc with stubLLM
		svcWithLLM := branch.NewService(branch.Config{
			BranchRepo:   env.branchRepo,
			ProfileRepo:  env.profileRepo,
			JobEnqueuer:  env.enqueuer,
			Validator:    validator.New(),
			Renderer:     renderer.NewTemplateRenderer(),
			LLMService:   stubLLM,
		})

		report, err := svcWithLLM.AnalyzeGaps(ctx, env.defaultUser, env.branchID)
		if err != nil {
			t.Fatalf("AnalyzeGaps failed: %v", err)
		}
		if report == nil {
			t.Fatalf("expected non-nil GapReport")
		}
		if report.OverallMatchSummary != "Decoupled fast analysis completed." {
			t.Errorf("unexpected summary: %q", report.OverallMatchSummary)
		}
		// 1 critical (-15) + 1 preferred (-7) = 100 - 22 = 78.0
		if report.MatchScore == nil || *report.MatchScore != 78.0 {
			t.Errorf("expected deterministic match score 78.0, got %v", report.MatchScore)
		}
		if len(report.IdentifiedGaps) != 2 {
			t.Errorf("expected 2 identified gaps alias, got %d", len(report.IdentifiedGaps))
		}
	})

	t.Run("normalizes identified_gaps before calculating deterministic match score", func(t *testing.T) {
		env := setupTestBranchEnv(t, 10)

		stubLLM := &stubLLMService{
			gapReport: &domain.GapReport{
				OverallMatchSummary: "Report with only identified_gaps populated.",
				IdentifiedGaps: []domain.GapItem{
					{
						Requirement: "Distributed consensus",
						Importance:  domain.GapImportanceCritical, // -15
					},
					{
						Requirement: "GraphQL subscriptions",
						Importance:  domain.GapImportancePreferred, // -7
					},
				},
			},
		}

		svcWithLLM := branch.NewService(branch.Config{
			BranchRepo:  env.branchRepo,
			ProfileRepo: env.profileRepo,
			LLMService:  stubLLM,
		})

		report, err := svcWithLLM.AnalyzeGaps(ctx, env.defaultUser, env.branchID)
		if err != nil {
			t.Fatalf("AnalyzeGaps failed: %v", err)
		}
		// 100 - (15 + 7) = 78.0
		if report.MatchScore == nil || *report.MatchScore != 78.0 {
			t.Errorf("expected score 78.0 after normalization, got %v", report.MatchScore)
		}
	})

	t.Run("persists gap report on versionless branch", func(t *testing.T) {
		env := setupTestBranchEnv(t, 10)

		// Create a branch without active version
		b := &domain.Branch{
			ID:                 "branch-no-ver-" + uuid.New().String(),
			UserID:             env.defaultUser,
			Name:               "New Branch Without Versions",
			TargetRole:         "Backend Engineer",
			TargetRequirements: "Go, Kubernetes",
			TemplateSetID:      renderer.TemplateStandardCleanID,
		}
		created, err := env.svc.CreateBranch(ctx, b)
		if err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}

		stubLLM := &stubLLMService{
			gapReport: &domain.GapReport{
				OverallMatchSummary: "Analysis for versionless branch",
				MissingRequirements: []domain.GapItem{
					{Requirement: "Kubernetes", Importance: domain.GapImportanceCritical},
				},
			},
		}

		svcWithLLM := branch.NewService(branch.Config{
			BranchRepo:  env.branchRepo,
			ProfileRepo: env.profileRepo,
			LLMService:  stubLLM,
		})

		report, err := svcWithLLM.AnalyzeGaps(ctx, env.defaultUser, created.ID)
		if err != nil {
			t.Fatalf("AnalyzeGaps failed: %v", err)
		}

		// Reload branch from DB to verify persistence on branch record
		reloaded, err := env.svc.GetBranch(ctx, env.defaultUser, created.ID)
		if err != nil {
			t.Fatalf("GetBranch failed: %v", err)
		}
		if reloaded.GapReport == nil {
			t.Fatalf("expected GapReport to be persisted directly on versionless branch")
		}
		if reloaded.GapReport.OverallMatchSummary != report.OverallMatchSummary {
			t.Errorf("expected summary %q, got %q", report.OverallMatchSummary, reloaded.GapReport.OverallMatchSummary)
		}
	})

	t.Run("calculates skill coverage ratio accurately", func(t *testing.T) {
		env := setupTestBranchEnv(t, 10)

		// Seed a skill on profile
		s := &domain.Skill{
			ID:       "skill-go-test",
			UserID:   env.defaultUser,
			Name:     "Go",
			Category: "Languages",
		}
		if err := env.profileRepo.CreateSkill(ctx, s); err != nil {
			t.Fatalf("failed to create skill: %v", err)
		}

		// Update branch with TargetRequirements containing "Go"
		branchRec, err := env.svc.GetBranch(ctx, env.defaultUser, env.branchID)
		if err != nil {
			t.Fatalf("GetBranch failed: %v", err)
		}
		branchRec.TargetRequirements = "Must know Go, Distributed Systems, and Raft"
		if _, err := env.svc.UpdateBranch(ctx, branchRec); err != nil {
			t.Fatalf("UpdateBranch failed: %v", err)
		}

		stubLLM := &stubLLMService{
			gapReport: &domain.GapReport{
				OverallMatchSummary: "Coverage test",
				MissingRequirements: []domain.GapItem{
					{Requirement: "Raft consensus", Importance: domain.GapImportanceCritical},
				},
			},
		}

		svcWithLLM := branch.NewService(branch.Config{
			BranchRepo:  env.branchRepo,
			ProfileRepo: env.profileRepo,
			LLMService:  stubLLM,
		})

		report, err := svcWithLLM.AnalyzeGaps(ctx, env.defaultUser, env.branchID)
		if err != nil {
			t.Fatalf("AnalyzeGaps failed: %v", err)
		}
		if report.SkillCoverageRatio == nil {
			t.Fatalf("expected SkillCoverageRatio to be computed, got nil")
		}
		// Go matched (1), Gaps (1) => 1 / (1 + 1) = 0.50
		if *report.SkillCoverageRatio <= 0.0 || *report.SkillCoverageRatio > 1.0 {
			t.Errorf("expected ratio between 0.0 and 1.0, got %v", *report.SkillCoverageRatio)
		}
	})

	t.Run("deobfuscates gap report returned by LLM to candidate authentic name", func(t *testing.T) {
		env := setupTestBranchEnv(t, 10)
		realName := "Grace Hopper"
		prof, _ := env.profileRepo.GetProfile(ctx, env.defaultUser)
		prof.FullName = realName
		prof.Email = "grace.hopper@navy.mil"
		_ = env.profileRepo.UpsertProfile(ctx, prof)

		stubLLM := &stubLLMService{
			gapReport: &domain.GapReport{
				OverallMatchSummary: realName + " matches compiler design requirements.",
				StrategicRecommendations: []string{
					realName + " should highlight COBOL leadership.",
				},
				MissingRequirements: []domain.GapItem{
					{
						Requirement:       "Machine code optimization",
						ClarifyingPrompt:  "Does " + realName + " have machine code experience?",
						RecommendedAction: "Consult with " + realName,
					},
				},
			},
		}

		svcWithLLM := branch.NewService(branch.Config{
			BranchRepo:  env.branchRepo,
			ProfileRepo: env.profileRepo,
			LLMService:  stubLLM,
		})

		report, err := svcWithLLM.AnalyzeGaps(ctx, env.defaultUser, env.branchID)
		if err != nil {
			t.Fatalf("AnalyzeGaps failed: %v", err)
		}

		if strings.Contains(report.OverallMatchSummary, "[John Doe]") {
			t.Errorf("gap report leaked placeholder [John Doe]")
		}
		if !strings.Contains(report.OverallMatchSummary, realName) {
			t.Errorf("gap report missing authentic name %q", realName)
		}
		if !strings.Contains(report.StrategicRecommendations[0], realName) {
			t.Errorf("strategic recommendation missing authentic name %q", realName)
		}
		if !strings.Contains(report.MissingRequirements[0].ClarifyingPrompt, realName) {
			t.Errorf("clarifying prompt missing authentic name %q", realName)
		}
	})
}

func TestService_GetPromptPreview_ContactPrivacy(t *testing.T) {
	ctx := context.Background()
	env := setupTestBranchEnv(t, 10)

	realName := "Barbara Liskov"
	realEmail := "liskov@csail.mit.edu"
	realPhone := "+1 (617) 253-5860"
	prof, _ := env.profileRepo.GetProfile(ctx, env.defaultUser)
	prof.FullName = realName
	prof.Email = realEmail
	prof.Phone = realPhone
	prof.Summary = realName + " created the Liskov substitution principle."
	prof.Profiles = []domain.SocialLink{
		{Network: "Website", URL: "https://liskov.csail.mit.edu"},
	}
	_ = env.profileRepo.UpsertProfile(ctx, prof)

	preview, err := env.svc.GetPromptPreview(ctx, env.defaultUser, env.branchID, nil)
	if err != nil {
		t.Fatalf("GetPromptPreview failed: %v", err)
	}

	assertNoPII := func(t *testing.T, field, text string) {
		t.Helper()
		if strings.Contains(text, realName) {
			t.Errorf("%s leaked realName %q", field, realName)
		}
		if strings.Contains(text, realEmail) {
			t.Errorf("%s leaked realEmail %q", field, realEmail)
		}
		if strings.Contains(text, realPhone) {
			t.Errorf("%s leaked realPhone %q", field, realPhone)
		}
		if strings.Contains(text, "https://liskov.csail.mit.edu") {
			t.Errorf("%s leaked real website", field)
		}
	}

	assertNoPII(t, "SystemPrompt", preview.SystemPrompt)
	assertNoPII(t, "UserPrompt", preview.UserPrompt)

	if !strings.Contains(preview.UserPrompt, privacy.PlaceholderFullName) {
		t.Errorf("UserPrompt missing PlaceholderFullName %q", privacy.PlaceholderFullName)
	}
	if !strings.Contains(preview.UserPrompt, privacy.PlaceholderEmail) {
		t.Errorf("UserPrompt missing PlaceholderEmail %q", privacy.PlaceholderEmail)
	}
	if !strings.Contains(preview.UserPrompt, privacy.PlaceholderPhone) {
		t.Errorf("UserPrompt missing PlaceholderPhone %q", privacy.PlaceholderPhone)
	}
}



