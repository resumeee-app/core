package worker_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/memory"
	"github.com/resumeee-app/resumeee-core/provenance"
	"github.com/resumeee-app/resumeee-core/renderer"
	"github.com/resumeee-app/resumeee-core/worker"
)

type failingValidator struct {
	err error
}

func (v *failingValidator) Validate(_ *core.TailoredContent, _ *core.MainProfileSnapshot, _ *core.Branch) (*core.ValidationResult, error) {
	return nil, v.err
}

type failingCompleteBranchStore struct {
	worker.BranchStore
	completeErr error
}

func (s *failingCompleteBranchStore) CompleteVersion(_ context.Context, _ *core.BranchVersion) error {
	return s.completeErr
}

type failingCompletePDFStore struct {
	worker.PDFJobStore
	completeErr error
}

func (s *failingCompletePDFStore) UpdateJobStatus(ctx context.Context, jobID string, status core.PDFParseJobStatus, result *core.JSONResume, errMsg string) error {
	if status == core.PDFParseJobStatusCompleted {
		return s.completeErr
	}
	return s.PDFJobStore.UpdateJobStatus(ctx, jobID, status, result, errMsg)
}

func waitForVersionTerminal(ctx context.Context, t *testing.T, store core.BranchStore, branchID, versionID string) *core.BranchVersion {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		ver, err := store.Version(ctx, branchID, versionID)
		if err == nil && (ver.Status == core.VersionStatusCompleted || ver.Status == core.VersionStatusCompletedWithWarnings || ver.Status == core.VersionStatusFailed) {
			return ver
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for version %s to reach terminal state", versionID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForPDFJobTerminal(ctx context.Context, t *testing.T, store core.PDFJobStore, userID, jobID string) *core.PDFParseJob {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		job, err := store.Job(ctx, userID, jobID)
		if err == nil && (job.Status == core.PDFParseJobStatusCompleted || job.Status == core.PDFParseJobStatusFailed) {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for PDF job %s to reach terminal state", jobID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForUsageSettled(ctx context.Context, t *testing.T, store core.QuotaStore, userID, reservationID string) core.LLMUsageRecord {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		records, err := store.ListUsageRecords(ctx, userID, 50)
		if err == nil {
			for _, rec := range records {
				if rec.ID == reservationID && rec.Status != core.ReservationStatusPending {
					return rec
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for usage record %s to settle", reservationID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertTelemetryFields(t *testing.T, rec core.LLMUsageRecord, want core.LLMTelemetry, wantTotal int) {
	t.Helper()
	if rec.Provider != want.Provider {
		t.Errorf("record Provider = %q, want %q", rec.Provider, want.Provider)
	}
	if rec.Model != want.Model {
		t.Errorf("record Model = %q, want %q", rec.Model, want.Model)
	}
	if rec.StatusCode != want.StatusCode {
		t.Errorf("record StatusCode = %d, want %d", rec.StatusCode, want.StatusCode)
	}
	if rec.Latency != want.Latency {
		t.Errorf("record Latency = %v, want %v", rec.Latency, want.Latency)
	}
	if rec.PromptTokens != want.Usage.PromptTokens || rec.CompletionTokens != want.Usage.CompletionTokens || rec.TotalTokens != wantTotal {
		t.Errorf("record tokens = (%d, %d, %d), want (%d, %d, %d)",
			rec.PromptTokens, rec.CompletionTokens, rec.TotalTokens,
			want.Usage.PromptTokens, want.Usage.CompletionTokens, wantTotal)
	}
}

func TestPool_TailoringPipeline_TelemetryCommitAndVersionMetadata(t *testing.T) {
	ctx := context.Background()

	t.Run("empty branch overrides populate ProviderUsed and ModelUsed from resolved telemetry", func(t *testing.T) {
		store, err := memory.NewSeeded(ctx, "user-tailor-tel-1")
		if err != nil {
			t.Fatalf("NewSeeded failed: %v", err)
		}

		branch := &core.Branch{
			ID:            "branch-empty-overrides",
			UserID:        "user-tailor-tel-1",
			Name:          "Backend Staff",
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := store.CreateBranch(ctx, branch); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}

		rec, err := store.ReserveQuota(ctx, "user-tailor-tel-1", core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}

		ver := &core.BranchVersion{
			ID:                "ver-tel-1",
			BranchID:          branch.ID,
			Status:            core.VersionStatusPending,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: core.TemplateStandardCleanID,
		}
		if err := store.CreateVersion(ctx, ver); err != nil {
			t.Fatalf("CreateVersion failed: %v", err)
		}

		wantTel := core.LLMTelemetry{
			Provider:   "openai",
			Model:      "gpt-4o",
			StatusCode: 200,
			Latency:    42 * time.Millisecond,
			Usage:      core.TokenUsage{PromptTokens: 320, CompletionTokens: 180},
		}
		gen := &callbackLLM{
			fn: func(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
				return &core.GenerationResponse{
					TailoredContent: core.TailoredContent{Summary: "Tailored summary"},
					GapReport:       core.GapReport{OverallMatchSummary: "Strong fit"},
					Telemetry:       wantTel,
				}, nil
			},
		}

		pool := worker.New(worker.Config{
			Concurrency: 1,
			QueueSize:   5,
			Branches:    store,
			Profiles:    store,
			Presets:     store,
			Templates:   store,
			Quotas:      store,
			Generator:   gen,
			Validator:   provenance.New(),
			Renderer:    renderer.New(),
		})
		pool.Start(ctx)
		defer pool.Stop()

		if err := pool.Enqueue(core.GenerationJob{
			UserID:        "user-tailor-tel-1",
			BranchID:      branch.ID,
			VersionID:     ver.ID,
			ReservationID: rec.ID,
		}); err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}

		gotVer := waitForVersionTerminal(ctx, t, store, branch.ID, ver.ID)
		if gotVer.Status != core.VersionStatusCompleted {
			t.Fatalf("expected Completed, got %s (%s)", gotVer.Status, gotVer.ErrorMessage)
		}
		if gotVer.ProviderUsed != "openai" || gotVer.ModelUsed != "gpt-4o" {
			t.Errorf("ProviderUsed/ModelUsed = (%q, %q), want (%q, %q)", gotVer.ProviderUsed, gotVer.ModelUsed, "openai", "gpt-4o")
		}

		gotRec := waitForUsageSettled(ctx, t, store, "user-tailor-tel-1", rec.ID)
		if gotRec.Status != core.ReservationStatusCommitted {
			t.Fatalf("expected Committed, got %s", gotRec.Status)
		}
		assertTelemetryFields(t, gotRec, wantTel, 500)

		quota, err := store.Quota(ctx, "user-tailor-tel-1")
		if err != nil {
			t.Fatalf("Quota failed: %v", err)
		}
		if quota.MonthlyTokensUsed != 500 || quota.DailyRequestsUsed != 1 {
			t.Errorf("quota = (tokens=%d, reqs=%d), want (500, 1)", quota.MonthlyTokensUsed, quota.DailyRequestsUsed)
		}
	})

	t.Run("telemetry overrides stale branch values and legacy Usage backfills via EffectiveTelemetry", func(t *testing.T) {
		store, err := memory.NewSeeded(ctx, "user-tailor-tel-2")
		if err != nil {
			t.Fatalf("NewSeeded failed: %v", err)
		}

		branch := &core.Branch{
			ID:               "branch-override-precedence",
			UserID:           "user-tailor-tel-2",
			Name:             "Platform Engineer",
			TemplateSetID:    core.TemplateStandardCleanID,
			ProviderOverride: "compat",
			ModelOverride:    "alias-default",
		}
		if err := store.CreateBranch(ctx, branch); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}

		rec, err := store.ReserveQuota(ctx, "user-tailor-tel-2", core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}

		ver := &core.BranchVersion{
			ID:                "ver-tel-2",
			BranchID:          branch.ID,
			Status:            core.VersionStatusPending,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: core.TemplateStandardCleanID,
			ProviderUsed:      branch.ProviderOverride,
			ModelUsed:         branch.ModelOverride,
		}
		if err := store.CreateVersion(ctx, ver); err != nil {
			t.Fatalf("CreateVersion failed: %v", err)
		}

		gen := &callbackLLM{
			fn: func(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
				return &core.GenerationResponse{
					TailoredContent: core.TailoredContent{Summary: "Tailored summary"},
					GapReport:       core.GapReport{OverallMatchSummary: "Strong fit"},
					Usage:           core.TokenUsage{PromptTokens: 110, CompletionTokens: 90},
					Telemetry: core.LLMTelemetry{
						Provider:   "openaicompat",
						Model:      "llama-3.3-70b",
						StatusCode: 200,
						Latency:    88 * time.Millisecond,
					},
				}, nil
			},
		}

		pool := worker.New(worker.Config{
			Concurrency: 1,
			QueueSize:   5,
			Branches:    store,
			Profiles:    store,
			Presets:     store,
			Templates:   store,
			Quotas:      store,
			Generator:   gen,
			Validator:   provenance.New(),
			Renderer:    renderer.New(),
		})
		pool.Start(ctx)
		defer pool.Stop()

		if err := pool.Enqueue(core.GenerationJob{
			UserID:        "user-tailor-tel-2",
			BranchID:      branch.ID,
			VersionID:     ver.ID,
			ReservationID: rec.ID,
		}); err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}

		gotVer := waitForVersionTerminal(ctx, t, store, branch.ID, ver.ID)
		if gotVer.ProviderUsed != "openaicompat" || gotVer.ModelUsed != "llama-3.3-70b" {
			t.Errorf("ProviderUsed/ModelUsed = (%q, %q), want (%q, %q)", gotVer.ProviderUsed, gotVer.ModelUsed, "openaicompat", "llama-3.3-70b")
		}

		gotRec := waitForUsageSettled(ctx, t, store, "user-tailor-tel-2", rec.ID)
		assertTelemetryFields(t, gotRec, core.LLMTelemetry{
			Provider:   "openaicompat",
			Model:      "llama-3.3-70b",
			StatusCode: 200,
			Latency:    88 * time.Millisecond,
			Usage:      core.TokenUsage{PromptTokens: 110, CompletionTokens: 90},
		}, 200)
	})

	t.Run("empty telemetry falls back to branch overrides and default tailoring tokens", func(t *testing.T) {
		store, err := memory.NewSeeded(ctx, "user-tailor-tel-3")
		if err != nil {
			t.Fatalf("NewSeeded failed: %v", err)
		}

		branch := &core.Branch{
			ID:               "branch-fallback-overrides",
			UserID:           "user-tailor-tel-3",
			Name:             "Fallback Test",
			TemplateSetID:    core.TemplateStandardCleanID,
			ProviderOverride: "anthropic",
			ModelOverride:    "claude-sonnet-4-5",
		}
		if err := store.CreateBranch(ctx, branch); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}

		rec, err := store.ReserveQuota(ctx, "user-tailor-tel-3", core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}

		ver := &core.BranchVersion{
			ID:                "ver-tel-3",
			BranchID:          branch.ID,
			Status:            core.VersionStatusPending,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: core.TemplateStandardCleanID,
		}
		if err := store.CreateVersion(ctx, ver); err != nil {
			t.Fatalf("CreateVersion failed: %v", err)
		}

		pool := worker.New(worker.Config{
			Concurrency: 1,
			QueueSize:   5,
			Branches:    store,
			Profiles:    store,
			Presets:     store,
			Templates:   store,
			Quotas:      store,
			Generator:   &callbackLLM{},
			Validator:   provenance.New(),
			Renderer:    renderer.New(),
		})
		pool.Start(ctx)
		defer pool.Stop()

		if err := pool.Enqueue(core.GenerationJob{
			UserID:        "user-tailor-tel-3",
			BranchID:      branch.ID,
			VersionID:     ver.ID,
			ReservationID: rec.ID,
		}); err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}

		gotVer := waitForVersionTerminal(ctx, t, store, branch.ID, ver.ID)
		if gotVer.ProviderUsed != "anthropic" || gotVer.ModelUsed != "claude-sonnet-4-5" {
			t.Errorf("ProviderUsed/ModelUsed = (%q, %q), want (%q, %q)", gotVer.ProviderUsed, gotVer.ModelUsed, "anthropic", "claude-sonnet-4-5")
		}

		gotRec := waitForUsageSettled(ctx, t, store, "user-tailor-tel-3", rec.ID)
		if gotRec.Status != core.ReservationStatusCommitted || gotRec.TotalTokens != 2000 {
			t.Errorf("expected Committed with 2000 fallback tokens, got status=%s total=%d", gotRec.Status, gotRec.TotalTokens)
		}
	})
}

func TestPool_TailoringPipeline_TelemetryReleaseOnErrors(t *testing.T) {
	ctx := context.Background()

	errorCases := []struct {
		name      string
		telemetry core.LLMTelemetry
		baseErr   error
		wantTotal int
		wantErr   string
	}{
		{
			name: "429 rate limit releases quota with call telemetry",
			telemetry: core.LLMTelemetry{
				Provider:   "anthropic",
				Model:      "claude-sonnet-4-5",
				StatusCode: 429,
				Latency:    25 * time.Millisecond,
				Usage:      core.TokenUsage{PromptTokens: 15},
			},
			baseErr:   errors.New("rate limit exceeded"),
			wantTotal: 15,
			wantErr:   "error occurred during resume tailoring",
		},
		{
			name: "500 upstream failure releases quota with call telemetry",
			telemetry: core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o",
				StatusCode: 500,
				Latency:    80 * time.Millisecond,
			},
			baseErr:   errors.New("internal server error"),
			wantTotal: 0,
			wantErr:   "error occurred during resume tailoring",
		},
		{
			name: "timeout failure releases quota with zero status and latency",
			telemetry: core.LLMTelemetry{
				Provider:   "gemini",
				Model:      "gemini-2.5-pro",
				StatusCode: 0,
				Latency:    150 * time.Millisecond,
			},
			baseErr:   context.DeadlineExceeded,
			wantTotal: 0,
			wantErr:   "timed out",
		},
	}

	for i, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			userID := fmt.Sprintf("user-tailor-err-%d", i)
			store, err := memory.NewSeeded(ctx, userID)
			if err != nil {
				t.Fatalf("NewSeeded failed: %v", err)
			}
			branch := &core.Branch{
				ID:            fmt.Sprintf("branch-err-%d", i),
				UserID:        userID,
				Name:          "Err Branch",
				TemplateSetID: core.TemplateStandardCleanID,
			}
			if err := store.CreateBranch(ctx, branch); err != nil {
				t.Fatalf("CreateBranch failed: %v", err)
			}
			rec, err := store.ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 50, 100000)
			if err != nil {
				t.Fatalf("ReserveQuota failed: %v", err)
			}
			ver := &core.BranchVersion{
				ID:                fmt.Sprintf("ver-err-%d", i),
				BranchID:          branch.ID,
				Status:            core.VersionStatusPending,
				MainRevisionUsed:  1,
				TemplateSetIDUsed: core.TemplateStandardCleanID,
			}
			if err := store.CreateVersion(ctx, ver); err != nil {
				t.Fatalf("CreateVersion failed: %v", err)
			}

			gen := &mockLLM{
				failErr: core.AttachLLMTelemetry(tc.baseErr, tc.telemetry),
			}
			pool := worker.New(worker.Config{
				Concurrency: 1,
				QueueSize:   5,
				Branches:    store,
				Profiles:    store,
				Presets:     store,
				Templates:   store,
				Quotas:      store,
				Generator:   gen,
				Validator:   provenance.New(),
				Renderer:    renderer.New(),
			})
			pool.Start(ctx)
			defer pool.Stop()

			if err := pool.Enqueue(core.GenerationJob{
				UserID:        userID,
				BranchID:      branch.ID,
				VersionID:     ver.ID,
				ReservationID: rec.ID,
			}); err != nil {
				t.Fatalf("Enqueue failed: %v", err)
			}

			gotVer := waitForVersionTerminal(ctx, t, store, branch.ID, ver.ID)
			if gotVer.Status != core.VersionStatusFailed {
				t.Fatalf("expected Failed, got %s", gotVer.Status)
			}
			if !strings.Contains(strings.ToLower(gotVer.ErrorMessage), tc.wantErr) {
				t.Errorf("ErrorMessage = %q, want substring %q", gotVer.ErrorMessage, tc.wantErr)
			}

			gotRec := waitForUsageSettled(ctx, t, store, userID, rec.ID)
			if gotRec.Status != core.ReservationStatusReleased {
				t.Fatalf("expected Released, got %s", gotRec.Status)
			}
			assertTelemetryFields(t, gotRec, tc.telemetry, tc.wantTotal)

			quota, err := store.Quota(ctx, userID)
			if err != nil {
				t.Fatalf("Quota failed: %v", err)
			}
			if quota.MonthlyTokensUsed != 0 || quota.DailyRequestsUsed != 0 {
				t.Errorf("expected refunded quota (0, 0), got (%d, %d)", quota.MonthlyTokensUsed, quota.DailyRequestsUsed)
			}
		})
	}

	t.Run("post-LLM provenance validation error releases quota while preserving telemetry", func(t *testing.T) {
		userID := "user-tailor-val-err"
		store, err := memory.NewSeeded(ctx, userID)
		if err != nil {
			t.Fatalf("NewSeeded failed: %v", err)
		}
		branch := &core.Branch{
			ID:            "branch-val-err",
			UserID:        userID,
			Name:          "Val Err Branch",
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := store.CreateBranch(ctx, branch); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}
		rec, err := store.ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}
		ver := &core.BranchVersion{
			ID:                "ver-val-err",
			BranchID:          branch.ID,
			Status:            core.VersionStatusPending,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: core.TemplateStandardCleanID,
		}
		if err := store.CreateVersion(ctx, ver); err != nil {
			t.Fatalf("CreateVersion failed: %v", err)
		}

		wantTel := core.LLMTelemetry{
			Provider:   "openai",
			Model:      "gpt-4o",
			StatusCode: 200,
			Latency:    55 * time.Millisecond,
			Usage:      core.TokenUsage{PromptTokens: 200, CompletionTokens: 100},
		}
		pool := worker.New(worker.Config{
			Concurrency: 1,
			QueueSize:   5,
			Branches:    store,
			Profiles:    store,
			Presets:     store,
			Templates:   store,
			Quotas:      store,
			Generator:   &mockLLM{telemetry: wantTel},
			Validator:   &failingValidator{err: errors.New("hallucinated claim")},
			Renderer:    renderer.New(),
		})
		pool.Start(ctx)
		defer pool.Stop()

		if err := pool.Enqueue(core.GenerationJob{
			UserID:        userID,
			BranchID:      branch.ID,
			VersionID:     ver.ID,
			ReservationID: rec.ID,
		}); err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}

		gotVer := waitForVersionTerminal(ctx, t, store, branch.ID, ver.ID)
		if gotVer.Status != core.VersionStatusFailed {
			t.Fatalf("expected Failed, got %s", gotVer.Status)
		}

		gotRec := waitForUsageSettled(ctx, t, store, userID, rec.ID)
		if gotRec.Status != core.ReservationStatusReleased {
			t.Fatalf("expected Released, got %s", gotRec.Status)
		}
		assertTelemetryFields(t, gotRec, wantTel, 300)
		if !strings.Contains(gotRec.ErrorMessage, "hallucinated claim") {
			t.Errorf("ErrorMessage = %q, want validation error context", gotRec.ErrorMessage)
		}

		quota, err := store.Quota(ctx, userID)
		if err != nil {
			t.Fatalf("Quota failed: %v", err)
		}
		if quota.MonthlyTokensUsed != 0 || quota.DailyRequestsUsed != 0 {
			t.Errorf("expected refunded quota (0, 0), got (%d, %d)", quota.MonthlyTokensUsed, quota.DailyRequestsUsed)
		}
	})

	t.Run("post-LLM CompleteVersion persistence failure releases quota while preserving telemetry", func(t *testing.T) {
		userID := "user-tailor-persist-err"
		store, err := memory.NewSeeded(ctx, userID)
		if err != nil {
			t.Fatalf("NewSeeded failed: %v", err)
		}
		branch := &core.Branch{
			ID:            "branch-persist-err",
			UserID:        userID,
			Name:          "Persist Err Branch",
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := store.CreateBranch(ctx, branch); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}
		rec, err := store.ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}
		ver := &core.BranchVersion{
			ID:                "ver-persist-err",
			BranchID:          branch.ID,
			Status:            core.VersionStatusPending,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: core.TemplateStandardCleanID,
		}
		if err := store.CreateVersion(ctx, ver); err != nil {
			t.Fatalf("CreateVersion failed: %v", err)
		}

		wantTel := core.LLMTelemetry{
			Provider:   "gemini",
			Model:      "gemini-2.5-pro",
			StatusCode: 200,
			Latency:    70 * time.Millisecond,
		}
		pool := worker.New(worker.Config{
			Concurrency: 1,
			QueueSize:   5,
			Branches:    &failingCompleteBranchStore{BranchStore: store, completeErr: errors.New("disk full")},
			Profiles:    store,
			Presets:     store,
			Templates:   store,
			Quotas:      store,
			Generator:   &mockLLM{telemetry: wantTel},
			Validator:   provenance.New(),
			Renderer:    renderer.New(),
		})
		pool.Start(ctx)
		defer pool.Stop()

		if err := pool.Enqueue(core.GenerationJob{
			UserID:        userID,
			BranchID:      branch.ID,
			VersionID:     ver.ID,
			ReservationID: rec.ID,
		}); err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}

		gotVer := waitForVersionTerminal(ctx, t, store, branch.ID, ver.ID)
		if gotVer.Status != core.VersionStatusFailed {
			t.Fatalf("expected Failed, got %s", gotVer.Status)
		}

		gotRec := waitForUsageSettled(ctx, t, store, userID, rec.ID)
		if gotRec.Status != core.ReservationStatusReleased {
			t.Fatalf("expected Released, got %s", gotRec.Status)
		}
		// Zero reported usage must NOT be overwritten with fallback tokens on failure release.
		assertTelemetryFields(t, gotRec, wantTel, 0)
	})

	t.Run("pre-LLM load error releases quota with zero telemetry", func(t *testing.T) {
		userID := "user-tailor-pre-err"
		store, err := memory.NewSeeded(ctx, userID)
		if err != nil {
			t.Fatalf("NewSeeded failed: %v", err)
		}
		branch := &core.Branch{
			ID:            "branch-pre-err",
			UserID:        userID,
			Name:          "Pre Err Branch",
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := store.CreateBranch(ctx, branch); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}
		rec, err := store.ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}
		ver := &core.BranchVersion{
			ID:                "ver-pre-err",
			BranchID:          branch.ID,
			Status:            core.VersionStatusPending,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: core.TemplateStandardCleanID,
		}
		if err := store.CreateVersion(ctx, ver); err != nil {
			t.Fatalf("CreateVersion failed: %v", err)
		}

		pool := worker.New(worker.Config{
			Concurrency: 1,
			QueueSize:   5,
			Branches:    store,
			Profiles:    store,
			Presets:     store,
			Templates:   store,
			Quotas:      store,
			Generator:   &mockLLM{},
			Validator:   provenance.New(),
			Renderer:    renderer.New(),
		})
		pool.Start(ctx)
		defer pool.Stop()

		if err := pool.Enqueue(core.GenerationJob{
			UserID:        userID,
			BranchID:      "missing-branch-id",
			VersionID:     ver.ID,
			ReservationID: rec.ID,
		}); err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}

		gotVer := waitForVersionTerminal(ctx, t, store, branch.ID, ver.ID)
		if gotVer.Status != core.VersionStatusFailed {
			t.Fatalf("expected Failed, got %s", gotVer.Status)
		}

		gotRec := waitForUsageSettled(ctx, t, store, userID, rec.ID)
		if gotRec.Status != core.ReservationStatusReleased {
			t.Fatalf("expected Released, got %s", gotRec.Status)
		}
		assertTelemetryFields(t, gotRec, core.LLMTelemetry{}, 0)
	})
}

func TestPool_PDFParseJob_Telemetry(t *testing.T) {
	ctx := context.Background()

	t.Run("success commits full telemetry preserving actual metadata tokens", func(t *testing.T) {
		store := memory.New()
		userID := "user-pdf-tel-ok"
		rec, err := store.ReserveQuota(ctx, userID, core.OperationPDFParse, 1000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}

		jobID := "pdf-tel-ok"
		if err := store.CreateJob(ctx, &core.PDFParseJob{
			ID:     jobID,
			UserID: userID,
			Status: core.PDFParseJobStatusPending,
		}); err != nil {
			t.Fatalf("CreateJob failed: %v", err)
		}

		wantTel := core.LLMTelemetry{
			Provider:   "gemini",
			Model:      "gemini-2.5-flash",
			StatusCode: 200,
			Latency:    65 * time.Millisecond,
			Usage:      core.TokenUsage{PromptTokens: 210, CompletionTokens: 90},
		}
		ext := &mockResumeExtractor{
			telemetry: wantTel,
			result:    &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Ada Lovelace"}},
		}

		pool := worker.New(worker.Config{
			Concurrency:     1,
			QueueSize:       5,
			PDFJobs:         store,
			Quotas:          store,
			ResumeExtractor: ext,
			LLMTimeout:      5 * time.Second,
		})
		pool.Start(ctx)
		defer pool.Stop()

		if err := pool.EnqueuePDFJob(core.PDFParseJobTask{
			JobID:         jobID,
			UserID:        userID,
			RawText:       "Ada Lovelace resume",
			Provider:      "gemini",
			Model:         "gemini-2.5-flash",
			ReservationID: rec.ID,
		}); err != nil {
			t.Fatalf("EnqueuePDFJob failed: %v", err)
		}

		gotJob := waitForPDFJobTerminal(ctx, t, store, userID, jobID)
		if gotJob.Status != core.PDFParseJobStatusCompleted {
			t.Fatalf("expected Completed, got %s (%s)", gotJob.Status, gotJob.ErrorMessage)
		}

		gotRec := waitForUsageSettled(ctx, t, store, userID, rec.ID)
		if gotRec.Status != core.ReservationStatusCommitted {
			t.Fatalf("expected Committed, got %s", gotRec.Status)
		}
		assertTelemetryFields(t, gotRec, wantTel, 300)

		quota, err := store.Quota(ctx, userID)
		if err != nil {
			t.Fatalf("Quota failed: %v", err)
		}
		if quota.MonthlyTokensUsed != 300 || quota.DailyRequestsUsed != 1 {
			t.Errorf("quota = (tokens=%d, reqs=%d), want (300, 1)", quota.MonthlyTokensUsed, quota.DailyRequestsUsed)
		}
	})

	t.Run("success with zero tokens applies defaultPDFExtractionTokens fallback", func(t *testing.T) {
		store := memory.New()
		userID := "user-pdf-tel-fb"
		rec, err := store.ReserveQuota(ctx, userID, core.OperationPDFParse, 1000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}

		jobID := "pdf-tel-fb"
		if err := store.CreateJob(ctx, &core.PDFParseJob{
			ID:     jobID,
			UserID: userID,
			Status: core.PDFParseJobStatusPending,
		}); err != nil {
			t.Fatalf("CreateJob failed: %v", err)
		}

		wantTel := core.LLMTelemetry{
			Provider:   "ollama",
			Model:      "llama3.1",
			StatusCode: 200,
			Latency:    120 * time.Millisecond,
		}
		ext := &mockResumeExtractor{
			telemetry: wantTel,
			result:    &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Alan Turing"}},
		}

		pool := worker.New(worker.Config{
			Concurrency:     1,
			QueueSize:       5,
			PDFJobs:         store,
			Quotas:          store,
			ResumeExtractor: ext,
			LLMTimeout:      5 * time.Second,
		})
		pool.Start(ctx)
		defer pool.Stop()

		if err := pool.EnqueuePDFJob(core.PDFParseJobTask{
			JobID:         jobID,
			UserID:        userID,
			RawText:       "Alan Turing resume",
			ReservationID: rec.ID,
		}); err != nil {
			t.Fatalf("EnqueuePDFJob failed: %v", err)
		}

		gotJob := waitForPDFJobTerminal(ctx, t, store, userID, jobID)
		if gotJob.Status != core.PDFParseJobStatusCompleted {
			t.Fatalf("expected Completed, got %s", gotJob.Status)
		}

		gotRec := waitForUsageSettled(ctx, t, store, userID, rec.ID)
		if gotRec.Status != core.ReservationStatusCommitted {
			t.Fatalf("expected Committed, got %s", gotRec.Status)
		}
		assertTelemetryFields(t, gotRec, wantTel, 1000)
	})

	errCases := []struct {
		name      string
		telemetry core.LLMTelemetry
		err       error
		wantTotal int
	}{
		{
			name: "429 rate limit releases quota with telemetry",
			telemetry: core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o",
				StatusCode: 429,
				Latency:    30 * time.Millisecond,
			},
			err:       errors.New("rate limited"),
			wantTotal: 0,
		},
		{
			name: "timeout releases quota with zero status and latency",
			telemetry: core.LLMTelemetry{
				Provider:   "anthropic",
				Model:      "claude-sonnet-4-5",
				StatusCode: 0,
				Latency:    200 * time.Millisecond,
			},
			err:       context.DeadlineExceeded,
			wantTotal: 0,
		},
		{
			name: "invalid response preserves partial telemetry without fallback",
			telemetry: core.LLMTelemetry{
				Provider:   "gemini",
				Model:      "gemini-2.5-flash",
				StatusCode: 200,
				Latency:    45 * time.Millisecond,
				Usage:      core.TokenUsage{PromptTokens: 140, CompletionTokens: 60},
			},
			err:       fmt.Errorf("%w: invalid JSON output", core.ErrValidation),
			wantTotal: 200,
		},
		{
			name:      "pre-LLM empty raw text validation releases zero telemetry",
			telemetry: core.LLMTelemetry{},
			err:       fmt.Errorf("%w: empty resume text", core.ErrValidation),
			wantTotal: 0,
		},
	}

	for i, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			store := memory.New()
			userID := fmt.Sprintf("user-pdf-err-%d", i)
			rec, err := store.ReserveQuota(ctx, userID, core.OperationPDFParse, 1000, 50, 100000)
			if err != nil {
				t.Fatalf("ReserveQuota failed: %v", err)
			}

			jobID := fmt.Sprintf("pdf-err-%d", i)
			if err := store.CreateJob(ctx, &core.PDFParseJob{
				ID:     jobID,
				UserID: userID,
				Status: core.PDFParseJobStatusPending,
			}); err != nil {
				t.Fatalf("CreateJob failed: %v", err)
			}

			ext := &mockResumeExtractor{
				failErr: core.AttachLLMTelemetry(tc.err, tc.telemetry),
			}
			pool := worker.New(worker.Config{
				Concurrency:     1,
				QueueSize:       5,
				PDFJobs:         store,
				Quotas:          store,
				ResumeExtractor: ext,
				LLMTimeout:      5 * time.Second,
			})
			pool.Start(ctx)
			defer pool.Stop()

			if err := pool.EnqueuePDFJob(core.PDFParseJobTask{
				JobID:         jobID,
				UserID:        userID,
				RawText:       "resume",
				ReservationID: rec.ID,
			}); err != nil {
				t.Fatalf("EnqueuePDFJob failed: %v", err)
			}

			gotJob := waitForPDFJobTerminal(ctx, t, store, userID, jobID)
			if gotJob.Status != core.PDFParseJobStatusFailed {
				t.Fatalf("expected Failed, got %s", gotJob.Status)
			}

			gotRec := waitForUsageSettled(ctx, t, store, userID, rec.ID)
			if gotRec.Status != core.ReservationStatusReleased {
				t.Fatalf("expected Released, got %s", gotRec.Status)
			}
			assertTelemetryFields(t, gotRec, tc.telemetry, tc.wantTotal)

			quota, err := store.Quota(ctx, userID)
			if err != nil {
				t.Fatalf("Quota failed: %v", err)
			}
			if quota.MonthlyTokensUsed != 0 || quota.DailyRequestsUsed != 0 {
				t.Errorf("expected refunded quota (0, 0), got (%d, %d)", quota.MonthlyTokensUsed, quota.DailyRequestsUsed)
			}
		})
	}

	t.Run("persistence failure releases quota while preserving telemetry", func(t *testing.T) {
		store := memory.New()
		userID := "user-pdf-persist-err"
		rec, err := store.ReserveQuota(ctx, userID, core.OperationPDFParse, 1000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}

		jobID := "pdf-persist-err"
		if err := store.CreateJob(ctx, &core.PDFParseJob{
			ID:     jobID,
			UserID: userID,
			Status: core.PDFParseJobStatusPending,
		}); err != nil {
			t.Fatalf("CreateJob failed: %v", err)
		}

		wantTel := core.LLMTelemetry{
			Provider:   "openai",
			Model:      "gpt-4o",
			StatusCode: 200,
			Latency:    52 * time.Millisecond,
			Usage:      core.TokenUsage{PromptTokens: 120, CompletionTokens: 80},
		}
		ext := &mockResumeExtractor{
			telemetry: wantTel,
			result:    &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Grace Hopper"}},
		}

		pool := worker.New(worker.Config{
			Concurrency:     1,
			QueueSize:       5,
			PDFJobs:         &failingCompletePDFStore{PDFJobStore: store, completeErr: errors.New("write failed")},
			Quotas:          store,
			ResumeExtractor: ext,
			LLMTimeout:      5 * time.Second,
		})
		pool.Start(ctx)
		defer pool.Stop()

		if err := pool.EnqueuePDFJob(core.PDFParseJobTask{
			JobID:         jobID,
			UserID:        userID,
			RawText:       "Grace Hopper resume",
			ReservationID: rec.ID,
		}); err != nil {
			t.Fatalf("EnqueuePDFJob failed: %v", err)
		}

		gotJob := waitForPDFJobTerminal(ctx, t, store, userID, jobID)
		if gotJob.Status != core.PDFParseJobStatusFailed {
			t.Fatalf("expected Failed, got %s", gotJob.Status)
		}

		gotRec := waitForUsageSettled(ctx, t, store, userID, rec.ID)
		if gotRec.Status != core.ReservationStatusReleased {
			t.Fatalf("expected Released, got %s", gotRec.Status)
		}
		assertTelemetryFields(t, gotRec, wantTel, 200)
	})
}

func TestPool_ConcurrentMixedTelemetry_Race(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	const tmplID = "tmpl-race-minimal"
	if err := store.CreateTemplate(ctx, &core.TemplateSet{
		ID:               tmplID,
		UserID:           core.SystemUserID,
		Name:             "Minimal Race Template",
		IsBuiltIn:        true,
		HTMLTemplate:     "<p>{{.Profile.FullName}}</p>",
		MarkdownTemplate: "# {{.Profile.FullName}}",
		TextTemplate:     "{{.Profile.FullName}}",
	}); err != nil {
		t.Fatalf("CreateTemplate failed: %v", err)
	}

	gen := &callbackLLM{
		fn: func(_ context.Context, req core.GenerationRequest) (*core.GenerationResponse, error) {
			time.Sleep(5 * time.Millisecond)
			if strings.Contains(req.BranchContext.Name, "fail-429") {
				return nil, core.AttachLLMTelemetry(errors.New("rate limited"), core.LLMTelemetry{
					Provider:   "anthropic",
					Model:      "claude-sonnet-4-5",
					StatusCode: 429,
					Latency:    11 * time.Millisecond,
				})
			}
			return &core.GenerationResponse{
				TailoredContent: core.TailoredContent{Summary: "Race summary"},
				GapReport:       core.GapReport{OverallMatchSummary: "Good"},
				Telemetry: core.LLMTelemetry{
					Provider:   "openai",
					Model:      "gpt-4o",
					StatusCode: 200,
					Latency:    20 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 150, CompletionTokens: 100},
				},
			}, nil
		},
	}

	ext := &callbackResumeExtractor{
		fnTel: func(_ context.Context, text, _, _ string) (*core.JSONResume, core.LLMTelemetry, error) {
			time.Sleep(5 * time.Millisecond)
			if strings.Contains(text, "fail-timeout") {
				return nil, core.LLMTelemetry{}, core.AttachLLMTelemetry(context.DeadlineExceeded, core.LLMTelemetry{
					Provider:   "gemini",
					Model:      "gemini-2.5-flash",
					StatusCode: 0,
					Latency:    30 * time.Millisecond,
				})
			}
			if strings.Contains(text, "zero-tokens") {
				return &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Zero"}}, core.LLMTelemetry{
					Provider:   "ollama",
					Model:      "llama3.1",
					StatusCode: 200,
					Latency:    15 * time.Millisecond,
				}, nil
			}
			return &core.JSONResume{Basics: core.JSONResumeBasics{Name: "Parsed"}}, core.LLMTelemetry{
				Provider:   "gemini",
				Model:      "gemini-2.5-flash",
				StatusCode: 200,
				Latency:    18 * time.Millisecond,
				Usage:      core.TokenUsage{PromptTokens: 90, CompletionTokens: 60},
			}, nil
		},
	}

	pool := worker.New(worker.Config{
		Concurrency:            4,
		MaxConcurrentPerTenant: 2,
		QueueSize:              64,
		Branches:               store,
		Profiles:               store,
		Presets:                store,
		Templates:              store,
		PDFJobs:                store,
		Quotas:                 store,
		ResumeExtractor:        ext,
		Generator:              gen,
		Validator:              provenance.New(),
		Renderer:               renderer.New(),
		LLMTimeout:             5 * time.Second,
	})
	pool.Start(ctx)

	const tenantCount = 4
	type expectedTenantTotals struct {
		commits   int
		releases  int
		tokens    int
		versionID string
		branchID  string
	}
	expected := make(map[string]expectedTenantTotals, tenantCount)

	for i := 0; i < tenantCount; i++ {
		userID := fmt.Sprintf("race-tenant-%d", i)
		if err := store.UpsertProfile(ctx, &core.Profile{
			ID:       "profile-" + userID,
			UserID:   userID,
			FullName: "Candidate " + userID,
		}); err != nil {
			t.Fatalf("UpsertProfile failed: %v", err)
		}

		okBranch := &core.Branch{
			ID:            fmt.Sprintf("branch-ok-%d", i),
			UserID:        userID,
			Name:          "ok-tailor",
			TemplateSetID: tmplID,
		}
		if err := store.CreateBranch(ctx, okBranch); err != nil {
			t.Fatalf("CreateBranch ok failed: %v", err)
		}
		okVer := &core.BranchVersion{
			ID:                fmt.Sprintf("ver-ok-%d", i),
			BranchID:          okBranch.ID,
			Status:            core.VersionStatusPending,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: tmplID,
		}
		if err := store.CreateVersion(ctx, okVer); err != nil {
			t.Fatalf("CreateVersion ok failed: %v", err)
		}
		recGenOK, err := store.ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota gen ok failed: %v", err)
		}
		if err := pool.Enqueue(core.GenerationJob{
			UserID:        userID,
			BranchID:      okBranch.ID,
			VersionID:     okVer.ID,
			ReservationID: recGenOK.ID,
		}); err != nil {
			t.Fatalf("Enqueue gen ok failed: %v", err)
		}

		failBranch := &core.Branch{
			ID:            fmt.Sprintf("branch-fail-%d", i),
			UserID:        userID,
			Name:          "fail-429",
			TemplateSetID: tmplID,
		}
		if err := store.CreateBranch(ctx, failBranch); err != nil {
			t.Fatalf("CreateBranch fail failed: %v", err)
		}
		failVer := &core.BranchVersion{
			ID:                fmt.Sprintf("ver-fail-%d", i),
			BranchID:          failBranch.ID,
			Status:            core.VersionStatusPending,
			MainRevisionUsed:  1,
			TemplateSetIDUsed: tmplID,
		}
		if err := store.CreateVersion(ctx, failVer); err != nil {
			t.Fatalf("CreateVersion fail failed: %v", err)
		}
		recGenFail, err := store.ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota gen fail failed: %v", err)
		}
		if err := pool.Enqueue(core.GenerationJob{
			UserID:        userID,
			BranchID:      failBranch.ID,
			VersionID:     failVer.ID,
			ReservationID: recGenFail.ID,
		}); err != nil {
			t.Fatalf("Enqueue gen fail failed: %v", err)
		}

		pdfJobs := []struct {
			suffix string
			raw    string
		}{
			{"ok", "resume-ok"},
			{"zero", "resume-zero-tokens"},
			{"timeout", "resume-fail-timeout"},
		}
		for _, pj := range pdfJobs {
			jobID := fmt.Sprintf("pdf-%s-%d", pj.suffix, i)
			if err := store.CreateJob(ctx, &core.PDFParseJob{
				ID:     jobID,
				UserID: userID,
				Status: core.PDFParseJobStatusPending,
			}); err != nil {
				t.Fatalf("CreateJob %s failed: %v", jobID, err)
			}
			recPDF, err := store.ReserveQuota(ctx, userID, core.OperationPDFParse, 1000, 50, 100000)
			if err != nil {
				t.Fatalf("ReserveQuota %s failed: %v", jobID, err)
			}
			if err := pool.EnqueuePDFJob(core.PDFParseJobTask{
				JobID:         jobID,
				UserID:        userID,
				RawText:       pj.raw,
				ReservationID: recPDF.ID,
			}); err != nil {
				t.Fatalf("EnqueuePDFJob %s failed: %v", jobID, err)
			}
		}

		expected[userID] = expectedTenantTotals{
			commits:   3, // tailor ok (250) + pdf ok (150) + pdf zero (1000)
			releases:  2, // tailor 429 + pdf timeout
			tokens:    250 + 150 + 1000,
			versionID: okVer.ID,
			branchID:  okBranch.ID,
		}
	}

	pool.Stop()

	for userID, want := range expected {
		ver, err := store.Version(ctx, want.branchID, want.versionID)
		if err != nil {
			t.Fatalf("Version(%s) failed: %v", want.versionID, err)
		}
		if ver.Status != core.VersionStatusCompleted {
			t.Errorf("tenant %s version status = %s (%s), want completed", userID, ver.Status, ver.ErrorMessage)
		}
		if ver.ProviderUsed != "openai" || ver.ModelUsed != "gpt-4o" {
			t.Errorf("tenant %s version ProviderUsed/ModelUsed = (%q, %q), want (openai, gpt-4o)", userID, ver.ProviderUsed, ver.ModelUsed)
		}

		records, err := store.ListUsageRecords(ctx, userID, 20)
		if err != nil {
			t.Fatalf("ListUsageRecords(%s) failed: %v", userID, err)
		}
		if len(records) != want.commits+want.releases {
			t.Fatalf("tenant %s usage records count = %d, want %d", userID, len(records), want.commits+want.releases)
		}

		var gotCommits, gotReleases, gotTokens int
		for _, r := range records {
			switch r.Status {
			case core.ReservationStatusCommitted:
				gotCommits++
				gotTokens += r.TotalTokens
				if r.Provider == "" || r.Model == "" || r.StatusCode != 200 || r.Latency <= 0 {
					t.Errorf("tenant %s committed record missing telemetry: %+v", userID, r)
				}
			case core.ReservationStatusReleased:
				gotReleases++
				if r.Provider == "" || r.Model == "" || r.Latency <= 0 || r.ErrorMessage == "" {
					t.Errorf("tenant %s released record missing telemetry: %+v", userID, r)
				}
			default:
				t.Errorf("tenant %s record %s remained in non-terminal status %s", userID, r.ID, r.Status)
			}
		}
		if gotCommits != want.commits || gotReleases != want.releases || gotTokens != want.tokens {
			t.Errorf("tenant %s usage summary = (commits=%d, releases=%d, tokens=%d), want (%d, %d, %d)",
				userID, gotCommits, gotReleases, gotTokens, want.commits, want.releases, want.tokens)
		}

		quota, err := store.Quota(ctx, userID)
		if err != nil {
			t.Fatalf("Quota(%s) failed: %v", userID, err)
		}
		if quota.DailyRequestsUsed != want.commits || quota.MonthlyTokensUsed != want.tokens {
			t.Errorf("tenant %s quota = (daily=%d, monthly=%d), want (%d, %d)",
				userID, quota.DailyRequestsUsed, quota.MonthlyTokensUsed, want.commits, want.tokens)
		}
	}
}
