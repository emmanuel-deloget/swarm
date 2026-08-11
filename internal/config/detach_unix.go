//go:build !windows

package config

// DefaultDetachKey leaves an attached terminal when nothing else is configured.
//
// ctrl+\ is SIGQUIT's key and nothing types it by accident, which is what
// makes it a good one to reserve. Windows cannot produce it — see the file
// next to this one.
const DefaultDetachKey = "ctrl+\\"
