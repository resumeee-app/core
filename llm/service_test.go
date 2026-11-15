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

// compactCaptureClient records outbound LLM requests and serves deterministic JSON responses
// for GenerateTailoredResume, GenerateGapAnalysis, and SimulateATS operations.
type compactCaptureClient struct {
	requests []Request
}

// Name returns a registered compat provider name.
func (c *compactCaptureClient) Name() string { return ProviderCompat }

// DefaultModel returns a test model identifier.
func (c *compactCaptureClient) DefaultModel() string { return "compact-test-model" }

// Models returns nil so Registry resolution relies on DefaultModel.
func (c *compactCaptureClient) Models() []core.ModelOption { return nil }

// SupportsStructuredOutput reports native structured output support to skip fallback schemas.
func (c *compactCaptureClient) SupportsStructuredOutput(_ string) bool { return true }

// Complete captures req and returns schema-valid JSON for whichever schema name was invoked.
func (c *compactCaptureClient) Complete(_ context.Context, req Request) (*Response, error) {
	c.requests = append(c.requests, req)
	switch req.SchemaName {
	case "gap_analysis_response":
		return &Response{
			Text:       `{"gap_report":{"overall_match_summary":"Strong match","missing_requirements":[]}}`,
			StatusCode: 200,
		}, nil
	case "ats_extraction_response":
		return &Response{
			Text:       `{"extracted_candidate":{"summary":"Backend engineer","skills":["Go"],"experiences":[],"educations":[]},"extraction_summary":"Extracted Go skill."}`,
			StatusCode: 200,
		}, nil
	case "ats_matching_response":
		return &Response{
			Text:       `{"job_matching_score":0.85,"matching_summary":"Strong Go alignment."}`,
			StatusCode: 200,
		}, nil
	default:
		raw, err := json.Marshal(sampleResponse)
		if err != nil {
			return nil, err
		}
		return &Response{Text: string(raw), StatusCode: 200}, nil
	}
}

// TestService_LiveRequestsUseCompactJSON verifies that live LLM service workflows serialize
// structured payloads compactly while PromptCompiler preserves indented JSON for previews.
func TestService_LiveRequestsUseCompactJSON(t *testing.T) {
	req := sampleRequest()
	req.PriorVersion = &core.TailoredContent{
		Summary: "Previous summary",
		SkillGroups: []core.TailoredSkillGroup{
			{Category: "Languages", Skills: []string{"Go"}},
		},
	}

	client := &compactCaptureClient{}
	reg := NewRegistry()
	reg.Register(client)
	svc := NewService(reg)

	if _, err := svc.GenerateTailoredResume(context.Background(), req); err != nil {
		t.Fatalf("GenerateTailoredResume failed: %v", err)
	}
	if _, err := svc.GenerateGapAnalysis(context.Background(), req); err != nil {
		t.Fatalf("GenerateGapAnalysis failed: %v", err)
	}
	if _, err := svc.SimulateATS(context.Background(), core.ATSSimulationRequest{
		FormatUsed:    string(core.OutputFormatMarkdown),
		ResumeText:    "# Candidate\n- Go systems engineer",
		BranchContext: req.BranchContext,
		MainProfile:   req.MainProfile,
	}); err != nil {
		t.Fatalf("SimulateATS failed: %v", err)
	}

	if len(client.requests) != 4 {
		t.Fatalf("expected 4 outbound LLM calls across tailor/gap/ats, got %d", len(client.requests))
	}

	for _, r := range client.requests {
		for _, tag := range []string{"main_profile_data", "prior_active_version", "extracted_candidate_data"} {
			openTag := "<" + tag + ">"
			closeTag := "</" + tag + ">"
			start := strings.Index(r.UserPrompt, openTag)
			if start == -1 {
				continue
			}
			inner := r.UserPrompt[start+len(openTag):]
			end := strings.Index(inner, closeTag)
			if end == -1 {
				t.Fatalf("missing closing tag %s in live %s request", closeTag, r.SchemaName)
			}
			block := strings.TrimSpace(inner[:end])
			if fStart := strings.Index(block, "```json\n"); fStart != -1 {
				fRest := block[fStart+len("```json\n"):]
				if fEnd := strings.Index(fRest, "\n```"); fEnd != -1 {
					block = strings.TrimSpace(fRest[:fEnd])
				}
			}
			if strings.Contains(block, "\n  \"") {
				t.Errorf("live %s request <%s> JSON should be compact, got indented:\n%s", r.SchemaName, tag, block)
			}
		}
	}

	preview, err := NewPromptCompiler(WithPromptRegistry(reg)).CompilePrompt(req)
	if err != nil {
		t.Fatalf("CompilePrompt failed: %v", err)
	}
	if !strings.Contains(preview.UserPrompt, "\n  \"") {
		t.Errorf("PromptCompiler preview should preserve indented JSON:\n%s", preview.UserPrompt)
	}
}
