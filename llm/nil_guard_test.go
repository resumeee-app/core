package llm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/worker"
)

// failingResumeExtractor is a worker.ResumeExtractor that always fails while returning configured telemetry.
type failingResumeExtractor struct {
	telemetry core.LLMTelemetry
	err       error
}

// ExtractResume returns the configured error and telemetry.
func (f failingResumeExtractor) ExtractResume(_ context.Context, _, _, _ string) (*core.JSONResume, core.LLMTelemetry, error) {
	return nil, f.telemetry, f.err
}

var _ worker.ResumeExtractor = failingResumeExtractor{}

// TestNilGuards_RegistryAndService verifies nil receivers and nil clients fail closed with ErrNoProviders.
func TestNilGuards_RegistryAndService(t *testing.T) {
	ctx := context.Background()

	var nilReg *Registry
	if got, err := nilReg.Client("openai"); !errors.Is(err, ErrNoProviders) || got != nil {
		t.Errorf("expected nil Client and ErrNoProviders from nil *Registry, got (%+v, %v)", got, err)
	}
	if _, err := nilReg.Complete(ctx, nil, "op", Request{}); !errors.Is(err, ErrNoProviders) {
		t.Errorf("expected nilReg.Complete ErrNoProviders, got %v", err)
	}
	if _, tel, err := nilReg.CompleteStructured(ctx, nil, "op", Request{}); !errors.Is(err, ErrNoProviders) || !tel.IsZero() {
		t.Errorf("expected nilReg.CompleteStructured (\"\", zero, ErrNoProviders), got (%+v, %v)", tel, err)
	}

	reg := NewRegistry()
	if _, err := reg.Complete(ctx, nil, "op", Request{}); !errors.Is(err, ErrNoProviders) {
		t.Errorf("expected reg.Complete(nil) ErrNoProviders, got %v", err)
	}
	if _, tel, err := reg.CompleteStructured(ctx, nil, "op", Request{}); !errors.Is(err, ErrNoProviders) || !tel.IsZero() {
		t.Errorf("expected reg.CompleteStructured(nil) (\"\", zero, ErrNoProviders), got (%+v, %v)", tel, err)
	}

	services := map[string]*Service{
		"nil receiver": nil,
		"nil registry": NewService(nil),
	}
	for name, svc := range services {
		t.Run(name, func(t *testing.T) {
			if resp, err := svc.GenerateTailoredResume(ctx, core.GenerationRequest{}); !errors.Is(err, ErrNoProviders) || resp != nil {
				t.Errorf("expected GenerateTailoredResume (nil, ErrNoProviders), got (%v, %v)", resp, err)
			}
			if resp, err := svc.GenerateGapAnalysis(ctx, core.GenerationRequest{}); !errors.Is(err, ErrNoProviders) || resp != nil {
				t.Errorf("expected GenerateGapAnalysis (nil, ErrNoProviders), got (%v, %v)", resp, err)
			}
			if resp, err := svc.SimulateATS(ctx, core.ATSSimulationRequest{}); !errors.Is(err, ErrNoProviders) || resp != nil {
				t.Errorf("expected SimulateATS (nil, ErrNoProviders), got (%v, %v)", resp, err)
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
			if _, tel, err := rc.Complete(context.Background(), CompletionRequest{
				Operation:    "op",
				SystemPrompt: "sys",
				UserPrompt:   "usr",
				Provider:     "p",
				Model:        "m",
			}); !errors.Is(err, ErrNoProviders) || !tel.IsZero() {
				t.Errorf("expected (\"\", zero, ErrNoProviders), got (%+v, %v)", tel, err)
			}
		})
	}

	t.Run("empty registry returns ErrNoProviders and zero telemetry", func(t *testing.T) {
		rc := NewRegistryCompleter(NewRegistry())
		txt, tel, err := rc.Complete(context.Background(), CompletionRequest{
			Operation: "op",
			Provider:  ProviderOpenAI,
			Model:     "gpt-4o",
		})
		if !errors.Is(err, ErrNoProviders) || txt != "" || !tel.IsZero() {
			t.Errorf("expected (\"\", zero, ErrNoProviders), got (%q, %+v, %v)", txt, tel, err)
		}
	})

	t.Run("missing provider returns ErrProviderNotFound and zero telemetry", func(t *testing.T) {
		reg := NewRegistry()
		reg.Register(&fakeClient{name: ProviderOpenAI})
		rc := NewRegistryCompleter(reg)
		txt, tel, err := rc.Complete(context.Background(), CompletionRequest{
			Operation: "op",
			Provider:  "nonexistent",
			Model:     "m",
		})
		if !errors.Is(err, ErrProviderNotFound) || txt != "" || !tel.IsZero() {
			t.Errorf("expected (\"\", zero, ErrProviderNotFound), got (%q, %+v, %v)", txt, tel, err)
		}
	})
}

// TestNilGuards_ResumeExtraction verifies nil extractors and completers fail closed with
// core.ErrValidation, and that default token accounting is never applied to failed calls.
func TestNilGuards_ResumeExtraction(t *testing.T) {
	ctx := context.Background()

	t.Run("nil *Extractor and nil Completer return ErrValidation", func(t *testing.T) {
		extractors := map[string]*Extractor{
			"nil receiver":  nil,
			"nil completer": NewExtractor(nil),
		}
		for name, e := range extractors {
			if got := e.AvailableModels(); got != nil {
				t.Errorf("%s: expected nil AvailableModels, got %+v", name, got)
			}
			if res, tel, err := e.ExtractResume(ctx, "resume text", "p", "m"); !errors.Is(err, core.ErrValidation) || res != nil || !tel.IsZero() {
				t.Errorf("%s: expected (nil, zero, ErrValidation), got (%v, %+v, %v)", name, res, tel, err)
			}
		}
	})

	t.Run("nil CompleterFunc returns ErrValidation", func(t *testing.T) {
		if _, tel, err := CompleterFunc(nil).Complete(ctx, CompletionRequest{
			Operation:    "op",
			SystemPrompt: "sys",
			UserPrompt:   "usr",
			Provider:     "p",
			Model:        "m",
		}); !errors.Is(err, core.ErrValidation) || !tel.IsZero() {
			t.Errorf("expected (\"\", zero, ErrValidation), got (%+v, %v)", tel, err)
		}
	})

	t.Run("ExtractResumeWithTelemetry fails closed on nil extractor", func(t *testing.T) {
		res, tel, err := ExtractResumeWithTelemetry(ctx, nil, "resume text", "p", "m", 1000)
		if !errors.Is(err, core.ErrValidation) {
			t.Fatalf("expected ErrValidation, got %v", err)
		}
		if res != nil || !tel.IsZero() {
			t.Errorf("expected nil resume and zero telemetry, got (%v, %+v)", res, tel)
		}
	})

	t.Run("ExtractResumeWithTelemetry does not apply default tokens on error and preserves upstream telemetry", func(t *testing.T) {
		errExtract := errors.New("extraction failed")
		wantTel := core.LLMTelemetry{
			Provider:   "openai",
			Model:      "gpt-4o",
			StatusCode: 429,
			Latency:    18 * time.Millisecond,
			Usage:      core.TokenUsage{PromptTokens: 12},
		}
		res, tel, err := ExtractResumeWithTelemetry(ctx, failingResumeExtractor{
			telemetry: wantTel,
			err:       errExtract,
		}, "resume text", "openai", "gpt-4o", 1000)
		if !errors.Is(err, errExtract) {
			t.Fatalf("expected extraction error, got %v", err)
		}
		if res != nil {
			t.Errorf("expected nil resume on error, got %+v", res)
		}
		if tel.Provider != "openai" || tel.Model != "gpt-4o" || tel.StatusCode != 429 || tel.Latency != 18*time.Millisecond {
			t.Errorf("expected preserved upstream telemetry, got %+v", tel)
		}
		if tel.Usage.TotalTokens != 12 {
			t.Errorf("expected normalized partial prompt tokens (12) without fallback 1000, got %+v", tel.Usage)
		}
		errTel, ok := core.LLMTelemetryFromError(err)
		if !ok || errTel != tel {
			t.Errorf("expected attached error telemetry %+v, got (%+v, ok=%v)", tel, errTel, ok)
		}
	})

	t.Run("ExtractResumeWithTelemetry leaves zero usage as zero on error without applying fallback", func(t *testing.T) {
		errExtract := errors.New("extraction failed")
		res, tel, err := ExtractResumeWithTelemetry(ctx, failingResumeExtractor{err: errExtract}, "resume text", "p", "m", 1000)
		if !errors.Is(err, errExtract) {
			t.Fatalf("expected extraction error, got %v", err)
		}
		if res != nil || !tel.IsZero() {
			t.Errorf("expected nil resume and zero telemetry on error, got (%v, %+v)", res, tel)
		}
	})
}
