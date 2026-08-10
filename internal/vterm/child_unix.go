//go:build !windows

package vterm

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// unixChild is a process in a session of its own with the pty as its
// controlling terminal. That arrangement is what makes job control,
// SIGINT-on-^C and terminal queries work: without it the child stays in our
// session and reads its keys from our terminal rather than from the pty it was
// given.
type unixChild struct {
	cmd *exec.Cmd
	ptm *os.File // pty master
}

func spawn(o Options) (child, error) {
	cmd := exec.Command(o.Command[0], o.Command[1:]...)
	cmd.Dir = o.Dir
	cmd.Env = o.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}

	ptm, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(o.Cols), Rows: uint16(o.Rows)})
	if err != nil {
		return nil, fmt.Errorf("vterm: start %s: %w", o.Command[0], err)
	}
	return &unixChild{cmd: cmd, ptm: ptm}, nil
}

func (c *unixChild) Read(p []byte) (int, error)  { return c.ptm.Read(p) }
func (c *unixChild) Write(p []byte) (int, error) { return c.ptm.Write(p) }
func (c *unixChild) Close() error                { return c.ptm.Close() }

func (c *unixChild) Resize(cols, rows int) error {
	return pty.Setsize(c.ptm, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (c *unixChild) Pid() int {
	if c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

// Signal addresses the process group, so that a whole tool tree — shell
// wrappers, node children — gets it and not just the leader. The leader alone
// is the fallback for a child that somehow has no group.
func (c *unixChild) Signal(sig syscall.Signal) error {
	if c.cmd.Process == nil {
		return ErrExited
	}
	pid := c.cmd.Process.Pid
	if err := syscall.Kill(-pid, sig); err != nil {
		return syscall.Kill(pid, sig)
	}
	return nil
}

func (c *unixChild) Wait() ExitStatus {
	err := c.cmd.Wait()
	st := ExitStatus{At: time.Now(), Err: err}
	if c.cmd.ProcessState != nil {
		st.Code = c.cmd.ProcessState.ExitCode()
		if ws, ok := c.cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			st.Signal = ws.Signal()
		}
	}
	return st
}
