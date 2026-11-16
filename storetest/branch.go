package storetest

import (
	"errors"
	"testing"

	"github.com/resumeee-app/core"
)

// RunBranches verifies branch CRUD, default immutability, epoch tracking, version promotion, and reports.
func RunBranches(t *testing.T, newStore Factory) {
	t.Run("DefaultProtectionAndEpochBumps", func(t *testing.T) {
		st := openStore(t, newStore, newTestClock(defaultTestBaseTime).Now)
		ctx := runCtx(t)

		defBranch := &core.Branch{
			ID:            core.DefaultBranchID("u1"),
			UserID:        "u1",
			Name:          core.DefaultBranchName,
			IsDefault:     true,
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := st.Branches().CreateBranch(ctx, defBranch); err != nil {
			t.Fatalf("CreateBranch default failed: %v", err)
		}
		if defBranch.Epoch != 1 {
			t.Fatalf("expected initial epoch 1, got %d", defBranch.Epoch)
		}

		if err := st.Branches().DeleteBranch(ctx, "u1", defBranch.ID); !errors.Is(err, core.ErrDefaultBranchImmutable) {
			t.Fatalf("expected ErrDefaultBranchImmutable deleting default branch, got %v", err)
		}
		badUpdate := *defBranch
		badUpdate.TargetRole = "Staff Engineer"
		if err := st.Branches().UpdateBranch(ctx, &badUpdate); !errors.Is(err, core.ErrDefaultBranchImmutable) {
			t.Fatalf("expected ErrDefaultBranchImmutable altering default branch role, got %v", err)
		}

		// Non-default branch creation, cosmetic update vs generation-defining update.
		br := &core.Branch{
			ID:            "br-stripe",
			UserID:        "u1",
			Name:          "Stripe Backend",
			TargetRole:    "Backend Engineer",
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := st.Branches().CreateBranch(ctx, br); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}
		if err := st.Branches().CreateBranch(ctx, &core.Branch{ID: "br-stripe", UserID: "u1", Name: "Dup"}); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("expected ErrConflict on duplicate branch ID, got %v", err)
		}

		// Cosmetic name update does NOT advance Epoch.
		br.Name = "Stripe Staff"
		if err := st.Branches().UpdateBranch(ctx, br); err != nil {
			t.Fatalf("cosmetic UpdateBranch failed: %v", err)
		}
		if br.Epoch != 1 {
			t.Fatalf("expected cosmetic update to keep epoch 1, got %d", br.Epoch)
		}

		// Tailoring directive update DOES advance Epoch.
		br.TargetRole = "Staff Engineer"
		if err := st.Branches().UpdateBranch(ctx, br); err != nil {
			t.Fatalf("generation UpdateBranch failed: %v", err)
		}
		if br.Epoch != 2 {
			t.Fatalf("expected generation change to bump epoch to 2, got %d", br.Epoch)
		}
	})

	t.Run("SaveCompletedVersionAndReports", func(t *testing.T) {
		st := openStore(t, newStore, newTestClock(defaultTestBaseTime).Now)
		ctx := runCtx(t)

		br := &core.Branch{
			ID:            "br-1",
			UserID:        "u1",
			Name:          "Role A",
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := st.Branches().CreateBranch(ctx, br); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}

		v1 := &core.BranchVersion{
			ID:               "ver-1",
			BranchID:         br.ID,
			UserID:           "u1",
			MainRevisionUsed: 3,
			ProviderUsed:     "gemini",
			ModelUsed:        "flash",
			HTMLOutput:       "<p>v1</p>",
		}
		if err := st.Branches().SaveCompletedVersion(ctx, v1); err != nil {
			t.Fatalf("SaveCompletedVersion v1 failed: %v", err)
		}
		if v1.VersionNumber != 1 || v1.Status != core.VersionStatusCompleted {
			t.Fatalf("unexpected v1 metadata: %+v", v1)
		}

		v2 := &core.BranchVersion{
			ID:               "ver-2",
			BranchID:         br.ID,
			UserID:           "u1",
			MainRevisionUsed: 4,
			ProviderUsed:     "gemini",
			ModelUsed:        "pro",
			HTMLOutput:       "<p>v2</p>",
		}
		if err := st.Branches().SaveCompletedVersion(ctx, v2); err != nil {
			t.Fatalf("SaveCompletedVersion v2 failed: %v", err)
		}
		if v2.VersionNumber != 2 {
			t.Fatalf("expected auto VersionNumber 2, got %d", v2.VersionNumber)
		}

		// GapReport on inactive v1 does NOT overwrite active v2's GapReport on branch.
		score55 := 55.0
		if err := st.Branches().SetVersionGapReport(ctx, v1.ID, &core.GapReport{MatchScore: &score55}); err != nil {
			t.Fatalf("SetVersionGapReport v1 failed: %v", err)
		}
		brNow, _ := st.Branches().Branch(ctx, "u1", br.ID)
		if brNow.ActiveVersionID == nil || *brNow.ActiveVersionID != v2.ID || brNow.GapReport != nil {
			t.Fatalf("expected active v2 untouched when setting report on inactive v1, got %+v", brNow)
		}

		// GapReport and ATSScore on active v2 mirror onto branch; ATSScore cannot be overwritten once set.
		score91 := 91.0
		if err := st.Branches().SetVersionGapReport(ctx, v2.ID, &core.GapReport{MatchScore: &score91}); err != nil {
			t.Fatalf("SetVersionGapReport v2 failed: %v", err)
		}
		if err := st.Branches().SetVersionATSScore(ctx, v2.ID, &core.ATSScore{ExtractionConfidenceScore: 88, JobMatchingScore: 86}); err != nil {
			t.Fatalf("SetVersionATSScore v2 failed: %v", err)
		}
		if err := st.Branches().SetVersionATSScore(ctx, v2.ID, &core.ATSScore{ExtractionConfidenceScore: 99}); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("expected ErrConflict overwriting existing ATSScore, got %v", err)
		}

		brNow, _ = st.Branches().Branch(ctx, "u1", br.ID)
		if brNow.GapReport == nil || brNow.GapReport.MatchScore == nil || *brNow.GapReport.MatchScore != 91 || brNow.ATSScore == nil || brNow.ATSScore.ExtractionConfidenceScore != 88 {
			t.Fatalf("expected mirrored reports on active branch, got gap=%+v ats=%+v", brNow.GapReport, brNow.ATSScore)
		}

		byNum, err := st.Branches().VersionByNumber(ctx, br.ID, 1)
		if err != nil || byNum.ID != v1.ID {
			t.Fatalf("VersionByNumber(1) failed: got %+v, err=%v", byNum, err)
		}
		batch, err := st.Branches().VersionsByIDs(ctx, []string{v2.ID, v1.ID})
		if err != nil || len(batch) != 2 || batch[0].ID != v2.ID {
			t.Fatalf("VersionsByIDs failed: got %+v, err=%v", batch, err)
		}

		if err := st.Branches().DeleteBranch(ctx, "u1", br.ID); err != nil {
			t.Fatalf("DeleteBranch failed: %v", err)
		}
		if _, err := st.Branches().Version(ctx, br.ID, v1.ID); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("expected cascade deletion of branch versions, got %v", err)
		}
	})
}
