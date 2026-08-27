package ui

import (
	"strings"
	"testing"
	"time"
)

// TestThePulseVisitsEveryAgent: the mark is five agents in a ring, and the
// animation is something going round it. A pulse that skipped one, or lit two,
// would be drawing a different fleet.
func TestThePulseVisitsEveryAgent(t *testing.T) {
	seen := map[int]bool{}
	for f := range logoFrames {
		art := strings.Join(logoLines(f, ""), "\n")
		if n := strings.Count(art, "●"); n != 1 {
			t.Fatalf("frame %d lights %d nodes, want exactly one", f, n)
		}
		if n := strings.Count(art, "○"); n != 4 {
			t.Errorf("frame %d draws %d unlit nodes, want four", f, n)
		}
		seen[ringOrder[f]] = true
	}
	if len(seen) != logoFrames {
		t.Errorf("the pulse visited %d of the %d nodes", len(seen), logoFrames)
	}
}

// TestTheFrameIsTakenFromTheClock: no state to carry and none to reset, so a
// pane that appears mid-animation is already in step with the others.
func TestTheFrameIsTakenFromTheClock(t *testing.T) {
	base := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	first := logoFrame(base)
	if got := logoFrame(base.Add(logoStep)); got == first {
		t.Error("a step later is the same frame")
	}
	if got := logoFrame(base.Add(logoStep * logoFrames)); got != first {
		t.Errorf("a full turn later is frame %d, want %d", got, first)
	}
	// Whatever the clock says, including before the epoch.
	for _, at := range []time.Time{{}, base.Add(-1e9 * time.Second)} {
		if f := logoFrame(at); f < -logoFrames || f >= logoFrames {
			t.Errorf("frame %d is outside the ring", f)
		}
		if len(logoLines(logoFrame(at), "")) == 0 {
			t.Error("a frame from an odd clock drew nothing")
		}
	}
}

// TestTheLogoFitsWhereATerminalWouldHaveBeen: it is drawn in place of an
// agent's screen, so it must not be wider than the pane it replaces.
func TestTheLogoFitsWhereATerminalWouldHaveBeen(t *testing.T) {
	for _, width := range []int{10, 17, 40, 120} {
		for _, line := range logoPane(width, 12, "dev-1 is starting") {
			if w := len([]rune(stripANSI(line))); w > width && width >= 17 {
				t.Errorf("at width %d a line is %d wide: %q", width, w, line)
			}
		}
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
