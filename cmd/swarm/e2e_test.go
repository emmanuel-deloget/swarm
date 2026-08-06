package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/vterm"
)

// These tests drive the real binary inside a pty, using swarm's own terminal
// emulator to read what it draws. It is the only way to check that the TUI
// actually renders the fleet and reacts to keys.

func buildSwarm(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "swarm")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/emmanuel-deloget/swarm/cmd/swarm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building swarm: %v\n%s", err, out)
	}
	return bin
}

func writeFleet(t *testing.T, session string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "swarm.yaml")
	body := `session: ` + session + `
defaults:
  cols: 100
  rows: 24
  idle_after: 300ms
web:
  enabled: false
agents:
  - name: alpha
    role: dev
    command: [sh, -c, "printf 'alpha ready\n'; while IFS= read -r l; do printf 'alpha saw:%s\n' \"$l\"; done"]
  - name: beta
    role: review
    command: [sh, -c, "printf 'beta ready\n'; while IFS= read -r l; do printf 'beta saw:%s\n' \"$l\"; done"]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// typeText sends characters one at a time. A whole string written at once
// arrives as a single key event — that is what a paste looks like — and would
// not exercise the interactive path.
func typeText(t *testing.T, term *vterm.Terminal, s string) {
	t.Helper()
	for _, r := range s {
		if _, err := term.Write([]byte(string(r))); err != nil {
			t.Fatalf("typing %q: %v", s, err)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func waitScreen(t *testing.T, term *vterm.Terminal, what string, wants ...string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		screen := term.Text()
		all := true
		for _, w := range wants {
			if !strings.Contains(screen, w) {
				all = false
				break
			}
		}
		if all {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s (%v); screen was:\n%s", what, wants, term.Text())
}

func TestTUIRendersFleetAndAcceptsCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary and runs a full UI")
	}
	bin := buildSwarm(t)
	cfg := writeFleet(t, "tui-test")

	term, err := vterm.Start(vterm.Options{
		Command: []string{bin, "run", "-c", cfg},
		Dir:     filepath.Dir(cfg),
		Cols:    120,
		Rows:    40,
	})
	if err != nil {
		t.Fatalf("starting the TUI: %v", err)
	}
	defer func() { _ = term.Stop(3 * time.Second) }()

	// The fleet and its state show up.
	waitScreen(t, term, "the agent list", "swarm", "tui-test", "alpha", "beta")
	waitScreen(t, term, "the selected agent's screen", "alpha ready")

	// Moving down selects the second agent, whose terminal is then shown.
	typeText(t, term, "j")
	waitScreen(t, term, "beta's screen after moving down", "beta ready")

	// The command bar injects into the selected agent.
	typeText(t, term, ":inject typed from the tui\r")
	waitScreen(t, term, "the injected text in beta", "beta saw:typed from the tui")

	// A bus message to the other agent, by name.
	typeText(t, term, ":send alpha hello alpha\r")
	typeText(t, term, "k")
	waitScreen(t, term, "the bus message in alpha", "hello alpha")

	// The mosaic shows both agents at once.
	typeText(t, term, "m")
	waitScreen(t, term, "the mosaic", "alpha", "beta")
	typeText(t, term, "m")

	// Help is reachable and mentions the keys.
	typeText(t, term, "?")
	waitScreen(t, term, "the help screen", "swarm — keys", "mosaic")
	typeText(t, term, " ")

	// Quitting asks for confirmation, then stops everything.
	typeText(t, term, "q")
	waitScreen(t, term, "the quit confirmation", "quit swarm")
	typeText(t, term, "y")

	select {
	case <-term.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("swarm did not exit; screen was:\n%s", term.Text())
	}
	if st := term.Status(); st == nil || st.Code != 0 {
		t.Errorf("exit status = %v, want 0", st)
	}
}

func TestAttachDrivesAnAgentDirectly(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary")
	}
	bin := buildSwarm(t)
	cfg := writeFleet(t, "attach-test")
	dir := filepath.Dir(cfg)

	// A headless swarm, driven from another terminal.
	run := exec.Command(bin, "run", "-c", cfg, "--no-tui")
	run.Dir = dir
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = run.Process.Kill()
		_, _ = run.Process.Wait()
	}()

	// Wait for the socket to answer.
	deadline := time.Now().Add(15 * time.Second)
	for {
		cmd := exec.Command(bin, "ls", "-c", cfg)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err == nil && strings.Contains(string(out), "alpha") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the swarm never came up")
		}
		time.Sleep(200 * time.Millisecond)
	}

	term, err := vterm.Start(vterm.Options{
		Command: []string{bin, "attach", "-c", cfg, "alpha"},
		Dir:     dir,
		Cols:    100,
		Rows:    24,
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer func() { _ = term.Stop(3 * time.Second) }()

	// Attaching repaints what the agent already printed.
	waitScreen(t, term, "the agent screen through attach", "alpha ready")

	// The last row keeps telling you how to get out, whatever the agent draws.
	waitScreen(t, term, "the detach reminder", "ctrl+\\ detach")

	// It sits on the very last row, and nowhere else: the agent was given the
	// rows above it.
	rows := strings.Split(term.Text(), "\n")
	if len(rows) < 24 {
		t.Fatalf("expected a 24-row screen, got %d", len(rows))
	}
	if !strings.Contains(rows[23], "ctrl+\\ detach") {
		t.Errorf("the status bar should be on the last row, got %q", rows[23])
	}
	for i, row := range rows[:23] {
		if strings.Contains(row, "ctrl+\\ detach") {
			t.Errorf("the status bar leaked onto row %d: %q", i+1, row)
		}
	}

	// The agent got the window minus that row.
	status := exec.Command(bin, "status", "-c", cfg, "alpha")
	status.Dir = dir
	out, err := status.CombinedOutput()
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "100x23") {
		t.Errorf("the agent should have been resized to the window minus the status row:\n%s", out)
	}

	// Typing here goes straight to the agent.
	typeText(t, term, "through attach\r")
	waitScreen(t, term, "the echo of what we typed", "alpha saw:through attach")

	// ctrl+\ detaches without stopping the agent.
	if _, err := term.Write([]byte{0x1c}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-term.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("ctrl+\\ did not detach")
	}

	cmd := exec.Command(bin, "ls", "-c", cfg)
	cmd.Dir = dir
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ls after detach: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "alpha") {
		t.Fatalf("the agent should still be there after detaching:\n%s", out)
	}
}

// TestAgentDrivesSwarmItself is the promise of the whole thing: an agent runs
// the swarm CLI from its own shell and reaches a peer. It relies on nothing but
// the PATH shim and the environment `swarm run` sets up.
func TestAgentDrivesSwarmItself(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary")
	}
	bin := buildSwarm(t)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "swarm.yaml")
	body := `session: peers-test
defaults:
  idle_after: 300ms
web:
  enabled: false
agents:
  - name: alpha
    command: [sh, -c, "printf 'alpha ready\n'; while IFS= read -r l; do printf 'alpha saw:%s\n' \"$l\"; done"]
  - name: talker
    command: [sh, -c, "printf 'talker ready\n'; while IFS= read -r l; do swarm send alpha \"$l\"; done"]
`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	run := exec.Command(bin, "run", "-c", cfg, "--no-tui")
	run.Dir = dir
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = run.Process.Kill()
		_, _ = run.Process.Wait()
	}()

	// Flags are accepted after the positional arguments, so appending -c works.
	swarm := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, append(args, "-c", cfg)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("swarm %v: %v\n%s", args, err, out)
		}
		return string(out)
	}

	// Wait for the fleet to answer.
	deadline := time.Now().Add(15 * time.Second)
	for {
		cmd := exec.Command(bin, "ls", "-c", cfg)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err == nil && strings.Contains(string(out), "talker") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the swarm never came up")
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Tell talker something; its shell forwards it to alpha with `swarm send`.
	swarm("inject", "talker", "please build the thing")

	// alpha must end up seeing it, attributed to talker — swarm sets
	// $SWARM_AGENT in the sender's environment.
	deadline = time.Now().Add(15 * time.Second)
	for {
		screen := swarm("screen", "alpha", "-plain")
		if strings.Contains(screen, "please build the thing") && strings.Contains(screen, "talker") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("alpha never heard from talker; its screen was:\n%s", screen)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
