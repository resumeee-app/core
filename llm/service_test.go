package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

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

			resp, err := svc.GenerateGapAnalysis(context.Background(), sampleRequest())

			if tt.client.genCalls != tt.wantGenCalls {
				t.Errorf("expected %d GenerateTailoredResume call(s), got %d", tt.wantGenCalls, tt.client.genCalls)
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				if resp != nil {
					t.Errorf("expected nil response on error, got %+v", resp)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp == nil || resp.GapReport.OverallMatchSummary != tt.wantSummary {
				t.Errorf("expected fallback summary %q, got %+v", tt.wantSummary, resp)
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

func TestService_GenerateTailoredResume_Telemetry(t *testing.T) {
	bodyWithHallucinatedUsage := copySampleResponse()
	bodyWithHallucinatedUsage.Usage = core.TokenUsage{PromptTokens: 999, CompletionTokens: 888, TotalTokens: 1887}
	rawWithUsage, err := json.Marshal(bodyWithHallucinatedUsage)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	bodyWithUnnormalizedUsage := copySampleResponse()
	bodyWithUnnormalizedUsage.Usage = core.TokenUsage{PromptTokens: 60, CompletionTokens: 30}
	rawUnnormUsage, err := json.Marshal(bodyWithUnnormalizedUsage)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	cases := []struct {
		name           string
		completeFn     func(ctx context.Context, req Request) (*Response, error)
		wantErr        error
		wantStatusCode int
		wantMinLatency time.Duration
		wantUsage      core.TokenUsage
	}{
		{
			name: "transport usage overrides body JSON usage and populates telemetry",
			completeFn: func(_ context.Context, _ Request) (*Response, error) {
				return &Response{
					Text:       string(rawWithUsage),
					StatusCode: 200,
					Latency:    25 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 120, CompletionTokens: 80},
				}, nil
			},
			wantStatusCode: 200,
			wantMinLatency: 25 * time.Millisecond,
			wantUsage:      core.TokenUsage{PromptTokens: 120, CompletionTokens: 80, TotalTokens: 200},
		},
		{
			name: "zero transport usage falls back to normalized body JSON usage",
			completeFn: func(_ context.Context, _ Request) (*Response, error) {
				return &Response{
					Text:       string(rawUnnormUsage),
					StatusCode: 200,
					Latency:    14 * time.Millisecond,
				}, nil
			},
			wantStatusCode: 200,
			wantMinLatency: 14 * time.Millisecond,
			wantUsage:      core.TokenUsage{PromptTokens: 60, CompletionTokens: 30, TotalTokens: 90},
		},
		{
			name: "HTTP 200 malformed JSON parse failure attaches transport telemetry to error",
			completeFn: func(_ context.Context, _ Request) (*Response, error) {
				return &Response{
					Text:       `{"tailored_content": invalid`,
					StatusCode: 200,
					Latency:    18 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 45, CompletionTokens: 15},
				}, nil
			},
			wantErr:        ErrInvalidResponse,
			wantStatusCode: 200,
			wantMinLatency: 18 * time.Millisecond,
			wantUsage:      core.TokenUsage{PromptTokens: 45, CompletionTokens: 15, TotalTokens: 60},
		},
		{
			name: "HTTP 200 empty JSON output attaches transport telemetry to error",
			completeFn: func(_ context.Context, _ Request) (*Response, error) {
				return &Response{
					Text:       "   ",
					StatusCode: 200,
					Latency:    9 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 30, CompletionTokens: 0},
				}, nil
			},
			wantErr:        ErrInvalidResponse,
			wantStatusCode: 200,
			wantMinLatency: 9 * time.Millisecond,
			wantUsage:      core.TokenUsage{PromptTokens: 30, TotalTokens: 30},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeTransportClient{
				name:         ProviderOpenAI,
				defaultModel: "gpt-4o",
				completeFn:   tc.completeFn,
			}
			reg := NewRegistry()
			reg.Register(client)
			svc := NewService(reg)

			req := sampleRequest()
			req.Model = "gpt-4o"

			resp, err := svc.GenerateTailoredResume(context.Background(), req)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				if resp != nil {
					t.Errorf("expected nil response on error, got %+v", resp)
				}
				tel, ok := core.LLMTelemetryFromError(err)
				if !ok {
					t.Fatalf("expected telemetry attached to error")
				}
				if tel.Provider != ProviderOpenAI || tel.Model != "gpt-4o" || tel.StatusCode != tc.wantStatusCode {
					t.Errorf("error telemetry = %+v", tel)
				}
				if tel.Latency < tc.wantMinLatency || tel.Usage != tc.wantUsage {
					t.Errorf("error telemetry latency/usage = (%v, %+v), want (>=%v, %+v)", tel.Latency, tel.Usage, tc.wantMinLatency, tc.wantUsage)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.Usage != tc.wantUsage {
				t.Errorf("resp.Usage = %+v, want %+v", resp.Usage, tc.wantUsage)
			}
			if resp.Telemetry.Provider != ProviderOpenAI || resp.Telemetry.Model != "gpt-4o" || resp.Telemetry.StatusCode != tc.wantStatusCode {
				t.Errorf("resp.Telemetry = %+v", resp.Telemetry)
			}
			if resp.Telemetry.Latency < tc.wantMinLatency || resp.Telemetry.Usage != tc.wantUsage {
				t.Errorf("resp.Telemetry latency/usage = (%v, %+v), want (>=%v, %+v)", resp.Telemetry.Latency, resp.Telemetry.Usage, tc.wantMinLatency, tc.wantUsage)
			}
		})
	}
}

func TestService_GenerateGapAnalysis_Telemetry(t *testing.T) {
	rawTailored, err := json.Marshal(sampleResponse)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	gapJSON := `{"gap_report":{"overall_match_summary":"Strong gap match","missing_requirements":[]}}`

	cases := []struct {
		name           string
		responses      []*Response
		errs           []error
		wantErr        error
		wantSummary    string
		wantStatusCode int
		wantLatency    time.Duration
		wantUsage      core.TokenUsage
	}{
		{
			name: "lightweight success commits normalized usage and telemetry",
			responses: []*Response{
				{
					Text:       gapJSON,
					StatusCode: 200,
					Latency:    8 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 40, CompletionTokens: 15},
				},
			},
			wantSummary:    "Strong gap match",
			wantStatusCode: 200,
			wantLatency:    8 * time.Millisecond,
			wantUsage:      core.TokenUsage{PromptTokens: 40, CompletionTokens: 15, TotalTokens: 55},
		},
		{
			name: "fallback success merges lightweight and tailoring usage and latency",
			responses: []*Response{
				{
					Text:       `{"unexpected":"shape"}`,
					StatusCode: 200,
					Latency:    5 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 20, CompletionTokens: 10},
				},
				{
					Text:       string(rawTailored),
					StatusCode: 200,
					Latency:    12 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 100, CompletionTokens: 50},
				},
			},
			wantSummary:    sampleResponse.GapReport.OverallMatchSummary,
			wantStatusCode: 200,
			wantLatency:    17 * time.Millisecond,
			wantUsage:      core.TokenUsage{PromptTokens: 120, CompletionTokens: 60, TotalTokens: 180},
		},
		{
			name: "fallback upstream error merges lightweight and failed fallback telemetry",
			responses: []*Response{
				{
					Text:       "not json",
					StatusCode: 200,
					Latency:    5 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 20, CompletionTokens: 10},
				},
				{
					RawBody:    `{"error":"upstream"}`,
					StatusCode: 503,
					Latency:    7 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 15},
				},
			},
			errs:           []error{nil, ErrUpstream},
			wantErr:        ErrUpstream,
			wantStatusCode: 503,
			wantLatency:    12 * time.Millisecond,
			wantUsage:      core.TokenUsage{PromptTokens: 35, CompletionTokens: 10, TotalTokens: 45},
		},
		{
			name: "fallback HTTP 200 malformed JSON merges lightweight and fallback telemetry",
			responses: []*Response{
				{
					Text:       "not json",
					StatusCode: 200,
					Latency:    5 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 20, CompletionTokens: 10},
				},
				{
					Text:       "{bad-json",
					StatusCode: 200,
					Latency:    9 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 50, CompletionTokens: 12},
				},
			},
			wantErr:        ErrInvalidResponse,
			wantStatusCode: 200,
			wantLatency:    14 * time.Millisecond,
			wantUsage:      core.TokenUsage{PromptTokens: 70, CompletionTokens: 22, TotalTokens: 92},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var idx int
			client := &fakeTransportClient{
				name:         ProviderGemini,
				defaultModel: "gemini-2.5-flash",
				completeFn: func(_ context.Context, _ Request) (*Response, error) {
					i := idx
					idx++
					var r *Response
					if i < len(tc.responses) {
						r = tc.responses[i]
					}
					var callErr error
					if i < len(tc.errs) {
						callErr = tc.errs[i]
					}
					return r, callErr
				},
			}
			reg := NewRegistry()
			reg.Register(client)
			svc := NewService(reg)

			req := sampleRequest()
			req.Model = "gemini-2.5-flash"
			resp, err := svc.GenerateGapAnalysis(context.Background(), req)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				if resp != nil {
					t.Errorf("expected nil response on error, got %+v", resp)
				}
				tel, ok := core.LLMTelemetryFromError(err)
				if !ok {
					t.Fatalf("expected merged telemetry attached to error")
				}
				if tel.Provider != ProviderGemini || tel.Model != "gemini-2.5-flash" || tel.StatusCode != tc.wantStatusCode {
					t.Errorf("error telemetry = %+v, want status=%d", tel, tc.wantStatusCode)
				}
				if tel.Latency != tc.wantLatency || tel.Usage != tc.wantUsage {
					t.Errorf("error telemetry latency/usage = (%v, %+v), want (%v, %+v)", tel.Latency, tel.Usage, tc.wantLatency, tc.wantUsage)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.GapReport.OverallMatchSummary != tc.wantSummary {
				t.Errorf("summary = %q, want %q", resp.GapReport.OverallMatchSummary, tc.wantSummary)
			}
			if resp.Usage != tc.wantUsage {
				t.Errorf("resp.Usage = %+v, want %+v", resp.Usage, tc.wantUsage)
			}
			if resp.Telemetry.Provider != ProviderGemini || resp.Telemetry.Model != "gemini-2.5-flash" || resp.Telemetry.StatusCode != tc.wantStatusCode {
				t.Errorf("resp.Telemetry = %+v, want status=%d", resp.Telemetry, tc.wantStatusCode)
			}
			if resp.Telemetry.Latency != tc.wantLatency || resp.Telemetry.Usage != tc.wantUsage {
				t.Errorf("resp.Telemetry latency/usage = (%v, %+v), want (%v, %+v)", resp.Telemetry.Latency, resp.Telemetry.Usage, tc.wantLatency, tc.wantUsage)
			}
		})
	}
}

func TestService_Concurrent_Race(t *testing.T) {
	rawTailored, err := json.Marshal(sampleResponse)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	client := &fakeTransportClient{
		name:         ProviderOpenAI,
		defaultModel: "gpt-4o",
		completeFn: func(_ context.Context, req Request) (*Response, error) {
			var text string
			switch req.SchemaName {
			case "gap_analysis_response":
				text = `{"gap_report":{"overall_match_summary":"Concurrent gap match","missing_requirements":[]}}`
			case "ats_extraction_response":
				text = `{"extraction_confidence_score":0.9,"extraction_summary":"Clean","extracted_candidate":{"summary":"Eng","skills":["Go"],"experiences":[],"educations":[]}}`
			case "ats_matching_response":
				text = `{"job_matching_score":0.8,"matching_summary":"Good"}`
			default:
				text = string(rawTailored)
			}
			return &Response{
				Text:       text,
				StatusCode: 200,
				Usage:      core.TokenUsage{PromptTokens: 10, CompletionTokens: 5},
			}, nil
		},
	}

	reg := NewRegistry()
	reg.Register(client)
	svc := NewService(reg)

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := sampleRequest()
			req.Model = "gpt-4o"
			switch idx % 3 {
			case 0:
				resp, callErr := svc.GenerateTailoredResume(context.Background(), req)
				if callErr != nil || resp == nil || resp.Telemetry.Usage.TotalTokens != 15 {
					t.Errorf("concurrent GenerateTailoredResume failed: resp=%+v err=%v", resp, callErr)
				}
			case 1:
				resp, callErr := svc.GenerateGapAnalysis(context.Background(), req)
				if callErr != nil || resp == nil || resp.Telemetry.Usage.TotalTokens != 15 {
					t.Errorf("concurrent GenerateGapAnalysis failed: resp=%+v err=%v", resp, callErr)
				}
			case 2:
				resp, callErr := svc.SimulateATS(context.Background(), core.ATSSimulationRequest{
					Model:         "gpt-4o",
					ResumeText:    "# Candidate\nGo systems engineer",
					FormatUsed:    "markdown",
					BranchContext: req.BranchContext,
					MainProfile:   req.MainProfile,
				})
				if callErr != nil || resp == nil || resp.Telemetry.Usage.TotalTokens != 30 {
					t.Errorf("concurrent SimulateATS failed: resp=%+v err=%v", resp, callErr)
				}
			}
		}(i)
	}
	wg.Wait()
}
