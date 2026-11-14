package llm

import "fmt"

// GenerationResponseSchema returns the canonical JSON Schema for core.GenerationResponse
// (TailoredContent and GapReport), enforcing strict validation keywords and field hints.
func GenerationResponseSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"tailored_content": tailoredContentSchema(),
			"gap_report":       gapReportSchema(),
		},
		"tailored_content", "gap_report",
	)
}

// GenerationResponseSchemaJSON returns the indented JSON representation of GenerationResponseSchema().
func GenerationResponseSchemaJSON() (string, error) {
	return GenerationResponseSchema().JSON()
}

// tailoredContentSchema defines the JSON Schema for core.TailoredContent.
func tailoredContentSchema() *Schema {
	return strictObjectSchema(
		"Curated and customized resume content matching the target role, grounded strictly in the candidate's main profile",
		map[string]*Schema{
			"summary":        stringProp("Professional executive summary tailored to target role (2-4 sentences highlighting candidate strengths, key domains, and role fit)"),
			"skill_groups":   arrayProp("Categorized groups of technical and domain skills relevant to target role", tailoredSkillGroupItemSchema()),
			"experiences":    arrayProp("Chronological employment history tailored to target role", tailoredExperienceItemSchema()),
			"projects":       arrayProp("Key technical projects, open-source initiatives, or deliverables tailored to target role", tailoredProjectItemSchema()),
			"education":      arrayProp("Academic degrees, diplomas, and relevant higher education", tailoredEducationItemSchema()),
			"certifications": arrayProp("Professional certifications and technical credentials", tailoredCertificationItemSchema()),
		},
		"summary", "skill_groups", "experiences", "projects", "education", "certifications",
	)
}

// tailoredSkillGroupItemSchema defines the JSON Schema for an element of TailoredContent.SkillGroups.
func tailoredSkillGroupItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"category":         stringProp("Functional skill category title (e.g. Programming Languages, Cloud Infrastructure, Databases)"),
			"skills":           stringArrayProp("Array of candidate skill names belonging to this category"),
			"source_skill_ids": stringArrayProp("Short identifiers of cited Skills (e.g. skill-1)"),
		},
		"category", "skills",
	)
}

// tailoredBulletItemSchema defines the JSON Schema for tailored bullets citing source facts under ownerKind.
func tailoredBulletItemSchema(ownerKind, bulletDesc string) *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"text":             stringProp(bulletDesc),
			"source_fact_ids":  stringArrayProp(fmt.Sprintf("Short identifiers of Facts under this %s that justify this bullet (e.g. fact-1)", ownerKind)),
			"source_skill_ids": stringArrayProp("Short identifiers of Skills (e.g. skill-1)"),
		},
		"text", "source_fact_ids",
	)
}

// tailoredExperienceItemSchema defines the JSON Schema for an element of TailoredContent.Experiences.
func tailoredExperienceItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"source_entity_id": stringProp("Short identifier of the Main WorkExperience (e.g. exp-1)"),
			"company":          stringProp("Official employer or company name"),
			"role_title":       stringProp("Professional job title or position held"),
			"start_date":       stringProp("Start date of employment in YYYY-MM or YYYY format"),
			"end_date":         stringProp("End date of employment in YYYY-MM or YYYY format, or 'Present'"),
			"duties_summary":   stringProp("High-level summary of responsibilities and scope of the role"),
			"bullets": arrayProp(
				"Tailored accomplishment statements with metrics and impact",
				tailoredBulletItemSchema("WorkExperience", "Tailored accomplishment bullet phrased with action verb, technical context, and quantifiable results"),
			),
		},
		"source_entity_id", "company", "role_title", "start_date", "bullets",
	)
}

// tailoredProjectItemSchema defines the JSON Schema for an element of TailoredContent.Projects.
func tailoredProjectItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"source_entity_id": stringProp("Short identifier of the Main Project (e.g. proj-1)"),
			"name":             stringProp("Project title or initiative name"),
			"role":             stringProp("Role or contribution of candidate in project"),
			"url":              stringProp("Live URL, repository, or documentation link"),
			"start_date":       stringProp("Project start date in YYYY-MM or YYYY format"),
			"end_date":         stringProp("Project completion date in YYYY-MM or YYYY format, or 'Present'"),
			"summary":          stringProp("Brief synopsis of project scope and architecture"),
			"bullets": arrayProp(
				"Accomplishment statements detailing implementation and measurable impact",
				tailoredBulletItemSchema("Project", "Accomplishment bullet detailing implementation, architectural decisions, and impact"),
			),
		},
		"source_entity_id", "name", "bullets",
	)
}

// tailoredEducationItemSchema defines the JSON Schema for an element of TailoredContent.Education.
func tailoredEducationItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"source_entity_id": stringProp("Short identifier of the Main Education (e.g. edu-1)"),
			"institution":      stringProp("Name of university, college, or school"),
			"degree":           stringProp("Degree type or credential (e.g. Bachelor of Science, Master of Science, Ph.D.)"),
			"field":            stringProp("Academic major, discipline, or field of study"),
			"start_date":       stringProp("Start date in YYYY-MM or YYYY format"),
			"end_date":         stringProp("Graduation or completion date in YYYY-MM or YYYY format, or 'Expected YYYY'"),
			"score":            stringProp("Academic GPA, graduation honors, or class rank"),
		},
		"source_entity_id", "institution", "degree", "field", "start_date",
	)
}

// tailoredCertificationItemSchema defines the JSON Schema for an element of TailoredContent.Certifications.
func tailoredCertificationItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"source_entity_id": stringProp("Short identifier of the Main Certification (e.g. cert-1)"),
			"name":             stringProp("Certification or credential title"),
			"issuer":           stringProp("Issuing organization or authority (e.g. AWS, CNCF, Google Cloud)"),
			"date":             stringProp("Date obtained or renewed in YYYY-MM or YYYY format"),
			"url":              stringProp("Credential verification URL or badge link"),
		},
		"source_entity_id", "name", "issuer", "date",
	)
}
