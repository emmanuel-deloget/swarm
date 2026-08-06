package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/emmanuel-deloget/swarm/internal/agent"
)

var (
	colBase      = lipgloss.AdaptiveColor{Light: "#3c3c43", Dark: "#c8ccd4"}
	colMuted     = lipgloss.AdaptiveColor{Light: "#8a8f98", Dark: "#7d8590"}
	colAccent    = lipgloss.AdaptiveColor{Light: "#0a58ca", Dark: "#7aa2f7"}
	colWorking   = lipgloss.AdaptiveColor{Light: "#0f7b3e", Dark: "#7ee787"}
	colIdle      = lipgloss.AdaptiveColor{Light: "#1f6f8b", Dark: "#79c0ff"}
	colAttention = lipgloss.AdaptiveColor{Light: "#a35b00", Dark: "#e3b341"}
	colDead      = lipgloss.AdaptiveColor{Light: "#b42318", Dark: "#ff7b72"}
	colMsg       = lipgloss.AdaptiveColor{Light: "#7a3ba8", Dark: "#d2a8ff"}

	styHeader = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styMuted  = lipgloss.NewStyle().Foreground(colMuted)
	styBase   = lipgloss.NewStyle().Foreground(colBase)
	styKey    = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	stySelect = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styErr    = lipgloss.NewStyle().Foreground(colDead)
	styMsg    = lipgloss.NewStyle().Foreground(colMsg)
	styAttn   = lipgloss.NewStyle().Foreground(colAttention)
)

// stateColor picks the colour that says "this agent needs you" at a glance.
func stateColor(in agent.Info) lipgloss.TerminalColor {
	if in.Attention != "" {
		return colAttention
	}
	switch in.State {
	case agent.StateWorking:
		return colWorking
	case agent.StateIdle:
		return colIdle
	case agent.StateStarting:
		return colAccent
	case agent.StateExited:
		return colDead
	default:
		return colMuted
	}
}

// stateGlyph is the one-character state badge used in the agent list.
func stateGlyph(in agent.Info) string {
	if in.Attention != "" {
		return "▲"
	}
	switch in.State {
	case agent.StateWorking:
		return "●"
	case agent.StateIdle:
		return "○"
	case agent.StateStarting:
		return "◐"
	case agent.StateExited:
		return "✖"
	default:
		return "·"
	}
}

// cropScreen fits an agent's rendered screen into a pane of the given size.
//
// The screen is already full of escape sequences, so every line is truncated
// with an ANSI-aware helper and terminated with a reset: without that, a
// background colour set on the last visible cell bleeds across the pane.
func cropScreen(screen string, width, height, offset int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(screen, "\r\n", "\n"), "\n")

	// Trailing blank lines carry no information; dropping them keeps the
	// interesting part (the prompt) at the bottom of the pane.
	end := len(lines)
	for end > 0 && strings.TrimSpace(ansi.Strip(lines[end-1])) == "" {
		end--
	}
	lines = lines[:end]

	if offset < 0 {
		offset = 0
	}
	// offset counts lines scrolled up from the bottom.
	last := len(lines) - offset
	if last < 1 {
		last = 1
		if len(lines) == 0 {
			last = 0
		}
	}
	first := last - height
	if first < 0 {
		first = 0
	}
	if last > len(lines) {
		last = len(lines)
	}
	view := lines[first:last]

	out := make([]string, 0, height)
	for _, l := range view {
		out = append(out, ansi.Truncate(l, width, "")+"\x1b[0m")
	}
	for len(out) < height {
		out = append(out, "")
	}
	return out
}

// flattenLines collapses a multi-line message onto one line. Event texts carry
// things like an agent's argv, which contains newlines and would otherwise
// shift everything below them.
func flattenLines(s string) string {
	if !strings.ContainsAny(s, "\r\n\t") {
		return s
	}
	r := strings.NewReplacer("\r\n", " ", "\n", " ⏎ ", "\r", " ", "\t", " ")
	return strings.Join(strings.Fields(r.Replace(s)), " ")
}

// padRight pads a possibly styled line to an exact display width.
func padRight(s string, width int) string {
	w := ansi.StringWidth(s)
	if w >= width {
		return ansi.Truncate(s, width, "")
	}
	return s + strings.Repeat(" ", width-w)
}

// block turns lines into a fixed-size rectangle.
func block(lines []string, width, height int) []string {
	out := make([]string, 0, height)
	for i := range height {
		if i < len(lines) {
			out = append(out, padRight(lines[i], width))
		} else {
			out = append(out, strings.Repeat(" ", width))
		}
	}
	return out
}

// joinColumns places blocks of equal height side by side with a separator.
func joinColumns(sep string, cols ...[]string) []string {
	height := 0
	for _, c := range cols {
		if len(c) > height {
			height = len(c)
		}
	}
	out := make([]string, height)
	for i := range height {
		var b strings.Builder
		for j, c := range cols {
			if j > 0 {
				b.WriteString(sep)
			}
			if i < len(c) {
				b.WriteString(c[i])
			}
		}
		out[i] = b.String()
	}
	return out
}
