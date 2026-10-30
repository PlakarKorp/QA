// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package apiv2

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestParity: every v1 scenario has a v2 scenario of the same name. It reads
// the test functions out of both packages' sources, so adding a v1 scenario
// without its mirror is a red line here, naming the feature by the comment
// above the v1 function.
func TestParity(t *testing.T) {
	v1 := scenarios(t, "../v1")
	v2 := scenarios(t, ".")

	var names []string
	for name := range v1 {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := v2[name]; !ok {
			t.Errorf("v1 scenario %s has no v2 mirror: %s", name, v1[name])
		}
	}
}

// scenarios maps each Test function of a package to the first line of its
// doc comment, which is the feature it covers.
func scenarios(t *testing.T, dir string) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	fset := token.NewFileSet()
	for _, file := range files {
		f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" {
				continue
			}
			feature := ""
			if fn.Doc != nil {
				feature = strings.SplitN(strings.TrimSpace(fn.Doc.Text()), "\n", 2)[0]
			}
			found[fn.Name.Name] = feature
		}
	}
	return found
}
