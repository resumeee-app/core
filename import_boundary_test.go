package core_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// coreDomainPackageDirs enumerates all core domain packages subject to strict
// infrastructure, HTTP, database, and vendor LLM SDK isolation guarantees.
var coreDomainPackageDirs = []string{
	".",
	"store",
	"storetest",
	"seed",
	"provenance",
	"sanitizer",
	"branch",
	"profile",
	"renderer",
	"worker",
	"llm",
	"memory",
	"pdf",
}

// llmSubpackageImportPrefix identifies vendor transport and internal HTTP helpers under llm/.
const llmSubpackageImportPrefix = "github.com/resumeee-app/core/llm/"

// forbiddenDependencyPrefixes lists external dependency import paths prohibited across core domain packages.
var forbiddenDependencyPrefixes = []string{
	"gorm.io/",
	"github.com/glebarez/sqlite",
	"github.com/mattn/go-sqlite3",
	"github.com/lib/pq",
	"github.com/jackc/pgx",
	"github.com/go-sql-driver/mysql",
	"github.com/mark3labs/mcp-go",
	"github.com/google/generative-ai-go",
	"google.golang.org/genai",
	"github.com/sashabaranov/go-openai",
	"github.com/anthropics/anthropic-sdk-go",
}

// TestCorePackageSet_ImportBoundaries verifies that core domain packages remain decoupled from
// database drivers, ORMs, net/http, vendor LLM SDKs, and (in production code) llm/* transport subpackages.
func TestCorePackageSet_ImportBoundaries(t *testing.T) {
	repoRoot := findRepoRoot(t)
	if _, err := os.Stat(filepath.Join(repoRoot, "core.go")); err != nil {
		t.Fatalf("could not locate core.go at repository root %s: %v", repoRoot, err)
	}

	fset := token.NewFileSet()
	var violations []string

	for _, relDir := range coreDomainPackageDirs {
		dir := filepath.Join(repoRoot, relDir)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Fatalf("expected core domain directory %s to exist at %s", relDir, dir)
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("failed to read dir %s: %v", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}

			isTestFile := strings.HasSuffix(entry.Name(), "_test.go")
			filePath := filepath.Join(dir, entry.Name())
			relPath := filepath.Join(relDir, entry.Name())

			f, err := parser.ParseFile(fset, filePath, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("failed to parse imports from %s: %v", relPath, err)
			}

			for _, imp := range f.Imports {
				importPath := strings.Trim(imp.Path.Value, `"`)

				// Ban exact database/sql while allowing database/sql/driver in value objects like date.go.
				if importPath == "database/sql" {
					violations = append(violations, relPath+": imports forbidden standard library package database/sql")
				}

				if importPath == "net/http" || strings.HasPrefix(importPath, "net/http/") {
					violations = append(violations, relPath+": imports forbidden HTTP package "+importPath)
				}

				if !isTestFile && strings.HasPrefix(importPath, llmSubpackageImportPrefix) {
					violations = append(violations, relPath+": non-test core domain file imports vendor/internal llm subpackage "+importPath)
				}

				for _, forbidden := range forbiddenDependencyPrefixes {
					if strings.HasPrefix(importPath, forbidden) || importPath == strings.TrimSuffix(forbidden, "/") {
						violations = append(violations, relPath+": imports forbidden external dependency "+importPath)
					}
				}
			}
		}
	}

	if len(violations) > 0 {
		t.Errorf("found %d import boundary violation(s):\n%s", len(violations), strings.Join(violations, "\n"))
	}
}
