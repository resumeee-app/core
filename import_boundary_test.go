package core_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allowedCoreSubpackages lists the relative subpackages permitted within the resumeee-core package set.
var allowedCoreSubpackages = []string{
	"seed",
	"validator",
	"sanitizer",
	"branch",
	"profile",
	"renderer",
}

// forbiddenDependencyPrefixes lists external dependency import paths strictly prohibited in the core package set.
var forbiddenDependencyPrefixes = []string{
	"gorm.io/",
	"github.com/glebarez/sqlite",
	"github.com/mattn/go-sqlite3",
	"github.com/lib/pq",
	"github.com/jackc/pgx",
	"github.com/go-sql-driver/mysql",
	"firebase.google.com/",
	"github.com/mark3labs/mcp-go",
	"github.com/google/generative-ai-go",
	"google.golang.org/genai",
	"github.com/sashabaranov/go-openai",
	"github.com/anthropics/anthropic-sdk-go",
}

// TestCorePackageSet_ImportBoundaries verifies that no file (production or test) in the
// resumeee-core package set imports packages outside the core set or forbidden external SDKs.
func TestCorePackageSet_ImportBoundaries(t *testing.T) {
	repoRoot := findRepoRoot(t)

	// Detect if running inside monorepo or extracted core repository
	var coreDirs []string
	if _, err := os.Stat(filepath.Join(repoRoot, "internal", "domain")); err == nil {
		// Monorepo layout
		for _, pkg := range allowedCoreSubpackages {
			dir := filepath.Join(repoRoot, "internal", pkg)
			if _, err := os.Stat(dir); err == nil {
				coreDirs = append(coreDirs, dir)
			}
		}
	} else if _, err := os.Stat(filepath.Join(repoRoot, "core.go")); err == nil {
		// Extracted core repo layout: root (.), validator, sanitizer, services/branch, services/profile, services/renderer
		extractedSubdirs := []string{
			".",
			"seed",
			"validator",
			"sanitizer",
			filepath.Join("services", "branch"),
			filepath.Join("services", "profile"),
			filepath.Join("services", "renderer"),
		}
		for _, sub := range extractedSubdirs {
			dir := filepath.Join(repoRoot, sub)
			if _, err := os.Stat(dir); err == nil {
				coreDirs = append(coreDirs, dir)
			}
		}
	} else {
		t.Fatalf("could not determine repository layout from root: %s", repoRoot)
	}

	if len(coreDirs) == 0 {
		t.Fatalf("no core package directories found under repo root: %s", repoRoot)
	}

	fset := token.NewFileSet()
	var violations []string

	for _, dir := range coreDirs {
		relDir, err := filepath.Rel(repoRoot, dir)
		if err != nil {
			relDir = dir
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("failed to read dir %s: %v", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}

			filePath := filepath.Join(dir, entry.Name())
			relPath := filepath.Join(relDir, entry.Name())

			f, err := parser.ParseFile(fset, filePath, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("failed to parse imports from %s: %v", relPath, err)
			}

			for _, imp := range f.Imports {
				importPath := strings.Trim(imp.Path.Value, `"`)

				// 1. Check for monorepo internal imports outside core package set
				monorepoRoot := "resumeee" + "/"
				if strings.HasPrefix(importPath, monorepoRoot) {
					isAllowedCore := false
					for _, pkg := range allowedCoreSubpackages {
						prefix := "resumeee" + "/internal/" + pkg
						if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
							isAllowedCore = true
							break
						}
					}
					if !isAllowedCore {
						violations = append(violations, relPath+": imports out-of-core package "+importPath)
					}
				}

				// 2. Check for forbidden external dependencies (ORMs, SQL drivers, Firebase, MCP, LLM SDKs)
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
