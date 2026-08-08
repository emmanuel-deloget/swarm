package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/ipc"
)

const busUsage = `swarm bus — what the agents say to each other

  swarm bus tail [-f] [-n 50]   the messages, as they are carried
  swarm bus stats [-since 1h]   how much of the fleet's time went into talking
  swarm bus pause "reason"      hold every delivery; the agents keep working
  swarm bus resume [-flush]     let them through again
  swarm bus status              whether the bus is holding anything back
`

func cmdBus(args []string) error {
	if len(args) == 0 {
		fmt.Print(busUsage)
		return nil
	}
	switch args[0] {
	case "tail":
		return cmdBusTail(args[1:])
	case "stats":
		return cmdBusStats(args[1:])
	case "pause":
		return cmdBusPause("pause", args[1:])
	case "resume":
		return cmdBusPause("resume", args[1:])
	case "status":
		return cmdBusPause("status", args[1:])
	case "help", "-h", "--help":
		fmt.Print(busUsage)
		return nil
	default:
		return fmt.Errorf("unknown bus command %q\n\n%s", args[0], busUsage)
	}
}

func cmdBusTail(args []string) error {
	var cf clientFlags
	fs := newFlagSet("bus tail")
	cf.register(fs)
	follow := fs.Bool("f", false, "keep printing as messages are carried")
	n := fs.Int("n", 50, "how many to show first")
	if err := parseArgs(fs, args, -1); err != nil {
		return err
	}
	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	return c.Stream(ipc.Request{Cmd: ipc.CmdBusTail, Lines: *n, Follow: *follow},
		func(resp ipc.Response) bool {
			for _, m := range resp.Messages {
				fmt.Println(tailLine(m))
			}
			return true
		})
}

// tailLine is one message, at a glance: when, who to whom, and enough of the
// body to recognise it. The thread is what makes a conversation legible as a
// conversation rather than a list.
func tailLine(m bus.Message) string {
	mark := "→"
	if m.Pushed {
		// Typed into the recipient's prompt rather than left in its mailbox:
		// the difference between interrupting and waiting to be asked.
		mark = "⇥"
	}
	body := strings.ReplaceAll(strings.TrimSpace(m.Body), "\n", " ⏎ ")
	if len(body) > 100 {
		body = body[:100] + "…"
	}
	files := ""
	if len(m.Files) > 0 {
		files = fmt.Sprintf(" (+%d file)", len(m.Files))
	}
	kind := ""
	if m.Kind != bus.KindNote {
		kind = " [" + string(m.Kind) + "]"
	}
	return fmt.Sprintf("%s  #%-4d %-10s %s %-10s%s %s%s",
		m.At.Format("15:04:05"), m.Thread, m.From, mark, m.To, kind, body, files)
}

func cmdBusStats(args []string) error {
	var cf clientFlags
	fs := newFlagSet("bus stats")
	cf.register(fs)
	since := fs.Duration("since", time.Hour, "how far back to look")
	if err := parseArgs(fs, args, -1); err != nil {
		return err
	}
	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	resp, err := c.Do(ipc.Request{Cmd: ipc.CmdBusStats, Since: *since})
	if err != nil {
		return err
	}
	if resp.Stats == nil {
		return fmt.Errorf("no statistics came back")
	}
	printStats(*resp.Stats)
	return nil
}

func printStats(s bus.Stats) {
	fmt.Printf("over the last %s\n\n", durLabel(s.Window))
	fmt.Printf("  messages   %d\n", s.Messages)
	fmt.Printf("  threads    %d\n", s.Threads)
	if s.Deepest > 0 {
		fmt.Printf("  deepest    %d messages on one thread\n", s.Deepest)
	}
	if s.Messages == 0 {
		return
	}

	if len(s.Kinds) > 0 {
		fmt.Println("\nby kind")
		for _, k := range bus.Kinds() {
			if n := s.Kinds[k]; n > 0 {
				fmt.Printf("  %-10s %d\n", k, n)
			}
		}
	}

	if len(s.Pairs) > 0 {
		fmt.Println("\nbusiest exchanges")
		for i, p := range s.Pairs {
			if i == 8 {
				fmt.Printf("  … and %d more\n", len(s.Pairs)-i)
				break
			}
			fmt.Printf("  %-10s → %-10s %s\n", p.From, p.To, bar(p.Count, s.Pairs[0].Count))
		}
	}

	fmt.Println("\nper agent")
	rows := [][]string{{"AGENT", "SENT", "RECEIVED", "UNREAD"}}
	for _, name := range names(s.Sent, s.Received, s.Unread) {
		rows = append(rows, []string{
			name, fmt.Sprint(s.Sent[name]), fmt.Sprint(s.Received[name]), msgLabel(s.Unread[name]),
		})
	}
	fmt.Println()
	printTable(rows)
}

// bar draws a count so the shape of the traffic is visible without reading the
// numbers — which is the whole reason to look at this rather than at the tail.
func bar(n, top int) string {
	const width = 24
	if top <= 0 {
		return ""
	}
	filled := n * width / top
	if filled == 0 {
		filled = 1
	}
	return strings.Repeat("█", filled) + fmt.Sprintf(" %d", n)
}

func names(sets ...map[string]int) []string {
	seen := map[string]bool{}
	var out []string
	for _, set := range sets {
		for name := range set {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// cmdBusPause works the circuit breaker. Pausing is what you reach for when you
// have stopped understanding what the fleet is doing: the agents keep working
// and keep their terminals, and only what they say to each other waits.
func cmdBusPause(action string, args []string) error {
	var cf clientFlags
	fs := newFlagSet("bus " + action)
	cf.register(fs)
	flush := fs.Bool("flush", false, "on resume, hand over what piled up instead of leaving it in the mailboxes")
	if err := parseArgs(fs, args, 0); err != nil {
		return err
	}
	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	resp, err := c.Do(ipc.Request{
		Cmd:   ipc.CmdBusPause,
		Text:  action,
		Keys:  joinArgs(fs.Args()),
		Flush: *flush,
	})
	if err != nil {
		return err
	}
	if resp.Paused == "" {
		fmt.Println("the bus is delivering")
		return nil
	}
	fmt.Printf("the bus is holding messages back: %s\n", resp.Paused)
	return nil
}
