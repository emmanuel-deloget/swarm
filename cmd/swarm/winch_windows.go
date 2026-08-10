//go:build windows

package main

import "os"

// notifyResize subscribes to nothing here: a Windows console reports a resize
// as a WINDOW_BUFFER_SIZE_EVENT record on its input handle, and reading those
// means owning the console input loop rather than registering a signal.
//
// The consequence is stated rather than worked around: an attach does not yet
// follow the window, and the agent keeps the geometry its configuration gave
// it — which is exactly what -keep-size asks for on Unix.
func notifyResize(chan<- os.Signal) {}
