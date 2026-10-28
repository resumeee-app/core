package llm

import (
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

// GenerationResponseSchema returns the canonical JSON Schema for domain.GenerationResponse
// (TailoredContent and GapReport), enforcing strict validation keywords (required, additionalProperties: false).
func GenerationResponseSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tailored_content": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"summary": map[string]any{
						"type":        "string",
						"description": "Professional executive summary tailored to target role",
					},
					"skill_groups": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"category": map[string]any{"type": "string"},
								"skills": map[string]any{
									"type":  "array",
									"items": map[string]any{"type": "string"},
								},
								"source_skill_ids": map[string]any{
									"type":  "array",
									"items": map[string]any{"type": "string"},
								},
							},
							"required":             []string{"category", "skills"},
							"additionalProperties": false,
						},
					},
					"experiences": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"source_entity_id": map[string]any{
									"type":        "string",
									"description": "Exact UUID of the Master WorkExperience",
								},
								"company":    map[string]any{"type": "string"},
								"role_title": map[string]any{"type": "string"},
								"start_date": map[string]any{"type": "string"},
								"end_date":   map[string]any{"type": "string"},
								"location": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"city":         map[string]any{"type": "string"},
										"region":       map[string]any{"type": "string"},
										"country_code": map[string]any{"type": "string"},
									},
								},
								"hours_per_week":     map[string]any{"type": "integer"},
								"supervisor_name":    map[string]any{"type": "string"},
								"supervisor_contact": map[string]any{"type": "string"},
								"duties_summary":     map[string]any{"type": "string"},
								"bullets": map[string]any{
									"type": "array",
									"items": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"text": map[string]any{
												"type":        "string",
												"description": "Tailored accomplishment bullet",
											},
											"source_fact_ids": map[string]any{
												"type":        "array",
												"items":       map[string]any{"type": "string"},
												"description": "UUIDs of Facts under this WorkExperience that justify this bullet",
											},
											"source_skill_ids": map[string]any{
												"type":  "array",
												"items": map[string]any{"type": "string"},
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
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"source_entity_id": map[string]any{"type": "string"},
								"name":             map[string]any{"type": "string"},
								"role":             map[string]any{"type": "string"},
								"url":              map[string]any{"type": "string"},
								"start_date":       map[string]any{"type": "string"},
								"end_date":         map[string]any{"type": "string"},
								"summary":          map[string]any{"type": "string"},
								"bullets": map[string]any{
									"type": "array",
									"items": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"text":             map[string]any{"type": "string"},
											"source_fact_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
											"source_skill_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
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
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"source_entity_id": map[string]any{"type": "string"},
								"institution":      map[string]any{"type": "string"},
								"degree":           map[string]any{"type": "string"},
								"field":            map[string]any{"type": "string"},
								"start_date":       map[string]any{"type": "string"},
								"end_date":         map[string]any{"type": "string"},
								"score":            map[string]any{"type": "string"},
							},
							"required":             []string{"source_entity_id", "institution", "degree", "field", "start_date"},
							"additionalProperties": false,
						},
					},
					"certifications": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"source_entity_id": map[string]any{"type": "string"},
								"name":             map[string]any{"type": "string"},
								"issuer":           map[string]any{"type": "string"},
								"date":             map[string]any{"type": "string"},
								"url":              map[string]any{"type": "string"},
							},
							"required":             []string{"source_entity_id", "name", "issuer", "date"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"summary", "skill_groups", "experiences", "projects", "education", "certifications"},
				"additionalProperties": false,
			},
			"gap_report": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"overall_match_summary": map[string]any{
						"type":        "string",
						"description": "Concise summary of alignment against target requirements",
					},
					"missing_requirements": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"requirement": map[string]any{
									"type":        "string",
									"description": "Target requirement or qualification missing from master profile",
								},
								"importance": map[string]any{
									"type": "string",
									"enum": []string{"critical", "preferred", "nice_to_have"},
								},
								"clarifying_prompt": map[string]any{
									"type":        "string",
									"description": "Question to prompt user for missing experience or facts",
								},
							},
							"required":             []string{"requirement", "importance", "clarifying_prompt"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"overall_match_summary", "missing_requirements"},
				"additionalProperties": false,
			},
		},
		"required":             []string{"tailored_content", "gap_report"},
		"additionalProperties": false,
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

// GeminiResponseSchema returns a Google Gemini-compatible JSON schema for domain.GenerationResponse
// by stripping additionalProperties and OpenAPI extensions unsupported by Gemini.
func GeminiResponseSchema() map[string]any {
	return SanitizeGeminiSchema(GenerationResponseSchema())
}

