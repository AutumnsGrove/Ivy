package waiting

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Brief invariant 6: a list of waits is a view, and nothing acts on one. The surest way to
// keep that true is for this package to be unable to reach what acts on mail, or the
// network, at all: it reads the mirror, asks Jev through the engine, and writes one
// "stop waiting" row when the operator says so. Nothing is ever sent for her.
func TestWaitingCannotReachAnythingThatActsOnMail(t *testing.T) {
	forbidden := []string{
		"/smtp", "/send", "/compose", "/sync", "/update", "/backup", "/accountsvc",
		"/gateway", "net/smtp", "net/http",
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
					t.Errorf("%s imports %s; waiting must not be able to act on mail or reach the network itself", name, path)
				}
			}
		}
	}
}
