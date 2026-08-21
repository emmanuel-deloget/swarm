package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A flag that is accepted and ignored is worse than one that does not exist:
// `swarm bus stats -json` printed prose, exited zero, and said nothing about
// it. Twelve of the twenty commands did that, because -json is registered once
// for every client and honoured one command at a time.
//
// So: a command that registers it has to use it. One that has nothing to
// serialise — attach hands over a terminal, logs pours out bytes — calls
// registerWithout and does not offer the flag at all.
func TestEveryCommandThatOffersJSONPrintsIt(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	checked := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			from := fset.Position(fn.Pos()).Offset
			to := fset.Position(fn.End()).Offset
			body := string(src[from:to])
			if !strings.Contains(body, ".register(fs)") {
				continue
			}
			checked++
			if !strings.Contains(body, "asJSON") {
				t.Errorf("%s in %s offers -json and never prints any: use it, "+
					"or registerWithout", fn.Name.Name, filepath.Base(name))
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d commands register the client flags; has register moved?", checked)
	}
}
