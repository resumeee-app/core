package renderer

import (
	"bytes"
	"context"
	"fmt"
	htmltemplate "html/template"
	"io"
	"strings"
	texttemplate "text/template"

	"resumeee/internal/domain"
)

// RenderContext binds the Master Profile, Tailored Content, and template styling into a unified execution context.
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
	endDate := "2023-01-01"
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
				StartDate:         "2020-01-01",
				EndDate:           &endDate,
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
				StartDate:      "2021-06-01",
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
				StartDate:      "2014-09-01",
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
				Date:           "2022-05-01",
				URL:            "https://cncf.io/cert/cka",
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

	if tmpl.MarkdownTemplate != "" {
		mt, err := texttemplate.New("markdown").Funcs(SafeFuncMap).Parse(tmpl.MarkdownTemplate)
		if err != nil {
			return fmt.Errorf("%w in markdown_template: %v", domain.ErrTemplateSyntax, err)
		}
		if err := mt.Execute(io.Discard, synth); err != nil {
			return fmt.Errorf("%w in markdown_template evaluation: %v", domain.ErrTemplateSyntax, err)
		}
	}

	if tmpl.TextTemplate != "" {
		tt, err := texttemplate.New("text").Funcs(SafeFuncMap).Parse(tmpl.TextTemplate)
		if err != nil {
			return fmt.Errorf("%w in text_template: %v", domain.ErrTemplateSyntax, err)
		}
		if err := tt.Execute(io.Discard, synth); err != nil {
			return fmt.Errorf("%w in text_template evaluation: %v", domain.ErrTemplateSyntax, err)
		}
	}

	return nil
}

// RenderFormat compiles and executes a single template string targeting the designated OutputFormat.
func RenderFormat(format domain.OutputFormat, tmplStr string, data any) (string, error) {
	if tmplStr == "" {
		return "", nil
	}

	switch format {
	case domain.OutputFormatHTML:
		t, err := htmltemplate.New("html").Funcs(SafeFuncMap).Parse(tmplStr)
		if err != nil {
			return "", fmt.Errorf("%w in html template: %v", domain.ErrTemplateSyntax, err)
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, data); err != nil {
			return "", fmt.Errorf("%w in html template execution: %v", domain.ErrTemplateSyntax, err)
		}
		return buf.String(), nil

	case domain.OutputFormatMarkdown:
		t, err := texttemplate.New("markdown").Funcs(SafeFuncMap).Parse(tmplStr)
		if err != nil {
			return "", fmt.Errorf("%w in markdown template: %v", domain.ErrTemplateSyntax, err)
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, data); err != nil {
			return "", fmt.Errorf("%w in markdown template execution: %v", domain.ErrTemplateSyntax, err)
		}
		return buf.String(), nil

	case domain.OutputFormatPlainText:
		t, err := texttemplate.New("text").Funcs(SafeFuncMap).Parse(tmplStr)
		if err != nil {
			return "", fmt.Errorf("%w in text template: %v", domain.ErrTemplateSyntax, err)
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, data); err != nil {
			return "", fmt.Errorf("%w in text template execution: %v", domain.ErrTemplateSyntax, err)
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
