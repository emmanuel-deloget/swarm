package bus

import (
	"testing"
	"time"
)

// A debt outlives the message that opened it. That is the whole reason `swarm
// why` can answer at all — an agent stalled for two days has been compacted
// several times over and no longer knows what it was asked — and it is also
// the trap: the message may be gone while the debt is still there, and a
// report that quietly renders a missing message as an empty one tells the
// reader nothing was asked.

func TestOpenedFindsTheMessageThatAskedForSomething(t *testing.T) {
	b := New(50)
	th := b.NewThread()
	b.Post(Message{Thread: th, From: "triage", To: "dev-1", Kind: KindNote, Body: "unrelated"})
	asked := b.Post(Message{Thread: th, From: "triage", To: "dev-1", Kind: KindQuestion, Body: "which one?"})
	b.Post(Message{Thread: th, From: "triage", To: "dev-1", Kind: KindNote, Body: "later chatter"})

	m, live, ok := b.Opened("dev-1", th)
	if !ok {
		t.Fatal("the message that opened the debt was not found")
	}
	if m.ID != asked.ID {
		t.Errorf("found message %d (%q), want the question %d", m.ID, m.Body, asked.ID)
	}
	if !live {
		t.Error("the recipient's mailbox still holds it, so the copy should be the live one")
	}
}

// TestOpenedReportsDeliveryOnlyFromTheMailbox is the distinction that made this
// worth a second look. The fleet-wide history keeps a message as it was posted
// and is never marked afterwards, so its copy always says "not delivered". That
// is a wrong answer, not an unknown one, and it is the sort that sends someone
// looking for a delivery bug that does not exist.
func TestOpenedReportsDeliveryOnlyFromTheMailbox(t *testing.T) {
	// history 1: the mailbox keeps one message, the fleet history keeps twenty.
	b := New(1)
	th := b.NewThread()
	asked := b.Post(Message{Thread: th, From: "triage", To: "dev-1", Kind: KindQuestion, Body: "which one?"})
	b.MarkPushed("dev-1", asked.ID)

	m, live, ok := b.Opened("dev-1", th)
	if !ok || !live {
		t.Fatalf("mailbox copy not returned: ok=%v live=%v", ok, live)
	}
	if !m.Pushed {
		t.Error("the mailbox copy was marked pushed and does not say so")
	}

	// Push it out of the mailbox, but not out of the fleet history.
	b.Post(Message{Thread: th, From: "triage", To: "dev-1", Kind: KindNote, Body: "chatter"})

	m, live, ok = b.Opened("dev-1", th)
	if !ok {
		t.Fatal("the fleet history still has it and it was not found")
	}
	if live {
		t.Error("the mailbox no longer holds it, so the copy cannot be the live one")
	}
	if m.Body != "which one?" {
		t.Errorf("wrong message from the fleet history: %q", m.Body)
	}
	if m.Pushed {
		t.Error("the history copy claims delivery it never recorded")
	}
}

// TestOpenedSaysWhenTheMessageIsGone: this is the state an agent stalled for
// days ends up in, and the report has to be able to tell it apart from an empty
// message.
func TestOpenedSaysWhenTheMessageIsGone(t *testing.T) {
	b := New(1) // mailbox 1, fleet history 20
	th := b.NewThread()
	b.Post(Message{Thread: th, From: "triage", To: "dev-1", Kind: KindQuestion, Body: "the question"})
	for range 40 {
		b.Post(Message{From: "triage", To: "dev-1", Kind: KindNote, Body: "chatter"})
	}

	if _, _, ok := b.Opened("dev-1", th); ok {
		t.Error("the message is long gone from both and was reported as found")
	}
	// And the debt it opened is still there, which is the point: who asked and
	// when survive what was asked.
	if _, owes := b.OwedSince("dev-1"); !owes {
		t.Error("the debt was dropped along with the message; it must outlive it")
	}
}

// TestClosingComesFromTheKinds: the command that tells someone how to get out
// of a debt names these, and a hand-kept copy would be wrong the first time the
// classification moved.
func TestClosingComesFromTheKinds(t *testing.T) {
	closing := Closing()
	if len(closing) == 0 {
		t.Fatal("no kind settles a debt, so nothing can ever be finished")
	}
	for _, k := range closing {
		if !closes(k) {
			t.Errorf("%q is offered as settling and does not settle", k)
		}
	}
	// Every settling kind must be a real kind, or the advice names something
	// `swarm send -kind` will refuse.
	known := map[Kind]bool{}
	for _, k := range Kinds() {
		known[k] = true
	}
	for _, k := range closing {
		if !known[k] {
			t.Errorf("%q settles a debt but is not a kind anyone can send", k)
		}
	}
}

func TestADebtIsNotOpenedTwiceByAskingTwice(t *testing.T) {
	b := New(50)
	th := b.NewThread()
	b.Post(Message{Thread: th, From: "triage", To: "dev-1", Kind: KindQuestion, Body: "well?"})
	b.Post(Message{Thread: th, From: "triage", To: "dev-1", Kind: KindQuestion, Body: "well??"})

	if n := len(b.Owed("dev-1")); n != 1 {
		t.Errorf("asking twice on one thread opened %d debts", n)
	}
	d, _ := b.OwedSince("dev-1")
	if time.Since(d.Since) > time.Minute {
		t.Error("the debt is dated from something other than the message")
	}
}
