package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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

	// The fleet and its state show up. "2 idle" proves both agents are running:
	// the event log prints their argv, so waiting on their output alone would
	// match before they had started.
	waitScreen(t, term, "the agent list", "swarm", "tui-test", "alpha", "beta")
	waitScreen(t, term, "both agents up and quiet", "2 idle")

	// The header says which build this is, so a screenshot in a bug report
	// carries the version without anyone having to ask for it. The version has
	// to come from the binary under test rather than from this test binary:
	// `go test` stamps no VCS information, so version.Short() here says "devel"
	// while the built swarm says what it was built from.
	waitScreen(t, term, "the version in the header", binVersion(t, bin))

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

	waitRunning(t, bin, cfg, "alpha")

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
	waitRunning(t, bin, cfg, "alpha", "talker")

	// Tell talker something; its shell forwards it to alpha with `swarm send`.
	swarm("inject", "talker", "please build the thing")

	// alpha must end up seeing it, attributed to talker — swarm sets
	// $SWARM_AGENT in the sender's environment.
	deadline := time.Now().Add(15 * time.Second)
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

// waitRunning waits until the named agents have a live process. `swarm ls`
// answers as soon as the socket is up and lists stopped agents too, so waiting
// on a name there proves nothing — which made these tests fail intermittently
// under -race, where the gap between "socket up" and "agent started" widens.
func waitRunning(t *testing.T, bin, cfg string, names ...string) {
	t.Helper()
	dir := filepath.Dir(cfg)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ready := 0
		for _, name := range names {
			cmd := exec.Command(bin, "status", name, "-c", cfg, "-json")
			cmd.Dir = dir
			out, err := cmd.Output()
			if err != nil {
				break
			}
			var infos []struct {
				Pid   int    `json:"pid"`
				State string `json:"state"`
			}
			if json.Unmarshal(out, &infos) != nil || len(infos) != 1 {
				break
			}
			if infos[0].Pid > 0 && infos[0].State != "stopped" && infos[0].State != "exited" {
				ready++
			}
		}
		if ready == len(names) {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("agents %v never started", names)
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

// TestDetachKeyIsConfigurable covers the reason it exists: ctrl+\ is taken by
// whatever is recording or multiplexing the terminal (asciinema, tmux, screen),
// so it has to be movable — and the key it replaces must then reach the agent
// like any other input.
func TestDetachKeyIsConfigurable(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary")
	}
	bin := buildSwarm(t)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "swarm.yaml")
	// cat -v shows control characters, so we can see ctrl+\ arrive as ^\.
	body := `session: detach-test
detach_key: ctrl+g
defaults:
  idle_after: 300ms
web:
  enabled: false
agents:
  - name: a1
    # stty -isig so that ctrl+\ arrives as a character instead of raising
    # SIGQUIT: this test is about who intercepts the key, not about signals.
    command: [sh, -c, "stty -isig; printf 'a1 ready\n'; cat -v"]
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

	waitRunning(t, bin, cfg, "a1")

	term, err := vterm.Start(vterm.Options{
		Command: []string{bin, "attach", "-c", cfg, "a1"},
		Dir:     dir,
		Cols:    100,
		Rows:    24,
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer func() { _ = term.Stop(3 * time.Second) }()

	// The status bar advertises the configured key, not the default.
	waitScreen(t, term, "the agent screen", "a1 ready")
	waitScreen(t, term, "the configured key in the status bar", "ctrl+g detach")
	if strings.Contains(term.Text(), `ctrl+\ detach`) {
		t.Error("the status bar still advertises the default key")
	}

	// ctrl+\ is no longer special: it must reach the agent, and leave the
	// attachment alone.
	pressKey(t, term, "\x1c")
	waitScreen(t, term, "ctrl+\\ passed through to the agent", "^\\")
	time.Sleep(500 * time.Millisecond)
	if term.Exited() {
		t.Fatal("ctrl+\\ detached even though the key was moved")
	}

	// ctrl+g does detach.
	pressKey(t, term, "\x07")
	select {
	case <-term.Done():
	case <-time.After(10 * time.Second):
		t.Fatalf("ctrl+g did not detach; screen was:\n%s", term.Text())
	}

	// The flag overrides the configured key.
	term2, err := vterm.Start(vterm.Options{
		Command: []string{bin, "attach", "-c", cfg, "-detach-key", "ctrl+]", "a1"},
		Dir:     dir,
		Cols:    100,
		Rows:    24,
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer func() { _ = term2.Stop(3 * time.Second) }()

	waitScreen(t, term2, "the overridden key in the status bar", "ctrl+] detach")
	pressKey(t, term2, "\x1d")
	select {
	case <-term2.Done():
	case <-time.After(10 * time.Second):
		t.Fatalf("-detach-key was ignored; screen was:\n%s", term2.Text())
	}
}

// TestTUIUsesTheConfiguredDetachKey checks the TUI's attached mode honours the
// same setting as `swarm attach`, so there is one key to remember.
func TestTUIUsesTheConfiguredDetachKey(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary and runs a full UI")
	}
	bin := buildSwarm(t)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "swarm.yaml")
	body := `session: tui-detach
detach_key: ctrl+g
defaults:
  idle_after: 300ms
web:
  enabled: false
agents:
  - name: a1
    command: [sh, -c, "stty -isig; printf 'a1 ready\n'; cat -v"]
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

	// Wait for the state, not for a string that also appears in the argv the
	// event log prints: that would match before the agent is running.
	waitScreen(t, term, "the agent to be up and quiet", "1 idle")

	// Attach: the status bar names the configured key.
	pressKey(t, term, "\r")
	waitScreen(t, term, "the attached status bar", "ATTACHED", "ctrl+g")

	// ctrl+\ now goes to the agent instead of detaching.
	pressKey(t, term, "\x1c")
	waitScreen(t, term, "ctrl+\\ reaching the agent", "^\\")
	if !strings.Contains(term.Text(), "ATTACHED") {
		t.Error("ctrl+\\ left the attached mode even though the key was moved")
	}

	// ctrl+g comes back.
	pressKey(t, term, "\x07")
	waitScreen(t, term, "the normal status bar again", "detached")
	if strings.Contains(term.Text(), "ATTACHED") {
		t.Error("ctrl+g did not leave the attached mode")
	}
}

// commands is every subcommand the CLI dispatches, so the sweeps below cannot
// silently miss one that is added later.
var commands = []string{
	"ls", "status", "start", "stop", "restart", "inject", "keys", "screen",
	"attach", "logs", "send", "broadcast", "inbox", "stage", "events", "info",
	"shutdown", "run", "init",
}

// TestNoCommandPanicsOnMissingArguments sweeps every subcommand with too few
// arguments. `swarm keys` used to panic on fs.Args()[1:] instead of printing
// its usage, and nothing would have caught the next one.
func TestNoCommandPanicsOnMissingArguments(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary")
	}
	bin := buildSwarm(t)

	// An empty directory: no config to find, so each command takes its early
	// path with nothing to lean on.
	dir := t.TempDir()

	for _, name := range commands {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(bin, name)
			cmd.Dir = dir
			// Keep the swarm socket of the developer's own session out of it.
			cmd.Env = append(os.Environ(), "SWARM_SOCKET=")
			out, _ := cmd.CombinedOutput()
			assertNoPanic(t, name, out)
		})
	}
}

// TestNoCommandPanicsAgainstARunningSwarm is the same sweep with a live swarm,
// which takes the commands past their early error paths and into the code that
// actually talks to the fleet.
func TestNoCommandPanicsAgainstARunningSwarm(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary")
	}
	bin := buildSwarm(t)
	cfg := writeFleet(t, "sweep-test")
	dir := filepath.Dir(cfg)

	run := exec.Command(bin, "run", "-c", cfg, "--no-tui")
	run.Dir = dir
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = run.Process.Kill()
		_, _ = run.Process.Wait()
	}()

	waitRunning(t, bin, cfg, "alpha")

	// Each command with no arguments, then with only a target: the two shapes
	// that skip the value a command needs.
	for _, name := range commands {
		if name == "shutdown" || name == "run" || name == "init" {
			continue // would stop the fleet, or start another one
		}
		for _, args := range [][]string{{name}, {name, "alpha"}} {
			t.Run(strings.Join(args, "_"), func(t *testing.T) {
				cmd := exec.Command(bin, append(args, "-c", cfg)...)
				cmd.Dir = dir
				out, _ := cmd.CombinedOutput()
				assertNoPanic(t, strings.Join(args, " "), out)
			})
		}
	}
}

func assertNoPanic(t *testing.T, what string, out []byte) {
	t.Helper()
	text := string(out)
	for _, bad := range []string{"panic:", "goroutine ", "runtime error"} {
		if strings.Contains(text, bad) {
			t.Fatalf("`swarm %s` crashed:\n%s", what, text)
		}
	}
}

// TestMouseReportingIsOffByDefault checks the TUI does not ask the terminal for
// mouse events unless told to. A terminal that reports them to an application
// stops handling text selection itself, so this is the difference between being
// able to copy an agent's output and not.
//
// The assertion is on the escape sequences swarm actually writes: OnOutput sees
// the raw stream, so there is no need to trust a claim about the mode.
func TestMouseReportingIsOffByDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary and runs a full UI")
	}
	bin := buildSwarm(t)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "swarm.yaml")
	body := `session: mouse-test
defaults:
  idle_after: 300ms
web:
  enabled: false
agents:
  - name: a1
    command: [sh, -c, "printf 'ready\n'; while :; do sleep 1; done"]
`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	var (
		mu     sync.Mutex
		stream []byte
	)
	seen := func(pattern string) bool {
		mu.Lock()
		defer mu.Unlock()
		return bytes.Contains(stream, []byte(pattern))
	}

	term, err := vterm.Start(vterm.Options{
		Command: []string{bin, "run", "-c", cfg},
		Dir:     dir,
		Cols:    100,
		Rows:    30,
		OnOutput: func(b []byte) {
			mu.Lock()
			stream = append(stream, b...)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("starting the TUI: %v", err)
	}
	defer func() { _ = term.Stop(3 * time.Second) }()

	waitScreen(t, term, "the agent to be up", "1 idle")

	// 1002 is cell-motion tracking, 1003 all-motion: neither should be asked
	// for while the setting is off.
	for _, mode := range []string{"\x1b[?1002h", "\x1b[?1003h"} {
		if seen(mode) {
			t.Errorf("the TUI asked for mouse reporting (%q) without being told to", mode)
		}
	}

	// M turns it on, for the wheel and for clicking an agent.
	pressKey(t, term, "M")
	deadline := time.Now().Add(5 * time.Second)
	for !seen("\x1b[?1002h") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !seen("\x1b[?1002h") {
		t.Fatalf("M did not turn mouse reporting on; screen was:\n%s", term.Text())
	}
	waitScreen(t, term, "the status line to say so", "mouse on")

	// And off again, giving text selection back.
	pressKey(t, term, "M")
	waitScreen(t, term, "the status line to say so", "mouse off")
}

// TestMouseReportingCanBeConfigured checks mouse: true asks for it from the
// start, for whoever prefers the wheel.
func TestMouseReportingCanBeConfigured(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary and runs a full UI")
	}
	bin := buildSwarm(t)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "swarm.yaml")
	body := `session: mouse-on-test
mouse: true
defaults:
  idle_after: 300ms
web:
  enabled: false
agents:
  - name: a1
    command: [sh, -c, "printf 'ready\n'; while :; do sleep 1; done"]
`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	var (
		mu     sync.Mutex
		stream []byte
	)
	term, err := vterm.Start(vterm.Options{
		Command: []string{bin, "run", "-c", cfg},
		Dir:     dir,
		Cols:    100,
		Rows:    30,
		OnOutput: func(b []byte) {
			mu.Lock()
			stream = append(stream, b...)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("starting the TUI: %v", err)
	}
	defer func() { _ = term.Stop(3 * time.Second) }()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := bytes.Contains(stream, []byte("\x1b[?1002h"))
		mu.Unlock()
		if got {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("mouse: true did not turn mouse reporting on")
}

// TestTinyWindowDoesNotShrinkTheAgent guards the mistake that made the web view
// look broken: a small TUI pane resized the agent down to it — three rows in the
// reported case — which flattens the agent's own layout and pushes everything
// that no longer fits into the scrollback. A pane that small should crop, not
// resize.
func TestTinyWindowDoesNotShrinkTheAgent(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary and runs a full UI")
	}
	bin := buildSwarm(t)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "swarm.yaml")
	body := `session: tiny-test
defaults:
  cols: 120
  rows: 40
  idle_after: 300ms
web:
  enabled: false
agents:
  - name: a1
    command: [sh, -c, "printf 'ready\n'; while :; do sleep 1; done"]
`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// A window with room for a pane of only a few rows once the header, the
	// status line and the event log have taken theirs.
	term, err := vterm.Start(vterm.Options{
		Command: []string{bin, "run", "-c", cfg},
		Dir:     dir,
		Cols:    146,
		Rows:    15,
	})
	if err != nil {
		t.Fatalf("starting the TUI: %v", err)
	}
	defer func() { _ = term.Stop(3 * time.Second) }()

	waitScreen(t, term, "the agent to be up", "1 idle")
	time.Sleep(1500 * time.Millisecond) // give the fit a chance to misbehave

	cols, rows := agentSize(t, bin, cfg, "a1")
	if rows < minAgentRows {
		t.Errorf("agent shrunk to %dx%d in a 15-row window; it should have kept its own geometry", cols, rows)
	}

	// A window with room to spare does fit the agent, so the guard has not
	// simply disabled the feature.
	if err := term.Resize(146, 50); err != nil {
		t.Fatalf("resize: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if c, r := agentSize(t, bin, cfg, "a1"); c != 120 || r != 40 {
			t.Logf("fitted to %dx%d in a 146x50 window", c, r)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Error("a roomy window should still fit the agent to the pane")
}

// minAgentRows mirrors the floor the UI applies; a terminal below it is not one.
const minAgentRows = 12

// TestCommandLineUsesTheWholeWidth checks the command line is as wide as the
// window. It used to be fixed at 60 columns, so on a wide terminal it stopped
// around the middle and the rest of what you typed scrolled out of sight.
func TestCommandLineUsesTheWholeWidth(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary and runs a full UI")
	}
	bin := buildSwarm(t)
	cfg := writeFleet(t, "cmdline-test")

	const cols = 150
	term, err := vterm.Start(vterm.Options{
		Command: []string{bin, "run", "-c", cfg},
		Dir:     filepath.Dir(cfg),
		Cols:    cols,
		Rows:    40,
	})
	if err != nil {
		t.Fatalf("starting the TUI: %v", err)
	}
	defer func() { _ = term.Stop(3 * time.Second) }()

	waitScreen(t, term, "both agents up and quiet", "2 idle")

	// Type a command whose text alone is wider than the old 60-column field.
	const payload = 100
	pressKey(t, term, ":")
	typeText(t, term, "broadcast "+strings.Repeat("x", payload))

	// The x's must all be on screen: with a 60-column field, only a window onto
	// them would be.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(term.Text(), "\n") {
			if strings.Count(line, "x") >= payload {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	var widest int
	for _, line := range strings.Split(term.Text(), "\n") {
		if n := strings.Count(line, "x"); n > widest {
			widest = n
		}
	}
	t.Fatalf("the command line shows %d of %d typed characters in a %d-column window", widest, payload, cols)
}

// TestTabCompletesInTheCommandLine drives completion through the real UI: the
// key has to reach the command line rather than being swallowed as a normal
// key, and the result has to appear on screen.
func TestTabCompletesInTheCommandLine(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary and runs a full UI")
	}
	bin := buildSwarm(t)
	cfg := writeFleet(t, "tab-test")

	term, err := vterm.Start(vterm.Options{
		Command: []string{bin, "run", "-c", cfg},
		Dir:     filepath.Dir(cfg),
		Cols:    140,
		Rows:    40,
	})
	if err != nil {
		t.Fatalf("starting the TUI: %v", err)
	}
	defer func() { _ = term.Stop(3 * time.Second) }()

	waitScreen(t, term, "both agents up and quiet", "2 idle")

	// A verb: "bro" has one match.
	pressKey(t, term, ":")
	typeText(t, term, "bro")
	pressKey(t, term, "\t")
	waitScreen(t, term, "the completed verb", "broadcast")

	// A target. "al" is ambiguous — the agent alpha and the target "all" — so
	// the first candidate lands and tab again moves to the other.
	pressKey(t, term, "\x1b") // esc, back to normal mode
	pressKey(t, term, ":")
	typeText(t, term, "inject al")
	pressKey(t, term, "\t")
	waitScreen(t, term, "the first candidate", "inject all")
	pressKey(t, term, "\t")
	waitScreen(t, term, "the next candidate", "inject alpha")

	// An empty target lists what is on offer.
	pressKey(t, term, "\x1b")
	pressKey(t, term, ":")
	typeText(t, term, "restart ")
	pressKey(t, term, "\t")
	waitScreen(t, term, "the candidate list", "alpha", "beta")

	// And the completed command still runs.
	pressKey(t, term, "\x1b")
	pressKey(t, term, ":")
	typeText(t, term, "inj")
	pressKey(t, term, "\t")
	typeText(t, term, "beta completed and sent\r")
	// The pane still shows alpha; move to beta to see what it received.
	typeText(t, term, "j")
	waitScreen(t, term, "the injected text reaching the agent", "beta saw:completed and sent")
}

// TestHelpFitsTheWindow checks the help screen stays inside the terminal. It
// grew past 40 rows once and scrolled its own title off the top, which is how
// one ends up with a help screen that does not say what it is.
func TestHelpFitsTheWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary and runs a full UI")
	}
	bin := buildSwarm(t)
	cfg := writeFleet(t, "help-test")

	for _, size := range [][2]int{{140, 40}, {100, 24}, {180, 60}} {
		cols, rows := size[0], size[1]
		term, err := vterm.Start(vterm.Options{
			Command: []string{bin, "run", "-c", cfg},
			Dir:     filepath.Dir(cfg),
			Cols:    cols,
			Rows:    rows,
		})
		if err != nil {
			t.Fatalf("starting the TUI: %v", err)
		}

		waitScreen(t, term, "both agents up and quiet", "2 idle")
		typeText(t, term, "?")
		// The title has to be there, which it is not if the screen scrolled.
		waitScreen(t, term, "the help title", "swarm — keys")
		// And the commands, which live in the other column.
		waitScreen(t, term, "the commands column", "swarm — commands", ":broadcast")

		screen := term.Text()
		if lines := strings.Split(screen, "\n"); len(lines) > rows {
			t.Errorf("%dx%d: help rendered %d lines", cols, rows, len(lines))
		}
		_ = term.Stop(3 * time.Second)
	}
}

// TestInputLogRecordsWhatSwarmSends covers the question that took an afternoon
// to answer without it: did swarm type that, or did the agent print it itself?
// With log_input on, every byte swarm sends is recorded with its origin.
func TestInputLogRecordsWhatSwarmSends(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary")
	}
	bin := buildSwarm(t)

	dir := t.TempDir()
	cfg := filepath.Join(dir, "swarm.yaml")
	body := `session: inputlog-test
log_input: true
defaults:
  idle_after: 300ms
web:
  enabled: false
agents:
  - name: a1
    command: [sh, -c, "printf 'ready\n'; while IFS= read -r l; do printf 'saw:%s\n' \"$l\"; done"]
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
	waitRunning(t, bin, cfg, "a1")

	swarm := func(args ...string) {
		t.Helper()
		cmd := exec.Command(bin, append(args, "-c", cfg)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("swarm %v: %v\n%s", args, err, out)
		}
	}
	swarm("inject", "a1", "hello from the log test")
	swarm("keys", "a1", "ctrl+l")

	logPath := filepath.Join(dir, ".swarm", "logs", "a1.input.log")
	deadline := time.Now().Add(10 * time.Second)
	var text string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(logPath)
		if err == nil {
			text = string(data)
			if strings.Contains(text, "hello from the log test") && strings.Contains(text, "keys") {
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
	}

	// Each line says when, from where, and what.
	for _, want := range []string{"started", "inject", "hello from the log test", "submit", "keys", `\f`} {
		if !strings.Contains(text, want) {
			t.Errorf("the input log should mention %q, got:\n%s", want, text)
		}
	}
	// And it is not world-readable: it holds what was typed at an agent.
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("input log mode = %v, want 0600", perm)
	}
}

// TestInputLogIsOffByDefault checks the recording is opt-in.
func TestInputLogIsOffByDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the swarm binary")
	}
	bin := buildSwarm(t)
	cfg := writeFleet(t, "no-inputlog")
	dir := filepath.Dir(cfg)

	run := exec.Command(bin, "run", "-c", cfg, "--no-tui")
	run.Dir = dir
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = run.Process.Kill()
		_, _ = run.Process.Wait()
	}()
	waitRunning(t, bin, cfg, "alpha")

	cmd := exec.Command(bin, "inject", "alpha", "no record of this", "-c", cfg)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("inject: %v\n%s", err, out)
	}
	time.Sleep(500 * time.Millisecond)

	if _, err := os.Stat(filepath.Join(dir, ".swarm", "logs", "alpha.input.log")); !os.IsNotExist(err) {
		t.Error("no input log should be written unless log_input is set")
	}
}

// binVersion asks the built binary what it calls itself, which is what the TUI
// header must agree with.
func binVersion(t *testing.T, bin string) string {
	t.Helper()
	out, err := exec.Command(bin, "version", "-short").Output()
	if err != nil {
		t.Fatalf("asking %s for its version: %v", bin, err)
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		t.Fatal("the binary reported an empty version")
	}
	return v
}
