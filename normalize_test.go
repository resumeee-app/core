package core_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDomainPurity_ZeroGORMImports verifies that root package core contains zero imports of gorm.io/gorm.
func TestDomainPurity_ZeroGORMImports(t *testing.T) {
	domainDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
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
