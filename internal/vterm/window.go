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

	// Where the cursor is, so the pane can show it. Without this there is no
	// way to tell where typing would land — which matters most while attached.
	cursor := t.emu.CursorPosition()
	cursorRow := -1
	if t.curVisible.Load() {
		cursorRow = cursor.Y
	}

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
		cursorX := -1
		if y == cursorRow {
			cursorX = cursor.X
		}
		out = append(out, renderScreenRow(t, y, cols, cursorX))
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
	}, cols, -1)
}

func renderScreenRow(t *Terminal, y, cols, cursorX int) string {
	return renderCells(func(x int) *uv.Cell { return t.emu.CellAt(x, y) }, cols, cursorX)
}

// renderCells turns a row of cells into an ANSI string, merging runs that share
// a style. Trailing blanks are dropped: padding is the caller's business, and
// an unstyled tail would otherwise carry a background colour across the pane.
//
// cursorX, when not negative, is drawn in reverse video: the pane is a picture
// of the screen, not the terminal's own cursor, so the cursor has to be part of
// the picture.
func renderCells(at func(x int) *uv.Cell, cols, cursorX int) string {
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
	// The cursor may sit past the last written cell — at the end of a prompt,
	// which is exactly where one looks for it.
	if cursorX >= 0 && cursorX < cols && cursorX+1 > end {
		end = cursorX + 1
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
		if x == cursorX {
			// Its own run, whatever the cell around it looks like.
			flush()
			cursorStyle := style
			cursorStyle.Attrs ^= uv.AttrReverse
			b.WriteString(cursorStyle.Styled(content))
			x += width
			continue
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

// scrollUp moves the screen up by n lines, pushing what leaves the top into the
// scrollback. It is used before shrinking the height, so the cursor and the
// output around it survive.
//
// It works on cells rather than by writing a scroll sequence to the emulator,
// and that is the whole point: the parser may be halfway through an escape
// sequence from the agent — an OSC carrying a window title, say — and injecting
// bytes there truncates it, spilling the rest onto the screen.
//
// The caller holds mu.
func (t *Terminal) scrollUp(n int) {
	if n <= 0 {
		return
	}
	if n > t.rows {
		n = t.rows
	}

	if sb := t.emu.Scrollback(); sb != nil {
		for y := range n {
			line := make(uv.Line, t.cols)
			for x := range t.cols {
				if c := t.emu.CellAt(x, y); c != nil {
					line[x] = *c
				}
			}
			sb.Push(line)
		}
	}

	// Top to bottom: each source row sits below the destination, so nothing is
	// overwritten before it has been read.
	for y := n; y < t.rows; y++ {
		for x := range t.cols {
			cell := uv.Cell{Content: " ", Width: 1}
			if c := t.emu.CellAt(x, y); c != nil {
				cell = *c
			}
			t.emu.SetCell(x, y-n, &cell)
		}
	}
	for y := t.rows - n; y < t.rows; y++ {
		for x := range t.cols {
			blank := uv.Cell{Content: " ", Width: 1}
			t.emu.SetCell(x, y, &blank)
		}
	}
}
