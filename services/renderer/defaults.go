package renderer

import (
	"time"

	"resumeee/internal/domain"
)

const (
	// TemplateStandardCleanID is the canonical ID for the built-in Standard Clean template set.
	TemplateStandardCleanID = "builtin-standard-clean"

	// TemplateDetailedImmigrationCVID is the canonical ID for the built-in Detailed Immigration / CV template set.
	TemplateDetailedImmigrationCVID = "builtin-detailed-immigration-cv"
)

// StandardCleanTemplate returns the production default modern tech resume template set.
func StandardCleanTemplate() domain.TemplateSet {
	now := time.Now().UTC()
	return domain.TemplateSet{
		ID:          TemplateStandardCleanID,
		UserID:      "system",
		Name:        "Standard Clean",
		Description: "Modern tech resume with clean typography, blue accent rules, categorized skill tags, and chronological experience.",
		IsBuiltIn:   true,
		CSSStyles: `body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif; line-height: 1.5; color: #1a1a1a; margin: 0; padding: 24px; background: #ffffff; }
.resume { max-width: 800px; margin: 0 auto; }
.header { border-bottom: 2px solid #2563eb; padding-bottom: 12px; margin-bottom: 20px; }
h1 { margin: 0 0 4px 0; font-size: 28px; color: #0f172a; }
.headline { font-size: 16px; color: #4b5563; }
.contact { font-size: 14px; color: #6b7280; margin-top: 4px; }
.links { font-size: 13px; color: #2563eb; margin-top: 2px; }
h2 { font-size: 18px; border-bottom: 1px solid #e5e7eb; padding-bottom: 4px; margin-top: 20px; margin-bottom: 10px; color: #1e3a8a; text-transform: uppercase; letter-spacing: 0.5px; }
h3 { font-size: 15px; margin: 8px 0 2px 0; color: #111827; }
.meta { font-size: 13px; color: #6b7280; }
.skills-group { margin-bottom: 6px; font-size: 14px; }
.skills-cat { font-weight: 600; color: #374151; }
ul { margin: 6px 0 12px 18px; padding: 0; }
li { margin-bottom: 4px; font-size: 14px; }`,
		HTMLTemplate: `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>{{.FullName}} - Resume</title>
<style>{{.CSSStyles}}</style>
</head>
<body>
<div class="resume">
  <header class="header">
    <h1>{{.FullName}}</h1>
    {{if .Headline}}<div class="headline">{{.Headline}}</div>{{end}}
    <div class="contact">
      {{if .Email}}<span>{{.Email}}</span>{{end}}
      {{if .Phone}} | <span>{{.Phone}}</span>{{end}}
      {{if .Location.City}} | <span>{{.Location.City}}{{if .Location.Region}}, {{.Location.Region}}{{end}}</span>{{end}}
    </div>
    {{if .Profiles}}
    <div class="links">
      {{range $i, $p := .Profiles}}{{if $i}} | {{end}}<a href="{{$p.URL}}">{{$p.Network}}: {{$p.Username}}</a>{{end}}
    </div>
    {{end}}
  </header>
  {{if .Summary}}
  <section class="summary">
    <h2>Professional Summary</h2>
    <p>{{.Summary}}</p>
  </section>
  {{end}}
  {{if .SkillGroups}}
  <section class="skills">
    <h2>Technical Skills</h2>
    {{range .SkillGroups}}
    <div class="skills-group">
      <span class="skills-cat">{{.Category}}:</span> {{join .Skills ", "}}
    </div>
    {{end}}
  </section>
  {{end}}
  {{if .Experiences}}
  <section class="experience">
    <h2>Work Experience</h2>
    {{range .Experiences}}
    <div class="job">
      <h3>{{.RoleTitle}} — {{.Company}}</h3>
      <div class="meta">{{formatDateRange .StartDate .EndDate "Present"}}{{if .Location.City}} | {{.Location.City}}{{if .Location.Region}}, {{.Location.Region}}{{end}}{{end}}</div>
      <ul>
        {{range .Bullets}}
        <li>{{.Text}}</li>
        {{end}}
      </ul>
    </div>
    {{end}}
  </section>
  {{end}}
  {{if .Projects}}
  <section class="projects">
    <h2>Projects</h2>
    {{range .Projects}}
    <div class="project">
      <h3>{{.Name}}{{if .Role}} ({{.Role}}){{end}}</h3>
      {{if .Summary}}<p class="project-summary">{{.Summary}}</p>{{end}}
      <ul>
        {{range .Bullets}}
        <li>{{.Text}}</li>
        {{end}}
      </ul>
    </div>
    {{end}}
  </section>
  {{end}}
  {{if .Education}}
  <section class="education">
    <h2>Education</h2>
    {{range .Education}}
    <div class="edu">
      <h3>{{.Degree}}{{if .Field}} in {{.Field}}{{end}} — {{.Institution}}</h3>
      <div class="meta">{{formatDateRange .StartDate .EndDate "Present"}}{{if .Score}} | {{.Score}}{{end}}</div>
      {{if .Bullets}}
      <ul>
        {{range .Bullets}}
        <li>{{.Text}}</li>
        {{end}}
      </ul>
      {{end}}
    </div>
    {{end}}
  </section>
  {{end}}
  {{if .Certifications}}
  <section class="certifications">
    <h2>Certifications</h2>
    <ul>
      {{range .Certifications}}
      <li><strong>{{.Name}}</strong> — {{.Issuer}} ({{.Date}})</li>
      {{end}}
    </ul>
  </section>
  {{end}}
</div>
</body>
</html>`,
		MarkdownTemplate: `# {{.FullName}}
{{if .Headline}}**{{.Headline}}**
{{end}}{{if .Email}}{{.Email}}{{end}}{{if .Phone}} | {{.Phone}}{{end}}{{if .Location.City}} | {{.Location.City}}{{if .Location.Region}}, {{.Location.Region}}{{end}}{{end}}
{{if .Profiles}}{{range $i, $p := .Profiles}}{{if $i}} | {{end}}[{{$p.Network}}]({{$p.URL}}){{end}}
{{end}}
{{if .Summary}}
## Professional Summary
{{.Summary}}
{{end}}
{{if .SkillGroups}}
## Technical Skills
{{range .SkillGroups}}- **{{.Category}}:** {{join .Skills ", "}}
{{end}}{{end}}
{{if .Experiences}}
## Work Experience
{{range .Experiences}}
### {{.RoleTitle}} — {{.Company}}
*{{formatDateRange .StartDate .EndDate "Present"}}{{if .Location.City}} | {{.Location.City}}{{if .Location.Region}}, {{.Location.Region}}{{end}}{{end}}*

{{range .Bullets}}- {{.Text}}
{{end}}
{{end}}{{end}}
{{if .Projects}}
## Projects
{{range .Projects}}
### {{.Name}}{{if .Role}} — {{.Role}}{{end}}
{{if .Summary}}{{.Summary}}
{{end}}
{{range .Bullets}}- {{.Text}}
{{end}}
{{end}}{{end}}
{{if .Education}}
## Education
{{range .Education}}
### {{.Degree}}{{if .Field}} in {{.Field}}{{end}} — {{.Institution}}
*{{formatDateRange .StartDate .EndDate "Present"}}{{if .Score}} | {{.Score}}{{end}}*
{{range .Bullets}}- {{.Text}}
{{end}}
{{end}}{{end}}
{{if .Certifications}}
## Certifications
{{range .Certifications}}- **{{.Name}}** — {{.Issuer}} ({{.Date}})
{{end}}{{end}}`,
		TextTemplate: `{{.FullName}}
{{if .Headline}}{{.Headline}}
{{end}}{{if .Email}}{{.Email}}{{end}}{{if .Phone}} | {{.Phone}}{{end}}{{if .Location.City}} | {{.Location.City}}{{if .Location.Region}}, {{.Location.Region}}{{end}}{{end}}
{{if .Profiles}}{{range $i, $p := .Profiles}}{{if $i}} | {{end}}{{$p.Network}}: {{$p.Username}}{{end}}
{{end}}
{{if .Summary}}
PROFESSIONAL SUMMARY:
{{.Summary}}
{{end}}
{{if .SkillGroups}}
TECHNICAL SKILLS:
{{range .SkillGroups}}{{.Category}}: {{join .Skills ", "}}
{{end}}{{end}}
{{if .Experiences}}
WORK EXPERIENCE:
{{range .Experiences}}
{{.RoleTitle}} at {{.Company}} ({{formatDateRange .StartDate .EndDate "Present"}})
{{range .Bullets}}* {{.Text}}
{{end}}
{{end}}{{end}}
{{if .Projects}}
PROJECTS:
{{range .Projects}}
{{.Name}}{{if .Role}} - {{.Role}}{{end}}
{{if .Summary}}{{.Summary}}
{{end}}{{range .Bullets}}* {{.Text}}
{{end}}
{{end}}{{end}}
{{if .Education}}
EDUCATION:
{{range .Education}}
{{.Degree}}{{if .Field}} in {{.Field}}{{end}} - {{.Institution}} ({{formatDateRange .StartDate .EndDate "Present"}}){{if .Score}} - {{.Score}}{{end}}
{{range .Bullets}}* {{.Text}}
{{end}}
{{end}}{{end}}
{{if .Certifications}}
CERTIFICATIONS:
{{range .Certifications}}* {{.Name}} - {{.Issuer}} ({{.Date}})
{{end}}{{end}}`,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// DetailedImmigrationCVTemplate returns the unabridged formal curriculum vitae template set.
func DetailedImmigrationCVTemplate() domain.TemplateSet {
	now := time.Now().UTC()
	return domain.TemplateSet{
		ID:          TemplateDetailedImmigrationCVID,
		UserID:      "system",
		Name:        "Detailed Immigration / CV",
		Description: "Complete formal career history template with hours per week, company location, supervisor contact, and duties summary for visa compliance.",
		IsBuiltIn:   true,
		CSSStyles: `body { font-family: "Times New Roman", Times, Georgia, serif; line-height: 1.6; color: #111; margin: 0; padding: 32px; background: #ffffff; }
.cv { max-width: 850px; margin: 0 auto; }
.header { border-bottom: 2px solid #000; padding-bottom: 12px; margin-bottom: 24px; text-align: center; }
h1 { margin: 0 0 6px 0; font-size: 26px; text-transform: uppercase; letter-spacing: 1px; color: #000; }
.headline { font-size: 15px; font-style: italic; color: #333; }
.contact { font-size: 13px; margin-top: 6px; }
h2 { font-size: 17px; border-bottom: 1px solid #444; padding-bottom: 3px; margin-top: 24px; text-transform: uppercase; color: #111; letter-spacing: 0.5px; }
h3 { font-size: 15px; margin: 12px 0 4px 0; color: #000; }
.meta { font-size: 13px; color: #333; margin-bottom: 6px; }
.duties { font-style: italic; margin: 4px 0; }
ul { margin: 6px 0 16px 20px; padding: 0; }
li { margin-bottom: 4px; font-size: 14px; }`,
		HTMLTemplate: `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>{{.FullName}} - Comprehensive Curriculum Vitae</title>
<style>{{.CSSStyles}}</style>
</head>
<body>
<div class="cv">
  <header class="header">
    <h1>{{.FullName}}</h1>
    {{if .Headline}}<div class="headline">{{.Headline}}</div>{{end}}
    <div class="contact">
      {{if .Email}}<span>Email: {{.Email}}</span>{{end}}
      {{if .Phone}} | <span>Phone: {{.Phone}}</span>{{end}}
      {{if .Location.City}} | <span>Residence: {{.Location.City}}{{if .Location.Region}}, {{.Location.Region}}{{end}}{{if .Location.CountryCode}}, {{.Location.CountryCode}}{{end}}</span>{{end}}
    </div>
  </header>
  {{if .Summary}}
  <section class="summary">
    <h2>Professional Statement</h2>
    <p>{{.Summary}}</p>
  </section>
  {{end}}
  {{if .SkillGroups}}
  <section class="skills">
    <h2>Competencies & Specializations</h2>
    {{range .SkillGroups}}
    <div class="skills-group">
      <strong>{{.Category}}:</strong> {{join .Skills ", "}}
    </div>
    {{end}}
  </section>
  {{end}}
  {{if .Experiences}}
  <section class="experience">
    <h2>Comprehensive Employment & Verification History</h2>
    {{range .Experiences}}
    <div class="record">
      <h3>{{.RoleTitle}} — {{.Company}}</h3>
      <div class="meta">
        <div><strong>Dates of Employment:</strong> {{formatDateRange .StartDate .EndDate "Present"}}</div>
        {{if .CompanyLocation.City}}<div><strong>Company Location:</strong> {{.CompanyLocation.City}}{{if .CompanyLocation.Region}}, {{.CompanyLocation.Region}}{{end}}{{if .CompanyLocation.CountryCode}}, {{.CompanyLocation.CountryCode}}{{end}}</div>{{end}}
        {{if .CompanyContact}}<div><strong>Company Contact:</strong> {{.CompanyContact.Type}}: {{.CompanyContact.Value}}</div>{{end}}
        {{if .HoursPerWeek}}<div><strong>Hours per Week:</strong> {{.HoursPerWeek}}</div>{{end}}
        {{if .SupervisorName}}<div><strong>Supervisor:</strong> {{.SupervisorName}}{{if .SupervisorContact}} ({{.SupervisorContact}}){{end}}</div>{{end}}
        {{if .DutiesSummary}}<p class="duties"><strong>Official Duties & Responsibilities:</strong> {{.DutiesSummary}}</p>{{end}}
      </div>
      <ul>
        {{range .Bullets}}
        <li>{{.Text}}</li>
        {{end}}
      </ul>
    </div>
    {{end}}
  </section>
  {{end}}
  {{if .Projects}}
  <section class="projects">
    <h2>Key Projects & Initiatives</h2>
    {{range .Projects}}
    <div class="project-record">
      <h3>{{.Name}}{{if .Role}} ({{.Role}}){{end}}</h3>
      <div class="meta">{{formatDateRange .StartDate .EndDate "Present"}}{{if .URL}} | {{.URL}}{{end}}</div>
      {{if .Summary}}<p>{{.Summary}}</p>{{end}}
      <ul>
        {{range .Bullets}}
        <li>{{.Text}}</li>
        {{end}}
      </ul>
    </div>
    {{end}}
  </section>
  {{end}}
  {{if .Education}}
  <section class="education">
    <h2>Academic Credentials</h2>
    {{range .Education}}
    <div class="edu-record">
      <h3>{{.Degree}}{{if .Field}} in {{.Field}}{{end}} — {{.Institution}}</h3>
      <div class="meta"><strong>Period:</strong> {{formatDateRange .StartDate .EndDate "Present"}}{{if .Score}} | <strong>Grade/Score:</strong> {{.Score}}{{end}}</div>
      {{if .Bullets}}
      <ul>
        {{range .Bullets}}
        <li>{{.Text}}</li>
        {{end}}
      </ul>
      {{end}}
    </div>
    {{end}}
  </section>
  {{end}}
  {{if .Certifications}}
  <section class="certifications">
    <h2>Professional Licenses & Certifications</h2>
    <ul>
      {{range .Certifications}}
      <li><strong>{{.Name}}</strong> — Issued by {{.Issuer}} ({{.Date}}){{if .URL}} [{{.URL}}]{{end}}</li>
      {{end}}
    </ul>
  </section>
  {{end}}
</div>
</body>
</html>`,
		MarkdownTemplate: `# {{.FullName}} — Comprehensive Curriculum Vitae
{{if .Headline}}*{{.Headline}}*
{{end}}{{if .Email}}Email: {{.Email}}{{end}}{{if .Phone}} | Phone: {{.Phone}}{{end}}{{if .Location.City}} | Location: {{.Location.City}}{{if .Location.Region}}, {{.Location.Region}}{{end}}{{end}}

{{if .Summary}}
## Professional Statement
{{.Summary}}
{{end}}
{{if .SkillGroups}}
## Competencies & Specializations
{{range .SkillGroups}}- **{{.Category}}:** {{join .Skills ", "}}
{{end}}{{end}}
{{if .Experiences}}
## Comprehensive Employment History
{{range .Experiences}}
### {{.RoleTitle}} — {{.Company}}
- **Dates:** {{formatDateRange .StartDate .EndDate "Present"}}
{{if .CompanyLocation.City}}- **Company Location:** {{.CompanyLocation.City}}{{if .CompanyLocation.Region}}, {{.CompanyLocation.Region}}{{end}}
{{end}}{{if .CompanyContact}}- **Company Contact:** {{.CompanyContact.Type}}: {{.CompanyContact.Value}}
{{end}}{{if .HoursPerWeek}}- **Hours per Week:** {{.HoursPerWeek}}
{{end}}{{if .SupervisorName}}- **Supervisor:** {{.SupervisorName}}{{if .SupervisorContact}} ({{.SupervisorContact}}){{end}}
{{end}}{{if .DutiesSummary}}- **Official Duties:** {{.DutiesSummary}}
{{end}}
**Responsibilities & Verifiable Achievements:**
{{range .Bullets}}- {{.Text}}
{{end}}
{{end}}{{end}}
{{if .Projects}}
## Projects & Technical Initiatives
{{range .Projects}}
### {{.Name}}{{if .Role}} — {{.Role}}{{end}}
- **Timeline:** {{formatDateRange .StartDate .EndDate "Present"}}{{if .URL}} | **URL:** {{.URL}}{{end}}
{{if .Summary}}{{.Summary}}
{{end}}
{{range .Bullets}}- {{.Text}}
{{end}}
{{end}}{{end}}
{{if .Education}}
## Academic Credentials
{{range .Education}}
### {{.Degree}}{{if .Field}} in {{.Field}}{{end}} — {{.Institution}}
- **Period:** {{formatDateRange .StartDate .EndDate "Present"}}{{if .Score}} | **Score:** {{.Score}}{{end}}
{{range .Bullets}}- {{.Text}}
{{end}}
{{end}}{{end}}
{{if .Certifications}}
## Certifications & Licenses
{{range .Certifications}}- **{{.Name}}** — {{.Issuer}} ({{.Date}})
{{end}}{{end}}`,
		TextTemplate: `{{.FullName}} - COMPREHENSIVE CURRICULUM VITAE
{{if .Headline}}{{.Headline}}
{{end}}{{if .Email}}Email: {{.Email}}{{end}}{{if .Phone}} | Phone: {{.Phone}}{{end}}{{if .Location.City}} | Location: {{.Location.City}}{{if .Location.Region}}, {{.Location.Region}}{{end}}{{end}}

{{if .Summary}}
PROFESSIONAL STATEMENT:
{{.Summary}}
{{end}}
{{if .SkillGroups}}
COMPETENCIES:
{{range .SkillGroups}}{{.Category}}: {{join .Skills ", "}}
{{end}}{{end}}
{{if .Experiences}}
EMPLOYMENT HISTORY:
{{range .Experiences}}
{{.RoleTitle}} at {{.Company}}
Dates: {{formatDateRange .StartDate .EndDate "Present"}}
{{if .CompanyLocation.City}}Company Location: {{.CompanyLocation.City}}{{if .CompanyLocation.Region}}, {{.CompanyLocation.Region}}{{end}}
{{end}}{{if .CompanyContact}}Company Contact: {{.CompanyContact.Type}}: {{.CompanyContact.Value}}
{{end}}{{if .HoursPerWeek}}Hours/Week: {{.HoursPerWeek}}
{{end}}{{if .SupervisorName}}Supervisor: {{.SupervisorName}}{{if .SupervisorContact}} ({{.SupervisorContact}}){{end}}
{{end}}{{if .DutiesSummary}}Official Duties: {{.DutiesSummary}}
{{end}}Achievements:
{{range .Bullets}}* {{.Text}}
{{end}}
{{end}}{{end}}
{{if .Projects}}
PROJECTS:
{{range .Projects}}
{{.Name}}{{if .Role}} - {{.Role}}{{end}} ({{formatDateRange .StartDate .EndDate "Present"}})
{{if .Summary}}{{.Summary}}
{{end}}{{range .Bullets}}* {{.Text}}
{{end}}
{{end}}{{end}}
{{if .Education}}
ACADEMIC CREDENTIALS:
{{range .Education}}
{{.Degree}}{{if .Field}} in {{.Field}}{{end}} - {{.Institution}} ({{formatDateRange .StartDate .EndDate "Present"}}){{if .Score}} - {{.Score}}{{end}}
{{range .Bullets}}* {{.Text}}
{{end}}
{{end}}{{end}}
{{if .Certifications}}
CERTIFICATIONS:
{{range .Certifications}}* {{.Name}} - {{.Issuer}} ({{.Date}})
{{end}}{{end}}`,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// DefaultTemplateSets returns the list of built-in production templates.
func DefaultTemplateSets() []domain.TemplateSet {
	return []domain.TemplateSet{
		StandardCleanTemplate(),
		DetailedImmigrationCVTemplate(),
	}
}
