package llm_test

import (
	"context"
	"errors"
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
		ue, ok := extractor.(llm.UsageResumeExtractor)
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
		ue, ok := extractor.(llm.UsageResumeExtractor)
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
