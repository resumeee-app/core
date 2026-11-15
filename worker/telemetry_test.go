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
	"github.com/resumeee-app/resumeee-core/store"
	"github.com/resumeee-app/resumeee-core/worker"
)

type failingValidator struct {
	err error
}

func (v *failingValidator) Validate(_ *core.TailoredContent, _ *core.MainProfileSnapshot, _ *core.Branch) (*core.ValidationResult, error) {
	return nil, v.err
}

type failingCompleteBranchStore struct {
	worker.JobStore
	completeErr error
}

func (s *failingCompleteBranchStore) CompleteBranchTailor(_ context.Context, _, _ string, _ *core.BranchVersion) error {
	return s.completeErr
}

type failingCompletePDFStore struct {
	worker.JobStore
	completeErr error
}

func (s *failingCompletePDFStore) CompletePDFParse(_ context.Context, _, _ string, _ *core.JSONResume) error {
	return s.completeErr
}

func waitForVersionTerminal(ctx context.Context, t *testing.T, brReader worker.BranchReader, branchID, versionID string) *core.BranchVersion {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		ver, err := brReader.Version(ctx, branchID, versionID)
		if err == nil && (ver.Status == core.VersionStatusCompleted || ver.Status == core.VersionStatusCompletedWithWarnings || ver.Status == core.VersionStatusFailed) {
			return ver
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for version %s to reach terminal state", versionID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForPDFJobTerminal(ctx context.Context, t *testing.T, jobs worker.JobStore, userID, jobID string) *core.Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		job, err := jobs.Job(ctx, userID, jobID)
		if err == nil && (job.Status == core.JobStatusCompleted || job.Status == core.JobStatusFailed) {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for PDF job %s to reach terminal state", jobID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForUsageSettled(ctx context.Context, t *testing.T, quotas *store.Quotas, userID, reservationID string) core.LLMUsageRecord {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		records, err := quotas.ListUsageRecords(ctx, userID, 50)
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

func submitTelemetryTailorJob(ctx context.Context, t *testing.T, st *store.Store, userID, branchID, verID, reservationID, tmplID, provider, model string) {
	t.Helper()
	now := time.Now()
	ver := &core.BranchVersion{
		ID:                verID,
		BranchID:          branchID,
		UserID:            userID,
		MainRevisionUsed:  1,
		TemplateSetIDUsed: tmplID,
		ProviderUsed:      provider,
		ModelUsed:         model,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	job := &core.Job{
		ID:            "job-" + verID,
		Kind:          core.JobKindBranchTailor,
		UserID:        userID,
		ReservationID: reservationID,
		Tailor: &core.BranchTailorSpec{
			BranchID:  branchID,
			VersionID: verID,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if _, err := st.Jobs().SubmitBranchTailor(ctx, job, ver, core.PendingJobOptions{}); err != nil {
		t.Fatalf("SubmitBranchTailor failed: %v", err)
	}
}

func submitTelemetryPDFJob(ctx context.Context, t *testing.T, st *store.Store, userID, jobID, rawText, provider, model, reservationID string) {
	t.Helper()
	now := time.Now()
	if err := st.Jobs().SubmitPDFParse(ctx, &core.Job{
		ID:            jobID,
		Kind:          core.JobKindPDFParse,
		UserID:        userID,
		ReservationID: reservationID,
		PDF: &core.PDFParseSpec{
			RawText:  rawText,
			Provider: provider,
			Model:    model,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, core.PendingJobOptions{}); err != nil {
		t.Fatalf("SubmitPDFParse failed: %v", err)
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
		st, err := memory.NewSeeded(ctx, "user-tailor-tel-1")
		if err != nil {
			t.Fatalf("NewSeeded failed: %v", err)
		}

		br := &core.Branch{
			ID:            "branch-empty-overrides",
			UserID:        "user-tailor-tel-1",
			Name:          "Backend Staff",
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := st.Branches().CreateBranch(ctx, br); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}

		rec, err := st.Quotas().ReserveQuota(ctx, "user-tailor-tel-1", core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}

		const verID = "ver-tel-1"
		submitTelemetryTailorJob(ctx, t, st, "user-tailor-tel-1", br.ID, verID, rec.ID, core.TemplateStandardCleanID, "", "")

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
			Concurrency:  1,
			PollInterval: 10 * time.Millisecond,
			MaxAttempts:  1,
			Jobs:         st.Jobs(),
			Branches:     st.Branches(),
			Profiles:     st.Profiles(),
			Presets:      st.Presets(),
			Templates:    st.Templates(),
			Quotas:       st.Quotas(),
			Generator:    gen,
			Validator:    provenance.New(),
			Renderer:     renderer.New(),
		})
		pool.Start(ctx)
		defer pool.Stop()
		pool.Notify()

		gotVer := waitForVersionTerminal(ctx, t, st.Branches(), br.ID, verID)
		if gotVer.Status != core.VersionStatusCompleted {
			t.Fatalf("expected Completed, got %s (%s)", gotVer.Status, gotVer.ErrorMessage)
		}
		if gotVer.ProviderUsed != "openai" || gotVer.ModelUsed != "gpt-4o" {
			t.Errorf("ProviderUsed/ModelUsed = (%q, %q), want (%q, %q)", gotVer.ProviderUsed, gotVer.ModelUsed, "openai", "gpt-4o")
		}

		gotRec := waitForUsageSettled(ctx, t, st.Quotas(), "user-tailor-tel-1", rec.ID)
		if gotRec.Status != core.ReservationStatusCommitted {
			t.Fatalf("expected Committed, got %s", gotRec.Status)
		}
		assertTelemetryFields(t, gotRec, wantTel, 500)

		quota, err := st.Quotas().Quota(ctx, "user-tailor-tel-1")
		if err != nil {
			t.Fatalf("Quota failed: %v", err)
		}
		if quota.MonthlyTokensUsed != 500 || quota.DailyRequestsUsed != 1 {
			t.Errorf("quota = (tokens=%d, reqs=%d), want (500, 1)", quota.MonthlyTokensUsed, quota.DailyRequestsUsed)
		}
	})

	t.Run("telemetry overrides stale branch values and legacy Usage backfills via EffectiveTelemetry", func(t *testing.T) {
		st, err := memory.NewSeeded(ctx, "user-tailor-tel-2")
		if err != nil {
			t.Fatalf("NewSeeded failed: %v", err)
		}

		br := &core.Branch{
			ID:               "branch-override-precedence",
			UserID:           "user-tailor-tel-2",
			Name:             "Platform Engineer",
			TemplateSetID:    core.TemplateStandardCleanID,
			ProviderOverride: "compat",
			ModelOverride:    "alias-default",
		}
		if err := st.Branches().CreateBranch(ctx, br); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}

		rec, err := st.Quotas().ReserveQuota(ctx, "user-tailor-tel-2", core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}

		const verID = "ver-tel-2"
		submitTelemetryTailorJob(ctx, t, st, "user-tailor-tel-2", br.ID, verID, rec.ID, core.TemplateStandardCleanID, br.ProviderOverride, br.ModelOverride)

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
			Concurrency:  1,
			PollInterval: 10 * time.Millisecond,
			MaxAttempts:  1,
			Jobs:         st.Jobs(),
			Branches:     st.Branches(),
			Profiles:     st.Profiles(),
			Presets:      st.Presets(),
			Templates:    st.Templates(),
			Quotas:       st.Quotas(),
			Generator:    gen,
			Validator:    provenance.New(),
			Renderer:     renderer.New(),
		})
		pool.Start(ctx)
		defer pool.Stop()
		pool.Notify()

		gotVer := waitForVersionTerminal(ctx, t, st.Branches(), br.ID, verID)
		if gotVer.ProviderUsed != "openaicompat" || gotVer.ModelUsed != "llama-3.3-70b" {
			t.Errorf("ProviderUsed/ModelUsed = (%q, %q), want (%q, %q)", gotVer.ProviderUsed, gotVer.ModelUsed, "openaicompat", "llama-3.3-70b")
		}

		gotRec := waitForUsageSettled(ctx, t, st.Quotas(), "user-tailor-tel-2", rec.ID)
		assertTelemetryFields(t, gotRec, core.LLMTelemetry{
			Provider:   "openaicompat",
			Model:      "llama-3.3-70b",
			StatusCode: 200,
			Latency:    88 * time.Millisecond,
			Usage:      core.TokenUsage{PromptTokens: 110, CompletionTokens: 90},
		}, 200)
	})

	t.Run("empty telemetry falls back to branch overrides and default tailoring tokens", func(t *testing.T) {
		st, err := memory.NewSeeded(ctx, "user-tailor-tel-3")
		if err != nil {
			t.Fatalf("NewSeeded failed: %v", err)
		}

		br := &core.Branch{
			ID:               "branch-fallback-overrides",
			UserID:           "user-tailor-tel-3",
			Name:             "Fallback Test",
			TemplateSetID:    core.TemplateStandardCleanID,
			ProviderOverride: "anthropic",
			ModelOverride:    "claude-sonnet-4-5",
		}
		if err := st.Branches().CreateBranch(ctx, br); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}

		rec, err := st.Quotas().ReserveQuota(ctx, "user-tailor-tel-3", core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}

		const verID = "ver-tel-3"
		submitTelemetryTailorJob(ctx, t, st, "user-tailor-tel-3", br.ID, verID, rec.ID, core.TemplateStandardCleanID, "", "")

		pool := worker.New(worker.Config{
			Concurrency:  1,
			PollInterval: 10 * time.Millisecond,
			MaxAttempts:  1,
			Jobs:         st.Jobs(),
			Branches:     st.Branches(),
			Profiles:     st.Profiles(),
			Presets:      st.Presets(),
			Templates:    st.Templates(),
			Quotas:       st.Quotas(),
			Generator:    &callbackLLM{},
			Validator:    provenance.New(),
			Renderer:     renderer.New(),
		})
		pool.Start(ctx)
		defer pool.Stop()
		pool.Notify()

		gotVer := waitForVersionTerminal(ctx, t, st.Branches(), br.ID, verID)
		if gotVer.ProviderUsed != "anthropic" || gotVer.ModelUsed != "claude-sonnet-4-5" {
			t.Errorf("ProviderUsed/ModelUsed = (%q, %q), want (%q, %q)", gotVer.ProviderUsed, gotVer.ModelUsed, "anthropic", "claude-sonnet-4-5")
		}

		gotRec := waitForUsageSettled(ctx, t, st.Quotas(), "user-tailor-tel-3", rec.ID)
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
			st, err := memory.NewSeeded(ctx, userID)
			if err != nil {
				t.Fatalf("NewSeeded failed: %v", err)
			}
			br := &core.Branch{
				ID:            fmt.Sprintf("branch-err-%d", i),
				UserID:        userID,
				Name:          "Err Branch",
				TemplateSetID: core.TemplateStandardCleanID,
			}
			if err := st.Branches().CreateBranch(ctx, br); err != nil {
				t.Fatalf("CreateBranch failed: %v", err)
			}
			rec, err := st.Quotas().ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 50, 100000)
			if err != nil {
				t.Fatalf("ReserveQuota failed: %v", err)
			}
			verID := fmt.Sprintf("ver-err-%d", i)
			submitTelemetryTailorJob(ctx, t, st, userID, br.ID, verID, rec.ID, core.TemplateStandardCleanID, "", "")

			gen := &mockLLM{
				failErr: core.AttachLLMTelemetry(tc.baseErr, tc.telemetry),
			}
			pool := worker.New(worker.Config{
				Concurrency:  1,
				PollInterval: 10 * time.Millisecond,
				MaxAttempts:  1,
				Jobs:         st.Jobs(),
				Branches:     st.Branches(),
				Profiles:     st.Profiles(),
				Presets:      st.Presets(),
				Templates:    st.Templates(),
				Quotas:       st.Quotas(),
				Generator:    gen,
				Validator:    provenance.New(),
				Renderer:     renderer.New(),
			})
			pool.Start(ctx)
			defer pool.Stop()
			pool.Notify()

			gotVer := waitForVersionTerminal(ctx, t, st.Branches(), br.ID, verID)
			if gotVer.Status != core.VersionStatusFailed {
				t.Fatalf("expected Failed, got %s", gotVer.Status)
			}
			if !strings.Contains(strings.ToLower(gotVer.ErrorMessage), tc.wantErr) {
				t.Errorf("ErrorMessage = %q, want substring %q", gotVer.ErrorMessage, tc.wantErr)
			}

			gotRec := waitForUsageSettled(ctx, t, st.Quotas(), userID, rec.ID)
			if gotRec.Status != core.ReservationStatusReleased {
				t.Fatalf("expected Released, got %s", gotRec.Status)
			}
			assertTelemetryFields(t, gotRec, tc.telemetry, tc.wantTotal)

			quota, err := st.Quotas().Quota(ctx, userID)
			if err != nil {
				t.Fatalf("Quota failed: %v", err)
			}
			if quota.MonthlyTokensUsed != 0 || quota.DailyRequestsUsed != 0 {
				t.Errorf("expected refunded quota (0, 0), got (%d, %d)", quota.MonthlyTokensUsed, quota.DailyRequestsUsed)
			}
		})
	}

	t.Run("post-LLM provenance validation error commits consumed quota with telemetry without refunding", func(t *testing.T) {
		userID := "user-tailor-val-err"
		st, err := memory.NewSeeded(ctx, userID)
		if err != nil {
			t.Fatalf("NewSeeded failed: %v", err)
		}
		br := &core.Branch{
			ID:            "branch-val-err",
			UserID:        userID,
			Name:          "Val Err Branch",
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := st.Branches().CreateBranch(ctx, br); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}
		rec, err := st.Quotas().ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}
		const verID = "ver-val-err"
		submitTelemetryTailorJob(ctx, t, st, userID, br.ID, verID, rec.ID, core.TemplateStandardCleanID, "", "")

		wantTel := core.LLMTelemetry{
			Provider:   "openai",
			Model:      "gpt-4o",
			StatusCode: 200,
			Latency:    55 * time.Millisecond,
			Usage:      core.TokenUsage{PromptTokens: 200, CompletionTokens: 100},
		}
		pool := worker.New(worker.Config{
			Concurrency:  1,
			PollInterval: 10 * time.Millisecond,
			MaxAttempts:  1,
			Jobs:         st.Jobs(),
			Branches:     st.Branches(),
			Profiles:     st.Profiles(),
			Presets:      st.Presets(),
			Templates:    st.Templates(),
			Quotas:       st.Quotas(),
			Generator:    &mockLLM{telemetry: wantTel},
			Validator:    &failingValidator{err: errors.New("hallucinated claim")},
			Renderer:     renderer.New(),
		})
		pool.Start(ctx)
		defer pool.Stop()
		pool.Notify()

		gotVer := waitForVersionTerminal(ctx, t, st.Branches(), br.ID, verID)
		if gotVer.Status != core.VersionStatusFailed {
			t.Fatalf("expected Failed, got %s", gotVer.Status)
		}
		if strings.Contains(gotVer.ErrorMessage, "hallucinated claim") {
			t.Errorf("expected raw validation error redacted from ErrorMessage, got %q", gotVer.ErrorMessage)
		}

		gotRec := waitForUsageSettled(ctx, t, st.Quotas(), userID, rec.ID)
		if gotRec.Status != core.ReservationStatusCommitted {
			t.Fatalf("expected Committed after successful LLM generation, got %s", gotRec.Status)
		}
		assertTelemetryFields(t, gotRec, wantTel, 300)

		quota, err := st.Quotas().Quota(ctx, userID)
		if err != nil {
			t.Fatalf("Quota failed: %v", err)
		}
		if quota.MonthlyTokensUsed != 300 || quota.DailyRequestsUsed != 1 {
			t.Errorf("expected committed quota (300, 1), got (%d, %d)", quota.MonthlyTokensUsed, quota.DailyRequestsUsed)
		}
	})

	t.Run("post-LLM CompleteBranchTailor persistence failure commits consumed quota with fallback tokens and telemetry", func(t *testing.T) {
		userID := "user-tailor-persist-err"
		st, err := memory.NewSeeded(ctx, userID)
		if err != nil {
			t.Fatalf("NewSeeded failed: %v", err)
		}
		br := &core.Branch{
			ID:            "branch-persist-err",
			UserID:        userID,
			Name:          "Persist Err Branch",
			TemplateSetID: core.TemplateStandardCleanID,
		}
		if err := st.Branches().CreateBranch(ctx, br); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}
		rec, err := st.Quotas().ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}
		const verID = "ver-persist-err"
		submitTelemetryTailorJob(ctx, t, st, userID, br.ID, verID, rec.ID, core.TemplateStandardCleanID, "", "")

		wantTel := core.LLMTelemetry{
			Provider:   "gemini",
			Model:      "gemini-2.5-pro",
			StatusCode: 200,
			Latency:    70 * time.Millisecond,
		}
		pool := worker.New(worker.Config{
			Concurrency:  1,
			PollInterval: 10 * time.Millisecond,
			MaxAttempts:  1,
			Jobs:         &failingCompleteBranchStore{JobStore: st.Jobs(), completeErr: errors.New("disk full")},
			Branches:     st.Branches(),
			Profiles:     st.Profiles(),
			Presets:      st.Presets(),
			Templates:    st.Templates(),
			Quotas:       st.Quotas(),
			Generator:    &mockLLM{telemetry: wantTel},
			Validator:    provenance.New(),
			Renderer:     renderer.New(),
		})
		pool.Start(ctx)
		defer pool.Stop()
		pool.Notify()

		gotVer := waitForVersionTerminal(ctx, t, st.Branches(), br.ID, verID)
		if gotVer.Status != core.VersionStatusFailed {
			t.Fatalf("expected Failed, got %s", gotVer.Status)
		}

		gotRec := waitForUsageSettled(ctx, t, st.Quotas(), userID, rec.ID)
		if gotRec.Status != core.ReservationStatusCommitted {
			t.Fatalf("expected Committed after successful LLM generation, got %s", gotRec.Status)
		}
		// Consumed LLM generation succeeded before CompleteBranchTailor failed, so fallback tokens and telemetry are committed.
		assertTelemetryFields(t, gotRec, wantTel, 2000)
	})

	t.Run("pre-LLM load error releases quota with zero telemetry", func(t *testing.T) {
		userID := "user-tailor-pre-err"
		st, err := memory.NewSeeded(ctx, userID)
		if err != nil {
			t.Fatalf("NewSeeded failed: %v", err)
		}
		br := &core.Branch{
			ID:            "branch-pre-err",
			UserID:        userID,
			Name:          "Pre Err Branch",
			TemplateSetID: "missing-template-set-id",
		}
		if err := st.Branches().CreateBranch(ctx, br); err != nil {
			t.Fatalf("CreateBranch failed: %v", err)
		}
		rec, err := st.Quotas().ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}
		const verID = "ver-pre-err"
		submitTelemetryTailorJob(ctx, t, st, userID, br.ID, verID, rec.ID, "missing-template-set-id", "", "")

		pool := worker.New(worker.Config{
			Concurrency:  1,
			PollInterval: 10 * time.Millisecond,
			MaxAttempts:  1,
			Jobs:         st.Jobs(),
			Branches:     st.Branches(),
			Profiles:     st.Profiles(),
			Presets:      st.Presets(),
			Templates:    st.Templates(),
			Quotas:       st.Quotas(),
			Generator:    &mockLLM{},
			Validator:    provenance.New(),
			Renderer:     renderer.New(),
		})
		pool.Start(ctx)
		defer pool.Stop()
		pool.Notify()

		gotVer := waitForVersionTerminal(ctx, t, st.Branches(), br.ID, verID)
		if gotVer.Status != core.VersionStatusFailed {
			t.Fatalf("expected Failed, got %s", gotVer.Status)
		}

		gotRec := waitForUsageSettled(ctx, t, st.Quotas(), userID, rec.ID)
		if gotRec.Status != core.ReservationStatusReleased {
			t.Fatalf("expected Released, got %s", gotRec.Status)
		}
		assertTelemetryFields(t, gotRec, core.LLMTelemetry{}, 0)
	})
}

func TestPool_PDFParseJob_Telemetry(t *testing.T) {
	ctx := context.Background()

	t.Run("success commits full telemetry preserving actual metadata tokens", func(t *testing.T) {
		st := memory.New()
		userID := "user-pdf-tel-ok"
		rec, err := st.Quotas().ReserveQuota(ctx, userID, core.OperationPDFParse, 1000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}

		const jobID = "pdf-tel-ok"
		submitTelemetryPDFJob(ctx, t, st, userID, jobID, "Ada Lovelace resume", "gemini", "gemini-2.5-flash", rec.ID)

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
			PollInterval:    10 * time.Millisecond,
			MaxAttempts:     1,
			Jobs:            st.Jobs(),
			Quotas:          st.Quotas(),
			ResumeExtractor: ext,
			LLMTimeout:      5 * time.Second,
		})
		pool.Start(ctx)
		defer pool.Stop()
		pool.Notify()

		gotJob := waitForPDFJobTerminal(ctx, t, st.Jobs(), userID, jobID)
		if gotJob.Status != core.JobStatusCompleted {
			t.Fatalf("expected Completed, got %s (%s)", gotJob.Status, gotJob.ErrorMessage)
		}

		gotRec := waitForUsageSettled(ctx, t, st.Quotas(), userID, rec.ID)
		if gotRec.Status != core.ReservationStatusCommitted {
			t.Fatalf("expected Committed, got %s", gotRec.Status)
		}
		assertTelemetryFields(t, gotRec, wantTel, 300)

		quota, err := st.Quotas().Quota(ctx, userID)
		if err != nil {
			t.Fatalf("Quota failed: %v", err)
		}
		if quota.MonthlyTokensUsed != 300 || quota.DailyRequestsUsed != 1 {
			t.Errorf("quota = (tokens=%d, reqs=%d), want (300, 1)", quota.MonthlyTokensUsed, quota.DailyRequestsUsed)
		}
	})

	t.Run("success with zero tokens applies defaultPDFExtractionTokens fallback", func(t *testing.T) {
		st := memory.New()
		userID := "user-pdf-tel-fb"
		rec, err := st.Quotas().ReserveQuota(ctx, userID, core.OperationPDFParse, 1000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}

		const jobID = "pdf-tel-fb"
		submitTelemetryPDFJob(ctx, t, st, userID, jobID, "Alan Turing resume", "", "", rec.ID)

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
			PollInterval:    10 * time.Millisecond,
			MaxAttempts:     1,
			Jobs:            st.Jobs(),
			Quotas:          st.Quotas(),
			ResumeExtractor: ext,
			LLMTimeout:      5 * time.Second,
		})
		pool.Start(ctx)
		defer pool.Stop()
		pool.Notify()

		gotJob := waitForPDFJobTerminal(ctx, t, st.Jobs(), userID, jobID)
		if gotJob.Status != core.JobStatusCompleted {
			t.Fatalf("expected Completed, got %s", gotJob.Status)
		}

		gotRec := waitForUsageSettled(ctx, t, st.Quotas(), userID, rec.ID)
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
			st := memory.New()
			userID := fmt.Sprintf("user-pdf-err-%d", i)
			rec, err := st.Quotas().ReserveQuota(ctx, userID, core.OperationPDFParse, 1000, 50, 100000)
			if err != nil {
				t.Fatalf("ReserveQuota failed: %v", err)
			}

			jobID := fmt.Sprintf("pdf-err-%d", i)
			submitTelemetryPDFJob(ctx, t, st, userID, jobID, "resume", "", "", rec.ID)

			ext := &mockResumeExtractor{
				failErr: core.AttachLLMTelemetry(tc.err, tc.telemetry),
			}
			pool := worker.New(worker.Config{
				Concurrency:     1,
				PollInterval:    10 * time.Millisecond,
				MaxAttempts:     1,
				Jobs:            st.Jobs(),
				Quotas:          st.Quotas(),
				ResumeExtractor: ext,
				LLMTimeout:      5 * time.Second,
			})
			pool.Start(ctx)
			defer pool.Stop()
			pool.Notify()

			gotJob := waitForPDFJobTerminal(ctx, t, st.Jobs(), userID, jobID)
			if gotJob.Status != core.JobStatusFailed {
				t.Fatalf("expected Failed, got %s", gotJob.Status)
			}

			gotRec := waitForUsageSettled(ctx, t, st.Quotas(), userID, rec.ID)
			if gotRec.Status != core.ReservationStatusReleased {
				t.Fatalf("expected Released, got %s", gotRec.Status)
			}
			assertTelemetryFields(t, gotRec, tc.telemetry, tc.wantTotal)

			quota, err := st.Quotas().Quota(ctx, userID)
			if err != nil {
				t.Fatalf("Quota failed: %v", err)
			}
			if quota.MonthlyTokensUsed != 0 || quota.DailyRequestsUsed != 0 {
				t.Errorf("expected refunded quota (0, 0), got (%d, %d)", quota.MonthlyTokensUsed, quota.DailyRequestsUsed)
			}
		})
	}

	t.Run("persistence failure commits consumed quota with telemetry without refunding", func(t *testing.T) {
		st := memory.New()
		userID := "user-pdf-persist-err"
		rec, err := st.Quotas().ReserveQuota(ctx, userID, core.OperationPDFParse, 1000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota failed: %v", err)
		}

		const jobID = "pdf-persist-err"
		submitTelemetryPDFJob(ctx, t, st, userID, jobID, "Grace Hopper resume", "", "", rec.ID)

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
			PollInterval:    10 * time.Millisecond,
			MaxAttempts:     1,
			Jobs:            &failingCompletePDFStore{JobStore: st.Jobs(), completeErr: errors.New("write failed")},
			Quotas:          st.Quotas(),
			ResumeExtractor: ext,
			LLMTimeout:      5 * time.Second,
		})
		pool.Start(ctx)
		defer pool.Stop()
		pool.Notify()

		gotJob := waitForPDFJobTerminal(ctx, t, st.Jobs(), userID, jobID)
		if gotJob.Status != core.JobStatusFailed {
			t.Fatalf("expected Failed, got %s", gotJob.Status)
		}

		gotRec := waitForUsageSettled(ctx, t, st.Quotas(), userID, rec.ID)
		if gotRec.Status != core.ReservationStatusCommitted {
			t.Fatalf("expected Committed after successful LLM extraction, got %s", gotRec.Status)
		}
		assertTelemetryFields(t, gotRec, wantTel, 200)
	})
}

func TestPool_ConcurrentMixedTelemetry_Race(t *testing.T) {
	ctx := context.Background()
	st := memory.New()

	const tmplID = "tmpl-race-minimal"
	if err := st.Templates().CreateTemplate(ctx, &core.TemplateSet{
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
		PollInterval:           10 * time.Millisecond,
		MaxAttempts:            1,
		Jobs:                   st.Jobs(),
		Branches:               st.Branches(),
		Profiles:               st.Profiles(),
		Presets:                st.Presets(),
		Templates:              st.Templates(),
		Quotas:                 st.Quotas(),
		ResumeExtractor:        ext,
		Generator:              gen,
		Validator:              provenance.New(),
		Renderer:               renderer.New(),
		LLMTimeout:             5 * time.Second,
	})
	pool.Start(ctx)

	const tenantCount = 4
	type expectedTenantTotals struct {
		commits        int
		releases       int
		tokens         int
		versionID      string
		branchID       string
		reservationIDs []string
	}
	expected := make(map[string]expectedTenantTotals, tenantCount)

	for i := 0; i < tenantCount; i++ {
		userID := fmt.Sprintf("race-tenant-%d", i)
		if err := st.Profiles().UpsertProfile(ctx, &core.Profile{
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
		if err := st.Branches().CreateBranch(ctx, okBranch); err != nil {
			t.Fatalf("CreateBranch ok failed: %v", err)
		}
		recGenOK, err := st.Quotas().ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota gen ok failed: %v", err)
		}
		okVerID := fmt.Sprintf("ver-ok-%d", i)
		submitTelemetryTailorJob(ctx, t, st, userID, okBranch.ID, okVerID, recGenOK.ID, tmplID, "", "")

		failBranch := &core.Branch{
			ID:            fmt.Sprintf("branch-fail-%d", i),
			UserID:        userID,
			Name:          "fail-429",
			TemplateSetID: tmplID,
		}
		if err := st.Branches().CreateBranch(ctx, failBranch); err != nil {
			t.Fatalf("CreateBranch fail failed: %v", err)
		}
		recGenFail, err := st.Quotas().ReserveQuota(ctx, userID, core.OperationResumeTailoring, 2000, 50, 100000)
		if err != nil {
			t.Fatalf("ReserveQuota gen fail failed: %v", err)
		}
		failVerID := fmt.Sprintf("ver-fail-%d", i)
		submitTelemetryTailorJob(ctx, t, st, userID, failBranch.ID, failVerID, recGenFail.ID, tmplID, "", "")

		resIDs := []string{recGenOK.ID, recGenFail.ID}
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
			recPDF, err := st.Quotas().ReserveQuota(ctx, userID, core.OperationPDFParse, 1000, 50, 100000)
			if err != nil {
				t.Fatalf("ReserveQuota %s failed: %v", jobID, err)
			}
			submitTelemetryPDFJob(ctx, t, st, userID, jobID, pj.raw, "", "", recPDF.ID)
			resIDs = append(resIDs, recPDF.ID)
		}

		expected[userID] = expectedTenantTotals{
			commits:        3, // tailor ok (250) + pdf ok (150) + pdf zero (1000)
			releases:       2, // tailor 429 + pdf timeout
			tokens:         250 + 150 + 1000,
			versionID:      okVerID,
			branchID:       okBranch.ID,
			reservationIDs: resIDs,
		}
	}
	pool.Notify()

	for userID, want := range expected {
		for _, resID := range want.reservationIDs {
			waitForUsageSettled(ctx, t, st.Quotas(), userID, resID)
		}
	}

	pool.Stop()

	for userID, want := range expected {
		ver, err := st.Branches().Version(ctx, want.branchID, want.versionID)
		if err != nil {
			t.Fatalf("Version(%s) failed: %v", want.versionID, err)
		}
		if ver.Status != core.VersionStatusCompleted {
			t.Errorf("tenant %s version status = %s (%s), want completed", userID, ver.Status, ver.ErrorMessage)
		}
		if ver.ProviderUsed != "openai" || ver.ModelUsed != "gpt-4o" {
			t.Errorf("tenant %s version ProviderUsed/ModelUsed = (%q, %q), want (openai, gpt-4o)", userID, ver.ProviderUsed, ver.ModelUsed)
		}

		records, err := st.Quotas().ListUsageRecords(ctx, userID, 20)
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

		quota, err := st.Quotas().Quota(ctx, userID)
		if err != nil {
			t.Fatalf("Quota(%s) failed: %v", userID, err)
		}
		if quota.DailyRequestsUsed != want.commits || quota.MonthlyTokensUsed != want.tokens {
			t.Errorf("tenant %s quota = (daily=%d, monthly=%d), want (%d, %d)",
				userID, quota.DailyRequestsUsed, quota.MonthlyTokensUsed, want.commits, want.tokens)
		}
	}
}
