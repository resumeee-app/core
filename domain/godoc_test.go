package domain_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackageDocCommentConsistency verifies that:
// 1. Every Go package in the repository has a comprehensive package doc comment.
// 2. Exactly ONE file per package directory contains the package doc comment (f.Doc != nil).
// 3. For non-main packages, that file is named <pkgname>.go.
// 4. For package main binaries in cmd/*, that file is main.go.
// 5. The package doc comment starts with "Package <pkgname>".
func TestPackageDocCommentConsistency(t *testing.T) {
	repoRoot := findRepoRoot(t)

	// Discover all package directories containing .go files
	pkgDirs := discoverPackageDirectories(t, repoRoot)
	if len(pkgDirs) < 15 {
		t.Fatalf("expected at least 15 Go packages in repository, found %d", len(pkgDirs))
	}

	for _, dir := range pkgDirs {
		relDir, err := filepath.Rel(repoRoot, dir)
		if err != nil {
			relDir = dir
		}

		t.Run(relDir, func(t *testing.T) {
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("failed to read dir %s: %v", dir, err)
			}

			fset := token.NewFileSet()
			var docFiles []string
			var docTexts []string
			var nonTestFiles []string
			var detectedPkgName string

			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
					continue
				}

				filePath := filepath.Join(dir, entry.Name())
				f, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
				if err != nil {
					t.Fatalf("failed to parse %s: %v", filePath, err)
				}

				isTestFile := strings.HasSuffix(entry.Name(), "_test.go")
				if !isTestFile {
					nonTestFiles = append(nonTestFiles, entry.Name())
					if detectedPkgName == "" {
						detectedPkgName = f.Name.Name
					}
				}

				if f.Doc != nil {
					docText := strings.TrimSpace(f.Doc.Text())
					if docText != "" {
						docFiles = append(docFiles, entry.Name())
						docTexts = append(docTexts, docText)
					}
				}
			}

			if len(nonTestFiles) == 0 {
				t.Fatalf("package in %s has no non-test Go source files", relDir)
			}

			// If directory currently has only test files (e.g. api/ before api.go is added),
			// infer pkg name from directory name or package clause in test.
			if detectedPkgName == "" {
				detectedPkgName = filepath.Base(dir)
			}

			// Invariant 1 & 2: Exactly ONE file in the package must have the package doc comment
			if len(docFiles) == 0 {
				t.Fatalf("package %s (%s) has NO package doc comment", detectedPkgName, relDir)
			}
			if len(docFiles) > 1 {
				t.Fatalf("package %s (%s) has multiple files with package doc comments: %v (expected only 1)",
					detectedPkgName, relDir, docFiles)
			}

			docFile := docFiles[0]
			docText := docTexts[0]

			// Invariant: Doc comment must NOT be in a test file
			if strings.HasSuffix(docFile, "_test.go") {
				t.Fatalf("package doc comment for %s is in a test file (%s); must be in non-test file",
					detectedPkgName, docFile)
			}

			// Invariant 3 & 4: Expected file name
			expectedFile := detectedPkgName + ".go"
			if detectedPkgName == "main" {
				expectedFile = "main.go"
			}
			if docFile != expectedFile {
				t.Fatalf("package doc comment for %s in %s is in file %s; expected %s",
					detectedPkgName, relDir, docFile, expectedFile)
			}

			// Invariant 5: Doc comment must start with "Package <pkgname>"
			expectedPrefix := "Package " + detectedPkgName
			if !strings.HasPrefix(docText, expectedPrefix) {
				t.Fatalf("package doc comment in %s/%s does not start with %q; got: %q",
					relDir, docFile, expectedPrefix, docText)
			}

			// Invariant 6: Doc comment must be meaningful (> 20 chars)
			if len(docText) < 20 {
				t.Fatalf("package doc comment in %s/%s is too short (%d chars)", relDir, docFile, len(docText))
			}
		})
	}
}

func findRepoRoot(t *testing.T) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			realDir, evalErr := filepath.EvalSymlinks(dir)
			if evalErr == nil {
				return realDir
			}
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate repo root containing go.mod from %s", dir)
		}
		dir = parent
	}
}

func discoverPackageDirectories(t *testing.T, repoRoot string) []string {
	var pkgDirs []string
	seen := make(map[string]bool)

	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			name := d.Name()
			// Skip hidden dirs, worktrees, git, agent dirs, frontend, node_modules
			if strings.HasPrefix(name, ".") || name == "web" || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.HasSuffix(d.Name(), ".go") {
			dir := filepath.Dir(path)
			if !seen[dir] {
				seen[dir] = true
				pkgDirs = append(pkgDirs, dir)
			}
		}

		return nil
	})

	if err != nil {
		t.Fatalf("failed to walk repository directories: %v", err)
	}

	return pkgDirs
}
