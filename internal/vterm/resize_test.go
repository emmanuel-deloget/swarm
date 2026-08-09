package vterm

import (
	"slices"
	"sync"
	"testing"
	"time"
)

// A frame is computed against a geometry. Every full-screen TUI brackets its
// frame with DECSET 2026 and then writes absolute positions worked out from the
// height it believes in — so a resize applied halfway through leaves the rest of
// that frame addressing a screen that no longer exists. What it meant to put on
// line 34 of 50 lands wherever the last line clamps it, which is the prompt.
//
// It shows up here and not in a real terminal because here the geometry changes
// on its own: a pane relayout, an attach, a detach.

// TestResizeWaitsForTheFrameToEnd is the fix for the reported bug: text that
// belonged to a frame turning up in the prompt.
func TestResizeWaitsForTheFrameToEnd(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"sh", "-c", `printf '\033[?2026h'; sleep 30`},
		Cols:    80,
		Rows:    40,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()
	waitFor(t, "the frame to open", func() bool { return term.syncOn.Load() })

	if err := term.Resize(80, 24); err != nil {
		t.Fatal(err)
	}
	if _, rows := term.Size(); rows != 40 {
		t.Fatalf("the emulator resized to %d rows mid-frame", rows)
	}

	// Closing the frame releases it. The sequence goes in as if the child had
	// written it, which is what ends the frame for the scanner.
	term.consume([]byte("\x1b[?2026l"))
	waitFor(t, "the held resize", func() bool { _, r := term.Size(); return r == 24 })
}

// TestAFrameThatNeverEndsDoesNotHoldTheGeometry: a child that crashes mid-frame,
// or never clears the mode, must not freeze the size for good.
func TestAFrameThatNeverEndsDoesNotHoldTheGeometry(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"sh", "-c", `printf '\033[?2026h'; sleep 30`},
		Cols:    80,
		Rows:    40,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()
	waitFor(t, "the frame to open", func() bool { return term.syncOn.Load() })

	if err := term.Resize(80, 24); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the resize to land anyway", func() bool { _, r := term.Size(); return r == 24 })
}

// TestAResizeIsRecordedLikeAnyOtherInput: an input log that shows a full redraw
// with nothing to explain it is a log that costs an evening.
func TestAResizeIsRecordedLikeAnyOtherInput(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	term, err := Start(Options{
		Command: []string{"sh", "-c", `printf 'ready\n'; sleep 30`},
		Cols:    80,
		Rows:    24,
		OnInput: func(source string, data []byte) {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, source+" "+string(data))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	if err := term.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(seen, "resize 100x30") {
		t.Errorf("the resize was not recorded: %v", seen)
	}
}
