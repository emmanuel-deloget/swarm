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

// TestAPushedMessageWakesNobody: a message on its way into a terminal is not
// for the mailbox. Waking a client blocked on `swarm inbox` races MarkPushed,
// and the client that wins collects a message the agent is being shown at the
// same moment — one message, handled twice.
func TestAPushedMessageWakesNobody(t *testing.T) {
	b := New(10)
	// Whether the waiter was released, not what it found: MarkPushed empties
	// the mailbox either way, so a test that reads Wait's answer passes for the
	// wrong reason and would not notice the wake coming back.
	released := make(chan struct{})
	go func() { _ = b.Wait("a", 5*time.Second, nil); close(released) }()
	time.Sleep(50 * time.Millisecond)

	m := b.PostPushed(Message{To: "a", Body: "typed in, not filed for collection"})
	b.MarkPushed("a", m.ID)

	select {
	case <-released:
		t.Error("a waiter was released by a message that went into the terminal")
	case <-time.After(200 * time.Millisecond):
	}
	if n := b.Pending("a"); n != 0 {
		t.Errorf("%d message(s) left to collect after a push, want none", n)
	}
}

// TestWakeTellsThemAfterAFailedPush: the push did not land, so the message
// stayed in the mailbox — and nobody was woken when it was filed.
func TestWakeTellsThemAfterAFailedPush(t *testing.T) {
	b := New(10)
	done := make(chan bool, 1)
	go func() { done <- b.Wait("a", 5*time.Second, nil) }()
	time.Sleep(50 * time.Millisecond)

	b.PostPushed(Message{To: "a", Body: "the injection failed"})
	b.Wake("a") // what the hub does when Inject returns an error

	select {
	case ok := <-done:
		if !ok {
			t.Fatal("Wait returned false although the message is still pending")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wake did not release the waiter")
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

func TestRecentSpansEveryMailbox(t *testing.T) {
	b := New(10)
	th := b.NewThread()
	b.Post(Message{Thread: th, From: "user", To: "dev-1", Body: "go"})
	b.Post(Message{Thread: th, From: "user", To: "dev-2", Body: "go"})
	b.Post(Message{From: "dev-1", To: "dev-2", Body: "what do you think"})

	got := b.All()
	if len(got) != 3 {
		t.Fatalf("Recent = %d messages, want 3", len(got))
	}
	// Oldest first, and ids increasing: a tail has to read in order.
	for i := 1; i < len(got); i++ {
		if got[i].ID <= got[i-1].ID {
			t.Errorf("Recent is not ordered: %d then %d", got[i-1].ID, got[i].ID)
		}
	}
	if got[2].From != "dev-1" {
		t.Errorf("last message is from %q, want dev-1", got[2].From)
	}
}

// TestRecentSurvivesAChattyPair: per-mailbox histories are capped on their own,
// so merging them after the fact would lose a quiet exchange behind a loud one.
func TestRecentSurvivesAChattyPair(t *testing.T) {
	b := New(4)
	b.Post(Message{From: "review-1", To: "triage", Body: "the quiet one"})
	for range 20 {
		b.Post(Message{From: "dev-1", To: "dev-2", Body: "and another thing"})
	}
	for _, m := range b.All() {
		if m.From == "review-1" {
			return
		}
	}
	t.Error("the quiet exchange was pushed out by the chatty pair")
}

func TestSinceReturnsOnlyWhatIsNew(t *testing.T) {
	b := New(10)
	first := b.Post(Message{From: "a", To: "b", Body: "one"})
	b.Post(Message{From: "a", To: "b", Body: "two"})

	got := b.Since(first.ID)
	if len(got) != 1 || got[0].Body != "two" {
		t.Errorf("Since(%d) = %+v, want only the second", first.ID, got)
	}
	if got := b.Since(0); len(got) != 2 {
		t.Errorf("Since(0) should return everything, got %d", len(got))
	}
}

func TestStatsSince(t *testing.T) {
	b := New(100)
	th := b.NewThread()
	b.Post(Message{Thread: th, From: "dev-1", To: "review-1", Body: "?"})
	b.Post(Message{Thread: th, From: "review-1", To: "dev-1", Body: "no"})
	b.Post(Message{Thread: th, From: "dev-1", To: "review-1", Body: "but"})
	b.Post(Message{From: "user", To: "dev-2", Body: "unrelated"})

	s := b.StatsSince(time.Now().Add(-time.Minute))
	if s.Messages != 4 {
		t.Errorf("Messages = %d, want 4", s.Messages)
	}
	if s.Threads != 2 {
		t.Errorf("Threads = %d, want 2", s.Threads)
	}
	if s.Deepest != 3 {
		t.Errorf("Deepest = %d, want 3: a thread nobody ends is the shape of the trouble", s.Deepest)
	}
	if s.Sent["dev-1"] != 2 || s.Received["review-1"] != 2 {
		t.Errorf("per-agent counts are wrong: sent=%v received=%v", s.Sent, s.Received)
	}
	// Busiest pair first, and directed: dev-1 → review-1 is not the reverse.
	if len(s.Pairs) == 0 || s.Pairs[0].From != "dev-1" || s.Pairs[0].To != "review-1" || s.Pairs[0].Count != 2 {
		t.Errorf("Pairs = %+v, want dev-1 → review-1 twice at the top", s.Pairs)
	}
}

func TestStatsSinceIgnoresWhatCameBefore(t *testing.T) {
	b := New(100)
	b.Post(Message{From: "a", To: "b", At: time.Now().Add(-time.Hour), Body: "old"})
	b.Post(Message{From: "a", To: "b", Body: "new"})

	if s := b.StatsSince(time.Now().Add(-time.Minute)); s.Messages != 1 {
		t.Errorf("Messages = %d, want only the recent one", s.Messages)
	}
}

func TestSentSince(t *testing.T) {
	b := New(100)
	b.Post(Message{From: "dev-1", To: "b", At: time.Now().Add(-time.Hour)})
	for range 3 {
		b.Post(Message{From: "dev-1", To: "b"})
	}
	b.Post(Message{From: "dev-2", To: "b"})

	if n := b.SentSince("dev-1", time.Now().Add(-time.Minute)); n != 3 {
		t.Errorf("SentSince = %d, want 3", n)
	}
	if n := b.SentSince("nobody", time.Now().Add(-time.Minute)); n != 0 {
		t.Errorf("SentSince for an unknown agent = %d, want 0", n)
	}
}

func TestStatsCountsKinds(t *testing.T) {
	b := New(100)
	b.Post(Message{From: "a", To: "b", Kind: KindQuestion})
	b.Post(Message{From: "a", To: "b", Kind: KindQuestion})
	b.Post(Message{From: "b", To: "a", Kind: KindAnswer})
	b.Post(Message{From: "a", To: "b"}) // a plain note, deliberately uncounted

	s := b.StatsSince(time.Now().Add(-time.Minute))
	if s.Kinds[KindQuestion] != 2 || s.Kinds[KindAnswer] != 1 {
		t.Errorf("Kinds = %v", s.Kinds)
	}
	if _, ok := s.Kinds[KindNote]; ok {
		t.Error("an unclassified message should not appear as a kind of its own")
	}
}

func TestValidKind(t *testing.T) {
	for _, k := range append(Kinds(), KindNote) {
		if !ValidKind(k) {
			t.Errorf("%q should be valid", k)
		}
	}
	if ValidKind(Kind("nonsense")) {
		t.Error("an invented kind should not be valid")
	}
}

// TestZeroMeansNoneNotAll: `-n 0` asks to watch what happens next without being
// shown what already did. Reading it as "unset" gave the default; reading it as
// "no bound" gave the whole ring — both the opposite of what was asked.
func TestZeroMeansNoneNotAll(t *testing.T) {
	b := New(10)
	for i := range 5 {
		b.Post(Message{From: "user", To: "alpha", Body: string(rune('a' + i))})
	}

	if got := b.Recent(0); len(got) != 0 {
		t.Errorf("Recent(0) returned %d messages, want none", len(got))
	}
	if got := b.Recent(2); len(got) != 2 {
		t.Errorf("Recent(2) returned %d, want 2", len(got))
	}
	if got := b.All(); len(got) != 5 {
		t.Errorf("All() returned %d, want 5", len(got))
	}
	if got := b.Recent(-1); len(got) != 5 {
		t.Errorf("Recent(-1) returned %d, want all 5", len(got))
	}
}

// TestLastIDIsWhereAFollowerStarts: from zero, a follower that asked for no
// history would be handed the whole ring at its first poll.
func TestLastIDIsWhereAFollowerStarts(t *testing.T) {
	b := New(10)
	if id := b.LastID(); id != 0 {
		t.Errorf("an empty bus reports id %d", id)
	}
	var last uint64
	for range 3 {
		last = b.Post(Message{From: "user", To: "alpha", Body: "x"}).ID
	}
	if id := b.LastID(); id != last {
		t.Errorf("LastID is %d, want %d", id, last)
	}
	if got := b.Since(b.LastID()); len(got) != 0 {
		t.Errorf("following from the newest id already offers %d messages", len(got))
	}
}
