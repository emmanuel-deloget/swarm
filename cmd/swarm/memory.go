package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/ipc"
	"github.com/emmanuel-deloget/swarm/internal/memory"
)

// What the fleet knows, from the outside.
//
// An entry is a key and one short line, and the command refuses anything else
// rather than asking for it — a prompt that asks for brevity gets an essay
// about brevity. The key is what makes an entry correctable: writing it again
// replaces what was there, which is why there is no edit command.

func cmdRemember(args []string) error {
	var cf clientFlags
	fs := newFlagSet("remember")
	cf.register(fs)
	// One positional before the text: the key, then the fact as written.
	_ = parseArgs(fs, args, 1)

	key := fs.Arg(0)
	fact := joinArgs(argsFrom(fs, 1))
	if key == "" || fact == "" {
		return fmt.Errorf("usage: swarm remember <key> <one short line>   " +
			"(e.g. swarm remember gate-runtime \"make integration takes 8-12 min\")")
	}
	sender, err := whoAmI("")
	if err != nil {
		return err
	}

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	resp, err := c.Do(ipc.Request{Cmd: ipc.CmdRemember, From: sender, Name: key, Text: fact})
	if err != nil {
		return err
	}
	if cf.asJSON {
		return emitJSON(resp.Memory)
	}
	printMemory(resp.Memory, "")
	return nil
}

func cmdForget(args []string) error {
	var cf clientFlags
	fs := newFlagSet("forget")
	cf.register(fs)
	_ = parseArgs(fs, args, -1)

	key := fs.Arg(0)
	if key == "" {
		return fmt.Errorf("usage: swarm forget <key>   (`swarm recall` lists them)")
	}
	sender, err := whoAmI("")
	if err != nil {
		return err
	}

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	resp, err := c.Do(ipc.Request{Cmd: ipc.CmdForget, From: sender, Name: key})
	if err != nil {
		return err
	}
	if cf.asJSON {
		return emitJSON(resp)
	}
	fmt.Printf("%s forgotten\n", key)
	return nil
}

func cmdRecall(args []string) error {
	var cf clientFlags
	fs := newFlagSet("recall")
	cf.register(fs)
	_ = parseArgs(fs, args, 0)

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	resp, err := c.Do(ipc.Request{Cmd: ipc.CmdRecall, Text: joinArgs(fs.Args())})
	if err != nil {
		return err
	}
	if cf.asJSON {
		return emitJSON(resp.Memory)
	}
	printMemory(resp.Memory, resp.Text)
	return nil
}

// ago is how old an entry is, in words. Rounding to the minute turns anything
// fresh into nothing at all, and "-" beside a line somebody just wrote reads
// as a fault.
func ago(at time.Time) string {
	d := time.Since(at)
	if d < time.Minute {
		return "just now"
	}
	return durLabel(d.Round(time.Minute)) + " ago"
}

// printMemory lists what is held, newest first, with how old each entry is.
// Age is shown because a memory ages: a fact nobody has restated in a month is
// a fact worth looking at again.
func printMemory(entries []memory.Entry, footer string) {
	if len(entries) == 0 {
		fmt.Println("nothing remembered")
		if footer != "" {
			fmt.Fprintln(os.Stderr, "swarm: "+footer)
		}
		return
	}
	width := 0
	for _, e := range entries {
		if n := len(e.Key); n > width {
			width = n
		}
	}
	for _, e := range entries {
		fmt.Printf("\x1b[1m%-*s\x1b[0m  %s\n", width, e.Key, e.Fact)
		fmt.Printf("%s  \x1b[90m%s, %s\x1b[0m\n", strings.Repeat(" ", width), e.By, ago(e.At))
	}
	if footer != "" {
		fmt.Fprintln(os.Stderr, "swarm: "+footer)
	}
}
