// Package event carries what happens in the swarm to whoever is watching:
// the TUI, the web clients, the log.
package event

import (
	"sync"
	"time"
)

// Kind classifies an event.
type Kind string

// Event kinds.
const (
	KindStarted Kind = "started"
	KindExited  Kind = "exited"
	KindState   Kind = "state"
	KindPattern Kind = "pattern"
	KindBell    Kind = "bell"
	KindMessage Kind = "message"
	KindInject  Kind = "inject"
	KindInfo    Kind = "info"
	KindError   Kind = "error"
)

// Event is one thing that happened.
type Event struct {
	At    time.Time         `json:"at"`
	Kind  Kind              `json:"kind"`
	Agent string            `json:"agent,omitempty"`
	Text  string            `json:"text"`
	Data  map[string]string `json:"data,omitempty"`
}

// Severity ranks an event for display purposes.
func (e Event) Severity() int {
	switch e.Kind {
	case KindError:
		return 2
	case KindExited, KindPattern, KindBell:
		return 1
	default:
		return 0
	}
}

// Log is a bounded event history with fan-out to live subscribers.
type Log struct {
	mu      sync.RWMutex
	entries []Event
	max     int
	next    uint64
	subs    map[uint64]chan Event
}

// NewLog returns a log keeping at most size events.
func NewLog(size int) *Log {
	if size <= 0 {
		size = 500
	}
	return &Log{max: size, subs: make(map[uint64]chan Event)}
}

// Publish records an event and hands it to every subscriber. Subscribers that
// are not draining fast enough lose events rather than blocking the swarm.
//
// The sends happen with the lock still held, and that is the point: a
// subscriber's cancel closes its channel under the same lock, so letting go of
// it first left a window where Publish would send on a channel cancel had just
// closed — a panic, not merely a race. The window is widest at shutdown, when
// the UI drops its subscription while agents are still reporting that they
// stopped. Holding the lock is safe because none of these sends can block: the
// default arm drops the event instead.
func (l *Log) Publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	l.entries = append(l.entries, e)
	if len(l.entries) > l.max {
		l.entries = l.entries[len(l.entries)-l.max:]
	}
	for _, ch := range l.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Emit publishes an event with just a kind, an agent and a message.
func (l *Log) Emit(kind Kind, agent, text string) {
	l.Publish(Event{Kind: kind, Agent: agent, Text: text})
}

// History returns the last n events, oldest first. n <= 0 means everything.
// History returns the last n events, oldest first. A negative n means all of
// them; zero means none, which is what `-n 0` asks for — watch what happens
// next, without the backlog.
func (l *Log) History(n int) []Event {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if n < 0 || n > len(l.entries) {
		n = len(l.entries)
	}
	out := make([]Event, n)
	copy(out, l.entries[len(l.entries)-n:])
	return out
}

// Subscribe returns a channel of future events and a function to detach.
func (l *Log) Subscribe(buffer int) (<-chan Event, func()) {
	if buffer <= 0 {
		buffer = 64
	}
	ch := make(chan Event, buffer)
	l.mu.Lock()
	l.next++
	id := l.next
	l.subs[id] = ch
	l.mu.Unlock()

	return ch, func() {
		l.mu.Lock()
		if c, ok := l.subs[id]; ok {
			delete(l.subs, id)
			close(c)
		}
		l.mu.Unlock()
	}
}
