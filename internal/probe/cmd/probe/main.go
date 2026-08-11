// Command probe is the child process the end-to-end tests drive instead of a
// shell. See internal/probe for what it does.
//
// The other packages get the same behaviour by running their own test binary
// again, which costs nothing to build. The end-to-end tests build a real
// binary instead: they already build swarm itself, and a separate program has
// no TestMain to reach, no testing flags to survive and no race instrumentation
// to carry — one less thing between a failing test and its cause.
package main

import (
	"os"

	"github.com/emmanuel-deloget/swarm/internal/probe"
)

func main() {
	if !probe.Run(os.Args[1:]) {
		os.Exit(2)
	}
}
