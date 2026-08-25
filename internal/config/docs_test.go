package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Every key this package understands has to appear in the reference, in the
// section it belongs to. A key that only exists in the code is a key nobody
// can use; one documented under the wrong heading sends the reader to a block
// where it will not work.
//
// Both halves are read structurally rather than interpreted: the keys come
// from the struct tags through go/ast, and the document is cut at its own ##
// headings. Nothing here reads a sentence, so nothing here can be wrong about
// what a sentence meant.

// docSections says where each config struct's keys are documented. A struct
// may have more than one home: an agent inherits every key in `defaults`, and
// has a few of its own under `agents`.
var docSections = map[string][]string{
	"Config":          {"Top level"},
	"AgentDefaults":   {"`defaults`"},
	"AgentConfig":     {"`defaults`", "`agents`"},
	"PatternConfig":   {"`patterns`"},
	"WebConfig":       {"`web`"},
	"BusConfig":       {"`bus`"},
	"EphemeralConfig": {"`ephemeral`"},
	"NudgeRule":       {"`on_stalled`", "`on_idle`"},
	"BusBudget":       {"`budget`"},
	"AgentBudget":     {"`budget`"},
	"HookConfig":      {"`hooks`"},
	"OutgoingConfig":  {"`outgoing`"},
}

func TestEveryKeyIsDocumentedWhereItBelongs(t *testing.T) {
	doc, err := os.ReadFile("../../docs/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	sections := splitSections(string(doc))

	for _, structName := range sortedKeys(docSections) {
		want := docSections[structName]
		for _, key := range yamlKeys(t, structName) {
			found := false
			for _, s := range want {
				if strings.Contains(sections[s], "`"+key+"`") {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s.%s: `%s` is not documented under %s",
					structName, key, key, strings.Join(want, " or "))
			}
		}
	}

	// And a struct nobody thought to place is caught here rather than by a
	// reader: adding a config type means saying where its keys are written up.
	for _, name := range structsWithYAMLTags(t) {
		if _, ok := docSections[name]; !ok {
			t.Errorf("%s carries yaml tags but docSections does not say which "+
				"section of configuration.md documents them", name)
		}
	}
}

// splitSections cuts the document at its "## " headings and returns the body
// under each, keyed by the heading text.
func splitSections(doc string) map[string]string {
	out := map[string]string{}
	var current string
	var body strings.Builder
	for _, line := range strings.Split(doc, "\n") {
		if title, ok := strings.CutPrefix(line, "## "); ok {
			if current != "" {
				out[current] = body.String()
			}
			current, body = strings.TrimSpace(title), strings.Builder{}
			continue
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	if current != "" {
		out[current] = body.String()
	}
	return out
}

// yamlKeys returns the yaml tag of every field of a struct, by name.
func yamlKeys(t *testing.T, structName string) []string {
	t.Helper()
	var keys []string
	forEachStruct(t, func(name string, st *ast.StructType) {
		if name != structName {
			return
		}
		for _, f := range st.Fields.List {
			if f.Tag == nil {
				continue
			}
			tag := reflect.StructTag(strings.Trim(f.Tag.Value, "`")).Get("yaml")
			if key, _, _ := strings.Cut(tag, ","); key != "" && key != "-" {
				keys = append(keys, key)
			}
		}
	})
	if len(keys) == 0 {
		t.Fatalf("no yaml keys found on %s; has it been renamed?", structName)
	}
	return keys
}

// structsWithYAMLTags names every struct in this package that has at least one
// yaml tag.
func structsWithYAMLTags(t *testing.T) []string {
	t.Helper()
	var names []string
	forEachStruct(t, func(name string, st *ast.StructType) {
		for _, f := range st.Fields.List {
			if f.Tag != nil && strings.Contains(f.Tag.Value, "yaml:") {
				names = append(names, name)
				return
			}
		}
	})
	return names
}

func forEachStruct(t *testing.T, fn func(name string, st *ast.StructType)) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "config.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		if st, ok := spec.Type.(*ast.StructType); ok {
			fn(spec.Name.Name, st)
		}
		return true
	})
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
