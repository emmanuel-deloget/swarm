//go:build !windows

package main

// enableVTOutput is a no-op here: a Unix terminal has always interpreted the
// escape sequences an attached agent produces. See the Windows file.
func enableVTOutput() func() { return func() {} }

// attachOutputMode is a no-op here: a Unix terminal already behaves the way an
// attach needs. See the Windows file.
func attachOutputMode() func() { return enableVTOutput() }
