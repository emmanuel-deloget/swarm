package hub

import (
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/bus"
)

// waitFor gives an escalation time to happen: it is deliberately not on the
// caller's goroutine, so that a refusal is not delayed by the alarm it raises.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	waitUntil(t, 2*time.Second, cond)
}

// waitUntil is waitFor with the patience spelled out, for the tests that wait
// on a schedule they do not control — a shell loop, a ticker — where a CI
// machine under load is slower than any margin picked on a laptop.
func waitUntil(t *testing.T, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting")
}

const twoAgents = `
web: {enabled: false}
bus: {max_turns: 3}
agents:
  - name: alpha
    command: [probe-echo]
  - name: beta
    command: [probe-echo]
`

// TestAReplyStaysOnTheThread: nobody carries an identifier around. An agent
// answers whoever wrote to it, and the bus is the one that remembers what that
// conversation was.
func TestAReplyStaysOnTheThread(t *testing.T) {
	h := fleet(t, twoAgents)

	first, err := h.Send("user", "alpha", "have a look at this", nil)
	if err != nil {
		t.Fatal(err)
	}
	thread := first[0].Thread

	reply, err := h.SendKind("alpha", "beta", bus.KindQuestion, "what do you make of it?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if reply[0].Thread != thread {
		t.Errorf("reply opened thread #%d, want #%d: an answer is not a new subject",
			reply[0].Thread, thread)
	}
}

// TestTheUserAlwaysStartsSomething: a person writing to an agent is starting a
// conversation, not continuing whatever that agent was last in the middle of.
func TestTheUserAlwaysStartsSomething(t *testing.T) {
	h := fleet(t, twoAgents)

	first, _ := h.Send("user", "alpha", "one thing", nil)
	second, err := h.Send("user", "alpha", "an unrelated thing", nil)
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Thread == first[0].Thread {
		t.Error("two user messages landed on one thread")
	}
}

// TestAThreadRunsOutOfTurns is the whole point of a budget: the refusal is the
// instruction, because the only thing that reads it is an agent.
func TestAThreadRunsOutOfTurns(t *testing.T) {
	h := fleet(t, twoAgents)

	if _, err := h.Send("user", "alpha", "start", nil); err != nil {
		t.Fatal(err)
	}
	// max_turns is 3, and the opening message is one of them.
	if _, err := h.SendKind("alpha", "beta", bus.KindQuestion, "and?", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SendKind("beta", "alpha", bus.KindAnswer, "well", nil); err != nil {
		t.Fatal(err)
	}

	_, err := h.SendKind("alpha", "beta", bus.KindQuestion, "but still", nil)
	if err == nil {
		t.Fatal("a fourth turn went through on a three-turn budget")
	}
	for _, want := range []string{"3 turns", "decide alone"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should say %q, said %q", want, err)
		}
	}
}

// TestANewThreadEscapesTheBudget: the bound is on one conversation, not on an
// agent. Something genuinely else to say is always allowed.
func TestANewThreadEscapesTheBudget(t *testing.T) {
	h := fleet(t, twoAgents)

	_, _ = h.Send("user", "alpha", "start", nil)
	_, _ = h.SendKind("alpha", "beta", bus.KindQuestion, "and?", nil)
	_, _ = h.SendKind("beta", "alpha", bus.KindAnswer, "well", nil)

	msgs, err := h.SendOn("alpha", "beta", bus.KindFYI, "unrelated", nil, SendOptions{NewThread: true})
	if err != nil {
		t.Fatalf("a new subject was refused: %v", err)
	}
	if h.Bus().Turns(msgs[0].Thread) != 1 {
		t.Error("the new thread inherited turns from the old one")
	}
}

// TestAFinalAnswerCannotBeAnswered: a decision that can be reopened is not a
// decision. Only the recipient is stopped — whoever sent it may still speak.
func TestAFinalAnswerCannotBeAnswered(t *testing.T) {
	h := fleet(t, `
web: {enabled: false}
agents:
  - name: alpha
    command: [probe-echo]
  - name: beta
    command: [probe-echo]
`)
	_, _ = h.Send("user", "alpha", "start", nil)
	if _, err := h.SendOn("alpha", "beta", bus.KindDecision, "we do it this way", nil,
		SendOptions{Final: true}); err != nil {
		t.Fatal(err)
	}

	_, err := h.SendKind("beta", "alpha", bus.KindQuestion, "yes but", nil)
	if err == nil {
		t.Fatal("a final answer was answered")
	}
	if !strings.Contains(err.Error(), "act on it") {
		t.Errorf("the refusal should say what to do instead, said %q", err)
	}
}

// TestAFinalAnswerBindsEveryRecipient: a decision sent to a role reaches one
// agent per member, so the thread's last message belongs to whoever was served
// last. Asking about the thread alone bound that one and let the others answer
// — which made `-final` on a fan-out close the debate for exactly one
// philosopher.
func TestAFinalAnswerBindsEveryRecipient(t *testing.T) {
	h := fleet(t, `
web: {enabled: false}
agents:
  - name: chair
    role: chair
    command: [probe-echo]
  - name: first
    role: member
    command: [probe-echo]
  - name: second
    role: member
    command: [probe-echo]
`)
	_, _ = h.Send("user", "chair", "put the question", nil)
	msgs, err := h.SendOn("chair", "@member", bus.KindDecision, "it is settled", nil,
		SendOptions{Final: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("a send to @member carried %d messages, want one per member", len(msgs))
	}

	// Both, not just the one the fan-out happened to end on.
	for _, name := range []string{"first", "second"} {
		if _, err := h.SendKind(name, "chair", bus.KindQuestion, "yes but", nil); err == nil {
			t.Errorf("%s answered a final decision", name)
		} else if !strings.Contains(err.Error(), "act on it") {
			t.Errorf("%s: the refusal should say what to do instead, said %q", name, err)
		}
	}
}

// TestASaturatedThreadReachesTheArbiter: running out of turns is not an error
// to swallow. Somebody is told, with what was said.
func TestASaturatedThreadReachesTheArbiter(t *testing.T) {
	h := fleet(t, `
web: {enabled: false}
bus: {max_turns: 2, escalate_to: chief}
agents:
  - name: alpha
    command: [probe-echo]
  - name: beta
    command: [probe-echo]
  - name: chief
    command: [probe-echo]
`)
	_, _ = h.Send("user", "alpha", "settle this", nil)
	_, _ = h.SendKind("alpha", "beta", bus.KindQuestion, "and?", nil)
	if _, err := h.SendKind("beta", "alpha", bus.KindAnswer, "no", nil); err == nil {
		t.Fatal("a third turn went through on a two-turn budget")
	}

	waitFor(t, func() bool { return h.Bus().Pending("chief") > 0 })

	msgs := h.Bus().All()
	last := msgs[len(msgs)-1]
	if last.To != "chief" {
		t.Fatalf("the escalation went to %q", last.To)
	}
	for _, want := range []string{"ran out of turns", "settle this", "--final"} {
		if !strings.Contains(last.Body, want) {
			t.Errorf("the escalation should carry %q:\n%s", want, last.Body)
		}
	}
}

// TestTheLastTurnIsAnnounced: the budget is only useful if the agent about to
// spend the last turn knows it is the last one.
func TestTheLastTurnIsAnnounced(t *testing.T) {
	h := fleet(t, twoAgents)

	_, _ = h.Send("user", "alpha", "start", nil)
	_, _ = h.SendKind("alpha", "beta", bus.KindQuestion, "and?", nil)

	msgs, err := h.SendKind("beta", "alpha", bus.KindAnswer, "well", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msgs[0].Body, "last turn on this thread") {
		t.Errorf("the third of three turns arrived without warning:\n%s", msgs[0].Body)
	}
}
