package ui

import (
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// swarm's mark is five agents wired in a ring, and the only honest way to
// animate it is to pass something round that ring — which is what the fleet it
// stands for does.
//
// It is drawn where a terminal would otherwise be blank: an agent CLI can take
// a while to draw its first frame, and an empty pane during that is
// indistinguishable from one that failed to start.
//
// In braille, because the alternatives are worse. A terminal cell is about
// twice as tall as it is wide, so the mark's shallow diagonals are nothing
// like 45° — and `╱` repeated across a row is what a shallow diagonal looks
// like in box drawing, which is to say a staircase. Braille gives eight
// sub-cells to place a line in, and the line comes out as a line.

// The mark's five nodes, in the coordinates assets/icon.svg uses, and the ring
// its links make: top, right, bottom right, bottom left, left, back to top.
var (
	logoNodes = [][2]float64{{16, 8.5}, {8.5, 14}, {23.5, 14}, {11, 22.5}, {21, 22.5}}
	logoRing  = [][2]int{{0, 2}, {2, 4}, {4, 3}, {3, 1}, {1, 0}}
)

// logoTurn is how long the pulse takes to go round once. Slow enough to follow.
const logoTurn = 2200 * time.Millisecond

var (
	styLogoWire = lipgloss.NewStyle().Foreground(lipgloss.Color("#0f766e"))
	styLogoNode = lipgloss.NewStyle().Foreground(lipgloss.Color("#2dd4bf"))
	styLogoLit  = lipgloss.NewStyle().Foreground(lipgloss.Color("#5eead4")).Bold(true)
)

// brailleDot is the bit each of a cell's eight positions owns.
var brailleDot = [4][2]uint8{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

// canvas is a grid of braille cells with a colour each. A cell takes the
// brightest colour anything in it asked for, so a pulse passing through shows
// even when a dim wire shares the cell.
type canvas struct {
	w, h  int
	dots  [][]uint8
	shade [][]int
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: w, h: h, dots: make([][]uint8, h), shade: make([][]int, h)}
	for i := range c.dots {
		c.dots[i] = make([]uint8, w)
		c.shade[i] = make([]int, w)
	}
	return c
}

func (c *canvas) set(dx, dy, shade int) {
	x, y := dx/2, dy/4
	if x < 0 || y < 0 || x >= c.w || y >= c.h || dx < 0 || dy < 0 {
		return
	}
	c.dots[y][x] |= brailleDot[dy%4][dx%2]
	if shade > c.shade[y][x] {
		c.shade[y][x] = shade
	}
}

func (c *canvas) lines() []string {
	styles := []lipgloss.Style{styLogoWire, styLogoNode, styLogoLit}
	out := make([]string, c.h)
	for y := range c.dots {
		var b strings.Builder
		for x := range c.dots[y] {
			if c.dots[y][x] == 0 {
				b.WriteString(" ")
				continue
			}
			b.WriteString(styles[c.shade[y][x]].Render(string(rune(0x2800 + int(c.dots[y][x])))))
		}
		out[y] = strings.TrimRight(b.String(), " ")
	}
	return out
}

// logoPhase is where the pulse is on the ring, 0 to 1, taken from the clock:
// no state to carry, none to reset, and a pane that appears mid-turn is
// already in step with the others.
func logoPhase(now time.Time) float64 {
	ms := now.UnixMilli() % int64(logoTurn/time.Millisecond)
	if ms < 0 {
		ms += int64(logoTurn / time.Millisecond)
	}
	return float64(ms) / float64(logoTurn/time.Millisecond)
}

// logoLines draws the mark at a size in cells, with the pulse at a phase.
func logoLines(w, h int, phase float64) []string {
	c := logoCanvas(w, h, phase)
	if c == nil {
		return nil
	}
	return c.lines()
}

// logoCanvas is the drawing itself, before it becomes strings — which is what
// a test can ask questions of, since the colour half of the pulse is stripped
// the moment nothing is attached to a terminal.
func logoCanvas(w, h int, phase float64) *canvas {
	if w < 8 || h < 4 {
		return nil
	}
	c := newCanvas(w, h)
	dw, dh := w*2, h*4

	at := make([][2]float64, len(logoNodes))
	for i, p := range logoNodes {
		at[i] = [2]float64{
			2 + (p[0]-8.5)/15*float64(dw-5),
			2 + (p[1]-8.5)/14*float64(dh-5),
		}
	}

	seg := 1.0 / float64(len(logoRing))
	for i, l := range logoRing {
		a, b := at[l[0]], at[l[1]]
		steps := int(math.Hypot(b[0]-a[0], b[1]-a[1])) + 1
		for s := 0; s <= steps; s++ {
			t := float64(s) / float64(steps)
			// How far this point is from the pulse, the short way round.
			d := math.Abs(float64(i)*seg + t*seg - phase)
			if d > 0.5 {
				d = 1 - d
			}
			shade := 0
			switch {
			case d < 0.018:
				shade = 2
			case d < 0.055:
				shade = 1
			}
			x, y := int(a[0]+(b[0]-a[0])*t), int(a[1]+(b[1]-a[1])*t)
			c.set(x, y, shade)
			// The pulse is thicker as well as brighter. Colour alone is
			// invisible on a terminal that has none, and swarm has no way of
			// knowing whether this one does.
			if shade == 2 {
				c.set(x+1, y, shade)
				c.set(x, y+1, shade)
			}
		}
	}
	// The nodes themselves, always brighter than the wire between them.
	for _, p := range at {
		for _, d := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}, {-1, 0}, {-1, 1}} {
			c.set(int(p[0])+d[0], int(p[1])+d[1], 1)
		}
	}
	return c
}

// logoSize picks a mark that fits the space, keeping the shape square-ish: a
// cell is about twice as tall as it is wide, so the height in cells is about
// half the width.
func logoSize(width, height int) (w, h int) {
	w = width - 4
	if w > 44 {
		w = 44
	}
	if w > (height-4)*2 {
		w = (height - 4) * 2
	}
	if w < 16 {
		w = 16
	}
	h = int(float64(w)*0.47) + 1
	if h < 6 {
		h = 6
	}
	return w, h
}

// logoPane centres the mark in the space a terminal would have taken, with the
// caption under it — cut to fit, since it is a name and a word and either can
// be longer than a narrow pane.
func logoPane(width, height int, caption string) []string {
	w, h := logoSize(width, height)
	art := logoLines(w, h, logoPhase(time.Now()))
	if len(art) == 0 {
		return nil
	}
	pad := (width - w) / 2
	if pad < 0 {
		pad = 0
	}
	left := strings.Repeat(" ", pad)

	out := make([]string, 0, height)
	for range (height - len(art) - 2) / 2 {
		out = append(out, "")
	}
	for _, line := range art {
		out = append(out, left+line)
	}
	if caption != "" {
		if room := width - pad; room > 1 && len([]rune(caption)) > room {
			caption = string([]rune(caption)[:room-1]) + "…"
		}
		out = append(out, "", left+styMuted.Render(caption))
	}
	return out
}
