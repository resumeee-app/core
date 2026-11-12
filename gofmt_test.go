package core_test

import (
	"bytes"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoFmtCompliance verifies that all Go source files in the repository are formatted with gofmt.
func TestGoFmtCompliance(t *testing.T) {
	repoRoot := findRepoRoot(t)

	var unformatted []string
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
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		formatted, fmtErr := format.Source(src)
		if fmtErr != nil {
			t.Errorf("failed to format %s: %v", path, fmtErr)
			return nil
		}
		if !bytes.Equal(src, formatted) {
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				rel = path
			}
			unformatted = append(unformatted, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking repository for gofmt check: %v", err)
	}
	if len(unformatted) > 0 {
		t.Fatalf("the following Go files are not formatted with gofmt (run `gofmt -w .`):\n  %s", strings.Join(unformatted, "\n  "))
	}
}
