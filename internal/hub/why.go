package hub

import (
	"fmt"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/agent"
)

// Why an agent is in the state it is in.
//
// The states swarm shows are cheap to display and expensive to explain, and
// stalled is the worst of them: it is the one that means something is wrong,
// and the one nobody can act on. An agent stalled for two days has been through
// several context compactions by then, so the message that put it there is gone
// from its own memory — the one place a reader would think to look.
//
// The bus still has it. A debt is not bounded: it lives until it is settled, so
// who asked, what kind of thing was asked, on which thread and since when are
// all still here. That makes swarm the fleet's external memory — the only part
// of it that does not get compacted — and not answering the question was simply
// a waste of what it already knew.
//
// What is bounded is the messages. A debt can outlive the text that opened it,
// and when it does this says so rather than leaving a blank where a sentence
// should be.

// Why is the explanation of one agent's state.
type Why struct {
	Agent string `json:"agent"`
	// State is what `swarm ls` shows.
	State string `json:"state"`
	// Stalled is the state that has a threshold behind it, kept separate
	// because an agent can be idle and owe nothing at all.
	Stalled bool `json:"stalled"`
	// Quiet is how long since the agent last printed anything. Unrounded:
	// rounding is a decision about how to show a number, and the caller that
	// shows it is the one that should make it.
	Quiet time.Duration `json:"quiet"`
	// StalledAfter is the configured threshold, so a reader can tell "not yet"
	// from "never will be".
	StalledAfter time.Duration `json:"stalled_after"`
	// Debts are what the agent has been asked and has not settled, oldest
	// first. Empty is an answer too: it means nothing is waiting on this agent,
	// whatever it looks like on screen.
	Debts []Debt `json:"debts,omitempty"`
	// Gone is set when the agent is an ephemeral instance that has finished,
	// been collected or died with the swarm. Nil for an agent that is running.
	Gone *Gone `json:"gone,omitempty"`
}

// whyDead explains an instance that is no longer running. What it was, what it
// was asked, when it went and why — plus whatever it still owes, since that is
// the reason anyone is asking.
func (h *Hub) whyDead(gone Gone) Why {
	w := Why{
		Agent: gone.Name,
		State: "gone",
		Gone:  &gone,
	}
	for _, d := range h.bus.Owed(gone.Name) {
		w.Debts = append(w.Debts, Debt{
			Thread: d.Thread,
			From:   d.From,
			Kind:   string(d.Kind),
			Since:  d.Since,
			Age:    time.Since(d.Since),
			Text:   d.Body,
			Kept:   d.Body != "",
			Settle: SettleCommand(d.Kind, d.From, d.Thread),
		})
	}
	return w
}

// Debt is one thing owed, with the message that opened it when the bus still
// has it.
type Debt struct {
	Thread uint64        `json:"thread"`
	From   string        `json:"from"`
	Kind   string        `json:"kind"`
	Since  time.Time     `json:"since"`
	Age    time.Duration `json:"age"`
	// Text is the message that opened the debt. Kept is false when the bus no
	// longer holds it, which is not the same as an empty message.
	Text  string   `json:"text,omitempty"`
	Kept  bool     `json:"kept"`
	Files []string `json:"files,omitempty"`
	// Delivered says the message was typed into the agent's terminal, Read
	// that the agent collected it with `swarm inbox`. Neither being true is a
	// different failure from an agent that saw the message and stopped: one is
	// a delivery problem, the other is the agent.
	Delivered bool `json:"delivered"`
	Read      bool `json:"read"`
	// DeliveryKnown says the two above were read from the recipient's mailbox
	// and mean something. False when only the fleet history still had the
	// message, where delivery is not recorded.
	DeliveryKnown bool `json:"delivery_known"`
	// Settle is the command that closes this debt, from the one function that
	// also writes it into the message itself.
	Settle string `json:"settle"`
}

// Why explains one agent. An unknown name is an error rather than an empty
// answer: "nothing is wrong with dev-99" is a bad thing to tell someone who
// misspelled dev-9.
func (h *Hub) Why(name string) (Why, error) {
	var in agent.Info
	var found bool
	for _, i := range h.Infos() {
		if i.Name == name {
			in, found = i, true
			break
		}
	}
	if !found {
		// An ephemeral instance that is gone still answers. Its debt can
		// outlive it — that is the rule for one a person spawned — and a debt
		// whose owner is reported as never having existed is worse than no
		// answer at all: it reads as a typo rather than as a death.
		if gone, ok := h.Dead(name); ok {
			return h.whyDead(gone), nil
		}
		return Why{}, fmt.Errorf("no agent named %q in this swarm", name)
	}

	w := Why{
		Agent:        in.Name,
		State:        string(in.State),
		Stalled:      in.Stalled,
		Quiet:        time.Since(in.LastOutput),
		StalledAfter: h.cfg.Bus.StalledAfter,
	}
	// The threshold an agent is actually measured against, since its own
	// idle_after is added to the fleet's setting.
	if ac, ok := h.cfg.Agent(in.Name); ok && w.StalledAfter > 0 {
		w.StalledAfter += ac.IdleAfter
	}

	for _, d := range h.bus.Owed(name) {
		debt := Debt{
			Thread: d.Thread,
			From:   d.From,
			Kind:   string(d.Kind),
			Since:  d.Since,
			Age:    time.Since(d.Since),
		}
		debt.Settle = SettleCommand(d.Kind, d.From, d.Thread)
		// The debt's own copy first: it is the one that survives both the
		// bounded history and a restart of this process.
		if d.Body != "" {
			debt.Text, debt.Kept = d.Body, true
		}
		if m, live, ok := h.bus.Opened(name, d.Thread); ok {
			debt.Kept, debt.Files = true, m.Files
			if debt.Text == "" {
				debt.Text = m.Body
			}
			// Only from the mailbox copy. Whether a message was typed into the
			// terminal or collected tells a reader whether the silence is the
			// delivery's fault or the agent's — and the history's copy is not
			// marked, so asking it would answer no to both, every time.
			if live {
				debt.DeliveryKnown = true
				debt.Delivered, debt.Read = m.Pushed, !m.ReadAt.IsZero()
			}
		}
		w.Debts = append(w.Debts, debt)
	}
	return w, nil
}
