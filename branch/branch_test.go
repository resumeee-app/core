package branch_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/branch"
	"github.com/resumeee-app/resumeee-core/provenance"
	"github.com/resumeee-app/resumeee-core/renderer"
	"github.com/resumeee-app/resumeee-core/sanitizer"
)

type stubEnqueuer struct {
	mu       sync.Mutex
	capacity int
	jobs     []core.GenerationJob
	closed   bool
}

func newStubEnqueuer(capacity int) *stubEnqueuer {
	return &stubEnqueuer{capacity: capacity}
}

func (e *stubEnqueuer) Enqueue(job core.GenerationJob) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed || (e.capacity > 0 && len(e.jobs) >= e.capacity) {
		return core.ErrQueueFull
	}
	e.jobs = append(e.jobs, job)
	return nil
}

type testBranchEnv struct {
	db           any
	svc          *branch.Service
	branchRepo   core.BranchStore
	profileRepo  core.ProfileStore
	careerRepo   core.CareerStore
	templateRepo core.TemplateStore
	enqueuer     *stubEnqueuer
	defaultUser  string
	branchID     string
	v1ID         string
	validFactID  string
	validExpID   string
	validSkillID string
}

func setupTestBranchEnv(t *testing.T, enqueuerCap int) *testBranchEnv {
	t.Helper()

	ctx := context.Background()
	defaultUser := "default"
	store := newSeededBranchStore(ctx, defaultUser)

	profileRepo := store
	presetRepo := store
	templateRepo := store
	branchRepo := store
	val := provenance.New()
	rend := renderer.New()
	enqueuer := newStubEnqueuer(enqueuerCap)

	svc := branch.New(branch.Config{
		Branches:       branchRepo,
		Profiles:       profileRepo,
		Presets:        presetRepo,
		Templates:      templateRepo,
		Quotas:         store,
		JobEnqueuer:    enqueuer,
		Validator:      val,
		Renderer:       rend,
		PromptCompiler: &fakePromptCompiler{},
	})

	b := &core.Branch{
		ID:            "branch-" + uuid.New().String(),
		UserID:        defaultUser,
		Name:          "Senior Cloud Architect",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	created, err := svc.CreateBranch(ctx, b)
	if err != nil {
		t.Fatalf("failed to create branch: %v", err)
	}

	// Create initial v1
	v1 := &core.BranchVersion{
		ID:                "ver-init-1",
		BranchID:          created.ID,
		Status:            core.VersionStatusCompleted,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		SourceType:        core.SourceTypeLLMGeneration,
	}
	if err := branchRepo.CreateVersion(ctx, v1); err != nil {
		t.Fatalf("failed to create v1: %v", err)
	}
	if err := branchRepo.CompleteVersion(ctx, v1); err != nil {
		t.Fatalf("failed to complete v1: %v", err)
	}

	// Seed an experience and fact so manual edits have valid provenance
	exp := &core.WorkExperience{
		ID:        "exp-test-1",
		UserID:    defaultUser,
		Company:   "Acme Corp",
		RoleTitle: "Lead Systems Architect",
		StartDate: core.NewDate(2020, 1, 1),
	}
	if err := profileRepo.CreateExperience(ctx, exp); err != nil {
		t.Fatalf("failed to create experience: %v", err)
	}

	fact := &core.Fact{
		ID:        "fact-test-1",
		UserID:    defaultUser,
		OwnerID:   exp.ID,
		OwnerType: core.OwnerTypeWorkExperience,
		Statement: "Optimized distributed consensus throughput by 40%.",
	}
	if err := profileRepo.CreateFact(ctx, fact); err != nil {
		t.Fatalf("failed to create fact: %v", err)
	}

	snap, err := profileRepo.Snapshot(ctx, defaultUser)
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
		careerRepo:   store,
		templateRepo: templateRepo,
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
	results := make([]*core.BranchVersion, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			content := core.TailoredContent{
				Summary: fmt.Sprintf("Summary updated by thread %d", idx),
				Experiences: []core.TailoredExperience{
					{
						SourceEntityID: env.validExpID,
						Company:        "Acme Corp",
						RoleTitle:      "Engineer",
						StartDate:      core.NewDate(2021, 1, 1),
						Bullets: []core.TailoredBullet{
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
	badContent := core.TailoredContent{
		Summary: "Fake accomplishments",
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID: env.validExpID,
				Company:        "Acme Corp",
				RoleTitle:      "Engineer",
				StartDate:      core.NewDate(2021, 1, 1),
				Bullets: []core.TailoredBullet{
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
	if !errors.Is(err, core.ErrProvenance) {
		t.Fatalf("expected errors.Is(err, core.ErrProvenance), got %v", err)
	}
}

// 3. Batch Sync and Backpressure Saturation
func TestService_BatchSyncAndBackpressure(t *testing.T) {
	env := setupTestBranchEnv(t, 1) // Capacity of 1 to test backpressure saturation
	ctx := context.Background()

	// Create a second branch
	b2 := &core.Branch{
		ID:            "branch-2-" + uuid.New().String(),
		UserID:        env.defaultUser,
		Name:          "Frontend Architect",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	_, err := env.svc.CreateBranch(ctx, b2)
	if err != nil {
		t.Fatalf("failed to create branch 2: %v", err)
	}

	// Increment main revision to make branches stale
	_, err = env.profileRepo.IncrementMainRevision(ctx, env.defaultUser)
	if err != nil {
		t.Fatalf("failed to increment main revision: %v", err)
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
	if !errors.Is(err, core.ErrQueueFull) {
		t.Fatalf("expected core.ErrQueueFull on saturated batch sync, got %v", err)
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

	if ver.Status != core.VersionStatusPending {
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

// 5. Rollback/Promotion updates LastSyncedMainRevision and reflects staleness
func TestService_UpdateBranchActiveVersionRollbackStaleness(t *testing.T) {
	env := setupTestBranchEnv(t, 10)
	ctx := context.Background()

	// Obtain current profile revision as rev1
	prof, err := env.profileRepo.Profile(ctx, env.defaultUser)
	if err != nil {
		t.Fatalf("failed to get profile: %v", err)
	}
	rev1 := prof.MainRevision

	// Update initial v1 to have MainRevisionUsed = rev1
	v1, err := env.branchRepo.Version(ctx, env.branchID, env.v1ID)
	if err != nil {
		t.Fatalf("failed to get v1: %v", err)
	}
	v1.MainRevisionUsed = rev1
	if err := env.branchRepo.CompleteVersion(ctx, v1); err != nil {
		t.Fatalf("failed to complete v1: %v", err)
	}

	// Advance profile main revision to rev2
	rev2, err := env.profileRepo.IncrementMainRevision(ctx, env.defaultUser)
	if err != nil {
		t.Fatalf("failed to increment main revision: %v", err)
	}

	// Create and complete v2 at MainRevisionUsed = rev2
	v2 := &core.BranchVersion{
		ID:                "ver-v2-test",
		BranchID:          env.branchID,
		Status:            core.VersionStatusCompleted,
		MainRevisionUsed:  rev2,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		SourceType:        core.SourceTypeLLMGeneration,
	}
	if err := env.branchRepo.CreateVersion(ctx, v2); err != nil {
		t.Fatalf("create v2: %v", err)
	}
	if err := env.branchRepo.CompleteVersion(ctx, v2); err != nil {
		t.Fatalf("complete v2: %v", err)
	}

	// Fetch branch - should be active on v2 and not stale (rev2 == profile rev2)
	b, err := env.svc.Branch(ctx, env.defaultUser, env.branchID)
	if err != nil {
		t.Fatalf("failed to get branch: %v", err)
	}
	if b.ActiveVersionID == nil || *b.ActiveVersionID != v2.ID {
		t.Fatalf("expected ActiveVersionID=%s, got %v", v2.ID, b.ActiveVersionID)
	}
	if b.IsStale {
		t.Fatalf("expected branch not stale when active on v2 (rev %d), but got is_stale=true", rev2)
	}

	// Rollback active version to v1 (which had MainRevisionUsed = rev1)
	b.ActiveVersionID = &env.v1ID
	updated, err := env.svc.UpdateBranch(ctx, b)
	if err != nil {
		t.Fatalf("UpdateBranch rollback failed: %v", err)
	}

	if updated.LastSyncedMainRevision != rev1 {
		t.Errorf("expected LastSyncedMainRevision to be rolled back to %d, got %d", rev1, updated.LastSyncedMainRevision)
	}
	if !updated.IsStale {
		t.Errorf("expected branch to be marked stale after rolling back to rev %d (profile is rev %d)", rev1, rev2)
	}

	// Verify persistence: fetching branch anew reports IsStale = true
	refetched, err := env.svc.Branch(ctx, env.defaultUser, env.branchID)
	if err != nil {
		t.Fatalf("failed to refetch branch: %v", err)
	}
	if refetched.LastSyncedMainRevision != rev1 {
		t.Errorf("persisted LastSyncedMainRevision expected %d, got %d", rev1, refetched.LastSyncedMainRevision)
	}
	if !refetched.IsStale {
		t.Errorf("persisted branch expected IsStale=true, got false")
	}
}

func TestService_UpdateBranchSyncsGapReportOnActiveVersionSwitch(t *testing.T) {
	env := setupTestBranchEnv(t, 10)
	ctx := context.Background()

	score70 := 70.0
	score95 := 95.0

	// Set up v1 with GapReport score 70
	v1, err := env.branchRepo.Version(ctx, env.branchID, env.v1ID)
	if err != nil {
		t.Fatalf("failed to get v1: %v", err)
	}
	v1.Status = core.VersionStatusCompleted
	v1.GapReport = &core.GapReport{
		OverallMatchSummary: "V1 match report",
		MatchScore:          &score70,
		MissingRequirements: []core.GapItem{
			{Requirement: "Rust programming", Importance: core.GapImportanceCritical},
			{Requirement: "Distributed consensus", Importance: core.GapImportancePreferred},
		},
	}
	if err := env.branchRepo.CompleteVersion(ctx, v1); err != nil {
		t.Fatalf("failed to complete v1: %v", err)
	}

	// Create v2 with GapReport score 95
	v2 := &core.BranchVersion{
		ID:                "ver-v2-gap-test",
		BranchID:          env.branchID,
		Status:            core.VersionStatusCompleted,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		SourceType:        core.SourceTypeLLMGeneration,
		GapReport: &core.GapReport{
			OverallMatchSummary: "V2 improved match report",
			MatchScore:          &score95,
			MissingRequirements: []core.GapItem{
				{Requirement: "Docker orchestration", Importance: core.GapImportanceNiceToHave},
			},
		},
	}
	if err := env.branchRepo.CreateVersion(ctx, v2); err != nil {
		t.Fatalf("failed to create v2: %v", err)
	}
	if err := env.branchRepo.CompleteVersion(ctx, v2); err != nil {
		t.Fatalf("failed to complete v2: %v", err)
	}

	// Fetch current branch
	b, err := env.svc.Branch(ctx, env.defaultUser, env.branchID)
	if err != nil {
		t.Fatalf("failed to get branch: %v", err)
	}

	// Switch active version back to v1
	b.ActiveVersionID = &env.v1ID
	b.GapReport = nil // Simulate calling update with just ActiveVersionID switch
	updated, err := env.svc.UpdateBranch(ctx, b)
	if err != nil {
		t.Fatalf("UpdateBranch to v1 failed: %v", err)
	}
	if updated.GapReport == nil || updated.GapReport.MatchScore == nil || *updated.GapReport.MatchScore != score70 {
		t.Fatalf("expected updated branch GapReport score %f, got %v", score70, updated.GapReport)
	}
	if updated.GapReport.OverallMatchSummary != "V1 match report" {
		t.Errorf("expected summary 'V1 match report', got %q", updated.GapReport.OverallMatchSummary)
	}

	// Now switch active version forward to v2
	b.ActiveVersionID = &v2.ID
	b.GapReport = nil
	updatedV2, err := env.svc.UpdateBranch(ctx, b)
	if err != nil {
		t.Fatalf("UpdateBranch to v2 failed: %v", err)
	}
	if updatedV2.GapReport == nil || updatedV2.GapReport.MatchScore == nil || *updatedV2.GapReport.MatchScore != score95 {
		t.Fatalf("expected updated branch GapReport score %f, got %v", score95, updatedV2.GapReport)
	}
	if updatedV2.GapReport.OverallMatchSummary != "V2 improved match report" {
		t.Errorf("expected summary 'V2 improved match report', got %q", updatedV2.GapReport.OverallMatchSummary)
	}
}

func TestService_GetAndListBranchesFallbackResolution(t *testing.T) {
	env := setupTestBranchEnv(t, 10)
	ctx := context.Background()

	score87 := 87.0

	// Create branch with active version
	b := &core.Branch{
		ID:            "branch-service-fallback-" + uuid.New().String(),
		UserID:        env.defaultUser,
		Name:          "Data Platform Architect",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	if err := env.branchRepo.CreateBranch(ctx, b); err != nil {
		t.Fatalf("create branch: %v", err)
	}

	v := &core.BranchVersion{
		ID:                "ver-service-fallback",
		BranchID:          b.ID,
		Status:            core.VersionStatusCompleted,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		SourceType:        core.SourceTypeLLMGeneration,
		GapReport: &core.GapReport{
			OverallMatchSummary: "Fallback summary from version",
			MatchScore:          &score87,
			MissingRequirements: []core.GapItem{
				{Requirement: "Apache Spark Streaming", Importance: core.GapImportanceCritical},
			},
		},
	}
	if err := env.branchRepo.CreateVersion(ctx, v); err != nil {
		t.Fatalf("create version: %v", err)
	}

	// Directly set ActiveVersionID without GapReport on parent branch in repo (simulating legacy store state)
	b.ActiveVersionID = &v.ID
	b.GapReport = nil
	if err := env.branchRepo.UpdateBranch(ctx, b); err != nil {
		t.Fatalf("update branch in repo: %v", err)
	}

	// 1. Service Branch must fallback-resolve GapReport
	fetched, err := env.svc.Branch(ctx, env.defaultUser, b.ID)
	if err != nil {
		t.Fatalf("svc.Branch failed: %v", err)
	}
	if fetched.GapReport == nil {
		t.Fatalf("expected svc.Branch to fallback-resolve GapReport, got nil")
	}
	if fetched.GapReport.MatchScore == nil || *fetched.GapReport.MatchScore != score87 {
		t.Errorf("expected fallback match score %f, got %v", score87, fetched.GapReport.MatchScore)
	}
	if fetched.GapReport.OverallMatchSummary != "Fallback summary from version" {
		t.Errorf("expected summary 'Fallback summary from version', got %q", fetched.GapReport.OverallMatchSummary)
	}

	// 2. Service ListBranches must fallback-resolve GapReport
	branches, err := env.svc.ListBranches(ctx, env.defaultUser, false)
	if err != nil {
		t.Fatalf("svc.ListBranches failed: %v", err)
	}

	var found *core.Branch
	for i := range branches {
		if branches[i].ID == b.ID {
			found = &branches[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected branch %s in ListBranches output", b.ID)
	}
	if found.GapReport == nil {
		t.Fatalf("expected svc.ListBranches to fallback-resolve GapReport, got nil")
	}
	if found.GapReport.MatchScore == nil || *found.GapReport.MatchScore != score87 {
		t.Errorf("expected ListBranches fallback match score %f, got %v", score87, found.GapReport.MatchScore)
	}
}

// 6. Foreign, non-existent, or incomplete ActiveVersionID must be rejected
func TestService_UpdateBranchForeignAndInvalidVersionProtection(t *testing.T) {
	env := setupTestBranchEnv(t, 10)
	ctx := context.Background()

	// Create Branch 2 with version vOther
	b2 := &core.Branch{
		ID:            "branch-other-" + uuid.New().String(),
		UserID:        env.defaultUser,
		Name:          "Other Branch",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	created2, err := env.svc.CreateBranch(ctx, b2)
	if err != nil {
		t.Fatalf("failed to create branch 2: %v", err)
	}

	vOther := &core.BranchVersion{
		ID:                "ver-other-branch",
		BranchID:          created2.ID,
		Status:            core.VersionStatusCompleted,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		SourceType:        core.SourceTypeLLMGeneration,
	}
	if err := env.branchRepo.CreateVersion(ctx, vOther); err != nil {
		t.Fatalf("failed to create vOther: %v", err)
	}
	if err := env.branchRepo.CompleteVersion(ctx, vOther); err != nil {
		t.Fatalf("failed to complete vOther: %v", err)
	}

	b, err := env.svc.Branch(ctx, env.defaultUser, env.branchID)
	if err != nil {
		t.Fatalf("failed to get branch: %v", err)
	}

	// 1. Cross-branch version assignment
	b.ActiveVersionID = &vOther.ID
	_, err = env.svc.UpdateBranch(ctx, b)
	if err == nil {
		t.Fatalf("expected error assigning foreign branch version, got nil")
	}
	if !errors.Is(err, core.ErrValidation) {
		t.Errorf("expected core.ErrValidation, got: %v", err)
	}

	// 2. Non-existent version assignment
	nonExistent := "ver-does-not-exist"
	b.ActiveVersionID = &nonExistent
	_, err = env.svc.UpdateBranch(ctx, b)
	if err == nil {
		t.Fatalf("expected error assigning non-existent version, got nil")
	}
	if !errors.Is(err, core.ErrValidation) {
		t.Errorf("expected core.ErrValidation, got: %v", err)
	}

	// 3. Pending/incomplete version assignment
	vPending := &core.BranchVersion{
		ID:                "ver-pending-test",
		BranchID:          env.branchID,
		Status:            core.VersionStatusPending,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		SourceType:        core.SourceTypeLLMGeneration,
	}
	if err := env.branchRepo.CreateVersion(ctx, vPending); err != nil {
		t.Fatalf("failed to create pending version: %v", err)
	}
	b.ActiveVersionID = &vPending.ID
	_, err = env.svc.UpdateBranch(ctx, b)
	if err == nil {
		t.Fatalf("expected error assigning pending version as active, got nil")
	}
	if !errors.Is(err, core.ErrValidation) {
		t.Errorf("expected core.ErrValidation, got: %v", err)
	}
}

func TestBranchService_DefaultBranch(t *testing.T) {
	ctx := context.Background()
	env := setupTestBranchEnv(t, 10)
	defaultBranchID := core.DefaultBranchID(env.defaultUser)

	// Fetch default branch
	defaultBranch, err := env.svc.Branch(ctx, env.defaultUser, defaultBranchID)
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
		if ver.Status != core.VersionStatusCompleted {
			t.Errorf("expected status completed, got %s", ver.Status)
		}
		if ver.SourceType != core.SourceTypeMainProfile {
			t.Errorf("expected SourceType %s, got %s", core.SourceTypeMainProfile, ver.SourceType)
		}
		if ver.ProviderUsed != "none" || ver.ModelUsed != "none" {
			t.Errorf("expected provider/model to be none, got %s/%s", ver.ProviderUsed, ver.ModelUsed)
		}
		if ver.HTMLOutput == "" || ver.MarkdownOutput == "" || ver.PlainTextOutput == "" {
			t.Errorf("expected non-empty rendered outputs")
		}

		// Verify branch active_version_id was updated
		bAfter, err := env.svc.Branch(ctx, env.defaultUser, defaultBranchID)
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
		if ver.Status != core.VersionStatusPending {
			t.Errorf("expected status pending for tailored branch, got %s", ver.Status)
		}
	})

	t.Run("UpdateBranch on default branch allows TemplateSetID and triggers direct re-render", func(t *testing.T) {
		b, err := env.svc.Branch(ctx, env.defaultUser, defaultBranchID)
		if err != nil {
			t.Fatalf("failed to get default branch: %v", err)
		}

		oldActiveVer := ""
		if b.ActiveVersionID != nil {
			oldActiveVer = *b.ActiveVersionID
		}

		b.TemplateSetID = core.TemplateDetailedImmigrationCVID
		updated, err := env.svc.UpdateBranch(ctx, b)
		if err != nil {
			t.Fatalf("failed to update default branch template: %v", err)
		}

		if updated.TemplateSetID != core.TemplateDetailedImmigrationCVID {
			t.Errorf("expected template %s, got %s", core.TemplateDetailedImmigrationCVID, updated.TemplateSetID)
		}
		if updated.ActiveVersionID == nil || *updated.ActiveVersionID == oldActiveVer {
			t.Errorf("expected new active version rendered after template update")
		}

		newVer, err := env.svc.Version(ctx, defaultBranchID, *updated.ActiveVersionID)
		if err != nil {
			t.Fatalf("failed to get new active version: %v", err)
		}
		if newVer.TemplateSetIDUsed != core.TemplateDetailedImmigrationCVID {
			t.Errorf("expected version to use new template, got %s", newVer.TemplateSetIDUsed)
		}
	})

	t.Run("UpdateBranch on default branch rejects tailor fields", func(t *testing.T) {
		b, err := env.svc.Branch(ctx, env.defaultUser, defaultBranchID)
		if err != nil {
			t.Fatalf("failed to get default branch: %v", err)
		}

		b.TargetRole = "Staff Engineer"
		_, err = env.svc.UpdateBranch(ctx, b)
		if err == nil {
			t.Fatalf("expected error modifying target role on default branch, got nil")
		}
		if !errors.Is(err, core.ErrDefaultBranchImmutable) {
			t.Errorf("expected ErrDefaultBranchImmutable, got: %v", err)
		}

		// Reset target role and test reasoning level rejection on default branch
		b.TargetRole = ""
		b.ReasoningLevel = "high"
		_, err = env.svc.UpdateBranch(ctx, b)
		if err == nil {
			t.Fatalf("expected error modifying reasoning level on default branch, got nil")
		}
		if !errors.Is(err, core.ErrDefaultBranchImmutable) {
			t.Errorf("expected ErrDefaultBranchImmutable for reasoning_level, got: %v", err)
		}
	})

	t.Run("DeleteBranch on default branch returns ErrDefaultBranchImmutable", func(t *testing.T) {
		err := env.svc.DeleteBranch(ctx, env.defaultUser, defaultBranchID)
		if err == nil {
			t.Fatalf("expected error deleting default branch, got nil")
		}
		if !errors.Is(err, core.ErrDefaultBranchImmutable) {
			t.Errorf("expected ErrDefaultBranchImmutable, got: %v", err)
		}
	})

	t.Run("EditVersion on default branch returns ErrDefaultBranchImmutable", func(t *testing.T) {
		b, err := env.svc.Branch(ctx, env.defaultUser, defaultBranchID)
		if err != nil {
			t.Fatalf("failed to get default branch: %v", err)
		}
		if b.ActiveVersionID == nil {
			t.Fatalf("expected active version on default branch")
		}

		_, err = env.svc.EditVersion(ctx, env.defaultUser, defaultBranchID, *b.ActiveVersionID, core.TailoredContent{})
		if err == nil {
			t.Fatalf("expected error editing version on default branch, got nil")
		}
		if !errors.Is(err, core.ErrDefaultBranchImmutable) {
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
		defVer, err := env.svc.Version(ctx, defaultBranchID, vIDs[0])
		if err != nil {
			t.Fatalf("failed to get default branch version: %v", err)
		}
		if defVer.Status != core.VersionStatusCompleted {
			t.Errorf("expected default branch version to be completed, got %s", defVer.Status)
		}
	})
}

func TestService_EditVersionPreservesGapReport(t *testing.T) {
	env := setupTestBranchEnv(t, 10)
	ctx := context.Background()

	score := 88.0
	expectedReport := &core.GapReport{
		OverallMatchSummary: "Solid engineering match with minor distributed gaps.",
		MatchScore:          &score,
		MissingRequirements: []core.GapItem{
			{
				Requirement:      "Kubernetes Operator SDK",
				MissingArea:      "Orchestration",
				Importance:       core.GapImportancePreferred,
				ClarifyingPrompt: "Have you written Kubernetes operators in Go?",
			},
		},
		StrategicRecommendations: []string{"Highlight controller loops in summary."},
	}

	// Attach GapReport to parent version (v1)
	v1, err := env.svc.Version(ctx, env.branchID, env.v1ID)
	if err != nil {
		t.Fatalf("failed to get v1: %v", err)
	}
	v1.GapReport = expectedReport
	if err := env.branchRepo.CompleteVersion(ctx, v1); err != nil {
		t.Fatalf("failed to update v1 with gap report: %v", err)
	}

	validContent := core.TailoredContent{
		Summary: "Updated manual summary for Senior Cloud Architect.",
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID: env.validExpID,
				Company:        "Acme Corp",
				RoleTitle:      "Lead Systems Architect",
				StartDate:      core.NewDate(2020, 1, 1),
				Location:       core.Location{City: "San Francisco"},
				Bullets: []core.TailoredBullet{
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

var (
	_ branch.Generator    = (*stubGenerator)(nil)
	_ branch.Generator    = (*stubATSSimulator)(nil)
	_ branch.QuotaManager = (*spyQuotaRepo)(nil)
)

type stubGenerator struct {
	gapReport *core.GapReport
	gapResp   *core.GapAnalysisResponse
	genResp   *core.GenerationResponse
	err       error
}

func (s *stubGenerator) GenerateTailoredResume(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
	return s.genResp, s.err
}

func (s *stubGenerator) GenerateGapAnalysis(_ context.Context, _ core.GenerationRequest) (*core.GapAnalysisResponse, error) {
	if s.gapResp != nil {
		cp := *s.gapResp
		return &cp, s.err
	}
	if s.gapReport != nil {
		return &core.GapAnalysisResponse{GapReport: *s.gapReport}, s.err
	}
	if s.genResp != nil {
		return &core.GapAnalysisResponse{
			GapReport: s.genResp.GapReport,
			Usage:     s.genResp.Usage,
			Telemetry: s.genResp.Telemetry,
		}, s.err
	}
	return nil, s.err
}

func (s *stubGenerator) SimulateATS(_ context.Context, _ core.ATSSimulationRequest) (*core.ATSSimulationResponse, error) {
	return &core.ATSSimulationResponse{}, s.err
}

func TestService_AnalyzeGaps(t *testing.T) {
	ctx := context.Background()

	t.Run("returns ErrNotFound for unknown branch", func(t *testing.T) {
		env := setupTestBranchEnv(t, 10)
		_, err := env.svc.AnalyzeGaps(ctx, env.defaultUser, "non-existent-branch")
		if !errors.Is(err, core.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("analyzes gaps via Generator and returns deterministic score", func(t *testing.T) {
		env := setupTestBranchEnv(t, 10)

		stubLLM := &stubGenerator{
			gapReport: &core.GapReport{
				OverallMatchSummary: "Decoupled fast analysis completed.",
				MissingRequirements: []core.GapItem{
					{
						Requirement:      "Kubernetes CRD controllers",
						MissingArea:      "Container Orchestration",
						Importance:       core.GapImportanceCritical,
						ClarifyingPrompt: "Have you authored custom controllers?",
					},
					{
						Requirement:      "Kafka event streams",
						MissingArea:      "Messaging",
						Importance:       core.GapImportancePreferred,
						ClarifyingPrompt: "Have you worked with high-throughput Kafka?",
					},
				},
				StrategicRecommendations: []string{"Highlight controller loops in summary."},
			},
		}

		// Rebuild svc with stubLLM
		svcWithLLM := branch.New(branch.Config{
			Branches:    env.branchRepo,
			Profiles:    env.profileRepo,
			JobEnqueuer: env.enqueuer,
			Validator:   provenance.New(),
			Renderer:    renderer.New(),
			Generator:   stubLLM,
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

	t.Run("returns error and releases quota when Generator returns a nil report without error", func(t *testing.T) {
		env := setupTestBranchEnv(t, 10)
		spyQuota := &spyQuotaRepo{}

		svcWithLLM := branch.New(branch.Config{
			Branches:    env.branchRepo,
			Profiles:    env.profileRepo,
			Quotas:      spyQuota,
			JobEnqueuer: env.enqueuer,
			Validator:   provenance.New(),
			Renderer:    renderer.New(),
			Generator:   &stubGenerator{}, // GenerateGapAnalysis returns (nil, nil)
		})

		report, err := svcWithLLM.AnalyzeGaps(ctx, env.defaultUser, env.branchID)
		if err == nil {
			t.Fatalf("expected error for nil gap report, got report %+v", report)
		}
		if report != nil {
			t.Errorf("expected nil report on error, got %+v", report)
		}

		spyQuota.mu.Lock()
		defer spyQuota.mu.Unlock()
		if len(spyQuota.releases) != 1 {
			t.Errorf("expected quota reservation to be released once, got %d releases", len(spyQuota.releases))
		}
		if len(spyQuota.commits) != 0 {
			t.Errorf("expected no committed usage for a failed analysis, got %d commits", len(spyQuota.commits))
		}
	})

	t.Run("normalizes identified_gaps before calculating deterministic match score", func(t *testing.T) {
		env := setupTestBranchEnv(t, 10)

		stubLLM := &stubGenerator{
			gapReport: &core.GapReport{
				OverallMatchSummary: "Report with only identified_gaps populated.",
				IdentifiedGaps: []core.GapItem{
					{
						Requirement: "Distributed consensus",
						Importance:  core.GapImportanceCritical, // -15
					},
					{
						Requirement: "GraphQL subscriptions",
						Importance:  core.GapImportancePreferred, // -7
					},
				},
			},
		}

		svcWithLLM := branch.New(branch.Config{
			Branches:  env.branchRepo,
			Profiles:  env.profileRepo,
			Generator: stubLLM,
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
		b := &core.Branch{
			ID:                 "branch-no-ver-" + uuid.New().String(),
			UserID:             env.defaultUser,
			Name:               "New Branch Without Versions",
			TargetRole:         "Backend Engineer",
			TargetRequirements: "Go, Kubernetes",
			TemplateSetID:      core.TemplateStandardCleanID,
		}
		created, err := env.svc.CreateBranch(ctx, b)
		if err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}

		stubLLM := &stubGenerator{
			gapReport: &core.GapReport{
				OverallMatchSummary: "Analysis for versionless branch",
				MissingRequirements: []core.GapItem{
					{Requirement: "Kubernetes", Importance: core.GapImportanceCritical},
				},
			},
		}

		svcWithLLM := branch.New(branch.Config{
			Branches:  env.branchRepo,
			Profiles:  env.profileRepo,
			Generator: stubLLM,
		})

		report, err := svcWithLLM.AnalyzeGaps(ctx, env.defaultUser, created.ID)
		if err != nil {
			t.Fatalf("AnalyzeGaps failed: %v", err)
		}

		// Reload branch from DB to verify persistence on branch record
		reloaded, err := env.svc.Branch(ctx, env.defaultUser, created.ID)
		if err != nil {
			t.Fatalf("Branch failed: %v", err)
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
		s := &core.Skill{
			ID:       "skill-go-test",
			UserID:   env.defaultUser,
			Name:     "Go",
			Category: "Languages",
		}
		if err := env.careerRepo.CreateSkill(ctx, s); err != nil {
			t.Fatalf("failed to create skill: %v", err)
		}

		// Update branch with TargetRequirements containing "Go"
		branchRec, err := env.svc.Branch(ctx, env.defaultUser, env.branchID)
		if err != nil {
			t.Fatalf("Branch failed: %v", err)
		}
		branchRec.TargetRequirements = "Must know Go, Distributed Systems, and Raft"
		if _, err := env.svc.UpdateBranch(ctx, branchRec); err != nil {
			t.Fatalf("UpdateBranch failed: %v", err)
		}

		stubLLM := &stubGenerator{
			gapReport: &core.GapReport{
				OverallMatchSummary: "Coverage test",
				MissingRequirements: []core.GapItem{
					{Requirement: "Raft consensus", Importance: core.GapImportanceCritical},
				},
			},
		}

		svcWithLLM := branch.New(branch.Config{
			Branches:  env.branchRepo,
			Profiles:  env.profileRepo,
			Generator: stubLLM,
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
		prof, _ := env.profileRepo.Profile(ctx, env.defaultUser)
		prof.FullName = realName
		prof.Email = "grace.hopper@navy.mil"
		_ = env.profileRepo.UpsertProfile(ctx, prof)

		stubLLM := &stubGenerator{
			gapReport: &core.GapReport{
				OverallMatchSummary: realName + " matches compiler design requirements.",
				StrategicRecommendations: []string{
					realName + " should highlight COBOL leadership.",
				},
				MissingRequirements: []core.GapItem{
					{
						Requirement:       "Machine code optimization",
						ClarifyingPrompt:  "Does " + realName + " have machine code experience?",
						RecommendedAction: "Consult with " + realName,
					},
				},
			},
		}

		svcWithLLM := branch.New(branch.Config{
			Branches:  env.branchRepo,
			Profiles:  env.profileRepo,
			Generator: stubLLM,
		})

		report, err := svcWithLLM.AnalyzeGaps(ctx, env.defaultUser, env.branchID)
		if err != nil {
			t.Fatalf("AnalyzeGaps failed: %v", err)
		}

		if strings.Contains(report.OverallMatchSummary, sanitizer.PlaceholderFullName) {
			t.Errorf("gap report leaked placeholder %q", sanitizer.PlaceholderFullName)
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

func TestService_GetPromptPreview_ContactSanitizer(t *testing.T) {
	ctx := context.Background()
	env := setupTestBranchEnv(t, 10)

	realName := "Barbara Liskov"
	realEmail := "liskov@csail.mit.edu"
	realPhone := "+1 (617) 253-5860"
	prof, _ := env.profileRepo.Profile(ctx, env.defaultUser)
	prof.FullName = realName
	prof.Email = realEmail
	prof.Phone = realPhone
	prof.Summary = realName + " created the Liskov substitution principle."
	prof.Profiles = []core.SocialLink{
		{Network: "Website", URL: "https://liskov.csail.mit.edu"},
	}
	_ = env.profileRepo.UpsertProfile(ctx, prof)

	preview, err := env.svc.PromptPreview(ctx, env.defaultUser, env.branchID, nil)
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

	if !strings.Contains(preview.UserPrompt, sanitizer.PlaceholderFullName) {
		t.Errorf("UserPrompt missing PlaceholderFullName %q", sanitizer.PlaceholderFullName)
	}
	if strings.Contains(preview.UserPrompt, sanitizer.PlaceholderEmail) {
		t.Errorf("UserPrompt should not contain stripped PlaceholderEmail %q", sanitizer.PlaceholderEmail)
	}
	if strings.Contains(preview.UserPrompt, sanitizer.PlaceholderPhone) {
		t.Errorf("UserPrompt should not contain stripped PlaceholderPhone %q", sanitizer.PlaceholderPhone)
	}
}

func TestService_GetPromptPreview_ZeroInternalLeakage(t *testing.T) {
	ctx := context.Background()
	env := setupTestBranchEnv(t, 10)

	preview, err := env.svc.PromptPreview(ctx, env.defaultUser, env.branchID, nil)
	if err != nil {
		t.Fatalf("GetPromptPreview failed: %v", err)
	}

	combined := preview.SystemPrompt + "\n" + preview.UserPrompt

	forbiddenTokens := []string{
		`"user_id"`,
		env.defaultUser,
		`"created_at"`,
		`"updated_at"`,
		`"is_global"`,
		`"metadata"`,
		`"main_revision"`,
		`"exported_at"`,
		`"default_provider"`,
		`"default_model"`,
		`"short_id"`,
	}

	for _, token := range forbiddenTokens {
		if strings.Contains(combined, token) {
			t.Errorf("GetPromptPreview leaked internal token %s into prompt", token)
		}
	}
}

func TestService_GetPromptPreview_UnconfiguredCompiler(t *testing.T) {
	ctx := context.Background()
	store := newSeededBranchStore(ctx, "user-no-compiler")
	b := &core.Branch{
		ID:            "branch-no-comp",
		UserID:        "user-no-compiler",
		Name:          "Branch No Compiler",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	if err := store.CreateBranch(ctx, b); err != nil {
		t.Fatalf("failed to create branch: %v", err)
	}

	svc := branch.New(branch.Config{
		Branches:       store,
		Profiles:       store,
		Presets:        store,
		Templates:      store,
		Quotas:         store,
		PromptCompiler: nil,
	})

	_, err := svc.PromptPreview(ctx, "user-no-compiler", b.ID, nil)
	if !errors.Is(err, core.ErrValidation) {
		t.Fatalf("expected ErrValidation when PromptCompiler is nil, got %v", err)
	}
}

func TestService_PromptPreview_WorkflowsBeyondTailoring(t *testing.T) {
	ctx := context.Background()
	env := setupTestBranchEnv(t, 10)

	renderedMD := "# Ada Lovelace\n- Built analytical engines in Go"
	v1, err := env.svc.Version(ctx, env.branchID, env.v1ID)
	if err != nil {
		t.Fatalf("Version v1 failed: %v", err)
	}
	v1.MarkdownOutput = renderedMD
	v1.ATSScore = &core.ATSScore{JobMatchingScore: 0.88}
	if err := env.branchRepo.CompleteVersion(ctx, v1); err != nil {
		t.Fatalf("CompleteVersion v1 failed: %v", err)
	}

	pendingVer := &core.BranchVersion{
		ID:       "ver-pending-preview",
		BranchID: env.branchID,
		Status:   core.VersionStatusPending,
	}
	if err := env.branchRepo.CreateVersion(ctx, pendingVer); err != nil {
		t.Fatalf("CreateVersion(pending) failed: %v", err)
	}

	explicitVer := &core.BranchVersion{
		ID:            "ver-explicit-preview",
		BranchID:      env.branchID,
		VersionNumber: 2,
		Status:        core.VersionStatusCompleted,
	}
	if err := env.branchRepo.CreateVersion(ctx, explicitVer); err != nil {
		t.Fatalf("CreateVersion(explicit) failed: %v", err)
	}
	explicitVer.PlainTextOutput = "Explicit plain text resume"
	explicitVer.HTMLOutput = "<h1>HTML resume output</h1>"
	if err := env.branchRepo.CompleteVersion(ctx, explicitVer); err != nil {
		t.Fatalf("CompleteVersion(explicit) failed: %v", err)
	}

	b, err := env.svc.Branch(ctx, env.defaultUser, env.branchID)
	if err != nil {
		t.Fatalf("Branch failed: %v", err)
	}
	b.ActiveVersionID = &v1.ID
	if _, err := env.svc.UpdateBranch(ctx, b); err != nil {
		t.Fatalf("UpdateBranch failed: %v", err)
	}

	// Update candidate name in profile so ATS matching preview test asserts PII scrubbing too.
	prof, err := env.profileRepo.Profile(ctx, env.defaultUser)
	if err != nil {
		t.Fatalf("Profile failed: %v", err)
	}
	prof.FullName = "Ada Lovelace"
	if err := env.profileRepo.UpsertProfile(ctx, prof); err != nil {
		t.Fatalf("UpsertProfile failed: %v", err)
	}

	noVersionBranch, err := env.svc.CreateBranch(ctx, &core.Branch{
		UserID:        env.defaultUser,
		Name:          "No Version Branch",
		TargetRole:    "Staff Engineer",
		TemplateSetID: core.TemplateStandardCleanID,
	})
	if err != nil {
		t.Fatalf("CreateBranch(noVersionBranch) failed: %v", err)
	}

	draftRole := "Principal Systems Engineer"
	draftReqs := "Strong Go concurrency experience"
	missingPresetID := "preset-does-not-exist"
	callerExtracted := &core.ATSExtractedCandidate{
		Summary: "Ada Lovelace engineered distributed systems",
		Skills:  nil,
	}

	cases := []struct {
		name           string
		branchID       string
		opts           *core.PromptPreviewOptions
		wantSysSub     string
		wantUserSub    string
		wantUserNotSub string
		wantErr        error
	}{
		{
			name:     "gap analysis workflow compiles with draft overrides",
			branchID: env.branchID,
			opts: &core.PromptPreviewOptions{
				Workflow:           core.PromptWorkflowGapAnalysis,
				TargetRole:         &draftRole,
				TargetRequirements: &draftReqs,
			},
			wantSysSub:  "Workflow: gap_analysis",
			wantUserSub: draftRole,
		},
		{
			name:     "ats extraction workflow compiles from completed branch version even when already scored",
			branchID: env.branchID,
			opts: &core.PromptPreviewOptions{
				Workflow: core.PromptWorkflowATSExtraction,
				Format:   string(core.OutputFormatMarkdown),
			},
			wantSysSub:  "Workflow: ats_extraction",
			wantUserSub: "Built analytical engines in Go",
		},
		{
			name:     "ats extraction workflow supports explicit VersionID override",
			branchID: env.branchID,
			opts: &core.PromptPreviewOptions{
				Workflow:  core.PromptWorkflowATSExtraction,
				VersionID: explicitVer.ID,
				Format:    string(core.OutputFormatPlainText),
			},
			wantSysSub:  "Format: text",
			wantUserSub: "Explicit plain text resume",
		},
		{
			name:     "ats extraction workflow normalizes format aliases on saved versions",
			branchID: env.branchID,
			opts: &core.PromptPreviewOptions{
				Workflow:  core.PromptWorkflowATSExtraction,
				VersionID: explicitVer.ID,
				Format:    "txt",
			},
			wantSysSub:  "Format: text",
			wantUserSub: "Explicit plain text resume",
		},
		{
			name:     "ats extraction workflow normalizes HTML format case on saved versions",
			branchID: env.branchID,
			opts: &core.PromptPreviewOptions{
				Workflow:  core.PromptWorkflowATSExtraction,
				VersionID: explicitVer.ID,
				Format:    "HTML",
			},
			wantSysSub:  "Format: html",
			wantUserSub: "<h1>HTML resume output</h1>",
		},
		{
			name:     "ats extraction workflow rejects unsupported format on saved versions",
			branchID: env.branchID,
			opts: &core.PromptPreviewOptions{
				Workflow: core.PromptWorkflowATSExtraction,
				Format:   "docx",
			},
			wantErr: core.ErrValidation,
		},
		{
			name:     "ats extraction workflow compiles from draft resume text",
			branchID: env.branchID,
			opts: &core.PromptPreviewOptions{
				Workflow:   core.PromptWorkflowATSExtraction,
				Format:     string(core.OutputFormatPlainText),
				ResumeText: "Draft resume plain text",
			},
			wantSysSub:  "Format: text",
			wantUserSub: "Draft resume plain text",
		},
		{
			name:     "ats extraction workflow rejects pending version",
			branchID: env.branchID,
			opts: &core.PromptPreviewOptions{
				Workflow:  core.PromptWorkflowATSExtraction,
				VersionID: pendingVer.ID,
			},
			wantErr: core.ErrValidation,
		},
		{
			name:     "ats extraction workflow rejects branch missing both version and draft text",
			branchID: noVersionBranch.ID,
			opts: &core.PromptPreviewOptions{
				Workflow: core.PromptWorkflowATSExtraction,
			},
			wantErr: core.ErrValidation,
		},
		{
			name:     "ats extraction workflow rejects unsupported draft format",
			branchID: env.branchID,
			opts: &core.PromptPreviewOptions{
				Workflow:   core.PromptWorkflowATSExtraction,
				Format:     "docx",
				ResumeText: "Draft resume text",
			},
			wantErr: core.ErrValidation,
		},
		{
			name:     "ats matching workflow compiles and scrubs PII without mutating caller",
			branchID: env.branchID,
			opts: &core.PromptPreviewOptions{
				Workflow:           core.PromptWorkflowATSMatching,
				TargetRequirements: &draftReqs,
				ExtractedCandidate: callerExtracted,
			},
			wantSysSub:     "Workflow: ats_matching",
			wantUserSub:    sanitizer.PlaceholderFullName + " engineered distributed systems",
			wantUserNotSub: "Ada Lovelace",
		},
		{
			name:     "explicit missing preset override returns error",
			branchID: env.branchID,
			opts: &core.PromptPreviewOptions{
				PresetID: &missingPresetID,
			},
			wantErr: core.ErrNotFound,
		},
		{
			name:     "unsupported workflow returns validation error",
			branchID: env.branchID,
			opts: &core.PromptPreviewOptions{
				Workflow: "unsupported",
			},
			wantErr: core.ErrValidation,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev, err := env.svc.PromptPreview(ctx, env.defaultUser, tc.branchID, tc.opts)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("PromptPreview failed: %v", err)
			}
			if !strings.Contains(prev.SystemPrompt, tc.wantSysSub) {
				t.Errorf("SystemPrompt missing %q:\n%s", tc.wantSysSub, prev.SystemPrompt)
			}
			if !strings.Contains(prev.UserPrompt, tc.wantUserSub) {
				t.Errorf("UserPrompt missing %q:\n%s", tc.wantUserSub, prev.UserPrompt)
			}
			if tc.wantUserNotSub != "" && strings.Contains(prev.UserPrompt, tc.wantUserNotSub) {
				t.Errorf("UserPrompt unexpectedly contained %q:\n%s", tc.wantUserNotSub, prev.UserPrompt)
			}
		})
	}

	if callerExtracted.Skills != nil || callerExtracted.Summary != "Ada Lovelace engineered distributed systems" {
		t.Errorf("PromptPreview mutated caller ExtractedCandidate: %+v", callerExtracted)
	}
}

type crossTenantPresetReader struct {
	preset core.Preset
}

func (r *crossTenantPresetReader) Preset(_ context.Context, _, _ string) (*core.Preset, error) {
	cp := r.preset
	return &cp, nil
}

func TestService_PromptPreview_RejectsCrossTenantPreset(t *testing.T) {
	ctx := context.Background()
	store := newSeededBranchStore(ctx, "user-preset")
	b := &core.Branch{
		ID:            "branch-preset-check",
		UserID:        "user-preset",
		Name:          "Branch Preset Check",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	if err := store.CreateBranch(ctx, b); err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}
	otherPresetID := "preset-other-tenant"
	svc := branch.New(branch.Config{
		Branches:       store,
		Profiles:       store,
		Presets:        &crossTenantPresetReader{preset: core.Preset{ID: otherPresetID, UserID: "other-user", SystemInstructions: "SECRET"}},
		Templates:      store,
		Quotas:         store,
		PromptCompiler: &fakePromptCompiler{},
	})

	_, err := svc.PromptPreview(ctx, "user-preset", b.ID, &core.PromptPreviewOptions{
		PresetID: &otherPresetID,
	})
	if !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("expected ErrForbidden for cross-tenant preset, got %v", err)
	}
}

type spyQuotaRepo struct {
	mu           sync.Mutex
	reservations []struct {
		UserID          string
		Op              core.LLMOperationType
		EstimatedTokens int
		DailyLimit      int
		MonthlyLimit    int
	}
	commits []struct {
		ReservationID string
		Telemetry     core.LLMTelemetry
	}
	releases []struct {
		ReservationID string
		Reason        string
		Telemetry     core.LLMTelemetry
	}
}

func (s *spyQuotaRepo) ReserveQuota(_ context.Context, userID string, op core.LLMOperationType, estimatedTokens, dailyLimit, monthlyLimit int) (*core.LLMUsageRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reservations = append(s.reservations, struct {
		UserID          string
		Op              core.LLMOperationType
		EstimatedTokens int
		DailyLimit      int
		MonthlyLimit    int
	}{
		UserID:          userID,
		Op:              op,
		EstimatedTokens: estimatedTokens,
		DailyLimit:      dailyLimit,
		MonthlyLimit:    monthlyLimit,
	})
	return &core.LLMUsageRecord{
		ID:              "res-test-1",
		UserID:          userID,
		Operation:       op,
		EstimatedTokens: estimatedTokens,
		Status:          core.ReservationStatusPending,
	}, nil
}

func (s *spyQuotaRepo) Quota(_ context.Context, userID string) (*core.TenantQuota, error) {
	return &core.TenantQuota{UserID: userID}, nil
}

func (s *spyQuotaRepo) CommitUsage(_ context.Context, reservationID string, telemetry core.LLMTelemetry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commits = append(s.commits, struct {
		ReservationID string
		Telemetry     core.LLMTelemetry
	}{
		ReservationID: reservationID,
		Telemetry:     telemetry,
	})
	return nil
}

func (s *spyQuotaRepo) ReleaseQuota(_ context.Context, reservationID string, reason string, telemetry core.LLMTelemetry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases = append(s.releases, struct {
		ReservationID string
		Reason        string
		Telemetry     core.LLMTelemetry
	}{
		ReservationID: reservationID,
		Reason:        reason,
		Telemetry:     telemetry,
	})
	return nil
}

func (s *spyQuotaRepo) ListUsageRecords(_ context.Context, _ string, _ int) ([]core.LLMUsageRecord, error) {
	return nil, nil
}

func TestBranchService_ConfigurableQuotaLimits(t *testing.T) {
	ctx := context.Background()
	defaultUser := "user-custom-quota"
	store := newSeededBranchStore(ctx, defaultUser)

	b := &core.Branch{
		ID:                 "branch-quota-test",
		UserID:             defaultUser,
		Name:               "DevOps Lead",
		TargetRequirements: "Go, Kubernetes, Terraform",
		TemplateSetID:      core.TemplateStandardCleanID,
	}
	err := store.CreateBranch(ctx, b)
	if err != nil {
		t.Fatalf("failed to create branch: %v", err)
	}

	vQuota1 := &core.BranchVersion{
		ID:               "ver-quota-ats-1",
		BranchID:         b.ID,
		MainRevisionUsed: 1,
		Status:           core.VersionStatusCompleted,
		MarkdownOutput:   "# DevOps Resume\nGo, Kubernetes, Terraform",
	}
	if err := store.CreateVersion(ctx, vQuota1); err != nil {
		t.Fatalf("failed to create vQuota1: %v", err)
	}
	if err := store.CompleteVersion(ctx, vQuota1); err != nil {
		t.Fatalf("failed to complete vQuota1: %v", err)
	}

	vQuota2 := &core.BranchVersion{
		ID:               "ver-quota-ats-2",
		BranchID:         b.ID,
		MainRevisionUsed: 1,
		Status:           core.VersionStatusCompleted,
		MarkdownOutput:   "# DevOps Resume v2\nGo, Kubernetes, Terraform",
	}
	if err := store.CreateVersion(ctx, vQuota2); err != nil {
		t.Fatalf("failed to create vQuota2: %v", err)
	}
	if err := store.CompleteVersion(ctx, vQuota2); err != nil {
		t.Fatalf("failed to complete vQuota2: %v", err)
	}

	stubLLM := &stubATSSimulator{
		stubGenerator: stubGenerator{
			gapReport: &core.GapReport{OverallMatchSummary: "Quota test gap report"},
		},
	}

	t.Run("Custom limits propagated across GenerateVersion, SyncBranches, AnalyzeGaps, and SimulateATS", func(t *testing.T) {
		spyQuota := &spyQuotaRepo{}
		enqueuer := newStubEnqueuer(10)

		svc := branch.New(branch.Config{
			Branches:          store,
			Profiles:          store,
			Presets:           store,
			Templates:         store,
			Quotas:            spyQuota,
			JobEnqueuer:       enqueuer,
			Generator:         stubLLM,
			DailyRequestLimit: 25,
			MonthlyTokenLimit: 250000,
		})

		// 1. GenerateVersion
		_, err := svc.GenerateVersion(ctx, defaultUser, b.ID)
		if err != nil {
			t.Fatalf("GenerateVersion failed: %v", err)
		}

		// 2. SyncBranches
		_, err = svc.SyncBranches(ctx, defaultUser, []string{b.ID})
		if err != nil {
			t.Fatalf("SyncBranches failed: %v", err)
		}

		// 3. AnalyzeGaps
		_, err = svc.AnalyzeGaps(ctx, defaultUser, b.ID)
		if err != nil {
			t.Fatalf("AnalyzeGaps failed: %v", err)
		}

		// 4. SimulateATS
		_, err = svc.SimulateATS(ctx, defaultUser, b.ID, &core.ATSSimulationOptions{VersionID: vQuota1.ID})
		if err != nil {
			t.Fatalf("SimulateATS failed: %v", err)
		}

		spyQuota.mu.Lock()
		defer spyQuota.mu.Unlock()

		if len(spyQuota.reservations) != 4 {
			t.Fatalf("expected 4 reservations, got %d", len(spyQuota.reservations))
		}

		for i, res := range spyQuota.reservations {
			if res.DailyLimit != 25 {
				t.Errorf("reservation %d: expected DailyLimit 25, got %d", i, res.DailyLimit)
			}
			if res.MonthlyLimit != 250000 {
				t.Errorf("reservation %d: expected MonthlyLimit 250000, got %d", i, res.MonthlyLimit)
			}
		}
	})

	t.Run("Fallback default limits 50 and 1000000 applied when limits are <= 0", func(t *testing.T) {
		spyQuota := &spyQuotaRepo{}
		enqueuer := newStubEnqueuer(10)

		svcDefault := branch.New(branch.Config{
			Branches:          store,
			Profiles:          store,
			Presets:           store,
			Templates:         store,
			Quotas:            spyQuota,
			JobEnqueuer:       enqueuer,
			Generator:         stubLLM,
			DailyRequestLimit: 0,
			MonthlyTokenLimit: -100,
		})

		_, err := svcDefault.GenerateVersion(ctx, defaultUser, b.ID)
		if err != nil {
			t.Fatalf("GenerateVersion failed: %v", err)
		}

		_, err = svcDefault.SyncBranches(ctx, defaultUser, []string{b.ID})
		if err != nil {
			t.Fatalf("SyncBranches failed: %v", err)
		}

		_, err = svcDefault.AnalyzeGaps(ctx, defaultUser, b.ID)
		if err != nil {
			t.Fatalf("AnalyzeGaps failed: %v", err)
		}

		_, err = svcDefault.SimulateATS(ctx, defaultUser, b.ID, &core.ATSSimulationOptions{VersionID: vQuota2.ID})
		if err != nil {
			t.Fatalf("SimulateATS failed: %v", err)
		}

		spyQuota.mu.Lock()
		defer spyQuota.mu.Unlock()

		if len(spyQuota.reservations) != 4 {
			t.Fatalf("expected 4 reservations, got %d", len(spyQuota.reservations))
		}

		for i, res := range spyQuota.reservations {
			if res.DailyLimit != 50 {
				t.Errorf("reservation %d: expected fallback DailyLimit 50, got %d", i, res.DailyLimit)
			}
			if res.MonthlyLimit != 1000000 {
				t.Errorf("reservation %d: expected fallback MonthlyLimit 1000000, got %d", i, res.MonthlyLimit)
			}
		}
	})
}

type stubATSSimulator struct {
	stubGenerator
	mu      sync.Mutex
	atsResp *core.ATSSimulationResponse
	atsErr  error
	delay   time.Duration
	calls   int
	lastReq core.ATSSimulationRequest
}

func (s *stubATSSimulator) SimulateATS(_ context.Context, req core.ATSSimulationRequest) (*core.ATSSimulationResponse, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.lastReq = req
	if s.atsErr != nil {
		if s.atsResp != nil {
			cp := *s.atsResp
			return &cp, s.atsErr
		}
		return nil, s.atsErr
	}
	if s.atsResp != nil {
		cp := *s.atsResp
		return &cp, nil
	}
	return &core.ATSSimulationResponse{
		Score: core.ATSScore{
			ExtractionConfidenceScore: 93.0,
			JobMatchingScore:          87.0,
			FormatUsed:                req.FormatUsed,
			ExtractionSummary:         "High confidence extraction",
			MatchingSummary:           "Strong job match",
		},
		Usage: core.TokenUsage{TotalTokens: 400},
	}, nil
}

type failingSnapshotProfileRepo struct {
	core.ProfileStore
	snapErr error
}

func (f *failingSnapshotProfileRepo) Snapshot(ctx context.Context, userID string) (*core.MainProfileSnapshot, error) {
	if f.snapErr != nil {
		return nil, f.snapErr
	}
	return f.ProfileStore.Snapshot(ctx, userID)
}

func TestService_SimulateATS(t *testing.T) {
	ctx := context.Background()
	env := setupTestBranchEnv(t, 10)

	t.Run("Rejects Default Main Branch", func(t *testing.T) {
		branches, err := env.svc.ListBranches(ctx, env.defaultUser, false)
		if err != nil {
			t.Fatalf("ListBranches failed: %v", err)
		}
		var defaultBranchID string
		for _, b := range branches {
			if b.IsDefault {
				defaultBranchID = b.ID
				break
			}
		}
		if defaultBranchID == "" {
			t.Fatal("expected seeded default branch")
		}

		_, err = env.svc.SimulateATS(ctx, env.defaultUser, defaultBranchID, nil)
		if !errors.Is(err, core.ErrDefaultBranchImmutable) {
			t.Errorf("expected ErrDefaultBranchImmutable for default branch, got %v", err)
		}
	})

	t.Run("Rejects tailored branch when Job Description is missing", func(t *testing.T) {
		noJDBranch := &core.Branch{
			ID:                 "branch-no-jd",
			UserID:             env.defaultUser,
			Name:               "No JD Branch",
			TargetRole:         "Backend Engineer",
			TargetRequirements: "   ",
			TemplateSetID:      core.TemplateStandardCleanID,
		}
		if _, err := env.svc.CreateBranch(ctx, noJDBranch); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}

		_, err := env.svc.SimulateATS(ctx, env.defaultUser, noJDBranch.ID, nil)
		if !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation when job description is missing, got %v", err)
		}
	})

	t.Run("Rejects when version is not completed", func(t *testing.T) {
		b, err := env.svc.Branch(ctx, env.defaultUser, env.branchID)
		if err != nil {
			t.Fatalf("Branch failed: %v", err)
		}
		b.TargetRequirements = "Strong Go, Kubernetes, and distributed systems architecture."
		if _, err := env.svc.UpdateBranch(ctx, b); err != nil {
			t.Fatalf("UpdateBranch failed: %v", err)
		}

		pendingVer := &core.BranchVersion{
			ID:       "ver-pending-ats",
			BranchID: env.branchID,
			Status:   core.VersionStatusPending,
		}
		if err := env.branchRepo.CreateVersion(ctx, pendingVer); err != nil {
			t.Fatalf("CreateVersion failed: %v", err)
		}

		_, err = env.svc.SimulateATS(ctx, env.defaultUser, env.branchID, &core.ATSSimulationOptions{
			VersionID: pendingVer.ID,
		})
		if !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation for pending version, got %v", err)
		}
	})

	t.Run("Triggers once per tailored version and persists across active version switches", func(t *testing.T) {
		v1, err := env.svc.Version(ctx, env.branchID, env.v1ID)
		if err != nil {
			t.Fatalf("GetVersion v1 failed: %v", err)
		}
		v1.MarkdownOutput = "# Jane Doe\n## Summary\nLead Systems Architect skilled in Go and Kubernetes."
		if err := env.branchRepo.CompleteVersion(ctx, v1); err != nil {
			t.Fatalf("CompleteVersion v1 failed: %v", err)
		}

		sim := &stubATSSimulator{}
		svc := branch.New(branch.Config{
			Branches:  env.branchRepo,
			Profiles:  env.profileRepo,
			Templates: env.templateRepo,
			Renderer:  renderer.New(),
			Generator: sim,
		})

		// First trigger on v1 succeeds
		score, err := svc.SimulateATS(ctx, env.defaultUser, env.branchID, &core.ATSSimulationOptions{
			Format: "markdown",
		})
		if err != nil {
			t.Fatalf("SimulateATS failed on first trigger: %v", err)
		}
		if score.ExtractionConfidenceScore != 93.0 || score.JobMatchingScore != 87.0 {
			t.Errorf("unexpected score values: %+v", score)
		}
		if sim.calls != 1 {
			t.Errorf("expected 1 LLM simulator call, got %d", sim.calls)
		}

		// Second trigger on the same version (v1) must fail with ErrConflict (triggers once per tailored version)
		_, err = svc.SimulateATS(ctx, env.defaultUser, env.branchID, nil)
		if !errors.Is(err, core.ErrConflict) {
			t.Errorf("expected ErrConflict on duplicate ATS trigger for same version, got %v", err)
		}
		if sim.calls != 1 {
			t.Errorf("expected simulator not to be called again on conflict, got %d calls", sim.calls)
		}

		// Verify parent branch has v1's ATS score
		bAfterV1, err := svc.Branch(ctx, env.defaultUser, env.branchID)
		if err != nil {
			t.Fatalf("Branch failed: %v", err)
		}
		if bAfterV1.ATSScore == nil || bAfterV1.ATSScore.JobMatchingScore != 87.0 {
			t.Errorf("expected branch ATSScore 87.0, got %+v", bAfterV1.ATSScore)
		}

		// Create and complete v2 (newly tailored version has nil ATSScore)
		v2 := &core.BranchVersion{
			ID:               "ver-ats-switch-2",
			BranchID:         env.branchID,
			MainRevisionUsed: 2,
			Status:           core.VersionStatusCompleted,
			MarkdownOutput:   "# Tailored v2\n## Summary\nCloud architect",
			HTMLOutput:       "<h1>Tailored v2</h1>",
			PlainTextOutput:  "Tailored v2",
		}
		if err := env.branchRepo.CreateVersion(ctx, v2); err != nil {
			t.Fatalf("CreateVersion v2 failed: %v", err)
		}
		if err := env.branchRepo.CompleteVersion(ctx, v2); err != nil {
			t.Fatalf("CompleteVersion v2 failed: %v", err)
		}

		bOnV2, err := svc.Branch(ctx, env.defaultUser, env.branchID)
		if err != nil {
			t.Fatalf("Branch on v2 failed: %v", err)
		}
		if bOnV2.ATSScore != nil {
			t.Errorf("expected branch ATSScore to be nil when newly completed v2 is active, got %+v", bOnV2.ATSScore)
		}

		// Switch active version back to v1 via UpdateBranch -> branch.ATSScore restores to v1's persisted score!
		v1ID := env.v1ID
		bOnV2.ActiveVersionID = &v1ID
		bSwitchedToV1, err := svc.UpdateBranch(ctx, bOnV2)
		if err != nil {
			t.Fatalf("UpdateBranch switch to v1 failed: %v", err)
		}
		if bSwitchedToV1.ATSScore == nil || bSwitchedToV1.ATSScore.ExtractionConfidenceScore != 93.0 || bSwitchedToV1.ATSScore.JobMatchingScore != 87.0 {
			t.Errorf("expected branch ATSScore restored to v1's (93.0, 87.0) after switching active version, got %+v", bSwitchedToV1.ATSScore)
		}
	})

	t.Run("Rejects unsupported Format with ErrValidation", func(t *testing.T) {
		_, err := env.svc.SimulateATS(ctx, env.defaultUser, env.branchID, &core.ATSSimulationOptions{
			Format: "docx",
		})
		if !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation for unsupported format 'docx', got %v", err)
		}
	})

	t.Run("Propagates Snapshot error to prevent cleartext PII transmission", func(t *testing.T) {
		vSnap := &core.BranchVersion{
			ID:               "ver-ats-snap-err",
			BranchID:         env.branchID,
			MainRevisionUsed: 2,
			Status:           core.VersionStatusCompleted,
			MarkdownOutput:   "# Jane Doe\nGo Engineer",
		}
		if err := env.branchRepo.CreateVersion(ctx, vSnap); err != nil {
			t.Fatalf("CreateVersion failed: %v", err)
		}
		if err := env.branchRepo.CompleteVersion(ctx, vSnap); err != nil {
			t.Fatalf("CompleteVersion failed: %v", err)
		}

		sim := &stubATSSimulator{}
		svcSnapErr := branch.New(branch.Config{
			Branches: env.branchRepo,
			Profiles: &failingSnapshotProfileRepo{
				ProfileStore: env.profileRepo,
				snapErr:      errors.New("db connection lost"),
			},
			Generator: sim,
		})

		_, err := svcSnapErr.SimulateATS(ctx, env.defaultUser, env.branchID, &core.ATSSimulationOptions{
			VersionID: vSnap.ID,
		})
		if err == nil || !strings.Contains(err.Error(), "failed to load profile snapshot") {
			t.Fatalf("expected snapshot error to abort SimulateATS, got %v", err)
		}
		if sim.calls != 0 {
			t.Errorf("expected LLM simulator not to be called when snapshot load fails, got %d calls", sim.calls)
		}
	})

	t.Run("Commits quota usage (including 800 fallback tokens) and releases quota on LLM failure", func(t *testing.T) {
		vQuotaCommit := &core.BranchVersion{
			ID:               "ver-ats-quota-commit",
			BranchID:         env.branchID,
			MainRevisionUsed: 2,
			Status:           core.VersionStatusCompleted,
			MarkdownOutput:   "# Jane Doe\nGo Engineer",
		}
		if err := env.branchRepo.CreateVersion(ctx, vQuotaCommit); err != nil {
			t.Fatalf("CreateVersion failed: %v", err)
		}
		if err := env.branchRepo.CompleteVersion(ctx, vQuotaCommit); err != nil {
			t.Fatalf("CompleteVersion failed: %v", err)
		}

		spyQuota := &spyQuotaRepo{}
		simZeroTokens := &stubATSSimulator{
			atsResp: &core.ATSSimulationResponse{
				Score: core.ATSScore{
					ExtractionConfidenceScore: 90.0,
					JobMatchingScore:          85.0,
				},
				Usage: core.TokenUsage{TotalTokens: 0},
			},
		}
		svcQuota := branch.New(branch.Config{
			Branches:  env.branchRepo,
			Profiles:  env.profileRepo,
			Quotas:    spyQuota,
			Generator: simZeroTokens,
		})

		if _, err := svcQuota.SimulateATS(ctx, env.defaultUser, env.branchID, &core.ATSSimulationOptions{
			VersionID: vQuotaCommit.ID,
		}); err != nil {
			t.Fatalf("SimulateATS failed: %v", err)
		}
		if len(spyQuota.commits) != 1 || spyQuota.commits[0].Telemetry.Usage.TotalTokens != 800 {
			t.Errorf("expected 1 quota commit with fallback 800 tokens, got %+v", spyQuota.commits)
		}

		// Now test LLM failure releases quota reservation
		vQuotaFail := &core.BranchVersion{
			ID:               "ver-ats-quota-fail",
			BranchID:         env.branchID,
			MainRevisionUsed: 2,
			Status:           core.VersionStatusCompleted,
			MarkdownOutput:   "# Jane Doe\nGo Engineer",
		}
		if err := env.branchRepo.CreateVersion(ctx, vQuotaFail); err != nil {
			t.Fatalf("CreateVersion failed: %v", err)
		}
		if err := env.branchRepo.CompleteVersion(ctx, vQuotaFail); err != nil {
			t.Fatalf("CompleteVersion failed: %v", err)
		}

		simErr := &stubATSSimulator{atsErr: errors.New("upstream llm failure")}
		svcQuotaFail := branch.New(branch.Config{
			Branches:  env.branchRepo,
			Profiles:  env.profileRepo,
			Quotas:    spyQuota,
			Generator: simErr,
		})

		if _, err := svcQuotaFail.SimulateATS(ctx, env.defaultUser, env.branchID, &core.ATSSimulationOptions{
			VersionID: vQuotaFail.ID,
		}); err == nil {
			t.Fatal("expected error from failing LLM simulator")
		}
		if len(spyQuota.releases) != 1 {
			t.Errorf("expected 1 quota release on LLM failure, got %+v", spyQuota.releases)
		}
	})

	t.Run("Renders missing HTML format on the fly when only MarkdownOutput is cached", func(t *testing.T) {
		vPartial := &core.BranchVersion{
			ID:                "ver-ats-partial-render",
			BranchID:          env.branchID,
			MainRevisionUsed:  2,
			Status:            core.VersionStatusCompleted,
			TemplateSetIDUsed: core.TemplateStandardCleanID,
			MarkdownOutput:    "# Cached Markdown Only",
			HTMLOutput:        "",
			TailoredContent: &core.TailoredContent{
				Summary: "Distributed systems architect",
			},
		}
		if err := env.branchRepo.CreateVersion(ctx, vPartial); err != nil {
			t.Fatalf("CreateVersion failed: %v", err)
		}
		if err := env.branchRepo.CompleteVersion(ctx, vPartial); err != nil {
			t.Fatalf("CompleteVersion failed: %v", err)
		}

		sim := &stubATSSimulator{}
		svcRender := branch.New(branch.Config{
			Branches:  env.branchRepo,
			Profiles:  env.profileRepo,
			Templates: env.templateRepo,
			Renderer:  renderer.New(),
			Generator: sim,
		})

		score, err := svcRender.SimulateATS(ctx, env.defaultUser, env.branchID, &core.ATSSimulationOptions{
			VersionID: vPartial.ID,
			Format:    "html",
		})
		if err != nil {
			t.Fatalf("SimulateATS with html format failed: %v", err)
		}
		if score.FormatUsed != "html" {
			t.Errorf("expected FormatUsed 'html', got %q", score.FormatUsed)
		}
		if !strings.Contains(sim.lastReq.ResumeText, "<") {
			t.Errorf("expected rendered HTML resume text to be passed to simulator, got %q", sim.lastReq.ResumeText)
		}
	})

	t.Run("Falls back deterministically when Generator is nil and formats Projects and Certifications", func(t *testing.T) {
		vDet := &core.BranchVersion{
			ID:               "ver-ats-deterministic",
			BranchID:         env.branchID,
			MainRevisionUsed: 2,
			Status:           core.VersionStatusCompleted,
			TailoredContent: &core.TailoredContent{
				Summary: "Distributed Go and Kubernetes engineer",
				Projects: []core.TailoredProject{
					{
						Name:    "RaftKV",
						Role:    "Creator",
						Summary: "Consensus store in Go",
						Bullets: []core.TailoredBullet{{Text: "Implemented Raft leader election"}},
					},
				},
				Certifications: []core.TailoredCert{
					{
						Name:   "Certified Kubernetes Administrator",
						Issuer: "CNCF",
					},
				},
			},
		}
		if err := env.branchRepo.CreateVersion(ctx, vDet); err != nil {
			t.Fatalf("CreateVersion failed: %v", err)
		}
		if err := env.branchRepo.CompleteVersion(ctx, vDet); err != nil {
			t.Fatalf("CompleteVersion failed: %v", err)
		}

		svcDet := branch.New(branch.Config{
			Branches:  env.branchRepo,
			Profiles:  env.profileRepo,
			Generator: nil,
		})

		score, err := svcDet.SimulateATS(ctx, env.defaultUser, env.branchID, &core.ATSSimulationOptions{
			VersionID: vDet.ID,
		})
		if err != nil {
			t.Fatalf("deterministic SimulateATS failed: %v", err)
		}
		if score.ExtractionConfidenceScore <= 0 || score.JobMatchingScore <= 0 {
			t.Errorf("expected positive deterministic ATS scores, got %+v", score)
		}
		if score.EvaluatedAt == nil {
			t.Errorf("expected EvaluatedAt to be populated on deterministic score")
		}
	})

	t.Run("Rejects concurrent in-flight SimulateATS requests for the same version with ErrConflict", func(t *testing.T) {
		vConc := &core.BranchVersion{
			ID:               "ver-ats-concurrent",
			BranchID:         env.branchID,
			MainRevisionUsed: 2,
			Status:           core.VersionStatusCompleted,
			MarkdownOutput:   "# Concurrent Test\nGo and Kubernetes",
		}
		if err := env.branchRepo.CreateVersion(ctx, vConc); err != nil {
			t.Fatalf("CreateVersion failed: %v", err)
		}
		if err := env.branchRepo.CompleteVersion(ctx, vConc); err != nil {
			t.Fatalf("CompleteVersion failed: %v", err)
		}

		simSlow := &stubATSSimulator{
			delay: 80 * time.Millisecond,
		}
		svcConc := branch.New(branch.Config{
			Branches:  env.branchRepo,
			Profiles:  env.profileRepo,
			Generator: simSlow,
		})

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				_, errs[idx] = svcConc.SimulateATS(ctx, env.defaultUser, env.branchID, &core.ATSSimulationOptions{
					VersionID: vConc.ID,
				})
			}(i)
		}
		wg.Wait()

		successCount := 0
		conflictCount := 0
		for _, e := range errs {
			switch {
			case e == nil:
				successCount++
			case errors.Is(e, core.ErrConflict):
				conflictCount++
			default:
				t.Errorf("unexpected error in concurrent SimulateATS: %v", e)
			}
		}
		if successCount != 1 || conflictCount != 1 {
			t.Errorf("expected exactly 1 success and 1 ErrConflict in concurrent SimulateATS, got success=%d conflict=%d (errs=%v)", successCount, conflictCount, errs)
		}
		if simSlow.calls != 1 {
			t.Errorf("expected LLM simulator to be invoked only once across concurrent requests, got %d", simSlow.calls)
		}
	})
}
