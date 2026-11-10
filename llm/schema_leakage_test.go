package llm_test

import (
	"testing"

	"resumeee/internal/llm"
)

// assertNoInternalPropertiesRecursively traverses a JSON Schema map and asserts that
// no internal database, tenant, or configuration field appears in properties.
func assertNoInternalPropertiesRecursively(t *testing.T, path string, node any, forbidden []string) {
	t.Helper()
	nodeMap, ok := node.(map[string]any)
	if !ok {
		return
	}

	if props, hasProps := nodeMap["properties"].(map[string]any); hasProps {
		for propName, propDef := range props {
			currentPath := path + "." + propName
			for _, f := range forbidden {
				if propName == f {
					t.Errorf("schema leaked forbidden internal property %q at %s", propName, currentPath)
				}
			}
			assertNoInternalPropertiesRecursively(t, currentPath, propDef, forbidden)
		}
	}

	if items, hasItems := nodeMap["items"].(map[string]any); hasItems {
		assertNoInternalPropertiesRecursively(t, path+"[]", items, forbidden)
	}
}

func TestSchemas_ZeroInternalFieldLeakage(t *testing.T) {
	forbiddenSchemaFields := []string{
		"user_id",
		"created_at",
		"updated_at",
		"is_global",
		"slug",
		"metadata",
		"main_revision",
		"exported_at",
		"default_provider",
		"default_model",
		"global_writing_rules",
		"billing_tier",
	}

	schemas := []struct {
		name   string
		getter func() map[string]any
	}{
		{name: "GenerationResponseSchema", getter: llm.GenerationResponseSchema},
		{name: "GapAnalysisResponseSchema", getter: llm.GapAnalysisResponseSchema},
		{name: "GapReportSchema", getter: llm.GapReportSchema},
		{name: "ResumeExtractResponseSchema", getter: llm.ResumeExtractResponseSchema},
		{name: "GeminiResponseSchema", getter: llm.GeminiResponseSchema},
		{name: "LeanMainProfileSchema", getter: llm.LeanMainProfileSchema},
		{name: "ATSExtractionResponseSchema", getter: llm.ATSExtractionResponseSchema},
		{name: "ATSMatchingResponseSchema", getter: llm.ATSMatchingResponseSchema},
	}

	for _, sc := range schemas {
		t.Run(sc.name, func(t *testing.T) {
			s := sc.getter()
			if s == nil {
				t.Fatalf("%s returned nil", sc.name)
			}
			assertNoInternalPropertiesRecursively(t, sc.name, s, forbiddenSchemaFields)
		})
	}
}

func TestGenerationResponseSchema_StripsNonTailoringFields(t *testing.T) {
	schemas := []struct {
		name   string
		schema map[string]any
	}{
		{"GenerationResponseSchema", llm.GenerationResponseSchema()},
		{"GeminiResponseSchema", llm.GeminiResponseSchema()},
	}

	for _, sc := range schemas {
		t.Run(sc.name, func(t *testing.T) {
			tailoredContent, ok := sc.schema["properties"].(map[string]any)["tailored_content"].(map[string]any)
			if !ok {
				t.Fatalf("%s missing tailored_content property", sc.name)
			}
			expItems, ok := tailoredContent["properties"].(map[string]any)["experiences"].(map[string]any)["items"].(map[string]any)
			if !ok {
				t.Fatalf("%s missing experiences.items", sc.name)
			}
			expProps, ok := expItems["properties"].(map[string]any)
			if !ok {
				t.Fatalf("%s missing experiences.items.properties", sc.name)
			}

			forbiddenExpFields := []string{
				"location",
				"hours_per_week",
				"supervisor_name",
				"supervisor_contact",
			}
			for _, f := range forbiddenExpFields {
				if _, exists := expProps[f]; exists {
					t.Errorf("%s experiences.items leaked non-tailoring field %q", sc.name, f)
				}
			}
		})
	}
}

func TestLeanMainProfileSchema_StripsNonTailoringFields(t *testing.T) {
	schema := llm.LeanMainProfileSchema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("LeanMainProfileSchema missing properties")
	}

	// Profile properties: full_name, email, phone, location, profiles must be stripped
	profileProps, ok := props["profile"].(map[string]any)["properties"].(map[string]any)
	if !ok {
		t.Fatal("LeanMainProfileSchema missing profile.properties")
	}
	forbiddenProfileFields := []string{"full_name", "email", "phone", "location", "profiles"}
	for _, f := range forbiddenProfileFields {
		if _, exists := profileProps[f]; exists {
			t.Errorf("LeanMainProfileSchema.profile leaked non-tailoring field %q", f)
		}
	}

	// Experience properties: location, company_location, company_contact, hours_per_week, supervisor_name, supervisor_contact must be stripped
	expProps, ok := props["experiences"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if !ok {
		t.Fatal("LeanMainProfileSchema missing experiences.items.properties")
	}
	forbiddenExpFields := []string{
		"location",
		"company_location",
		"company_contact",
		"hours_per_week",
		"supervisor_name",
		"supervisor_contact",
	}
	for _, f := range forbiddenExpFields {
		if _, exists := expProps[f]; exists {
			t.Errorf("LeanMainProfileSchema.experiences leaked non-tailoring field %q", f)
		}
	}

	// Education properties: location must be stripped
	eduProps, ok := props["education"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if !ok {
		t.Fatal("LeanMainProfileSchema missing education.items.properties")
	}
	if _, exists := eduProps["location"]; exists {
		t.Errorf("LeanMainProfileSchema.education leaked non-tailoring field 'location'")
	}
}

