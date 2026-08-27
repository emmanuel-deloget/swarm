package ui

import "strings"

// The two small drawings the sidebar and the pane share with `swarm ls`. They
// are here rather than imported from the CLI because the two ends render the
// same numbers for different widths, and a package that draws is the wrong
// thing to depend on a package that parses flags.

// meter draws a proportion as a bar.
func meter(n, ceiling, width int) string {
	if ceiling <= 0 || width <= 0 {
		return ""
	}
	filled := n * width / ceiling
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	// Spent but not gone still shows something: an empty bar and no bar at all
	// read the same and mean different things.
	if filled == 0 && n > 0 {
		filled = 1
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// sparkline draws a series as one line of eighths, empty slices included —
// a gap and a quiet moment look different and mean different things.
func sparkline(series []int) string {
	// Runes, not bytes: each of these is three bytes, and scaling by the byte
	// length walks off the end of the marks.
	marks := []rune("▁▂▃▄▅▆▇█")
	if len(series) == 0 {
		return ""
	}
	top := 0
	for _, n := range series {
		if n > top {
			top = n
		}
	}
	var b strings.Builder
	for _, n := range series {
		if top == 0 {
			b.WriteRune(marks[0])
			continue
		}
		b.WriteRune(marks[n*(len(marks)-1)/top])
	}
	return b.String()
}
