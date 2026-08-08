package bus

import (
	"sort"
	"time"
)

// Recent returns the last n messages the bus has carried, oldest first, across
// every mailbox. A broadcast appears once per recipient, sharing a thread —
// which is what makes "who talks to whom" answerable at all.
//
// The bus keeps this alongside the per-mailbox histories because those cannot
// be merged after the fact: each is capped on its own, so a chatty pair would
// push a quiet one out of a combined view that never existed.
func (b *Bus) Recent(n int) []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > len(b.recent) {
		n = len(b.recent)
	}
	out := make([]Message, n)
	copy(out, b.recent[len(b.recent)-n:])
	return out
}

// Since returns the messages carried after id, oldest first. It is what lets a
// follower ask again without being handed everything it has already seen.
func (b *Bus) Since(id uint64) []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	i := sort.Search(len(b.recent), func(i int) bool { return b.recent[i].ID > id })
	out := make([]Message, len(b.recent)-i)
	copy(out, b.recent[i:])
	return out
}

// Pair counts what passed between two agents, in one direction.
type Pair struct {
	From, To string
	Count    int
}

// Stats is what the bus looked like over a window. It answers the question the
// terminals cannot: not what any one agent said, but how much of the fleet's
// time went into saying it.
type Stats struct {
	// Window is how far back the numbers reach, and Since when that starts.
	Window time.Duration
	Since  time.Time

	// Messages is the total, Threads the number of distinct conversations.
	Messages int
	Threads  int

	// Sent and Received are per agent. "user" appears among the senders, which
	// is worth seeing: a fleet where every message comes from you is not
	// coordinating, and one where none does may not need you.
	Sent     map[string]int
	Received map[string]int

	// Pairs are the directed exchanges, busiest first.
	Pairs []Pair

	// Deepest is the longest conversation in the window, measured in messages
	// on one thread. A thread nobody can end is the shape the trouble takes.
	Deepest int

	// Unread is what is still sitting in mailboxes.
	Unread map[string]int
}

// StatsSince summarises the messages carried after t.
func (b *Bus) StatsSince(t time.Time) Stats {
	msgs := b.Recent(0)
	now := time.Now()
	s := Stats{
		Window:   now.Sub(t),
		Since:    t,
		Sent:     map[string]int{},
		Received: map[string]int{},
		Unread:   b.PendingAll(),
	}

	threads := map[uint64]int{}
	pairs := map[Pair]int{}
	for _, m := range msgs {
		if m.At.Before(t) {
			continue
		}
		s.Messages++
		s.Sent[m.From]++
		s.Received[m.To]++
		threads[m.Thread]++
		pairs[Pair{From: m.From, To: m.To}]++
	}
	s.Threads = len(threads)
	for _, depth := range threads {
		if depth > s.Deepest {
			s.Deepest = depth
		}
	}
	for p, n := range pairs {
		p.Count = n
		s.Pairs = append(s.Pairs, p)
	}
	sort.Slice(s.Pairs, func(i, j int) bool {
		if s.Pairs[i].Count != s.Pairs[j].Count {
			return s.Pairs[i].Count > s.Pairs[j].Count
		}
		if s.Pairs[i].From != s.Pairs[j].From {
			return s.Pairs[i].From < s.Pairs[j].From
		}
		return s.Pairs[i].To < s.Pairs[j].To
	})
	return s
}

// SentSince counts what an agent has put on the bus since t. It is the cheap
// half of a talk-to-work ratio, and on its own it is already the signal worth
// watching: an agent posting nine messages in ten minutes is doing something
// other than the task.
func (b *Bus) SentSince(agent string, t time.Time) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for i := len(b.recent) - 1; i >= 0; i-- {
		if b.recent[i].At.Before(t) {
			break
		}
		if b.recent[i].From == agent {
			n++
		}
	}
	return n
}
