package domain_test

import (
	"errors"
	"testing"

	"github.com/resumeee-app/resumeee-core/domain"
)

func TestDomainTemplateSeam(t *testing.T) {
	// Verify template constants exist in domain
	expectedIDs := []string{
		domain.TemplateStandardCleanID,
		domain.TemplateDetailedImmigrationCVID,
		domain.TemplateATSMinimalTxtID,
		domain.TemplateATSHybridTechID,
		domain.TemplateATSExecutiveCleanID,
	}

	for _, id := range expectedIDs {
		if id == "" {
			t.Errorf("expected non-empty template ID constant")
		}
	}

	// Verify domain provides default built-in template sets
	templates := domain.DefaultTemplateSets()
	if len(templates) != 5 {
		t.Fatalf("expected 5 default template sets, got %d", len(templates))
	}

	validator := domain.DefaultTemplateValidator{}

	// Verify all built-in templates pass DefaultTemplateValidator
	for _, tmpl := range templates {
		if err := validator.Validate(&tmpl); err != nil {
			t.Errorf("built-in template %s failed DefaultTemplateValidator: %v", tmpl.Name, err)
		}
	}

	// Verify nil template set returns ErrValidation
	if err := validator.Validate(nil); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("expected ErrValidation for nil template set, got: %v", err)
	}

	// Verify malformed HTML template returns ErrTemplateSyntax
	badHTML := &domain.TemplateSet{
		ID:               "bad-html",
		HTMLTemplate:     "<h1>{{.Unclosed",
		MarkdownTemplate: "# OK",
		TextTemplate:     "OK",
	}
	if err := validator.Validate(badHTML); !errors.Is(err, domain.ErrTemplateSyntax) {
		t.Errorf("expected ErrTemplateSyntax for malformed HTML, got: %v", err)
	}

	// Verify undeclared template function returns ErrTemplateSyntax
	badFunc := &domain.TemplateSet{
		ID:               "bad-func",
		HTMLTemplate:     "<h1>{{exec .Cmd}}</h1>",
		MarkdownTemplate: "# OK",
		TextTemplate:     "OK",
	}
	if err := validator.Validate(badFunc); !errors.Is(err, domain.ErrTemplateSyntax) {
		t.Errorf("expected ErrTemplateSyntax for undeclared function, got: %v", err)
	}

	// Verify malformed Markdown template returns ErrTemplateSyntax
	badMD := &domain.TemplateSet{
		ID:               "bad-md",
		HTMLTemplate:     "<h1>OK</h1>",
		MarkdownTemplate: "# {{with .Items}}{{end",
		TextTemplate:     "OK",
	}
	if err := validator.Validate(badMD); !errors.Is(err, domain.ErrTemplateSyntax) {
		t.Errorf("expected ErrTemplateSyntax for malformed Markdown, got: %v", err)
	}

	// Verify malformed Text template returns ErrTemplateSyntax
	badText := &domain.TemplateSet{
		ID:               "bad-text",
		HTMLTemplate:     "<h1>OK</h1>",
		MarkdownTemplate: "# OK",
		TextTemplate:     "{{range .Items",
	}
	if err := validator.Validate(badText); !errors.Is(err, domain.ErrTemplateSyntax) {
		t.Errorf("expected ErrTemplateSyntax for malformed Text template, got: %v", err)
	}

	// Verify custom TemplateValidatorFunc injection
	customCalled := false
	var customValidator domain.TemplateValidator = domain.TemplateValidatorFunc(func(tmpl *domain.TemplateSet) error {
		customCalled = true
		if tmpl.ID == "reject-me" {
			return domain.ErrValidation
		}
		return nil
	})

	if err := customValidator.Validate(&domain.TemplateSet{ID: "accept"}); err != nil {
		t.Errorf("unexpected validation error from custom validator: %v", err)
	}
	if !customCalled {
		t.Errorf("expected custom validator to be invoked")
	}
	if err := customValidator.Validate(&domain.TemplateSet{ID: "reject-me"}); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("expected ErrValidation from custom validator, got: %v", err)
	}
}
