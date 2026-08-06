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

// NewLog returns a log keeping at most max events.
func NewLog(max int) *Log {
	if max <= 0 {
		max = 500
	}
	return &Log{max: max, subs: make(map[uint64]chan Event)}
}

// Publish records an event and hands it to every subscriber. Subscribers that
// are not draining fast enough lose events rather than blocking the swarm.
func (l *Log) Publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	l.mu.Lock()
	l.entries = append(l.entries, e)
	if len(l.entries) > l.max {
		l.entries = l.entries[len(l.entries)-l.max:]
	}
	subs := make([]chan Event, 0, len(l.subs))
	for _, ch := range l.subs {
		subs = append(subs, ch)
	}
	l.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Publishf is Publish with formatting.
func (l *Log) Emit(kind Kind, agent, text string) {
	l.Publish(Event{Kind: kind, Agent: agent, Text: text})
}

// History returns the last n events, oldest first. n <= 0 means everything.
func (l *Log) History(n int) []Event {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if n <= 0 || n > len(l.entries) {
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
