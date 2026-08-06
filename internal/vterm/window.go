package vterm

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
)

// RenderWindow returns height lines of the agent's output, ending offset lines
// above the bottom, drawing from the scrollback once it walks off the top of
// the screen. It also reports the largest offset that still shows something, so
// a caller can stop scrolling at the beginning of the session instead of
// wandering into blank space.
//
// This is what makes scrolling in a pane worth anything: the visible screen is
// usually exactly the size of the pane, so without the scrollback there would
// be nothing above it to see.
func (t *Terminal) RenderWindow(offset, height int) (lines []string, maxOffset int) {
	if height <= 0 {
		return nil, 0
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	cols, rows := t.cols, t.rows
	back := t.emu.ScrollbackLen()
	total := back + rows

	maxOffset = total - height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}

	// The window covers global lines [first, last), where global line i is in
	// the scrollback while i < back, and row i-back of the screen after that.
	last := total - offset
	first := last - height
	if first < 0 {
		first = 0
	}

	out := make([]string, 0, height)
	for i := first; i < last; i++ {
		if i < back {
			out = append(out, renderScrollbackLine(t.emu.Scrollback().Line(i), cols))
			continue
		}
		y := i - back
		out = append(out, renderScreenRow(t, y, cols))
	}
	for len(out) < height {
		out = append(out, "")
	}
	return out, maxOffset
}

// ScrollbackLen is the number of lines that have scrolled off the top.
func (t *Terminal) ScrollbackLen() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.emu.ScrollbackLen()
}

func renderScrollbackLine(line uv.Line, cols int) string {
	return renderCells(func(x int) *uv.Cell {
		if x < 0 || x >= len(line) {
			return nil
		}
		return &line[x]
	}, cols)
}

func renderScreenRow(t *Terminal, y, cols int) string {
	return renderCells(func(x int) *uv.Cell { return t.emu.CellAt(x, y) }, cols)
}

// renderCells turns a row of cells into an ANSI string, merging runs that share
// a style. Trailing blanks are dropped: padding is the caller's business, and
// an unstyled tail would otherwise carry a background colour across the pane.
func renderCells(at func(x int) *uv.Cell, cols int) string {
	// Find the last cell worth printing.
	end := 0
	for x := range cols {
		c := at(x)
		if c == nil {
			continue
		}
		content := c.Content
		if (content != "" && content != " ") || !c.Style.IsZero() {
			end = x + 1
		}
	}
	if end == 0 {
		return ""
	}

	var (
		b        strings.Builder
		run      strings.Builder
		runStyle uv.Style
		runOpen  bool
	)
	flush := func() {
		if !runOpen {
			return
		}
		b.WriteString(runStyle.Styled(run.String()))
		run.Reset()
		runOpen = false
	}

	for x := 0; x < end; {
		c := at(x)
		width := 1
		content := " "
		var style uv.Style
		if c != nil {
			style = c.Style
			if c.Content != "" {
				content = c.Content
			}
			if c.Width > 1 {
				width = c.Width
			}
		}
		if runOpen && !runStyle.Equal(&style) {
			flush()
		}
		if !runOpen {
			runStyle, runOpen = style, true
		}
		run.WriteString(content)
		x += width
	}
	flush()
	return b.String()
}
