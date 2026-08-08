package event

import (
	"sync"
	"testing"
	"time"
)

// TestPublishRacesCancel is the failure CI caught: Publish copied the
// subscriber channels, let go of the lock, and only then sent on them, while a
// cancel could be closing one of those channels in between. The race detector
// reports it, but the real cost is worse — a send on a closed channel panics,
// and the window is widest exactly at shutdown, when the UI cancels its
// subscription while agents are still reporting that they stopped.
func TestPublishRacesCancel(t *testing.T) {
	l := NewLog(64)
	var wg sync.WaitGroup

	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					l.Emit(KindInfo, "", "something happened")
				}
			}
		}()
	}

	// Subscribers come and go under that traffic, which is what a TUI, a web
	// client and `swarm events -f` all do.
	for range 200 {
		ch, cancel := l.Subscribe(1)
		go func() {
			for range ch {
			}
		}()
		cancel()
	}

	close(stop)
	wg.Wait()
}

// TestSubscribeReceivesWhatFollows: a subscriber gets events published after it
// arrived, and nothing it was too late for.
func TestSubscribeReceivesWhatFollows(t *testing.T) {
	l := NewLog(64)
	l.Emit(KindInfo, "", "before")

	ch, cancel := l.Subscribe(8)
	defer cancel()
	l.Emit(KindStarted, "dev-1", "after")

	select {
	case got := <-ch:
		if got.Text != "after" {
			t.Errorf("received %q, want the event published after subscribing", got.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing arrived")
	}
}

// TestCancelClosesTheChannel: callers range over it, so it has to end.
func TestCancelClosesTheChannel(t *testing.T) {
	l := NewLog(64)
	ch, cancel := l.Subscribe(4)
	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("the channel should be closed and drained")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not close the channel")
	}

	// Cancelling twice is something a defer plus an explicit call will do.
	cancel()
}

// TestSlowSubscriberLosesEventsRatherThanBlocking is the promise Publish makes:
// a client that stops reading must not stall the swarm.
func TestSlowSubscriberLosesEventsRatherThanBlocking(t *testing.T) {
	l := NewLog(1024)
	_, cancel := l.Subscribe(1) // never drained
	defer cancel()

	done := make(chan struct{})
	go func() {
		for range 500 {
			l.Emit(KindInfo, "", "flood")
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a subscriber that was not reading")
	}

	if n := len(l.History(0)); n != 500 {
		t.Errorf("the log kept %d events, want all 500 regardless of subscribers", n)
	}
}
