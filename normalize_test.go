package core_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/resumeee-app/resumeee-core"
)

// TestDomainPurity_ZeroGORMImports verifies that internal/domain contains zero imports of gorm.io/gorm.
func TestDomainPurity_ZeroGORMImports(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}

	// Find core root dir (or internal/domain in monorepo)
	domainDir := wd
	if _, err := os.Stat(filepath.Join(domainDir, "core.go")); err != nil && filepath.Base(domainDir) != "domain" {
		domainDir = filepath.Join(wd, "internal", "domain")
	}

	entries, err := os.ReadDir(domainDir)
	if err != nil {
		t.Fatalf("failed to read domain directory: %v", err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}

		filePath := filepath.Join(domainDir, entry.Name())
		f, err := parser.ParseFile(fset, filePath, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("failed to parse %s: %v", filePath, err)
		}

		for _, imp := range f.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(impPath, "gorm.io") || strings.Contains(impPath, "gorm") {
				t.Errorf("file %s has forbidden ORM import: %s", entry.Name(), impPath)
			}
		}
	}
}

// TestNormalizableInterface verifies that domain entities implement core.Normalizable.
func TestNormalizableInterface(_ *testing.T) {
	// Compile-time interface assertions ensuring domain entities with alias fields implement core.Normalizable for pre-persistence normalization hooks.
	var (
		_ core.Normalizable = (*core.Preset)(nil)
		_ core.Normalizable = (*core.Branch)(nil)
		_ core.Normalizable = (*core.BranchVersion)(nil)
	)
}

// TestTailoringStrategy_Normalize verifies pure Go normalization for TailoringStrategy.
func TestTailoringStrategy_Normalize(t *testing.T) {
	theme := "theme-1"
	preset := &core.Preset{
		Language:         "",
		SuggestedThemeID: &theme,
	}

	if err := preset.Normalize(); err != nil {
		t.Fatalf("unexpected normalization error: %v", err)
	}

	if preset.Language != core.DefaultLanguage {
		t.Errorf("expected language %s, got %s", core.DefaultLanguage, preset.Language)
	}
	if preset.TemplateSetID != "theme-1" {
		t.Errorf("expected TemplateSetID 'theme-1', got %s", preset.TemplateSetID)
	}

	// Reverse sync
	preset2 := &core.Preset{
		TemplateSetID: "theme-2",
	}
	if err := preset2.Normalize(); err != nil {
		t.Fatalf("unexpected normalization error: %v", err)
	}
	if preset2.SuggestedThemeID == nil || *preset2.SuggestedThemeID != "theme-2" {
		t.Errorf("expected SuggestedThemeID 'theme-2', got %v", preset2.SuggestedThemeID)
	}
}

// TestTargetResume_Normalize verifies pure Go normalization for TargetResume.
func TestTargetResume_Normalize(t *testing.T) {
	presetID := "preset-1"
	resume := &core.Branch{
		PresetID:           &presetID,
		BranchInstructions: "Focus on distributed systems",
	}

	if err := resume.Normalize(); err != nil {
		t.Fatalf("unexpected normalization error: %v", err)
	}

	if resume.TailoringStrategyID == nil || *resume.TailoringStrategyID != "preset-1" {
		t.Errorf("expected TailoringStrategyID 'preset-1', got %v", resume.TailoringStrategyID)
	}
	if resume.CustomDirectives != "Focus on distributed systems" {
		t.Errorf("expected CustomDirectives 'Focus on distributed systems', got %s", resume.CustomDirectives)
	}

	// Reverse sync
	presetID2 := "preset-2"
	resume2 := &core.Branch{
		TailoringStrategyID: &presetID2,
		CustomDirectives:    "Directives 2",
	}
	if err := resume2.Normalize(); err != nil {
		t.Fatalf("unexpected normalization error: %v", err)
	}
	if resume2.PresetID == nil || *resume2.PresetID != "preset-2" {
		t.Errorf("expected PresetID 'preset-2', got %v", resume2.PresetID)
	}
	if resume2.BranchInstructions != "Directives 2" {
		t.Errorf("expected BranchInstructions 'Directives 2', got %s", resume2.BranchInstructions)
	}
}

// TestResumeVersion_Normalize verifies pure Go normalization for ResumeVersion.
func TestResumeVersion_Normalize(t *testing.T) {
	ver := &core.BranchVersion{
		BranchID: "branch-1",
	}

	if err := ver.Normalize(); err != nil {
		t.Fatalf("unexpected normalization error: %v", err)
	}

	if ver.ResumeID != "branch-1" {
		t.Errorf("expected ResumeID 'branch-1', got %s", ver.ResumeID)
	}

	ver2 := &core.BranchVersion{
		ResumeID: "branch-2",
	}
	if err := ver2.Normalize(); err != nil {
		t.Fatalf("unexpected normalization error: %v", err)
	}
	if ver2.BranchID != "branch-2" {
		t.Errorf("expected BranchID 'branch-2', got %s", ver2.BranchID)
	}
}
