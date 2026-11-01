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
		CSSStyles: `*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif; line-height: 1.5; color: #1a1a1a; padding: 24px; background: #ffffff; -webkit-font-smoothing: antialiased; }
.resume { max-width: 800px; margin: 0 auto; background: #ffffff; }
.header { border-bottom: 2px solid #2563eb; padding-bottom: 12px; margin-bottom: 20px; }
h1.candidate-name, h1 { margin: 0 0 4px 0; font-size: 28px; color: #0f172a; line-height: 1.15; }
.headline { font-size: 16px; color: #4b5563; margin-bottom: 4px; }
address.contact-info, .contact { font-style: normal; font-size: 14px; color: #6b7280; margin-top: 4px; display: flex; flex-wrap: wrap; align-items: center; gap: 4px 8px; }
address.contact-info a, .links a { color: #2563eb; text-decoration: none; }
.separator { color: #94a3b8; user-select: none; }
.links { font-size: 13px; color: #2563eb; margin-top: 2px; }
section.resume-section, section { margin-bottom: 16px; }
h2.section-title, h2 { font-size: 18px; border-bottom: 1px solid #e5e7eb; padding-bottom: 4px; margin-top: 20px; margin-bottom: 10px; color: #1e3a8a; text-transform: uppercase; letter-spacing: 0.5px; }
article.entry-item, .job, .project, .edu { margin-bottom: 12px; }
.entry-header { display: flex; justify-content: space-between; align-items: baseline; flex-wrap: wrap; gap: 4px; margin-bottom: 2px; }
h3.entry-title, h3 { font-size: 15px; margin: 4px 0 2px 0; color: #111827; }
.entry-meta, .meta { font-size: 13px; color: #6b7280; font-weight: 500; }
.date-range { font-variant-numeric: tabular-nums; }
.skills-group { margin-bottom: 6px; font-size: 14px; }
.skills-cat, .skills-category { font-weight: 600; color: #374151; }
ul.bullet-list, ul { margin: 6px 0 12px 18px; padding: 0; list-style-type: disc; }
li { margin-bottom: 4px; font-size: 14px; color: #1e293b; }
@page { size: letter portrait; margin: 0.5in; }
@media print {
  body { padding: 0; background: #ffffff !important; color: #000000 !important; }
  .resume { max-width: 100%; margin: 0; padding: 0; }
  h1, h2, h3 { break-after: avoid; page-break-after: avoid; color: #000000 !important; }
  article.entry-item, .job, .project, .edu, .skills-group, ul li { break-inside: avoid; page-break-inside: avoid; }
  p, li { orphans: 3; widows: 3; }
  address.contact-info a, a { color: #000000 !important; text-decoration: none !important; }
  .no-print { display: none !important; }
}`,
		HTMLTemplate: `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.FullName}} - Resume</title>
<script type="application/ld+json">
{
  "@context": "https://schema.org",
  "@type": "Person",
  "name": {{.FullName}}
  {{if .Headline}}, "jobTitle": {{.Headline}}{{end}}
  {{if .Email}}, "email": {{.Email}}{{end}}
  {{if .Phone}}, "telephone": {{.Phone}}{{end}}
  {{if .Website}}, "url": {{.Website}}{{end}}
  {{if .Location.City}}, "address": {
    "@type": "PostalAddress",
    "addressLocality": {{.Location.City}}
    {{if .Location.Region}}, "addressRegion": {{.Location.Region}}{{end}}
    {{if .Location.CountryCode}}, "addressCountry": {{.Location.CountryCode}}{{end}}
  }{{end}}
  {{if .Profiles}}, "sameAs": [
    {{range $i, $p := .Profiles}}{{if $i}}, {{end}}{{$p.URL}}{{end}}
  ]{{end}}
  {{if .SkillGroups}}, "knowsAbout": [
    {{range $i, $sg := .SkillGroups}}{{range $j, $s := $sg.Skills}}{{if or $i $j}}, {{end}}{{$s}}{{end}}{{end}}
  ]{{end}}
}
</script>
<style>{{.CSSStyles}}</style>
</head>
<body>
<main class="resume" id="resume">
  <header class="header">
    <h1 class="candidate-name">{{.FullName}}</h1>
    {{if .Headline}}<div class="headline">{{.Headline}}</div>{{end}}
    <address class="contact-info contact">
      {{if .Email}}<span><a href="mailto:{{.Email}}">{{.Email}}</a></span>{{end}}
      {{if .Phone}}<span class="separator">•</span><span><a href="tel:{{.Phone}}">{{.Phone}}</a></span>{{end}}
      {{if .Location.City}}<span class="separator">•</span><span class="location">{{.Location.City}}{{if .Location.Region}}, {{.Location.Region}}{{end}}{{if .Location.CountryCode}}, {{.Location.CountryCode}}{{end}}</span>{{end}}
      {{if .Website}}<span class="separator">•</span><span><a href="{{.Website}}">{{.Website}}</a></span>{{end}}
      {{range .Profiles}}
      <span class="separator">•</span><span><a href="{{.URL}}">{{.Network}}: {{.Username}}</a></span>
      {{end}}
    </address>
  </header>
  {{if .Summary}}
  <section class="resume-section summary" id="summary">
    <h2 class="section-title">Professional Summary</h2>
    <p class="summary-text">{{.Summary}}</p>
  </section>
  {{end}}
  {{if .SkillGroups}}
  <section class="resume-section skills" id="skills">
    <h2 class="section-title">Technical Skills</h2>
    {{range .SkillGroups}}
    <div class="skills-group">
      <strong class="skills-cat skills-category">{{.Category}}:</strong> <span class="skills-list">{{join .Skills ", "}}</span>
    </div>
    {{end}}
  </section>
  {{end}}
  {{if .Experiences}}
  <section class="resume-section experience" id="experience">
    <h2 class="section-title">Work Experience</h2>
    {{range .Experiences}}
    <article class="entry-item experience-item job">
      <div class="entry-header">
        <h3 class="entry-title">{{.RoleTitle}} — {{.Company}}</h3>
        <div class="entry-meta meta"><span class="date-range">{{formatDateRange .StartDate .EndDate "Present"}}</span>{{if .Location.City}}<span class="entry-location"> | {{.Location.City}}{{if .Location.Region}}, {{.Location.Region}}{{end}}</span>{{end}}</div>
      </div>
      {{if .Bullets}}
      <ul class="bullet-list">
        {{range .Bullets}}
        <li>{{.Text}}</li>
        {{end}}
      </ul>
      {{end}}
    </article>
    {{end}}
  </section>
  {{end}}
  {{if .Projects}}
  <section class="resume-section projects" id="projects">
    <h2 class="section-title">Projects</h2>
    {{range .Projects}}
    <article class="entry-item project-item project">
      <div class="entry-header">
        <h3 class="entry-title">{{.Name}}{{if .Role}} ({{.Role}}){{end}}</h3>
        {{if .URL}}<div class="entry-meta meta"><a href="{{.URL}}">{{.URL}}</a></div>{{end}}
      </div>
      {{if .Summary}}<p class="project-summary">{{.Summary}}</p>{{end}}
      {{if .Bullets}}
      <ul class="bullet-list">
        {{range .Bullets}}
        <li>{{.Text}}</li>
        {{end}}
      </ul>
      {{end}}
    </article>
    {{end}}
  </section>
  {{end}}
  {{if .Education}}
  <section class="resume-section education" id="education">
    <h2 class="section-title">Education</h2>
    {{range .Education}}
    <article class="entry-item education-item edu">
      <div class="entry-header">
        <h3 class="entry-title">{{.Degree}}{{if .Field}} in {{.Field}}{{end}} — {{.Institution}}</h3>
        <div class="entry-meta meta"><span class="date-range">{{formatDateRange .StartDate .EndDate "Present"}}</span>{{if .Score}}<span class="entry-score"> | {{.Score}}</span>{{end}}</div>
      </div>
      {{if .Bullets}}
      <ul class="bullet-list">
        {{range .Bullets}}
        <li>{{.Text}}</li>
        {{end}}
      </ul>
      {{end}}
    </article>
    {{end}}
  </section>
  {{end}}
  {{if .Certifications}}
  <section class="resume-section certifications" id="certifications">
    <h2 class="section-title">Certifications</h2>
    <ul class="bullet-list certifications-list">
      {{range .Certifications}}
      <li><strong>{{.Name}}</strong> — {{.Issuer}}{{if .Date}} ({{.Date}}){{end}}</li>
      {{end}}
    </ul>
  </section>
  {{end}}
</main>
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
		CSSStyles: `*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
body { font-family: "Times New Roman", Times, Georgia, serif; line-height: 1.6; color: #111; padding: 32px; background: #ffffff; -webkit-font-smoothing: antialiased; }
.cv { max-width: 850px; margin: 0 auto; background: #ffffff; }
.header { border-bottom: 2px solid #000; padding-bottom: 12px; margin-bottom: 24px; text-align: center; }
h1.candidate-name, h1 { margin: 0 0 6px 0; font-size: 26px; text-transform: uppercase; letter-spacing: 1px; color: #000; line-height: 1.15; }
.headline { font-size: 15px; font-style: italic; color: #333; margin-bottom: 6px; }
address.contact-info, .contact { font-style: normal; font-size: 13px; margin-top: 6px; display: flex; flex-wrap: wrap; justify-content: center; align-items: center; gap: 4px 6px; }
address.contact-info a, a { color: #111; text-decoration: none; }
.separator { color: #555; user-select: none; }
section.resume-section, section { margin-bottom: 20px; }
h2.section-title, h2 { font-size: 17px; border-bottom: 1px solid #444; padding-bottom: 3px; margin-top: 24px; text-transform: uppercase; color: #111; letter-spacing: 0.5px; }
article.entry-item, .record, .project-record, .edu-record { margin-bottom: 16px; }
.entry-header { display: flex; justify-content: space-between; align-items: baseline; flex-wrap: wrap; gap: 4px; margin-bottom: 4px; }
h3.entry-title, h3 { font-size: 15px; margin: 8px 0 4px 0; color: #000; }
.entry-meta, .meta { font-size: 13px; color: #333; margin-bottom: 6px; }
.date-range { font-variant-numeric: tabular-nums; }
.duties { font-style: italic; margin: 4px 0; }
.skills-group { margin-bottom: 6px; font-size: 14px; }
.skills-cat { font-weight: 600; }
ul.bullet-list, ul { margin: 6px 0 16px 20px; padding: 0; list-style-type: disc; }
li { margin-bottom: 4px; font-size: 14px; color: #111; }
@page { size: letter portrait; margin: 0.5in; }
@media print {
  body { padding: 0; background: #ffffff !important; color: #000000 !important; }
  .cv { max-width: 100%; margin: 0; padding: 0; }
  h1, h2, h3 { break-after: avoid; page-break-after: avoid; color: #000000 !important; }
  article.entry-item, .record, .project-record, .edu-record, .skills-group, ul li { break-inside: avoid; page-break-inside: avoid; }
  p, li { orphans: 3; widows: 3; }
  address.contact-info a, a { color: #000000 !important; text-decoration: none !important; }
  .no-print { display: none !important; }
}`,
		HTMLTemplate: `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.FullName}} - Comprehensive Curriculum Vitae</title>
<script type="application/ld+json">
{
  "@context": "https://schema.org",
  "@type": "Person",
  "name": {{.FullName}}
  {{if .Headline}}, "jobTitle": {{.Headline}}{{end}}
  {{if .Email}}, "email": {{.Email}}{{end}}
  {{if .Phone}}, "telephone": {{.Phone}}{{end}}
  {{if .Website}}, "url": {{.Website}}{{end}}
  {{if .Location.City}}, "address": {
    "@type": "PostalAddress",
    "addressLocality": {{.Location.City}}
    {{if .Location.Region}}, "addressRegion": {{.Location.Region}}{{end}}
    {{if .Location.CountryCode}}, "addressCountry": {{.Location.CountryCode}}{{end}}
  }{{end}}
  {{if .Profiles}}, "sameAs": [
    {{range $i, $p := .Profiles}}{{if $i}}, {{end}}{{$p.URL}}{{end}}
  ]{{end}}
  {{if .SkillGroups}}, "knowsAbout": [
    {{range $i, $sg := .SkillGroups}}{{range $j, $s := $sg.Skills}}{{if or $i $j}}, {{end}}{{$s}}{{end}}{{end}}
  ]{{end}}
}
</script>
<style>{{.CSSStyles}}</style>
</head>
<body>
<main class="cv" id="cv">
  <header class="header">
    <h1 class="candidate-name">{{.FullName}}</h1>
    {{if .Headline}}<div class="headline">{{.Headline}}</div>{{end}}
    <address class="contact-info contact">
      {{if .Email}}<span>Email: <a href="mailto:{{.Email}}">{{.Email}}</a></span>{{end}}
      {{if .Phone}}<span class="separator"> | </span><span>Phone: <a href="tel:{{.Phone}}">{{.Phone}}</a></span>{{end}}
      {{if .Location.City}}<span class="separator"> | </span><span class="location">Residence: {{.Location.City}}{{if .Location.Region}}, {{.Location.Region}}{{end}}{{if .Location.CountryCode}}, {{.Location.CountryCode}}{{end}}</span>{{end}}
      {{if .Website}}<span class="separator"> | </span><span>Website: <a href="{{.Website}}">{{.Website}}</a></span>{{end}}
      {{range .Profiles}}
      <span class="separator"> | </span><span><a href="{{.URL}}">{{.Network}}: {{.Username}}</a></span>
      {{end}}
    </address>
  </header>
  {{if .Summary}}
  <section class="resume-section summary" id="summary">
    <h2 class="section-title">Professional Statement</h2>
    <p class="summary-text">{{.Summary}}</p>
  </section>
  {{end}}
  {{if .SkillGroups}}
  <section class="resume-section skills" id="skills">
    <h2 class="section-title">Competencies & Specializations</h2>
    {{range .SkillGroups}}
    <div class="skills-group">
      <strong class="skills-cat">{{.Category}}:</strong> <span class="skills-list">{{join .Skills ", "}}</span>
    </div>
    {{end}}
  </section>
  {{end}}
  {{if .Experiences}}
  <section class="resume-section experience" id="experience">
    <h2 class="section-title">Comprehensive Employment & Verification History</h2>
    {{range .Experiences}}
    <article class="entry-item experience-item record">
      <div class="entry-header">
        <h3 class="entry-title">{{.RoleTitle}} — {{.Company}}</h3>
      </div>
      <div class="entry-meta meta">
        <div><strong>Dates of Employment:</strong> <span class="date-range">{{formatDateRange .StartDate .EndDate "Present"}}</span></div>
        {{if .CompanyLocation.City}}<div><strong>Company Location:</strong> {{.CompanyLocation.City}}{{if .CompanyLocation.Region}}, {{.CompanyLocation.Region}}{{end}}{{if .CompanyLocation.CountryCode}}, {{.CompanyLocation.CountryCode}}{{end}}</div>{{end}}
        {{if .CompanyContact}}<div><strong>Company Contact:</strong> {{.CompanyContact.Type}}: {{.CompanyContact.Value}}</div>{{end}}
        {{if .HoursPerWeek}}<div><strong>Hours per Week:</strong> {{.HoursPerWeek}}</div>{{end}}
        {{if .SupervisorName}}<div><strong>Supervisor:</strong> {{.SupervisorName}}{{if .SupervisorContact}} ({{.SupervisorContact}}){{end}}</div>{{end}}
        {{if .DutiesSummary}}<p class="duties"><strong>Official Duties & Responsibilities:</strong> {{.DutiesSummary}}</p>{{end}}
      </div>
      {{if .Bullets}}
      <ul class="bullet-list">
        {{range .Bullets}}
        <li>{{.Text}}</li>
        {{end}}
      </ul>
      {{end}}
    </article>
    {{end}}
  </section>
  {{end}}
  {{if .Projects}}
  <section class="resume-section projects" id="projects">
    <h2 class="section-title">Key Projects & Initiatives</h2>
    {{range .Projects}}
    <article class="entry-item project-item project-record">
      <div class="entry-header">
        <h3 class="entry-title">{{.Name}}{{if .Role}} ({{.Role}}){{end}}</h3>
        <div class="entry-meta meta"><span class="date-range">{{formatDateRange .StartDate .EndDate "Present"}}</span>{{if .URL}} | <a href="{{.URL}}">{{.URL}}</a>{{end}}</div>
      </div>
      {{if .Summary}}<p class="project-summary">{{.Summary}}</p>{{end}}
      {{if .Bullets}}
      <ul class="bullet-list">
        {{range .Bullets}}
        <li>{{.Text}}</li>
        {{end}}
      </ul>
      {{end}}
    </article>
    {{end}}
  </section>
  {{end}}
  {{if .Education}}
  <section class="resume-section education" id="education">
    <h2 class="section-title">Academic Credentials</h2>
    {{range .Education}}
    <article class="entry-item education-item edu-record">
      <div class="entry-header">
        <h3 class="entry-title">{{.Degree}}{{if .Field}} in {{.Field}}{{end}} — {{.Institution}}</h3>
        <div class="entry-meta meta"><strong>Period:</strong> <span class="date-range">{{formatDateRange .StartDate .EndDate "Present"}}</span>{{if .Score}} | <strong>Grade/Score:</strong> {{.Score}}{{end}}</div>
      </div>
      {{if .Bullets}}
      <ul class="bullet-list">
        {{range .Bullets}}
        <li>{{.Text}}</li>
        {{end}}
      </ul>
      {{end}}
    </article>
    {{end}}
  </section>
  {{end}}
  {{if .Certifications}}
  <section class="resume-section certifications" id="certifications">
    <h2 class="section-title">Professional Licenses & Certifications</h2>
    <ul class="bullet-list certifications-list">
      {{range .Certifications}}
      <li><strong>{{.Name}}</strong> — Issued by {{.Issuer}}{{if .Date}} ({{.Date}}){{end}}{{if .URL}} [<a href="{{.URL}}">{{.URL}}</a>]{{end}}</li>
      {{end}}
    </ul>
  </section>
  {{end}}
</main>
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
