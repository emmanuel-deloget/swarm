package main

import (
	"fmt"

	"github.com/emmanuel-deloget/swarm/internal/version"
)

func cmdVersion(args []string) error {
	fs := newFlagSet("version")
	short := fs.Bool("short", false, "print just the version, for a script")
	if err := parseArgs(fs, args, -1); err != nil {
		return err
	}
	if *short {
		fmt.Println(version.Short())
		return nil
	}
	fmt.Print(version.Read().Long())
	return nil
}
