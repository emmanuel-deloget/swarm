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

// TestAnAgentMayOnlyTypeWhereItMayWrite: `swarm inject` went straight into a
// terminal, so an agent restricted to one peer could type into any of them.
// can_send is the rule for reaching a peer, whichever command was used.
func TestAnAgentMayOnlyTypeWhereItMayWrite(t *testing.T) {
	h := newFleet(t, restrictedAgents(t))
	srv := serve(t, h)

	_, err := Call(srv.Path(), Request{Cmd: CmdInject, From: "p1", Target: "dev-1",
		Text: "round the back", Submit: true})
	if err == nil {
		t.Fatal("p1 injected into an agent it may not write to")
	}
	if !strings.Contains(err.Error(), "cannot reach") {
		t.Errorf("the refusal should name what it may reach, said %q", err)
	}

	// Where it may, nothing else changes: an injection is still an injection.
	call(t, srv.Path(), Request{Cmd: CmdInject, From: "p1", Target: "chair",
		Text: "through the front", Submit: true})
	a, _ := h.Agent("chair")
	waitForText(t, a.Text, "saw:through the front")
	if strings.Contains(a.Text(), "[swarm] message from") {
		t.Error("the injection was rendered as a bus message; it should arrive as typed")
	}
}

// TestAnAgentKeepsTheOptionsOnlyInjectHas: an injection is not a message and
// cannot become one. -raw and -submit=false are the reason an agent driving a
// shell uses inject at all, and gating it must not cost them.
func TestAnAgentKeepsTheOptionsOnlyInjectHas(t *testing.T) {
	h := newFleet(t, restrictedAgents(t))
	srv := serve(t, h)

	for _, req := range []Request{
		{Cmd: CmdInject, From: "p1", Target: "chair", Text: "typed, not sent", Submit: false},
		{Cmd: CmdInject, From: "p1", Target: "chair", Text: "exact bytes\n", Submit: true, Raw: true},
	} {
		if _, err := Call(srv.Path(), req); err != nil {
			t.Errorf("an agent lost an option only inject has: %v", err)
		}
	}
	a, _ := h.Agent("chair")
	waitForText(t, a.Text, "saw:typed, not sentexact bytes")
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
