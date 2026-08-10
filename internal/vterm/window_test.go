//go:build !windows

// These drive a real child through sh, so they do not run on Windows yet.
// Making them portable means replacing the shell with a test binary; until
// then this tag is what keeps the Windows job honest: it runs the whole
// package rather than a list of test names, so a test added there cannot be
// quietly skipped.

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

func TestRenderWindowShowsTheCursor(t *testing.T) {
	// A prompt with the cursor sitting after it, which is where one looks to
	// know whether typing would land in the right place.
	term, err := Start(Options{
		Command: []string{"sh", "-c", "printf 'prompt> '; sleep 30"},
		Cols:    40,
		Rows:    6,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "the prompt", func() bool { return strings.Contains(term.Text(), "prompt>") })

	lines, _ := term.RenderWindow(0, 6)
	joined := strings.Join(lines, "\n")

	// Reverse video marks the cell the cursor is on.
	if !strings.Contains(joined, "\x1b[7m") {
		t.Errorf("the cursor should be drawn in reverse video:\n%q", joined)
	}
	// And it is on the prompt's line, past the last written character.
	x, y, visible := term.Cursor()
	if !visible {
		t.Fatal("the cursor should be visible here")
	}
	if !strings.Contains(lines[y], "\x1b[7m") {
		t.Errorf("row %d holds the cursor at column %d but is not marked: %q", y, x, lines[y])
	}
	for i, line := range lines {
		if i != y && strings.Contains(line, "\x1b[7m") {
			t.Errorf("row %d should not carry the cursor: %q", i, line)
		}
	}
}

func TestHiddenCursorIsNotDrawn(t *testing.T) {
	term, err := Start(Options{
		// Hide the cursor, as a full-screen application does.
		Command: []string{"sh", "-c", "printf 'text\\033[?25l'; sleep 30"},
		Cols:    40,
		Rows:    6,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "the output", func() bool { return strings.Contains(term.Text(), "text") })
	waitFor(t, "the cursor to be hidden", func() bool {
		_, _, visible := term.Cursor()
		return !visible
	})

	lines, _ := term.RenderWindow(0, 6)
	if joined := strings.Join(lines, "\n"); strings.Contains(joined, "\x1b[7m") {
		t.Errorf("a hidden cursor should not be drawn:\n%q", joined)
	}
}
