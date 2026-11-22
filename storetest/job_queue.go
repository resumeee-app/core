package storetest

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/resumeee-app/core"
)

// RunJobQueue verifies unified queueing across branch_tailor and pdf_parse workloads,
// including admission caps, fairness, lease ownership, epoch fencing, retries, sweeps, and cleanup.
func RunJobQueue(t *testing.T, newStore Factory) {
	t.Run("TailorLifecycleAndEpochFencing", func(t *testing.T) {
		clk := newTestClock(defaultTestBaseTime)
		st := openStore(t, newStore, clk.Now)
		ctx := runCtx(t)

		br := &core.Branch{ID: "br-1", UserID: "u1", Name: "Role", TemplateSetID: core.TemplateStandardCleanID}
		if err := st.Branches().CreateBranch(ctx, br); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}

		job1 := &core.Job{
			ID:            "job-1",
			UserID:        "u1",
			ReservationID: "res-1",
			Tailor:        &core.BranchTailorSpec{BranchID: br.ID},
		}
		ver1 := &core.BranchVersion{ID: "ver-1", BranchID: br.ID, UserID: "u1"}
		if err := st.Jobs().SubmitBranchTailor(ctx, job1, ver1, core.PendingJobOptions{}); err != nil {
			t.Fatalf("SubmitBranchTailor 1 failed: %v", err)
		}
		if ver1.JobID != job1.ID || ver1.BranchEpoch != 1 {
			t.Fatalf("expected linked JobID and epoch 1 on version, got %+v", ver1)
		}

		// Submitting a second pending job on the same branch supersedes job1.
		job2 := &core.Job{
			ID:            "job-2",
			UserID:        "u1",
			ReservationID: "res-2",
			Tailor:        &core.BranchTailorSpec{BranchID: br.ID},
		}
		ver2 := &core.BranchVersion{ID: "ver-2", BranchID: br.ID, UserID: "u1"}
		if err := st.Jobs().SubmitBranchTailor(ctx, job2, ver2, core.PendingJobOptions{}); err != nil {
			t.Fatalf("SubmitBranchTailor supersede failed: %v", err)
		}
		j1After, _ := st.Jobs().Job(ctx, "u1", job1.ID)
		v1After, _ := st.Branches().Version(ctx, br.ID, ver1.ID)
		if j1After.Status != core.JobStatusCancelled || v1After.Status != core.VersionStatusCancelled {
			t.Fatalf("expected superseded job/version cancelled, got job=%s ver=%s", j1After.Status, v1After.Status)
		}

		// Claim job2.
		claimed, err := st.Jobs().ClaimNext(ctx, core.ClaimJobRequest{
			WorkerID:      "worker-a",
			LeaseDuration: 5 * time.Minute,
		})
		if err != nil || claimed == nil || claimed.ID != job2.ID {
			t.Fatalf("expected claim of %s, got %+v (err=%v)", job2.ID, claimed, err)
		}

		// Another tailor submission while active lease is held must be rejected with ErrConflict.
		if err := st.Jobs().SubmitBranchTailor(ctx, &core.Job{ID: "job-3", UserID: "u1"}, &core.BranchVersion{ID: "ver-3", BranchID: br.ID}, core.PendingJobOptions{}); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("expected ErrConflict submitting while processing lease is active, got %v", err)
		}

		// Wrong worker cannot renew or complete.
		if err := st.Jobs().RenewLease(ctx, job2.ID, "worker-b", clk.Now().Add(time.Minute)); !errors.Is(err, core.ErrLeaseExpired) {
			t.Fatalf("expected ErrLeaseExpired for wrong worker, got %v", err)
		}
		if err := st.Jobs().CompleteBranchTailor(ctx, job2.ID, "worker-b", &core.BranchVersion{HTMLOutput: "bad"}); !errors.Is(err, core.ErrLeaseExpired) {
			t.Fatalf("expected ErrLeaseExpired on foreign complete, got %v", err)
		}

		// Branch epoch change while processing fences CompleteBranchTailor with ErrConflict.
		br.TargetRole = "Changed Mid-Flight"
		if err := st.Branches().UpdateBranch(ctx, br); err != nil {
			t.Fatalf("UpdateBranch failed: %v", err)
		}
		if err := st.Jobs().CompleteBranchTailor(ctx, job2.ID, "worker-a", &core.BranchVersion{HTMLOutput: "stale"}); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("expected ErrConflict due to branch epoch fence, got %v", err)
		}
		if err := st.Jobs().FailClaimed(ctx, core.FailClaimedRequest{
			JobID:     job2.ID,
			ClaimedBy: "worker-a",
			ErrMsg:    "fenced",
		}); err != nil {
			t.Fatalf("FailClaimed failed: %v", err)
		}

		// Fresh submission after epoch bump succeeds cleanly end-to-end.
		job4 := &core.Job{ID: "job-4", UserID: "u1", Tailor: &core.BranchTailorSpec{BranchID: br.ID}}
		ver4 := &core.BranchVersion{ID: "ver-4", BranchID: br.ID, UserID: "u1"}
		if err := st.Jobs().SubmitBranchTailor(ctx, job4, ver4, core.PendingJobOptions{}); err != nil {
			t.Fatalf("SubmitBranchTailor job4 failed: %v", err)
		}
		claimed, err = st.Jobs().ClaimNext(ctx, core.ClaimJobRequest{WorkerID: "worker-a"})
		if err != nil || claimed == nil || claimed.ID != job4.ID || claimed.Tailor.BranchEpoch != br.Epoch {
			t.Fatalf("expected claim of job4 at epoch %d, got %+v (err=%v)", br.Epoch, claimed, err)
		}
		if err := st.Jobs().CompleteBranchTailor(ctx, job4.ID, "worker-a", &core.BranchVersion{
			MainRevisionUsed: 1,
			ProviderUsed:     "gemini",
			ModelUsed:        "flash",
			HTMLOutput:       "<p>done</p>",
		}); err != nil {
			t.Fatalf("CompleteBranchTailor job4 failed: %v", err)
		}
		brFinal, _ := st.Branches().Branch(ctx, "u1", br.ID)
		if brFinal.ActiveVersionID == nil || *brFinal.ActiveVersionID != ver4.ID {
			t.Fatalf("expected active version promoted to %s, got %+v", ver4.ID, brFinal.ActiveVersionID)
		}
	})

	t.Run("PDFParseRawTextPruningAndUnifiedAdmission", func(t *testing.T) {
		clk := newTestClock(defaultTestBaseTime)
		st := openStore(t, newStore, clk.Now)
		ctx := runCtx(t)

		tooBig := &core.Job{
			ID:     "pdf-big",
			UserID: "u1",
			PDF:    &core.PDFParseSpec{RawText: "123456"},
		}
		if err := st.Jobs().SubmitPDFParse(ctx, tooBig, core.PendingJobOptions{MaxRawTextBytes: 5}); !errors.Is(err, core.ErrValidation) {
			t.Fatalf("expected ErrValidation for oversized PDF raw text, got %v", err)
		}

		// Unified admission counts both branch_tailor and pdf_parse against MaxPendingPerTenant.
		br := &core.Branch{ID: "br-cap", UserID: "u1", Name: "Cap", TemplateSetID: core.TemplateStandardCleanID}
		_ = st.Branches().CreateBranch(ctx, br)
		opts := core.PendingJobOptions{MaxPendingPerTenant: 2}
		if err := st.Jobs().SubmitBranchTailor(ctx, &core.Job{ID: "t-1", UserID: "u1"}, &core.BranchVersion{ID: "v-1", BranchID: br.ID}, opts); err != nil {
			t.Fatalf("SubmitBranchTailor t-1 failed: %v", err)
		}
		pdf1 := &core.Job{
			ID:     "pdf-1",
			UserID: "u1",
			PDF:    &core.PDFParseSpec{Filename: "cv.pdf", RawText: "secret resume pii"},
		}
		if err := st.Jobs().SubmitPDFParse(ctx, pdf1, opts); err != nil {
			t.Fatalf("SubmitPDFParse pdf-1 failed: %v", err)
		}
		if err := st.Jobs().SubmitPDFParse(ctx, &core.Job{ID: "pdf-2", UserID: "u1", PDF: &core.PDFParseSpec{RawText: "x"}}, opts); !errors.Is(err, core.ErrPendingLimitExceeded) {
			t.Fatalf("expected ErrPendingLimitExceeded across mixed queue, got %v", err)
		}

		// SupersedeBranchTailor cancels t-1, then claim and complete pdf-1 verifying RawText is scrubbed.
		if err := st.Jobs().SupersedeBranchTailor(ctx, br.ID, "cancelled"); err != nil {
			t.Fatalf("SupersedeBranchTailor failed: %v", err)
		}
		claimed, err := st.Jobs().ClaimNext(ctx, core.ClaimJobRequest{WorkerID: "w1"})
		if err != nil || claimed == nil || claimed.ID != pdf1.ID {
			t.Fatalf("expected claim pdf-1, got %+v (err=%v)", claimed, err)
		}
		parsed := &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Alice"}}
		if err := st.Jobs().CompletePDFParse(ctx, pdf1.ID, "w1", parsed); err != nil {
			t.Fatalf("CompletePDFParse failed: %v", err)
		}
		donePDF, _ := st.Jobs().Job(ctx, "u1", pdf1.ID)
		if donePDF.Status != core.JobStatusCompleted || donePDF.PDF.RawText != "" || donePDF.PDF.Result == nil || donePDF.PDF.Result.Basics.Name != "Alice" {
			t.Fatalf("expected RawText scrubbed and Result preserved, got %+v", donePDF)
		}
	})

	t.Run("FairnessRetriesSweepsAndCleanup", func(t *testing.T) {
		clk := newTestClock(defaultTestBaseTime)
		st := openStore(t, newStore, clk.Now)
		ctx := runCtx(t)

		// Submit mixed jobs for tenant A and tenant B with deterministic staggered timestamps.
		_ = st.Jobs().SubmitPDFParse(ctx, &core.Job{ID: "a-1", UserID: "a", CreatedAt: clk.Now(), PDF: &core.PDFParseSpec{RawText: "a1"}}, core.PendingJobOptions{})
		clk.Advance(time.Second)
		_ = st.Jobs().SubmitPDFParse(ctx, &core.Job{ID: "a-2", UserID: "a", CreatedAt: clk.Now(), PDF: &core.PDFParseSpec{RawText: "a2"}}, core.PendingJobOptions{})
		clk.Advance(time.Second)
		_ = st.Jobs().SubmitPDFParse(ctx, &core.Job{ID: "b-1", UserID: "b", CreatedAt: clk.Now(), PDF: &core.PDFParseSpec{RawText: "b1"}}, core.PendingJobOptions{})

		// Tenant "a" was served at seq 10, tenant "b" never served (0): ClaimNext must pick "b-1" first.
		claimedB, err := st.Jobs().ClaimNext(ctx, core.ClaimJobRequest{
			WorkerID:      "w1",
			LastServedSeq: map[string]uint64{"a": 10},
			LeaseDuration: time.Minute,
		})
		if err != nil || claimedB == nil || claimedB.ID != "b-1" {
			t.Fatalf("expected LRU fairness to pick b-1, got %+v (err=%v)", claimedB, err)
		}

		// Requeue b-1 with 2m backoff delay: b-1 must be skipped until clock advances past NextRetryAt.
		if err := st.Jobs().RequeueForRetry(ctx, "b-1", "w1", clk.Now().Add(2*time.Minute), "rate limit"); err != nil {
			t.Fatalf("RequeueForRetry failed: %v", err)
		}
		claimedA, err := st.Jobs().ClaimNext(ctx, core.ClaimJobRequest{
			WorkerID:               "w1",
			MaxConcurrentPerTenant: 1,
			LeaseDuration:          time.Minute,
		})
		if err != nil || claimedA == nil || claimedA.ID != "a-1" {
			t.Fatalf("expected a-1 claimed while b-1 in backoff, got %+v (err=%v)", claimedA, err)
		}
		// Tenant "a" now has 1 active processing job, so MaxConcurrentPerTenant=1 blocks a-2.
		blockedA2, err := st.Jobs().ClaimNext(ctx, core.ClaimJobRequest{
			WorkerID:               "w2",
			MaxConcurrentPerTenant: 1,
		})
		if err != nil || blockedA2 != nil {
			t.Fatalf("expected nil due to tenant concurrency cap & b-1 backoff, got %+v (err=%v)", blockedA2, err)
		}

		// Advance clock 2m -> lease on a-1 expires AND b-1 backoff clears.
		clk.Advance(2 * time.Minute)
		expired, err := st.Jobs().FailExpiredLeases(ctx, clk.Now(), "lease timeout")
		if err != nil {
			t.Fatalf("FailExpiredLeases failed: %v", err)
		}
		if expired != 1 {
			t.Fatalf("expected 1 expired lease, got %d", expired)
		}
		a1Now, _ := st.Jobs().Job(ctx, "a", "a-1")
		if a1Now.Status != core.JobStatusFailed || a1Now.PDF.RawText != "" {
			t.Fatalf("expected expired a-1 failed & scrubbed, got %+v", a1Now)
		}

		// Claim b-1 after retry delay (with "a" last served after claiming a-1), then test RecoverInterrupted.
		claimedB2, err := st.Jobs().ClaimNext(ctx, core.ClaimJobRequest{
			WorkerID:      "w2",
			LastServedSeq: map[string]uint64{"a": 10},
		})
		if err != nil || claimedB2 == nil || claimedB2.ID != "b-1" || claimedB2.AttemptCount != 2 {
			t.Fatalf("expected reclaim b-1 attempt 2, got %+v (err=%v)", claimedB2, err)
		}
		recovered, err := st.Jobs().RecoverInterrupted(ctx, "restart")
		if err != nil {
			t.Fatalf("RecoverInterrupted failed: %v", err)
		}
		if recovered != 1 {
			t.Fatalf("expected 1 interrupted job recovered, got %d", recovered)
		}
		bNow, _ := st.Jobs().Job(ctx, "b", "b-1")
		a2Now, _ := st.Jobs().Job(ctx, "a", "a-2")
		if bNow.Status != core.JobStatusFailed || a2Now.Status != core.JobStatusPending {
			t.Fatalf("expected b-1 failed and unexpired a-2 preserved pending, got b=%s a2=%s", bNow.Status, a2Now.Status)
		}

		// Cleanup old terminal jobs keeps recent/pending untouched.
		clk.Advance(2 * time.Hour)
		if err := st.Jobs().CleanupCompletedJobs(ctx, "a", time.Hour); err != nil {
			t.Fatalf("CleanupCompletedJobs failed: %v", err)
		}
		if _, err := st.Jobs().Job(ctx, "a", "a-1"); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("expected old terminal job a-1 deleted, got %v", err)
		}
		if a2Check, err := st.Jobs().Job(ctx, "a", "a-2"); err != nil || a2Check.Status != core.JobStatusPending {
			t.Fatalf("expected pending a-2 preserved across cleanup, got %+v (err=%v)", a2Check, err)
		}
	})

	t.Run("ConcurrentClaimExclusivity", func(t *testing.T) {
		st := openStore(t, newStore, newTestClock(defaultTestBaseTime).Now)
		ctx := runCtx(t)

		const totalJobs = 12
		for i := range totalJobs {
			_ = st.Jobs().SubmitPDFParse(ctx, &core.Job{
				ID:     core.GenerateID("pdf"),
				UserID: core.GenerateShortID("u"),
				PDF:    &core.PDFParseSpec{RawText: string(rune('a' + i))},
			}, core.PendingJobOptions{})
		}

		var wg sync.WaitGroup
		claimedIDs := make(chan string, totalJobs*2)
		for w := range 6 {
			workerID := core.GenerateID("w")
			_ = w
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				for {
					job, err := st.Jobs().ClaimNext(ctx, core.ClaimJobRequest{WorkerID: id})
					if err != nil {
						t.Errorf("ClaimNext failed: %v", err)
						return
					}
					if job == nil {
						return
					}
					claimedIDs <- job.ID
				}
			}(workerID)
		}
		wg.Wait()
		close(claimedIDs)

		seen := make(map[string]int)
		for id := range claimedIDs {
			seen[id]++
		}
		if len(seen) != totalJobs {
			t.Fatalf("expected %d unique jobs claimed, got %d", totalJobs, len(seen))
		}
		for id, count := range seen {
			if count != 1 {
				t.Fatalf("job %s claimed %d times", id, count)
			}
		}
	})

	t.Run("AtomicQuotaReservationAndSettlement", func(t *testing.T) {
		clk := newTestClock(defaultTestBaseTime)
		st := openStore(t, newStore, clk.Now)
		ctx := runCtx(t)

		br := &core.Branch{ID: "br-q", UserID: "u1", Name: "Quota Branch", TemplateSetID: core.TemplateStandardCleanID}
		if err := st.Branches().CreateBranch(ctx, br); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}

		// Daily limit = 1, Monthly = 1000: SubmitBranchTailor reserves atomically.
		spec := &core.QuotaReservationSpec{
			Operation:       core.OperationResumeTailoring,
			EstimatedTokens: 400,
			DailyLimit:      1,
			MonthlyLimit:    1000,
		}
		job1 := &core.Job{ID: "jq-1", UserID: "u1", Tailor: &core.BranchTailorSpec{BranchID: br.ID}}
		ver1 := &core.BranchVersion{ID: "vq-1", BranchID: br.ID, UserID: "u1"}
		if err := st.Jobs().SubmitBranchTailor(ctx, job1, ver1, core.PendingJobOptions{
			MaxPendingPerTenant: 1,
			Quota:               spec,
		}); err != nil {
			t.Fatalf("SubmitBranchTailor jq-1 failed: %v", err)
		}
		if job1.ReservationID == "" {
			t.Fatalf("expected ReservationID populated on jq-1")
		}

		// Queue cap (ErrPendingLimitExceeded) must win before quota exhaustion when submitting a PDF job,
		// and rejected submissions must roll back without modifying quota usage.
		pdfBlocked := &core.Job{ID: "pq-blocked", UserID: "u1", PDF: &core.PDFParseSpec{RawText: "hello"}}
		if err := st.Jobs().SubmitPDFParse(ctx, pdfBlocked, core.PendingJobOptions{
			MaxPendingPerTenant: 1,
			Quota: &core.QuotaReservationSpec{
				Operation:       core.OperationPDFParse,
				EstimatedTokens: 400,
				DailyLimit:      1,
				MonthlyLimit:    1000,
			},
		}); !errors.Is(err, core.ErrPendingLimitExceeded) {
			t.Fatalf("expected ErrPendingLimitExceeded before ErrQuotaExceeded, got %v", err)
		}

		// Superseding jq-1 on the same branch refunds jq-1's reservation and reserves jq-2 in one tx, even at 1/1 limit.
		job2 := &core.Job{ID: "jq-2", UserID: "u1", Tailor: &core.BranchTailorSpec{BranchID: br.ID}}
		ver2 := &core.BranchVersion{ID: "vq-2", BranchID: br.ID, UserID: "u1"}
		if err := st.Jobs().SubmitBranchTailor(ctx, job2, ver2, core.PendingJobOptions{
			MaxPendingPerTenant: 1,
			Quota:               spec,
		}); err != nil {
			t.Fatalf("superseding SubmitBranchTailor at 1/1 quota failed: %v", err)
		}
		if job2.ReservationID == "" || job2.ReservationID == job1.ReservationID {
			t.Fatalf("expected fresh ReservationID on jq-2, got old=%s new=%s", job1.ReservationID, job2.ReservationID)
		}
		qAfterSupersede, _ := st.Quotas().Quota(ctx, "u1")
		if qAfterSupersede.DailyRequestsUsed != 1 || qAfterSupersede.MonthlyTokensUsed != 400 {
			t.Fatalf("expected net 1 request / 400 tokens after supersede, got daily=%d monthly=%d",
				qAfterSupersede.DailyRequestsUsed, qAfterSupersede.MonthlyTokensUsed)
		}

		// Claim jq-2 and FailClaimed with RefundQuota: true + Telemetry settles atomically.
		claimed, err := st.Jobs().ClaimNext(ctx, core.ClaimJobRequest{WorkerID: "w-atomic"})
		if err != nil || claimed == nil || claimed.ID != job2.ID {
			t.Fatalf("expected claim of jq-2, got %+v (err=%v)", claimed, err)
		}
		if err := st.Jobs().FailClaimed(ctx, core.FailClaimedRequest{
			JobID:       job2.ID,
			ClaimedBy:   "w-atomic",
			ErrMsg:      "upstream error",
			RefundQuota: true,
			Telemetry:   core.LLMTelemetry{Provider: "gemini", Model: "flash", StatusCode: 503},
		}); err != nil {
			t.Fatalf("FailClaimed failed: %v", err)
		}
		qAfterFail, _ := st.Quotas().Quota(ctx, "u1")
		if qAfterFail.DailyRequestsUsed != 0 || qAfterFail.MonthlyTokensUsed != 0 {
			t.Fatalf("expected quota fully refunded on FailClaimed, got daily=%d monthly=%d",
				qAfterFail.DailyRequestsUsed, qAfterFail.MonthlyTokensUsed)
		}
		recs, _ := st.Quotas().ListUsageRecords(ctx, "u1", 10)
		foundReleased2 := false
		for _, r := range recs {
			if r.ID == job2.ReservationID {
				foundReleased2 = true
				if r.Status != core.ReservationStatusReleased || r.StatusCode != 503 || r.Provider != "gemini" {
					t.Fatalf("unexpected settled usage record for jq-2: %+v", r)
				}
			}
		}
		if !foundReleased2 {
			t.Fatalf("missing settled usage record %s for jq-2", job2.ReservationID)
		}

		// SubmitPDFParse reserves quota, then ExpirePending atomically refunds it and scrubs RawText.
		pdfExp := &core.Job{ID: "pq-exp", UserID: "u1", PDF: &core.PDFParseSpec{RawText: "confidential pdf"}}
		if err := st.Jobs().SubmitPDFParse(ctx, pdfExp, core.PendingJobOptions{
			MaxPendingPerTenant: 5,
			TTL:                 time.Minute,
			Now:                 clk.Now(),
			Quota: &core.QuotaReservationSpec{
				Operation:       core.OperationPDFParse,
				EstimatedTokens: 300,
				DailyLimit:      2,
				MonthlyLimit:    1000,
			},
		}); err != nil {
			t.Fatalf("SubmitPDFParse pq-exp failed: %v", err)
		}
		clk.Advance(2 * time.Minute)
		expCount, err := st.Jobs().ExpirePending(ctx, clk.Now(), "expired")
		if err != nil || expCount != 1 {
			t.Fatalf("expected 1 expired pdf job, got count=%d err=%v", expCount, err)
		}
		qAfterExp, _ := st.Quotas().Quota(ctx, "u1")
		if qAfterExp.DailyRequestsUsed != 0 || qAfterExp.MonthlyTokensUsed != 0 {
			t.Fatalf("expected pdf quota released on ExpirePending, got daily=%d monthly=%d",
				qAfterExp.DailyRequestsUsed, qAfterExp.MonthlyTokensUsed)
		}
	})
}
