//go:build !windows

package main

// statusBarSupported: the reminder can live on the last row for the whole
// attach, held there by a scrolling region.
const statusBarSupported = true
