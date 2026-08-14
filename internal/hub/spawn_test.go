package hub

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/config"
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
