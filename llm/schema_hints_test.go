package llm_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/resumeee-app/resumeee-core/llm"
)

func recursivelyCheckDescriptions(t *testing.T, path string, node *llm.Schema) {
	t.Helper()
	if node == nil {
		return
	}
	for propName, prop := range node.Properties {
		if prop == nil {
			continue
		}
		currentPath := path + "." + propName
		if strings.TrimSpace(prop.Description) == "" {
			t.Errorf("schema property %q is missing a non-empty description", currentPath)
		}
		if prop.Type.Contains(llm.TypeObject) {
			recursivelyCheckDescriptions(t, currentPath, prop)
		}
		if prop.Items != nil && prop.Items.Type.Contains(llm.TypeObject) {
			recursivelyCheckDescriptions(t, currentPath+"[]", prop.Items)
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

	if _, ok := schema.Properties["overall_match_summary"]; !ok {
		t.Error("expected overall_match_summary property in GapReportSchema")
	}
	if _, ok := schema.Properties["missing_requirements"]; !ok {
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
}

func assertStrictNormalizedObjectRecursively(t *testing.T, path string, node *llm.Schema) {
	t.Helper()
	if node == nil {
		return
	}

	for _, item := range node.AnyOf {
		assertStrictNormalizedObjectRecursively(t, path+".anyOf[]", item)
	}
	for _, item := range node.OneOf {
		assertStrictNormalizedObjectRecursively(t, path+".oneOf[]", item)
	}
	for _, item := range node.AllOf {
		assertStrictNormalizedObjectRecursively(t, path+".allOf[]", item)
	}

	if len(node.Properties) > 0 || node.Type.Contains(llm.TypeObject) {
		if node.AdditionalProperties == nil || *node.AdditionalProperties {
			t.Errorf("%s: expected additionalProperties=false, got %v", path, node.AdditionalProperties)
		}
		if node.Required == nil {
			t.Errorf("%s: expected non-nil Required slice on strict object", path)
		}
		reqSet := make(map[string]bool, len(node.Required))
		for _, k := range node.Required {
			reqSet[k] = true
		}
		for propName, propVal := range node.Properties {
			if !reqSet[propName] {
				t.Errorf("%s: property %q missing from required slice", path, propName)
			}
			assertStrictNormalizedObjectRecursively(t, path+"."+propName, propVal)
		}
	}

	if node.Items != nil {
		assertStrictNormalizedObjectRecursively(t, path+"[]", node.Items)
	}
}

func isPropertyNullable(prop *llm.Schema) bool {
	if prop == nil {
		return false
	}
	if prop.Type.Contains(llm.TypeNull) {
		return true
	}
	for _, item := range prop.AnyOf {
		if item != nil && item.Type.Contains(llm.TypeNull) {
			return true
		}
	}
	return false
}

func TestSchemaStrictAndClone_NonMutating(t *testing.T) {
	canonicalSchemas := []struct {
		name   string
		getter func() *llm.Schema
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

			strict := orig.Strict()
			assertStrictNormalizedObjectRecursively(t, sc.name+"(Strict)", strict)

			cloned := orig.Clone()
			if cloned == nil {
				t.Fatalf("Clone returned nil for %s", sc.name)
			}

			afterBytes, err := json.Marshal(orig)
			if err != nil {
				t.Fatalf("failed to marshal canonical schema %s after Strict/Clone: %v", sc.name, err)
			}
			if string(beforeBytes) != string(afterBytes) {
				t.Errorf("%s: canonical schema was mutated in place!\nbefore: %s\nafter:  %s", sc.name, string(beforeBytes), string(afterBytes))
			}
		})
	}

	t.Run("falsifiable optional vs required nullability and deep non-mutation", func(t *testing.T) {
		input := &llm.Schema{
			Type: llm.SchemaTypes{llm.TypeObject},
			Properties: map[string]*llm.Schema{
				"req_str": {Type: llm.SchemaTypes{llm.TypeString}, Description: "Required scalar"},
				"opt_str": {Type: llm.SchemaTypes{llm.TypeString}, Description: "Optional scalar"},
				"opt_arr": {
					Type:        llm.SchemaTypes{llm.TypeArray},
					Description: "Optional array",
					Items:       &llm.Schema{Type: llm.SchemaTypes{llm.TypeString}},
				},
				"opt_enum": {
					Type:        llm.SchemaTypes{llm.TypeString},
					Enum:        []string{"a", "b"},
					Description: "Optional enum",
				},
				"opt_obj": {
					Type:        llm.SchemaTypes{llm.TypeObject},
					Description: "Optional nested object",
					Properties: map[string]*llm.Schema{
						"inner_req": {Type: llm.SchemaTypes{llm.TypeNumber}, Description: "Inner required"},
						"inner_opt": {Type: llm.SchemaTypes{llm.TypeBoolean}, Description: "Inner optional"},
					},
					Required: []string{"inner_req"},
				},
			},
			Required: []string{"req_str"},
		}

		out := input.Strict()
		assertStrictNormalizedObjectRecursively(t, "Strict", out)

		if isPropertyNullable(out.Properties["req_str"]) {
			t.Errorf("Strict erroneously made originally required property req_str nullable")
		}
		for _, optKey := range []string{"opt_str", "opt_arr", "opt_enum", "opt_obj"} {
			if !isPropertyNullable(out.Properties[optKey]) {
				t.Errorf("Strict failed to make optional property %q nullable: %+v", optKey, out.Properties[optKey])
			}
		}

		// Mutating returned schema must not affect input schema.
		out.Properties["req_str"].Type = llm.SchemaTypes{"mutated"}
		if len(input.Required) != 1 || input.Required[0] != "req_str" {
			t.Errorf("Strict mutated caller's required slice: %v", input.Required)
		}
		if len(input.Properties["req_str"].Type) != 1 || input.Properties["req_str"].Type[0] != llm.TypeString {
			t.Errorf("Strict shared mutable references with caller input")
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

			if !strings.Contains(payload.SystemPrompt, "expert resume parsing engine") {
				t.Errorf("expected SystemPrompt to contain parser persona, got: %s", payload.SystemPrompt)
			}
			if !strings.Contains(payload.SystemPrompt, "<json_schema_hints>") {
				t.Errorf("expected SystemPrompt to contain <json_schema_hints> tag")
			}
			if !strings.Contains(payload.SystemPrompt, "basics: { name, label, email, phone, url, summary") {
				t.Errorf("expected SystemPrompt to contain schema hints outline")
			}

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

func TestSchemaStrict_CombinatorsEmptyObjectAndIdempotency(t *testing.T) {
	combinatorSchema := &llm.Schema{
		Type: llm.SchemaTypes{llm.TypeObject},
		Properties: map[string]*llm.Schema{
			"choice": {
				AnyOf: []*llm.Schema{
					{
						Type: llm.SchemaTypes{llm.TypeObject},
						Properties: map[string]*llm.Schema{
							"a": {Type: llm.SchemaTypes{llm.TypeString}},
							"b": {Type: llm.SchemaTypes{llm.TypeInteger}},
						},
						Required: []string{"a"},
					},
					{Type: llm.SchemaTypes{llm.TypeString}},
				},
			},
			"alt": {
				OneOf: []*llm.Schema{
					{
						Type: llm.SchemaTypes{llm.TypeObject},
						Properties: map[string]*llm.Schema{
							"flag": {Type: llm.SchemaTypes{llm.TypeBoolean}},
						},
					},
				},
			},
			"empty_obj": {
				Type: llm.SchemaTypes{llm.TypeObject},
			},
		},
		Required: []string{"choice", "empty_obj"},
	}

	once := combinatorSchema.Strict()
	twice := once.Strict()

	onceBytes, err := json.Marshal(once)
	if err != nil {
		t.Fatalf("failed to marshal once: %v", err)
	}
	twiceBytes, err := json.Marshal(twice)
	if err != nil {
		t.Fatalf("failed to marshal twice: %v", err)
	}
	if string(onceBytes) != string(twiceBytes) {
		t.Errorf("expected Strict() to be idempotent:\nonce:  %s\ntwice: %s", string(onceBytes), string(twiceBytes))
	}

	firstBranch := once.Properties["choice"].AnyOf[0]
	if firstBranch.AdditionalProperties == nil || *firstBranch.AdditionalProperties {
		t.Errorf("expected nested anyOf object branch to set additionalProperties=false")
	}

	// Empty object in strict mode must serialize `"required":[]`, including after Clone().
	if !strings.Contains(string(onceBytes), `"required":[]`) {
		t.Errorf("expected empty strict object to marshal \"required\":[], got %s", string(onceBytes))
	}
	clonedStrictBytes, err := json.Marshal(once.Clone())
	if err != nil {
		t.Fatalf("failed to marshal cloned strict schema: %v", err)
	}
	if string(onceBytes) != string(clonedStrictBytes) {
		t.Errorf("expected Clone() to preserve non-nil empty Required slice:\nonce:   %s\ncloned: %s", string(onceBytes), string(clonedStrictBytes))
	}

	explicitEmptyReq := (&llm.Schema{
		Type:     llm.SchemaTypes{llm.TypeObject},
		Required: []string{},
	}).Clone()
	explicitBytes, err := json.Marshal(explicitEmptyReq)
	if err != nil {
		t.Fatalf("failed to marshal explicit empty Required clone: %v", err)
	}
	if !strings.Contains(string(explicitBytes), `"required":[]`) {
		t.Errorf("expected cloned empty Required slice to marshal \"required\":[], got %s", string(explicitBytes))
	}
}
