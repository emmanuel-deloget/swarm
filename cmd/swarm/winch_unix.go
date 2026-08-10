//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// notifyResize asks to be told when this terminal changes size, so that an
// attached agent follows the window it is displayed in.
//
// Unix has a signal for it. Windows reports the same thing as a
// WINDOW_BUFFER_SIZE_EVENT record on the console input handle — an event to be
// read, not a signal to be caught — which is why this is a function behind a
// build tag rather than a constant passed to signal.Notify.
func notifyResize(ch chan<- os.Signal) { signal.Notify(ch, syscall.SIGWINCH) }
