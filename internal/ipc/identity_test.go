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

// TestNoAgentShutsTheFleetDown: stopping one instance is work a parent hands
// out and takes back. Ending the fleet is not work, and an agent that decides
// it is finished takes everyone else with it.
func TestNoAgentShutsTheFleetDown(t *testing.T) {
	h := newFleet(t, "")
	srv := serve(t, h)
	_ = h

	_, err := Call(srv.Path(), Request{Cmd: CmdShutdown, From: "dev-1"})
	if err == nil {
		t.Fatal("an agent shut the fleet down")
	}
	if !strings.Contains(err.Error(), "operator") {
		t.Errorf("the refusal should say whose it is, said %q", err)
	}

	// A person is not an agent.
	if _, err := Call(srv.Path(), Request{Cmd: CmdShutdown}); err != nil {
		t.Errorf("the operator was refused: %v", err)
	}
}

// TestTellingTheFleetIsASendLikeAnyOther: `swarm remember -tell` puts the entry
// on the bus, and it goes through the bus rather than beside it — can_send
// bounds it and the budget charges it once per recipient. A memory that could
// notify everybody on every write, unbudgeted, would be a broadcast channel
// next to the one that was just given a price.
func TestTellingTheFleetIsASendLikeAnyOther(t *testing.T) {
	h := newFleet(t, restrictedAgents(t))
	srv := serve(t, h)

	// p1 may write to chair and nowhere else.
	_, err := Call(srv.Path(), Request{Cmd: CmdRemember, From: "p1",
		Name: "gate-runtime", Text: "8-12 min", Target: "dev-1"})
	if err != nil {
		t.Fatalf("the whole command failed where only the telling should have: %v", err)
	}

	// The entry is written even so: it is the valuable half, and losing it
	// because can_send said no would be the wrong trade.
	held := call(t, srv.Path(), Request{Cmd: CmdRecall})
	if len(held.Memory) != 1 || held.Memory[0].Key != "gate-runtime" {
		t.Errorf("a refused telling took the entry with it: %+v", held.Memory)
	}

	// And where it may write, the message goes.
	resp := call(t, srv.Path(), Request{Cmd: CmdRemember, From: "p1",
		Name: "spec-281", Text: "v9 approved", Target: "chair"})
	if len(resp.Messages) != 1 {
		t.Fatalf("telling an agent it may reach carried %d messages", len(resp.Messages))
	}
	if !strings.Contains(resp.Messages[0].Body, "spec-281") {
		t.Errorf("the notice does not name the entry: %q", resp.Messages[0].Body)
	}
	if !strings.Contains(resp.Messages[0].Body, "swarm recall") {
		t.Errorf("the notice does not say where the record is: %q", resp.Messages[0].Body)
	}
}

// TestWritingWithoutTellingTellsNobody: notifying on every write would make the
// memory a platform, which is the shape a fleet runs away in.
func TestWritingWithoutTellingTellsNobody(t *testing.T) {
	h := newFleet(t, "")
	srv := serve(t, h)
	_ = h

	resp := call(t, srv.Path(), Request{Cmd: CmdRemember, Name: "gate", Text: "8-12 min"})
	if len(resp.Messages) != 0 {
		t.Errorf("a plain write put %d messages on the bus", len(resp.Messages))
	}
}
