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
	// Telling the fleet is a deliberate act rather than what writing does. An
	// entry that notified everybody on every write would make the memory a
	// platform, which is the shape a fleet runs away in — and the message goes
	// through the bus, so can_send bounds it and the budget charges it once
	// per recipient, like any other send.
	tell := fs.String("tell", "", "also say so on the bus: an agent, @group, @role or all")
	// One positional before the text: the key, then the fact as written.
	_ = parseArgs(fs, args, 1)

	key := fs.Arg(0)
	fact := joinArgs(argsFrom(fs, 1))
	if key == "" || fact == "" {
		return fmt.Errorf("usage: swarm remember [-tell <target>] <key> <one short line>   " +
			"(e.g. swarm remember gate-runtime \"make integration takes 8-12 min\")")
	}
	// Free text starts at the key, which is what stops a fact about `-race`
	// from being read as a flag — and what would otherwise write `-tell dev-2`
	// into the memory as though somebody meant it.
	if strings.Contains(" "+fact+" ", " -tell ") || strings.Contains(" "+fact+" ", " --tell ") {
		return fmt.Errorf("-tell goes before the key, or it is part of the line: " +
			"swarm remember -tell <target> <key> <one short line>")
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

	resp, err := c.Do(ipc.Request{Cmd: ipc.CmdRemember, From: sender, Name: key,
		Text: fact, Target: *tell})
	if err != nil {
		return err
	}
	if cf.asJSON {
		return emitJSON(resp)
	}
	printMemory(resp.Memory, "")
	printDelivered(resp.Messages)
	if resp.Text != "" {
		fmt.Fprintln(os.Stderr, "swarm: "+resp.Text)
	}
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
		fmt.Printf("%s  \x1b[90m%s\x1b[0m\n", strings.Repeat(" ", width), provenance(e))
	}
	if footer != "" {
		fmt.Fprintln(os.Stderr, "swarm: "+footer)
	}
}

// provenance is the second line of an entry: who wrote it, when, who they
// wrote over, and when it was last wanted.
//
// The revision is shown because writing a key again is how a memory is
// corrected and therefore also how one is quietly rewritten. The reading is
// shown because it is what eviction and ttl go by, and an operator asked why
// a fact went should not have to work that out from the source.
func provenance(e memory.Entry) string {
	who := e.By
	if e.Was != "" {
		who = fmt.Sprintf("%s, revising %s", e.By, e.Was)
		if e.Rev > 1 {
			who = fmt.Sprintf("%s, revising %s (%d times over)", e.By, e.Was, e.Rev)
		}
	}
	line := who + ", " + ago(e.At)
	if e.Used.After(e.At.Add(time.Minute)) {
		line += ", read " + ago(e.Used)
	}
	return line
}
