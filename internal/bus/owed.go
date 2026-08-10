package bus

import "time"

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
