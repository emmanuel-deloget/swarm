package hub

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/agent"
	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/config"
	"github.com/emmanuel-deloget/swarm/internal/event"
	"github.com/emmanuel-deloget/swarm/internal/workspace"
)

// Ephemeral agents.
//
// A template is an entry with `ephemeral: true`: not an agent, but the shape of
// one. `swarm spawn <template> "<task>"` makes an instance from it, which runs
// that one task and is collected when it says it has finished.
//
// This is what gives a working copy an owner without swarm growing a notion of
// task. IDEAS/WORKTREES.md refused the per-task workspace because it had
// nothing to attach to — when is it created, when destroyed, what does it
// belong to — and an ephemeral agent answers all three with the object swarm
// already has.
//
// Instances live here rather than in the configuration. The configuration is
// what someone wrote and can reread; a file that quietly grew four agents while
// the fleet ran would describe nothing, and it is read from several goroutines
// without being guarded for it. So the hub keeps them, and the few places that
// resolve names ask the hub as well.

// ephemeral tracks the instances of one template that are alive.
type instance struct {
	// Config is the instance's own copy, made from the template.
	Config *config.AgentConfig
	// Template is what it was made from.
	Template string
	// Parent is the agent that spawned it, empty when a person did.
	Parent string
	// Task is what it was asked to do, kept so its death can say what it was
	// on and its parent can be told in a sentence that stands alone.
	Task string
	// Thread is the conversation its task was opened on.
	Thread uint64
	Born   time.Time
}

// spawner holds every instance and what they were named.
type spawner struct {
	mu    sync.Mutex
	live  map[string]*instance
	next  map[string]int
	shim  string
	gone  []Gone
	dirty bool
}

// Gone is an instance that is no longer running, kept so that a debt left
// behind has something to point at: `swarm why` on a dead agent has to be able
// to say it is dead rather than that it never existed.
type Gone struct {
	Name     string    `json:"name"`
	Template string    `json:"template"`
	Parent   string    `json:"parent,omitempty"`
	Task     string    `json:"task,omitempty"`
	Thread   uint64    `json:"thread,omitempty"`
	Born     time.Time `json:"born"`
	Died     time.Time `json:"died"`
	Why      string    `json:"why"`
}

// goneKept bounds the record of the dead. Enough to answer for what happened
// this session, not a permanent archive: what matters long-term is in the event
// log and in git.
const goneKept = 100

// Spawn makes an instance of a template and gives it its task.
//
// The task arrives as a bus request rather than as a prompt on the command
// line, which is what makes everything else work without being written twice:
// `swarm why` says what the instance is on, on_stalled asks it where it is when
// it goes quiet, the debt survives a restart of the hub, and `swarm done` — the
// agent saying it has finished — is what collects it.
func (h *Hub) Spawn(from, template, task string) (string, error) {
	if strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("spawn needs a task: an agent with nothing asked of it " +
			"has nothing to finish, so nothing would ever collect it")
	}
	tc, ok := h.cfg.Agent(template)
	if !ok || !tc.Ephemeral {
		return "", fmt.Errorf("%q is not an ephemeral template in this configuration", template)
	}
	if err := h.maySpawn(from, template); err != nil {
		return "", err
	}

	h.spawn.mu.Lock()
	alive := 0
	for _, in := range h.spawn.live {
		if in.Template == template {
			alive++
		}
	}
	if alive >= tc.MaxAlive {
		h.spawn.mu.Unlock()
		return "", fmt.Errorf("%d instances of %s are already running, which is its "+
			"max_alive; wait for one to finish or raise the limit", alive, template)
	}
	h.spawn.next[template]++
	name := fmt.Sprintf("%s-%d", template, h.spawn.next[template])
	h.spawn.dirty = true
	h.spawn.mu.Unlock()

	ac := instanceConfig(tc, name, from)
	if ac.Workspace == config.WorkspaceWorktree {
		// Its own directory, named after the instance. Inherited from the
		// template it would be the same one for every instance, and two agents
		// in one worktree is exactly what the mode exists to prevent.
		ac.Workdir = filepath.Join(h.stateDir, "worktrees", name)
	}
	a := h.newAgent(ac)

	h.mu.Lock()
	h.agents[name] = a
	h.order = append(h.order, name)
	h.mu.Unlock()

	in := &instance{Config: ac, Template: template, Parent: parentOf(from), Task: task, Born: time.Now()}
	h.spawn.mu.Lock()
	h.spawn.live[name] = in
	h.spawn.mu.Unlock()

	if err := a.Start(); err != nil {
		h.drop(name, "would not start")
		return "", fmt.Errorf("starting %s: %w", name, err)
	}
	h.log.Emit(event.KindInfo, name, fmt.Sprintf(
		"spawned from %s by %s", template, orString(from, "user")))

	// The task, as a debt. Failing here would leave an agent running with
	// nothing asked of it and no way to be collected, so it is fatal to the
	// spawn rather than a warning.
	msgs, err := h.SendOn(orString(from, "user"), name, bus.KindRequest, task, nil,
		SendOptions{NewThread: true, Push: true})
	if err != nil {
		h.drop(name, "could not be given its task")
		return "", fmt.Errorf("giving %s its task: %w", name, err)
	}
	if len(msgs) > 0 {
		h.spawn.mu.Lock()
		in.Thread = msgs[0].Thread
		h.spawn.mu.Unlock()
	}
	return name, nil
}

// maySpawn reports whether an agent is allowed to make instances of a template.
// The user always is; an agent only if the file says so, because the ability to
// create agents is the ability to spend.
func (h *Hub) maySpawn(from, template string) error {
	if from == "" || from == "user" {
		return nil
	}
	ac, ok := h.cfg.Agent(from)
	if !ok {
		// An instance spawning: it carries its template's can_spawn, which is
		// empty unless someone wrote it down.
		h.spawn.mu.Lock()
		in := h.spawn.live[from]
		h.spawn.mu.Unlock()
		if in == nil {
			return fmt.Errorf("%q is not an agent of this swarm", from)
		}
		ac = in.Config
	}
	for _, t := range ac.CanSpawn {
		if t == template {
			return nil
		}
	}
	if len(ac.CanSpawn) == 0 {
		return fmt.Errorf("%s may not spawn agents; add can_spawn to it in the "+
			"configuration if it should", from)
	}
	return fmt.Errorf("%s may spawn %s, not %s",
		from, strings.Join(ac.CanSpawn, ", "), template)
}

// instanceConfig makes an instance's own configuration from its template.
func instanceConfig(tc *config.AgentConfig, name, parent string) *config.AgentConfig {
	ac := *tc // a copy: the template must not learn anything from its instances
	ac.Name = name
	ac.Ephemeral = true

	// The parent and the instance can reach each other. Written into the
	// instance's own can_send rather than resolved later, so the rule is where
	// anyone would look for it — and the parent's side is handled by the hub,
	// since its configuration is what someone wrote and stays that way.
	if parent != "" && parent != "user" {
		ac.CanSend = append(append([]string{}, ac.CanSend...), parent)
		ac.Env = mergeEnv(ac.Env, map[string]string{"SWARM_PARENT": parent})
	}
	// An instance never restarts: it would come back with no memory of the work
	// and the same debt still open.
	ac.Autostart = ptrBool(false)
	ac.RestartOnExit = ptrBool(false)
	return &ac
}

func mergeEnv(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func ptrBool(b bool) *bool { return &b }

func parentOf(from string) string {
	if from == "user" {
		return ""
	}
	return from
}

// newAgent builds an agent the way New does, so an instance is in every respect
// an agent of this fleet.
func (h *Hub) newAgent(ac *config.AgentConfig) *agent.Agent {
	opts := agent.Options{
		Config:    ac,
		CloneFrom: h.cfg.Workdir,
		OnIdle: func() {
			h.brief(ac.Name)
			h.flushDeferred(ac.Name)
			h.notifyIdle(ac.Name)
		},
		Log:     h.log,
		Env:     h.agentEnv(ac, h.spawn.shim),
		LogFile: filepath.Join(h.stateDir, "logs", ac.Name+".log"),
	}
	if h.cfg.LogInput {
		opts.InputLogFile = filepath.Join(h.stateDir, "logs", ac.Name+".input.log")
	}
	return agent.New(opts)
}

// Collect ends an instance and takes it out of the fleet.
//
// why is what happened, in the words the event log and the parent will read:
// "finished", "max_lifetime", "stopped".
func (h *Hub) Collect(name, why string) error {
	h.spawn.mu.Lock()
	in := h.spawn.live[name]
	h.spawn.mu.Unlock()
	if in == nil {
		return fmt.Errorf("%s is not an ephemeral instance", name)
	}

	if a, err := h.Agent(name); err == nil {
		// A process that will not stop still leaves the fleet: what follows —
		// telling the parent, settling or keeping the debt — is what someone is
		// waiting for, and holding it back because a pty is stubborn would help
		// nobody. The failure is worth saying, not worth stopping for.
		if err := a.Stop(2 * time.Second); err != nil {
			h.log.Emit(event.KindError, name, "collecting: "+err.Error())
		}
	}

	// What it still owed. Someone asked for this work, and an agent
	// disappearing with it is how a person concludes it was done.
	var debts []debtView
	for _, d := range h.bus.Owed(name) {
		debts = append(debts, debtView{Thread: d.Thread, From: d.From, Kind: d.Kind, Since: d.Since, Body: d.Body})
	}
	h.settleOnDeath(in, debts, why)

	h.collectWorktree(in)
	h.drop(name, why)
	h.log.Emit(event.KindInfo, name, "collected: "+why)
	return nil
}

// collectWorktree takes back the working copy of an instance that is gone.
//
// Only what swarm made, and only for an instance: a declared agent keeps its
// workspace between runs, and a worktree an agent made for itself is none of
// swarm's business.
//
// Nothing here decides whether there is work to lose. `git worktree remove`
// refuses a directory holding modified or untracked files, and swarm never
// passes --force, so a refusal means somebody's work is in there — which is
// reported and left exactly where it is. The branch outlives the directory
// either way, and is only deleted when the remote already has every commit on
// it.
func (h *Hub) collectWorktree(in *instance) {
	if in.Config.Workspace != config.WorkspaceWorktree {
		return
	}
	dir := in.Config.Workdir
	repo, err := workspace.RepoRoot(dir)
	if err != nil {
		// Already gone, or never made: nothing to collect and nothing wrong.
		return
	}
	branch := workspace.BranchName(in.Config.Name)

	if err := workspace.RemoveWorktree(dir); err != nil {
		h.log.Emit(event.KindPattern, in.Config.Name, fmt.Sprintf(
			"its worktree is kept: %v — the branch %s still has it, and "+
				"`git worktree remove --force %s` is the only way to drop it",
			err, branch, dir))
		return
	}

	deleted, err := workspace.DeleteBranch(repo, branch)
	switch {
	case err != nil:
		h.log.Emit(event.KindError, in.Config.Name, "removing "+branch+": "+err.Error())
	case deleted:
		h.log.Emit(event.KindInfo, in.Config.Name, fmt.Sprintf(
			"worktree collected, and %s deleted: the remote has all of it", branch))
	default:
		h.log.Emit(event.KindInfo, in.Config.Name, fmt.Sprintf(
			"worktree collected; %s is kept, since the remote does not have "+
				"every commit on it", branch))
	}
}

// settleOnDeath decides what happens to what an instance still owed.
//
// Spawned by an agent: its parent is told, and only then is the debt cleared —
// in that order, because clearing first and failing to send loses the fact
// entirely. Spawned by a person: the debt stays, `swarm why` says the agent is
// dead, and it is cleared by hand. The difference is that an agent can be told
// and will act; a person has to find out.
func (h *Hub) settleOnDeath(in *instance, debts []debtView, why string) {
	if len(debts) == 0 {
		if in.Parent != "" {
			h.tellParent(in, why, "")
		}
		return
	}
	if in.Parent == "" {
		// Left standing on purpose. Said out loud so it is not a surprise
		// later.
		h.log.Emit(event.KindPattern, in.Config.Name, fmt.Sprintf(
			"died owing %d thing(s), and nobody spawned it to tell; "+
				"`swarm why %s` still answers, `swarm done -from %s` clears it",
			len(debts), in.Config.Name, in.Config.Name))
		return
	}
	h.tellParent(in, why, fmt.Sprintf("It still owed %d thing(s).", len(debts)))
	for _, d := range debts {
		h.bus.Settle(in.Config.Name, d.Thread)
	}
}

// tellParent writes the message that stands on its own.
//
// A parent with can_spawn restarts on exit, so the one reading this may be a
// fresh process that never knew the instance existed. It is told what the
// instance was, what it was asked, and what happened — the same rule as `swarm
// why`: never assume the reader remembers.
func (h *Hub) tellParent(in *instance, why, extra string) {
	var b strings.Builder
	fmt.Fprintf(&b, "[swarm] %s, the %s you spawned, is gone: %s.\n",
		in.Config.Name, in.Template, why)
	if in.Task != "" {
		fmt.Fprintf(&b, "\nIt was asked to:\n  %s\n", strings.TrimSpace(in.Task))
	}
	if extra != "" {
		b.WriteString("\n" + extra + "\n")
	}
	if _, err := h.SendOn(stallSender, in.Parent, bus.KindFYI, b.String(), nil,
		SendOptions{NewThread: true, Push: true}); err != nil {
		h.log.Emit(event.KindError, in.Config.Name,
			"could not tell "+in.Parent+" it is gone: "+err.Error())
	}
}

// drop removes an instance from the fleet and records that it existed, and
// why it stopped — which is what `swarm why` reads back for the dead.
func (h *Hub) drop(name, why string) {
	h.mu.Lock()
	delete(h.agents, name)
	for i, n := range h.order {
		if n == name {
			h.order = append(h.order[:i], h.order[i+1:]...)
			break
		}
	}
	h.mu.Unlock()

	h.spawn.mu.Lock()
	defer h.spawn.mu.Unlock()
	in := h.spawn.live[name]
	delete(h.spawn.live, name)
	if in == nil {
		return
	}
	h.spawn.gone = append(h.spawn.gone, Gone{
		Name: name, Template: in.Template, Parent: in.Parent, Task: in.Task,
		Thread: in.Thread, Born: in.Born, Died: time.Now(), Why: why,
	})
	if len(h.spawn.gone) > goneKept {
		h.spawn.gone = h.spawn.gone[len(h.spawn.gone)-goneKept:]
	}
	h.spawn.dirty = true
}

// Ephemeral reports whether a name is a live instance, and of what.
func (h *Hub) Ephemeral(name string) (template string, ok bool) {
	h.spawn.mu.Lock()
	defer h.spawn.mu.Unlock()
	in, live := h.spawn.live[name]
	if !live {
		return "", false
	}
	return in.Template, true
}

// Dead returns what is known about an instance that is no longer running.
func (h *Hub) Dead(name string) (Gone, bool) {
	h.spawn.mu.Lock()
	defer h.spawn.mu.Unlock()
	for i := len(h.spawn.gone) - 1; i >= 0; i-- {
		if h.spawn.gone[i].Name == name {
			return h.spawn.gone[i], true
		}
	}
	return Gone{}, false
}

// instancesOf returns the live instances of a template, in the order they were
// made, which is what @template resolves to.
func (h *Hub) instancesOf(template string) []string {
	h.spawn.mu.Lock()
	defer h.spawn.mu.Unlock()
	var out []string
	for name, in := range h.spawn.live {
		if in.Template == template {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func orString(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// resolveEphemeral resolves the targets the configuration cannot: a live
// instance by name, and @template for all of them.
//
// It returns nil when the target is not one of those, so the caller falls
// through to the configuration — the two never disagree about a name, because
// an instance's name contains a dash and a number that no declared agent may
// take.
//
// A template with no live instance is an error rather than an empty result.
// `swarm send @worker` that quietly reaches nobody looks exactly like one that
// worked, and the whole point of the group is to write it into someone's
// can_send and forget about it.
func (h *Hub) resolveEphemeral(target string) (names []string, handled bool, err error) {
	if name, ok := strings.CutPrefix(target, "@"); ok {
		tc, found := h.cfg.Agent(name)
		if !found || !tc.Ephemeral {
			return nil, false, nil
		}
		live := h.instancesOf(name)
		if len(live) == 0 {
			// An error rather than an empty result. `swarm send @worker` that
			// quietly reaches nobody looks exactly like one that worked, and
			// the point of the group is to be written into someone's can_send
			// and forgotten about.
			return nil, true, fmt.Errorf("no %s is running; `swarm spawn %s \"…\"` starts one",
				name, name)
		}
		return live, true, nil
	}
	h.spawn.mu.Lock()
	_, alive := h.spawn.live[target]
	h.spawn.mu.Unlock()
	if alive {
		return []string{target}, true, nil
	}
	return nil, false, nil
}

// lifetimeTick is how often instances are checked against their max_lifetime.
// Derived from the shortest one so a two-minute limit is not enforced a minute
// late, and bounded so a fleet of long-lived instances is not polled all day.
func lifetimeTick(cfg *config.Config) time.Duration {
	shortest := time.Duration(0)
	for i := range cfg.Agents {
		a := &cfg.Agents[i]
		if !a.Ephemeral || a.MaxLifetime <= 0 {
			continue
		}
		if shortest == 0 || a.MaxLifetime < shortest {
			shortest = a.MaxLifetime
		}
	}
	if shortest == 0 {
		return 0 // nothing to watch
	}
	return min(max(shortest/4, 250*time.Millisecond), time.Minute)
}

// watchLifetimes collects instances that have outlived their template's
// max_lifetime.
//
// An instance is collected when it says it has finished, which assumes it
// eventually says something. One that does not — wedged, waiting on something
// that will never arrive, or simply unaware it was supposed to report — is a
// permanent agent nobody declared, holding a slot against max_alive for ever.
// This is the part that does not depend on its cooperation.
func (h *Hub) watchLifetimes(every time.Duration) {
	defer close(h.lifeDone)
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-h.lifeStop:
			return
		case <-tick.C:
			h.reapExpired()
		}
	}
}

// reapExpired collects what has run out of time.
func (h *Hub) reapExpired() {
	type expired struct {
		name string
		age  time.Duration
		max  time.Duration
	}
	var out []expired

	h.spawn.mu.Lock()
	for name, in := range h.spawn.live {
		limit := in.Config.MaxLifetime
		if limit <= 0 {
			continue
		}
		if age := time.Since(in.Born); age >= limit {
			out = append(out, expired{name, age, limit})
		}
	}
	h.spawn.mu.Unlock()

	// Outside the lock: collecting takes it, and takes the fleet's as well.
	for _, e := range out {
		h.log.Emit(event.KindPattern, e.name, fmt.Sprintf(
			"ran for %s without finishing, past its max_lifetime of %s",
			e.age.Round(time.Second), e.max))
		if err := h.Collect(e.name, fmt.Sprintf("max_lifetime of %s reached", e.max)); err != nil {
			h.log.Emit(event.KindError, e.name, "collecting on lifetime: "+err.Error())
		}
	}
}

// reportOrphanWorktrees says what was left behind by a previous run.
//
// Instances die with their ptys; their worktrees do not. A hub killed outright
// leaves directories on disk, locked — and a lock set by a process that is gone
// is never released, so nobody can remove the worktree without knowing to
// unlock it first. Releasing those is the one thing done here.
//
// Nothing is deleted. What is in them may be hours of work, and a swarm
// starting up is the worst possible moment to decide otherwise: it has no idea
// yet what anybody wanted. So they are unlocked, named in the log with what
// they were for, and left alone.
func (h *Hub) reportOrphanWorktrees() {
	repo, err := workspace.RepoRoot(h.cfg.Workdir)
	if err != nil {
		return // not a repository: there can be no worktrees of ours
	}
	dirs, err := workspace.Worktrees(repo)
	if err != nil {
		h.log.Emit(event.KindError, "", err.Error())
		return
	}
	ours := filepath.Join(h.stateDir, "worktrees") + string(filepath.Separator)
	for _, dir := range dirs {
		if !strings.HasPrefix(dir, ours) {
			continue // somebody else's, and none of our business
		}
		name := filepath.Base(dir)
		// Its lock outlived the process that set it.
		if err := workspace.UnlockWorktree(dir); err == nil {
			h.log.Emit(event.KindInfo, name, "released the lock its worktree was left with")
		}

		what := "a worktree from before this start is still here: " + dir
		if gone, ok := h.Dead(name); ok && gone.Task != "" {
			what += fmt.Sprintf(" (it was asked to: %s)", strings.TrimSpace(gone.Task))
		}
		h.log.Emit(event.KindPattern, name, what+
			" — nothing was deleted; `git worktree remove` takes it when you are done with it")
	}
}
