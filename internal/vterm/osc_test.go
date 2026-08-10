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

// probe starts a terminal that does nothing, to be fed by hand.
func probe(t *testing.T, cols, rows int) (*Terminal, *[]string) {
	t.Helper()
	var mu sync.Mutex
	titles := &[]string{}
	term, err := Start(Options{
		Command: []string{"sh", "-c", "sleep 30"},
		Cols:    cols,
		Rows:    rows,
		OnTitle: func(s string) {
			mu.Lock()
			defer mu.Unlock()
			*titles = append(*titles, s)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = term.Stop(time.Second) })
	return term, titles
}

func firstLine(term *Terminal) string {
	return strings.TrimRight(strings.Split(term.Text(), "\n")[0], " ")
}

// TestATitleNeverReachesTheScreen is the bug as reported: "Claude Code writes
// its title into my prompt". Every rune in the Dingbats block encodes with 0x9C
// as its second byte, which the parser reads as a String Terminator and so cuts
// the title in half, printing the rest wherever the cursor is.
func TestATitleNeverReachesTheScreen(t *testing.T) {
	term, titles := probe(t, 60, 4)

	for _, spinner := range []string{"✳", "✻", "✶", "✽", "⠂", "❯", "x"} {
		term.consume([]byte("\x1b[2J\x1b[1;1H"))
		term.consume([]byte("\x1b]0;" + spinner + " Déboguer\x07"))
		if got := firstLine(term); got != "" {
			t.Errorf("a title starting with %q was printed to the screen: %q", spinner, got)
		}
	}
	if len(*titles) != 7 {
		t.Errorf("%d titles reported, want 7", len(*titles))
	}
	if last := (*titles)[0]; last != "✳ Déboguer" {
		t.Errorf("the title came through as %q", last)
	}
}

// TestTheCursorDoesNotMove: the screen staying blank is not enough — a title
// that consumed a column would still shift everything drawn after it.
func TestTheCursorDoesNotMove(t *testing.T) {
	term, _ := probe(t, 60, 4)
	term.consume([]byte("\x1b[2J\x1b[1;5H"))
	x0, y0, _ := term.Cursor()
	term.consume([]byte("\x1b]0;✳ working\x07"))
	x, y, _ := term.Cursor()
	if x != x0 || y != y0 {
		t.Errorf("the cursor moved from (%d,%d) to (%d,%d)", x0, y0, x, y)
	}
}

// TestASplitTitleIsStillHandled: a title is not guaranteed to arrive in one
// read, and the byte that breaks it can land either side of the boundary.
func TestASplitTitleIsStillHandled(t *testing.T) {
	whole := []byte("\x1b]0;✳ split\x07")
	for cut := 1; cut < len(whole); cut++ {
		term, titles := probe(t, 60, 4)
		term.consume(whole[:cut])
		term.consume(whole[cut:])
		if got := firstLine(term); got != "" {
			t.Errorf("split at %d printed %q", cut, got)
		}
		if len(*titles) != 1 || (*titles)[0] != "✳ split" {
			t.Errorf("split at %d reported %v", cut, *titles)
		}
	}
}

// TestWhatIsSafeIsLeftAlone: the emulator must keep doing its own work for
// every sequence it can parse. A hyperlink is the one that would be missed.
func TestWhatIsSafeIsLeftAlone(t *testing.T) {
	term, titles := probe(t, 60, 4)
	term.consume([]byte("\x1b[2J\x1b[1;1H\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\ after"))
	if got := firstLine(term); got != "link after" {
		t.Errorf("a hyperlink was mangled: %q", got)
	}
	term.consume([]byte("\x1b]0;plain\x07"))
	if len(*titles) != 1 || (*titles)[0] != "plain" {
		t.Errorf("titles: %v", *titles)
	}
}

// TestSubscribersSeeWhatTheAgentWrote: an attached terminal is a real one, has
// none of this trouble, and should get the title so its own window changes.
func TestSubscribersSeeWhatTheAgentWrote(t *testing.T) {
	term, _ := probe(t, 60, 4)
	sub := term.Subscribe(1 << 16)
	defer sub.Close()

	term.consume([]byte("\x1b]0;✳ working\x07"))
	data, _, err := sub.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "✳ working") {
		t.Errorf("the subscriber got %q", string(data))
	}
}
