package hub

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/config"
	"github.com/emmanuel-deloget/swarm/internal/workspace"
)

// An ephemeral instance is an ordinary agent whose life is the length of one
// task. What these tests hold to is that the task and the life are the same
// thing: it is born owing the work, and saying the work is done is how it ends.

const spawnFleet = `
web: {enabled: false}
bus: {stalled_after: 300ms}
defaults: {idle_after: 100ms}
agents:
  - name: triage
    command: [probe-echo]
    can_spawn: [worker]
  - name: worker
    ephemeral: true
    command: [probe-echo]
    max_alive: 2
`

func TestATemplateIsNotAnAgent(t *testing.T) {
	h := fleet(t, spawnFleet)

	for _, name := range h.Names() {
		if name == "worker" {
			t.Error("the template is in the fleet; it is the shape of an agent, not one")
		}
	}
	if _, err := h.Agent("worker"); err == nil {
		t.Error("the template can be looked up as an agent")
	}
}

func TestSpawningGivesTheInstanceItsTaskAsADebt(t *testing.T) {
	h := fleet(t, spawnFleet)

	name, err := h.Spawn("triage", "worker", "take ticket 219")
	if err != nil {
		t.Fatal(err)
	}
	if name != "worker-1" {
		t.Errorf("first instance is called %q", name)
	}

	// The task is a debt, which is what makes everything else work without
	// being written twice.
	w, err := h.Why(name)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Debts) != 1 {
		t.Fatalf("a fresh instance owes %d things, want its task", len(w.Debts))
	}
	if !strings.Contains(w.Debts[0].Text, "take ticket 219") {
		t.Errorf("the debt does not carry the task: %q", w.Debts[0].Text)
	}
	if w.Debts[0].From != "triage" {
		t.Errorf("the task came from %q", w.Debts[0].From)
	}
}

// Saying the work is finished is saying the agent is finished: it was made for
// that one task.
func TestDoneCollectsTheInstance(t *testing.T) {
	h := fleet(t, spawnFleet)

	name, err := h.Spawn("triage", "worker", "take ticket 219")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.Done(name, 0, "pushed as rq-219"); err != nil {
		t.Fatal(err)
	}

	if _, err := h.Agent(name); err == nil {
		t.Error("the instance is still in the fleet after saying it had finished")
	}
	if _, ok := h.Ephemeral(name); ok {
		t.Error("the instance is still counted as alive")
	}
	// And its parent hears about it, in a message that stands on its own: a
	// parent with can_spawn restarts on exit, so the one reading may never have
	// known the instance existed.
	var told string
	for _, m := range h.bus.History("triage", -1) {
		if strings.Contains(m.Body, name) {
			told = m.Body
		}
	}
	if told == "" {
		t.Fatal("the parent was not told its instance is gone")
	}
	if !strings.Contains(told, "take ticket 219") {
		t.Errorf("the message does not say what the instance was asked:\n%s", told)
	}
}

func TestAnInstanceCannotBeRestarted(t *testing.T) {
	h := fleet(t, spawnFleet)

	name, err := h.Spawn("triage", "worker", "something")
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.Restart(name, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Error == "" {
		t.Fatalf("restarting an instance was allowed: %+v", res)
	}
	if !strings.Contains(res[0].Error, "spawn") {
		t.Errorf("the refusal does not say what to do instead: %s", res[0].Error)
	}
}

func TestMaxAliveIsACeiling(t *testing.T) {
	h := fleet(t, spawnFleet)

	for i := range 2 {
		if _, err := h.Spawn("triage", "worker", "task"); err != nil {
			t.Fatalf("instance %d: %v", i+1, err)
		}
	}
	if _, err := h.Spawn("triage", "worker", "one too many"); err == nil {
		t.Error("max_alive: 2 allowed a third instance")
	}

	// Collecting one makes room again.
	if err := h.Collect("worker-1", "stopped"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Spawn("triage", "worker", "now there is room"); err != nil {
		t.Errorf("no room after collecting one: %v", err)
	}
}

// Names are never reused: the bus, the debts and the logs all refer to them, and
// a worker-1 that meant two different agents would make the history false.
func TestNamesAreNotReused(t *testing.T) {
	h := fleet(t, spawnFleet)

	first, err := h.Spawn("triage", "worker", "one")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Collect(first, "stopped"); err != nil {
		t.Fatal(err)
	}
	second, err := h.Spawn("triage", "worker", "two")
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Errorf("the name %q was handed out twice", second)
	}
}

func TestOnlyDeclaredSpawnersMaySpawn(t *testing.T) {
	h := fleet(t, `
web: {enabled: false}
agents:
  - name: triage
    command: [probe-echo]
    can_spawn: [worker]
  - name: dev-1
    command: [probe-echo]
  - name: worker
    ephemeral: true
    command: [probe-echo]
`)
	if _, err := h.Spawn("dev-1", "worker", "task"); err == nil {
		t.Error("an agent with no can_spawn was allowed to spawn")
	}
	// The user always may.
	if _, err := h.Spawn("", "worker", "task"); err != nil {
		t.Errorf("the user was refused: %v", err)
	}
}

func TestSpawningNeedsATask(t *testing.T) {
	h := fleet(t, spawnFleet)

	if _, err := h.Spawn("triage", "worker", "   "); err == nil {
		t.Error("an instance was made with nothing asked of it, so nothing would collect it")
	}
}

// The template's name is a group of whatever is alive, which is what you write
// into another agent's can_send.
func TestTheTemplateNamesItsLiveInstances(t *testing.T) {
	h := fleet(t, spawnFleet)

	if _, _, err := h.resolveEphemeral("@worker"); err == nil {
		t.Error("sending to a template with nothing running was allowed to succeed quietly")
	}

	a, err := h.Spawn("triage", "worker", "one")
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.Spawn("triage", "worker", "two")
	if err != nil {
		t.Fatal(err)
	}
	agents, err := h.Resolve("@worker")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 {
		t.Fatalf("@worker resolved to %d agents", len(agents))
	}
	got := map[string]bool{agents[0].Name(): true, agents[1].Name(): true}
	if !got[a] || !got[b] {
		t.Errorf("@worker resolved to %v", got)
	}
}

// A parent and its instance can reach each other without anyone declaring it:
// the instance did not exist when the file was written.
func TestParentAndInstanceCanTalk(t *testing.T) {
	h := fleet(t, spawnFleet)

	name, err := h.Spawn("triage", "worker", "task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.SendKind(name, "triage", bus.KindQuestion, "which branch?", nil); err != nil {
		t.Errorf("an instance cannot reach the agent that spawned it: %v", err)
	}
}

// Spawned by a person, an instance that dies owing something keeps the debt:
// there is no parent to tell, and a debt that vanishes is how someone concludes
// the work was done.
func TestADebtOutlivesAnInstanceNobodySpawned(t *testing.T) {
	h := fleet(t, spawnFleet)

	name, err := h.Spawn("", "worker", "task from a person")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Collect(name, "stopped"); err != nil {
		t.Fatal(err)
	}
	if n := len(h.bus.Owed(name)); n == 0 {
		t.Error("the debt was cleared with nobody told about it")
	}
	if _, ok := h.Dead(name); !ok {
		t.Error("nothing remembers the instance existed, so its debt points at nobody")
	}
}

// An instance is collected when it says it has finished, which assumes it says
// something. One that never does would hold a slot against max_alive for ever.
func TestAnInstanceThatNeverFinishesIsCollected(t *testing.T) {
	h := fleet(t, `
web: {enabled: false}
defaults: {idle_after: 100ms}
agents:
  - name: triage
    command: [probe-echo]
    can_spawn: [worker]
  - name: worker
    ephemeral: true
    command: [probe-tick]
    max_lifetime: 400ms
`)
	name, err := h.Spawn("triage", "worker", "something that never ends")
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if _, alive := h.Ephemeral(name); !alive {
			// And its parent is told what happened, not left to notice.
			var told string
			for _, m := range h.bus.History("triage", -1) {
				if strings.Contains(m.Body, name) {
					told = m.Body
				}
			}
			if !strings.Contains(told, "max_lifetime") {
				t.Errorf("the parent was not told why it went:\n%s", told)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("an instance past its max_lifetime is still running")
}

func TestLifetimeTickIsDerivedFromTheShortest(t *testing.T) {
	tick := func(limits ...time.Duration) time.Duration {
		c := &config.Config{}
		for i, l := range limits {
			c.Agents = append(c.Agents, config.AgentConfig{
				Name: fmt.Sprint(i), Ephemeral: true, MaxLifetime: l,
			})
		}
		return lifetimeTick(c)
	}

	// A quarter of the shortest: a two-minute limit enforced a minute late is
	// not a limit.
	if got := tick(2*time.Hour, 2*time.Minute); got != 30*time.Second {
		t.Errorf("tick is %s, want a quarter of the shortest", got)
	}
	// Capped, so a fleet of long-lived instances is not polled all day.
	if got := tick(8 * time.Hour); got != time.Minute {
		t.Errorf("tick is %s, want the cap", got)
	}
	// And floored, so a limit measured in seconds is still enforced.
	if got := tick(400 * time.Millisecond); got != 250*time.Millisecond {
		t.Errorf("tick is %s, want the floor", got)
	}
	// Nothing to watch, no watcher.
	if got := tick(); got != 0 {
		t.Errorf("a fleet with no lifetimes watches anyway: %s", got)
	}
}

// Stopping an instance collects it. A declared agent that is stopped can be
// started again; an instance cannot, so leaving it listed would leave a corpse
// in the fleet that nothing could revive.
func TestStoppingAnInstanceCollectsIt(t *testing.T) {
	h := fleet(t, spawnFleet)

	name, err := h.Spawn("triage", "worker", "task")
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.Stop(name, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("stop failed: %+v", res)
	}
	if _, err := h.Agent(name); err == nil {
		t.Error("the instance is still in the fleet after being stopped")
	}
	if _, ok := h.Ephemeral(name); ok {
		t.Error("the instance is still counted as alive")
	}
	// A declared agent, on the other hand, stays where it is.
	if _, err := h.Stop("triage", time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Agent("triage"); err != nil {
		t.Error("stopping a declared agent took it out of the fleet")
	}
}

// Names must not come back after a restart. The bus, the debts and the logs all
// refer to agents by name, and the debts now survive a restart — which is
// exactly when a repeated name would put two agents' history under one.
func TestInstanceNamesSurviveARestart(t *testing.T) {
	dir := t.TempDir()

	first := fleetIn(t, dir, spawnFleet)
	a, err := first.Spawn("triage", "worker", "one")
	if err != nil {
		t.Fatal(err)
	}
	first.Shutdown(2 * time.Second)

	second := fleetIn(t, dir, spawnFleet)
	defer second.Shutdown(2 * time.Second)

	b, err := second.Spawn("triage", "worker", "two")
	if err != nil {
		t.Fatal(err)
	}
	if b == a {
		t.Errorf("after a restart the name %q was handed out again", b)
	}
}

// An instance alive when the swarm stops dies with its pty, and something has
// to remember it existed: its debt outlives it, and `swarm why` must be able to
// say the agent is dead rather than that it never was.
func TestAnInstanceAliveAtShutdownIsRemembered(t *testing.T) {
	dir := t.TempDir()

	first := fleetIn(t, dir, spawnFleet)
	name, err := first.Spawn("", "worker", "interrupted work")
	if err != nil {
		t.Fatal(err)
	}
	first.Shutdown(2 * time.Second)

	second := fleetIn(t, dir, spawnFleet)
	defer second.Shutdown(2 * time.Second)

	gone, ok := second.Dead(name)
	if !ok {
		t.Fatalf("%s is not remembered after the swarm stopped", name)
	}
	if gone.Task != "interrupted work" {
		t.Errorf("what it was asked came back as %q", gone.Task)
	}
	if gone.Why == "" {
		t.Error("nothing says why it is gone")
	}
}

// A debt whose owner is gone is the reason `swarm why` has to answer about the
// dead: reported as never having existed, it reads as a typo rather than as a
// death, and nobody looks for the work again.
func TestWhyAnswersForAnInstanceThatIsGone(t *testing.T) {
	h := fleet(t, spawnFleet)

	name, err := h.Spawn("", "worker", "the work it never finished")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Collect(name, "stopped"); err != nil {
		t.Fatal(err)
	}

	w, err := h.Why(name)
	if err != nil {
		t.Fatalf("why refuses an instance that is gone: %v", err)
	}
	if w.Gone == nil {
		t.Fatal("the answer does not say the agent is gone")
	}
	if w.Gone.Task != "the work it never finished" {
		t.Errorf("it does not say what the instance was asked: %q", w.Gone.Task)
	}
	// The reason it went, not a word that means nothing: "gone: gone" tells a
	// reader whether it finished, was stopped, or ran out of time — which is
	// the first thing anyone wants to know.
	if w.Gone.Why != "stopped" {
		t.Errorf("why it went came back as %q", w.Gone.Why)
	}
	if len(w.Debts) != 1 {
		t.Fatalf("the debt that outlived it is not reported: %d", len(w.Debts))
	}
	if w.Debts[0].Settle == "" {
		t.Error("no way out is offered for a debt nobody can settle from inside")
	}

	// A name that was never anything is still refused: "nothing is wrong with
	// dev-99" is a bad thing to tell someone who misspelled dev-9.
	if _, err := h.Why("worker-99"); err == nil {
		t.Error("a name that never existed was explained rather than refused")
	}
}

// worktreeFleet is a fleet in a real repository, since worktrees need one.
func worktreeFleet(t *testing.T) (*Hub, string) {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "test"},
		{"config", "commit.gpgsign", "false"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("no usable git here: %v %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "a.txt"}, {"commit", "-qm", "first"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	h := fleetIn(t, dir, `
web: {enabled: false}
defaults: {idle_after: 100ms}
agents:
  - name: triage
    command: [probe-echo]
    can_spawn: [worker]
  - name: worker
    ephemeral: true
    command: [probe-echo]
    workspace: worktree
`)
	return h, dir
}

func TestAnInstanceGetsItsOwnWorktree(t *testing.T) {
	h, _ := worktreeFleet(t)

	a, err := h.Spawn("triage", "worker", "one")
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.Spawn("triage", "worker", "two")
	if err != nil {
		t.Fatal(err)
	}

	one, _ := h.Agent(a)
	two, _ := h.Agent(b)
	dirA, dirB := one.Config().Workdir, two.Config().Workdir
	if dirA == dirB {
		t.Fatalf("both instances were given the same directory: %s", dirA)
	}
	for _, d := range []string{dirA, dirB} {
		if _, err := os.Stat(filepath.Join(d, "a.txt")); err != nil {
			t.Errorf("%s is not a checkout: %v", d, err)
		}
	}
}

// The whole point of collecting: the branch cannot be checked out twice, so
// leaving the directory would stop anyone picking the work up.
func TestACollectedInstanceGivesItsWorktreeBack(t *testing.T) {
	h, _ := worktreeFleet(t)

	name, err := h.Spawn("triage", "worker", "task")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := h.Agent(name)
	dir := a.Config().Workdir

	if err := h.Collect(name, "stopped"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the worktree of a collected instance is still on disk")
	}
}

// And the refusal, which is git's: a worktree holding work nobody has committed
// is kept, and swarm says where it is.
func TestAWorktreeWithWorkInItIsKept(t *testing.T) {
	h, _ := worktreeFleet(t)

	name, err := h.Spawn("triage", "worker", "task")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := h.Agent(name)
	dir := a.Config().Workdir
	if err := os.WriteFile(filepath.Join(dir, "draft.txt"), []byte("hours of it\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := h.Collect(name, "stopped"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "draft.txt")); err != nil {
		t.Fatal("the work was deleted with the instance")
	}
	var said bool
	for _, e := range h.Log().History(-1) {
		if strings.Contains(e.Text, "worktree is kept") {
			said = true
		}
	}
	if !said {
		t.Error("a worktree was kept and nothing in the log says so")
	}
}

// A hub killed outright leaves worktrees on disk, locked. A lock set by a
// process that is gone is never released, so nobody can remove the worktree
// without knowing to unlock it first — and nothing said it was there.
func TestWorktreesLeftBehindAreReportedAndUnlocked(t *testing.T) {
	h, repo := worktreeFleet(t)

	name, err := h.Spawn("triage", "worker", "interrupted work")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := h.Agent(name)
	dir := a.Config().Workdir

	// The hub goes without collecting anything, as a kill -9 would leave it.
	h.Shutdown(2 * time.Second)

	second := fleetIn(t, repo, `
web: {enabled: false}
defaults: {idle_after: 100ms}
agents:
  - name: triage
    command: [probe-echo]
    can_spawn: [worker]
  - name: worker
    ephemeral: true
    command: [probe-echo]
    workspace: worktree
`)
	defer second.Shutdown(2 * time.Second)

	// Still there — deleting somebody's work at startup would be the worst
	// possible moment to decide it does not matter.
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the worktree was removed at startup: %v", err)
	}
	var said bool
	for _, e := range second.Log().History(-1) {
		if strings.Contains(e.Text, "from before this start") {
			said = true
		}
	}
	if !said {
		t.Error("a worktree was left behind and nothing says so")
	}

	// And it can now be removed, which a lock from a dead process would
	// otherwise prevent for good. Through swarm's own function, since Windows
	// needs the retry it carries.
	if err := workspace.RemoveWorktree(dir); err != nil {
		t.Errorf("the worktree is still locked by a process that is gone: %v", err)
	}
}

// The state directory reached through a symbolic link is the macOS case: git
// answers with /private/var where the caller holds /var, so a worktree of ours
// looks like somebody else's and is neither unlocked nor reported. Windows has
// the same shape with short and long path names.
func TestWorktreesAreRecognisedThroughASymlink(t *testing.T) {
	actual := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(actual, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "test"},
		{"config", "commit.gpgsign", "false"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", actual}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("no usable git here: %v %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(actual, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "a.txt"}, {"commit", "-qm", "first"}} {
		if out, err := exec.Command("git", append([]string{"-C", actual}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}

	body := `
web: {enabled: false}
defaults: {idle_after: 100ms}
agents:
  - name: triage
    command: [probe-echo]
    can_spawn: [worker]
  - name: worker
    ephemeral: true
    command: [probe-echo]
    workspace: worktree
`
	// The fleet is configured through the link, so its state directory is the
	// unresolved path throughout.
	first := fleetIn(t, link, body)
	if _, err := first.Spawn("triage", "worker", "interrupted"); err != nil {
		t.Fatal(err)
	}
	first.Shutdown(2 * time.Second)

	second := fleetIn(t, link, body)
	defer second.Shutdown(2 * time.Second)

	var said bool
	for _, e := range second.Log().History(-1) {
		if strings.Contains(e.Text, "from before this start") {
			said = true
		}
	}
	if !said {
		t.Error("a worktree reached through a link was not recognised as ours")
	}
}

// The fleet-wide ceiling stops what the per-template ones cannot see: each
// template within its own limit, and the machine past its.
func TestTheFleetCeilingStopsSpawning(t *testing.T) {
	h := fleet(t, `
web: {enabled: false}
defaults: {idle_after: 100ms}
ephemeral: {max_alive: 2}
agents:
  - name: triage
    command: [probe-echo]
    can_spawn: [worker, helper]
  - name: worker
    ephemeral: true
    command: [probe-echo]
    max_alive: 5
  - name: helper
    ephemeral: true
    command: [probe-echo]
    max_alive: 5
`)
	if _, err := h.Spawn("triage", "worker", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Spawn("triage", "helper", "two"); err != nil {
		t.Fatal(err)
	}
	// Neither template is anywhere near its own limit of five.
	_, err := h.Spawn("triage", "worker", "three")
	if err == nil {
		t.Fatal("the fleet ceiling of 2 allowed a third agent")
	}
	if !strings.Contains(err.Error(), "across the fleet") {
		t.Errorf("the refusal does not say which limit was met: %v", err)
	}

	// Collecting one makes room again, whichever template it belonged to.
	if err := h.Collect("helper-1", "stopped"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Spawn("triage", "worker", "three"); err != nil {
		t.Errorf("no room after collecting one: %v", err)
	}
}
