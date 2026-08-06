package vterm

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

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

func TestKeySequence(t *testing.T) {
	cases := map[string]string{
		"enter":       "\r",
		"esc":         "\x1b",
		"ctrl+c":      "\x03",
		"^d":          "\x04",
		"ctrl+D":      "\x04",
		"alt+enter":   "\x1b\r",
		"up":          "\x1b[A",
		"shift+tab":   "\x1b[Z",
		"a":           "a",
		"alt+b":       "\x1bb",
		"pageup":      "\x1b[5~",
		"ctrl+enter":  "\n",
		"shift+enter": "\x1b\r",
	}
	for in, want := range cases {
		got, err := KeySequence(in)
		if err != nil {
			t.Errorf("KeySequence(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("KeySequence(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := KeySequence("nope-nope"); err == nil {
		t.Error("expected an error for an unknown key")
	}
	seq, err := KeySequences("ctrl+c esc enter")
	if err != nil {
		t.Fatalf("KeySequences: %v", err)
	}
	if seq != "\x03\x1b\r" {
		t.Fatalf("KeySequences = %q", seq)
	}
}

func TestSanitizeText(t *testing.T) {
	in := "hello\x1b[31m world\x03\r\nnext\ttab\x07"
	got := SanitizeText(in)
	want := "hello[31m world\nnext\ttab"
	if got != want {
		t.Fatalf("SanitizeText = %q, want %q", got, want)
	}
}

func TestScanModesTracksBracketedPaste(t *testing.T) {
	term := &Terminal{}
	if term.BracketedPaste() {
		t.Fatal("bracketed paste should start off")
	}

	term.scanModes([]byte("hello \x1b[?2004h world"))
	if !term.BracketedPaste() {
		t.Fatal("2004h should enable it")
	}
	term.scanModes([]byte("\x1b[?2004l"))
	if term.BracketedPaste() {
		t.Fatal("2004l should disable it")
	}

	// Combined parameters, as some applications send them.
	term.scanModes([]byte("\x1b[?1049;2004h"))
	if !term.BracketedPaste() {
		t.Fatal("2004 among several parameters should be seen")
	}

	// Unrelated modes must not touch it.
	term.scanModes([]byte("\x1b[?25l\x1b[?1000h"))
	if !term.BracketedPaste() {
		t.Fatal("an unrelated mode changed the paste mode")
	}

	// A sequence split across two reads must still be recognised.
	term2 := &Terminal{}
	term2.scanModes([]byte("text\x1b[?20"))
	if term2.BracketedPaste() {
		t.Fatal("half a sequence should not enable anything")
	}
	term2.scanModes([]byte("04h more"))
	if !term2.BracketedPaste() {
		t.Fatal("a sequence split across reads was missed")
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
