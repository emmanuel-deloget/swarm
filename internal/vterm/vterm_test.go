//go:build !windows

// These drive a real child through sh, so they do not run on Windows yet.
// Making them portable means replacing the shell with a test binary; until
// then this tag is what keeps the Windows job honest: it runs the whole
// package rather than a list of test names, so a test added there cannot be
// quietly skipped.

package vterm

import (
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTerminalRoundTrip(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"sh", "-c", "printf 'ready\\n'; read line; printf 'got=%s\\n' \"$line\""},
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "prompt", func() bool { return strings.Contains(term.Text(), "ready") })

	if _, err := term.Write([]byte("world" + Submit)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitFor(t, "echo of input", func() bool { return strings.Contains(term.Text(), "got=world") })

	<-term.Done()
	if st := term.Status(); st == nil || st.Code != 0 {
		t.Fatalf("unexpected status: %v", st)
	}
}

func TestSubscribeSnapshotThenStream(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"sh", "-c", "printf 'first\\n'; read _; printf 'second\\n'; sleep 30"},
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "first line", func() bool { return strings.Contains(term.Text(), "first") })

	sub := term.Subscribe(1 << 16)
	defer sub.Close()
	if !strings.Contains(sub.Snapshot, "first") {
		t.Fatalf("snapshot should contain output produced before subscribing, got %q", sub.Snapshot)
	}

	if _, err := term.Write([]byte(Submit)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var streamed strings.Builder
	deadline := time.After(5 * time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			data, resync, err := sub.Next()
			if err != nil {
				return
			}
			if resync {
				continue
			}
			streamed.Write(data)
			if strings.Contains(streamed.String(), "second") {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-deadline:
		t.Fatalf("timeout: streamed %q", streamed.String())
	}
}

func TestResize(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"sh", "-c", "read _; stty size"},
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	if err := term.Resize(100, 30); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if c, r := term.Size(); c != 100 || r != 30 {
		t.Fatalf("Size() = %dx%d, want 100x30", c, r)
	}
	if _, err := term.Write([]byte(Submit)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitFor(t, "stty output", func() bool { return strings.Contains(term.Text(), "30 100") })
}

func TestSignalStopsProcessGroup(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"sh", "-c", "sleep 60"},
		Cols:    40,
		Rows:    10,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := term.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	select {
	case <-term.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process did not die")
	}
	if !term.Exited() {
		t.Fatal("Exited() should be true")
	}
}

func TestBracketedPasteFromRealProcess(t *testing.T) {
	term, err := Start(Options{
		// Turn the mode on, then off, printing a marker each time.
		Command: []string{"sh", "-c", `printf '\033[?2004h'; printf 'on\n'; read _; printf '\033[?2004l'; printf 'off\n'; sleep 5`},
		Cols:    40,
		Rows:    10,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "mode on", func() bool { return term.BracketedPaste() })
	if _, err := term.Write([]byte(Submit)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitFor(t, "mode off", func() bool { return !term.BracketedPaste() })
}

func TestRenderKeepsColours(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"sh", "-c", `printf '\033[31mRED\033[0m plain\n'; sleep 5`},
		Cols:    40,
		Rows:    5,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "coloured output", func() bool { return strings.Contains(term.Text(), "RED") })

	// Plain text drops the styling.
	if strings.Contains(term.Text(), "\x1b[") {
		t.Error("Text() should not contain escape sequences")
	}
	// The ANSI rendering keeps it: that is what the TUI displays.
	render := term.Render()
	if !strings.Contains(render, "\x1b[") {
		t.Error("Render() lost the styling")
	}
	if !strings.Contains(render, "RED") {
		t.Errorf("Render() lost the content: %q", render)
	}
}

// TestRenderStopsAtTheContent: the emulator pads every row out to the full
// width, and a rendering that ends in the last column is a trap — whatever is
// written next wraps and scrolls the window. An attach prints this string
// directly, so the padding has to come off here.
//
// Styled blanks are not padding: a coloured run with nothing in it is what the
// agent drew, and it survives.
func TestRenderStopsAtTheContent(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"sh", "-c", `printf 'Hi\n\033[41m    \033[0m\n'; sleep 5`},
		Cols:    40,
		Rows:    5,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "output", func() bool { return strings.Contains(term.Text(), "Hi") })

	lines := strings.Split(term.Render(), "\n")
	if got := lines[0]; got != "Hi" {
		t.Errorf("first row is %q, want %q with no padding after it", got, "Hi")
	}
	for i, l := range lines {
		if strings.HasSuffix(l, " ") {
			t.Errorf("row %d still ends in a blank: %q", i, l)
		}
	}
	if !strings.Contains(term.Render(), "\x1b[41m") {
		t.Error("the coloured run was trimmed away with the padding")
	}
}

// TestRepaintPutsTheCursorBack: a client that prints a snapshot and stops has
// the cursor wherever the last row ended, not where the agent's cursor is. The
// next thing the agent echoes then lands at the bottom of the screen — and if
// the last row reached the last column, it wraps and scrolls too.
//
// A repaint says where the cursor goes, so the client does not have to guess.
func TestRepaintPutsTheCursorBack(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"sh", "-c", `printf 'ready\n'; sleep 5`},
		Cols:    40,
		Rows:    10,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "output", func() bool { return strings.Contains(term.Text(), "ready") })

	x, y, _ := term.Cursor()
	want := fmt.Sprintf("\x1b[%d;%dH", y+1, x+1)
	repaint := term.Repaint()
	if !strings.HasSuffix(repaint, want) {
		t.Errorf("repaint ends with %q, want a move to the cursor at %d,%d (%q)",
			tail(repaint), x, y, want)
	}
	if !strings.Contains(repaint, "ready") {
		t.Errorf("repaint lost the screen: %q", repaint)
	}
	if got := term.Subscribe(1 << 20).Snapshot; got != repaint {
		t.Errorf("a subscription's snapshot is not a repaint:\n got %q\nwant %q", tail(got), tail(repaint))
	}
}

func tail(s string) string {
	if len(s) > 24 {
		return "..." + s[len(s)-24:]
	}
	return s
}

// TestFocusEventIsDeliveredWhenTheAgentAsksForIt covers a gap that is invisible
// until an agent CLI relies on it: swarm lets the application enable focus
// reporting (DECSET 1004) and then never sends an event, so the agent stays in
// whatever it draws for an unfocused window — forever, since the focus it is
// waiting for never arrives.
//
// The child enables the mode, then echoes its input with control characters
// made visible, so the focus event comes back as "^[[I" in the output.
func TestFocusEventIsDeliveredWhenTheAgentAsksForIt(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"sh", "-c", `printf '\033[?1004hready\n'; exec cat -v`},
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "the child to enable focus reporting", func() bool {
		return strings.Contains(term.Text(), "ready")
	})
	waitFor(t, "the focus event to reach the child", func() bool {
		return strings.Contains(term.Text(), "^[[I")
	})

	// Losing the focus is reported too.
	term.SetFocus(false)
	waitFor(t, "the blur event to reach the child", func() bool {
		return strings.Contains(term.Text(), "^[[O")
	})
	if term.Focused() {
		t.Error("Focused() should follow SetFocus")
	}
}

// TestNoFocusEventWithoutTheMode: a terminal must not volunteer events the
// application never asked for. That is the same rule that keeps bracketed paste
// delimiters out of agents that never enabled 2004.
func TestNoFocusEventWithoutTheMode(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"sh", "-c", `printf 'ready\n'; exec cat -v`},
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "the child", func() bool { return strings.Contains(term.Text(), "ready") })
	term.SetFocus(false)
	term.SetFocus(true)
	time.Sleep(200 * time.Millisecond)

	if got := term.Text(); strings.Contains(got, "^[[I") || strings.Contains(got, "^[[O") {
		t.Errorf("focus events reached a child that never enabled the mode:\n%s", got)
	}
}
