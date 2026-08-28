package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/emmanuel-deloget/swarm/internal/agent"
)

// TestAnArrivalPointsAtTheAgent: the badge sits to the right of the name, so a
// mark pointing left is one coming in. `››` beside a name is a message leaving
// it, whatever the code meant by it.
func TestAnArrivalPointsAtTheAgent(t *testing.T) {
	seen := []string{}
	for _, age := range []time.Duration{0, 200 * time.Millisecond, 500 * time.Millisecond} {
		badge, live := arriving(age)
		if !live {
			t.Fatalf("nothing drawn at %s", age)
		}
		seen = append(seen, strings.TrimSpace(stripANSI(badge)))
	}
	want := []string{"‹‹", "‹", "✉"}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("at step %d the badge is %q, want %q", i, seen[i], want[i])
		}
	}
	if _, live := arriving(arriveFor); live {
		t.Error("the arrival is still drawn after it is over")
	}
}

// TestADepartureCarriesItsCount: a broadcast is one command and nine messages,
// arriving as nine events milliseconds apart. Nine flashes on one line read as
// a tremble; the number says it once.
func TestADepartureCarriesItsCount(t *testing.T) {
	one, _ := leaving(0, 1)
	many, _ := leaving(0, 9)
	if strings.Contains(stripANSI(one), "9") {
		t.Errorf("a single message counted itself: %q", stripANSI(one))
	}
	if !strings.Contains(stripANSI(many), "9") {
		t.Errorf("nine messages did not say so: %q", stripANSI(many))
	}
	// And it points away from the name, where an arrival points at it.
	late, _ := leaving(600*time.Millisecond, 1)
	if !strings.Contains(stripANSI(late), "›") || strings.Contains(stripANSI(late), "‹") {
		t.Errorf("a departure points the wrong way: %q", stripANSI(late))
	}
}

// TestAQueuedMessageDoesNotMove: a message waiting in a mailbox has
// interrupted nobody. It is a count, not a movement, and the two say different
// things about the same fleet.
func TestAQueuedMessageDoesNotMove(t *testing.T) {
	m := newTestModel(t)
	in := agent.Info{Name: "dev-1", Unread: 2}
	badge := stripANSI(m.messageBadge(in))
	if !strings.Contains(badge, "2✉") {
		t.Errorf("a full mailbox shows %q, want the count", badge)
	}

	// Delivered, and now it moves.
	m.delivered["dev-1"] = time.Now()
	in.Unread = 0
	if got := stripANSI(m.messageBadge(in)); !strings.Contains(got, "‹") {
		t.Errorf("a delivered message shows %q, want it arriving", got)
	}
}

// TestStalledBreathesRatherThanFades: stalled is a state, not an event. It
// lasts as long as the agent owes something and says nothing, so a fade would
// stop while the trouble carried on.
func TestStalledBreathesRatherThanFades(t *testing.T) {
	// Asked of the colour itself, not of lipgloss: it resolves nothing when
	// there is no terminal, and every shade would come back the same.
	shade := func(at time.Time) string {
		c, ok := breathing(at).(lipgloss.AdaptiveColor)
		if !ok {
			t.Fatalf("the pulse is a %T, which cannot be asked what it is", breathing(at))
		}
		return c.Dark
	}
	base := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	seen := map[string]bool{}
	for i := range 8 {
		seen[shade(base.Add(time.Duration(i)*breatheFor/8))] = true
	}
	if len(seen) < 5 {
		t.Errorf("a turn of the pulse took %d shades; it is barely moving", len(seen))
	}
	// A quarter turn apart is the peak against the trough. Half a turn is not:
	// a sine is back where it started by then, which is what a breath is.
	if shade(base.Add(breatheFor/4)) == shade(base.Add(3*breatheFor/4)) {
		t.Error("the top and the bottom of the breath are the same shade")
	}
	// Still going a minute later, where a fade would long have finished.
	if shade(base) != shade(base.Add(time.Minute+breatheFor)) {
		t.Error("the pulse has drifted after a minute")
	}
}
