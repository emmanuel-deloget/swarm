//go:build windows

package vterm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
