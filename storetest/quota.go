package storetest

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// RunQuotas verifies quota initialization, window resets, atomic reservations, telemetry, and refunds.
func RunQuotas(t *testing.T, newStore Factory) {
	t.Run("ReserveCommitReleaseAndRollover", func(t *testing.T) {
		clk := newTestClock(defaultTestBaseTime)
		st := openStore(t, newStore, clk.Now)
		ctx := runCtx(t)

		q, err := st.Quotas().Quota(ctx, "u1")
		if err != nil {
			t.Fatalf("Quota init failed: %v", err)
		}
		if q.DailyRequestLimit != core.DefaultDailyRequestLimit || q.MonthlyTokenLimit != core.DefaultMonthlyTokenLimit {
			t.Fatalf("unexpected default quota limits: %+v", q)
		}

		rec1, err := st.Quotas().ReserveQuota(ctx, "u1", core.OperationResumeTailoring, 1000, 2, 2500)
		if err != nil {
			t.Fatalf("ReserveQuota 1 failed: %v", err)
		}
		if rec1.Status != core.ReservationStatusPending {
			t.Fatalf("expected pending reservation, got %s", rec1.Status)
		}

		// Exceeding monthly token cap fails atomically without charging daily request count.
		if _, err := st.Quotas().ReserveQuota(ctx, "u1", core.OperationResumeTailoring, 2000, 2, 2500); !errors.Is(err, core.ErrQuotaExceeded) {
			t.Fatalf("expected ErrQuotaExceeded on monthly tokens, got %v", err)
		}

		rec2, err := st.Quotas().ReserveQuota(ctx, "u1", core.OperationGapAnalysis, 500, 2, 2500)
		if err != nil {
			t.Fatalf("ReserveQuota 2 failed: %v", err)
		}
		if _, err := st.Quotas().ReserveQuota(ctx, "u1", core.OperationATSSimulation, 100, 2, 2500); !errors.Is(err, core.ErrQuotaExceeded) {
			t.Fatalf("expected ErrQuotaExceeded on daily requests, got %v", err)
		}

		// Commit reconciles estimated tokens -> actual tokens (1000 estimated vs 400 actual => -600 delta).
		telemetry := core.LLMTelemetry{
			Provider:   "gemini",
			Model:      "gemini-2.5-flash",
			StatusCode: 200,
			Latency:    250 * time.Millisecond,
			Usage: core.TokenUsage{
				PromptTokens:     300,
				CompletionTokens: 100,
				TotalTokens:      400,
			},
		}
		if err := st.Quotas().CommitUsage(ctx, rec1.ID, telemetry); err != nil {
			t.Fatalf("CommitUsage failed: %v", err)
		}
		// Second commit must be idempotent without double-adjusting quota.
		if err := st.Quotas().CommitUsage(ctx, rec1.ID, telemetry); err != nil {
			t.Fatalf("idempotent CommitUsage failed: %v", err)
		}

		q, _ = st.Quotas().Quota(ctx, "u1")
		if q.DailyRequestsUsed != 2 || q.MonthlyTokensUsed != 900 {
			t.Fatalf("unexpected quota after commit: daily=%d monthly=%d", q.DailyRequestsUsed, q.MonthlyTokensUsed)
		}

		// Release refunds rec2's daily count and estimated tokens idempotently.
		if err := st.Quotas().ReleaseQuota(ctx, rec2.ID, "provider error", core.LLMTelemetry{Provider: "gemini", StatusCode: 503}); err != nil {
			t.Fatalf("ReleaseQuota failed: %v", err)
		}
		if err := st.Quotas().ReleaseQuota(ctx, rec2.ID, "duplicate", core.LLMTelemetry{}); err != nil {
			t.Fatalf("idempotent ReleaseQuota failed: %v", err)
		}

		q, _ = st.Quotas().Quota(ctx, "u1")
		if q.DailyRequestsUsed != 1 || q.MonthlyTokensUsed != 400 {
			t.Fatalf("unexpected quota after release: daily=%d monthly=%d", q.DailyRequestsUsed, q.MonthlyTokensUsed)
		}

		recs, err := st.Quotas().ListUsageRecords(ctx, "u1", 10)
		if err != nil || len(recs) != 2 {
			t.Fatalf("expected 2 usage records, got %d (err=%v)", len(recs), err)
		}

		// Advance clock 25 hours: daily resets while monthly remains 400.
		clk.Advance(25 * time.Hour)
		q, err = st.Quotas().Quota(ctx, "u1")
		if err != nil {
			t.Fatalf("Quota after 25h failed: %v", err)
		}
		if q.DailyRequestsUsed != 0 || q.MonthlyTokensUsed != 400 {
			t.Fatalf("expected daily reset and monthly preserved, got daily=%d monthly=%d", q.DailyRequestsUsed, q.MonthlyTokensUsed)
		}

		// Advance clock 31 days: monthly window resets to 0.
		clk.Advance(31 * 24 * time.Hour)
		q, err = st.Quotas().Quota(ctx, "u1")
		if err != nil {
			t.Fatalf("Quota after 31d failed: %v", err)
		}
		if q.DailyRequestsUsed != 0 || q.MonthlyTokensUsed != 0 {
			t.Fatalf("expected both windows reset, got daily=%d monthly=%d", q.DailyRequestsUsed, q.MonthlyTokensUsed)
		}
	})

	t.Run("ConcurrentReserveAtomicity", func(t *testing.T) {
		st := openStore(t, newStore, newTestClock(defaultTestBaseTime).Now)
		ctx := runCtx(t)

		const workers = 16
		const dailyCap = 5
		var wg sync.WaitGroup
		results := make(chan error, workers)
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := st.Quotas().ReserveQuota(ctx, "race-user", core.OperationResumeTailoring, 100, dailyCap, 100000)
				results <- err
			}()
		}
		wg.Wait()
		close(results)

		var succeeded, exceeded int
		for err := range results {
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, core.ErrQuotaExceeded):
				exceeded++
			default:
				t.Fatalf("unexpected error: %v", err)
			}
		}
		if succeeded != dailyCap || exceeded != workers-dailyCap {
			t.Fatalf("expected %d succeeded and %d exceeded, got %d / %d", dailyCap, workers-dailyCap, succeeded, exceeded)
		}
	})
}
