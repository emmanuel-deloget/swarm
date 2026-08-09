// Package ui is the terminal interface: one place to watch every agent, jump
// into one, and drive the fleet without leaving the keyboard.
package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/emmanuel-deloget/swarm/internal/agent"
	"github.com/emmanuel-deloget/swarm/internal/config"
	"github.com/emmanuel-deloget/swarm/internal/event"
	"github.com/emmanuel-deloget/swarm/internal/hub"
	"github.com/emmanuel-deloget/swarm/internal/version"
	"github.com/emmanuel-deloget/swarm/internal/vterm"
)

// Run shows the interface and returns when the user quits, or when quit is
// closed (a signal, or `swarm shutdown`).
func Run(h *hub.Hub, quit <-chan struct{}) error {
	events, cancel := h.Log().Subscribe(256)
	defer cancel()

	m := newModel(h, events, quit)
	// Mouse reporting is requested from Init when configured, not here: with it
	// on, the terminal stops handling text selection itself, so it has to be
	// something one can turn off.
	p := tea.NewProgram(m, tea.WithAltScreen())

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

// commandPrompt is what the command line shows when it is not searching.
const commandPrompt = ":"

// maxKeyName is the longest binding name a run of runes could be mistaken for
// ("backspace"), with room to spare.
const maxKeyName = 12

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

	// detachKey is the key name that leaves the attached mode, and detachSeq the
	// bytes it produces. Configurable because the default collides with tmux,
	// screen and asciinema.
	detachKey string
	detachSeq string

	// mouse tracks whether the terminal is reporting mouse events to us, which
	// is also what stops it from selecting text.
	mouse bool

	// completions holds the candidates of the current tab cycle, and completeAt
	// the line they were applied to — pressing tab again on an untouched line
	// moves to the next candidate rather than starting over.
	completions []string
	completeIdx int
	completeAt  string

	// hist is what was typed before, and verb the kind of line being edited —
	// the key the history is filtered by.
	hist *history
	verb string

	// prefs is what the TUI remembers between runs; escNext is set by the esc
	// prefix, which reaches a shortcut while the dialogue lock is on.
	prefs   *prefs
	escNext bool

	// delivered stamps when an agent last had a message handed over, so the
	// envelope can be faded out instead of disappearing between two frames.
	delivered map[string]time.Time

	// visibleLines is the rendered window into the selected agent's output, and
	// maxOffset how far back its scrollback still goes. Both come from the last
	// refresh, so scrolling can be bounded by what actually exists.
	visibleLines []string
	maxOffset    int
}

func newModel(h *hub.Hub, events <-chan event.Event, quit <-chan struct{}) *model {
	in := textinput.New()
	in.Prompt = commandPrompt
	in.CharLimit = 4096
	// Width is set from the window size; 60 is only what it starts at, before
	// the first WindowSizeMsg arrives.
	in.Width = 60

	key := h.Config().DetachKey
	seq, err := vterm.KeySequences(key)
	if err != nil {
		// The config validated this key, so this cannot normally happen; fall
		// back rather than leave the user with no way out of an attached pane.
		key, seq = config.DefaultDetachKey, "\x1c"
	}

	return &model{
		hist:      loadHistory(h.StateDir()),
		prefs:     loadPrefs(h.StateDir()),
		h:         h,
		events:    events,
		quit:      quit,
		mode:      modeNormal,
		infos:     h.Infos(),
		showLog:   true,
		log:       h.Log().History(200),
		input:     in,
		status:    "press ? for help",
		delivered: map[string]time.Time{},
		detachKey: key,
		detachSeq: seq,
		mouse:     h.Config().Mouse,
	}
}

type tickMsg time.Time
type eventMsg event.Event
type resultMsg struct {
	text  string
	isErr bool
}

func tick() tea.Cmd { return tickEvery(120 * time.Millisecond) }

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// tick paces the refresh. It runs faster while an envelope is fading, because
// half a second at the usual cadence is four frames and a staircase; it drops
// back as soon as nothing is fading, which is almost always.
func (m *model) tick() tea.Cmd {
	if m.anyFading() {
		return tickEvery(40 * time.Millisecond)
	}
	return tick()
}

// noteDeliveries replaces the fleet snapshot, stamping every agent whose unread
// count has just reached zero. That covers both ways a message leaves a
// mailbox: typed into a push agent's terminal, or collected by a pull agent.
func (m *model) noteDeliveries(fresh []agent.Info) {
	was := make(map[string]int, len(m.infos))
	for _, in := range m.infos {
		was[in.Name] = in.Unread
	}
	now := time.Now()
	for _, in := range fresh {
		if in.Unread == 0 && was[in.Name] > 0 {
			m.delivered[in.Name] = now
		}
	}
	m.infos = fresh
}

func (m *model) anyFading() bool {
	for name := range m.delivered {
		if _, ok := msgFadeStyle(time.Since(m.delivered[name])); ok {
			return true
		}
	}
	return false
}

// messageBadge is what follows an agent's name: the count while messages are
// waiting, then an envelope dimming away once they have gone.
func (m *model) messageBadge(in agent.Info) string {
	if in.Unread > 0 {
		return styMsg.Render(fmt.Sprintf(" %d✉", in.Unread))
	}
	at, ok := m.delivered[in.Name]
	if !ok {
		return ""
	}
	style, fading := msgFadeStyle(time.Since(at))
	if !fading {
		delete(m.delivered, in.Name)
		return ""
	}
	return style.Render(" ✉")
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
	cmds := []tea.Cmd{tick(), m.waitEvent()}
	if m.mouse {
		cmds = append(cmds, tea.EnableMouseCellMotion)
	}
	return tea.Batch(cmds...)
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
		m.resizeInput()
		return m, nil

	case tickMsg:
		m.noteDeliveries(m.h.Infos())
		m.fitSelected()
		m.refreshScreen()
		return m, m.tick()

	case eventMsg:
		if e := event.Event(msg); e.Kind == event.KindMessage && e.Agent != "" {
			m.delivered[e.Agent] = time.Now()
		}
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

	// The dialogue lock: a printable key is text meant for the agent, not a
	// shortcut. esc reaches the shortcuts for one key, the way a prefix does,
	// and esc twice leaves the lock for good — the difference between "let me
	// do one thing" and "give me the keyboard back".
	if m.prefs.dialogue {
		switch {
		case msg.Type == tea.KeyEsc && m.escNext:
			m.escNext = false
			m.prefs.dialogue = false
			m.prefs.save()
			m.status = "dialogue off — letters are shortcuts again, d to come back"
			return m, nil
		case msg.Type == tea.KeyEsc:
			m.escNext = true
			m.status = "esc — next key is a shortcut, esc again to leave dialogue"
			return m, nil
		case !m.escNext && (msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace):
			return m.beginDialogue(msg)
		}
	}
	m.escNext = false
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
			m.status = fmt.Sprintf("attached to %s — %s to come back", in.Name, m.detachKey)
		} else {
			m.status, m.isError = "that agent is not running", true
		}
		return m, nil

	case "A":
		return m, m.fullScreenAttach()

	case "d":
		m.prefs.dialogue = !m.prefs.dialogue
		m.prefs.save()
		if m.prefs.dialogue {
			m.status = "dialogue on — what you type goes to the agent; esc reaches a shortcut"
		} else {
			m.status = "dialogue off — letters are shortcuts again"
		}
		return m, nil

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

	case "M":
		m.mouse = !m.mouse
		if m.mouse {
			m.status = "mouse on — wheel scrolls, click selects; text selection is the terminal's no more"
			return m, tea.EnableMouseCellMotion
		}
		m.status = "mouse off — select and copy text as usual"
		return m, tea.DisableMouse

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

// handleAttachedKey forwards keystrokes to the selected agent. The detach key
// leaves the mode instead, and is the same one the standalone `swarm attach`
// uses.
func (m *model) handleAttachedKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	data := keyBytes(msg)
	if string(data) == m.detachSeq {
		m.mode = modeNormal
		m.status = "detached"
		return m, nil
	}
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

// resizeInput gives the command line the whole width. Left at its default, it
// stops halfway across the window and the rest of what you type scrolls out of
// sight for no reason.
func (m *model) resizeInput() {
	// The prompt takes its own columns and the cursor one more; leave a third so
	// the line never reaches the last column, which would wrap. The search
	// prompt is much wider than ":", and it grows as the term is typed.
	width := m.usable() - lipgloss.Width(m.input.Prompt) - 2
	if width < 20 {
		width = 20
	}
	m.input.Width = width
}

// beginDialogue opens the inject line for the agent on screen, carrying the key
// that started it — inject rather than send, because this is talking to the
// agent's own prompt, not putting a message on the bus.
func (m *model) beginDialogue(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	name := m.currentName()
	if name == "" {
		m.status, m.isError = "no agent selected", true
		return m, nil
	}
	typed := string(msg.Runes)
	if msg.Type == tea.KeySpace {
		typed = " "
	}
	m.openCommand("inject " + name + " ")
	return m, m.typeInto(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(typed)})
}

func (m *model) openCommand(prefill string) {
	m.returnTo = m.mode
	m.mode = modeCommand
	m.resizeInput()
	// The verb the line was opened with is what its history is filtered by; the
	// bare `:` line has none and sees everything.
	m.verb, _ = cut(prefill)
	m.hist.begin()
	m.input.Prompt = commandPrompt
	m.input.SetValue(prefill)
	m.input.CursorEnd()
	m.input.Focus()
	m.completions, m.completeAt = nil, ""
}

func (m *model) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.hist.searching {
		return m.handleSearchKey(msg)
	}
	switch msg.Type {
	case tea.KeyCtrlR:
		m.hist.startSearch(m.input.Value())
		m.input.Prompt = m.hist.searchPrompt(true)
		m.resizeInput()
		m.input.SetValue("")
		return m, nil
	case tea.KeyUp:
		if line, ok := m.hist.prev(m.verb, m.input.Value()); ok {
			m.input.SetValue(line)
			m.input.CursorEnd()
		}
		return m, nil
	case tea.KeyDown:
		if line, ok := m.hist.next(m.verb); ok {
			m.input.SetValue(line)
			m.input.CursorEnd()
		}
		return m, nil
	case tea.KeyTab:
		m.complete()
		return m, nil
	case tea.KeyEsc, tea.KeyCtrlC:
		m.mode = m.returnTo
		m.input.Blur()
		m.input.SetValue("")
		return m, nil
	case tea.KeyEnter:
		line := strings.TrimSpace(m.input.Value())
		m.hist.add(line)
		m.input.SetValue("")
		m.input.Blur()
		m.mode = m.returnTo
		return m, m.runCommand(line)
	}
	// Any other key ends the completion cycle: the candidates were for what was
	// on the line before it changed.
	m.completions, m.completeAt = nil, ""
	return m, m.typeInto(msg)
}

// typeInto hands a key to the input field, one rune at a time when the field
// would otherwise mistake the text for a key press.
//
// bubbles matches its bindings on tea.KeyMsg.String(), and a run of runes
// stringifies to the runes themselves: typing u then p quickly enough that they
// arrive together produces a message whose String() is "up", which the field
// reads as the up arrow and swallows. The same goes for down, left, right,
// home, end, delete and backspace — every one of them a key name you would
// want to type after `:keys`, and ordinary words you might send to an agent.
//
// A single rune can never collide, so splitting settles it. Only short runs are
// split: no binding name is longer than a few characters, and a paste arrives
// as one long run that must not be fed in character by character.
func (m *model) typeInto(msg tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && len(msg.Runes) <= maxKeyName {
		for _, r := range msg.Runes {
			var c tea.Cmd
			m.input, c = m.input.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: msg.Alt})
			cmd = tea.Batch(cmd, c)
		}
		return cmd
	}
	m.input, cmd = m.input.Update(msg)
	return cmd
}

// handleSearchKey drives reverse-i-search, in the shape readline made familiar:
// typing narrows, ctrl+r steps further back, enter runs the match, and escape
// puts back the line that was being written.
func (m *model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	h := m.hist
	switch msg.Type {
	case tea.KeyCtrlR:
		// Step past the current match rather than finding it again.
		if line, at, ok := h.searchNext(m.verb, h.at); ok {
			h.at = at
			m.input.SetValue(line)
			m.input.CursorEnd()
		}
		m.input.Prompt = h.searchPrompt(true)
		return m, nil

	case tea.KeyEsc, tea.KeyCtrlC, tea.KeyCtrlG:
		// Abandon the search and the line it found.
		h.searching = false
		m.input.Prompt = commandPrompt
		m.resizeInput()
		m.input.SetValue(h.base)
		m.input.CursorEnd()
		return m, nil

	case tea.KeyEnter:
		h.searching = false
		m.input.Prompt = commandPrompt
		line := strings.TrimSpace(m.input.Value())
		h.add(line)
		m.input.SetValue("")
		m.input.Blur()
		m.mode = m.returnTo
		return m, m.runCommand(line)

	case tea.KeyRunes, tea.KeySpace, tea.KeyBackspace:
		switch msg.Type {
		case tea.KeyBackspace:
			if h.term != "" {
				_, n := utf8.DecodeLastRuneInString(h.term)
				h.term = h.term[:len(h.term)-n]
			}
		case tea.KeySpace:
			h.term += " "
		default:
			h.term += string(msg.Runes)
		}
		// Always search afresh from the newest entry: narrowing the term should
		// not be answered with a match older than the one on screen.
		line, at, ok := h.searchNext(m.verb, -1)
		if ok {
			h.at = at
			m.input.SetValue(line)
			m.input.CursorEnd()
		}
		m.input.Prompt = h.searchPrompt(ok || h.term == "")
		m.resizeInput()
		return m, nil
	}

	// Anything else — the arrows, a control key — accepts the match and leaves
	// the search, so editing can continue on the line it found.
	h.searching = false
	m.input.Prompt = commandPrompt
	m.resizeInput()
	return m, m.typeInto(msg)
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

// A pane smaller than this is not a size to give an agent: agent CLIs collapse
// below it, and resizing down there costs the output that no longer fits. When
// the pane is this small the agent keeps its own geometry and the pane simply
// shows part of it.
const (
	minFitCols = 60
	minFitRows = 12
)

// fitSelected keeps the displayed agent the size of the space it is shown in,
// as long as that space is worth having.
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
	if paneW < minFitCols || rows < minFitRows {
		// Too small to be a terminal. Leave the agent alone and crop instead.
		return
	}
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
		styMuted.Render(version.Short()),
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
		badge := m.messageBadge(in)
		if in.Talking >= hub.TalkNoisy {
			badge += styAttn.Render(" ‼")
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
	if in.Git != "" {
		title += " " + styMuted.Render(in.Git)
	}
	meta := []string{}
	// What the agent is putting on the bus, next to what it is doing: the two
	// numbers are only worth anything side by side.
	if in.Talking > 0 {
		rate := fmt.Sprintf("%d msg/%dm", in.Talking, int(hub.TalkWindow.Minutes()))
		if in.Talking >= hub.TalkNoisy {
			rate = styAttn.Render(rate)
		}
		meta = append(meta, rate)
	}
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
	// Attached comes first: the keyboard belongs to the agent then, whatever
	// the lock says, and a bar claiming otherwise would be worse than none.
	if m.prefs.dialogue && m.mode != modeAttached {
		// The lock is invisible otherwise, and an invisible mode is how the
		// next surprise happens.
		//
		// ↵ still attaches, and the arrows still select: they are not text, so
		// the lock never sees them. Saying so costs a few columns on a line
		// that disappears the moment you type anything.
		lock := styKey.Render("↵") + " attach" + styMuted.Render(" · ") +
			styAttn.Render("⌨ DIALOGUE") +
			styMuted.Render(" — typing goes to "+m.currentName()+" · ") +
			styKey.Render("esc") + styMuted.Render(" for a shortcut")
		if m.escNext {
			lock = styAttn.Render("⌨ esc") + styMuted.Render(" — next key is a shortcut")
		}
		return padRight(lock, m.usable())
	}
	hints := []string{
		styKey.Render("↵") + " attach",
		styKey.Render("A") + " full",
		styKey.Render("i") + " inject",
		styKey.Render("s") + " send",
		styKey.Render("d") + " dialogue",
		styKey.Render("m") + " mosaic",
		styKey.Render(":") + " cmd",
		styKey.Render("?") + " help",
	}
	if m.mode == modeAttached {
		hints = []string{styAttn.Render("ATTACHED") + styMuted.Render(" — keys go to the agent, ") + styKey.Render(m.detachKey) + styMuted.Render(" to detach")}
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
	if in.Git != "" {
		title += " " + styMuted.Render(in.Git)
	}
	if in.Talking > 0 {
		rate := fmt.Sprintf(" %d msg/%dm", in.Talking, int(hub.TalkWindow.Minutes()))
		if in.Talking >= hub.TalkNoisy {
			title += styAttn.Render(rate)
		} else {
			title += styMuted.Render(rate)
		}
	}
	title += m.messageBadge(in)
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
	keys := [][2]string{
		{"j / k / ↑ / ↓", "select an agent"},
		{"tab / shift+tab", "cycle through agents"},
		{"1..9", "jump to an agent"},
		{"↵", "attach: keys go to that agent"},
		{"A", "attach full screen"},
		{"pgup / pgdn", "scroll back through its output"},
		{"d", "dialogue lock (on by default): typing talks to the agent"},
		{"esc", "in dialogue: one shortcut; esc esc leaves the lock"},
		{"m", "mosaic: every agent at once"},
		{"l", "show or hide the event log"},
		{"M", "mouse: wheel and clicks, no text selection"},
		{"i / s / b", "inject / send / broadcast"},
		{"f", "stage a file and inject its path"},
		{"K", "send key presses"},
		{"S / x / r", "start / stop / restart"},
		{":", "command line (tab completes)"},
		{"↑ / ↓", "on the command line: what you typed before"},
		{"ctrl+r", "search it — narrow, ctrl+r again for older"},
		{"?", "this screen"},
		{"q", "quit and stop every agent"},
	}
	cmds := make([][2]string, 0, len(commands))
	for _, c := range commands {
		usage := ":" + c.name
		if c.args != "" {
			usage += " " + c.args
		}
		cmds = append(cmds, [2]string{usage, c.help})
	}

	// Two columns, because the help outgrew the screen once and pushed its own
	// title off the top.
	half := m.usable() / 2
	left := helpColumn("keys", keys, half-1)
	right := helpColumn("commands", cmds, half-1)
	lines := joinColumns(" ", block(left, half-1, max(len(left), len(right))), right)

	lines = append(lines,
		"",
		styMuted.Render("A target is an agent name, @group, @role, all, or a list."),
		styMuted.Render("Omit it and the command applies to the selected agent."),
		styMuted.Render("Detach from an attached agent with "+m.detachKey+"."),
		styMuted.Render("press any key to go back"),
	)

	// Never draw past the window: the title has to stay visible.
	if len(lines) > m.height {
		lines = lines[:m.height-1]
		lines = append(lines, styMuted.Render("…"))
	}
	return strings.Join(lines, "\n")
}

// helpColumn renders one titled table of "key — meaning" rows.
func helpColumn(title string, rows [][2]string, width int) []string {
	label := 0
	for _, r := range rows {
		if w := lipgloss.Width(styKey.Render(r[0])); w > label {
			label = w
		}
	}
	out := []string{styHeader.Render("swarm — " + title), ""}
	for _, r := range rows {
		line := "  " + padRight(styKey.Render(r[0]), label+2) + styBase.Render(r[1])
		out = append(out, padRight(line, width))
	}
	return out
}
