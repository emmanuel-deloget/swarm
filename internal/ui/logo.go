package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// swarm's mark is five agents wired in a ring, and the only honest way to
// animate it is to pass something round that ring — which is what the fleet
// it stands for does.
//
// It is shown where a terminal would otherwise be blank: an agent CLI can take
// a while to draw its first frame, and an empty pane during that is
// indistinguishable from one that failed to start.

// ringOrder walks the five nodes the way the links in assets/icon.svg join
// them: top, right, bottom right, bottom left, left.
var ringOrder = []int{0, 2, 4, 3, 1}

// logoFrames is how many steps the pulse takes, and logoStep how long each
// lasts. Slow enough to read as a pulse rather than a flicker.
const (
	logoFrames = 5
	logoStep   = 320 * time.Millisecond
)

var (
	styLogoLit  = lipgloss.NewStyle().Foreground(lipgloss.Color("#2dd4bf")).Bold(true)
	styLogoDim  = lipgloss.NewStyle().Foreground(lipgloss.Color("#2dd4bf")).Faint(true)
	styLogoWire = lipgloss.NewStyle().Foreground(lipgloss.Color("#2dd4bf")).Faint(true)
)

// logoFrame is which node is lit now, from the clock rather than from state
// the model would have to carry and reset.
func logoFrame(now time.Time) int {
	return int(now.UnixMilli()/int64(logoStep/time.Millisecond)) % logoFrames
}

// logoLines draws the mark with one node lit, and the caption under it.
func logoLines(frame int, caption string) []string {
	lit := ringOrder[((frame%logoFrames)+logoFrames)%logoFrames]
	node := func(i int) string {
		if i == lit {
			return styLogoLit.Render("●")
		}
		return styLogoDim.Render("○")
	}
	wire := styLogoWire.Render
	out := []string{
		"        " + node(0),
		"      " + wire("╱") + "   " + wire("╲"),
		"    " + node(1) + "       " + node(2),
		"    " + wire("│") + "       " + wire("│"),
		"    " + node(3) + wire("───────") + node(4),
	}
	if caption != "" {
		out = append(out, "", "  "+styMuted.Render(caption))
	}
	return out
}

// logoPane centres the mark in the space a terminal would have taken, and cuts
// the caption to fit: it is a name and a word, and either can be longer than a
// narrow pane.
func logoPane(width, height int, caption string) []string {
	const artWidth = 17
	pad := (width - artWidth) / 2
	if pad < 0 {
		pad = 0
	}
	left := strings.Repeat(" ", pad)

	art := logoLines(logoFrame(time.Now()), "")
	out := make([]string, 0, height)
	for range (height - len(art) - 2) / 2 {
		out = append(out, "")
	}
	for _, line := range art {
		out = append(out, left+line)
	}
	if caption != "" {
		if room := width - pad - 2; room > 1 && len([]rune(caption)) > room {
			caption = string([]rune(caption)[:room-1]) + "…"
		}
		out = append(out, "", left+"  "+styMuted.Render(caption))
	}
	return out
}
