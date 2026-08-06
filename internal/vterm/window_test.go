package vterm

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRenderWindowReadsTheScrollbackAndBoundsTheOffset(t *testing.T) {
	// 40 numbered lines through a 10-row screen: 30 of them end up in the
	// scrollback.
	term, err := Start(Options{
		Command:    []string{"sh", "-c", "i=1; while [ $i -le 40 ]; do printf 'line-%02d\\n' $i; i=$((i+1)); done; sleep 30"},
		Cols:       40,
		Rows:       10,
		Scrollback: 500,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "the last line", func() bool { return strings.Contains(term.Text(), "line-40") })
	waitFor(t, "the scrollback to fill", func() bool { return term.ScrollbackLen() >= 30 })

	// At the bottom, the window shows the end of the output.
	lines, maxOffset := term.RenderWindow(0, 10)
	if len(lines) != 10 {
		t.Fatalf("got %d lines, want 10", len(lines))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "line-40") {
		t.Errorf("the bottom of the window should show the latest output:\n%s", strings.Join(lines, "\n"))
	}
	if maxOffset <= 0 {
		t.Fatalf("maxOffset = %d; there is a scrollback to walk into", maxOffset)
	}

	// Scrolling up reaches lines that have left the screen.
	up, _ := term.RenderWindow(20, 10)
	joined := strings.Join(up, "\n")
	if !strings.Contains(joined, "line-1") {
		t.Errorf("scrolling up should reach the scrollback, got:\n%s", joined)
	}
	if strings.Contains(joined, "line-40") {
		t.Errorf("scrolling up should have left the latest output behind:\n%s", joined)
	}

	// An offset past the beginning is clamped, not honoured: the first screen
	// of the session stays visible instead of scrolling into nothing.
	far, _ := term.RenderWindow(10_000, 10)
	clamped, _ := term.RenderWindow(maxOffset, 10)
	if strings.Join(far, "\n") != strings.Join(clamped, "\n") {
		t.Errorf("an excessive offset should be clamped to maxOffset (%d)", maxOffset)
	}
	if strings.TrimSpace(strings.Join(far, "")) == "" {
		t.Error("scrolling to the very top should still show something")
	}
}

func TestRenderWindowWithoutScrollback(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"sh", "-c", "printf 'only-line\\n'; sleep 30"},
		Cols:    40,
		Rows:    10,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "output", func() bool { return strings.Contains(term.Text(), "only-line") })

	// The screen fits the window exactly, so there is nowhere to scroll.
	lines, maxOffset := term.RenderWindow(0, 10)
	if maxOffset != 0 {
		t.Errorf("maxOffset = %d, want 0 when the screen fits", maxOffset)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "only-line") {
		t.Error("the window lost the output")
	}
}

func TestShrinkingKeepsTheRecentOutput(t *testing.T) {
	// A real terminal that loses height pushes the top into the scrollback and
	// keeps the bottom, where the prompt is. Truncating from the bottom instead
	// would discard exactly what the user is looking at.
	term, err := Start(Options{
		Command:    []string{"sh", "-c", "i=1; while [ $i -le 20 ]; do printf 'line-%02d\\n' $i; i=$((i+1)); done; sleep 30"},
		Cols:       40,
		Rows:       30,
		Scrollback: 500,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "all the output", func() bool { return strings.Contains(term.Text(), "line-20") })
	if term.ScrollbackLen() != 0 {
		t.Fatalf("a 30-row screen should hold 20 lines without scrolling, scrollback = %d", term.ScrollbackLen())
	}

	if err := term.Resize(40, 10); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	// The latest line survives on screen...
	screen := term.Text()
	if !strings.Contains(screen, "line-20") {
		t.Errorf("shrinking lost the most recent output:\n%s", screen)
	}
	// ... the earliest ones moved into the scrollback rather than vanishing.
	if strings.Contains(screen, "line-01") {
		t.Errorf("a 10-row screen cannot still show line-01:\n%s", screen)
	}
	if term.ScrollbackLen() == 0 {
		t.Error("the displaced lines should have gone into the scrollback")
	}
	window, _ := term.RenderWindow(15, 10)
	if !strings.Contains(strings.Join(window, "\n"), "line-01") {
		t.Errorf("the displaced lines should be reachable by scrolling:\n%s", strings.Join(window, "\n"))
	}
}

// TestResizeDoesNotCorruptAnEscapeSequence guards the mistake that shipped
// once: Resize used to write a scroll sequence into the emulator, and if that
// landed while the agent was halfway through an escape sequence, the sequence
// was truncated and its payload spilled onto the screen. A window title is the
// case one notices, since agent CLIs set it constantly.
func TestResizeDoesNotCorruptAnEscapeSequence(t *testing.T) {
	var (
		mu    sync.Mutex
		title string
	)

	term, err := Start(Options{
		OnTitle: func(s string) {
			mu.Lock()
			title = s
			mu.Unlock()
		},
		// Print enough to fill the screen, then send a title in two pieces with
		// a pause in the middle, so a resize can fall inside it.
		Command: []string{"sh", "-c", `
			i=1; while [ $i -le 30 ]; do printf 'line-%02d\n' $i; i=$((i+1)); done
			printf '\033]0;my window '
			sleep 1
			printf 'title\007'
			printf 'output-follows\n'
			sleep 30`},
		Cols:       40,
		Rows:       30,
		Scrollback: 200,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "the screen to fill", func() bool { return strings.Contains(term.Text(), "line-30") })

	// Shrink while the title is in flight. This is what the TUI does when the
	// window changes or the event log is toggled.
	if err := term.Resize(40, 12); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	waitFor(t, "the rest of the output", func() bool { return strings.Contains(term.Text(), "output-follows") })

	screen := term.Text()
	if strings.Contains(screen, "my window") {
		t.Errorf("the title leaked onto the screen after a resize:\n%s", screen)
	}
	mu.Lock()
	got := title
	mu.Unlock()
	if got != "my window title" {
		t.Errorf("title = %q, want the whole thing", got)
	}
}
