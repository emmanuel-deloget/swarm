package bus

import (
	"testing"
	"time"
)

// An agent waiting for work and an agent that stopped halfway through a request
// look the same on screen. The bus is what can tell them apart, because the bus
// is what asked.

func post(b *Bus, from, to string, k Kind, thread uint64) Message {
	return b.Post(Message{From: from, To: to, Kind: k, Thread: thread, Body: "x"})
}

// TestOnlyAskingOpensADebt: a note or an fyi must never make an agent look like
// it owes something, or `swarm send` would assign work by accident.
func TestOnlyAskingOpensADebt(t *testing.T) {
	for _, c := range []struct {
		kind Kind
		owes bool
	}{
		{KindNote, false},
		{KindFYI, false},
		{KindAnswer, false},
		{KindDecision, false},
		{KindDone, false},
		{KindQuestion, true},
		{KindRequest, true},
		{KindBlocked, true},
	} {
		b := New(10)
		post(b, "user", "alpha", c.kind, 0)
		if owes := len(b.Owed("alpha")) > 0; owes != c.owes {
			t.Errorf("%q opens a debt: %v, want %v", c.kind, owes, c.owes)
		}
	}
}

// TestAnsweringSettlesIt, on the thread it was asked on.
func TestAnsweringSettlesIt(t *testing.T) {
	b := New(10)
	m := post(b, "user", "alpha", KindRequest, 0)
	if len(b.Owed("alpha")) != 1 {
		t.Fatal("the request opened nothing")
	}
	post(b, "alpha", "user", KindDone, m.Thread)
	if got := b.Owed("alpha"); len(got) != 0 {
		t.Errorf("done left %d debts", len(got))
	}
}

// TestAnotherThreadIsNotSettled: finishing one thing does not finish the rest.
func TestAnotherThreadIsNotSettled(t *testing.T) {
	b := New(10)
	first := post(b, "user", "alpha", KindRequest, 0)
	post(b, "user", "alpha", KindQuestion, 0)
	post(b, "alpha", "user", KindAnswer, first.Thread)

	if got := b.Owed("alpha"); len(got) != 1 {
		t.Fatalf("%d debts left, want the other one", len(got))
	}
}

// TestAskingTwiceIsOneDebt: a sender that repeats itself has not doubled the
// work.
func TestAskingTwiceIsOneDebt(t *testing.T) {
	b := New(10)
	m := post(b, "user", "alpha", KindRequest, 0)
	post(b, "user", "alpha", KindRequest, m.Thread)
	if got := b.Owed("alpha"); len(got) != 1 {
		t.Errorf("%d debts for one thread", len(got))
	}
}

// TestOwedSinceIsTheOldest, since that is what decides how long a silence has
// gone unexplained.
func TestOwedSinceIsTheOldest(t *testing.T) {
	b := New(10)
	old := b.Post(Message{From: "user", To: "alpha", Kind: KindRequest,
		At: time.Now().Add(-time.Hour), Body: "x"})
	b.Post(Message{From: "user", To: "alpha", Kind: KindQuestion, Body: "x"})

	d, ok := b.OwedSince("alpha")
	if !ok {
		t.Fatal("nothing owed")
	}
	if d.Thread != old.Thread {
		t.Errorf("the oldest debt is thread %d, want %d", d.Thread, old.Thread)
	}
}

// TestSettleClosesEverything, for `swarm done` with no thread named.
func TestSettleClosesEverything(t *testing.T) {
	b := New(10)
	post(b, "user", "alpha", KindRequest, 0)
	post(b, "user", "alpha", KindQuestion, 0)

	if closed := b.Settle("alpha", 0); len(closed) != 2 {
		t.Errorf("settling everything closed %d", len(closed))
	}
	if got := b.Owed("alpha"); len(got) != 0 {
		t.Errorf("%d debts survived", len(got))
	}
}

// TestClosingWhatWasNeverOwedIsFine: an agent may report done for work it was
// given by hand, and being told is better than being right.
func TestClosingWhatWasNeverOwedIsFine(t *testing.T) {
	b := New(10)
	post(b, "alpha", "user", KindDone, 0) // must not panic
	if got := b.Owed("alpha"); len(got) != 0 {
		t.Errorf("done created a debt: %v", got)
	}
}
