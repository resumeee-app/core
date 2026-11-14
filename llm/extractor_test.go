package llm_test

import (
	"context"
	"errors"
	"strings"
	"testing"

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
		completer := llm.CompleterFunc(func(ctx context.Context, sys, user, prov, model string) (string, error) {
			return sampleResumeJSON, nil
		})
		extractor := llm.NewResumeExtractor(completer)

		res, err := extractor.ExtractResume(context.Background(), "Alex Mercer resume text...", "mock", "mock-model")
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
		if len(res.Work) != 1 || res.Work[0].GetCompany() != "CloudScale Inc" {
			t.Errorf("expected 1 work at CloudScale Inc, got: %+v", res.Work)
		}
	})

	t.Run("Markdown code fences are stripped cleanly", func(t *testing.T) {
		fenced := "```json\n" + sampleResumeJSON + "\n```"
		completer := llm.CompleterFunc(func(ctx context.Context, sys, user, prov, model string) (string, error) {
			return fenced, nil
		})
		extractor := llm.NewResumeExtractor(completer)

		res, err := extractor.ExtractResume(context.Background(), "Sample raw text", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Alex Mercer" {
			t.Errorf("expected name 'Alex Mercer', got: %q", res.Basics.Name)
		}
	})

	t.Run("Empty resume text returns validation error", func(t *testing.T) {
		completer := llm.CompleterFunc(func(ctx context.Context, sys, user, prov, model string) (string, error) {
			return sampleResumeJSON, nil
		})
		extractor := llm.NewResumeExtractor(completer)

		_, err := extractor.ExtractResume(context.Background(), "   \n\t  ", "", "")
		if err == nil {
			t.Fatal("expected validation error for empty text, got nil")
		}
		if !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("Malformed JSON from LLM returns validation error", func(t *testing.T) {
		completer := llm.CompleterFunc(func(ctx context.Context, sys, user, prov, model string) (string, error) {
			return "{ truncated json...", nil
		})
		extractor := llm.NewResumeExtractor(completer)

		_, err := extractor.ExtractResume(context.Background(), "Valid text", "", "")
		if err == nil {
			t.Fatal("expected validation error for malformed JSON, got nil")
		}
		if !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("Empty response from LLM returns validation error", func(t *testing.T) {
		completer := llm.CompleterFunc(func(ctx context.Context, sys, user, prov, model string) (string, error) {
			return "   ", nil
		})
		extractor := llm.NewResumeExtractor(completer)

		_, err := extractor.ExtractResume(context.Background(), "Valid text", "", "")
		if err == nil {
			t.Fatal("expected error for empty LLM response, got nil")
		}
		if !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("Upstream completer error is propagated", func(t *testing.T) {
		upstreamErr := errors.New("upstream connection reset")
		completer := llm.CompleterFunc(func(ctx context.Context, sys, user, prov, model string) (string, error) {
			return "", upstreamErr
		})
		extractor := llm.NewResumeExtractor(completer)

		_, err := extractor.ExtractResume(context.Background(), "Valid text", "", "")
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

		completer := llm.CompleterFunc(func(ctx context.Context, sys, user, prov, model string) (string, error) {
			return sampleResumeJSON, nil
		})
		extractor := llm.NewResumeExtractor(completer)

		_, err := extractor.ExtractResume(ctx, "Valid text", "", "")
		if err == nil {
			t.Fatal("expected error for cancelled context, got nil")
		}
	})

	t.Run("Minimal resume returns valid JSONResume", func(t *testing.T) {
		minimalJSON := `{"basics": {"name": "Only Name", "email": "only@test.com"}}`
		completer := llm.CompleterFunc(func(ctx context.Context, sys, user, prov, model string) (string, error) {
			return minimalJSON, nil
		})
		extractor := llm.NewResumeExtractor(completer)

		res, err := extractor.ExtractResume(context.Background(), "Only Name only@test.com", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Only Name" || res.Basics.Email != "only@test.com" {
			t.Errorf("unexpected minimal resume content: %+v", res)
		}
	})
}

type stubUsageCompleter struct {
	response string
	usage    core.TokenUsage
}

func (s *stubUsageCompleter) Complete(ctx context.Context, systemPrompt, userPrompt, provider, model string) (string, error) {
	return s.response, nil
}

func (s *stubUsageCompleter) CompleteWithUsage(ctx context.Context, systemPrompt, userPrompt, provider, model string) (string, core.TokenUsage, error) {
	return s.response, s.usage, nil
}

func TestResumeExtractor_WithUsage(t *testing.T) {
	minimalJSON := `{"basics": {"name": "Usage Test", "email": "usage@test.com"}}`

	t.Run("UsageCompleter propagates actual token usage", func(t *testing.T) {
		completer := &stubUsageCompleter{
			response: minimalJSON,
			usage: core.TokenUsage{
				PromptTokens:     150,
				CompletionTokens: 250,
				TotalTokens:      400,
			},
		}
		extractor := llm.NewResumeExtractor(completer)
		ue, ok := extractor.(core.UsageResumeExtractor)
		if !ok {
			t.Fatalf("expected extractor to implement UsageResumeExtractor")
		}

		res, usage, err := ue.ExtractResumeWithUsage(context.Background(), "Sample text", "test-prov", "test-mod")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Usage Test" {
			t.Errorf("expected Basics.Name 'Usage Test', got %q", res.Basics.Name)
		}
		if usage.TotalTokens != 400 || usage.PromptTokens != 150 || usage.CompletionTokens != 250 {
			t.Errorf("expected 400 total tokens (150/250), got %+v", usage)
		}
	})

	t.Run("Ordinary Completer fallback returns zero usage without error", func(t *testing.T) {
		completer := llm.CompleterFunc(func(ctx context.Context, sys, user, prov, model string) (string, error) {
			return minimalJSON, nil
		})
		extractor := llm.NewResumeExtractor(completer)
		ue, ok := extractor.(core.UsageResumeExtractor)
		if !ok {
			t.Fatalf("expected extractor to implement UsageResumeExtractor")
		}

		res, usage, err := ue.ExtractResumeWithUsage(context.Background(), "Sample text", "test-prov", "test-mod")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Usage Test" {
			t.Errorf("expected Basics.Name 'Usage Test', got %q", res.Basics.Name)
		}
		if usage.TotalTokens != 0 {
			t.Errorf("expected zero token usage fallback, got %+v", usage)
		}
	})

	t.Run("ExtractResumeWithTokenUsage applies default tokens when usage is zero", func(t *testing.T) {
		completer := llm.CompleterFunc(func(ctx context.Context, sys, user, prov, model string) (string, error) {
			return minimalJSON, nil
		})
		extractor := llm.NewResumeExtractor(completer)

		res, usage, err := llm.ExtractResumeWithTokenUsage(context.Background(), extractor, "Sample text", "test-prov", "test-mod", 1000)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Usage Test" {
			t.Errorf("expected Basics.Name 'Usage Test', got %q", res.Basics.Name)
		}
		if usage.TotalTokens != 1000 {
			t.Errorf("expected default 1000 total tokens, got %+v", usage)
		}
	})
}

func TestDefaultResumeExtractor_StructuredVsFallbackRegistryProviders(t *testing.T) {
	resumeJSON := `{"basics":{"name":"Ada Lovelace","email":"ada@analytical.org"}}`

	t.Run("structured provider attaches ResumeExtractResponseSchema and omits <response_schema>", func(t *testing.T) {
		stub := &transportStubClient{
			name:       llm.ProviderOpenAI,
			structured: true,
			responses:  []string{resumeJSON, resumeJSON, resumeJSON},
		}
		reg := llm.NewRegistry()
		reg.RegisterClient(stub)
		ext := llm.NewDefaultResumeExtractor(reg)

		res, usage, err := llm.ExtractResumeWithTokenUsage(context.Background(), ext, "Ada Lovelace resume", llm.ProviderOpenAI, "gpt-4o", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Ada Lovelace" || usage.TotalTokens != 30 {
			t.Errorf("unexpected extracted result or usage: %+v / %+v", res, usage)
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
		reg.RegisterClient(stub)
		ext := llm.NewDefaultResumeExtractor(reg)

		res, _, err := llm.ExtractResumeWithTokenUsage(context.Background(), ext, "Ada Lovelace resume", llm.ProviderCompat, "local", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Ada Lovelace" {
			t.Errorf("unexpected extracted result: %+v", res)
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
		reg.RegisterClient(stub)
		reg.SetDefault("", "")

		ext := llm.NewDefaultResumeExtractor(reg)
		res, _, err := llm.ExtractResumeWithTokenUsage(context.Background(), ext, "Ada Lovelace resume", "", "", 0)
		if err != nil {
			t.Fatalf("expected Registry.Resolve fallback to registered provider when DefaultProvider is cleared, got error: %v", err)
		}
		if res.Basics.Name != "Ada Lovelace" {
			t.Errorf("unexpected extracted name %q", res.Basics.Name)
		}

		if catModels := ext.(llm.ModelCatalogCompleter).AvailableModels(); len(catModels) != 1 {
			t.Errorf("expected 1 catalog model from registryCompleter, got %+v", catModels)
		}
		regComp := llm.NewRegistryCompleter(reg)
		txt, err := regComp.Complete(context.Background(), "sys", "usr", llm.ProviderOpenAI, "gpt-4o")
		if err != nil || txt != resumeJSON {
			t.Errorf("unexpected regComp.Complete result %q, err=%v", txt, err)
		}
		cp := llm.NewClientProvider(stub)
		cpTxt, err := cp.CompleteText(context.Background(), "sys", "usr", "gpt-4o")
		if err != nil || cpTxt != resumeJSON {
			t.Errorf("unexpected ClientProvider.CompleteText result %q, err=%v", cpTxt, err)
		}
	})

	t.Run("custom text-only Completer implementing StructuredOutputCapable still receives <response_schema>", func(t *testing.T) {
		custom := &capableTextOnlyCompleter{response: resumeJSON}
		ext := llm.NewResumeExtractor(custom)
		if models := ext.(llm.ModelCatalogCompleter).AvailableModels(); len(models) != 1 || models[0].Model != "custom-m1" {
			t.Errorf("expected ModelCatalogCompleter models exposed via AvailableModels, got %+v", models)
		}

		res, usage, err := llm.ExtractResumeWithTokenUsage(context.Background(), ext, "Ada Lovelace resume", "custom", "custom-m1", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Basics.Name != "Ada Lovelace" || usage.TotalTokens != 19 {
			t.Errorf("unexpected extracted result or usage: %+v / %+v", res, usage)
		}
		if strings.Count(custom.lastSysPrompt, "<response_schema>") != 1 {
			t.Errorf("expected text-only Completer to receive <response_schema> even if StructuredOutputCapable=true, got count=%d", strings.Count(custom.lastSysPrompt, "<response_schema>"))
		}
	})
}

type capableTextOnlyCompleter struct {
	response      string
	lastSysPrompt string
}

func (c *capableTextOnlyCompleter) SupportsStructuredOutput(_ string) bool {
	return true
}

func (c *capableTextOnlyCompleter) AvailableModels() []core.ModelOption {
	return []core.ModelOption{{Provider: "custom", Model: "custom-m1", DisplayName: "Custom M1", IsDefault: true}}
}

func (c *capableTextOnlyCompleter) Complete(ctx context.Context, sys, user, prov, model string) (string, error) {
	text, _, err := c.CompleteWithUsage(ctx, sys, user, prov, model)
	return text, err
}

func (c *capableTextOnlyCompleter) CompleteWithUsage(_ context.Context, sys, _, _, _ string) (string, core.TokenUsage, error) {
	c.lastSysPrompt = sys
	return c.response, core.TokenUsage{PromptTokens: 12, CompletionTokens: 7, TotalTokens: 19}, nil
}
