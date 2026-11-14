package llm

// gapReportSchema defines the canonical JSON Schema for a core.GapReport object.
func gapReportSchema() *Schema {
	return strictObjectSchema(
		"Objective gap analysis evaluating candidate profile alignment with target requirements",
		map[string]*Schema{
			"overall_match_summary":     stringProp("Concise summary of alignment against target requirements"),
			"missing_requirements":      arrayProp("Specific required or preferred qualifications missing from candidate profile", gapMissingRequirementItemSchema()),
			"strategic_recommendations": stringArrayProp("Actionable strategic recommendations for tailoring and interview preparation"),
		},
		"overall_match_summary", "missing_requirements",
	)
}

// gapMissingRequirementItemSchema defines a single gap item entry within GapReport.MissingRequirements.
func gapMissingRequirementItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"requirement":  stringProp("Target requirement or qualification missing from main profile"),
			"missing_area": stringProp("Functional domain or skill area (e.g. Distributed Systems, Frontend)"),
			"importance": {
				Type:        SchemaTypes{TypeString},
				Enum:        []string{"critical", "preferred", "nice_to_have"},
				Description: "Severity rating: critical (must-have blocker), preferred (strong differentiator), or nice_to_have (bonus)",
			},
			"clarifying_prompt":  stringProp("Question to prompt user for missing experience or facts"),
			"recommended_action": stringProp("Suggested action or resume framing strategy to address this gap"),
		},
		"requirement", "missing_area", "importance", "clarifying_prompt",
	)
}

// GapReportSchema returns the canonical JSON Schema for a core.GapReport object.
func GapReportSchema() *Schema {
	return gapReportSchema()
}

// GapReportSchemaJSON returns the indented JSON representation of GapReportSchema().
func GapReportSchemaJSON() (string, error) {
	return GapReportSchema().JSON()
}

// GapAnalysisResponseSchema returns the canonical JSON Schema for a standalone GapReport wrapped under "gap_report".
func GapAnalysisResponseSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"gap_report": gapReportSchema(),
		},
		"gap_report",
	)
}

// GapAnalysisResponseSchemaJSON returns the indented JSON representation of GapAnalysisResponseSchema().
func GapAnalysisResponseSchemaJSON() (string, error) {
	return GapAnalysisResponseSchema().JSON()
}

// leanSkillItemSchema defines the JSON Schema for a single LeanSkill entry.
func leanSkillItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"id":               stringProp("Compact short identifier for citation (e.g. sk-1)"),
			"name":             stringProp("Skill name"),
			"category":         stringProp("Skill functional category"),
			"proficiency":      stringProp("Proficiency level"),
			"years_experience": numberProp("Years of experience"),
			"keywords": arrayProp(
				"Sub-skills and related keywords",
				stringProp("Keyword or sub-skill term"),
			),
		},
		"id", "name",
	)
}

// leanFactItemSchema defines the JSON Schema for a single LeanFact entry.
func leanFactItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"id":         stringProp("Compact short identifier for bullet citation (e.g. fact-1)"),
			"owner_id":   stringProp("Parent entity short identifier"),
			"owner_type": stringProp("Parent entity type"),
			"statement":  stringProp("Factual accomplishment description"),
			"impact":     stringProp("Quantifiable outcome or metric"),
			"skills":     arrayProp("Skills utilized in this factual accomplishment", leanSkillItemSchema()),
		},
		"id", "statement",
	)
}

// leanProfileHeaderSchema defines the JSON Schema for LeanMainProfile.Profile.
func leanProfileHeaderSchema() *Schema {
	return objectSchema(
		"Candidate biographical and career summary",
		map[string]*Schema{
			"headline": stringProp("Professional headline"),
			"summary":  stringProp("Candidate summary"),
		},
	)
}

// leanExperienceItemSchema defines a single entry under LeanMainProfile.Experiences.
func leanExperienceItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"id":              stringProp("Compact short identifier for entity citation (e.g. exp-1)"),
			"company":         stringProp("Employer name"),
			"role_title":      stringProp("Position title"),
			"employment_type": stringProp("Employment type"),
			"start_date":      stringProp("Employment start date"),
			"end_date":        stringProp("Employment end date"),
			"is_current":      boolProp("Current position flag"),
			"duties_summary":  stringProp("Role duties summary"),
			"facts":           arrayProp("Achievements and metrics belonging to experience", leanFactItemSchema()),
			"skills":          arrayProp("Skills utilized in experience", leanSkillItemSchema()),
		},
		"id", "company", "role_title",
	)
}

// leanProjectItemSchema defines a single entry under LeanMainProfile.Projects.
func leanProjectItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"id":         stringProp("Compact short identifier for entity citation (e.g. proj-1)"),
			"name":       stringProp("Project title"),
			"role":       stringProp("Role in project"),
			"url":        stringProp("Project URL"),
			"start_date": stringProp("Start date"),
			"end_date":   stringProp("End date"),
			"summary":    stringProp("Project summary"),
			"facts":      arrayProp("Facts under project", leanFactItemSchema()),
			"skills":     arrayProp("Skills under project", leanSkillItemSchema()),
		},
		"id", "name",
	)
}

// leanEducationItemSchema defines a single entry under LeanMainProfile.Education.
func leanEducationItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"id":          stringProp("Compact short identifier for entity citation (e.g. edu-1)"),
			"institution": stringProp("Institution name"),
			"degree":      stringProp("Degree earned"),
			"field":       stringProp("Major or field of study"),
			"start_date":  stringProp("Start date"),
			"end_date":    stringProp("Completion date"),
			"score":       stringProp("GPA or academic score"),
			"facts":       arrayProp("Facts under education", leanFactItemSchema()),
		},
		"id", "institution",
	)
}

// leanCertificationItemSchema defines a single entry under LeanMainProfile.Certifications.
func leanCertificationItemSchema() *Schema {
	return objectSchema(
		"",
		map[string]*Schema{
			"id":      stringProp("Compact short identifier for entity citation (e.g. cert-1)"),
			"name":    stringProp("Certification name"),
			"issuer":  stringProp("Issuing authority"),
			"date":    stringProp("Date certified"),
			"url":     stringProp("Verification link"),
			"summary": stringProp("Credential summary"),
		},
		"id", "name",
	)
}

// LeanMainProfileSchema returns the canonical JSON Schema for LeanMainProfile,
// exposing only LLM-relevant public career attributes and short provenance identifiers.
func LeanMainProfileSchema() *Schema {
	return objectSchema(
		"Minimal, lean main profile structure for LLM grounding with short IDs",
		map[string]*Schema{
			"profile":        leanProfileHeaderSchema(),
			"skills":         arrayProp("Candidate technical and domain competencies", leanSkillItemSchema()),
			"experiences":    arrayProp("Employment history", leanExperienceItemSchema()),
			"projects":       arrayProp("Technical and open-source projects", leanProjectItemSchema()),
			"education":      arrayProp("Academic degrees and higher education", leanEducationItemSchema()),
			"certifications": arrayProp("Professional certifications and credentials", leanCertificationItemSchema()),
			"facts":          arrayProp("Atomic factual achievements and responsibilities", leanFactItemSchema()),
		},
		"profile",
	)
}

// LeanMainProfileSchemaJSON returns the indented JSON representation of LeanMainProfileSchema().
func LeanMainProfileSchemaJSON() (string, error) {
	return LeanMainProfileSchema().JSON()
}

// ATSExtractionResponseSchema returns the canonical JSON Schema for Stage 1 ATS extraction and confidence scoring.
func ATSExtractionResponseSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"extraction_confidence_score": numberProp("Confidence score from 0 to 1 (where 1.0 is 100%) indicating how complete and unambiguously structured the extracted resume information is"),
			"extraction_summary":          stringProp("Concise explanation of ATS extraction completeness and any structural or formatting observations"),
			"extracted_candidate":         atsExtractedCandidateSchema(),
		},
		"extraction_confidence_score", "extraction_summary", "extracted_candidate",
	)
}

// atsExtractedCandidateSchema defines the JSON Schema for core.ATSExtractedCandidate.
func atsExtractedCandidateSchema() *Schema {
	return strictObjectSchema(
		"Structured candidate career profile extracted and categorized from the text-based resume",
		map[string]*Schema{
			"summary":        stringProp("Extracted professional summary or objective statement"),
			"skills":         stringArrayProp("Categorized technical, domain, and professional skills extracted from the resume"),
			"experiences":    arrayProp("Work experience entries parsed from the resume", atsExperienceItemSchema()),
			"educations":     arrayProp("Academic credentials parsed from the resume", atsEducationItemSchema()),
			"certifications": stringArrayProp("Professional certifications, licenses, or awards extracted from the resume"),
		},
		"summary", "skills", "experiences", "educations",
	)
}

// atsExperienceItemSchema defines a single entry under ATSExtractedCandidate.Experiences.
func atsExperienceItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"company":    stringProp("Employer or organization name"),
			"title":      stringProp("Job title or role held"),
			"date_range": stringProp("Employment date range as parsed from the resume"),
			"highlights": stringArrayProp("Accomplishment bullets and responsibilities extracted for this role"),
		},
		"company", "title",
	)
}

// atsEducationItemSchema defines a single entry under ATSExtractedCandidate.Educations.
func atsEducationItemSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"institution": stringProp("University, college, or school name"),
			"degree":      stringProp("Degree or qualification earned"),
			"field":       stringProp("Field of study or major"),
			"date_range":  stringProp("Graduation date or attendance range"),
		},
		"institution", "degree",
	)
}

// ATSExtractionResponseSchemaJSON returns the indented JSON representation of ATSExtractionResponseSchema().
func ATSExtractionResponseSchemaJSON() (string, error) {
	return ATSExtractionResponseSchema().JSON()
}

// ATSMatchingResponseSchema returns the canonical JSON Schema for Stage 2 ATS job matching evaluation.
func ATSMatchingResponseSchema() *Schema {
	return strictObjectSchema(
		"",
		map[string]*Schema{
			"job_matching_score": numberProp("Perceived matching score from 0 to 1 (where 1.0 is 100%) evaluating how well the extracted candidate information matches the job description"),
			"matching_summary":   stringProp("Concise explanation of how the extracted candidate profile aligns with the target job description requirements"),
		},
		"job_matching_score", "matching_summary",
	)
}

// ATSMatchingResponseSchemaJSON returns the indented JSON representation of ATSMatchingResponseSchema().
func ATSMatchingResponseSchemaJSON() (string, error) {
	return ATSMatchingResponseSchema().JSON()
}
