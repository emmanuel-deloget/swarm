package hub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/config"
)

// A debt is the one piece of state a restart must not destroy, and the reason
// is circular in a way that took a while to see: an agent stuck for days is
// itself the reason someone restarts the fleet — to upgrade the binary, to fix
// the config, to try anything — and the restart used to take the explanation
// with it. The tool whose job is to remember what the agents cannot was the
// only one forgetting.

const owedFleet = `
web: {enabled: false}
bus: {stalled_after: 300ms}
defaults: {idle_after: 100ms}
agents:
  - name: alpha
    command: [probe-echo]
  - name: beta
    command: [probe-echo]
`

// fleetIn builds a hub against a state directory the caller owns, so a second
// one can be started over the same files — which is what a restart is.
func fleetIn(t *testing.T, dir, body string) *Hub {
	t.Helper()
	path := filepath.Join(dir, "swarm.yaml")
	if err := os.WriteFile(path, []byte(probeAgents(t, body)), 0o644); err != nil {
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
	return h
}

func TestADebtSurvivesTheProcessThatRecordedIt(t *testing.T) {
	dir := t.TempDir()

	first := fleetIn(t, dir, owedFleet)
	if _, err := first.SendKind("beta", "alpha", bus.KindQuestion, "sessions or tokens?", nil); err != nil {
		t.Fatal(err)
	}
	first.Shutdown(2 * time.Second)

	second := fleetIn(t, dir, owedFleet)
	defer second.Shutdown(2 * time.Second)

	w, err := second.Why("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Debts) != 1 {
		t.Fatalf("after a restart alpha owes %d things, want 1", len(w.Debts))
	}
	d := w.Debts[0]
	if d.From != "beta" {
		t.Errorf("who asked came back as %q", d.From)
	}
	// The text is the part that matters. Messages are bounded and debts are
	// not, so a debt that came back without what it was about would answer the
	// easy half of the question and lose the half anyone was asking.
	if !d.Kept || !strings.Contains(d.Text, "sessions or tokens?") {
		t.Errorf("the question did not survive: kept=%v %q", d.Kept, d.Text)
	}
	if d.Age < 0 {
		t.Errorf("the debt aged backwards across the restart: %s", d.Age)
	}
}

// TestARestoredThreadIsNotReusedByANewConversation is the failure that would
// have been worst and quietest: threads are handed out from a counter that
// starts at zero, so restoring a debt on thread 3 into a fresh bus means the
// third conversation after the restart shares its id — and settling one settles
// the other, closing a debt with a message that had nothing to do with it.
func TestARestoredThreadIsNotReusedByANewConversation(t *testing.T) {
	dir := t.TempDir()

	first := fleetIn(t, dir, owedFleet)
	var threads []uint64
	for range 3 {
		msgs, err := first.SendKind("beta", "alpha", bus.KindQuestion, "one of several", nil)
		if err != nil {
			t.Fatal(err)
		}
		threads = append(threads, msgs[0].Thread)
	}
	first.Shutdown(2 * time.Second)

	second := fleetIn(t, dir, owedFleet)
	defer second.Shutdown(2 * time.Second)

	// A new conversation, after the restart.
	fresh, err := second.SendKind("beta", "alpha", bus.KindQuestion, "brand new", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range threads {
		if fresh[0].Thread == old {
			t.Fatalf("a new conversation was given thread %d, which a restored debt "+
				"is already using; answering one would settle the other", old)
		}
	}
}

// TestADebtForAnAgentThatIsGoneIsNotRestored: nobody could settle it, so it
// would sit there for ever. Dropped, and said out loud — a debt disappearing
// quietly is how someone concludes the work got done.
func TestADebtForAnAgentThatIsGoneIsNotRestored(t *testing.T) {
	dir := t.TempDir()

	first := fleetIn(t, dir, owedFleet)
	if _, err := first.SendKind("beta", "alpha", bus.KindQuestion, "well?", nil); err != nil {
		t.Fatal(err)
	}
	first.Shutdown(2 * time.Second)

	// alpha is no longer in the fleet.
	smaller := `
web: {enabled: false}
bus: {stalled_after: 300ms}
defaults: {idle_after: 100ms}
agents:
  - name: beta
    command: [probe-echo]
`
	second := fleetIn(t, dir, smaller)
	defer second.Shutdown(2 * time.Second)

	if _, err := second.Why("alpha"); err == nil {
		t.Error("an agent that is not in the fleet was explained rather than refused")
	}
	var said bool
	for _, e := range second.Log().History(-1) {
		if strings.Contains(e.Text, "no longer has") {
			said = true
		}
	}
	if !said {
		t.Error("a debt was dropped and nothing in the log says so")
	}
}

// TestDebtsFromAnotherSessionAreNotRestored: two fleets can be pointed at one
// state directory, and an agent called alpha in another session is a different
// agent with a different conversation.
func TestDebtsFromAnotherSessionAreNotRestored(t *testing.T) {
	dir := t.TempDir()

	first := fleetIn(t, dir, "session: one\n"+owedFleet)
	if _, err := first.SendKind("beta", "alpha", bus.KindQuestion, "well?", nil); err != nil {
		t.Fatal(err)
	}
	first.Shutdown(2 * time.Second)

	second := fleetIn(t, dir, "session: two\n"+owedFleet)
	defer second.Shutdown(2 * time.Second)

	w, err := second.Why("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Debts) != 0 {
		t.Errorf("another session's debts were restored: %d", len(w.Debts))
	}
}

// TestAnUnreadableFileIsReportedAndNotFatal: swarm starting is more important
// than swarm remembering, but starting quietly with amnesia is how the fleet
// ends up trusted for something it is no longer doing.
func TestAnUnreadableFileIsReportedAndNotFatal(t *testing.T) {
	dir := t.TempDir()

	// Make the state directory and put rubbish where the debts go.
	first := fleetIn(t, dir, owedFleet)
	path := first.owedPath()
	first.Shutdown(2 * time.Second)
	if err := os.WriteFile(path, []byte("{not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}

	second := fleetIn(t, dir, owedFleet)
	defer second.Shutdown(2 * time.Second)

	var said bool
	for _, e := range second.Log().History(-1) {
		if strings.Contains(e.Text, "not readable") {
			said = true
		}
	}
	if !said {
		t.Error("an unreadable file was passed over without a word")
	}
}

// TestSettlingRemovesTheDebtFromDisk: a debt that comes back after it was
// settled is worse than one that was never saved, because the second time
// nobody believes the tool.
func TestSettlingRemovesTheDebtFromDisk(t *testing.T) {
	dir := t.TempDir()

	first := fleetIn(t, dir, owedFleet)
	if _, err := first.SendKind("beta", "alpha", bus.KindQuestion, "well?", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := first.Done("alpha", 0, "handled"); err != nil {
		t.Fatal(err)
	}
	first.Shutdown(2 * time.Second)

	second := fleetIn(t, dir, owedFleet)
	defer second.Shutdown(2 * time.Second)

	w, err := second.Why("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Debts) != 0 {
		t.Errorf("a settled debt came back from disk: %d", len(w.Debts))
	}
}

// TestNothingIsWrittenWhenNothingWasAsked keeps a fleet that never uses the bus
// from leaving a file behind claiming otherwise.
func TestNothingIsWrittenWhenNothingWasAsked(t *testing.T) {
	dir := t.TempDir()

	h := fleetIn(t, dir, owedFleet)
	path := h.owedPath()
	h.Shutdown(2 * time.Second)

	if _, err := os.Stat(path); err == nil {
		t.Error("a fleet that was never asked anything wrote a file about what it owes")
	}
}
