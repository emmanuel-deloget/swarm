//go:build windows

package config

// DefaultDetachKey leaves an attached terminal when nothing else is configured.
//
// Not ctrl+\ as everywhere else: a Windows console translates keys to escape
// sequences itself, and its support for ctrl with punctuation is incomplete —
// ctrl+\ arrives as a plain backslash. Attaching would then be a one-way door,
// with no way out but closing the window.
//
// ctrl+g is a letter, so it becomes 0x07 the way ctrl+a..z all do. It is the
// first alternative the documentation suggests, and it was tried by hand on
// Windows 10 before being made the default here.
const DefaultDetachKey = "ctrl+g"
