// Package hub owns the fleet: it creates the agents, wires their environment
// so they can talk back to swarm, and routes every command to them.
package hub

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
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
	"github.com/emmanuel-deloget/swarm/internal/hook"
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

	// escalated remembers which threads have already been handed to an arbiter,
	// so a saturated conversation raises one alarm rather than one per attempt.
	escalateMu sync.Mutex
	escalated  map[uint64]bool
	// stalled counts what the on_stalled rules have already done, per debt.
	stalled *stalledActor

	// sender posts fleet events outwards. Nil when nothing is configured, and
	// every call on it is nil-safe, so the rest of the hub need not care.
	sender    *hook.Sender
	outCancel func()

	// stalledStop ends the watcher that reports agents which owe something and
	// have gone quiet.
	stalledStop chan struct{}
	stalledOnce sync.Once
	// owedStop ends the writer that keeps the debts on disk. They are the one
	// piece of state a restart must not destroy: see owedstore.go.
	owedStop chan struct{}
	owedDone chan struct{}
	owedOnce sync.Once
	// lifeStop ends the watcher that collects instances which have outlived
	// their max_lifetime.
	lifeStop chan struct{}
	lifeDone chan struct{}
	lifeOnce sync.Once
	// spawnStop ends the writer that keeps instance names from being reused
	// across restarts. See spawnstore.go.
	spawnStop chan struct{}
	spawnDone chan struct{}
	spawnOnce sync.Once

	// paused holds the reason deliveries are suspended, empty when they are not.
	// A circuit breaker rather than a stop: the agents keep working, and what
	// they say to each other waits — which is what you want at the moment you
	// have stopped understanding what the fleet is doing.
	pauseMu sync.RWMutex
	paused  string

	// briefed records which launch of an agent has already had its opening
	// message, keyed by generation so a restart gets a fresh brief and a second
	// quiet spell does not.
	briefMu sync.Mutex
	briefed map[string]uint64

	mu     sync.RWMutex
	agents map[string]*agent.Agent
	order  []string

	// spawn holds the ephemeral instances, which are agents of this fleet
	// without being in the file that describes it. See spawn.go for why they
	// are not simply appended to the configuration.
	spawn *spawner

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

	h.spawn = &spawner{
		live: map[string]*instance{},
		next: map[string]int{},
	}

	shimDir, err := h.installShim()
	if err != nil {
		// Not fatal: without the shim agents can still be driven from the
		// outside, they just cannot call back with a bare `swarm`.
		h.log.Emit(event.KindError, "", "could not install the swarm shim: "+err.Error())
	}

	h.spawn.shim = shimDir

	for i := range cfg.Agents {
		ac := &cfg.Agents[i]
		if ac.Ephemeral {
			// A template is the shape of an agent, not one: nothing is started
			// for it, and it is not in the fleet.
			continue
		}
		opts := agent.Options{
			Config:    ac,
			CloneFrom: cfg.Workdir,
			OnIdle: func() {
				h.brief(ac.Name)
				h.flushDeferred(ac.Name)
				h.notifyIdle(ac.Name)
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

	// What was owed when the last process stopped. Before the watchers, so a
	// restored debt is already there the first time anything looks.
	if len(cfg.Bus.OnStalled) > 0 {
		h.stalled = &stalledActor{seen: map[stalledKey]stalledSeen{}}
	}

	h.loadSpawn()
	h.spawnStop = make(chan struct{})
	h.spawnDone = make(chan struct{})
	go h.watchSpawn(owedSaveEvery)

	h.reportOrphanWorktrees()

	h.loadOwed()
	h.owedStop = make(chan struct{})
	h.owedDone = make(chan struct{})
	go h.watchOwed(owedSaveEvery)

	if every := lifetimeTick(cfg); every > 0 {
		h.lifeStop = make(chan struct{})
		h.lifeDone = make(chan struct{})
		go h.watchLifetimes(every)
	}

	// Last, and not before: the watcher reads the agent list, which the loop
	// above is still writing.
	if after := cfg.Bus.StalledAfter; after > 0 {
		h.stalledStop = make(chan struct{})
		go h.watchStalled(after)
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

// installShim drops a `swarm` command in the state dir and returns its
// directory, so agents get the command in their PATH. How it is dropped
// differs per operating system; see shim_unix.go.
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
	if err := linkShim(dir, exe); err != nil {
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
	// LINES and COLUMNS are deliberately not set, and any inherited pair is
	// removed. They are a promise swarm cannot keep: an agent is resized to the
	// pane showing it, and no one can change a running process's environment,
	// so the numbers would be a snapshot of the geometry at launch — wrong from
	// the first relayout onwards.
	//
	// They are not harmless. Python's shutil.get_terminal_size, which Textual
	// and Rich are built on, prefers them to asking the terminal, so an agent
	// written with them draws for a screen it no longer has: Mistral Vibe kept
	// addressing row 40 on a 33-row pty and lost the top of every dialog. A
	// real terminal does not export them either — bash updates them, and keeps
	// them to itself.
	delete(merged, "LINES")
	delete(merged, "COLUMNS")

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
	// The directory holding swarm.yaml, which is what relative paths in that
	// file resolve against. An agent free to move — or one given its own clone
	// — otherwise has no way back to the project the fleet was started for.
	merged["SWARM_ROOT"] = h.cfg.Dir()
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
	// Instances are agents of this fleet without being in the file that
	// describes it, so the configuration cannot resolve them on its own.
	if names, handled, err := h.resolveEphemeral(target); handled {
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
		info.Owed = len(h.bus.Owed(info.Name))
		info.Stalled = h.isStalled(info)
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

// Pause suspends delivery. Messages are still accepted and still queued; what
// stops is anything being typed into a terminal.
func (h *Hub) Pause(reason string) {
	if reason == "" {
		reason = "paused"
	}
	h.pauseMu.Lock()
	h.paused = reason
	h.pauseMu.Unlock()
	h.log.Emit(event.KindInfo, "", "bus paused: "+reason)
}

// Resume lets delivery start again. With flush, everything that piled up is
// handed over; without, it stays in the mailboxes for the agents to collect.
func (h *Hub) Resume(flush bool) {
	h.pauseMu.Lock()
	h.paused = ""
	h.pauseMu.Unlock()
	h.log.Emit(event.KindInfo, "", "bus resumed")
	if !flush {
		return
	}
	for _, a := range h.Agents() {
		// Everything that would have been typed had the bus been running:
		// pausing turned push into a queue, and resuming has to undo that, or a
		// message sent during the pause waits for an inbox nobody will run.
		go h.flushPending(a.Name(), config.DeliveryPush, config.DeliveryDefer)
	}
}

// Paused reports the reason deliveries are held, empty when they are not.
func (h *Hub) Paused() string {
	h.pauseMu.RLock()
	defer h.pauseMu.RUnlock()
	return h.paused
}

// brief types an agent's standing message, once per launch, when it first falls
// quiet. Waiting matters: at the moment a process starts, an agent CLI has not
// drawn its prompt, and text typed into one still painting is lost.
func (h *Hub) brief(name string) {
	a, err := h.Agent(name)
	if err != nil || a.Config().Message == "" || h.Paused() != "" {
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
	h.flushPending(name, config.DeliveryDefer)
}

// flushPending types everything waiting for an agent whose delivery is one of
// modes, as a single injection.
func (h *Hub) flushPending(name string, modes ...string) {
	if h.Paused() != "" {
		return
	}
	a, err := h.Agent(name)
	if err != nil {
		return
	}
	wanted := func(mode string) bool {
		for _, m := range modes {
			if m == mode {
				return true
			}
		}
		return false
	}
	var pending []bus.Message
	for _, m := range h.bus.Collect(name, true) {
		if wanted(h.deliveryFor(a, m.Kind)) {
			pending = append(pending, m)
		}
	}
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

// StartOutgoing wires the outgoing webhook, if the configuration asks for one.
// It is separate from New because the trace file belongs to the caller that
// opened it, and because a hub with no endpoint should not open a socket to
// find that out.
func (h *Hub) StartOutgoing(trace *hook.Log) error {
	o := h.cfg.Outgoing
	if !o.Enabled {
		return nil
	}
	s, err := hook.NewSender(hook.OutOptions{
		URL:             o.URL,
		Rules:           o.Rules,
		Secret:          o.Secret,
		SignatureHeader: o.SignatureHeader,
		Token:           o.Token,
		Timeout:         o.Timeout,
		Retries:         o.Retries,
		RetryBackoff:    o.RetryBackoff,
		Queue:           o.Queue,
		Trace:           trace,
		Emit:            func(text string) { h.log.Emit(event.KindError, "", text) },
	})
	if err != nil {
		return err
	}
	h.sender = s

	// The same stream the TUI and `swarm events` read, so a rule can match
	// anything either of them shows.
	events, cancel := h.log.Subscribe(256)
	h.outCancel = cancel
	go h.watchEvents(events)
	return nil
}

// Shutdown stops every agent, giving each one the same grace period.
func (h *Hub) Shutdown(grace time.Duration) {
	// Before the agents go: their last events deserve to leave, and the sender
	// drains what is queued.
	h.stalledOnce.Do(func() {
		if h.stalledStop != nil {
			close(h.stalledStop)
		}
	})
	// Closing this writes the debts out one last time, on the way past: a
	// shutdown is the most likely reason anyone will want them back.
	h.spawnOnce.Do(func() {
		if h.spawnStop != nil {
			close(h.spawnStop)
			<-h.spawnDone
		}
	})
	h.lifeOnce.Do(func() {
		if h.lifeStop != nil {
			close(h.lifeStop)
			<-h.lifeDone
		}
	})
	h.owedOnce.Do(func() {
		if h.owedStop == nil {
			return
		}
		close(h.owedStop)
		// Waited for, not just signalled: the last write happens on the way
		// out, and a caller that tears down the state directory as soon as
		// Shutdown returns would otherwise race a file being created inside it.
		select {
		case <-h.owedDone:
		case <-time.After(2 * time.Second):
			h.log.Emit(event.KindError, "", "timed out writing what is owed")
		}
	})
	if h.outCancel != nil {
		h.outCancel()
	}
	h.sender.Close()

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
	return results(agents, func(a *agent.Agent) (string, error) {
		// Stopping an instance is collecting it. A declared agent that is
		// stopped stays in the fleet and can be started again; an instance
		// cannot — it would come back with no memory of the task it still owes
		// — so leaving it listed would leave a corpse nothing could revive.
		if _, ok := h.Ephemeral(a.Name()); ok {
			return "collected", h.Collect(a.Name(), "stopped")
		}
		return "stopped", a.Stop(grace)
	}), nil
}

// Restart restarts every agent matching target.
func (h *Hub) Restart(target string, grace time.Duration) ([]TargetResult, error) {
	agents, err := h.Resolve(target)
	if err != nil {
		return nil, err
	}
	return results(agents, func(a *agent.Agent) (string, error) {
		// An instance restarted would come back with no memory of the work and
		// the same debt still open — a worse state than being dead, and one
		// nothing would ever get it out of. Spawn another instead.
		if template, ok := h.Ephemeral(a.Name()); ok {
			return "", fmt.Errorf("%s is an ephemeral instance and cannot be restarted: "+
				"it would return knowing nothing of the task it still owes. "+
				"`swarm spawn %s \"…\"` makes a new one", a.Name(), template)
		}
		return "restarted", a.Restart(grace)
	}), nil
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
	return h.SendKind(from, target, bus.KindNote, body, files)
}

// Done settles what an agent was asked, and tells whoever asked. It is the only
// way a request can end when there is nothing to answer — the work is finished,
// or there turned out to be none after looking.
func (h *Hub) Done(from string, thread uint64, note string) (settled int, out []bus.Message, err error) {
	if from == "" {
		return 0, nil, errors.New("done needs an agent name (set SWARM_AGENT or pass one)")
	}
	closed := h.bus.Settle(from, thread)
	if len(closed) == 0 {
		// Not an error: an agent may report work it was given by hand, and
		// being told is better than being right.
		h.log.Emit(event.KindInfo, from, "done, with nothing outstanding")
		return 0, nil, nil
	}

	body := note
	if body == "" {
		body = "done"
	}
	for _, d := range closed {
		// Whoever asked may not be reachable on the bus — the user is not an
		// agent, and a webhook is a name, not a mailbox. The debt is settled
		// all the same: it was owed to the fleet, not to a mailbox.
		if d.From == "" || d.From == from {
			continue
		}
		if _, isAgent := h.cfg.Agent(d.From); !isAgent {
			continue
		}
		msgs, err := h.SendOn(from, d.From, bus.KindDone, body, nil,
			SendOptions{Thread: d.Thread})
		if err != nil {
			// Reporting must not fail because the far end cannot be written to.
			h.log.Emit(event.KindError, from, "done: telling "+d.From+": "+err.Error())
			continue
		}
		out = append(out, msgs...)
	}
	h.log.Emit(event.KindInfo, from, fmt.Sprintf("done: settled %d", len(closed)))

	// Outwards too, and this is the only place it can come from: done is
	// declared, so the declaration is the event. Deducing it from the disk was
	// what this replaced.
	data := map[string]string{"settled": fmt.Sprint(len(closed))}
	if len(closed) > 0 {
		data["thread"] = fmt.Sprint(closed[0].Thread)
		data["asked_by"] = closed[0].From
		data["asked_kind"] = string(closed[0].Kind)
	}
	if a, err := h.Agent(from); err == nil {
		if st, ok := a.GitState(); ok {
			data["branch"] = st.Branch
			data["dirty"] = fmt.Sprint(st.Dirty)
			data["ahead"] = fmt.Sprint(st.Ahead)
		}
	}
	h.notify(from, OutDone, body, data)

	// An ephemeral instance saying it has finished is an ephemeral instance
	// saying it is done existing: it was made for this one task. Collected
	// after the reporting above, so whoever asked hears about the work before
	// hearing that the agent is gone.
	if _, ok := h.Ephemeral(from); ok {
		if err := h.Collect(from, "finished"); err != nil {
			h.log.Emit(event.KindError, from, "could not collect: "+err.Error())
		}
	}
	return len(closed), out, nil
}

// ackLine is what to append to a message that opens a debt: the exact command
// that settles it, thread and all.
func ackLine(kind bus.Kind, from string, thread uint64) string {
	switch kind {
	case bus.KindQuestion:
		return "\n\n[swarm] answer with: " + SettleCommand(kind, from, thread)
	case bus.KindRequest, bus.KindBlocked:
		return "\n\n[swarm] when this is settled: " + SettleCommand(kind, from, thread) +
			" (or answer with -kind answer if there is something to say)"
	}
	return ""
}

// SettleCommand is the exact command that closes a debt.
//
// One function, because it is now said in two places — appended to the message
// that opens the debt, and printed by `swarm why` days later when that message
// is long gone — and two spellings of a command is one spelling that is wrong.
//
// The flags come before the target on purpose: Go's flag parsing stops at the
// first non-flag argument, so `swarm send dev-1 -kind answer` passes "-kind"
// and "answer" through as message text. A command printed as advice has to be
// one that runs.
func SettleCommand(kind bus.Kind, from string, thread uint64) string {
	if kind == bus.KindQuestion {
		return fmt.Sprintf("swarm send -kind answer -thread %d %s \"…\"", thread, from)
	}
	return fmt.Sprintf("swarm done -thread %d", thread)
}

// threadFor decides which conversation a message belongs to, and refuses when
// that conversation has run out of turns.
//
// This is the mechanism the bus was missing: a message had no thread, so
// nothing could be too long, so nothing ended. The agent is not asked to
// understand any of it — it reads a refusal on stderr, and the refusal says
// what to do instead.
func (h *Hub) threadFor(from string, o SendOptions) (uint64, error) {
	if o.NewThread {
		return h.bus.NewThread(), nil
	}
	thread := o.Thread
	if thread == 0 {
		// Inherited from whatever this agent was last written to on. The user
		// and webhooks open a new conversation instead: they are starting
		// something, not continuing it.
		if _, isAgent := h.cfg.Agent(from); isAgent {
			if t, ok := h.bus.ThreadFor(from); ok {
				thread = t
			}
		}
	}
	if thread == 0 {
		return h.bus.NewThread(), nil
	}

	if last, ok := h.bus.LastTo(thread, from); ok && last.Final {
		return 0, fmt.Errorf("that was a final answer on this thread; act on it or escalate to %s",
			h.escalationTarget())
	}

	budget := h.cfg.Bus.MaxTurns
	if budget <= 0 {
		return thread, nil
	}
	if turns := h.bus.Turns(thread); turns >= budget {
		go h.escalate(thread, from)
		return 0, fmt.Errorf("this thread has used its %d turns; decide alone or escalate to %s",
			budget, h.escalationTarget())
	}
	return thread, nil
}

// escalationTarget names who arbitrates, or the user when nobody does.
func (h *Hub) escalationTarget() string {
	if h.cfg.Bus.EscalateTo != "" {
		return h.cfg.Bus.EscalateTo
	}
	return "the user"
}

// escalate hands a saturated thread to an arbiter, with what was said. The
// answer is expected to come back final, which is what turns an escalation into
// an ending rather than a third opinion.
func (h *Hub) escalate(thread uint64, from string) {
	if h.cfg.Bus.EscalateTo == "" {
		h.log.Emit(event.KindPattern, from,
			fmt.Sprintf("thread #%d ran out of turns and there is no escalate_to", thread))
		return
	}
	h.escalateMu.Lock()
	if h.escalated == nil {
		h.escalated = map[uint64]bool{}
	}
	if h.escalated[thread] {
		h.escalateMu.Unlock()
		return
	}
	h.escalated[thread] = true
	h.escalateMu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "[swarm] thread #%d ran out of turns and needs a decision.\n\n", thread)
	for _, m := range h.bus.All() {
		if m.Thread == thread {
			fmt.Fprintf(&b, "%s → %s: %s\n", m.From, m.To, summarize(m.Body))
		}
	}
	b.WriteString("\nAnswer with `swarm send --final`, to whoever should act on it.")

	if _, err := h.SendOn("swarm", h.cfg.Bus.EscalateTo, bus.KindQuestion, b.String(), nil,
		SendOptions{NewThread: true}); err != nil {
		h.log.Emit(event.KindError, "", "escalation failed: "+err.Error())
	}
}

// deliveryFor is how a message reaches an agent: what the agent asked for,
// unless the kind of message overrides it.
func (h *Hub) deliveryFor(a *agent.Agent, kind bus.Kind) string {
	if mode, ok := h.cfg.DeliveryByKind[string(kind)]; ok && mode != "" {
		return mode
	}
	return a.Config().DeliveryMode
}

// SendKind delivers a classified message.
func (h *Hub) SendKind(from, target string, kind bus.Kind, body string, files []string) ([]bus.Message, error) {
	return h.SendOn(from, target, kind, body, files, SendOptions{})
}

// SendOptions carries what is not the message itself.
type SendOptions struct {
	// Final refuses anyone the right to answer. A decision that can be
	// reopened is not a decision.
	Final bool
	// Thread continues an existing conversation. Zero means "the one this
	// agent was last written to on", which is what makes replies inherit a
	// thread without anybody tracking an identifier.
	Thread uint64
	// NewThread starts a fresh conversation even when the sender is in one.
	NewThread bool
	// Push types the message into the recipient's terminal whatever delivery
	// mode it is configured for. It is an interruption, and it is meant to be:
	// the messages that use it are the ones aimed at an agent that has stopped
	// reading its mailbox, where waiting politely in a queue means never
	// arriving. A paused bus still holds it — pausing is the one instruction
	// that outranks everything, or it would not be a pause.
	Push bool
}

// SendOn delivers a message on a thread, applying whatever bounds the
// configuration puts on a conversation.
func (h *Hub) SendOn(from, target string, kind bus.Kind, body string, files []string, o SendOptions) ([]bus.Message, error) {
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
	// Refused before anything is delivered, so a message either reaches all its
	// recipients or none: half a broadcast is worse than none.
	var refused []string
	for _, a := range agents {
		if a.Name() == from {
			continue
		}
		if ok, why := h.cfg.MayReach(from, a.Name()); !ok {
			refused = append(refused, why)
		}
	}
	if len(refused) > 0 {
		return nil, errors.New(refused[0])
	}

	thread, err := h.threadFor(from, o)
	if err != nil {
		return nil, err
	}
	// A message that asks for something says how to settle it. Left to guess,
	// an agent guesses — and sometimes wrongly, which costs a turn to find out
	// and leaves the debt open in the meantime. The demand carries the way to
	// close it, as a refusal carries the way to proceed.
	if ack := ackLine(kind, from, thread); ack != "" {
		body += ack
	}

	// Said one turn early, so the recipient can spend the last one on an
	// answer rather than discovering the budget by being refused.
	if budget := h.cfg.Bus.MaxTurns; budget > 0 && h.bus.Turns(thread) == budget-1 {
		body += fmt.Sprintf("\n\n[swarm] last turn on this thread — answer, decide, or escalate to %s.",
			h.escalationTarget())
	}
	out := make([]bus.Message, 0, len(agents))
	for _, a := range agents {
		if a.Name() == from && !h.cfg.Bus.AllowSelfInject {
			continue
		}
		msg := h.bus.Post(bus.Message{
			Thread: thread,
			From:   from,
			To:     a.Name(),
			Kind:   kind,
			Final:  o.Final,
			Body:   body,
			Files:  files,
		})
		mode := h.deliveryFor(a, kind)
		if o.Push {
			mode = config.DeliveryPush
		}
		// A deferred recipient that is already quiet gets it now: waiting for a
		// transition that has already happened would hold the message until the
		// agent next did some work, which is the opposite of the intent.
		if h.Paused() != "" {
			// Queued and left there: a paused bus still records what happened,
			// it just stops interrupting anybody with it.
			mode = config.DeliveryPull
		}
		if mode == config.DeliveryDefer && a.Info().State == agent.StateIdle {
			go h.flushDeferred(a.Name())
		}
		if mode == config.DeliveryPush {
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
