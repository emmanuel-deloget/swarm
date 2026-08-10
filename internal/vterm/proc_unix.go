//go:build !windows

package vterm

import (
	"os/exec"
	"syscall"
)

// How a child is placed in the world, and how it is asked to leave it. Both
// answers are specific to the operating system, and neither has an equivalent
// on the other side: Unix has sessions, controlling terminals and signals;
// Windows has pseudoconsoles, job objects and console control events. Keeping
// them behind these two functions is what lets the rest of the package say
// "start it" and "signal it" without knowing which world it is in.

// setSessionLeader puts the child in a session of its own with the pty as its
// controlling terminal. That is what makes job control, SIGINT-on-^C and
// terminal queries work: without it the child stays in our session and reads
// its keys from our terminal rather than from the pty it was given.
func setSessionLeader(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
}

// signalGroup signals a whole process tree. Agent CLIs wrap themselves in
// shells and spawn node children; signalling the leader alone leaves those
// running and holding the pty open. The negative pid addresses the process
// group, and the leader alone is the fallback for a child that somehow has
// none.
func signalGroup(pid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pid, sig); err != nil {
		return syscall.Kill(pid, sig)
	}
	return nil
}
