package core_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/resumeee-app/resumeee-core"
)

func TestValidateLanguage_Defaults(t *testing.T) {
	lang, err := core.ValidateLanguage("")
	if err != nil {
		t.Fatalf("expected nil error for empty language, got: %v", err)
	}
	if lang != core.DefaultLanguage {
		t.Fatalf("expected default language %q, got: %q", core.DefaultLanguage, lang)
	}

	langSpace, err := core.ValidateLanguage("   ")
	if err != nil {
		t.Fatalf("expected nil error for whitespace language, got: %v", err)
	}
	if langSpace != core.DefaultLanguage {
		t.Fatalf("expected default language %q, got: %q", core.DefaultLanguage, langSpace)
	}
}

func TestValidateLanguage_SupportedLanguages(t *testing.T) {
	for _, expected := range core.SupportedLanguages {
		t.Run(expected, func(t *testing.T) {
			got, err := core.ValidateLanguage(expected)
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
			got, err := core.ValidateLanguage(tc.input)
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
			_, err := core.ValidateLanguage(rtl)
			if err == nil {
				t.Fatalf("expected error for RTL language %q, got nil", rtl)
			}
			if !errors.Is(err, core.ErrRTLLanguageNotSupported) {
				t.Fatalf("expected errors.Is(err, core.ErrRTLLanguageNotSupported), got: %v", err)
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
			_, err := core.ValidateLanguage(tc)
			if err == nil {
				t.Fatalf("expected error for unsupported language %q, got nil", tc)
			}
			if !errors.Is(err, core.ErrUnsupportedLanguage) {
				t.Fatalf("expected errors.Is(err, core.ErrUnsupportedLanguage), got: %v", err)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "unsupported") {
				t.Fatalf("expected error message to mention unsupported, got: %v", err)
			}
		})
	}
}

func TestTailoringStrategy_DefaultLanguageOnSave(t *testing.T) {
	preset := core.Preset{
		Name: "Custom Strategy",
	}
	if err := preset.Normalize(); err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if preset.Language != core.DefaultLanguage {
		t.Fatalf("expected language %q, got %q", core.DefaultLanguage, preset.Language)
	}
}

func TestTailoringStrategy_EffectiveLanguage(t *testing.T) {
	var nilStrat *core.Preset
	if got := nilStrat.EffectiveLanguage(); got != core.DefaultLanguage {
		t.Fatalf("expected nil strategy to have effective language %q, got: %q", core.DefaultLanguage, got)
	}

	emptyStrat := &core.Preset{Language: "   "}
	if got := emptyStrat.EffectiveLanguage(); got != core.DefaultLanguage {
		t.Fatalf("expected empty language strategy to have effective language %q, got: %q", core.DefaultLanguage, got)
	}

	preset := &core.Preset{Language: "Spanish"}
	if got := preset.EffectiveLanguage(); got != "Spanish" {
		t.Fatalf("expected Spanish, got: %q", got)
	}
}
