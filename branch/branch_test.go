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

	"github.com/resumeee-app/core"
	"github.com/resumeee-app/core/branch"
	"github.com/resumeee-app/core/provenance"
	"github.com/resumeee-app/core/renderer"
	"github.com/resumeee-app/core/sanitizer"
	"github.com/resumeee-app/core/store"
)

type testBranchEnv struct {
	db           *testBranchStore
	svc          *branch.Service
	branchRepo   *store.Branches
	jobRepo      *store.Jobs
	profileRepo  *store.Profiles
	careerRepo   *store.Career
	templateRepo *store.Templates
	notifier     *stubNotifier
	defaultUser  string
	branchID     string
	v1ID         string
	validFactID  string
	validExpID   string
	validSkillID string
}

func setupTestBranchEnv(t *testing.T, _ int) *testBranchEnv {
	t.Helper()

	ctx := context.Background()
	defaultUser := "default"
	st := newSeededBranchStore(ctx, defaultUser)

	profileRepo := st.Profiles()
	templateRepo := st.Templates()
	branchRepo := st.Branches()
	jobRepo := st.Jobs()
	val := provenance.New()
	rend := renderer.New()
	notifier := &stubNotifier{}

	svc := branch.New(
		branch.WithStore(st),
		branch.WithJobNotifier(notifier),
		branch.WithValidator(val),
		branch.WithRenderer(rend),
		branch.WithPromptCompiler(&fakePromptCompiler{}),
	)

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
		UserID:            defaultUser,
		BranchID:          created.ID,
		Status:            core.VersionStatusCompleted,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		SourceType:        core.SourceTypeLLMGeneration,
	}
	if err := st.seedCompletedVersion(ctx, v1); err != nil {
		t.Fatalf("failed to seed v1: %v", err)
	}

	// Seed an experience and fact so manual edits have valid provenance
	exp := &core.WorkExperience{
		ID:        "exp-test-1",
		UserID:    defaultUser,
		Company:   "Acme Corp",
		RoleTitle: "Lead Systems Architect",
		StartDate: core.NewDate(2020, 1, 1),
	}
	if err := st.Career().CreateExperience(ctx, exp); err != nil {
		t.Fatalf("failed to create experience: %v", err)
	}

	fact := &core.Fact{
		ID:        "fact-test-1",
		UserID:    defaultUser,
		OwnerID:   exp.ID,
		OwnerType: core.OwnerTypeWorkExperience,
		Statement: "Optimized distributed consensus throughput by 40%.",
	}
	if err := st.Career().CreateFact(ctx, fact); err != nil {
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
		db:           st,
		svc:          svc,
		branchRepo:   branchRepo,
		jobRepo:      jobRepo,
		profileRepo:  profileRepo,
		careerRepo:   st.Career(),
		templateRepo: templateRepo,
		notifier:     notifier,
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
	ctx := context.Background()
	defaultUser := "default-backpressure"
	fixedNow := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	store := newSeededBranchStoreWithClock(ctx, defaultUser, func() time.Time { return fixedNow })
	notifier := &stubNotifier{}

	svc := branch.New(
		branch.WithStore(store),
		branch.WithJobNotifier(notifier),
		branch.WithValidator(provenance.New()),
		branch.WithRenderer(renderer.New()),
		branch.WithPromptCompiler(&fakePromptCompiler{}),
		branch.WithMaxPendingPerTenant(1),
		branch.WithJobTTL(15*time.Minute),
		branch.WithClock(func() time.Time { return fixedNow }),
	)

	b1 := &core.Branch{
		ID:            "branch-bp-1",
		UserID:        defaultUser,
		Name:          "Backend Architect",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	b2 := &core.Branch{
		ID:            "branch-bp-2",
		UserID:        defaultUser,
		Name:          "Frontend Architect",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	if _, err := svc.CreateBranch(ctx, b1); err != nil {
		t.Fatalf("failed to create branch 1: %v", err)
	}
	if _, err := svc.CreateBranch(ctx, b2); err != nil {
		t.Fatalf("failed to create branch 2: %v", err)
	}

	if _, err := store.Profiles().IncrementMainRevision(ctx, defaultUser); err != nil {
		t.Fatalf("failed to increment main revision: %v", err)
	}

	staleBranches, err := svc.ListBranches(ctx, defaultUser, true)
	if err != nil {
		t.Fatalf("failed to list stale branches: %v", err)
	}
	if len(staleBranches) < 2 {
		t.Fatalf("expected at least 2 stale branches, got %d", len(staleBranches))
	}

	// MaxPendingPerTenant is 1: 1st branch persists pending + notifies, 2nd hits ErrPendingLimitExceeded/ErrQueueFull.
	enqueuedIDs, err := svc.SyncBranches(ctx, defaultUser, []string{b1.ID, b2.ID})
	if !errors.Is(err, core.ErrPendingLimitExceeded) {
		t.Fatalf("expected core.ErrPendingLimitExceeded on saturated batch sync, got %v", err)
	}
	if !errors.Is(err, core.ErrQueueFull) {
		t.Fatalf("expected error to match core.ErrQueueFull, got %v", err)
	}
	if len(enqueuedIDs) != 1 {
		t.Fatalf("expected exactly 1 enqueued version ID before saturation, got %d", len(enqueuedIDs))
	}
	if notifier.Count() != 1 {
		t.Errorf("expected exactly 1 JobNotifier.Notify() call, got %d", notifier.Count())
	}

	persistedVer, err := svc.Version(ctx, b1.ID, enqueuedIDs[0])
	if err != nil {
		t.Fatalf("failed to load persisted pending version: %v", err)
	}
	if persistedVer.JobID == "" {
		t.Fatalf("expected persisted pending version to reference JobID")
	}
	persistedJob, err := store.Job(ctx, defaultUser, persistedVer.JobID)
	if err != nil {
		t.Fatalf("failed to load persisted job: %v", err)
	}
	if persistedJob.ReservationID == "" {
		t.Errorf("expected persisted job to retain ReservationID")
	}
	wantExpiry := fixedNow.Add(15 * time.Minute)
	if persistedJob.ExpiresAt == nil || !persistedJob.ExpiresAt.Equal(wantExpiry) {
		t.Errorf("expected ExpiresAt=%v, got %v", wantExpiry, persistedJob.ExpiresAt)
	}

	// Verify zero leaked quota: 1 active pending reservation for b1, 0 records for cap-rejected b2.
	records, err := store.ListUsageRecords(ctx, defaultUser, 10)
	if err != nil {
		t.Fatalf("ListUsageRecords failed: %v", err)
	}
	if len(records) != 1 || records[0].Status != core.ReservationStatusPending {
		t.Fatalf("expected exactly 1 pending usage record (zero leak on cap rejection), got %+v", records)
	}
	quota, err := store.Quota(ctx, defaultUser)
	if err != nil {
		t.Fatalf("Quota failed: %v", err)
	}
	if quota.DailyRequestsUsed != 1 {
		t.Errorf("DailyRequestsUsed = %d, want 1", quota.DailyRequestsUsed)
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
	if ver.JobID == "" {
		t.Fatalf("expected JobID populated on pending version")
	}
	persistedJob, err := env.db.Job(ctx, env.defaultUser, ver.JobID)
	if err != nil {
		t.Fatalf("failed to load job %s: %v", ver.JobID, err)
	}
	if persistedJob.ReservationID == "" {
		t.Errorf("expected ReservationID populated on pending job")
	}
	if persistedJob.ExpiresAt == nil {
		t.Errorf("expected ExpiresAt populated on pending job")
	}
	if env.notifier.Count() != 1 {
		t.Errorf("expected 1 notifier call, got %d", env.notifier.Count())
	}
	if persistedJob.Tailor == nil || persistedJob.Tailor.VersionID != ver.ID {
		t.Errorf("expected job tailor VersionID %s, got %+v", ver.ID, persistedJob.Tailor)
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
	if err := env.db.seedCompletedVersion(ctx, v1); err != nil {
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
	if err := env.db.seedCompletedVersion(ctx, v2); err != nil {
		t.Fatalf("seed v2: %v", err)
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
	if err := env.db.seedCompletedVersion(ctx, v1); err != nil {
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
	if err := env.db.seedCompletedVersion(ctx, v2); err != nil {
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
		UserID:            env.defaultUser,
		BranchID:          b.ID,
		VersionNumber:     1,
		BranchEpoch:       1,
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

	// Directly set ActiveVersionID without GapReport on parent branch in repo (simulating legacy store state)
	b.ActiveVersionID = &v.ID
	b.GapReport = nil
	if err := env.db.setLegacyGaplessBranch(ctx, b, v); err != nil {
		t.Fatalf("set legacy gapless branch in repo: %v", err)
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
	if err := env.db.seedCompletedVersion(ctx, vOther); err != nil {
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
	if err := env.db.seedVersionInState(ctx, vPending, core.VersionStatusPending, "res-pending-active"); err != nil {
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
		initialNotifications := env.notifier.Count()

		ver, err := env.svc.GenerateVersion(ctx, env.defaultUser, defaultBranchID)
		if err != nil {
			t.Fatalf("GenerateVersion failed: %v", err)
		}

		if env.notifier.Count() != initialNotifications {
			t.Errorf("expected notifier calls to remain %d, got %d", initialNotifications, env.notifier.Count())
		}
		if ver.JobID != "" {
			t.Errorf("expected empty JobID on default branch sync, got %q", ver.JobID)
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
		initialNotifications := env.notifier.Count()

		ver, err := env.svc.GenerateVersion(ctx, env.defaultUser, env.branchID)
		if err != nil {
			t.Fatalf("GenerateVersion failed: %v", err)
		}

		if env.notifier.Count() != initialNotifications+1 {
			t.Errorf("expected notifier calls to increase by 1, got %d (was %d)", env.notifier.Count(), initialNotifications)
		}
		if ver.JobID == "" {
			t.Errorf("expected non-empty JobID on tailored branch version")
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
		initialNotifications := env.notifier.Count()

		vIDs, err := env.svc.SyncBranches(ctx, env.defaultUser, []string{defaultBranchID, env.branchID})
		if err != nil {
			t.Fatalf("SyncBranches failed: %v", err)
		}
		if len(vIDs) != 2 {
			t.Fatalf("expected 2 version IDs returned, got %d", len(vIDs))
		}

		// Only 1 notification (for env.branchID), default branch was rendered synchronously
		if env.notifier.Count() != initialNotifications+1 {
			t.Errorf("expected exactly 1 notification for tailored branch, got %d", env.notifier.Count()-initialNotifications)
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
	if err := env.db.seedCompletedVersion(ctx, v1); err != nil {
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
	_ branch.Generator  = (*stubGenerator)(nil)
	_ branch.Generator  = (*stubATSSimulator)(nil)
	_ core.QuotaManager = (*spyQuotaRepo)(nil)
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
		svcWithLLM := branch.New(
			branch.WithStore(env.db),
			branch.WithValidator(provenance.New()),
			branch.WithRenderer(renderer.New()),
			branch.WithGenerator(stubLLM),
		)

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

		svcWithLLM := branch.New(
			branch.WithStore(&quotaOverrideStore{Store: env.db, quotas: spyQuota}),
			branch.WithValidator(provenance.New()),
			branch.WithRenderer(renderer.New()),
			branch.WithGenerator(&stubGenerator{}), // GenerateGapAnalysis returns (nil, nil)
		)

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

		svcWithLLM := branch.New(
			branch.WithStore(env.db),
			branch.WithGenerator(stubLLM),
		)

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

		svcWithLLM := branch.New(
			branch.WithStore(env.db),
			branch.WithGenerator(stubLLM),
		)

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

		svcWithLLM := branch.New(
			branch.WithStore(env.db),
			branch.WithGenerator(stubLLM),
		)

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

		svcWithLLM := branch.New(
			branch.WithStore(env.db),
			branch.WithGenerator(stubLLM),
		)

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

	svc := branch.New(
		branch.WithStore(store),
		branch.WithPromptCompiler(nil),
	)

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
	if err := env.db.seedCompletedVersion(ctx, v1); err != nil {
		t.Fatalf("CompleteVersion v1 failed: %v", err)
	}

	pendingVer := &core.BranchVersion{
		ID:       "ver-pending-preview",
		BranchID: env.branchID,
		Status:   core.VersionStatusPending,
	}
	if err := env.db.seedVersionInState(ctx, pendingVer, core.VersionStatusPending, "res-pending-preview"); err != nil {
		t.Fatalf("seed pending failed: %v", err)
	}

	explicitVer := &core.BranchVersion{
		ID:              "ver-explicit-preview",
		BranchID:        env.branchID,
		VersionNumber:   2,
		Status:          core.VersionStatusCompleted,
		PlainTextOutput: "Explicit plain text resume",
		HTMLOutput:      "<h1>HTML resume output</h1>",
	}
	if err := env.db.seedCompletedVersion(ctx, explicitVer); err != nil {
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
	branch.Store
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
	svc := branch.New(
		branch.WithStore(&crossTenantPresetReader{
			Store:  store,
			preset: core.Preset{ID: otherPresetID, UserID: "other-user", SystemInstructions: "SECRET"},
		}),
		branch.WithPromptCompiler(&fakePromptCompiler{}),
	)

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

type quotaSpecCaptureStore struct {
	*quotaOverrideStore
	spy *spyQuotaRepo
}

func (s *quotaSpecCaptureStore) SubmitBranchTailor(ctx context.Context, job *core.Job, version *core.BranchVersion, opts core.PendingJobOptions) error {
	if opts.Quota != nil {
		s.spy.mu.Lock()
		s.spy.reservations = append(s.spy.reservations, struct {
			UserID          string
			Op              core.LLMOperationType
			EstimatedTokens int
			DailyLimit      int
			MonthlyLimit    int
		}{
			UserID:          job.UserID,
			Op:              opts.Quota.Operation,
			EstimatedTokens: opts.Quota.EstimatedTokens,
			DailyLimit:      opts.Quota.DailyLimit,
			MonthlyLimit:    opts.Quota.MonthlyLimit,
		})
		s.spy.mu.Unlock()
	}
	return s.quotaOverrideStore.SubmitBranchTailor(ctx, job, version, opts)
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
	if err := store.seedCompletedVersion(ctx, vQuota1); err != nil {
		t.Fatalf("failed to seed vQuota1: %v", err)
	}

	vQuota2 := &core.BranchVersion{
		ID:               "ver-quota-ats-2",
		BranchID:         b.ID,
		MainRevisionUsed: 1,
		Status:           core.VersionStatusCompleted,
		MarkdownOutput:   "# DevOps Resume v2\nGo, Kubernetes, Terraform",
	}
	if err := store.seedCompletedVersion(ctx, vQuota2); err != nil {
		t.Fatalf("failed to seed vQuota2: %v", err)
	}

	stubLLM := &stubATSSimulator{
		stubGenerator: stubGenerator{
			gapReport: &core.GapReport{OverallMatchSummary: "Quota test gap report"},
		},
	}

	t.Run("Custom limits propagated across GenerateVersion, SyncBranches, AnalyzeGaps, and SimulateATS", func(t *testing.T) {
		spyQuota := &spyQuotaRepo{}
		capStore := &quotaSpecCaptureStore{
			quotaOverrideStore: &quotaOverrideStore{Store: store, quotas: spyQuota},
			spy:                spyQuota,
		}

		svc := branch.New(
			branch.WithStore(capStore),
			branch.WithGenerator(stubLLM),
			branch.WithDailyRequestLimit(25),
			branch.WithMonthlyTokenLimit(250000),
		)

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
		capStore := &quotaSpecCaptureStore{
			quotaOverrideStore: &quotaOverrideStore{Store: store, quotas: spyQuota},
			spy:                spyQuota,
		}

		svcDefault := branch.New(
			branch.WithStore(capStore),
			branch.WithGenerator(stubLLM),
			branch.WithDailyRequestLimit(0),
			branch.WithMonthlyTokenLimit(-100),
		)

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
	branch.Store
	snapErr error
}

func (f *failingSnapshotProfileRepo) Snapshot(ctx context.Context, userID string) (*core.MainProfileSnapshot, error) {
	if f.snapErr != nil {
		return nil, f.snapErr
	}
	return f.Store.Snapshot(ctx, userID)
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
		if err := env.db.seedVersionInState(ctx, pendingVer, core.VersionStatusPending, "res-pending-ats"); err != nil {
			t.Fatalf("seed pending failed: %v", err)
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
		if err := env.db.seedCompletedVersion(ctx, v1); err != nil {
			t.Fatalf("CompleteVersion v1 failed: %v", err)
		}

		sim := &stubATSSimulator{}
		svc := branch.New(
			branch.WithStore(env.db),
			branch.WithRenderer(renderer.New()),
			branch.WithGenerator(sim),
		)

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
		if err := env.db.seedCompletedVersion(ctx, v2); err != nil {
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
		if err := env.db.seedCompletedVersion(ctx, vSnap); err != nil {
			t.Fatalf("CompleteVersion failed: %v", err)
		}

		sim := &stubATSSimulator{}
		svcSnapErr := branch.New(
			branch.WithStore(&failingSnapshotProfileRepo{
				Store:   env.db,
				snapErr: errors.New("db connection lost"),
			}),
			branch.WithGenerator(sim),
		)

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
		if err := env.db.seedCompletedVersion(ctx, vQuotaCommit); err != nil {
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
		svcQuota := branch.New(
			branch.WithStore(&quotaOverrideStore{Store: env.db, quotas: spyQuota}),
			branch.WithGenerator(simZeroTokens),
		)

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
		if err := env.db.seedCompletedVersion(ctx, vQuotaFail); err != nil {
			t.Fatalf("CompleteVersion failed: %v", err)
		}

		simErr := &stubATSSimulator{atsErr: errors.New("upstream llm failure")}
		svcQuotaFail := branch.New(
			branch.WithStore(&quotaOverrideStore{Store: env.db, quotas: spyQuota}),
			branch.WithGenerator(simErr),
		)

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
		if err := env.db.seedCompletedVersion(ctx, vPartial); err != nil {
			t.Fatalf("CompleteVersion failed: %v", err)
		}

		sim := &stubATSSimulator{}
		svcRender := branch.New(
			branch.WithStore(env.db),
			branch.WithRenderer(renderer.New()),
			branch.WithGenerator(sim),
		)

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
		if err := env.db.seedCompletedVersion(ctx, vDet); err != nil {
			t.Fatalf("seedCompletedVersion failed: %v", err)
		}

		svcDet := branch.New(
			branch.WithStore(env.db),
			branch.WithGenerator(nil),
		)

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
		if err := env.db.seedCompletedVersion(ctx, vConc); err != nil {
			t.Fatalf("seedCompletedVersion failed: %v", err)
		}

		simSlow := &stubATSSimulator{
			delay: 80 * time.Millisecond,
		}
		svcConc := branch.New(
			branch.WithStore(env.db),
			branch.WithGenerator(simSlow),
		)

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

type stubNotifier struct {
	mu    sync.Mutex
	calls int
}

func (n *stubNotifier) Notify() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
}

func (n *stubNotifier) Count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

type spyAbortStore struct {
	*testBranchStore
	mu              sync.Mutex
	abortedCalls    []string
	abortedStatuses map[string][]core.VersionStatus
	cancelCtxFn     func()
}

func (s *spyAbortStore) AbortBranchJobs(ctx context.Context, branchID, reason string) error {
	if s.cancelCtxFn != nil {
		s.cancelCtxFn()
	}

	err := s.testBranchStore.AbortBranchJobs(ctx, branchID, reason)
	if err == nil {
		versionsAfter, _ := s.ListVersions(ctx, branchID)
		statuses := make([]core.VersionStatus, 0, len(versionsAfter))
		for _, v := range versionsAfter {
			statuses = append(statuses, v.Status)
		}
		s.mu.Lock()
		s.abortedCalls = append(s.abortedCalls, branchID)
		if s.abortedStatuses == nil {
			s.abortedStatuses = make(map[string][]core.VersionStatus)
		}
		s.abortedStatuses[branchID] = statuses
		s.mu.Unlock()
	}
	return err
}

func (s *spyAbortStore) SubmitBranchTailor(ctx context.Context, job *core.Job, version *core.BranchVersion, opts core.PendingJobOptions) error {
	err := s.testBranchStore.SubmitBranchTailor(ctx, job, version, opts)
	if s.cancelCtxFn != nil {
		s.cancelCtxFn()
	}
	return err
}

func TestService_AdmissionControlAndNotifications(t *testing.T) {
	cases := []struct {
		name               string
		maxPending         int
		jobTTL             time.Duration
		cleanupTimeout     time.Duration
		useNotifier        bool
		preSeedPending     int
		cancelCtxOnSubmit  bool
		wantErr            error
		wantNotifierCalls  int
		wantPendingVersion bool
		wantUsageRecords   int
	}{
		{
			name:               "notifier wakes workers and persists Job ReservationID and ExpiresAt",
			maxPending:         2,
			jobTTL:             20 * time.Minute,
			useNotifier:        true,
			wantNotifierCalls:  1,
			wantPendingVersion: true,
			wantUsageRecords:   1,
		},
		{
			name:               "default MaxPendingPerTenant and JobTTL applied when unset",
			maxPending:         0,
			jobTTL:             0,
			useNotifier:        true,
			preSeedPending:     core.DefaultMaxPendingPerTenant - 1,
			wantNotifierCalls:  1,
			wantPendingVersion: true,
			wantUsageRecords:   1,
		},
		{
			name:             "default MaxPendingPerTenant rejects once 25 other branches have pending versions",
			maxPending:       -1,
			jobTTL:           -1,
			useNotifier:      true,
			preSeedPending:   core.DefaultMaxPendingPerTenant,
			wantErr:          core.ErrPendingLimitExceeded,
			wantUsageRecords: 0,
		},
		{
			name:              "cancelled caller context still rolls back atomically on cap error with zero leaked quota",
			maxPending:        1,
			cleanupTimeout:    2 * time.Second,
			useNotifier:       true,
			preSeedPending:    1,
			cancelCtxOnSubmit: true,
			wantErr:           core.ErrPendingLimitExceeded,
			wantUsageRecords:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			userID := "user-admission-" + uuid.New().String()
			baseStore := newSeededBranchStore(ctx, userID)
			notifier := &stubNotifier{}

			fixedNow := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)

			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			var st branch.Store = baseStore
			if tc.cancelCtxOnSubmit {
				st = &spyAbortStore{
					testBranchStore: baseStore,
					cancelCtxFn:     cancel,
				}
			}

			opts := []branch.Option{
				branch.WithStore(st),
				branch.WithMaxPendingPerTenant(tc.maxPending),
				branch.WithJobTTL(tc.jobTTL),
				branch.WithCleanupTimeout(tc.cleanupTimeout),
				branch.WithClock(func() time.Time { return fixedNow }),
			}
			if tc.useNotifier {
				opts = append(opts, branch.WithJobNotifier(notifier))
			}
			svc := branch.New(opts...)

			for i := 0; i < tc.preSeedPending; i++ {
				extraBranch := &core.Branch{
					ID:            fmt.Sprintf("branch-seed-%d", i),
					UserID:        userID,
					Name:          fmt.Sprintf("Seed Branch %d", i),
					TemplateSetID: core.TemplateStandardCleanID,
				}
				if _, err := svc.CreateBranch(ctx, extraBranch); err != nil {
					t.Fatalf("CreateBranch seed %d failed: %v", i, err)
				}
				seedVer := &core.BranchVersion{
					ID:       fmt.Sprintf("ver-seed-%d", i),
					UserID:   userID,
					BranchID: extraBranch.ID,
				}
				if err := baseStore.seedVersionInState(ctx, seedVer, core.VersionStatusPending, fmt.Sprintf("res-seed-%d", i)); err != nil {
					t.Fatalf("seedVersionInState %d failed: %v", i, err)
				}
			}

			targetBranch := &core.Branch{
				ID:            "branch-target-" + uuid.New().String(),
				UserID:        userID,
				Name:          "Target Branch",
				TemplateSetID: core.TemplateStandardCleanID,
			}
			if _, err := svc.CreateBranch(ctx, targetBranch); err != nil {
				t.Fatalf("CreateBranch target failed: %v", err)
			}

			ver, err := svc.GenerateVersion(callCtx, userID, targetBranch.ID)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				if !errors.Is(err, core.ErrQueueFull) {
					t.Fatalf("expected ErrPendingLimitExceeded to match ErrQueueFull, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantPendingVersion {
				if ver == nil || ver.Status != core.VersionStatusPending {
					t.Fatalf("expected pending version, got %+v", ver)
				}
				if ver.JobID == "" {
					t.Fatalf("expected non-empty JobID on pending version")
				}
				persistedJob, jErr := baseStore.Job(ctx, userID, ver.JobID)
				if jErr != nil {
					t.Fatalf("expected persisted job in store: %v", jErr)
				}
				if persistedJob.ReservationID == "" {
					t.Errorf("expected non-empty ReservationID on persisted job")
				}
				expectedTTL := tc.jobTTL
				if expectedTTL <= 0 {
					expectedTTL = core.DefaultJobTTL
				}
				wantExpiry := fixedNow.Add(expectedTTL)
				if persistedJob.ExpiresAt == nil || !persistedJob.ExpiresAt.Equal(wantExpiry) {
					t.Errorf("expected ExpiresAt=%v, got %v", wantExpiry, persistedJob.ExpiresAt)
				}
				reloaded, getErr := svc.Version(ctx, targetBranch.ID, ver.ID)
				if getErr != nil {
					t.Fatalf("expected persisted version in store: %v", getErr)
				}
				if reloaded.Status != core.VersionStatusPending || reloaded.JobID != ver.JobID {
					t.Errorf("reloaded version mismatch: %+v", reloaded)
				}
			}

			if notifier.Count() != tc.wantNotifierCalls {
				t.Errorf("expected %d notifier calls, got %d", tc.wantNotifierCalls, notifier.Count())
			}

			records, listErr := baseStore.ListUsageRecords(ctx, userID, 50)
			if listErr != nil {
				t.Fatalf("ListUsageRecords failed: %v", listErr)
			}
			if len(records) != tc.wantUsageRecords {
				t.Fatalf("expected %d usage record(s), got %d (%+v)", tc.wantUsageRecords, len(records), records)
			}
			if tc.wantErr != nil {
				quota, qErr := baseStore.Quota(ctx, userID)
				if qErr != nil {
					t.Fatalf("Quota failed: %v", qErr)
				}
				if quota.DailyRequestsUsed != 0 {
					t.Errorf("expected 0 DailyRequestsUsed on rolled-back submission, got %d", quota.DailyRequestsUsed)
				}
			}
		})
	}
}

func TestService_GenerateVersionSupersedenceAndConflict(t *testing.T) {
	cases := []struct {
		name              string
		existingStatus    core.VersionStatus
		maxPending        int
		wantErr           error
		wantSuperseded    bool
		wantFinalVersions int
	}{
		{
			name:              "supersedes existing pending version and releases its quota even at MaxPendingPerTenant=1",
			existingStatus:    core.VersionStatusPending,
			maxPending:        1,
			wantSuperseded:    true,
			wantFinalVersions: 2,
		},
		{
			name:              "rejects with ErrConflict when branch already has processing version and keeps existing reservation pending",
			existingStatus:    core.VersionStatusProcessing,
			maxPending:        5,
			wantErr:           core.ErrConflict,
			wantSuperseded:    false,
			wantFinalVersions: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			userID := "user-supersedence"
			store := newSeededBranchStore(ctx, userID)
			notifier := &stubNotifier{}

			svc := branch.New(
				branch.WithStore(store),
				branch.WithJobNotifier(notifier),
				branch.WithMaxPendingPerTenant(tc.maxPending),
			)

			b := &core.Branch{
				ID:            "branch-sup-" + uuid.New().String(),
				UserID:        userID,
				Name:          "Supersedence Branch",
				TemplateSetID: core.TemplateStandardCleanID,
			}
			if _, err := svc.CreateBranch(ctx, b); err != nil {
				t.Fatalf("CreateBranch failed: %v", err)
			}

			// Reserve quota and seed prior version
			priorRec, err := store.ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 50, 1000000)
			if err != nil {
				t.Fatalf("ReserveQuota prior failed: %v", err)
			}
			priorVer := &core.BranchVersion{
				ID:       "ver-prior-1",
				UserID:   userID,
				BranchID: b.ID,
			}
			if err := store.seedVersionInState(ctx, priorVer, tc.existingStatus, priorRec.ID); err != nil {
				t.Fatalf("seedVersionInState prior failed: %v", err)
			}

			newVer, err := svc.GenerateVersion(ctx, userID, b.ID)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if newVer.Status != core.VersionStatusPending || newVer.JobID == "" {
					t.Errorf("expected new pending version with JobID, got %+v", newVer)
				}
				if newJob, jErr := store.Job(ctx, userID, newVer.JobID); jErr != nil || newJob.ReservationID == "" {
					t.Errorf("expected persisted job with ReservationID, got %+v err=%v", newJob, jErr)
				} else {
					assertReservationState(ctx, t, store, userID, newJob.ReservationID, core.ReservationStatusPending, "")
				}
			}

			reloadedPrior, err := svc.Version(ctx, b.ID, priorVer.ID)
			if err != nil {
				t.Fatalf("Version prior failed: %v", err)
			}
			if tc.wantSuperseded {
				if reloadedPrior.Status != core.VersionStatusCancelled {
					t.Errorf("expected prior version to be cancelled, got %s", reloadedPrior.Status)
				}
				assertReservationState(ctx, t, store, userID, priorRec.ID, core.ReservationStatusReleased, "superseded by newer pending version")
			} else {
				if reloadedPrior.Status != tc.existingStatus {
					t.Errorf("expected prior version to remain %s, got %s", tc.existingStatus, reloadedPrior.Status)
				}
				assertReservationState(ctx, t, store, userID, priorRec.ID, core.ReservationStatusPending, "")
			}

			quota, err := store.Quota(ctx, userID)
			if err != nil {
				t.Fatalf("Quota failed: %v", err)
			}
			if quota.DailyRequestsUsed != 1 {
				t.Errorf("DailyRequestsUsed = %d, want 1", quota.DailyRequestsUsed)
			}

			versions, err := svc.ListVersions(ctx, b.ID)
			if err != nil {
				t.Fatalf("ListVersions failed: %v", err)
			}
			if len(versions) != tc.wantFinalVersions {
				t.Errorf("expected %d versions, got %d", tc.wantFinalVersions, len(versions))
			}
		})
	}
}

func TestService_SyncBranchesPartialBatchAndDefaultHandling(t *testing.T) {
	ctx := context.Background()
	userID := "user-sync-partial"
	store := newSeededBranchStore(ctx, userID)
	notifier := &stubNotifier{}

	svc := branch.New(
		branch.WithStore(store),
		branch.WithJobNotifier(notifier),
		branch.WithRenderer(renderer.New()),
		branch.WithMaxPendingPerTenant(1),
	)

	b1 := &core.Branch{ID: "br-sync-1", UserID: userID, Name: "Sync 1", TemplateSetID: core.TemplateStandardCleanID}
	b2 := &core.Branch{ID: "br-sync-2", UserID: userID, Name: "Sync 2", TemplateSetID: core.TemplateStandardCleanID}
	if _, err := svc.CreateBranch(ctx, b1); err != nil {
		t.Fatalf("CreateBranch b1: %v", err)
	}
	if _, err := svc.CreateBranch(ctx, b2); err != nil {
		t.Fatalf("CreateBranch b2: %v", err)
	}

	defaultID := core.DefaultBranchID(userID)
	ids, err := svc.SyncBranches(ctx, userID, []string{defaultID, b1.ID, b2.ID})
	if !errors.Is(err, core.ErrPendingLimitExceeded) {
		t.Fatalf("expected ErrPendingLimitExceeded, got %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 version IDs (default sync + 1st pending), got %d (%v)", len(ids), ids)
	}
	if notifier.Count() != 1 {
		t.Errorf("expected 1 notifier call for b1 only, got %d", notifier.Count())
	}

	records, err := store.ListUsageRecords(ctx, userID, 10)
	if err != nil {
		t.Fatalf("ListUsageRecords failed: %v", err)
	}
	if len(records) != 1 || records[0].Status != core.ReservationStatusPending {
		t.Fatalf("expected exactly 1 pending reservation for b1 (zero leak for rejected b2), got %+v", records)
	}
	quota, err := store.Quota(ctx, userID)
	if err != nil {
		t.Fatalf("Quota failed: %v", err)
	}
	if quota.DailyRequestsUsed != 1 {
		t.Errorf("DailyRequestsUsed = %d, want 1", quota.DailyRequestsUsed)
	}
}

func TestService_MutationSupersedence(t *testing.T) {
	cases := []struct {
		name           string
		action         func(t *testing.T, ctx context.Context, env *testBranchEnv, svc *branch.Service, b *core.Branch, v1 *core.BranchVersion)
		expectedReason string
	}{
		{
			name: "EditVersion supersedes pending versions and releases quota",
			action: func(t *testing.T, ctx context.Context, env *testBranchEnv, svc *branch.Service, b *core.Branch, v1 *core.BranchVersion) {
				t.Helper()
				content := core.TailoredContent{
					Summary: "Manual edit summary",
					Experiences: []core.TailoredExperience{
						{
							SourceEntityID: env.validExpID,
							Company:        "Acme Corp",
							RoleTitle:      "Lead Systems Architect",
							StartDate:      core.NewDate(2020, 1, 1),
							Bullets: []core.TailoredBullet{
								{
									Text:          "Optimized distributed consensus throughput by 40%.",
									SourceFactIDs: []string{env.validFactID},
								},
							},
						},
					},
				}
				if _, err := svc.EditVersion(ctx, env.defaultUser, b.ID, v1.ID, content); err != nil {
					t.Fatalf("EditVersion failed: %v", err)
				}
			},
			expectedReason: "superseded by manual edit",
		},
		{
			name: "UpdateBranch config change supersedes pending versions and releases quota",
			action: func(t *testing.T, ctx context.Context, _ *testBranchEnv, svc *branch.Service, b *core.Branch, _ *core.BranchVersion) {
				t.Helper()
				b.TargetRequirements = "Updated JD requirements"
				if _, err := svc.UpdateBranch(ctx, b); err != nil {
					t.Fatalf("UpdateBranch failed: %v", err)
				}
			},
			expectedReason: "superseded by branch update",
		},
		{
			name: "UpdateBranch active version rollback supersedes pending versions and releases quota",
			action: func(t *testing.T, ctx context.Context, _ *testBranchEnv, svc *branch.Service, b *core.Branch, v1 *core.BranchVersion) {
				t.Helper()
				b.ActiveVersionID = &v1.ID
				if _, err := svc.UpdateBranch(ctx, b); err != nil {
					t.Fatalf("UpdateBranch rollback failed: %v", err)
				}
			},
			expectedReason: "superseded by branch update",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			env := setupTestBranchEnv(t, 10)
			store := env.db
			notifier := &stubNotifier{}

			svc := branch.New(
				branch.WithStore(store),
				branch.WithJobNotifier(notifier),
				branch.WithValidator(provenance.New()),
				branch.WithRenderer(renderer.New()),
				branch.WithPromptCompiler(&fakePromptCompiler{}),
			)

			// Create a completed v2 so rollback back to v1 is distinct
			v1, err := svc.Version(ctx, env.branchID, env.v1ID)
			if err != nil {
				t.Fatalf("Version v1 failed: %v", err)
			}
			v2 := &core.BranchVersion{
				ID:                "ver-completed-v2-" + uuid.New().String(),
				UserID:            env.defaultUser,
				BranchID:          env.branchID,
				Status:            core.VersionStatusCompleted,
				MainRevisionUsed:  1,
				TemplateSetIDUsed: core.TemplateStandardCleanID,
				SourceType:        core.SourceTypeLLMGeneration,
			}
			if err := store.seedCompletedVersion(ctx, v2); err != nil {
				t.Fatalf("CompleteVersion v2: %v", err)
			}

			// Queue a pending version on env.branchID
			pendingVer, err := svc.GenerateVersion(ctx, env.defaultUser, env.branchID)
			if err != nil {
				t.Fatalf("GenerateVersion pending failed: %v", err)
			}
			pendingJob, err := store.Job(ctx, env.defaultUser, pendingVer.JobID)
			if err != nil {
				t.Fatalf("Job pending load failed: %v", err)
			}

			b, err := svc.Branch(ctx, env.defaultUser, env.branchID)
			if err != nil {
				t.Fatalf("Branch failed: %v", err)
			}

			tc.action(t, ctx, env, svc, b, v1)

			afterPending, err := svc.Version(ctx, env.branchID, pendingVer.ID)
			if err != nil {
				t.Fatalf("Version pending reload failed: %v", err)
			}
			if afterPending.Status != core.VersionStatusCancelled {
				t.Errorf("expected pending version to be cancelled, got %s", afterPending.Status)
			}
			if afterPending.ErrorMessage != tc.expectedReason {
				t.Errorf("expected reason %q, got %q", tc.expectedReason, afterPending.ErrorMessage)
			}

			assertReservationState(ctx, t, store, env.defaultUser, pendingJob.ReservationID, core.ReservationStatusReleased, tc.expectedReason)
			quota, err := store.Quota(ctx, env.defaultUser)
			if err != nil {
				t.Fatalf("Quota failed: %v", err)
			}
			if quota.DailyRequestsUsed != 0 {
				t.Errorf("DailyRequestsUsed = %d, want 0", quota.DailyRequestsUsed)
			}
		})
	}
}

func TestService_DeleteBranchAbortsVersionsAndReleasesQuota(t *testing.T) {
	ctx := context.Background()
	owner := "delete-owner"
	baseStore := newSeededBranchStore(ctx, owner)
	spyStore := &spyAbortStore{testBranchStore: baseStore}

	svc := branch.New(
		branch.WithStore(spyStore),
	)

	bPending := &core.Branch{
		ID:            "branch-delete-abort-pending",
		UserID:        owner,
		Name:          "Branch To Delete (Pending)",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	if _, err := svc.CreateBranch(ctx, bPending); err != nil {
		t.Fatalf("CreateBranch pending failed: %v", err)
	}
	bProcessing := &core.Branch{
		ID:            "branch-delete-abort-processing",
		UserID:        owner,
		Name:          "Branch To Delete (Processing)",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	if _, err := svc.CreateBranch(ctx, bProcessing); err != nil {
		t.Fatalf("CreateBranch processing failed: %v", err)
	}

	resPending, err := baseStore.ReserveQuota(ctx, owner, core.OperationResumeTailoring, 2000, 50, 1000000)
	if err != nil {
		t.Fatalf("ReserveQuota pending failed: %v", err)
	}
	resProcessing, err := baseStore.ReserveQuota(ctx, owner, core.OperationResumeTailoring, 2000, 50, 1000000)
	if err != nil {
		t.Fatalf("ReserveQuota processing failed: %v", err)
	}

	vPending := &core.BranchVersion{
		ID:       "ver-del-pending",
		UserID:   owner,
		BranchID: bPending.ID,
	}
	vProcessing := &core.BranchVersion{
		ID:       "ver-del-processing",
		UserID:   owner,
		BranchID: bProcessing.ID,
	}
	if err := baseStore.seedVersionInState(ctx, vPending, core.VersionStatusPending, resPending.ID); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	if err := baseStore.seedVersionInState(ctx, vProcessing, core.VersionStatusProcessing, resProcessing.ID); err != nil {
		t.Fatalf("seed processing: %v", err)
	}

	t.Run("cross-tenant DeleteBranch denied before aborting versions", func(t *testing.T) {
		err := svc.DeleteBranch(ctx, "other-tenant", bPending.ID)
		if !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("expected ErrNotFound for cross-tenant delete, got %v", err)
		}
		if len(spyStore.abortedCalls) != 0 {
			t.Errorf("expected AbortBranchJobs NOT to be called on unauthorized delete, got %v", spyStore.abortedCalls)
		}
		assertReservationState(ctx, t, baseStore, owner, resPending.ID, core.ReservationStatusPending, "")
	})

	for _, badUserID := range []struct {
		name   string
		userID string
	}{
		{name: "empty userID DeleteBranch rejected before aborting versions", userID: ""},
		{name: "whitespace userID DeleteBranch rejected before aborting versions", userID: "   "},
	} {
		t.Run(badUserID.name, func(t *testing.T) {
			err := svc.DeleteBranch(ctx, badUserID.userID, bPending.ID)
			if !errors.Is(err, core.ErrValidation) {
				t.Fatalf("expected ErrValidation for %q DeleteBranch, got %v", badUserID.userID, err)
			}
			if len(spyStore.abortedCalls) != 0 {
				t.Errorf("expected AbortBranchJobs NOT to be called, got %v", spyStore.abortedCalls)
			}
			assertReservationState(ctx, t, baseStore, owner, resPending.ID, core.ReservationStatusPending, "")
			assertReservationState(ctx, t, baseStore, owner, resProcessing.ID, core.ReservationStatusPending, "")
			gotPending, err := svc.Version(ctx, bPending.ID, vPending.ID)
			if err != nil || gotPending.Status != core.VersionStatusPending {
				t.Errorf("expected victim pending version untouched, got %+v err=%v", gotPending, err)
			}
			gotProcessing, err := svc.Version(ctx, bProcessing.ID, vProcessing.ID)
			if err != nil || gotProcessing.Status != core.VersionStatusProcessing {
				t.Errorf("expected victim processing version untouched, got %+v err=%v", gotProcessing, err)
			}
		})
	}

	t.Run("default branch DeleteBranch rejected before aborting", func(t *testing.T) {
		err := svc.DeleteBranch(ctx, owner, core.DefaultBranchID(owner))
		if !errors.Is(err, core.ErrDefaultBranchImmutable) {
			t.Fatalf("expected ErrDefaultBranchImmutable, got %v", err)
		}
	})

	t.Run("owner DeleteBranch aborts pending and processing versions and releases all quota", func(t *testing.T) {
		if err := svc.DeleteBranch(ctx, owner, bPending.ID); err != nil {
			t.Fatalf("DeleteBranch pending failed: %v", err)
		}
		if err := svc.DeleteBranch(ctx, owner, bProcessing.ID); err != nil {
			t.Fatalf("DeleteBranch processing failed: %v", err)
		}

		for _, branchID := range []string{bPending.ID, bProcessing.ID} {
			statuses := spyStore.abortedStatuses[branchID]
			if len(statuses) != 1 || statuses[0] != core.VersionStatusCancelled {
				t.Fatalf("expected 1 cancelled status on %s before branch deletion, got %v", branchID, statuses)
			}
			if _, err := svc.Branch(ctx, owner, branchID); !errors.Is(err, core.ErrNotFound) {
				t.Errorf("expected deleted branch %s to return ErrNotFound, got %v", branchID, err)
			}
		}

		assertReservationState(ctx, t, baseStore, owner, resPending.ID, core.ReservationStatusReleased, "branch deleted")
		assertReservationState(ctx, t, baseStore, owner, resProcessing.ID, core.ReservationStatusReleased, "branch deleted")
		quota, qErr := baseStore.Quota(ctx, owner)
		if qErr != nil || quota.DailyRequestsUsed != 0 {
			t.Errorf("expected DailyRequestsUsed=0 after branch deletion, got %+v err=%v", quota, qErr)
		}
	})
}

func TestStore_JobLeaseGuardsAndLifecycle(t *testing.T) {
	ctx := context.Background()
	userID := "store-guard-user"
	st := newSeededBranchStore(ctx, userID)

	b := &core.Branch{
		ID:            "br-store-guard",
		UserID:        userID,
		Name:          "Store Guard",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	if err := st.CreateBranch(ctx, b); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	t0 := time.Date(2026, 11, 15, 8, 0, 0, 0, time.UTC)
	exp := t0.Add(10 * time.Minute)
	v := &core.BranchVersion{
		ID:                "ver-lease-1",
		UserID:            userID,
		BranchID:          b.ID,
		JobID:             "job-lease-1",
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		SourceType:        core.SourceTypeLLMGeneration,
	}
	job := &core.Job{
		ID:            "job-lease-1",
		Kind:          core.JobKindBranchTailor,
		UserID:        userID,
		Status:        core.JobStatusPending,
		ReservationID: "res-lease-1",
		ExpiresAt:     &exp,
		Tailor: &core.BranchTailorSpec{
			BranchID:  b.ID,
			VersionID: v.ID,
		},
		CreatedAt: t0,
		UpdatedAt: t0,
	}
	if err := st.SubmitBranchTailor(ctx, job, v, core.PendingJobOptions{
		MaxPendingPerTenant: 5,
		TTL:                 10 * time.Minute,
		Now:                 t0,
	}); err != nil {
		t.Fatalf("SubmitBranchTailor: %v", err)
	}

	claimed, err := st.ClaimNext(ctx, core.ClaimJobRequest{
		WorkerID:               "worker-a",
		Now:                    t0.Add(time.Minute),
		LeaseDuration:          2 * time.Minute,
		MaxConcurrentPerTenant: 1,
	})
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext failed: claimed=%v err=%v", claimed, err)
	}
	if claimed.Status != core.JobStatusProcessing || claimed.ClaimedBy != "worker-a" || claimed.AttemptCount != 1 {
		t.Fatalf("unexpected claimed job: %+v", claimed)
	}

	// Lease renewal by wrong worker fails
	if err := st.RenewLease(ctx, claimed.ID, "worker-b", t0.Add(5*time.Minute)); !errors.Is(err, core.ErrLeaseExpired) {
		t.Errorf("expected ErrLeaseExpired for wrong worker renewal, got %v", err)
	}

	// Requeue and reclaim by worker-b; stale worker-a CompleteBranchTailor must fail with ErrLeaseExpired
	if err := st.RequeueForRetry(ctx, claimed.ID, "worker-a", t0.Add(2*time.Minute), "transient"); err != nil {
		t.Fatalf("RequeueForRetry failed: %v", err)
	}
	reclaimed, err := st.ClaimNext(ctx, core.ClaimJobRequest{
		WorkerID:      "worker-b",
		Now:           t0.Add(2 * time.Minute),
		LeaseDuration: 2 * time.Minute,
	})
	if err != nil || reclaimed == nil || reclaimed.ClaimedBy != "worker-b" || reclaimed.AttemptCount != 2 {
		t.Fatalf("expected reclaim by worker-b, got %+v, err=%v", reclaimed, err)
	}

	staleComplete := *v
	staleComplete.Status = core.VersionStatusCompleted
	if err := st.CompleteBranchTailor(ctx, claimed.ID, "worker-a", &staleComplete); !errors.Is(err, core.ErrLeaseExpired) {
		t.Errorf("expected ErrLeaseExpired for stale worker-a CompleteBranchTailor, got %v", err)
	}

	// Expire lease and verify terminal guards
	failedCount, err := st.FailExpiredLeases(ctx, t0.Add(10*time.Minute), "lease timed out")
	if err != nil {
		t.Fatalf("FailExpiredLeases: %v", err)
	}
	if failedCount != 1 {
		t.Fatalf("expected 1 failed expired-lease job, got %d", failedCount)
	}
	if err := st.CompleteBranchTailor(ctx, reclaimed.ID, "worker-b", &staleComplete); !errors.Is(err, core.ErrConflict) {
		t.Errorf("expected ErrConflict completing failed job, got %v", err)
	}
}

func TestService_UpdateBranchProcessingAbortionAndEpochGuard(t *testing.T) {
	setupTailoredWithActiveAndProcessing := func(t *testing.T, withSecondCompleted bool) (
		context.Context,
		*testBranchStore,
		*branch.Service,
		*core.Branch,
		*core.BranchVersion,
		*core.BranchVersion,
		*core.Job,
		string,
	) {
		t.Helper()
		ctx := context.Background()
		userID := "user-epoch-" + uuid.New().String()
		baseStore := newSeededBranchStore(ctx, userID)
		svc := branch.New(
			branch.WithStore(baseStore),
			branch.WithCleanupTimeout(2*time.Second),
		)

		b, err := svc.CreateBranch(ctx, &core.Branch{
			ID:                 "branch-epoch-" + uuid.New().String(),
			UserID:             userID,
			Name:               "Principal Go Engineer",
			TargetRequirements: "Go, PostgreSQL, distributed systems",
			TemplateSetID:      core.TemplateStandardCleanID,
		})
		if err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}

		v1 := &core.BranchVersion{
			ID:                "ver-epoch-1-" + uuid.New().String(),
			UserID:            userID,
			BranchID:          b.ID,
			Status:            core.VersionStatusCompleted,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: core.TemplateStandardCleanID,
			SourceType:        core.SourceTypeLLMGeneration,
		}
		if err := baseStore.seedCompletedVersion(ctx, v1); err != nil {
			t.Fatalf("seed v1 failed: %v", err)
		}

		if withSecondCompleted {
			v2 := &core.BranchVersion{
				ID:                "ver-epoch-2-" + uuid.New().String(),
				UserID:            userID,
				BranchID:          b.ID,
				Status:            core.VersionStatusCompleted,
				MainRevisionUsed:  1,
				TemplateSetIDUsed: core.TemplateStandardCleanID,
				SourceType:        core.SourceTypeLLMGeneration,
			}
			if err := baseStore.seedCompletedVersion(ctx, v2); err != nil {
				t.Fatalf("seed v2 failed: %v", err)
			}
		}

		pendingVer, err := svc.GenerateVersion(ctx, userID, b.ID)
		if err != nil {
			t.Fatalf("GenerateVersion failed: %v", err)
		}
		pendingJob, err := baseStore.Job(ctx, userID, pendingVer.JobID)
		if err != nil {
			t.Fatalf("Job reload failed: %v", err)
		}
		claimedJob, err := baseStore.ClaimNext(ctx, core.ClaimJobRequest{
			WorkerID:      "worker-1",
			LeaseDuration: 2 * time.Minute,
		})
		if err != nil || claimedJob == nil || claimedJob.Tailor == nil || claimedJob.Tailor.VersionID != pendingVer.ID {
			t.Fatalf("ClaimNext failed: claimed=%+v err=%v", claimedJob, err)
		}

		loadedBranch, err := svc.Branch(ctx, userID, b.ID)
		if err != nil {
			t.Fatalf("Branch reload failed: %v", err)
		}
		return ctx, baseStore, svc, loadedBranch, v1, pendingVer, claimedJob, pendingJob.ReservationID
	}

	t.Run("rollback of ActiveVersionID cancels higher-numbered processing version, releases quota, and blocks stale CompleteBranchTailor", func(t *testing.T) {
		ctx, baseStore, svc, b, v1, pendingVer, claimedJob, reservationID := setupTailoredWithActiveAndProcessing(t, true)
		callCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		spyStore := &spyAbortStore{
			testBranchStore: baseStore,
			cancelCtxFn:     cancel,
		}
		svcWithCancel := branch.New(
			branch.WithStore(spyStore),
			branch.WithCleanupTimeout(2*time.Second),
		)

		b.ActiveVersionID = &v1.ID
		updated, err := svcWithCancel.UpdateBranch(callCtx, b)
		if err != nil {
			t.Fatalf("UpdateBranch rollback failed: %v", err)
		}
		if updated.ActiveVersionID == nil || *updated.ActiveVersionID != v1.ID {
			t.Fatalf("expected ActiveVersionID=%s, got %v", v1.ID, updated.ActiveVersionID)
		}

		reloadedV3, err := svc.Version(ctx, b.ID, pendingVer.ID)
		if err != nil {
			t.Fatalf("Version v3 reload failed: %v", err)
		}
		if reloadedV3.Status != core.VersionStatusCancelled {
			t.Errorf("expected processing v3 to be cancelled, got %s", reloadedV3.Status)
		}
		if reloadedV3.ErrorMessage != "superseded by branch update" {
			t.Errorf("expected reason 'superseded by branch update', got %q", reloadedV3.ErrorMessage)
		}

		assertReservationState(ctx, t, baseStore, b.UserID, reservationID, core.ReservationStatusReleased, "superseded by branch update")

		staleComplete := *pendingVer
		staleComplete.Status = core.VersionStatusCompleted
		if err := baseStore.CompleteBranchTailor(ctx, claimedJob.ID, claimedJob.ClaimedBy, &staleComplete); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("expected stale CompleteBranchTailor to return ErrConflict, got %v", err)
		}

		afterBranch, err := svc.Branch(ctx, b.UserID, b.ID)
		if err != nil {
			t.Fatalf("Branch refetch failed: %v", err)
		}
		if afterBranch.ActiveVersionID == nil || *afterBranch.ActiveVersionID != v1.ID {
			t.Errorf("expected ActiveVersionID to remain %s, got %v", v1.ID, afterBranch.ActiveVersionID)
		}
	})

	t.Run("TargetRequirements change cancels processing version, releases quota, and blocks stale CompleteBranchTailor", func(t *testing.T) {
		ctx, baseStore, svc, b, v1, pendingVer, claimedJob, reservationID := setupTailoredWithActiveAndProcessing(t, false)

		b.TargetRequirements = "Rust, Raft consensus, zero-copy networking"
		updated, err := svc.UpdateBranch(ctx, b)
		if err != nil {
			t.Fatalf("UpdateBranch failed: %v", err)
		}
		if updated.ActiveVersionID == nil || *updated.ActiveVersionID != v1.ID {
			t.Fatalf("expected ActiveVersionID=%s, got %v", v1.ID, updated.ActiveVersionID)
		}

		reloadedV2, err := svc.Version(ctx, b.ID, pendingVer.ID)
		if err != nil {
			t.Fatalf("Version v2 reload failed: %v", err)
		}
		if reloadedV2.Status != core.VersionStatusCancelled {
			t.Errorf("expected processing v2 to be cancelled, got %s", reloadedV2.Status)
		}

		assertReservationState(ctx, t, baseStore, b.UserID, reservationID, core.ReservationStatusReleased, "superseded by branch update")

		staleComplete := *pendingVer
		staleComplete.Status = core.VersionStatusCompleted
		if err := baseStore.CompleteBranchTailor(ctx, claimedJob.ID, claimedJob.ClaimedBy, &staleComplete); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("expected stale CompleteBranchTailor to return ErrConflict, got %v", err)
		}

		afterBranch, err := svc.Branch(ctx, b.UserID, b.ID)
		if err != nil {
			t.Fatalf("Branch refetch failed: %v", err)
		}
		if afterBranch.ActiveVersionID == nil || *afterBranch.ActiveVersionID != v1.ID {
			t.Errorf("expected ActiveVersionID to remain %s, got %v", v1.ID, afterBranch.ActiveVersionID)
		}
	})

	t.Run("store rejects stale processing CompleteBranchTailor via epoch bump even before abort runs", func(t *testing.T) {
		ctx, baseStore, svc, b, v1, pendingVer, claimedJob, _ := setupTailoredWithActiveAndProcessing(t, true)

		b.ActiveVersionID = &v1.ID
		if err := baseStore.UpdateBranch(ctx, b); err != nil {
			t.Fatalf("direct store UpdateBranch failed: %v", err)
		}

		staleComplete := *pendingVer
		staleComplete.Status = core.VersionStatusCompleted
		if err := baseStore.CompleteBranchTailor(ctx, claimedJob.ID, claimedJob.ClaimedBy, &staleComplete); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("expected ErrConflict from epoch guard while still processing, got %v", err)
		}

		afterBranch, err := svc.Branch(ctx, b.UserID, b.ID)
		if err != nil {
			t.Fatalf("Branch refetch failed: %v", err)
		}
		if afterBranch.ActiveVersionID == nil || *afterBranch.ActiveVersionID != v1.ID {
			t.Errorf("expected ActiveVersionID to remain %s, got %v", v1.ID, afterBranch.ActiveVersionID)
		}
	})

	t.Run("non-config metadata update does not abort in-flight processing version", func(t *testing.T) {
		ctx, baseStore, svc, b, _, pendingVer, claimedJob, reservationID := setupTailoredWithActiveAndProcessing(t, false)

		b.Name = "Renamed Metadata Only"
		b.PinnedEntityIDs = []string{}
		b.ExcludedEntityIDs = []string{}
		updated, err := svc.UpdateBranch(ctx, b)
		if err != nil {
			t.Fatalf("UpdateBranch metadata failed: %v", err)
		}
		if updated.Name != "Renamed Metadata Only" {
			t.Fatalf("expected updated Name, got %q", updated.Name)
		}

		reloadedV2, err := svc.Version(ctx, b.ID, pendingVer.ID)
		if err != nil {
			t.Fatalf("Version v2 reload failed: %v", err)
		}
		if reloadedV2.Status != core.VersionStatusProcessing {
			t.Errorf("expected processing v2 to remain processing, got %s", reloadedV2.Status)
		}
		assertReservationState(ctx, t, baseStore, b.UserID, reservationID, core.ReservationStatusPending, "")

		validComplete := *pendingVer
		validComplete.Status = core.VersionStatusCompleted
		if err := baseStore.CompleteBranchTailor(ctx, claimedJob.ID, claimedJob.ClaimedBy, &validComplete); err != nil {
			t.Fatalf("expected CompleteBranchTailor to succeed after metadata update, got %v", err)
		}

		afterBranch, err := svc.Branch(ctx, b.UserID, b.ID)
		if err != nil {
			t.Fatalf("Branch refetch failed: %v", err)
		}
		if afterBranch.ActiveVersionID == nil || *afterBranch.ActiveVersionID != pendingVer.ID {
			t.Errorf("expected ActiveVersionID=%s, got %v", pendingVer.ID, afterBranch.ActiveVersionID)
		}
		if afterBranch.Name != "Renamed Metadata Only" {
			t.Errorf("expected Name preserved after CompleteBranchTailor, got %q", afterBranch.Name)
		}
	})
}

func TestService_ClockInjectionAcrossEditRenderAndATS(t *testing.T) {
	ctx := context.Background()
	userID := "user-clock-" + uuid.New().String()
	store := newSeededBranchStore(ctx, userID)
	fixedNow := time.Date(2024, 3, 15, 9, 30, 0, 0, time.UTC)

	exp := &core.WorkExperience{
		ID:        "exp-clock-1",
		UserID:    userID,
		Company:   "Acme Corp",
		RoleTitle: "Staff Engineer",
		StartDate: core.NewDate(2020, 1, 1),
	}
	if err := store.Career().CreateExperience(ctx, exp); err != nil {
		t.Fatalf("CreateExperience failed: %v", err)
	}
	fact := &core.Fact{
		ID:        "fact-clock-1",
		UserID:    userID,
		OwnerID:   exp.ID,
		OwnerType: core.OwnerTypeWorkExperience,
		Statement: "Built deterministic Go services.",
	}
	if err := store.Career().CreateFact(ctx, fact); err != nil {
		t.Fatalf("CreateFact failed: %v", err)
	}

	svc := branch.New(
		branch.WithStore(store),
		branch.WithValidator(provenance.New()),
		branch.WithRenderer(renderer.New()),
		branch.WithClock(func() time.Time { return fixedNow }),
	)

	tailored, err := svc.CreateBranch(ctx, &core.Branch{
		ID:                 "branch-clock-" + uuid.New().String(),
		UserID:             userID,
		Name:               "Clock Branch",
		TargetRequirements: "Go, PostgreSQL, deterministic services",
		TemplateSetID:      core.TemplateStandardCleanID,
	})
	if err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}

	v1 := &core.BranchVersion{
		ID:                "ver-clock-1",
		UserID:            userID,
		BranchID:          tailored.ID,
		Status:            core.VersionStatusCompleted,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: core.TemplateStandardCleanID,
		SourceType:        core.SourceTypeLLMGeneration,
		MarkdownOutput:    "# Staff Engineer\nBuilt deterministic Go services.",
	}
	if err := store.seedCompletedVersion(ctx, v1); err != nil {
		t.Fatalf("CompleteVersion failed: %v", err)
	}

	edited, err := svc.EditVersion(ctx, userID, tailored.ID, v1.ID, core.TailoredContent{
		Summary: "Go systems engineer",
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID: exp.ID,
				Company:        exp.Company,
				RoleTitle:      exp.RoleTitle,
				StartDate:      exp.StartDate,
				Bullets: []core.TailoredBullet{
					{
						Text:          "Built deterministic Go services.",
						SourceFactIDs: []string{fact.ID},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("EditVersion failed: %v", err)
	}
	if !edited.CreatedAt.Equal(fixedNow) {
		t.Errorf("EditVersion CreatedAt = %v, want %v", edited.CreatedAt, fixedNow)
	}

	defBranch, err := svc.Branch(ctx, userID, core.DefaultBranchID(userID))
	if err != nil {
		t.Fatalf("Branch default failed: %v", err)
	}
	defBranch.TemplateSetID = core.TemplateDetailedImmigrationCVID
	updatedDef, err := svc.UpdateBranch(ctx, defBranch)
	if err != nil {
		t.Fatalf("UpdateBranch default failed: %v", err)
	}
	if updatedDef.ActiveVersionID == nil {
		t.Fatalf("expected ActiveVersionID on re-rendered default branch")
	}
	defVer, err := svc.Version(ctx, defBranch.ID, *updatedDef.ActiveVersionID)
	if err != nil {
		t.Fatalf("Version default failed: %v", err)
	}
	if !defVer.CreatedAt.Equal(fixedNow) {
		t.Errorf("default branch re-render CreatedAt = %v, want %v", defVer.CreatedAt, fixedNow)
	}

	score, err := svc.SimulateATS(ctx, userID, tailored.ID, &core.ATSSimulationOptions{
		VersionID: edited.ID,
	})
	if err != nil {
		t.Fatalf("SimulateATS deterministic failed: %v", err)
	}
	if score.EvaluatedAt == nil || !score.EvaluatedAt.Equal(fixedNow) {
		t.Errorf("SimulateATS deterministic EvaluatedAt = %v, want %v", score.EvaluatedAt, fixedNow)
	}

	svcWithGen := branch.New(
		branch.WithStore(store),
		branch.WithRenderer(renderer.New()),
		branch.WithGenerator(&stubATSSimulator{}),
		branch.WithClock(func() time.Time { return fixedNow }),
	)
	genScore, err := svcWithGen.SimulateATS(ctx, userID, tailored.ID, &core.ATSSimulationOptions{
		VersionID: v1.ID,
	})
	if err != nil {
		t.Fatalf("SimulateATS generator failed: %v", err)
	}
	if genScore.EvaluatedAt == nil || !genScore.EvaluatedAt.Equal(fixedNow) {
		t.Errorf("SimulateATS generator EvaluatedAt = %v, want %v", genScore.EvaluatedAt, fixedNow)
	}
}

type permissiveTenantStore struct {
	*testBranchStore
	ownerID string
}

func (s *permissiveTenantStore) Branch(ctx context.Context, _ string, id string) (*core.Branch, error) {
	return s.Store.Branch(ctx, "", id)
}

func (s *permissiveTenantStore) ListBranches(ctx context.Context, _ string) ([]core.Branch, error) {
	return s.Store.ListBranches(ctx, s.ownerID)
}

func TestService_GenerateVersionAndSyncBranches_RejectEmptyAndWhitespaceUserID(t *testing.T) {
	setupVictim := func(t *testing.T, withStore bool, permissive bool) (
		context.Context,
		*testBranchStore,
		*spyQuotaRepo,
		*stubNotifier,
		*branch.Service,
		*core.Branch,
		*core.BranchVersion,
	) {
		t.Helper()
		ctx := context.Background()
		victim := "victim-user"
		store := newSeededBranchStore(ctx, victim)
		if _, err := store.Profiles().IncrementMainRevision(ctx, victim); err != nil {
			t.Fatalf("IncrementMainRevision: %v", err)
		}
		victimBranch := &core.Branch{
			ID:                 "br-victim-gen",
			UserID:             victim,
			Name:               "Victim Branch",
			TargetRequirements: "Go, distributed systems",
			TemplateSetID:      core.TemplateStandardCleanID,
		}
		if err := store.CreateBranch(ctx, victimBranch); err != nil {
			t.Fatalf("CreateBranch victim: %v", err)
		}
		victimPending := &core.BranchVersion{
			ID:       "ver-victim-pending",
			UserID:   victim,
			BranchID: victimBranch.ID,
		}
		if err := store.seedVersionInState(ctx, victimPending, core.VersionStatusPending, "res-victim-existing"); err != nil {
			t.Fatalf("seed victim pending: %v", err)
		}

		quotaSpy := &spyQuotaRepo{}
		notifier := &stubNotifier{}
		opts := []branch.Option{branch.WithJobNotifier(notifier)}
		if withStore {
			var st branch.Store = store
			if permissive {
				st = &permissiveTenantStore{testBranchStore: store, ownerID: victim}
			}
			opts = append(opts, branch.WithStore(&quotaOverrideStore{Store: st, quotas: quotaSpy}))
		}
		svc := branch.New(opts...)
		return ctx, store, quotaSpy, notifier, svc, victimBranch, victimPending
	}

	for _, invalidUser := range []struct {
		label  string
		userID string
	}{
		{label: "empty userID", userID: ""},
		{label: "whitespace userID", userID: "   "},
	} {
		for _, withStore := range []bool{true, false} {
			storeLabel := "store_nil"
			if withStore {
				storeLabel = "store_configured"
			}

			t.Run("GenerateVersion/"+invalidUser.label+"/"+storeLabel, func(t *testing.T) {
				ctx, st, quotaSpy, notifier, svc, victimBranch, victimPending := setupVictim(t, withStore, false)

				_, err := svc.GenerateVersion(ctx, invalidUser.userID, victimBranch.ID)
				if !errors.Is(err, core.ErrValidation) {
					t.Fatalf("expected ErrValidation, got %v", err)
				}

				quotaSpy.mu.Lock()
				resCount := len(quotaSpy.reservations)
				relCount := len(quotaSpy.releases)
				quotaSpy.mu.Unlock()
				if resCount != 0 || relCount != 0 {
					t.Errorf("expected zero quota reservations/releases, got reservations=%d releases=%d", resCount, relCount)
				}
				if notifier.Count() != 0 {
					t.Errorf("expected zero worker notifications, got %d", notifier.Count())
				}
				gotPending, err := st.Version(ctx, victimBranch.ID, victimPending.ID)
				if err != nil || gotPending.Status != core.VersionStatusPending {
					t.Errorf("expected victim pending version untouched, got %+v err=%v", gotPending, err)
				}
			})

			t.Run("SyncBranches_explicit/"+invalidUser.label+"/"+storeLabel, func(t *testing.T) {
				ctx, st, quotaSpy, notifier, svc, victimBranch, victimPending := setupVictim(t, withStore, false)

				ids, err := svc.SyncBranches(ctx, invalidUser.userID, []string{victimBranch.ID})
				if !errors.Is(err, core.ErrValidation) {
					t.Fatalf("expected ErrValidation, got %v", err)
				}
				if len(ids) != 0 {
					t.Errorf("expected zero synced IDs, got %v", ids)
				}

				quotaSpy.mu.Lock()
				resCount := len(quotaSpy.reservations)
				relCount := len(quotaSpy.releases)
				quotaSpy.mu.Unlock()
				if resCount != 0 || relCount != 0 {
					t.Errorf("expected zero quota reservations/releases, got reservations=%d releases=%d", resCount, relCount)
				}
				if notifier.Count() != 0 {
					t.Errorf("expected zero worker notifications, got %d", notifier.Count())
				}
				gotPending, err := st.Version(ctx, victimBranch.ID, victimPending.ID)
				if err != nil || gotPending.Status != core.VersionStatusPending {
					t.Errorf("expected victim pending version untouched, got %+v err=%v", gotPending, err)
				}
			})

			t.Run("SyncBranches_allStale/"+invalidUser.label+"/"+storeLabel, func(t *testing.T) {
				ctx, st, quotaSpy, notifier, svc, victimBranch, victimPending := setupVictim(t, withStore, false)

				ids, err := svc.SyncBranches(ctx, invalidUser.userID, nil)
				if !errors.Is(err, core.ErrValidation) {
					t.Fatalf("expected ErrValidation, got %v", err)
				}
				if len(ids) != 0 {
					t.Errorf("expected zero synced IDs, got %v", ids)
				}
				quotaSpy.mu.Lock()
				resCount := len(quotaSpy.reservations)
				quotaSpy.mu.Unlock()
				if resCount != 0 || notifier.Count() != 0 {
					t.Errorf("expected zero reservations and notifications, got reservations=%d notifications=%d", resCount, notifier.Count())
				}
				gotPending, err := st.Version(ctx, victimBranch.ID, victimPending.ID)
				if err != nil || gotPending.Status != core.VersionStatusPending {
					t.Errorf("expected victim pending version untouched, got %+v err=%v", gotPending, err)
				}
			})
		}
	}

	t.Run("permissive store foreign branch rejected in GenerateVersion and SyncBranches", func(t *testing.T) {
		ctx, store, quotaSpy, notifier, svc, victimBranch, victimPending := setupVictim(t, true, true)
		attacker := "attacker-user"
		if err := store.UpsertProfile(ctx, &core.Profile{ID: "prof-att", UserID: attacker, FullName: "Attacker", MainRevision: 1}); err != nil {
			t.Fatalf("UpsertProfile attacker: %v", err)
		}

		if _, err := svc.GenerateVersion(ctx, attacker, victimBranch.ID); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("expected ErrNotFound for foreign GenerateVersion, got %v", err)
		}
		if _, err := svc.SyncBranches(ctx, attacker, []string{victimBranch.ID}); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("expected ErrNotFound for foreign SyncBranches explicit, got %v", err)
		}
		ids, err := svc.SyncBranches(ctx, attacker, nil)
		if err != nil {
			t.Fatalf("unexpected error for empty stale list: %v", err)
		}
		if len(ids) != 0 {
			t.Errorf("expected zero synced IDs when only foreign stale branch exists, got %v", ids)
		}

		quotaSpy.mu.Lock()
		resCount := len(quotaSpy.reservations)
		quotaSpy.mu.Unlock()
		if resCount != 0 || notifier.Count() != 0 {
			t.Errorf("expected zero reservations and notifications, got reservations=%d notifications=%d", resCount, notifier.Count())
		}
		gotPending, err := svc.Version(ctx, victimBranch.ID, victimPending.ID)
		if err != nil || gotPending.Status != core.VersionStatusPending {
			t.Errorf("expected victim pending version untouched, got %+v err=%v", gotPending, err)
		}
	})
}

func TestService_TenantScopedEntryPoints_RejectEmptyAndMismatchedUserID(t *testing.T) {
	ctx := context.Background()
	owner := "tenant-owner"
	baseStore := newSeededBranchStore(ctx, owner)
	permStore := &permissiveTenantStore{testBranchStore: baseStore, ownerID: owner}
	quotaSpy := &spyQuotaRepo{}

	exp := &core.WorkExperience{
		ID:        "exp-tenant-1",
		UserID:    owner,
		Company:   "Acme",
		RoleTitle: "Engineer",
		StartDate: core.NewDate(2020, 1, 1),
	}
	if err := baseStore.Career().CreateExperience(ctx, exp); err != nil {
		t.Fatalf("CreateExperience: %v", err)
	}
	fact := &core.Fact{
		ID:        "fact-tenant-1",
		UserID:    owner,
		OwnerID:   exp.ID,
		OwnerType: core.OwnerTypeWorkExperience,
		Statement: "Shipped core services.",
	}
	if err := baseStore.Career().CreateFact(ctx, fact); err != nil {
		t.Fatalf("CreateFact: %v", err)
	}

	victimBranch := &core.Branch{
		ID:                 "br-tenant-victim",
		UserID:             owner,
		Name:               "Victim Tailored",
		TargetRequirements: "Go, PostgreSQL",
		TemplateSetID:      core.TemplateStandardCleanID,
	}
	if err := baseStore.CreateBranch(ctx, victimBranch); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	completedVer := &core.BranchVersion{
		ID:               "ver-tenant-done",
		UserID:           owner,
		BranchID:         victimBranch.ID,
		Status:           core.VersionStatusCompleted,
		MainRevisionUsed: 1,
		MarkdownOutput:   "# Resume\nShipped core services.",
	}
	if err := baseStore.seedCompletedVersion(ctx, completedVer); err != nil {
		t.Fatalf("seedCompletedVersion: %v", err)
	}
	pendingVer := &core.BranchVersion{
		ID:       "ver-tenant-pending",
		UserID:   owner,
		BranchID: victimBranch.ID,
	}
	if err := baseStore.seedVersionInState(ctx, pendingVer, core.VersionStatusPending, "res-tenant-pending"); err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	svc := branch.New(
		branch.WithStore(&quotaOverrideStore{Store: permStore, quotas: quotaSpy}),
		branch.WithGenerator(&stubATSSimulator{stubGenerator: stubGenerator{gapReport: &core.GapReport{OverallMatchSummary: "ok"}}}),
		branch.WithPromptCompiler(&fakePromptCompiler{}),
	)

	editPayload := core.TailoredContent{Summary: "Updated"}

	for _, badUser := range []string{"", "   "} {
		cases := []struct {
			name string
			call func() error
		}{
			{name: "ListBranches", call: func() error { _, err := svc.ListBranches(ctx, badUser, false); return err }},
			{name: "Branch", call: func() error { _, err := svc.Branch(ctx, badUser, victimBranch.ID); return err }},
			{name: "CreateBranch", call: func() error {
				_, err := svc.CreateBranch(ctx, &core.Branch{UserID: badUser, Name: "New", TemplateSetID: core.TemplateStandardCleanID})
				return err
			}},
			{name: "UpdateBranch", call: func() error {
				_, err := svc.UpdateBranch(ctx, &core.Branch{ID: victimBranch.ID, UserID: badUser, Name: "Hack", TemplateSetID: core.TemplateStandardCleanID})
				return err
			}},
			{name: "DeleteBranch", call: func() error { return svc.DeleteBranch(ctx, badUser, victimBranch.ID) }},
			{name: "GenerateVersion", call: func() error { _, err := svc.GenerateVersion(ctx, badUser, victimBranch.ID); return err }},
			{name: "SyncBranches", call: func() error { _, err := svc.SyncBranches(ctx, badUser, []string{victimBranch.ID}); return err }},
			{name: "EditVersion", call: func() error {
				_, err := svc.EditVersion(ctx, badUser, victimBranch.ID, completedVer.ID, editPayload)
				return err
			}},
			{name: "AnalyzeGaps", call: func() error { _, err := svc.AnalyzeGaps(ctx, badUser, victimBranch.ID); return err }},
			{name: "SimulateATS", call: func() error {
				_, err := svc.SimulateATS(ctx, badUser, victimBranch.ID, &core.ATSSimulationOptions{VersionID: completedVer.ID})
				return err
			}},
			{name: "PromptPreview", call: func() error {
				_, err := svc.PromptPreview(ctx, badUser, victimBranch.ID, nil)
				return err
			}},
		}
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s/user=%q", tc.name, badUser), func(t *testing.T) {
				if err := tc.call(); !errors.Is(err, core.ErrValidation) {
					t.Errorf("expected ErrValidation for %s with user %q, got %v", tc.name, badUser, err)
				}
			})
		}
	}

	t.Run("nil branch rejected by CreateBranch and UpdateBranch", func(t *testing.T) {
		if _, err := svc.CreateBranch(ctx, nil); !errors.Is(err, core.ErrValidation) {
			t.Errorf("CreateBranch(nil) expected ErrValidation, got %v", err)
		}
		if _, err := svc.UpdateBranch(ctx, nil); !errors.Is(err, core.ErrValidation) {
			t.Errorf("UpdateBranch(nil) expected ErrValidation, got %v", err)
		}
	})

	attacker := "attacker-tenant"
	foreignCases := []struct {
		name string
		call func() error
	}{
		{name: "Branch", call: func() error { _, err := svc.Branch(ctx, attacker, victimBranch.ID); return err }},
		{name: "UpdateBranch", call: func() error {
			_, err := svc.UpdateBranch(ctx, &core.Branch{ID: victimBranch.ID, UserID: attacker, Name: "Hijack", TemplateSetID: core.TemplateStandardCleanID})
			return err
		}},
		{name: "DeleteBranch", call: func() error { return svc.DeleteBranch(ctx, attacker, victimBranch.ID) }},
		{name: "GenerateVersion", call: func() error { _, err := svc.GenerateVersion(ctx, attacker, victimBranch.ID); return err }},
		{name: "SyncBranches", call: func() error { _, err := svc.SyncBranches(ctx, attacker, []string{victimBranch.ID}); return err }},
		{name: "EditVersion", call: func() error {
			_, err := svc.EditVersion(ctx, attacker, victimBranch.ID, completedVer.ID, editPayload)
			return err
		}},
		{name: "AnalyzeGaps", call: func() error { _, err := svc.AnalyzeGaps(ctx, attacker, victimBranch.ID); return err }},
		{name: "SimulateATS", call: func() error {
			_, err := svc.SimulateATS(ctx, attacker, victimBranch.ID, &core.ATSSimulationOptions{VersionID: completedVer.ID})
			return err
		}},
		{name: "PromptPreview", call: func() error {
			_, err := svc.PromptPreview(ctx, attacker, victimBranch.ID, nil)
			return err
		}},
	}
	for _, tc := range foreignCases {
		t.Run("foreign_branch/"+tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, core.ErrNotFound) {
				t.Errorf("expected ErrNotFound for %s on foreign branch, got %v", tc.name, err)
			}
		})
	}

	quotaSpy.mu.Lock()
	resCount := len(quotaSpy.reservations)
	quotaSpy.mu.Unlock()
	if resCount != 0 {
		t.Errorf("expected zero quota reservations for invalid/foreign calls, got %d", resCount)
	}
	gotPending, err := svc.Version(ctx, victimBranch.ID, pendingVer.ID)
	if err != nil || gotPending.Status != core.VersionStatusPending {
		t.Errorf("expected victim pending version untouched, got %+v err=%v", gotPending, err)
	}
	gotBranch, err := svc.Branch(ctx, owner, victimBranch.ID)
	if err != nil || gotBranch.Name != "Victim Tailored" {
		t.Errorf("expected victim branch untouched, got %+v err=%v", gotBranch, err)
	}
}
