package llm_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/resumeee-app/resumeee-core/llm"
)

// recursivelyCheckDescriptions ensures every property node in a JSON schema has a non-empty description.
func recursivelyCheckDescriptions(t *testing.T, path string, node map[string]any) {
	t.Helper()
	properties, ok := node["properties"].(map[string]any)
	if !ok {
		return
	}
	for propName, propVal := range properties {
		propMap, isMap := propVal.(map[string]any)
		if !isMap {
			continue
		}
		currentPath := path + "." + propName

		// Check description
		desc, hasDesc := propMap["description"].(string)
		if !hasDesc || strings.TrimSpace(desc) == "" {
			t.Errorf("schema property %q is missing a non-empty description", currentPath)
		}

		// Recurse into object properties
		if propType, _ := propMap["type"].(string); propType == "object" {
			recursivelyCheckDescriptions(t, currentPath, propMap)
		}

		// Recurse into array item properties
		if items, hasItems := propMap["items"].(map[string]any); hasItems {
			if itemType, _ := items["type"].(string); itemType == "object" {
				recursivelyCheckDescriptions(t, currentPath+"[]", items)
			}
		}
	}
}

func TestGenerationResponseSchema_EnrichedFieldDescriptions(t *testing.T) {
	schema := llm.GenerationResponseSchema()
	if schema == nil {
		t.Fatal("GenerationResponseSchema returned nil")
	}

	recursivelyCheckDescriptions(t, "root", schema)
}

func TestGapAnalysisResponseSchema_EnrichedFieldDescriptions(t *testing.T) {
	schema := llm.GapAnalysisResponseSchema()
	if schema == nil {
		t.Fatal("GapAnalysisResponseSchema returned nil")
	}

	recursivelyCheckDescriptions(t, "root", schema)
}

func TestLeanMainProfileSchema_EnrichedFieldDescriptions(t *testing.T) {
	schema := llm.LeanMainProfileSchema()
	if schema == nil {
		t.Fatal("LeanMainProfileSchema returned nil")
	}

	recursivelyCheckDescriptions(t, "root", schema)
}

func TestATSExtractionResponseSchema_EnrichedFieldDescriptions(t *testing.T) {
	schema := llm.ATSExtractionResponseSchema()
	if schema == nil {
		t.Fatal("ATSExtractionResponseSchema returned nil")
	}

	recursivelyCheckDescriptions(t, "root", schema)
}

func TestATSMatchingResponseSchema_EnrichedFieldDescriptions(t *testing.T) {
	schema := llm.ATSMatchingResponseSchema()
	if schema == nil {
		t.Fatal("ATSMatchingResponseSchema returned nil")
	}

	recursivelyCheckDescriptions(t, "root", schema)
}

func TestGapReportSchema_Integrity(t *testing.T) {
	schema := llm.GapReportSchema()
	if schema == nil {
		t.Fatal("GapReportSchema returned nil")
	}
	recursivelyCheckDescriptions(t, "root", schema)

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map in GapReportSchema")
	}
	if _, ok := props["overall_match_summary"]; !ok {
		t.Error("expected overall_match_summary property in GapReportSchema")
	}
	if _, ok := props["missing_requirements"]; !ok {
		t.Error("expected missing_requirements property in GapReportSchema")
	}
}

func TestSchemaJSON_Helpers(t *testing.T) {
	tests := []struct {
		name       string
		jsonGetter func() (string, error)
		checkKey   string
	}{
		{
			name:       "GenerationResponseSchemaJSON",
			jsonGetter: llm.GenerationResponseSchemaJSON,
			checkKey:   `"tailored_content"`,
		},
		{
			name:       "GapAnalysisResponseSchemaJSON",
			jsonGetter: llm.GapAnalysisResponseSchemaJSON,
			checkKey:   `"gap_report"`,
		},
		{
			name:       "GapReportSchemaJSON",
			jsonGetter: llm.GapReportSchemaJSON,
			checkKey:   `"overall_match_summary"`,
		},
		{
			name:       "ResumeExtractResponseSchemaJSON",
			jsonGetter: llm.ResumeExtractResponseSchemaJSON,
			checkKey:   `"basics"`,
		},
		{
			name:       "LeanMainProfileSchemaJSON",
			jsonGetter: llm.LeanMainProfileSchemaJSON,
			checkKey:   `"profile"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			jsonStr, err := tc.jsonGetter()
			if err != nil {
				t.Fatalf("%s returned error: %v", tc.name, err)
			}
			if !strings.Contains(jsonStr, tc.checkKey) {
				t.Errorf("%s missing expected key %s", tc.name, tc.checkKey)
			}
			if !strings.Contains(jsonStr, "{\n  ") {
				t.Errorf("%s expected indented JSON, got: %s", tc.name, jsonStr)
			}
		})
	}
}

func TestResumeExtractResponseSchema_ExistsAndEnriched(t *testing.T) {
	schema := llm.ResumeExtractResponseSchema()
	if schema == nil {
		t.Fatal("ResumeExtractResponseSchema returned nil")
	}

	recursivelyCheckDescriptions(t, "root", schema)

	// Verify Gemini sanitization compatibility
	geminiSchema := llm.SanitizeGeminiSchema(schema)
	if geminiSchema == nil {
		t.Fatal("SanitizeGeminiSchema returned nil for ResumeExtractResponseSchema")
	}
}

func assertStrictNormalizedObjectRecursively(t *testing.T, path string, node any) {
	t.Helper()
	m, ok := node.(map[string]any)
	if !ok {
		return
	}

	if anyOfList, hasAnyOf := m["anyOf"].([]any); hasAnyOf {
		for _, item := range anyOfList {
			assertStrictNormalizedObjectRecursively(t, path+".anyOf[]", item)
		}
	}

	props, hasProps := m["properties"].(map[string]any)
	if hasProps {
		if ap, exists := m["additionalProperties"]; !exists || ap != false {
			t.Errorf("%s: expected additionalProperties=false, got %v", path, ap)
		}
		reqSet := make(map[string]bool)
		switch req := m["required"].(type) {
		case []string:
			for _, k := range req {
				reqSet[k] = true
			}
		case []any:
			for _, v := range req {
				if s, ok := v.(string); ok {
					reqSet[s] = true
				}
			}
		}
		for propName, propVal := range props {
			if !reqSet[propName] {
				t.Errorf("%s: property %q missing from required slice (optional parameters violate strict/grammar limits)", path, propName)
			}
			assertStrictNormalizedObjectRecursively(t, path+"."+propName, propVal)
		}
	}

	if items, hasItems := m["items"]; hasItems {
		assertStrictNormalizedObjectRecursively(t, path+"[]", items)
	}
}

func isPropertyNullable(prop map[string]any) bool {
	switch tv := prop["type"].(type) {
	case []string:
		for _, s := range tv {
			if s == "null" {
				return true
			}
		}
	case []any:
		for _, v := range tv {
			if s, ok := v.(string); ok && s == "null" {
				return true
			}
		}
	}
	if anyOfList, ok := prop["anyOf"].([]any); ok {
		for _, item := range anyOfList {
			if im, ok := item.(map[string]any); ok && im["type"] == "null" {
				return true
			}
		}
	}
	return false
}

func TestSanitizeOpenAIAndAnthropicSchemas_StrictAndNonMutating(t *testing.T) {
	canonicalSchemas := []struct {
		name   string
		getter func() map[string]any
	}{
		{"GenerationResponseSchema", llm.GenerationResponseSchema},
		{"GapAnalysisResponseSchema", llm.GapAnalysisResponseSchema},
		{"ResumeExtractResponseSchema", llm.ResumeExtractResponseSchema},
		{"ATSExtractionResponseSchema", llm.ATSExtractionResponseSchema},
		{"ATSMatchingResponseSchema", llm.ATSMatchingResponseSchema},
	}

	for _, sc := range canonicalSchemas {
		t.Run(sc.name, func(t *testing.T) {
			orig := sc.getter()
			beforeBytes, err := json.Marshal(orig)
			if err != nil {
				t.Fatalf("failed to marshal canonical schema %s: %v", sc.name, err)
			}

			oai := llm.SanitizeOpenAISchema(orig)
			assertStrictNormalizedObjectRecursively(t, sc.name+"(OpenAI)", oai)

			ant := llm.SanitizeAnthropicSchema(orig)
			assertStrictNormalizedObjectRecursively(t, sc.name+"(Anthropic)", ant)

			gem := llm.SanitizeGeminiJSONSchema(orig)
			if gem == nil {
				t.Fatalf("SanitizeGeminiJSONSchema returned nil for %s", sc.name)
			}

			afterBytes, err := json.Marshal(orig)
			if err != nil {
				t.Fatalf("failed to marshal canonical schema %s after sanitization: %v", sc.name, err)
			}
			if string(beforeBytes) != string(afterBytes) {
				t.Errorf("%s: canonical schema map was mutated in place!\nbefore: %s\nafter:  %s", sc.name, string(beforeBytes), string(afterBytes))
			}
		})
	}

	t.Run("falsifiable optional vs required nullability and deep non-mutation", func(t *testing.T) {
		input := map[string]any{
			"$schema":     "http://json-schema.org/draft-07/schema#",
			"definitions": map[string]any{"x": map[string]any{"type": "string"}},
			"type":        "object",
			"properties": map[string]any{
				"req_str": map[string]any{"type": "string", "description": "Required scalar"},
				"opt_str": map[string]any{"type": "string", "description": "Optional scalar"},
				"opt_arr": map[string]any{
					"type":        "array",
					"description": "Optional array",
					"items":       map[string]any{"type": "string"},
				},
				"opt_enum": map[string]any{
					"type":        "string",
					"enum":        []string{"a", "b"},
					"description": "Optional enum",
				},
				"opt_obj": map[string]any{
					"type":        "object",
					"description": "Optional nested object",
					"properties": map[string]any{
						"inner_req": map[string]any{"type": "number", "description": "Inner required"},
						"inner_opt": map[string]any{"type": "boolean", "description": "Inner optional"},
					},
					"required": []string{"inner_req"},
				},
			},
			"required": []string{"req_str"},
		}

		for _, fn := range []struct {
			name string
			call func(map[string]any) map[string]any
		}{
			{"SanitizeOpenAISchema", llm.SanitizeOpenAISchema},
			{"SanitizeAnthropicSchema", llm.SanitizeAnthropicSchema},
		} {
			t.Run(fn.name, func(t *testing.T) {
				out := fn.call(input)
				assertStrictNormalizedObjectRecursively(t, fn.name, out)

				if _, hasMeta := out["$schema"]; hasMeta {
					t.Errorf("%s failed to strip $schema", fn.name)
				}
				if _, hasDefs := out["definitions"]; hasDefs {
					t.Errorf("%s failed to strip definitions", fn.name)
				}

				props := out["properties"].(map[string]any)
				if isPropertyNullable(props["req_str"].(map[string]any)) {
					t.Errorf("%s erroneously made originally required property req_str nullable", fn.name)
				}
				for _, optKey := range []string{"opt_str", "opt_arr", "opt_enum", "opt_obj"} {
					if !isPropertyNullable(props[optKey].(map[string]any)) {
						t.Errorf("%s failed to make optional property %q nullable: %+v", fn.name, optKey, props[optKey])
					}
				}

				// Mutating returned schema must not affect input map
				props["req_str"].(map[string]any)["type"] = "mutated"
				origReqList := input["required"].([]string)
				if len(origReqList) != 1 || origReqList[0] != "req_str" {
					t.Errorf("%s mutated caller's required slice: %v", fn.name, origReqList)
				}
				if input["properties"].(map[string]any)["req_str"].(map[string]any)["type"] != "string" {
					t.Errorf("%s shared mutable map references with caller input", fn.name)
				}
				if _, stillHasMeta := input["$schema"]; !stillHasMeta {
					t.Errorf("%s deleted $schema from caller input map", fn.name)
				}
			})
		}
	})
}

func TestResumeExtractPrompt_Composition(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		expectedContains string
		expectedOmit     string
	}{
		{
			name:             "standard resume text",
			input:            "Jane Doe\nSenior SRE\njane@example.com\nWorked at Acme Corp 2021-Present",
			expectedContains: "Jane Doe\nSenior SRE",
		},
		{
			name:             "whitespace trimming around input",
			input:            "   \n\t  Alex Mercer\nStaff Distributed Systems Engineer  \n\t   ",
			expectedContains: "Alex Mercer\nStaff Distributed Systems Engineer",
			expectedOmit:     "   \n\t  Alex Mercer",
		},
		{
			name:             "empty string input",
			input:            "",
			expectedContains: "<raw_resume_text>\n\n</raw_resume_text>",
		},
		{
			name:             "whitespace-only input",
			input:            "   \n\t\r\n   ",
			expectedContains: "<raw_resume_text>\n\n</raw_resume_text>",
		},
		{
			name:             "special characters and markdown formatting in input",
			input:            "## John O'Connor & Sons <tech@example.org>\n* Built C++ / Go services (99.99% SLA)",
			expectedContains: "## John O'Connor & Sons <tech@example.org>\n* Built C++ / Go services (99.99% SLA)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := llm.BuildResumeExtractPrompt(tc.input)
			if err != nil {
				t.Fatalf("BuildResumeExtractPrompt failed for %q: %v", tc.name, err)
			}

			// Validate SystemPrompt invariants across all cases
			if !strings.Contains(payload.SystemPrompt, "expert resume parsing engine") {
				t.Errorf("expected SystemPrompt to contain parser persona, got: %s", payload.SystemPrompt)
			}
			if !strings.Contains(payload.SystemPrompt, "<json_schema_hints>") {
				t.Errorf("expected SystemPrompt to contain <json_schema_hints> tag")
			}
			if !strings.Contains(payload.SystemPrompt, "basics: { name, label, email, phone, url, summary") {
				t.Errorf("expected SystemPrompt to contain schema hints outline")
			}

			// Validate UserPrompt composition
			if !strings.Contains(payload.UserPrompt, "<raw_resume_text>") {
				t.Errorf("expected UserPrompt to contain <raw_resume_text> opening tag")
			}
			if !strings.Contains(payload.UserPrompt, "</raw_resume_text>") {
				t.Errorf("expected UserPrompt to contain </raw_resume_text> closing tag")
			}
			if tc.expectedContains != "" && !strings.Contains(payload.UserPrompt, tc.expectedContains) {
				t.Errorf("expected UserPrompt to contain %q, got: %s", tc.expectedContains, payload.UserPrompt)
			}
			if tc.expectedOmit != "" && strings.Contains(payload.UserPrompt, tc.expectedOmit) {
				t.Errorf("expected UserPrompt to omit untrimmed text %q", tc.expectedOmit)
			}
		})
	}
}

func TestSanitizers_CombinatorsStandardSchemaAndIdempotency(t *testing.T) {
	combinatorSchema := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]any{
			"choice": map[string]any{
				"anyOf": []any{
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"a": map[string]any{"type": "string"},
							"b": map[string]any{"type": "integer"},
						},
						"required": []string{"a"},
					},
					map[string]any{"type": "string"},
				},
			},
			"alt": map[string]any{
				"oneOf": []any{
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"flag": map[string]any{"type": "boolean"},
						},
					},
				},
			},
		},
		"required": []string{"choice"},
	}

	std := llm.SanitizeStandardJSONSchema(combinatorSchema)
	if _, hasMeta := std["$schema"]; hasMeta {
		t.Errorf("expected SanitizeStandardJSONSchema to strip $schema")
	}

	once := llm.SanitizeOpenAISchema(combinatorSchema)
	twice := llm.SanitizeOpenAISchema(once)

	onceBytes, err := json.Marshal(once)
	if err != nil {
		t.Fatalf("failed to marshal once: %v", err)
	}
	twiceBytes, err := json.Marshal(twice)
	if err != nil {
		t.Fatalf("failed to marshal twice: %v", err)
	}
	if string(onceBytes) != string(twiceBytes) {
		t.Errorf("expected SanitizeOpenAISchema to be idempotent:\nonce:  %s\ntwice: %s", string(onceBytes), string(twiceBytes))
	}

	props := once["properties"].(map[string]any)
	choiceBranches := props["choice"].(map[string]any)["anyOf"].([]any)
	firstBranch := choiceBranches[0].(map[string]any)
	if firstBranch["additionalProperties"] != false {
		t.Errorf("expected nested anyOf object branch to set additionalProperties=false, got %v", firstBranch["additionalProperties"])
	}
}
