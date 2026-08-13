package hub

import (
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/bus"
)

// `swarm why` exists because stalled was a state nobody could act on. An agent
// stalled for two days has been through several context compactions by then,
// so the message that put it there is gone from the one place a reader would
// think to look — the agent's own memory. Asking it is useless.
//
// The bus is where it survives, and these tests are about the difference
// between an explanation and a colour.

const whyFleet = `
web: {enabled: false}
bus: {stalled_after: 300ms}
defaults: {idle_after: 100ms}
agents:
  - name: alpha
    command: [probe-echo]
  - name: beta
    command: [probe-echo]
`

func TestWhyNamesWhoIsWaitingAndForWhat(t *testing.T) {
	h := fleet(t, whyFleet)

	if _, err := h.SendKind("beta", "alpha", bus.KindQuestion, "sessions or tokens?", nil); err != nil {
		t.Fatal(err)
	}

	w, err := h.Why("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Debts) != 1 {
		t.Fatalf("alpha owes %d things, want 1", len(w.Debts))
	}
	d := w.Debts[0]
	if d.From != "beta" {
		t.Errorf("who asked came out as %q", d.From)
	}
	if d.Kind != string(bus.KindQuestion) {
		t.Errorf("what was asked came out as %q", d.Kind)
	}
	if !d.Kept || !strings.Contains(d.Text, "sessions or tokens?") {
		t.Errorf("the question itself is not in the answer: kept=%v %q", d.Kept, d.Text)
	}
	if d.Thread == 0 {
		t.Error("no thread, so nothing can be answered on it")
	}
}

// TestWhySaysHowItEnds is the half that makes the rest worth having. Knowing
// why you are stuck without knowing how to get out is a better-documented dead
// end.
func TestWhySaysHowItEnds(t *testing.T) {
	h := fleet(t, whyFleet)

	if _, err := h.SendKind("beta", "alpha", bus.KindQuestion, "which one?", nil); err != nil {
		t.Fatal(err)
	}
	w, err := h.Why("alpha")
	if err != nil {
		t.Fatal(err)
	}
	settle := w.Debts[0].Settle
	if settle == "" {
		t.Fatal("no way out is offered")
	}

	// The flags have to come before the target: Go's flag parsing stops at the
	// first non-flag argument, so `swarm send alpha -kind answer` passes the
	// flags through as message text. Advice that does not run is worse than
	// none — it looks like it was tried.
	if i, j := strings.Index(settle, "-kind"), strings.Index(settle, "beta"); i < 0 || j < 0 || i > j {
		t.Errorf("the command would not parse as written: %q", settle)
	}
	if !strings.Contains(settle, "-thread") {
		t.Errorf("the way out does not name the thread: %q", settle)
	}

	// And it is the same sentence the message itself carried, from one
	// function: two spellings of a command is one spelling that is wrong.
	msgs, err := h.SendKind("beta", "alpha", bus.KindQuestion, "another", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msgs[0].Body, SettleCommand(bus.KindQuestion, "beta", msgs[0].Thread)) {
		t.Errorf("the message and `why` do not say the same thing:\n%s", msgs[0].Body)
	}
}

// TestWhyIsHonestWhenNothingIsWrong: an agent can be quiet for hours and owe
// nothing at all, and saying so is an answer. Reporting silence as a problem is
// how a tool teaches people to ignore it.
func TestWhyIsHonestWhenNothingIsWrong(t *testing.T) {
	h := fleet(t, whyFleet)

	w, err := h.Why("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Debts) != 0 {
		t.Errorf("alpha owes %d things without having been asked anything", len(w.Debts))
	}
	if w.Stalled {
		t.Error("alpha is stalled without owing anything")
	}
	if w.StalledAfter == 0 {
		t.Error("the threshold is not reported, so a reader cannot tell 'not yet' from 'never'")
	}
}

// TestWhyRefusesAnAgentItDoesNotHave: "nothing is wrong with dev-99" is a bad
// thing to tell someone who misspelled dev-9.
func TestWhyRefusesAnAgentItDoesNotHave(t *testing.T) {
	h := fleet(t, whyFleet)

	if _, err := h.Why("nosuch"); err == nil {
		t.Error("an unknown agent was explained rather than refused")
	}
}

// TestWhyFollowsTheAgentOutOfTheState: the report has to end when the debt
// does, or it becomes another thing that says stalled forever.
func TestWhyFollowsTheAgentOutOfTheState(t *testing.T) {
	h := fleet(t, whyFleet)

	if _, err := h.SendKind("beta", "alpha", bus.KindQuestion, "well?", nil); err != nil {
		t.Fatal(err)
	}
	if w, _ := h.Why("alpha"); len(w.Debts) != 1 {
		t.Fatalf("the debt was not opened")
	}

	if _, _, err := h.Done("alpha", 0, "handled"); err != nil {
		t.Fatal(err)
	}
	w, err := h.Why("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Debts) != 0 {
		t.Errorf("alpha settled and still owes %d things", len(w.Debts))
	}
	if w.Stalled {
		t.Error("alpha settled and is still called stalled")
	}
}

// TestWhyReportsTheAgeOfTheDebt: how long it has been waiting is the number
// that decides whether anyone cares, and it is the age of the debt rather than
// of the silence — an agent parked on a screen that redraws is not settling
// anything by redrawing.
func TestWhyReportsTheAgeOfTheDebt(t *testing.T) {
	h := fleet(t, whyFleet)

	if _, err := h.SendKind("beta", "alpha", bus.KindQuestion, "well?", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	w, err := h.Why("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if w.Debts[0].Age <= 0 {
		t.Errorf("the debt has no age: %s", w.Debts[0].Age)
	}
	if w.Debts[0].Since.IsZero() {
		t.Error("the debt has no date, so nothing can be said about how old it is")
	}
}
