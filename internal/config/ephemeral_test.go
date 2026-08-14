package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A template is not an agent: nothing is started for it, and `swarm spawn` makes
// instances from it. The checks here are about the shapes that cannot work,
// because each of them fails later and less clearly than at load.

const ephemeralBase = `
web: {enabled: false}
agents:
  - name: triage
    command: [cat]
    can_spawn: [worker]
  - name: worker
    ephemeral: true
    command: [cat]
`

func TestATemplateGetsItsDefaults(t *testing.T) {
	c, err := loadYAML(t, ephemeralBase)
	if err != nil {
		t.Fatal(err)
	}
	w, ok := c.Agent("worker")
	if !ok {
		t.Fatal("the template is not in the config")
	}
	if w.MaxAlive != DefaultMaxAlive {
		t.Errorf("max_alive defaults to %d", w.MaxAlive)
	}
	// An instance is created for one task and dies with it; bringing it back
	// with no memory of the work and the same debt open is worse than dead.
	if w.RestartOnExit == nil || *w.RestartOnExit {
		t.Error("a template restarts on exit")
	}
	if w.RestartMax != 0 {
		t.Errorf("restart_max is %d on a template", w.RestartMax)
	}
	if w.Autostart == nil || *w.Autostart {
		t.Error("a template autostarts, so the fleet would launch the shape of an agent")
	}
}

// An agent that launches ephemerals has to be there when they finish: their
// debts are settled by telling their parent.
func TestSpawningRequiresBeingThereWhenTheyFinish(t *testing.T) {
	c, err := loadYAML(t, ephemeralBase)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := c.Agent("triage")
	if a.RestartOnExit == nil || !*a.RestartOnExit {
		t.Error("an agent with can_spawn does not restart on exit")
	}
}

// And saying otherwise out loud is refused rather than quietly overridden:
// forcing it would make the file lie to whoever reads it next.
func TestSpawningWithRestartOffIsRefused(t *testing.T) {
	_, err := loadYAML(t, `
web: {enabled: false}
agents:
  - name: triage
    command: [cat]
    can_spawn: [worker]
    restart_on_exit: false
  - name: worker
    ephemeral: true
    command: [cat]
`)
	if err == nil {
		t.Fatal("can_spawn with restart_on_exit: false was accepted")
	}
	if !strings.Contains(err.Error(), "nobody to go back to") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func TestSpawningSomethingThatIsNotATemplateIsRefused(t *testing.T) {
	_, err := loadYAML(t, `
web: {enabled: false}
agents:
  - name: triage
    command: [cat]
    can_spawn: [dev-1]
  - name: dev-1
    command: [cat]
`)
	if err == nil {
		t.Fatal("can_spawn naming an ordinary agent was accepted")
	}
	if !strings.Contains(err.Error(), "not an ephemeral template") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
}

func TestTemplateOnlySettingsAreRefusedElsewhere(t *testing.T) {
	for _, line := range []string{"max_alive: 2", "max_lifetime: 1h"} {
		_, err := loadYAML(t, `
web: {enabled: false}
agents:
  - name: dev-1
    command: [cat]
    `+line+`
`)
		if err == nil {
			t.Errorf("%q was accepted on an ordinary agent", line)
		}
	}
}

func TestATemplateMayKeepALifetime(t *testing.T) {
	c, err := loadYAML(t, `
web: {enabled: false}
agents:
  - name: worker
    ephemeral: true
    command: [cat]
    max_lifetime: 90m
    max_alive: 5
`)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := c.Agent("worker")
	if w.MaxLifetime != 90*time.Minute {
		t.Errorf("max_lifetime came out as %s", w.MaxLifetime)
	}
	if w.MaxAlive != 5 {
		t.Errorf("max_alive came out as %d", w.MaxAlive)
	}
}

// `workspace: worktree` needs a repository to make one in. Refused at load
// rather than when the agent starts: a fleet that half-launches and then says
// four agents have no working copy is a worse way to find out.
func TestWorktreeNeedsARepository(t *testing.T) {
	// t.TempDir is not a repository.
	_, err := loadYAML(t, `
web: {enabled: false}
agents:
  - name: worker
    ephemeral: true
    command: [cat]
    workspace: worktree
`)
	if err == nil {
		t.Fatal("workspace: worktree was accepted outside a git repository")
	}
	if !strings.Contains(err.Error(), "git repository") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
}

func TestWorktreeIsAcceptedInARepository(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("no usable git here: %v %s", err, out)
	}
	path := filepath.Join(dir, "swarm.yaml")
	body := `
web: {enabled: false}
agents:
  - name: worker
    ephemeral: true
    command: [cat]
    workspace: worktree
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("workspace: worktree refused inside a repository: %v", err)
	}
	w, _ := c.Agent("worker")
	if w.Workspace != WorkspaceWorktree {
		t.Errorf("workspace came out as %q", w.Workspace)
	}
}

func TestAnInventedWorkspaceNamesTheRealOnes(t *testing.T) {
	_, err := loadYAML(t, `
web: {enabled: false}
agents:
  - name: dev-1
    command: [cat]
    workspace: sandbox
`)
	if err == nil {
		t.Fatal("an invented workspace mode was accepted")
	}
	if !strings.Contains(err.Error(), "worktree") {
		t.Errorf("the error does not name the modes that exist: %v", err)
	}
}

// A per-template max_alive bounds one kind of work; the fleet needs its own,
// or three templates of three quietly become nine agent processes.
func TestTheFleetHasItsOwnCeiling(t *testing.T) {
	c, err := loadYAML(t, ephemeralBase)
	if err != nil {
		t.Fatal(err)
	}
	if c.Ephemeral.MaxAlive != DefaultFleetMaxAlive {
		t.Errorf("ephemeral.max_alive defaults to %d", c.Ephemeral.MaxAlive)
	}
	if c.Ephemeral.Remember != DefaultRemember {
		t.Errorf("ephemeral.remember defaults to %d", c.Ephemeral.Remember)
	}

	c, err = loadYAML(t, ephemeralBase+`
ephemeral:
  max_alive: 4
  remember: 20
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Ephemeral.MaxAlive != 4 || c.Ephemeral.Remember != 20 {
		t.Errorf("came back as max_alive %d, remember %d", c.Ephemeral.MaxAlive, c.Ephemeral.Remember)
	}

	if _, err := loadYAML(t, ephemeralBase+"\nephemeral: {max_alive: -1}\n"); err == nil {
		t.Error("a negative ceiling was accepted")
	}
}
