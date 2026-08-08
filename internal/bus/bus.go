// Package bus stores the messages agents send each other.
//
// It is deliberately dumb: it keeps per-recipient mailboxes and wakes up
// waiters. Deciding whether a message is typed into a terminal (push) or left
// for the agent to collect (pull) is the hub's job.
package bus

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Kind classifies a message by what it asks of the reader.
type Kind string

// The kinds. They are deliberately few: a vocabulary nobody can remember is one
// nobody uses, and every extra name is another thing an agent gets wrong.
const (
	// KindNote is the default: something said, with nothing expected back.
	KindNote Kind = ""
	// KindQuestion expects an answer, KindAnswer is one.
	KindQuestion Kind = "question"
	KindAnswer   Kind = "answer"
	// KindFYI expects nothing at all, and saying so is the point: an agent that
	// acknowledges every notice has doubled the traffic for nothing.
	KindFYI Kind = "fyi"
	// KindRequest asks for work, KindDecision closes a matter, KindBlocked says
	// the sender cannot go on.
	KindRequest  Kind = "request"
	KindDecision Kind = "decision"
	KindBlocked  Kind = "blocked"
)

// Kinds lists every kind a message may carry, for validation and for help.
func Kinds() []Kind {
	return []Kind{KindQuestion, KindAnswer, KindFYI, KindRequest, KindDecision, KindBlocked}
}

// ValidKind reports whether k is one swarm understands. The empty kind is
// valid: not every message is worth classifying.
func ValidKind(k Kind) bool {
	if k == KindNote {
		return true
	}
	for _, v := range Kinds() {
		if v == k {
			return true
		}
	}
	return false
}

// Message is one note from a sender to one recipient. A broadcast becomes one
// Message per recipient, all sharing the same Thread.
type Message struct {
	ID     uint64    `json:"id"`
	Thread uint64    `json:"thread"`
	At     time.Time `json:"at"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	// Kind says what the message is for. It is what turns a count into a
	// verdict: twelve questions and no decisions in an hour is not a statistic.
	Kind Kind   `json:"kind,omitempty"`
	Body string `json:"body"`
	// Files are absolute paths staged in the shared directory, reachable by
	// every agent.
	Files []string `json:"files,omitempty"`
	// Final marks a message the bus refuses to let anyone answer. It is the
	// primitive that makes a decision a decision rather than another opening.
	Final bool `json:"final,omitempty"`

	// Pushed records that the message was typed into the recipient's terminal.
	Pushed bool `json:"pushed"`
	// ReadAt is when the recipient collected the message with `swarm inbox`.
	ReadAt time.Time `json:"read_at,omitzero"`
}

// Render expands a template. Placeholders: {id} {thread} {from} {to} {body}
// {files} {time}.
func (m Message) Render(tmpl string) string {
	r := strings.NewReplacer(
		"{id}", fmt.Sprint(m.ID),
		"{thread}", fmt.Sprint(m.Thread),
		"{from}", m.From,
		"{to}", m.To,
		"{body}", m.Body,
		"{files}", strings.Join(m.Files, " "),
		"{time}", m.At.Format(time.RFC3339),
		"{kind}", string(m.Kind),
	)
	out := r.Replace(tmpl)
	if len(m.Files) > 0 && !strings.Contains(tmpl, "{files}") {
		out += "\nattached: " + strings.Join(m.Files, " ")
	}
	return out
}

// Bus holds every mailbox.
type Bus struct {
	mu    sync.Mutex
	boxes map[string]*mailbox
	// recent is every message the bus has carried, newest last, bounded. The
	// per-mailbox histories cannot answer for the fleet: each is capped on its
	// own, so a chatty pair would push a quiet one out of a merged view.
	recent   []Message
	history  int
	nextID   uint64
	nextThrd uint64
}

type mailbox struct {
	// pending are the messages not yet collected, oldest first.
	pending []Message
	// seen is the bounded history of everything that arrived.
	seen []Message
	// waiters are the channels of clients blocked in Wait.
	waiters []chan struct{}
}

// New returns a bus keeping at most history messages per mailbox.
func New(history int) *Bus {
	if history <= 0 {
		history = 200
	}
	return &Bus{boxes: make(map[string]*mailbox), history: history}
}

func (b *Bus) box(agent string) *mailbox {
	m, ok := b.boxes[agent]
	if !ok {
		m = &mailbox{}
		b.boxes[agent] = m
	}
	return m
}

// Turns counts the messages already carried on a thread. A thread is what lets
// a conversation end: without one, nothing can be too long, because nothing is
// anything.
func (b *Bus) Turns(thread uint64) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, m := range b.recent {
		if m.Thread == thread {
			n++
		}
	}
	return n
}

// LastOn returns the most recent message carried on a thread, and whether there
// was one. It is how a reply learns what it is replying to.
func (b *Bus) LastOn(thread uint64) (Message, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.recent) - 1; i >= 0; i-- {
		if b.recent[i].Thread == thread {
			return b.recent[i], true
		}
	}
	return Message{}, false
}

// ThreadFor returns the thread an agent should answer on: the one carrying the
// last message it received. Inheriting rather than asking is what makes a
// conversation a conversation without anybody having to track an identifier.
func (b *Bus) ThreadFor(agent string) (uint64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.recent) - 1; i >= 0; i-- {
		if b.recent[i].To == agent {
			return b.recent[i].Thread, true
		}
	}
	return 0, false
}

// NewThread allocates a thread id, shared by all copies of a broadcast.
func (b *Bus) NewThread() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextThrd++
	return b.nextThrd
}

// Post files a message in the recipient's mailbox and returns it with its id
// assigned. Callers set Pushed afterwards with MarkPushed.
func (b *Bus) Post(m Message) Message {
	b.mu.Lock()
	b.nextID++
	m.ID = b.nextID
	if m.At.IsZero() {
		m.At = time.Now()
	}
	if m.Thread == 0 {
		b.nextThrd++
		m.Thread = b.nextThrd
	}
	b.recent = append(b.recent, m)
	if keep := b.history * 20; len(b.recent) > keep {
		b.recent = b.recent[len(b.recent)-keep:]
	}
	box := b.box(m.To)
	box.pending = append(box.pending, m)
	box.seen = append(box.seen, m)
	if len(box.seen) > b.history {
		box.seen = box.seen[len(box.seen)-b.history:]
	}
	if len(box.pending) > b.history {
		box.pending = box.pending[len(box.pending)-b.history:]
	}
	waiters := box.waiters
	box.waiters = nil
	b.mu.Unlock()

	for _, w := range waiters {
		close(w)
	}
	return m
}

// MarkPushed records that a message was injected into its recipient's
// terminal. A pushed message is no longer pending: the agent has it on screen.
func (b *Bus) MarkPushed(to string, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	box := b.box(to)
	for i := range box.seen {
		if box.seen[i].ID == id {
			box.seen[i].Pushed = true
		}
	}
	kept := box.pending[:0]
	for _, m := range box.pending {
		if m.ID != id {
			kept = append(kept, m)
		}
	}
	box.pending = kept
}

// Collect returns the pending messages of an agent and marks them read. When
// peek is true the messages stay pending.
func (b *Bus) Collect(agent string, peek bool) []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	box := b.box(agent)
	if len(box.pending) == 0 {
		return nil
	}
	out := make([]Message, len(box.pending))
	copy(out, box.pending)
	if peek {
		return out
	}
	now := time.Now()
	for i := range out {
		out[i].ReadAt = now
		for j := range box.seen {
			if box.seen[j].ID == out[i].ID {
				box.seen[j].ReadAt = now
			}
		}
	}
	box.pending = nil
	return out
}

// Pending counts the uncollected messages of an agent.
func (b *Bus) Pending(agent string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if box, ok := b.boxes[agent]; ok {
		return len(box.pending)
	}
	return 0
}

// PendingAll returns the uncollected count of every mailbox that has one.
func (b *Bus) PendingAll() map[string]int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]int, len(b.boxes))
	for name, box := range b.boxes {
		if len(box.pending) > 0 {
			out[name] = len(box.pending)
		}
	}
	return out
}

// History returns the last n messages seen by an agent, oldest first.
func (b *Bus) History(agent string, n int) []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	box, ok := b.boxes[agent]
	if !ok {
		return nil
	}
	if n <= 0 || n > len(box.seen) {
		n = len(box.seen)
	}
	out := make([]Message, n)
	copy(out, box.seen[len(box.seen)-n:])
	return out
}

// Wait blocks until the agent has a pending message or the timeout expires.
// A zero timeout waits forever. It reports whether a message is waiting.
func (b *Bus) Wait(agent string, timeout time.Duration, cancel <-chan struct{}) bool {
	b.mu.Lock()
	box := b.box(agent)
	if len(box.pending) > 0 {
		b.mu.Unlock()
		return true
	}
	ch := make(chan struct{})
	box.waiters = append(box.waiters, ch)
	b.mu.Unlock()

	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-ch:
		return b.Pending(agent) > 0
	case <-timer:
	case <-cancel:
	}
	b.removeWaiter(agent, ch)
	return b.Pending(agent) > 0
}

func (b *Bus) removeWaiter(agent string, ch chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	box, ok := b.boxes[agent]
	if !ok {
		return
	}
	kept := box.waiters[:0]
	for _, w := range box.waiters {
		if w != ch {
			kept = append(kept, w)
		}
	}
	box.waiters = kept
}
