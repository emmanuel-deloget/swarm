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

// TestTheCommandsAnAgentNeedsAreInTheGuide.
//
// AGENTS.md is the only place an agent learns what it can run. A command that
// is not here does not exist as far as the fleet is concerned, however well it
// works and however carefully the README describes it — nobody in the fleet
// reads the README.
//
// `swarm why` was written, tested, documented and shipped without reaching
// this file, and the gap is worse for that command than for most: it exists so
// a stalled agent can find out why it is stalled, and an agent that does not
// know about it will sit there instead. It was noticed by a person, which is
// the part this test is meant to replace.
//
// Not every command belongs here — `run`, `attach` and `init` are for whoever
// starts the fleet, not for the agents in it. This is the list of things an
// agent does, so adding to it is a deliberate act.
func TestTheCommandsAnAgentNeedsAreInTheGuide(t *testing.T) {
	full := `
bus: {max_turns: 6}
web: {enabled: false}
agents:
  - name: alpha
    command: [cat]
  - name: beta
    command: [cat]
`
	out := render(t, full)

	for _, cmd := range []struct{ name, why string }{
		{"swarm send", "an agent that cannot reach another agent is not in a fleet"},
		{"swarm inbox", "messages that are never collected were never delivered"},
		{"swarm done", "without it, finished work still counts as owed"},
		{"swarm why", "the way out of stalled, for the agent that is in it"},
	} {
		if !strings.Contains(out, cmd.name) {
			t.Errorf("the guide never mentions `%s`: %s", cmd.name, cmd.why)
		}
	}
}

// TestSpawningIsOnlyExplainedWhenSomeoneMay.
//
// The guide is what an agent reads to learn what it can run, and a section
// about a command nobody may use is worse than a missing one: an agent told
// about `swarm spawn` will try it, be refused, and have spent a turn learning
// what the file already knew.
// TestEachAgentIsToldWhetherToWait: agents call `swarm inbox -wait` liberally,
// including when their messages are typed into their terminal and the mailbox
// is never filled. The guide is where they read, so the answer goes there —
// per agent, because in one fleet it differs per agent.
func TestEachAgentIsToldWhetherToWait(t *testing.T) {
	out := render(t, `
web: {enabled: false}
agents:
  - name: chair
    delivery: push
    command: [probe-echo]
  - name: worker
    delivery: pull
    command: [probe-echo]
  - name: builder
    delivery: defer
    command: [probe-echo]
`)
	for _, want := range []string{
		"| `chair` | **no**",
		"| `worker` | **yes**",
		"| `builder` | **yes**",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the guide does not say %q:\n%s", want, out)
		}
	}
}

// TestOneKindLeftForCollectionChangesEveryAnswer: delivery_by_kind overrides
// every agent's mode for one kind, so a single pull entry means even a
// push-delivered agent has something to wait for — and the guide has to say so,
// or it contradicts what `swarm inbox` does.
func TestOneKindLeftForCollectionChangesEveryAnswer(t *testing.T) {
	out := render(t, `
web: {enabled: false}
delivery_by_kind: {blocked: pull}
agents:
  - name: chair
    delivery: push
    command: [probe-echo]
`)
	if !strings.Contains(out, "| `chair` | **yes**") {
		t.Errorf("blocked messages are left for collection; the guide says otherwise:\n%s", out)
	}
}

func TestSpawningIsOnlyExplainedWhenSomeoneMay(t *testing.T) {
	without := render(t, `
web: {enabled: false}
agents:
  - name: alpha
    command: [cat]
  - name: beta
    command: [cat]
`)
	if strings.Contains(without, "swarm spawn") {
		t.Error("a fleet where nobody may spawn is told how to spawn")
	}

	with := render(t, `
web: {enabled: false}
agents:
  - name: alpha
    command: [cat]
    can_spawn: [worker]
  - name: worker
    ephemeral: true
    command: [cat]
`)
	if !strings.Contains(with, "swarm spawn") {
		t.Error("a fleet with a template is not told how to use it")
	}
	if !strings.Contains(with, "`worker`") {
		t.Error("the guide does not name the template that can be spawned")
	}
	// The two things an agent has to know beyond the syntax: the instance
	// starts with none of this context, and its task is what ends it.
	for _, want := range []string{"no memory of this", "swarm done"} {
		if !strings.Contains(with, want) {
			t.Errorf("the guide never says %q", want)
		}
	}
}

// An instance whose worktree is taken back has one thing to do that no other
// agent has: get the work somewhere else first.
func TestWorktreeInstancesAreToldToPushFirst(t *testing.T) {
	with := render(t, `
web: {enabled: false}
agents:
  - name: alpha
    command: [cat]
    can_spawn: [worker]
  - name: worker
    ephemeral: true
    command: [cat]
    workspace: shared
`)
	if strings.Contains(with, "own git worktree") {
		t.Error("a template with no worktree is described as having one")
	}
}

// TestABudgetIsExplainedOnlyWhenThereIsOne: the guide describes the mechanisms
// that are switched on and no others. An agent told about an allowance in a
// fleet that has none learns a rule that does not exist.
func TestABudgetIsExplainedOnlyWhenThereIsOne(t *testing.T) {
	with := render(t, `
web: {enabled: false}
defaults:
  budget: {max: 40}
agents:
  - name: alpha
    command: [probe-echo]
  - name: chair
    budget: {max: 400}
    command: [probe-echo]
`)
	// Per agent, because a coordinator broadcasts for a living.
	for _, want := range []string{"| `alpha` | 40 |", "| `chair` | 400 |"} {
		if !strings.Contains(with, want) {
			t.Errorf("the guide does not say %q:\n%s", want, with)
		}
	}
	if !strings.Contains(with, "once per recipient") {
		t.Error("the guide does not say a send to ten costs ten times a send to one")
	}

	without := render(t, `
web: {enabled: false}
agents:
  - name: alpha
    command: [probe-echo]
`)
	if strings.Contains(without, "allowance for talking") {
		t.Errorf("a fleet with no budget is told about one:\n%s", without)
	}
}

// TestTheMemoryIsExplainedWithItsLimits: an agent told about a memory without
// its ceilings writes an essay and is refused, which teaches it nothing it
// could not have been told first.
func TestTheMemoryIsExplainedWithItsLimits(t *testing.T) {
	with := render(t, `
web: {enabled: false}
memory: {max: 40, chars: 120}
agents:
  - name: alpha
    command: [probe-echo]
`)
	for _, want := range []string{"120 characters", "40 entries", "swarm recall", "swarm forget"} {
		if !strings.Contains(with, want) {
			t.Errorf("the guide does not say %q:\n%s", want, with)
		}
	}

	without := render(t, `
web: {enabled: false}
memory: {max: 0}
agents:
  - name: alpha
    command: [probe-echo]
`)
	if strings.Contains(without, "swarm recall") {
		t.Errorf("a fleet with no memory is told about one:\n%s", without)
	}
}
