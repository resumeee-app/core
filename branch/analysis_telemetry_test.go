package branch_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/branch"
	"github.com/resumeee-app/resumeee-core/memory"
)

type failingATSScoreStore struct {
	branch.Store
	updateErr error
}

func (s *failingATSScoreStore) UpdateVersionATSScore(_ context.Context, _ string, _ *core.ATSScore) error {
	return s.updateErr
}

type failingCreateVersionStore struct {
	branch.Store
	createErr error
}

func (s *failingCreateVersionStore) CreateVersion(_ context.Context, _ *core.BranchVersion) error {
	return s.createErr
}

func assertTelemetryEqual(t *testing.T, got, want core.LLMTelemetry) {
	t.Helper()
	if got.Provider != want.Provider {
		t.Errorf("Provider = %q, want %q", got.Provider, want.Provider)
	}
	if got.Model != want.Model {
		t.Errorf("Model = %q, want %q", got.Model, want.Model)
	}
	if got.StatusCode != want.StatusCode {
		t.Errorf("StatusCode = %d, want %d", got.StatusCode, want.StatusCode)
	}
	if got.Latency != want.Latency {
		t.Errorf("Latency = %v, want %v", got.Latency, want.Latency)
	}
	if got.Usage != want.Usage {
		t.Errorf("Usage = %+v, want %+v", got.Usage, want.Usage)
	}
}

func assertUsageRecordTelemetry(t *testing.T, rec core.LLMUsageRecord, wantStatus core.ReservationStatus, wantTel core.LLMTelemetry, wantErrSubstr string) {
	t.Helper()
	if rec.Status != wantStatus {
		t.Errorf("record Status = %q, want %q", rec.Status, wantStatus)
	}
	if rec.Provider != wantTel.Provider {
		t.Errorf("record Provider = %q, want %q", rec.Provider, wantTel.Provider)
	}
	if rec.Model != wantTel.Model {
		t.Errorf("record Model = %q, want %q", rec.Model, wantTel.Model)
	}
	if rec.StatusCode != wantTel.StatusCode {
		t.Errorf("record StatusCode = %d, want %d", rec.StatusCode, wantTel.StatusCode)
	}
	if rec.Latency != wantTel.Latency {
		t.Errorf("record Latency = %v, want %v", rec.Latency, wantTel.Latency)
	}
	if rec.PromptTokens != wantTel.Usage.PromptTokens ||
		rec.CompletionTokens != wantTel.Usage.CompletionTokens ||
		rec.TotalTokens != wantTel.Usage.TotalTokens {
		t.Errorf("record tokens = (%d, %d, %d), want (%d, %d, %d)",
			rec.PromptTokens, rec.CompletionTokens, rec.TotalTokens,
			wantTel.Usage.PromptTokens, wantTel.Usage.CompletionTokens, wantTel.Usage.TotalTokens)
	}
	if wantErrSubstr != "" && !strings.Contains(rec.ErrorMessage, wantErrSubstr) {
		t.Errorf("record ErrorMessage = %q, want substring %q", rec.ErrorMessage, wantErrSubstr)
	}
}

func TestService_AnalyzeGaps_Telemetry(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name        string
		gapResp     *core.GapAnalysisResponse
		genErr      error
		wantErr     bool
		wantErrIs   error
		wantRelease bool
		wantTel     core.LLMTelemetry
	}{
		{
			name: "commits actual normalized component tokens and call telemetry",
			gapResp: &core.GapAnalysisResponse{
				GapReport: core.GapReport{OverallMatchSummary: "Strong match"},
				Usage:     core.TokenUsage{PromptTokens: 120, CompletionTokens: 30},
				Telemetry: core.LLMTelemetry{
					Provider:   "openai",
					Model:      "gpt-4o",
					StatusCode: 200,
					Latency:    42 * time.Millisecond,
				},
			},
			wantTel: core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o",
				StatusCode: 200,
				Latency:    42 * time.Millisecond,
				Usage:      core.TokenUsage{PromptTokens: 120, CompletionTokens: 30, TotalTokens: 150},
			},
		},
		{
			name: "commits explicit telemetry usage without overwriting with 500 fallback",
			gapResp: &core.GapAnalysisResponse{
				GapReport: core.GapReport{OverallMatchSummary: "Explicit telemetry usage"},
				Telemetry: core.LLMTelemetry{
					Provider:   "anthropic",
					Model:      "claude-sonnet-4-20250514",
					StatusCode: 200,
					Latency:    55 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 210, CompletionTokens: 90, TotalTokens: 300},
				},
			},
			wantTel: core.LLMTelemetry{
				Provider:   "anthropic",
				Model:      "claude-sonnet-4-20250514",
				StatusCode: 200,
				Latency:    55 * time.Millisecond,
				Usage:      core.TokenUsage{PromptTokens: 210, CompletionTokens: 90, TotalTokens: 300},
			},
		},
		{
			name: "applies 500 fallback tokens only when err is nil and usage is zero",
			gapResp: &core.GapAnalysisResponse{
				GapReport: core.GapReport{OverallMatchSummary: "Zero token fallback"},
				Telemetry: core.LLMTelemetry{
					Provider:   "gemini",
					Model:      "gemini-2.5-flash",
					StatusCode: 200,
					Latency:    19 * time.Millisecond,
				},
			},
			wantTel: core.LLMTelemetry{
				Provider:   "gemini",
				Model:      "gemini-2.5-flash",
				StatusCode: 200,
				Latency:    19 * time.Millisecond,
				Usage:      core.TokenUsage{TotalTokens: 500},
			},
		},
		{
			name: "releases quota with 429 telemetry and zero tokens",
			genErr: core.AttachLLMTelemetry(errors.New("rate limit exceeded"), core.LLMTelemetry{
				Provider:   "anthropic",
				Model:      "claude-sonnet-4-20250514",
				StatusCode: 429,
				Latency:    18 * time.Millisecond,
			}),
			wantErr:     true,
			wantRelease: true,
			wantTel: core.LLMTelemetry{
				Provider:   "anthropic",
				Model:      "claude-sonnet-4-20250514",
				StatusCode: 429,
				Latency:    18 * time.Millisecond,
			},
		},
		{
			name: "releases quota with 500 telemetry and zero tokens",
			genErr: core.AttachLLMTelemetry(errors.New("internal server error"), core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o",
				StatusCode: 500,
				Latency:    64 * time.Millisecond,
			}),
			wantErr:     true,
			wantRelease: true,
			wantTel: core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o",
				StatusCode: 500,
				Latency:    64 * time.Millisecond,
			},
		},
		{
			name: "releases quota with timeout telemetry and zero status code",
			genErr: core.AttachLLMTelemetry(context.DeadlineExceeded, core.LLMTelemetry{
				Provider:   "gemini",
				Model:      "gemini-2.5-flash",
				StatusCode: 0,
				Latency:    300 * time.Millisecond,
			}),
			wantErr:     true,
			wantErrIs:   context.DeadlineExceeded,
			wantRelease: true,
			wantTel: core.LLMTelemetry{
				Provider:   "gemini",
				Model:      "gemini-2.5-flash",
				StatusCode: 0,
				Latency:    300 * time.Millisecond,
			},
		},
		{
			name: "releases quota preserving partial tokens from error telemetry",
			genErr: core.AttachLLMTelemetry(errors.New("incomplete structured json"), core.LLMTelemetry{
				Provider:   "ollama",
				Model:      "llama3.1",
				StatusCode: 200,
				Latency:    95 * time.Millisecond,
				Usage:      core.TokenUsage{PromptTokens: 40, CompletionTokens: 10},
			}),
			wantErr:     true,
			wantRelease: true,
			wantTel: core.LLMTelemetry{
				Provider:   "ollama",
				Model:      "llama3.1",
				StatusCode: 200,
				Latency:    95 * time.Millisecond,
				Usage:      core.TokenUsage{PromptTokens: 40, CompletionTokens: 10, TotalTokens: 50},
			},
		},
		{
			name: "releases quota preserving partial tokens from response alongside error",
			gapResp: &core.GapAnalysisResponse{
				Usage: core.TokenUsage{PromptTokens: 35, CompletionTokens: 15},
				Telemetry: core.LLMTelemetry{
					Provider:   "openai",
					Model:      "gpt-4o-mini",
					StatusCode: 200,
					Latency:    48 * time.Millisecond,
				},
			},
			genErr:      errors.New("stream truncated"),
			wantErr:     true,
			wantRelease: true,
			wantTel: core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o-mini",
				StatusCode: 200,
				Latency:    48 * time.Millisecond,
				Usage:      core.TokenUsage{PromptTokens: 35, CompletionTokens: 15, TotalTokens: 50},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTestBranchEnv(t, 10)
			spyQuota := &spyQuotaRepo{}
			stubLLM := &stubGenerator{
				gapResp: tc.gapResp,
				err:     tc.genErr,
			}

			svc := branch.New(branch.Config{
				Branches:  env.branchRepo,
				Profiles:  env.profileRepo,
				Quotas:    spyQuota,
				Generator: stubLLM,
			})

			report, err := svc.AnalyzeGaps(ctx, env.defaultUser, env.branchID)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got report %+v", report)
				}
				if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
					t.Fatalf("expected errors.Is(%v), got %v", tc.wantErrIs, err)
				}
			} else if err != nil {
				t.Fatalf("AnalyzeGaps failed: %v", err)
			}

			spyQuota.mu.Lock()
			defer spyQuota.mu.Unlock()
			if tc.wantRelease {
				if len(spyQuota.commits) != 0 {
					t.Fatalf("expected 0 commits on failure, got %d", len(spyQuota.commits))
				}
				if len(spyQuota.releases) != 1 {
					t.Fatalf("expected 1 release on failure, got %d", len(spyQuota.releases))
				}
				assertTelemetryEqual(t, spyQuota.releases[0].Telemetry, tc.wantTel)
			} else {
				if len(spyQuota.releases) != 0 {
					t.Fatalf("expected 0 releases on success, got %d", len(spyQuota.releases))
				}
				if len(spyQuota.commits) != 1 {
					t.Fatalf("expected 1 commit on success, got %d", len(spyQuota.commits))
				}
				assertTelemetryEqual(t, spyQuota.commits[0].Telemetry, tc.wantTel)
			}
		})
	}
}

func seedCompletedATSVersion(ctx context.Context, t *testing.T, env *testBranchEnv, versionID string) {
	t.Helper()
	b, err := env.svc.Branch(ctx, env.defaultUser, env.branchID)
	if err != nil {
		t.Fatalf("Branch failed: %v", err)
	}
	if b.TargetRequirements == "" {
		b.TargetRequirements = "Strong Go, Kubernetes, and distributed systems architecture."
		if _, err := env.svc.UpdateBranch(ctx, b); err != nil {
			t.Fatalf("UpdateBranch failed: %v", err)
		}
	}
	ver := &core.BranchVersion{
		ID:               versionID,
		BranchID:         env.branchID,
		MainRevisionUsed: 1,
		Status:           core.VersionStatusCompleted,
		MarkdownOutput:   "# Jane Doe\nGo and Kubernetes Lead",
	}
	if err := env.branchRepo.CreateVersion(ctx, ver); err != nil {
		t.Fatalf("CreateVersion failed: %v", err)
	}
	if err := env.branchRepo.CompleteVersion(ctx, ver); err != nil {
		t.Fatalf("CompleteVersion failed: %v", err)
	}
}

func TestService_SimulateATS_Telemetry(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name           string
		atsResp        *core.ATSSimulationResponse
		atsErr         error
		updateScoreErr error
		wantErr        bool
		wantErrIs      error
		wantRelease    bool
		wantTel        core.LLMTelemetry
	}{
		{
			name: "commits full telemetry and normalized token usage",
			atsResp: &core.ATSSimulationResponse{
				Score: core.ATSScore{
					ExtractionConfidenceScore: 91,
					JobMatchingScore:          84,
				},
				Usage: core.TokenUsage{PromptTokens: 300, CompletionTokens: 120},
				Telemetry: core.LLMTelemetry{
					Provider:   "openai",
					Model:      "gpt-4o",
					StatusCode: 200,
					Latency:    75 * time.Millisecond,
				},
			},
			wantTel: core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o",
				StatusCode: 200,
				Latency:    75 * time.Millisecond,
				Usage:      core.TokenUsage{PromptTokens: 300, CompletionTokens: 120, TotalTokens: 420},
			},
		},
		{
			name: "applies 800 fallback tokens only when err is nil and usage is zero while preserving metadata",
			atsResp: &core.ATSSimulationResponse{
				Score: core.ATSScore{
					ExtractionConfidenceScore: 88,
					JobMatchingScore:          80,
				},
				Telemetry: core.LLMTelemetry{
					Provider:   "ollama",
					Model:      "llama3.1",
					StatusCode: 200,
					Latency:    110 * time.Millisecond,
				},
			},
			wantTel: core.LLMTelemetry{
				Provider:   "ollama",
				Model:      "llama3.1",
				StatusCode: 200,
				Latency:    110 * time.Millisecond,
				Usage:      core.TokenUsage{TotalTokens: 800},
			},
		},
		{
			name: "releases quota with 429 telemetry and zero tokens on LLM failure",
			atsErr: core.AttachLLMTelemetry(errors.New("rate limit exceeded"), core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o",
				StatusCode: 429,
				Latency:    22 * time.Millisecond,
			}),
			wantErr:     true,
			wantRelease: true,
			wantTel: core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o",
				StatusCode: 429,
				Latency:    22 * time.Millisecond,
			},
		},
		{
			name: "releases quota with partial stage-1 telemetry and partial tokens on stage-2 LLM failure",
			atsErr: core.AttachLLMTelemetry(errors.New("stage 2 failed with 500"), core.LLMTelemetry{
				Provider:   "anthropic",
				Model:      "claude-sonnet-4-20250514",
				StatusCode: 500,
				Latency:    140 * time.Millisecond,
				Usage:      core.TokenUsage{PromptTokens: 150, CompletionTokens: 40},
			}),
			wantErr:     true,
			wantRelease: true,
			wantTel: core.LLMTelemetry{
				Provider:   "anthropic",
				Model:      "claude-sonnet-4-20250514",
				StatusCode: 500,
				Latency:    140 * time.Millisecond,
				Usage:      core.TokenUsage{PromptTokens: 150, CompletionTokens: 40, TotalTokens: 190},
			},
		},
		{
			name: "releases quota with timeout telemetry on deadline exceeded",
			atsErr: core.AttachLLMTelemetry(context.DeadlineExceeded, core.LLMTelemetry{
				Provider:   "gemini",
				Model:      "gemini-2.5-flash",
				StatusCode: 0,
				Latency:    500 * time.Millisecond,
			}),
			wantErr:     true,
			wantErrIs:   context.DeadlineExceeded,
			wantRelease: true,
			wantTel: core.LLMTelemetry{
				Provider:   "gemini",
				Model:      "gemini-2.5-flash",
				StatusCode: 0,
				Latency:    500 * time.Millisecond,
			},
		},
		{
			name: "releases quota preserving captured telemetry and normalized usage when UpdateVersionATSScore fails",
			atsResp: &core.ATSSimulationResponse{
				Score: core.ATSScore{
					ExtractionConfidenceScore: 95,
					JobMatchingScore:          92,
				},
				Usage: core.TokenUsage{PromptTokens: 200, CompletionTokens: 50},
				Telemetry: core.LLMTelemetry{
					Provider:   "anthropic",
					Model:      "claude-sonnet-4-20250514",
					StatusCode: 200,
					Latency:    88 * time.Millisecond,
				},
			},
			updateScoreErr: errors.New("database write failed"),
			wantErr:        true,
			wantRelease:    true,
			wantTel: core.LLMTelemetry{
				Provider:   "anthropic",
				Model:      "claude-sonnet-4-20250514",
				StatusCode: 200,
				Latency:    88 * time.Millisecond,
				Usage:      core.TokenUsage{PromptTokens: 200, CompletionTokens: 50, TotalTokens: 250},
			},
		},
		{
			name: "releases quota preserving captured metadata without synthetic 800 fallback when UpdateVersionATSScore fails and usage is zero",
			atsResp: &core.ATSSimulationResponse{
				Score: core.ATSScore{
					ExtractionConfidenceScore: 89,
					JobMatchingScore:          82,
				},
				Telemetry: core.LLMTelemetry{
					Provider:   "openai",
					Model:      "gpt-4o",
					StatusCode: 200,
					Latency:    33 * time.Millisecond,
				},
			},
			updateScoreErr: errors.New("database write failed"),
			wantErr:        true,
			wantRelease:    true,
			wantTel: core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o",
				StatusCode: 200,
				Latency:    33 * time.Millisecond,
			},
		},
	}

	for idx, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTestBranchEnv(t, 10)
			verID := fmt.Sprintf("ver-ats-tel-%d", idx)
			seedCompletedATSVersion(ctx, t, env, verID)

			spyQuota := &spyQuotaRepo{}
			var branchStore branch.Store = env.branchRepo
			if tc.updateScoreErr != nil {
				branchStore = &failingATSScoreStore{
					Store:     env.branchRepo,
					updateErr: tc.updateScoreErr,
				}
			}

			sim := &stubATSSimulator{
				atsResp: tc.atsResp,
				atsErr:  tc.atsErr,
			}
			svc := branch.New(branch.Config{
				Branches:  branchStore,
				Profiles:  env.profileRepo,
				Quotas:    spyQuota,
				Generator: sim,
			})

			score, err := svc.SimulateATS(ctx, env.defaultUser, env.branchID, &core.ATSSimulationOptions{
				VersionID: verID,
			})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got score %+v", score)
				}
				if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
					t.Fatalf("expected errors.Is(%v), got %v", tc.wantErrIs, err)
				}
			} else if err != nil {
				t.Fatalf("SimulateATS failed: %v", err)
			}

			spyQuota.mu.Lock()
			defer spyQuota.mu.Unlock()
			if tc.wantRelease {
				if len(spyQuota.commits) != 0 {
					t.Fatalf("expected 0 commits on failure, got %d", len(spyQuota.commits))
				}
				if len(spyQuota.releases) != 1 {
					t.Fatalf("expected 1 release on failure, got %d", len(spyQuota.releases))
				}
				assertTelemetryEqual(t, spyQuota.releases[0].Telemetry, tc.wantTel)
			} else {
				if len(spyQuota.releases) != 0 {
					t.Fatalf("expected 0 releases on success, got %d", len(spyQuota.releases))
				}
				if len(spyQuota.commits) != 1 {
					t.Fatalf("expected 1 commit on success, got %d", len(spyQuota.commits))
				}
				assertTelemetryEqual(t, spyQuota.commits[0].Telemetry, tc.wantTel)
			}
		})
	}
}

func TestService_GenerateVersion_PreLLMReleaseZeroTelemetry(t *testing.T) {
	ctx := context.Background()

	t.Run("CreateVersion failure passes zero LLMTelemetry to ReleaseQuota", func(t *testing.T) {
		env := setupTestBranchEnv(t, 10)
		spyQuota := &spyQuotaRepo{}
		svc := branch.New(branch.Config{
			Branches:    &failingCreateVersionStore{Store: env.branchRepo, createErr: errors.New("insert version failed")},
			Profiles:    env.profileRepo,
			Quotas:      spyQuota,
			JobEnqueuer: newStubEnqueuer(10),
		})

		if _, err := svc.GenerateVersion(ctx, env.defaultUser, env.branchID); err == nil {
			t.Fatal("expected CreateVersion error")
		}

		spyQuota.mu.Lock()
		defer spyQuota.mu.Unlock()
		if len(spyQuota.releases) != 1 {
			t.Fatalf("expected 1 release, got %d", len(spyQuota.releases))
		}
		if !spyQuota.releases[0].Telemetry.IsZero() {
			t.Errorf("expected zero LLMTelemetry on pre-LLM release, got %+v", spyQuota.releases[0].Telemetry)
		}
	})

	t.Run("Enqueue failure passes zero LLMTelemetry to ReleaseQuota", func(t *testing.T) {
		env := setupTestBranchEnv(t, 10)
		spyQuota := &spyQuotaRepo{}
		closedEnqueuer := &stubEnqueuer{closed: true}
		svc := branch.New(branch.Config{
			Branches:    env.branchRepo,
			Profiles:    env.profileRepo,
			Quotas:      spyQuota,
			JobEnqueuer: closedEnqueuer,
		})

		_, err := svc.GenerateVersion(ctx, env.defaultUser, env.branchID)
		if !errors.Is(err, core.ErrQueueFull) {
			t.Fatalf("expected ErrQueueFull, got %v", err)
		}

		spyQuota.mu.Lock()
		defer spyQuota.mu.Unlock()
		if len(spyQuota.releases) != 1 {
			t.Fatalf("expected 1 release, got %d", len(spyQuota.releases))
		}
		if !spyQuota.releases[0].Telemetry.IsZero() {
			t.Errorf("expected zero LLMTelemetry on enqueue failure release, got %+v", spyQuota.releases[0].Telemetry)
		}
	})
}

type concurrentTelemetryGenerator struct{}

func (g *concurrentTelemetryGenerator) GenerateTailoredResume(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
	return nil, nil
}

func (g *concurrentTelemetryGenerator) GenerateGapAnalysis(_ context.Context, req core.GenerationRequest) (*core.GapAnalysisResponse, error) {
	switch req.BranchContext.TargetRole {
	case "gap-actual":
		return &core.GapAnalysisResponse{
			GapReport: core.GapReport{OverallMatchSummary: "gap actual"},
			Usage:     core.TokenUsage{PromptTokens: 70, CompletionTokens: 30},
			Telemetry: core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o",
				StatusCode: 200,
				Latency:    15 * time.Millisecond,
			},
		}, nil
	case "gap-fallback":
		return &core.GapAnalysisResponse{
			GapReport: core.GapReport{OverallMatchSummary: "gap fallback"},
			Telemetry: core.LLMTelemetry{
				Provider:   "gemini",
				Model:      "gemini-2.5-flash",
				StatusCode: 200,
				Latency:    12 * time.Millisecond,
			},
		}, nil
	default:
		return nil, core.AttachLLMTelemetry(errors.New("rate limit"), core.LLMTelemetry{
			Provider:   "anthropic",
			Model:      "claude-sonnet-4-20250514",
			StatusCode: 429,
			Latency:    9 * time.Millisecond,
		})
	}
}

func (g *concurrentTelemetryGenerator) SimulateATS(_ context.Context, req core.ATSSimulationRequest) (*core.ATSSimulationResponse, error) {
	switch req.BranchContext.TargetRole {
	case "ats-actual":
		return &core.ATSSimulationResponse{
			Score: core.ATSScore{ExtractionConfidenceScore: 90, JobMatchingScore: 86},
			Usage: core.TokenUsage{PromptTokens: 180, CompletionTokens: 60},
			Telemetry: core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o",
				StatusCode: 200,
				Latency:    28 * time.Millisecond,
			},
		}, nil
	case "ats-fallback":
		return &core.ATSSimulationResponse{
			Score: core.ATSScore{ExtractionConfidenceScore: 85, JobMatchingScore: 80},
			Telemetry: core.LLMTelemetry{
				Provider:   "ollama",
				Model:      "llama3.1",
				StatusCode: 200,
				Latency:    40 * time.Millisecond,
			},
		}, nil
	default:
		return nil, core.AttachLLMTelemetry(context.DeadlineExceeded, core.LLMTelemetry{
			Provider:   "gemini",
			Model:      "gemini-2.5-flash",
			StatusCode: 0,
			Latency:    250 * time.Millisecond,
		})
	}
}

func TestService_ConcurrentAnalysisAndATSTelemetryRace(t *testing.T) {
	t.Run("fakeBranchStore", func(t *testing.T) {
		store := newSeededBranchStore(context.Background(), "race-seed")
		runConcurrentTelemetryRace(t, store, store, store, false)
	})

	t.Run("memory.Store", func(t *testing.T) {
		store := memory.New()
		runConcurrentTelemetryRace(t, store, store, store, true)
	})
}

func runConcurrentTelemetryRace(
	t *testing.T,
	branchStore branch.Store,
	profileStore core.ProfileStore,
	quotaStore core.QuotaStore,
	verifyQuotaTotals bool,
) {
	t.Helper()
	ctx := context.Background()
	svc := branch.New(branch.Config{
		Branches:          branchStore,
		Profiles:          profileStore,
		Quotas:            quotaStore,
		Generator:         &concurrentTelemetryGenerator{},
		DailyRequestLimit: 100,
		MonthlyTokenLimit: 1000000,
	})

	type workerJob struct {
		userID     string
		branchID   string
		versionID  string
		isATS      bool
		role       string
		wantErr    bool
		wantStatus core.ReservationStatus
		wantTel    core.LLMTelemetry
	}

	modes := []struct {
		isATS      bool
		role       string
		wantErr    bool
		wantStatus core.ReservationStatus
		wantTel    core.LLMTelemetry
	}{
		{
			isATS:      false,
			role:       "gap-actual",
			wantStatus: core.ReservationStatusCommitted,
			wantTel: core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o",
				StatusCode: 200,
				Latency:    15 * time.Millisecond,
				Usage:      core.TokenUsage{PromptTokens: 70, CompletionTokens: 30, TotalTokens: 100},
			},
		},
		{
			isATS:      false,
			role:       "gap-fallback",
			wantStatus: core.ReservationStatusCommitted,
			wantTel: core.LLMTelemetry{
				Provider:   "gemini",
				Model:      "gemini-2.5-flash",
				StatusCode: 200,
				Latency:    12 * time.Millisecond,
				Usage:      core.TokenUsage{TotalTokens: 500},
			},
		},
		{
			isATS:      false,
			role:       "gap-error",
			wantErr:    true,
			wantStatus: core.ReservationStatusReleased,
			wantTel: core.LLMTelemetry{
				Provider:   "anthropic",
				Model:      "claude-sonnet-4-20250514",
				StatusCode: 429,
				Latency:    9 * time.Millisecond,
			},
		},
		{
			isATS:      true,
			role:       "ats-actual",
			wantStatus: core.ReservationStatusCommitted,
			wantTel: core.LLMTelemetry{
				Provider:   "openai",
				Model:      "gpt-4o",
				StatusCode: 200,
				Latency:    28 * time.Millisecond,
				Usage:      core.TokenUsage{PromptTokens: 180, CompletionTokens: 60, TotalTokens: 240},
			},
		},
		{
			isATS:      true,
			role:       "ats-fallback",
			wantStatus: core.ReservationStatusCommitted,
			wantTel: core.LLMTelemetry{
				Provider:   "ollama",
				Model:      "llama3.1",
				StatusCode: 200,
				Latency:    40 * time.Millisecond,
				Usage:      core.TokenUsage{TotalTokens: 800},
			},
		},
		{
			isATS:      true,
			role:       "ats-error",
			wantErr:    true,
			wantStatus: core.ReservationStatusReleased,
			wantTel: core.LLMTelemetry{
				Provider:   "gemini",
				Model:      "gemini-2.5-flash",
				StatusCode: 0,
				Latency:    250 * time.Millisecond,
			},
		},
	}

	const repeats = 3
	jobs := make([]workerJob, 0, len(modes)*repeats)
	for r := 0; r < repeats; r++ {
		for mIdx, m := range modes {
			idx := r*len(modes) + mIdx
			userID := fmt.Sprintf("race-user-%d", idx)
			branchID := fmt.Sprintf("race-branch-%d", idx)
			verID := fmt.Sprintf("race-ver-%d", idx)

			if err := profileStore.UpsertProfile(ctx, &core.Profile{
				ID:           "prof-" + userID,
				UserID:       userID,
				FullName:     "Concurrent Candidate",
				MainRevision: 1,
			}); err != nil {
				t.Fatalf("UpsertProfile failed: %v", err)
			}
			if err := branchStore.CreateBranch(ctx, &core.Branch{
				ID:                 branchID,
				UserID:             userID,
				Name:               "Branch " + branchID,
				TargetRole:         m.role,
				TargetRequirements: "Go, Kubernetes",
			}); err != nil {
				t.Fatalf("CreateBranch failed: %v", err)
			}
			if m.isATS {
				ver := &core.BranchVersion{
					ID:               verID,
					UserID:           userID,
					BranchID:         branchID,
					MainRevisionUsed: 1,
					Status:           core.VersionStatusCompleted,
					MarkdownOutput:   "# Concurrent Resume\nGo, Kubernetes",
				}
				if err := branchStore.CreateVersion(ctx, ver); err != nil {
					t.Fatalf("CreateVersion failed: %v", err)
				}
				if err := branchStore.CompleteVersion(ctx, ver); err != nil {
					t.Fatalf("CompleteVersion failed: %v", err)
				}
			}

			jobs = append(jobs, workerJob{
				userID:     userID,
				branchID:   branchID,
				versionID:  verID,
				isATS:      m.isATS,
				role:       m.role,
				wantErr:    m.wantErr,
				wantStatus: m.wantStatus,
				wantTel:    m.wantTel,
			})
		}
	}

	var wg sync.WaitGroup
	errs := make([]error, len(jobs))
	for i, job := range jobs {
		wg.Add(1)
		go func(idx int, j workerJob) {
			defer wg.Done()
			if j.isATS {
				_, errs[idx] = svc.SimulateATS(ctx, j.userID, j.branchID, &core.ATSSimulationOptions{VersionID: j.versionID})
				return
			}
			_, errs[idx] = svc.AnalyzeGaps(ctx, j.userID, j.branchID)
		}(i, job)
	}
	wg.Wait()

	for i, job := range jobs {
		if job.wantErr && errs[i] == nil {
			t.Errorf("job %d (%s): expected error, got nil", i, job.role)
		}
		if !job.wantErr && errs[i] != nil {
			t.Errorf("job %d (%s): unexpected error: %v", i, job.role, errs[i])
		}

		records, err := quotaStore.ListUsageRecords(ctx, job.userID, 10)
		if err != nil {
			t.Fatalf("ListUsageRecords(%s) failed: %v", job.userID, err)
		}
		if len(records) != 1 {
			t.Fatalf("job %d (%s): expected 1 usage record, got %d", i, job.role, len(records))
		}
		var errSubstr string
		if job.wantErr {
			errSubstr = "failed"
		}
		assertUsageRecordTelemetry(t, records[0], job.wantStatus, job.wantTel, errSubstr)

		if verifyQuotaTotals {
			quota, err := quotaStore.Quota(ctx, job.userID)
			if err != nil {
				t.Fatalf("Quota(%s) failed: %v", job.userID, err)
			}
			if job.wantStatus == core.ReservationStatusCommitted {
				if quota.DailyRequestsUsed != 1 || quota.MonthlyTokensUsed != job.wantTel.Usage.TotalTokens {
					t.Errorf("job %d (%s): quota = (daily=%d, monthly=%d), want (1, %d)",
						i, job.role, quota.DailyRequestsUsed, quota.MonthlyTokensUsed, job.wantTel.Usage.TotalTokens)
				}
			} else if quota.DailyRequestsUsed != 0 || quota.MonthlyTokensUsed != 0 {
				t.Errorf("job %d (%s): expected released quota (0, 0), got (%d, %d)",
					i, job.role, quota.DailyRequestsUsed, quota.MonthlyTokensUsed)
			}
		}
	}
}
