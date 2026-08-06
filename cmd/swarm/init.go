package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// starterConfig is written by `swarm init`. It describes the fleet the tool was
// built for — several dev agents, as many reviewers, one triage agent — and
// documents every knob inline so the file is its own manual.
const starterConfig = `# swarm — a fleet of terminal agents.
# Every agent is just a command line: swarm does not care which CLI it is.

session: default

# Where agents run by default. Relative paths resolve against this file.
workdir: .

# Files injected or attached to messages are staged here so every agent can
# read them by path (this is how images reach an agent).
shared: .swarm/shared

# Added to the environment of every agent.
env: {}

defaults:
  # Starting terminal geometry. The agent displayed in the TUI is resized to
  # the pane showing it, so these are the size an agent has before anyone
  # looks at it - and the size it keeps with follow_window: false.
  cols: 200
  rows: 50
  scrollback: 5000

  # Resize the displayed agent to the pane it appears in, so its own layout
  # adapts instead of being cropped. Turn it off to pin the geometry above,
  # which is then what the web UI and "swarm screen" show.
  follow_window: true

  # An agent that has printed nothing for this long is reported as idle,
  # which usually means it is waiting for you.
  idle_after: 3s

  autostart: true
  restart_on_exit: false
  restart_backoff: 2s

  # Injected text is wrapped in bracketed paste, then validated after this
  # delay. Multi-line prompts land as one message instead of being submitted
  # line by line.
  bracketed_paste: true
  submit_delay: 150ms

  # push: a bus message is typed into the agent's prompt right away.
  # pull: it waits until the agent runs "swarm inbox".
  delivery: push
  message_template: "[swarm] {from} says: {body}"

# Key that leaves an attached agent, in the TUI and in "swarm attach". Move it
# when something else is capturing the terminal: tmux, screen and asciinema all
# want ctrl+\ too. Run "swarm keys -list" for the names accepted here.
detach_key: 'ctrl+\'

web:
  enabled: true
  # 127.0.0.1 keeps it local. Use 0.0.0.0 to reach it from your phone, and
  # set tls_cert/tls_key (or put it behind a tunnel) before you do.
  addr: 127.0.0.1:7777
  # Leave empty to get a fresh token at every start; it is printed in the TUI.
  token: ""
  read_only: false

bus:
  enabled: true
  history: 200
  allow_self_inject: false

# Named sets of agents, usable as @name wherever a target is expected.
groups:
  dev: [dev-1, dev-2, dev-3, dev-4, dev-5]
  review: [review-1, review-2, review-3, review-4, review-5]

agents:
  - name: dev-1
    role: dev
    command: [claude]
    workdir: .
    # Patterns classify what the agent shows. A matching pattern turns into a
    # state badge in the TUI and an event in the log, so you can spot the
    # agent that needs you without watching every terminal yourself.
    patterns:
      - match: "(?i)\\b(do you want|proceed\\?|\\[y/n\\]|allow\\b.*\\?)"
        state: approval
        notify: true
      - match: "(?i)^\\s*(error|fatal|panic):"
        state: error
        notify: true

  - name: dev-2
    role: dev
    command: [claude]
  - name: dev-3
    role: dev
    command: [claude]
  - name: dev-4
    role: dev
    command: [codex]
  - name: dev-5
    role: dev
    command: [codex]

  - name: review-1
    role: review
    command: [claude]
    # Reviewers are often busy reading; let them collect messages themselves
    # instead of having text typed into their prompt mid-thought.
    delivery: pull
  - name: review-2
    role: review
    command: [claude]
    delivery: pull
  - name: review-3
    role: review
    command: [claude]
    delivery: pull
  - name: review-4
    role: review
    command: [codex]
    delivery: pull
  - name: review-5
    role: review
    command: [codex]
    delivery: pull

  - name: triage
    role: triage
    command: [claude]
    restart_on_exit: true
`

func cmdInit(args []string) error {
	fs := newFlagSet("init")
	force := fs.Bool("force", false, "overwrite an existing file")
	_ = parseArgs(fs, args, -1)

	path := fs.Arg(0)
	if path == "" {
		path = "swarm.yaml"
	}
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		path = filepath.Join(path, "swarm.yaml")
	}
	if _, err := os.Stat(path); err == nil && !*force {
		return fmt.Errorf("%s already exists (use -force to overwrite)", path)
	}
	if err := os.WriteFile(path, []byte(starterConfig), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", path)
	fmt.Println("edit the agent commands, then run: swarm run")
	return nil
}
