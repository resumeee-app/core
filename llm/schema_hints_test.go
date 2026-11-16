package llm_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/resumeee-app/core/llm"
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

// assertAllObjectsDisallowAdditionalProps verifies every object schema node sets additionalProperties=false.
func assertAllObjectsDisallowAdditionalProps(t *testing.T, path string, node *llm.Schema) {
	t.Helper()
	if node == nil {
		return
	}
	if len(node.Properties) > 0 || node.Type.Contains(llm.TypeObject) {
		if node.AdditionalProperties == nil || *node.AdditionalProperties {
			t.Errorf("%s: expected canonical additionalProperties=false, got %v", path, node.AdditionalProperties)
		}
	}
	for propName, prop := range node.Properties {
		assertAllObjectsDisallowAdditionalProps(t, path+"."+propName, prop)
	}
	if node.Items != nil {
		assertAllObjectsDisallowAdditionalProps(t, path+"[]", node.Items)
	}
	for _, item := range node.AnyOf {
		assertAllObjectsDisallowAdditionalProps(t, path+".anyOf[]", item)
	}
	for _, item := range node.OneOf {
		assertAllObjectsDisallowAdditionalProps(t, path+".oneOf[]", item)
	}
	for _, item := range node.AllOf {
		assertAllObjectsDisallowAdditionalProps(t, path+".allOf[]", item)
	}
}

// assertRequiredContainsAll verifies that node is non-nil and requires every key in want.
func assertRequiredContainsAll(t *testing.T, path string, node *llm.Schema, want []string) {
	t.Helper()
	if node == nil {
		t.Fatalf("%s: expected non-nil schema node", path)
	}
	for _, key := range want {
		if !slices.Contains(node.Required, key) {
			t.Errorf("%s: missing required key %q (have %v)", path, key, node.Required)
		}
	}
}

// assertNonNullableArrayProperty verifies prop is a non-nil, non-nullable array schema node.
func assertNonNullableArrayProperty(t *testing.T, path string, prop *llm.Schema) {
	t.Helper()
	if prop == nil {
		t.Fatalf("%s: expected non-nil array property", path)
	}
	if !prop.Type.Contains(llm.TypeArray) {
		t.Errorf("%s: expected array type, got %v", path, prop.Type)
	}
	if isPropertyNullable(prop) {
		t.Errorf("%s: expected non-nullable array in Strict() schema, got %+v", path, prop)
	}
}

// assertDescriptionContainsAll verifies prop has a description containing every expected term (case-insensitive).
func assertDescriptionContainsAll(t *testing.T, path string, prop *llm.Schema, terms ...string) {
	t.Helper()
	if prop == nil {
		t.Fatalf("%s: expected non-nil schema property", path)
	}
	desc := strings.ToLower(strings.TrimSpace(prop.Description))
	if desc == "" {
		t.Fatalf("%s: expected non-empty description", path)
	}
	for _, term := range terms {
		if !strings.Contains(desc, strings.ToLower(term)) {
			t.Errorf("%s: description %q missing expected term %q", path, prop.Description, term)
		}
	}
}

// TestResumeExtractResponseSchema_HighlightContracts verifies strict object contracts,
// required non-nullable section/highlight arrays, Clone preservation, and clear summary vs highlights descriptions.
func TestResumeExtractResponseSchema_HighlightContracts(t *testing.T) {
	schema := llm.ResumeExtractResponseSchema()
	if schema == nil {
		t.Fatal("ResumeExtractResponseSchema returned nil")
	}

	topLevelKeys := []string{
		"basics",
		"work",
		"volunteer",
		"education",
		"awards",
		"certificates",
		"publications",
		"skills",
		"languages",
		"interests",
		"references",
		"projects",
	}

	assertAllObjectsDisallowAdditionalProps(t, "ResumeExtractResponseSchema", schema)
	assertRequiredContainsAll(t, "ResumeExtractResponseSchema", schema, topLevelKeys)

	cloned := schema.Clone()
	assertAllObjectsDisallowAdditionalProps(t, "ResumeExtractResponseSchema.Clone", cloned)
	assertRequiredContainsAll(t, "ResumeExtractResponseSchema.Clone", cloned, topLevelKeys)
	assertRequiredContainsAll(t, "Clone.basics", cloned.Properties["basics"], []string{"name", "summary", "profiles"})
	assertRequiredContainsAll(t, "Clone.work[]", cloned.Properties["work"].Items, []string{"name", "position", "summary", "highlights"})
	assertRequiredContainsAll(t, "Clone.volunteer[]", cloned.Properties["volunteer"].Items, []string{"organization", "position", "summary", "highlights"})
	assertRequiredContainsAll(t, "Clone.education[]", cloned.Properties["education"].Items, []string{"institution", "courses", "highlights"})
	assertRequiredContainsAll(t, "Clone.projects[]", cloned.Properties["projects"].Items, []string{"name", "description", "highlights", "keywords"})

	strict := schema.Strict()
	if isPropertyNullable(strict.Properties["basics"]) {
		t.Errorf("Strict basics should not be nullable")
	}
	for _, sectionKey := range topLevelKeys[1:] {
		assertNonNullableArrayProperty(t, "Strict."+sectionKey, strict.Properties[sectionKey])
	}

	nonNullableArrays := []struct {
		path string
		prop *llm.Schema
	}{
		{"Strict.basics.profiles", strict.Properties["basics"].Properties["profiles"]},
		{"Strict.work[].highlights", strict.Properties["work"].Items.Properties["highlights"]},
		{"Strict.volunteer[].highlights", strict.Properties["volunteer"].Items.Properties["highlights"]},
		{"Strict.education[].highlights", strict.Properties["education"].Items.Properties["highlights"]},
		{"Strict.education[].courses", strict.Properties["education"].Items.Properties["courses"]},
		{"Strict.projects[].highlights", strict.Properties["projects"].Items.Properties["highlights"]},
		{"Strict.projects[].keywords", strict.Properties["projects"].Items.Properties["keywords"]},
		{"Strict.skills[].keywords", strict.Properties["skills"].Items.Properties["keywords"]},
		{"Strict.interests[].keywords", strict.Properties["interests"].Items.Properties["keywords"]},
	}
	for _, tc := range nonNullableArrays {
		assertNonNullableArrayProperty(t, tc.path, tc.prop)
	}

	assertDescriptionContainsAll(t, "work[].summary", schema.Properties["work"].Items.Properties["summary"], "concise", "highlights")
	assertDescriptionContainsAll(t, "projects[].description", schema.Properties["projects"].Items.Properties["description"], "concise", "highlights")

	eduHighlights := schema.Properties["education"].Items.Properties["highlights"]
	if eduHighlights == nil {
		t.Fatalf("education[].highlights property missing from ResumeExtractResponseSchema")
	}
	if !eduHighlights.Type.Contains(llm.TypeArray) || eduHighlights.Items == nil || !eduHighlights.Items.Type.Contains(llm.TypeString) {
		t.Errorf("education[].highlights must be an array of strings, got %+v", eduHighlights)
	}
	assertDescriptionContainsAll(t, "education[].highlights", eduHighlights, "honors", "distinctions", "thesis", "research", "[]")
}
