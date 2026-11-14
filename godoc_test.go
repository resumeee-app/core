package core_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
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
	if len(pkgDirs) == 0 {
		t.Fatalf("expected at least 1 Go package in repository, found 0")
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

// TestSymbolDocComments enforces that every exported and unexported symbol (function, method,
// type, constant, and package-level variable) plus every grouped const/var block in non-test
// Go files carries a doc comment starting with the exact identifier name.
func TestSymbolDocComments(t *testing.T) {
	repoRoot := findRepoRoot(t)
	pkgDirs := discoverPackageDirectories(t, repoRoot)
	fset := token.NewFileSet()
	var violations []string

	for _, dir := range pkgDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("failed to read dir %s: %v", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}

			filePath := filepath.Join(dir, entry.Name())
			relPath, relErr := filepath.Rel(repoRoot, filePath)
			if relErr != nil {
				relPath = filePath
			}

			f, parseErr := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
			if parseErr != nil {
				t.Fatalf("failed to parse %s: %v", relPath, parseErr)
			}

			violations = append(violations, collectFileDocViolations(fset, relPath, f)...)
		}
	}

	if len(violations) > 0 {
		t.Errorf("found %d symbol doc comment violation(s):\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

// hasSymbolDocPrefix reports whether doc is non-nil and starts with name followed by a non-identifier rune.
func hasSymbolDocPrefix(doc *ast.CommentGroup, name string) bool {
	if doc == nil {
		return false
	}
	text := strings.TrimSpace(doc.Text())
	if !strings.HasPrefix(text, name) || len(text) <= len(name) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(text[len(name):])
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
}

// hasNonEmptyDoc reports whether doc contains non-whitespace commentary.
func hasNonEmptyDoc(doc *ast.CommentGroup) bool {
	return doc != nil && strings.TrimSpace(doc.Text()) != ""
}

// collectFileDocViolations inspects all top-level declarations in a single parsed AST file.
func collectFileDocViolations(fset *token.FileSet, relPath string, f *ast.File) []string {
	var violations []string

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !hasSymbolDocPrefix(d.Doc, d.Name.Name) {
				line := fset.Position(d.Pos()).Line
				violations = append(violations, fmt.Sprintf("%s:%d: function %s must have a doc comment starting with %q", relPath, line, d.Name.Name, d.Name.Name))
			}
		case *ast.GenDecl:
			violations = append(violations, collectGenDeclDocViolations(fset, relPath, d)...)
		}
	}

	return violations
}

// collectGenDeclDocViolations checks type, const, and var declarations against AGENTS.md doc rules.
func collectGenDeclDocViolations(fset *token.FileSet, relPath string, decl *ast.GenDecl) []string {
	switch decl.Tok {
	case token.IMPORT:
		return nil
	case token.TYPE:
		return collectTypeDocViolations(fset, relPath, decl)
	case token.CONST:
		return collectValueDeclDocViolations(fset, relPath, decl, "const")
	case token.VAR:
		if allBlankIdents(decl) {
			return nil
		}
		return collectValueDeclDocViolations(fset, relPath, decl, "var")
	default:
		return nil
	}
}

// collectTypeDocViolations verifies doc comments on single or grouped type declarations.
func collectTypeDocViolations(fset *token.FileSet, relPath string, decl *ast.GenDecl) []string {
	var violations []string
	for _, spec := range decl.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		doc := ts.Doc
		if !decl.Lparen.IsValid() && len(decl.Specs) == 1 {
			doc = decl.Doc
		}
		if !hasSymbolDocPrefix(doc, ts.Name.Name) {
			line := fset.Position(ts.Pos()).Line
			violations = append(violations, fmt.Sprintf("%s:%d: type %s must have a doc comment starting with %q", relPath, line, ts.Name.Name, ts.Name.Name))
		}
	}
	return violations
}

// collectValueDeclDocViolations verifies grouped block headers and per-identifier doc comments on const/var declarations.
func collectValueDeclDocViolations(fset *token.FileSet, relPath string, decl *ast.GenDecl, kind string) []string {
	var violations []string
	grouped := decl.Lparen.IsValid()

	if grouped && !hasNonEmptyDoc(decl.Doc) {
		line := fset.Position(decl.Pos()).Line
		violations = append(violations, fmt.Sprintf("%s:%d: grouped %s block must have a leading doc comment", relPath, line, kind))
	}

	for _, spec := range decl.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		doc := decl.Doc
		if grouped {
			doc = vs.Doc
		}
		for _, ident := range vs.Names {
			if ident.Name == "_" {
				continue
			}
			if !hasSymbolDocPrefix(doc, ident.Name) {
				line := fset.Position(ident.Pos()).Line
				violations = append(violations, fmt.Sprintf("%s:%d: %s %s must have a doc comment starting with %q", relPath, line, kind, ident.Name, ident.Name))
			}
		}
	}

	return violations
}

// allBlankIdents reports whether every declared identifier in decl is the blank identifier "_".
func allBlankIdents(decl *ast.GenDecl) bool {
	hasAny := false
	for _, spec := range decl.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, ident := range vs.Names {
			hasAny = true
			if ident.Name != "_" {
				return false
			}
		}
	}
	return hasAny
}

func findRepoRoot(t *testing.T) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}
	if realDir, err := filepath.EvalSymlinks(dir); err == nil {
		dir = realDir
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
