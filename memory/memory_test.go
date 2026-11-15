package memory_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/memory"
)

func setupTestStore(t *testing.T) *memory.Store {
	t.Helper()
	return memory.New()
}

// TestMemoryStore_Interfaces verifies that memory.Store satisfies domain store interfaces.
func TestMemoryStore_Interfaces(t *testing.T) {
	store := setupTestStore(t)

	// Compile-time interface assertions verifying that memory.Store directly satisfies core.Store and all individual store interfaces.
	var (
		_ core.Store         = store
		_ core.ProfileStore  = store
		_ core.CareerStore   = store
		_ core.BranchStore   = store
		_ core.TemplateStore = store
		_ core.PresetStore   = store
		_ core.PDFJobStore   = store
		_ core.QuotaStore    = store
		_ core.UserStore     = store
	)

	if store.Profiles() == nil || store.Career() == nil || store.Branches() == nil || store.Presets() == nil || store.Templates() == nil || store.PDFJobs() == nil || store.Quotas() == nil || store.Users() == nil {
		t.Fatal("expected non-nil store handles from core.Store accessors")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("expected clean close: %v", err)
	}
}

// TestProfileStore_MainRevisionIncrements asserts that profile mutations increment main revision.
func TestProfileStore_MainRevisionIncrements(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	userID := "user-rev-test"

	prof := &core.Profile{
		ID:       "prof-1",
		UserID:   userID,
		FullName: "Initial User",
	}
	if err := store.UpsertProfile(ctx, prof); err != nil {
		t.Fatalf("failed to insert profile: %v", err)
	}
	if prof.MainRevision != 1 {
		t.Errorf("expected initial main revision 1, got %d", prof.MainRevision)
	}

	exp := &core.WorkExperience{
		ID:        "exp-1",
		UserID:    userID,
		Company:   "Acme Corp",
		RoleTitle: "Staff Engineer",
		StartDate: core.NewDate(2024, 1, 1),
	}
	if err := store.CreateExperience(ctx, exp); err != nil {
		t.Fatalf("failed to create experience: %v", err)
	}

	p, err := store.Profile(ctx, userID)
	if err != nil {
		t.Fatalf("failed to get profile: %v", err)
	}
	if p.MainRevision != 2 {
		t.Errorf("expected main revision 2 after experience creation, got %d", p.MainRevision)
	}

	rev, err := store.IncrementMainRevision(ctx, userID)
	if err != nil {
		t.Fatalf("failed manual increment: %v", err)
	}
	if rev != 3 {
		t.Errorf("expected revision 3, got %d", rev)
	}
}

// TestProfileStore_FactsAndSnapshot verifies facts association and snapshot generation.
func TestProfileStore_FactsAndSnapshot(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	userID := "user-facts-test"

	prof := &core.Profile{ID: "prof-facts", UserID: userID, FullName: "Facts Engineer"}
	if err := store.UpsertProfile(ctx, prof); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}

	skill1 := &core.Skill{ID: "sk-go", UserID: userID, Name: "Go"}
	if err := store.CreateSkill(ctx, skill1); err != nil {
		t.Fatalf("create skill: %v", err)
	}

	exp := &core.WorkExperience{
		ID:        "exp-poly",
		UserID:    userID,
		Company:   "Cloud Scale Inc",
		RoleTitle: "Backend Lead",
		StartDate: core.NewDate(2023, 1, 1),
		Skills:    []core.Skill{*skill1},
	}
	if err := store.CreateExperience(ctx, exp); err != nil {
		t.Fatalf("create exp: %v", err)
	}

	fact := &core.Fact{
		ID:        "fact-poly-1",
		UserID:    userID,
		OwnerID:   exp.ID,
		OwnerType: core.OwnerTypeWorkExperience,
		Statement: "Architected real-time microservices handling 50k QPS",
		Skills:    []core.Skill{*skill1},
	}
	if err := store.CreateFact(ctx, fact); err != nil {
		t.Fatalf("create fact: %v", err)
	}

	facts, err := store.ListFactsByOwner(ctx, userID, string(core.OwnerTypeWorkExperience), exp.ID)
	if err != nil {
		t.Fatalf("list facts: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].Statement != fact.Statement {
		t.Errorf("expected statement %q, got %q", fact.Statement, facts[0].Statement)
	}

	snap, err := store.Snapshot(ctx, userID)
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if len(snap.Experiences) != 1 {
		t.Fatalf("expected 1 experience in snapshot, got %d", len(snap.Experiences))
	}
	if len(snap.Experiences[0].Facts) != 1 {
		t.Errorf("expected 1 preloaded fact on experience, got %d", len(snap.Experiences[0].Facts))
	}
}

// TestProfileStore_ReplaceCareerGraph verifies atomic replacement of career records.
func TestProfileStore_ReplaceCareerGraph(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	userID := "user-replace-test"

	prof := &core.Profile{ID: "prof-rep", UserID: userID, FullName: "Graph Engineer"}
	if err := store.UpsertProfile(ctx, prof); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}

	sk1 := &core.Skill{ID: "sk-old", UserID: userID, Name: "OldSkill"}
	if err := store.CreateSkill(ctx, sk1); err != nil {
		t.Fatalf("create old skill: %v", err)
	}

	newSkill := core.Skill{ID: "sk-new", UserID: userID, Name: "NewSkill"}
	newExp := core.WorkExperience{
		ID:        "exp-new",
		UserID:    userID,
		Company:   "NewCo",
		RoleTitle: "Director",
		StartDate: core.NewDate(2024, 6, 1),
		Skills:    []core.Skill{newSkill},
	}
	newFact := core.Fact{
		ID:        "fact-new",
		UserID:    userID,
		OwnerID:   newExp.ID,
		OwnerType: core.OwnerTypeWorkExperience,
		Statement: "Expanded engineering team from 10 to 50",
		Skills:    []core.Skill{newSkill},
	}

	snap := &core.MainProfileSnapshot{
		Profile:      *prof,
		Skills:       []core.Skill{newSkill},
		Experiences:  []core.WorkExperience{newExp},
		Facts:        []core.Fact{newFact},
		MainRevision: 10,
	}

	if err := store.ReplaceCareerGraph(ctx, userID, snap); err != nil {
		t.Fatalf("replace career graph: %v", err)
	}

	skills, err := store.ListSkills(ctx, userID)
	if err != nil {
		t.Fatalf("list skills: %v", err)
	}
	if len(skills) != 1 || skills[0].ID != newSkill.ID {
		t.Errorf("expected only newSkill, got %+v", skills)
	}

	exps, err := store.ListExperiences(ctx, userID)
	if err != nil {
		t.Fatalf("list experiences: %v", err)
	}
	if len(exps) != 1 || exps[0].ID != newExp.ID {
		t.Errorf("expected only newExp, got %+v", exps)
	}
}

// TestPresetStore_Immutability verifies built-in preset protection.
func TestPresetStore_Immutability(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	userID := "user-preset-test"

	builtin := &core.Preset{
		ID:                 core.Preset1PageTechResumeID,
		UserID:             userID,
		Name:               "Builtin Tech",
		SystemInstructions: "Focus on tech metrics",
	}
	if err := store.CreatePreset(ctx, builtin); err != nil {
		t.Fatalf("create builtin preset: %v", err)
	}

	builtin.Name = "Attempted Mutation"
	err := store.UpdatePreset(ctx, builtin)
	if !errors.Is(err, core.ErrStrategyImmutable) {
		t.Errorf("expected ErrStrategyImmutable on update, got: %v", err)
	}

	err = store.DeletePreset(ctx, userID, builtin.ID)
	if !errors.Is(err, core.ErrStrategyImmutable) {
		t.Errorf("expected ErrStrategyImmutable on delete, got: %v", err)
	}

	custom := &core.Preset{
		ID:                 "custom-preset-1",
		UserID:             userID,
		Name:               "Custom Preset",
		SystemInstructions: "Custom rules",
	}
	if err := store.CreatePreset(ctx, custom); err != nil {
		t.Fatalf("create custom preset: %v", err)
	}
	custom.Name = "Updated Custom Preset"
	if err := store.UpdatePreset(ctx, custom); err != nil {
		t.Fatalf("update custom preset: %v", err)
	}
	if err := store.DeletePreset(ctx, userID, custom.ID); err != nil {
		t.Fatalf("delete custom preset: %v", err)
	}
}

// TestPresetStore_LanguageDefaulting verifies that empty language strings default to DefaultLanguage on Create and Update.
func TestPresetStore_LanguageDefaulting(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	userID := "user-lang-test"

	preset := &core.Preset{
		ID:                 "preset-lang-default",
		UserID:             userID,
		Name:               "Default Language Preset",
		SystemInstructions: "Test instructions",
		// Language is empty
	}
	if err := store.CreatePreset(ctx, preset); err != nil {
		t.Fatalf("create preset: %v", err)
	}

	fetched, err := store.Preset(ctx, userID, preset.ID)
	if err != nil {
		t.Fatalf("get preset: %v", err)
	}
	if fetched.Language != core.DefaultLanguage {
		t.Errorf("expected default language %q on create, got: %q", core.DefaultLanguage, fetched.Language)
	}

	// Update with explicit language
	fetched.Language = "Spanish"
	if err := store.UpdatePreset(ctx, fetched); err != nil {
		t.Fatalf("update preset with Spanish: %v", err)
	}
	fetched2, err := store.Preset(ctx, userID, preset.ID)
	if err != nil {
		t.Fatalf("get preset after update: %v", err)
	}
	if fetched2.Language != "Spanish" {
		t.Errorf("expected Spanish, got: %q", fetched2.Language)
	}

	// Update with empty language -> should default back to DefaultLanguage
	fetched2.Language = ""
	if err := store.UpdatePreset(ctx, fetched2); err != nil {
		t.Fatalf("update preset with empty language: %v", err)
	}
	fetched3, err := store.Preset(ctx, userID, preset.ID)
	if err != nil {
		t.Fatalf("get preset after empty language update: %v", err)
	}
	if fetched3.Language != core.DefaultLanguage {
		t.Errorf("expected default language %q when updated with empty string, got: %q", core.DefaultLanguage, fetched3.Language)
	}
}

// TestTemplateStore_Validation verifies template validation on persistence.
func TestTemplateStore_Validation(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	userID := "user-tmpl-test"

	// In the decoupled architecture, syntax validation belongs to domain/services,
	// while memory.Store persists template records directly.
	invalidTmpl := &core.TemplateSet{
		ID:               "raw-syntax-tmpl",
		UserID:           userID,
		Name:             "Raw Syntax",
		HTMLTemplate:     "<div>{{ unclosed tag",
		MarkdownTemplate: "# Resume",
		TextTemplate:     "Plain text",
	}
	if err := (core.DefaultTemplateValidator{}).Validate(invalidTmpl); err == nil {
		t.Fatal("expected domain validation error on invalid template syntax, got nil")
	}
	if err := store.CreateTemplate(ctx, invalidTmpl); err != nil {
		t.Fatalf("expected memory store to persist template record without validation: %v", err)
	}

	validTmpl := &core.TemplateSet{
		ID:               "valid-tmpl",
		UserID:           userID,
		Name:             "Valid Template",
		HTMLTemplate:     "<div>{{ .Profile.FullName }}</div>",
		MarkdownTemplate: "# {{ .Profile.FullName }}",
		TextTemplate:     "Name: {{ .Profile.FullName }}",
	}
	if err := store.CreateTemplate(ctx, validTmpl); err != nil {
		t.Fatalf("create valid template: %v", err)
	}

	fetched, err := store.Template(ctx, userID, validTmpl.ID)
	if err != nil {
		t.Fatalf("get template: %v", err)
	}
	if fetched.Name != validTmpl.Name {
		t.Errorf("expected template name %s, got %s", validTmpl.Name, fetched.Name)
	}
}

// TestBranchStore_LifecycleAndVersions verifies version sequencing and completion logic.
func TestBranchStore_LifecycleAndVersions(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	userID := "user-branch-test"

	p := &core.Profile{ID: "prof-br", UserID: userID, FullName: "Branch Engineer", MainRevision: 3}
	if err := store.UpsertProfile(ctx, p); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}

	branch := &core.Branch{
		ID:                     "br-test-1",
		UserID:                 userID,
		Name:                   "Tech Resume Branch",
		TemplateSetID:          core.TemplateStandardCleanID,
		LastSyncedMainRevision: 1,
	}
	if err := store.CreateBranch(ctx, branch); err != nil {
		t.Fatalf("create branch: %v", err)
	}

	fetchedBranch, err := store.Branch(ctx, userID, branch.ID)
	if err != nil {
		t.Fatalf("get branch: %v", err)
	}
	if !fetchedBranch.IsStale {
		t.Errorf("expected branch to be stale because last synced (1) < main revision (3)")
	}

	v1 := &core.BranchVersion{
		ID:               "ver-1",
		BranchID:         branch.ID,
		Status:           core.VersionStatusProcessing,
		MainRevisionUsed: 3,
	}
	if err := store.CreateVersion(ctx, v1); err != nil {
		t.Fatalf("create version 1: %v", err)
	}
	if v1.VersionNumber != 1 {
		t.Errorf("expected version number 1, got %d", v1.VersionNumber)
	}

	v2 := &core.BranchVersion{
		ID:               "ver-2",
		BranchID:         branch.ID,
		Status:           core.VersionStatusPending,
		MainRevisionUsed: 3,
	}
	if err := store.CreateVersion(ctx, v2); err != nil {
		t.Fatalf("create version 2: %v", err)
	}
	if v2.VersionNumber != 2 {
		t.Errorf("expected sequential version number 2, got %d", v2.VersionNumber)
	}

	v2.HTMLOutput = "<html>Rendered</html>"
	if err := store.CompleteVersion(ctx, v2); err != nil {
		t.Fatalf("complete version 2: %v", err)
	}

	updatedBranch, err := store.Branch(ctx, userID, branch.ID)
	if err != nil {
		t.Fatalf("get branch after completion: %v", err)
	}
	if updatedBranch.ActiveVersionID == nil || *updatedBranch.ActiveVersionID != v2.ID {
		t.Fatalf("expected active version ID %s, got %+v", v2.ID, updatedBranch.ActiveVersionID)
	}
	if updatedBranch.LastSyncedMainRevision != 3 {
		t.Errorf("expected last synced main revision 3, got %d", updatedBranch.LastSyncedMainRevision)
	}
	if updatedBranch.IsStale {
		t.Errorf("expected branch to no longer be stale")
	}

	v3Pending := &core.BranchVersion{
		ID:               "ver-3-unexpired-pending",
		BranchID:         branch.ID,
		Status:           core.VersionStatusPending,
		MainRevisionUsed: 3,
		ExpiresAt:        ptrTime(time.Now().UTC().Add(15 * time.Minute)),
	}
	if err := store.CreateVersion(ctx, v3Pending); err != nil {
		t.Fatalf("create version 3: %v", err)
	}

	if err := store.FailInterruptedVersions(ctx, "server reboot"); err != nil {
		t.Fatalf("fail interrupted: %v", err)
	}
	interruptedV1, err := store.Version(ctx, branch.ID, v1.ID)
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	if interruptedV1.Status != core.VersionStatusFailed {
		t.Errorf("expected v1 to fail after interruption, got %s", interruptedV1.Status)
	}
	if !strings.Contains(interruptedV1.ErrorMessage, "server reboot") {
		t.Errorf("expected error message to contain reason, got %q", interruptedV1.ErrorMessage)
	}
	preservedV3, err := store.Version(ctx, branch.ID, v3Pending.ID)
	if err != nil {
		t.Fatalf("get v3: %v", err)
	}
	if preservedV3.Status != core.VersionStatusPending {
		t.Errorf("expected unexpired pending v3 to remain pending, got %s", preservedV3.Status)
	}
}

// TestPDFJobStore_Lifecycle verifies PDF parse job status updates and crash recovery.
func TestPDFJobStore_Lifecycle(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	userID := "user-pdf-test"

	job := &core.PDFParseJob{
		ID:       "pdf-job-1",
		UserID:   userID,
		Status:   core.PDFParseJobStatusPending,
		Filename: "resume.pdf",
		Provider: "gemini",
		Model:    "gemini-3.8-flash",
		RawText:  "transient resume payload",
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	unexpiredPending := &core.PDFParseJob{
		ID:        "pdf-job-pending-unexpired",
		UserID:    userID,
		Status:    core.PDFParseJobStatusPending,
		Filename:  "queued.pdf",
		RawText:   "keep me while pending",
		ExpiresAt: ptrTime(time.Now().UTC().Add(15 * time.Minute)),
	}
	if err := store.CreateJob(ctx, unexpiredPending); err != nil {
		t.Fatalf("create unexpired pending job: %v", err)
	}

	got, err := store.Job(ctx, userID, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.Status != core.PDFParseJobStatusPending {
		t.Errorf("expected status pending, got %s", got.Status)
	}

	if err := store.UpdateJobStatus(ctx, job.ID, core.PDFParseJobStatusProcessing, nil, ""); err != nil {
		t.Fatalf("update to processing: %v", err)
	}

	if err := store.FailInterruptedJobs(ctx, "worker crash"); err != nil {
		t.Fatalf("fail interrupted jobs: %v", err)
	}

	crashed, err := store.Job(ctx, userID, job.ID)
	if err != nil {
		t.Fatalf("get job after crash: %v", err)
	}
	if crashed.Status != core.PDFParseJobStatusFailed {
		t.Errorf("expected failed status, got %s", crashed.Status)
	}
	if !strings.Contains(crashed.ErrorMessage, "worker crash") {
		t.Errorf("expected error message to contain reason, got %q", crashed.ErrorMessage)
	}
	if crashed.RawText != "" {
		t.Errorf("expected RawText to be pruned on terminal failed state, got %q", crashed.RawText)
	}

	stillPending, err := store.Job(ctx, userID, unexpiredPending.ID)
	if err != nil {
		t.Fatalf("get unexpired pending job: %v", err)
	}
	if stillPending.Status != core.PDFParseJobStatusPending {
		t.Errorf("expected unexpired pending job to remain pending, got %s", stillPending.Status)
	}
	if stillPending.RawText != "keep me while pending" {
		t.Errorf("expected unexpired pending RawText to be preserved, got %q", stillPending.RawText)
	}
}

// TestNewSeeded_DefaultData verifies that NewSeeded initializes built-ins, default profile, and default branch.
func TestNewSeeded_DefaultData(t *testing.T) {
	ctx := context.Background()
	store, err := memory.NewSeeded(ctx, "test-user")
	if err != nil {
		t.Fatalf("NewSeeded failed: %v", err)
	}

	prof, err := store.Profile(ctx, "test-user")
	if err != nil {
		t.Fatalf("get seeded profile: %v", err)
	}
	if prof.UserID != "test-user" {
		t.Errorf("expected user test-user, got %s", prof.UserID)
	}

	presets, err := store.ListPresets(ctx, "test-user")
	if err != nil {
		t.Fatalf("list presets: %v", err)
	}
	if len(presets) != 2 {
		t.Errorf("expected 2 seeded presets, got %d", len(presets))
	}

	templates, err := store.ListTemplates(ctx, "test-user")
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if len(templates) != 5 {
		t.Errorf("expected 5 seeded templates, got %d", len(templates))
	}

	branches, err := store.ListBranches(ctx, "test-user")
	if err != nil {
		t.Fatalf("list branches: %v", err)
	}
	if len(branches) != 1 || !branches[0].IsDefault {
		t.Fatalf("expected 1 default branch, got %+v", branches)
	}
	if branches[0].ActiveVersionID == nil {
		t.Fatal("expected default branch to have rendered active version")
	}
}

// TestMemoryStore_ConcurrentAccess verifies thread safety and state validity under parallel read/write workloads.
func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	userID := "user-race"

	prof := &core.Profile{ID: "prof-race", UserID: userID, FullName: "Race Tester"}
	if err := store.UpsertProfile(ctx, prof); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}

	var wg sync.WaitGroup
	workers := 20
	iterations := 50
	errCh := make(chan error, workers*iterations*3)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				expID := fmt.Sprintf("exp-%d-%d", w, j)
				exp := &core.WorkExperience{
					ID:        expID,
					UserID:    userID,
					Company:   fmt.Sprintf("Company-%d", w),
					RoleTitle: "Dev",
					StartDate: core.NewDate(2024, 1, 1),
				}
				if err := store.CreateExperience(ctx, exp); err != nil {
					errCh <- fmt.Errorf("create experience error: %w", err)
				}
				if _, err := store.Snapshot(ctx, userID); err != nil {
					errCh <- fmt.Errorf("get snapshot error: %w", err)
				}
				if err := store.DeleteExperience(ctx, userID, expID); err != nil {
					errCh <- fmt.Errorf("delete experience error: %w", err)
				}
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("unexpected concurrent error: %v", err)
	}

	snap, err := store.Snapshot(ctx, userID)
	if err != nil {
		t.Fatalf("failed to retrieve snapshot after concurrent operations: %v", err)
	}
	if len(snap.Experiences) != 0 {
		t.Errorf("expected 0 experiences remaining after deletes, got %d", len(snap.Experiences))
	}
}

// TestMemoryStore_DeepCopyIsolation verifies mutations after writes or reads do not mutate internal store state.
func TestMemoryStore_DeepCopyIsolation(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	userID := "user-deep-copy"

	// 1. Profile metadata isolation
	prof := &core.Profile{
		ID:       "prof-dc",
		UserID:   userID,
		FullName: "Original Name",
		Metadata: map[string]any{"role": "staff"},
	}
	if err := store.UpsertProfile(ctx, prof); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}
	prof.Metadata["role"] = "tampered"
	fetchedProf, err := store.Profile(ctx, userID)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if fetchedProf.Metadata["role"] != "staff" {
		t.Errorf("store metadata was mutated by external caller mutation: got %v", fetchedProf.Metadata["role"])
	}
	fetchedProf.Metadata["role"] = "tampered-again"
	fetchedProf2, _ := store.Profile(ctx, userID)
	if fetchedProf2.Metadata["role"] != "staff" {
		t.Errorf("store metadata was mutated by caller mutating return value: got %v", fetchedProf2.Metadata["role"])
	}

	// 2. Skill keywords isolation
	skill := &core.Skill{
		ID:       "sk-dc",
		UserID:   userID,
		Name:     "Go",
		Keywords: []string{"backend", "concurrency"},
	}
	if err := store.CreateSkill(ctx, skill); err != nil {
		t.Fatalf("create skill: %v", err)
	}
	skill.Keywords[0] = "mutated"
	fetchedSkill, err := store.Skill(ctx, userID, skill.ID)
	if err != nil {
		t.Fatalf("get skill: %v", err)
	}
	if fetchedSkill.Keywords[0] != "backend" {
		t.Errorf("skill keywords mutated after write: got %s", fetchedSkill.Keywords[0])
	}
	fetchedSkill.Keywords[0] = "mutated-2"
	fetchedSkill2, _ := store.Skill(ctx, userID, skill.ID)
	if fetchedSkill2.Keywords[0] != "backend" {
		t.Errorf("skill keywords mutated after read: got %s", fetchedSkill2.Keywords[0])
	}

	// 3. Branch pinned entity IDs isolation
	branch := &core.Branch{
		ID:              "br-dc",
		UserID:          userID,
		Name:            "Deep Copy Branch",
		PinnedEntityIDs: []string{"ent-1", "ent-2"},
	}
	if err := store.CreateBranch(ctx, branch); err != nil {
		t.Fatalf("create branch: %v", err)
	}
	branch.PinnedEntityIDs[0] = "tampered"
	fetchedBranch, err := store.Branch(ctx, userID, branch.ID)
	if err != nil {
		t.Fatalf("get branch: %v", err)
	}
	if fetchedBranch.PinnedEntityIDs[0] != "ent-1" {
		t.Errorf("branch pinned IDs mutated: got %s", fetchedBranch.PinnedEntityIDs[0])
	}

	// 4. PDF parse job result isolation
	job := &core.PDFParseJob{
		ID:     "job-dc",
		UserID: userID,
		Status: core.PDFParseJobStatusPending,
		Result: &core.JSONResume{Basics: core.JSONResumeBasics{Summary: "original text"}},
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	job.Result.Basics.Summary = "tampered text"
	fetchedJob, err := store.Job(ctx, userID, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if fetchedJob.Result == nil || fetchedJob.Result.Basics.Summary != "original text" {
		t.Errorf("pdf job result mutated: got %v", fetchedJob.Result)
	}
}

// TestMemoryStore_ErrorSentinels verifies domain error sentinels returned across operations.
func TestMemoryStore_ErrorSentinels(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	userID := "user-sentinels"

	t.Run("ErrNotFound", func(t *testing.T) {
		tests := []struct {
			name string
			call func() error
		}{
			{"Profile", func() error { _, err := store.Profile(ctx, "nonexistent"); return err }},
			{"Skill", func() error { _, err := store.Skill(ctx, userID, "nonexistent"); return err }},
			{"UpdateSkill", func() error { return store.UpdateSkill(ctx, &core.Skill{ID: "none", UserID: userID}) }},
			{"DeleteSkill", func() error { return store.DeleteSkill(ctx, userID, "none") }},
			{"Experience", func() error { _, err := store.Experience(ctx, userID, "none"); return err }},
			{"UpdateExperience", func() error { return store.UpdateExperience(ctx, &core.WorkExperience{ID: "none", UserID: userID}) }},
			{"DeleteExperience", func() error { return store.DeleteExperience(ctx, userID, "none") }},
			{"Project", func() error { _, err := store.Project(ctx, userID, "none"); return err }},
			{"UpdateProject", func() error { return store.UpdateProject(ctx, &core.Project{ID: "none", UserID: userID}) }},
			{"DeleteProject", func() error { return store.DeleteProject(ctx, userID, "none") }},
			{"Education", func() error { _, err := store.Education(ctx, userID, "none"); return err }},
			{"UpdateEducation", func() error { return store.UpdateEducation(ctx, &core.Education{ID: "none", UserID: userID}) }},
			{"DeleteEducation", func() error { return store.DeleteEducation(ctx, userID, "none") }},
			{"Certification", func() error { _, err := store.Certification(ctx, userID, "none"); return err }},
			{"UpdateCertification", func() error { return store.UpdateCertification(ctx, &core.Certification{ID: "none", UserID: userID}) }},
			{"DeleteCertification", func() error { return store.DeleteCertification(ctx, userID, "none") }},
			{"Fact", func() error { _, err := store.Fact(ctx, userID, "none"); return err }},
			{"UpdateFact", func() error { return store.UpdateFact(ctx, &core.Fact{ID: "none", UserID: userID}) }},
			{"DeleteFact", func() error { return store.DeleteFact(ctx, userID, "none") }},
			{"Preset", func() error { _, err := store.Preset(ctx, userID, "none"); return err }},
			{"UpdatePreset", func() error { return store.UpdatePreset(ctx, &core.Preset{ID: "none", UserID: userID}) }},
			{"DeletePreset", func() error { return store.DeletePreset(ctx, userID, "none") }},
			{"Template", func() error { _, err := store.Template(ctx, userID, "none"); return err }},
			{"DeleteTemplate", func() error { return store.DeleteTemplate(ctx, userID, "none") }},
			{"Branch", func() error { _, err := store.Branch(ctx, userID, "none"); return err }},
			{"UpdateBranch", func() error { return store.UpdateBranch(ctx, &core.Branch{ID: "none", UserID: userID}) }},
			{"DeleteBranch", func() error { return store.DeleteBranch(ctx, userID, "none") }},
			{"Version", func() error { _, err := store.Version(ctx, "none", "none"); return err }},
			{"VersionByNumber", func() error { _, err := store.VersionByNumber(ctx, "none", 1); return err }},
			{"UpdateVersionStatus", func() error { return store.UpdateVersionStatus(ctx, "none", core.VersionStatusCompleted, "") }},
			{"CompleteVersion", func() error { return store.CompleteVersion(ctx, &core.BranchVersion{ID: "none", BranchID: "none"}) }},
			{"Job", func() error { _, err := store.Job(ctx, userID, "none"); return err }},
			{"UpdateJobStatus", func() error { return store.UpdateJobStatus(ctx, "none", core.PDFParseJobStatusCompleted, nil, "") }},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				err := tc.call()
				if !errors.Is(err, core.ErrNotFound) {
					t.Errorf("%s: expected ErrNotFound, got: %v", tc.name, err)
				}
			})
		}
	})

	t.Run("ErrConflict", func(t *testing.T) {
		sk := &core.Skill{ID: "sk-dup", UserID: userID, Name: "Duplicate"}
		if err := store.CreateSkill(ctx, sk); err != nil {
			t.Fatalf("initial create: %v", err)
		}
		if err := store.CreateSkill(ctx, sk); !errors.Is(err, core.ErrConflict) {
			t.Errorf("expected ErrConflict on duplicate skill, got: %v", err)
		}
	})

	t.Run("ErrDefaultBranchImmutable", func(t *testing.T) {
		defBranch := &core.Branch{ID: "br-def", UserID: userID, IsDefault: true}
		if err := store.CreateBranch(ctx, defBranch); err != nil {
			t.Fatalf("create branch: %v", err)
		}
		err := store.DeleteBranch(ctx, userID, defBranch.ID)
		if !errors.Is(err, core.ErrDefaultBranchImmutable) {
			t.Errorf("expected ErrDefaultBranchImmutable on deleting default branch, got: %v", err)
		}
	})

	t.Run("ErrValidation", func(t *testing.T) {
		if err := store.CreateJob(ctx, nil); !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation on nil job, got: %v", err)
		}
		if err := store.CreatePreset(ctx, nil); !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation on nil preset, got: %v", err)
		}
	})
}

func TestMemoryStore_QuotaLifecycle(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	userID := "quota-user-1"

	// 1. Initial Quota creates default quota
	q, err := store.Quota(ctx, userID)
	if err != nil {
		t.Fatalf("failed to get quota: %v", err)
	}
	if q.DailyRequestsUsed != 0 || q.MonthlyTokensUsed != 0 {
		t.Fatalf("expected 0 usage, got daily=%d monthly=%d", q.DailyRequestsUsed, q.MonthlyTokensUsed)
	}

	// 2. ReserveQuota success
	rec, err := store.ReserveQuota(ctx, userID, core.OperationResumeTailoring, 1500, 2, 5000)
	if err != nil {
		t.Fatalf("failed to reserve quota: %v", err)
	}
	if rec.Status != core.ReservationStatusPending || rec.EstimatedTokens != 1500 {
		t.Fatalf("unexpected record: %+v", rec)
	}

	// 3. CommitUsage
	usage := core.LLMTelemetry{
		Usage: core.TokenUsage{
			PromptTokens:     500,
			CompletionTokens: 1200,
			TotalTokens:      1700,
		},
	}
	if err := store.CommitUsage(ctx, rec.ID, usage); err != nil {
		t.Fatalf("failed to commit usage: %v", err)
	}

	// Verify committed record
	records, err := store.ListUsageRecords(ctx, userID, 10)
	if err != nil {
		t.Fatalf("failed to list usage records: %v", err)
	}
	if len(records) != 1 || records[0].Status != core.ReservationStatusCommitted || records[0].TotalTokens != 1700 {
		t.Fatalf("unexpected records: %+v", records)
	}

	// 4. Reserve second quota reaching daily limit
	rec2, err := store.ReserveQuota(ctx, userID, core.OperationGapAnalysis, 1000, 2, 5000)
	if err != nil {
		t.Fatalf("failed to reserve second quota: %v", err)
	}

	// 5. Third reservation fails with ErrQuotaExceeded
	_, err = store.ReserveQuota(ctx, userID, core.OperationPDFParse, 500, 2, 5000)
	if !errors.Is(err, core.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded on exceeding daily request limit, got %v", err)
	}

	// 6. Release second quota
	if err := store.ReleaseQuota(ctx, rec2.ID, "cancelled", core.LLMTelemetry{}); err != nil {
		t.Fatalf("failed to release quota: %v", err)
	}

	// 7. Verify reservation can now succeed after release
	rec3, err := store.ReserveQuota(ctx, userID, core.OperationPDFParse, 500, 2, 5000)
	if err != nil {
		t.Fatalf("expected reservation to succeed after release, got %v", err)
	}
	if rec3.ID == "" {
		t.Fatal("expected non-empty reservation ID")
	}

	// 8. Releasing non-existent reservation returns ErrNotFound
	if err := store.ReleaseQuota(ctx, "non-existent-id", "err", core.LLMTelemetry{}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown reservation, got %v", err)
	}
}

func TestMemoryStore_GlobalSkillsAndImmutability(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// 1. Seed global skill
	globalSkill := &core.Skill{
		ID:       "global-golang",
		UserID:   "system",
		Name:     "Go",
		Slug:     "go",
		IsGlobal: true,
	}
	if err := store.CreateSkill(ctx, globalSkill); err != nil {
		t.Fatalf("failed to create global skill: %v", err)
	}

	// 2. User skill
	userSkill := &core.Skill{
		ID:       "user-skill-1",
		UserID:   "user-a",
		Name:     "Docker",
		Slug:     "docker",
		IsGlobal: false,
	}
	if err := store.CreateSkill(ctx, userSkill); err != nil {
		t.Fatalf("failed to create user skill: %v", err)
	}

	// 3. ListGlobalSkills returns global skills
	globals, err := store.ListGlobalSkills(ctx)
	if err != nil {
		t.Fatalf("failed to list global skills: %v", err)
	}
	if len(globals) != 1 || globals[0].ID != "global-golang" {
		t.Fatalf("expected 1 global skill, got %d (%+v)", len(globals), globals)
	}

	// 4. Deleting global skill returns ErrGlobalSkillImmutable
	if err := store.DeleteSkill(ctx, "user-a", "global-golang"); !errors.Is(err, core.ErrGlobalSkillImmutable) {
		t.Fatalf("expected ErrGlobalSkillImmutable when deleting global skill, got %v", err)
	}

	// 5. Deleting another user's skill returns ErrForbidden
	if err := store.DeleteSkill(ctx, "user-b", "user-skill-1"); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("expected ErrForbidden when deleting another tenant's skill, got %v", err)
	}

	// 6. Deleting non-existent skill returns ErrNotFound
	if err := store.DeleteSkill(ctx, "user-a", "unknown-skill"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown skill, got %v", err)
	}

	// 7. Legitimate owner can delete user skill
	if err := store.DeleteSkill(ctx, "user-a", "user-skill-1"); err != nil {
		t.Fatalf("expected success deleting own skill, got %v", err)
	}
}

func TestMemoryStore_CleanupCompletedJobs(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	completedAt := now.Add(-48 * time.Hour)
	oldCompleted := &core.PDFParseJob{
		ID:          "job-old-completed",
		UserID:      "user-1",
		Status:      core.PDFParseJobStatusCompleted,
		CreatedAt:   now.Add(-48 * time.Hour),
		UpdatedAt:   now.Add(-48 * time.Hour),
		CompletedAt: &completedAt,
	}
	oldFailed := &core.PDFParseJob{
		ID:          "job-old-failed",
		UserID:      "user-1",
		Status:      core.PDFParseJobStatusFailed,
		CreatedAt:   now.Add(-48 * time.Hour),
		UpdatedAt:   now.Add(-48 * time.Hour),
		CompletedAt: &completedAt,
	}
	oldCancelled := &core.PDFParseJob{
		ID:          "job-old-cancelled",
		UserID:      "user-1",
		Status:      core.PDFParseJobStatusCancelled,
		CreatedAt:   now.Add(-48 * time.Hour),
		UpdatedAt:   now.Add(-48 * time.Hour),
		CompletedAt: &completedAt,
	}
	recentCancelled := &core.PDFParseJob{
		ID:        "job-recent-cancelled",
		UserID:    "user-1",
		Status:    core.PDFParseJobStatusCancelled,
		CreatedAt: now.Add(-1 * time.Hour),
		UpdatedAt: now.Add(-1 * time.Hour),
	}
	recentCompleted := &core.PDFParseJob{
		ID:        "job-recent-completed",
		UserID:    "user-1",
		Status:    core.PDFParseJobStatusCompleted,
		CreatedAt: now.Add(-1 * time.Hour),
		UpdatedAt: now.Add(-1 * time.Hour),
	}
	oldPending := &core.PDFParseJob{
		ID:        "job-old-pending",
		UserID:    "user-1",
		Status:    core.PDFParseJobStatusPending,
		CreatedAt: now.Add(-48 * time.Hour),
		UpdatedAt: now.Add(-48 * time.Hour),
	}

	for _, j := range []*core.PDFParseJob{oldCompleted, oldFailed, oldCancelled, recentCancelled, recentCompleted, oldPending} {
		if err := store.CreateJob(ctx, j); err != nil {
			t.Fatalf("failed to create job %s: %v", j.ID, err)
		}
	}

	// Cleanup jobs older than 24 hours
	if err := store.CleanupCompletedJobs(ctx, "user-1", 24*time.Hour); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	// Old pending and recent terminal jobs must remain
	if _, err := store.Job(ctx, "user-1", "job-old-pending"); err != nil {
		t.Errorf("expected old pending job to be preserved, got error: %v", err)
	}
	if _, err := store.Job(ctx, "user-1", "job-recent-completed"); err != nil {
		t.Errorf("expected recent completed job to be preserved, got error: %v", err)
	}
	if _, err := store.Job(ctx, "user-1", "job-recent-cancelled"); err != nil {
		t.Errorf("expected recent cancelled job to be preserved, got error: %v", err)
	}
	// Old completed, failed, and cancelled must be gone
	if _, err := store.Job(ctx, "user-1", "job-old-completed"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("expected old completed job to be purged, got %v", err)
	}
	if _, err := store.Job(ctx, "user-1", "job-old-failed"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("expected old failed job to be purged, got %v", err)
	}
	if _, err := store.Job(ctx, "user-1", "job-old-cancelled"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("expected old cancelled job to be purged, got %v", err)
	}
}

func TestMemoryStore_TemplateImmutability(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Seed built-in template
	builtIn := &core.TemplateSet{
		ID:               "builtin-standard",
		UserID:           "system",
		Name:             "Builtin Standard",
		IsBuiltIn:        true,
		HTMLTemplate:     "<div>{{.Profile.FullName}}</div>",
		MarkdownTemplate: "# {{.Profile.FullName}}",
		TextTemplate:     "{{.Profile.FullName}}",
	}
	if err := store.CreateTemplate(ctx, builtIn); err != nil {
		t.Fatalf("failed to create built-in template: %v", err)
	}

	// User custom template
	custom := &core.TemplateSet{
		ID:               "custom-tmpl-1",
		UserID:           "user-a",
		Name:             "Custom Template",
		IsBuiltIn:        false,
		HTMLTemplate:     "<div>{{.Profile.FullName}}</div>",
		MarkdownTemplate: "# {{.Profile.FullName}}",
		TextTemplate:     "{{.Profile.FullName}}",
	}
	if err := store.CreateTemplate(ctx, custom); err != nil {
		t.Fatalf("failed to create custom template: %v", err)
	}

	t.Run("Update built-in template returns ErrStrategyImmutable", func(t *testing.T) {
		mutation := *builtIn
		mutation.Name = "Poisoned Name"
		mutation.UserID = "user-a"
		if err := store.UpdateTemplate(ctx, &mutation); !errors.Is(err, core.ErrStrategyImmutable) {
			t.Fatalf("expected ErrStrategyImmutable when updating built-in template, got %v", err)
		}
	})

	t.Run("Delete built-in template returns ErrStrategyImmutable", func(t *testing.T) {
		if err := store.DeleteTemplate(ctx, "user-a", "builtin-standard"); !errors.Is(err, core.ErrStrategyImmutable) {
			t.Fatalf("expected ErrStrategyImmutable when deleting built-in template, got %v", err)
		}
	})

	t.Run("Update another user's template returns ErrForbidden", func(t *testing.T) {
		mutation := *custom
		mutation.UserID = "user-b"
		if err := store.UpdateTemplate(ctx, &mutation); !errors.Is(err, core.ErrForbidden) {
			t.Fatalf("expected ErrForbidden when updating another user's template, got %v", err)
		}
	})

	t.Run("Delete another user's template returns ErrForbidden", func(t *testing.T) {
		if err := store.DeleteTemplate(ctx, "user-b", "custom-tmpl-1"); !errors.Is(err, core.ErrForbidden) {
			t.Fatalf("expected ErrForbidden when deleting another user's template, got %v", err)
		}
	})
}

func TestMemoryStore_UserStore(t *testing.T) {
	ctx := context.Background()
	store := setupTestStore(t)

	t.Run("Validation failure on nil user or empty ID", func(t *testing.T) {
		if err := store.UpsertUser(ctx, nil); !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation for nil user, got %v", err)
		}
		if err := store.UpsertUser(ctx, &core.User{ID: ""}); !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation for empty ID, got %v", err)
		}
	})

	t.Run("User not found returns ErrNotFound", func(t *testing.T) {
		_, err := store.User(ctx, "non-existent")
		if !errors.Is(err, core.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("Create user and retrieve", func(t *testing.T) {
		u := &core.User{
			ID:       "mem-user-1",
			Email:    "mem1@example.com",
			FullName: "Memory User 1",
			Role:     core.UserRoleUser,
			Tier:     "free",
			IsActive: true,
		}
		if err := store.UpsertUser(ctx, u); err != nil {
			t.Fatalf("failed to upsert user: %v", err)
		}

		retrieved, err := store.User(ctx, "mem-user-1")
		if err != nil {
			t.Fatalf("failed to get user: %v", err)
		}
		if retrieved.Email != "mem1@example.com" {
			t.Errorf("expected mem1@example.com, got %q", retrieved.Email)
		}
		if retrieved.Role != core.UserRoleUser {
			t.Errorf("expected user role, got %q", retrieved.Role)
		}
	})

	t.Run("Preserve existing admin role on conflict", func(t *testing.T) {
		adminUser := &core.User{
			ID:       "admin-user-mem",
			Email:    "admin@resumeee.io",
			FullName: "Admin Mem",
			Role:     core.UserRoleAdmin,
			Tier:     "enterprise",
			IsActive: true,
		}
		if err := store.UpsertUser(ctx, adminUser); err != nil {
			t.Fatalf("failed to create admin user: %v", err)
		}

		demoteAttempt := &core.User{
			ID:       "admin-user-mem",
			Email:    "admin.new@resumeee.io",
			FullName: "Admin Mem Updated",
			Role:     core.UserRoleUser,
			Tier:     "free",
			IsActive: true,
		}
		if err := store.UpsertUser(ctx, demoteAttempt); err != nil {
			t.Fatalf("failed to upsert user: %v", err)
		}

		retrieved, err := store.User(ctx, "admin-user-mem")
		if err != nil {
			t.Fatalf("failed to retrieve user: %v", err)
		}
		if retrieved.Role != core.UserRoleAdmin {
			t.Errorf("expected role to remain %q, got %q", core.UserRoleAdmin, retrieved.Role)
		}
		if retrieved.Email != "admin.new@resumeee.io" {
			t.Errorf("expected email to be updated to admin.new@resumeee.io, got %q", retrieved.Email)
		}
	})
}

func TestMemoryStore_GapReportPropagation(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	userID := "user-mem-gap-1"
	score88 := 88.0

	t.Run("CompleteVersion propagates GapReport and match score to parent branch", func(t *testing.T) {
		b := &core.Branch{
			ID:            "br-mem-gap-1",
			UserID:        userID,
			Name:          "Distributed Systems Lead",
			TemplateSetID: "tmpl-1",
		}
		if err := store.CreateBranch(ctx, b); err != nil {
			t.Fatalf("failed to create branch: %v", err)
		}

		v := &core.BranchVersion{
			ID:               "ver-mem-gap-1",
			BranchID:         b.ID,
			MainRevisionUsed: 2,
			Status:           core.VersionStatusProcessing,
		}
		if err := store.CreateVersion(ctx, v); err != nil {
			t.Fatalf("failed to create version: %v", err)
		}

		v.Status = core.VersionStatusCompleted
		v.GapReport = &core.GapReport{
			OverallMatchSummary: "Solid engineering match",
			MatchScore:          &score88,
			MissingRequirements: []core.GapItem{
				{Requirement: "Raft consensus protocol", Importance: core.GapImportanceCritical},
			},
		}

		if err := store.CompleteVersion(ctx, v); err != nil {
			t.Fatalf("failed to complete version: %v", err)
		}

		fetched, err := store.Branch(ctx, userID, b.ID)
		if err != nil {
			t.Fatalf("failed to get branch: %v", err)
		}
		if fetched.GapReport == nil {
			t.Fatalf("expected parent branch GapReport to be populated after CompleteVersion, got nil")
		}
		if fetched.GapReport.MatchScore == nil || *fetched.GapReport.MatchScore != score88 {
			t.Errorf("expected match score %f, got %v", score88, fetched.GapReport.MatchScore)
		}
	})

	t.Run("CompleteVersion persists nil when version.GapReport is nil", func(t *testing.T) {
		b := &core.Branch{
			ID:            "br-mem-nil",
			UserID:        userID,
			Name:          "Untailored Memory Variant",
			TemplateSetID: "tmpl-1",
		}
		if err := store.CreateBranch(ctx, b); err != nil {
			t.Fatalf("failed to create branch: %v", err)
		}

		v := &core.BranchVersion{
			ID:               "ver-mem-nil-1",
			BranchID:         b.ID,
			MainRevisionUsed: 1,
			Status:           core.VersionStatusCompleted,
			GapReport:        nil,
		}
		if err := store.CreateVersion(ctx, v); err != nil {
			t.Fatalf("failed to create version: %v", err)
		}
		if err := store.CompleteVersion(ctx, v); err != nil {
			t.Fatalf("failed to complete version: %v", err)
		}

		fetched, err := store.Branch(ctx, userID, b.ID)
		if err != nil {
			t.Fatalf("failed to get branch: %v", err)
		}
		if fetched.GapReport != nil {
			t.Errorf("expected parent branch GapReport to be nil when version.GapReport is nil, got %+v", fetched.GapReport)
		}
		if fetched.ActiveVersionID == nil || *fetched.ActiveVersionID != v.ID {
			t.Errorf("expected active version ID %s, got %v", v.ID, fetched.ActiveVersionID)
		}
	})
}

func TestMemoryStore_ATSScorePropagation(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	userID := "user-mem-ats-1"

	t.Run("UpdateVersionATSScore persists on version and propagates to parent branch when active", func(t *testing.T) {
		b := &core.Branch{
			ID:            "br-mem-ats-1",
			UserID:        userID,
			Name:          "ATS Memory Lead",
			TemplateSetID: "tmpl-1",
		}
		if err := store.CreateBranch(ctx, b); err != nil {
			t.Fatalf("failed to create branch: %v", err)
		}

		v1 := &core.BranchVersion{
			ID:               "ver-mem-ats-1",
			BranchID:         b.ID,
			MainRevisionUsed: 1,
			Status:           core.VersionStatusCompleted,
		}
		if err := store.CreateVersion(ctx, v1); err != nil {
			t.Fatalf("failed to create v1: %v", err)
		}
		if err := store.CompleteVersion(ctx, v1); err != nil {
			t.Fatalf("failed to complete v1: %v", err)
		}

		ats1 := &core.ATSScore{
			ExtractionConfidenceScore: 94.0,
			JobMatchingScore:          86.0,
			FormatUsed:                "markdown",
		}
		if err := store.UpdateVersionATSScore(ctx, v1.ID, ats1); err != nil {
			t.Fatalf("failed to update version ATS score: %v", err)
		}

		fetchedV1, err := store.Version(ctx, b.ID, v1.ID)
		if err != nil {
			t.Fatalf("failed to get v1: %v", err)
		}
		if fetchedV1.ATSScore == nil || fetchedV1.ATSScore.ExtractionConfidenceScore != 94.0 || fetchedV1.ATSScore.JobMatchingScore != 86.0 {
			t.Fatalf("expected v1 ATSScore (94.0, 86.0), got %+v", fetchedV1.ATSScore)
		}

		fetchedBranch, err := store.Branch(ctx, userID, b.ID)
		if err != nil {
			t.Fatalf("failed to get branch: %v", err)
		}
		if fetchedBranch.ATSScore == nil || fetchedBranch.ATSScore.ExtractionConfidenceScore != 94.0 || fetchedBranch.ATSScore.JobMatchingScore != 86.0 {
			t.Fatalf("expected branch ATSScore propagated from active v1, got %+v", fetchedBranch.ATSScore)
		}

		// Attempting to overwrite an already-evaluated version's ATSScore returns ErrConflict
		if err := store.UpdateVersionATSScore(ctx, v1.ID, &core.ATSScore{ExtractionConfidenceScore: 50, JobMatchingScore: 50}); !errors.Is(err, core.ErrConflict) {
			t.Errorf("expected ErrConflict when updating already-scored version, got %v", err)
		}

		v2 := &core.BranchVersion{
			ID:               "ver-mem-ats-2",
			BranchID:         b.ID,
			MainRevisionUsed: 2,
			Status:           core.VersionStatusCompleted,
		}
		if err := store.CreateVersion(ctx, v2); err != nil {
			t.Fatalf("failed to create v2: %v", err)
		}
		if err := store.CompleteVersion(ctx, v2); err != nil {
			t.Fatalf("failed to complete v2: %v", err)
		}

		fetchedBranchAfterV2, err := store.Branch(ctx, userID, b.ID)
		if err != nil {
			t.Fatalf("failed to get branch after v2: %v", err)
		}
		if fetchedBranchAfterV2.ATSScore != nil {
			t.Errorf("expected branch ATSScore to be nil for newly completed v2, got %+v", fetchedBranchAfterV2.ATSScore)
		}
	})

	t.Run("UpdateVersionATSScore returns ErrNotFound for nonexistent version", func(t *testing.T) {
		err := store.UpdateVersionATSScore(ctx, "nonexistent-ver", &core.ATSScore{ExtractionConfidenceScore: 90, JobMatchingScore: 80})
		if !errors.Is(err, core.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}

// TestBranchStore_UpdateBranchRejectsForeignUser verifies store-level tenant ownership enforcement on UpdateBranch.
func TestBranchStore_UpdateBranchRejectsForeignUser(t *testing.T) {
	ctx := context.Background()
	store := setupTestStore(t)

	br := &core.Branch{
		ID:            "br-owner-only",
		UserID:        "owner-1",
		Name:          "Original Branch Name",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	if err := store.CreateBranch(ctx, br); err != nil {
		t.Fatalf("create branch: %v", err)
	}

	err := store.UpdateBranch(ctx, &core.Branch{
		ID:            br.ID,
		UserID:        "attacker-2",
		Name:          "Hijacked Name",
		TemplateSetID: core.TemplateStandardCleanID,
	})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("expected ErrNotFound when foreign tenant updates branch, got %v", err)
	}

	fetched, err := store.Branch(ctx, "owner-1", br.ID)
	if err != nil {
		t.Fatalf("fetch owner branch: %v", err)
	}
	if fetched.Name != "Original Branch Name" {
		t.Errorf("expected original branch name to be preserved, got %q", fetched.Name)
	}
}

// TestCareerStore_InlineFactsSingleSourceOfTruth verifies inline entity facts stay synchronized with s.facts across reads, updates, and deletes.
func TestCareerStore_InlineFactsSingleSourceOfTruth(t *testing.T) {
	ctx := context.Background()
	store := setupTestStore(t)
	userID := "user-inline-facts"

	if err := store.UpsertProfile(ctx, &core.Profile{ID: "prof-if", UserID: userID, FullName: "Inline Engineer"}); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}

	exp := &core.WorkExperience{
		ID:        "exp-inline-1",
		UserID:    userID,
		Company:   "FactCo",
		RoleTitle: "Principal Engineer",
		StartDate: core.NewDate(2023, 1, 1),
		Facts: []core.Fact{
			{ID: "fact-inline-1", Statement: "Built zero-downtime migration engine"},
			{ID: "fact-inline-2", Statement: "Cut p99 latency by 45%"},
		},
	}
	if err := store.CreateExperience(ctx, exp); err != nil {
		t.Fatalf("create experience with inline facts: %v", err)
	}

	proj := &core.Project{
		ID:     "proj-inline-1",
		UserID: userID,
		Name:   "OpenRouter",
		Facts:  []core.Fact{{ID: "fact-proj-1", Statement: "Shipped deterministic routing SDK"}},
	}
	if err := store.CreateProject(ctx, proj); err != nil {
		t.Fatalf("create project with inline facts: %v", err)
	}

	edu := &core.Education{
		ID:          "edu-inline-1",
		UserID:      userID,
		Institution: "State Tech",
		Degree:      "B.S. CS",
		Facts:       []core.Fact{{ID: "fact-edu-1", Statement: "Summa Cum Laude"}},
	}
	if err := store.CreateEducation(ctx, edu); err != nil {
		t.Fatalf("create education with inline facts: %v", err)
	}

	// Single-entity getters, list getters, and Snapshot must hydrate inline facts from s.facts.
	fetchedExp, err := store.Experience(ctx, userID, exp.ID)
	if err != nil {
		t.Fatalf("get experience: %v", err)
	}
	if len(fetchedExp.Facts) != 2 {
		t.Fatalf("expected 2 hydrated experience facts, got %d", len(fetchedExp.Facts))
	}

	fetchedProj, err := store.Project(ctx, userID, proj.ID)
	if err != nil || len(fetchedProj.Facts) != 1 {
		t.Fatalf("expected 1 hydrated project fact, got err=%v facts=%+v", err, fetchedProj)
	}

	fetchedEdu, err := store.Education(ctx, userID, edu.ID)
	if err != nil || len(fetchedEdu.Facts) != 1 {
		t.Fatalf("expected 1 hydrated education fact, got err=%v facts=%+v", err, fetchedEdu)
	}

	// Mutating and deleting facts via Fact CRUD must reflect immediately in Experience, ListExperiences, and Snapshot.
	if err := store.UpdateFact(ctx, &core.Fact{
		ID:        "fact-inline-1",
		UserID:    userID,
		OwnerID:   exp.ID,
		OwnerType: core.OwnerTypeWorkExperience,
		Statement: "Built multi-region zero-downtime migration engine",
	}); err != nil {
		t.Fatalf("update fact: %v", err)
	}
	if err := store.DeleteFact(ctx, userID, "fact-inline-2"); err != nil {
		t.Fatalf("delete fact: %v", err)
	}

	// Updating parent experience metadata with nil Facts must preserve remaining facts in s.facts.
	if err := store.UpdateExperience(ctx, &core.WorkExperience{
		ID:        exp.ID,
		UserID:    userID,
		Company:   "FactCo Global",
		RoleTitle: "Distinguished Engineer",
		StartDate: exp.StartDate,
	}); err != nil {
		t.Fatalf("update experience metadata: %v", err)
	}

	exps, err := store.ListExperiences(ctx, userID)
	if err != nil {
		t.Fatalf("list experiences: %v", err)
	}
	if len(exps) != 1 || len(exps[0].Facts) != 1 || exps[0].Facts[0].Statement != "Built multi-region zero-downtime migration engine" {
		t.Fatalf("expected updated single fact on listed experience, got %+v", exps)
	}

	snap, err := store.Snapshot(ctx, userID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snap.Experiences) != 1 || len(snap.Experiences[0].Facts) != 1 {
		t.Fatalf("expected 1 fact on snapshot experience, got %+v", snap.Experiences)
	}

	// Deleting parent experience must clean up its child facts.
	if err := store.DeleteExperience(ctx, userID, exp.ID); err != nil {
		t.Fatalf("delete experience: %v", err)
	}
	remainingExpFacts, err := store.ListFactsByOwner(ctx, userID, string(core.OwnerTypeWorkExperience), exp.ID)
	if err != nil {
		t.Fatalf("list facts by owner: %v", err)
	}
	if len(remainingExpFacts) != 0 {
		t.Errorf("expected 0 experience facts after parent deletion, got %+v", remainingExpFacts)
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(initial time.Time) *fakeClock {
	return &fakeClock{now: initial.UTC()}
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

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t.UTC()
}

// TestMemoryStore_SubmitPendingLimitsAndSupersede verifies atomic admission limits, default TTL,
// superseding pending versions, and processing conflict/expired-lease handling.
func TestMemoryStore_SubmitPendingLimitsAndSupersede(t *testing.T) {
	ctx := context.Background()
	baseNow := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("default ExpiresAt uses DefaultJobTTL and custom TTL is honored", func(t *testing.T) {
		store := setupTestStore(t)
		br := &core.Branch{ID: "br-ttl", UserID: "u-ttl", Name: "TTL Branch", TemplateSetID: core.TemplateStandardCleanID}
		if err := store.CreateBranch(ctx, br); err != nil {
			t.Fatalf("create branch: %v", err)
		}

		v := &core.BranchVersion{ID: "ver-ttl-default", UserID: "u-ttl", BranchID: br.ID}
		if _, err := store.SubmitPendingVersion(ctx, v, core.PendingJobOptions{Now: baseNow}); err != nil {
			t.Fatalf("submit pending version: %v", err)
		}
		gotV, err := store.Version(ctx, br.ID, v.ID)
		if err != nil {
			t.Fatalf("get version: %v", err)
		}
		wantExp := baseNow.Add(core.DefaultJobTTL)
		if gotV.ExpiresAt == nil || !gotV.ExpiresAt.Equal(wantExp) {
			t.Fatalf("expected version ExpiresAt %v, got %v", wantExp, gotV.ExpiresAt)
		}

		job := &core.PDFParseJob{ID: "job-ttl-custom", UserID: "u-ttl", RawText: "resume text"}
		if err := store.SubmitPendingJob(ctx, job, core.PendingJobOptions{Now: baseNow, TTL: 5 * time.Minute}); err != nil {
			t.Fatalf("submit pending job: %v", err)
		}
		gotJ, err := store.Job(ctx, "u-ttl", job.ID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		wantJobExp := baseNow.Add(5 * time.Minute)
		if gotJ.ExpiresAt == nil || !gotJ.ExpiresAt.Equal(wantJobExp) {
			t.Fatalf("expected job ExpiresAt %v, got %v", wantJobExp, gotJ.ExpiresAt)
		}
	})

	t.Run("atomic MaxPendingPerTenant across combined versions and PDF jobs and MaxGlobalPending", func(t *testing.T) {
		store := setupTestStore(t)
		for _, id := range []string{"br-1", "br-2", "br-3"} {
			if err := store.CreateBranch(ctx, &core.Branch{ID: id, UserID: "u1", Name: id, TemplateSetID: core.TemplateStandardCleanID}); err != nil {
				t.Fatalf("create branch %s: %v", id, err)
			}
		}
		if err := store.CreateBranch(ctx, &core.Branch{ID: "br-u2", UserID: "u2", Name: "u2", TemplateSetID: core.TemplateStandardCleanID}); err != nil {
			t.Fatalf("create u2 branch: %v", err)
		}

		opts := core.PendingJobOptions{MaxPendingPerTenant: 2, MaxGlobalPending: 3, Now: baseNow}
		if _, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{ID: "v-u1-1", UserID: "u1", BranchID: "br-1"}, opts); err != nil {
			t.Fatalf("submit v-u1-1: %v", err)
		}
		if err := store.SubmitPendingJob(ctx, &core.PDFParseJob{ID: "j-u1-1", UserID: "u1", RawText: "raw"}, opts); err != nil {
			t.Fatalf("submit j-u1-1: %v", err)
		}

		// 3rd pending item for u1 (either version on another branch or PDF job) exceeds MaxPendingPerTenant=2.
		_, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{ID: "v-u1-2", UserID: "u1", BranchID: "br-2"}, opts)
		if !errors.Is(err, core.ErrPendingLimitExceeded) || !errors.Is(err, core.ErrQueueFull) {
			t.Fatalf("expected ErrPendingLimitExceeded/ErrQueueFull for version cap, got %v", err)
		}
		if _, lookupErr := store.Version(ctx, "br-2", "v-u1-2"); !errors.Is(lookupErr, core.ErrNotFound) {
			t.Fatalf("expected rejected version not to be persisted, got %v", lookupErr)
		}

		err = store.SubmitPendingJob(ctx, &core.PDFParseJob{ID: "j-u1-2", UserID: "u1", RawText: "raw"}, opts)
		if !errors.Is(err, core.ErrPendingLimitExceeded) || !errors.Is(err, core.ErrQueueFull) {
			t.Fatalf("expected ErrPendingLimitExceeded/ErrQueueFull for PDF job cap, got %v", err)
		}

		// Tenant u2 can fill the 3rd global slot, then hits MaxGlobalPending=3.
		if _, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{ID: "v-u2-1", UserID: "u2", BranchID: "br-u2"}, opts); err != nil {
			t.Fatalf("submit v-u2-1: %v", err)
		}
		err = store.SubmitPendingJob(ctx, &core.PDFParseJob{ID: "j-u2-1", UserID: "u2", RawText: "raw"}, opts)
		if !errors.Is(err, core.ErrPendingLimitExceeded) || !errors.Is(err, core.ErrQueueFull) {
			t.Fatalf("expected ErrPendingLimitExceeded/ErrQueueFull on MaxGlobalPending, got %v", err)
		}
	})

	t.Run("supersedes older pending versions on same branch even at tenant cap and releases reservations", func(t *testing.T) {
		store := setupTestStore(t)
		br := &core.Branch{ID: "br-sup", UserID: "u-sup", Name: "Supersede", TemplateSetID: core.TemplateStandardCleanID}
		if err := store.CreateBranch(ctx, br); err != nil {
			t.Fatalf("create branch: %v", err)
		}

		opts := core.PendingJobOptions{MaxPendingPerTenant: 1, MaxGlobalPending: 1, Now: baseNow}
		v1 := &core.BranchVersion{ID: "v-sup-1", UserID: "u-sup", BranchID: br.ID, ReservationID: "res-sup-1"}
		released, err := store.SubmitPendingVersion(ctx, v1, opts)
		if err != nil {
			t.Fatalf("submit v1: %v", err)
		}
		if len(released) != 0 {
			t.Fatalf("expected 0 released on first submit, got %+v", released)
		}

		v2 := &core.BranchVersion{ID: "v-sup-2", UserID: "u-sup", BranchID: br.ID, ReservationID: "res-sup-2"}
		released, err = store.SubmitPendingVersion(ctx, v2, core.PendingJobOptions{MaxPendingPerTenant: 1, MaxGlobalPending: 1, Now: baseNow.Add(time.Minute)})
		if err != nil {
			t.Fatalf("submit v2 superseding v1 at cap=1: %v", err)
		}
		if len(released) != 1 || released[0].JobID != v1.ID || released[0].ReservationID != "res-sup-1" || released[0].UserID != "u-sup" {
			t.Fatalf("expected v1 reservation released, got %+v", released)
		}

		gotV1, err := store.Version(ctx, br.ID, v1.ID)
		if err != nil {
			t.Fatalf("get v1: %v", err)
		}
		if gotV1.Status != core.VersionStatusCancelled {
			t.Errorf("expected v1 status cancelled, got %s", gotV1.Status)
		}
		gotV2, err := store.Version(ctx, br.ID, v2.ID)
		if err != nil {
			t.Fatalf("get v2: %v", err)
		}
		if gotV2.Status != core.VersionStatusPending || gotV2.VersionNumber != 2 {
			t.Errorf("expected v2 pending version_number=2, got status=%s ver=%d", gotV2.Status, gotV2.VersionNumber)
		}
	})

	t.Run("rejects ErrConflict on unexpired processing version and fails expired processing lease cleanly", func(t *testing.T) {
		store := setupTestStore(t)
		br := &core.Branch{ID: "br-proc", UserID: "u-proc", Name: "Proc", TemplateSetID: core.TemplateStandardCleanID}
		if err := store.CreateBranch(ctx, br); err != nil {
			t.Fatalf("create branch: %v", err)
		}

		v1 := &core.BranchVersion{ID: "v-proc-1", UserID: "u-proc", BranchID: br.ID, ReservationID: "res-proc-1"}
		if _, err := store.SubmitPendingVersion(ctx, v1, core.PendingJobOptions{Now: baseNow}); err != nil {
			t.Fatalf("submit v1: %v", err)
		}
		claimed, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{
			WorkerID:      "worker-1",
			Now:           baseNow.Add(10 * time.Second),
			LeaseDuration: 2 * time.Minute,
		})
		if err != nil || claimed == nil {
			t.Fatalf("claim v1: claimed=%v err=%v", claimed, err)
		}

		// While lease is unexpired (baseNow + 1m < baseNow + 2m10s), submitting new pending version conflicts.
		v2 := &core.BranchVersion{ID: "v-proc-2", UserID: "u-proc", BranchID: br.ID, ReservationID: "res-proc-2"}
		_, err = store.SubmitPendingVersion(ctx, v2, core.PendingJobOptions{Now: baseNow.Add(time.Minute)})
		if !errors.Is(err, core.ErrConflict) {
			t.Fatalf("expected ErrConflict while unexpired processing version runs, got %v", err)
		}

		// Once lease expires (baseNow + 5m > baseNow + 2m10s), expired lease is failed and v2 is admitted.
		released, err := store.SubmitPendingVersion(ctx, v2, core.PendingJobOptions{Now: baseNow.Add(5 * time.Minute)})
		if err != nil {
			t.Fatalf("expected submit after lease expiry to succeed, got %v", err)
		}
		if len(released) != 1 || released[0].JobID != v1.ID || released[0].ReservationID != "res-proc-1" {
			t.Fatalf("expected expired processing reservation released, got %+v", released)
		}
		gotV1, err := store.Version(ctx, br.ID, v1.ID)
		if err != nil {
			t.Fatalf("get v1: %v", err)
		}
		if gotV1.Status != core.VersionStatusFailed {
			t.Errorf("expected expired processing v1 to become failed, got %s", gotV1.Status)
		}
	})

	t.Run("concurrent submissions respect MaxPendingPerTenant and MaxGlobalPending under race", func(t *testing.T) {
		store := setupTestStore(t)
		const workers = 16
		for i := 0; i < workers; i++ {
			bID := fmt.Sprintf("br-race-%d", i)
			if err := store.CreateBranch(ctx, &core.Branch{ID: bID, UserID: "u-race", Name: bID, TemplateSetID: core.TemplateStandardCleanID}); err != nil {
				t.Fatalf("create branch: %v", err)
			}
		}

		opts := core.PendingJobOptions{MaxPendingPerTenant: 5, MaxGlobalPending: 5, Now: baseNow}
		var wg sync.WaitGroup
		var mu sync.Mutex
		var admitted int
		var rejected int

		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				var err error
				if idx%2 == 0 {
					_, err = store.SubmitPendingVersion(ctx, &core.BranchVersion{
						ID:       fmt.Sprintf("v-race-%d", idx),
						UserID:   "u-race",
						BranchID: fmt.Sprintf("br-race-%d", idx),
					}, opts)
				} else {
					err = store.SubmitPendingJob(ctx, &core.PDFParseJob{
						ID:      fmt.Sprintf("j-race-%d", idx),
						UserID:  "u-race",
						RawText: "raw",
					}, opts)
				}
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					admitted++
				case errors.Is(err, core.ErrPendingLimitExceeded):
					rejected++
				default:
					t.Errorf("unexpected error from concurrent submit: %v", err)
				}
			}(i)
		}
		wg.Wait()

		if admitted != 5 || rejected != workers-5 {
			t.Fatalf("expected admitted=5 rejected=%d, got admitted=%d rejected=%d", workers-5, admitted, rejected)
		}
	})
}

// TestMemoryStore_CancelSupersedeAndAbort verifies cancellation, superseding, aborting,
// ownership enforcement, conflict handling, and RawText pruning.
func TestMemoryStore_CancelSupersedeAndAbort(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("CancelVersion table-driven states and ownership", func(t *testing.T) {
		tests := []struct {
			name        string
			initial     core.VersionStatus
			reqUserID   string
			reqBranchID string
			reqVerID    string
			wantErr     error
		}{
			{name: "cancels pending owned version", initial: core.VersionStatusPending, reqUserID: "u1", reqBranchID: "br-1", reqVerID: "v1"},
			{name: "wrong user returns ErrNotFound", initial: core.VersionStatusPending, reqUserID: "u2", reqBranchID: "br-1", reqVerID: "v1", wantErr: core.ErrNotFound},
			{name: "wrong branch returns ErrNotFound", initial: core.VersionStatusPending, reqUserID: "u1", reqBranchID: "br-other", reqVerID: "v1", wantErr: core.ErrNotFound},
			{name: "missing version returns ErrNotFound", initial: core.VersionStatusPending, reqUserID: "u1", reqBranchID: "br-1", reqVerID: "missing", wantErr: core.ErrNotFound},
			{name: "processing returns ErrConflict", initial: core.VersionStatusProcessing, reqUserID: "u1", reqBranchID: "br-1", reqVerID: "v1", wantErr: core.ErrConflict},
			{name: "completed returns ErrConflict", initial: core.VersionStatusCompleted, reqUserID: "u1", reqBranchID: "br-1", reqVerID: "v1", wantErr: core.ErrConflict},
			{name: "completed_with_warnings returns ErrConflict", initial: core.VersionStatusCompletedWithWarnings, reqUserID: "u1", reqBranchID: "br-1", reqVerID: "v1", wantErr: core.ErrConflict},
			{name: "failed returns ErrConflict", initial: core.VersionStatusFailed, reqUserID: "u1", reqBranchID: "br-1", reqVerID: "v1", wantErr: core.ErrConflict},
			{name: "cancelled returns ErrConflict", initial: core.VersionStatusCancelled, reqUserID: "u1", reqBranchID: "br-1", reqVerID: "v1", wantErr: core.ErrConflict},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				store := setupTestStore(t)
				if err := store.CreateBranch(ctx, &core.Branch{ID: "br-1", UserID: "u1", Name: "B1", TemplateSetID: core.TemplateStandardCleanID}); err != nil {
					t.Fatalf("create branch: %v", err)
				}
				if err := store.CreateVersion(ctx, &core.BranchVersion{ID: "v1", UserID: "u1", BranchID: "br-1", Status: tc.initial}); err != nil {
					t.Fatalf("create version: %v", err)
				}

				got, err := store.CancelVersion(ctx, tc.reqUserID, tc.reqBranchID, tc.reqVerID, "user aborted")
				if tc.wantErr != nil {
					if !errors.Is(err, tc.wantErr) {
						t.Fatalf("expected error %v, got %v", tc.wantErr, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got.Status != core.VersionStatusCancelled || got.ErrorMessage != "user aborted" || got.CompletedAt == nil {
					t.Fatalf("unexpected cancelled version: %+v", got)
				}
			})
		}
	})

	t.Run("CancelJob table-driven states, ownership, and RawText pruning", func(t *testing.T) {
		tests := []struct {
			name      string
			initial   core.PDFParseJobStatus
			reqUserID string
			reqJobID  string
			wantErr   error
		}{
			{name: "cancels pending owned job and prunes RawText", initial: core.PDFParseJobStatusPending, reqUserID: "u1", reqJobID: "j1"},
			{name: "wrong user returns ErrNotFound", initial: core.PDFParseJobStatusPending, reqUserID: "u2", reqJobID: "j1", wantErr: core.ErrNotFound},
			{name: "missing job returns ErrNotFound", initial: core.PDFParseJobStatusPending, reqUserID: "u1", reqJobID: "missing", wantErr: core.ErrNotFound},
			{name: "processing returns ErrConflict", initial: core.PDFParseJobStatusProcessing, reqUserID: "u1", reqJobID: "j1", wantErr: core.ErrConflict},
			{name: "completed returns ErrConflict", initial: core.PDFParseJobStatusCompleted, reqUserID: "u1", reqJobID: "j1", wantErr: core.ErrConflict},
			{name: "failed returns ErrConflict", initial: core.PDFParseJobStatusFailed, reqUserID: "u1", reqJobID: "j1", wantErr: core.ErrConflict},
			{name: "cancelled returns ErrConflict", initial: core.PDFParseJobStatusCancelled, reqUserID: "u1", reqJobID: "j1", wantErr: core.ErrConflict},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				store := setupTestStore(t)
				if err := store.CreateJob(ctx, &core.PDFParseJob{
					ID:      "j1",
					UserID:  "u1",
					Status:  tc.initial,
					RawText: "sensitive pdf text",
				}); err != nil {
					t.Fatalf("create job: %v", err)
				}

				got, err := store.CancelJob(ctx, tc.reqUserID, tc.reqJobID, "cancelled by user")
				if tc.wantErr != nil {
					if !errors.Is(err, tc.wantErr) {
						t.Fatalf("expected error %v, got %v", tc.wantErr, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got.Status != core.PDFParseJobStatusCancelled || got.ErrorMessage != "cancelled by user" || got.CompletedAt == nil {
					t.Fatalf("unexpected cancelled job: %+v", got)
				}
				if got.RawText != "" {
					t.Errorf("expected RawText to be pruned on CancelJob, got %q", got.RawText)
				}
			})
		}
	})

	t.Run("SupersedePendingVersions and AbortBranchVersions", func(t *testing.T) {
		store := setupTestStore(t)
		for _, id := range []string{"br-a", "br-b"} {
			if err := store.CreateBranch(ctx, &core.Branch{ID: id, UserID: "u1", Name: id, TemplateSetID: core.TemplateStandardCleanID}); err != nil {
				t.Fatalf("create branch %s: %v", id, err)
			}
		}

		versions := []*core.BranchVersion{
			{ID: "va-pending-1", UserID: "u1", BranchID: "br-a", Status: core.VersionStatusPending, ReservationID: "res-a1", CreatedAt: now},
			{ID: "va-pending-nores", UserID: "u1", BranchID: "br-a", Status: core.VersionStatusPending, CreatedAt: now},
			{ID: "va-proc", UserID: "u1", BranchID: "br-a", Status: core.VersionStatusProcessing, ReservationID: "res-a-proc", CreatedAt: now},
			{ID: "va-done", UserID: "u1", BranchID: "br-a", Status: core.VersionStatusCompleted, ReservationID: "res-a-done", CreatedAt: now},
			{ID: "vb-pending", UserID: "u1", BranchID: "br-b", Status: core.VersionStatusPending, ReservationID: "res-b1", CreatedAt: now},
		}
		for _, v := range versions {
			if err := store.CreateVersion(ctx, v); err != nil {
				t.Fatalf("create version %s: %v", v.ID, err)
			}
		}

		supRes, err := store.SupersedePendingVersions(ctx, "br-a", "superseded")
		if err != nil {
			t.Fatalf("SupersedePendingVersions: %v", err)
		}
		if len(supRes) != 1 || supRes[0].ReservationID != "res-a1" || supRes[0].JobID != "va-pending-1" {
			t.Fatalf("expected only pending version with reservation released, got %+v", supRes)
		}

		vProc, _ := store.Version(ctx, "br-a", "va-proc")
		if vProc.Status != core.VersionStatusProcessing {
			t.Fatalf("expected va-proc to remain processing after SupersedePendingVersions, got %s", vProc.Status)
		}
		vNoRes, _ := store.Version(ctx, "br-a", "va-pending-nores")
		if vNoRes.Status != core.VersionStatusCancelled {
			t.Fatalf("expected va-pending-nores to be cancelled, got %s", vNoRes.Status)
		}
		vB, _ := store.Version(ctx, "br-b", "vb-pending")
		if vB.Status != core.VersionStatusPending {
			t.Fatalf("expected br-b pending version untouched, got %s", vB.Status)
		}

		// Add another pending version on br-a and abort all non-terminal versions on br-a.
		if _, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{ID: "va-pending-2", UserID: "u1", BranchID: "br-a", ReservationID: "res-a2"}, core.PendingJobOptions{Now: now}); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("expected conflict while va-proc runs, got %v", err)
		}
		if err := store.CreateVersion(ctx, &core.BranchVersion{ID: "va-pending-2", UserID: "u1", BranchID: "br-a", Status: core.VersionStatusPending, ReservationID: "res-a2"}); err != nil {
			t.Fatalf("create va-pending-2: %v", err)
		}

		aborted, err := store.AbortBranchVersions(ctx, "br-a", "branch deleted")
		if err != nil {
			t.Fatalf("AbortBranchVersions: %v", err)
		}
		if len(aborted) != 2 {
			t.Fatalf("expected 2 aborted reservations (va-proc + va-pending-2), got %+v", aborted)
		}
		vDone, _ := store.Version(ctx, "br-a", "va-done")
		if vDone.Status != core.VersionStatusCompleted {
			t.Errorf("expected completed version untouched by AbortBranchVersions, got %s", vDone.Status)
		}
		vB, _ = store.Version(ctx, "br-b", "vb-pending")
		if vB.Status != core.VersionStatusPending {
			t.Errorf("expected br-b version untouched by AbortBranchVersions, got %s", vB.Status)
		}
	})
}

// TestMemoryStore_ClaimNextSchedulingAndFairness verifies atomic claiming, LRU fairness,
// expiration/retry filtering, tenant concurrency caps, branch exclusivity, and race safety.
func TestMemoryStore_ClaimNextSchedulingAndFairness(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("ClaimNextVersion fair LRU, CreatedAt tie-break, skips expired/future-retry, branch exclusivity, and tenant cap", func(t *testing.T) {
		store := setupTestStore(t)
		for _, pair := range []struct{ id, uid string }{
			{"br-u1-1", "u1"}, {"br-u1-2", "u1"}, {"br-u2-1", "u2"}, {"br-u3-1", "u3"},
		} {
			if err := store.CreateBranch(ctx, &core.Branch{ID: pair.id, UserID: pair.uid, Name: pair.id, TemplateSetID: core.TemplateStandardCleanID}); err != nil {
				t.Fatalf("create branch: %v", err)
			}
		}

		// Expired version for u3 (should be skipped even though u3 has seq=0).
		if _, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{
			ID: "v-u3-expired", UserID: "u3", BranchID: "br-u3-1", CreatedAt: base.Add(-10 * time.Minute),
			ExpiresAt: ptrTime(base.Add(-time.Second)),
		}, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit v-u3-expired: %v", err)
		}
		// Future retry for u3 (should be skipped at base).
		if _, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{
			ID: "v-u3-retry", UserID: "u3", BranchID: "br-u3-1", CreatedAt: base.Add(-9 * time.Minute),
			NextRetryAt: ptrTime(base.Add(time.Minute)),
		}, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit v-u3-retry: %v", err)
		}

		// u1 (seq 5) and u2 (seq 2): u2 should win over u1 despite u1 having older CreatedAt.
		if _, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{
			ID: "v-u1-old", UserID: "u1", BranchID: "br-u1-1", CreatedAt: base.Add(-5 * time.Minute),
		}, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit v-u1-old: %v", err)
		}
		if _, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{
			ID: "v-u2-newer", UserID: "u2", BranchID: "br-u2-1", CreatedAt: base.Add(-1 * time.Minute),
		}, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit v-u2-newer: %v", err)
		}

		// Also add an active PDF job for u2 in sibling store to test MaxConcurrentPerTenant=1 fallback.
		if err := store.CreateJob(ctx, &core.PDFParseJob{
			ID: "pdf-u2-active", UserID: "u2", Status: core.PDFParseJobStatusProcessing,
			LeaseExpiresAt: ptrTime(base.Add(2 * time.Minute)),
		}); err != nil {
			t.Fatalf("create sibling pdf job: %v", err)
		}

		// With OtherActiveByTenant=nil and MaxConcurrentPerTenant=1, u2 is throttled by sibling pdf-u2-active, so u1 wins!
		c1, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{
			WorkerID:               "w1",
			Now:                    base,
			LeaseDuration:          2 * time.Minute,
			MaxConcurrentPerTenant: 1,
			LastServedSeq:          map[string]uint64{"u1": 5, "u2": 2},
		})
		if err != nil || c1 == nil || c1.ID != "v-u1-old" {
			t.Fatalf("expected v-u1-old when u2 throttled by sibling store, got c=%+v err=%v", c1, err)
		}
		if c1.AttemptCount != 1 || c1.ClaimedBy != "w1" || c1.ClaimedAt == nil || c1.LeaseExpiresAt == nil {
			t.Fatalf("expected lease metadata initialized, got %+v", c1)
		}

		// With explicit non-nil empty OtherActiveByTenant, sibling pdf-u2-active is ignored (override), so u2 wins!
		c2, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{
			WorkerID:               "w2",
			Now:                    base,
			LeaseDuration:          2 * time.Minute,
			MaxConcurrentPerTenant: 1,
			OtherActiveByTenant:    map[string]int{},
			LastServedSeq:          map[string]uint64{"u1": 5, "u2": 2},
		})
		if err != nil || c2 == nil || c2.ID != "v-u2-newer" {
			t.Fatalf("expected v-u2-newer when OtherActiveByTenant overrides sibling count, got c=%+v err=%v", c2, err)
		}

		// Branch exclusivity: if br-u1-1 already has active processing v-u1-old, another pending version on br-u1-1 cannot be claimed
		// even with unlimited MaxConcurrentPerTenant, while br-u1-2 CAN be claimed.
		if err := store.CreateVersion(ctx, &core.BranchVersion{
			ID: "v-u1-same-branch", UserID: "u1", BranchID: "br-u1-1", Status: core.VersionStatusPending, CreatedAt: base.Add(-8 * time.Minute),
		}); err != nil {
			t.Fatalf("create v-u1-same-branch: %v", err)
		}
		if err := store.CreateVersion(ctx, &core.BranchVersion{
			ID: "v-u1-other-branch", UserID: "u1", BranchID: "br-u1-2", Status: core.VersionStatusPending, CreatedAt: base.Add(-2 * time.Minute),
		}); err != nil {
			t.Fatalf("create v-u1-other-branch: %v", err)
		}
		c3, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{
			WorkerID: "w3",
			Now:      base,
		})
		if err != nil || c3 == nil || c3.ID != "v-u1-other-branch" {
			t.Fatalf("expected v-u1-other-branch due to branch exclusivity on br-u1-1, got c=%+v err=%v", c3, err)
		}

		// Advance clock past v-u3-retry's NextRetryAt (base + 1m): u3 retry becomes claimable.
		c4, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{
			WorkerID:      "w4",
			Now:           base.Add(time.Minute),
			LastServedSeq: map[string]uint64{"u1": 10},
		})
		if err != nil || c4 == nil || c4.ID != "v-u3-retry" {
			t.Fatalf("expected v-u3-retry at base+1m, got c=%+v err=%v", c4, err)
		}
	})

	t.Run("ClaimNextJob fairness, sibling throttling, and expired/future-retry skipping", func(t *testing.T) {
		store := setupTestStore(t)
		if err := store.CreateBranch(ctx, &core.Branch{ID: "br-sib", UserID: "u2", Name: "sib", TemplateSetID: core.TemplateStandardCleanID}); err != nil {
			t.Fatalf("create branch: %v", err)
		}
		// Active processing version for u2 throttles u2 PDF jobs when OtherActiveByTenant is nil.
		if err := store.CreateVersion(ctx, &core.BranchVersion{
			ID: "ver-sib-proc", UserID: "u2", BranchID: "br-sib", Status: core.VersionStatusProcessing,
			LeaseExpiresAt: ptrTime(base.Add(2 * time.Minute)),
		}); err != nil {
			t.Fatalf("create active sibling version: %v", err)
		}

		jobs := []*core.PDFParseJob{
			{ID: "j-exp", UserID: "u1", Status: core.PDFParseJobStatusPending, CreatedAt: base.Add(-10 * time.Minute), ExpiresAt: ptrTime(base.Add(-time.Second))},
			{ID: "j-u1-new", UserID: "u1", Status: core.PDFParseJobStatusPending, CreatedAt: base.Add(-1 * time.Minute), ExpiresAt: ptrTime(base.Add(time.Hour))},
			{ID: "j-u1-old", UserID: "u1", Status: core.PDFParseJobStatusPending, CreatedAt: base.Add(-5 * time.Minute), ExpiresAt: ptrTime(base.Add(time.Hour))},
			{ID: "j-u2", UserID: "u2", Status: core.PDFParseJobStatusPending, CreatedAt: base.Add(-8 * time.Minute), ExpiresAt: ptrTime(base.Add(time.Hour))},
		}
		for _, j := range jobs {
			if err := store.CreateJob(ctx, j); err != nil {
				t.Fatalf("create job %s: %v", j.ID, err)
			}
		}

		// u2 is blocked by ver-sib-proc (MaxConcurrentPerTenant=1), u1 oldest eligible is j-u1-old.
		c1, err := store.ClaimNextJob(ctx, core.ClaimJobRequest{
			WorkerID:               "w1",
			Now:                    base,
			MaxConcurrentPerTenant: 1,
		})
		if err != nil || c1 == nil || c1.ID != "j-u1-old" {
			t.Fatalf("expected j-u1-old, got c=%+v err=%v", c1, err)
		}

		// With u1 at capacity=1 and u2 at capacity=1, claim returns (nil, nil).
		cNone, err := store.ClaimNextJob(ctx, core.ClaimJobRequest{
			WorkerID:               "w2",
			Now:                    base,
			MaxConcurrentPerTenant: 1,
		})
		if err != nil || cNone != nil {
			t.Fatalf("expected (nil, nil) when all tenants throttled, got (%+v, %v)", cNone, err)
		}

		// After ver-sib-proc lease expires (base+3m), u2 becomes claimable even with MaxConcurrentPerTenant=1.
		c2, err := store.ClaimNextJob(ctx, core.ClaimJobRequest{
			WorkerID:               "w2",
			Now:                    base.Add(3 * time.Minute),
			MaxConcurrentPerTenant: 1,
		})
		if err != nil || c2 == nil || c2.ID != "j-u2" {
			t.Fatalf("expected j-u2 after sibling lease expired, got c=%+v err=%v", c2, err)
		}
	})

	t.Run("multi-goroutine race claim uniqueness for versions and PDF jobs", func(t *testing.T) {
		store := setupTestStore(t)
		const total = 20
		for i := 0; i < total; i++ {
			bID := fmt.Sprintf("br-uniq-%d", i)
			uID := fmt.Sprintf("u-uniq-%d", i%4)
			if err := store.CreateBranch(ctx, &core.Branch{ID: bID, UserID: uID, Name: bID, TemplateSetID: core.TemplateStandardCleanID}); err != nil {
				t.Fatalf("create branch: %v", err)
			}
			if _, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{
				ID: fmt.Sprintf("v-uniq-%d", i), UserID: uID, BranchID: bID,
			}, core.PendingJobOptions{Now: base}); err != nil {
				t.Fatalf("submit version: %v", err)
			}
			if err := store.SubmitPendingJob(ctx, &core.PDFParseJob{
				ID: fmt.Sprintf("j-uniq-%d", i), UserID: uID, RawText: "text",
			}, core.PendingJobOptions{Now: base}); err != nil {
				t.Fatalf("submit job: %v", err)
			}
		}

		var wg sync.WaitGroup
		var mu sync.Mutex
		claimedVerIDs := make(map[string]int)
		claimedJobIDs := make(map[string]int)

		for w := 0; w < 24; w++ {
			wg.Add(1)
			go func(workerNum int) {
				defer wg.Done()
				workerID := fmt.Sprintf("worker-%d", workerNum)
				for {
					v, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{WorkerID: workerID, Now: base})
					if err != nil {
						t.Errorf("ClaimNextVersion err: %v", err)
						return
					}
					if v == nil {
						break
					}
					mu.Lock()
					claimedVerIDs[v.ID]++
					mu.Unlock()
				}
				for {
					j, err := store.ClaimNextJob(ctx, core.ClaimJobRequest{WorkerID: workerID, Now: base})
					if err != nil {
						t.Errorf("ClaimNextJob err: %v", err)
						return
					}
					if j == nil {
						break
					}
					mu.Lock()
					claimedJobIDs[j.ID]++
					mu.Unlock()
				}
			}(w)
		}
		wg.Wait()

		if len(claimedVerIDs) != total {
			t.Fatalf("expected %d unique claimed versions, got %d", total, len(claimedVerIDs))
		}
		for id, count := range claimedVerIDs {
			if count != 1 {
				t.Errorf("version %s claimed %d times (expected 1)", id, count)
			}
		}
		if len(claimedJobIDs) != total {
			t.Fatalf("expected %d unique claimed PDF jobs, got %d", total, len(claimedJobIDs))
		}
		for id, count := range claimedJobIDs {
			if count != 1 {
				t.Errorf("job %s claimed %d times (expected 1)", id, count)
			}
		}
	})
}

// TestMemoryStore_LeaseRenewRetryCompleteAndFail covers Renew*Lease, Requeue*ForRetry,
// CompleteClaimedJob, FailClaimedVersion, and FailClaimedJob.
func TestMemoryStore_LeaseRenewRetryCompleteAndFail(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("version renew, requeue within TTL vs past ExpiresAt, and fail claimed", func(t *testing.T) {
		store := setupTestStore(t)
		br := &core.Branch{ID: "br-lr", UserID: "u1", Name: "LR", TemplateSetID: core.TemplateStandardCleanID}
		if err := store.CreateBranch(ctx, br); err != nil {
			t.Fatalf("create branch: %v", err)
		}
		v := &core.BranchVersion{
			ID: "v-lr-1", UserID: "u1", BranchID: br.ID, ReservationID: "res-lr-1",
		}
		if _, err := store.SubmitPendingVersion(ctx, v, core.PendingJobOptions{Now: base, TTL: 10 * time.Minute}); err != nil {
			t.Fatalf("submit version: %v", err)
		}
		expiresAt := base.Add(10 * time.Minute)

		claimed, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{WorkerID: "w1", Now: base, LeaseDuration: 2 * time.Minute})
		if err != nil || claimed == nil {
			t.Fatalf("claim version: %v", err)
		}

		// Wrong worker or missing ID fails RenewVersionLease.
		if err := store.RenewVersionLease(ctx, "missing", "w1", base.Add(5*time.Minute)); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
		if err := store.RenewVersionLease(ctx, v.ID, "w2", base.Add(5*time.Minute)); !errors.Is(err, core.ErrConflict) {
			t.Errorf("expected ErrConflict for wrong worker, got %v", err)
		}
		newLease := base.Add(5 * time.Minute)
		if err := store.RenewVersionLease(ctx, v.ID, "w1", newLease); err != nil {
			t.Fatalf("RenewVersionLease: %v", err)
		}
		got, _ := store.Version(ctx, br.ID, v.ID)
		if got.LeaseExpiresAt == nil || !got.LeaseExpiresAt.Equal(newLease) {
			t.Fatalf("expected LeaseExpiresAt %v, got %v", newLease, got.LeaseExpiresAt)
		}

		// Requeue past or equal to ExpiresAt returns ErrJobExpired without clearing claim.
		if err := store.RequeueVersionForRetry(ctx, v.ID, "w1", expiresAt, "transient"); !errors.Is(err, core.ErrJobExpired) {
			t.Fatalf("expected ErrJobExpired when nextRetryAt == ExpiresAt, got %v", err)
		}
		got, _ = store.Version(ctx, br.ID, v.ID)
		if got.Status != core.VersionStatusProcessing || got.ClaimedBy != "w1" {
			t.Fatalf("expected failed requeue to leave version processing by w1, got %+v", got)
		}

		// Requeue strictly before ExpiresAt succeeds, clears lease metadata, preserves ReservationID.
		retryAt := base.Add(3 * time.Minute)
		if err := store.RequeueVersionForRetry(ctx, v.ID, "w2", retryAt, "transient"); !errors.Is(err, core.ErrConflict) {
			t.Errorf("expected ErrConflict on requeue by wrong worker, got %v", err)
		}
		if err := store.RequeueVersionForRetry(ctx, v.ID, "w1", retryAt, "rate limited"); err != nil {
			t.Fatalf("RequeueVersionForRetry: %v", err)
		}
		got, _ = store.Version(ctx, br.ID, v.ID)
		if got.Status != core.VersionStatusPending || got.ClaimedBy != "" || got.ClaimedAt != nil || got.LeaseExpiresAt != nil {
			t.Fatalf("expected lease metadata cleared on requeue, got %+v", got)
		}
		if got.NextRetryAt == nil || !got.NextRetryAt.Equal(retryAt) || got.ReservationID != "res-lr-1" || got.AttemptCount != 1 {
			t.Fatalf("unexpected requeued version fields: %+v", got)
		}

		// Second claim increments AttemptCount to 2, then FailClaimedVersion transitions to failed.
		claimed2, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{WorkerID: "w2", Now: retryAt, LeaseDuration: 2 * time.Minute})
		if err != nil || claimed2 == nil || claimed2.AttemptCount != 2 {
			t.Fatalf("expected second claim attempt_count=2, got %+v err=%v", claimed2, err)
		}
		if err := store.FailClaimedVersion(ctx, v.ID, "w1", "stale worker"); !errors.Is(err, core.ErrConflict) {
			t.Errorf("expected ErrConflict on FailClaimedVersion by wrong worker, got %v", err)
		}
		if err := store.FailClaimedVersion(ctx, v.ID, "w2", "permanent failure"); err != nil {
			t.Fatalf("FailClaimedVersion: %v", err)
		}
		got, _ = store.Version(ctx, br.ID, v.ID)
		if got.Status != core.VersionStatusFailed || got.ErrorMessage != "permanent failure" || got.CompletedAt == nil {
			t.Fatalf("unexpected failed version: %+v", got)
		}
	})

	t.Run("PDF job renew, requeue preserves RawText & ReservationID, complete and fail prune RawText", func(t *testing.T) {
		store := setupTestStore(t)
		job := &core.PDFParseJob{
			ID:            "job-lr-1",
			UserID:        "u1",
			RawText:       "original extracted pdf text",
			ReservationID: "res-pdf-1",
		}
		if err := store.SubmitPendingJob(ctx, job, core.PendingJobOptions{Now: base, TTL: 10 * time.Minute}); err != nil {
			t.Fatalf("submit job: %v", err)
		}
		claimed, err := store.ClaimNextJob(ctx, core.ClaimJobRequest{WorkerID: "w1", Now: base, LeaseDuration: 2 * time.Minute})
		if err != nil || claimed == nil {
			t.Fatalf("claim job: %v", err)
		}

		if err := store.RenewJobLease(ctx, job.ID, "w2", base.Add(4*time.Minute)); !errors.Is(err, core.ErrConflict) {
			t.Errorf("expected ErrConflict on wrong worker renew, got %v", err)
		}
		if err := store.RenewJobLease(ctx, job.ID, "w1", base.Add(4*time.Minute)); err != nil {
			t.Fatalf("RenewJobLease: %v", err)
		}
		if err := store.RequeueJobForRetry(ctx, job.ID, "w1", base.Add(11*time.Minute), "too late"); !errors.Is(err, core.ErrJobExpired) {
			t.Fatalf("expected ErrJobExpired when nextRetryAt > ExpiresAt, got %v", err)
		}
		retryAt := base.Add(3 * time.Minute)
		if err := store.RequeueJobForRetry(ctx, job.ID, "w1", retryAt, "temporary 429"); err != nil {
			t.Fatalf("RequeueJobForRetry: %v", err)
		}
		got, _ := store.Job(ctx, "u1", job.ID)
		if got.Status != core.PDFParseJobStatusPending || got.RawText != "original extracted pdf text" || got.ReservationID != "res-pdf-1" || got.ClaimedBy != "" {
			t.Fatalf("unexpected requeued pdf job: %+v", got)
		}

		// Re-claim and complete.
		if _, err := store.ClaimNextJob(ctx, core.ClaimJobRequest{WorkerID: "w2", Now: retryAt}); err != nil {
			t.Fatalf("reclaim job: %v", err)
		}
		if err := store.CompleteClaimedJob(ctx, job.ID, "w1", &core.JSONResume{}); !errors.Is(err, core.ErrConflict) {
			t.Errorf("expected ErrConflict completing with wrong worker, got %v", err)
		}
		res := &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Alice"}}
		if err := store.CompleteClaimedJob(ctx, job.ID, "w2", res); err != nil {
			t.Fatalf("CompleteClaimedJob: %v", err)
		}
		got, _ = store.Job(ctx, "u1", job.ID)
		if got.Status != core.PDFParseJobStatusCompleted || got.RawText != "" || got.Result == nil || got.Result.Basics.Name != "Alice" {
			t.Fatalf("unexpected completed pdf job: %+v", got)
		}

		// Also verify FailClaimedJob prunes RawText.
		job2 := &core.PDFParseJob{ID: "job-lr-2", UserID: "u1", RawText: "secret raw text", ReservationID: "res-pdf-2"}
		if err := store.SubmitPendingJob(ctx, job2, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit job2: %v", err)
		}
		if _, err := store.ClaimNextJob(ctx, core.ClaimJobRequest{WorkerID: "w1", Now: base}); err != nil {
			t.Fatalf("claim job2: %v", err)
		}
		if err := store.FailClaimedJob(ctx, job2.ID, "w2", "wrong"); !errors.Is(err, core.ErrConflict) {
			t.Errorf("expected ErrConflict failing with wrong worker, got %v", err)
		}
		if err := store.FailClaimedJob(ctx, job2.ID, "w1", "bad pdf"); err != nil {
			t.Fatalf("FailClaimedJob: %v", err)
		}
		got2, _ := store.Job(ctx, "u1", job2.ID)
		if got2.Status != core.PDFParseJobStatusFailed || got2.RawText != "" || got2.ErrorMessage != "bad pdf" {
			t.Fatalf("unexpected failed job2: %+v", got2)
		}
	})
}

// TestMemoryStore_ExpirationLeaseFailAndRecovery verifies ExpirePending*, FailExpired*Leases,
// and RecoverInterrupted* behavior including reservation release, RawText pruning, and preserving unexpired pending work.
func TestMemoryStore_ExpirationLeaseFailAndRecovery(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("ExpirePendingVersions and ExpirePendingJobs", func(t *testing.T) {
		store := setupTestStore(t)
		if err := store.CreateBranch(ctx, &core.Branch{ID: "br-exp", UserID: "u1", Name: "Exp", TemplateSetID: core.TemplateStandardCleanID}); err != nil {
			t.Fatalf("create branch: %v", err)
		}
		if err := store.CreateVersion(ctx, &core.BranchVersion{
			ID: "v-expired", UserID: "u1", BranchID: "br-exp", Status: core.VersionStatusPending,
			ReservationID: "res-v-exp", ExpiresAt: ptrTime(base.Add(-time.Second)),
		}); err != nil {
			t.Fatalf("create v-expired: %v", err)
		}
		if err := store.CreateVersion(ctx, &core.BranchVersion{
			ID: "v-valid", UserID: "u1", BranchID: "br-exp", Status: core.VersionStatusPending,
			ReservationID: "res-v-ok", ExpiresAt: ptrTime(base.Add(time.Minute)),
		}); err != nil {
			t.Fatalf("create v-valid: %v", err)
		}

		relV, err := store.ExpirePendingVersions(ctx, base, "ttl expired")
		if err != nil {
			t.Fatalf("ExpirePendingVersions: %v", err)
		}
		if len(relV) != 1 || relV[0].JobID != "v-expired" || relV[0].ReservationID != "res-v-exp" || relV[0].Reason != "ttl expired" {
			t.Fatalf("unexpected expired version reservations: %+v", relV)
		}
		vValid, _ := store.Version(ctx, "br-exp", "v-valid")
		if vValid.Status != core.VersionStatusPending {
			t.Errorf("expected v-valid to stay pending, got %s", vValid.Status)
		}

		if err := store.CreateJob(ctx, &core.PDFParseJob{
			ID: "j-expired", UserID: "u1", Status: core.PDFParseJobStatusPending, RawText: "prune me",
			ReservationID: "res-j-exp", ExpiresAt: ptrTime(base),
		}); err != nil {
			t.Fatalf("create j-expired: %v", err)
		}
		if err := store.CreateJob(ctx, &core.PDFParseJob{
			ID: "j-valid", UserID: "u1", Status: core.PDFParseJobStatusPending, RawText: "keep me",
			ReservationID: "res-j-ok", ExpiresAt: ptrTime(base.Add(time.Minute)),
		}); err != nil {
			t.Fatalf("create j-valid: %v", err)
		}

		relJ, err := store.ExpirePendingJobs(ctx, base, "job ttl expired")
		if err != nil {
			t.Fatalf("ExpirePendingJobs: %v", err)
		}
		if len(relJ) != 1 || relJ[0].JobID != "j-expired" || relJ[0].ReservationID != "res-j-exp" {
			t.Fatalf("unexpected expired job reservations: %+v", relJ)
		}
		jExp, _ := store.Job(ctx, "u1", "j-expired")
		if jExp.Status != core.PDFParseJobStatusFailed || jExp.RawText != "" {
			t.Errorf("expected expired job failed with RawText pruned, got %+v", jExp)
		}
		jValid, _ := store.Job(ctx, "u1", "j-valid")
		if jValid.Status != core.PDFParseJobStatusPending || jValid.RawText != "keep me" {
			t.Errorf("expected unexpired job preserved, got %+v", jValid)
		}
	})

	t.Run("FailExpiredVersionLeases and FailExpiredJobLeases", func(t *testing.T) {
		store := setupTestStore(t)
		if err := store.CreateBranch(ctx, &core.Branch{ID: "br-lease", UserID: "u1", Name: "Lease", TemplateSetID: core.TemplateStandardCleanID}); err != nil {
			t.Fatalf("create branch: %v", err)
		}
		if err := store.CreateVersion(ctx, &core.BranchVersion{
			ID: "v-lease-exp", UserID: "u1", BranchID: "br-lease", Status: core.VersionStatusProcessing,
			ClaimedBy: "w1", ReservationID: "res-vl-exp", LeaseExpiresAt: ptrTime(base.Add(-time.Second)),
		}); err != nil {
			t.Fatalf("create v-lease-exp: %v", err)
		}
		if err := store.CreateVersion(ctx, &core.BranchVersion{
			ID: "v-lease-ok", UserID: "u1", BranchID: "br-lease", Status: core.VersionStatusProcessing,
			ClaimedBy: "w2", ReservationID: "res-vl-ok", LeaseExpiresAt: ptrTime(base.Add(time.Minute)),
		}); err != nil {
			t.Fatalf("create v-lease-ok: %v", err)
		}

		relV, err := store.FailExpiredVersionLeases(ctx, base, "lease timed out")
		if err != nil {
			t.Fatalf("FailExpiredVersionLeases: %v", err)
		}
		if len(relV) != 1 || relV[0].JobID != "v-lease-exp" || relV[0].ReservationID != "res-vl-exp" {
			t.Fatalf("unexpected lease failure reservations: %+v", relV)
		}
		vOK, _ := store.Version(ctx, "br-lease", "v-lease-ok")
		if vOK.Status != core.VersionStatusProcessing {
			t.Errorf("expected unexpired lease version to remain processing, got %s", vOK.Status)
		}

		if err := store.CreateJob(ctx, &core.PDFParseJob{
			ID: "j-lease-exp", UserID: "u1", Status: core.PDFParseJobStatusProcessing, ClaimedBy: "w1",
			RawText: "secret", ReservationID: "res-jl-exp", LeaseExpiresAt: ptrTime(base),
		}); err != nil {
			t.Fatalf("create j-lease-exp: %v", err)
		}
		if err := store.CreateJob(ctx, &core.PDFParseJob{
			ID: "j-lease-ok", UserID: "u1", Status: core.PDFParseJobStatusProcessing, ClaimedBy: "w2",
			RawText: "active", ReservationID: "res-jl-ok", LeaseExpiresAt: ptrTime(base.Add(time.Minute)),
		}); err != nil {
			t.Fatalf("create j-lease-ok: %v", err)
		}

		relJ, err := store.FailExpiredJobLeases(ctx, base, "job lease timed out")
		if err != nil {
			t.Fatalf("FailExpiredJobLeases: %v", err)
		}
		if len(relJ) != 1 || relJ[0].JobID != "j-lease-exp" || relJ[0].ReservationID != "res-jl-exp" {
			t.Fatalf("unexpected job lease failure reservations: %+v", relJ)
		}
		jExp, _ := store.Job(ctx, "u1", "j-lease-exp")
		if jExp.Status != core.PDFParseJobStatusFailed || jExp.RawText != "" {
			t.Errorf("expected expired lease job failed and RawText pruned, got %+v", jExp)
		}
		jOK, _ := store.Job(ctx, "u1", "j-lease-ok")
		if jOK.Status != core.PDFParseJobStatusProcessing || jOK.RawText != "active" {
			t.Errorf("expected active job lease untouched, got %+v", jOK)
		}
	})

	t.Run("RecoverInterruptedVersions and RecoverInterruptedJobs fail processing and expired pending while preserving unexpired pending", func(t *testing.T) {
		clk := newFakeClock(base)
		store := memory.NewWithClock(clk.Now)
		if err := store.CreateBranch(ctx, &core.Branch{ID: "br-rec", UserID: "u1", Name: "Rec", TemplateSetID: core.TemplateStandardCleanID}); err != nil {
			t.Fatalf("create branch: %v", err)
		}

		// Versions: processing, expired pending, unexpired pending.
		_ = store.CreateVersion(ctx, &core.BranchVersion{ID: "v-rec-proc", UserID: "u1", BranchID: "br-rec", Status: core.VersionStatusProcessing, ReservationID: "r-vp"})
		_ = store.CreateVersion(ctx, &core.BranchVersion{ID: "v-rec-exp", UserID: "u1", BranchID: "br-rec", Status: core.VersionStatusPending, ReservationID: "r-ve", ExpiresAt: ptrTime(base.Add(-time.Minute))})
		_ = store.CreateVersion(ctx, &core.BranchVersion{ID: "v-rec-ok", UserID: "u1", BranchID: "br-rec", Status: core.VersionStatusPending, ReservationID: "r-vo", ExpiresAt: ptrTime(base.Add(10 * time.Minute))})

		relV, err := store.RecoverInterruptedVersions(ctx, "process restarted")
		if err != nil {
			t.Fatalf("RecoverInterruptedVersions: %v", err)
		}
		if len(relV) != 2 {
			t.Fatalf("expected 2 released version reservations, got %+v", relV)
		}
		vOK, _ := store.Version(ctx, "br-rec", "v-rec-ok")
		if vOK.Status != core.VersionStatusPending {
			t.Errorf("expected unexpired pending version preserved, got %s", vOK.Status)
		}

		// Jobs: processing, expired pending, unexpired pending.
		_ = store.CreateJob(ctx, &core.PDFParseJob{ID: "j-rec-proc", UserID: "u1", Status: core.PDFParseJobStatusProcessing, RawText: "p", ReservationID: "r-jp"})
		_ = store.CreateJob(ctx, &core.PDFParseJob{ID: "j-rec-exp", UserID: "u1", Status: core.PDFParseJobStatusPending, RawText: "e", ReservationID: "r-je", ExpiresAt: ptrTime(base.Add(-time.Minute))})
		_ = store.CreateJob(ctx, &core.PDFParseJob{ID: "j-rec-ok", UserID: "u1", Status: core.PDFParseJobStatusPending, RawText: "keep", ReservationID: "r-jo", ExpiresAt: ptrTime(base.Add(10 * time.Minute))})

		relJ, err := store.RecoverInterruptedJobs(ctx, "process restarted")
		if err != nil {
			t.Fatalf("RecoverInterruptedJobs: %v", err)
		}
		if len(relJ) != 2 {
			t.Fatalf("expected 2 released job reservations, got %+v", relJ)
		}
		jProc, _ := store.Job(ctx, "u1", "j-rec-proc")
		jExp, _ := store.Job(ctx, "u1", "j-rec-exp")
		jOK, _ := store.Job(ctx, "u1", "j-rec-ok")
		if jProc.Status != core.PDFParseJobStatusFailed || jProc.RawText != "" {
			t.Errorf("expected j-rec-proc failed and RawText pruned, got %+v", jProc)
		}
		if jExp.Status != core.PDFParseJobStatusFailed || jExp.RawText != "" {
			t.Errorf("expected j-rec-exp failed and RawText pruned, got %+v", jExp)
		}
		if jOK.Status != core.PDFParseJobStatusPending || jOK.RawText != "keep" {
			t.Errorf("expected j-rec-ok preserved, got %+v", jOK)
		}
	})
}

// TestMemoryStore_StatusGuardsAndCompleteVersion verifies terminal status non-resurrection,
// CompleteVersion claim ownership checks, stale VersionNumber rejection, and AnalyzeGaps updates.
func TestMemoryStore_StatusGuardsAndCompleteVersion(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("UpdateVersionStatus and UpdateJobStatus reject resurrection out of terminal states", func(t *testing.T) {
		store := setupTestStore(t)
		if err := store.CreateBranch(ctx, &core.Branch{ID: "br-term", UserID: "u1", Name: "Term", TemplateSetID: core.TemplateStandardCleanID}); err != nil {
			t.Fatalf("create branch: %v", err)
		}
		for _, status := range []core.VersionStatus{core.VersionStatusFailed, core.VersionStatusCancelled, core.VersionStatusCompleted} {
			id := "v-" + string(status)
			_ = store.CreateVersion(ctx, &core.BranchVersion{ID: id, UserID: "u1", BranchID: "br-term", Status: status})
			if err := store.UpdateVersionStatus(ctx, id, core.VersionStatusProcessing, ""); !errors.Is(err, core.ErrConflict) {
				t.Errorf("UpdateVersionStatus from %s: expected ErrConflict, got %v", status, err)
			}
		}

		for _, status := range []core.PDFParseJobStatus{core.PDFParseJobStatusFailed, core.PDFParseJobStatusCancelled, core.PDFParseJobStatusCompleted} {
			id := "j-" + string(status)
			_ = store.CreateJob(ctx, &core.PDFParseJob{ID: id, UserID: "u1", Status: status})
			if err := store.UpdateJobStatus(ctx, id, core.PDFParseJobStatusProcessing, nil, ""); !errors.Is(err, core.ErrConflict) {
				t.Errorf("UpdateJobStatus from %s: expected ErrConflict, got %v", status, err)
			}
		}

		// UpdateJobStatus to terminal prunes RawText.
		_ = store.CreateJob(ctx, &core.PDFParseJob{ID: "j-prune", UserID: "u1", Status: core.PDFParseJobStatusProcessing, RawText: "sensitive"})
		if err := store.UpdateJobStatus(ctx, "j-prune", core.PDFParseJobStatusCompleted, &core.JSONResume{}, ""); err != nil {
			t.Fatalf("UpdateJobStatus to completed: %v", err)
		}
		got, _ := store.Job(ctx, "u1", "j-prune")
		if got.RawText != "" {
			t.Errorf("expected RawText pruned on UpdateJobStatus completed, got %q", got.RawText)
		}
	})

	t.Run("CompleteVersion guards terminal resurrection, wrong ClaimedBy, stale VersionNumber, and preserves AnalyzeGaps", func(t *testing.T) {
		clk := newFakeClock(base)
		store := memory.NewWithClock(clk.Now)
		br := &core.Branch{ID: "br-cv", UserID: "u1", Name: "CV", TemplateSetID: core.TemplateStandardCleanID}
		if err := store.CreateBranch(ctx, br); err != nil {
			t.Fatalf("create branch: %v", err)
		}

		// 1. Terminal failed/cancelled resurrection rejected.
		for _, termStatus := range []core.VersionStatus{core.VersionStatusFailed, core.VersionStatusCancelled} {
			vTerm := &core.BranchVersion{ID: "v-term-" + string(termStatus), UserID: "u1", BranchID: br.ID, Status: termStatus}
			_ = store.CreateVersion(ctx, vTerm)
			vTerm.Status = core.VersionStatusCompleted
			if err := store.CompleteVersion(ctx, vTerm); !errors.Is(err, core.ErrConflict) {
				t.Errorf("CompleteVersion on %s: expected ErrConflict, got %v", termStatus, err)
			}
		}

		// 2. Claimed processing version rejects wrong ClaimedBy (both wrong ID and empty).
		vClaimed := &core.BranchVersion{ID: "v-claimed", UserID: "u1", BranchID: br.ID}
		if _, err := store.SubmitPendingVersion(ctx, vClaimed, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit vClaimed: %v", err)
		}
		claimed, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{WorkerID: "w1", Now: base})
		if err != nil || claimed == nil {
			t.Fatalf("claim vClaimed: %v", err)
		}

		wrongWorkerCopy := *claimed
		wrongWorkerCopy.ClaimedBy = "w2"
		wrongWorkerCopy.Status = core.VersionStatusCompleted
		if err := store.CompleteVersion(ctx, &wrongWorkerCopy); !errors.Is(err, core.ErrConflict) {
			t.Errorf("expected ErrConflict for wrong ClaimedBy, got %v", err)
		}
		emptyWorkerCopy := *claimed
		emptyWorkerCopy.ClaimedBy = ""
		emptyWorkerCopy.Status = core.VersionStatusCompleted
		if err := store.CompleteVersion(ctx, &emptyWorkerCopy); !errors.Is(err, core.ErrConflict) {
			t.Errorf("expected ErrConflict for empty ClaimedBy on claimed version, got %v", err)
		}

		// Completing with matching ClaimedBy succeeds.
		claimed.Status = core.VersionStatusCompleted
		claimed.HTMLOutput = "<html>v1</html>"
		if err := store.CompleteVersion(ctx, claimed); err != nil {
			t.Fatalf("CompleteVersion with matching ClaimedBy: %v", err)
		}

		// 3. Higher VersionNumber completes and becomes active, then stale older VersionNumber is rejected.
		vHigher := &core.BranchVersion{ID: "v-higher", UserID: "u1", BranchID: br.ID, Status: core.VersionStatusCompleted, HTMLOutput: "<html>v2</html>"}
		if err := store.CreateVersion(ctx, vHigher); err != nil {
			t.Fatalf("create vHigher: %v", err)
		}
		if err := store.CompleteVersion(ctx, vHigher); err != nil {
			t.Fatalf("complete vHigher: %v", err)
		}

		// Older VersionNumber (vClaimed) attempting CompleteVersion must fail with ErrConflict.
		if err := store.CompleteVersion(ctx, claimed); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("expected ErrConflict for stale older VersionNumber overwriting higher active version, got %v", err)
		}
		gotBr, _ := store.Branch(ctx, "u1", br.ID)
		if gotBr.ActiveVersionID == nil || *gotBr.ActiveVersionID != vHigher.ID {
			t.Fatalf("expected active version to remain %s, got %v", vHigher.ID, gotBr.ActiveVersionID)
		}

		// 4. AnalyzeGaps update on the active completed version succeeds and advances UpdatedAt.
		clk.Advance(2 * time.Minute)
		activeCopy, err := store.Version(ctx, br.ID, vHigher.ID)
		if err != nil {
			t.Fatalf("get active version: %v", err)
		}
		score := 92.5
		activeCopy.GapReport = &core.GapReport{OverallMatchSummary: "Updated by AnalyzeGaps", MatchScore: &score}
		if err := store.CompleteVersion(ctx, activeCopy); err != nil {
			t.Fatalf("AnalyzeGaps CompleteVersion on active version failed: %v", err)
		}
		gotActive, _ := store.Version(ctx, br.ID, vHigher.ID)
		if gotActive.GapReport == nil || gotActive.GapReport.OverallMatchSummary != "Updated by AnalyzeGaps" {
			t.Fatalf("expected updated GapReport on active version, got %+v", gotActive.GapReport)
		}
		if !gotActive.UpdatedAt.Equal(clk.Now()) {
			t.Errorf("expected UpdatedAt %v, got %v", clk.Now(), gotActive.UpdatedAt)
		}
		gotBr, _ = store.Branch(ctx, "u1", br.ID)
		if gotBr.GapReport == nil || gotBr.GapReport.OverallMatchSummary != "Updated by AnalyzeGaps" {
			t.Errorf("expected updated GapReport mirrored onto branch, got %+v", gotBr.GapReport)
		}
	})
}

// TestMemoryStore_DeepCopyIsolation_QueueFields verifies mutations to time pointers on
// BranchVersion and PDFParseJob do not alias internal store state.
func TestMemoryStore_DeepCopyIsolation_QueueFields(t *testing.T) {
	ctx := context.Background()
	store := setupTestStore(t)
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	_ = store.CreateBranch(ctx, &core.Branch{ID: "br-dcq", UserID: "u1", Name: "DCQ", TemplateSetID: core.TemplateStandardCleanID})

	origExp := base.Add(30 * time.Minute)
	origRetry := base.Add(2 * time.Minute)
	exp := origExp
	retry := origRetry
	v := &core.BranchVersion{
		ID: "v-dcq", UserID: "u1", BranchID: "br-dcq", ExpiresAt: &exp, NextRetryAt: &retry,
	}
	if _, err := store.SubmitPendingVersion(ctx, v, core.PendingJobOptions{Now: base}); err != nil {
		t.Fatalf("submit version: %v", err)
	}
	*v.ExpiresAt = base.Add(99 * time.Hour)
	*v.NextRetryAt = base.Add(99 * time.Hour)

	pendingV, _ := store.Version(ctx, "br-dcq", "v-dcq")
	if pendingV.NextRetryAt == nil || !pendingV.NextRetryAt.Equal(origRetry) {
		t.Errorf("version NextRetryAt aliased caller pointer: %+v", pendingV.NextRetryAt)
	}

	claimedV, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{WorkerID: "w1", Now: base.Add(3 * time.Minute)})
	if err != nil || claimedV == nil {
		t.Fatalf("claim version: %v", err)
	}
	*claimedV.ClaimedAt = base.Add(-99 * time.Hour)
	*claimedV.LeaseExpiresAt = base.Add(-99 * time.Hour)

	fetchedV, _ := store.Version(ctx, "br-dcq", "v-dcq")
	if !fetchedV.ExpiresAt.Equal(origExp) || !fetchedV.ClaimedAt.Equal(base.Add(3*time.Minute)) || !fetchedV.LeaseExpiresAt.Equal(base.Add(5*time.Minute)) {
		t.Errorf("version time pointers aliased internal state: %+v", fetchedV)
	}

	origJExp := base.Add(30 * time.Minute)
	jExp := origJExp
	j := &core.PDFParseJob{ID: "j-dcq", UserID: "u1", RawText: "txt", ExpiresAt: &jExp}
	if err := store.SubmitPendingJob(ctx, j, core.PendingJobOptions{Now: base}); err != nil {
		t.Fatalf("submit job: %v", err)
	}
	*j.ExpiresAt = base.Add(99 * time.Hour)

	claimedJ, err := store.ClaimNextJob(ctx, core.ClaimJobRequest{WorkerID: "w1", Now: base})
	if err != nil || claimedJ == nil {
		t.Fatalf("claim job: %v", err)
	}
	*claimedJ.ClaimedAt = base.Add(-99 * time.Hour)
	*claimedJ.LeaseExpiresAt = base.Add(-99 * time.Hour)

	fetchedJ, _ := store.Job(ctx, "u1", "j-dcq")
	if !fetchedJ.ExpiresAt.Equal(origJExp) || !fetchedJ.ClaimedAt.Equal(base) || !fetchedJ.LeaseExpiresAt.Equal(base.Add(2*time.Minute)) {
		t.Errorf("pdf job time pointers aliased internal state: %+v", fetchedJ)
	}
}

// TestMemoryStore_QuotaWindowRolloverSafety verifies window stamping and independent
// daily/monthly rollover guards in ReleaseQuota and CommitUsage.
func TestMemoryStore_QuotaWindowRolloverSafety(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	t.Run("ReserveQuota stamps windows and daily rollover blocks daily decrement while allowing same-month refund/commit", func(t *testing.T) {
		clk := newFakeClock(t0)
		store := memory.NewWithClock(clk.Now)
		userID := "quota-rollover-daily"

		r1, err := store.ReserveQuota(ctx, userID, core.OperationResumeTailoring, 1000, 10, 10000)
		if err != nil {
			t.Fatalf("reserve r1: %v", err)
		}
		if !r1.DailyWindowStart.Equal(t0) || !r1.MonthlyWindowStart.Equal(t0) {
			t.Fatalf("expected windows stamped to %v, got daily=%v monthly=%v", t0, r1.DailyWindowStart, r1.MonthlyWindowStart)
		}

		r2, err := store.ReserveQuota(ctx, userID, core.OperationGapAnalysis, 800, 10, 10000)
		if err != nil {
			t.Fatalf("reserve r2: %v", err)
		}

		// Advance 25 hours: daily window resets, monthly window stays at t0.
		t1 := clk.Advance(25 * time.Hour)
		r3, err := store.ReserveQuota(ctx, userID, core.OperationPDFParse, 500, 10, 10000)
		if err != nil {
			t.Fatalf("reserve r3 in new daily window: %v", err)
		}
		if !r3.DailyWindowStart.Equal(t1) || !r3.MonthlyWindowStart.Equal(t0) {
			t.Fatalf("expected daily=%v monthly=%v on r3, got daily=%v monthly=%v", t1, t0, r3.DailyWindowStart, r3.MonthlyWindowStart)
		}

		// Delayed ReleaseQuota of r1 (from old daily window, same monthly window):
		// DailyRequestsUsed must remain 1 (r3), MonthlyTokensUsed must drop by 1000 (2300 -> 1300).
		if err := store.ReleaseQuota(ctx, r1.ID, "delayed failure", core.LLMTelemetry{}); err != nil {
			t.Fatalf("release r1: %v", err)
		}
		q, err := store.Quota(ctx, userID)
		if err != nil {
			t.Fatalf("get quota: %v", err)
		}
		if q.DailyRequestsUsed != 1 {
			t.Errorf("expected DailyRequestsUsed=1 after delayed release across daily rollover, got %d", q.DailyRequestsUsed)
		}
		if q.MonthlyTokensUsed != 1300 {
			t.Errorf("expected MonthlyTokensUsed=1300 (same monthly window refunded), got %d", q.MonthlyTokensUsed)
		}

		// Delayed CommitUsage of r2 (from old daily window, same monthly window) adjusts MonthlyTokensUsed by (1100 - 800 = +300 -> 1600).
		if err := store.CommitUsage(ctx, r2.ID, core.LLMTelemetry{Usage: core.TokenUsage{TotalTokens: 1100}}); err != nil {
			t.Fatalf("commit r2: %v", err)
		}
		q, _ = store.Quota(ctx, userID)
		if q.MonthlyTokensUsed != 1600 {
			t.Errorf("expected MonthlyTokensUsed=1600 after delayed commit in same monthly window, got %d", q.MonthlyTokensUsed)
		}
	})

	t.Run("monthly rollover prevents delayed CommitUsage and ReleaseQuota from skewing new monthly counters", func(t *testing.T) {
		clk := newFakeClock(t0)
		store := memory.NewWithClock(clk.Now)
		userID := "quota-rollover-monthly"

		r1, err := store.ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 10, 10000)
		if err != nil {
			t.Fatalf("reserve r1: %v", err)
		}
		r2, err := store.ReserveQuota(ctx, userID, core.OperationGapAnalysis, 1500, 10, 10000)
		if err != nil {
			t.Fatalf("reserve r2: %v", err)
		}

		// Advance 31 days: both daily and monthly windows roll over.
		clk.Advance(31 * 24 * time.Hour)
		if _, err := store.ReserveQuota(ctx, userID, core.OperationPDFParse, 400, 10, 10000); err != nil {
			t.Fatalf("reserve r3 in new month: %v", err)
		}

		// Delayed CommitUsage with actual < estimated (100 - 2000 = -1900) must NOT reduce new month's 400 tokens.
		if err := store.CommitUsage(ctx, r1.ID, core.LLMTelemetry{Usage: core.TokenUsage{PromptTokens: 40, CompletionTokens: 60, TotalTokens: 100}}); err != nil {
			t.Fatalf("commit r1: %v", err)
		}
		// Delayed ReleaseQuota must NOT decrement new month's DailyRequestsUsed (1) or MonthlyTokensUsed (400).
		if err := store.ReleaseQuota(ctx, r2.ID, "delayed abort", core.LLMTelemetry{}); err != nil {
			t.Fatalf("release r2: %v", err)
		}

		q, _ := store.Quota(ctx, userID)
		if q.DailyRequestsUsed != 1 {
			t.Errorf("expected DailyRequestsUsed=1 in new window, got %d", q.DailyRequestsUsed)
		}
		if q.MonthlyTokensUsed != 400 {
			t.Errorf("expected MonthlyTokensUsed=400 in new monthly window, got %d", q.MonthlyTokensUsed)
		}
	})

	t.Run("independent daily-valid monthly-expired window refunds daily request without touching new monthly tokens", func(t *testing.T) {
		clk := newFakeClock(t0)
		store := memory.NewWithClock(clk.Now)
		userID := "quota-rollover-split"

		// Initialize quota at t0 so MonthlyWindowStart == t0.
		if _, err := store.Quota(ctx, userID); err != nil {
			t.Fatalf("init quota: %v", err)
		}

		// Advance 29d23h: daily window rolls over to t0+29d23h; monthly window stays at t0!
		tNearMonthEnd := clk.Advance(29*24*time.Hour + 23*time.Hour)
		r1, err := store.ReserveQuota(ctx, userID, core.OperationResumeTailoring, 1000, 10, 10000)
		if err != nil {
			t.Fatalf("reserve r1: %v", err)
		}
		if !r1.DailyWindowStart.Equal(tNearMonthEnd) || !r1.MonthlyWindowStart.Equal(t0) {
			t.Fatalf("unexpected split windows on r1: daily=%v monthly=%v", r1.DailyWindowStart, r1.MonthlyWindowStart)
		}

		// Advance 2 hours (total 30d1h): daily window is STILL tNearMonthEnd (2h < 24h), while monthly window resets!
		clk.Advance(2 * time.Hour)
		if _, err := store.ReserveQuota(ctx, userID, core.OperationPDFParse, 300, 10, 10000); err != nil {
			t.Fatalf("reserve r2: %v", err)
		}

		// Before release: DailyRequestsUsed=2 (r1 + r2 in same daily window), MonthlyTokensUsed=300 (r2 in new monthly window).
		qBefore, _ := store.Quota(ctx, userID)
		if qBefore.DailyRequestsUsed != 2 || qBefore.MonthlyTokensUsed != 300 {
			t.Fatalf("unexpected pre-release quota: %+v", qBefore)
		}

		// Release r1: daily window matches (2 -> 1), monthly window expired (stays 300!).
		if err := store.ReleaseQuota(ctx, r1.ID, "split rollover", core.LLMTelemetry{}); err != nil {
			t.Fatalf("release r1: %v", err)
		}
		qAfter, _ := store.Quota(ctx, userID)
		if qAfter.DailyRequestsUsed != 1 {
			t.Errorf("expected DailyRequestsUsed=1 (same daily window refunded), got %d", qAfter.DailyRequestsUsed)
		}
		if qAfter.MonthlyTokensUsed != 300 {
			t.Errorf("expected MonthlyTokensUsed=300 (new monthly window untouched), got %d", qAfter.MonthlyTokensUsed)
		}
	})
}

// TestMemoryStore_SubmitPendingVersion_BranchAndTenantIsolation verifies SubmitPendingVersion
// rejects nonexistent and foreign-owned branches with ErrNotFound without superseding victim versions.
func TestMemoryStore_SubmitPendingVersion_BranchAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("nonexistent branch returns ErrNotFound", func(t *testing.T) {
		store := setupTestStore(t)

		for _, uid := range []string{"attacker", ""} {
			v := &core.BranchVersion{
				ID:       "v-missing-br-" + uid,
				UserID:   uid,
				BranchID: "br-does-not-exist",
			}
			_, err := store.SubmitPendingVersion(ctx, v, core.PendingJobOptions{Now: base})
			if !errors.Is(err, core.ErrNotFound) {
				t.Fatalf("uid=%q: expected ErrNotFound for nonexistent branch, got %v", uid, err)
			}
			if _, lookupErr := store.Version(ctx, "br-does-not-exist", v.ID); !errors.Is(lookupErr, core.ErrNotFound) {
				t.Fatalf("uid=%q: expected version not to be persisted, got %v", uid, lookupErr)
			}
		}
	})

	t.Run("foreign-owned branch returns ErrNotFound and preserves victim pending version", func(t *testing.T) {
		store := setupTestStore(t)
		victimBr := &core.Branch{
			ID:            "br-victim",
			UserID:        "victim-user",
			Name:          "Victim Branch",
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := store.CreateBranch(ctx, victimBr); err != nil {
			t.Fatalf("create victim branch: %v", err)
		}

		victimPending := &core.BranchVersion{
			ID:            "ver-victim-pending",
			UserID:        "victim-user",
			BranchID:      victimBr.ID,
			ReservationID: "res-victim-1",
		}
		if _, err := store.SubmitPendingVersion(ctx, victimPending, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit victim pending version: %v", err)
		}

		attackerVer := &core.BranchVersion{
			ID:       "ver-attacker",
			UserID:   "attacker-user",
			BranchID: victimBr.ID,
		}
		released, err := store.SubmitPendingVersion(ctx, attackerVer, core.PendingJobOptions{Now: base.Add(time.Minute)})
		if !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("expected ErrNotFound when foreign user submits to victim branch, got %v", err)
		}
		if len(released) != 0 {
			t.Fatalf("expected no released reservations on rejected foreign submit, got %+v", released)
		}

		if _, lookupErr := store.Version(ctx, victimBr.ID, attackerVer.ID); !errors.Is(lookupErr, core.ErrNotFound) {
			t.Fatalf("expected attacker version not to be persisted, got %v", lookupErr)
		}
		stillPending, err := store.Version(ctx, victimBr.ID, victimPending.ID)
		if err != nil {
			t.Fatalf("fetch victim pending version: %v", err)
		}
		if stillPending.Status != core.VersionStatusPending || stillPending.ReservationID != "res-victim-1" {
			t.Fatalf("expected victim pending version untouched, got %+v", stillPending)
		}
	})

	t.Run("empty UserID inherits branch UserID", func(t *testing.T) {
		store := setupTestStore(t)
		br := &core.Branch{
			ID:            "br-inherit-user",
			UserID:        "owner-user",
			Name:          "Owner Branch",
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := store.CreateBranch(ctx, br); err != nil {
			t.Fatalf("create branch: %v", err)
		}

		v := &core.BranchVersion{ID: "ver-inherit", BranchID: br.ID}
		if _, err := store.SubmitPendingVersion(ctx, v, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit version with empty UserID: %v", err)
		}
		got, err := store.Version(ctx, br.ID, v.ID)
		if err != nil {
			t.Fatalf("get version: %v", err)
		}
		if got.UserID != "owner-user" {
			t.Errorf("expected UserID inherited as %q, got %q", "owner-user", got.UserID)
		}
	})
}

// TestMemoryStore_LeaseLossAndStaleCompleteGuards verifies ErrLeaseExpired (unwrapping to ErrConflict)
// across lease-mutating version and PDF job operations, stale CompleteVersion guards, and ClaimedBy clearing.
func TestMemoryStore_LeaseLossAndStaleCompleteGuards(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	assertLeaseExpired := func(t *testing.T, op string, err error) {
		t.Helper()
		if !errors.Is(err, core.ErrLeaseExpired) {
			t.Errorf("%s: expected ErrLeaseExpired, got %v", op, err)
		}
		if !errors.Is(err, core.ErrConflict) {
			t.Errorf("%s: expected ErrConflict (via ErrLeaseExpired), got %v", op, err)
		}
	}

	t.Run("version lease-loss methods return ErrLeaseExpired and ErrConflict", func(t *testing.T) {
		store := setupTestStore(t)
		br := &core.Branch{ID: "br-ll-v", UserID: "u1", Name: "LeaseLoss", TemplateSetID: core.TemplateStandardCleanID}
		if err := store.CreateBranch(ctx, br); err != nil {
			t.Fatalf("create branch: %v", err)
		}
		if _, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{ID: "v-ll-1", UserID: "u1", BranchID: br.ID}, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit version: %v", err)
		}
		claimed, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{WorkerID: "worker-a", Now: base, LeaseDuration: 2 * time.Minute})
		if err != nil || claimed == nil {
			t.Fatalf("claim version: %v", err)
		}

		assertLeaseExpired(t, "RenewVersionLease wrong worker", store.RenewVersionLease(ctx, claimed.ID, "worker-b", base.Add(4*time.Minute)))
		assertLeaseExpired(t, "RequeueVersionForRetry wrong worker", store.RequeueVersionForRetry(ctx, claimed.ID, "worker-b", base.Add(3*time.Minute), "err"))
		assertLeaseExpired(t, "FailClaimedVersion wrong worker", store.FailClaimedVersion(ctx, claimed.ID, "worker-b", "err"))
	})

	t.Run("PDF job lease-loss methods return ErrLeaseExpired and ErrConflict", func(t *testing.T) {
		store := setupTestStore(t)
		job := &core.PDFParseJob{ID: "j-ll-1", UserID: "u1", RawText: "resume text"}
		if err := store.SubmitPendingJob(ctx, job, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit job: %v", err)
		}
		claimed, err := store.ClaimNextJob(ctx, core.ClaimJobRequest{WorkerID: "worker-a", Now: base, LeaseDuration: 2 * time.Minute})
		if err != nil || claimed == nil {
			t.Fatalf("claim job: %v", err)
		}

		assertLeaseExpired(t, "RenewJobLease wrong worker", store.RenewJobLease(ctx, claimed.ID, "worker-b", base.Add(4*time.Minute)))
		assertLeaseExpired(t, "RequeueJobForRetry wrong worker", store.RequeueJobForRetry(ctx, claimed.ID, "worker-b", base.Add(3*time.Minute), "err"))
		assertLeaseExpired(t, "CompleteClaimedJob wrong worker", store.CompleteClaimedJob(ctx, claimed.ID, "worker-b", &core.JSONResume{}))
		assertLeaseExpired(t, "FailClaimedJob wrong worker", store.FailClaimedJob(ctx, claimed.ID, "worker-b", "err"))
	})

	t.Run("stale worker CompleteVersion fails after requeue, after same-number completion, and for missing version", func(t *testing.T) {
		store := setupTestStore(t)
		br := &core.Branch{ID: "br-stale-cv", UserID: "u1", Name: "StaleCV", TemplateSetID: core.TemplateStandardCleanID}
		if err := store.CreateBranch(ctx, br); err != nil {
			t.Fatalf("create branch: %v", err)
		}

		if _, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{ID: "v-stale-1", UserID: "u1", BranchID: br.ID}, core.PendingJobOptions{Now: base, TTL: 10 * time.Minute}); err != nil {
			t.Fatalf("submit version: %v", err)
		}
		claimedA, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{WorkerID: "worker-a", Now: base, LeaseDuration: 2 * time.Minute})
		if err != nil || claimedA == nil {
			t.Fatalf("claim by worker-a: %v", err)
		}

		// Missing version with ClaimedBy set must fail with ErrLeaseExpired.
		missingClaimed := &core.BranchVersion{
			ID:        "v-does-not-exist",
			UserID:    "u1",
			BranchID:  br.ID,
			ClaimedBy: "worker-a",
			Status:    core.VersionStatusCompleted,
		}
		assertLeaseExpired(t, "CompleteVersion missing claimed version", store.CompleteVersion(ctx, missingClaimed))
		if _, err := store.Version(ctx, br.ID, missingClaimed.ID); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("expected missing claimed version not to be persisted, got %v", err)
		}

		// Requeue to Pending: stale worker-a CompleteVersion must fail with ErrLeaseExpired.
		retryAt := base.Add(time.Minute)
		if err := store.RequeueVersionForRetry(ctx, claimedA.ID, "worker-a", retryAt, "transient"); err != nil {
			t.Fatalf("requeue version: %v", err)
		}
		staleAfterRequeue := *claimedA
		staleAfterRequeue.Status = core.VersionStatusCompleted
		staleAfterRequeue.HTMLOutput = "<html>stale-a</html>"
		assertLeaseExpired(t, "CompleteVersion after requeue", store.CompleteVersion(ctx, &staleAfterRequeue))

		pendingCheck, err := store.Version(ctx, br.ID, claimedA.ID)
		if err != nil || pendingCheck.Status != core.VersionStatusPending {
			t.Fatalf("expected version to remain pending after stale CompleteVersion, got %+v err=%v", pendingCheck, err)
		}

		// Reclaim by worker-b and complete at the same VersionNumber (1).
		claimedB, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{WorkerID: "worker-b", Now: retryAt, LeaseDuration: 2 * time.Minute})
		if err != nil || claimedB == nil || claimedB.ClaimedBy != "worker-b" {
			t.Fatalf("reclaim by worker-b: got %+v err=%v", claimedB, err)
		}
		claimedB.Status = core.VersionStatusCompleted
		claimedB.HTMLOutput = "<html>winner-b</html>"
		if err := store.CompleteVersion(ctx, claimedB); err != nil {
			t.Fatalf("CompleteVersion by worker-b: %v", err)
		}

		// ClaimedBy must be cleared on completion in both returned version and stored record.
		if claimedB.ClaimedBy != "" || claimedB.LeaseExpiresAt != nil || claimedB.NextRetryAt != nil {
			t.Errorf("expected lease fields cleared on completed input, got ClaimedBy=%q LeaseExpiresAt=%v NextRetryAt=%v", claimedB.ClaimedBy, claimedB.LeaseExpiresAt, claimedB.NextRetryAt)
		}
		storedB, err := store.Version(ctx, br.ID, claimedB.ID)
		if err != nil {
			t.Fatalf("get completed version: %v", err)
		}
		if storedB.ClaimedBy != "" || storedB.LeaseExpiresAt != nil || storedB.NextRetryAt != nil {
			t.Errorf("expected lease fields cleared in store, got ClaimedBy=%q LeaseExpiresAt=%v NextRetryAt=%v", storedB.ClaimedBy, storedB.LeaseExpiresAt, storedB.NextRetryAt)
		}

		// Stale worker-a attempting CompleteVersion at same VersionNumber after worker-b completion must fail with ErrLeaseExpired.
		staleAfterComplete := *claimedA
		staleAfterComplete.Status = core.VersionStatusCompleted
		staleAfterComplete.HTMLOutput = "<html>stale-overwrite</html>"
		assertLeaseExpired(t, "CompleteVersion after same-number completion", store.CompleteVersion(ctx, &staleAfterComplete))

		storedAfterStale, _ := store.Version(ctx, br.ID, claimedB.ID)
		if storedAfterStale.HTMLOutput != "<html>winner-b</html>" {
			t.Errorf("expected winner-b HTMLOutput preserved, got %q", storedAfterStale.HTMLOutput)
		}
	})

	t.Run("terminal version and empty ClaimedBy on active claim return non-lease ErrConflict", func(t *testing.T) {
		store := setupTestStore(t)
		br := &core.Branch{ID: "br-term-claim", UserID: "u1", Name: "TermClaim", TemplateSetID: core.TemplateStandardCleanID}
		if err := store.CreateBranch(ctx, br); err != nil {
			t.Fatalf("create branch: %v", err)
		}
		if _, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{ID: "v-tc-1", UserID: "u1", BranchID: br.ID}, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit v1: %v", err)
		}
		claimed, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{WorkerID: "worker-a", Now: base})
		if err != nil || claimed == nil {
			t.Fatalf("claim v1: %v", err)
		}

		emptyClaimComplete := *claimed
		emptyClaimComplete.ClaimedBy = ""
		emptyClaimComplete.Status = core.VersionStatusCompleted
		if err := store.CompleteVersion(ctx, &emptyClaimComplete); !errors.Is(err, core.ErrConflict) || errors.Is(err, core.ErrLeaseExpired) {
			t.Errorf("expected non-lease ErrConflict for empty ClaimedBy on claimed version, got %v", err)
		}

		if err := store.FailClaimedVersion(ctx, claimed.ID, "worker-a", "failed"); err != nil {
			t.Fatalf("FailClaimedVersion: %v", err)
		}
		failedClaimComplete := *claimed
		failedClaimComplete.Status = core.VersionStatusCompleted
		if err := store.CompleteVersion(ctx, &failedClaimComplete); !errors.Is(err, core.ErrConflict) || errors.Is(err, core.ErrLeaseExpired) {
			t.Errorf("expected non-lease ErrConflict for terminal failed version even with ClaimedBy set, got %v", err)
		}
	})
}

// TestMemoryStore_UpdateBranchEpochGuards verifies that UpdateBranch changes to ActiveVersionID
// or tailoring configuration block completion of already-claimed processing versions with ErrConflict,
// while non-config updates like branch rename do not block completion.
func TestMemoryStore_UpdateBranchEpochGuards(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("rollback of ActiveVersionID blocks claimed higher-numbered processing version", func(t *testing.T) {
		store := setupTestStore(t)
		br := &core.Branch{
			ID:            "br-epoch-rollback",
			UserID:        "u1",
			Name:          "Backend Role",
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := store.CreateBranch(ctx, br); err != nil {
			t.Fatalf("create branch: %v", err)
		}

		v1 := &core.BranchVersion{ID: "v-rb-1", UserID: "u1", BranchID: br.ID, Status: core.VersionStatusCompleted, HTMLOutput: "<html>v1</html>"}
		if err := store.CreateVersion(ctx, v1); err != nil {
			t.Fatalf("create v1: %v", err)
		}
		if err := store.CompleteVersion(ctx, v1); err != nil {
			t.Fatalf("complete v1: %v", err)
		}

		v2 := &core.BranchVersion{ID: "v-rb-2", UserID: "u1", BranchID: br.ID, Status: core.VersionStatusCompleted, HTMLOutput: "<html>v2</html>"}
		if err := store.CreateVersion(ctx, v2); err != nil {
			t.Fatalf("create v2: %v", err)
		}
		if err := store.CompleteVersion(ctx, v2); err != nil {
			t.Fatalf("complete v2: %v", err)
		}

		v3 := &core.BranchVersion{ID: "v-rb-3", UserID: "u1", BranchID: br.ID}
		if _, err := store.SubmitPendingVersion(ctx, v3, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit v3: %v", err)
		}
		claimedV3, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{WorkerID: "w1", Now: base})
		if err != nil || claimedV3 == nil || claimedV3.VersionNumber != 3 {
			t.Fatalf("claim v3: got %+v err=%v", claimedV3, err)
		}

		// Roll back ActiveVersionID from v2 to v1 while v3 (version 3) is processing.
		currentBr, err := store.Branch(ctx, "u1", br.ID)
		if err != nil {
			t.Fatalf("get branch: %v", err)
		}
		rollbackID := v1.ID
		currentBr.ActiveVersionID = &rollbackID
		if err := store.UpdateBranch(ctx, currentBr); err != nil {
			t.Fatalf("rollback UpdateBranch: %v", err)
		}

		claimedV3.Status = core.VersionStatusCompleted
		claimedV3.HTMLOutput = "<html>v3</html>"
		err = store.CompleteVersion(ctx, claimedV3)
		if !errors.Is(err, core.ErrConflict) {
			t.Fatalf("expected ErrConflict completing processing v3 after ActiveVersionID rollback, got %v", err)
		}
		if errors.Is(err, core.ErrLeaseExpired) {
			t.Errorf("expected non-lease ErrConflict after branch rollback, got %v", err)
		}

		gotBr, _ := store.Branch(ctx, "u1", br.ID)
		if gotBr.ActiveVersionID == nil || *gotBr.ActiveVersionID != v1.ID {
			t.Errorf("expected ActiveVersionID to remain rolled back to %s, got %v", v1.ID, gotBr.ActiveVersionID)
		}
	})

	t.Run("TargetRequirements change blocks processing version while rename and nil-vs-empty slices do not", func(t *testing.T) {
		store := setupTestStore(t)
		br := &core.Branch{
			ID:                 "br-epoch-cfg",
			UserID:             "u1",
			Name:               "Original Name",
			TemplateSetID:      core.TemplateStandardCleanID,
			TargetRequirements: "Go and PostgreSQL",
			PinnedEntityIDs:    nil,
		}
		if err := store.CreateBranch(ctx, br); err != nil {
			t.Fatalf("create branch: %v", err)
		}

		// Submit & claim v1.
		if _, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{ID: "v-cfg-1", UserID: "u1", BranchID: br.ID}, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit v1: %v", err)
		}
		claimed1, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{WorkerID: "w1", Now: base})
		if err != nil || claimed1 == nil {
			t.Fatalf("claim v1: %v", err)
		}

		// Non-config update: rename + nil vs empty PinnedEntityIDs slice.
		renameBr, err := store.Branch(ctx, "u1", br.ID)
		if err != nil {
			t.Fatalf("get branch: %v", err)
		}
		renameBr.Name = "Renamed Branch"
		renameBr.PinnedEntityIDs = []string{}
		if err := store.UpdateBranch(ctx, renameBr); err != nil {
			t.Fatalf("rename UpdateBranch: %v", err)
		}

		claimed1.Status = core.VersionStatusCompleted
		claimed1.HTMLOutput = "<html>v1-ok</html>"
		if err := store.CompleteVersion(ctx, claimed1); err != nil {
			t.Fatalf("expected CompleteVersion to succeed after non-config rename, got %v", err)
		}

		// Submit & claim v2, then change TargetRequirements while v2 is processing.
		if _, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{ID: "v-cfg-2", UserID: "u1", BranchID: br.ID}, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit v2: %v", err)
		}
		claimed2, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{WorkerID: "w1", Now: base})
		if err != nil || claimed2 == nil {
			t.Fatalf("claim v2: %v", err)
		}

		cfgBr, err := store.Branch(ctx, "u1", br.ID)
		if err != nil {
			t.Fatalf("get branch: %v", err)
		}
		cfgBr.TargetRequirements = "Rust and Kubernetes"
		if err := store.UpdateBranch(ctx, cfgBr); err != nil {
			t.Fatalf("config UpdateBranch: %v", err)
		}

		claimed2.Status = core.VersionStatusCompleted
		claimed2.HTMLOutput = "<html>v2-stale-cfg</html>"
		if err := store.CompleteVersion(ctx, claimed2); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("expected ErrConflict completing v2 after TargetRequirements change, got %v", err)
		}
		if err := store.FailClaimedVersion(ctx, claimed2.ID, "w1", "aborted after branch config change"); err != nil {
			t.Fatalf("FailClaimedVersion v2: %v", err)
		}

		// Config change before ClaimNextVersion picks up the new epoch upon claiming.
		if _, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{ID: "v-cfg-3", UserID: "u1", BranchID: br.ID}, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit v3: %v", err)
		}
		cfgBr2, _ := store.Branch(ctx, "u1", br.ID)
		cfgBr2.TargetRequirements = "Rust, Kubernetes, and Kafka"
		if err := store.UpdateBranch(ctx, cfgBr2); err != nil {
			t.Fatalf("pre-claim config UpdateBranch: %v", err)
		}
		claimed3, err := store.ClaimNextVersion(ctx, core.ClaimJobRequest{WorkerID: "w1", Now: base})
		if err != nil || claimed3 == nil || claimed3.ID != "v-cfg-3" {
			t.Fatalf("claim v3: got %+v err=%v", claimed3, err)
		}
		claimed3.Status = core.VersionStatusCompleted
		claimed3.HTMLOutput = "<html>v3-fresh-cfg</html>"
		if err := store.CompleteVersion(ctx, claimed3); err != nil {
			t.Fatalf("expected v3 claimed after UpdateBranch to complete cleanly, got %v", err)
		}
	})

	t.Run("all generation-defining Branch fields trigger epoch conflict for processing versions", func(t *testing.T) {
		strPtr := func(s string) *string { return &s }
		mutations := []struct {
			name   string
			mutate func(*core.Branch)
		}{
			{name: "TargetRole", mutate: func(b *core.Branch) { b.TargetRole = "Principal" }},
			{name: "TargetCompany", mutate: func(b *core.Branch) { b.TargetCompany = "NewCo" }},
			{name: "CustomDirectives", mutate: func(b *core.Branch) { b.CustomDirectives = "Be concise" }},
			{name: "BranchInstructions", mutate: func(b *core.Branch) { b.BranchInstructions = "Use metrics" }},
			{name: "TailoringStrategyID", mutate: func(b *core.Branch) { b.TailoringStrategyID = strPtr("strategy-2") }},
			{name: "PresetID", mutate: func(b *core.Branch) { b.PresetID = strPtr("preset-2") }},
			{name: "TemplateSetID", mutate: func(b *core.Branch) { b.TemplateSetID = "tmpl-2" }},
			{name: "ProviderOverride", mutate: func(b *core.Branch) { b.ProviderOverride = "anthropic" }},
			{name: "ModelOverride", mutate: func(b *core.Branch) { b.ModelOverride = "claude-4" }},
			{name: "ReasoningLevel", mutate: func(b *core.Branch) { b.ReasoningLevel = "high" }},
			{name: "PinnedEntityIDs", mutate: func(b *core.Branch) { b.PinnedEntityIDs = []string{"exp-1"} }},
			{name: "ExcludedEntityIDs", mutate: func(b *core.Branch) { b.ExcludedEntityIDs = []string{"exp-2"} }},
		}

		for _, tc := range mutations {
			t.Run(tc.name, func(t *testing.T) {
				store := setupTestStore(t)
				br := &core.Branch{
					ID:                  "br-mut-" + tc.name,
					UserID:              "u1",
					Name:                "Branch",
					TemplateSetID:       core.TemplateStandardCleanID,
					TailoringStrategyID: strPtr("strategy-1"),
					PresetID:            strPtr("preset-1"),
				}
				if err := store.CreateBranch(ctx, br); err != nil {
					t.Fatalf("create branch: %v", err)
				}
				v := &core.BranchVersion{
					ID:       "v-mut-" + tc.name,
					UserID:   "u1",
					BranchID: br.ID,
					Status:   core.VersionStatusProcessing,
				}
				if err := store.CreateVersion(ctx, v); err != nil {
					t.Fatalf("create processing version: %v", err)
				}
				updated, _ := store.Branch(ctx, "u1", br.ID)
				tc.mutate(updated)
				if err := store.UpdateBranch(ctx, updated); err != nil {
					t.Fatalf("UpdateBranch: %v", err)
				}
				v.Status = core.VersionStatusCompleted
				if err := store.CompleteVersion(ctx, v); !errors.Is(err, core.ErrConflict) {
					t.Fatalf("%s: expected ErrConflict completing processing version, got %v", tc.name, err)
				}
			})
		}
	})

	t.Run("DeleteBranch cleans up epoch tracking for deleted branch and versions", func(t *testing.T) {
		store := setupTestStore(t)
		br := &core.Branch{ID: "br-del-epoch", UserID: "u1", Name: "Temp", TemplateSetID: core.TemplateStandardCleanID}
		if err := store.CreateBranch(ctx, br); err != nil {
			t.Fatalf("create branch: %v", err)
		}
		br.TargetRole = "Staff Engineer"
		if err := store.UpdateBranch(ctx, br); err != nil {
			t.Fatalf("update branch: %v", err)
		}
		if _, err := store.SubmitPendingVersion(ctx, &core.BranchVersion{ID: "v-del-1", UserID: "u1", BranchID: br.ID}, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("submit v1: %v", err)
		}
		if err := store.DeleteBranch(ctx, "u1", br.ID); err != nil {
			t.Fatalf("delete branch: %v", err)
		}

		// Recreating branch and version with the same IDs must start clean without false epoch conflict.
		recreated := &core.Branch{ID: "br-del-epoch", UserID: "u1", Name: "Recreated", TemplateSetID: core.TemplateStandardCleanID}
		if err := store.CreateBranch(ctx, recreated); err != nil {
			t.Fatalf("recreate branch: %v", err)
		}
		vRecreated := &core.BranchVersion{ID: "v-del-1", UserID: "u1", BranchID: recreated.ID, Status: core.VersionStatusProcessing}
		if err := store.CreateVersion(ctx, vRecreated); err != nil {
			t.Fatalf("recreate version: %v", err)
		}
		vRecreated.Status = core.VersionStatusCompleted
		if err := store.CompleteVersion(ctx, vRecreated); err != nil {
			t.Fatalf("complete recreated version: %v", err)
		}
	})
}

// TestMemoryStore_BranchClockDeterminism verifies CreateBranch, UpdateBranch, and ATS score
// mirroring use the injected clock from NewWithClock rather than wall-clock time.
func TestMemoryStore_BranchClockDeterminism(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	clk := newFakeClock(t0)
	store := memory.NewWithClock(clk.Now)

	br := &core.Branch{
		ID:            "br-clock-det",
		UserID:        "u-clock",
		Name:          "Clock Branch",
		TemplateSetID: core.TemplateStandardCleanID,
	}
	if err := store.CreateBranch(ctx, br); err != nil {
		t.Fatalf("create branch: %v", err)
	}
	if !br.CreatedAt.Equal(t0) || !br.UpdatedAt.Equal(t0) {
		t.Fatalf("expected CreatedAt and UpdatedAt equal to %v, got created=%v updated=%v", t0, br.CreatedAt, br.UpdatedAt)
	}

	t1 := clk.Advance(5 * time.Minute)
	br.Name = "Renamed Clock Branch"
	if err := store.UpdateBranch(ctx, br); err != nil {
		t.Fatalf("update branch: %v", err)
	}
	fetched, err := store.Branch(ctx, "u-clock", br.ID)
	if err != nil {
		t.Fatalf("get branch: %v", err)
	}
	if !fetched.CreatedAt.Equal(t0) || !fetched.UpdatedAt.Equal(t1) {
		t.Fatalf("expected CreatedAt=%v UpdatedAt=%v, got created=%v updated=%v", t0, t1, fetched.CreatedAt, fetched.UpdatedAt)
	}

	v := &core.BranchVersion{ID: "v-clock-1", UserID: "u-clock", BranchID: br.ID, Status: core.VersionStatusCompleted}
	if err := store.CreateVersion(ctx, v); err != nil {
		t.Fatalf("create version: %v", err)
	}
	if err := store.CompleteVersion(ctx, v); err != nil {
		t.Fatalf("complete version: %v", err)
	}

	t2 := clk.Advance(3 * time.Minute)
	if err := store.UpdateVersionATSScore(ctx, v.ID, &core.ATSScore{ExtractionConfidenceScore: 95, JobMatchingScore: 88}); err != nil {
		t.Fatalf("UpdateVersionATSScore: %v", err)
	}
	fetchedAfterATS, err := store.Branch(ctx, "u-clock", br.ID)
	if err != nil {
		t.Fatalf("get branch after ATS: %v", err)
	}
	if !fetchedAfterATS.UpdatedAt.Equal(t2) {
		t.Errorf("expected mirrored ATS score UpdatedAt=%v, got %v", t2, fetchedAfterATS.UpdatedAt)
	}
}

// TestMemoryStore_SubmitPendingJob_MaxRawTextBytes verifies SubmitPendingJob enforces default
// and custom MaxRawTextBytes caps with ErrValidation without persisting oversized jobs.
func TestMemoryStore_SubmitPendingJob_MaxRawTextBytes(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("default 1 MiB limit accepts exact boundary and rejects +1 byte", func(t *testing.T) {
		store := setupTestStore(t)

		okJob := &core.PDFParseJob{
			ID:      "job-default-ok",
			UserID:  "u1",
			RawText: strings.Repeat("a", core.DefaultMaxPDFRawTextBytes),
		}
		if err := store.SubmitPendingJob(ctx, okJob, core.PendingJobOptions{Now: base}); err != nil {
			t.Fatalf("expected exact DefaultMaxPDFRawTextBytes job to succeed, got %v", err)
		}

		oversizedJob := &core.PDFParseJob{
			ID:      "job-default-too-big",
			UserID:  "u1",
			RawText: strings.Repeat("a", core.DefaultMaxPDFRawTextBytes+1),
		}
		err := store.SubmitPendingJob(ctx, oversizedJob, core.PendingJobOptions{Now: base})
		if !errors.Is(err, core.ErrValidation) {
			t.Fatalf("expected ErrValidation for >1 MiB RawText, got %v", err)
		}
		if _, lookupErr := store.Job(ctx, "u1", oversizedJob.ID); !errors.Is(lookupErr, core.ErrNotFound) {
			t.Fatalf("expected oversized job not to be persisted, got %v", lookupErr)
		}
	})

	t.Run("custom MaxRawTextBytes accepts exact boundary and rejects +1 byte", func(t *testing.T) {
		store := setupTestStore(t)
		opts := core.PendingJobOptions{Now: base, MaxRawTextBytes: 64}

		okJob := &core.PDFParseJob{
			ID:      "job-custom-ok",
			UserID:  "u1",
			RawText: strings.Repeat("b", 64),
		}
		if err := store.SubmitPendingJob(ctx, okJob, opts); err != nil {
			t.Fatalf("expected exact custom limit job to succeed, got %v", err)
		}

		oversizedJob := &core.PDFParseJob{
			ID:      "job-custom-too-big",
			UserID:  "u1",
			RawText: strings.Repeat("b", 65),
		}
		err := store.SubmitPendingJob(ctx, oversizedJob, opts)
		if !errors.Is(err, core.ErrValidation) {
			t.Fatalf("expected ErrValidation for >64 byte RawText, got %v", err)
		}
		if _, lookupErr := store.Job(ctx, "u1", oversizedJob.ID); !errors.Is(lookupErr, core.ErrNotFound) {
			t.Fatalf("expected oversized custom job not to be persisted, got %v", lookupErr)
		}
	})
}
