package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

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
	// colStalled is orange: next to the green of working and the blue of idle it
	// reads as "not moving" without shouting like the red of a dead agent.
	colStalled = lipgloss.AdaptiveColor{Light: "#c2410c", Dark: "#ff9e64"}
	colDead    = lipgloss.AdaptiveColor{Light: "#b42318", Dark: "#ff7b72"}
	colMsg     = lipgloss.AdaptiveColor{Light: "#7a3ba8", Dark: "#d2a8ff"}

	styHeader = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styMuted  = lipgloss.NewStyle().Foreground(colMuted)
	styBase   = lipgloss.NewStyle().Foreground(colBase)
	styKey    = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	stySelect = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styErr    = lipgloss.NewStyle().Foreground(colDead)
	styMsg    = lipgloss.NewStyle().Foreground(colMsg)
	styAttn   = lipgloss.NewStyle().Foreground(colAttention)
	styAccent = lipgloss.NewStyle().Foreground(colAccent)
)

// stateColor picks the colour that says "this agent needs you" at a glance.
func stateColor(in agent.Info) lipgloss.TerminalColor {
	if in.Attention != "" {
		return colAttention
	}
	// Stalled before idle: an agent that owes something and has gone quiet is
	// the one case where "idle" reads as "fine" and is not.
	if in.Stalled {
		return colStalled
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

// stateLabel is what the pane header calls the agent. Stalled is not a state
// the agent knows it is in — the bus decides that — so it is said here rather
// than carried in Info.State, where it would change what "idle" means for the
// delivery paths that key on it.
func stateLabel(in agent.Info) string {
	if in.Stalled {
		return "stalled"
	}
	return string(in.State)
}

// stateGlyph is the one-character state badge used in the agent list.
func stateGlyph(in agent.Info) string {
	if in.Attention != "" {
		return "▲"
	}
	if in.Stalled {
		// The same dot as the others: the colour carries the difference, and a
		// glyph nobody has a font for carries nothing at all.
		return "●"
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

// fitLines truncates pre-rendered lines to the pane width and pads the block to
// its height. Every line is terminated with a reset, otherwise a background
// colour on the last visible cell bleeds across the pane.
func fitLines(lines []string, width, height int) []string {
	out := make([]string, 0, height)
	for _, l := range lines {
		if len(out) == height {
			break
		}
		out = append(out, ansi.Truncate(l, width, "")+"\x1b[0m")
	}
	for len(out) < height {
		out = append(out, "")
	}
	return out
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

// A delivered message shows for as long as it takes the hub to hand it over,
// which in push mode is a frame or two — you catch it going past rather than
// see it. Instead of vanishing between two renders, the envelope now dims
// toward the background over half a second, so the eye has something to follow.
const (
	msgFadeFor   = 500 * time.Millisecond
	msgFadeSteps = 12
)

// msgFadeStyles is colMsg dimmed step by step. Each step stays an AdaptiveColor
// so lipgloss still picks the variant matching the terminal at render time: the
// light one fades to white, the dark one to black, and swarm never has to ask
// what the background actually is.
var msgFadeStyles = fadeStyles(colMsg, msgFadeSteps)

// arriveFor is longer than the envelope's own fade: an arrival is drawn as a
// small movement toward the agent, and half a second is not enough to read a
// movement in.
const arriveFor = 800 * time.Millisecond

// accentFadeStyles is the same ramp in the accent colour, for what an agent
// sends. Sending and receiving are the two halves of one exchange, and telling
// them apart at a glance is the whole reason to draw either.
var accentFadeStyles = fadeStyles(colAccent, msgFadeSteps)

func fadeStyle(styles []lipgloss.Style, age, over time.Duration) (lipgloss.Style, bool) {
	if age < 0 || age >= over {
		return lipgloss.Style{}, false
	}
	i := int(float64(len(styles)) * float64(age) / float64(over))
	if i >= len(styles) {
		i = len(styles) - 1
	}
	return styles[i], true
}

// arriving is the badge for a message reaching an agent: chevrons pointing at
// the name, closing into an envelope. The badge sits to the right of the name,
// so a mark that points left is one coming in — which is the whole difference
// between this and what an agent sends.
func arriving(age time.Duration) (string, bool) {
	style, ok := fadeStyle(msgFadeStyles, age, arriveFor)
	if !ok {
		return "", false
	}
	switch t := float64(age) / float64(arriveFor); {
	case t < 0.22:
		return styMsg.Render(" ‹‹"), true
	case t < 0.4:
		return styMsg.Render(" ‹"), true
	default:
		return style.Render(" ✉"), true
	}
}

// leaving is the same thing played the other way: the envelope first, then the
// chevrons carrying it off. The count comes with it, because a broadcast is one
// command and nine messages, and nine flashes on one line read as a tremble
// rather than as nine.
func leaving(age time.Duration, n int) (string, bool) {
	style, ok := fadeStyle(accentFadeStyles, age, arriveFor)
	if !ok {
		return "", false
	}
	many := ""
	if n > 1 {
		many = fmt.Sprint(n)
	}
	switch t := float64(age) / float64(arriveFor); {
	case t < 0.3:
		return styAccent.Render(" ✉" + many), true
	case t < 0.5:
		return styAccent.Render(" ››" + many), true
	default:
		return style.Render(" ›" + many), true
	}
}

// nameFlash is the colour an agent's name takes while something is happening
// to it, sliding back to what it was. The name is the widest thing on the row,
// so it is what reads across a list of eleven.
func nameFlash(from lipgloss.AdaptiveColor, to lipgloss.TerminalColor, age, over time.Duration) (lipgloss.TerminalColor, bool) {
	if age < 0 || age >= over {
		return to, false
	}
	t := float64(age) / float64(over)
	base, ok := to.(lipgloss.AdaptiveColor)
	if !ok {
		base = colBase
	}
	return lipgloss.AdaptiveColor{
		Light: mixHex(from.Light, base.Light, t),
		Dark:  mixHex(from.Dark, base.Dark, t),
	}, true
}

// breatheFor is one turn of the pulse on a stalled agent. Slow: the list is
// looked at all day, and anything quicker is a flicker rather than a signal.
const breatheFor = 2 * time.Second

// breathing is the stalled colour rising and falling. Stalled is a state, not
// an event — it lasts as long as the agent owes something and says nothing —
// so this does not fade out, it keeps going until the state does.
func breathing(now time.Time) lipgloss.TerminalColor {
	ms := now.UnixMilli() % int64(breatheFor/time.Millisecond)
	if ms < 0 {
		ms += int64(breatheFor / time.Millisecond)
	}
	t := (math.Sin(2*math.Pi*float64(ms)/float64(breatheFor/time.Millisecond)) + 1) / 2
	return lipgloss.AdaptiveColor{
		Light: mixHex(mixHex(colStalled.Light, "#ffffff", 0.55), colStalled.Light, t),
		Dark:  mixHex(mixHex(colStalled.Dark, "#000000", 0.55), colStalled.Dark, t),
	}
}

func fadeStyles(c lipgloss.AdaptiveColor, steps int) []lipgloss.Style {
	out := make([]lipgloss.Style, steps)
	for i := range steps {
		t := float64(i) / float64(steps-1)
		out[i] = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
			Light: mixHex(c.Light, "#ffffff", t),
			Dark:  mixHex(c.Dark, "#000000", t),
		})
	}
	return out
}

// msgFadeStyle returns how to draw the envelope at a given age, and whether it
// is still worth drawing at all.
func msgFadeStyle(age time.Duration) (lipgloss.Style, bool) {
	if age < 0 || age >= msgFadeFor {
		return lipgloss.Style{}, false
	}
	i := int(float64(msgFadeSteps) * float64(age) / float64(msgFadeFor))
	if i >= msgFadeSteps {
		i = msgFadeSteps - 1
	}
	return msgFadeStyles[i], true
}

// mixHex blends two "#rrggbb" colours, t running from a to b.
func mixHex(a, b string, t float64) string {
	ar, ag, ab := parseHex(a)
	br, bg, bb := parseHex(b)
	// math.Round, not a +0.5 truncation: the latter rounds the wrong way for a
	// negative delta, and the fade would stop one unit short of the background.
	lerp := func(x, y int) int { return x + int(math.Round(float64(y-x)*t)) }
	return fmt.Sprintf("#%02x%02x%02x", lerp(ar, br), lerp(ag, bg), lerp(ab, bb))
}

func parseHex(s string) (r, g, b int) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return 0, 0, 0
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0
	}
	return int(v>>16) & 0xff, int(v>>8) & 0xff, int(v) & 0xff
}
