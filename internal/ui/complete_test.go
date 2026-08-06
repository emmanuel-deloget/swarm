package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/emmanuel-deloget/swarm/internal/config"
	"github.com/emmanuel-deloget/swarm/internal/hub"
)

// newTestModel builds a UI over a small fleet, without starting any process:
// completion only needs the configuration.
func newTestModel(t *testing.T) *model {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "swarm.yaml")
	body := `session: complete-test
groups:
  backend: [dev-1, dev-2]
web:
  enabled: false
agents:
  - name: dev-1
    role: dev
    command: [sh]
  - name: dev-2
    role: dev
    command: [sh]
  - name: review-1
    role: review
    command: [sh]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.New(hub.Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Shutdown(time.Second) })

	events, cancel := h.Log().Subscribe(8)
	t.Cleanup(cancel)
	m := newModel(h, events, make(chan struct{}))
	m.width, m.height = 120, 40
	return m
}

// tab types a line and presses tab n times, returning what the line becomes.
func tab(m *model, line string, n int) string {
	m.input = textinput.New()
	m.input.SetValue(line)
	m.input.CursorEnd()
	m.completions, m.completeAt = nil, ""
	for range n {
		m.complete()
	}
	return m.input.Value()
}

func TestCompleteCommandNames(t *testing.T) {
	m := newTestModel(t)

	// A unique prefix completes outright, with the space that follows it.
	if got := tab(m, "bro", 1); got != "broadcast " {
		t.Errorf("tab on %q = %q, want %q", "bro", got, "broadcast ")
	}
	// When the candidates share nothing beyond what was typed — start, stop —
	// the first one goes on the line and the rest are listed, so a press is
	// never wasted.
	first := tab(m, "st", 1)
	if first != "start" && first != "stop" {
		t.Errorf("tab on %q = %q, want one of the candidates", "st", first)
	}
	if !strings.Contains(m.status, "start") || !strings.Contains(m.status, "stop") {
		t.Errorf("the candidates should be listed, status was %q", m.status)
	}
	// Pressing again moves to the next one, and round again.
	second := tab(m, "st", 2)
	if second == first {
		t.Errorf("tab should cycle, stayed on %q", first)
	}
	if third := tab(m, "st", 3); third != first {
		t.Errorf("the cycle should come back round: %q then %q then %q", first, second, third)
	}
	// A shared prefix longer than what was typed is filled in: re -> restart,
	// resize share "res".
	if got := tab(m, "re", 1); got != "res" {
		t.Errorf("tab on %q = %q, want %q", "re", got, "res")
	}
}

func TestCompleteTargets(t *testing.T) {
	m := newTestModel(t)

	if got := tab(m, "inject rev", 1); got != "inject review-1 " {
		t.Errorf("tab = %q, want %q", got, "inject review-1 ")
	}
	// Groups and roles are targets too.
	if got := tab(m, "send @b", 1); got != "send @backend " {
		t.Errorf("tab = %q, want %q", got, "send @backend ")
	}
	if got := tab(m, "keys @rev", 1); got != "keys @review " {
		t.Errorf("tab = %q, want %q", got, "keys @review ")
	}
	// "all" is offered as well.
	if got := tab(m, "restart al", 1); got != "restart all " {
		t.Errorf("tab = %q, want %q", got, "restart all ")
	}
	// An empty target lists everything available.
	tab(m, "stop ", 1)
	for _, want := range []string{"dev-1", "dev-2", "review-1", "@dev", "@review", "@backend", "all"} {
		if !strings.Contains(m.status, want) {
			t.Errorf("the target list should include %q, got %q", want, m.status)
		}
	}
}

func TestCompleteKeysAndFreeText(t *testing.T) {
	m := newTestModel(t)

	// After a target, :keys completes key names.
	if got := tab(m, "keys dev-1 pgu", 1); got != "keys dev-1 pgup " {
		t.Errorf("tab = %q, want %q", got, "keys dev-1 pgup ")
	}
	// Free text is not guessed at: :send takes a message, not a name.
	before := "send dev-1 hel"
	if got := tab(m, before, 1); got != before {
		t.Errorf("tab changed free text: %q -> %q", before, got)
	}
	if m.status == "" {
		t.Error("tab on free text should say there is nothing to complete")
	}
	// :broadcast takes text from the first argument on.
	before = "broadcast hel"
	if got := tab(m, before, 1); got != before {
		t.Errorf("tab changed broadcast text: %q -> %q", before, got)
	}
}

func TestCompletePaths(t *testing.T) {
	m := newTestModel(t)
	dir := m.h.Config().Dir()
	for _, name := range []string{"notes.md", "notes-old.md", "shot.png"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if got := tab(m, "file dev-1 sh", 1); got != "file dev-1 shot.png " {
		t.Errorf("tab = %q, want %q", got, "file dev-1 shot.png ")
	}
	// Two files share a prefix: extend to it and list both.
	if got := tab(m, "file dev-1 not", 1); got != "file dev-1 notes" {
		t.Errorf("tab = %q, want %q", got, "file dev-1 notes")
	}
	if !strings.Contains(m.status, "notes.md") || !strings.Contains(m.status, "notes-old.md") {
		t.Errorf("both files should be listed, status was %q", m.status)
	}
}

func TestUnknownCommandCompletesNothing(t *testing.T) {
	m := newTestModel(t)
	before := "nonsense dev"
	if got := tab(m, before, 1); got != before {
		t.Errorf("tab after an unknown verb changed the line: %q -> %q", before, got)
	}
}

// TestEveryCommandIsRunnable keeps the completion table honest: a verb offered
// by tab must be one runCommand knows, or tab teaches the wrong vocabulary.
func TestEveryCommandIsRunnable(t *testing.T) {
	m := newTestModel(t)
	for _, name := range commandNames() {
		m.status, m.isError = "", false
		cmd := m.runCommand(name)
		if cmd != nil {
			// Run the returned command so the status reflects the outcome; the
			// fleet is stopped, so nothing happens beyond a message.
			if msg := cmd(); msg != nil {
				if r, ok := msg.(resultMsg); ok {
					m.status, m.isError = r.text, r.isErr
				}
			}
		}
		if strings.Contains(m.status, "unknown command") {
			t.Errorf("tab offers %q but runCommand rejects it", name)
		}
	}
}
