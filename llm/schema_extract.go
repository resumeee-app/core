package llm

// ResumeExtractResponseSchema returns the canonical JSON Schema for extracting unstructured resume text
// into standard JSON Resume format (v1.0.0) with detailed descriptions and formatting hints.
func ResumeExtractResponseSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"basics":       extractBasicsSchema(),
			"work":         arrayProp("Chronological employment history and positions held", extractWorkItemSchema()),
			"volunteer":    arrayProp("Volunteer experience and community service", extractVolunteerItemSchema()),
			"education":    arrayProp("Academic degrees and higher education history", extractEducationItemSchema()),
			"awards":       arrayProp("Honors, awards, and industry recognitions", extractAwardItemSchema()),
			"certificates": arrayProp("Professional certifications and technical credentials", extractCertificateItemSchema()),
			"publications": arrayProp("Academic papers, journal articles, books, or blog posts", extractPublicationItemSchema()),
			"skills":       arrayProp("Technical, functional, and domain competencies", extractSkillItemSchema()),
			"languages":    arrayProp("Spoken and written natural languages", extractLanguageItemSchema()),
			"interests":    arrayProp("Personal interests, hobbies, or extracurricular pursuits", extractInterestItemSchema()),
			"references":   arrayProp("Professional references or recommendations", extractReferenceItemSchema()),
			"projects":     arrayProp("Independent, open-source, or academic projects", extractProjectItemSchema()),
		},
		"basics", "work", "volunteer", "education", "awards", "certificates", "publications", "skills", "languages", "interests", "references", "projects",
	)
}

// ResumeExtractResponseSchemaJSON returns the indented JSON representation of ResumeExtractResponseSchema().
func ResumeExtractResponseSchemaJSON() (string, error) {
	return ResumeExtractResponseSchema().JSON()
}

// extractBasicsSchema defines the JSON Resume basics object schema.
func extractBasicsSchema() *Schema {
	return strictObjectSchema(
		"Personal contact details, headline, and profile links",
		map[string]*Schema{
			"name":     stringProp("Candidate's full legal or preferred name"),
			"label":    stringProp("Professional title or headline (e.g. Senior Software Engineer)"),
			"image":    stringProp("URL to a profile photo"),
			"email":    stringProp("Contact email address"),
			"phone":    stringProp("Primary contact phone number"),
			"url":      stringProp("Personal website or portfolio URL"),
			"summary":  stringProp("Professional biographical summary or objective"),
			"location": extractLocationSchema(),
			"profiles": arrayProp("Social and developer profiles (e.g. LinkedIn, GitHub)", extractProfileLinkItemSchema()),
		},
		"name", "summary", "profiles",
	)
}

// extractLocationSchema defines the JSON Resume basics.location object schema.
func extractLocationSchema() *Schema {
	return strictObjectSchema(
		"Postal and geographic residence information",
		map[string]*Schema{
			"address":     stringProp("Street address line"),
			"postalCode":  stringProp("Postal or ZIP code"),
			"city":        stringProp("City of residence"),
			"countryCode": stringProp("ISO 3166-1 alpha-2 two-letter country code (e.g. US)"),
			"region":      stringProp("State, province, or canton"),
		},
	)
}

// extractProfileLinkItemSchema defines a single entry under basics.profiles.
func extractProfileLinkItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"network":  stringProp("Platform or network name (e.g. LinkedIn, GitHub, Twitter)"),
			"username": stringProp("Profile username or handle"),
			"url":      stringProp("Direct URL to user profile page"),
		},
		"network",
	)
}

// extractWorkItemSchema defines a single entry under work[].
func extractWorkItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"name":       stringProp("Company or employer name"),
			"company":    stringProp("Company or employer name (synonym for name)"),
			"position":   stringProp("Job title or position held"),
			"url":        stringProp("Company website or employer URL"),
			"startDate":  stringProp("Start date of employment (YYYY-MM-DD, YYYY-MM, or YYYY)"),
			"endDate":    stringProp("End date of employment (YYYY-MM-DD, YYYY-MM, YYYY, or 'Present')"),
			"summary":    stringProp("Concise overview of role scope and context; place individual accomplishments, responsibilities, and metrics in highlights rather than merging them here"),
			"highlights": stringArrayProp("Every individual bullet point, accomplishment, responsibility, and quantified metric as discrete list items separate from summary (return [] only if none exist in source text)"),
			"location":   stringProp("Location of employment (e.g. City, State or Remote)"),
		},
		"name", "position", "summary", "highlights",
	)
}

// extractVolunteerItemSchema defines a single entry under volunteer[].
func extractVolunteerItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"organization": stringProp("Volunteer organization or non-profit name"),
			"position":     stringProp("Role or title held within organization"),
			"url":          stringProp("Organization website link"),
			"startDate":    stringProp("Start date of service (YYYY-MM-DD or YYYY-MM)"),
			"endDate":      stringProp("End date of service or 'Present'"),
			"summary":      stringProp("Concise overview of volunteer role and context; place individual achievements, initiatives, and bullet points in highlights rather than merging them here"),
			"highlights":   stringArrayProp("Key achievements, initiatives, impact metrics, and contributions as discrete list items separate from summary (return [] only if none exist in source text)"),
		},
		"organization", "position", "summary", "highlights",
	)
}

// extractEducationItemSchema defines a single entry under education[].
func extractEducationItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"institution": stringProp("School, college, or university name"),
			"url":         stringProp("Institution website link"),
			"area":        stringProp("Academic field of study or major (e.g. Computer Science)"),
			"studyType":   stringProp("Degree type or credential earned (e.g. B.S., M.S., Ph.D.)"),
			"startDate":   stringProp("Start date of matriculation (YYYY-MM or YYYY)"),
			"endDate":     stringProp("Graduation or completion date (YYYY-MM, YYYY, or 'Expected YYYY')"),
			"score":       stringProp("Grade point average (GPA) or class rank; place honors, scholarships, and thesis or research bullets in highlights"),
			"courses":     stringArrayProp("Relevant completed coursework titles (return [] only if none exist in source text)"),
			"highlights":  stringArrayProp("Academic honors, distinctions, scholarships, thesis or research bullets, leadership roles, and notable accomplishments as discrete list items (return [] only if none exist in source text)"),
		},
		"institution", "courses", "highlights",
	)
}

// extractAwardItemSchema defines a single entry under awards[].
func extractAwardItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"title":   stringProp("Award or honor title"),
			"date":    stringProp("Date received (YYYY-MM or YYYY)"),
			"awarder": stringProp("Organization or entity presenting the award"),
			"summary": stringProp("Description of why the award was conferred"),
		},
		"title",
	)
}

// extractCertificateItemSchema defines a single entry under certificates[].
func extractCertificateItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"name":   stringProp("Certification or credential title"),
			"issuer": stringProp("Accreditation body or issuer (e.g. AWS, CNCF, Google)"),
			"date":   stringProp("Date certified or issued (YYYY-MM or YYYY)"),
			"url":    stringProp("Verification link or badge URL"),
		},
		"name",
	)
}

// extractPublicationItemSchema defines a single entry under publications[].
func extractPublicationItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"name":        stringProp("Publication or article title"),
			"publisher":   stringProp("Publisher, journal, conference, or platform name"),
			"releaseDate": stringProp("Date published (YYYY-MM-DD, YYYY-MM, or YYYY)"),
			"url":         stringProp("Link to publication or DOI"),
			"summary":     stringProp("Abstract or synopsis of published work"),
		},
		"name",
	)
}

// extractSkillItemSchema defines a single entry under skills[].
func extractSkillItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"name":     stringProp("Skill name or technology title"),
			"level":    stringProp("Proficiency level (e.g. Expert, Intermediate, Beginner)"),
			"keywords": stringArrayProp("Sub-skills, tools, or related technical concepts (return [] only if none exist in source text)"),
		},
		"name", "keywords",
	)
}

// extractLanguageItemSchema defines a single entry under languages[].
func extractLanguageItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"language": stringProp("Natural language name (e.g. English, Spanish, Japanese)"),
			"fluency":  stringProp("Fluency level (e.g. Native, Fluent, Professional Working)"),
		},
		"language",
	)
}

// extractInterestItemSchema defines a single entry under interests[].
func extractInterestItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"name":     stringProp("Interest or hobby category"),
			"keywords": stringArrayProp("Associated topics or specific activities (return [] only if none exist in source text)"),
		},
		"name", "keywords",
	)
}

// extractReferenceItemSchema defines a single entry under references[].
func extractReferenceItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"name":      stringProp("Reference person name"),
			"reference": stringProp("Reference statement or testimonial"),
		},
		"name",
	)
}

// extractProjectItemSchema defines a single entry under projects[].
func extractProjectItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"name":        stringProp("Project title or repository name"),
			"description": stringProp("Concise synopsis of project architecture and purpose; place individual achievements, metrics, and technical milestones in highlights rather than merging them here"),
			"highlights":  stringArrayProp("Key achievements, metrics, features delivered, or technical milestones as discrete list items separate from description (return [] only if none exist in source text)"),
			"keywords":    stringArrayProp("Technologies and libraries used in project (return [] only if none exist in source text)"),
			"startDate":   stringProp("Project start date (YYYY-MM or YYYY)"),
			"endDate":     stringProp("Project completion date (YYYY-MM, YYYY, or 'Present')"),
			"url":         stringProp("Live URL or repository link"),
			"roles":       stringArrayProp("Functional roles performed on project"),
			"entity":      stringProp("Organization or entity affiliated with project"),
			"type":        stringProp("Project classification (e.g. application, library, open-source)"),
		},
		"name", "description", "highlights", "keywords",
	)
}
