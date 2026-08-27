package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/emmanuel-deloget/swarm/internal/bus"
)

// The fleet as a shape.
//
// can_send is written one agent at a time and read one agent at a time, so a
// topology nothing like the one its author had in mind stays invisible: five
// reviewers and five developers meant as a star turn out to be a near-complete
// bipartite graph, and nobody notices until the messages arrive. A matrix says
// it in one screen — who may write to whom underneath, and who actually did on
// top.
//
// A matrix rather than a graph: a graph drawn in characters stops being
// readable at six nodes, and this is for fleets that have more than that.

const (
	glyphSelf    = "·" // an agent's own column
	glyphBarred  = "✗" // can_send says no
	glyphSilent  = "·" // allowed, and nothing said
	matrixShades = "░▒▓█"
)

// matrixCell is what one pair looks like: barred, silent, or a shade of how
// much was said.
func matrixCell(allowed bool, sent, busiest int) string {
	switch {
	case !allowed:
		return styMuted.Render(glyphBarred)
	case sent == 0:
		return styMuted.Render(glyphSilent)
	}
	shades := []rune(matrixShades)
	i := 0
	if busiest > 1 {
		i = (sent - 1) * (len(shades) - 1) / (busiest - 1)
	}
	if i >= len(shades) {
		i = len(shades) - 1
	}
	style := styBase
	if i >= len(shades)-1 {
		style = styAttn
	}
	return style.Render(string(shades[i]))
}

// shortName is a column heading: two characters, because a matrix of eleven
// agents has to fit a terminal and their names do not.
func shortName(name string, taken map[string]bool) string {
	runes := []rune(name)
	for n := 2; n <= len(runes); n++ {
		try := string(runes[:n])
		if !taken[try] {
			taken[try] = true
			return try
		}
	}
	taken[name] = true
	return name
}

// matrixLines draws the whole thing: rows are senders, columns recipients.
func matrixLines(names []string, reach map[string][]string, stats bus.Stats, width int) []string {
	if len(names) == 0 {
		return []string{styMuted.Render("no agents")}
	}
	sent := make(map[string]int, len(stats.Pairs))
	busiest := 0
	for _, p := range stats.Pairs {
		sent[p.From+"\x00"+p.To] = p.Count
		if p.Count > busiest {
			busiest = p.Count
		}
	}
	allowed := make(map[string]bool)
	for from, tos := range reach {
		for _, to := range tos {
			allowed[from+"\x00"+to] = true
		}
	}

	taken := map[string]bool{}
	short := make([]string, len(names))
	for i, n := range names {
		short[i] = shortName(n, taken)
	}

	// The widest name we can afford, so the grid still fits.
	label := 0
	for _, n := range names {
		if len(n) > label {
			label = len(n)
		}
	}
	if room := width - 4*len(names) - 4; label > room && room > 3 {
		label = room
	}

	// One column width for all of them: the abbreviations are not all the same
	// length, and a header that assumes they are walks out of line with the
	// grid under it.
	cw := 3
	for _, s := range short {
		if len([]rune(s))+1 > cw {
			cw = len([]rune(s)) + 1
		}
	}
	head := strings.Repeat(" ", label+2)
	for _, s := range short {
		head += styMuted.Render(fmt.Sprintf("%-*s", cw, s))
	}
	out := []string{styMuted.Render("  to →"), head}

	for i, from := range names {
		name := from
		if len(name) > label {
			name = name[:label-1] + "…"
		}
		row := lipgloss.NewStyle().Render(fmt.Sprintf("%-*s", label, name)) + "  "
		for j, to := range names {
			cell := styMuted.Render(glyphSelf)
			if i != j {
				cell = matrixCell(allowed[from+"\x00"+to], sent[from+"\x00"+to], busiest)
			}
			row += cell + strings.Repeat(" ", cw-1)
		}
		out = append(out, row)
	}
	out = append(out, "", styMuted.Render(
		"  ✗ can_send says no   · allowed, silent   ░▒▓█ what was said"))
	return out
}
