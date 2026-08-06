package vterm

import (
	"fmt"
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// HTML renders the current screen as HTML, one <div> per line, with styled
// spans for runs of identical formatting.
//
// Producing the HTML here rather than shipping the raw escape stream means the
// browser needs no terminal emulator library: the page stays a few kilobytes
// and works with no network beyond swarm itself.
func (t *Terminal) HTML() string {
	return strings.Join(t.HTMLLines(), "")
}

// HTMLLines renders the screen as one HTML fragment per line. Keeping the lines
// apart lets the web server send only the ones that changed, which is what
// makes remote control usable on a phone.
func (t *Terminal) HTMLLines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	cols, rows := t.cols, t.rows
	curPos := t.emu.CursorPosition()
	curVisible := t.curVisible.Load()

	out := make([]string, 0, rows)
	var b strings.Builder
	b.Grow(cols * 2)

	for y := 0; y < rows; y++ {
		b.Reset()
		b.WriteString(`<div class="l">`)
		var (
			run      strings.Builder
			runStyle uv.Style
			runOpen  bool
		)
		flush := func() {
			if !runOpen {
				return
			}
			writeSpan(&b, runStyle, run.String())
			run.Reset()
			runOpen = false
		}

		for x := 0; x < cols; {
			cell := t.emu.CellAt(x, y)
			width := 1
			content := " "
			var style uv.Style
			if cell != nil {
				style = cell.Style
				if cell.Content != "" {
					content = cell.Content
				}
				if cell.Width > 1 {
					width = cell.Width
				}
			}

			isCursor := curVisible && y == curPos.Y && x == curPos.X
			if isCursor {
				flush()
				b.WriteString(`<span class="cur">`)
				b.WriteString(escapeHTML(content))
				b.WriteString(`</span>`)
				x += width
				continue
			}

			if runOpen && !runStyle.Equal(&style) {
				flush()
			}
			if !runOpen {
				runStyle = style
				runOpen = true
			}
			run.WriteString(escapeHTML(content))
			x += width
		}
		flush()
		b.WriteString("</div>")
		out = append(out, b.String())
	}
	return out
}

func writeSpan(b *strings.Builder, s uv.Style, text string) {
	if text == "" {
		return
	}
	css := styleCSS(s)
	if css == "" {
		b.WriteString(text)
		return
	}
	fmt.Fprintf(b, `<span style="%s">%s</span>`, css, text)
}

func styleCSS(s uv.Style) string {
	var parts []string
	fg, bg := s.Fg, s.Bg
	if s.Attrs&uv.AttrReverse != 0 {
		fg, bg = bg, fg
		if fg == nil {
			parts = append(parts, "color:var(--bg)")
		}
		if bg == nil {
			parts = append(parts, "background:var(--fg)")
		}
	}
	if fg != nil {
		parts = append(parts, "color:"+cssColor(fg))
	}
	if bg != nil {
		parts = append(parts, "background:"+cssColor(bg))
	}
	if s.Attrs&uv.AttrBold != 0 {
		parts = append(parts, "font-weight:700")
	}
	if s.Attrs&uv.AttrFaint != 0 {
		parts = append(parts, "opacity:.6")
	}
	if s.Attrs&uv.AttrItalic != 0 {
		parts = append(parts, "font-style:italic")
	}
	if s.Attrs&uv.AttrStrikethrough != 0 {
		parts = append(parts, "text-decoration:line-through")
	} else if s.Underline != uv.UnderlineNone {
		parts = append(parts, "text-decoration:underline")
	}
	if s.Attrs&uv.AttrConceal != 0 {
		parts = append(parts, "visibility:hidden")
	}
	return strings.Join(parts, ";")
}

// webPalette gives the 16 ANSI colours a modern look in the browser; the
// classic 128,0,0-style values the emulator reports are unreadable on a dark
// page.
var webPalette = [16]string{
	"#3b4048", "#e06c75", "#98c379", "#e5c07b", "#61afef", "#c678dd", "#56b6c2", "#c8ccd4",
	"#5c6370", "#ff7b72", "#7ee787", "#f0d67c", "#7aa2f7", "#d2a8ff", "#79c0ff", "#ffffff",
}

func cssColor(c color.Color) string {
	if b, ok := c.(ansi.BasicColor); ok && int(b) < len(webPalette) {
		return webPalette[b]
	}
	r, g, bl, a := c.RGBA()
	if a == 0 {
		return "inherit"
	}
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, bl>>8)
}

func escapeHTML(s string) string {
	if !strings.ContainsAny(s, `&<>"`) {
		return s
	}
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
