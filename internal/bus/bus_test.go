package bus

import (
	"testing"
	"time"
)

func TestPostAndCollect(t *testing.T) {
	b := New(10)

	if n := b.Pending("dev-1"); n != 0 {
		t.Fatalf("a fresh mailbox should be empty, got %d", n)
	}

	m1 := b.Post(Message{From: "user", To: "dev-1", Body: "first"})
	m2 := b.Post(Message{From: "user", To: "dev-1", Body: "second"})
	if m1.ID == 0 || m2.ID == m1.ID {
		t.Fatalf("ids should be assigned and unique: %d %d", m1.ID, m2.ID)
	}
	if m1.At.IsZero() {
		t.Error("a timestamp should be filled in")
	}
	if n := b.Pending("dev-1"); n != 2 {
		t.Fatalf("Pending = %d, want 2", n)
	}

	peeked := b.Collect("dev-1", true)
	if len(peeked) != 2 || b.Pending("dev-1") != 2 {
		t.Fatal("peeking must not consume the mailbox")
	}

	got := b.Collect("dev-1", false)
	if len(got) != 2 || got[0].Body != "first" || got[1].Body != "second" {
		t.Fatalf("collected %+v, want first then second", got)
	}
	for _, m := range got {
		if m.ReadAt.IsZero() {
			t.Error("collecting should mark messages read")
		}
	}
	if n := b.Pending("dev-1"); n != 0 {
		t.Fatalf("Pending after collect = %d, want 0", n)
	}
	if len(b.History("dev-1", 0)) != 2 {
		t.Error("history should survive collection")
	}
}

func TestPushedMessagesLeaveThePendingQueue(t *testing.T) {
	b := New(10)
	m := b.Post(Message{From: "user", To: "dev-1", Body: "typed straight in"})
	b.MarkPushed("dev-1", m.ID)

	if n := b.Pending("dev-1"); n != 0 {
		t.Fatalf("a pushed message is already on screen, Pending = %d", n)
	}
	hist := b.History("dev-1", 0)
	if len(hist) != 1 || !hist[0].Pushed {
		t.Fatalf("history should record the push: %+v", hist)
	}
}

func TestPendingAllOnlyReportsWaitingMailboxes(t *testing.T) {
	b := New(10)
	b.Post(Message{To: "a", Body: "x"})
	m := b.Post(Message{To: "b", Body: "y"})
	b.MarkPushed("b", m.ID)
	b.Post(Message{To: "c", Body: "z"})
	b.Collect("c", false)

	all := b.PendingAll()
	if len(all) != 1 || all["a"] != 1 {
		t.Fatalf("PendingAll = %v, want only a", all)
	}
}

func TestHistoryIsBounded(t *testing.T) {
	b := New(3)
	for i := range 6 {
		b.Post(Message{To: "a", Body: string(rune('a' + i))})
	}
	hist := b.History("a", 0)
	if len(hist) != 3 {
		t.Fatalf("history length = %d, want 3", len(hist))
	}
	if hist[0].Body != "d" || hist[2].Body != "f" {
		t.Fatalf("history should keep the newest: %+v", hist)
	}
	if n := b.History("a", 2); len(n) != 2 || n[1].Body != "f" {
		t.Fatalf("History(2) = %+v", n)
	}
	if b.History("nobody", 0) != nil {
		t.Error("an unknown mailbox should have no history")
	}
}

func TestThreadsAreSharedByBroadcastCopies(t *testing.T) {
	b := New(10)
	thread := b.NewThread()
	a := b.Post(Message{Thread: thread, To: "a", Body: "hi"})
	c := b.Post(Message{Thread: thread, To: "b", Body: "hi"})
	if a.Thread != thread || c.Thread != thread {
		t.Fatalf("copies should share the thread: %d %d %d", thread, a.Thread, c.Thread)
	}
	if solo := b.Post(Message{To: "c", Body: "alone"}); solo.Thread == thread || solo.Thread == 0 {
		t.Fatalf("a message without a thread should get its own, got %d", solo.Thread)
	}
}

func TestWaitReturnsImmediatelyWhenMailIsWaiting(t *testing.T) {
	b := New(10)
	b.Post(Message{To: "a", Body: "already here"})

	start := time.Now()
	if !b.Wait("a", time.Second, nil) {
		t.Fatal("Wait should report the pending message")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("Wait blocked for %s on an already-full mailbox", elapsed)
	}
}

func TestWaitWakesOnDelivery(t *testing.T) {
	b := New(10)
	done := make(chan bool, 1)
	go func() { done <- b.Wait("a", 5*time.Second, nil) }()

	// Give the waiter time to park, then deliver.
	time.Sleep(50 * time.Millisecond)
	b.Post(Message{To: "a", Body: "wake up"})

	select {
	case ok := <-done:
		if !ok {
			t.Fatal("Wait returned false although a message arrived")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not wake up on delivery")
	}
}

func TestWaitHonoursTimeoutAndCancel(t *testing.T) {
	b := New(10)

	start := time.Now()
	if b.Wait("a", 80*time.Millisecond, nil) {
		t.Error("Wait should report nothing after a timeout")
	}
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Errorf("Wait returned after %s, expected to wait for the timeout", elapsed)
	}

	cancel := make(chan struct{})
	done := make(chan bool, 1)
	go func() { done <- b.Wait("b", 0, cancel) }()
	time.Sleep(50 * time.Millisecond)
	close(cancel)
	select {
	case ok := <-done:
		if ok {
			t.Error("a cancelled Wait should report nothing")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not unblock Wait")
	}
}

func TestRenderExpandsPlaceholders(t *testing.T) {
	m := Message{ID: 7, Thread: 3, From: "triage", To: "dev-1", Body: "fix #12", Files: []string{"/shared/a.png"}}

	got := m.Render("[{from}→{to}] #{id}: {body} ({files})")
	want := "[triage→dev-1] #7: fix #12 (/shared/a.png)"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}

	// A template that forgets {files} must not silently drop the attachments.
	got = m.Render("{body}")
	if got != "fix #12\nattached: /shared/a.png" {
		t.Errorf("attachments should be appended, got %q", got)
	}

	plain := Message{ID: 1, From: "a", To: "b", Body: "hi"}
	if out := plain.Render("{body}"); out != "hi" {
		t.Errorf("Render = %q, want hi", out)
	}
}
