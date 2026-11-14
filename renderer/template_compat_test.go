package renderer_test

import (
	"bytes"
	"context"
	htmltemplate "html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/renderer"
	"github.com/resumeee-app/resumeee-core/seed"
)

func TestATSCompatibleTemplate_ValidationAndExecution(t *testing.T) {
	templatePath := filepath.Join("testdata", "best_ats_compatible_template.html")
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

	templates := seed.DefaultTemplateSets()
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

func TestATSTemplateOptions_StructuresAndPlaintextFormatting(t *testing.T) {
	ctx := context.Background()
	engine := renderer.New()
	profile, content := mockFullProfileAndContent()

	t.Run("ATS Minimal Plain Text & Clean HTML", func(t *testing.T) {
		tmpl := seed.ATSMinimalTemplate()
		if tmpl.ID != core.TemplateATSMinimalTxtID {
			t.Errorf("expected template ID %q, got %q", core.TemplateATSMinimalTxtID, tmpl.ID)
		}

		out, err := engine.Render(ctx, &tmpl, profile, content)
		if err != nil {
			t.Fatalf("failed to render ATS Minimal template: %v", err)
		}

		txt := out.PlainText
		// 72-character divider rule
		if !strings.Contains(txt, "------------------------------------------------------------------------") {
			t.Errorf("plain text missing standard 72-char hyphen ASCII section divider")
		}
		// Standardized uppercase headings
		expectedHeadings := []string{
			"CONTACT INFORMATION",
			"PROFESSIONAL SUMMARY",
			"TECHNICAL SKILLS",
			"WORK EXPERIENCE",
			"PROJECTS",
			"EDUCATION",
			"CERTIFICATIONS",
		}
		for _, heading := range expectedHeadings {
			if !strings.Contains(txt, heading) {
				t.Errorf("plain text missing expected heading %q", heading)
			}
		}
		// Hyphen bullet points
		if !strings.Contains(txt, "- Formulated the world's first algorithm intended for implementation on a mechanical computer.") {
			t.Errorf("plain text missing hyphen bullet item")
		}

		// HTML semantic landmarks and print styles
		html := out.HTML
		expectedLandmarks := []string{
			`<main class="resume" id="resume">`,
			`<header class="header">`,
			`<h1 class="candidate-name">Ada Lovelace</h1>`,
			`<address class="contact-info contact">`,
			`<section class="resume-section summary" id="summary">`,
			`<section class="resume-section skills" id="skills">`,
			`<section class="resume-section experience" id="experience">`,
			`<article class="entry-item experience-item job">`,
			`<section class="resume-section projects" id="projects">`,
			`<article class="entry-item project-item project">`,
			`<section class="resume-section education" id="education">`,
			`<article class="entry-item education-item edu">`,
			`<section class="resume-section certifications" id="certifications">`,
		}
		for _, lm := range expectedLandmarks {
			if !strings.Contains(html, lm) {
				t.Errorf("HTML missing landmark %q", lm)
			}
		}
		if !strings.Contains(html, `@page { size: letter portrait; margin: 0.5in; }`) {
			t.Errorf("HTML CSS missing @page letter portrait declaration")
		}

		// Markdown deep assertions
		md := out.Markdown
		expectedMDSnippets := []string{
			`# Ada Lovelace`,
			`## Professional Summary`,
			`Pioneering computer scientist and algorithmic strategist.`,
			`## Technical Skills`,
			`- **Core Competencies:** Algorithmic Design, Bernoulli Numbers, Mechanical Computing`,
			`## Work Experience`,
			`### Lead Algorithmic Engineer — Babbage Engine Works`,
			`- Formulated the world's first algorithm intended for implementation on a mechanical computer.`,
			`## Projects`,
			`### Bernoulli Engine Script — Author`,
			`## Education`,
			`### Studies in Advanced Mathematics in Mathematics & Science — Private Tutelage`,
			`## Certifications`,
			`- **Fellow of the Analytical Sciences** — Royal Society Affiliate (1845)`,
		}
		for _, snip := range expectedMDSnippets {
			if !strings.Contains(md, snip) {
				t.Errorf("Markdown missing expected snippet %q", snip)
			}
		}
	})

	t.Run("ATS Modern Tech", func(t *testing.T) {
		tmpl := seed.ATSHybridTechTemplate()
		if tmpl.ID != core.TemplateATSHybridTechID {
			t.Errorf("expected template ID %q, got %q", core.TemplateATSHybridTechID, tmpl.ID)
		}

		out, err := engine.Render(ctx, &tmpl, profile, content)
		if err != nil {
			t.Fatalf("failed to render ATS Modern Tech template: %v", err)
		}

		txt := out.PlainText
		if !strings.Contains(txt, "TECHNICAL COMPETENCIES") {
			t.Errorf("plain text missing TECHNICAL COMPETENCIES section")
		}
		if !strings.Contains(txt, "PROFESSIONAL EXPERIENCE") {
			t.Errorf("plain text missing PROFESSIONAL EXPERIENCE section")
		}
		if !strings.Contains(txt, "* ") {
			t.Errorf("plain text missing asterisk bullet marker")
		}
		// Plain text pipe metadata delimiter
		if !strings.Contains(txt, "Lead Algorithmic Engineer | Babbage Engine Works | Jan 1842 – Dec 2023 | London, UK") {
			t.Errorf("plain text missing pipe metadata entry header")
		}

		// HTML complete landmark assertions
		html := out.HTML
		expectedLandmarks := []string{
			`<main class="resume" id="resume">`,
			`<header class="header">`,
			`<h1 class="candidate-name">Ada Lovelace</h1>`,
			`<address class="contact-info contact">`,
			`<section class="resume-section summary" id="summary">`,
			`<section class="resume-section skills" id="skills">`,
			`<section class="resume-section experience" id="experience">`,
			`<article class="entry-item experience-item job">`,
			`<section class="resume-section projects" id="projects">`,
			`<article class="entry-item project-item project">`,
			`<section class="resume-section education" id="education">`,
			`<article class="entry-item education-item edu">`,
			`<section class="resume-section certifications" id="certifications">`,
		}
		for _, lm := range expectedLandmarks {
			if !strings.Contains(html, lm) {
				t.Errorf("HTML missing landmark %q", lm)
			}
		}
		if !strings.Contains(html, `font-family: -apple-system, BlinkMacSystemFont`) {
			t.Errorf("HTML missing modern tech sans-serif font stack")
		}

		// Markdown deep assertions
		md := out.Markdown
		expectedMDSnippets := []string{
			`# Ada Lovelace`,
			`## Professional Summary`,
			`## Technical Competencies`,
			`- **Core Competencies:** Algorithmic Design, Bernoulli Numbers, Mechanical Computing`,
			`## Professional Experience`,
			`### Lead Algorithmic Engineer — Babbage Engine Works`,
			`## Technical Projects`,
			`## Education & Credentials`,
			`## Certifications`,
		}
		for _, snip := range expectedMDSnippets {
			if !strings.Contains(md, snip) {
				t.Errorf("Markdown missing expected snippet %q", snip)
			}
		}
	})

	t.Run("ATS Executive Clean", func(t *testing.T) {
		tmpl := seed.ATSExecutiveCleanTemplate()
		if tmpl.ID != core.TemplateATSExecutiveCleanID {
			t.Errorf("expected template ID %q, got %q", core.TemplateATSExecutiveCleanID, tmpl.ID)
		}

		out, err := engine.Render(ctx, &tmpl, profile, content)
		if err != nil {
			t.Fatalf("failed to render ATS Executive Clean template: %v", err)
		}

		txt := out.PlainText
		// Executive double-line ASCII separator
		if !strings.Contains(txt, "========================================================================") {
			t.Errorf("plain text missing executive double-line ASCII separator")
		}
		if !strings.Contains(txt, "EXECUTIVE SUMMARY") {
			t.Errorf("plain text missing EXECUTIVE SUMMARY section")
		}
		if !strings.Contains(txt, "CORE COMPETENCIES") {
			t.Errorf("plain text missing CORE COMPETENCIES section")
		}
		if !strings.Contains(txt, "EXECUTIVE EXPERIENCE") {
			t.Errorf("plain text missing EXECUTIVE EXPERIENCE section")
		}
		// Plain text pipe metadata delimiter
		if !strings.Contains(txt, "Period: Jan 1842 – Dec 2023 | Location: London, UK") {
			t.Errorf("plain text missing executive pipe metadata entry header")
		}

		// HTML complete landmark assertions
		html := out.HTML
		expectedLandmarks := []string{
			`<main class="resume" id="resume">`,
			`<header class="header">`,
			`<h1 class="candidate-name">Ada Lovelace</h1>`,
			`<address class="contact-info contact">`,
			`<section class="resume-section summary" id="summary">`,
			`<section class="resume-section skills" id="skills">`,
			`<section class="resume-section experience" id="experience">`,
			`<article class="entry-item experience-item job">`,
			`<section class="resume-section projects" id="projects">`,
			`<article class="entry-item project-item project">`,
			`<section class="resume-section education" id="education">`,
			`<article class="entry-item education-item edu">`,
			`<section class="resume-section certifications" id="certifications">`,
		}
		for _, lm := range expectedLandmarks {
			if !strings.Contains(html, lm) {
				t.Errorf("HTML missing landmark %q", lm)
			}
		}
		if !strings.Contains(html, `#0f172a`) {
			t.Errorf("HTML missing executive charcoal/slate accent color")
		}

		// Markdown deep assertions
		md := out.Markdown
		expectedMDSnippets := []string{
			`# Ada Lovelace`,
			`## Executive Summary`,
			`## Core Competencies`,
			`## Executive Experience`,
			`### Lead Algorithmic Engineer — Babbage Engine Works`,
			`## Strategic Projects`,
			`## Education`,
			`## Certifications & Board Appointments`,
		}
		for _, snip := range expectedMDSnippets {
			if !strings.Contains(md, snip) {
				t.Errorf("Markdown missing expected snippet %q", snip)
			}
		}
	})

	t.Run("EdgeCases_ContactDelimitersAndOmissions", func(t *testing.T) {
		// Profile with no email, only phone and location
		partialProf := &core.Profile{
			FullName: "Solo Phone Candidate",
			Phone:    "+1-555-0199",
			Location: core.Location{City: "Seattle", Region: "WA"},
		}

		for _, tmpl := range []core.TemplateSet{
			seed.ATSMinimalTemplate(),
			seed.ATSHybridTechTemplate(),
			seed.ATSExecutiveCleanTemplate(),
		} {
			out, err := engine.Render(ctx, &tmpl, partialProf, nil)
			if err != nil {
				t.Fatalf("render failed for %s: %v", tmpl.Name, err)
			}

			// PlainText must not start with dangling pipe delimiter
			if strings.Contains(out.PlainText, " | Phone:") || strings.Contains(out.PlainText, "\n | ") {
				t.Errorf("%s plain text contains dangling pipe when email is omitted: %q", tmpl.Name, out.PlainText)
			}
			// Markdown must not start with dangling pipe delimiter
			lines := strings.Split(out.Markdown, "\n")
			for _, line := range lines {
				if strings.HasPrefix(strings.TrimSpace(line), "|") {
					t.Errorf("%s markdown line begins with unconditioned delimiter: %q", tmpl.Name, line)
				}
			}
			// HTML address must not have leading separator before phone
			if strings.Contains(out.HTML, `<address class="contact-info contact"><span class="separator">`) {
				t.Errorf("%s HTML contains leading separator bullet when email is omitted", tmpl.Name)
			}
		}
	})

	t.Run("EdgeCases_JSONLDEmptyFirstSkillGroup", func(t *testing.T) {
		prof := &core.Profile{FullName: "Skill Tester"}
		tailored := &core.TailoredContent{
			SkillGroups: []core.TailoredSkillGroup{
				{Category: "Empty Group", Skills: []string{}},
				{Category: "Active Group", Skills: []string{"Docker", "Kubernetes"}},
			},
		}

		for _, tmpl := range []core.TemplateSet{
			seed.ATSMinimalTemplate(),
			seed.ATSHybridTechTemplate(),
			seed.ATSExecutiveCleanTemplate(),
		} {
			out, err := engine.Render(ctx, &tmpl, prof, tailored)
			if err != nil {
				t.Fatalf("render failed for %s: %v", tmpl.Name, err)
			}

			if strings.Contains(out.HTML, `[ ,`) || strings.Contains(out.HTML, `[,`) {
				t.Errorf("%s produced invalid JSON-LD leading comma when first skill group is empty: %s", tmpl.Name, out.HTML)
			}
			if !strings.Contains(out.HTML, `"Docker"`) {
				t.Errorf("%s missing skill Docker in JSON-LD", tmpl.Name)
			}
		}
	})

	t.Run("EdgeCases_CertificationAndEducationEmptyDates", func(t *testing.T) {
		prof := &core.Profile{FullName: "Date Tester"}
		tailored := &core.TailoredContent{
			Education: []core.TailoredEducation{
				{
					Degree:      "B.S.",
					Institution: "State University",
				},
			},
			Certifications: []core.TailoredCert{
				{
					Name:   "Security+",
					Issuer: "CompTIA",
				},
			},
		}

		for _, tmpl := range []core.TemplateSet{
			seed.ATSMinimalTemplate(),
			seed.ATSHybridTechTemplate(),
			seed.ATSExecutiveCleanTemplate(),
		} {
			out, err := engine.Render(ctx, &tmpl, prof, tailored)
			if err != nil {
				t.Fatalf("render failed for %s: %v", tmpl.Name, err)
			}

			if strings.Contains(out.Markdown, "()") {
				t.Errorf("%s markdown rendered empty parentheses for empty certification date: %s", tmpl.Name, out.Markdown)
			}
			if strings.Contains(out.PlainText, "()") {
				t.Errorf("%s plain text rendered empty parentheses for empty education dates: %s", tmpl.Name, out.PlainText)
			}
		}
	})

	t.Run("DefaultTemplateSets Count and Invariants", func(t *testing.T) {
		templates := seed.DefaultTemplateSets()
		if len(templates) != 5 {
			t.Fatalf("expected exactly 5 built-in templates, got %d", len(templates))
		}

		ids := make(map[string]bool)
		for _, tmpl := range templates {
			if !tmpl.IsBuiltIn {
				t.Errorf("template %s must have IsBuiltIn = true", tmpl.Name)
			}
			if ids[tmpl.ID] {
				t.Errorf("duplicate template ID: %s", tmpl.ID)
			}
			ids[tmpl.ID] = true
		}

		expectedIDs := []string{
			core.TemplateStandardCleanID,
			core.TemplateDetailedImmigrationCVID,
			core.TemplateATSMinimalTxtID,
			core.TemplateATSHybridTechID,
			core.TemplateATSExecutiveCleanID,
		}
		for _, id := range expectedIDs {
			if !ids[id] {
				t.Errorf("missing expected template ID: %s", id)
			}
		}
	})
}
