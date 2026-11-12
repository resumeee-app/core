package core

import (
	"errors"
	"fmt"
	"strings"
)

// DefaultLanguage defines the canonical fallback language for resume tailoring and presets.
const DefaultLanguage = "English"

// SupportedLanguages enumerates all broadly supported, high-resource left-to-right languages.
var SupportedLanguages = []string{
	"English",
	"Spanish",
	"French",
	"German",
	"Portuguese",
	"Italian",
	"Dutch",
	"Polish",
	"Swedish",
	"Danish",
	"Norwegian",
	"Finnish",
	"Czech",
	"Romanian",
	"Hungarian",
	"Turkish",
	"Vietnamese",
	"Indonesian",
	"Japanese",
	"Korean",
	"Chinese (Simplified)",
	"Chinese (Traditional)",
	"Hindi",
}

// rtlLanguages contains known right-to-left language names and ISO codes for rejection.
var rtlLanguages = map[string]string{
	"arabic":  "Arabic",
	"ar":      "Arabic",
	"hebrew":  "Hebrew",
	"he":      "Hebrew",
	"iw":      "Hebrew",
	"persian": "Persian",
	"farsi":   "Persian",
	"fa":      "Persian",
	"urdu":    "Urdu",
	"ur":      "Urdu",
	"yiddish": "Yiddish",
	"yi":      "Yiddish",
	"pashto":  "Pashto",
	"ps":      "Pashto",
}

// languageAliases maps normalized aliases and ISO-639-1 language codes to canonical language names.
var languageAliases = map[string]string{
	"en":                    "English",
	"english":               "English",
	"es":                    "Spanish",
	"spanish":               "Spanish",
	"fr":                    "French",
	"french":                "French",
	"de":                    "German",
	"german":                "German",
	"pt":                    "Portuguese",
	"portuguese":            "Portuguese",
	"it":                    "Italian",
	"italian":               "Italian",
	"nl":                    "Dutch",
	"dutch":                 "Dutch",
	"pl":                    "Polish",
	"polish":                "Polish",
	"sv":                    "Swedish",
	"swedish":               "Swedish",
	"da":                    "Danish",
	"danish":                "Danish",
	"no":                    "Norwegian",
	"norwegian":             "Norwegian",
	"fi":                    "Finnish",
	"finnish":               "Finnish",
	"cs":                    "Czech",
	"czech":                 "Czech",
	"ro":                    "Romanian",
	"romanian":              "Romanian",
	"hu":                    "Hungarian",
	"hungarian":             "Hungarian",
	"tr":                    "Turkish",
	"turkish":               "Turkish",
	"vi":                    "Vietnamese",
	"vietnamese":            "Vietnamese",
	"id":                    "Indonesian",
	"indonesian":            "Indonesian",
	"ja":                    "Japanese",
	"japanese":              "Japanese",
	"ko":                    "Korean",
	"korean":                "Korean",
	"zh":                    "Chinese (Simplified)",
	"zh-cn":                 "Chinese (Simplified)",
	"chinese":               "Chinese (Simplified)",
	"chinese (simplified)":  "Chinese (Simplified)",
	"zh-tw":                 "Chinese (Traditional)",
	"chinese (traditional)": "Chinese (Traditional)",
	"hi":                    "Hindi",
	"hindi":                 "Hindi",
}

var (
	// ErrUnsupportedLanguage indicates the requested target language is not supported.
	ErrUnsupportedLanguage = errors.New("unsupported target language")
	// ErrRTLLanguageNotSupported indicates a right-to-left language was provided where only LTR is supported.
	ErrRTLLanguageNotSupported = errors.New("right-to-left languages (e.g., Arabic, Hebrew, Urdu) are not currently supported")
)

// ValidateLanguage normalizes and validates the specified target language.
// If empty or whitespace, it returns DefaultLanguage.
// Returns an error if the language is an unsupported or RTL language.
func ValidateLanguage(lang string) (string, error) {
	trimmed := strings.TrimSpace(lang)
	if trimmed == "" {
		return DefaultLanguage, nil
	}

	lower := strings.ToLower(trimmed)

	if rtlName, isRTL := rtlLanguages[lower]; isRTL {
		return "", fmt.Errorf("%w: %s", ErrRTLLanguageNotSupported, rtlName)
	}

	if canonical, ok := languageAliases[lower]; ok {
		return canonical, nil
	}

	return "", fmt.Errorf("%w: %q (must be one of: %s)", ErrUnsupportedLanguage, trimmed, strings.Join(SupportedLanguages, ", "))
}
