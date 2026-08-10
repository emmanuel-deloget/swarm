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
	"unsafe"

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

	// job holds the child and everything it spawns. It is the closest thing
	// Windows has to a process group, and the only way to end a tool tree
	// rather than a single process.
	job windows.Handle

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

	job, err := newJob()
	if err != nil {
		return nil, err
	}

	cpty, err := conpty.New(o.Cols, o.Rows, 0)
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("vterm: pseudoconsole: %w", err)
	}

	pid, handle, err := cpty.Spawn(name, argv, &syscall.ProcAttr{Dir: o.Dir, Env: o.Env})
	if err != nil {
		_ = cpty.Close()
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("vterm: start %s: %w", o.Command[0], err)
	}

	// Assign as early as possible: everything the child spawns from here on
	// belongs to the job and can be ended with it. The window between
	// CreateProcess and this call is microseconds of process startup, before
	// the child has run a line of its own code — closing it entirely would
	// mean creating the process suspended, and conpty.Spawn closes the thread
	// handle needed to resume it.
	//
	// A failure here is fatal on purpose. An agent that cannot be stopped is
	// worse than an agent that never started, and it is not visible until the
	// day it matters.
	if err := windows.AssignProcessToJobObject(job, windows.Handle(handle)); err != nil {
		_ = windows.TerminateProcess(windows.Handle(handle), 1)
		_ = windows.CloseHandle(windows.Handle(handle))
		_ = cpty.Close()
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("vterm: job object: %w", err)
	}
	return &windowsChild{pty: cpty, pid: pid, job: job, handle: windows.Handle(handle)}, nil
}

// newJob creates the job the child will live in.
//
// KILL_ON_JOB_CLOSE makes the tree die with swarm, which is not an extra
// safeguard but the Unix behaviour rebuilt: there, closing the pty master
// hangs up the session and the agent goes with it. Without the flag a crashed
// swarm would leave a fleet of headless agents holding pseudoconsoles nobody
// reads.
func newJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("vterm: job object: %w", err)
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("vterm: job object limits: %w", err)
	}
	return job, nil
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

// Signal ends, or interrupts, the whole tree.
//
// Windows has no signals, so each one is answered by whatever means the same
// thing here — and anything with no honest equivalent is refused rather than
// approximated.
//
// SIGTERM and SIGKILL both become TerminateJobObject: the child and everything
// it spawned, at once. There is no polite variant to give SIGTERM, so the
// grace period in Stop buys nothing on Windows; it simply is not used.
//
// SIGINT is the interesting one. It is not TerminateProcess — it is a ^C, and
// under a pseudoconsole a ^C is a byte: conhost turns 0x03 on the input pipe
// into a CTRL_C_EVENT for the applications attached to it. That is the same
// path a person's keystroke takes, and it is what SIGINT does on a pty, where
// the terminal driver signals the foreground group.
//
// GenerateConsoleCtrlEvent, the API this looks like it should use, is
// deliberately not used: it needs the child created with
// CREATE_NEW_PROCESS_GROUP, and that flag disables Ctrl+C for the group — it
// would trade the interrupt someone types every day for one nothing sends.
func (c *windowsChild) Signal(sig syscall.Signal) error {
	switch sig {
	case syscall.SIGTERM, syscall.SIGKILL:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.handle == 0 {
			return ErrExited
		}
		// 1, the way a shell reports a process that was killed rather than
		// one that chose to stop.
		return windows.TerminateJobObject(c.job, 1)
	case syscall.SIGINT:
		// Outside mu on purpose: a child that has stopped reading its input
		// would block this write, and Wait must stay free to reap it.
		_, err := c.pty.Write([]byte{0x03})
		return err
	default:
		return fmt.Errorf("vterm: %v has no equivalent on windows", sig)
	}
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

	// The job goes last, and closing it takes anything the child left running
	// with it. A leader that exits while a build it started keeps going is
	// exactly the orphan this is here to prevent.
	_ = windows.CloseHandle(c.job)
	return st
}
