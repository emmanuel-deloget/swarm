package hub

import (
	"fmt"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/agent"
	"github.com/emmanuel-deloget/swarm/internal/event"
	"github.com/emmanuel-deloget/swarm/internal/hook"
)

// What the fleet says about itself, outwards.
//
// The events swarm already logs are raw: a state changed, a process died. Two
// of them are worth deriving, because they are the ones a person would want to
// be told about and neither is a line in the log:
//
//   - agent.done — an agent stopped working and left changes behind it. Not "a
//     task finished", which swarm has no notion of: a transition plus a dirty
//     tree, which is as close as an agnostic tool can get, and close enough to
//     be worth a message.
//   - agent.gave_up — the restart streak ran out. Nobody is coming back for
//     that agent, and nothing else will say so.
//
// The rest pass through under their own names, so a rule can match anything the
// event log holds.

// Outgoing event names. They are what a rule matches on `event`.
const (
	OutStarted   = "agent.started"
	OutExited    = "agent.exited"
	OutIdle      = "agent.idle"
	OutDone      = "agent.done"
	OutGaveUp    = "agent.gave_up"
	OutAttention = "agent.attention"
	OutError     = "agent.error"
	OutMessage   = "bus.message"
)

// notify offers a notice to the sender, if there is one.
func (h *Hub) notify(name, kind, text string, data map[string]string) {
	if h.sender == nil {
		return
	}
	if data == nil {
		data = map[string]string{}
	}
	data["session"] = h.cfg.Session
	h.sender.Notify(hook.Notice{
		Event: kind,
		Agent: name,
		Text:  text,
		Data:  data,
		At:    time.Now(),
	})
}

// watchEvents turns the internal log into outgoing notices. It reads the same
// stream the TUI and `swarm events` read, so a rule can match anything either
// of them shows.
func (h *Hub) watchEvents(events <-chan event.Event) {
	for e := range events {
		switch e.Kind {
		case event.KindExited:
			h.notify(e.Agent, OutExited, e.Text, copyData(e.Data))
		case event.KindPattern:
			h.notify(e.Agent, OutAttention, e.Text, copyData(e.Data))
		case event.KindError:
			h.notify(e.Agent, OutError, e.Text, copyData(e.Data))
		case event.KindState:
			if e.Text == string(agent.StateStarting) {
				h.notify(e.Agent, OutStarted, e.Text, copyData(e.Data))
			}
		}
	}
}

func copyData(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// notifyIdle is the derived pair, raised where the transition is known: the
// agent has just fallen quiet. Whether it left work behind decides which of the
// two it is.
func (h *Hub) notifyIdle(name string) {
	if h.sender == nil {
		return
	}
	a, err := h.Agent(name)
	if err != nil {
		return
	}
	in := a.Info()
	data := map[string]string{"git": in.Git}
	if st, ok := a.GitState(); ok {
		data["branch"] = st.Branch
		data["dirty"] = fmt.Sprint(st.Dirty)
		data["ahead"] = fmt.Sprint(st.Ahead)
		data["behind"] = fmt.Sprint(st.Behind)
		if st.Dirty || st.Ahead > 0 {
			// Something to show for the work. This is the closest an agnostic
			// tool gets to "it finished": no notion of a task, just a fleet
			// that went quiet with changes under it.
			h.notify(name, OutDone, in.Git, data)
			return
		}
	}
	h.notify(name, OutIdle, in.Git, data)
}
