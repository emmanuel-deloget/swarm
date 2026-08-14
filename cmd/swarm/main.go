// Command swarm runs a fleet of terminal agents and lets you drive them from
// one place: a TUI, a web page, or the swarm CLI itself — which is also how
// the agents talk to each other.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/emmanuel-deloget/swarm/internal/sockpath"
)

const usage = `swarm — run and drive a fleet of terminal agents

usage: swarm <command> [flags] [args]

fleet
  run                       start the fleet, the TUI, the control socket and the web UI
  init [path]               write a starter swarm.yaml
  ls                       list the agents and their state
  status [target]           detailed status of the matching agents
  start|stop|restart <target>
  shutdown                  stop the whole swarm

driving an agent
  inject <target> [text]    type text into the agent prompt
  keys <target> <keys...>   send key presses (ctrl+c, esc, enter, up, ...)
  keys -list                list the key names swarm understands
  screen <target>           print what the agent's terminal shows right now
  attach <agent>            take over an agent's terminal in this window
  logs <agent>              show the recorded terminal output

talking
  send <target> <message>   send a bus message (agents use this to reach peers)
  broadcast <message>       send to every agent
  inbox [agent]             collect the messages addressed to you
  stage <file>              copy a file where every agent can read it
  events                    show the swarm event log
  bus tail [-f]             the messages agents send each other, as they go
  bus stats [-since 1h]     how much of the fleet's time went into talking
  hook test <payload.json>  show what an incoming webhook would send
  hook post <payload.json>  send a payload to the running listener

  spawn <template> <task>   make an agent for one task; -f reads it from a file
  why [agent]               why an agent is stalled, and how it gets out
  info                      session, socket, web URL and token
  config check [-fix]       report (and fix) a config that has gone stale
  version                   which build this is (also shown in the TUI header)
  licenses [name]           the terms of everything bundled in this binary

A target is an agent name, @group, @role, "all", or a comma-separated list.

Run "swarm <command> -h" for the flags of a command.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	// Nearly every command prints escape sequences — colours in `ls`, a whole
	// agent screen in `attach`. A Windows console has to be asked to interpret
	// them; everywhere else this does nothing. See vtout_windows.go.
	//
	// Not restored on the way out: several paths here end in os.Exit, and a
	// console that understands escape sequences is not a state worth undoing.
	// `attach`, which turns on far more than this, still restores its own.
	enableVTOutput()

	cmd, args := os.Args[1], os.Args[2:]

	var err error
	switch cmd {
	case "run":
		err = cmdRun(args)
	case "init":
		err = cmdInit(args)
	case "ls", "list":
		err = cmdList(args, false)
	case "status":
		err = cmdList(args, true)
	case "start":
		err = cmdLifecycle("start", args)
	case "stop":
		err = cmdLifecycle("stop", args)
	case "restart":
		err = cmdLifecycle("restart", args)
	case "inject", "type":
		err = cmdInject(args)
	case "keys", "key":
		err = cmdKeys(args)
	case "screen":
		err = cmdScreen(args)
	case "attach":
		err = cmdAttach(args)
	case "logs", "log":
		err = cmdLogs(args)
	case "send", "tell":
		err = cmdSend(args, false)
	case "broadcast":
		err = cmdSend(args, true)
	case "inbox":
		err = cmdInbox(args)
	case "done":
		err = cmdDone(args)
	case "stage":
		err = cmdStage(args)
	case "events":
		err = cmdEvents(args)
	case "hook", "hooks":
		err = cmdHook(args)
	case "info":
		err = cmdInfo(args)
	case "shutdown":
		err = cmdShutdown(args)
	case "bus":
		err = cmdBus(args)
	case "config":
		err = cmdConfig(args)
	case "spawn":
		err = cmdSpawn(args)
	case "why":
		err = cmdWhy(args)
	case "licenses", "licences":
		err = cmdLicenses(args)
	case "version", "-version", "--version", "-v":
		err = cmdVersion(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "swarm: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "swarm: "+err.Error())
		os.Exit(1)
	}
}

// configNames are the file names looked up when -c is not given.
var configNames = []string{"swarm.yaml", "swarm.yml", ".swarm.yaml", ".swarm.yml"}

// findConfig walks up from the working directory looking for a config file.
func findConfig() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		for _, name := range configNames {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// resolveSocket finds the control socket of the swarm to talk to.
//
// Inside an agent, $SWARM_SOCKET is already set by the hub, which is what
// makes `swarm send` work with no configuration at all.
func resolveSocket(flagSocket, flagConfig string) (string, error) {
	if flagSocket != "" {
		return flagSocket, nil
	}
	if s := os.Getenv("SWARM_SOCKET"); s != "" {
		return s, nil
	}
	path := flagConfig
	if path == "" {
		path = findConfig()
	}
	if path != "" {
		cfg, err := loadConfig(path)
		if err != nil {
			return "", err
		}
		return sockpath.Resolve(filepath.Join(cfg.Dir(), ".swarm"), cfg.Session), nil
	}
	// Last resort: the default session in the working directory.
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	candidate := sockpath.Resolve(filepath.Join(wd, ".swarm"), "default")
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("no swarm.yaml found and no $SWARM_SOCKET set; pass -socket or run from the project directory")
	}
	return candidate, nil
}

func joinArgs(args []string) string { return strings.Join(args, " ") }
