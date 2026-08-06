package main

import (
	"flag"
	"strings"
)

// argsFrom returns the positional arguments from index n onwards, and nothing
// when there are fewer than that. fs.Args()[n:] panics instead, which is how
// `swarm keys` with no arguments crashed rather than printing its usage.
func argsFrom(fs *flag.FlagSet, n int) []string {
	args := fs.Args()
	if len(args) <= n {
		return nil
	}
	return args[n:]
}

// parseArgs parses a command line where flags may appear after the positional
// arguments, which is how anyone naturally writes it:
//
//	swarm inject dev-1 -file shot.png "what is wrong here?"
//
// Go's flag package stops at the first positional argument, so "-file" would
// otherwise end up as part of the message.
//
// textAfter says how many positional arguments come before free text begins.
// Everything from the first non-flag argument past that point is taken
// literally, so a message is never mistaken for a flag:
//
//	swarm send dev-1 "check the -json output"   → the message keeps -json
//	swarm inject dev-1 -submit=false "half"     → -submit is a flag
//
// A negative textAfter means the command takes no free text, and flags are
// recognised anywhere. "--" always ends flag parsing.
func parseArgs(fs *flag.FlagSet, args []string, textAfter int) error {
	var flags, positional []string
	seen := 0

	for i := 0; i < len(args); i++ {
		a := args[i]

		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}

		isFlag := len(a) > 1 && a[0] == '-'
		if !isFlag {
			positional = append(positional, a)
			seen++
			if textAfter >= 0 && seen > textAfter {
				// Free text starts here: take the rest as it was written.
				positional = append(positional, args[i+1:]...)
				break
			}
			continue
		}

		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			flags = append(flags, a)
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			// Unknown: hand it to the FlagSet so it reports it properly.
			flags = append(flags, a)
			continue
		}
		flags = append(flags, a)
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}

	return fs.Parse(append(flags, positional...))
}
