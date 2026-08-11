//go:build windows

package main

// statusBarSupported is false here, and the reminder is printed once instead.
//
// Keeping a line at the bottom of somebody else's screen needs a scrolling
// region (DECSTBM) to fence the agent above it, and a redraw after every chunk
// in case the agent cleared the screen anyway. A Windows console does not hold
// that fence the way a terminal does: the agent's output scrolls the reserved
// row along with the rest, the redraw puts a fresh copy below it, and after a
// few screens of output the bar is stacked dozens of times across the display.
//
// Seen on Windows 10, in conhost. Which sequence it mishandles — DECSTBM, the
// save/restore around it, or writing near the last column — is not something to
// work out from another operating system, so what is removed here is the
// mechanism rather than the information: the same line is printed once, when
// the attach starts.
const statusBarSupported = false
