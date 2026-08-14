package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/hub"
	"github.com/emmanuel-deloget/swarm/internal/ipc"
)

// swarm why says why an agent is in the state it is in.
//
// stalled was the state that needed this. It is the one that means something is
// wrong, and it was the one nobody could act on: an agent stalled for two days
// has been through several context compactions by then, so the message that put
// it there is gone from the place a reader would think to look — the agent's own
// memory. Asking it is useless; it no longer knows.
//
// The bus does. A debt lives until it is settled, so who asked, what they asked
// for, on which thread and since when survive anything that happens inside the
// agent. That is the useful shape of swarm here: the fleet's external memory,
// the one part of it that does not get compacted.
//
// And the last section is the point. Knowing why you are stuck without knowing
// how to get out is only a better-documented dead end.
func cmdWhy(args []string) error {
	var cf clientFlags
	fs := newFlagSet("why")
	cf.register(fs)
	if err := parseArgs(fs, args, -1); err != nil {
		return err
	}

	// An agent asking about itself needs no argument, which is the case this
	// exists for: the agent that is stuck is the one that wants to know.
	who := strings.Join(fs.Args(), " ")
	if who == "" {
		who = os.Getenv("SWARM_AGENT")
	}

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	resp, err := c.Do(ipc.Request{Cmd: ipc.CmdWhy, Target: who})
	if err != nil {
		return err
	}
	if resp.Why == nil {
		return fmt.Errorf("no explanation came back")
	}
	printWhy(*resp.Why)
	return nil
}

func printWhy(w hub.Why) {
	if w.Gone != nil {
		printGone(w)
		return
	}
	state := w.State
	if w.Stalled {
		state = "stalled"
	}
	fmt.Printf("%s is %s, quiet for %s\n", w.Agent, state, short(w.Quiet))

	if len(w.Debts) == 0 {
		fmt.Println("\nNothing is waiting on it. Whatever it is doing or not doing,")
		fmt.Println("no message it was sent is unanswered.")
		if w.StalledAfter > 0 {
			fmt.Printf("\nIt would be called stalled after owing something for %s.\n",
				short(w.StalledAfter))
		}
		return
	}

	fmt.Printf("\nowes %d %s\n", len(w.Debts), plural(len(w.Debts), "thing", "things"))
	for _, d := range w.Debts {
		fmt.Printf("\n  thread %d — a %s from %s, %s ago (%s)\n",
			d.Thread, d.Kind, d.From, short(d.Age), d.Since.Format("2 Jan 15:04"))

		switch {
		case d.Kept && d.Text != "":
			for _, line := range strings.Split(strings.TrimRight(d.Text, "\n"), "\n") {
				fmt.Printf("    │ %s\n", line)
			}
		case d.Kept:
			fmt.Println("    │ (an empty message)")
		default:
			// Said plainly: a debt outlives the messages, and a blank here
			// would read as "nothing was asked".
			fmt.Println("    the message itself is no longer in the bus history;")
			fmt.Println("    what it asked for is gone, who asked and when is not.")
		}
		for _, f := range d.Files {
			fmt.Printf("    attached: %s\n", f)
		}
		if d.DeliveryKnown && !d.Delivered {
			fmt.Println("    never typed into the terminal — it was waiting in the mailbox")
		}
		if d.DeliveryKnown && d.Delivered && !d.Read {
			fmt.Println("    delivered to the terminal, never collected with `swarm inbox`")
		}
	}

	fmt.Println("\nhow it ends")
	d := w.Debts[0]
	fmt.Printf("  As %s:\n    %s\n", w.Agent, d.Settle)
	if !strings.HasPrefix(d.Settle, "swarm done") {
		fmt.Printf("\n  Or settle it without a reply:\n    swarm done -thread %d \"what happened\"\n",
			d.Thread)
	}
	if len(w.Debts) > 1 {
		fmt.Printf("\n  swarm done \"...\"   settles all %d at once\n", len(w.Debts))
	}
	fmt.Println("\n  Either has to come from the agent that owes it: run it inside")
	fmt.Printf("  %s, or add -from %s.\n", w.Agent, w.Agent)
}

// short prints a duration the way someone reading it would say it out loud.
// time.Duration renders two days as 51h13m8.4s, which is precise and useless
// when the question is how long something has been stuck.
func short(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%d days", int(d.Hours())/24)
	}
}

// printGone answers for an ephemeral instance that is no longer running.
//
// The debt of one a person spawned outlives it deliberately, so this is the
// only thing that can explain why something is still owed by a name that is not
// in the fleet.
func printGone(w hub.Why) {
	g := w.Gone
	fmt.Printf("%s is gone: %s\n", g.Name, g.Why)
	fmt.Printf("  a %s, spawned %s", g.Template, g.Born.Format("2 Jan 15:04"))
	if g.Parent != "" {
		fmt.Printf(" by %s", g.Parent)
	}
	fmt.Printf(", gone %s\n", g.Died.Format("2 Jan 15:04"))

	if g.Task != "" {
		fmt.Println("\nit was asked to")
		for _, line := range strings.Split(strings.TrimRight(g.Task, "\n"), "\n") {
			fmt.Printf("    │ %s\n", line)
		}
	}

	if len(w.Debts) == 0 {
		fmt.Println("\nNothing is still owed in its name.")
		return
	}
	fmt.Printf("\nand %d %s outlived it\n",
		len(w.Debts), plural(len(w.Debts), "thing", "things"))
	for _, d := range w.Debts {
		fmt.Printf("\n  thread %d — a %s from %s, %s ago\n",
			d.Thread, d.Kind, d.From, short(d.Age))
		if d.Kept && d.Text != "" {
			for _, line := range strings.Split(strings.TrimRight(d.Text, "\n"), "\n") {
				fmt.Printf("    │ %s\n", line)
			}
		}
	}
	fmt.Println("\nThe agent cannot settle it: it no longer exists. Clear it when it")
	fmt.Println("is no longer true, or hand the work to someone else:")
	fmt.Printf("    swarm done -from %s -thread %d \"what happened\"\n",
		w.Agent, w.Debts[0].Thread)
}
