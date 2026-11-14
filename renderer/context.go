package renderer

import (
	htmltemplate "html/template"
	"strings"

	"github.com/resumeee-app/resumeee-core"
)

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
	populateProfileFields(ctx, profile)
	populateContentFields(ctx, content)
	return ctx
}

// populateProfileFields copies contact, location, social links, and metadata from profile into ctx.
func populateProfileFields(ctx *RenderContext, profile *core.Profile) {
	if profile == nil {
		ctx.Profile = &core.Profile{}
		return
	}
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
}

// populateContentFields binds tailored sections onto ctx, overriding profile summary when tailored summary is present.
func populateContentFields(ctx *RenderContext, content *core.TailoredContent) {
	if content == nil {
		ctx.Content = &core.TailoredContent{}
		return
	}
	ctx.Content = content
	if strings.TrimSpace(content.Summary) != "" {
		ctx.Summary = content.Summary
	}
	ctx.SkillGroups = content.SkillGroups
	ctx.Experiences = content.Experiences
	ctx.Projects = content.Projects
	ctx.Education = content.Education
	ctx.Certifications = content.Certifications
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
