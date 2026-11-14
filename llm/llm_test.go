package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
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
	name         string
	defaultModel string
	models       []core.ModelOption
	completeFn   func(ctx context.Context, req core.LLMCompletionRequest) (*core.LLMCompletionResponse, error)
}

func (f *fakeTransportClient) Name() string               { return f.name }
func (f *fakeTransportClient) DefaultModel() string       { return f.defaultModel }
func (f *fakeTransportClient) Models() []core.ModelOption { return f.models }
func (f *fakeTransportClient) Complete(ctx context.Context, req core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
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

	if schema["type"] != "object" {
		t.Errorf("expected root type object, got %v", schema["type"])
	}
	if schema["additionalProperties"] != false {
		t.Errorf("expected root additionalProperties: false")
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map")
	}
	if _, ok := props["tailored_content"]; !ok {
		t.Error("missing tailored_content property")
	}
	if _, ok := props["gap_report"]; !ok {
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

func TestClientProvider_GenerateTailoredResume_SuccessAndFences(t *testing.T) {
	rawBytes, _ := json.Marshal(sampleResponse)
	fencedText := "Here is the output:\n```json\n" + string(rawBytes) + "\n```\n"

	var capturedReq core.LLMCompletionRequest
	client := &fakeTransportClient{
		name:         ProviderGemini,
		defaultModel: defaultGeminiModel,
		completeFn: func(_ context.Context, req core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
			capturedReq = req
			return &core.LLMCompletionResponse{
				Text:       fencedText,
				RawBody:    fencedText,
				StatusCode: http.StatusOK,
				Usage: core.TokenUsage{
					PromptTokens:     120,
					CompletionTokens: 80,
					TotalTokens:      200,
				},
			}, nil
		},
	}

	prov := NewClientProvider(client)
	res, err := prov.GenerateTailoredResume(context.Background(), sampleRequest())
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
		t.Error("expected ResponseSchema to be attached by ClientProvider")
	}
}

func TestRegistry_PrecedenceResolution(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeProvider{name: ProviderGemini})
	r.Register(&fakeProvider{name: ProviderOpenAI})
	r.Register(&fakeProvider{name: ProviderAnthropic})
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

	r.Register(&fakeProvider{name: ProviderCompat})
	p, err := r.Get(ProviderCompatAlt)
	if err != nil || p.Name() != ProviderCompat {
		t.Errorf("expected alias resolution for openai-compat, got %v, err=%v", p, err)
	}

	_, err = r.Get("nonexistent")
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

	r.Register(&fakeProvider{name: ProviderGemini})
	models := r.AvailableModels()
	if len(models) == 0 {
		t.Fatal("expected gemini models to be returned")
	}
	for _, m := range models {
		if m.Provider != ProviderGemini {
			t.Errorf("unexpected provider %s in models", m.Provider)
		}
	}
}

func TestGapAnalysisResponseSchema(t *testing.T) {
	schema := GapAnalysisResponseSchema()
	if schema == nil || schema["type"] != "object" {
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

type fakeProvider struct {
	name string
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) GenerateTailoredResume(_ context.Context, _ core.GenerationRequest) (*core.GenerationResponse, error) {
	return &sampleResponse, nil
}
