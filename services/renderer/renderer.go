// Package renderer executes multi-format resume templates across HTML, Markdown, and Plain Text
// in a sandboxed, memory-efficient environment with built-in AST validation and custom func maps.
package renderer

import (
	"bytes"
	"context"
	"fmt"
	htmltemplate "html/template"
	"io"
	"regexp"
	"strings"
	"sync"
	texttemplate "text/template"

	"github.com/resumeee-app/resumeee-core"
)

var (
	scriptBlockRegex = regexp.MustCompile(`(?i)<script\b[^>]*>[\s\S]*?<\/script\b[^>]*>`)
	scriptTagRegex   = regexp.MustCompile(`(?i)<\/?script\b[^>]*\/?>`)
)

// StripScriptTags removes all <script>...</script> blocks and lingering <script...> or </script> tags from the provided HTML string.
// This prevents sandboxed preview iframes (sandbox="") from encountering script execution errors or security blocks.
func StripScriptTags(html string) string {
	if html == "" {
		return ""
	}
	res := html
	for {
		stripped := scriptBlockRegex.ReplaceAllString(res, "")
		stripped = scriptTagRegex.ReplaceAllString(stripped, "")
		if stripped == res {
			break
		}
		res = stripped
	}
	return res
}

// bufferPool recycles bytes.Buffer instances to eliminate allocations during frequent template rendering passes.
var bufferPool = sync.Pool{
	New: func() any {
		// Preallocate 16KB which comfortably fits typical rendered resume output.
		return bytes.NewBuffer(make([]byte, 0, 16*1024))
	},
}

func getBuffer() *bytes.Buffer {
	b := bufferPool.Get().(*bytes.Buffer)
	b.Reset()
	return b
}

func putBuffer(b *bytes.Buffer) {
	// Discard abnormally bloated buffers (e.g., > 512KB) to prevent unbounded memory retention.
	if b.Cap() <= 512*1024 {
		b.Reset()
		bufferPool.Put(b)
	}
}

// RenderContext binds the Main Profile, Tailored Content, and template styling into a unified execution context.
type RenderContext struct {
	// Root shortcuts for ergonomic and backward-compatible template authoring
	FullName       string
	Headline       string
	Email          string
	Phone          string
	Website        string
	Location       core.Location
	Profiles       []core.SocialLink
	Summary        string
	CSSStyles      htmltemplate.CSS
	SkillGroups    []core.TailoredSkillGroup
	Experiences    []core.TailoredExperience
	Projects       []core.TailoredProject
	Education      []core.TailoredEducation
	Certifications []core.TailoredCert

	// Structured domains for advanced template authors
	Profile *core.Profile
	Content *core.TailoredContent
	Meta    map[string]any
}

// AllSkills returns a deduplicated, flattened slice of non-empty skills across all skill groups.
func (rc *RenderContext) AllSkills() []string {
	if rc == nil {
		return nil
	}
	var res []string
	seen := make(map[string]bool)
	for _, sg := range rc.SkillGroups {
		for _, s := range sg.Skills {
			trimmed := strings.TrimSpace(s)
			if trimmed != "" && !seen[trimmed] {
				seen[trimmed] = true
				res = append(res, trimmed)
			}
		}
	}
	return res
}

// NewRenderContext builds a populated RenderContext combining Profile, TailoredContent, and TemplateSet styles.
func NewRenderContext(tmpl *core.TemplateSet, profile *core.Profile, content *core.TailoredContent) *RenderContext {
	ctx := &RenderContext{
		Meta: make(map[string]any),
	}
	if tmpl != nil {
		ctx.CSSStyles = htmltemplate.CSS(tmpl.CSSStyles)
	}
	if profile != nil {
		ctx.Profile = profile
		ctx.FullName = profile.FullName
		ctx.Headline = profile.Headline
		ctx.Email = profile.Email
		ctx.Phone = profile.Phone
		ctx.Location = profile.Location
		ctx.Profiles = profile.Profiles
		ctx.Summary = profile.Summary
		for _, p := range profile.Profiles {
			if strings.EqualFold(p.Network, "website") || strings.EqualFold(p.Network, "portfolio") {
				ctx.Website = p.URL
				break
			}
		}
		for k, v := range profile.Metadata {
			ctx.Meta[k] = v
		}
		if ctx.Website == "" && profile.Metadata != nil {
			if w, ok := profile.Metadata["website"].(string); ok {
				ctx.Website = w
			}
		}
	} else {
		ctx.Profile = &core.Profile{}
	}
	if content != nil {
		ctx.Content = content
		if strings.TrimSpace(content.Summary) != "" {
			ctx.Summary = content.Summary
		}
		ctx.SkillGroups = content.SkillGroups
		ctx.Experiences = content.Experiences
		ctx.Projects = content.Projects
		ctx.Education = content.Education
		ctx.Certifications = content.Certifications
	} else {
		ctx.Content = &core.TailoredContent{}
	}
	return ctx
}

// SyntheticRenderContext returns a fully populated mock context used for pre-persistence dry-run validation.
func SyntheticRenderContext() *RenderContext {
	endDate := core.NewDate(2023, 1, 1)
	projStart := core.NewDate(2021, 6, 1)
	eduStart := core.NewDate(2014, 9, 1)
	certDate := core.NewDate(2022, 5, 1)
	hours := 40
	return &RenderContext{
		FullName: "Jane Doe",
		Headline: "Staff Software Engineer",
		Email:    "jane@example.com",
		Phone:    "+1-555-0123",
		Website:  "https://example.com",
		Location: core.Location{
			Address:     "123 Main St",
			PostalCode:  "94105",
			City:        "San Francisco",
			Region:      "CA",
			CountryCode: "US",
		},
		Profiles: []core.SocialLink{
			{Network: "LinkedIn", Username: "janedoe", URL: "https://linkedin.com/in/janedoe"},
			{Network: "GitHub", Username: "janedoe", URL: "https://github.com/janedoe"},
		},
		Summary:   "Experienced software engineer with a track record of building reliable systems.",
		CSSStyles: htmltemplate.CSS("body { font-family: sans-serif; }"),
		SkillGroups: []core.TailoredSkillGroup{
			{
				Category:       "Languages",
				Skills:         []string{"Go", "Python", "TypeScript"},
				SourceSkillIDs: []string{"skill-1", "skill-2"},
			},
		},
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID:    "exp-1",
				Company:           "Acme Corp",
				RoleTitle:         "Senior Software Engineer",
				EmploymentType:    core.EmploymentTypeFullTime,
				StartDate:         core.NewDate(2020, 1, 1),
				EndDate:           &endDate,
				IsCurrent:         false,
				Location:          core.Location{City: "San Francisco", Region: "CA"},
				CompanyLocation:   core.Location{City: "San Francisco", Region: "CA"},
				CompanyContact:    &core.ContactDetail{Type: core.ContactTypeEmail, Value: "hr@acme.com"},
				HoursPerWeek:      &hours,
				SupervisorName:    "Alice Manager",
				SupervisorContact: "alice@acme.com",
				DutiesSummary:     "Led core infrastructure development.",
				Bullets: []core.TailoredBullet{
					{Text: "Engineered distributed caching layer reducing latency by 45%.", SourceFactIDs: []string{"fact-1"}},
				},
			},
		},
		Projects: []core.TailoredProject{
			{
				SourceEntityID: "proj-1",
				Name:           "High-Throughput Queue",
				Role:           "Lead Architect",
				URL:            "https://github.com/example/queue",
				StartDate:      &projStart,
				EndDate:        &endDate,
				Summary:        "A distributed message queue in Go.",
				Bullets: []core.TailoredBullet{
					{Text: "Processed 1M msg/sec with sub-millisecond p99 latency.", SourceFactIDs: []string{"fact-2"}},
				},
			},
		},
		Education: []core.TailoredEducation{
			{
				SourceEntityID: "edu-1",
				Institution:    "University of Technology",
				Degree:         "B.S.",
				Field:          "Computer Science",
				StartDate:      &eduStart,
				EndDate:        &endDate,
				Score:          "3.9 GPA",
				Bullets: []core.TailoredBullet{
					{Text: "Graduated with Honors; President of ACM chapter.", SourceFactIDs: []string{"fact-3"}},
				},
			},
		},
		Certifications: []core.TailoredCert{
			{
				SourceEntityID: "cert-1",
				Name:           "Certified Kubernetes Administrator",
				Issuer:         "CNCF",
				Date:           &certDate,
				URL:            "https://cncf.io/cert/cka",
				Summary:        "Demonstrated proficiency in cluster architecture and cloud native tooling.",
			},
		},
		Profile: &core.Profile{
			FullName: "Jane Doe",
			Headline: "Staff Software Engineer",
			Email:    "jane@example.com",
			Phone:    "+1-555-0123",
		},
		Content: &core.TailoredContent{
			Summary: "Experienced software engineer with a track record of building reliable systems.",
		},
		Meta: map[string]any{
			"version": "1.0",
		},
	}
}

// Validate executes pre-persistence dry-run validation on HTML, Markdown, and Plain Text templates.
func Validate(tmpl *core.TemplateSet) error {
	if tmpl == nil {
		return fmt.Errorf("%w: template set cannot be nil", core.ErrValidation)
	}

	synth := SyntheticRenderContext()
	synth.CSSStyles = htmltemplate.CSS(tmpl.CSSStyles)

	if tmpl.HTMLTemplate != "" {
		ht, err := htmltemplate.New("html").Funcs(SafeFuncMap).Parse(tmpl.HTMLTemplate)
		if err != nil {
			return fmt.Errorf("%w in html_template: %v", core.ErrTemplateSyntax, err)
		}
		if err := ht.Execute(io.Discard, synth); err != nil {
			return fmt.Errorf("%w in html_template evaluation: %v", core.ErrTemplateSyntax, err)
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
			tt, err := texttemplate.New(textTmpl.name).Funcs(SafeFuncMap).Parse(textTmpl.body)
			if err != nil {
				return fmt.Errorf("%w in %s: %v", core.ErrTemplateSyntax, textTmpl.name, err)
			}
			if err := tt.Execute(io.Discard, synth); err != nil {
				return fmt.Errorf("%w in %s evaluation: %v", core.ErrTemplateSyntax, textTmpl.name, err)
			}
		}
	}

	return nil
}

// RenderFormat compiles and executes a single template string targeting the designated OutputFormat.
func RenderFormat(format core.OutputFormat, tmplStr string, data any) (string, error) {
	if tmplStr == "" {
		return "", nil
	}

	buf := getBuffer()
	defer putBuffer(buf)

	switch format {
	case core.OutputFormatHTML:
		t, err := htmltemplate.New("html").Funcs(SafeFuncMap).Parse(tmplStr)
		if err != nil {
			return "", fmt.Errorf("%w in html template: %v", core.ErrTemplateSyntax, err)
		}
		if err := t.Execute(buf, data); err != nil {
			return "", fmt.Errorf("%w in html template execution: %v", core.ErrTemplateSyntax, err)
		}
		return buf.String(), nil

	case core.OutputFormatMarkdown, core.OutputFormatPlainText:
		name := "markdown"
		if format == core.OutputFormatPlainText {
			name = "text"
		}
		t, err := texttemplate.New(name).Funcs(SafeFuncMap).Parse(tmplStr)
		if err != nil {
			return "", fmt.Errorf("%w in %s template: %v", core.ErrTemplateSyntax, name, err)
		}
		if err := t.Execute(buf, data); err != nil {
			return "", fmt.Errorf("%w in %s template execution: %v", core.ErrTemplateSyntax, name, err)
		}
		return buf.String(), nil

	default:
		return "", fmt.Errorf("unsupported output format: %s", format)
	}
}

// Render compiles and executes HTML, Markdown, and Plain Text formats for a given TemplateSet against candidate data.
func Render(ctx context.Context, tmpl *core.TemplateSet, profile *core.Profile, content *core.TailoredContent) (*core.RenderOutput, error) {
	if tmpl == nil {
		return nil, fmt.Errorf("%w: template set is required", core.ErrValidation)
	}

	renderCtx := NewRenderContext(tmpl, profile, content)

	htmlOut, err := RenderFormat(core.OutputFormatHTML, tmpl.HTMLTemplate, renderCtx)
	if err != nil {
		return nil, err
	}

	mdOut, err := RenderFormat(core.OutputFormatMarkdown, tmpl.MarkdownTemplate, renderCtx)
	if err != nil {
		return nil, err
	}

	txtOut, err := RenderFormat(core.OutputFormatPlainText, tmpl.TextTemplate, renderCtx)
	if err != nil {
		return nil, err
	}

	return &core.RenderOutput{
		HTML:      htmlOut,
		Markdown:  mdOut,
		PlainText: txtOut,
	}, nil
}

type engine struct{}

// New constructs a thread-safe, stateless core.TemplateRenderer implementation.
func New() core.TemplateRenderer {
	return &engine{}
}

// NewTemplateRenderer is a constructor alias for New.
func NewTemplateRenderer() core.TemplateRenderer {
	return &engine{}
}

func (e *engine) Render(ctx context.Context, tmpl *core.TemplateSet, profile *core.Profile, content *core.TailoredContent) (*core.RenderOutput, error) {
	return Render(ctx, tmpl, profile, content)
}

func (e *engine) RenderFormat(format core.OutputFormat, tmplStr string, data any) (string, error) {
	return RenderFormat(format, tmplStr, data)
}

func (e *engine) Validate(tmpl *core.TemplateSet) error {
	return Validate(tmpl)
}

// DefaultSeededProfileName identifies the default placeholder name seeded upon database initialization.
const DefaultSeededProfileName = "Default User"

// HasCustomProfileData returns true if the profile contains non-empty custom user data rather than unconfigured defaults.
func HasCustomProfileData(prof *core.Profile, snap *core.MainProfileSnapshot) bool {
	if prof == nil {
		return false
	}
	if prof.FullName != "" && prof.FullName != DefaultSeededProfileName {
		return true
	}
	if snap != nil && (len(snap.Experiences) > 0 || len(snap.Skills) > 0 || len(snap.Projects) > 0 || len(snap.Education) > 0) {
		return true
	}
	return false
}

// BuildPreviewRenderContext constructs a comprehensive execution context for template preview rendering.
// Prefers actual user profile facts when present, merging with synthetic sample data for empty sections.
func BuildPreviewRenderContext(ctx context.Context, profileRepo core.ProfileRepository, userID string, tmpl *core.TemplateSet) *RenderContext {
	synth := SyntheticRenderContext()
	if tmpl != nil && tmpl.CSSStyles != "" {
		synth.CSSStyles = htmltemplate.CSS(tmpl.CSSStyles)
	}

	if profileRepo == nil {
		return synth
	}

	prof, err := profileRepo.GetProfile(ctx, userID)
	if err == nil && prof != nil {
		snapshot, _ := profileRepo.GetSnapshot(ctx, userID)
		if HasCustomProfileData(prof, snapshot) {
			var tailored *core.TailoredContent
			if snapshot != nil {
				tailored = core.SnapshotToTailoredContent(snapshot)
			}
			userCtx := NewRenderContext(tmpl, prof, tailored)
			if len(userCtx.Experiences) == 0 {
				userCtx.Experiences = synth.Experiences
			}
			if len(userCtx.SkillGroups) == 0 {
				userCtx.SkillGroups = synth.SkillGroups
			}
			if len(userCtx.Projects) == 0 {
				userCtx.Projects = synth.Projects
			}
			if len(userCtx.Education) == 0 {
				userCtx.Education = synth.Education
			}
			if len(userCtx.Certifications) == 0 {
				userCtx.Certifications = synth.Certifications
			}
			if userCtx.Summary == "" {
				userCtx.Summary = synth.Summary
			}
			return userCtx
		}
	}

	return synth
}

// RenderAllFormats compiles and executes all template formats (HTML, Markdown, PlainText) using the provided context.
func RenderAllFormats(r core.TemplateRenderer, tmpl *core.TemplateSet, renderCtx *RenderContext) (core.RenderOutput, error) {
	if tmpl == nil || r == nil {
		return core.RenderOutput{}, nil
	}

	var out core.RenderOutput
	if tmpl.HTMLTemplate != "" {
		htmlOut, err := r.RenderFormat(core.OutputFormatHTML, tmpl.HTMLTemplate, renderCtx)
		if err != nil {
			return core.RenderOutput{}, fmt.Errorf("html template rendering failed: %w", err)
		}
		out.HTML = StripScriptTags(htmlOut)
	}

	if tmpl.MarkdownTemplate != "" {
		mdOut, err := r.RenderFormat(core.OutputFormatMarkdown, tmpl.MarkdownTemplate, renderCtx)
		if err != nil {
			return core.RenderOutput{}, fmt.Errorf("markdown template rendering failed: %w", err)
		}
		out.Markdown = mdOut
	}

	if tmpl.TextTemplate != "" {
		txtOut, err := r.RenderFormat(core.OutputFormatPlainText, tmpl.TextTemplate, renderCtx)
		if err != nil {
			return core.RenderOutput{}, fmt.Errorf("text template rendering failed: %w", err)
		}
		out.PlainText = txtOut
	}

	return out, nil
}

// ResolveVersionOutputs resolves or dynamically re-renders resume outputs incorporating targetTemplateID.
func ResolveVersionOutputs(
	ctx context.Context,
	r core.TemplateRenderer,
	templateRepo core.TemplateRepository,
	profileRepo core.ProfileRepository,
	userID string,
	b *core.Branch,
	ver *core.BranchVersion,
	targetTemplateID string,
) core.RenderOutput {
	if ver == nil {
		return core.RenderOutput{}
	}
	htmlOut := ver.HTMLOutput
	mdOut := ver.MarkdownOutput
	txtOut := ver.PlainTextOutput

	if targetTemplateID == "" && b != nil {
		targetTemplateID = b.TemplateSetID
	}
	if targetTemplateID == "" {
		targetTemplateID = ver.TemplateSetIDUsed
	}

	isDifferentTemplate := targetTemplateID != "" && targetTemplateID != ver.TemplateSetIDUsed
	missingOutputs := htmlOut == "" || mdOut == "" || txtOut == ""
	shouldRender := isDifferentTemplate || missingOutputs

	if shouldRender && r != nil && templateRepo != nil && targetTemplateID != "" {
		if tmpl, tErr := templateRepo.GetTemplate(ctx, userID, targetTemplateID); tErr == nil && tmpl != nil {
			var prof *core.Profile
			if profileRepo != nil {
				prof, _ = profileRepo.GetProfile(ctx, userID)
			}
			if ver.TailoredContent != nil {
				if rendered, rErr := r.Render(ctx, tmpl, prof, ver.TailoredContent); rErr == nil && rendered != nil {
					htmlOut = rendered.HTML
					mdOut = rendered.Markdown
					txtOut = rendered.PlainText
				}
			} else {
				renderCtx := NewRenderContext(tmpl, prof, nil)
				if htmlOut == "" && tmpl.HTMLTemplate != "" {
					if hOut, rErr := r.RenderFormat(core.OutputFormatHTML, tmpl.HTMLTemplate, renderCtx); rErr == nil {
						htmlOut = hOut
					}
				}
				if mdOut == "" && tmpl.MarkdownTemplate != "" {
					if mOut, rErr := r.RenderFormat(core.OutputFormatMarkdown, tmpl.MarkdownTemplate, renderCtx); rErr == nil {
						mdOut = mOut
					}
				}
				if txtOut == "" && tmpl.TextTemplate != "" {
					if tOut, rErr := r.RenderFormat(core.OutputFormatPlainText, tmpl.TextTemplate, renderCtx); rErr == nil {
						txtOut = tOut
					}
				}
			}
		}
	}

	return core.RenderOutput{
		HTML:      htmlOut,
		Markdown:  mdOut,
		PlainText: txtOut,
	}
}
