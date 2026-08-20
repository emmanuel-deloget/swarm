package hub

import (
	"strings"
	"testing"
	"time"
)

const pushOnly = `
web: {enabled: false}
agents:
  - name: alpha
    command: [probe-echo]
    delivery: push
  - name: beta
    command: [probe-echo]
    delivery: pull
  - name: gamma
    command: [probe-echo]
    delivery: defer
`

// TestWaitingIsRefusedWhenNothingIsFiled: an agent whose messages are typed
// into its terminal has a mailbox that never fills, so a wait on it blocks for
// the whole timeout and then reports nothing — every time. Agents ask for it
// liberally, so swarm answers at once and says why.
//
// Only push. defer leaves the message in the box until the agent falls quiet,
// and pull is what the box is for: both are worth waiting on.
func TestWaitingIsRefusedWhenNothingIsFiled(t *testing.T) {
	h := fleet(t, pushOnly)

	start := time.Now()
	_, note, err := h.Inbox("alpha", false, 30*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("a push-delivered agent waited %s", elapsed)
	}
	if !strings.Contains(note, "typed straight into") {
		t.Errorf("the note should say where the messages went, said %q", note)
	}

	for _, name := range []string{"beta", "gamma"} {
		start := time.Now()
		_, note, err := h.Inbox(name, false, 300*time.Millisecond, nil)
		if err != nil {
			t.Fatal(err)
		}
		if note != "" {
			t.Errorf("%s can be waited on; got the note %q", name, note)
		}
		if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
			t.Errorf("%s returned after %s without waiting", name, elapsed)
		}
	}
}

// TestAPausedBusIsWorthWaitingOn: a pause queues everything, whatever each
// agent asked for, so the mailbox of a push-delivered agent does fill.
func TestAPausedBusIsWorthWaitingOn(t *testing.T) {
	h := fleet(t, pushOnly)
	h.Pause("shipping")

	start := time.Now()
	_, note, err := h.Inbox("alpha", false, 300*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		t.Errorf("waiting was refused while the bus was paused: %q", note)
	}
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Errorf("returned after %s without waiting", elapsed)
	}
}

// TestOneKindLeftForCollectionIsEnough: delivery_by_kind overrides every
// agent's mode for one kind of message, so a single pull entry means a
// push-delivered agent has something to wait for after all.
func TestOneKindLeftForCollectionIsEnough(t *testing.T) {
	h := fleet(t, `
web: {enabled: false}
delivery_by_kind: {blocked: pull}
agents:
  - name: alpha
    command: [probe-echo]
    delivery: push
`)
	_, note, err := h.Inbox("alpha", false, 300*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		t.Errorf("blocked messages are left for collection; got the note %q", note)
	}
}
