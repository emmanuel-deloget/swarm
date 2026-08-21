package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/emmanuel-deloget/swarm/internal/ipc"
)

// swarm spawn makes an agent for one task.
//
// The task is not a flag or an afterthought: an instance is created owing it,
// and saying it is finished is what collects the agent. An instance with
// nothing asked of it would never end, so this refuses to make one.
//
// The task can come from a file or from standard input, because the useful
// ones are not one-liners. A brief written in a file, reviewed, and handed to
// an agent is a better working habit than a sentence typed into a shell — and
// it is the same brief every time it is spawned.
func cmdSpawn(args []string) error {
	var cf clientFlags
	fs := newFlagSet("spawn")
	cf.register(fs)
	file := fs.String("f", "", "read the task from a file, or - for standard input")
	quiet := fs.Bool("q", false, "print only the name of the new agent")
	if err := parseArgs(fs, args, -1); err != nil {
		return err
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return errors.New("spawn needs a template: `swarm spawn <template> \"<task>\"`")
	}
	template := rest[0]

	task := strings.Join(rest[1:], " ")
	if *file != "" {
		if task != "" {
			return errors.New("give the task on the command line or with -f, not both")
		}
		body, err := readTask(*file)
		if err != nil {
			return err
		}
		task = body
	}
	if strings.TrimSpace(task) == "" {
		return errors.New("spawn needs a task: an agent with nothing asked of it has " +
			"nothing to finish, so nothing would ever collect it")
	}

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	resp, err := c.Do(ipc.Request{
		Cmd:    ipc.CmdSpawn,
		Target: template,
		Text:   task,
		// Set inside an agent by the shim; empty means a person asked, and a
		// person is always allowed to spawn.
		From: os.Getenv("SWARM_AGENT"),
	})
	if err != nil {
		return err
	}
	if cf.asJSON {
		return emitJSON(resp)
	}
	if *quiet {
		fmt.Println(resp.Text)
		return nil
	}
	fmt.Printf("%s spawned from %s\n", resp.Text, template)
	fmt.Printf("  it owes the task; `swarm why %s` says what, and it is collected\n", resp.Text)
	fmt.Printf("  when it runs `swarm done`\n")
	return nil
}

// readTask reads a brief from a file, or from standard input for "-".
func readTask(path string) (string, error) {
	if path == "-" {
		body, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("reading the task from standard input: %w", err)
		}
		return string(body), nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading the task: %w", err)
	}
	return string(body), nil
}
