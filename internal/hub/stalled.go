package hub

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/config"
	"github.com/emmanuel-deloget/swarm/internal/event"
)

// What swarm does about a stalled agent, which is: ask.
//
// The state is a guess, and it has to stay one — an agent waiting on a
// nine-minute test gate is silent and does owe work, and no amount of watching
// the terminal will tell it apart from an agent that stopped. What separates
// them is a question, and the answer costs one exchange: "I am waiting on the
// gate" ends the doubt for everybody, and lands in the bus where the rest of
// the fleet can read it.
//
// So nothing here restarts, kills or reassigns anything. It sends messages.
// What happens next belongs to whoever is told — usually a triage agent, which
// knows what the work was and can open a real question the stalled agent then
// has to settle. That is the useful division: the hub notices, an agent with
// context decides.

// debtView is what the rules need to know about a debt, in a shape this package
// can name — the bus keeps its own type unexported, which is right for it and
// unhelpful here.
type debtView struct {
	Thread uint64
	From   string
	Kind   bus.Kind
	Since  time.Time
	Body   string
}

// stalledSeen counts what a rule has already done about one debt.
type stalledSeen struct {
	sent int
	last time.Time
}

// stalledKey identifies a rule applied to one debt. The thread is in it because
// repeats are counted per debt: settle it and the counter goes with it, and a
// new question later starts again from nothing.
type stalledKey struct {
	agent string
	rule  int
	thrd  uint64
}

// stalledActor applies the on_stalled rules.
type stalledActor struct {
	mu   sync.Mutex
	seen map[stalledKey]stalledSeen
}

// act runs the rules for one stalled agent. It is called from the watcher that
// already decides what stalled means, so this never has to guess.
func (h *Hub) act(name string, d debtView) {
	rules := h.cfg.Bus.OnStalled
	if len(rules) == 0 {
		return
	}
	if h.stalled == nil {
		return
	}

	// A debt swarm opened does not set the rules going again. Telling a triage
	// agent with a `question` is the useful shape — it owes an answer, so it
	// will deal with it — but that debt makes the triage agent stalled in turn,
	// and asking it where it is on a notice we sent it ourselves is swarm
	// generating work out of its own work. The state is still shown and still
	// reported; only the asking stops.
	if d.From == stallSender {
		return
	}

	age := time.Since(d.Since)
	for i, r := range rules {
		if age < r.After {
			continue // this rule is for later
		}
		key := stalledKey{agent: name, rule: i, thrd: d.Thread}

		h.stalled.mu.Lock()
		s := h.stalled.seen[key]
		switch {
		case s.sent >= r.Max:
			h.stalled.mu.Unlock()
			continue
		case s.sent > 0 && r.Every == 0:
			// Once for this debt, which is the default: enough for a gate that
			// takes ten minutes.
			h.stalled.mu.Unlock()
			continue
		case s.sent > 0 && time.Since(s.last) < r.Every:
			h.stalled.mu.Unlock()
			continue
		}
		s.sent, s.last = s.sent+1, time.Now()
		h.stalled.seen[key] = s
		lastOne := s.sent >= r.Max
		h.stalled.mu.Unlock()

		h.tellAboutStall(name, d, r, i, lastOne)
	}
}

// tellAboutStall sends one rule's message.
func (h *Hub) tellAboutStall(name string, d debtView, r config.StalledRule, idx int, lastOne bool) {
	to := r.To
	if to == config.StalledSelf {
		to = name
	}
	// An agent is never told about its own stall as though it were someone
	// else's problem: that case is `self`, and it reads differently.
	body := r.Text
	if body == "" {
		body = h.stallMessage(name, d, to == name)
	}

	opts := SendOptions{NewThread: true, Push: r.PushWanted()}
	if _, err := h.SendOn(stallSender, to, bus.Kind(r.Kind), body, nil, opts); err != nil {
		h.log.Emit(event.KindError, name, fmt.Sprintf(
			"on_stalled[%d]: could not tell %s: %v", idx, to, err))
		return
	}
	h.log.Emit(event.KindPattern, name, fmt.Sprintf(
		"on_stalled[%d]: asked %s about a %s owed to %s since %s",
		idx, to, d.Kind, d.From, d.Since.Format("15:04")))

	if lastOne {
		// Said out loud rather than simply stopping: a rule that goes quiet
		// looks exactly like a rule that worked.
		h.log.Emit(event.KindPattern, name, fmt.Sprintf(
			"on_stalled[%d]: that was the last of %d for this one; not asking again "+
				"until it is settled", idx, r.Max))
	}
}

// stallSender is who the message comes from. The same name the fleet already
// sees when a thread runs out of turns, because it is the same thing: the hub
// speaking for itself rather than pretending to be an agent.
const stallSender = "swarm"

// stallMessage is what swarm sends when the rule gives no text of its own.
//
// It is `swarm why`, addressed. The agent asking itself what it was doing is
// the one case where the answer is certainly unavailable — days of compaction
// have removed it — so the message carries what the bus still knows, and ends
// with the command that settles it.
func (h *Hub) stallMessage(name string, d debtView, toSelf bool) string {
	var b strings.Builder
	age := time.Since(d.Since).Round(time.Minute)

	if toSelf {
		fmt.Fprintf(&b, "[swarm] You have owed %s a %s since %s (%s), on thread %d, "+
			"and you have been quiet for a while.\n\n",
			d.From, d.Kind, d.Since.Format("15:04"), short(age), d.Thread)
	} else {
		fmt.Fprintf(&b, "[swarm] %s has owed %s a %s since %s (%s), on thread %d, "+
			"and has been quiet since.\n\n",
			name, d.From, d.Kind, d.Since.Format("15:04"), short(age), d.Thread)
	}

	if d.Body != "" {
		b.WriteString("What was asked:\n")
		for _, line := range strings.Split(strings.TrimRight(d.Body, "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
		b.WriteString("\n")
	}

	if toSelf {
		b.WriteString("If you are waiting on something long — a build, a gate, a review —\n")
		b.WriteString("say so and it is understood; one line is enough:\n")
		fmt.Fprintf(&b, "  swarm send -kind fyi -thread %d %s \"still waiting on …\"\n\n", d.Thread, d.From)
		// Both ways out, as `swarm why` gives them: an agent that has the answer
		// should answer, and one that finished the work should close it. Offering
		// only the first leaves someone who is done with nothing to type.
		b.WriteString("If you have the answer:\n  ")
		b.WriteString(SettleCommand(d.Kind, d.From, d.Thread))
		fmt.Fprintf(&b, "\n\nIf it is finished:\n  swarm done -thread %d \"what happened\"\n", d.Thread)
		b.WriteString("\nThis is a question, not an accusation: swarm cannot tell a long wait\n")
		b.WriteString("from a stop, and asking is cheaper than guessing.")
	} else {
		fmt.Fprintf(&b, "`swarm why %s` has the detail. It may simply be waiting on something long.\n", name)
	}
	return b.String()
}

// short prints a duration the way someone would say it.
func short(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%d days", int(d.Hours())/24)
	}
}

// forget drops what was counted for an agent whose debts are settled, so the
// next one starts from nothing.
func (h *Hub) forget(name string) {
	if h.stalled == nil {
		return
	}
	h.stalled.mu.Lock()
	defer h.stalled.mu.Unlock()
	for k := range h.stalled.seen {
		if k.agent == name {
			delete(h.stalled.seen, k)
		}
	}
}
