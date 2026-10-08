package llm

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// These are the architecture tests from TESTING.md section 4: nothing that costs
// money is reachable except through the one gate. Each scan is a function over a
// directory so the same code is shown to catch a deliberately violating file.

// forbiddenEndpoints are the provider paths no package but llm may name.
var forbiddenEndpoints = []string{
	`"/embeddings"`,
	`"/api/embed"`,
	`"/api/embeddings"`,
	`"/systemone"`,
	`"/chat/completions"`,
	`"/vision"`,
}

// productionGo calls visit on every non-test Go file under root, skipping
// directories that hold no Ivy code, with the path relative to root.
func productionGo(t *testing.T, root string, visit func(rel, path string)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		visit(filepath.ToSlash(rel), path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// outsideTheGate is true for code that may legitimately name a provider: the llm
// package itself and the fake provider.
func outsideTheGate(rel string) bool {
	return !strings.HasPrefix(rel, "llm/") && !strings.HasPrefix(rel, "internal/mailworld/")
}

// scanEndpoints reports every production file outside the gate that names a
// provider endpoint.
func scanEndpoints(t *testing.T, root string) []string {
	t.Helper()
	var offenders []string
	productionGo(t, root, func(rel, path string) {
		if !outsideTheGate(rel) {
			return
		}
		data, err := os.ReadFile(path) //nolint:gosec // G304: a path this test built by walking the repo
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range forbiddenEndpoints {
			if bytes.Contains(data, []byte(f)) {
				offenders = append(offenders, rel+" contains "+f)
			}
		}
	})
	return offenders
}

// Assertion 1: no endpoint string outside llm/.
func TestNoProviderEndpointOutsideTheGate(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if offenders := scanEndpoints(t, root); len(offenders) > 0 {
		t.Fatalf("paid endpoint reachable outside the llm gate:\n%s", strings.Join(offenders, "\n"))
	}
}

func TestEndpointScanCatchesAViolatingFile(t *testing.T) {
	t.Parallel()
	for _, endpoint := range forbiddenEndpoints {
		root := t.TempDir()
		writeGo(t, root, "rogue/call.go", "package rogue\n\nconst url = "+endpoint+"\n")
		if got := scanEndpoints(t, root); len(got) != 1 {
			t.Errorf("%s: offenders = %v, want the rogue file reported", endpoint, got)
		}
	}
	root := t.TempDir()
	writeGo(t, root, "llm/ok.go", "package llm\n\nconst url = \"/systemone\"\n")
	if got := scanEndpoints(t, root); len(got) != 0 {
		t.Errorf("the gate itself was reported: %v", got)
	}
}

// bannedExports are names that would let another package hold a provider client.
var bannedExports = []string{"NewOpenRouter", "NewOllama", "Embedder", "Jev", "Chat", "Vision"}

// clientMethods are the methods that make a type a provider client. The Gate has
// them too, and is the one type allowed to.
var clientMethods = []string{"Call", "Embed", "Complete", "See", "Decide"}

// scanExports parses the llm package in dir and reports any exported identifier
// that would hand out a provider client: a banned name, or a function returning a
// package type (other than the Gate) that has a client method.
func scanExports(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, file)
	}

	withClientMethod := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			if slices.Contains(clientMethods, fn.Name.Name) {
				withClientMethod[receiverName(fn.Recv.List[0].Type)] = true
			}
		}
	}
	var offenders []string
	for _, file := range files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil || !d.Name.IsExported() {
					continue
				}
				if slices.Contains(bannedExports, d.Name.Name) {
					offenders = append(offenders, "func "+d.Name.Name+" is a banned export")
				}
				if d.Type.Results == nil {
					continue
				}
				for _, res := range d.Type.Results.List {
					if name := receiverName(res.Type); name != "Gate" && withClientMethod[name] {
						offenders = append(offenders, "func "+d.Name.Name+" returns "+name+", a provider client")
					}
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() && slices.Contains(bannedExports, ts.Name.Name) {
						offenders = append(offenders, "type "+ts.Name.Name+" is a banned export")
					}
				}
			}
		}
	}
	return offenders
}

// receiverName is the name of the type a receiver or result expression refers to,
// through any pointer.
func receiverName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return ""
}

// Assertion 2: no exported provider constructor or client interface.
func TestNoExportedProviderClient(t *testing.T) {
	t.Parallel()
	if offenders := scanExports(t, "."); len(offenders) > 0 {
		t.Fatalf("the llm package exports a way to reach a provider without the gate:\n%s", strings.Join(offenders, "\n"))
	}
}

func TestExportScanCatchesAViolatingFile(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"constructor": "package llm\n\ntype Embedder interface{ Embed() }\nfunc NewOpenRouter() Embedder { return nil }\n",
		"client type": "package llm\n\ntype client struct{}\nfunc (client) Complete() {}\nfunc NewThing() *client { return nil }\n",
	}
	for name, src := range cases {
		dir := t.TempDir()
		writeGo(t, dir, "bad.go", src)
		if got := scanExports(t, dir); len(got) == 0 {
			t.Errorf("%s: nothing reported", name)
		}
	}
	dir := t.TempDir()
	writeGo(t, dir, "good.go", "package llm\n\ntype Gate struct{}\nfunc (*Gate) Embed() {}\nfunc NewGate() *Gate { return nil }\n")
	if got := scanExports(t, dir); len(got) != 0 {
		t.Errorf("the gate's own constructor was reported: %v", got)
	}
}

// scanFeatureLiterals reports every `Feature: "name"` outside the gate whose name
// is not in the feature table, so a typo cannot ship as a feature the gate would
// refuse only at runtime.
func scanFeatureLiterals(t *testing.T, root string) []string {
	t.Helper()
	var offenders []string
	productionGo(t, root, func(rel, path string) {
		if !outsideTheGate(rel) {
			return
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := kv.Key.(*ast.Ident)
			lit, isLit := kv.Value.(*ast.BasicLit)
			if !ok || key.Name != "Feature" || !isLit || lit.Kind != token.STRING {
				return true
			}
			name, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if _, known := features[name]; !known {
				offenders = append(offenders, rel+" names the unknown feature "+lit.Value)
			}
			return true
		})
	})
	return offenders
}

// Assertion 3: every feature named in a gate call is in the feature table.
func TestEveryNamedFeatureIsInTheTable(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if offenders := scanFeatureLiterals(t, root); len(offenders) > 0 {
		t.Fatalf("features the gate would refuse:\n%s", strings.Join(offenders, "\n"))
	}
}

func TestFeatureScanCatchesAnUnknownName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGo(t, root, "search/x.go", "package search\n\ntype R struct{ Feature string }\n\nvar bad = R{Feature: \"serch\"}\nvar good = R{Feature: \"search\"}\n")
	got := scanFeatureLiterals(t, root)
	if len(got) != 1 || !strings.Contains(got[0], `"serch"`) {
		t.Fatalf("offenders = %v, want only the misspelt feature", got)
	}
}

func writeGo(t *testing.T, root, rel, src string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
}
