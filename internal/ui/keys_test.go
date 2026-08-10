package ui

import (
	"testing"

	"github.com/emmanuel-deloget/swarm/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// Attaching from the TUI rebuilds the bytes a terminal would have sent, because
// bubbletea has already parsed them. Anything the rebuild does not know about
// is a key that silently does nothing — and ctrl+left moving a word is the kind
// of thing you notice only once it is gone.

func TestArrowsWithModifiers(t *testing.T) {
	// The modifier parameter is 1 + shift(1) + alt(2) + ctrl(4), the encoding
	// xterm defined and everything since follows.
	for _, c := range []struct {
		name string
		msg  tea.KeyMsg
		want string
	}{
		{"up", tea.KeyMsg{Type: tea.KeyUp}, "\x1b[A"},
		{"ctrl+up", tea.KeyMsg{Type: tea.KeyCtrlUp}, "\x1b[1;5A"},
		{"ctrl+down", tea.KeyMsg{Type: tea.KeyCtrlDown}, "\x1b[1;5B"},
		{"ctrl+right", tea.KeyMsg{Type: tea.KeyCtrlRight}, "\x1b[1;5C"},
		{"ctrl+left", tea.KeyMsg{Type: tea.KeyCtrlLeft}, "\x1b[1;5D"},
		{"shift+left", tea.KeyMsg{Type: tea.KeyShiftLeft}, "\x1b[1;2D"},
		{"shift+up", tea.KeyMsg{Type: tea.KeyShiftUp}, "\x1b[1;2A"},
		{"ctrl+shift+left", tea.KeyMsg{Type: tea.KeyCtrlShiftLeft}, "\x1b[1;6D"},
		{"ctrl+shift+up", tea.KeyMsg{Type: tea.KeyCtrlShiftUp}, "\x1b[1;6A"},
		{"alt+left", tea.KeyMsg{Type: tea.KeyLeft, Alt: true}, "\x1b[1;3D"},
		{"alt+ctrl+left", tea.KeyMsg{Type: tea.KeyCtrlLeft, Alt: true}, "\x1b[1;7D"},
		{"ctrl+home", tea.KeyMsg{Type: tea.KeyCtrlHome}, "\x1b[1;5H"},
		{"ctrl+end", tea.KeyMsg{Type: tea.KeyCtrlEnd}, "\x1b[1;5F"},
		{"shift+home", tea.KeyMsg{Type: tea.KeyShiftHome}, "\x1b[1;2H"},
		{"ctrl+pgup", tea.KeyMsg{Type: tea.KeyCtrlPgUp}, "\x1b[5;5~"},
		{"ctrl+pgdn", tea.KeyMsg{Type: tea.KeyCtrlPgDown}, "\x1b[6;5~"},
	} {
		if got := string(keyBytes(c.msg)); got != c.want {
			t.Errorf("%s -> %q, want %q", c.name, got, c.want)
		}
	}
}

// TestNoModifiedKeyIsDropped: a key that rebuilds to nothing is a key that does
// nothing, with no error and no clue. This is the check that would have caught
// the missing arrows.
func TestNoModifiedKeyIsDropped(t *testing.T) {
	for typ, name := range map[tea.KeyType]string{
		tea.KeyShiftUp: "shift+up", tea.KeyShiftDown: "shift+down",
		tea.KeyShiftLeft: "shift+left", tea.KeyShiftRight: "shift+right",
		tea.KeyShiftHome: "shift+home", tea.KeyShiftEnd: "shift+end",
		tea.KeyCtrlUp: "ctrl+up", tea.KeyCtrlDown: "ctrl+down",
		tea.KeyCtrlLeft: "ctrl+left", tea.KeyCtrlRight: "ctrl+right",
		tea.KeyCtrlHome: "ctrl+home", tea.KeyCtrlEnd: "ctrl+end",
		tea.KeyCtrlPgUp: "ctrl+pgup", tea.KeyCtrlPgDown: "ctrl+pgdown",
		tea.KeyCtrlShiftUp: "ctrl+shift+up", tea.KeyCtrlShiftDown: "ctrl+shift+down",
		tea.KeyCtrlShiftLeft: "ctrl+shift+left", tea.KeyCtrlShiftRight: "ctrl+shift+right",
		tea.KeyCtrlShiftHome: "ctrl+shift+home", tea.KeyCtrlShiftEnd: "ctrl+shift+end",
	} {
		if got := keyBytes(tea.KeyMsg{Type: typ}); len(got) == 0 {
			t.Errorf("%s rebuilds to nothing", name)
		}
	}
}

// TestPlainKeysAreUnchanged: the modifier work must not disturb what already
// reached agents correctly.
func TestPlainKeysAreUnchanged(t *testing.T) {
	for _, c := range []struct {
		msg  tea.KeyMsg
		want string
	}{
		{tea.KeyMsg{Type: tea.KeyUp}, "\x1b[A"},
		{tea.KeyMsg{Type: tea.KeyDown}, "\x1b[B"},
		{tea.KeyMsg{Type: tea.KeyRight}, "\x1b[C"},
		{tea.KeyMsg{Type: tea.KeyLeft}, "\x1b[D"},
		{tea.KeyMsg{Type: tea.KeyHome}, "\x1b[H"},
		{tea.KeyMsg{Type: tea.KeyEnd}, "\x1b[F"},
		{tea.KeyMsg{Type: tea.KeyEsc}, "\x1b"},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, "\x1b[Z"},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")}, "hi"},
	} {
		if got := string(keyBytes(c.msg)); got != c.want {
			t.Errorf("%v -> %q, want %q", c.msg.Type, got, c.want)
		}
	}
}

// TestStalledLooksDifferentFromIdle: the whole complaint about idle was that an
// agent which owes something and has gone quiet is green, and green reads as
// fine. It has to look like something.
func TestStalledLooksDifferentFromIdle(t *testing.T) {
	idle := agent.Info{Name: "a", State: agent.StateIdle}
	stalled := agent.Info{Name: "a", State: agent.StateIdle, Stalled: true, Owed: 1}

	if stateGlyph(idle) == stateGlyph(stalled) {
		t.Error("stalled has the same glyph as idle")
	}
	if stateColor(idle) == stateColor(stalled) {
		t.Error("stalled has the same colour as idle")
	}
	if got := stateLabel(stalled); got != "stalled" {
		t.Errorf("the pane header calls it %q", got)
	}
	if got := stateLabel(idle); got != "idle" {
		t.Errorf("an ordinary idle agent is called %q", got)
	}
	// Attention still wins: a prompt waiting on you is the more urgent of the
	// two, and it is the one you can act on.
	both := agent.Info{Name: "a", State: agent.StateIdle, Stalled: true, Attention: "approval"}
	if stateGlyph(both) != "▲" {
		t.Errorf("attention lost to stalled: %q", stateGlyph(both))
	}
}
