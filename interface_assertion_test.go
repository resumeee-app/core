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

// TestNoInterfaceTypeAssertions ensures non-test Go files do not probe optional or
// sidecar interfaces via runtime type assertions or inline interface type-switch clauses.
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
		violations = append(violations, collectAssertionViolations(t, fset, path, relPath)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walking repository for interface type assertion check: %v", err)
	}

	if len(violations) > 0 {
		t.Errorf("found %d forbidden interface type assertion(s):\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

// collectAssertionViolations parses a single production Go file and returns all disallowed
// type assertions and inline interface type-switch clauses.
func collectAssertionViolations(t *testing.T, fset *token.FileSet, path, relPath string) []string {
	t.Helper()

	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("failed to parse %s: %v", relPath, err)
	}

	var violations []string
	ast.Inspect(f, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.TypeAssertExpr:
			// Ignore switch x.(type) guards where Type is nil.
			if n.Type == nil {
				return true
			}
			typeStr := types.ExprString(n.Type)
			if !allowedProductionAssertionTypes[typeStr] {
				pos := fset.Position(n.Pos())
				violations = append(violations, fmt.Sprintf("%s:%d: forbidden type assertion to %s; use a cohesive typed interface instead", relPath, pos.Line, typeStr))
			}
		case *ast.TypeSwitchStmt:
			for _, stmt := range n.Body.List {
				cc, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expr := range cc.List {
					if _, isIface := expr.(*ast.InterfaceType); isIface {
						pos := fset.Position(expr.Pos())
						violations = append(violations, fmt.Sprintf("%s:%d: forbidden inline interface type switch clause %s; use a cohesive typed interface instead", relPath, pos.Line, types.ExprString(expr)))
					}
				}
			}
		}
		return true
	})

	return violations
}
