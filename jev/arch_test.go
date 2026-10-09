package jev

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Brief invariant 6: nothing acts on a probability beyond a local, reversible tag.
// The surest way to keep that true is for this package to be unable to reach what
// acts on mail at all: it reads the mirror and the gate, and it hands a verdict back.
// A package that wants to move, delete, send or unsubscribe has to be a caller of
// jev, where a person's click stands in between.
func TestJevCannotReachAnythingThatActsOnMail(t *testing.T) {
	forbidden := []string{
		"/smtp", "/send", "/compose", "/sync", "/update", "/backup", "/accountsvc",
		"/gateway", "net/smtp", "net/http/httputil",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			for _, bad := range forbidden {
				if strings.HasSuffix(path, bad) || strings.Contains(path, bad+"/") {
					t.Errorf("%s imports %s; jev must not be able to act on mail", name, path)
				}
			}
		}
	}
}
