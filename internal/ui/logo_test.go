package ui

import (
	"strings"
	"testing"
	"time"
)

// braille is the block the mark is drawn from. A cell outside it means the art
// escaped into characters that are not part of the drawing.
func isBraille(r rune) bool { return r >= 0x2800 && r <= 0x28FF }

// TestTheMarkIsDrawnInBraille: a terminal cell is about twice as tall as it is
// wide, so the mark's diagonals are nothing like 45° — and box drawing has no
// glyph for a shallow one, which is why the first version came out as a
// staircase. Braille has eight sub-cells to put a line in.
func TestTheMarkIsDrawnInBraille(t *testing.T) {
	w, h := logoSize(78, 22)
	art := logoLines(w, h, 0)
	if len(art) != h {
		t.Fatalf("asked for %d rows, drew %d", h, len(art))
	}
	dots := 0
	for _, line := range art {
		for _, r := range stripANSI(line) {
			if r == ' ' {
				continue
			}
			if !isBraille(r) {
				t.Errorf("the mark contains %q, which is not part of the drawing", r)
			}
			dots++
		}
	}
	if dots < 40 {
		t.Errorf("the mark is %d cells of ink, which is not a drawing", dots)
	}
}

// TestThePulseGoesRound: the mark is five agents in a ring and the animation is
// something going round it, so successive phases must differ — and a full turn
// must come back to where it started.
func TestThePulseGoesRound(t *testing.T) {
	w, h := logoSize(78, 22)
	// Compared as canvases, not as strings: lipgloss strips colour the moment
	// nothing is attached to a terminal, and half the pulse is colour.
	shape := func(phase float64) string {
		c := logoCanvas(w, h, phase)
		var b strings.Builder
		for y := range c.shade {
			for x := range c.shade[y] {
				b.WriteByte(byte('0' + c.shade[y][x]))
				b.WriteByte(c.dots[y][x])
			}
		}
		return b.String()
	}
	seen := map[string]bool{}
	for i := range 8 {
		seen[shape(float64(i)/8)] = true
	}
	if len(seen) < 6 {
		t.Errorf("eight phases drew %d different frames; the pulse is barely moving", len(seen))
	}
	if shape(0) != shape(1) {
		t.Error("a full turn does not come back to the start")
	}
}

// TestThePulseIsVisibleWithoutColour: a terminal that has no colour, or a
// reader who cannot tell teal from teal, still has to see something move. The
// pulse thickens the wire as well as brightening it.
func TestThePulseIsVisibleWithoutColour(t *testing.T) {
	w, h := logoSize(78, 22)
	ink := func(phase float64) string {
		return strings.Join(logoLines(w, h, phase), "\n")
	}
	if ink(0.05) == ink(0.30) {
		t.Error("two phases draw the same ink; the pulse is colour only")
	}
}

// TestThePhaseIsTakenFromTheClock: no state to carry and none to reset, so a
// pane that appears mid-turn is already in step with the others.
func TestThePhaseIsTakenFromTheClock(t *testing.T) {
	base := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	if p := logoPhase(base); p < 0 || p >= 1 {
		t.Errorf("phase %v is outside the ring", p)
	}
	if a, b := logoPhase(base), logoPhase(base.Add(logoTurn)); a != b {
		t.Errorf("a full turn later is phase %v, want %v", b, a)
	}
	// Whatever the clock says, including before the epoch.
	for _, at := range []time.Time{{}, base.Add(-1e9 * time.Second)} {
		if p := logoPhase(at); p < 0 || p >= 1 {
			t.Errorf("an odd clock gave phase %v", p)
		}
	}
}

// TestTheMarkFitsWhereATerminalWouldHaveBeen: it is drawn in place of an
// agent's screen, so no line may be wider than the pane it replaces.
func TestTheMarkFitsWhereATerminalWouldHaveBeen(t *testing.T) {
	for _, size := range [][2]int{{20, 8}, {40, 12}, {80, 24}, {200, 60}} {
		for _, line := range logoPane(size[0], size[1], "review-2 is starting") {
			if w := len([]rune(stripANSI(line))); w > size[0] {
				t.Errorf("at %dx%d a line is %d wide", size[0], size[1], w)
			}
		}
	}
	// Too small to draw in at all, rather than something broken.
	if got := logoLines(4, 2, 0); got != nil {
		t.Errorf("a pane with no room drew %d lines", len(got))
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
