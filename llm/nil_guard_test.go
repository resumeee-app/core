package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/resumeee-app/resumeee-core"
)

// failingResumeExtractor is a core.ResumeExtractor that always fails while reporting zero usage.
type failingResumeExtractor struct {
	err error
}

// ExtractResume returns the configured error.
func (f failingResumeExtractor) ExtractResume(_ context.Context, _, _, _ string) (*core.JSONResume, error) {
	return nil, f.err
}

// ExtractResumeWithUsage returns the configured error with zero token usage.
func (f failingResumeExtractor) ExtractResumeWithUsage(_ context.Context, _, _, _ string) (*core.JSONResume, core.TokenUsage, error) {
	return nil, core.TokenUsage{}, f.err
}

// TestNilGuards_ClientProvider verifies a nil *ClientProvider or nil transport client returns
// zero values or ErrNoProviders instead of panicking.
func TestNilGuards_ClientProvider(t *testing.T) {
	ctx := context.Background()
	providers := map[string]*ClientProvider{
		"nil receiver":         nil,
		"nil transport client": NewClientProvider(nil),
	}

	for name, p := range providers {
		t.Run(name, func(t *testing.T) {
			if got := p.Name(); got != "" {
				t.Errorf("expected empty Name, got %q", got)
			}
			if got := p.Models(); got != nil {
				t.Errorf("expected nil Models, got %+v", got)
			}
			if p.SupportsStructuredOutput("any-model") {
				t.Error("expected SupportsStructuredOutput=false")
			}
			if _, _, err := p.CompleteStructured(ctx, "op", core.LLMCompletionRequest{}); !errors.Is(err, ErrNoProviders) {
				t.Errorf("expected CompleteStructured ErrNoProviders, got %v", err)
			}
			if resp, err := p.GenerateTailoredResume(ctx, core.GenerationRequest{}); !errors.Is(err, ErrNoProviders) || resp != nil {
				t.Errorf("expected GenerateTailoredResume (nil, ErrNoProviders), got (%v, %v)", resp, err)
			}
		})
	}
}

// TestNilGuards_RegistryCompleter verifies a nil *RegistryCompleter or nil Registry reports no
// catalog, no structured support, and ErrNoProviders on Complete.
func TestNilGuards_RegistryCompleter(t *testing.T) {
	completers := map[string]*RegistryCompleter{
		"nil receiver": nil,
		"nil registry": NewRegistryCompleter(nil),
	}

	for name, rc := range completers {
		t.Run(name, func(t *testing.T) {
			if got := rc.AvailableModels(); got != nil {
				t.Errorf("expected nil AvailableModels, got %+v", got)
			}
			if rc.SupportsStructuredCompletion("p", "m") {
				t.Error("expected SupportsStructuredCompletion=false")
			}
			if _, _, err := rc.Complete(context.Background(), "op", "sys", "usr", "p", "m", "", nil); !errors.Is(err, ErrNoProviders) {
				t.Errorf("expected ErrNoProviders, got %v", err)
			}
		})
	}
}

// TestNilGuards_ResumeExtraction verifies nil extractors and completers fail closed with
// core.ErrValidation, and that default token accounting is never applied to failed calls.
func TestNilGuards_ResumeExtraction(t *testing.T) {
	ctx := context.Background()

	t.Run("nil *ResumeExtractor and nil Completer return ErrValidation", func(t *testing.T) {
		extractors := map[string]*ResumeExtractor{
			"nil receiver":  nil,
			"nil completer": NewResumeExtractor(nil),
		}
		for name, e := range extractors {
			if got := e.AvailableModels(); got != nil {
				t.Errorf("%s: expected nil AvailableModels, got %+v", name, got)
			}
			if res, _, err := e.ExtractResumeWithUsage(ctx, "resume text", "p", "m"); !errors.Is(err, core.ErrValidation) || res != nil {
				t.Errorf("%s: expected (nil, ErrValidation), got (%v, %v)", name, res, err)
			}
		}
	})

	t.Run("nil CompleterFunc returns ErrValidation", func(t *testing.T) {
		if _, _, err := CompleterFunc(nil).Complete(ctx, "op", "sys", "usr", "p", "m", "", nil); !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation, got %v", err)
		}
	})

	t.Run("ExtractResumeWithTokenUsage fails closed on nil extractor", func(t *testing.T) {
		res, usage, err := ExtractResumeWithTokenUsage(ctx, nil, "resume text", "p", "m", 1000)
		if !errors.Is(err, core.ErrValidation) {
			t.Fatalf("expected ErrValidation, got %v", err)
		}
		if res != nil || usage.TotalTokens != 0 {
			t.Errorf("expected nil resume and zero usage, got (%v, %+v)", res, usage)
		}
	})

	t.Run("ExtractResumeWithTokenUsage does not apply default tokens on error", func(t *testing.T) {
		errExtract := errors.New("extraction failed")
		res, usage, err := ExtractResumeWithTokenUsage(ctx, failingResumeExtractor{err: errExtract}, "resume text", "p", "m", 1000)
		if !errors.Is(err, errExtract) {
			t.Fatalf("expected extraction error, got %v", err)
		}
		if res != nil || usage.TotalTokens != 0 {
			t.Errorf("expected nil resume and zero usage on error, got (%v, %+v)", res, usage)
		}
	})
}
