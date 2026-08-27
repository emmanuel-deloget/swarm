package bus

import (
	"testing"
	"time"
)

// TestSentOverSaysWhenNotJustHowMuch: the count alone misleads. An agent
// talking steadily for ten minutes and one that started forty seconds ago
// report the same number, and only one of them is running away.
func TestSentOverSaysWhenNotJustHowMuch(t *testing.T) {
	b := New(100)
	now := time.Now()
	// Four messages, all within the last minute of a ten-minute window.
	for range 4 {
		b.Post(Message{From: "chair", To: "a", At: now.Add(-30 * time.Second)})
	}
	// And one at the very start of it.
	b.Post(Message{From: "chair", To: "a", At: now.Add(-9 * time.Minute)})

	over := b.SentOver("chair", 10*time.Minute, 10)
	if len(over) != 10 {
		t.Fatalf("asked for 10 slices, got %d", len(over))
	}
	if sum := total(over); sum != 5 {
		t.Errorf("the slices total %d, want the 5 messages sent", sum)
	}
	if over[len(over)-1] != 4 {
		t.Errorf("the last slice holds %d, want the 4 that just arrived", over[len(over)-1])
	}
	// In the first half somewhere: the exact slice depends on the moment
	// SentOver reads the clock, which is a shade after this test did.
	if early := total(over[:5]); early != 1 {
		t.Errorf("the first half holds %d, want the 1 sent nine minutes back", early)
	}
	if n := b.SentOver("nobody", 10*time.Minute, 10); total(n) != 0 {
		t.Errorf("an agent that said nothing has %d", total(n))
	}
}

func total(xs []int) int {
	n := 0
	for _, x := range xs {
		n += x
	}
	return n
}
