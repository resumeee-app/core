package llm

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	codeFenceRegex = regexp.MustCompile("(?s)```(?:json|JSON)?\\s*([\\s\\S]*?)\\s*```")
)

// ExtractJSON extracts raw JSON from conversational wrappers, markdown fences, or raw text.
func ExtractJSON(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}

	// 1. Look for markdown code fence
	if matches := codeFenceRegex.FindStringSubmatch(trimmed); len(matches) > 1 {
		inner := strings.TrimSpace(matches[1])
		firstObj := strings.Index(inner, "{")
		lastObj := strings.LastIndex(inner, "}")
		if firstObj != -1 && lastObj > firstObj {
			return inner[firstObj : lastObj+1]
		}
		firstArr := strings.Index(inner, "[")
		lastArr := strings.LastIndex(inner, "]")
		if firstArr != -1 && lastArr > firstArr {
			return inner[firstArr : lastArr+1]
		}
		return inner
	}

	// 2. Greedy search for outermost matching JSON braces without fences
	firstBrace := strings.Index(trimmed, "{")
	lastBrace := strings.LastIndex(trimmed, "}")
	if firstBrace != -1 && lastBrace > firstBrace {
		return trimmed[firstBrace : lastBrace+1]
	}

	firstBracket := strings.Index(trimmed, "[")
	lastBracket := strings.LastIndex(trimmed, "]")
	if firstBracket != -1 && lastBracket > firstBracket {
		return trimmed[firstBracket : lastBracket+1]
	}

	return trimmed
}

// GenerationResponseSchema returns the canonical JSON Schema for core.GenerationResponse
// (TailoredContent and GapReport), enforcing strict validation keywords (required, additionalProperties: false)
// with comprehensive field descriptions and hints to guide accurate LLM token generation.
//
// Returns:
//   - A map[string]any representing the JSON Schema draft-07 compatible structure for structured LLM decoding.
func GenerationResponseSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tailored_content": map[string]any{
				"type":        "object",
				"description": "Curated and customized resume content matching the target role, grounded strictly in the candidate's main profile",
				"properties": map[string]any{
					"summary": map[string]any{
						"type":        "string",
						"description": "Professional executive summary tailored to target role (2-4 sentences highlighting candidate strengths, key domains, and role fit)",
					},
					"skill_groups": map[string]any{
						"type":        "array",
						"description": "Categorized groups of technical and domain skills relevant to target role",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"category": map[string]any{
									"type":        "string",
									"description": "Functional skill category title (e.g. Programming Languages, Cloud Infrastructure, Databases)",
								},
								"skills": map[string]any{
									"type":        "array",
									"items":       map[string]any{"type": "string"},
									"description": "Array of candidate skill names belonging to this category",
								},
								"source_skill_ids": map[string]any{
									"type":        "array",
									"items":       map[string]any{"type": "string"},
									"description": "Short identifiers of cited Skills (e.g. skill-1)",
								},
							},
							"required":             []string{"category", "skills"},
							"additionalProperties": false,
						},
					},
					"experiences": map[string]any{
						"type":        "array",
						"description": "Chronological employment history tailored to target role",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"source_entity_id": map[string]any{
									"type":        "string",
									"description": "Short identifier of the Main WorkExperience (e.g. exp-1)",
								},
								"company": map[string]any{
									"type":        "string",
									"description": "Official employer or company name",
								},
								"role_title": map[string]any{
									"type":        "string",
									"description": "Professional job title or position held",
								},
								"start_date": map[string]any{
									"type":        "string",
									"description": "Start date of employment in YYYY-MM or YYYY format",
								},
								"end_date": map[string]any{
									"type":        "string",
									"description": "End date of employment in YYYY-MM or YYYY format, or 'Present'",
								},
								"duties_summary": map[string]any{
									"type":        "string",
									"description": "High-level summary of responsibilities and scope of the role",
								},
								"bullets": map[string]any{
									"type":        "array",
									"description": "Tailored accomplishment statements with metrics and impact",
									"items": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"text": map[string]any{
												"type":        "string",
												"description": "Tailored accomplishment bullet phrased with action verb, technical context, and quantifiable results",
											},
											"source_fact_ids": map[string]any{
												"type":        "array",
												"items":       map[string]any{"type": "string"},
												"description": "Short identifiers of Facts under this WorkExperience that justify this bullet (e.g. fact-1)",
											},
											"source_skill_ids": map[string]any{
												"type":        "array",
												"items":       map[string]any{"type": "string"},
												"description": "Short identifiers of Skills (e.g. skill-1)",
											},
										},
										"required":             []string{"text", "source_fact_ids"},
										"additionalProperties": false,
									},
								},
							},
							"required":             []string{"source_entity_id", "company", "role_title", "start_date", "bullets"},
							"additionalProperties": false,
						},
					},
					"projects": map[string]any{
						"type":        "array",
						"description": "Key technical projects, open-source initiatives, or deliverables tailored to target role",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"source_entity_id": map[string]any{
									"type":        "string",
									"description": "Short identifier of the Main Project (e.g. proj-1)",
								},
								"name": map[string]any{
									"type":        "string",
									"description": "Project title or initiative name",
								},
								"role": map[string]any{
									"type":        "string",
									"description": "Role or contribution of candidate in project",
								},
								"url": map[string]any{
									"type":        "string",
									"description": "Live URL, repository, or documentation link",
								},
								"start_date": map[string]any{
									"type":        "string",
									"description": "Project start date in YYYY-MM or YYYY format",
								},
								"end_date": map[string]any{
									"type":        "string",
									"description": "Project completion date in YYYY-MM or YYYY format, or 'Present'",
								},
								"summary": map[string]any{
									"type":        "string",
									"description": "Brief synopsis of project scope and architecture",
								},
								"bullets": map[string]any{
									"type":        "array",
									"description": "Accomplishment statements detailing implementation and measurable impact",
									"items": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"text": map[string]any{
												"type":        "string",
												"description": "Accomplishment bullet detailing implementation, architectural decisions, and impact",
											},
											"source_fact_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Short identifiers of Facts under this Project (e.g. fact-1)"},
											"source_skill_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Short identifiers of Skills (e.g. skill-1)"},
										},
										"required":             []string{"text", "source_fact_ids"},
										"additionalProperties": false,
									},
								},
							},
							"required":             []string{"source_entity_id", "name", "bullets"},
							"additionalProperties": false,
						},
					},
					"education": map[string]any{
						"type":        "array",
						"description": "Academic degrees, diplomas, and relevant higher education",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"source_entity_id": map[string]any{
									"type":        "string",
									"description": "Short identifier of the Main Education (e.g. edu-1)",
								},
								"institution": map[string]any{
									"type":        "string",
									"description": "Name of university, college, or school",
								},
								"degree": map[string]any{
									"type":        "string",
									"description": "Degree type or credential (e.g. Bachelor of Science, Master of Science, Ph.D.)",
								},
								"field": map[string]any{
									"type":        "string",
									"description": "Academic major, discipline, or field of study",
								},
								"start_date": map[string]any{
									"type":        "string",
									"description": "Start date in YYYY-MM or YYYY format",
								},
								"end_date": map[string]any{
									"type":        "string",
									"description": "Graduation or completion date in YYYY-MM or YYYY format, or 'Expected YYYY'",
								},
								"score": map[string]any{
									"type":        "string",
									"description": "Academic GPA, graduation honors, or class rank",
								},
							},
							"required":             []string{"source_entity_id", "institution", "degree", "field", "start_date"},
							"additionalProperties": false,
						},
					},
					"certifications": map[string]any{
						"type":        "array",
						"description": "Professional certifications and technical credentials",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"source_entity_id": map[string]any{
									"type":        "string",
									"description": "Short identifier of the Main Certification (e.g. cert-1)",
								},
								"name": map[string]any{
									"type":        "string",
									"description": "Certification or credential title",
								},
								"issuer": map[string]any{
									"type":        "string",
									"description": "Issuing organization or authority (e.g. AWS, CNCF, Google Cloud)",
								},
								"date": map[string]any{
									"type":        "string",
									"description": "Date obtained or renewed in YYYY-MM or YYYY format",
								},
								"url": map[string]any{
									"type":        "string",
									"description": "Credential verification URL or badge link",
								},
							},
							"required":             []string{"source_entity_id", "name", "issuer", "date"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"summary", "skill_groups", "experiences", "projects", "education", "certifications"},
				"additionalProperties": false,
			},
			"gap_report": gapReportSchema(),
		},
		"required":             []string{"tailored_content", "gap_report"},
		"additionalProperties": false,
	}
}

// gapReportSchema returns the canonical JSON Schema for a core.GapReport object,
// enforcing strict validation keywords, enum constraints, and comprehensive field descriptions.
func gapReportSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "Objective gap analysis evaluating candidate profile alignment with target requirements",
		"properties": map[string]any{
			"overall_match_summary": map[string]any{
				"type":        "string",
				"description": "Concise summary of alignment against target requirements",
			},
			"missing_requirements": map[string]any{
				"type":        "array",
				"description": "Specific required or preferred qualifications missing from candidate profile",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"requirement": map[string]any{
							"type":        "string",
							"description": "Target requirement or qualification missing from main profile",
						},
						"missing_area": map[string]any{
							"type":        "string",
							"description": "Functional domain or skill area (e.g. Distributed Systems, Frontend)",
						},
						"importance": map[string]any{
							"type":        "string",
							"enum":        []string{"critical", "preferred", "nice_to_have"},
							"description": "Severity rating: critical (must-have blocker), preferred (strong differentiator), or nice_to_have (bonus)",
						},
						"clarifying_prompt": map[string]any{
							"type":        "string",
							"description": "Question to prompt user for missing experience or facts",
						},
						"recommended_action": map[string]any{
							"type":        "string",
							"description": "Suggested action or resume framing strategy to address this gap",
						},
					},
					"required":             []string{"requirement", "missing_area", "importance", "clarifying_prompt"},
					"additionalProperties": false,
				},
			},
			"strategic_recommendations": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "Actionable strategic recommendations for tailoring and interview preparation",
			},
		},
		"required":             []string{"overall_match_summary", "missing_requirements"},
		"additionalProperties": false,
	}
}

// GapReportSchema returns the canonical JSON Schema for a core.GapReport object,
// enforcing strict validation keywords, enum constraints, and comprehensive field descriptions.
func GapReportSchema() map[string]any {
	return gapReportSchema()
}

// GapAnalysisResponseSchema returns the canonical JSON Schema for a standalone GapReport,
// enforcing strict validation keywords and wrapping the report under the "gap_report" key.
func GapAnalysisResponseSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"gap_report": gapReportSchema(),
		},
		"required":             []string{"gap_report"},
		"additionalProperties": false,
	}
}

// ResumeExtractResponseSchema returns the canonical JSON Schema for extracting unstructured resume text
// into standard JSON Resume format (v1.0.0) with detailed descriptions and formatting hints.
func ResumeExtractResponseSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"basics": map[string]any{
				"type":        "object",
				"description": "Personal contact details, headline, and profile links",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Candidate's full legal or preferred name",
					},
					"label": map[string]any{
						"type":        "string",
						"description": "Professional title or headline (e.g. Senior Software Engineer)",
					},
					"image": map[string]any{
						"type":        "string",
						"description": "URL to a profile photo",
					},
					"email": map[string]any{
						"type":        "string",
						"description": "Contact email address",
					},
					"phone": map[string]any{
						"type":        "string",
						"description": "Primary contact phone number",
					},
					"url": map[string]any{
						"type":        "string",
						"description": "Personal website or portfolio URL",
					},
					"summary": map[string]any{
						"type":        "string",
						"description": "Professional biographical summary or objective",
					},
					"location": map[string]any{
						"type":        "object",
						"description": "Postal and geographic residence information",
						"properties": map[string]any{
							"address": map[string]any{
								"type":        "string",
								"description": "Street address line",
							},
							"postalCode": map[string]any{
								"type":        "string",
								"description": "Postal or ZIP code",
							},
							"city": map[string]any{
								"type":        "string",
								"description": "City of residence",
							},
							"countryCode": map[string]any{
								"type":        "string",
								"description": "ISO 3166-1 alpha-2 two-letter country code (e.g. US)",
							},
							"region": map[string]any{
								"type":        "string",
								"description": "State, province, or canton",
							},
						},
					},
					"profiles": map[string]any{
						"type":        "array",
						"description": "Social and developer profiles (e.g. LinkedIn, GitHub)",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"network": map[string]any{
									"type":        "string",
									"description": "Platform or network name (e.g. LinkedIn, GitHub, Twitter)",
								},
								"username": map[string]any{
									"type":        "string",
									"description": "Profile username or handle",
								},
								"url": map[string]any{
									"type":        "string",
									"description": "Direct URL to user profile page",
								},
							},
						},
					},
				},
				"required": []string{"name"},
			},
			"work": map[string]any{
				"type":        "array",
				"description": "Chronological employment history and positions held",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "Company or employer name",
						},
						"company": map[string]any{
							"type":        "string",
							"description": "Company or employer name (synonym for name)",
						},
						"position": map[string]any{
							"type":        "string",
							"description": "Job title or position held",
						},
						"url": map[string]any{
							"type":        "string",
							"description": "Company website or employer URL",
						},
						"startDate": map[string]any{
							"type":        "string",
							"description": "Start date of employment (YYYY-MM-DD, YYYY-MM, or YYYY)",
						},
						"endDate": map[string]any{
							"type":        "string",
							"description": "End date of employment (YYYY-MM-DD, YYYY-MM, YYYY, or 'Present')",
						},
						"summary": map[string]any{
							"type":        "string",
							"description": "Summary of role duties and scope",
						},
						"highlights": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Bulleted accomplishment statements and contributions",
						},
						"location": map[string]any{
							"type":        "string",
							"description": "Location of employment (e.g. City, State or Remote)",
						},
					},
				},
			},
			"volunteer": map[string]any{
				"type":        "array",
				"description": "Volunteer experience and community service",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"organization": map[string]any{
							"type":        "string",
							"description": "Volunteer organization or non-profit name",
						},
						"position": map[string]any{
							"type":        "string",
							"description": "Role or title held within organization",
						},
						"url": map[string]any{
							"type":        "string",
							"description": "Organization website link",
						},
						"startDate": map[string]any{
							"type":        "string",
							"description": "Start date of service (YYYY-MM-DD or YYYY-MM)",
						},
						"endDate": map[string]any{
							"type":        "string",
							"description": "End date of service or 'Present'",
						},
						"summary": map[string]any{
							"type":        "string",
							"description": "Summary of volunteer activities",
						},
						"highlights": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Key achievements and contributions",
						},
					},
				},
			},
			"education": map[string]any{
				"type":        "array",
				"description": "Academic degrees and higher education history",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"institution": map[string]any{
							"type":        "string",
							"description": "School, college, or university name",
						},
						"url": map[string]any{
							"type":        "string",
							"description": "Institution website link",
						},
						"area": map[string]any{
							"type":        "string",
							"description": "Academic field of study or major (e.g. Computer Science)",
						},
						"studyType": map[string]any{
							"type":        "string",
							"description": "Degree type or credential earned (e.g. B.S., M.S., Ph.D.)",
						},
						"startDate": map[string]any{
							"type":        "string",
							"description": "Start date of matriculation (YYYY-MM or YYYY)",
						},
						"endDate": map[string]any{
							"type":        "string",
							"description": "Graduation or completion date (YYYY-MM, YYYY, or 'Expected YYYY')",
						},
						"score": map[string]any{
							"type":        "string",
							"description": "Grade point average (GPA), honors, or distinction",
						},
						"courses": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Relevant completed coursework titles",
						},
					},
				},
			},
			"awards": map[string]any{
				"type":        "array",
				"description": "Honors, awards, and industry recognitions",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title": map[string]any{
							"type":        "string",
							"description": "Award or honor title",
						},
						"date": map[string]any{
							"type":        "string",
							"description": "Date received (YYYY-MM or YYYY)",
						},
						"awarder": map[string]any{
							"type":        "string",
							"description": "Organization or entity presenting the award",
						},
						"summary": map[string]any{
							"type":        "string",
							"description": "Description of why the award was conferred",
						},
					},
				},
			},
			"certificates": map[string]any{
				"type":        "array",
				"description": "Professional certifications and technical credentials",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "Certification or credential title",
						},
						"issuer": map[string]any{
							"type":        "string",
							"description": "Accreditation body or issuer (e.g. AWS, CNCF, Google)",
						},
						"date": map[string]any{
							"type":        "string",
							"description": "Date certified or issued (YYYY-MM or YYYY)",
						},
						"url": map[string]any{
							"type":        "string",
							"description": "Verification link or badge URL",
						},
					},
				},
			},
			"publications": map[string]any{
				"type":        "array",
				"description": "Academic papers, journal articles, books, or blog posts",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "Publication or article title",
						},
						"publisher": map[string]any{
							"type":        "string",
							"description": "Publisher, journal, conference, or platform name",
						},
						"releaseDate": map[string]any{
							"type":        "string",
							"description": "Date published (YYYY-MM-DD, YYYY-MM, or YYYY)",
						},
						"url": map[string]any{
							"type":        "string",
							"description": "Link to publication or DOI",
						},
						"summary": map[string]any{
							"type":        "string",
							"description": "Abstract or synopsis of published work",
						},
					},
				},
			},
			"skills": map[string]any{
				"type":        "array",
				"description": "Technical, functional, and domain competencies",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "Skill name or technology title",
						},
						"level": map[string]any{
							"type":        "string",
							"description": "Proficiency level (e.g. Expert, Intermediate, Beginner)",
						},
						"keywords": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Sub-skills, tools, or related technical concepts",
						},
					},
				},
			},
			"languages": map[string]any{
				"type":        "array",
				"description": "Spoken and written natural languages",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"language": map[string]any{
							"type":        "string",
							"description": "Natural language name (e.g. English, Spanish, Japanese)",
						},
						"fluency": map[string]any{
							"type":        "string",
							"description": "Fluency level (e.g. Native, Fluent, Professional Working)",
						},
					},
				},
			},
			"interests": map[string]any{
				"type":        "array",
				"description": "Personal interests, hobbies, or extracurricular pursuits",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "Interest or hobby category",
						},
						"keywords": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Associated topics or specific activities",
						},
					},
				},
			},
			"references": map[string]any{
				"type":        "array",
				"description": "Professional references or recommendations",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "Reference person name",
						},
						"reference": map[string]any{
							"type":        "string",
							"description": "Reference statement or testimonial",
						},
					},
				},
			},
			"projects": map[string]any{
				"type":        "array",
				"description": "Independent, open-source, or academic projects",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "Project title or repository name",
						},
						"description": map[string]any{
							"type":        "string",
							"description": "Synopsis of project architecture and purpose",
						},
						"highlights": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Key achievements, metrics, or technical milestones",
						},
						"keywords": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Technologies and libraries used in project",
						},
						"startDate": map[string]any{
							"type":        "string",
							"description": "Project start date (YYYY-MM or YYYY)",
						},
						"endDate": map[string]any{
							"type":        "string",
							"description": "Project completion date (YYYY-MM, YYYY, or 'Present')",
						},
						"url": map[string]any{
							"type":        "string",
							"description": "Live URL or repository link",
						},
						"roles": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Functional roles performed on project",
						},
						"entity": map[string]any{
							"type":        "string",
							"description": "Organization or entity affiliated with project",
						},
						"type": map[string]any{
							"type":        "string",
							"description": "Project classification (e.g. application, library, open-source)",
						},
					},
				},
			},
		},
	}
}

// SanitizeGeminiSchema creates a deep clone of the provided schema map and recursively strips
// unsupported keywords (such as "additionalProperties", "$schema", "$defs", "definitions", "title")
// while preserving OpenAPI 3.0 fields supported by the Google Generative Language API.
func SanitizeGeminiSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	res, ok := sanitizeNode(schema).(map[string]any)
	if !ok {
		return nil
	}
	return res
}

// sanitizeNode recursively traverses and sanitizes maps, slices, and primitive values.
func sanitizeNode(val any) any {
	switch v := val.(type) {
	case map[string]any:
		cleaned := make(map[string]any, len(v))
		for k, child := range v {
			switch k {
			case "additionalProperties", "$schema", "$defs", "definitions", "title":
				continue
			default:
				cleaned[k] = sanitizeNode(child)
			}
		}
		return cleaned
	case []any:
		cleaned := make([]any, len(v))
		for i, child := range v {
			cleaned[i] = sanitizeNode(child)
		}
		return cleaned
	case []string:
		cleaned := make([]string, len(v))
		copy(cleaned, v)
		return cleaned
	default:
		return v
	}
}

// GeminiResponseSchema returns a Google Gemini-compatible JSON schema for core.GenerationResponse
// by stripping additionalProperties and OpenAPI extensions unsupported by Gemini.
func GeminiResponseSchema() map[string]any {
	return SanitizeGeminiSchema(GenerationResponseSchema())
}

// GenerationResponseSchemaJSON returns the indented JSON representation of GenerationResponseSchema().
func GenerationResponseSchemaJSON() (string, error) {
	b, err := json.MarshalIndent(GenerationResponseSchema(), "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// GapAnalysisResponseSchemaJSON returns the indented JSON representation of GapAnalysisResponseSchema().
func GapAnalysisResponseSchemaJSON() (string, error) {
	b, err := json.MarshalIndent(GapAnalysisResponseSchema(), "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// GapReportSchemaJSON returns the indented JSON representation of GapReportSchema().
func GapReportSchemaJSON() (string, error) {
	b, err := json.MarshalIndent(GapReportSchema(), "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ResumeExtractResponseSchemaJSON returns the indented JSON representation of ResumeExtractResponseSchema().
func ResumeExtractResponseSchemaJSON() (string, error) {
	b, err := json.MarshalIndent(ResumeExtractResponseSchema(), "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// leanSkillItemSchema returns the JSON Schema definition for a single LeanSkill item.
func leanSkillItemSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":               map[string]any{"type": "string", "description": "Compact short identifier for citation (e.g. sk-1)"},
			"name":             map[string]any{"type": "string", "description": "Skill name"},
			"category":         map[string]any{"type": "string", "description": "Skill functional category"},
			"proficiency":      map[string]any{"type": "string", "description": "Proficiency level"},
			"years_experience": map[string]any{"type": "number", "description": "Years of experience"},
			"keywords": map[string]any{
				"type":        "array",
				"description": "Sub-skills and related keywords",
				"items":       map[string]any{"type": "string", "description": "Keyword or sub-skill term"},
			},
		},
		"required": []string{"id", "name"},
	}
}

// leanFactItemSchema returns the JSON Schema definition for a single LeanFact item.
func leanFactItemSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":         map[string]any{"type": "string", "description": "Compact short identifier for bullet citation (e.g. fact-1)"},
			"owner_id":   map[string]any{"type": "string", "description": "Parent entity short identifier"},
			"owner_type": map[string]any{"type": "string", "description": "Parent entity type"},
			"statement":  map[string]any{"type": "string", "description": "Factual accomplishment description"},
			"impact":     map[string]any{"type": "string", "description": "Quantifiable outcome or metric"},
			"skills": map[string]any{
				"type":        "array",
				"description": "Skills utilized in this factual accomplishment",
				"items":       leanSkillItemSchema(),
			},
		},
		"required": []string{"id", "statement"},
	}
}

// LeanMainProfileSchema returns the canonical JSON Schema for LeanMainProfile,
// defining only LLM-relevant public career profile attributes and short identifiers for provenance,
// with zero leakage of internal database or multi-tenancy attributes.
func LeanMainProfileSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "Minimal, lean main profile structure for LLM grounding with short IDs",
		"properties": map[string]any{
			"profile": map[string]any{
				"type":        "object",
				"description": "Candidate biographical and career summary",
				"properties": map[string]any{
					"headline": map[string]any{"type": "string", "description": "Professional headline"},
					"summary":  map[string]any{"type": "string", "description": "Candidate summary"},
				},
			},
			"skills": map[string]any{
				"type":        "array",
				"description": "Candidate technical and domain competencies",
				"items":       leanSkillItemSchema(),
			},
			"experiences": map[string]any{
				"type":        "array",
				"description": "Employment history",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":              map[string]any{"type": "string", "description": "Compact short identifier for entity citation (e.g. exp-1)"},
						"company":         map[string]any{"type": "string", "description": "Employer name"},
						"role_title":      map[string]any{"type": "string", "description": "Position title"},
						"employment_type": map[string]any{"type": "string", "description": "Employment type"},
						"start_date":      map[string]any{"type": "string", "description": "Employment start date"},
						"end_date":        map[string]any{"type": "string", "description": "Employment end date"},
						"is_current":      map[string]any{"type": "boolean", "description": "Current position flag"},
						"duties_summary":  map[string]any{"type": "string", "description": "Role duties summary"},
						"facts": map[string]any{
							"type":        "array",
							"description": "Achievements and metrics belonging to experience",
							"items":       leanFactItemSchema(),
						},
						"skills": map[string]any{
							"type":        "array",
							"description": "Skills utilized in experience",
							"items":       leanSkillItemSchema(),
						},
					},
					"required": []string{"id", "company", "role_title"},
				},
			},
			"projects": map[string]any{
				"type":        "array",
				"description": "Technical and open-source projects",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":         map[string]any{"type": "string", "description": "Compact short identifier for entity citation (e.g. proj-1)"},
						"name":       map[string]any{"type": "string", "description": "Project title"},
						"role":       map[string]any{"type": "string", "description": "Role in project"},
						"url":        map[string]any{"type": "string", "description": "Project URL"},
						"start_date": map[string]any{"type": "string", "description": "Start date"},
						"end_date":   map[string]any{"type": "string", "description": "End date"},
						"summary":    map[string]any{"type": "string", "description": "Project summary"},
						"facts": map[string]any{
							"type":        "array",
							"description": "Facts under project",
							"items":       leanFactItemSchema(),
						},
						"skills": map[string]any{
							"type":        "array",
							"description": "Skills under project",
							"items":       leanSkillItemSchema(),
						},
					},
					"required": []string{"id", "name"},
				},
			},
			"education": map[string]any{
				"type":        "array",
				"description": "Academic degrees and higher education",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":          map[string]any{"type": "string", "description": "Compact short identifier for entity citation (e.g. edu-1)"},
						"institution": map[string]any{"type": "string", "description": "Institution name"},
						"degree":      map[string]any{"type": "string", "description": "Degree earned"},
						"field":       map[string]any{"type": "string", "description": "Major or field of study"},
						"start_date":  map[string]any{"type": "string", "description": "Start date"},
						"end_date":    map[string]any{"type": "string", "description": "Completion date"},
						"score":       map[string]any{"type": "string", "description": "GPA or academic score"},
						"facts": map[string]any{
							"type":        "array",
							"description": "Facts under education",
							"items":       leanFactItemSchema(),
						},
					},
					"required": []string{"id", "institution"},
				},
			},
			"certifications": map[string]any{
				"type":        "array",
				"description": "Professional certifications and credentials",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":      map[string]any{"type": "string", "description": "Compact short identifier for entity citation (e.g. cert-1)"},
						"name":    map[string]any{"type": "string", "description": "Certification name"},
						"issuer":  map[string]any{"type": "string", "description": "Issuing authority"},
						"date":    map[string]any{"type": "string", "description": "Date certified"},
						"url":     map[string]any{"type": "string", "description": "Verification link"},
						"summary": map[string]any{"type": "string", "description": "Credential summary"},
					},
					"required": []string{"id", "name"},
				},
			},
			"facts": map[string]any{
				"type":        "array",
				"description": "Atomic factual achievements and responsibilities",
				"items":       leanFactItemSchema(),
			},
		},
		"required": []string{"profile"},
	}
}

// LeanMainProfileSchemaJSON returns the indented JSON representation of LeanMainProfileSchema().
func LeanMainProfileSchemaJSON() (string, error) {
	b, err := json.MarshalIndent(LeanMainProfileSchema(), "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ATSExtractionResponseSchema returns the canonical JSON Schema for Stage 1 ATS extraction and confidence scoring.
func ATSExtractionResponseSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"extraction_confidence_score": map[string]any{
				"type":        "number",
				"description": "Confidence score from 0 to 1 (where 1.0 is 100%) indicating how complete and unambiguously structured the extracted resume information is",
			},
			"extraction_summary": map[string]any{
				"type":        "string",
				"description": "Concise explanation of ATS extraction completeness and any structural or formatting observations",
			},
			"extracted_candidate": map[string]any{
				"type":        "object",
				"description": "Structured candidate career profile extracted and categorized from the text-based resume",
				"properties": map[string]any{
					"summary": map[string]any{
						"type":        "string",
						"description": "Extracted professional summary or objective statement",
					},
					"skills": map[string]any{
						"type":        "array",
						"description": "Categorized technical, domain, and professional skills extracted from the resume",
						"items":       map[string]any{"type": "string"},
					},
					"experiences": map[string]any{
						"type":        "array",
						"description": "Work experience entries parsed from the resume",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"company": map[string]any{
									"type":        "string",
									"description": "Employer or organization name",
								},
								"title": map[string]any{
									"type":        "string",
									"description": "Job title or role held",
								},
								"date_range": map[string]any{
									"type":        "string",
									"description": "Employment date range as parsed from the resume",
								},
								"highlights": map[string]any{
									"type":        "array",
									"description": "Accomplishment bullets and responsibilities extracted for this role",
									"items":       map[string]any{"type": "string"},
								},
							},
							"required":             []string{"company", "title"},
							"additionalProperties": false,
						},
					},
					"educations": map[string]any{
						"type":        "array",
						"description": "Academic credentials parsed from the resume",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"institution": map[string]any{
									"type":        "string",
									"description": "University, college, or school name",
								},
								"degree": map[string]any{
									"type":        "string",
									"description": "Degree or qualification earned",
								},
								"field": map[string]any{
									"type":        "string",
									"description": "Field of study or major",
								},
								"date_range": map[string]any{
									"type":        "string",
									"description": "Graduation date or attendance range",
								},
							},
							"required":             []string{"institution", "degree"},
							"additionalProperties": false,
						},
					},
					"certifications": map[string]any{
						"type":        "array",
						"description": "Professional certifications, licenses, or awards extracted from the resume",
						"items":       map[string]any{"type": "string"},
					},
				},
				"required":             []string{"summary", "skills", "experiences", "educations"},
				"additionalProperties": false,
			},
		},
		"required":             []string{"extraction_confidence_score", "extraction_summary", "extracted_candidate"},
		"additionalProperties": false,
	}
}

// ATSExtractionResponseSchemaJSON returns the indented JSON representation of ATSExtractionResponseSchema().
func ATSExtractionResponseSchemaJSON() (string, error) {
	b, err := json.MarshalIndent(ATSExtractionResponseSchema(), "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ATSMatchingResponseSchema returns the canonical JSON Schema for Stage 2 ATS job matching evaluation.
func ATSMatchingResponseSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"job_matching_score": map[string]any{
				"type":        "number",
				"description": "Perceived matching score from 0 to 1 (where 1.0 is 100%) evaluating how well the extracted candidate information matches the job description",
			},
			"matching_summary": map[string]any{
				"type":        "string",
				"description": "Concise explanation of how the extracted candidate profile aligns with the target job description requirements",
			},
		},
		"required":             []string{"job_matching_score", "matching_summary"},
		"additionalProperties": false,
	}
}

// ATSMatchingResponseSchemaJSON returns the indented JSON representation of ATSMatchingResponseSchema().
func ATSMatchingResponseSchemaJSON() (string, error) {
	b, err := json.MarshalIndent(ATSMatchingResponseSchema(), "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
