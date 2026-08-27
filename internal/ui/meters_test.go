package ui

import (
	"strings"
	"testing"
)

// TestSparklineCountsRunesNotBytes: each mark is three bytes, and scaling by
// the byte length indexes past the end of them. The first version panicked on
// the first fleet it was pointed at.
func TestSparklineCountsRunesNotBytes(t *testing.T) {
	for _, series := range [][]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
		{100, 0, 0, 0},
		{1},
		{0, 0, 0},
	} {
		out := sparkline(series)
		if n := len([]rune(out)); n != len(series) {
			t.Errorf("%v drew %d marks, want %d", series, n, len(series))
		}
		for _, r := range out {
			if !strings.ContainsRune("▁▂▃▄▅▆▇█", r) {
				t.Errorf("%v drew %q, which is not a mark", series, r)
			}
		}
	}
}

// TestSparklineIsFlatWhenNothingHappened: a silent window is a floor, not a
// blank. A gap and a quiet moment look different and mean different things.
func TestSparklineIsFlatWhenNothingHappened(t *testing.T) {
	if got := sparkline([]int{0, 0, 0}); got != "▁▁▁" {
		t.Errorf("a silent window drew %q", got)
	}
	if got := sparkline(nil); got != "" {
		t.Errorf("no window at all drew %q", got)
	}
}

// TestMeterShowsSomethingUntilItIsGone: an empty bar and no bar at all read the
// same and mean different things — one agent is out of budget, the other has
// none.
func TestMeterShowsSomethingUntilItIsGone(t *testing.T) {
	cases := []struct{ n, max, want int }{
		{60, 60, 10}, // full
		{30, 60, 5},  // half
		{1, 60, 1},   // nearly gone, and still visible
		{0, 60, 0},   // gone
	}
	for _, c := range cases {
		got := strings.Count(meter(c.n, c.max, 10), "█")
		if got != c.want {
			t.Errorf("meter(%d, %d) filled %d of 10, want %d", c.n, c.max, got, c.want)
		}
		if n := len([]rune(meter(c.n, c.max, 10))); n != 10 {
			t.Errorf("meter(%d, %d) is %d wide, want 10", c.n, c.max, n)
		}
	}
	if got := meter(5, 0, 10); got != "" {
		t.Errorf("an agent with no budget drew %q", got)
	}
}
