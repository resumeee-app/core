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

	"github.com/resumeee-app/resumeee-core/domain"
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
	Location       domain.Location
	Profiles       []domain.SocialLink
	Summary        string
	CSSStyles      htmltemplate.CSS
	SkillGroups    []domain.TailoredSkillGroup
	Experiences    []domain.TailoredExperience
	Projects       []domain.TailoredProject
	Education      []domain.TailoredEducation
	Certifications []domain.TailoredCert

	// Structured domains for advanced template authors
	Profile *domain.Profile
	Content *domain.TailoredContent
	Meta    map[string]any
}

// TemplateContext is an alias to RenderContext for unified naming across domain contracts.
type TemplateContext = RenderContext

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
func NewRenderContext(tmpl *domain.TemplateSet, profile *domain.Profile, content *domain.TailoredContent) *RenderContext {
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
		ctx.Profile = &domain.Profile{}
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
		ctx.Content = &domain.TailoredContent{}
	}
	return ctx
}

// SyntheticRenderContext returns a fully populated mock context used for pre-persistence dry-run validation.
func SyntheticRenderContext() *RenderContext {
	endDate := domain.NewDate(2023, 1, 1)
	projStart := domain.NewDate(2021, 6, 1)
	eduStart := domain.NewDate(2014, 9, 1)
	certDate := domain.NewDate(2022, 5, 1)
	hours := 40
	return &RenderContext{
		FullName: "Jane Doe",
		Headline: "Staff Software Engineer",
		Email:    "jane@example.com",
		Phone:    "+1-555-0123",
		Website:  "https://example.com",
		Location: domain.Location{
			Address:     "123 Main St",
			PostalCode:  "94105",
			City:        "San Francisco",
			Region:      "CA",
			CountryCode: "US",
		},
		Profiles: []domain.SocialLink{
			{Network: "LinkedIn", Username: "janedoe", URL: "https://linkedin.com/in/janedoe"},
			{Network: "GitHub", Username: "janedoe", URL: "https://github.com/janedoe"},
		},
		Summary:   "Experienced software engineer with a track record of building reliable systems.",
		CSSStyles: htmltemplate.CSS("body { font-family: sans-serif; }"),
		SkillGroups: []domain.TailoredSkillGroup{
			{
				Category:       "Languages",
				Skills:         []string{"Go", "Python", "TypeScript"},
				SourceSkillIDs: []string{"skill-1", "skill-2"},
			},
		},
		Experiences: []domain.TailoredExperience{
			{
				SourceEntityID:    "exp-1",
				Company:           "Acme Corp",
				RoleTitle:         "Senior Software Engineer",
				EmploymentType:    domain.EmploymentTypeFullTime,
				StartDate:         domain.NewDate(2020, 1, 1),
				EndDate:           &endDate,
				IsCurrent:         false,
				Location:          domain.Location{City: "San Francisco", Region: "CA"},
				CompanyLocation:   domain.Location{City: "San Francisco", Region: "CA"},
				CompanyContact:    &domain.ContactDetail{Type: domain.ContactTypeEmail, Value: "hr@acme.com"},
				HoursPerWeek:      &hours,
				SupervisorName:    "Alice Manager",
				SupervisorContact: "alice@acme.com",
				DutiesSummary:     "Led core infrastructure development.",
				Bullets: []domain.TailoredBullet{
					{Text: "Engineered distributed caching layer reducing latency by 45%.", SourceFactIDs: []string{"fact-1"}},
				},
			},
		},
		Projects: []domain.TailoredProject{
			{
				SourceEntityID: "proj-1",
				Name:           "High-Throughput Queue",
				Role:           "Lead Architect",
				URL:            "https://github.com/example/queue",
				StartDate:      &projStart,
				EndDate:        &endDate,
				Summary:        "A distributed message queue in Go.",
				Bullets: []domain.TailoredBullet{
					{Text: "Processed 1M msg/sec with sub-millisecond p99 latency.", SourceFactIDs: []string{"fact-2"}},
				},
			},
		},
		Education: []domain.TailoredEducation{
			{
				SourceEntityID: "edu-1",
				Institution:    "University of Technology",
				Degree:         "B.S.",
				Field:          "Computer Science",
				StartDate:      &eduStart,
				EndDate:        &endDate,
				Score:          "3.9 GPA",
				Bullets: []domain.TailoredBullet{
					{Text: "Graduated with Honors; President of ACM chapter.", SourceFactIDs: []string{"fact-3"}},
				},
			},
		},
		Certifications: []domain.TailoredCert{
			{
				SourceEntityID: "cert-1",
				Name:           "Certified Kubernetes Administrator",
				Issuer:         "CNCF",
				Date:           &certDate,
				URL:            "https://cncf.io/cert/cka",
				Summary:        "Demonstrated proficiency in cluster architecture and cloud native tooling.",
			},
		},
		Profile: &domain.Profile{
			FullName: "Jane Doe",
			Headline: "Staff Software Engineer",
			Email:    "jane@example.com",
			Phone:    "+1-555-0123",
		},
		Content: &domain.TailoredContent{
			Summary: "Experienced software engineer with a track record of building reliable systems.",
		},
		Meta: map[string]any{
			"version": "1.0",
		},
	}
}

// Validate executes pre-persistence dry-run validation on HTML, Markdown, and Plain Text templates.
func Validate(tmpl *domain.TemplateSet) error {
	if tmpl == nil {
		return fmt.Errorf("%w: template set cannot be nil", domain.ErrValidation)
	}

	synth := SyntheticRenderContext()
	synth.CSSStyles = htmltemplate.CSS(tmpl.CSSStyles)

	if tmpl.HTMLTemplate != "" {
		ht, err := htmltemplate.New("html").Funcs(SafeFuncMap).Parse(tmpl.HTMLTemplate)
		if err != nil {
			return fmt.Errorf("%w in html_template: %v", domain.ErrTemplateSyntax, err)
		}
		if err := ht.Execute(io.Discard, synth); err != nil {
			return fmt.Errorf("%w in html_template evaluation: %v", domain.ErrTemplateSyntax, err)
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
				return fmt.Errorf("%w in %s: %v", domain.ErrTemplateSyntax, textTmpl.name, err)
			}
			if err := tt.Execute(io.Discard, synth); err != nil {
				return fmt.Errorf("%w in %s evaluation: %v", domain.ErrTemplateSyntax, textTmpl.name, err)
			}
		}
	}

	return nil
}

// RenderFormat compiles and executes a single template string targeting the designated OutputFormat.
func RenderFormat(format domain.OutputFormat, tmplStr string, data any) (string, error) {
	if tmplStr == "" {
		return "", nil
	}

	buf := getBuffer()
	defer putBuffer(buf)

	switch format {
	case domain.OutputFormatHTML:
		t, err := htmltemplate.New("html").Funcs(SafeFuncMap).Parse(tmplStr)
		if err != nil {
			return "", fmt.Errorf("%w in html template: %v", domain.ErrTemplateSyntax, err)
		}
		if err := t.Execute(buf, data); err != nil {
			return "", fmt.Errorf("%w in html template execution: %v", domain.ErrTemplateSyntax, err)
		}
		return buf.String(), nil

	case domain.OutputFormatMarkdown, domain.OutputFormatPlainText:
		name := "markdown"
		if format == domain.OutputFormatPlainText {
			name = "text"
		}
		t, err := texttemplate.New(name).Funcs(SafeFuncMap).Parse(tmplStr)
		if err != nil {
			return "", fmt.Errorf("%w in %s template: %v", domain.ErrTemplateSyntax, name, err)
		}
		if err := t.Execute(buf, data); err != nil {
			return "", fmt.Errorf("%w in %s template execution: %v", domain.ErrTemplateSyntax, name, err)
		}
		return buf.String(), nil

	default:
		return "", fmt.Errorf("unsupported output format: %s", format)
	}
}

// Render compiles and executes HTML, Markdown, and Plain Text formats for a given TemplateSet against candidate data.
func Render(ctx context.Context, tmpl *domain.TemplateSet, profile *domain.Profile, content *domain.TailoredContent) (*domain.RenderOutput, error) {
	if tmpl == nil {
		return nil, fmt.Errorf("%w: template set is required", domain.ErrValidation)
	}

	renderCtx := NewRenderContext(tmpl, profile, content)

	htmlOut, err := RenderFormat(domain.OutputFormatHTML, tmpl.HTMLTemplate, renderCtx)
	if err != nil {
		return nil, err
	}

	mdOut, err := RenderFormat(domain.OutputFormatMarkdown, tmpl.MarkdownTemplate, renderCtx)
	if err != nil {
		return nil, err
	}

	txtOut, err := RenderFormat(domain.OutputFormatPlainText, tmpl.TextTemplate, renderCtx)
	if err != nil {
		return nil, err
	}

	return &domain.RenderOutput{
		HTML:      htmlOut,
		Markdown:  mdOut,
		PlainText: txtOut,
	}, nil
}

type engine struct{}

// New constructs a thread-safe, stateless domain.TemplateRenderer implementation.
func New() domain.TemplateRenderer {
	return &engine{}
}

// NewTemplateRenderer is a constructor alias for New.
func NewTemplateRenderer() domain.TemplateRenderer {
	return &engine{}
}

func (e *engine) Render(ctx context.Context, tmpl *domain.TemplateSet, profile *domain.Profile, content *domain.TailoredContent) (*domain.RenderOutput, error) {
	return Render(ctx, tmpl, profile, content)
}

func (e *engine) RenderFormat(format domain.OutputFormat, tmplStr string, data any) (string, error) {
	return RenderFormat(format, tmplStr, data)
}

func (e *engine) Validate(tmpl *domain.TemplateSet) error {
	return Validate(tmpl)
}

// DefaultSeededProfileName identifies the default placeholder name seeded upon database initialization.
const DefaultSeededProfileName = "Default User"

// HasCustomProfileData returns true if the profile contains non-empty custom user data rather than unconfigured defaults.
func HasCustomProfileData(prof *domain.Profile, snap *domain.MainProfileSnapshot) bool {
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
func BuildPreviewRenderContext(ctx context.Context, profileRepo domain.ProfileRepository, userID string, tmpl *domain.TemplateSet) *RenderContext {
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
			var tailored *domain.TailoredContent
			if snapshot != nil {
				tailored = domain.SnapshotToTailoredContent(snapshot)
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
func RenderAllFormats(r domain.TemplateRenderer, tmpl *domain.TemplateSet, renderCtx *RenderContext) (domain.RenderOutput, error) {
	if tmpl == nil || r == nil {
		return domain.RenderOutput{}, nil
	}

	var out domain.RenderOutput
	if tmpl.HTMLTemplate != "" {
		htmlOut, err := r.RenderFormat(domain.OutputFormatHTML, tmpl.HTMLTemplate, renderCtx)
		if err != nil {
			return domain.RenderOutput{}, fmt.Errorf("html template rendering failed: %w", err)
		}
		out.HTML = StripScriptTags(htmlOut)
	}

	if tmpl.MarkdownTemplate != "" {
		mdOut, err := r.RenderFormat(domain.OutputFormatMarkdown, tmpl.MarkdownTemplate, renderCtx)
		if err != nil {
			return domain.RenderOutput{}, fmt.Errorf("markdown template rendering failed: %w", err)
		}
		out.Markdown = mdOut
	}

	if tmpl.TextTemplate != "" {
		txtOut, err := r.RenderFormat(domain.OutputFormatPlainText, tmpl.TextTemplate, renderCtx)
		if err != nil {
			return domain.RenderOutput{}, fmt.Errorf("text template rendering failed: %w", err)
		}
		out.PlainText = txtOut
	}

	return out, nil
}

// ResolveVersionOutputs resolves or dynamically re-renders resume outputs incorporating targetTemplateID.
func ResolveVersionOutputs(
	ctx context.Context,
	r domain.TemplateRenderer,
	templateRepo domain.TemplateRepository,
	profileRepo domain.ProfileRepository,
	userID string,
	b *domain.Branch,
	ver *domain.BranchVersion,
	targetTemplateID string,
) domain.RenderOutput {
	if ver == nil {
		return domain.RenderOutput{}
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
			var prof *domain.Profile
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
					if hOut, rErr := r.RenderFormat(domain.OutputFormatHTML, tmpl.HTMLTemplate, renderCtx); rErr == nil {
						htmlOut = hOut
					}
				}
				if mdOut == "" && tmpl.MarkdownTemplate != "" {
					if mOut, rErr := r.RenderFormat(domain.OutputFormatMarkdown, tmpl.MarkdownTemplate, renderCtx); rErr == nil {
						mdOut = mOut
					}
				}
				if txtOut == "" && tmpl.TextTemplate != "" {
					if tOut, rErr := r.RenderFormat(domain.OutputFormatPlainText, tmpl.TextTemplate, renderCtx); rErr == nil {
						txtOut = tOut
					}
				}
			}
		}
	}

	return domain.RenderOutput{
		HTML:      htmlOut,
		Markdown:  mdOut,
		PlainText: txtOut,
	}
}

