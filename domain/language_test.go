package domain_test

import (
	"errors"
	"strings"
	"testing"

	"resumeee/internal/domain"
)

func TestValidateLanguage_Defaults(t *testing.T) {
	lang, err := domain.ValidateLanguage("")
	if err != nil {
		t.Fatalf("expected nil error for empty language, got: %v", err)
	}
	if lang != domain.DefaultLanguage {
		t.Fatalf("expected default language %q, got: %q", domain.DefaultLanguage, lang)
	}

	langSpace, err := domain.ValidateLanguage("   ")
	if err != nil {
		t.Fatalf("expected nil error for whitespace language, got: %v", err)
	}
	if langSpace != domain.DefaultLanguage {
		t.Fatalf("expected default language %q, got: %q", domain.DefaultLanguage, langSpace)
	}
}

func TestValidateLanguage_SupportedLanguages(t *testing.T) {
	for _, expected := range domain.SupportedLanguages {
		t.Run(expected, func(t *testing.T) {
			got, err := domain.ValidateLanguage(expected)
			if err != nil {
				t.Fatalf("expected valid language %q, got error: %v", expected, err)
			}
			if got != expected {
				t.Fatalf("expected %q, got: %q", expected, got)
			}
		})
	}
}

func TestValidateLanguage_CaseInsensitiveAndCodes(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"english", "English"},
		{"ENGLISH", "English"},
		{"en", "English"},
		{"spanish", "Spanish"},
		{"ES", "Spanish"},
		{"french", "French"},
		{"fr", "French"},
		{"german", "German"},
		{"DE", "German"},
		{"japanese", "Japanese"},
		{"ja", "Japanese"},
		{"chinese (simplified)", "Chinese (Simplified)"},
		{"zh-cn", "Chinese (Simplified)"},
		{"zh", "Chinese (Simplified)"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := domain.ValidateLanguage(tc.input)
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if got != tc.expected {
				t.Fatalf("expected %q for input %q, got %q", tc.expected, tc.input, got)
			}
		})
	}
}

func TestValidateLanguage_RTLRejected(t *testing.T) {
	rtlCases := []string{
		"Arabic",
		"arabic",
		"ar",
		"Hebrew",
		"hebrew",
		"he",
		"Persian",
		"Farsi",
		"fa",
		"Urdu",
		"ur",
		"Yiddish",
		"yi",
	}

	for _, rtl := range rtlCases {
		t.Run(rtl, func(t *testing.T) {
			_, err := domain.ValidateLanguage(rtl)
			if err == nil {
				t.Fatalf("expected error for RTL language %q, got nil", rtl)
			}
			if !errors.Is(err, domain.ErrRTLLanguageNotSupported) {
				t.Fatalf("expected errors.Is(err, domain.ErrRTLLanguageNotSupported), got: %v", err)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "right-to-left") {
				t.Fatalf("expected error message to mention right-to-left, got: %v", err)
			}
		})
	}
}

func TestValidateLanguage_Unsupported(t *testing.T) {
	unsupportedCases := []string{
		"Klingon",
		"Elvish",
		"invalid-language-xyz",
	}

	for _, tc := range unsupportedCases {
		t.Run(tc, func(t *testing.T) {
			_, err := domain.ValidateLanguage(tc)
			if err == nil {
				t.Fatalf("expected error for unsupported language %q, got nil", tc)
			}
			if !errors.Is(err, domain.ErrUnsupportedLanguage) {
				t.Fatalf("expected errors.Is(err, domain.ErrUnsupportedLanguage), got: %v", err)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "unsupported") {
				t.Fatalf("expected error message to mention unsupported, got: %v", err)
			}
		})
	}
}

func TestTailoringStrategy_DefaultLanguageOnSave(t *testing.T) {
	strat := domain.TailoringStrategy{
		Name: "Custom Strategy",
	}
	if err := strat.Normalize(); err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if strat.Language != domain.DefaultLanguage {
		t.Fatalf("expected language %q, got %q", domain.DefaultLanguage, strat.Language)
	}
}

func TestTailoringStrategy_EffectiveLanguage(t *testing.T) {
	var nilStrat *domain.TailoringStrategy
	if got := nilStrat.EffectiveLanguage(); got != domain.DefaultLanguage {
		t.Fatalf("expected nil strategy to have effective language %q, got: %q", domain.DefaultLanguage, got)
	}

	emptyStrat := &domain.TailoringStrategy{Language: "   "}
	if got := emptyStrat.EffectiveLanguage(); got != domain.DefaultLanguage {
		t.Fatalf("expected empty language strategy to have effective language %q, got: %q", domain.DefaultLanguage, got)
	}

	strat := &domain.TailoringStrategy{Language: "Spanish"}
	if got := strat.EffectiveLanguage(); got != "Spanish" {
		t.Fatalf("expected Spanish, got: %q", got)
	}
}
