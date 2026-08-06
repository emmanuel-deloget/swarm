package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// pressKey sends one key, escape sequence and all, in a single write. Splitting
// it would be read as ESC followed by plain characters, which is not the key.
func pressKey(t *testing.T, term *vterm.Terminal, seq string) {
	t.Helper()
	if _, err := term.Write([]byte(seq)); err != nil {
		t.Fatalf("pressing %q: %v", seq, err)
	}
	time.Sleep(30 * time.Millisecond)
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

// agentSize asks a running swarm for one agent's terminal geometry.
func agentSize(t *testing.T, bin, cfg, agent string) (cols, rows int) {
	t.Helper()
	cmd := exec.Command(bin, "status", agent, "-c", cfg, "-json")
	cmd.Dir = filepath.Dir(cfg)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("status -json: %v", err)
	}
	var infos []struct {
		Name string `json:"name"`
		Cols int    `json:"cols"`
		Rows int    `json:"rows"`
	}
	if err := json.Unmarshal(out, &infos); err != nil {
		t.Fatalf("decoding status: %v\n%s", err, out)
	}
	if len(infos) != 1 {
		t.Fatalf("status returned %d agents", len(infos))
	}
	return infos[0].Cols, infos[0].Rows
}

// TestTUIFitsTheAgentToTheWindow checks that an agent shown in the TUI is given
// the size of the pane it appears in, and follows the window when it changes.
// Without this an agent configured wider than the window is simply cropped, and
// its own layout never adapts.
//
// The agent prints what stty reports, so the assertion is on what the agent
// itself sees — not merely on what swarm believes it set.
func TestTUIFitsTheAgentToTheWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary and runs a full UI")
	}
	bin := buildSwarm(t)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "swarm.yaml")
	// Configured much wider than the window we are about to open.
	body := `session: fit-test
defaults:
  cols: 200
  rows: 50
  idle_after: 300ms
web:
  enabled: false
agents:
  - name: a1
    command: [sh, -c, "while :; do printf 'size='; stty size; sleep 0.3; done"]
`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	term, err := vterm.Start(vterm.Options{
		Command: []string{bin, "run", "-c", cfg},
		Dir:     dir,
		Cols:    120,
		Rows:    40,
	})
	if err != nil {
		t.Fatalf("starting the TUI: %v", err)
	}
	defer func() { _ = term.Stop(3 * time.Second) }()

	// 120 columns: 1 kept from the last column, 24 for the sidebar, 1 for the
	// separator. 40 rows: 2 for the header, 1 for the status line, 7 for the
	// event log, 2 for the pane title.
	waitScreen(t, term, "the agent fitted to the window", "size=28 94")
	if cols, rows := agentSize(t, bin, cfg, "a1"); cols != 94 || rows != 28 {
		t.Errorf("swarm reports %dx%d, the agent sees 28x94", cols, rows)
	}

	// Grow the window: the agent must follow.
	if err := term.Resize(160, 50); err != nil {
		t.Fatalf("resize: %v", err)
	}
	waitScreen(t, term, "the agent following the bigger window", "size=38 134")

	// And shrink it.
	if err := term.Resize(100, 30); err != nil {
		t.Fatalf("resize: %v", err)
	}
	waitScreen(t, term, "the agent following the smaller window", "size=18 74")
}

// TestFollowWindowCanBePinned checks the escape hatch: with follow_window off,
// the configured geometry is kept whatever the window does, which is what the
// web UI and `swarm screen` then see.
func TestFollowWindowCanBePinned(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary and runs a full UI")
	}
	bin := buildSwarm(t)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "swarm.yaml")
	body := `session: pin-test
defaults:
  cols: 150
  rows: 45
  idle_after: 300ms
  follow_window: false
web:
  enabled: false
agents:
  - name: a1
    command: [sh, -c, "printf 'a1 ready\n'; while :; do sleep 1; done"]
`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	term, err := vterm.Start(vterm.Options{
		Command: []string{bin, "run", "-c", cfg},
		Dir:     dir,
		Cols:    120,
		Rows:    40,
	})
	if err != nil {
		t.Fatalf("starting the TUI: %v", err)
	}
	defer func() { _ = term.Stop(3 * time.Second) }()

	waitScreen(t, term, "the agent's screen", "a1 ready")
	time.Sleep(1500 * time.Millisecond)

	if cols, rows := agentSize(t, bin, cfg, "a1"); cols != 150 || rows != 45 {
		t.Errorf("agent is %dx%d, want the pinned 150x45", cols, rows)
	}
}

// TestTUIScrollStopsAtTheStartOfTheSession checks two things about pgup: that it
// reaches output which has left the screen, and that it stops at the beginning
// instead of drifting into blank space with an ever-growing offset.
func TestTUIScrollStopsAtTheStartOfTheSession(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary and runs a full UI")
	}
	bin := buildSwarm(t)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "swarm.yaml")
	// 60 numbered lines: more than any pane in a 40-row window can hold.
	body := `session: scroll-test
defaults:
  idle_after: 300ms
  scrollback: 500
web:
  enabled: false
agents:
  - name: a1
    command: [sh, -c, "i=1; while [ $i -le 60 ]; do printf 'line-%02d\n' $i; i=$((i+1)); done; while :; do sleep 1; done"]
`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	term, err := vterm.Start(vterm.Options{
		Command: []string{bin, "run", "-c", cfg},
		Dir:     dir,
		Cols:    120,
		Rows:    40,
	})
	if err != nil {
		t.Fatalf("starting the TUI: %v", err)
	}
	defer func() { _ = term.Stop(3 * time.Second) }()

	// The bottom of the output is on screen, the beginning is not.
	waitScreen(t, term, "the end of the output", "line-60")
	if strings.Contains(term.Text(), "line-01") {
		t.Fatal("the window is too tall for this test to mean anything")
	}

	// Scrolling up reaches the first line, which had left the screen.
	for range 8 {
		pressKey(t, term, "\x1b[5~") // pgup
	}
	waitScreen(t, term, "the start of the output after scrolling up", "line-01")

	// Keep going: the offset must stop at the top, and the first lines stay
	// visible instead of the pane emptying out.
	for range 30 {
		pressKey(t, term, "\x1b[5~")
	}
	screen := term.Text()
	if !strings.Contains(screen, "line-01") {
		t.Fatalf("scrolling past the top emptied the pane:\n%s", screen)
	}

	// The indicator reports a bounded offset, not a runaway one.
	re := regexp.MustCompile(`scrolled (\d+)/(\d+)`)
	m := re.FindStringSubmatch(screen)
	if m == nil {
		t.Fatalf("no bounded scroll indicator on screen:\n%s", screen)
	}
	at, top := m[1], m[2]
	if at != top {
		t.Errorf("scrolled %s/%s: holding pgup should settle at the top", at, top)
	}

	// And coming back down returns to the live end of the output.
	for range 40 {
		pressKey(t, term, "\x1b[6~") // pgdown
	}
	waitScreen(t, term, "the end of the output again", "line-60")
	if strings.Contains(term.Text(), "scrolled") {
		t.Error("back at the bottom, there should be no scroll indicator")
	}
}
