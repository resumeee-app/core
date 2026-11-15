package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/llm"
)

func TestResumeExtractor_Table(t *testing.T) {
	sampleResumeJSON := `{
		"basics": {
			"name": "Alex Mercer",
			"label": "Distributed Systems Engineer",
			"email": "alex@example.com",
			"phone": "+1-555-0199",
			"summary": "Specialist in consensus protocols and distributed databases.",
			"location": {
				"city": "Seattle",
				"region": "WA",
				"countryCode": "US"
			}
		},
		"skills": [
			{"name": "Go", "level": "Expert", "keywords": ["goroutines", "concurrency"]},
			{"name": "Distributed Systems", "level": "Senior", "keywords": ["Raft", "Paxos"]}
		],
		"work": [
			{
				"name": "CloudScale Inc",
				"position": "Staff Engineer",
				"startDate": "2020-01-01",
				"endDate": "2024-05-01",
				"summary": "Led storage architecture team.",
				"highlights": ["Built distributed lock manager", "Cut tail latency by 40%"]
			}
		],
		"education": [
			{
				"institution": "University of Washington",
				"area": "Computer Science",
				"studyType": "B.S.",
				"startDate": "2014-09-01",
				"endDate": "2018-06-01"
			}
		],
		"projects": [
			{
				"name": "MiniRaft",
				"description": "Educational Raft implementation in pure Go",
				"highlights": ["100% test coverage with Jepsen tests"]
			}
		]
	}`

	t.Run("Complete resume text extracts successfully", func(t *testing.T) {
		completer := llm.CompleterFunc(func(_ context.Context, _, _, _, _ string) (string, error) {
			return sampleResumeJSON, nil
		})
		extractor := llm.NewExtractor(completer)

		res, _, err := extractor.ExtractResume(context.Background(), "Alex Mercer resume text...", "mock", "mock-model")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Alex Mercer" {
			t.Errorf("expected name 'Alex Mercer', got: %q", res.Basics.Name)
		}
		if res.Basics.Email != "alex@example.com" {
			t.Errorf("expected email 'alex@example.com', got: %q", res.Basics.Email)
		}
		if len(res.Skills) != 2 {
			t.Errorf("expected 2 skills, got: %d", len(res.Skills))
		}
		if len(res.Work) != 1 || res.Work[0].CompanyName() != "CloudScale Inc" {
			t.Errorf("expected 1 work at CloudScale Inc, got: %+v", res.Work)
		}
	})

	t.Run("Markdown code fences are stripped cleanly", func(t *testing.T) {
		fenced := "```json\n" + sampleResumeJSON + "\n```"
		completer := llm.CompleterFunc(func(_ context.Context, _, _, _, _ string) (string, error) {
			return fenced, nil
		})
		extractor := llm.NewExtractor(completer)

		res, _, err := extractor.ExtractResume(context.Background(), "Sample raw text", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Alex Mercer" {
			t.Errorf("expected name 'Alex Mercer', got: %q", res.Basics.Name)
		}
	})

	t.Run("Empty resume text returns validation error", func(t *testing.T) {
		completer := llm.CompleterFunc(func(_ context.Context, _, _, _, _ string) (string, error) {
			return sampleResumeJSON, nil
		})
		extractor := llm.NewExtractor(completer)

		_, _, err := extractor.ExtractResume(context.Background(), "   \n\t  ", "", "")
		if err == nil {
			t.Fatal("expected validation error for empty text, got nil")
		}
		if !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("Malformed JSON from LLM returns validation error", func(t *testing.T) {
		completer := llm.CompleterFunc(func(_ context.Context, _, _, _, _ string) (string, error) {
			return "{ truncated json...", nil
		})
		extractor := llm.NewExtractor(completer)

		_, _, err := extractor.ExtractResume(context.Background(), "Valid text", "", "")
		if err == nil {
			t.Fatal("expected validation error for malformed JSON, got nil")
		}
		if !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("Empty response from LLM returns validation error", func(t *testing.T) {
		completer := llm.CompleterFunc(func(_ context.Context, _, _, _, _ string) (string, error) {
			return "   ", nil
		})
		extractor := llm.NewExtractor(completer)

		_, _, err := extractor.ExtractResume(context.Background(), "Valid text", "", "")
		if err == nil {
			t.Fatal("expected error for empty LLM response, got nil")
		}
		if !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("Upstream completer error is propagated", func(t *testing.T) {
		upstreamErr := errors.New("upstream connection reset")
		completer := llm.CompleterFunc(func(_ context.Context, _, _, _, _ string) (string, error) {
			return "", upstreamErr
		})
		extractor := llm.NewExtractor(completer)

		_, _, err := extractor.ExtractResume(context.Background(), "Valid text", "", "")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, upstreamErr) {
			t.Errorf("expected upstreamErr, got: %v", err)
		}
	})

	t.Run("Context cancelled returns error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		completer := llm.CompleterFunc(func(_ context.Context, _, _, _, _ string) (string, error) {
			return sampleResumeJSON, nil
		})
		extractor := llm.NewExtractor(completer)

		_, _, err := extractor.ExtractResume(ctx, "Valid text", "", "")
		if err == nil {
			t.Fatal("expected error for cancelled context, got nil")
		}
	})

	t.Run("Minimal resume returns valid JSONResume", func(t *testing.T) {
		minimalJSON := `{"basics": {"name": "Only Name", "email": "only@test.com"}}`
		completer := llm.CompleterFunc(func(_ context.Context, _, _, _, _ string) (string, error) {
			return minimalJSON, nil
		})
		extractor := llm.NewExtractor(completer)

		res, _, err := extractor.ExtractResume(context.Background(), "Only Name only@test.com", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Only Name" || res.Basics.Email != "only@test.com" {
			t.Errorf("unexpected minimal resume content: %+v", res)
		}
	})
}

type stubUsageCompleter struct {
	response  string
	telemetry core.LLMTelemetry
	err       error
}

func (s *stubUsageCompleter) AvailableModels() []core.ModelOption {
	return nil
}

func (s *stubUsageCompleter) SupportsStructuredCompletion(_, _ string) bool {
	return false
}

func (s *stubUsageCompleter) Complete(_ context.Context, _ llm.CompletionRequest) (string, core.LLMTelemetry, error) {
	return s.response, s.telemetry, s.err
}

func TestResumeExtractor_WithUsage(t *testing.T) {
	minimalJSON := `{"basics": {"name": "Usage Test", "email": "usage@test.com"}}`

	t.Run("Completer propagates actual token usage and telemetry", func(t *testing.T) {
		completer := &stubUsageCompleter{
			response: minimalJSON,
			telemetry: core.LLMTelemetry{
				Provider:   "test-prov",
				Model:      "test-mod",
				StatusCode: 200,
				Latency:    12 * time.Millisecond,
				Usage: core.TokenUsage{
					PromptTokens:     150,
					CompletionTokens: 250,
					TotalTokens:      400,
				},
			},
		}
		extractor := llm.NewExtractor(completer)

		res, tel, err := extractor.ExtractResume(context.Background(), "Sample text", "test-prov", "test-mod")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Usage Test" {
			t.Errorf("expected Basics.Name 'Usage Test', got %q", res.Basics.Name)
		}
		if tel.Usage.TotalTokens != 400 || tel.Usage.PromptTokens != 150 || tel.Usage.CompletionTokens != 250 {
			t.Errorf("expected 400 total tokens (150/250), got %+v", tel.Usage)
		}
		if tel.Provider != "test-prov" || tel.Model != "test-mod" || tel.StatusCode != 200 || tel.Latency != 12*time.Millisecond {
			t.Errorf("unexpected telemetry metadata: %+v", tel)
		}
	})

	t.Run("CompleterFunc fallback returns zero usage without error", func(t *testing.T) {
		completer := llm.CompleterFunc(func(_ context.Context, _, _, _, _ string) (string, error) {
			return minimalJSON, nil
		})
		extractor := llm.NewExtractor(completer)

		res, tel, err := extractor.ExtractResume(context.Background(), "Sample text", "test-prov", "test-mod")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Usage Test" {
			t.Errorf("expected Basics.Name 'Usage Test', got %q", res.Basics.Name)
		}
		if !tel.IsZero() {
			t.Errorf("expected zero telemetry fallback, got %+v", tel)
		}
	})

	t.Run("ExtractResumeWithTelemetry applies default tokens when usage is zero", func(t *testing.T) {
		completer := llm.CompleterFunc(func(_ context.Context, _, _, _, _ string) (string, error) {
			return minimalJSON, nil
		})
		extractor := llm.NewExtractor(completer)

		res, tel, err := llm.ExtractResumeWithTelemetry(context.Background(), extractor, "Sample text", "test-prov", "test-mod", 1000)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Usage Test" {
			t.Errorf("expected Basics.Name 'Usage Test', got %q", res.Basics.Name)
		}
		if tel.Usage.TotalTokens != 1000 {
			t.Errorf("expected default 1000 total tokens, got %+v", tel.Usage)
		}
	})
}

func TestExtractResumeWithTelemetry_Table(t *testing.T) {
	minimalJSON := `{"basics": {"name": "Ada Lovelace", "email": "ada@example.com"}}`

	cases := []struct {
		name           string
		completer      llm.Completer
		defaultTokens  int
		wantErr        error
		wantName       string
		wantProvider   string
		wantModel      string
		wantStatusCode int
		wantMinLatency time.Duration
		wantUsage      core.TokenUsage
	}{
		{
			name: "real metadata commits normalized tokens and does not apply fallback",
			completer: &stubUsageCompleter{
				response: minimalJSON,
				telemetry: core.LLMTelemetry{
					Provider:   llm.ProviderOpenAI,
					Model:      "gpt-4o",
					StatusCode: 200,
					Latency:    20 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 120, CompletionTokens: 45},
				},
			},
			defaultTokens:  1000,
			wantName:       "Ada Lovelace",
			wantProvider:   llm.ProviderOpenAI,
			wantModel:      "gpt-4o",
			wantStatusCode: 200,
			wantMinLatency: 20 * time.Millisecond,
			wantUsage:      core.TokenUsage{PromptTokens: 120, CompletionTokens: 45, TotalTokens: 165},
		},
		{
			name: "zero usage on success applies defaultTokens while preserving provider status and latency",
			completer: &stubUsageCompleter{
				response: minimalJSON,
				telemetry: core.LLMTelemetry{
					Provider:   llm.ProviderCompat,
					Model:      "local-model",
					StatusCode: 200,
					Latency:    14 * time.Millisecond,
				},
			},
			defaultTokens:  1000,
			wantName:       "Ada Lovelace",
			wantProvider:   llm.ProviderCompat,
			wantModel:      "local-model",
			wantStatusCode: 200,
			wantMinLatency: 14 * time.Millisecond,
			wantUsage:      core.TokenUsage{TotalTokens: 1000},
		},
		{
			name: "completer error preserves upstream telemetry and never applies defaultTokens",
			completer: &stubUsageCompleter{
				telemetry: core.LLMTelemetry{
					Provider:   llm.ProviderOpenAI,
					Model:      "gpt-4o",
					StatusCode: 429,
					Latency:    9 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 8},
				},
				err: llm.ErrRateLimited,
			},
			defaultTokens:  1000,
			wantErr:        llm.ErrRateLimited,
			wantProvider:   llm.ProviderOpenAI,
			wantModel:      "gpt-4o",
			wantStatusCode: 429,
			wantMinLatency: 9 * time.Millisecond,
			wantUsage:      core.TokenUsage{PromptTokens: 8, TotalTokens: 8},
		},
		{
			name: "HTTP 200 malformed JSON preserves real usage and status without applying defaultTokens",
			completer: &stubUsageCompleter{
				response: "{not-valid-json",
				telemetry: core.LLMTelemetry{
					Provider:   llm.ProviderAnthropic,
					Model:      "claude-sonnet-4-5",
					StatusCode: 200,
					Latency:    18 * time.Millisecond,
					Usage:      core.TokenUsage{PromptTokens: 25, CompletionTokens: 10},
				},
			},
			defaultTokens:  1000,
			wantErr:        core.ErrValidation,
			wantProvider:   llm.ProviderAnthropic,
			wantModel:      "claude-sonnet-4-5",
			wantStatusCode: 200,
			wantMinLatency: 18 * time.Millisecond,
			wantUsage:      core.TokenUsage{PromptTokens: 25, CompletionTokens: 10, TotalTokens: 35},
		},
		{
			name: "HTTP 200 empty JSON preserves status and zero usage without applying defaultTokens",
			completer: &stubUsageCompleter{
				response: "   ",
				telemetry: core.LLMTelemetry{
					Provider:   llm.ProviderGemini,
					Model:      "gemini-2.5-flash",
					StatusCode: 200,
					Latency:    7 * time.Millisecond,
				},
			},
			defaultTokens:  1000,
			wantErr:        core.ErrValidation,
			wantProvider:   llm.ProviderGemini,
			wantModel:      "gemini-2.5-flash",
			wantStatusCode: 200,
			wantMinLatency: 7 * time.Millisecond,
			wantUsage:      core.TokenUsage{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ext := llm.NewExtractor(tc.completer)
			res, tel, err := llm.ExtractResumeWithTelemetry(context.Background(), ext, "Ada Lovelace resume", tc.wantProvider, tc.wantModel, tc.defaultTokens)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				if res != nil {
					t.Errorf("expected nil resume on error, got %+v", res)
				}
				errTel, ok := core.LLMTelemetryFromError(err)
				if !ok {
					t.Fatalf("expected telemetry attached to error, got none")
				}
				if errTel != tel {
					t.Errorf("error telemetry %+v != returned telemetry %+v", errTel, tel)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if res == nil || res.Basics.Name != tc.wantName {
					t.Errorf("expected Basics.Name %q, got %+v", tc.wantName, res)
				}
			}

			if tel.Provider != tc.wantProvider || tel.Model != tc.wantModel || tel.StatusCode != tc.wantStatusCode {
				t.Errorf("telemetry = %+v, want provider=%q model=%q status=%d", tel, tc.wantProvider, tc.wantModel, tc.wantStatusCode)
			}
			if tel.Latency < tc.wantMinLatency {
				t.Errorf("telemetry latency = %v, want >= %v", tel.Latency, tc.wantMinLatency)
			}
			if tel.Usage != tc.wantUsage {
				t.Errorf("telemetry usage = %+v, want %+v", tel.Usage, tc.wantUsage)
			}
		})
	}
}

func TestRegistryExtractor_StructuredVsFallbackRegistryProviders(t *testing.T) {
	resumeJSON := `{"basics":{"name":"Ada Lovelace","email":"ada@analytical.org"}}`

	t.Run("structured provider attaches ResumeExtractResponseSchema and omits <response_schema>", func(t *testing.T) {
		stub := &transportStubClient{
			name:       llm.ProviderOpenAI,
			structured: true,
			responses:  []string{resumeJSON, resumeJSON, resumeJSON},
		}
		reg := llm.NewRegistry()
		reg.Register(stub)
		ext := llm.NewRegistryExtractor(reg)

		res, tel, err := llm.ExtractResumeWithTelemetry(context.Background(), ext, "Ada Lovelace resume", llm.ProviderOpenAI, "gpt-4o", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Ada Lovelace" || tel.Usage.TotalTokens != 30 {
			t.Errorf("unexpected extracted result or telemetry: %+v / %+v", res, tel)
		}
		if tel.Provider != llm.ProviderOpenAI || tel.Model != "gpt-4o" || tel.StatusCode != 200 {
			t.Errorf("unexpected telemetry metadata: %+v", tel)
		}
		if len(stub.reqs) != 1 {
			t.Fatalf("expected 1 transport call, got %d", len(stub.reqs))
		}
		if strings.Contains(stub.reqs[0].SystemPrompt, "<response_schema>") {
			t.Errorf("expected <response_schema> omitted for structured provider")
		}
		if stub.reqs[0].ResponseSchema == nil {
			t.Errorf("expected ResponseSchema attached for structured provider")
		}
	})

	t.Run("fallback provider appends <response_schema> and leaves ResponseSchema nil", func(t *testing.T) {
		stub := &transportStubClient{
			name:       llm.ProviderCompat,
			structured: false,
			responses:  []string{resumeJSON},
		}
		reg := llm.NewRegistry()
		reg.Register(stub)
		ext := llm.NewRegistryExtractor(reg)

		res, tel, err := llm.ExtractResumeWithTelemetry(context.Background(), ext, "Ada Lovelace resume", llm.ProviderCompat, "local", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Ada Lovelace" || tel.Provider != llm.ProviderCompat || tel.Model != "local" {
			t.Errorf("unexpected extracted result or telemetry: %+v / %+v", res, tel)
		}
		if len(stub.reqs) != 1 {
			t.Fatalf("expected 1 transport call, got %d", len(stub.reqs))
		}
		if strings.Count(stub.reqs[0].SystemPrompt, "<response_schema>") != 1 {
			t.Errorf("expected single <response_schema> in fallback system prompt")
		}
		if stub.reqs[0].ResponseSchema != nil {
			t.Errorf("expected ResponseSchema == nil for fallback provider")
		}
	})

	t.Run("registry with cleared DefaultProvider still resolves registered provider via Registry.Resolve", func(t *testing.T) {
		stub := &transportStubClient{
			name:       llm.ProviderOpenAI,
			structured: true,
			responses:  []string{resumeJSON, resumeJSON, resumeJSON},
		}
		reg := llm.NewRegistry()
		reg.Register(stub)
		reg.SetDefault("", "")

		ext := llm.NewRegistryExtractor(reg)
		res, _, err := llm.ExtractResumeWithTelemetry(context.Background(), ext, "Ada Lovelace resume", "", "", 0)
		if err != nil {
			t.Fatalf("expected Registry.Resolve fallback to registered provider when DefaultProvider is cleared, got error: %v", err)
		}
		if res.Basics.Name != "Ada Lovelace" {
			t.Errorf("unexpected extracted name %q", res.Basics.Name)
		}

		if catModels := ext.AvailableModels(); len(catModels) != 1 {
			t.Errorf("expected 1 catalog model from RegistryCompleter, got %+v", catModels)
		}
		regComp := llm.NewRegistryCompleter(reg)
		txt, tel, err := regComp.Complete(context.Background(), llm.CompletionRequest{
			Operation:    "CompleteText",
			SystemPrompt: "sys",
			UserPrompt:   "usr",
			Provider:     llm.ProviderOpenAI,
			Model:        "gpt-4o",
		})
		if err != nil || txt != resumeJSON || tel.Provider != llm.ProviderOpenAI || tel.Model != "gpt-4o" {
			t.Errorf("unexpected regComp.Complete result %q, tel=%+v, err=%v", txt, tel, err)
		}
		cpTxt, cpTel, err := reg.CompleteStructured(context.Background(), stub, "CompleteText", llm.Request{
			Model:        "gpt-4o",
			SystemPrompt: "sys",
			UserPrompt:   "usr",
		})
		if err != nil || cpTxt != resumeJSON || cpTel.Provider != llm.ProviderOpenAI {
			t.Errorf("unexpected Registry.CompleteStructured result %q, tel=%+v, err=%v", cpTxt, cpTel, err)
		}
	})

	t.Run("unstructured Completer with catalog receives <response_schema> and nil transport schema", func(t *testing.T) {
		custom := &unstructuredCatalogCompleter{response: resumeJSON}
		ext := llm.NewExtractor(custom)
		if models := ext.AvailableModels(); len(models) != 1 || models[0].Model != "custom-m1" {
			t.Errorf("expected catalog models exposed via AvailableModels, got %+v", models)
		}

		res, tel, err := llm.ExtractResumeWithTelemetry(context.Background(), ext, "Ada Lovelace resume", "custom", "custom-m1", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Ada Lovelace" || tel.Usage.TotalTokens != 19 || tel.Provider != "custom" || tel.Model != "custom-m1" {
			t.Errorf("unexpected extracted result or telemetry: %+v / %+v", res, tel)
		}
		if strings.Count(custom.lastSysPrompt, "<response_schema>") != 1 {
			t.Errorf("expected unstructured Completer to receive <response_schema>, got count=%d", strings.Count(custom.lastSysPrompt, "<response_schema>"))
		}
		if custom.lastSchema != nil {
			t.Errorf("expected nil Schema for unstructured Completer, got %+v", custom.lastSchema)
		}
	})
}

type unstructuredCatalogCompleter struct {
	response      string
	lastSysPrompt string
	lastSchema    *llm.Schema
}

func (c *unstructuredCatalogCompleter) SupportsStructuredCompletion(_, _ string) bool {
	return false
}

func (c *unstructuredCatalogCompleter) AvailableModels() []core.ModelOption {
	return []core.ModelOption{{Provider: "custom", Model: "custom-m1", DisplayName: "Custom M1", IsDefault: true}}
}

func (c *unstructuredCatalogCompleter) Complete(_ context.Context, req llm.CompletionRequest) (string, core.LLMTelemetry, error) {
	c.lastSysPrompt = req.SystemPrompt
	c.lastSchema = req.Schema
	return c.response, core.LLMTelemetry{
		Provider:   "custom",
		Model:      "custom-m1",
		StatusCode: 200,
		Latency:    15 * time.Millisecond,
		Usage:      core.TokenUsage{PromptTokens: 12, CompletionTokens: 7, TotalTokens: 19},
	}, nil
}

func TestExtractor_Concurrent_Race(t *testing.T) {
	resumeJSON := `{"basics":{"name":"Ada Lovelace","email":"ada@analytical.org"}}`
	completer := &stubUsageCompleter{
		response: resumeJSON,
		telemetry: core.LLMTelemetry{
			Provider:   llm.ProviderOpenAI,
			Model:      "gpt-4o",
			StatusCode: 200,
			Latency:    5 * time.Millisecond,
			Usage:      core.TokenUsage{PromptTokens: 10, CompletionTokens: 20},
		},
	}
	ext := llm.NewExtractor(completer)

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, tel, err := llm.ExtractResumeWithTelemetry(context.Background(), ext, "Ada Lovelace resume", llm.ProviderOpenAI, "gpt-4o", 1000)
			if err != nil {
				t.Errorf("concurrent ExtractResumeWithTelemetry failed: %v", err)
				return
			}
			if res == nil || res.Basics.Name != "Ada Lovelace" || tel.Usage.TotalTokens != 30 {
				t.Errorf("unexpected concurrent extraction result: res=%+v tel=%+v", res, tel)
			}
		}()
	}
	wg.Wait()
}

// assertSchemaSectionHasNonNullableHighlights verifies section[].highlights exists and is non-nullable when checkStrict is true.
func assertSchemaSectionHasNonNullableHighlights(t *testing.T, schema *llm.Schema, section string, checkStrict bool) {
	t.Helper()
	if schema == nil {
		t.Fatalf("schema is nil checking %s[].highlights", section)
	}
	sec := schema.Properties[section]
	if sec == nil || sec.Items == nil {
		t.Fatalf("schema missing %s array items", section)
	}
	hl := sec.Items.Properties["highlights"]
	if hl == nil {
		t.Fatalf("schema missing %s[].highlights property", section)
	}
	if !hl.Type.Contains(llm.TypeArray) {
		t.Errorf("%s[].highlights expected array type, got %v", section, hl.Type)
	}
	if checkStrict && isPropertyNullable(hl) {
		t.Errorf("%s[].highlights must remain non-nullable in Strict() schema, got %+v", section, hl)
	}
}

// TestResumeExtractor_ExtractsSectionHighlights verifies structured and fallback extraction paths
// preserve highlight extraction prompts, schema contracts, and decoded section highlights.
func TestResumeExtractor_ExtractsSectionHighlights(t *testing.T) {
	rawResumeText := `Alex Mercer - Staff Engineer
Work: CloudScale Inc (2021-Present)
- Reduced p99 replication latency by 45%
Projects: MiniRaft
- Passed Jepsen deterministic fault-injection suite
Education: University of Washington, B.S. Computer Science
- Summa Cum Laude
- Senior thesis on Byzantine quorum reconfiguration
Volunteer: CodeForAll Mentor
- Coached 12 first-generation engineering students`

	fullResumeJSON := `{
		"basics": {"name": "Alex Mercer", "summary": "Staff systems engineer."},
		"work": [{
			"name": "CloudScale Inc",
			"position": "Staff Engineer",
			"summary": "Distributed storage engineering.",
			"highlights": ["Reduced p99 replication latency by 45%"]
		}],
		"projects": [{
			"name": "MiniRaft",
			"description": "Educational Raft implementation in Go.",
			"highlights": ["Passed Jepsen deterministic fault-injection suite"],
			"keywords": ["Go", "Raft"]
		}],
		"education": [{
			"institution": "University of Washington",
			"area": "Computer Science",
			"studyType": "B.S.",
			"courses": ["Distributed Systems"],
			"highlights": ["Summa Cum Laude", "Senior thesis on Byzantine quorum reconfiguration"]
		}],
		"volunteer": [{
			"organization": "CodeForAll",
			"position": "Mentor",
			"summary": "Community engineering mentorship.",
			"highlights": ["Coached 12 first-generation engineering students"]
		}]
	}`

	highlightedSections := []string{"work", "projects", "education", "volunteer"}

	t.Run("structured registry provider OpenAI", func(t *testing.T) {
		stub := &transportStubClient{
			name:       llm.ProviderOpenAI,
			structured: true,
			responses:  []string{fullResumeJSON},
		}
		reg := llm.NewRegistry()
		reg.Register(stub)
		ext := llm.NewRegistryExtractor(reg)

		res, _, err := ext.ExtractResume(context.Background(), rawResumeText, llm.ProviderOpenAI, "gpt-4o")
		if err != nil {
			t.Fatalf("ExtractResume failed: %v", err)
		}
		if len(stub.reqs) != 1 {
			t.Fatalf("expected 1 transport request, got %d", len(stub.reqs))
		}

		req := stub.reqs[0]
		if strings.Contains(req.SystemPrompt, "<response_schema>") {
			t.Errorf("structured provider must omit inline <response_schema>")
		}
		for _, term := range []string{"<instructions>", "<json_schema_hints>", "highlights", "volunteer", "education", "awards"} {
			if !strings.Contains(strings.ToLower(req.SystemPrompt), term) {
				t.Errorf("structured system prompt missing %q", term)
			}
		}
		if !strings.Contains(strings.ToLower(req.UserPrompt), "highlights") {
			t.Errorf("structured user prompt missing highlights guidance")
		}

		if req.ResponseSchema == nil {
			t.Fatal("expected ResponseSchema attached for structured provider")
		}
		strict := req.ResponseSchema.Strict()
		for _, section := range highlightedSections {
			assertSchemaSectionHasNonNullableHighlights(t, strict, section, true)
		}

		assertDecodedSectionHighlights(t, res)
	})

	t.Run("unstructured fallback registry provider Compat", func(t *testing.T) {
		stub := &transportStubClient{
			name:       llm.ProviderCompat,
			structured: false,
			responses:  []string{fullResumeJSON},
		}
		reg := llm.NewRegistry()
		reg.Register(stub)
		ext := llm.NewRegistryExtractor(reg)

		res, _, err := ext.ExtractResume(context.Background(), rawResumeText, llm.ProviderCompat, "local-model")
		if err != nil {
			t.Fatalf("ExtractResume failed: %v", err)
		}
		if len(stub.reqs) != 1 {
			t.Fatalf("expected 1 transport request, got %d", len(stub.reqs))
		}

		req := stub.reqs[0]
		if req.ResponseSchema != nil {
			t.Errorf("fallback provider must leave ResponseSchema nil, got %+v", req.ResponseSchema)
		}
		if strings.Count(req.SystemPrompt, "<response_schema>") != 1 {
			t.Fatalf("fallback provider must append single <response_schema>")
		}
		var embeddedSchema llm.Schema
		if err := json.Unmarshal([]byte(llm.ExtractJSON(req.SystemPrompt)), &embeddedSchema); err != nil {
			t.Fatalf("failed to parse fallback <response_schema>: %v", err)
		}
		for _, section := range highlightedSections {
			assertSchemaSectionHasNonNullableHighlights(t, &embeddedSchema, section, false)
		}
		if !strings.Contains(strings.ToLower(req.UserPrompt), "highlights") {
			t.Errorf("fallback user prompt missing highlights guidance")
		}

		assertDecodedSectionHighlights(t, res)
	})
}

// assertDecodedSectionHighlights verifies decoded JSONResume retains section highlights across work, projects, education, and volunteer.
func assertDecodedSectionHighlights(t *testing.T, res *core.JSONResume) {
	t.Helper()
	if res == nil {
		t.Fatal("expected non-nil decoded JSONResume")
	}
	if len(res.Work) != 1 || !slices.Equal(res.Work[0].Highlights, []string{"Reduced p99 replication latency by 45%"}) {
		t.Errorf("unexpected Work highlights: %+v", res.Work)
	}
	if len(res.Projects) != 1 || !slices.Equal(res.Projects[0].Highlights, []string{"Passed Jepsen deterministic fault-injection suite"}) {
		t.Errorf("unexpected Projects highlights: %+v", res.Projects)
	}
	if len(res.Education) != 1 || !slices.Equal(res.Education[0].Highlights, []string{"Summa Cum Laude", "Senior thesis on Byzantine quorum reconfiguration"}) {
		t.Errorf("unexpected Education highlights: %+v", res.Education)
	}
	if len(res.Volunteer) != 1 || !slices.Equal(res.Volunteer[0].Highlights, []string{"Coached 12 first-generation engineering students"}) {
		t.Errorf("unexpected Volunteer highlights: %+v", res.Volunteer)
	}
}
