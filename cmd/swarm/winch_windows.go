//go:build windows

package main

import (
	"time"

	"github.com/charmbracelet/x/term"

	"os"
)

// resizePoll is how often the console is asked how big it is. Often enough that
// dragging a window edge settles before it is let go, seldom enough that the
// cost is a syscall a few times a second.
const resizePoll = 250 * time.Millisecond

// watchResize calls back whenever this terminal changes size, until stop is
// closed.
//
// Windows has no SIGWINCH. It reports a resize as a WINDOW_BUFFER_SIZE_EVENT
// record on the console input handle — which an attach cannot read, because it
// is reading that handle as a byte stream and consuming the records would eat
// the keystrokes. So it asks instead. A comparison a few times a second is not
// elegant, but the alternative is an attach that never follows its window, and
// an agent laid out for a size that no longer exists wraps every line it draws.
func watchResize(stop <-chan struct{}, fn func()) {
	t := time.NewTicker(resizePoll)
	defer t.Stop()
	lastW, lastH, _ := term.GetSize(os.Stdout.Fd())
	for {
		select {
		case <-t.C:
			w, h, err := term.GetSize(os.Stdout.Fd())
			if err != nil || (w == lastW && h == lastH) {
				continue
			}
			lastW, lastH = w, h
			fn()
		case <-stop:
			return
		}
	}
}
