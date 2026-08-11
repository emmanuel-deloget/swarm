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

// attachOutputMode prepares the console for a passthrough, and returns what
// puts it back.
//
// On top of interpreting escape sequences, it turns off
// ENABLE_WRAP_AT_EOL_OUTPUT. A terminal writing the last column of a row
// leaves the cursor there and wraps only when the *next* character arrives —
// the pending-wrap rule every VT has followed since the DEC VT100. A console
// with this flag wraps immediately, so the last character of a repaint scrolls
// the screen by one line, and everything after it lands a row low.
//
// Nothing here relies on the console wrapping for it: what an attach forwards
// is a rendering of the agent's screen, already broken into rows.
func attachOutputMode() func() {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return func() {}
	}
	want := (mode | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) &^ windows.ENABLE_WRAP_AT_EOL_OUTPUT
	if want == mode {
		return func() {}
	}
	if err := windows.SetConsoleMode(h, want); err != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }
}
