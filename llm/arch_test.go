package llm

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoProviderEndpointOutsideTheGate is the architecture test from
// TESTING.md section 4: nothing that costs money is reachable except through the
// one gate. The provider clients are unexported here, so the only remaining way
// another package could reach a paid endpoint is to name its URL itself. This
// test fails if any production package outside llm/ (and the mailworld fake)
// does.
func TestNoProviderEndpointOutsideTheGate(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		`"/embeddings"`,
		`"/api/embed"`,
		`"/api/embeddings"`,
		`"/systemone"`,
		`"/chat/completions"`,
	}
	var offenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "web", "docs", ".dev", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "llm/") || strings.HasPrefix(rel, "internal/mailworld/") {
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // G304: a path this test built by walking the repo
		if err != nil {
			return err
		}
		for _, f := range forbidden {
			if bytes.Contains(data, []byte(f)) {
				offenders = append(offenders, rel+" contains "+f)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("paid endpoint reachable outside the llm gate:\n%s", strings.Join(offenders, "\n"))
	}
}
