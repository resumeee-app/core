package renderer_test

import (
	"context"
	"errors"
	htmltemplate "html/template"
	"strings"
	"sync"
	"testing"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/services/renderer"
)

type emptyStringer struct{}

func (emptyStringer) String() string { return "" }

func mockFullProfileAndContent() (*core.Profile, *core.TailoredContent) {
	endDate := core.NewDate(2023, 12, 31)
	projStart := core.NewDate(1843, 1, 1)
	eduStart := core.NewDate(1830, 1, 1)
	certDate := core.NewDate(1845, 1, 1)
	hours := 40
	profile := &core.Profile{
		ID:       "prof-test",
		UserID:   "user-test",
		FullName: "Ada Lovelace",
		Headline: "Chief Computing Pioneer",
		Email:    "ada@analytical.org",
		Phone:    "+44-20-7946-0919",
		Location: core.Location{
			City:        "London",
			Region:      "Greater London",
			CountryCode: "GB",
		},
		Profiles: []core.SocialLink{
			{Network: "GitHub", Username: "adalovelace", URL: "https://github.com/adalovelace"},
		},
		Summary: "First computer programmer, specializing in the Analytical Engine.",
	}

	content := &core.TailoredContent{
		Summary: "Pioneering computer scientist and algorithmic strategist.",
		SkillGroups: []core.TailoredSkillGroup{
			{
				Category: "Core Competencies",
				Skills:   []string{"Algorithmic Design", "Bernoulli Numbers", "Mechanical Computing"},
			},
		},
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID:    "exp-1",
				Company:           "Babbage Engine Works",
				RoleTitle:         "Lead Algorithmic Engineer",
				StartDate:         core.NewDate(1842, 1, 1),
				EndDate:           &endDate,
				Location:          core.Location{City: "London", Region: "UK"},
				CompanyLocation:   core.Location{City: "London", Region: "UK"},
				CompanyContact:    &core.ContactDetail{Type: core.ContactTypeEmail, Value: "inquiries@babbage.org"},
				HoursPerWeek:      &hours,
				SupervisorName:    "Charles Babbage",
				SupervisorContact: "charles@babbage.org",
				DutiesSummary:     "Authored notes and algorithm for calculating Bernoulli numbers.",
				Bullets: []core.TailoredBullet{
					{Text: "Formulated the world's first algorithm intended for implementation on a mechanical computer."},
					{Text: "Translated Luigi Menabrea's memoir on Babbage's Analytical Engine."},
				},
			},
		},
		Projects: []core.TailoredProject{
			{
				SourceEntityID: "proj-1",
				Name:           "Bernoulli Engine Script",
				Role:           "Author",
				URL:            "https://babbage.org/bernoulli",
				StartDate:      &projStart,
				EndDate:        &endDate,
				Summary:        "Diagram and plan for the computation of Bernoulli numbers.",
				Bullets: []core.TailoredBullet{
					{Text: "Derived recurrence relations and punch card sequence specifications."},
				},
			},
		},
		Education: []core.TailoredEducation{
			{
				SourceEntityID: "edu-1",
				Institution:    "Private Tutelage",
				Degree:         "Studies in Advanced Mathematics",
				Field:          "Mathematics & Science",
				StartDate:      &eduStart,
				EndDate:        &endDate,
				Score:          "Distinction",
				Bullets: []core.TailoredBullet{
					{Text: "Mentored by Mary Somerville and Augustus De Morgan."},
				},
			},
		},
		Certifications: []core.TailoredCert{
			{
				SourceEntityID: "cert-1",
				Name:           "Fellow of the Analytical Sciences",
				Issuer:         "Royal Society Affiliate",
				Date:           &certDate,
				URL:            "https://example.org/fellowship",
			},
		},
	}

	return profile, content
}

func TestRenderer_MultiFormatRender(t *testing.T) {
	ctx := context.Background()
	engine := renderer.New()
	profile, content := mockFullProfileAndContent()

	tmpl := renderer.StandardCleanTemplate()
	out, err := engine.Render(ctx, &tmpl, profile, content)
	if err != nil {
		t.Fatalf("Render failed unexpectedly: %v", err)
	}

	// 1. Verify HTML output
	if !strings.Contains(out.HTML, "Ada Lovelace") {
		t.Errorf("HTML missing candidate name")
	}
	if !strings.Contains(out.HTML, "Chief Computing Pioneer") {
		t.Errorf("HTML missing candidate headline")
	}
	if !strings.Contains(out.HTML, "Babbage Engine Works") {
		t.Errorf("HTML missing company")
	}
	if !strings.Contains(out.HTML, "Bernoulli Numbers") {
		t.Errorf("HTML missing skills")
	}
	if !strings.Contains(out.HTML, "<style>") {
		t.Errorf("HTML missing embedded style tag")
	}

	// 2. Verify Markdown output
	if !strings.Contains(out.Markdown, "# Ada Lovelace") {
		t.Errorf("Markdown missing top-level heading for name")
	}
	if !strings.Contains(out.Markdown, "## Work Experience") {
		t.Errorf("Markdown missing Work Experience heading")
	}
	if !strings.Contains(out.Markdown, "Babbage Engine Works") {
		t.Errorf("Markdown missing company")
	}

	// 3. Verify Plain Text output
	if !strings.Contains(out.PlainText, "Ada Lovelace") {
		t.Errorf("Plain Text missing candidate name")
	}
	if !strings.Contains(out.PlainText, "WORK EXPERIENCE:") {
		t.Errorf("Plain Text missing WORK EXPERIENCE section")
	}
	if !strings.Contains(out.PlainText, "Babbage Engine Works") {
		t.Errorf("Plain Text missing company")
	}
}

func TestRenderer_SecurityAutoEscaping(t *testing.T) {
	ctx := context.Background()
	engine := renderer.New()

	maliciousProfile := &core.Profile{
		FullName: `<script>alert('xss-name')</script>`,
		Headline: `"><img src=x onerror=alert('xss-headline')>`,
		Email:    `xss@test.org`,
		Profiles: []core.SocialLink{
			{Network: "Exploit", Username: "bad", URL: `javascript:alert('xss-url')`},
		},
	}
	maliciousContent := &core.TailoredContent{
		Summary: `<script>alert('xss-summary')</script>`,
		Experiences: []core.TailoredExperience{
			{
				Company:   `<b onmouseover=alert('xss-company')>Evil Corp</b>`,
				RoleTitle: `<script>alert('xss-role')</script>`,
				Bullets: []core.TailoredBullet{
					{Text: `<script>alert('xss-bullet')</script>`},
				},
			},
		},
	}

	tmpl := renderer.StandardCleanTemplate()
	out, err := engine.Render(ctx, &tmpl, maliciousProfile, maliciousContent)
	if err != nil {
		t.Fatalf("Render returned unexpected error on hostile input: %v", err)
	}

	// Verify unescaped raw <script> is NOT present in HTML
	if strings.Contains(out.HTML, "<script>alert('xss-name')</script>") {
		t.Errorf("Security failure: raw unescaped script tag found in HTML output")
	}
	if strings.Contains(out.HTML, `"><img src=x onerror=alert('xss-headline')>`) {
		t.Errorf("Security failure: raw unescaped img tag found in HTML output")
	}

	// Verify contextual escaping transformed tags into HTML entities
	if !strings.Contains(out.HTML, "&lt;script&gt;alert(&#39;xss-name&#39;)&lt;/script&gt;") {
		t.Errorf("Expected escaped HTML script entity, got: %s", out.HTML)
	}

	// Verify javascript: URL was sanitized or filtered by html/template to #ZgotmplZ
	if strings.Contains(out.HTML, `href="javascript:alert('xss-url')"`) {
		t.Errorf("Security failure: javascript: URL not neutralized in href")
	}
	if !strings.Contains(out.HTML, "#ZgotmplZ") && strings.Contains(out.HTML, "javascript:") {
		t.Errorf("Security failure: unescaped javascript URL scheme permitted")
	}
}

func TestRenderer_SandboxForbiddenFunctions(t *testing.T) {
	engine := renderer.New()

	forbiddenTemplates := []struct {
		name string
		tmpl core.TemplateSet
	}{
		{
			name: "call exec",
			tmpl: core.TemplateSet{
				HTMLTemplate: `<div>{{exec "cat /etc/passwd"}}</div>`,
			},
		},
		{
			name: "call os system",
			tmpl: core.TemplateSet{
				MarkdownTemplate: `{{os "whoami"}}`,
			},
		},
		{
			name: "call io read",
			tmpl: core.TemplateSet{
				TextTemplate: `{{read "/dev/urandom"}}`,
			},
		},
		{
			name: "call nonexistent function",
			tmpl: core.TemplateSet{
				HTMLTemplate: `<span>{{nonExistentHelper .FullName}}</span>`,
			},
		},
	}

	for _, tc := range forbiddenTemplates {
		t.Run(tc.name, func(t *testing.T) {
			err := engine.Validate(&tc.tmpl)
			if err == nil {
				t.Fatalf("expected validation error for forbidden function in %s, got nil", tc.name)
			}
			if !errors.Is(err, core.ErrTemplateSyntax) {
				t.Errorf("expected ErrTemplateSyntax, got %v", err)
			}
		})
	}
}

func TestRenderer_SyntaxErrorDetection(t *testing.T) {
	engine := renderer.New()

	badTemplates := []struct {
		name string
		tmpl core.TemplateSet
	}{
		{
			name: "unclosed action tag in HTML",
			tmpl: core.TemplateSet{
				HTMLTemplate: `<h1>{{.FullName</h1>`,
			},
		},
		{
			name: "unclosed block in Markdown",
			tmpl: core.TemplateSet{
				MarkdownTemplate: `{{if .Summary}}Summary: {{.Summary}}`,
			},
		},
		{
			name: "invalid pipeline syntax in Text",
			tmpl: core.TemplateSet{
				TextTemplate: `{{.FullName | | }}`,
			},
		},
		{
			name: "nonexistent field in synthetic context HTML",
			tmpl: core.TemplateSet{
				HTMLTemplate: `<div>{{.NonExistentField}}</div>`,
			},
		},
		{
			name: "nonexistent field in synthetic context Markdown",
			tmpl: core.TemplateSet{
				MarkdownTemplate: `<div>{{.NonExistentField}}</div>`,
			},
		},
		{
			name: "nonexistent field in synthetic context Text",
			tmpl: core.TemplateSet{
				TextTemplate: `<div>{{.NonExistentField}}</div>`,
			},
		},
		{
			name: "unclosed tag in Text",
			tmpl: core.TemplateSet{
				TextTemplate: `{{.FullName`,
			},
		},
	}

	for _, tc := range badTemplates {
		t.Run(tc.name, func(t *testing.T) {
			err := engine.Validate(&tc.tmpl)
			if err == nil {
				t.Fatalf("expected validation failure for %s, got nil", tc.name)
			}
			if !errors.Is(err, core.ErrTemplateSyntax) {
				t.Errorf("expected ErrTemplateSyntax for %s, got %v", tc.name, err)
			}
		})
	}

	// Validate nil template set
	if err := engine.Validate(nil); err == nil {
		t.Errorf("expected error when validating nil TemplateSet")
	}
}

func TestRenderer_FuncMapHelpers(t *testing.T) {
	// Date & Time
	endDate := "2023-05-01"
	rangeWithEnd := renderer.SafeFuncMap["formatDateRange"].(func(any, any, string) string)("2021-01-01", &endDate, "Present")
	if rangeWithEnd != "Jan 2021 – May 2023" {
		t.Errorf("formatDateRange expected 'Jan 2021 – May 2023', got '%s'", rangeWithEnd)
	}

	rangeOngoing := renderer.SafeFuncMap["formatDateRange"].(func(any, any, string) string)("2021-01", nil, "Present")
	if rangeOngoing != "Jan 2021 – Present" {
		t.Errorf("formatDateRange expected 'Jan 2021 – Present', got '%s'", rangeOngoing)
	}

	rangeYears := renderer.SafeFuncMap["formatDateRange"].(func(any, any, string) string)("2018", "2020", "")
	if rangeYears != "2018 – 2020" {
		t.Errorf("formatDateRange expected '2018 – 2020', got '%s'", rangeYears)
	}

	// Verify core.Date inputs also produce Month-Year format (no day displayed)
	domainStart := core.NewDate(2021, 1, 15)
	domainEnd := core.NewDate(2023, 5, 20)
	rangeDomain := renderer.SafeFuncMap["formatDateRange"].(func(any, any, string) string)(domainStart, &domainEnd, "Present")
	if rangeDomain != "Jan 2021 – May 2023" {
		t.Errorf("formatDateRange with core.Date expected 'Jan 2021 – May 2023', got '%s'", rangeDomain)
	}

	formatDate := renderer.SafeFuncMap["formatDate"].(func(string, any) string)
	if formatted := formatDate("Jan 2006", "2026-02-14"); formatted != "Feb 2026" {
		t.Errorf("formatDate expected 'Feb 2026', got '%s'", formatted)
	}
	if formatted := formatDate("Jan 2006", domainStart); formatted != "Jan 2021" {
		t.Errorf("formatDate with core.Date expected 'Jan 2021', got '%s'", formatted)
	}
	if raw := formatDate("Jan 2006", "invalid-date"); raw != "invalid-date" {
		t.Errorf("formatDate expected 'invalid-date' fallback, got '%s'", raw)
	}

	year := renderer.SafeFuncMap["year"].(func(any) string)
	if y := year("2024-08-19"); y != "2024" {
		t.Errorf("year expected '2024', got '%s'", y)
	}
	if y := year(domainStart); y != "2021" {
		t.Errorf("year with core.Date expected '2021', got '%s'", y)
	}
	if y := year("2021"); y != "2021" {
		t.Errorf("year expected '2021', got '%s'", y)
	}

	// String Casing
	lower := renderer.SafeFuncMap["lower"].(func(string) string)
	if lower("HELLO World") != "hello world" {
		t.Errorf("lower failed")
	}

	upper := renderer.SafeFuncMap["upper"].(func(string) string)
	if upper("hello world") != "HELLO WORLD" {
		t.Errorf("upper failed")
	}

	title := renderer.SafeFuncMap["title"].(func(string) string)
	if title("staff software engineer") != "Staff Software Engineer" {
		t.Errorf("title failed, got '%s'", title("staff software engineer"))
	}

	// Trimming & Replacing
	trim := renderer.SafeFuncMap["trim"].(func(string) string)
	if trim("  padded  ") != "padded" {
		t.Errorf("trim failed")
	}

	trimPrefix := renderer.SafeFuncMap["trimPrefix"].(func(string, string) string)
	if trimPrefix("v1.2.3", "v") != "1.2.3" {
		t.Errorf("trimPrefix failed")
	}

	trimSuffix := renderer.SafeFuncMap["trimSuffix"].(func(string, string) string)
	if trimSuffix("archive.tar.gz", ".gz") != "archive.tar" {
		t.Errorf("trimSuffix failed")
	}

	replace := renderer.SafeFuncMap["replace"].(func(string, string, string) string)
	if replace("foo bar foo", "foo", "baz") != "baz bar baz" {
		t.Errorf("replace failed")
	}

	contains := renderer.SafeFuncMap["contains"].(func(string, string) bool)
	if !contains("fullstack", "stack") || contains("fullstack", "cloud") {
		t.Errorf("contains failed")
	}

	hasPrefix := renderer.SafeFuncMap["hasPrefix"].(func(string, string) bool)
	if !hasPrefix("golang", "go") {
		t.Errorf("hasPrefix failed")
	}

	hasSuffix := renderer.SafeFuncMap["hasSuffix"].(func(string, string) bool)
	if !hasSuffix("document.pdf", ".pdf") {
		t.Errorf("hasSuffix failed")
	}

	// List & Slice
	join := renderer.SafeFuncMap["join"].(func(any, string) string)
	if res := join([]string{"Go", "Python", "Rust"}, ", "); res != "Go, Python, Rust" {
		t.Errorf("join failed, got '%s'", res)
	}

	split := renderer.SafeFuncMap["split"].(func(string, string) []string)
	if parts := split("a,b,c", ","); len(parts) != 3 || parts[1] != "b" {
		t.Errorf("split failed")
	}

	first := renderer.SafeFuncMap["first"].(func(any) string)
	if first([]string{"first", "second"}) != "first" {
		t.Errorf("first failed")
	}
	if first([]string{}) != "" || first(nil) != "" {
		t.Errorf("first on empty/nil failed")
	}

	last := renderer.SafeFuncMap["last"].(func(any) string)
	if last([]string{"first", "second", "third"}) != "third" {
		t.Errorf("last failed")
	}
	if last([]string{}) != "" || last(nil) != "" {
		t.Errorf("last on empty/nil failed")
	}

	// Fallbacks
	def := renderer.SafeFuncMap["default"].(func(any, any) any)
	if def("fallback", "") != "fallback" {
		t.Errorf("default on empty string failed")
	}
	if def("fallback", nil) != "fallback" {
		t.Errorf("default on nil failed")
	}
	if def("fallback", "actual") != "actual" {
		t.Errorf("default on populated string failed")
	}

	coalesce := renderer.SafeFuncMap["coalesce"].(func(...any) string)
	if coalesce("", "", "found", "later") != "found" {
		t.Errorf("coalesce failed")
	}
	if coalesce("", "") != "" {
		t.Errorf("coalesce empty failed")
	}
}

func TestRenderer_FuncMapHelpers_EdgeCases(t *testing.T) {
	formatDate := renderer.SafeFuncMap["formatDate"].(func(string, any) string)
	if formatDate("2006", "   ") != "" {
		t.Errorf("formatDate on blank string should return empty string")
	}

	formatDateRange := renderer.SafeFuncMap["formatDateRange"].(func(any, any, string) string)
	if formatDateRange("", nil, "Present") != "" {
		t.Errorf("formatDateRange on empty start and nil end should return empty string")
	}
	emptyEnd := ""
	if r := formatDateRange("2020", &emptyEnd, "Present"); r != "2020 – Present" {
		t.Errorf("formatDateRange with empty end pointer failed: %s", r)
	}
	if r := formatDateRange("2020", "", "Present"); r != "2020 – Present" {
		t.Errorf("formatDateRange with empty end string failed: %s", r)
	}
	if r := formatDateRange("", "2020", "Present"); r != "2020" {
		t.Errorf("formatDateRange with empty start should return end: %s", r)
	}
	if r := formatDateRange("2020", 42, "Present"); r != "2020 – 42" {
		t.Errorf("formatDateRange with int end failed: %s", r)
	}
	if r := formatDateRange("2020", emptyStringer{}, "Present"); r != "2020 – Present" {
		t.Errorf("formatDateRange with emptyStringer failed: %s", r)
	}
	if r := formatDateRange("2020", nil, "Present"); r != "2020 – Present" {
		t.Errorf("formatDateRange year only failed: %s", r)
	}

	year := renderer.SafeFuncMap["year"].(func(any) string)
	if year("   ") != "" {
		t.Errorf("year on empty string should return empty string")
	}
	if year("Graduated in 1999 during summer") != "1999" {
		t.Errorf("year on string with 4-digit regex failed")
	}
	if year("NoDigitsHere") != "NoDigitsHere" {
		t.Errorf("year without digits should return original")
	}

	join := renderer.SafeFuncMap["join"].(func(any, string) string)
	if join(nil, ",") != "" {
		t.Errorf("join on nil should return empty string")
	}
	if res := join([]int{1, 2, 3}, "-"); res != "1-2-3" {
		t.Errorf("join on int slice failed, got %s", res)
	}
	if res := join(1234, "-"); res != "1234" {
		t.Errorf("join on non-slice failed, got %s", res)
	}

	first := renderer.SafeFuncMap["first"].(func(any) string)
	if first([]int{10, 20}) != "10" {
		t.Errorf("first on int slice failed")
	}
	if first([]int{}) != "" {
		t.Errorf("first on empty int slice failed")
	}
	if first(99) != "99" {
		t.Errorf("first on scalar failed")
	}

	last := renderer.SafeFuncMap["last"].(func(any) string)
	if last([]int{10, 20}) != "20" {
		t.Errorf("last on int slice failed")
	}
	if last([]int{}) != "" {
		t.Errorf("last on empty int slice failed")
	}
	if last(99) != "99" {
		t.Errorf("last on scalar failed")
	}

	def := renderer.SafeFuncMap["default"].(func(any, any) any)
	var nilPtr *string
	valStr := "valid"
	nonNilPtr := &valStr
	if def("fallback", nilPtr) != "fallback" {
		t.Errorf("default on nil pointer failed")
	}
	if def("fallback", nonNilPtr) != nonNilPtr {
		t.Errorf("default on non-nil pointer failed")
	}
	if def("fallback", 0) != "fallback" {
		t.Errorf("default on 0 failed")
	}
	if def("fallback", 42) != 42 {
		t.Errorf("default on non-zero int failed")
	}

	coalesce := renderer.SafeFuncMap["coalesce"].(func(...any) string)
	var emptyStrPtr *string
	emptyStr := ""
	emptyStrPtr = &emptyStr
	if coalesce(nil, emptyStrPtr, &valStr) != "valid" {
		t.Errorf("coalesce with pointers failed")
	}
	if coalesce(nil, 42) != "42" {
		t.Errorf("coalesce with scalar failed")
	}
	if coalesce(nil, nil) != "" {
		t.Errorf("coalesce on all nil failed")
	}

	cssFn, ok := renderer.SafeFuncMap["safeCSS"].(func(any) htmltemplate.CSS)
	if !ok {
		t.Fatalf("expected safeCSS in funcmap")
	}
	cssRes := cssFn("body { color: blue; }")
	if cssRes == "" {
		t.Errorf("safeCSS failed")
	}
}

func TestRenderer_RenderContext_ProfilesAndMetadata(t *testing.T) {
	prof := &core.Profile{
		FullName: "Test User",
		Profiles: []core.SocialLink{
			{Network: "website", URL: "https://myportfolio.com"},
		},
		Metadata: map[string]any{
			"website": "https://meta.com",
			"tier":    "premium",
		},
	}
	rctx := renderer.NewRenderContext(nil, prof, nil)
	if rctx.Website != "https://myportfolio.com" {
		t.Errorf("expected website from profile social link, got %s", rctx.Website)
	}

	profMetaOnly := &core.Profile{
		FullName: "Test User 2",
		Metadata: map[string]any{
			"website": "https://metaonly.com",
		},
	}
	rctx2 := renderer.NewRenderContext(nil, profMetaOnly, nil)
	if rctx2.Website != "https://metaonly.com" {
		t.Errorf("expected website from metadata, got %s", rctx2.Website)
	}

	rctxEmpty := renderer.NewRenderContext(nil, nil, nil)
	if rctxEmpty.Profile == nil || rctxEmpty.Content == nil {
		t.Errorf("expected initialized Profile and Content structs")
	}
}

func TestRenderer_Engine_AliasesAndMethods(t *testing.T) {
	r := renderer.NewTemplateRenderer()
	res, err := r.RenderFormat(core.OutputFormatHTML, "<b>{{.Text}}</b>", map[string]string{"Text": "bold"})
	if err != nil || res != "<b>bold</b>" {
		t.Errorf("r.RenderFormat failed: %v", err)
	}

	emptyRes, err := renderer.RenderFormat(core.OutputFormatHTML, "", nil)
	if err != nil || emptyRes != "" {
		t.Errorf("RenderFormat with empty template string failed")
	}
}

func TestRenderer_Render_ExecutionErrors(t *testing.T) {
	ctx := context.Background()
	engine := renderer.New()
	profile, content := mockFullProfileAndContent()

	// HTML execution error
	badHTMLTmpl := &core.TemplateSet{
		HTMLTemplate: `{{.Experiences.InvalidCall}}`,
	}
	_, err := engine.Render(ctx, badHTMLTmpl, profile, content)
	if err == nil {
		t.Errorf("expected HTML render execution error")
	}

	// Markdown execution error
	badMDTmpl := &core.TemplateSet{
		MarkdownTemplate: `{{.Experiences.InvalidCall}}`,
	}
	_, err = engine.Render(ctx, badMDTmpl, profile, content)
	if err == nil {
		t.Errorf("expected Markdown render execution error")
	}

	// Text execution error
	badTxtTmpl := &core.TemplateSet{
		TextTemplate: `{{.Experiences.InvalidCall}}`,
	}
	_, err = engine.Render(ctx, badTxtTmpl, profile, content)
	if err == nil {
		t.Errorf("expected Text render execution error")
	}
}

func TestRenderer_DefaultTemplates(t *testing.T) {
	ctx := context.Background()
	engine := renderer.New()
	profile, content := mockFullProfileAndContent()

	templates := renderer.DefaultTemplateSets()
	if len(templates) < 2 {
		t.Fatalf("expected at least 2 default templates, got %d", len(templates))
	}

	for _, tmpl := range templates {
		t.Run(tmpl.Name, func(t *testing.T) {
			// 1. Verify dry-run validation passes
			if err := engine.Validate(&tmpl); err != nil {
				t.Fatalf("validation failed for default template %s: %v", tmpl.Name, err)
			}

			// 2. Render against full profile & tailored content
			out, err := engine.Render(ctx, &tmpl, profile, content)
			if err != nil {
				t.Fatalf("render failed for %s: %v", tmpl.Name, err)
			}

			if strings.TrimSpace(out.HTML) == "" {
				t.Errorf("empty HTML output for %s", tmpl.Name)
			}
			if strings.TrimSpace(out.Markdown) == "" {
				t.Errorf("empty Markdown output for %s", tmpl.Name)
			}
			if strings.TrimSpace(out.PlainText) == "" {
				t.Errorf("empty Plain Text output for %s", tmpl.Name)
			}

			// 3. Render against minimal / empty content (must not crash or panic)
			minimalProfile := &core.Profile{FullName: "Minimal User"}
			minimalOut, err := engine.Render(ctx, &tmpl, minimalProfile, nil)
			if err != nil {
				t.Fatalf("render failed on minimal profile for %s: %v", tmpl.Name, err)
			}
			if !strings.Contains(minimalOut.HTML, "Minimal User") {
				t.Errorf("minimal HTML output missing name")
			}
		})
	}
}

func TestRenderer_DetailedImmigrationCVFields(t *testing.T) {
	ctx := context.Background()
	engine := renderer.New()
	profile, content := mockFullProfileAndContent()

	tmpl := renderer.DetailedImmigrationCVTemplate()
	out, err := engine.Render(ctx, &tmpl, profile, content)
	if err != nil {
		t.Fatalf("failed to render Detailed Immigration CV: %v", err)
	}

	// Assert presence of administrative fields in output
	for _, formatName := range []string{"HTML", "Markdown", "PlainText"} {
		var output string
		switch formatName {
		case "HTML":
			output = out.HTML
		case "Markdown":
			output = out.Markdown
		case "PlainText":
			output = out.PlainText
		}

		if !strings.Contains(output, "Charles Babbage") {
			t.Errorf("%s missing SupervisorName", formatName)
		}
		if !strings.Contains(output, "charles@babbage.org") {
			t.Errorf("%s missing SupervisorContact", formatName)
		}
		if !strings.Contains(output, "40") {
			t.Errorf("%s missing HoursPerWeek", formatName)
		}
		if !strings.Contains(output, "Authored notes and algorithm") {
			t.Errorf("%s missing DutiesSummary", formatName)
		}
	}
}

func TestRenderer_RenderFormat(t *testing.T) {
	data := struct {
		Name string
	}{
		Name: "Gopher",
	}

	htmlRes, err := renderer.RenderFormat(core.OutputFormatHTML, "<h1>Hello {{.Name}}</h1>", data)
	if err != nil || htmlRes != "<h1>Hello Gopher</h1>" {
		t.Errorf("RenderFormat HTML failed: res=%s, err=%v", htmlRes, err)
	}

	mdRes, err := renderer.RenderFormat(core.OutputFormatMarkdown, "# Hello {{.Name}}", data)
	if err != nil || mdRes != "# Hello Gopher" {
		t.Errorf("RenderFormat Markdown failed: res=%s, err=%v", mdRes, err)
	}

	txtRes, err := renderer.RenderFormat(core.OutputFormatPlainText, "Hello {{.Name}}", data)
	if err != nil || txtRes != "Hello Gopher" {
		t.Errorf("RenderFormat PlainText failed: res=%s, err=%v", txtRes, err)
	}

	_, err = renderer.RenderFormat(core.OutputFormat("invalid"), "Hello", data)
	if err == nil {
		t.Errorf("expected error for invalid OutputFormat")
	}

	// Bad template syntax
	_, err = renderer.RenderFormat(core.OutputFormatHTML, "<h1>{{.Name</h1>", data)
	if err == nil {
		t.Errorf("expected HTML parse error")
	}
	_, err = renderer.RenderFormat(core.OutputFormatMarkdown, "# {{.Name", data)
	if err == nil {
		t.Errorf("expected Markdown parse error")
	}
	_, err = renderer.RenderFormat(core.OutputFormatPlainText, "{{.Name", data)
	if err == nil {
		t.Errorf("expected PlainText parse error")
	}
}

func TestRenderer_BulletPointsCompatibility(t *testing.T) {
	tmpl := core.TemplateSet{
		HTMLTemplate: `{{range .Experiences}}{{range .BulletPoints}}<li>{{.Text}}</li>{{end}}{{end}}`,
	}
	err := renderer.Validate(&tmpl)
	if err != nil {
		t.Errorf("expected template using .BulletPoints to validate successfully, got %v", err)
	}
}

func TestRenderer_ThreadSafety(t *testing.T) {
	ctx := context.Background()
	engine := renderer.New()
	profile, content := mockFullProfileAndContent()
	tmpl := renderer.StandardCleanTemplate()

	var wg sync.WaitGroup
	workers := 50
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			out, err := engine.Render(ctx, &tmpl, profile, content)
			if err != nil {
				t.Errorf("concurrent render failed: %v", err)
			}
			if len(out.HTML) == 0 || len(out.Markdown) == 0 || len(out.PlainText) == 0 {
				t.Errorf("empty render output in concurrent run")
			}
		}()
	}

	wg.Wait()
}

func TestRenderer_RenderNilInputs(t *testing.T) {
	ctx := context.Background()
	engine := renderer.New()

	_, err := engine.Render(ctx, nil, nil, nil)
	if err == nil || !errors.Is(err, core.ErrValidation) {
		t.Errorf("expected ErrValidation when rendering nil TemplateSet, got %v", err)
	}
}

func TestValidate_CSSStylesIncluded(t *testing.T) {
	tmpl := &core.TemplateSet{
		HTMLTemplate: `<style>{{.CSSStyles}}</style><h1>{{.FullName}}</h1>`,
		CSSStyles:    `body { background-color: #f0f0f0; }`,
	}
	if err := renderer.Validate(tmpl); err != nil {
		t.Fatalf("expected valid template with CSSStyles to pass Validate, got: %v", err)
	}

	invalidTmpl := &core.TemplateSet{
		HTMLTemplate: `<style>{{.CSSStyles}}</style><h1>{{.NonExistentField}}</h1>`,
		CSSStyles:    `body { color: red; }`,
	}
	if err := renderer.Validate(invalidTmpl); err == nil {
		t.Fatalf("expected template with non-existent field to fail Validate, got nil")
	}
}

func TestStripScriptTags(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "html without scripts",
			input:    `<div class="resume"><h1>John Doe</h1></div>`,
			expected: `<div class="resume"><h1>John Doe</h1></div>`,
		},
		{
			name:     "single script tag with content",
			input:    `<div>Hello</div><script>alert('xss');</script><p>World</p>`,
			expected: `<div>Hello</div><p>World</p>`,
		},
		{
			name:     "script tag with attributes and multiline JSON-LD content",
			input:    `<div>Header</div><script type="application/ld+json">{"@context": "https://schema.org"}</script><footer>Footer</footer>`,
			expected: `<div>Header</div><footer>Footer</footer>`,
		},
		{
			name:     "multiple script tags with mixed case",
			input:    `<SCRIPT src="evil.js"></SCRIPT><h1>Title</h1><Script type="text/javascript">console.log(1);</Script>`,
			expected: `<h1>Title</h1>`,
		},
		{
			name:     "self closing script tag",
			input:    `<p>Before</p><script src="tracker.js" /><p>After</p>`,
			expected: `<p>Before</p><p>After</p>`,
		},
		{
			name:     "nested script tags or edge formatting",
			input:    `<div><script><script>nested</script></script>Valid</div>`,
			expected: `<div>Valid</div>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderer.StripScriptTags(tt.input)
			if got != tt.expected {
				t.Errorf("StripScriptTags(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestRenderer_AllEntityFieldsAccessible(t *testing.T) {
	ctx := context.Background()
	engine := renderer.New()

	tmpl := core.TemplateSet{
		ID:     "tmpl-comprehensive-fields",
		UserID: "user-1",
		Name:   "All Fields Template",
		HTMLTemplate: `<div>
{{range .Experiences}}
  <span class="emp-type">{{.EmploymentType}}</span>
  <span class="is-current">{{.IsCurrent}}</span>
  <span class="company-loc">{{.CompanyLocation.City}}, {{.CompanyLocation.Region}}</span>
  <span class="hours">{{.HoursPerWeek}}</span>
  <span class="supervisor">{{.SupervisorName}} ({{.SupervisorContact}})</span>
  <span class="duties">{{.DutiesSummary}}</span>
  <span class="exp-meta">{{index .Metadata "custom_meta"}}</span>
{{end}}
{{range .Education}}
  <span class="edu-score">{{.Score}}</span>
  <span class="edu-meta">{{index .Metadata "honors"}}</span>
{{end}}
{{range .Projects}}
  <span class="proj-summary">{{.Summary}}</span>
  <span class="proj-meta">{{index .Metadata "stars"}}</span>
{{end}}
{{range .Certifications}}
  <span class="cert-summary">{{.Summary}}</span>
  <span class="cert-meta">{{index .Metadata "badge"}}</span>
{{end}}
</div>`,
		MarkdownTemplate: `# Resume
{{range .Experiences}}
- {{.Company}} [{{.EmploymentType}}] (Current: {{.IsCurrent}})
  Duties: {{.DutiesSummary}}
  Hours: {{.HoursPerWeek}}
  Supervisor: {{.SupervisorName}}
{{end}}
{{range .Certifications}}
- {{.Name}}: {{.Summary}}
{{end}}`,
		TextTemplate: `{{range .Experiences}}{{.Company}}: {{.EmploymentType}}, {{.DutiesSummary}}{{end}}`,
	}

	hours := 40
	content := &core.TailoredContent{
		Experiences: []core.TailoredExperience{
			{
				Company:           "Acme Technologies",
				RoleTitle:         "Lead Systems Architect",
				EmploymentType:    core.EmploymentTypeFullTime,
				IsCurrent:         true,
				CompanyLocation:   core.Location{City: "San Francisco", Region: "CA"},
				HoursPerWeek:      &hours,
				SupervisorName:    "Jane Doe",
				SupervisorContact: "jane@acme.com",
				DutiesSummary:     "Architected distributed event pipelines.",
				Metadata:          map[string]any{"custom_meta": "level-7"},
			},
		},
		Education: []core.TailoredEducation{
			{
				Institution: "MIT",
				Degree:      "B.S.",
				Score:       "Summa Cum Laude",
				Metadata:    map[string]any{"honors": "Dean's List"},
			},
		},
		Projects: []core.TailoredProject{
			{
				Name:     "Kube-Scale",
				Summary:  "Distributed autoscaler for Kubernetes.",
				Metadata: map[string]any{"stars": "1.2k"},
			},
		},
		Certifications: []core.TailoredCert{
			{
				Name:     "Certified Kubernetes Administrator",
				Issuer:   "CNCF",
				Summary:  "Comprehensive mastery of Kubernetes cluster administration.",
				Metadata: map[string]any{"badge": "gold"},
			},
		},
	}

	out, err := engine.Render(ctx, &tmpl, &core.Profile{FullName: "Test User"}, content)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	expectedSubstrings := []string{
		"Full-time",
		"true",
		"San Francisco, CA",
		"40",
		"Jane Doe (jane@acme.com)",
		"Architected distributed event pipelines.",
		"level-7",
		"Summa Cum Laude",
		"Dean's List",
		"Distributed autoscaler for Kubernetes.",
		"1.2k",
		"Comprehensive mastery of Kubernetes cluster administration.",
		"gold",
	}

	for _, s := range expectedSubstrings {
		if s == "Dean's List" {
			if !strings.Contains(out.HTML, "Dean&#39;s List") && !strings.Contains(out.HTML, "Dean's List") {
				t.Errorf("HTML output missing expected string: %q", s)
			}
			continue
		}
		if !strings.Contains(out.HTML, s) {
			t.Errorf("HTML output missing expected string: %q", s)
		}
	}
	if !strings.Contains(out.Markdown, "Comprehensive mastery of Kubernetes cluster administration.") {
		t.Errorf("Markdown output missing certification summary")
	}
}
