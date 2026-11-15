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
	"github.com/resumeee-app/resumeee-core/sanitizer"
)

var sampleResponse = core.GenerationResponse{
	TailoredContent: core.TailoredContent{
		Summary: "Staff Systems Engineer specializing in low-latency distributed storage engines.",
		SkillGroups: []core.TailoredSkillGroup{
			{
				Category:       "Systems Programming",
				Skills:         []string{"Go", "Rust", "C++"},
				SourceSkillIDs: []string{"skill-uuid-1", "skill-uuid-2"},
			},
		},
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID: "exp-uuid-1",
				Company:        "Acme Corp",
				RoleTitle:      "Staff Engineer",
				StartDate:      core.NewDate(2021, 3, 1),
				Bullets: []core.TailoredBullet{
					{
						Text:           "Reduced p99 replication latency by 45% via raft batch pipelining.",
						SourceFactIDs:  []string{"fact-uuid-101", "fact-uuid-102"},
						SourceSkillIDs: []string{"skill-uuid-1"},
					},
				},
			},
		},
		Projects: []core.TailoredProject{
			{
				SourceEntityID: "proj-uuid-1",
				Name:           "QuantumFS",
				Bullets: []core.TailoredBullet{
					{
						Text:          "Authored append-only distributed log in Go with deterministic fault testing.",
						SourceFactIDs: []string{"fact-uuid-201"},
					},
				},
			},
		},
		Education: []core.TailoredEducation{
			{
				SourceEntityID: "edu-uuid-1",
				Institution:    "Carnegie Mellon University",
				Degree:         "B.S.",
				Field:          "Computer Science",
				StartDate:      core.DatePtr(core.NewDate(2013, 9, 1)),
			},
		},
		Certifications: []core.TailoredCert{
			{
				SourceEntityID: "cert-uuid-1",
				Name:           "Certified Kubernetes Administrator",
				Issuer:         "CNCF",
				Date:           core.DatePtr(core.NewDate(2022, 6, 15)),
			},
		},
	},
	GapReport: core.GapReport{
		OverallMatchSummary: "Exceptional alignment with distributed systems core requirements.",
		MissingRequirements: []core.GapItem{
			{
				Requirement:      "Kafka Streams operational production experience",
				Importance:       core.GapImportancePreferred,
				ClarifyingPrompt: "Have you operated Kafka Streams applications at high partition counts?",
			},
		},
	},
}

func sampleRequest() core.GenerationRequest {
	return core.GenerationRequest{
		Model: "test-model-override",
		MainProfile: &core.MainProfileSnapshot{
			Profile: core.Profile{
				FullName:           "Ada Lovelace",
				GlobalWritingRules: "Rule 1: Always highlight algorithmic complexity.",
			},
			Skills: []core.Skill{
				{ID: "skill-uuid-1", Name: "Go"},
			},
			Experiences: []core.WorkExperience{
				{ID: "exp-uuid-1", Company: "Acme Corp", RoleTitle: "Staff Engineer"},
			},
			Facts: []core.Fact{
				{ID: "fact-uuid-101", Statement: "Implemented batching in Raft layer."},
			},
			ExportedAt: time.Now(),
		},
		GlobalRules:        "Never use passive voice.",
		PresetInstructions: "Maintain strict 1-page formatting constraints.",
		BranchContext: core.Branch{
			ID:                 "branch-uuid-1",
			Name:               "Stripe Backend",
			TargetRole:         "Staff Distributed Systems Engineer",
			TargetCompany:      "Stripe",
			TargetRequirements: "Strong experience with Raft consensus and consensus algorithms.",
			BranchInstructions: "Emphasize fault injection testing experience.",
			PinnedEntityIDs:    []string{"exp-uuid-1"},
			ExcludedEntityIDs:  []string{"proj-uuid-99"},
		},
		PriorVersion: &core.TailoredContent{
			Summary: "Prior refined summary by user.",
		},
	}
}

type fakeTransportClient struct {
	name               string
	defaultModel       string
	models             []core.ModelOption
	supportsStructured *bool
	completeFn         func(ctx context.Context, req Request) (*Response, error)
}

func (f *fakeTransportClient) Name() string               { return f.name }
func (f *fakeTransportClient) DefaultModel() string       { return f.defaultModel }
func (f *fakeTransportClient) Models() []core.ModelOption { return f.models }
func (f *fakeTransportClient) SupportsStructuredOutput(_ string) bool {
	if f.supportsStructured != nil {
		return *f.supportsStructured
	}
	return f.name != ProviderCompat && f.name != ProviderCompatAlt
}
func (f *fakeTransportClient) Complete(ctx context.Context, req Request) (*Response, error) {
	if f.completeFn != nil {
		return f.completeFn(ctx, req)
	}
	return nil, ErrInvalidResponse
}

func TestGenerationResponseSchema_Integrity(t *testing.T) {
	schema := GenerationResponseSchema()
	if schema == nil {
		t.Fatal("expected non-nil schema")
	}

	if !schema.Type.Contains(TypeObject) {
		t.Errorf("expected root type object, got %v", schema.Type)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Errorf("expected root additionalProperties: false, got %v", schema.AdditionalProperties)
	}

	if _, ok := schema.Properties["tailored_content"]; !ok {
		t.Error("missing tailored_content property")
	}
	if _, ok := schema.Properties["gap_report"]; !ok {
		t.Error("missing gap_report property")
	}
}

func TestPromptBuilder_Composition(t *testing.T) {
	req := sampleRequest()
	payload, err := BuildPrompt(req)
	if err != nil {
		t.Fatalf("unexpected error building prompt: %v", err)
	}

	if !strings.Contains(payload.SystemPrompt, "GROUNDING") && !strings.Contains(payload.SystemPrompt, "fact") {
		t.Error("expected system prompt to contain grounding instructions")
	}
	if !strings.Contains(payload.UserPrompt, "Staff Distributed Systems Engineer") {
		t.Error("expected user prompt to include target role")
	}
	if !strings.Contains(payload.UserPrompt, "Prior refined summary by user.") {
		t.Error("expected user prompt to include prior version content")
	}

	session := sanitizer.NewSessionFromRequest(req)
	if session == nil {
		t.Fatal("expected non-nil sanitizer session")
	}
}

func TestPromptBuilder_NilPriorVersionFallback(t *testing.T) {
	req := sampleRequest()
	req.PriorVersion = nil

	payload, err := BuildPrompt(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(payload.UserPrompt, "None (Initial generation)") {
		t.Error("expected fallback message when PriorVersion is nil")
	}
}

func TestService_GenerateTailoredResume_SuccessAndFences(t *testing.T) {
	rawBytes, _ := json.Marshal(sampleResponse)
	fencedText := "Here is the output:\n```json\n" + string(rawBytes) + "\n```\n"

	var capturedReq Request
	client := &fakeTransportClient{
		name:         ProviderGemini,
		defaultModel: defaultGeminiModel,
		completeFn: func(_ context.Context, req Request) (*Response, error) {
			capturedReq = req
			return &Response{
				Text:       fencedText,
				RawBody:    fencedText,
				StatusCode: 200,
				Usage: core.TokenUsage{
					PromptTokens:     120,
					CompletionTokens: 80,
					TotalTokens:      200,
				},
			}, nil
		},
	}

	reg := NewRegistry()
	reg.Register(client)
	svc := NewService(reg)
	res, err := svc.GenerateTailoredResume(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TailoredContent.Summary != sampleResponse.TailoredContent.Summary {
		t.Errorf("summary mismatch: got %q", res.TailoredContent.Summary)
	}
	if res.Usage.TotalTokens != 200 {
		t.Errorf("expected total tokens 200, got %d", res.Usage.TotalTokens)
	}
	if capturedReq.ResponseSchema == nil {
		t.Error("expected ResponseSchema to be attached by Registry.CompleteStructured")
	}
}

func TestRegistry_PrecedenceResolution(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeClient{name: ProviderGemini})
	r.Register(&fakeClient{name: ProviderOpenAI})
	r.Register(&fakeClient{name: ProviderAnthropic})
	r.SetDefault(ProviderGemini, "gemini-default")

	req := core.GenerationRequest{
		BranchContext: core.Branch{
			ProviderOverride: ProviderAnthropic,
			ModelOverride:    "claude-branch",
		},
	}
	prof := &core.Profile{}

	p, m, err := r.Resolve(req, nil, prof)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != ProviderAnthropic || m != "claude-branch" {
		t.Errorf("expected anthropic/claude-branch, got %s/%s", p.Name(), m)
	}

	// Fallback to system default when branch overrides are omitted
	req.BranchContext.ProviderOverride = ""
	req.BranchContext.ModelOverride = ""
	p, m, err = r.Resolve(req, nil, prof)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != ProviderGemini || m != "gemini-default" {
		t.Errorf("expected gemini/gemini-default, got %s/%s", p.Name(), m)
	}

	// Fallback to system default with nil profile
	p, m, err = r.Resolve(req, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != ProviderGemini || m != "gemini-default" {
		t.Errorf("expected gemini/gemini-default, got %s/%s", p.Name(), m)
	}
}

func TestRegistry_AliasesAndErrors(t *testing.T) {
	r := NewRegistry()
	_, _, err := r.Resolve(core.GenerationRequest{}, nil, nil)
	if !errors.Is(err, ErrNoProviders) {
		t.Errorf("expected ErrNoProviders, got %v", err)
	}

	r.Register(&fakeClient{name: ProviderCompat})
	p, err := r.Client(ProviderCompatAlt)
	if err != nil || p.Name() != ProviderCompat {
		t.Errorf("expected alias resolution for openai-compat, got %v, err=%v", p, err)
	}

	_, err = r.Client("nonexistent")
	if !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("expected ErrProviderNotFound, got %v", err)
	}
}

func TestExtractJSON_Resilience(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "clean json",
			input: `{"key": "val"}`,
			want:  `{"key": "val"}`,
		},
		{
			name:  "fenced json",
			input: "Sure!\n```json\n{\"key\": \"val\"}\n```\nHope this helps!",
			want:  `{"key": "val"}`,
		},
		{
			name:  "conversational preamble without fences",
			input: "Here is the requested JSON object: {\"a\": 1, \"b\": [2, 3]} end.",
			want:  `{"a": 1, "b": [2, 3]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractJSON(tc.input)
			if got != tc.want {
				t.Errorf("ExtractJSON(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestRegistry_AvailableModels_FilterMatrix(t *testing.T) {
	r := NewRegistry()
	if len(r.AvailableModels()) != 0 {
		t.Fatalf("expected 0 models on empty registry")
	}

	r.Register(&fakeClient{name: ProviderGemini})
	models := r.AvailableModels()
	if len(models) == 0 {
		t.Fatal("expected gemini models to be returned")
	}
	for _, m := range models {
		if m.Provider != ProviderGemini {
			t.Errorf("unexpected provider %s in models", m.Provider)
		}
	}

	rAll := NewRegistry()
	for _, name := range []string{ProviderGemini, ProviderOpenAI, ProviderAnthropic, ProviderOllama, ProviderCompat} {
		rAll.Register(&fakeClient{name: name})
	}
	allModels := rAll.AvailableModels()
	if len(allModels) != 5 {
		t.Fatalf("expected 5 fallback models for all registered providers, got %d", len(allModels))
	}
	seen := make(map[string]bool, len(allModels))
	for _, m := range allModels {
		seen[m.Provider] = true
	}
	for _, name := range []string{ProviderGemini, ProviderOpenAI, ProviderAnthropic, ProviderOllama, ProviderCompat} {
		if !seen[name] {
			t.Errorf("expected provider %q in AvailableModels output", name)
		}
	}
}

func TestGapAnalysisResponseSchema(t *testing.T) {
	schema := GapAnalysisResponseSchema()
	if schema == nil || !schema.Type.Contains(TypeObject) {
		t.Fatal("expected valid GapAnalysisResponseSchema object")
	}
}

func TestBuildGapAnalysisPrompt(t *testing.T) {
	req := sampleRequest()
	prompts, err := BuildGapAnalysisPrompt(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prompts.SystemPrompt == "" || prompts.UserPrompt == "" {
		t.Fatal("expected non-empty gap analysis prompts")
	}
}

type fakeClient struct {
	name string
}

func (f *fakeClient) Name() string                           { return f.name }
func (f *fakeClient) DefaultModel() string                   { return "" }
func (f *fakeClient) Models() []core.ModelOption             { return nil }
func (f *fakeClient) SupportsStructuredOutput(_ string) bool { return false }
func (f *fakeClient) Complete(_ context.Context, req Request) (*Response, error) {
	if req.SchemaName == "tailored_resume_response" || strings.Contains(req.SystemPrompt, "\"tailored_content\"") {
		raw, err := json.Marshal(sampleResponse)
		if err != nil {
			return nil, err
		}
		return &Response{Text: string(raw), StatusCode: 200}, nil
	}
	return &Response{Text: "", StatusCode: 200}, nil
}

func TestStructuredVsFallbackDispatch_TailorAndGapAnalysis(t *testing.T) {
	rawTailorBytes, _ := json.Marshal(sampleResponse)
	gapJSON := `{"gap_report":{"overall_match_summary":"Strong match","missing_requirements":[]}}`

	boolPtr := func(b bool) *bool { return &b }

	cases := []struct {
		name                   string
		providerName           string
		supportsStructured     *bool
		wantPromptSchemaInSys  bool
		wantTransportSchemaSet bool
	}{
		{
			name:                   "structured provider omits <response_schema> and attaches ResponseSchema",
			providerName:           ProviderGemini,
			supportsStructured:     boolPtr(true),
			wantPromptSchemaInSys:  false,
			wantTransportSchemaSet: true,
		},
		{
			name:                   "fallback provider appends <response_schema> and leaves ResponseSchema nil",
			providerName:           ProviderCompat,
			supportsStructured:     boolPtr(false),
			wantPromptSchemaInSys:  true,
			wantTransportSchemaSet: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var capturedReqs []Request
			client := &fakeTransportClient{
				name:               tc.providerName,
				defaultModel:       "test-model",
				supportsStructured: tc.supportsStructured,
				completeFn: func(_ context.Context, req Request) (*Response, error) {
					capturedReqs = append(capturedReqs, req)
					out := string(rawTailorBytes)
					if len(capturedReqs) == 2 {
						out = gapJSON
					}
					return &Response{
						Text:       out,
						RawBody:    out,
						StatusCode: 200,
						Usage:      core.TokenUsage{PromptTokens: 10, CompletionTokens: 10, TotalTokens: 20},
					}, nil
				},
			}

			reg := NewRegistry()
			reg.Register(client)
			svc := NewService(reg)

			req := sampleRequest()
			req.BranchContext.ProviderOverride = tc.providerName

			if _, err := svc.GenerateTailoredResume(context.Background(), req); err != nil {
				t.Fatalf("GenerateTailoredResume failed: %v", err)
			}
			if _, err := svc.GenerateGapAnalysis(context.Background(), req); err != nil {
				t.Fatalf("GenerateGapAnalysis failed: %v", err)
			}

			if len(capturedReqs) != 2 {
				t.Fatalf("expected 2 transport calls (tailor + gap), got %d", len(capturedReqs))
			}

			for idx, creq := range capturedReqs {
				hasSysSchema := strings.Contains(creq.SystemPrompt, "<response_schema>")
				if hasSysSchema != tc.wantPromptSchemaInSys {
					t.Errorf("call %d: SystemPrompt <response_schema> = %v, want %v", idx, hasSysSchema, tc.wantPromptSchemaInSys)
				}
				if tc.wantPromptSchemaInSys && strings.Count(creq.SystemPrompt, "<response_schema>") != 1 {
					t.Errorf("call %d: expected exactly 1 <response_schema>, got %d", idx, strings.Count(creq.SystemPrompt, "<response_schema>"))
				}
				hasSchemaObj := creq.ResponseSchema != nil
				if hasSchemaObj != tc.wantTransportSchemaSet {
					t.Errorf("call %d: ResponseSchema != nil is %v, want %v", idx, hasSchemaObj, tc.wantTransportSchemaSet)
				}
			}
		})
	}

	t.Run("custom provider with SupportsStructuredOutput=false keeps <response_schema> and omits ResponseSchema", func(t *testing.T) {
		customClient := &unstructuredCustomClient{
			name: "custom-raw",
			responses: []string{
				gapJSON,
				`{"extraction_confidence_score":0.9,"extraction_summary":"clean","extracted_candidate":{"full_name":"Jane"}}`,
				`{"job_matching_score":0.85,"matching_summary":"strong fit"}`,
			},
		}
		reg := NewRegistry()
		reg.Register(customClient)
		svc := NewService(reg)

		req := sampleRequest()
		if _, err := svc.GenerateGapAnalysis(context.Background(), req); err != nil {
			t.Fatalf("GenerateGapAnalysis failed: %v", err)
		}
		if _, err := svc.SimulateATS(context.Background(), core.ATSSimulationRequest{
			ResumeText:    "# Jane Doe\nGo Engineer",
			FormatUsed:    "markdown",
			MainProfile:   req.MainProfile,
			BranchContext: req.BranchContext,
		}); err != nil {
			t.Fatalf("SimulateATS failed: %v", err)
		}
		if len(customClient.reqs) != 3 {
			t.Fatalf("expected 3 structured calls (gap + ats extract + ats match), got %d", len(customClient.reqs))
		}
		for i, creq := range customClient.reqs {
			if strings.Count(creq.SystemPrompt, "<response_schema>") != 1 {
				t.Errorf("call %d: expected <response_schema> preserved when SupportsStructuredOutput=false, got count=%d", i, strings.Count(creq.SystemPrompt, "<response_schema>"))
			}
			if creq.ResponseSchema != nil {
				t.Errorf("call %d: expected nil ResponseSchema when SupportsStructuredOutput=false", i)
			}
		}
	})

	t.Run("GenerateGapAnalysis falls back to GenerateTailoredResume when Complete returns empty output", func(t *testing.T) {
		reg := NewRegistry()
		reg.Register(&fakeClient{name: ProviderCompat})
		svc := NewService(reg)

		resp, err := svc.GenerateGapAnalysis(context.Background(), sampleRequest())
		if err != nil {
			t.Fatalf("GenerateGapAnalysis fallback failed: %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil GapAnalysisResponse")
		}
		if resp.GapReport.OverallMatchSummary != sampleResponse.GapReport.OverallMatchSummary {
			t.Errorf("expected fallback gap summary %q, got %q", sampleResponse.GapReport.OverallMatchSummary, resp.GapReport.OverallMatchSummary)
		}
		if resp.Telemetry.Provider != ProviderCompat || resp.Telemetry.StatusCode != 200 {
			t.Errorf("expected fallback telemetry populated for compat/200, got %+v", resp.Telemetry)
		}
	})
}

func TestResponse_PopulateTelemetryAndTelemetry(t *testing.T) {
	t.Run("nil response Telemetry returns zero and PopulateTelemetry allocates", func(t *testing.T) {
		var nilResp *Response
		if got := nilResp.Telemetry(); !got.IsZero() {
			t.Errorf("expected zero telemetry from nil *Response, got %+v", got)
		}

		allocated := nilResp.PopulateTelemetry(ProviderOpenAI, "gpt-4o", 15*time.Millisecond)
		if allocated == nil {
			t.Fatal("expected PopulateTelemetry on nil receiver to allocate *Response")
		}
		gotTel := allocated.Telemetry()
		wantTel := core.LLMTelemetry{
			Provider: ProviderOpenAI,
			Model:    "gpt-4o",
			Latency:  15 * time.Millisecond,
		}
		if gotTel != wantTel {
			t.Errorf("allocated telemetry = %+v, want %+v", gotTel, wantTel)
		}
	})

	t.Run("non-nil response backfills missing fields and normalizes usage while preserving explicit values", func(t *testing.T) {
		resp := &Response{
			Text:       "ok",
			StatusCode: 200,
			Usage:      core.TokenUsage{PromptTokens: 12, CompletionTokens: 8},
		}
		got := resp.PopulateTelemetry(ProviderGemini, "gemini-2.5-flash", 22*time.Millisecond)
		if got != resp {
			t.Fatal("expected PopulateTelemetry to mutate and return existing *Response")
		}
		if got.Provider != ProviderGemini || got.Model != "gemini-2.5-flash" || got.Latency != 22*time.Millisecond {
			t.Errorf("unexpected populated metadata: %+v", got)
		}
		if got.Usage.TotalTokens != 20 {
			t.Errorf("expected normalized TotalTokens=20, got %+v", got.Usage)
		}

		// Calling PopulateTelemetry again preserves already-populated Provider, Model, and Latency.
		got.PopulateTelemetry(ProviderOpenAI, "other-model", 99*time.Millisecond)
		if got.Provider != ProviderGemini || got.Model != "gemini-2.5-flash" || got.Latency != 22*time.Millisecond {
			t.Errorf("expected explicit fields preserved, got %+v", got)
		}
	})
}

func TestRegistry_CompleteAndStructured_Telemetry(t *testing.T) {
	cases := []struct {
		name           string
		providerName   string
		defaultModel   string
		reqModel       string
		completeFn     func(ctx context.Context, req Request) (*Response, error)
		wantErr        error
		wantProvider   string
		wantModel      string
		wantStatusCode int
		wantMinLatency time.Duration
		wantUsage      core.TokenUsage
		wantText       string
	}{
		{
			name:         "success populates provider model latency and normalizes omitted TotalTokens",
			providerName: ProviderOpenAI,
			defaultModel: "gpt-4o",
			completeFn: func(_ context.Context, _ Request) (*Response, error) {
				time.Sleep(2 * time.Millisecond)
				return &Response{
					Text:       "  hello world  ",
					StatusCode: 200,
					Usage:      core.TokenUsage{PromptTokens: 14, CompletionTokens: 6},
				}, nil
			},
			wantProvider:   ProviderOpenAI,
			wantModel:      "gpt-4o",
			wantStatusCode: 200,
			wantMinLatency: 2 * time.Millisecond,
			wantUsage:      core.TokenUsage{PromptTokens: 14, CompletionTokens: 6, TotalTokens: 20},
			wantText:       "hello world",
		},
		{
			name:         "success preserves explicit response provider model and latency",
			providerName: ProviderAnthropic,
			defaultModel: "claude-sonnet-4-5",
			reqModel:     "claude-opus-4-1",
			completeFn: func(_ context.Context, _ Request) (*Response, error) {
				return &Response{
					Text:       "ok",
					Provider:   "anthropic-custom",
					Model:      "claude-resolved",
					StatusCode: 200,
					Latency:    45 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
				}, nil
			},
			wantProvider:   "anthropic-custom",
			wantModel:      "claude-resolved",
			wantStatusCode: 200,
			wantMinLatency: 45 * time.Millisecond,
			wantUsage:      core.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			wantText:       "ok",
		},
		{
			name:         "HTTP error attaches status code latency and partial usage to response and error",
			providerName: ProviderOpenAI,
			defaultModel: "gpt-4o",
			completeFn: func(_ context.Context, _ Request) (*Response, error) {
				time.Sleep(2 * time.Millisecond)
				return &Response{
					RawBody:    `{"error":"rate limit"}`,
					StatusCode: 429,
					Usage:      core.TokenUsage{PromptTokens: 9},
				}, ErrRateLimited
			},
			wantErr:        ErrRateLimited,
			wantProvider:   ProviderOpenAI,
			wantModel:      "gpt-4o",
			wantStatusCode: 429,
			wantMinLatency: 2 * time.Millisecond,
			wantUsage:      core.TokenUsage{PromptTokens: 9, TotalTokens: 9},
		},
		{
			name:         "nil response stub client error still populates provider model and latency",
			providerName: ProviderGemini,
			defaultModel: "gemini-2.5-flash",
			completeFn: func(_ context.Context, _ Request) (*Response, error) {
				time.Sleep(2 * time.Millisecond)
				return nil, ErrUpstream
			},
			wantErr:        ErrUpstream,
			wantProvider:   ProviderGemini,
			wantModel:      "gemini-2.5-flash",
			wantStatusCode: 0,
			wantMinLatency: 2 * time.Millisecond,
		},
		{
			name:         "nil response with nil error converts to ErrInvalidResponse with telemetry",
			providerName: ProviderCompat,
			defaultModel: "local-v1",
			completeFn: func(_ context.Context, _ Request) (*Response, error) {
				time.Sleep(2 * time.Millisecond)
				return nil, nil
			},
			wantErr:        ErrInvalidResponse,
			wantProvider:   ProviderCompat,
			wantModel:      "local-v1",
			wantStatusCode: 0,
			wantMinLatency: 2 * time.Millisecond,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeTransportClient{
				name:         tc.providerName,
				defaultModel: tc.defaultModel,
				completeFn:   tc.completeFn,
			}
			reg := NewRegistry()
			reg.Register(client)

			resp, err := reg.Complete(context.Background(), client, "TestOp", Request{
				Model:        tc.reqModel,
				SystemPrompt: "sys",
				UserPrompt:   "usr",
			})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Complete error = %v, want %v", err, tc.wantErr)
				}
				if resp == nil {
					t.Fatalf("expected non-nil Response carrying telemetry on error")
				}
				errTel, ok := core.LLMTelemetryFromError(err)
				if !ok {
					t.Fatalf("expected telemetry attached to error, got none")
				}
				if errTel != resp.Telemetry() {
					t.Errorf("error telemetry %+v != resp.Telemetry() %+v", errTel, resp.Telemetry())
				}
			} else if err != nil {
				t.Fatalf("Complete unexpected error: %v", err)
			}

			gotTel := resp.Telemetry()
			if gotTel.Provider != tc.wantProvider || gotTel.Model != tc.wantModel || gotTel.StatusCode != tc.wantStatusCode {
				t.Errorf("Complete telemetry = %+v, want provider=%q model=%q status=%d", gotTel, tc.wantProvider, tc.wantModel, tc.wantStatusCode)
			}
			if gotTel.Latency < tc.wantMinLatency {
				t.Errorf("Complete latency = %v, want >= %v", gotTel.Latency, tc.wantMinLatency)
			}
			if gotTel.Usage != tc.wantUsage {
				t.Errorf("Complete usage = %+v, want %+v", gotTel.Usage, tc.wantUsage)
			}

			txt, structTel, structErr := reg.CompleteStructured(context.Background(), client, "TestOp", Request{
				Model:        tc.reqModel,
				SystemPrompt: "sys",
				UserPrompt:   "usr",
			})
			if tc.wantErr != nil {
				if !errors.Is(structErr, tc.wantErr) {
					t.Fatalf("CompleteStructured error = %v, want %v", structErr, tc.wantErr)
				}
				if txt != "" {
					t.Errorf("expected empty text on error, got %q", txt)
				}
			} else {
				if structErr != nil {
					t.Fatalf("CompleteStructured unexpected error: %v", structErr)
				}
				if txt != tc.wantText {
					t.Errorf("CompleteStructured text = %q, want %q", txt, tc.wantText)
				}
			}
			if structTel.Provider != tc.wantProvider || structTel.Model != tc.wantModel || structTel.StatusCode != tc.wantStatusCode {
				t.Errorf("CompleteStructured telemetry = %+v, want provider=%q model=%q status=%d", structTel, tc.wantProvider, tc.wantModel, tc.wantStatusCode)
			}
			if structTel.Latency < tc.wantMinLatency {
				t.Errorf("CompleteStructured latency = %v, want >= %v", structTel.Latency, tc.wantMinLatency)
			}
			if structTel.Usage != tc.wantUsage {
				t.Errorf("CompleteStructured usage = %+v, want %+v", structTel.Usage, tc.wantUsage)
			}
		})
	}
}

func TestRegistry_ConcurrentComplete_Race(t *testing.T) {
	reg := NewRegistry()
	primary := &fakeTransportClient{
		name:         ProviderOpenAI,
		defaultModel: "gpt-4o",
		completeFn: func(_ context.Context, _ Request) (*Response, error) {
			return &Response{
				Text:       `{"ok":true}`,
				StatusCode: 200,
				Usage:      core.TokenUsage{PromptTokens: 10, CompletionTokens: 5},
			}, nil
		},
	}
	failing := &fakeTransportClient{
		name:         ProviderGemini,
		defaultModel: "gemini-2.5-flash",
		completeFn: func(_ context.Context, _ Request) (*Response, error) {
			return &Response{StatusCode: 503}, ErrUpstream
		},
	}
	reg.Register(primary)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if idx%4 == 0 {
				reg.Register(failing)
			}
			_ = reg.AvailableModels()
			if idx%2 == 0 {
				txt, tel, err := reg.CompleteStructured(context.Background(), primary, "RaceOp", Request{
					SystemPrompt: "sys",
					UserPrompt:   "usr",
				})
				if err != nil || txt == "" || tel.Provider != ProviderOpenAI || tel.Usage.TotalTokens != 15 {
					t.Errorf("unexpected concurrent success result: txt=%q tel=%+v err=%v", txt, tel, err)
				}
			} else {
				_, tel, err := reg.CompleteStructured(context.Background(), failing, "RaceOp", Request{
					SystemPrompt: "sys",
					UserPrompt:   "usr",
				})
				if !errors.Is(err, ErrUpstream) || tel.Provider != ProviderGemini || tel.StatusCode != 503 {
					t.Errorf("unexpected concurrent error result: tel=%+v err=%v", tel, err)
				}
			}
		}(i)
	}
	wg.Wait()
}

type unstructuredCustomClient struct {
	name      string
	responses []string
	reqs      []Request
}

func (p *unstructuredCustomClient) Name() string                           { return p.name }
func (p *unstructuredCustomClient) DefaultModel() string                   { return "custom-model" }
func (p *unstructuredCustomClient) Models() []core.ModelOption             { return nil }
func (p *unstructuredCustomClient) SupportsStructuredOutput(_ string) bool { return false }

func (p *unstructuredCustomClient) Complete(_ context.Context, req Request) (*Response, error) {
	idx := len(p.reqs)
	p.reqs = append(p.reqs, req)
	return &Response{
		Text:       p.responses[idx],
		StatusCode: 200,
		Usage:      core.TokenUsage{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 10},
	}, nil
}

func TestRegistry_Resolve_CompatAliasDefaultModel(t *testing.T) {
	reg := NewRegistry()
	compatClient := &unstructuredCustomClient{name: ProviderCompat}
	reg.Register(compatClient)
	reg.SetDefault(ProviderCompatAlt, "custom-compat-alias-model")

	client, model, err := reg.Resolve(core.GenerationRequest{}, nil, nil)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if client.Name() != ProviderCompat {
		t.Errorf("resolved provider = %q, want %q", client.Name(), ProviderCompat)
	}
	if model != "custom-compat-alias-model" {
		t.Errorf("resolved model = %q, want %q", model, "custom-compat-alias-model")
	}
}
