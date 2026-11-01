package renderer_test

import (
	"bytes"
	"context"
	htmltemplate "html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"resumeee/internal/renderer"
)

func TestATSCompatibleTemplate_ValidationAndExecution(t *testing.T) {
	templatePath := filepath.Join("..", "..", "docs", "research", "best_ats_compatible_template.html")
	content, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("failed to read best_ats_compatible_template.html: %v", err)
	}

	tmpl, err := htmltemplate.New("ats_compatible_template").Funcs(renderer.SafeFuncMap).Parse(string(content))
	if err != nil {
		t.Fatalf("failed to parse best_ats_compatible_template.html: %v", err)
	}

	synth := renderer.SyntheticRenderContext()
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, synth); err != nil {
		t.Fatalf("failed to execute best_ats_compatible_template.html: %v", err)
	}

	rendered := buf.String()

	// Assertions for semantic structure
	expectedSnippets := []string{
		`<!DOCTYPE html>`,
		`<main class="resume" id="resume">`,
		`<h1 class="candidate-name">Jane Doe</h1>`,
		`"jobTitle": "Staff Software Engineer"`,
		`"email": "jane@example.com"`,
		`address.contact-info`,
		`<h2 class="section-title">Professional Summary</h2>`,
		`<h2 class="section-title">Technical Skills</h2>`,
		`<h2 class="section-title">Professional Experience</h2>`,
		`<h2 class="section-title">Key Projects</h2>`,
		`<h2 class="section-title">Education</h2>`,
		`<h2 class="section-title">Certifications</h2>`,
		`@page`,
		`@media print`,
		`break-inside: avoid`,
		`break-after: avoid`,
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(rendered, snippet) {
			t.Errorf("rendered template missing expected snippet: %q", snippet)
		}
	}
}

func TestBuiltinTemplates_SemanticLandmarksAndPrintStyles(t *testing.T) {
	ctx := context.Background()
	engine := renderer.New()
	profile, content := mockFullProfileAndContent()

	templates := renderer.DefaultTemplateSets()
	if len(templates) < 2 {
		t.Fatalf("expected at least 2 default templates, got %d", len(templates))
	}

	for _, tmpl := range templates {
		t.Run(tmpl.Name, func(t *testing.T) {
			out, err := engine.Render(ctx, &tmpl, profile, content)
			if err != nil {
				t.Fatalf("render failed for %s: %v", tmpl.Name, err)
			}

			html := out.HTML

			// 1. Semantic landmarks
			if !strings.Contains(html, "<main") || !strings.Contains(html, "</main>") {
				t.Errorf("%s missing <main> landmark container", tmpl.Name)
			}
			if !strings.Contains(html, "<header class=\"header\">") {
				t.Errorf("%s missing <header> landmark", tmpl.Name)
			}
			if !strings.Contains(html, "<h1 class=\"candidate-name\">") {
				t.Errorf("%s missing <h1 class=\"candidate-name\">", tmpl.Name)
			}
			if !strings.Contains(html, "<address class=\"contact-info") {
				t.Errorf("%s missing <address> landmark for contact info", tmpl.Name)
			}
			if !strings.Contains(html, "<section class=\"resume-section") {
				t.Errorf("%s missing <section class=\"resume-section\"", tmpl.Name)
			}
			if !strings.Contains(html, "<article class=\"entry-item") {
				t.Errorf("%s missing <article class=\"entry-item\"", tmpl.Name)
			}

			// 2. Schema.org JSON-LD structured data in <head>
			if !strings.Contains(html, `type="application/ld+json"`) {
				t.Errorf("%s missing application/ld+json script block", tmpl.Name)
			}
			if !strings.Contains(html, `"@type": "Person"`) {
				t.Errorf("%s missing @type: Person in JSON-LD", tmpl.Name)
			}
			if !strings.Contains(html, `"name": "Ada Lovelace"`) {
				t.Errorf("%s missing candidate name in JSON-LD", tmpl.Name)
			}
			if !strings.Contains(html, `"email": "ada@analytical.org"`) {
				t.Errorf("%s missing email in JSON-LD", tmpl.Name)
			}
			if !strings.Contains(html, `"https://github.com/adalovelace"`) {
				t.Errorf("%s missing social profile URL in JSON-LD", tmpl.Name)
			}
			if !strings.Contains(html, `"Algorithmic Design"`) {
				t.Errorf("%s missing skills in JSON-LD knowsAbout", tmpl.Name)
			}

			// 3. Print CSS best practices
			css := tmpl.CSSStyles
			if !strings.Contains(css, "@page") || !strings.Contains(css, "margin: 0.5in") {
				t.Errorf("%s CSSStyles missing @page 0.5in margin declaration", tmpl.Name)
			}
			if !strings.Contains(css, "@media print") {
				t.Errorf("%s CSSStyles missing @media print block", tmpl.Name)
			}
			if !strings.Contains(css, "break-inside: avoid") {
				t.Errorf("%s CSSStyles missing break-inside: avoid", tmpl.Name)
			}
			if !strings.Contains(css, "break-after: avoid") {
				t.Errorf("%s CSSStyles missing break-after: avoid", tmpl.Name)
			}
		})
	}
}
