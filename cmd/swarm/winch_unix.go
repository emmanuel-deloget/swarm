//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// watchResize calls back whenever this terminal changes size, until stop is
// closed. Unix has a signal for it; see the Windows file for the other way.
func watchResize(stop <-chan struct{}, fn func()) {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	for {
		select {
		case <-winch:
			fn()
		case <-stop:
			return
		}
	}
}
