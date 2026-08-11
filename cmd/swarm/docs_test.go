package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// A command that exists and is written up nowhere is a command nobody runs.
// Four of them had drifted out of the README before anyone noticed, and the
// only reason it was noticed at all is that somebody went looking.
//
// The list comes from the switch in main.go, read through go/ast, and the
// check is whether "swarm <name>" appears in the README. Neither step reads a
// sentence: one is Go source, the other is a substring.
func TestEveryCommandIsInTheReadme(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}

	// help lists the commands itself, so it needs no entry of its own.
	skip := map[string]bool{"help": true}

	for _, aliases := range commandCases(t) {
		documented, named := false, []string{}
		for _, name := range aliases {
			// "-version" and friends are flags spelled as commands, not
			// commands.
			if strings.HasPrefix(name, "-") || skip[name] {
				documented = true
				break
			}
			named = append(named, name)
			if strings.Contains(string(readme), "swarm "+name) {
				documented = true
				break
			}
		}
		if !documented {
			t.Errorf("`swarm %s` is a command the README never mentions",
				strings.Join(named, "`/`swarm "))
		}
	}
}

// commandCases returns the names in each case of main's command switch, one
// slice per case: `case "ls", "list":` gives one entry of two names, because
// documenting either is documenting the command.
func commandCases(t *testing.T) [][]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var out [][]string
	ast.Inspect(file, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		var names []string
		for _, expr := range clause.List {
			lit, ok := expr.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			names = append(names, strings.Trim(lit.Value, `"`))
		}
		if len(names) > 0 {
			out = append(out, names)
		}
		return true
	})
	if len(out) < 15 {
		t.Fatalf("found %d command cases in main.go; has the switch moved?", len(out))
	}
	return out
}
