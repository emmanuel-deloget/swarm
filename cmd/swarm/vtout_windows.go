//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVTOutput asks the console to interpret escape sequences, and returns
// what puts it back.
//
// An attach is a passthrough: the agent's output goes to this terminal byte
// for byte, and all of it is escape sequences. A Windows console only acts on
// them once ENABLE_VIRTUAL_TERMINAL_PROCESSING is set, and it is not set by
// default on the older consoles — which is exactly where this matters, since
// Windows Terminal needs build 18362 while a pseudoconsole needs only 17763.
// Without it an attach prints its own control codes as text.
//
// A failure is not one: a console that refuses the mode is a console that has
// no such mode, and there is nothing better to do than carry on and let the
// output speak for itself.
func enableVTOutput() func() {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return func() {}
	}
	const want = windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	if mode&want != 0 {
		return func() {}
	}
	if err := windows.SetConsoleMode(h, mode|want); err != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }
}
