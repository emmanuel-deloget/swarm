// Package ui is the terminal interface: one place to watch every agent, jump
// into one, and drive the fleet without leaving the keyboard.
package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/emmanuel-deloget/swarm/internal/agent"
	"github.com/emmanuel-deloget/swarm/internal/event"
	"github.com/emmanuel-deloget/swarm/internal/hub"
)

// Run shows the interface and returns when the user quits, or when quit is
// closed (a signal, or `swarm shutdown`).
func Run(h *hub.Hub, quit <-chan struct{}) error {
	events, cancel := h.Log().Subscribe(256)
	defer cancel()

	m := newModel(h, events, quit)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// A shutdown asked for from elsewhere must also close the UI.
	go func() {
		<-quit
		p.Quit()
	}()

	_, err := p.Run()
	return err
}

type mode int

const (
	modeNormal mode = iota
	modeAttached
	modeCommand
	modeMosaic
	modeHelp
)

const sidebarWidth = 24

type model struct {
	h      *hub.Hub
	events <-chan event.Event
	quit   <-chan struct{}

	width, height int
	mode          mode
	returnTo      mode

	infos   []agent.Info
	sel     int
	offset  int // lines scrolled up in the terminal pane
	showLog bool
	log     []event.Event

	input   textinput.Model
	status  string
	isError bool
	confirm string // pending confirmation prompt

	// visibleLines is the rendered window into the selected agent's output, and
	// maxOffset how far back its scrollback still goes. Both come from the last
	// refresh, so scrolling can be bounded by what actually exists.
	visibleLines []string
	maxOffset    int
}

func newModel(h *hub.Hub, events <-chan event.Event, quit <-chan struct{}) *model {
	in := textinput.New()
	in.Prompt = ":"
	in.CharLimit = 4096
	in.Width = 60

	return &model{
		h:       h,
		events:  events,
		quit:    quit,
		mode:    modeNormal,
		infos:   h.Infos(),
		showLog: true,
		log:     h.Log().History(200),
		input:   in,
		status:  "press ? for help",
	}
}

type tickMsg time.Time
type eventMsg event.Event
type resultMsg struct {
	text  string
	isErr bool
}

func tick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) waitEvent() tea.Cmd {
	return func() tea.Msg {
		e, ok := <-m.events
		if !ok {
			return nil
		}
		return eventMsg(e)
	}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(tick(), m.waitEvent())
}

func (m *model) current() *agent.Info {
	if len(m.infos) == 0 {
		return nil
	}
	if m.sel >= len(m.infos) {
		m.sel = len(m.infos) - 1
	}
	if m.sel < 0 {
		m.sel = 0
	}
	return &m.infos[m.sel]
}

func (m *model) currentName() string {
	if in := m.current(); in != nil {
		return in.Name
	}
	return ""
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		m.infos = m.h.Infos()
		m.fitSelected()
		m.refreshScreen()
		return m, tick()

	case eventMsg:
		m.log = append(m.log, event.Event(msg))
		if len(m.log) > 500 {
			m.log = m.log[len(m.log)-500:]
		}
		return m, m.waitEvent()

	case resultMsg:
		m.status, m.isError = msg.text, msg.isErr
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) refreshScreen() {
	m.visibleLines, m.maxOffset = nil, 0

	name := m.currentName()
	if name == "" {
		return
	}
	a, err := m.h.Agent(name)
	if err != nil {
		return
	}
	_, paneHeight, _ := m.paneGeometry()
	rows := m.screenRows(paneHeight)
	lines, maxOffset := a.RenderWindow(m.offset, rows)
	m.visibleLines, m.maxOffset = lines, maxOffset
	// The scrollback shrinks when an agent restarts, so an offset kept from
	// before must not survive past the top of what is left.
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
}

func (m *model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scroll(3)
	case tea.MouseButtonWheelDown:
		m.scroll(-3)
	case tea.MouseButtonLeft:
		// Clicking the sidebar selects an agent.
		if msg.X < sidebarWidth && msg.Y >= 2 {
			if i := msg.Y - 2; i < len(m.infos) {
				m.sel = i
				m.offset = 0
				m.refreshScreen()
			}
		}
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A pending confirmation swallows everything else.
	if m.confirm != "" {
		switch msg.String() {
		case "y", "Y":
			m.confirm = ""
			return m, tea.Quit
		default:
			m.confirm = ""
			m.status = "cancelled"
			return m, nil
		}
	}

	switch m.mode {
	case modeCommand:
		return m.handleCommandKey(msg)
	case modeAttached:
		return m.handleAttachedKey(msg)
	case modeHelp:
		m.mode = m.returnTo
		return m, nil
	}
	return m.handleNormalKey(msg)
}

func (m *model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.confirm = "quit swarm and stop every agent? (y/n)"
		return m, nil

	case "j", "down":
		m.sel++
		m.clampSel()
		return m, nil
	case "k", "up":
		m.sel--
		m.clampSel()
		return m, nil
	case "tab":
		m.sel++
		if m.sel >= len(m.infos) {
			m.sel = 0
		}
		m.offset = 0
		m.refreshScreen()
		return m, nil
	case "shift+tab":
		m.sel--
		if m.sel < 0 {
			m.sel = len(m.infos) - 1
		}
		m.offset = 0
		m.refreshScreen()
		return m, nil
	case "g", "home":
		m.sel = 0
		m.clampSel()
		return m, nil
	case "G", "end":
		m.sel = len(m.infos) - 1
		m.clampSel()
		return m, nil

	case "pgup":
		m.scroll(10)
		return m, nil
	case "pgdown":
		m.scroll(-10)
		return m, nil

	case "enter":
		if in := m.current(); in != nil && in.State != agent.StateExited && in.State != agent.StateStopped {
			m.mode = modeAttached
			m.offset = 0
			m.status = fmt.Sprintf("attached to %s — ctrl+\\ to come back", in.Name)
		} else {
			m.status, m.isError = "that agent is not running", true
		}
		return m, nil

	case "A":
		return m, m.fullScreenAttach()

	case "m":
		if m.mode == modeMosaic {
			m.mode = modeNormal
		} else {
			m.mode = modeMosaic
		}
		return m, nil

	case "l":
		m.showLog = !m.showLog
		return m, nil

	case "?":
		m.returnTo = m.mode
		m.mode = modeHelp
		return m, nil

	case ":":
		m.openCommand("")
		return m, nil
	case "i":
		m.openCommand("inject " + m.currentName() + " ")
		return m, nil
	case "s":
		m.openCommand("send " + m.currentName() + " ")
		return m, nil
	case "b":
		m.openCommand("broadcast ")
		return m, nil
	case "f":
		m.openCommand("file " + m.currentName() + " ")
		return m, nil
	case "K":
		m.openCommand("keys " + m.currentName() + " ")
		return m, nil

	case "r":
		return m, m.lifecycle("restart", m.currentName())
	case "x":
		return m, m.lifecycle("stop", m.currentName())
	case "S":
		return m, m.lifecycle("start", m.currentName())
	}

	// Number keys jump straight to an agent.
	if len(msg.String()) == 1 && msg.String()[0] >= '1' && msg.String()[0] <= '9' {
		if i := int(msg.String()[0] - '1'); i < len(m.infos) {
			m.sel = i
			m.offset = 0
			m.refreshScreen()
		}
	}
	return m, nil
}

// scroll moves the pane through the agent's history, stopping at the start of
// the session rather than drifting into blank space.
func (m *model) scroll(by int) {
	m.offset += by
	if m.offset < 0 {
		m.offset = 0
	}
	if m.offset > m.maxOffset {
		m.offset = m.maxOffset
	}
	m.refreshScreen()
}

func (m *model) clampSel() {
	if len(m.infos) == 0 {
		m.sel = 0
		return
	}
	if m.sel < 0 {
		m.sel = 0
	}
	if m.sel >= len(m.infos) {
		m.sel = len(m.infos) - 1
	}
	m.offset = 0
	m.refreshScreen()
}

// handleAttachedKey forwards keystrokes to the selected agent. ctrl+\ leaves
// the mode, which is the same key the standalone `swarm attach` uses.
func (m *model) handleAttachedKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlBackslash {
		m.mode = modeNormal
		m.status = "detached"
		return m, nil
	}
	data := keyBytes(msg)
	if len(data) == 0 {
		return m, nil
	}
	name := m.currentName()
	a, err := m.h.Agent(name)
	if err != nil {
		m.mode = modeNormal
		return m, nil
	}
	if err := a.WriteRaw(data); err != nil {
		m.status, m.isError = err.Error(), true
		m.mode = modeNormal
	}
	return m, nil
}

func (m *model) openCommand(prefill string) {
	m.returnTo = m.mode
	m.mode = modeCommand
	m.input.SetValue(prefill)
	m.input.CursorEnd()
	m.input.Focus()
}

func (m *model) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.mode = m.returnTo
		m.input.Blur()
		m.input.SetValue("")
		return m, nil
	case tea.KeyEnter:
		line := strings.TrimSpace(m.input.Value())
		m.input.SetValue("")
		m.input.Blur()
		m.mode = m.returnTo
		return m, m.runCommand(line)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// fullScreenAttach suspends the UI and runs `swarm attach` for real, which
// gives the agent the whole window and a byte-perfect keyboard.
func (m *model) fullScreenAttach() tea.Cmd {
	name := m.currentName()
	if name == "" {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return func() tea.Msg { return resultMsg{err.Error(), true} }
	}
	c := exec.Command(exe, "attach", "-socket", m.h.SocketPath(), name)
	c.Env = append(os.Environ(), "SWARM_SOCKET="+m.h.SocketPath())
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return resultMsg{"attach: " + err.Error(), true}
		}
		return resultMsg{"back from " + name, false}
	})
}

func (m *model) View() string {
	if m.width == 0 {
		return "starting..."
	}
	switch m.mode {
	case modeHelp:
		return m.viewHelp()
	case modeMosaic:
		return m.viewMosaic()
	default:
		return m.viewMain()
	}
}

// usable is the width the UI draws into. The last column is left alone: a
// character written there makes the terminal wrap, which pushes the header off
// the top of the screen.
func (m *model) usable() int {
	if m.width < 12 {
		return 12
	}
	return m.width - 1
}

// paneGeometry is the size of the terminal pane: the window minus the sidebar,
// the header, the status line and the event log. The agent shown there is
// resized to it, so its own layout adapts instead of being cropped.
func (m *model) paneGeometry() (width, height, logHeight int) {
	if m.showLog {
		logHeight = 7
	}
	height = m.height - 2 /*header*/ - 1 /*status*/ - logHeight
	if height < 3 {
		height = 3
	}
	width = m.usable() - sidebarWidth - 1
	if width < 10 {
		width = 10
	}
	return width, height, logHeight
}

// screenRows is the part of the pane the agent's own screen occupies, below the
// pane's title and separator.
func (m *model) screenRows(paneHeight int) int {
	rows := paneHeight - 2
	if rows < 3 {
		rows = 3
	}
	return rows
}

// fitSelected keeps the displayed agent the size of the space it is shown in.
// It is a no-op once the sizes match, so it costs nothing on a steady window.
func (m *model) fitSelected() {
	if m.mode == modeMosaic || m.width == 0 {
		return
	}
	in := m.current()
	if in == nil || in.Pid == 0 {
		return
	}
	a, err := m.h.Agent(in.Name)
	if err != nil || !a.Config().FollowsWindow() {
		return
	}
	paneW, paneH, _ := m.paneGeometry()
	rows := m.screenRows(paneH)
	if in.Cols == paneW && in.Rows == rows {
		return
	}
	if err := a.Resize(paneW, rows); err != nil {
		m.status, m.isError = err.Error(), true
	}
}

func (m *model) viewMain() string {
	paneWidth, bodyHeight, logHeight := m.paneGeometry()

	side := block(m.sidebarLines(bodyHeight), sidebarWidth, bodyHeight)
	pane := block(m.paneLines(paneWidth, bodyHeight), paneWidth, bodyHeight)
	body := joinColumns(styMuted.Render("│"), side, pane)

	out := []string{m.headerLine(), m.tabLine()}
	out = append(out, body...)
	if m.showLog {
		out = append(out, styMuted.Render(strings.Repeat("─", m.usable())))
		out = append(out, m.logLines(logHeight-1)...)
	}
	out = append(out, m.statusLine())
	return strings.Join(out, "\n")
}

func (m *model) headerLine() string {
	cfg := m.h.Config()
	var working, idle, attention, dead, unread int
	for _, in := range m.infos {
		switch {
		case in.Attention != "":
			attention++
		case in.State == agent.StateWorking:
			working++
		case in.State == agent.StateIdle:
			idle++
		case in.State == agent.StateExited, in.State == agent.StateStopped:
			dead++
		}
		unread += in.Unread
	}
	parts := []string{
		styHeader.Render("swarm"),
		styMuted.Render(cfg.Session),
		fmt.Sprintf("%d agents", len(m.infos)),
	}
	if working > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colWorking).Render(fmt.Sprintf("%d working", working)))
	}
	if idle > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colIdle).Render(fmt.Sprintf("%d idle", idle)))
	}
	if attention > 0 {
		parts = append(parts, styAttn.Render(fmt.Sprintf("%d need you", attention)))
	}
	if dead > 0 {
		parts = append(parts, styErr.Render(fmt.Sprintf("%d down", dead)))
	}
	if unread > 0 {
		parts = append(parts, styMsg.Render(fmt.Sprintf("%d unread", unread)))
	}
	left := strings.Join(parts, styMuted.Render(" · "))

	right := ""
	if url, tok := m.h.WebURL(); url != "" {
		right = styMuted.Render(url + "?t=" + tok)
	}
	return padRight(left, m.usable()-lipgloss.Width(right)) + right
}

func (m *model) tabLine() string {
	return styMuted.Render(strings.Repeat("─", m.usable()))
}

func (m *model) sidebarLines(height int) []string {
	lines := make([]string, 0, len(m.infos))
	// Keep the selection visible when there are more agents than rows.
	start := 0
	if len(m.infos) > height {
		start = m.sel - height/2
		if start < 0 {
			start = 0
		}
		if start > len(m.infos)-height {
			start = len(m.infos) - height
		}
	}
	for i := start; i < len(m.infos) && len(lines) < height; i++ {
		in := m.infos[i]
		glyph := lipgloss.NewStyle().Foreground(stateColor(in)).Render(stateGlyph(in))
		name := in.Name
		badge := ""
		if in.Unread > 0 {
			badge = styMsg.Render(fmt.Sprintf(" %d✉", in.Unread))
		}
		prefix := "  "
		style := styBase
		if i == m.sel {
			prefix = stySelect.Render("▌ ")
			style = stySelect
		}
		room := sidebarWidth - 4 - lipgloss.Width(badge)
		if len(name) > room && room > 1 {
			name = name[:room-1] + "…"
		}
		lines = append(lines, prefix+glyph+" "+style.Render(name)+badge)
	}
	return lines
}

func (m *model) paneLines(width, height int) []string {
	in := m.current()
	if in == nil {
		return []string{styMuted.Render("no agent")}
	}

	title := fmt.Sprintf("%s  %s", stySelect.Render(in.Name), lipgloss.NewStyle().Foreground(stateColor(*in)).Render(string(in.State)))
	if in.Attention != "" {
		title += styAttn.Render(" ▲ " + in.Attention)
	}
	meta := []string{}
	if in.Pid > 0 {
		meta = append(meta, fmt.Sprintf("pid %d", in.Pid))
	}
	if in.Uptime > 0 {
		meta = append(meta, "up "+in.Uptime.String())
	}
	if in.Quiet > 3*time.Second {
		meta = append(meta, "quiet "+in.Quiet.String())
	}
	if in.Exit != "" {
		meta = append(meta, in.Exit)
	}
	if in.Restarts > 0 {
		meta = append(meta, fmt.Sprintf("%d restarts", in.Restarts))
	}
	if m.offset > 0 {
		meta = append(meta, fmt.Sprintf("scrolled %d/%d", m.offset, m.maxOffset))
	}
	if m.mode == modeAttached {
		meta = append(meta, styAttn.Render("ATTACHED"))
	}
	header := padRight(title, width-lipgloss.Width(styMuted.Render(strings.Join(meta, " · ")))) +
		styMuted.Render(strings.Join(meta, " · "))

	out := []string{header, styMuted.Render(strings.Repeat("╌", width))}
	screenHeight := height - len(out)
	if screenHeight < 1 {
		return out
	}
	if len(m.visibleLines) == 0 {
		msg := "not running — press S to start it"
		if in.State == agent.StateStarting {
			msg = "starting..."
		}
		out = append(out, "", styMuted.Render("  "+msg))
		return out
	}
	out = append(out, fitLines(m.visibleLines, width, screenHeight)...)
	return out
}

func (m *model) logLines(height int) []string {
	if height < 1 {
		return nil
	}
	start := len(m.log) - height
	if start < 0 {
		start = 0
	}
	out := make([]string, 0, height)
	for _, e := range m.log[start:] {
		out = append(out, m.formatEvent(e, m.usable()))
	}
	for len(out) < height {
		out = append(out, "")
	}
	return out
}

func (m *model) formatEvent(e event.Event, width int) string {
	style := styMuted
	switch e.Severity() {
	case 2:
		style = styErr
	case 1:
		style = styAttn
	}
	who := e.Agent
	if who == "" {
		who = "swarm"
	}
	line := fmt.Sprintf("%s %s %s %s",
		styMuted.Render(e.At.Format("15:04:05")),
		style.Render(fmt.Sprintf("%-8s", e.Kind)),
		padRight(styBase.Render(who), 14),
		flattenLines(e.Text))
	return padRight(line, width)
}

func (m *model) statusLine() string {
	if m.confirm != "" {
		return padRight(styAttn.Render(m.confirm), m.usable())
	}
	if m.mode == modeCommand {
		return m.input.View()
	}
	hints := []string{
		styKey.Render("↵") + " attach",
		styKey.Render("A") + " full",
		styKey.Render("i") + " inject",
		styKey.Render("s") + " send",
		styKey.Render("m") + " mosaic",
		styKey.Render(":") + " cmd",
		styKey.Render("?") + " help",
	}
	if m.mode == modeAttached {
		hints = []string{styAttn.Render("ATTACHED") + styMuted.Render(" — keys go to the agent, ") + styKey.Render("ctrl+\\") + styMuted.Render(" to detach")}
	}
	left := strings.Join(hints, styMuted.Render(" · "))
	status := m.status
	if m.isError && status != "" {
		status = styErr.Render(status)
	} else if status != "" {
		status = styMuted.Render(status)
	}
	if lipgloss.Width(left)+lipgloss.Width(status)+2 > m.usable() {
		return padRight(status, m.usable())
	}
	return padRight(left, m.usable()-lipgloss.Width(status)) + status
}

func (m *model) viewMosaic() string {
	// A wall of small previews: enough to see who is stuck without cycling.
	n := len(m.infos)
	if n == 0 {
		return "no agent"
	}
	cols := 1
	for cols*cols < n {
		cols++
	}
	if cols > 4 {
		cols = 4
	}
	rows := (n + cols - 1) / cols

	bodyHeight := m.height - 3
	cellW := (m.usable() - (cols - 1)) / cols
	cellH := bodyHeight / rows
	if cellW < 12 || cellH < 3 {
		return m.viewMain()
	}

	out := []string{m.headerLine(), m.tabLine()}
	for r := range rows {
		var cells [][]string
		for c := range cols {
			i := r*cols + c
			if i >= n {
				cells = append(cells, block(nil, cellW, cellH))
				continue
			}
			cells = append(cells, m.mosaicCell(i, cellW, cellH))
		}
		out = append(out, joinColumns(styMuted.Render("│"), cells...)...)
	}
	for len(out) < m.height-1 {
		out = append(out, "")
	}
	out = append(out, m.statusLine())
	return strings.Join(out[:m.height], "\n")
}

func (m *model) mosaicCell(i, width, height int) []string {
	in := m.infos[i]
	marker := "  "
	if i == m.sel {
		marker = stySelect.Render("▌ ")
	}
	glyph := lipgloss.NewStyle().Foreground(stateColor(in)).Render(stateGlyph(in))
	title := marker + glyph + " " + styBase.Render(in.Name)
	if in.Attention != "" {
		title += styAttn.Render(" ▲")
	}
	if in.Unread > 0 {
		title += styMsg.Render(fmt.Sprintf(" %d✉", in.Unread))
	}
	lines := []string{padRight(title, width)}

	var screen string
	if a, err := m.h.Agent(in.Name); err == nil {
		screen = a.Render()
	}
	if screen == "" {
		lines = append(lines, styMuted.Render("  (not running)"))
	} else {
		lines = append(lines, cropScreen(screen, width, height-1, 0)...)
	}
	return block(lines, width, height)
}

func (m *model) viewHelp() string {
	rows := [][2]string{
		{"j / k / ↑ / ↓", "select an agent"},
		{"tab / shift+tab", "cycle through agents"},
		{"1..9", "jump to an agent"},
		{"↵", "attach: your keys go to that agent (ctrl+\\ to come back)"},
		{"A", "attach full screen, with a byte-perfect keyboard"},
		{"pgup / pgdn", "scroll the agent's screen"},
		{"m", "mosaic: every agent at once"},
		{"l", "show or hide the event log"},
		{"i", "inject text into the selected agent"},
		{"s", "send a bus message to the selected agent"},
		{"b", "broadcast a bus message"},
		{"f", "stage a file and inject its path (images included)"},
		{"K", "send key presses (esc, ctrl+c, up, ...)"},
		{"S / x / r", "start / stop / restart the selected agent"},
		{":", "command line"},
		{"q", "quit and stop every agent"},
	}
	cmds := [][2]string{
		{":inject <target> <text>", "type text and submit it"},
		{":type <target> <text>", "type text without submitting"},
		{":keys <target> <keys>", "send key presses"},
		{":send <target> <text>", "bus message (agents see it as coming from you)"},
		{":broadcast <text>", "bus message to everyone"},
		{":file <target> <path>", "stage a file and inject its path"},
		{":start|:stop|:restart <target>", "lifecycle"},
		{":web", "show the remote-control URL"},
		{":q", "quit"},
	}

	var b strings.Builder
	b.WriteString(styHeader.Render("swarm — keys") + "\n\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "  %s  %s\n", padRight(styKey.Render(r[0]), 20), styBase.Render(r[1]))
	}
	b.WriteString("\n" + styHeader.Render("commands") + "\n\n")
	for _, r := range cmds {
		fmt.Fprintf(&b, "  %s  %s\n", padRight(styKey.Render(r[0]), 32), styBase.Render(r[1]))
	}
	b.WriteString("\n" + styMuted.Render("A target is an agent name, @group, @role, all, or a comma-separated list.") + "\n")
	b.WriteString(styMuted.Render("press any key to go back"))
	return b.String()
}
