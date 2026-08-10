//go:build windows

package vterm

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/x/conpty"
	"golang.org/x/sys/windows"
)

// windowsChild is a process attached to a pseudoconsole.
//
// A ConPTY is not a pty. What it gives back is a pair of pipes and a hidden
// conhost that owns the screen: the child writes console API calls or escape
// sequences, conhost renders them into a screen buffer, and what arrives on the
// read pipe is that buffer serialised back into escape sequences. swarm's
// emulator therefore sees output produced by a terminal, not by the agent —
// which is why the fidelity of what is displayed has to be checked against a
// real Windows terminal rather than assumed.
//
// Windows 10 build 17763 is the floor: CreatePseudoConsole does not exist
// before it.
type windowsChild struct {
	pty *conpty.ConPty
	pid int

	// mu guards handle, which Wait closes. A signal arriving after that would
	// otherwise address a handle the kernel may already have reused.
	mu     sync.Mutex
	handle windows.Handle
}

// drainGrace is how long conhost is given to finish serialising what the child
// wrote before it died. Nothing closes the read pipe on its own: the pipes
// belong to the pseudoconsole, not to the child, so a read blocks until the
// pseudoconsole is closed — and closing it immediately would cut off an agent's
// last words, which are usually the error that killed it.
const drainGrace = 200 * time.Millisecond

func spawn(o Options) (child, error) {
	name, argv, err := resolveCommand(o.Command)
	if err != nil {
		return nil, fmt.Errorf("vterm: start %s: %w", o.Command[0], err)
	}

	cpty, err := conpty.New(o.Cols, o.Rows, 0)
	if err != nil {
		return nil, fmt.Errorf("vterm: pseudoconsole: %w", err)
	}

	pid, handle, err := cpty.Spawn(name, argv, &syscall.ProcAttr{Dir: o.Dir, Env: o.Env})
	if err != nil {
		_ = cpty.Close()
		return nil, fmt.Errorf("vterm: start %s: %w", o.Command[0], err)
	}
	return &windowsChild{pty: cpty, pid: pid, handle: windows.Handle(handle)}, nil
}

// resolveCommand turns a configured command into something CreateProcess can
// actually run. Two differences from Unix have to be absorbed here.
//
// The PATH is not searched. conpty.Spawn ends up in CreateProcess, like
// syscall.StartProcess and unlike exec.Command, so "claude" has to become the
// full path to claude.exe before it is handed over.
//
// And a .cmd or .bat is not an executable. CreateProcess only loads PE images;
// running a script means starting the command interpreter and passing the
// script to it, which is how npm-installed CLIs — claude, opencode, most of
// them — are shipped on Windows.
func resolveCommand(command []string) (name string, argv []string, err error) {
	path, err := exec.LookPath(command[0])
	if err != nil {
		return "", nil, err
	}
	argv = append([]string{path}, command[1:]...)

	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		shell := os.Getenv("COMSPEC")
		if shell == "" {
			shell = "cmd.exe"
		}
		// The interpreter's own quoting rules apply to what follows /c, and
		// they are not the ones ComposeCommandLine implements. An argument
		// carrying &, | or ^ can therefore be read as a command separator.
		// The arguments come from the fleet's own configuration, so this is a
		// sharp edge rather than an exposure — but it is a real one.
		return shell, append([]string{shell, "/c"}, argv...), nil
	}
	return path, argv, nil
}

func (c *windowsChild) Read(p []byte) (int, error)  { return c.pty.Read(p) }
func (c *windowsChild) Write(p []byte) (int, error) { return c.pty.Write(p) }
func (c *windowsChild) Close() error                { return c.pty.Close() }
func (c *windowsChild) Pid() int                    { return c.pid }

func (c *windowsChild) Resize(cols, rows int) error { return c.pty.Resize(cols, rows) }

// errNoTree is what Signal returns for anything it cannot express. Saying so
// is the point: a stub returning nil would leave the hub believing an agent had
// been asked to stop while nothing was sent.
var errNoTree = errors.New("vterm: windows cannot signal a process tree; " +
	"ending one needs a job object")

// Signal ends the process, and only that process.
//
// Windows has no signals. What SIGTERM and SIGKILL both mean here is
// TerminateProcess, which is abrupt in either case — there is no polite
// request to deliver, so the grace period in Stop buys nothing and costs a
// couple of seconds. And it reaches the leader alone: a node wrapper's
// children survive it. Ending a tree needs a job object, which is the next
// piece of the port; until then a stopped agent can leave processes behind.
//
// SIGINT is not TerminateProcess and is not pretended to be. It would be
// GenerateConsoleCtrlEvent, which needs the child in a console process group
// of its own — decided at creation, so it belongs with the job object work.
func (c *windowsChild) Signal(sig syscall.Signal) error {
	switch sig {
	case syscall.SIGTERM, syscall.SIGKILL:
	default:
		return errNoTree
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handle == 0 {
		return ErrExited
	}
	// 1, the way a shell reports a process that was killed rather than one
	// that chose to stop.
	return windows.TerminateProcess(c.handle, 1)
}

func (c *windowsChild) Wait() ExitStatus {
	st := ExitStatus{}
	if _, err := windows.WaitForSingleObject(c.handle, windows.INFINITE); err != nil {
		st.Err = err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(c.handle, &code); err != nil {
		st.Err = errors.Join(st.Err, err)
	} else {
		st.Code = int(code)
	}
	st.At = time.Now()

	c.mu.Lock()
	_ = windows.CloseHandle(c.handle)
	c.handle = 0
	c.mu.Unlock()

	// Then release the pseudoconsole, which is what ends the read loop; see
	// drainGrace for why not immediately.
	time.Sleep(drainGrace)
	_ = c.pty.Close()
	return st
}
