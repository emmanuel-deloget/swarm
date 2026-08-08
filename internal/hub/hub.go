// Package hub owns the fleet: it creates the agents, wires their environment
// so they can talk back to swarm, and routes every command to them.
package hub

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/agent"
	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/config"
	"github.com/emmanuel-deloget/swarm/internal/event"
	"github.com/emmanuel-deloget/swarm/internal/sockpath"
)

// Hub is the fleet supervisor.
type Hub struct {
	cfg      *config.Config
	log      *event.Log
	bus      *bus.Bus
	stateDir string

	// ports remembers what {alloc_port} resolved to, so a restart keeps it.
	portMu sync.Mutex
	ports  map[string]int

	// briefed records which launch of an agent has already had its opening
	// message, keyed by generation so a restart gets a fresh brief and a second
	// quiet spell does not.
	briefMu sync.Mutex
	briefed map[string]uint64

	mu     sync.RWMutex
	agents map[string]*agent.Agent
	order  []string

	webURL string
	token  string
}

// Options builds a Hub.
type Options struct {
	Config *config.Config
	// StateDir holds the socket, the logs, the shim and the shared files.
	// Defaults to <config dir>/.swarm.
	StateDir string
	// EventHistory bounds the in-memory event log.
	EventHistory int
}

// New builds the fleet described by the config. No process is started yet.
func New(o Options) (*Hub, error) {
	cfg := o.Config
	stateDir := o.StateDir
	if stateDir == "" {
		stateDir = filepath.Join(cfg.Dir(), ".swarm")
	}
	for _, dir := range []string{stateDir, filepath.Join(stateDir, "logs"), filepath.Join(stateDir, "bin"), cfg.Shared} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("hub: %w", err)
		}
	}

	h := &Hub{
		cfg:      cfg,
		log:      event.NewLog(o.EventHistory),
		bus:      bus.New(cfg.Bus.History),
		stateDir: stateDir,
		agents:   make(map[string]*agent.Agent, len(cfg.Agents)),
	}

	shimDir, err := h.installShim()
	if err != nil {
		// Not fatal: without the shim agents can still be driven from the
		// outside, they just cannot call back with a bare `swarm`.
		h.log.Emit(event.KindError, "", "could not install the swarm shim: "+err.Error())
	}

	for i := range cfg.Agents {
		ac := &cfg.Agents[i]
		opts := agent.Options{
			Config:    ac,
			CloneFrom: cfg.Workdir,
			OnIdle: func() {
				h.brief(ac.Name)
				h.flushDeferred(ac.Name)
			},
			Log:     h.log,
			Env:     h.agentEnv(ac, shimDir),
			LogFile: filepath.Join(stateDir, "logs", ac.Name+".log"),
		}
		if cfg.LogInput {
			opts.InputLogFile = filepath.Join(stateDir, "logs", ac.Name+".input.log")
		}
		h.agents[ac.Name] = agent.New(opts)
		h.order = append(h.order, ac.Name)
	}
	return h, nil
}

// Config returns the loaded configuration.
func (h *Hub) Config() *config.Config { return h.cfg }

// Log returns the event log.
func (h *Hub) Log() *event.Log { return h.log }

// Bus returns the message bus.
func (h *Hub) Bus() *bus.Bus { return h.bus }

// StateDir returns the directory holding the socket, logs and shared files.
func (h *Hub) StateDir() string { return h.stateDir }

// SocketPath is where the IPC server listens.
func (h *Hub) SocketPath() string {
	socket, _ := sockpath.For(h.stateDir, h.cfg.Session)
	return socket
}

// SocketPointer is the file recording the socket location when it had to be
// moved out of the state directory, or "" when it did not.
func (h *Hub) SocketPointer() string {
	_, pointer := sockpath.For(h.stateDir, h.cfg.Session)
	return pointer
}

// SetWebURL records the address the web server ended up on.
func (h *Hub) SetWebURL(u, token string) {
	h.mu.Lock()
	h.webURL, h.token = u, token
	h.mu.Unlock()
}

// WebURL returns the remote-control URL and its token.
func (h *Hub) WebURL() (string, string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.webURL, h.token
}

// installShim drops a `swarm` executable in the state dir and returns its
// directory, so agents get the command in their PATH.
func (h *Hub) installShim() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(h.stateDir, "bin")
	link := filepath.Join(dir, "swarm")
	if current, err := os.Readlink(link); err == nil && current == exe {
		return dir, nil
	}
	_ = os.Remove(link)
	if err := os.Symlink(exe, link); err != nil {
		return "", err
	}
	return dir, nil
}

// agentEnv builds the environment of an agent process: the parent environment,
// the config overrides, a sane terminal description, and the SWARM_* variables
// an agent needs to reach the bus.
func (h *Hub) agentEnv(ac *config.AgentConfig, shimDir string) []string {
	merged := make(map[string]string)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			merged[k] = v
		}
	}
	// Agent CLIs render much better when they can assume a modern terminal,
	// and our emulator does support 256 colours and truecolor.
	merged["TERM"] = "xterm-256color"
	merged["COLORTERM"] = "truecolor"
	merged["LINES"] = fmt.Sprint(ac.Rows)
	merged["COLUMNS"] = fmt.Sprint(ac.Cols)

	for k, v := range h.cfg.Env {
		merged[k] = v
	}
	for k, v := range ac.Env {
		merged[k] = v
	}

	// {alloc_port} in any value becomes a port nobody else is listening on,
	// picked once per agent so a restart keeps it. This is the other half of
	// preparing an environment: two agents running a dev server both want 3000,
	// and no amount of talking to each other settles that.
	for k, v := range merged {
		if strings.Contains(v, "{alloc_port}") {
			merged[k] = strings.ReplaceAll(v, "{alloc_port}", fmt.Sprint(h.portFor(ac.Name, k)))
		}
	}

	merged["SWARM_AGENT"] = ac.Name
	merged["SWARM_ROLE"] = ac.Role
	merged["SWARM_SESSION"] = h.cfg.Session
	merged["SWARM_SOCKET"] = h.SocketPath()
	merged["SWARM_SHARED"] = h.cfg.Shared
	merged["SWARM_STATE_DIR"] = h.stateDir
	merged["SWARM_PEERS"] = strings.Join(h.peerNames(ac.Name), ",")
	if shimDir != "" {
		merged["PATH"] = shimDir + string(os.PathListSeparator) + merged["PATH"]
	}

	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func (h *Hub) peerNames(self string) []string {
	var out []string
	for i := range h.cfg.Agents {
		if h.cfg.Agents[i].Name != self {
			out = append(out, h.cfg.Agents[i].Name)
		}
	}
	return out
}

// Names returns the agent names in configuration order.
func (h *Hub) Names() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, len(h.order))
	copy(out, h.order)
	return out
}

// Agent returns one agent by name.
func (h *Hub) Agent(name string) (*agent.Agent, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	a, ok := h.agents[name]
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", name)
	}
	return a, nil
}

// Agents returns every agent in configuration order.
func (h *Hub) Agents() []*agent.Agent {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*agent.Agent, 0, len(h.order))
	for _, n := range h.order {
		out = append(out, h.agents[n])
	}
	return out
}

// Resolve expands a target expression ("dev-1", "@review", "all") into agents.
func (h *Hub) Resolve(target string) ([]*agent.Agent, error) {
	names, err := h.cfg.Resolve(target)
	if err != nil {
		return nil, err
	}
	out := make([]*agent.Agent, 0, len(names))
	for _, n := range names {
		a, err := h.Agent(n)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// Infos snapshots the whole fleet, unread counts included.
func (h *Hub) Infos() []agent.Info {
	pending := h.bus.PendingAll()
	since := time.Now().Add(-TalkWindow)
	agents := h.Agents()
	out := make([]agent.Info, 0, len(agents))
	for _, a := range agents {
		info := a.Info()
		info.Unread = pending[info.Name]
		info.Talking = h.bus.SentSince(info.Name, since)
		out = append(out, info)
	}
	return out
}

// TalkWindow is how far back an agent's share of the bus is counted, and
// TalkNoisy how many messages in that window are worth pointing at.
//
// The threshold is a display hint, not a policy: swarm marks an agent that is
// talking a lot and does nothing about it. Ten minutes is long enough that a
// burst of coordination at the start of a task does not trip it, and short
// enough that a spiral shows up while it is still going.
const (
	TalkWindow = 10 * time.Minute
	TalkNoisy  = 8
)

// portFor hands out a free TCP port, remembering it per agent and variable so
// that a restart does not move the agent's server from under whatever was
// pointing at it.
func (h *Hub) portFor(agent, key string) int {
	h.portMu.Lock()
	defer h.portMu.Unlock()
	if h.ports == nil {
		h.ports = map[string]int{}
	}
	id := agent + "\x00" + key
	if p, ok := h.ports[id]; ok {
		return p
	}
	p := freePort()
	h.ports[id] = p
	return p
}

// freePort asks the kernel for one and lets it go again. There is a window
// between letting go and the agent binding it, which nothing can close from
// out here — the alternative is asking people to pick ports by hand, which
// collides far more often than this races.
func freePort() int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// brief types an agent's standing message, once per launch, when it first falls
// quiet. Waiting matters: at the moment a process starts, an agent CLI has not
// drawn its prompt, and text typed into one still painting is lost.
func (h *Hub) brief(name string) {
	a, err := h.Agent(name)
	if err != nil || a.Config().Message == "" {
		return
	}
	h.briefMu.Lock()
	if h.briefed == nil {
		h.briefed = map[string]uint64{}
	}
	gen := a.Generation()
	if h.briefed[name] == gen {
		h.briefMu.Unlock()
		return
	}
	h.briefed[name] = gen
	h.briefMu.Unlock()

	if _, err := a.Inject(a.Config().Message, agent.InjectOptions{Submit: true}); err != nil {
		h.log.Emit(event.KindError, name, "opening message failed: "+err.Error())
		return
	}
	h.log.Emit(event.KindInfo, name, "sent the opening message")
}

// flushDeferred types everything waiting for an agent, as one injection.
//
// Coalescing is the point as much as the deferral: three messages that arrived
// while the agent worked are one interruption when it stops, not three.
func (h *Hub) flushDeferred(name string) {
	a, err := h.Agent(name)
	if err != nil || a.Config().DeliveryMode != config.DeliveryDefer {
		return
	}
	pending := h.bus.Collect(name, true)
	if len(pending) == 0 {
		return
	}

	tmpl := a.Config().MessageTemplate
	var b strings.Builder
	if len(pending) > 1 {
		fmt.Fprintf(&b, "[swarm] %d messages arrived while you were working\n\n", len(pending))
	}
	for i, m := range pending {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.Render(tmpl))
	}

	if _, err := a.Inject(b.String(), agent.InjectOptions{Submit: true}); err != nil {
		h.log.Emit(event.KindError, name, "deferred delivery failed: "+err.Error())
		return
	}
	for _, m := range pending {
		h.bus.MarkPushed(name, m.ID)
	}
	h.log.Publish(event.Event{
		Kind:  event.KindMessage,
		Agent: name,
		Text:  fmt.Sprintf("delivered %d deferred message(s)", len(pending)),
	})
}

// StartAll launches every agent marked autostart.
func (h *Hub) StartAll() {
	var wg sync.WaitGroup
	for _, a := range h.Agents() {
		if !a.Config().AutostartEnabled() {
			continue
		}
		wg.Add(1)
		go func(a *agent.Agent) {
			defer wg.Done()
			if err := a.Start(); err != nil {
				h.log.Emit(event.KindError, a.Name(), err.Error())
			}
		}(a)
	}
	wg.Wait()
}

// Shutdown stops every agent, giving each one the same grace period.
func (h *Hub) Shutdown(grace time.Duration) {
	var wg sync.WaitGroup
	for _, a := range h.Agents() {
		wg.Add(1)
		go func(a *agent.Agent) {
			defer wg.Done()
			if err := a.Stop(grace); err != nil {
				h.log.Emit(event.KindError, a.Name(), "stop: "+err.Error())
			}
		}(a)
	}
	wg.Wait()
}

// TargetResult reports the outcome of a fleet-wide operation on one agent.
type TargetResult struct {
	Agent string `json:"agent"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Info  string `json:"info,omitempty"`
}

func results(agents []*agent.Agent, fn func(*agent.Agent) (string, error)) []TargetResult {
	out := make([]TargetResult, 0, len(agents))
	for _, a := range agents {
		info, err := fn(a)
		r := TargetResult{Agent: a.Name(), OK: err == nil, Info: info}
		if err != nil {
			r.Error = err.Error()
		}
		out = append(out, r)
	}
	return out
}

// Start starts every agent matching target.
func (h *Hub) Start(target string) ([]TargetResult, error) {
	agents, err := h.Resolve(target)
	if err != nil {
		return nil, err
	}
	return results(agents, func(a *agent.Agent) (string, error) { return "started", a.Start() }), nil
}

// Stop stops every agent matching target.
func (h *Hub) Stop(target string, grace time.Duration) ([]TargetResult, error) {
	agents, err := h.Resolve(target)
	if err != nil {
		return nil, err
	}
	return results(agents, func(a *agent.Agent) (string, error) { return "stopped", a.Stop(grace) }), nil
}

// Restart restarts every agent matching target.
func (h *Hub) Restart(target string, grace time.Duration) ([]TargetResult, error) {
	agents, err := h.Resolve(target)
	if err != nil {
		return nil, err
	}
	return results(agents, func(a *agent.Agent) (string, error) { return "restarted", a.Restart(grace) }), nil
}

// Inject types text into every agent matching target.
func (h *Hub) Inject(target, text string, o agent.InjectOptions) ([]TargetResult, error) {
	agents, err := h.Resolve(target)
	if err != nil {
		return nil, err
	}
	res := results(agents, func(a *agent.Agent) (string, error) {
		n, err := a.Inject(text, o)
		return fmt.Sprintf("%d bytes", n), err
	})
	for _, r := range res {
		if r.OK {
			h.log.Publish(event.Event{Kind: event.KindInject, Agent: r.Agent, Text: summarize(text)})
		}
	}
	return res, nil
}

// Keys sends key names to every agent matching target.
func (h *Hub) Keys(target, keys string) ([]TargetResult, error) {
	agents, err := h.Resolve(target)
	if err != nil {
		return nil, err
	}
	res := results(agents, func(a *agent.Agent) (string, error) { return keys, a.SendKeys(keys) })
	for _, r := range res {
		if r.OK {
			h.log.Publish(event.Event{Kind: event.KindInject, Agent: r.Agent, Text: "keys: " + keys})
		}
	}
	return res, nil
}

// Send delivers a message from one sender to every agent matching target.
// Recipients in push mode get it typed into their terminal right away; those in
// pull mode keep it pending until they run `swarm inbox`.
func (h *Hub) Send(from, target, body string, files []string) ([]bus.Message, error) {
	if !h.cfg.BusEnabled() {
		return nil, fmt.Errorf("the bus is disabled in the configuration")
	}
	agents, err := h.Resolve(target)
	if err != nil {
		return nil, err
	}
	if from == "" {
		from = "user"
	}
	thread := h.bus.NewThread()
	out := make([]bus.Message, 0, len(agents))
	for _, a := range agents {
		if a.Name() == from && !h.cfg.Bus.AllowSelfInject {
			continue
		}
		msg := h.bus.Post(bus.Message{
			Thread: thread,
			From:   from,
			To:     a.Name(),
			Body:   body,
			Files:  files,
		})
		// A deferred recipient that is already quiet gets it now: waiting for a
		// transition that has already happened would hold the message until the
		// agent next did some work, which is the opposite of the intent.
		if a.Config().DeliveryMode == config.DeliveryDefer && a.Info().State == agent.StateIdle {
			go h.flushDeferred(a.Name())
		}
		if a.Config().DeliveryMode == config.DeliveryPush {
			rendered := msg.Render(a.Config().MessageTemplate)
			if _, err := a.Inject(rendered, agent.InjectOptions{Submit: true}); err != nil {
				h.log.Publish(event.Event{
					Kind:  event.KindMessage,
					Agent: a.Name(),
					Text:  fmt.Sprintf("queued (push failed: %v)", err),
					Data:  map[string]string{"from": from},
				})
			} else {
				msg.Pushed = true
				h.bus.MarkPushed(a.Name(), msg.ID)
			}
		}
		h.log.Publish(event.Event{
			Kind:  event.KindMessage,
			Agent: a.Name(),
			Text:  fmt.Sprintf("%s → %s: %s", from, a.Name(), summarize(body)),
			Data:  map[string]string{"from": from, "id": fmt.Sprint(msg.ID)},
		})
		out = append(out, msg)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("target %q resolved to no recipient", target)
	}
	return out, nil
}

// Inbox collects the pending messages of an agent, optionally waiting for one.
func (h *Hub) Inbox(name string, peek bool, wait time.Duration, cancel <-chan struct{}) ([]bus.Message, error) {
	if _, err := h.Agent(name); err != nil {
		return nil, err
	}
	if wait != 0 {
		h.bus.Wait(name, wait, cancel)
	}
	return h.bus.Collect(name, peek), nil
}

var unsafeFilename = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// StageFile writes data into the shared directory and returns its absolute
// path. This is how an image or a patch becomes something every agent can
// reach: swarm injects the path, the agent opens it.
func (h *Hub) StageFile(name string, data []byte) (string, error) {
	base := unsafeFilename.ReplaceAllString(filepath.Base(name), "_")
	if base == "" || base == "_" {
		base = "file"
	}
	if err := os.MkdirAll(h.cfg.Shared, 0o755); err != nil {
		return "", err
	}
	stamp := time.Now().Format("20060102-150405")
	path := filepath.Join(h.cfg.Shared, stamp+"-"+base)
	for i := 1; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		path = filepath.Join(h.cfg.Shared, fmt.Sprintf("%s-%d-%s", stamp, i, base))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// CopyToShared stages an existing file and returns its new path.
func (h *Hub) CopyToShared(src string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	return h.StageFile(filepath.Base(src), data)
}

// NewToken returns a random token for the web server.
func NewToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprint(time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func summarize(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " ⏎ "))
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}
