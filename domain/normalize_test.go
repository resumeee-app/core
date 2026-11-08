package domain_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"resumeee/internal/domain"
)

// TestDomainPurity_ZeroGORMImports verifies that internal/domain contains zero imports of gorm.io/gorm.
func TestDomainPurity_ZeroGORMImports(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}

	// Find internal/domain dir
	domainDir := wd
	if filepath.Base(domainDir) != "domain" {
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

// TestNormalizableInterface verifies that domain entities implement domain.Normalizable.
func TestNormalizableInterface(t *testing.T) {
	var _ domain.Normalizable = (*domain.TailoringStrategy)(nil)
	var _ domain.Normalizable = (*domain.TargetResume)(nil)
	var _ domain.Normalizable = (*domain.ResumeVersion)(nil)
}

// TestTailoringStrategy_Normalize verifies pure Go normalization for TailoringStrategy.
func TestTailoringStrategy_Normalize(t *testing.T) {
	theme := "theme-1"
	strat := &domain.TailoringStrategy{
		Language:         "",
		SuggestedThemeID: &theme,
	}

	if err := strat.Normalize(); err != nil {
		t.Fatalf("unexpected normalization error: %v", err)
	}

	if strat.Language != domain.DefaultLanguage {
		t.Errorf("expected language %s, got %s", domain.DefaultLanguage, strat.Language)
	}
	if strat.TemplateSetID != "theme-1" {
		t.Errorf("expected TemplateSetID 'theme-1', got %s", strat.TemplateSetID)
	}

	// Reverse sync
	strat2 := &domain.TailoringStrategy{
		TemplateSetID: "theme-2",
	}
	if err := strat2.Normalize(); err != nil {
		t.Fatalf("unexpected normalization error: %v", err)
	}
	if strat2.SuggestedThemeID == nil || *strat2.SuggestedThemeID != "theme-2" {
		t.Errorf("expected SuggestedThemeID 'theme-2', got %v", strat2.SuggestedThemeID)
	}
}

// TestTargetResume_Normalize verifies pure Go normalization for TargetResume.
func TestTargetResume_Normalize(t *testing.T) {
	presetID := "preset-1"
	resume := &domain.TargetResume{
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
	stratID := "strat-2"
	resume2 := &domain.TargetResume{
		TailoringStrategyID: &stratID,
		CustomDirectives:    "Directives 2",
	}
	if err := resume2.Normalize(); err != nil {
		t.Fatalf("unexpected normalization error: %v", err)
	}
	if resume2.PresetID == nil || *resume2.PresetID != "strat-2" {
		t.Errorf("expected PresetID 'strat-2', got %v", resume2.PresetID)
	}
	if resume2.BranchInstructions != "Directives 2" {
		t.Errorf("expected BranchInstructions 'Directives 2', got %s", resume2.BranchInstructions)
	}
}

// TestResumeVersion_Normalize verifies pure Go normalization for ResumeVersion.
func TestResumeVersion_Normalize(t *testing.T) {
	ver := &domain.ResumeVersion{
		BranchID: "branch-1",
	}

	if err := ver.Normalize(); err != nil {
		t.Fatalf("unexpected normalization error: %v", err)
	}

	if ver.ResumeID != "branch-1" {
		t.Errorf("expected ResumeID 'branch-1', got %s", ver.ResumeID)
	}

	ver2 := &domain.ResumeVersion{
		ResumeID: "branch-2",
	}
	if err := ver2.Normalize(); err != nil {
		t.Fatalf("unexpected normalization error: %v", err)
	}
	if ver2.BranchID != "branch-2" {
		t.Errorf("expected BranchID 'branch-2', got %s", ver2.BranchID)
	}
}
