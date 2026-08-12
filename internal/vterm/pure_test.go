package vterm

import (
	"strings"
	"testing"
	"time"
)

// The parts of the package that need no child process, and therefore run
// everywhere. What is left in vterm_test.go and its neighbours drives a real
// process through sh, which is why those carry a //go:build !windows.

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

func TestBindableVersusSendable(t *testing.T) {
	// Some names exist so those bytes can be *sent* to an agent, but a terminal
	// never produces them as a distinct key press: binding one would leave the
	// user with a key that is advertised and never fires.
	for _, name := range []string{"ctrl+enter", "shift+enter"} {
		if _, err := KeySequence(name); err != nil {
			t.Errorf("%q should still be sendable: %v", name, err)
		}
		if Bindable(name) {
			t.Errorf("%q should not be bindable", name)
		}
		if err := CheckBindable(name); err == nil {
			t.Errorf("CheckBindable(%q) should explain why not", name)
		} else if !strings.Contains(err.Error(), "same bytes") {
			t.Errorf("CheckBindable(%q) should say why: %v", name, err)
		}
	}

	// The keys one would actually reach for are bindable.
	for _, name := range []string{"ctrl+g", "ctrl+]", "^q", "esc", "f12", "alt+enter", "shift+tab"} {
		if err := CheckBindable(name); err != nil {
			t.Errorf("CheckBindable(%q) = %v, want it accepted", name, err)
		}
	}

	// An unknown name is still an error, with its own message.
	if err := CheckBindable("ctrl+nonsense"); err == nil {
		t.Error("an unknown name should be refused")
	}

	// A multi-key binding is checked key by key.
	if err := CheckBindable("esc ctrl+enter"); err == nil {
		t.Error("a sequence containing an unbindable key should be refused")
	}
}

func TestKeyNamesAreAllUsable(t *testing.T) {
	names := KeyNames()
	if len(names) < 20 {
		t.Fatalf("KeyNames returned %d names, expected the whole alias table", len(names))
	}
	// Everything the listing prints must translate, or the listing is a lie.
	for _, n := range names {
		if _, err := KeySequence(n); err != nil {
			t.Errorf("KeyNames lists %q but KeySequence rejects it: %v", n, err)
		}
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("KeyNames should be sorted: %q before %q", names[i-1], names[i])
		}
	}
}

// TestModifiedNavigationKeys: a terminal reports these with a parameter — 1
// plus shift(1), alt(2), ctrl(4) — and swarm had no name for any of them. They
// could be received and not sent, so `swarm keys dev-1 ctrl+left` failed and
// ctrl+left could not be a detach key.
//
// The bytes on the right were measured with `swarm keys -read` on a Windows 10
// console, which sends the whole family.
func TestModifiedNavigationKeys(t *testing.T) {
	for name, want := range map[string]string{
		"ctrl+up":       "\x1b[1;5A",
		"ctrl+right":    "\x1b[1;5C",
		"ctrl+down":     "\x1b[1;5B",
		"ctrl+left":     "\x1b[1;5D",
		"ctrl+home":     "\x1b[1;5H",
		"ctrl+end":      "\x1b[1;5F",
		"ctrl+pgup":     "\x1b[5;5~",
		"ctrl+pagedown": "\x1b[6;5~",
		"shift+up":      "\x1b[1;2A",
		"shift+left":    "\x1b[1;2D",
		"shift+home":    "\x1b[1;2H",
		"alt+up":        "\x1b[1;3A",
		"alt+left":      "\x1b[1;3D",
		"ctrl+shift+up": "\x1b[1;6A",
		"ctrl+alt+left": "\x1b[1;7D",
		"shift+delete":  "\x1b[3;2~",
		"ctrl+insert":   "\x1b[2;5~",
	} {
		got, err := KeySequence(name)
		if err != nil {
			t.Errorf("KeySequence(%q): %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("KeySequence(%q) = %q, want %q", name, got, want)
		}
	}

	// The plain keys are untouched, and so are the patterns a modifier shares
	// its prefix with: ctrl+a is a control character, not a navigation key.
	for name, want := range map[string]string{
		"up": "\x1b[A", "home": "\x1b[H", "pgup": "\x1b[5~",
		"ctrl+a": "\x01", "alt+a": "\x1ba",
	} {
		if got, err := KeySequence(name); err != nil || got != want {
			t.Errorf("KeySequence(%q) = %q, %v; want %q", name, got, err, want)
		}
	}

	// A modifier on something that has no modified form is still an error
	// rather than a silent nothing.
	for _, name := range []string{"shift+nonsense", "ctrl+shift+nope"} {
		if _, err := KeySequence(name); err == nil {
			t.Errorf("KeySequence(%q) should have failed", name)
		}
	}
}

// TestMouseModesAreTrackedSeparately: 1000 asks for clicks, 1002 and 1003 also
// want the pointer's movements. Sending movements to an application that asked
// for neither is noise it has to skip, so the difference is kept rather than
// flattened into "the mouse is on".
func TestMouseModesAreTrackedSeparately(t *testing.T) {
	term := &Terminal{}
	if term.MouseReporting() || term.MouseMotion() {
		t.Fatal("a fresh terminal is tracking the mouse")
	}

	term.scanModes([]byte("\x1b[?1000h"))
	if !term.MouseReporting() || term.MouseMotion() {
		t.Error("1000 asks for clicks, not for movements")
	}

	term.scanModes([]byte("\x1b[?1002h"))
	if !term.MouseMotion() {
		t.Error("1002 asks for movements too")
	}

	// Dropping 1002 leaves 1000 behind: an application narrowing what it wants
	// still wants the clicks.
	term.scanModes([]byte("\x1b[?1002l"))
	if !term.MouseReporting() {
		t.Error("turning 1002 off took 1000 with it")
	}
	if term.MouseMotion() {
		t.Error("1002 was turned off and movements are still expected")
	}

	term.scanModes([]byte("\x1b[?1000l"))
	if term.MouseReporting() {
		t.Error("with every mode off, the mouse is still reported as tracked")
	}

	// 1003 counts as movement as well, and combined parameters are seen.
	term.scanModes([]byte("\x1b[?1049;1003h"))
	if !term.MouseMotion() {
		t.Error("1003 among several parameters was missed")
	}
}
