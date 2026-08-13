package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/bus"
)

// on_stalled is the first thing swarm does about a state rather than only
// showing it, so what it refuses matters as much as what it accepts.

func loadYAML(t *testing.T, body string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "swarm.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

const stalledBase = `
web: {enabled: false}
agents:
  - {name: alpha, command: [cat]}
  - {name: triage, command: [cat]}
`

// TestTheDefaultsAreThingsThatExist. The default kind was `note` for a while,
// which is not a kind at all — the fleet refused to start, and the tests did
// not catch it because they built rules in Go and never went through here.
func TestTheDefaultsAreThingsThatExist(t *testing.T) {
	c, err := loadYAML(t, stalledBase+`
bus:
  on_stalled:
    - {}
`)
	if err != nil {
		t.Fatal(err)
	}
	r := c.Bus.OnStalled[0]
	if r.To != StalledSelf {
		t.Errorf("to defaults to %q", r.To)
	}
	if !bus.ValidKind(bus.Kind(r.Kind)) {
		t.Errorf("the default kind %q is not a kind swarm can send", r.Kind)
	}
	if bus.Opening(bus.Kind(r.Kind)) {
		t.Errorf("the default kind %q opens a debt on an agent that already has one", r.Kind)
	}
	if r.Max != 1 {
		t.Errorf("max defaults to %d, want one ask for one debt", r.Max)
	}
	if !r.PushWanted() {
		t.Error("push defaults to off, so the ask would wait in the mailbox of an " +
			"agent that is not reading its mailbox")
	}
}

func TestRepeatingRulesGetAHigherDefault(t *testing.T) {
	c, err := loadYAML(t, stalledBase+`
bus:
  on_stalled:
    - {every: 15m}
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Bus.OnStalled[0].Max != 3 {
		t.Errorf("a repeating rule defaults to max %d", c.Bus.OnStalled[0].Max)
	}
}

func TestAskingTheStalledAgentForSomethingIsRefused(t *testing.T) {
	for _, kind := range []string{"question", "request", "blocked"} {
		_, err := loadYAML(t, stalledBase+`
bus:
  on_stalled:
    - to: self
      kind: `+kind+`
`)
		if err == nil {
			t.Errorf("a %q to the stalled agent was accepted; it opens a second debt "+
				"on top of the one it is stuck on", kind)
			continue
		}
		if !strings.Contains(err.Error(), "second debt") {
			t.Errorf("the refusal does not say why: %v", err)
		}
	}
}

// The same kinds are fine towards somebody else: opening a debt on a triage
// agent is the useful half, since it can then ask properly.
func TestAskingSomebodyElseForSomethingIsAllowed(t *testing.T) {
	if _, err := loadYAML(t, stalledBase+`
bus:
  on_stalled:
    - to: triage
      kind: question
`); err != nil {
		t.Errorf("a question to a triage agent was refused: %v", err)
	}
}

func TestAKindThatDoesNotExistIsRefusedWithTheList(t *testing.T) {
	_, err := loadYAML(t, stalledBase+`
bus:
  on_stalled:
    - kind: nudge
`)
	if err == nil {
		t.Fatal("an invented kind was accepted")
	}
	if !strings.Contains(err.Error(), "question") {
		t.Errorf("the error does not name the kinds that do exist: %v", err)
	}
}

func TestAnUnreasonableIntervalIsRefused(t *testing.T) {
	_, err := loadYAML(t, stalledBase+`
bus:
  on_stalled:
    - {every: 2s}
`)
	if err == nil {
		t.Error("a reminder every two seconds was accepted")
	}
}

func TestTheExampleFromTheDocumentationLoads(t *testing.T) {
	c, err := loadYAML(t, `
web: {enabled: false}
agents:
  - {name: alpha, command: [cat]}
  - {name: myself, command: [cat]}
bus:
  stalled_after: 15m
  on_stalled:
    - to: self
      every: 15m
      max: 3
    - to: myself
      after: 2h
      kind: question
      max: 1
`)
	if err != nil {
		t.Fatalf("the shape the documentation shows does not load: %v", err)
	}
	if len(c.Bus.OnStalled) != 2 {
		t.Fatalf("%d rules", len(c.Bus.OnStalled))
	}
	if c.Bus.OnStalled[1].After != 2*time.Hour {
		t.Errorf("after came out as %s", c.Bus.OnStalled[1].After)
	}
}
