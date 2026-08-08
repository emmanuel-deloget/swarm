package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/config"
	"github.com/emmanuel-deloget/swarm/internal/event"
)

func hookAgent(t *testing.T, cfg *config.AgentConfig) *Agent {
	t.Helper()
	return New(Options{Config: cfg, Log: event.NewLog(64), Env: os.Environ()})
}

func TestRunHookDoesNothingWithoutOne(t *testing.T) {
	a := hookAgent(t, &config.AgentConfig{Name: "a", Workdir: t.TempDir()})
	if err := a.runHook("on_start", nil); err != nil {
		t.Errorf("an absent hook should be a no-op, got %v", err)
	}
}

func TestRunHookRunsInTheWorkdirWithTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	a := hookAgent(t, &config.AgentConfig{Name: "dev-1", Workdir: dir})
	a.env = append(os.Environ(), "SWARM_AGENT=dev-1", "PORT=4321")

	if err := a.runHook("on_start", []string{"sh", "-c", `echo "$SWARM_AGENT $PORT" > witness.txt`}); err != nil {
		t.Fatal(err)
	}
	// Written in the agent's directory, not wherever the test happens to run.
	got, err := os.ReadFile(filepath.Join(dir, "witness.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "dev-1 4321" {
		t.Errorf("the hook saw %q, want the agent's own environment", strings.TrimSpace(string(got)))
	}
}

// TestRunHookCarriesTheFailureUp: a hook that fails has to say why, because the
// agent will not start and the reason is in the script's output.
func TestRunHookCarriesTheFailureUp(t *testing.T) {
	a := hookAgent(t, &config.AgentConfig{Name: "dev-1", Workdir: t.TempDir()})
	err := a.runHook("on_start", []string{"sh", "-c", "echo 'npm ERR! no such package' >&2; exit 1"})
	if err == nil {
		t.Fatal("a failing hook should be an error")
	}
	for _, want := range []string{"dev-1", "on_start", "npm ERR!"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got %v", want, err)
		}
	}
}

func TestTailKeepsTheEnd(t *testing.T) {
	if got := tail("a\nb\nc\nd", 2); got != "c\nd" {
		t.Errorf("tail = %q, want the last two lines", got)
	}
	if got := tail("only", 5); got != "only" {
		t.Errorf("tail = %q, want it unchanged", got)
	}
}

// TestStopWaitsForTheExitHook is the property that makes on_exit worth having:
// a hook that takes down a container or frees a port is worthless if swarm
// exits from under it. Stop returns only once that has actually happened.
func TestStopWaitsForTheExitHook(t *testing.T) {
	dir := t.TempDir()
	witness := filepath.Join(dir, "torn-down.txt")
	a := hookAgent(t, &config.AgentConfig{
		Name: "dev-1", Command: []string{"sh", "-c", "sleep 30"},
		Workdir: dir, Cols: 80, Rows: 24,
		OnExit: []string{"sh", "-c", "sleep 1; echo gone > " + witness},
	})
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	if err := a.Stop(5 * time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := os.Stat(witness); err != nil {
		t.Error("Stop returned before the exit hook had finished")
	}
}

// TestStopGivesUpOnAHungHook: waiting is right, waiting for ever is not. A
// script that never returns delays a shutdown, it does not prevent one.
func TestStopGivesUpOnAHungHook(t *testing.T) {
	a := hookAgent(t, &config.AgentConfig{
		Name: "dev-1", Command: []string{"sh", "-c", "sleep 30"},
		Workdir: t.TempDir(), Cols: 80, Rows: 24,
		OnExit: []string{"sh", "-c", "sleep 60"},
	})
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := a.Stop(time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("Stop waited %s on a hook that never returns", took)
	}
}

// TestStartRefusesAfterAFailedHook: an agent must not be launched into a
// directory its preparation never finished.
func TestStartRefusesAfterAFailedHook(t *testing.T) {
	a := hookAgent(t, &config.AgentConfig{
		Name: "dev-1", Command: []string{"sh", "-c", "sleep 30"},
		Workdir: t.TempDir(), Cols: 80, Rows: 24,
		OnStart: []string{"sh", "-c", "exit 3"},
	})
	if err := a.Start(); err == nil {
		_ = a.Stop(time.Second)
		t.Fatal("Start should fail when on_start does")
	}
	if got := a.Info().State; got == StateWorking || got == StateStarting {
		t.Errorf("the agent is %q after a failed preparation", got)
	}
}
