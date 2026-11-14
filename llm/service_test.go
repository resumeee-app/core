package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/resumeee-app/resumeee-core"
)

// gapFallbackClient is a configurable Client used to drive the
// Service.GenerateGapAnalysis fallback ladder through its unhappy paths.
type gapFallbackClient struct {
	completeText string
	completeErr  error
	genResp      *core.GenerationResponse
	genErr       error
	genCalls     int
}

// Name returns the compat provider identifier so Registry resolution selects this fake.
func (g *gapFallbackClient) Name() string { return ProviderCompat }

// DefaultModel returns an empty model so the Registry applies its own defaulting.
func (g *gapFallbackClient) DefaultModel() string { return "" }

// Models returns no catalog entries.
func (g *gapFallbackClient) Models() []core.ModelOption { return nil }

// SupportsStructuredOutput reports no native structured output support.
func (g *gapFallbackClient) SupportsStructuredOutput(_ string) bool { return false }

// Complete dispatches gap-analysis and fallback tailoring requests to the configured fixtures.
func (g *gapFallbackClient) Complete(_ context.Context, req Request) (*Response, error) {
	if req.SchemaName == "tailored_resume_response" || strings.Contains(req.SystemPrompt, "\"tailored_content\"") {
		g.genCalls++
		if g.genErr != nil {
			return nil, g.genErr
		}
		if g.genResp == nil {
			return nil, nil
		}
		raw, err := json.Marshal(g.genResp)
		if err != nil {
			return nil, err
		}
		return &Response{Text: string(raw), StatusCode: 200}, nil
	}
	if g.completeErr != nil {
		return nil, g.completeErr
	}
	return &Response{Text: g.completeText, StatusCode: 200}, nil
}

// TestService_GenerateGapAnalysisUnhappyPaths verifies that unrecognized lightweight output
// falls back to full generation, that transport errors are not masked by the fallback, and that a
// (nil, nil) fallback response is rejected instead of dereferenced.
func TestService_GenerateGapAnalysisUnhappyPaths(t *testing.T) {
	errTransport := errors.New("transport down")
	errGeneration := errors.New("generation failed")

	tests := []struct {
		name         string
		client       *gapFallbackClient
		wantErr      error
		wantGenCalls int
		wantSummary  string
	}{
		{
			name:         "non-JSON lightweight output falls back to GenerateTailoredResume",
			client:       &gapFallbackClient{completeText: "I cannot produce JSON for this request.", genResp: copySampleResponse()},
			wantGenCalls: 1,
			wantSummary:  sampleResponse.GapReport.OverallMatchSummary,
		},
		{
			name:         "JSON without recognizable gap fields falls back to GenerateTailoredResume",
			client:       &gapFallbackClient{completeText: `{"unexpected":"shape"}`, genResp: copySampleResponse()},
			wantGenCalls: 1,
			wantSummary:  sampleResponse.GapReport.OverallMatchSummary,
		},
		{
			name:         "Complete error is returned without falling back",
			client:       &gapFallbackClient{completeErr: errTransport, genResp: copySampleResponse()},
			wantErr:      errTransport,
			wantGenCalls: 0,
		},
		{
			name:         "fallback generation error is propagated",
			client:       &gapFallbackClient{genErr: errGeneration},
			wantErr:      errGeneration,
			wantGenCalls: 1,
		},
		{
			name:         "fallback (nil, nil) response is rejected with ErrInvalidResponse",
			client:       &gapFallbackClient{},
			wantErr:      ErrInvalidResponse,
			wantGenCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewRegistry()
			reg.Register(tt.client)
			svc := NewService(reg)

			report, err := svc.GenerateGapAnalysis(context.Background(), sampleRequest())

			if tt.client.genCalls != tt.wantGenCalls {
				t.Errorf("expected %d GenerateTailoredResume call(s), got %d", tt.wantGenCalls, tt.client.genCalls)
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
