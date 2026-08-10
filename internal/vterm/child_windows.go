//go:build windows

package vterm

import "errors"

// The Windows child is not implemented yet: this file is what the port fills
// in. What it will need is already decided — a pseudoconsole from
// CreatePseudoConsole for the terminal, a job object for ending the tree — and
// none of it can be expressed with the calls the Unix side uses.
//
// It fails loudly rather than quietly. An agent that never starts is a visible
// problem carrying its own cause; one that appears to start and then answers
// nothing costs an afternoon to understand.

var errNoConPTY = errors.New("vterm: swarm has no Windows terminal yet; " +
	"agents need a pseudoconsole (Windows 10 build 17763 or later)")

func spawn(Options) (child, error) { return nil, errNoConPTY }
