package hub

import (
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/config"
)

func bucket(t *testing.T, ceiling int, refill time.Duration) *budgets {
	t.Helper()
	c := config.BusBudget{Max: ceiling, Refill: refill}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
	return newBudgets(c)
}

// TestSilenceDoesNotPayForTheStorm is the one this whole thing turns on. A
// balance that keeps climbing while an agent says nothing means a fleet that
// has been quiet for a night can spend the night in a minute — replayed against
// a real runaway, a bucket deep enough to hold the silence before it funded the
// entire storm and refused nothing.
func TestSilenceDoesNotPayForTheStorm(t *testing.T) {
	b := bucket(t, 30, time.Minute)
	t0 := time.Date(2026, 8, 25, 3, 0, 0, 0, time.UTC)

	// Spend everything, then say nothing for a day.
	if _, ok, _ := b.spend("chair", 30, t0); !ok {
		t.Fatal("a full bucket refused its own ceiling")
	}
	quiet := t0.Add(24 * time.Hour)
	if got := b.balance("chair", quiet); got != 30 {
		t.Errorf("a day of silence left %d, want the ceiling of 30", got)
	}
	// And the ceiling is a ceiling: one spend, not twenty-four hours of them.
	if _, ok, _ := b.spend("chair", 30, quiet); !ok {
		t.Fatal("the saved-up ceiling would not spend")
	}
	if _, ok, _ := b.spend("chair", 1, quiet); ok {
		t.Error("a day of silence bought more than the ceiling")
	}
}

// TestAMessageCostsWhatItInterrupts: width is what ran away, and a send to ten
// is ten interruptions however few commands it took.
func TestAMessageCostsWhatItInterrupts(t *testing.T) {
	b := bucket(t, 100, time.Minute)
	one := b.price(bus.KindFYI, 1)
	ten := b.price(bus.KindFYI, 10)
	if ten != one*10 {
		t.Errorf("a send to ten costs %d and a send to one costs %d; want ten times", ten, one)
	}
	if b.price(bus.KindAnswer, 1) >= b.price(bus.KindFYI, 1) {
		t.Error("answering should cost less than telling everybody")
	}
}

// TestBeingBlockedIsAlwaysFree: an agent that cannot go on must be able to say
// so. A budget that can silence that turns a stuck agent into a quiet one,
// which is the failure nobody sees.
func TestBeingBlockedIsAlwaysFree(t *testing.T) {
	c := config.BusBudget{Max: 10, Refill: time.Minute, Cost: map[string]int{"blocked": 99}}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
	if got := c.Cost["blocked"]; got != 0 {
		t.Errorf("a fleet priced blocked at %d; it is not a price a fleet may set", got)
	}

	b := newBudgets(c)
	now := time.Now()
	b.spend("chair", 10, now) // drained
	if _, ok, _ := b.spend("chair", b.price(bus.KindBlocked, 3), now); !ok {
		t.Error("a drained agent could not say it was blocked")
	}
}

// TestARefusalSaysWhen: a bucket refusal is transient, which is exactly the
// kind an agent retries in a loop. It gets the time, not just the no.
func TestARefusalSaysWhen(t *testing.T) {
	b := bucket(t, 10, time.Minute)
	now := time.Date(2026, 8, 25, 3, 0, 0, 0, time.UTC)
	b.spend("chair", 10, now)

	left, ok, ready := b.spend("chair", 5, now)
	if ok {
		t.Fatal("a drained bucket paid")
	}
	if ready.Sub(now) != 5*time.Minute {
		t.Errorf("five points at one a minute is ready in %s, want 5m", ready.Sub(now))
	}
	err := b.refuse("chair", bus.KindFYI, 2, left, ready)
	for _, want := range []string{"of 10", "recipients", ready.Format("15:04:05")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %s", want, err)
		}
	}
}

// TestOnlyAgentsPay: a person, a webhook and swarm's own escalations are not
// the fleet talking to itself, and a fleet that cannot report is worse than one
// that talks too much.
func TestOnlyAgentsPay(t *testing.T) {
	h := fleet(t, `
web: {enabled: false}
bus:
  budget: {max: 2, refill: 1h}
agents:
  - name: alpha
    command: [probe-echo]
  - name: beta
    command: [probe-echo]
`)
	for i := range 4 {
		if _, err := h.Send("user", "alpha", "from a person", nil); err != nil {
			t.Fatalf("the operator was charged on message %d: %v", i+1, err)
		}
	}
	if _, ok := h.BudgetLeft("user"); ok {
		t.Error("a person has a budget")
	}
	if _, ok := h.BudgetLeft("alpha"); !ok {
		t.Error("an agent has none")
	}
}

// TestAnAgentIsToldWhatItHasLeft: the refusal stops one message; the number an
// agent can see before spending is what changes what it writes.
func TestAnAgentIsToldWhatItHasLeft(t *testing.T) {
	h := fleet(t, `
web: {enabled: false}
bus:
  budget: {max: 30, refill: 1h}
agents:
  - name: alpha
    command: [probe-echo]
  - name: beta
    command: [probe-echo]
`)
	before, _ := h.BudgetLeft("alpha")
	if before.Left != 30 || before.Max != 30 {
		t.Fatalf("an agent starts at %d of %d, want a full 30", before.Left, before.Max)
	}
	if _, err := h.SendKind("alpha", "beta", bus.KindFYI, "one", nil); err != nil {
		t.Fatal(err)
	}
	after, _ := h.BudgetLeft("alpha")
	if after.Left != 20 {
		t.Errorf("an fyi to one left %d of 30, want 20", after.Left)
	}
}
