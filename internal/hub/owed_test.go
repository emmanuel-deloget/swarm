package hub

import (
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/bus"
)

// An agent waiting for work and an agent that stopped halfway through a request
// look the same on screen. What separates them is whether anything was asked of
// it — which the bus knows, because the bus is what asked.

const askFleet = `
web: {enabled: false}
bus: {stalled_after: 300ms}
defaults: {idle_after: 100ms}
agents:
  - name: alpha
    command: [cat]
  - name: beta
    command: [cat]
`

// TestARequestSaysHowToSettleIt: left to guess, an agent guesses, and a wrong
// guess costs a turn to discover while the debt stays open.
func TestARequestSaysHowToSettleIt(t *testing.T) {
	h := fleet(t, askFleet)

	msgs, err := h.SendKind("user", "alpha", bus.KindRequest, "rewrite the parser", nil)
	if err != nil {
		t.Fatal(err)
	}
	body := msgs[0].Body
	if !strings.Contains(body, "swarm done -thread") {
		t.Errorf("a request does not say how to settle it:\n%s", body)
	}

	q, err := h.SendKind("user", "beta", bus.KindQuestion, "which parser?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q[0].Body, "-kind answer") {
		t.Errorf("a question does not say how to answer it:\n%s", q[0].Body)
	}

	// And what asks nothing says nothing.
	fyi, _ := h.SendKind("user", "alpha", bus.KindFYI, "for your information", nil)
	if strings.Contains(fyi[0].Body, "[swarm] when this") {
		t.Errorf("an fyi carries an acknowledgement line:\n%s", fyi[0].Body)
	}
}

// TestDoneSettlesAndReportsBack: whoever asked hears about it, on the thread
// they asked on.
func TestDoneSettlesAndReportsBack(t *testing.T) {
	h := fleet(t, askFleet)

	asked, err := h.SendKind("beta", "alpha", bus.KindRequest, "have a look", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Bus().Owed("alpha")) != 1 {
		t.Fatal("the request opened no debt")
	}

	settled, msgs, err := h.Done("alpha", 0, "nothing to change")
	if err != nil {
		t.Fatal(err)
	}
	if settled != 1 {
		t.Errorf("done settled %d debts, want 1", settled)
	}
	if len(h.Bus().Owed("alpha")) != 0 {
		t.Error("done left the debt open")
	}
	if len(msgs) != 1 {
		t.Fatalf("done sent %d messages, want one back to beta", len(msgs))
	}
	if msgs[0].To != "beta" || msgs[0].Kind != bus.KindDone {
		t.Errorf("the report went to %s as %q", msgs[0].To, msgs[0].Kind)
	}
	if msgs[0].Thread != asked[0].Thread {
		t.Error("the report opened a new thread instead of closing the one asked on")
	}
}

// TestDoneWithNothingOutstandingIsNotAnError: an agent may report work it was
// given by hand, and being told is better than being right.
func TestDoneWithNothingOutstandingIsNotAnError(t *testing.T) {
	h := fleet(t, askFleet)
	_, msgs, err := h.Done("alpha", 0, "")
	if err != nil {
		t.Fatalf("done with nothing outstanding failed: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("it wrote to %d agents with nobody waiting", len(msgs))
	}
}

// TestStalledNeedsBothSilenceAndADebt. Either alone is normal: an agent with
// nothing to do is quiet, and an agent that is working is not stalled.
func TestStalledNeedsBothSilenceAndADebt(t *testing.T) {
	h := fleet(t, askFleet)
	a, err := h.Agent("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}

	// Quiet, but nothing was asked: never stalled.
	waitFor(t, func() bool { return a.Info().State == "idle" })
	time.Sleep(400 * time.Millisecond)
	if stalledEvents(t, h) != 0 {
		t.Error("an agent with nothing to do was called stalled")
	}

	// Now something is owed, and the silence means something.
	if _, err := h.SendKind("beta", "alpha", bus.KindRequest, "look at this", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return stalledEvents(t, h) > 0 })

	// Settling it stops the reporting.
	before := stalledEvents(t, h)
	if _, _, err := h.Done("alpha", 0, ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if got := stalledEvents(t, h); got > before+1 {
		t.Errorf("still reported after being settled: %d then %d", before, got)
	}
}

func stalledEvents(t *testing.T, h *Hub) int {
	t.Helper()
	n := 0
	for _, e := range h.Log().History(-1) {
		if strings.Contains(e.Text, "stalled:") {
			n++
		}
	}
	return n
}

// TestStalledStartsWhenIdleDoes: the wait begins where the agent's idle_after
// ends, so the two settings add up instead of competing. No pair of values can
// be posed in a way that never fires.
func TestStalledStartsWhenIdleDoes(t *testing.T) {
	// idle_after 400ms, stalled_after 300ms: nothing before 700ms of silence.
	h := fleet(t, `
web: {enabled: false}
bus: {stalled_after: 300ms}
defaults: {idle_after: 400ms}
agents:
  - name: alpha
    command: [cat]
  - name: beta
    command: [cat]
`)
	a, err := h.Agent("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SendKind("beta", "alpha", bus.KindRequest, "look", nil); err != nil {
		t.Fatal(err)
	}

	// Idle arrives at 400ms; stalled must not.
	waitFor(t, func() bool { return a.Info().State == "idle" })
	if n := stalledEvents(t, h); n != 0 {
		t.Errorf("stalled reported %d times as soon as the agent went idle", n)
	}
	waitFor(t, func() bool { return stalledEvents(t, h) > 0 })
}

// TestOutputDoesNotResetTheClock is the bug seen on a live fleet: an agent
// parked on a configuration screen redraws every few minutes, and every redraw
// pushed the state back to zero, so it blinked out and took a full cycle to
// return. What is measured is how long the work has been owed — a redraw
// settles nothing.
func TestOutputDoesNotResetTheClock(t *testing.T) {
	// Prints every 200ms: long enough to fall back to idle (100ms), short
	// enough that the silence never reaches the 600ms threshold. Measured from
	// the last byte, this agent could never be stalled however long it owed.
	h := fleet(t, `
web: {enabled: false}
bus: {stalled_after: 500ms}
defaults: {idle_after: 100ms}
agents:
  - name: alpha
    command: [sh, -c, "while :; do sleep 0.2; printf .; done"]
  - name: beta
    command: [cat]
`)
	a, err := h.Agent("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SendKind("beta", "alpha", bus.KindRequest, "look at this", nil); err != nil {
		t.Fatal(err)
	}

	// It falls back to idle between prints, and the debt keeps ageing.
	waitFor(t, func() bool {
		for _, in := range h.Infos() {
			if in.Name == "alpha" && in.Stalled {
				return true
			}
		}
		return false
	})
}
