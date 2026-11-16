package llm_test

import (
	"testing"

	"github.com/resumeee-app/core/llm"
)

func assertNoInternalPropertiesRecursively(t *testing.T, path string, node *llm.Schema, forbidden []string) {
	t.Helper()
	if node == nil {
		return
	}

	for propName, propDef := range node.Properties {
		currentPath := path + "." + propName
		for _, f := range forbidden {
			if propName == f {
				t.Errorf("schema leaked forbidden internal property %q at %s", propName, currentPath)
			}
		}
		assertNoInternalPropertiesRecursively(t, currentPath, propDef, forbidden)
	}

	if node.Items != nil {
		assertNoInternalPropertiesRecursively(t, path+"[]", node.Items, forbidden)
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
		getter func() *llm.Schema
	}{
		{name: "GenerationResponseSchema", getter: llm.GenerationResponseSchema},
		{name: "GapAnalysisResponseSchema", getter: llm.GapAnalysisResponseSchema},
		{name: "GapReportSchema", getter: llm.GapReportSchema},
		{name: "ResumeExtractResponseSchema", getter: llm.ResumeExtractResponseSchema},
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
	schema := llm.GenerationResponseSchema()
	tailoredContent := schema.Properties["tailored_content"]
	if tailoredContent == nil {
		t.Fatal("GenerationResponseSchema missing tailored_content property")
	}
	exps := tailoredContent.Properties["experiences"]
	if exps == nil || exps.Items == nil || len(exps.Items.Properties) == 0 {
		t.Fatal("GenerationResponseSchema missing experiences.items.properties")
	}

	forbiddenExpFields := []string{
		"location",
		"hours_per_week",
		"supervisor_name",
		"supervisor_contact",
	}
	for _, f := range forbiddenExpFields {
		if _, exists := exps.Items.Properties[f]; exists {
			t.Errorf("GenerationResponseSchema experiences.items leaked non-tailoring field %q", f)
		}
	}
}

func TestLeanMainProfileSchema_StripsNonTailoringFields(t *testing.T) {
	schema := llm.LeanMainProfileSchema()
	if len(schema.Properties) == 0 {
		t.Fatal("LeanMainProfileSchema missing properties")
	}

	prof := schema.Properties["profile"]
	if prof == nil || len(prof.Properties) == 0 {
		t.Fatal("LeanMainProfileSchema missing profile.properties")
	}
	for _, f := range []string{"full_name", "email", "phone", "location", "profiles"} {
		if _, exists := prof.Properties[f]; exists {
			t.Errorf("LeanMainProfileSchema.profile leaked non-tailoring field %q", f)
		}
	}

	exps := schema.Properties["experiences"]
	if exps == nil || exps.Items == nil || len(exps.Items.Properties) == 0 {
		t.Fatal("LeanMainProfileSchema missing experiences.items.properties")
	}
	for _, f := range []string{"location", "company_location", "company_contact", "hours_per_week", "supervisor_name", "supervisor_contact"} {
		if _, exists := exps.Items.Properties[f]; exists {
			t.Errorf("LeanMainProfileSchema.experiences leaked non-tailoring field %q", f)
		}
	}

	edu := schema.Properties["education"]
	if edu == nil || edu.Items == nil || len(edu.Items.Properties) == 0 {
		t.Fatal("LeanMainProfileSchema missing education.items.properties")
	}
	if _, exists := edu.Items.Properties["location"]; exists {
		t.Errorf("LeanMainProfileSchema.education leaked non-tailoring field 'location'")
	}
}
