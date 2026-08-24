package hub

import (
	"fmt"
	"sync"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/config"
)

// budgets is what an agent may say, as hit points: a balance that refills a
// little at a time and never passes its ceiling.
//
// The ceiling is the part that matters, and it is the part that is easy to
// leave out. A fleet that has been quiet has, by definition, saved up for its
// worst hour — replayed against a real runaway, a bucket sized to the day of
// silence before it funded the whole storm and refused nothing. The refill
// rate sets the steady state; the ceiling sets the disaster.
//
// A message is priced by what it interrupts, so a send to ten costs ten times a
// send to one. That is the whole point: what ran away was width, not depth, and
// depth is the only thing `max_turns` can see.
type budgets struct {
	mu   sync.Mutex
	max  int
	per  time.Duration
	cost map[bus.Kind]int
	bal  map[string]float64
	at   map[string]time.Time
}

func newBudgets(c config.BusBudget) *budgets {
	cost := make(map[bus.Kind]int, len(c.Cost))
	for kind, price := range c.Cost {
		cost[bus.Kind(kind)] = price
	}
	return &budgets{
		max:  c.Max,
		per:  c.Refill,
		cost: cost,
		bal:  map[string]float64{},
		at:   map[string]time.Time{},
	}
}

// on reports whether any of this applies. A fleet that says nothing about a
// budget has none, and nothing changes for it.
func (b *budgets) on() bool { return b != nil && b.max > 0 }

// price is what a send costs: the kind, once per recipient.
func (b *budgets) price(kind bus.Kind, recipients int) int {
	if !b.on() || recipients <= 0 {
		return 0
	}
	return b.cost[kind] * recipients
}

// balance is what an agent has now, refilled for the time since it last spent.
func (b *budgets) balance(agent string, now time.Time) int {
	if !b.on() {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return int(b.refilled(agent, now))
}

// refilled brings an agent's balance up to date. The caller holds the lock.
func (b *budgets) refilled(agent string, now time.Time) float64 {
	have, seen := b.bal[agent]
	if !seen {
		// Everyone starts full: a budget is a bound on running away, not a
		// waiting period before an agent may speak at all.
		return float64(b.max)
	}
	if last, ok := b.at[agent]; ok && b.per > 0 {
		have += float64(now.Sub(last)) / float64(b.per)
	}
	if have > float64(b.max) {
		have = float64(b.max)
	}
	return have
}

// spend takes cost from an agent's balance. It returns what is left, whether
// there was enough, and — when there was not — when there will be.
func (b *budgets) spend(agent string, cost int, now time.Time) (left int, ok bool, ready time.Time) {
	if !b.on() {
		return 0, true, time.Time{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	have := b.refilled(agent, now)
	b.at[agent] = now
	if have < float64(cost) {
		b.bal[agent] = have
		short := float64(cost) - have
		return int(have), false, now.Add(time.Duration(short * float64(b.per)))
	}
	have -= float64(cost)
	b.bal[agent] = have
	return int(have), true, time.Time{}
}

// refuse is the message an agent reads. It says the number, the way to spend
// less, and the time — a refusal that only says "no" is one an agent retries in
// a loop, and a transient refusal is exactly the kind it would retry.
func (b *budgets) refuse(agent string, kind bus.Kind, recipients, left int, ready time.Time) error {
	cost := b.price(kind, recipients)
	msg := fmt.Sprintf("%s has %d of %d and this costs %d", agent, left, b.max, cost)
	if recipients > 1 {
		msg += fmt.Sprintf(" (%d recipients)", recipients)
	}
	msg += fmt.Sprintf("; it can be sent at %s.", ready.Format("15:04:05"))
	if recipients > 1 {
		msg += " Fewer recipients costs less."
	}
	if kind != bus.KindAnswer && kind != bus.KindDone && kind != bus.KindBlocked {
		msg += " Answering, finishing and saying you are blocked cost least."
	}
	return fmt.Errorf("%s", msg)
}

// Budget is what an agent has left to say, for a client to show it.
type Budget struct {
	Left int `json:"left"`
	Max  int `json:"max"`
}

// BudgetLeft reports what an agent has, and whether a budget applies to it at
// all. Telling an agent its balance is the part of this that changes anything:
// a refusal stops one message, a number it can see before spending changes what
// it writes.
func (h *Hub) BudgetLeft(agent string) (Budget, bool) {
	if !h.budget.on() || !h.isAgent(agent) {
		return Budget{}, false
	}
	return Budget{Left: h.budget.balance(agent, time.Now()), Max: h.budget.max}, true
}
