package guide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/swarm/internal/config"
)

func render(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "swarm.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(c, dir); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

const plain = `
web: {enabled: false}
agents:
  - name: alpha
    command: [cat]
  - name: beta
    command: [cat]
`

// TestASimpleFleetIsToldSimpleThings: the guide is what an agent reads before
// doing anything, so every paragraph it does not need is one it may act on.
func TestASimpleFleetIsToldSimpleThings(t *testing.T) {
	out := render(t, plain)

	for _, unwanted := range []string{
		"Conversations end", "Who you may write to", "Your working copy",
		"Events from outside", "fall quiet",
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a fleet with nothing switched on was told about %q", unwanted)
		}
	}
	if !strings.Contains(out, "`alpha`") || !strings.Contains(out, "`beta`") {
		t.Error("the fleet table is the one thing that is always worth saying")
	}
}

// TestEachMechanismSpeaksForItself: switched on in the configuration, described
// in the guide — that is the whole point of generating it.
func TestEachMechanismSpeaksForItself(t *testing.T) {
	out := render(t, `
web: {enabled: false}
bus: {max_turns: 4, escalate_to: chief}
hooks: {enabled: true, from: github}
agents:
  - name: alpha
    command: [cat]
    delivery: defer
    workspace: clone
    can_send: [chief]
  - name: chief
    command: [cat]
`)
	for _, want := range []string{
		"worth 4 messages", "`chief`", "fall quiet", "Who you may write to",
		"Your working copy", "Messages from `github`", "| `alpha` | — | defer | clone |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the guide never mentions %q:\n%s", want, out)
		}
	}
}

// TestYourOwnTemplateGetsTheSameData: an override that could not see what the
// built-in one sees would be a downgrade dressed up as a feature.
func TestYourOwnTemplateGetsTheSameData(t *testing.T) {
	dir := t.TempDir()
	tpl := filepath.Join(dir, "agents.tmpl")
	if err := os.WriteFile(tpl, []byte("fleet {{.Session}}: {{range .Agents}}{{.Name}} {{end}}"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := render(t, `
web: {enabled: false}
session: mine
agents_template: `+tpl+`
agents:
  - name: alpha
    command: [cat]
`)
	if out != "fleet mine: alpha " {
		t.Errorf("the override rendered %q", out)
	}
}

// TestAMissingTemplateIsFoundAtLoad: discovering it while writing the guide
// would leave a running fleet with no guide at all.
func TestAMissingTemplateIsFoundAtLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "swarm.yaml")
	if err := os.WriteFile(path, []byte(`
web: {enabled: false}
agents_template: nowhere.tmpl
agents:
  - name: alpha
    command: [cat]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err == nil {
		t.Fatal("a template that does not exist loaded fine")
	}
}
