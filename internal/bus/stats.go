package bus

import (
	"sort"
	"strings"
	"time"
)

// Recent returns the last n messages the bus has carried, oldest first, across
// every mailbox. A broadcast appears once per recipient, sharing a thread —
// which is what makes "who talks to whom" answerable at all.
//
// A negative n means all of them; zero means none. Zero is a real request —
// `swarm bus tail -n 0 -f` asks to watch what happens next without being shown
// what already did — so it must not be read as "unset".
//
// The bus keeps this alongside the per-mailbox histories because those cannot
// be merged after the fact: each is capped on its own, so a chatty pair would
// push a quiet one out of a combined view that never existed.
func (b *Bus) Recent(n int) []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n < 0 || n > len(b.recent) {
		n = len(b.recent)
	}
	out := make([]Message, n)
	copy(out, b.recent[len(b.recent)-n:])
	return out
}

// All returns everything the bus still holds. It is Recent(-1) said plainly,
// for the callers that mean the whole ring rather than a tail of it.
func (b *Bus) All() []Message { return b.Recent(-1) }

// LastID is the id of the newest message, or zero when nothing has been
// carried. It is what a follower starts from when it asked for no history: from
// zero it would be handed everything at the first poll.
func (b *Bus) LastID() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.recent) == 0 {
		return 0
	}
	return b.recent[len(b.recent)-1].ID
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

	// Kinds counts what the messages were for. Twelve questions and no
	// decisions in an hour is a verdict, not a statistic — which is the whole
	// reason kinds exist.
	Kinds map[Kind]int

	// Deepest is the longest conversation in the window, measured in messages
	// on one thread. A thread nobody can end is the shape the trouble takes.
	Deepest int

	// Unread is what is still sitting in mailboxes.
	Unread map[string]int
}

// StatsSince summarises the messages carried after t.
func (b *Bus) StatsSince(t time.Time) Stats {
	msgs := b.All()
	now := time.Now()
	s := Stats{
		Window:   now.Sub(t),
		Since:    t,
		Sent:     map[string]int{},
		Received: map[string]int{},
		Kinds:    map[Kind]int{},
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
		if m.Kind != KindNote {
			s.Kinds[m.Kind]++
		}
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

// Thread is a conversation seen from outside: who is in it, how long it has
// run, and whether anything closed it. A thread is the only unit on the bus
// that can be *too long*, so it is the only one worth listing on its own.
type Thread struct {
	ID       uint64
	Turns    int
	Started  time.Time
	Last     time.Time
	Agents   []string
	Subject  string
	Final    bool
	Escalate bool
}

// Threads summarises the conversations still in the ring, busiest first. budget
// is the turn allowance the bus enforces; zero means none, and Escalate stays
// false.
func (b *Bus) Threads(budget int) []Thread {
	msgs := b.All()
	byID := map[uint64]*Thread{}
	seen := map[uint64]map[string]bool{}
	var order []uint64
	for _, m := range msgs {
		t, ok := byID[m.Thread]
		if !ok {
			t = &Thread{ID: m.Thread, Started: m.At, Subject: summarizeBody(m.Body)}
			byID[m.Thread] = t
			seen[m.Thread] = map[string]bool{}
			order = append(order, m.Thread)
		}
		t.Turns++
		t.Last = m.At
		t.Final = m.Final
		for _, who := range []string{m.From, m.To} {
			if !seen[m.Thread][who] {
				seen[m.Thread][who] = true
				t.Agents = append(t.Agents, who)
			}
		}
	}
	out := make([]Thread, 0, len(order))
	for _, id := range order {
		t := byID[id]
		t.Escalate = budget > 0 && t.Turns >= budget
		out = append(out, *t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

// summarizeBody is the first line of a message, short enough to sit in a
// column: what the conversation is about, not what was said in it.
func summarizeBody(body string) string {
	s := strings.TrimSpace(body)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 60 {
		s = s[:60] + "…"
	}
	return s
}
