//go:build windows

package vterm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain doubles as the grandchild TestJobEndsTheWholeTree needs. Running the
// test binary again is how the standard library spawns a helper process, and it
// beats scripting one: no shell quoting, and the helper is Go.
func TestMain(m *testing.M) {
	if path := os.Getenv("SWARM_TEST_TICK"); path != "" {
		tick(path)
		return
	}
	os.Exit(m.Run())
}

// tick appends a byte to a file until it is killed, or gives up. The deadline
// is not a timeout, it is the blast radius: if the job object fails to end this
// process, it stops on its own instead of outliving the test run.
func tick(path string) {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.Write([]byte("x"))
			_ = f.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// These run on Windows only, because what they check exists nowhere else: a
// pseudoconsole, a job object, and a command resolution that has to do by hand
// what exec.Command does on Unix.
//
// They are deliberately small. The rest of the package's tests drive a child
// through sh, and making those portable is its own piece of work; this is the
// floor — that an agent starts, says something, and ends the way it says.

func TestWindowsChildStartsAndReports(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"cmd.exe", "/c", "echo ready"},
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "the child's output", func() bool {
		return strings.Contains(term.Text(), "ready")
	})

	select {
	case <-term.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the child never ended")
	}
	if st := term.Status(); st == nil || st.Code != 0 {
		t.Fatalf("status %v, want exit 0", st)
	}
}

// TestWindowsChildOutlivesItsOutput is the drain in drainGrace: what a child
// prints just before exiting has to survive the pseudoconsole being closed,
// because that is where the error that killed it lives.
func TestWindowsChildKeepsItsLastWords(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"cmd.exe", "/c", "echo the-last-thing-it-said & exit 3"},
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	select {
	case <-term.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the child never ended")
	}
	if !strings.Contains(term.Text(), "the-last-thing-it-said") {
		t.Errorf("the last line was lost:\n%s", term.Text())
	}
	if st := term.Status(); st == nil || st.Code != 3 {
		t.Errorf("status %v, want exit 3", st)
	}
}

// TestResolveCommandFindsThePath: conpty.Spawn ends in CreateProcess, which
// does not search the PATH. Without this, every fleet on Windows would fail to
// start with "file not found" for a command that is plainly there.
func TestResolveCommandFindsThePath(t *testing.T) {
	name, argv, err := resolveCommand([]string{"cmd", "/c", "echo hi"})
	if err != nil {
		t.Fatalf("resolveCommand: %v", err)
	}
	if !filepath.IsAbs(name) {
		t.Errorf("resolved to %q, which CreateProcess cannot use", name)
	}
	if !strings.EqualFold(filepath.Base(name), "cmd.exe") {
		t.Errorf("resolved to %q, want cmd.exe", name)
	}
	// argv[0] is the program, the way ComposeCommandLine expects it.
	if len(argv) != 3 || argv[0] != name || argv[2] != "echo hi" {
		t.Errorf("argv = %q", argv)
	}

	if _, _, err := resolveCommand([]string{"no-such-command-anywhere"}); err == nil {
		t.Error("a missing command resolved to something")
	}
}

// TestResolveCommandWrapsAScript: npm installs its CLIs as .cmd shims, so this
// is not an edge case — it is how claude, opencode and the rest arrive on
// Windows. CreateProcess only loads PE images and would refuse them outright.
func TestResolveCommandWrapsAScript(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "agentish.cmd")
	if err := os.WriteFile(script, []byte("@echo hello from a script\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	name, argv, err := resolveCommand([]string{script, "--flag"})
	if err != nil {
		t.Fatalf("resolveCommand: %v", err)
	}
	if !strings.EqualFold(filepath.Base(name), "cmd.exe") {
		t.Errorf("a .cmd is run by %q, want the command interpreter", name)
	}
	if len(argv) != 4 || !strings.EqualFold(argv[1], "/c") || argv[2] != script || argv[3] != "--flag" {
		t.Errorf("argv = %q, want the interpreter, /c, the script and its arguments", argv)
	}

	// And it runs.
	term, err := Start(Options{Command: []string{script}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()
	waitFor(t, "the script's output", func() bool {
		return strings.Contains(term.Text(), "hello from a script")
	})
}

// TestJobEndsTheWholeTree is the one that cannot be argued about. Agent CLIs
// are wrappers around wrappers -- a .cmd starting node starting whatever the
// agent decided to build -- and TerminateProcess reaches the leader alone.
// Every survivor holds a handle on a pseudoconsole nobody reads, until the
// machine is rebooted by someone who never connected the two.
//
// So: an agent whose only job is to start something else that keeps writing.
// Stop the agent, then watch the file. If it is still growing, the tree
// outlived the agent and the job object is not doing what this depends on.
func TestJobEndsTheWholeTree(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Not t.TempDir(): a grandchild that survives keeps writing into it, and
	// the cleanup would then fail on a locked file, hiding the real failure
	// behind a second one.
	beat := filepath.Join(os.TempDir(), "swarm-jobtest-"+t.Name())
	defer func() { _ = os.Remove(beat) }()
	_ = os.Remove(beat)

	// cmd.exe is the agent; the ticker it starts is the descendant that a
	// process-level kill would leave behind.
	term, err := Start(Options{
		Command: []string{"cmd.exe", "/c", self},
		Env:     append(os.Environ(), "SWARM_TEST_TICK="+beat),
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	size := func() int64 {
		st, err := os.Stat(beat)
		if err != nil {
			return -1
		}
		return st.Size()
	}
	waitFor(t, "the descendant to start writing", func() bool { return size() > 0 })

	if err := term.Stop(2 * time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Let a write already in flight land before taking the reading.
	time.Sleep(500 * time.Millisecond)
	before := size()
	time.Sleep(700 * time.Millisecond)
	if after := size(); after != before {
		t.Errorf("the descendant outlived the agent: %d bytes then %d after stop", before, after)
	}
}
