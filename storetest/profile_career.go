package storetest

import (
	"errors"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// RunProfilesAndCareer verifies profile revisions, skill resolution, career CRUD, short IDs,
// fact ownership, snapshots, and atomic ReplaceCareerGraph transitions.
func RunProfilesAndCareer(t *testing.T, newStore Factory) {
	t.Run("ProfileRevisionAndCareerCRUD", func(t *testing.T) {
		st := openStore(t, newStore, newTestClock(defaultTestBaseTime).Now)
		ctx := runCtx(t)

		p := &core.Profile{UserID: "u1", FullName: "Alice", Headline: "Staff Engineer"}
		if err := st.Profiles().UpsertProfile(ctx, p); err != nil {
			t.Fatalf("UpsertProfile failed: %v", err)
		}
		if p.MainRevision != 1 {
			t.Fatalf("expected initial MainRevision 1, got %d", p.MainRevision)
		}
		rev, err := st.Profiles().IncrementMainRevision(ctx, "u1")
		if err != nil || rev != 2 {
			t.Fatalf("expected rev 2, got %d (err=%v)", rev, err)
		}

		// Global skills cannot be modified or deleted by tenants.
		globalGo := &core.Skill{ID: "skill-global-go", UserID: core.SystemUserID, Name: "Go", IsGlobal: true, Category: "Languages"}
		if err := st.Career().CreateSkill(ctx, globalGo); err != nil {
			t.Fatalf("CreateSkill global failed: %v", err)
		}
		if err := st.Career().UpdateSkill(ctx, &core.Skill{ID: globalGo.ID, UserID: "u1", Name: "Golang"}); !errors.Is(err, core.ErrGlobalSkillImmutable) {
			t.Fatalf("expected ErrGlobalSkillImmutable updating global skill, got %v", err)
		}
		if err := st.Career().DeleteSkill(ctx, "u1", globalGo.ID); !errors.Is(err, core.ErrGlobalSkillImmutable) {
			t.Fatalf("expected ErrGlobalSkillImmutable deleting global skill, got %v", err)
		}

		// Tenant skill creation & isolation.
		customRust := &core.Skill{UserID: "u1", Name: "Rust", Category: "Languages"}
		if err := st.Career().CreateSkill(ctx, customRust); err != nil {
			t.Fatalf("CreateSkill tenant failed: %v", err)
		}
		if err := st.Career().DeleteSkill(ctx, "u2", customRust.ID); !errors.Is(err, core.ErrForbidden) {
			t.Fatalf("expected ErrForbidden deleting foreign skill, got %v", err)
		}

		// Creating experience resolves global "Go" and auto-registers new skill "Distributed Systems" + inline facts.
		exp := &core.WorkExperience{
			UserID:    "u1",
			Company:   "Acme",
			RoleTitle: "Principal Engineer",
			StartDate: core.NewDate(2022, time.January, 1),
			Skills:    []core.Skill{{Name: "Go"}, {Name: "Distributed Systems"}},
			Facts: []core.Fact{
				{Statement: "Reduced p99 latency by 45%"},
			},
		}
		if err := st.Career().CreateExperience(ctx, exp); err != nil {
			t.Fatalf("CreateExperience failed: %v", err)
		}
		if len(exp.Skills) != 2 || exp.Skills[0].ID != globalGo.ID {
			t.Fatalf("expected Go resolved to global skill %s, got %+v", globalGo.ID, exp.Skills)
		}
		if exp.ShortID == "" || len(exp.Facts) != 1 || exp.Facts[0].OwnerID != exp.ID {
			t.Fatalf("expected generated ShortID and bound child fact, got %+v", exp)
		}

		// ShortID lookup and foreign isolation.
		byShort, err := st.Career().Experience(ctx, "u1", exp.ShortID)
		if err != nil || byShort.ID != exp.ID || len(byShort.Facts) != 1 {
			t.Fatalf("Experience by ShortID failed: got %+v, err=%v", byShort, err)
		}
		if _, err := st.Career().Experience(ctx, "u2", exp.ID); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("expected ErrNotFound for foreign experience, got %v", err)
		}

		// Updating experience with Facts == nil preserves existing child facts.
		expUpdate := &core.WorkExperience{
			ID:        exp.ID,
			UserID:    "u1",
			Company:   "Acme Corp",
			RoleTitle: "Principal Engineer",
			StartDate: exp.StartDate,
			Facts:     nil,
		}
		if err := st.Career().UpdateExperience(ctx, expUpdate); err != nil {
			t.Fatalf("UpdateExperience failed: %v", err)
		}
		reloaded, _ := st.Career().Experience(ctx, "u1", exp.ID)
		if len(reloaded.Facts) != 1 || reloaded.Company != "Acme Corp" {
			t.Fatalf("expected facts preserved after UpdateExperience with nil Facts, got %+v", reloaded)
		}

		// Direct project, education, certification, and fact ownership checks.
		proj := &core.Project{UserID: "u1", Name: "Chronos", Skills: []core.Skill{{Name: "Go"}}}
		if err := st.Career().CreateProject(ctx, proj); err != nil {
			t.Fatalf("CreateProject failed: %v", err)
		}
		edu := &core.Education{UserID: "u1", Institution: "MIT", Degree: "BS", Field: "CS"}
		if err := st.Career().CreateEducation(ctx, edu); err != nil {
			t.Fatalf("CreateEducation failed: %v", err)
		}
		cert := &core.Certification{UserID: "u1", Name: "CKA", Issuer: "CNCF"}
		if err := st.Career().CreateCertification(ctx, cert); err != nil {
			t.Fatalf("CreateCertification failed: %v", err)
		}
		badFact := &core.Fact{UserID: "u2", OwnerType: core.OwnerTypeProject, OwnerID: proj.ID, Statement: "Steal"}
		if err := st.Career().CreateFact(ctx, badFact); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("expected ErrNotFound creating fact on foreign owner, got %v", err)
		}

		snap, err := st.Profiles().Snapshot(ctx, "u1")
		if err != nil {
			t.Fatalf("Snapshot failed: %v", err)
		}
		if len(snap.Experiences) != 1 || len(snap.Projects) != 1 || len(snap.Education) != 1 || len(snap.Certifications) != 1 || len(snap.Facts) != 1 {
			t.Fatalf("unexpected snapshot graph counts: exp=%d proj=%d edu=%d cert=%d facts=%d",
				len(snap.Experiences), len(snap.Projects), len(snap.Education), len(snap.Certifications), len(snap.Facts))
		}
	})

	t.Run("ReplaceCareerGraphAtomicityAndRollback", func(t *testing.T) {
		st := openStore(t, newStore, newTestClock(defaultTestBaseTime).Now)
		ctx := runCtx(t)

		if err := st.Profiles().UpsertProfile(ctx, &core.Profile{UserID: "u1", FullName: "Original"}); err != nil {
			t.Fatalf("UpsertProfile failed: %v", err)
		}
		exp := &core.WorkExperience{UserID: "u1", Company: "OldCo", RoleTitle: "SWE", StartDate: core.NewDate(2020, time.January, 1)}
		if err := st.Career().CreateExperience(ctx, exp); err != nil {
			t.Fatalf("CreateExperience failed: %v", err)
		}

		// Invalid ReplaceCareerGraph (missing profile / nil) must not destroy existing career graph.
		if err := st.Profiles().ReplaceCareerGraph(ctx, "u1", nil); !errors.Is(err, core.ErrValidation) {
			t.Fatalf("expected ErrValidation for nil snapshot, got %v", err)
		}
		// Fact pointing to missing owner aborts transaction cleanly without deleting OldCo.
		invalidSnap := &core.MainProfileSnapshot{
			Profile: core.Profile{UserID: "u1", FullName: "Broken"},
			Facts: []core.Fact{
				{ID: "f-bad", OwnerType: core.OwnerTypeWorkExperience, OwnerID: "nonexistent", Statement: "Invalid"},
			},
		}
		if err := st.Profiles().ReplaceCareerGraph(ctx, "u1", invalidSnap); !errors.Is(err, core.ErrValidation) {
			t.Fatalf("expected ErrValidation for dangling fact owner, got %v", err)
		}
		afterFail, err := st.Career().ListExperiences(ctx, "u1")
		if err != nil || len(afterFail) != 1 || afterFail[0].Company != "OldCo" {
			t.Fatalf("expected OldCo preserved after rolled-back ReplaceCareerGraph, got %+v (err=%v)", afterFail, err)
		}

		// Valid full replacement swaps graph and increments MainRevision.
		validSnap := &core.MainProfileSnapshot{
			Profile: core.Profile{UserID: "u1", FullName: "Updated"},
			Skills:  []core.Skill{{Name: "Kubernetes"}},
			Experiences: []core.WorkExperience{
				{
					ID:        "exp-new",
					Company:   "NewCo",
					RoleTitle: "Staff",
					StartDate: core.NewDate(2024, time.January, 1),
					Skills:    []core.Skill{{Name: "Kubernetes"}, {Name: "Go"}},
					Facts:     []core.Fact{{Statement: "Built control plane"}},
				},
			},
		}
		if err := st.Profiles().ReplaceCareerGraph(ctx, "u1", validSnap); err != nil {
			t.Fatalf("ReplaceCareerGraph valid failed: %v", err)
		}
		snap, err := st.Profiles().Snapshot(ctx, "u1")
		if err != nil {
			t.Fatalf("Snapshot failed: %v", err)
		}
		if snap.Profile.FullName != "Updated" || snap.MainRevision <= 2 {
			t.Fatalf("expected Updated profile and incremented revision, got name=%q rev=%d", snap.Profile.FullName, snap.MainRevision)
		}
		if len(snap.Experiences) != 1 || snap.Experiences[0].Company != "NewCo" || len(snap.Facts) != 1 {
			t.Fatalf("expected replaced graph with NewCo and 1 fact, got %+v", snap)
		}
	})
}
