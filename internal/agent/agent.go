// Package agent turns one configured command into a supervised agent running
// in its own virtual terminal, with a state derived from what it prints.
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/config"
	"github.com/emmanuel-deloget/swarm/internal/event"
	"github.com/emmanuel-deloget/swarm/internal/vterm"
)

// State is the coarse lifecycle state of an agent.
type State string

// Agent states.
const (
	// StateStopped means the agent has never run, or was stopped on purpose.
	StateStopped State = "stopped"
	// StateStarting means the process exists but has not printed anything yet.
	StateStarting State = "starting"
	// StateWorking means output is flowing right now.
	StateWorking State = "working"
	// StateIdle means the process is alive but quiet: usually waiting for you.
	StateIdle State = "idle"
	// StateExited means the process is gone.
	StateExited State = "exited"
)

// Info is a serialisable view of an agent, for the CLI, the TUI and the web.
type Info struct {
	Name       string        `json:"name"`
	Role       string        `json:"role"`
	Command    []string      `json:"command"`
	Workdir    string        `json:"workdir"`
	State      State         `json:"state"`
	Attention  string        `json:"attention,omitempty"`
	Pid        int           `json:"pid"`
	Cols       int           `json:"cols"`
	Rows       int           `json:"rows"`
	StartedAt  time.Time     `json:"started_at,omitzero"`
	Uptime     time.Duration `json:"uptime"`
	LastOutput time.Time     `json:"last_output,omitzero"`
	Quiet      time.Duration `json:"quiet"`
	BytesOut   uint64        `json:"bytes_out"`
	Restarts   int           `json:"restarts"`
	Title      string        `json:"title,omitempty"`
	AltScreen  bool          `json:"alt_screen"`
	Exit       string        `json:"exit,omitempty"`
	Delivery   string        `json:"delivery"`
	Unread     int           `json:"unread"`
}

// Options builds an Agent.
type Options struct {
	// Config is the agent description; it is not copied, so it must outlive
	// the agent.
	Config *config.AgentConfig
	// Log receives the agent's events.
	Log *event.Log
	// Env is the complete environment handed to the process.
	Env []string
	// LogFile, when set, receives the raw terminal output for later replay.
	LogFile string
}

// Agent supervises one command inside a virtual terminal.
type Agent struct {
	cfg     *config.AgentConfig
	log     *event.Log
	env     []string
	logPath string

	mu         sync.Mutex
	term       *vterm.Terminal
	state      State
	attention  string
	title      string
	startedAt  time.Time
	exit       *vterm.ExitStatus
	restarts   int
	stopping   bool
	generation uint64
	matched    map[int]bool
	logFile    *os.File
	watchStop  chan struct{}

	injectMu sync.Mutex
}

// New creates a stopped agent.
func New(o Options) *Agent {
	return &Agent{
		cfg:     o.Config,
		log:     o.Log,
		env:     o.Env,
		logPath: o.LogFile,
		state:   StateStopped,
		matched: make(map[int]bool),
	}
}

// Name returns the agent handle.
func (a *Agent) Name() string { return a.cfg.Name }

// Role returns the agent role.
func (a *Agent) Role() string { return a.cfg.Role }

// Config returns the agent configuration.
func (a *Agent) Config() *config.AgentConfig { return a.cfg }

// Start launches the process. Starting a running agent is a no-op.
func (a *Agent) Start() error {
	a.mu.Lock()
	if a.term != nil && !a.term.Exited() {
		a.mu.Unlock()
		return fmt.Errorf("agent %s: already running", a.cfg.Name)
	}
	a.stopping = false
	a.generation++
	gen := a.generation
	a.mu.Unlock()

	if a.logPath != "" {
		if err := os.MkdirAll(filepath.Dir(a.logPath), 0o755); err == nil {
			if f, err := os.OpenFile(a.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
				a.mu.Lock()
				if a.logFile != nil {
					_ = a.logFile.Close()
				}
				a.logFile = f
				a.mu.Unlock()
				fmt.Fprintf(f, "\n\x1b[0m--- swarm: %s started at %s ---\r\n", a.cfg.Name, time.Now().Format(time.RFC3339))
			}
		}
	}

	term, err := vterm.Start(vterm.Options{
		Command:     a.cfg.Command,
		Dir:         a.cfg.Workdir,
		Env:         a.env,
		Cols:        a.cfg.Cols,
		Rows:        a.cfg.Rows,
		Scrollback:  a.cfg.Scrollback,
		OnTitle:     func(s string) { a.setTitle(s) },
		OnBell:      func() { a.log.Emit(event.KindBell, a.cfg.Name, "bell") },
		OnOutput:    a.onOutput,
		OnAltScreen: func(bool) {},
		OnExit:      func(st vterm.ExitStatus) { a.onExit(gen, st) },
	})
	if err != nil {
		a.mu.Lock()
		a.state = StateExited
		a.mu.Unlock()
		a.log.Emit(event.KindError, a.cfg.Name, err.Error())
		return err
	}

	stop := make(chan struct{})
	a.mu.Lock()
	a.term = term
	a.state = StateStarting
	a.attention = ""
	a.exit = nil
	a.startedAt = time.Now()
	a.matched = make(map[int]bool)
	a.watchStop = stop
	a.mu.Unlock()

	go a.watch(term, stop)
	a.log.Publish(event.Event{
		Kind:  event.KindStarted,
		Agent: a.cfg.Name,
		Text:  strings.Join(a.cfg.Command, " "),
		Data:  map[string]string{"pid": fmt.Sprint(term.Pid()), "workdir": a.cfg.Workdir},
	})
	return nil
}

// Stop terminates the process and disables automatic restart.
func (a *Agent) Stop(grace time.Duration) error {
	a.mu.Lock()
	a.stopping = true
	term := a.term
	a.mu.Unlock()
	if term == nil || term.Exited() {
		a.mu.Lock()
		if a.state != StateExited {
			a.state = StateStopped
		}
		a.mu.Unlock()
		return nil
	}
	if grace <= 0 {
		grace = 5 * time.Second
	}
	return term.Stop(grace)
}

// Restart stops then starts the agent.
func (a *Agent) Restart(grace time.Duration) error {
	if err := a.Stop(grace); err != nil {
		return err
	}
	a.mu.Lock()
	term := a.term
	a.mu.Unlock()
	if term != nil {
		select {
		case <-term.Done():
		case <-time.After(grace + 2*time.Second):
			return fmt.Errorf("agent %s: did not stop in time", a.cfg.Name)
		}
	}
	a.mu.Lock()
	a.restarts++
	a.mu.Unlock()
	return a.Start()
}

// Signal forwards a signal to the process group.
func (a *Agent) Signal(sig syscall.Signal) error {
	term := a.terminal()
	if term == nil {
		return vterm.ErrExited
	}
	return term.Signal(sig)
}

func (a *Agent) terminal() *vterm.Terminal {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.term == nil || a.term.Exited() {
		return nil
	}
	return a.term
}

// Terminal returns the live terminal, or nil when the agent is not running.
// Callers use it to subscribe to output.
func (a *Agent) Terminal() *vterm.Terminal { return a.terminal() }

func (a *Agent) onOutput(chunk []byte) {
	a.mu.Lock()
	f := a.logFile
	if a.state == StateStarting || a.state == StateIdle {
		a.state = StateWorking
	}
	a.mu.Unlock()
	if f != nil {
		_, _ = f.Write(chunk)
	}
}

func (a *Agent) setTitle(s string) {
	a.mu.Lock()
	a.title = s
	a.mu.Unlock()
}

func (a *Agent) onExit(gen uint64, st vterm.ExitStatus) {
	a.mu.Lock()
	if gen != a.generation {
		// A newer generation already took over; ignore the late reaping.
		a.mu.Unlock()
		return
	}
	a.state = StateExited
	a.exit = &st
	stopping := a.stopping
	if a.watchStop != nil {
		close(a.watchStop)
		a.watchStop = nil
	}
	if a.logFile != nil {
		fmt.Fprintf(a.logFile, "\r\n--- swarm: %s %s ---\r\n", a.cfg.Name, st)
		_ = a.logFile.Close()
		a.logFile = nil
	}
	a.mu.Unlock()

	a.log.Publish(event.Event{
		Kind:  event.KindExited,
		Agent: a.cfg.Name,
		Text:  st.String(),
		Data:  map[string]string{"code": fmt.Sprint(st.Code)},
	})

	if stopping || !a.cfg.RestartEnabled() {
		return
	}
	go func() {
		time.Sleep(a.cfg.RestartBackoff)
		a.mu.Lock()
		abort := a.stopping || gen != a.generation
		if !abort {
			a.restarts++
		}
		a.mu.Unlock()
		if abort {
			return
		}
		a.log.Emit(event.KindInfo, a.cfg.Name, "restarting after exit")
		if err := a.Start(); err != nil {
			a.log.Emit(event.KindError, a.cfg.Name, "restart failed: "+err.Error())
		}
	}()
}

// watch derives the agent state from its output cadence and matches the
// configured patterns against the screen.
func (a *Agent) watch(term *vterm.Terminal, stop chan struct{}) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-term.Done():
			return
		case <-tick.C:
			a.refresh(term)
		}
	}
}

func (a *Agent) refresh(term *vterm.Terminal) {
	quiet := time.Since(term.LastOutput())

	a.mu.Lock()
	prev := a.state
	if a.state == StateWorking || a.state == StateStarting {
		if quiet >= a.cfg.IdleAfter {
			a.state = StateIdle
		}
	}
	next := a.state
	a.mu.Unlock()

	if next != prev {
		a.log.Publish(event.Event{Kind: event.KindState, Agent: a.cfg.Name, Text: string(next)})
	}

	if len(a.cfg.Patterns) == 0 {
		return
	}
	screen := term.Text()
	// Only the tail matters: agent CLIs redraw their prompt at the bottom.
	if len(screen) > 8192 {
		screen = screen[len(screen)-8192:]
	}

	var attention string
	for i := range a.cfg.Patterns {
		p := &a.cfg.Patterns[i]
		hit := p.Regexp().MatchString(screen)
		a.mu.Lock()
		was := a.matched[i]
		a.matched[i] = hit
		a.mu.Unlock()

		if hit && attention == "" && p.State != "" {
			attention = p.State
		}
		if hit && !was {
			if p.Notify {
				a.log.Publish(event.Event{
					Kind:  event.KindPattern,
					Agent: a.cfg.Name,
					Text:  firstMatchLine(screen, p),
					Data:  map[string]string{"state": p.State, "pattern": p.Match},
				})
			}
			if p.Reply != "" {
				reply := p.Reply
				go func() {
					if _, err := a.Inject(reply, InjectOptions{Submit: true}); err != nil {
						a.log.Emit(event.KindError, a.cfg.Name, "auto-reply failed: "+err.Error())
					} else {
						a.log.Emit(event.KindInject, a.cfg.Name, "auto-reply: "+reply)
					}
				}()
			}
		}
	}

	a.mu.Lock()
	changed := a.attention != attention
	a.attention = attention
	a.mu.Unlock()
	if changed && attention != "" {
		a.log.Publish(event.Event{Kind: event.KindState, Agent: a.cfg.Name, Text: attention})
	}
}

func firstMatchLine(screen string, p *config.PatternConfig) string {
	for _, line := range strings.Split(screen, "\n") {
		if p.Regexp().MatchString(line) {
			return strings.TrimSpace(line)
		}
	}
	return p.Match
}

// InjectOptions tunes how text reaches the agent's prompt.
type InjectOptions struct {
	// Submit appends the newline that sends the prompt.
	Submit bool
	// Raw writes the bytes untouched: no sanitising, no bracketed paste.
	Raw bool
	// Paste overrides the agent's bracketed-paste setting.
	Paste *bool
	// SubmitDelay overrides the agent's delay between paste and newline.
	SubmitDelay time.Duration
}

// Inject types text into the agent's terminal. It returns the number of bytes
// written to the pty.
func (a *Agent) Inject(text string, o InjectOptions) (int, error) {
	term := a.terminal()
	if term == nil {
		return 0, fmt.Errorf("agent %s: not running", a.cfg.Name)
	}

	payload := text
	if !o.Raw {
		payload = vterm.SanitizeText(payload)
		// Only delimit the payload when the agent's own UI asked for
		// bracketed paste, exactly as a terminal would. The config can veto
		// it, and an explicit option can force it either way.
		paste := a.cfg.PasteEnabled() && term.BracketedPaste()
		if o.Paste != nil {
			paste = *o.Paste
		}
		if paste && payload != "" {
			payload = vterm.PasteSequence(payload)
		}
	}

	delay := a.cfg.SubmitDelay
	if o.SubmitDelay > 0 {
		delay = o.SubmitDelay
	}

	// One injection at a time, so a paste and its newline are never split by
	// another injection.
	a.injectMu.Lock()
	defer a.injectMu.Unlock()

	n := 0
	if payload != "" {
		written, err := term.Write([]byte(payload))
		n += written
		if err != nil {
			return n, err
		}
	}
	if o.Submit {
		if payload != "" && delay > 0 {
			time.Sleep(delay)
		}
		written, err := term.Write([]byte(vterm.Submit))
		n += written
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// SendKeys writes the given key names ("ctrl+c", "esc enter", ...) to the
// terminal.
func (a *Agent) SendKeys(names string) error {
	seq, err := vterm.KeySequences(names)
	if err != nil {
		return err
	}
	term := a.terminal()
	if term == nil {
		return fmt.Errorf("agent %s: not running", a.cfg.Name)
	}
	a.injectMu.Lock()
	defer a.injectMu.Unlock()
	_, err = term.Write([]byte(seq))
	return err
}

// WriteRaw forwards raw input bytes, used by the attached TUI pane and by web
// clients typing in a browser terminal.
func (a *Agent) WriteRaw(p []byte) error {
	term := a.terminal()
	if term == nil {
		return fmt.Errorf("agent %s: not running", a.cfg.Name)
	}
	_, err := term.Write(p)
	return err
}

// Resize changes the agent's terminal geometry.
func (a *Agent) Resize(cols, rows int) error {
	a.mu.Lock()
	a.cfg.Cols, a.cfg.Rows = cols, rows
	term := a.term
	a.mu.Unlock()
	if term == nil {
		return nil
	}
	return term.Resize(cols, rows)
}

// Render returns the current screen with ANSI styling.
func (a *Agent) Render() string {
	a.mu.Lock()
	term := a.term
	a.mu.Unlock()
	if term == nil {
		return ""
	}
	return term.Render()
}

// Text returns the current screen as plain text.
func (a *Agent) Text() string {
	a.mu.Lock()
	term := a.term
	a.mu.Unlock()
	if term == nil {
		return ""
	}
	return term.Text()
}

// HTMLLines renders the screen as one HTML fragment per line, for the web UI.
func (a *Agent) HTMLLines() []string {
	a.mu.Lock()
	term := a.term
	a.mu.Unlock()
	if term == nil {
		return nil
	}
	return term.HTMLLines()
}

// State returns the current state and the attention label, if any.
func (a *Agent) State() (State, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state, a.attention
}

// LogPath returns the file holding the raw terminal output, or "".
func (a *Agent) LogPath() string { return a.logPath }

// Info snapshots the agent for display.
func (a *Agent) Info() Info {
	a.mu.Lock()
	term := a.term
	info := Info{
		Name:      a.cfg.Name,
		Role:      a.cfg.Role,
		Command:   a.cfg.Command,
		Workdir:   a.cfg.Workdir,
		State:     a.state,
		Attention: a.attention,
		Cols:      a.cfg.Cols,
		Rows:      a.cfg.Rows,
		StartedAt: a.startedAt,
		Restarts:  a.restarts,
		Title:     a.title,
		Delivery:  a.cfg.DeliveryMode,
	}
	if a.exit != nil {
		info.Exit = a.exit.String()
	}
	a.mu.Unlock()

	if term != nil {
		info.Pid = term.Pid()
		info.Cols, info.Rows = term.Size()
		info.BytesOut = term.BytesOut()
		info.LastOutput = term.LastOutput()
		info.AltScreen = term.AltScreen()
		if !info.LastOutput.IsZero() {
			info.Quiet = time.Since(info.LastOutput).Round(time.Second)
		}
	}
	if !info.StartedAt.IsZero() && info.State != StateStopped && info.State != StateExited {
		info.Uptime = time.Since(info.StartedAt).Round(time.Second)
	}
	return info
}
