package needsme

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Brief invariant 6: a verdict is a hint, and nothing acts on one beyond a local
// marker. The surest way to keep that true is for this package to be unable to reach
// what acts on mail, or the network, at all: it reads the mirror, asks the gate, and
// writes one verdict row. A caller that wants more has a person's click in between.
func TestNeedsMeCannotReachAnythingThatActsOnMail(t *testing.T) {
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
					t.Errorf("%s imports %s; needsme must not be able to act on mail or reach the network itself", name, path)
				}
			}
		}
	}
}
