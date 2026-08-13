package bus

import (
	"sort"
	"time"
)

// What is owed.
//
// An agent is idle when it has printed nothing for a while. That says nothing
// about whether it should be doing something: an agent waiting for work and an
// agent that stopped halfway through a request look exactly the same on screen,
// and neither can be told apart by watching the terminal.
//
// The bus knows, because the bus is what asked. A question, a request or a
// blocked message addressed to an agent opens a debt; an answer, a decision or
// a done from that agent closes it. Nothing is inferred from the screen, from
// the file system, or from what the agent said — only from what it was sent and
// what it sent back.
//
// Two things this deliberately does not see. Work given by hand — `swarm
// inject`, or typing into the terminal — never touches the bus, so it opens no
// debt; swarm knows what it delivered, not what you typed. And an agent that
// forgets to close a debt stays in it, which is why being owed something is a
// signal and never an action.

// opens reports whether a kind asks something of its recipient.
func opens(k Kind) bool {
	switch k {
	case KindQuestion, KindRequest, KindBlocked:
		return true
	}
	return false
}

// closes reports whether a kind settles what its sender was asked.
func closes(k Kind) bool {
	switch k {
	case KindAnswer, KindDecision, KindDone:
		return true
	}
	return false
}

// debt is one thing owed: which thread it belongs to, and since when.
type debt struct {
	Thread uint64
	Since  time.Time
	// From is who asked, so an acknowledgement knows where to go back to.
	From string
	// Kind is what was asked, so a report can say what kind of silence this is.
	Kind Kind
	// Body is what was asked, in words.
	//
	// Carried by the debt rather than looked up when needed, because the two
	// have different lifetimes: messages are bounded and debts are not, so the
	// question an agent has been sitting on for three days is exactly the one
	// whose text has been pushed out of history by everything said since. A
	// debt that knows why it exists is one a restart can restore whole.
	Body string
}

// debtBodyMax bounds what a debt carries of the message that opened it. Debts
// are unbounded in number and outlive everything, and a fleet that staged a
// large file into a message would otherwise keep a copy of it for as long as
// nobody answered.
const debtBodyMax = 2000

func clipBody(s string) string {
	if len(s) <= debtBodyMax {
		return s
	}
	return s[:debtBodyMax] + "\n[…truncated; the message itself is in `swarm bus tail` while it lasts]"
}

// track records what a message opens or closes. The caller holds mu.
func (b *Bus) track(m Message) {
	switch {
	case opens(m.Kind):
		if b.owed == nil {
			b.owed = map[string][]debt{}
		}
		for _, d := range b.owed[m.To] {
			if d.Thread == m.Thread {
				return // already owed on this thread; asking twice is not two debts
			}
		}
		b.owed[m.To] = append(b.owed[m.To], debt{
			Thread: m.Thread, Since: m.At, From: m.From, Kind: m.Kind,
			Body: clipBody(m.Body),
		})
	case closes(m.Kind):
		// The sender settles what it owed on this thread. A close with no debt
		// is not an error: an agent may report done for work it was given by
		// hand, and being told is better than being right.
		kept := b.owed[m.From][:0]
		for _, d := range b.owed[m.From] {
			if d.Thread != m.Thread {
				kept = append(kept, d)
			}
		}
		if len(kept) == 0 {
			delete(b.owed, m.From)
		} else {
			b.owed[m.From] = kept
		}
	}
}

// Owed returns what an agent has been asked and has not settled, oldest first.
func (b *Bus) Owed(agent string) []debt {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]debt, len(b.owed[agent]))
	copy(out, b.owed[agent])
	return out
}

// OwedSince returns the oldest debt of an agent, and whether it has one. It is
// what decides how long a silence has gone unexplained.
func (b *Bus) OwedSince(agent string) (debt, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var oldest debt
	for _, d := range b.owed[agent] {
		if oldest.Since.IsZero() || d.Since.Before(oldest.Since) {
			oldest = d
		}
	}
	return oldest, !oldest.Since.IsZero()
}

// Settle closes every debt an agent owes, and reports what was closed. It is
// what `swarm done` calls when no thread is named: an agent that says it has
// finished has finished with all of it.
func (b *Bus) Settle(agent string, thread uint64) []debt {
	b.mu.Lock()
	defer b.mu.Unlock()
	var closed, kept []debt
	for _, d := range b.owed[agent] {
		if thread == 0 || d.Thread == thread {
			closed = append(closed, d)
			continue
		}
		kept = append(kept, d)
	}
	if len(kept) == 0 {
		delete(b.owed, agent)
	} else {
		b.owed[agent] = kept
	}
	return closed
}

// Opened returns the message that opened a debt: the one that reached this
// agent on this thread and asked something of it.
//
// It is what turns "dev-22 owes an answer to triage-1" into something a reader
// can act on, and it is the part most likely to be missing — a debt lives until
// it is settled, but the messages are bounded, so a question left unanswered
// for two days may well have been pushed out of history by everything said
// since.
//
// live says the copy came from the recipient's own mailbox, where Pushed and
// ReadAt are kept up to date. The fleet-wide history holds the message as it
// was posted and is never marked afterwards, so its copy can say what was
// asked but must not be asked whether it was delivered — it would always
// answer no, which is a wrong answer rather than an unknown one.
func (b *Bus) Opened(agent string, thread uint64) (m Message, live, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if box, found := b.boxes[agent]; found {
		for _, m := range box.seen {
			if m.Thread == thread && opens(m.Kind) {
				return m, true, true
			}
		}
	}
	for _, m := range b.recent {
		if m.Thread == thread && m.To == agent && opens(m.Kind) {
			return m, false, true
		}
	}
	return Message{}, false, false
}

// Closing returns the kinds that settle a debt.
//
// Derived from Kinds and closes rather than written out, so that a new kind is
// classified in exactly one place. The list has a reader: telling someone how
// to get out of a debt means naming these, and a hand-kept copy of them would
// be wrong the first time the classification changed — which is the failure
// this area already had once, when `done` existed without settling anything.
func Closing() []Kind {
	var out []Kind
	for _, k := range Kinds() {
		if closes(k) {
			out = append(out, k)
		}
	}
	return out
}

// Owing is one debt, with the agent that carries it, in a form that can be
// written down. The bus keeps debts in memory and a process does not last
// forever; what follows is how they outlive it.
type Owing struct {
	Agent  string    `json:"agent"`
	Thread uint64    `json:"thread"`
	Since  time.Time `json:"since"`
	From   string    `json:"from"`
	Kind   Kind      `json:"kind"`
	Body   string    `json:"body,omitempty"`
}

// Snapshot returns everything currently owed, and the next thread id.
//
// The thread id matters as much as the debts. Restoring a debt on thread 42
// into a bus whose counter starts at zero means the forty-second conversation
// after the restart collides with it, and settling one settles the other —
// a debt closed by a message that had nothing to do with it.
func (b *Bus) Snapshot() (owing []Owing, nextThread uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for agent, debts := range b.owed {
		for _, d := range debts {
			owing = append(owing, Owing{
				Agent: agent, Thread: d.Thread, Since: d.Since,
				From: d.From, Kind: d.Kind, Body: d.Body,
			})
		}
	}
	// Sorted, because this comes out of a map and a caller comparing one
	// snapshot to the next to decide whether anything changed would otherwise
	// see a change every time and rewrite the file for ever.
	sort.Slice(owing, func(i, j int) bool {
		if owing[i].Agent != owing[j].Agent {
			return owing[i].Agent < owing[j].Agent
		}
		return owing[i].Thread < owing[j].Thread
	})
	return owing, b.nextThrd
}

// Restore puts debts back, and moves the thread counter past anything they
// mention. Existing debts are kept: this is meant for a bus that has just been
// made, but a restore that dropped live state would be a worse failure than one
// that duplicated it, and duplicates cannot happen — a thread already owed by
// an agent is not owed twice.
//
// nextThread is honoured only if it is ahead of where the bus already is, so a
// stale file cannot wind the counter backwards into ids that are in use.
func (b *Bus) Restore(owing []Owing, nextThread uint64) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.owed == nil {
		b.owed = map[string][]debt{}
	}
	n := 0
	for _, o := range owing {
		dup := false
		for _, d := range b.owed[o.Agent] {
			if d.Thread == o.Thread {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		b.owed[o.Agent] = append(b.owed[o.Agent], debt{
			Thread: o.Thread, Since: o.Since, From: o.From, Kind: o.Kind, Body: o.Body,
		})
		if o.Thread > b.nextThrd {
			b.nextThrd = o.Thread
		}
		n++
	}
	if nextThread > b.nextThrd {
		b.nextThrd = nextThread
	}
	return n
}
