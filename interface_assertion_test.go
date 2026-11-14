package core_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allowedProductionAssertionTypes enumerates the concrete value types permitted in runtime
// type assertions across non-test files (strictly for untyped context.Value, sync.Pool,
// Metadata map[string]any, JSON Schema AST traversal, and template FuncMap boundaries).
var allowedProductionAssertionTypes = map[string]bool{
	"bool":           true,
	"string":         true,
	"*bytes.Buffer":  true,
	"[]string":       true,
	"[]any":          true,
	"map[string]any": true,
}

// allowedProductionTypeSwitchCases enumerates the additional case types permitted in
// `switch x.(type)` statements across non-test files, on top of allowedProductionAssertionTypes.
// Entries are concrete value types decoded at untyped boundaries (SQL scanning in date.go and
// template FuncMap formatting in services/renderer/funcmap.go). fmt.Stringer is the single
// interface exception: the template formatter renders arbitrary values and falls back to
// String() for display only, never to probe for optional domain capabilities.
var allowedProductionTypeSwitchCases = map[string]bool{
	"nil":          true,
	"[]byte":       true,
	"*string":      true,
	"time.Time":    true,
	"*time.Time":   true,
	"core.Date":    true,
	"*core.Date":   true,
	"fmt.Stringer": true,
}

// TestNoInterfaceTypeAssertions ensures non-test Go files do not probe optional or
// sidecar interfaces via runtime type assertions or type-switch clauses.
func TestNoInterfaceTypeAssertions(t *testing.T) {
	repoRoot := findRepoRoot(t)
	fset := token.NewFileSet()
	var violations []string

	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "web" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}

		relPath, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			relPath = path
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", relPath, readErr)
		}
		fileViolations, collectErr := collectAssertionViolations(fset, relPath, src)
		if collectErr != nil {
			return collectErr
		}
		violations = append(violations, fileViolations...)
		return nil
	})
	if err != nil {
		t.Fatalf("walking repository for interface type assertion check: %v", err)
	}

	if len(violations) > 0 {
		t.Errorf("found %d forbidden interface type assertion(s):\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

// TestCollectAssertionViolations proves the guard is falsifiable: forbidden named, qualified,
// and inline interface assertions or type-switch cases must be reported, while allowlisted
// boundary types must not.
func TestCollectAssertionViolations(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantCount    int
		wantContains string
	}{
		{
			name:         "named interface assertion",
			body:         `_ = x.(UsageCompleter)`,
			wantCount:    1,
			wantContains: "forbidden type assertion to UsageCompleter",
		},
		{
			name:         "qualified interface assertion",
			body:         `_ = x.(core.UserRepository)`,
			wantCount:    1,
			wantContains: "forbidden type assertion to core.UserRepository",
		},
		{
			name:         "inline interface assertion",
			body:         `_ = x.(interface{ SimulateATS() })`,
			wantCount:    1,
			wantContains: "forbidden type assertion to interface{SimulateATS()}",
		},
		{
			name:         "named interface type switch case",
			body:         `switch x.(type) { case UsageCompleter: }`,
			wantCount:    1,
			wantContains: "forbidden type switch case UsageCompleter",
		},
		{
			name:         "qualified interface type switch case",
			body:         `switch r := x.(type) { case core.UserRepository: _ = r }`,
			wantCount:    1,
			wantContains: "forbidden type switch case core.UserRepository",
		},
		{
			name:         "inline interface type switch case",
			body:         `switch x.(type) { case interface{ GenerateGapAnalysis() }: }`,
			wantCount:    1,
			wantContains: "forbidden type switch case interface{GenerateGapAnalysis()}",
		},
		{
			name:         "forbidden case mixed into an allowed case list",
			body:         `switch x.(type) { case string, core.UserRepository: }`,
			wantCount:    1,
			wantContains: "forbidden type switch case core.UserRepository",
		},
		{
			name:      "allowlisted assertions",
			body:      `_ = x.(string); _ = x.(map[string]any); _ = x.(*bytes.Buffer)`,
			wantCount: 0,
		},
		{
			name:      "allowlisted type switch cases including default",
			body:      `switch v := x.(type) { case nil: case string, []any: case time.Time, *core.Date: case fmt.Stringer: _ = v; default: }`,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "package p\n\nfunc f(x any) {\n\t" + tt.body + "\n}\n"
			got, err := collectAssertionViolations(token.NewFileSet(), "synthetic.go", src)
			if err != nil {
				t.Fatalf("collectAssertionViolations returned error: %v", err)
			}
			if len(got) != tt.wantCount {
				t.Fatalf("expected %d violation(s), got %d: %v", tt.wantCount, len(got), got)
			}
			if tt.wantContains != "" && !strings.Contains(got[0], tt.wantContains) {
				t.Errorf("expected violation to contain %q, got %q", tt.wantContains, got[0])
			}
		})
	}

	t.Run("unparseable source returns error", func(t *testing.T) {
		if _, err := collectAssertionViolations(token.NewFileSet(), "broken.go", "package p\nfunc {"); err == nil {
			t.Fatal("expected parse error for malformed source")
		}
	})
}

// collectAssertionViolations parses a single Go source file and returns every type assertion
// and type-switch case whose type is not allowlisted. relPath labels the returned messages and
// src is passed to parser.ParseFile.
func collectAssertionViolations(fset *token.FileSet, relPath string, src any) ([]string, error) {
	f, err := parser.ParseFile(fset, relPath, src, 0)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", relPath, err)
	}

	var violations []string
	report := func(pos token.Pos, format string, typeStr string) {
		violations = append(violations, fmt.Sprintf("%s:%d: "+format, relPath, fset.Position(pos).Line, typeStr))
	}

	ast.Inspect(f, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.TypeAssertExpr:
			// A nil Type is the `x.(type)` guard of a type switch; its cases are checked below.
			if n.Type == nil {
				return true
			}
			typeStr := types.ExprString(n.Type)
			if !allowedProductionAssertionTypes[typeStr] {
				report(n.Pos(), "forbidden type assertion to %s; use a cohesive typed interface instead", typeStr)
			}
		case *ast.TypeSwitchStmt:
			for _, stmt := range n.Body.List {
				cc, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expr := range cc.List {
					typeStr := types.ExprString(expr)
					if !allowedProductionAssertionTypes[typeStr] && !allowedProductionTypeSwitchCases[typeStr] {
						report(expr.Pos(), "forbidden type switch case %s; use a cohesive typed interface or allowlist a concrete boundary type", typeStr)
					}
				}
			}
		}
		return true
	})

	return violations, nil
}
