package core_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	core "github.com/resumeee-app/resumeee-core"
)

func TestTokenUsage_NormalizeAddAndWithFallback(t *testing.T) {
	cases := []struct {
		name     string
		usage    core.TokenUsage
		other    core.TokenUsage
		fallback int
		wantNorm core.TokenUsage
		wantSum  core.TokenUsage
		wantFB   core.TokenUsage
		wantZero bool
	}{
		{
			name:     "explicit totals preserved",
			usage:    core.TokenUsage{PromptTokens: 40, CompletionTokens: 20, TotalTokens: 60},
			other:    core.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			fallback: 500,
			wantNorm: core.TokenUsage{PromptTokens: 40, CompletionTokens: 20, TotalTokens: 60},
			wantSum:  core.TokenUsage{PromptTokens: 50, CompletionTokens: 25, TotalTokens: 75},
			wantFB:   core.TokenUsage{PromptTokens: 40, CompletionTokens: 20, TotalTokens: 60},
		},
		{
			name:     "omitted total backfilled from prompt and completion",
			usage:    core.TokenUsage{PromptTokens: 30, CompletionTokens: 12},
			other:    core.TokenUsage{PromptTokens: 15, CompletionTokens: 5},
			fallback: 500,
			wantNorm: core.TokenUsage{PromptTokens: 30, CompletionTokens: 12, TotalTokens: 42},
			wantSum:  core.TokenUsage{PromptTokens: 45, CompletionTokens: 17, TotalTokens: 62},
			wantFB:   core.TokenUsage{PromptTokens: 30, CompletionTokens: 12, TotalTokens: 42},
		},
		{
			name:     "zero usage applies fallback only via WithFallback",
			usage:    core.TokenUsage{},
			other:    core.TokenUsage{TotalTokens: 25},
			fallback: 500,
			wantNorm: core.TokenUsage{},
			wantSum:  core.TokenUsage{TotalTokens: 25},
			wantFB:   core.TokenUsage{TotalTokens: 500},
			wantZero: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.usage.IsZero(); got != tc.wantZero {
				t.Errorf("IsZero() = %v, want %v", got, tc.wantZero)
			}
			if got := tc.usage.Normalize(); got != tc.wantNorm {
				t.Errorf("Normalize() = %+v, want %+v", got, tc.wantNorm)
			}
			if got := tc.usage.Add(tc.other); got != tc.wantSum {
				t.Errorf("Add() = %+v, want %+v", got, tc.wantSum)
			}
			if got := tc.usage.WithFallback(tc.fallback); got != tc.wantFB {
				t.Errorf("WithFallback() = %+v, want %+v", got, tc.wantFB)
			}
		})
	}
}

func TestLLMTelemetry_MergeAndErrorChain(t *testing.T) {
	baseErr := errors.New("upstream rate limited")
	first := core.LLMTelemetry{
		Provider:   "openai",
		Model:      "gpt-4o",
		StatusCode: 200,
		Latency:    15 * time.Millisecond,
		Usage:      core.TokenUsage{PromptTokens: 20, CompletionTokens: 10},
	}
	second := core.LLMTelemetry{
		Provider:   "openai",
		Model:      "gpt-4o",
		StatusCode: 429,
		Latency:    25 * time.Millisecond,
		Usage:      core.TokenUsage{PromptTokens: 5, CompletionTokens: 0},
	}

	merged := first.Merge(second)
	if merged.Provider != "openai" || merged.Model != "gpt-4o" {
		t.Fatalf("unexpected provider/model: %+v", merged)
	}
	if merged.StatusCode != 429 {
		t.Errorf("expected latest StatusCode 429, got %d", merged.StatusCode)
	}
	if merged.Latency != 40*time.Millisecond {
		t.Errorf("expected summed latency 40ms, got %v", merged.Latency)
	}
	if merged.Usage.PromptTokens != 25 || merged.Usage.CompletionTokens != 10 || merged.Usage.TotalTokens != 35 {
		t.Errorf("unexpected merged usage: %+v", merged.Usage)
	}

	// Merging zero second call (no execution) retains prior HTTP status.
	retained := first.Merge(core.LLMTelemetry{})
	if retained.StatusCode != 200 {
		t.Errorf("expected StatusCode 200 preserved when next telemetry is zero, got %d", retained.StatusCode)
	}

	// Merging timed-out second call (Provider/Model/Latency set, StatusCode 0) reflects StatusCode 0.
	timedOut := first.Merge(core.LLMTelemetry{
		Provider:   "openai",
		Model:      "gpt-4o",
		StatusCode: 0,
		Latency:    30 * time.Millisecond,
	})
	if timedOut.StatusCode != 0 || timedOut.Latency != 45*time.Millisecond {
		t.Errorf("expected timed-out stage status=0 latency=45ms, got %+v", timedOut)
	}

	wrapped := fmt.Errorf("stage failed: %w", core.AttachLLMTelemetry(baseErr, merged))
	if !errors.Is(wrapped, baseErr) {
		t.Fatalf("expected wrapped error to match baseErr")
	}
	gotTel, ok := core.LLMTelemetryFromError(wrapped)
	if !ok {
		t.Fatalf("expected telemetry to be extracted from error chain")
	}
	if gotTel != merged {
		t.Errorf("extracted telemetry = %+v, want %+v", gotTel, merged)
	}

	resolved := core.ResolveTelemetry(core.LLMTelemetry{Provider: "custom"}, wrapped)
	if resolved.Provider != "custom" || resolved.StatusCode != 429 || resolved.Usage.TotalTokens != 35 {
		t.Errorf("unexpected ResolveTelemetry result: %+v", resolved)
	}
}

func TestResponseEffectiveTelemetry(t *testing.T) {
	gen := &core.GenerationResponse{
		Usage:     core.TokenUsage{PromptTokens: 100, CompletionTokens: 50},
		Telemetry: core.LLMTelemetry{Provider: "anthropic", Model: "claude-sonnet-4-20250514", StatusCode: 200, Latency: 20 * time.Millisecond},
	}
	effGen := gen.EffectiveTelemetry()
	if effGen.Usage.TotalTokens != 150 || effGen.Provider != "anthropic" || effGen.StatusCode != 200 {
		t.Errorf("unexpected GenerationResponse EffectiveTelemetry: %+v", effGen)
	}

	gap := &core.GapAnalysisResponse{
		Usage:     core.TokenUsage{PromptTokens: 60, CompletionTokens: 30, TotalTokens: 90},
		Telemetry: core.LLMTelemetry{Provider: "gemini", Model: "gemini-2.5-flash", StatusCode: 200, Latency: 12 * time.Millisecond},
	}
	if effGap := gap.EffectiveTelemetry(); effGap.Usage.TotalTokens != 90 || effGap.Model != "gemini-2.5-flash" {
		t.Errorf("unexpected GapAnalysisResponse EffectiveTelemetry: %+v", effGap)
	}

	ats := &core.ATSSimulationResponse{
		Usage:     core.TokenUsage{PromptTokens: 80, CompletionTokens: 40, TotalTokens: 120},
		Telemetry: core.LLMTelemetry{Provider: "ollama", Model: "llama3.1", StatusCode: 200, Latency: 55 * time.Millisecond},
	}
	if effATS := ats.EffectiveTelemetry(); effATS.Usage.TotalTokens != 120 || effATS.Latency != 55*time.Millisecond {
		t.Errorf("unexpected ATSSimulationResponse EffectiveTelemetry: %+v", effATS)
	}
}
