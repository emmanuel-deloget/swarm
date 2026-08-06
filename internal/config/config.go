// Package config loads and validates the swarm fleet description.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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

	// Env is added to the environment of every agent.
	Env map[string]string `yaml:"env"`

	// Defaults are merged into every agent that leaves a field empty.
	Defaults AgentDefaults `yaml:"defaults"`

	// DetachKey leaves an attached terminal, in the TUI and in `swarm attach`
	// alike. It is a key name as understood by the keys command ("ctrl+g",
	// "ctrl+]", "esc esc"). Configurable because the default collides with
	// whatever else is capturing the terminal — tmux, asciinema, screen.
	DetachKey string `yaml:"detach_key"`

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

	// Agents is the fleet itself.
	Agents []AgentConfig `yaml:"agents"`

	// Groups are named sets of agent names, usable wherever a target is
	// expected (":send @dev fix the build").
	Groups map[string][]string `yaml:"groups"`

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
	BracketedPaste  *bool         `yaml:"bracketed_paste"`
	FollowWindow    *bool         `yaml:"follow_window"`
	DeliveryMode    string        `yaml:"delivery"`
	MessageTemplate string        `yaml:"message_template"`
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
}

// Delivery modes.
const (
	DeliveryPush = "push"
	DeliveryPull = "pull"
)

// DefaultMessageTemplate is what a pushed bus message looks like in a terminal.
const DefaultMessageTemplate = "[swarm] message from {from}: {body}"

// DefaultDetachKey leaves an attached terminal when nothing else is configured.
const DefaultDetachKey = "ctrl+\\"

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
	if c.Shared == "" {
		c.Shared = filepath.Join(base, ".swarm", "shared")
	}
	c.Shared = resolve(base, c.Shared)

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
	if c.Bus.History == 0 {
		c.Bus.History = 200
	}
	if c.Bus.Enabled == nil {
		c.Bus.Enabled = ptr(true)
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
		a.Workdir = resolve(base, orString(a.Workdir, c.Workdir))
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
		case DeliveryPush, DeliveryPull:
		default:
			return fmt.Errorf("agent %q: delivery must be %q or %q", a.Name, DeliveryPush, DeliveryPull)
		}
		if a.MessageTemplate == "" {
			a.MessageTemplate = d.MessageTemplate
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
	return nil
}

// BusEnabled reports whether inter-agent messaging is allowed.
func (c *Config) BusEnabled() bool { return c.Bus.Enabled != nil && *c.Bus.Enabled }

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
