// Package config loads and validates the swarm fleet description.
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/hook"
	"github.com/emmanuel-deloget/swarm/internal/vterm"
	"gopkg.in/yaml.v3"
)

// Config is the whole fleet description.
type Config struct {
	// Session names the swarm instance. It selects the IPC socket, so two
	// swarms with different session names can run side by side.
	Session string `yaml:"session"`

	// Workdir is the default working directory for every agent that does not
	// override it. Relative paths are resolved against the config file.
	Workdir string `yaml:"workdir"`

	// Shared is the directory where injected files (images, patches, notes)
	// are staged so that every agent can reach them by path.
	Shared string `yaml:"shared"`

	// StateDir holds everything swarm writes: the control socket, the logs, the
	// CLI shim and the shared files. Relative to this file, ".swarm" by
	// default. It is worth putting in .gitignore, which `swarm init` offers to
	// do.
	StateDir string `yaml:"state_dir"`

	// Env is added to the environment of every agent.
	Env map[string]string `yaml:"env"`

	// Defaults are merged into every agent that leaves a field empty.
	Defaults AgentDefaults `yaml:"defaults"`

	// DetachKey leaves an attached terminal, in the TUI and in `swarm attach`
	// alike. It is a key name as understood by the keys command ("ctrl+g",
	// "ctrl+]", "esc esc"). Configurable because the default collides with
	// whatever else is capturing the terminal — tmux, asciinema, screen.
	DetachKey string `yaml:"detach_key"`

	// LogInput records everything swarm sends to an agent, in
	// <state>/logs/<agent>.input.log: injections, key presses, and the answers
	// the emulator gives to the agent's own queries. Off by default — it is a
	// record of what you typed — and worth turning on to settle "did swarm send
	// that, or did the agent print it itself?".
	LogInput bool `yaml:"log_input"`

	// Mouse turns on mouse reporting in the TUI: the wheel scrolls the pane and
	// a click selects an agent. It is off by default, because a terminal that
	// reports mouse events to swarm no longer lets you select and copy text
	// with it — and reading an agent's output matters more than the wheel.
	// Toggle it at runtime with M.
	Mouse bool `yaml:"mouse"`

	// Web configures the remote-control HTTP server.
	Web WebConfig `yaml:"web"`

	// Bus configures inter-agent messaging.
	Bus BusConfig `yaml:"bus"`

	// DeliveryByKind overrides the recipient's delivery mode for a kind of
	// message. A notice nobody has to act on can wait for a mailbox even when
	// the agent takes everything else at once; a decision can interrupt an
	// agent that otherwise batches.
	//
	//	delivery_by_kind:
	//	  fyi: pull
	//	  question: defer
	//	  decision: push
	DeliveryByKind map[string]string `yaml:"delivery_by_kind"`

	// Hooks configures the inbound webhook listener.
	Hooks HookConfig `yaml:"hooks"`

	// Agents is the fleet itself.
	Agents []AgentConfig `yaml:"agents"`

	// Groups are named sets of agent names, usable wherever a target is
	// expected (":send @dev fix the build").
	Groups map[string][]string `yaml:"groups"`

	// AgentsTemplate replaces the generated AGENTS.md with your own. It gets
	// the same data the built-in one does, so an override can describe this
	// fleet rather than a fleet in general.
	AgentsTemplate string `yaml:"agents_template"`

	// path is the file this config was loaded from, "" when synthesised.
	path string
}

// AgentDefaults holds the values inherited by agents.
type AgentDefaults struct {
	Cols            int           `yaml:"cols"`
	Rows            int           `yaml:"rows"`
	Scrollback      int           `yaml:"scrollback"`
	IdleAfter       time.Duration `yaml:"idle_after"`
	Autostart       *bool         `yaml:"autostart"`
	RestartOnExit   *bool         `yaml:"restart_on_exit"`
	RestartBackoff  time.Duration `yaml:"restart_backoff"`
	SubmitDelay     time.Duration `yaml:"submit_delay"`
	KeyDelay        time.Duration `yaml:"key_delay"`
	BracketedPaste  *bool         `yaml:"bracketed_paste"`
	FollowWindow    *bool         `yaml:"follow_window"`
	DeliveryMode    string        `yaml:"delivery"`
	MessageTemplate string        `yaml:"message_template"`
	Workspace       string        `yaml:"workspace"`
	OnStart         []string      `yaml:"on_start"`
	OnExit          []string      `yaml:"on_exit"`
	Message         string        `yaml:"message"`
	MessageFile     string        `yaml:"message_file"`
	CanSend         []string      `yaml:"can_send"`
}

// AgentConfig describes one agent process running in its own virtual terminal.
type AgentConfig struct {
	// Name is the unique handle used everywhere (CLI, TUI, web, bus).
	Name string `yaml:"name"`

	// Role is free-form ("dev", "review", "triage"); it only drives display
	// and group membership shortcuts.
	Role string `yaml:"role"`

	// Command is the argv of the agent CLI. swarm is agnostic: anything that
	// runs in a terminal works.
	Command []string `yaml:"command"`

	// Workdir overrides Config.Workdir.
	Workdir string `yaml:"workdir"`

	// Env overrides/extends Config.Env.
	Env map[string]string `yaml:"env"`

	// Cols/Rows size the virtual terminal. Agents keep this size regardless
	// of the size of the window that watches them.
	Cols int `yaml:"cols"`
	Rows int `yaml:"rows"`

	// Scrollback is the number of lines the emulator keeps above the screen.
	Scrollback int `yaml:"scrollback"`

	// IdleAfter is how long without output before the agent is considered
	// idle rather than working.
	IdleAfter time.Duration `yaml:"idle_after"`

	// Autostart launches the agent when the swarm starts.
	Autostart *bool `yaml:"autostart"`

	// RestartOnExit relaunches the agent when its process exits.
	RestartOnExit *bool `yaml:"restart_on_exit"`

	// RestartBackoff is the delay before an automatic restart.
	RestartBackoff time.Duration `yaml:"restart_backoff"`

	// KeyDelay is the pause between one key press and the next when several are
	// sent at once. Without it they arrive in a single read, and an agent whose
	// UI changes state on a key — a prompt submitted, a mode cycled — acts on
	// the first and drops the rest with the buffer it was holding.
	//
	// It is the same reason SubmitDelay exists, for the same kind of UI.
	KeyDelay time.Duration `yaml:"key_delay"`

	// SubmitDelay is the pause between pasting text and sending the newline
	// that submits it. Agent TUIs that re-render on paste need this.
	SubmitDelay time.Duration `yaml:"submit_delay"`

	// BracketedPaste wraps injected text in ESC[200~ / ESC[201~ so multi-line
	// payloads are not interpreted as successive submissions.
	BracketedPaste *bool `yaml:"bracketed_paste"`

	// FollowWindow resizes the agent to the pane showing it in the TUI, so its
	// own layout adapts instead of being cut off. Turn it off to pin the
	// geometry above, which is what the web UI and `swarm screen` then see.
	FollowWindow *bool `yaml:"follow_window"`

	// DeliveryMode decides what happens to a bus message addressed to this
	// agent: "push" injects it in the terminal, "pull" queues it until the
	// agent runs `swarm inbox`.
	DeliveryMode string `yaml:"delivery"`

	// MessageTemplate renders a bus message before injection. Placeholders:
	// {from}, {to}, {body}, {id}.
	MessageTemplate string `yaml:"message_template"`

	// Workspace says what swarm does about this agent's working copy.
	//
	//	shared  nothing: the agent runs in Workdir, like everyone else
	//	clone   swarm provisions a durable clone and points Workdir at it
	//	none    nothing, and swarm presumes nothing — the agent manages its
	//	        own isolation and is free to move
	//
	// Workdir and Workspace are orthogonal: the first says where, the second
	// says what swarm does there. With "clone" and no Workdir, the clone lands
	// in <state_dir>/workspaces/<name>.
	Workspace string `yaml:"workspace"`

	// OnStart runs before the agent process is launched, and OnExit after it
	// has gone. Each is an argv, run in the agent's working directory with the
	// agent's environment — so $SWARM_AGENT and any allocated port are already
	// there.
	//
	// This is swarm's answer to preparing an environment, which is the part of
	// isolation no agent can arrange for itself: installing dependencies,
	// copying a .env, pointing at a dedicated test database. A failing OnStart
	// stops the agent rather than launching it into a half-prepared directory.
	OnStart []string `yaml:"on_start"`
	OnExit  []string `yaml:"on_exit"`

	// Message is typed into the agent once, when it first falls quiet after
	// starting — its standing brief. YAML block scalars make it readable:
	//
	//	message: |
	//	  You review Go. Read the open pull requests assigned to you
	//	  and comment on the ones that touch the parser.
	//
	// MessageFile reads it from a file instead, for a brief long enough to
	// deserve its own document. Naming both is an error rather than a
	// precedence rule.
	//
	// It waits for quiet because an agent CLI has not drawn its prompt at the
	// moment its process starts, and typing into one that is still painting
	// loses the text.
	Message     string `yaml:"message"`
	MessageFile string `yaml:"message_file"`

	// CanSend limits who this agent may reach on the bus, in the usual target
	// syntax: names, "@group", "@role", "all". Empty means everyone.
	//
	// Left open, the bus is the complete graph — the worst case for how many
	// conversations can be going at once. Fifteen lines of configuration turn
	// it into a star and a whole class of noise disappears:
	//
	//	can_send: [lead-1]     a dev reports upward, and not sideways
	//
	// This is the config deciding rather than the content, which is the same
	// rule that forbids templating a webhook's target: an agent should not
	// choose freely who it wakes.
	CanSend []string `yaml:"can_send"`

	// Patterns classify the agent state from what it prints.
	Patterns []PatternConfig `yaml:"patterns"`
}

// PatternConfig maps a regexp on recent output to a state and/or a notification.
type PatternConfig struct {
	// Match is a regexp tested against the tail of the rendered screen.
	Match string `yaml:"match"`

	// State is the state to report while Match holds: "waiting", "blocked",
	// "attention", "error", or any label you like.
	State string `yaml:"state"`

	// Notify raises an event in the TUI/web log when the pattern appears.
	Notify bool `yaml:"notify"`

	// Reply is injected automatically when the pattern appears. Use with
	// care; it is how you auto-approve trusted prompts.
	Reply string `yaml:"reply"`

	re *regexp.Regexp
}

// Regexp returns the compiled pattern.
func (p *PatternConfig) Regexp() *regexp.Regexp { return p.re }

// WebConfig configures the remote-control server.
type WebConfig struct {
	Enabled bool   `yaml:"enabled"`
	Addr    string `yaml:"addr"`
	// Token authenticates every request. Empty means one is generated at
	// startup and printed in the TUI.
	Token string `yaml:"token"`
	// ReadOnly serves the terminals but refuses keystrokes and injections.
	ReadOnly bool `yaml:"read_only"`
	// TLSCert/TLSKey enable HTTPS. Strongly recommended past localhost.
	TLSCert string `yaml:"tls_cert"`
	TLSKey  string `yaml:"tls_key"`
}

// BusConfig configures inter-agent messaging.
type BusConfig struct {
	// Enabled turns the bus on. It defaults to true: agents reaching each
	// other through `swarm send` is a core feature, not an opt-in. Set it to
	// false to forbid messaging entirely.
	Enabled *bool `yaml:"enabled"`
	// History is the number of messages kept per mailbox for replay.
	History int `yaml:"history"`
	// AllowSelfInject lets an agent inject into its own terminal.
	AllowSelfInject bool `yaml:"allow_self_inject"`

	// MaxTurns bounds a conversation. Zero means unbounded, which is what a bus
	// is by default and why nothing on it can end.
	//
	// At the last turn but one, the delivery carries a warning. At the last, the
	// send fails — and the error text is the instruction, which is the cheapest
	// way to get a behaviour without teaching the model anything.
	MaxTurns int `yaml:"max_turns"`

	// EscalateTo is who arbitrates when a thread runs out of turns: an agent,
	// a group, or a role. Their answer comes back as final, so it closes the
	// matter instead of opening another.
	EscalateTo string `yaml:"escalate_to"`
}

// HookConfig configures the inbound webhook listener: HTTP in, bus messages
// out. It listens on its own address rather than sharing the web remote
// control, whose token can type into every terminal.
type HookConfig struct {
	// Enabled turns the listener on. Off by default: it opens a port.
	Enabled bool `yaml:"enabled"`

	// Addr is where to listen. Loopback by default — put a reverse proxy or a
	// tunnel in front rather than binding this to the world.
	Addr string `yaml:"addr"`

	// Token, when set, is required on every request as "X-Swarm-Token", as a
	// bearer token, or as ?t= in the query.
	Token string `yaml:"token"`

	// Secret enables HMAC-SHA256 verification of the raw body. Prefer
	// SecretEnv: a secret written in the config file is a secret in your
	// history, your backups and any screenshot of the file.
	Secret string `yaml:"secret"`

	// SecretEnv names an environment variable holding the secret.
	SecretEnv string `yaml:"secret_env"`

	// SecretPath reads the secret from a file, which must not be readable by
	// anyone but its owner. Relative paths are resolved against the config
	// file.
	SecretPath string `yaml:"secret_path"`

	// SignatureHeader carries the digest, "X-Reqwire-Signature". Required when
	// a secret is set — there is no universal name for it.
	SignatureHeader string `yaml:"signature_header"`

	// From is the sender name the agents see. Defaults to "webhook".
	From string `yaml:"from"`

	// MaxBody caps the request body in bytes.
	MaxBody int64 `yaml:"max_body"`

	// Log records every delivery in full — headers, payload, the verdict of
	// each rule and what was sent — in <state>/logs/webhooks.log. On by
	// default: a webhook that does nothing looks identical whether it never
	// arrived, was refused, or matched no rule, and nothing outside the
	// listener can tell those apart. The file is 0600, since a payload carries
	// whatever the sender put in it.
	Log *bool `yaml:"log"`

	// Rules are tried against every payload; each match sends a message.
	Rules []hook.Rule `yaml:"rules"`

	// Unmatched fires only when no rule matched — route it at a triage agent
	// and the swarm stops being blind to events nobody anticipated.
	Unmatched *hook.Rule `yaml:"unmatched"`
}

// Delivery modes.
const (
	DeliveryPush = "push"
	DeliveryPull = "pull"
	// DeliveryDefer holds a message until the agent falls quiet, then types it
	// in. A pushed message interrupts whatever the agent was doing, becomes the
	// salient thing, and gets answered — which interrupts the sender in turn.
	// Deferring means a conversation can follow work but never cut into it.
	DeliveryDefer = "defer"
)

// Workspace modes.
const (
	WorkspaceShared = "shared"
	WorkspaceClone  = "clone"
	WorkspaceNone   = "none"
)

// DefaultMessageTemplate is what a pushed bus message looks like in a terminal.
const DefaultMessageTemplate = "[swarm] message from {from}: {body}"

// DefaultDetachKey leaves an attached terminal when nothing else is configured.
const DefaultDetachKey = "ctrl+\\"

// DefaultStateDir is where swarm writes everything, relative to the config file.
const DefaultStateDir = ".swarm"

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	c.path = abs
	if err := c.normalize(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// Path returns the file the config came from.
func (c *Config) Path() string { return c.path }

// Dir returns the directory holding the config file, used to resolve relative
// paths. It falls back to the process working directory.
func (c *Config) Dir() string {
	if c.path == "" {
		wd, _ := os.Getwd()
		return wd
	}
	return filepath.Dir(c.path)
}

func (c *Config) normalize() error {
	if c.Session == "" {
		c.Session = "default"
	}
	if strings.ContainsAny(c.Session, "/\\ ") {
		return fmt.Errorf("session %q must not contain slashes or spaces", c.Session)
	}
	base := c.Dir()
	if c.Workdir == "" {
		c.Workdir = base
	}
	c.Workdir = resolve(base, c.Workdir)
	if c.StateDir == "" {
		c.StateDir = DefaultStateDir
	}
	c.StateDir = resolve(base, c.StateDir)
	if c.Shared == "" {
		c.Shared = filepath.Join(c.StateDir, "shared")
	}
	c.Shared = resolve(base, c.Shared)
	if c.AgentsTemplate != "" {
		c.AgentsTemplate = resolve(base, c.AgentsTemplate)
		// Checked at load rather than at write time: a template that cannot be
		// read would otherwise leave the fleet running with no guide at all.
		if _, err := os.Stat(c.AgentsTemplate); err != nil {
			return fmt.Errorf("agents_template: %w", err)
		}
	}

	d := &c.Defaults
	if d.Cols == 0 {
		d.Cols = 200
	}
	if d.Rows == 0 {
		d.Rows = 50
	}
	if d.Scrollback == 0 {
		d.Scrollback = 5000
	}
	if d.IdleAfter == 0 {
		d.IdleAfter = 3 * time.Second
	}
	if d.RestartBackoff == 0 {
		d.RestartBackoff = 2 * time.Second
	}
	if d.KeyDelay == 0 {
		d.KeyDelay = 40 * time.Millisecond
	}
	if d.SubmitDelay == 0 {
		d.SubmitDelay = 120 * time.Millisecond
	}
	if d.Autostart == nil {
		d.Autostart = ptr(true)
	}
	if d.RestartOnExit == nil {
		d.RestartOnExit = ptr(false)
	}
	if d.BracketedPaste == nil {
		d.BracketedPaste = ptr(true)
	}
	if d.FollowWindow == nil {
		d.FollowWindow = ptr(true)
	}
	if d.DeliveryMode == "" {
		d.DeliveryMode = DeliveryPush
	}
	if d.MessageTemplate == "" {
		d.MessageTemplate = DefaultMessageTemplate
	}
	if d.Workspace == "" {
		d.Workspace = WorkspaceShared
	}

	if c.DetachKey == "" {
		c.DetachKey = DefaultDetachKey
	}
	if err := vterm.CheckBindable(c.DetachKey); err != nil {
		return fmt.Errorf("detach_key: %w (see `swarm keys -list`)", err)
	}

	if c.Web.Addr == "" {
		c.Web.Addr = "127.0.0.1:7777"
	}
	if (c.Web.TLSCert == "") != (c.Web.TLSKey == "") {
		return fmt.Errorf("web: tls_cert and tls_key must be set together")
	}
	c.Web.TLSCert = resolveEmpty(base, c.Web.TLSCert)
	c.Web.TLSKey = resolveEmpty(base, c.Web.TLSKey)
	for kind, mode := range c.DeliveryByKind {
		if !bus.ValidKind(bus.Kind(kind)) {
			return fmt.Errorf("delivery_by_kind: unknown kind %q", kind)
		}
		switch mode {
		case DeliveryPush, DeliveryPull, DeliveryDefer:
		default:
			return fmt.Errorf("delivery_by_kind[%s]: must be %q, %q or %q",
				kind, DeliveryPush, DeliveryPull, DeliveryDefer)
		}
	}
	if c.Bus.History == 0 {
		c.Bus.History = 200
	}
	if c.Bus.Enabled == nil {
		c.Bus.Enabled = ptr(true)
	}
	if c.Bus.MaxTurns < 0 {
		return fmt.Errorf("bus: max_turns must not be negative")
	}

	if len(c.Agents) == 0 {
		return fmt.Errorf("no agents defined")
	}
	seen := make(map[string]bool, len(c.Agents))
	for i := range c.Agents {
		a := &c.Agents[i]
		if a.Name == "" {
			return fmt.Errorf("agent #%d: name is required", i+1)
		}
		if strings.ContainsAny(a.Name, " \t@,") {
			return fmt.Errorf("agent %q: name must not contain spaces, @ or commas", a.Name)
		}
		if seen[a.Name] {
			return fmt.Errorf("agent %q: duplicate name", a.Name)
		}
		seen[a.Name] = true
		if len(a.Command) == 0 {
			return fmt.Errorf("agent %q: command is required", a.Name)
		}
		if a.Workspace == "" {
			a.Workspace = d.Workspace
		}
		switch a.Workspace {
		case WorkspaceShared, WorkspaceClone, WorkspaceNone:
		default:
			return fmt.Errorf("agent %q: workspace must be %q, %q or %q",
				a.Name, WorkspaceShared, WorkspaceClone, WorkspaceNone)
		}
		// Where, and what swarm does there, are separate questions. A clone
		// with nowhere named goes under the state directory; one with a workdir
		// is provisioned in place, which is what an existing fleet of
		// hand-made clones already looks like.
		if a.Workspace == WorkspaceClone && a.Workdir == "" {
			a.Workdir = filepath.Join(c.StateDir, "workspaces", a.Name)
		} else {
			a.Workdir = resolve(base, orString(a.Workdir, c.Workdir))
		}
		if a.Cols == 0 {
			a.Cols = d.Cols
		}
		if a.Rows == 0 {
			a.Rows = d.Rows
		}
		if a.Scrollback == 0 {
			a.Scrollback = d.Scrollback
		}
		if a.IdleAfter == 0 {
			a.IdleAfter = d.IdleAfter
		}
		if a.RestartBackoff == 0 {
			a.RestartBackoff = d.RestartBackoff
		}
		if a.SubmitDelay == 0 {
			a.SubmitDelay = d.SubmitDelay
		}
		if a.KeyDelay == 0 {
			a.KeyDelay = d.KeyDelay
		}
		if a.Autostart == nil {
			a.Autostart = d.Autostart
		}
		if a.RestartOnExit == nil {
			a.RestartOnExit = d.RestartOnExit
		}
		if a.BracketedPaste == nil {
			a.BracketedPaste = d.BracketedPaste
		}
		if a.FollowWindow == nil {
			a.FollowWindow = d.FollowWindow
		}
		if a.DeliveryMode == "" {
			a.DeliveryMode = d.DeliveryMode
		}
		switch a.DeliveryMode {
		case DeliveryPush, DeliveryPull, DeliveryDefer:
		default:
			return fmt.Errorf("agent %q: delivery must be %q, %q or %q",
				a.Name, DeliveryPush, DeliveryPull, DeliveryDefer)
		}
		if a.MessageTemplate == "" {
			a.MessageTemplate = d.MessageTemplate
		}
		if a.OnStart == nil {
			a.OnStart = d.OnStart
		}
		if a.OnExit == nil {
			a.OnExit = d.OnExit
		}
		if a.Message == "" && a.MessageFile == "" {
			a.Message, a.MessageFile = d.Message, d.MessageFile
		}
		if a.CanSend == nil {
			a.CanSend = d.CanSend
		}
		if a.Message != "" && a.MessageFile != "" {
			return fmt.Errorf("agent %q: set message or message_file, not both", a.Name)
		}
		if a.MessageFile != "" {
			body, err := os.ReadFile(resolve(base, a.MessageFile))
			if err != nil {
				return fmt.Errorf("agent %q: message_file: %w", a.Name, err)
			}
			a.Message = string(body)
		}
		for j := range a.Patterns {
			p := &a.Patterns[j]
			if p.Match == "" {
				return fmt.Errorf("agent %q pattern #%d: match is required", a.Name, j+1)
			}
			re, err := regexp.Compile(p.Match)
			if err != nil {
				return fmt.Errorf("agent %q pattern #%d: %w", a.Name, j+1, err)
			}
			p.re = re
		}
	}

	if c.Bus.EscalateTo != "" {
		if _, err := c.Resolve(c.Bus.EscalateTo); err != nil {
			return fmt.Errorf("bus: escalate_to: %w", err)
		}
	}

	for i := range c.Agents {
		a := &c.Agents[i]
		for _, target := range a.CanSend {
			if _, err := c.Resolve(target); err != nil {
				return fmt.Errorf("agent %q: can_send: %w", a.Name, err)
			}
		}
	}

	for name, members := range c.Groups {
		if strings.ContainsAny(name, " \t@,") {
			return fmt.Errorf("group %q: name must not contain spaces, @ or commas", name)
		}
		for _, m := range members {
			if !seen[m] {
				return fmt.Errorf("group %q: unknown agent %q", name, m)
			}
		}
	}

	return c.normalizeHooks()
}

// normalizeHooks validates the webhook rules. It runs after the agents and the
// groups, because a rule names a target and an unknown target is worth
// reporting when the file is read rather than when the event arrives — by then
// nobody is watching.
func (c *Config) normalizeHooks() error {
	h := &c.Hooks
	if h.Addr == "" {
		h.Addr = "127.0.0.1:7778"
	}
	if h.From == "" {
		h.From = "webhook"
	}
	if h.MaxBody == 0 {
		h.MaxBody = 1 << 20
	}
	if h.Log == nil {
		h.Log = ptr(true)
	}
	// Fail closed, both ways round: a secret nobody looks for verifies nothing,
	// and a header nobody can check is worse than no header at all. This looks
	// at what was declared, not at what could be read — the two are checked
	// apart so that a listener that is switched off stays out of the way.
	declared := h.Secret != "" || h.SecretEnv != "" || h.SecretPath != ""
	if declared && h.SignatureHeader == "" {
		return fmt.Errorf("hooks: signature_header is required with a secret")
	}
	if h.SignatureHeader != "" && !declared {
		return fmt.Errorf("hooks: signature_header is set but no secret is")
	}
	if err := h.resolveSecret(c.Dir(), h.Enabled); err != nil {
		return err
	}
	check := func(what string, r *hook.Rule) error {
		if err := r.Compile(); err != nil {
			return fmt.Errorf("hook %s: %w", what, err)
		}
		if _, err := c.Resolve(r.To); err != nil {
			return fmt.Errorf("hook %s: to: %w", what, err)
		}
		return nil
	}
	for i := range h.Rules {
		r := &h.Rules[i]
		what := fmt.Sprintf("rule #%d", i+1)
		if r.Name != "" {
			what = fmt.Sprintf("rule %q", r.Name)
		}
		if err := check(what, r); err != nil {
			return err
		}
	}
	if h.Unmatched != nil {
		if err := check("unmatched", h.Unmatched); err != nil {
			return err
		}
	}
	return nil
}

// MayReach reports whether an agent is allowed to put a message in another's
// mailbox, and if not, where to go instead. The reason is written for the agent
// that will read it: a refusal that does not say what to do next only costs a
// turn.
func (c *Config) MayReach(from, to string) (bool, string) {
	sender, ok := c.Agent(from)
	if !ok || len(sender.CanSend) == 0 {
		// The user, a webhook, or an agent with no restriction.
		return true, ""
	}
	for _, target := range sender.CanSend {
		names, err := c.Resolve(target)
		if err != nil {
			continue
		}
		for _, n := range names {
			if n == to {
				return true, ""
			}
		}
	}
	return false, fmt.Sprintf("%s cannot reach %s; it may write to %s",
		from, to, strings.Join(sender.CanSend, ", "))
}

// BusEnabled reports whether inter-agent messaging is allowed.
func (c *Config) BusEnabled() bool { return c.Bus.Enabled != nil && *c.Bus.Enabled }

// HookLogEnabled reports whether every webhook delivery is recorded in full.
func (c *Config) HookLogEnabled() bool { return c.Hooks.Log != nil && *c.Hooks.Log }

// resolveSecret loads the HMAC secret from whichever source was configured and
// leaves it in Secret. Exactly one source may be named: silently preferring one
// over another is how a swarm ends up verifying signatures against a secret its
// owner thought they had replaced.
//
// load says whether to go and fetch it. A disabled listener still has its
// sources checked for coherence, but nothing is read: a hooks block that is
// switched off must not make the whole config unloadable because its secret
// file has not been created yet — that would take `swarm ls` down with it.
func (h *HookConfig) resolveSecret(base string, load bool) error {
	named := make([]string, 0, 3)
	for _, s := range []struct{ name, value string }{
		{"secret", h.Secret},
		{"secret_env", h.SecretEnv},
		{"secret_path", h.SecretPath},
	} {
		if s.value != "" {
			named = append(named, s.name)
		}
	}
	if len(named) > 1 {
		return fmt.Errorf("hooks: set only one of %s", strings.Join(named, ", "))
	}
	if !load {
		return nil
	}

	switch {
	case h.SecretEnv != "":
		h.Secret = strings.TrimSpace(os.Getenv(h.SecretEnv))
		if h.Secret == "" {
			return fmt.Errorf("hooks: secret_env names %s, which is empty", h.SecretEnv)
		}
	case h.SecretPath != "":
		h.SecretPath = resolve(base, h.SecretPath)
		secret, err := readSecretFile(h.SecretPath)
		if err != nil {
			return fmt.Errorf("hooks: secret_path: %w", err)
		}
		h.Secret = secret
	default:
		// A trailing newline from an editor is invisible and changes the digest
		// completely, which is indistinguishable from a wrong secret.
		h.Secret = strings.TrimSpace(h.Secret)
	}
	return nil
}

// readSecretFile reads a secret from a file that nobody but its owner may read.
// A shared secret in a group-readable file is not shared with the sender: it is
// shared with everyone on the machine, and the whole point of the signature is
// that only the two ends can produce it.
//
// The mode is checked through the open file rather than by stat-ing the path,
// so what was verified is what was read.
func readSecretFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	if perm := st.Mode().Perm(); perm&0o077 != 0 {
		return "", fmt.Errorf("%s is mode %#o: it must not be readable by group or others (chmod 600 %s)", path, perm, path)
	}

	raw, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return "", err
	}
	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return secret, nil
}

// Agent returns the config of the named agent.
func (c *Config) Agent(name string) (*AgentConfig, bool) {
	for i := range c.Agents {
		if c.Agents[i].Name == name {
			return &c.Agents[i], true
		}
	}
	return nil, false
}

// Resolve expands a target expression into agent names. It accepts a single
// agent name, "@group", "@role", "all"/"*", or a comma-separated list of those.
func (c *Config) Resolve(target string) ([]string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("empty target")
	}
	var out []string
	seen := make(map[string]bool)
	add := func(names ...string) {
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	for _, part := range strings.Split(target, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		switch {
		case part == "all", part == "*", part == "@all":
			for i := range c.Agents {
				add(c.Agents[i].Name)
			}
		case strings.HasPrefix(part, "@"):
			key := part[1:]
			if members, ok := c.Groups[key]; ok {
				add(members...)
				continue
			}
			var byRole []string
			for i := range c.Agents {
				if c.Agents[i].Role == key {
					byRole = append(byRole, c.Agents[i].Name)
				}
			}
			if len(byRole) == 0 {
				return nil, fmt.Errorf("unknown group or role %q", key)
			}
			add(byRole...)
		default:
			if _, ok := c.Agent(part); !ok {
				return nil, fmt.Errorf("unknown agent %q", part)
			}
			add(part)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("target %q matched no agent", target)
	}
	return out, nil
}

// AutostartEnabled reports whether the agent starts with the swarm.
func (a *AgentConfig) AutostartEnabled() bool { return a.Autostart != nil && *a.Autostart }

// RestartEnabled reports whether the agent is relaunched when it exits.
func (a *AgentConfig) RestartEnabled() bool { return a.RestartOnExit != nil && *a.RestartOnExit }

// PasteEnabled reports whether injected text is wrapped in bracketed paste.
func (a *AgentConfig) PasteEnabled() bool { return a.BracketedPaste != nil && *a.BracketedPaste }

// FollowsWindow reports whether the agent is resized to the pane showing it.
func (a *AgentConfig) FollowsWindow() bool { return a.FollowWindow != nil && *a.FollowWindow }

func resolve(base, p string) string {
	p = os.ExpandEnv(p)
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

func resolveEmpty(base, p string) string {
	if p == "" {
		return ""
	}
	return resolve(base, p)
}

func orString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func ptr[T any](v T) *T { return &v }
