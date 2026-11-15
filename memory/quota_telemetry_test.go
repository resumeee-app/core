package memory_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	core "github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/memory"
)

func TestMemoryQuotaStore_CallTelemetryPersistence(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name      string
		commit    bool
		errMsg    string
		telemetry core.LLMTelemetry
		wantTotal int
	}{
		{
			name:   "committed call persists full telemetry and normalized tokens",
			commit: true,
			telemetry: core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o",
				StatusCode: 200,
				Latency:    42 * time.Millisecond,
				Usage: core.TokenUsage{
					PromptTokens:     120,
					CompletionTokens: 30,
				},
			},
			wantTotal: 150,
		},
		{
			name:   "released 429 call persists provider model status latency and error",
			commit: false,
			errMsg: "rate limit exceeded",
			telemetry: core.LLMTelemetry{
				Provider:   "anthropic",
				Model:      "claude-sonnet-4-20250514",
				StatusCode: 429,
				Latency:    18 * time.Millisecond,
			},
			wantTotal: 0,
		},
		{
			name:   "released incomplete 200 preserves partial tokens while refunding quota",
			commit: false,
			errMsg: "incomplete response",
			telemetry: core.LLMTelemetry{
				Provider:   "ollama",
				Model:      "llama3.1",
				StatusCode: 200,
				Latency:    95 * time.Millisecond,
				Usage: core.TokenUsage{
					PromptTokens:     50,
					CompletionTokens: 10,
				},
			},
			wantTotal: 60,
		},
		{
			name:   "released timeout persists zero status_code and elapsed latency",
			commit: false,
			errMsg: "context deadline exceeded",
			telemetry: core.LLMTelemetry{
				Provider:   "gemini",
				Model:      "gemini-2.5-flash",
				StatusCode: 0,
				Latency:    300 * time.Millisecond,
			},
			wantTotal: 0,
		},
	}

	for idx, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memory.New()
			userID := fmt.Sprintf("user-tel-%d", idx)
			rec, err := store.ReserveQuota(ctx, userID, core.OperationGapAnalysis, 500, 10, 10000)
			if err != nil {
				t.Fatalf("ReserveQuota failed: %v", err)
			}

			if tc.commit {
				if err := store.CommitUsage(ctx, rec.ID, tc.telemetry); err != nil {
					t.Fatalf("CommitUsage failed: %v", err)
				}
			} else {
				if err := store.ReleaseQuota(ctx, rec.ID, tc.errMsg, tc.telemetry); err != nil {
					t.Fatalf("ReleaseQuota failed: %v", err)
				}
			}

			records, err := store.ListUsageRecords(ctx, userID, 10)
			if err != nil {
				t.Fatalf("ListUsageRecords failed: %v", err)
			}
			if len(records) != 1 {
				t.Fatalf("expected 1 record, got %d", len(records))
			}
			got := records[0]
			if got.Provider != tc.telemetry.Provider {
				t.Errorf("Provider = %q, want %q", got.Provider, tc.telemetry.Provider)
			}
			if got.Model != tc.telemetry.Model {
				t.Errorf("Model = %q, want %q", got.Model, tc.telemetry.Model)
			}
			if got.StatusCode != tc.telemetry.StatusCode {
				t.Errorf("StatusCode = %d, want %d", got.StatusCode, tc.telemetry.StatusCode)
			}
			if got.Latency != tc.telemetry.Latency {
				t.Errorf("Latency = %v, want %v", got.Latency, tc.telemetry.Latency)
			}
			if got.PromptTokens != tc.telemetry.Usage.PromptTokens ||
				got.CompletionTokens != tc.telemetry.Usage.CompletionTokens ||
				got.TotalTokens != tc.wantTotal {
				t.Errorf("tokens = (%d, %d, %d), want (%d, %d, %d)",
					got.PromptTokens, got.CompletionTokens, got.TotalTokens,
					tc.telemetry.Usage.PromptTokens, tc.telemetry.Usage.CompletionTokens, tc.wantTotal)
			}
			if !tc.commit && got.ErrorMessage != tc.errMsg {
				t.Errorf("ErrorMessage = %q, want %q", got.ErrorMessage, tc.errMsg)
			}

			quota, err := store.Quota(ctx, userID)
			if err != nil {
				t.Fatalf("Quota failed: %v", err)
			}
			if tc.commit && quota.MonthlyTokensUsed != tc.wantTotal {
				t.Errorf("MonthlyTokensUsed = %d, want %d", quota.MonthlyTokensUsed, tc.wantTotal)
			}
			if !tc.commit && quota.MonthlyTokensUsed != 0 {
				t.Errorf("expected released quota MonthlyTokensUsed=0, got %d", quota.MonthlyTokensUsed)
			}
		})
	}
}

func TestMemoryQuotaStore_ConcurrentTelemetryRace(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	const workers = 20

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			userID := fmt.Sprintf("user-race-%d", idx%4)
			rec, err := store.ReserveQuota(ctx, userID, core.OperationResumeTailoring, 100, 1000, 100000)
			if err != nil {
				t.Errorf("ReserveQuota failed: %v", err)
				return
			}
			tel := core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o",
				StatusCode: 200,
				Latency:    time.Duration(idx+1) * time.Millisecond,
				Usage:      core.TokenUsage{PromptTokens: 40, CompletionTokens: 15},
			}
			if idx%2 == 0 {
				if err := store.CommitUsage(ctx, rec.ID, tel); err != nil {
					t.Errorf("CommitUsage failed: %v", err)
				}
			} else {
				tel.StatusCode = 429
				if err := store.ReleaseQuota(ctx, rec.ID, "rate limited", tel); err != nil {
					t.Errorf("ReleaseQuota failed: %v", err)
				}
			}
			if _, err := store.ListUsageRecords(ctx, userID, 10); err != nil {
				t.Errorf("ListUsageRecords failed: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

func TestMemoryQuotaStore_NegativeTelemetryClamped(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	userID := "user-neg-clamp"

	// Establish prior positive monthly token consumption.
	seedRec, err := store.ReserveQuota(ctx, userID, core.OperationGapAnalysis, 500, 10, 10000)
	if err != nil {
		t.Fatalf("seed ReserveQuota failed: %v", err)
	}
	if err := store.CommitUsage(ctx, seedRec.ID, core.LLMTelemetry{
		Provider:   "openai",
		Model:      "gpt-4o",
		StatusCode: 200,
		Usage:      core.TokenUsage{TotalTokens: 400},
	}); err != nil {
		t.Fatalf("seed CommitUsage failed: %v", err)
	}

	// Commit with negative token counts, negative status code, and negative latency.
	recCommit, err := store.ReserveQuota(ctx, userID, core.OperationGapAnalysis, 300, 10, 10000)
	if err != nil {
		t.Fatalf("ReserveQuota for negative commit failed: %v", err)
	}
	if err := store.CommitUsage(ctx, recCommit.ID, core.LLMTelemetry{
		Provider:   "openai",
		Model:      "gpt-4o",
		StatusCode: -500,
		Latency:    -25 * time.Millisecond,
		Usage: core.TokenUsage{
			PromptTokens:     -150,
			CompletionTokens: -50,
			TotalTokens:      -200,
		},
	}); err != nil {
		t.Fatalf("CommitUsage failed: %v", err)
	}

	// Negative committed tokens must clamp to 0 (never lowering prior 400 monthly tokens).
	quotaAfterCommit, err := store.Quota(ctx, userID)
	if err != nil {
		t.Fatalf("Quota failed: %v", err)
	}
	if quotaAfterCommit.MonthlyTokensUsed != 400 {
		t.Errorf("MonthlyTokensUsed after clamped commit = %d, want 400", quotaAfterCommit.MonthlyTokensUsed)
	}

	// Release with negative token counts must also record zeros cleanly.
	recRelease, err := store.ReserveQuota(ctx, userID, core.OperationGapAnalysis, 200, 10, 10000)
	if err != nil {
		t.Fatalf("ReserveQuota for negative release failed: %v", err)
	}
	if err := store.ReleaseQuota(ctx, recRelease.ID, "upstream error", core.LLMTelemetry{
		Provider:   "gemini",
		Model:      "gemini-2.5-flash",
		StatusCode: -1,
		Latency:    -10 * time.Millisecond,
		Usage:      core.TokenUsage{PromptTokens: -10, CompletionTokens: -5, TotalTokens: -15},
	}); err != nil {
		t.Fatalf("ReleaseQuota failed: %v", err)
	}

	records, err := store.ListUsageRecords(ctx, userID, 10)
	if err != nil {
		t.Fatalf("ListUsageRecords failed: %v", err)
	}
	for _, r := range records {
		if r.StatusCode < 0 || r.Latency < 0 || r.PromptTokens < 0 || r.CompletionTokens < 0 || r.TotalTokens < 0 {
			t.Errorf("found negative telemetry/usage persisted on record %s: %+v", r.ID, r)
		}
	}
}
