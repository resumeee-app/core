package llm

// ResumeExtractResponseSchema returns the canonical JSON Schema for extracting unstructured resume text
// into standard JSON Resume format (v1.0.0) with detailed descriptions and formatting hints.
func ResumeExtractResponseSchema() *Schema {
	return objectSchema(
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
	)
}

// ResumeExtractResponseSchemaJSON returns the indented JSON representation of ResumeExtractResponseSchema().
func ResumeExtractResponseSchemaJSON() (string, error) {
	return ResumeExtractResponseSchema().JSON()
}

// extractBasicsSchema defines the JSON Resume basics object schema.
func extractBasicsSchema() *Schema {
	return objectSchema(
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
		"name",
	)
}

// extractLocationSchema defines the JSON Resume basics.location object schema.
func extractLocationSchema() *Schema {
	return objectSchema(
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
	return objectSchema(
		"",
		map[string]*Schema{
			"network":  stringProp("Platform or network name (e.g. LinkedIn, GitHub, Twitter)"),
			"username": stringProp("Profile username or handle"),
			"url":      stringProp("Direct URL to user profile page"),
		},
	)
}

// extractWorkItemSchema defines a single entry under work[].
func extractWorkItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"name":       stringProp("Company or employer name"),
			"company":    stringProp("Company or employer name (synonym for name)"),
			"position":   stringProp("Job title or position held"),
			"url":        stringProp("Company website or employer URL"),
			"startDate":  stringProp("Start date of employment (YYYY-MM-DD, YYYY-MM, or YYYY)"),
			"endDate":    stringProp("End date of employment (YYYY-MM-DD, YYYY-MM, YYYY, or 'Present')"),
			"summary":    stringProp("Summary of role duties and scope"),
			"highlights": stringArrayProp("Bulleted accomplishment statements and contributions"),
			"location":   stringProp("Location of employment (e.g. City, State or Remote)"),
		},
	)
}

// extractVolunteerItemSchema defines a single entry under volunteer[].
func extractVolunteerItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"organization": stringProp("Volunteer organization or non-profit name"),
			"position":     stringProp("Role or title held within organization"),
			"url":          stringProp("Organization website link"),
			"startDate":    stringProp("Start date of service (YYYY-MM-DD or YYYY-MM)"),
			"endDate":      stringProp("End date of service or 'Present'"),
			"summary":      stringProp("Summary of volunteer activities"),
			"highlights":   stringArrayProp("Key achievements and contributions"),
		},
	)
}

// extractEducationItemSchema defines a single entry under education[].
func extractEducationItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"institution": stringProp("School, college, or university name"),
			"url":         stringProp("Institution website link"),
			"area":        stringProp("Academic field of study or major (e.g. Computer Science)"),
			"studyType":   stringProp("Degree type or credential earned (e.g. B.S., M.S., Ph.D.)"),
			"startDate":   stringProp("Start date of matriculation (YYYY-MM or YYYY)"),
			"endDate":     stringProp("Graduation or completion date (YYYY-MM, YYYY, or 'Expected YYYY')"),
			"score":       stringProp("Grade point average (GPA), honors, or distinction"),
			"courses":     stringArrayProp("Relevant completed coursework titles"),
		},
	)
}

// extractAwardItemSchema defines a single entry under awards[].
func extractAwardItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"title":   stringProp("Award or honor title"),
			"date":    stringProp("Date received (YYYY-MM or YYYY)"),
			"awarder": stringProp("Organization or entity presenting the award"),
			"summary": stringProp("Description of why the award was conferred"),
		},
	)
}

// extractCertificateItemSchema defines a single entry under certificates[].
func extractCertificateItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"name":   stringProp("Certification or credential title"),
			"issuer": stringProp("Accreditation body or issuer (e.g. AWS, CNCF, Google)"),
			"date":   stringProp("Date certified or issued (YYYY-MM or YYYY)"),
			"url":    stringProp("Verification link or badge URL"),
		},
	)
}

// extractPublicationItemSchema defines a single entry under publications[].
func extractPublicationItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"name":        stringProp("Publication or article title"),
			"publisher":   stringProp("Publisher, journal, conference, or platform name"),
			"releaseDate": stringProp("Date published (YYYY-MM-DD, YYYY-MM, or YYYY)"),
			"url":         stringProp("Link to publication or DOI"),
			"summary":     stringProp("Abstract or synopsis of published work"),
		},
	)
}

// extractSkillItemSchema defines a single entry under skills[].
func extractSkillItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"name":     stringProp("Skill name or technology title"),
			"level":    stringProp("Proficiency level (e.g. Expert, Intermediate, Beginner)"),
			"keywords": stringArrayProp("Sub-skills, tools, or related technical concepts"),
		},
	)
}

// extractLanguageItemSchema defines a single entry under languages[].
func extractLanguageItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"language": stringProp("Natural language name (e.g. English, Spanish, Japanese)"),
			"fluency":  stringProp("Fluency level (e.g. Native, Fluent, Professional Working)"),
		},
	)
}

// extractInterestItemSchema defines a single entry under interests[].
func extractInterestItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"name":     stringProp("Interest or hobby category"),
			"keywords": stringArrayProp("Associated topics or specific activities"),
		},
	)
}

// extractReferenceItemSchema defines a single entry under references[].
func extractReferenceItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"name":      stringProp("Reference person name"),
			"reference": stringProp("Reference statement or testimonial"),
		},
	)
}

// extractProjectItemSchema defines a single entry under projects[].
func extractProjectItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"name":        stringProp("Project title or repository name"),
			"description": stringProp("Synopsis of project architecture and purpose"),
			"highlights":  stringArrayProp("Key achievements, metrics, or technical milestones"),
			"keywords":    stringArrayProp("Technologies and libraries used in project"),
			"startDate":   stringProp("Project start date (YYYY-MM or YYYY)"),
			"endDate":     stringProp("Project completion date (YYYY-MM, YYYY, or 'Present')"),
			"url":         stringProp("Live URL or repository link"),
			"roles":       stringArrayProp("Functional roles performed on project"),
			"entity":      stringProp("Organization or entity affiliated with project"),
			"type":        stringProp("Project classification (e.g. application, library, open-source)"),
		},
	)
}
