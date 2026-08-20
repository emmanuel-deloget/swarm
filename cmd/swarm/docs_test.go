package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"
)

// A command that exists and is written up nowhere is a command nobody runs.
// Four of them had drifted out of the documentation before anyone noticed, and
// the only reason it was noticed at all is that somebody went looking.
//
// The list comes from the switch in main.go, read through go/ast, and the
// check is whether "swarm <name>" appears anywhere in the prose. Neither step
// reads a sentence: one is Go source, the other is a substring.
//
// Anywhere, and not in README.md, because where a command is written up is a
// question of how the documents are arranged — and a test that names one file
// stops being about commands the moment the reference for them moves. It would
// then either pass on a stale copy or force the prose to stay where the test
// expects it.
func TestEveryCommandIsDocumented(t *testing.T) {
	prose := allProse(t)

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
			if strings.Contains(prose, "swarm "+name) {
				documented = true
				break
			}
		}
		if !documented {
			t.Errorf("`swarm %s` is a command no document mentions",
				strings.Join(named, "`/`swarm "))
		}
	}
}

// allProse is every document a reader could learn this project from, as one
// string: the README, and everything under docs/.
//
// Read from disk rather than listed here, so a document added tomorrow is
// searched without anybody remembering to say so.
func allProse(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	paths := []string{"../../README.md", "../../CONTRIBUTING.md"}
	if entries, err := os.ReadDir("../../docs"); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				paths = append(paths, "../../docs/"+e.Name())
			}
		}
	}
	found := 0
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil {
			continue // CONTRIBUTING.md need not exist yet
		}
		found++
		b.Write(body)
		b.WriteString("\n")
	}
	if found < 2 {
		t.Fatalf("only %d document(s) found to search; the paths are wrong", found)
	}
	return b.String()
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

// commandLine matches a line of the usage listing: two spaces, the command,
// then the gap before its description.
var commandLine = regexp.MustCompile(`^(  \S[^\t]*?)(\s\s+)(\S.*)$`)

// TestTheUsageListingIsAligned.
//
// `ls` sat one column left of everything else for a while, which nobody sees
// while editing the string and everybody sees in a terminal. It is the sort of
// defect a person has to notice, report and have fixed — three exchanges for
// one space — and the sort a machine can hold to for nothing.
func TestTheUsageListingIsAligned(t *testing.T) {
	columns := map[int][]string{}
	for _, line := range strings.Split(usage, "\n") {
		m := commandLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		columns[len(m[1])+len(m[2])] = append(columns[len(m[1])+len(m[2])], strings.TrimSpace(m[1]))
	}
	if len(columns) <= 1 {
		return
	}

	// The one most lines agree on is the right one; the rest are the mistake.
	best, most := 0, 0
	for col, cmds := range columns {
		if len(cmds) > most {
			best, most = col, len(cmds)
		}
	}
	for col, cmds := range columns {
		if col != best {
			t.Errorf("`%s` describes itself at column %d, where %d other commands "+
				"use column %d", strings.Join(cmds, "`, `"), col, most, best)
		}
	}
}
