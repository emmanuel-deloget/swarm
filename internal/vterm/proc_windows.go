//go:build windows

package vterm

import (
	"errors"
	"os/exec"
	"syscall"
)

// The Windows half of proc_unix.go. Both operations exist there and neither is
// a signal here, so this file is where the port's remaining work is named
// rather than hidden.

// errNoSignals is what a caller gets instead of a signal it cannot send. Saying
// so is the point: a stub that returned nil would leave the hub believing an
// agent had been asked to stop while nothing was sent, and the agent would be
// reported as stopping for ever.
var errNoSignals = errors.New("vterm: signals are not available on windows; " +
	"stopping a process tree needs a job object")

// setSessionLeader has nothing to do here. Windows has no sessions and no
// controlling terminal: a pseudoconsole is bound to the child when it is
// created, through PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, which os/exec cannot
// pass. The spawn itself therefore moves to the ConPTY library rather than
// being adjusted here.
func setSessionLeader(_ *exec.Cmd) {}

// signalGroup has no equivalent. Windows ends a process tree by assigning it to
// a job object and calling TerminateJobObject, and interrupts one with
// GenerateConsoleCtrlEvent — neither of which is a signal, and neither of which
// can be expressed as one.
func signalGroup(_ int, _ syscall.Signal) error { return errNoSignals }
