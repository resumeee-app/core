package core

import (
	"context"
	"fmt"
	htmltemplate "html/template"
	texttemplate "text/template"
	"time"
)

// Built-in template set canonical identifiers.
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

// OutputFormat designates the targeted rendering medium.
type OutputFormat string

// Supported output mediums for rendered resumes.
const (
	// OutputFormatHTML selects styled HTML output.
	OutputFormatHTML OutputFormat = "html"

	// OutputFormatMarkdown selects Markdown output.
	OutputFormatMarkdown OutputFormat = "markdown"

	// OutputFormatPlainText selects ATS-friendly plain text output.
	OutputFormatPlainText OutputFormat = "text"
)

// RenderOutput contains the rendered text artifacts across all three supported formats.
type RenderOutput struct {
	HTML      string `json:"html"`
	Markdown  string `json:"markdown"`
	PlainText string `json:"plain_text"`
}

// TemplateSet bundles HTML, Markdown, and Plain Text Go templates stored in the database.
type TemplateSet struct {
	ID               string    `json:"id"`
	UserID           string    `json:"user_id"`
	Name             string    `json:"name"`
	Description      string    `json:"description,omitempty"`
	IsBuiltIn        bool      `json:"is_built_in"`
	HTMLTemplate     string    `json:"html_template"` // Executed via html/template
	CSSStyles        string    `json:"css_styles,omitempty"`
	MarkdownTemplate string    `json:"markdown_template"` // Executed via text/template
	TextTemplate     string    `json:"text_template"`     // Executed via text/template
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// defaultFuncMap registers stub helper names allowed when validating user-defined Go templates.
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
func (v DefaultTemplateValidator) Validate(tmpl *TemplateSet) error {
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

// TemplateStore defines persistence operations for multi-format resume templates.
type TemplateStore interface {
	ListTemplates(ctx context.Context, userID string) ([]TemplateSet, error)
	Template(ctx context.Context, userID, id string) (*TemplateSet, error)
	CreateTemplate(ctx context.Context, tmpl *TemplateSet) error
	UpdateTemplate(ctx context.Context, tmpl *TemplateSet) error
	DeleteTemplate(ctx context.Context, userID, id string) error
}
