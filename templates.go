package core

import (
	"fmt"
	htmltemplate "html/template"
	texttemplate "text/template"
)

var defaultFuncMap = htmltemplate.FuncMap{
	"formatDate":      func(any, any) string { return "" },
	"formatDateRange": func(any, any, any) string { return "" },
	"year":            func(any) string { return "" },
	"safeCSS":         func(any) htmltemplate.CSS { return "" },
	"lower":           func(any) string { return "" },
	"upper":           func(any) string { return "" },
	"title":           func(any) string { return "" },
	"trim":            func(any) string { return "" },
	"trimPrefix":      func(any, any) string { return "" },
	"trimSuffix":      func(any, any) string { return "" },
	"replace":         func(any, any, any) string { return "" },
	"contains":        func(any, any) bool { return false },
	"hasPrefix":       func(any, any) bool { return false },
	"hasSuffix":       func(any, any) bool { return false },
	"join":            func(any, any) string { return "" },
	"split":           func(any, any) []string { return nil },
	"first":           func(any) string { return "" },
	"last":            func(any) string { return "" },
	"default":         func(any, any) any { return nil },
	"coalesce":        func(...any) string { return "" },
}

// DefaultTemplateValidator provides standard library template syntax and whitelist validation.
type DefaultTemplateValidator struct{}

// Validate checks that all templates within a TemplateSet parse cleanly and reference only valid syntax.
func (DefaultTemplateValidator) Validate(tmpl *TemplateSet) error {
	if tmpl == nil {
		return fmt.Errorf("%w: template set cannot be nil", ErrValidation)
	}

	if tmpl.HTMLTemplate != "" {
		if _, err := htmltemplate.New("html").Funcs(defaultFuncMap).Parse(tmpl.HTMLTemplate); err != nil {
			return fmt.Errorf("%w in html_template: %v", ErrTemplateSyntax, err)
		}
	}

	for _, textTmpl := range []struct {
		name string
		body string
	}{
		{"markdown_template", tmpl.MarkdownTemplate},
		{"text_template", tmpl.TextTemplate},
	} {
		if textTmpl.body != "" {
			if _, err := texttemplate.New(textTmpl.name).Funcs(defaultFuncMap).Parse(textTmpl.body); err != nil {
				return fmt.Errorf("%w in %s: %v", ErrTemplateSyntax, textTmpl.name, err)
			}
		}
	}

	return nil
}

const (
	// TemplateStandardCleanID is the canonical ID for the built-in Standard Clean template set.
	TemplateStandardCleanID = "builtin-standard-clean"

	// TemplateDetailedImmigrationCVID is the canonical ID for the built-in Detailed Immigration / CV template set.
	TemplateDetailedImmigrationCVID = "builtin-detailed-immigration-cv"

	// TemplateATSMinimalTxtID is the canonical ID for the built-in ATS Minimal Plain-Text & Clean HTML template set.
	TemplateATSMinimalTxtID = "builtin-ats-minimal-txt"

	// TemplateATSHybridTechID is the canonical ID for the built-in ATS Modern Tech resume template set.
	TemplateATSHybridTechID = "builtin-ats-hybrid-tech"

	// TemplateATSExecutiveCleanID is the canonical ID for the built-in ATS Executive Clean resume template set.
	TemplateATSExecutiveCleanID = "builtin-ats-executive-clean"
)
