package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/resumeee-app/resumeee-core"
)

// gapFallbackProvider is a configurable core.LLMProvider used to drive the
// ServiceAdapter.GenerateGapAnalysis fallback ladder through its unhappy paths.
type gapFallbackProvider struct {
	completeText string
	completeErr  error
	genResp      *core.GenerationResponse
	genErr       error
	genCalls     int
}

// Name returns the compat provider identifier so Registry resolution selects this fake.
func (g *gapFallbackProvider) Name() string { return ProviderCompat }

// DefaultModel returns an empty model so the Registry applies its own defaulting.
func (g *gapFallbackProvider) DefaultModel() string { return "" }

// Models returns no catalog entries.
func (g *gapFallbackProvider) Models() []core.ModelOption { return nil }

// SupportsStructuredOutput reports no native structured output support.
func (g *gapFallbackProvider) SupportsStructuredOutput(_ string) bool { return false }

// CompleteStructured returns the configured lightweight gap-analysis text or error.
func (g *gapFallbackProvider) CompleteStructured(_ context.Context, _ string, _ core.LLMCompletionRequest) (string, core.TokenUsage, error) {
	return g.completeText, core.TokenUsage{}, g.completeErr
}

// GenerateTailoredResume records the fallback invocation and returns the configured response or error.
func (g *gapFallbackProvider) GenerateTailoredResume(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
	g.genCalls++
	return g.genResp, g.genErr
}

// TestServiceAdapter_GenerateGapAnalysisUnhappyPaths verifies that unrecognized lightweight output
// falls back to full generation, that transport errors are not masked by the fallback, and that a
// (nil, nil) fallback response is rejected instead of dereferenced.
func TestServiceAdapter_GenerateGapAnalysisUnhappyPaths(t *testing.T) {
	errTransport := errors.New("transport down")
	errGeneration := errors.New("generation failed")

	tests := []struct {
		name         string
		provider     *gapFallbackProvider
		wantErr      error
		wantGenCalls int
		wantSummary  string
	}{
		{
			name:         "non-JSON lightweight output falls back to GenerateTailoredResume",
			provider:     &gapFallbackProvider{completeText: "I cannot produce JSON for this request.", genResp: copySampleResponse()},
			wantGenCalls: 1,
			wantSummary:  sampleResponse.GapReport.OverallMatchSummary,
		},
		{
			name:         "JSON without recognizable gap fields falls back to GenerateTailoredResume",
			provider:     &gapFallbackProvider{completeText: `{"unexpected":"shape"}`, genResp: copySampleResponse()},
			wantGenCalls: 1,
			wantSummary:  sampleResponse.GapReport.OverallMatchSummary,
		},
		{
			name:         "CompleteStructured error is returned without falling back",
			provider:     &gapFallbackProvider{completeErr: errTransport, genResp: copySampleResponse()},
			wantErr:      errTransport,
			wantGenCalls: 0,
		},
		{
			name:         "fallback generation error is propagated",
			provider:     &gapFallbackProvider{genErr: errGeneration},
			wantErr:      errGeneration,
			wantGenCalls: 1,
		},
		{
			name:         "fallback (nil, nil) response is rejected with ErrInvalidResponse",
			provider:     &gapFallbackProvider{},
			wantErr:      ErrInvalidResponse,
			wantGenCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewRegistry()
			reg.Register(tt.provider)
			adapter := NewServiceAdapter(reg)

			report, err := adapter.GenerateGapAnalysis(context.Background(), sampleRequest())

			if tt.provider.genCalls != tt.wantGenCalls {
				t.Errorf("expected %d GenerateTailoredResume call(s), got %d", tt.wantGenCalls, tt.provider.genCalls)
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				if report != nil {
					t.Errorf("expected nil report on error, got %+v", report)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if report == nil || report.OverallMatchSummary != tt.wantSummary {
				t.Errorf("expected fallback summary %q, got %+v", tt.wantSummary, report)
			}
		})
	}
}

// copySampleResponse returns a shallow copy of sampleResponse so deobfuscation in one
// subtest never mutates the shared fixture observed by another.
func copySampleResponse() *core.GenerationResponse {
	resp := sampleResponse
	return &resp
}
