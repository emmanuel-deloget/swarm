package hub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/config"
)

func fleet(t *testing.T, body string) *Hub {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "swarm.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{Config: cfg, StateDir: cfg.StateDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Shutdown(2 * time.Second) })
	return h
}

// TestDeferredMessagesArriveAsOne is the point of deferring as much as the
// waiting: three messages that turned up while an agent worked are one
// interruption when it stops, not three.
func TestDeferredMessagesArriveAsOne(t *testing.T) {
	h := fleet(t, `
log_input: true
web: {enabled: false}
agents:
  - name: quiet
    command: [cat]
    delivery: defer
    idle_after: 500ms
`)
	a, err := h.Agent("quiet")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}

	for _, body := range []string{"one", "two", "three"} {
		if _, err := h.Send("user", "quiet", body, nil); err != nil {
			t.Fatal(err)
		}
	}
	// Nothing is typed while they wait.
	if n := h.Bus().Pending("quiet"); n != 3 {
		t.Fatalf("%d messages pending, want all three held back", n)
	}

	h.flushDeferred("quiet")

	if n := h.Bus().Pending("quiet"); n != 0 {
		t.Errorf("%d still pending after a flush", n)
	}
	log := readInputLog(t, h, "quiet")
	if got := strings.Count(log, "\tinject\t"); got != 1 {
		t.Errorf("%d injections, want one: three messages must arrive together", got)
	}
	for _, want := range []string{"3 messages arrived", "one", "two", "three"} {
		if !strings.Contains(log, want) {
			t.Errorf("the injection should mention %q:\n%s", want, log)
		}
	}
}

// TestASingleDeferredMessageHasNoPreamble: the count only helps when there is
// more than one, and a header on every message would be noise.
func TestASingleDeferredMessageHasNoPreamble(t *testing.T) {
	h := fleet(t, `
log_input: true
web: {enabled: false}
agents:
  - name: quiet
    command: [cat]
    delivery: defer
`)
	a, _ := h.Agent("quiet")
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Send("dev-1", "quiet", "just one", nil); err != nil {
		t.Fatal(err)
	}
	h.flushDeferred("quiet")

	log := readInputLog(t, h, "quiet")
	if strings.Contains(log, "messages arrived") {
		t.Errorf("a lone message should not be announced as a batch:\n%s", log)
	}
	if !strings.Contains(log, "just one") {
		t.Errorf("the message did not arrive:\n%s", log)
	}
}

func readInputLog(t *testing.T, h *Hub, name string) string {
	t.Helper()
	// The injection ends with a submit after the paste delay.
	deadline := time.Now().Add(3 * time.Second)
	path := filepath.Join(h.StateDir(), "logs", name+".input.log")
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), "\tsubmit\t") {
			return string(b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	b, _ := os.ReadFile(path)
	return string(b)
}
