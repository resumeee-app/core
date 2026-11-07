package llm_test

import (
	"strings"
	"testing"

	"resumeee/internal/llm"
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

func TestLeanMasterProfileSchema_EnrichedFieldDescriptions(t *testing.T) {
	schema := llm.LeanMasterProfileSchema()
	if schema == nil {
		t.Fatal("LeanMasterProfileSchema returned nil")
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
			name:       "LeanMasterProfileSchemaJSON",
			jsonGetter: llm.LeanMasterProfileSchemaJSON,
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
