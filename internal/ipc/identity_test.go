package ipc

import (
	"os"
	"strings"
	"testing"
)

// restrictedAgents adds a pair with a can_send between them to the standard
// test fleet: chair may write to anyone, p1 only to chair.
func restrictedAgents(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return `  - name: chair
    can_send: [all]
    command: ['` + self + `', '-swarm-probe', 'print', 'ready', 'lines', 'saw:']
  - name: p1
    can_send: [chair]
    command: ['` + self + `', '-swarm-probe', 'print', 'ready', 'lines', 'saw:']
`
}

// TestAnInjectionFromAnAgentGoesOnTheBus: `swarm inject` typed straight into a
// terminal, so it went round can_send, left no trace in `swarm bus tail`, and
// a paused bus did not hold it. Whatever command an agent uses, an agent
// writing to an agent is the fleet talking to itself.
func TestAnInjectionFromAnAgentGoesOnTheBus(t *testing.T) {
	h := newFleet(t, restrictedAgents(t))
	srv := serve(t, h)

	// Where it may not write, it may not inject either.
	_, err := Call(srv.Path(), Request{Cmd: CmdInject, From: "p1", Target: "dev-1",
		Text: "round the back", Submit: true})
	if err == nil {
		t.Fatal("p1 injected into an agent it may not write to")
	}
	if !strings.Contains(err.Error(), "cannot reach") {
		t.Errorf("the refusal should name what it may reach, said %q", err)
	}

	// Where it may, the message is carried — and recorded.
	ok := call(t, srv.Path(), Request{Cmd: CmdInject, From: "p1", Target: "chair",
		Text: "through the front", Submit: true})
	if len(ok.Messages) != 1 {
		t.Fatalf("an injection from an agent returned %d messages, want one on the bus", len(ok.Messages))
	}
	if got := ok.Messages[0].From; got != "p1" {
		t.Errorf("the bus recorded %q as the sender", got)
	}
	if n := len(h.Bus().All()); n == 0 {
		t.Error("the injection left no trace on the bus")
	}
}

// TestAnInjectionAnAgentCannotExpressIsRefused: -submit=false and -raw have no
// bus equivalent, and dropping them quietly would be worse than saying so —
// -submit=false exists precisely so the newline is not sent.
func TestAnInjectionAnAgentCannotExpressIsRefused(t *testing.T) {
	h := newFleet(t, restrictedAgents(t))
	srv := serve(t, h)
	_ = h

	for _, req := range []Request{
		{Cmd: CmdInject, From: "p1", Target: "chair", Text: "no newline", Submit: false},
		{Cmd: CmdInject, From: "p1", Target: "chair", Text: "raw bytes", Submit: true, Raw: true},
	} {
		_, err := Call(srv.Path(), req)
		if err == nil {
			t.Errorf("an injection the bus cannot carry was accepted: %+v", req)
			continue
		}
		if !strings.Contains(err.Error(), "no equivalent") {
			t.Errorf("the refusal should say what cannot be carried, said %q", err)
		}
	}
}

// TestKeysAreCheckedRatherThanCarried: a key press is not a message, so it
// cannot go on the bus — but an agent may still only press keys where it may
// write.
func TestKeysAreCheckedRatherThanCarried(t *testing.T) {
	h := newFleet(t, restrictedAgents(t))
	srv := serve(t, h)
	_ = h

	_, err := Call(srv.Path(), Request{Cmd: CmdKeys, From: "p1", Target: "dev-1", Keys: "enter"})
	if err == nil {
		t.Fatal("p1 pressed keys in an agent it may not write to")
	}
	if !strings.Contains(err.Error(), "cannot reach") {
		t.Errorf("the refusal should name what it may reach, said %q", err)
	}

	// A person is not an agent, and is not restricted.
	if _, err := Call(srv.Path(), Request{Cmd: CmdKeys, Target: "dev-1", Keys: "enter"}); err != nil {
		t.Errorf("an operator's key press was refused: %v", err)
	}
}

// TestASenderHasToExist: the bus records who said what, and `swarm bus stats`
// reads it back. A sender nobody configured makes that record fiction.
func TestASenderHasToExist(t *testing.T) {
	h := newFleet(t, "")
	srv := serve(t, h)
	_ = h

	_, err := Call(srv.Path(), Request{Cmd: CmdSend, From: "nobody-here", Target: "dev-1", Text: "hello"})
	if err == nil {
		t.Fatal("a message was accepted from an agent that does not exist")
	}
	if !strings.Contains(err.Error(), "no agent named") {
		t.Errorf("the refusal should name the problem, said %q", err)
	}
}
