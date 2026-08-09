package main

import (
	"strings"
	"testing"
)

// An attach hands the agent's raw output straight to the local terminal, so
// every mode the agent turns on is on here too — and the agent is never told
// the connection ended, so it never turns them off. Mouse reporting is the one
// that hurts: the terminal stops selecting text of its own accord, and no key
// in swarm can undo it, because swarm did not do it.
func TestLeavingAnAttachPutsTheTerminalBack(t *testing.T) {
	for _, m := range []struct{ seq, what string }{
		{"\x1b[?1049l", "alternate screen"},
		{"\x1b[?25h", "cursor"},
		{"\x1b[r", "scrolling region"},
		{"\x1b[0m", "attributes"},
		{"\x1b[?1000l", "mouse clicks"},
		{"\x1b[?1002l", "mouse cell motion"},
		{"\x1b[?1003l", "mouse any motion"},
		{"\x1b[?1005l", "utf-8 mouse encoding"},
		{"\x1b[?1006l", "sgr mouse encoding"},
		{"\x1b[?1015l", "urxvt mouse encoding"},
		{"\x1b[?2004l", "bracketed paste"},
		{"\x1b[?1004l", "focus events"},
	} {
		if !strings.Contains(leaveAgentModes, m.seq) {
			t.Errorf("leaving an attach does not put back the %s (%q)", m.what, m.seq)
		}
	}
}
