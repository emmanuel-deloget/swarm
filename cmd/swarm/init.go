package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/emmanuel-deloget/swarm/internal/config"
)

// starterConfig is written by `swarm init`. It describes the fleet the tool was
// built for — several dev agents, as many reviewers, one triage agent — and
// documents every knob inline so the file is its own manual.
const starterConfig = `# swarm — a fleet of terminal agents.
# Every agent is just a command line: swarm does not care which CLI it is.
#
# This file starts small on purpose: one agent, nothing listening on a port.
# Everything else is here as a commented example. The exhaustive reference,
# with every key and its default, is in docs/configuration.md.

session: default

# Where agents run by default. Relative paths in this file resolve against the
# directory holding it, never against your working directory.
workdir: .

agents:
  # A name and a command are the only required fields. The command is an argv:
  # [claude], [codex], [aider, --model, ...] — anything that runs in a terminal.
  - name: dev-1
    role: dev             # free-form, and it gives you @dev as a target
    command: [claude]

  # Add as many as you have work for; nothing in swarm assumes a number.
  #
  # - name: review-1
  #   role: review
  #   command: [codex]
  #   # Reviewers are often busy reading. Defer holds a message until the agent
  #   # falls quiet, so a conversation follows work without cutting into it;
  #   # pull leaves it in a mailbox for "swarm inbox" and interrupts nothing.
  #   delivery: defer
  #   # Its own clone of the repository, made once and kept between runs.
  #   workspace: clone
  #   # What it is told at launch, typed once when it first falls quiet.
  #   message: |
  #     You review pull requests. Start with "swarm inbox".

# Inherited by every agent that does not override them. These are the built-in
# values, spelled out so you can see what you would be changing.
#
# defaults:
#   cols: 200             # geometry before anyone looks at the agent
#   rows: 50
#   scrollback: 5000      # lines kept above the screen, walked with pgup
#   follow_window: true   # resize the displayed agent to the pane showing it
#   idle_after: 3s        # quiet for this long -> reported as "idle"
#   autostart: true       # launch with the swarm
#   restart_on_exit: false
#   restart_backoff: 2s   # doubles on each death that follows a short run
#   restart_max_wait: 1m  # cap on that, and what counts as "it ran"
#   restart_max: 5        # deaths in a row before swarm stops trying; 0 = never
#   key_delay: 40ms       # pause between keys when several are sent at once
#   submit_delay: 120ms   # pause between pasting text and pressing Enter
#   bracketed_paste: true # a multi-line prompt arrives as one message
#   delivery: push        # push, pull, or defer
#   message_template: "[swarm] message from {from}: {body}"
#   workspace: shared     # shared, clone, or none
#   # Run before the process starts and after it has gone, in the agent's
#   # working directory with its environment: install dependencies, copy a
#   # .env, take down what was started. A failing on_start stops the agent
#   # rather than launching it into a half-prepared directory.
#   on_start: ["./scripts/prepare-agent.sh"]
#   on_exit: ["./scripts/cleanup-agent.sh"]
#   # The only agents this one may write to. Unset means everyone.
#   can_send: ["@review", triage-1]

# Added to every agent's environment; a per-agent "env:" overrides it.
# {alloc_port} in any value becomes a port nobody is listening on, picked once
# per agent and kept across restarts — two dev servers both want 3000, and no
# amount of talking to each other settles that.
#
# env:
#   PORT: "{alloc_port}"

# Named sets of agents. A target is an agent name, @group, @role, "all", or a
# comma-separated list of those.
#
# groups:
#   backend: [dev-1]      # add the other names as you define them

# Patterns classify what an agent shows. A match becomes a state badge in the
# TUI and, with notify, an entry in the event log — so you can spot the agent
# that needs you without watching every terminal yourself.
#
#     patterns:
#       - match: "(?i)\\b(do you want|proceed\\?|\\[y/n\\])"
#         state: approval
#         notify: true
#       - match: "(?i)^\\s*(error|fatal|panic):"
#         state: error
#         notify: true
#       # reply answers a prompt on your behalf. Only for prompts you would
#       # always answer the same way.
#       # - match: "Continue\\? \\(y/n\\)"
#       #   reply: "y"

# Record everything swarm sends to an agent, in .swarm/logs/<agent>.input.log:
# injections, key presses, and the replies the emulator gives to the agent's
# own queries. It settles "did swarm type that, or did the agent print it
# itself?". Off by default, and written 0600: it holds what you typed.
#
# log_input: true

# Mouse reporting in the TUI. Off by default: a terminal that reports mouse
# events stops selecting text itself, and reading an agent's output matters
# more than the wheel. "M" toggles it while running.
#
# mouse: true

# The key that leaves an attached agent. The default is ctrl+\ , which is also
# what tmux, screen and asciinema like to grab — hence this knob.
# "swarm keys -list" prints every usable name.
#
# detach_key: "ctrl+g"

# The web remote control: one page with every terminal, live and typeable.
# Past localhost, treat its URL as a shell on your machine — set a token you
# chose, or read_only, and put it behind TLS or a tunnel.
#
# web:
#   enabled: true
#   addr: 127.0.0.1:7777
#   token: ""             # empty -> a fresh one at each start, shown in the TUI
#   read_only: false
#   # tls_cert: cert.pem
#   # tls_key: key.pem

# Incoming webhooks, turned into bus messages by declarative rules, so the
# fleet reacts to a pull request or a ticket without you relaying it.
# The full vocabulary is in docs/configuration.md.
#
# Create the secret first, and keep it out of this file:
#   mkdir -p .swarm && (umask 077; openssl rand -hex 32 > .swarm/hook-secret)
#
# hooks:
#   enabled: true
#   addr: 127.0.0.1:7778
#   secret_path: .swarm/hook-secret        # 0600; or secret_env: HOOK_SECRET
#   signature_header: X-Hub-Signature-256
#   rules:
#     - name: review-requested
#       when:
#         event: pull_request.review_requested
#       to: "@dev"        # an agent, @group, @role, or "all"
#       message: "a review was asked of you; list your open PRs."

# The other direction: fleet events posted to an endpoint of yours, with the
# incoming rules read backwards. swarm does not know what is at the far end —
# Telegram, a CI job, a script behind a proxy are all the same to it.
#
# Events: agent.started, agent.exited, agent.idle, agent.done (quiet with
# changes under it), agent.attention, agent.error.
#
# Create its secret first, as for the listener:
#   (umask 077; openssl rand -hex 32 > .swarm/out-secret)
#
# outgoing:
#   enabled: true
#   url: https://example.invalid/swarm
#   secret_path: .swarm/out-secret
#   signature_header: X-Swarm-Signature
#   retries: 2
#   rules:
#     - name: finished
#       when:
#         event: agent.done
#       body: "{agent} finished on {data.branch}"

# Inter-agent messaging. On by default: agents reaching each other with
# "swarm send" is the point of running them together.
#
# bus:
#   enabled: true
#   history: 200          # messages kept per mailbox
#   allow_self_inject: false
#   # A thread is one conversation. Past this many messages the bus refuses the
#   # next one, and the refusal tells the agent to decide or escalate — because
#   # an agent is the only thing that reads it. 0 means no bound.
#   max_turns: 6
#   escalate_to: triage-1 # arbitrates a thread that ran out of turns

# Overrides every agent's delivery for one kind of message: the fleet can defer
# while "somebody is stuck" still gets through. Kinds are set with
# "swarm send -kind": question, answer, fyi, request, decision, blocked.
#
# delivery_by_kind:
#   blocked: push
#   fyi: pull

# Everything swarm writes: the control socket, the logs, the CLI shim, the
# staged files, and the per-agent clones. "swarm init" offers to gitignore it.
#
# state_dir: .swarm

# Your own template for the AGENTS.md the fleet generates for itself. It gets
# the same data the built-in one does; see docs/configuration.md.
#
# agents_template: agents.tmpl
`

func cmdInit(args []string) error {
	fs := newFlagSet("init")
	force := fs.Bool("force", false, "overwrite an existing file")
	stateDir := fs.String("swarm-dir", config.DefaultStateDir, "where swarm keeps its state, relative to the config")
	yes := fs.Bool("yes", false, "answer yes to the .gitignore question")
	if err := parseArgs(fs, args, -1); err != nil {
		return err
	}

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

	body := starterConfig
	if *stateDir != config.DefaultStateDir {
		body = strings.Replace(body, "workdir: .\n",
			"workdir: .\n\n# Where swarm keeps its state: socket, logs, shim, staged files.\nstate_dir: "+*stateDir+"\n", 1)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", path)

	if err := offerGitignore(filepath.Dir(path), *stateDir, *yes); err != nil {
		// Not fatal: the config is written, and the only thing lost is a line
		// somebody can add by hand.
		fmt.Fprintln(os.Stderr, "swarm: "+err.Error())
	}

	fmt.Println("edit the agent commands, then run: swarm run")
	return nil
}

// offerGitignore adds the state directory to .gitignore, asking first. Everything
// swarm writes goes there — sockets, logs, a shim binary, whatever agents stage —
// and none of it belongs in a repository.
func offerGitignore(dir, stateDir string, yes bool) error {
	if !insideGitRepo(dir) {
		return nil
	}
	pattern := strings.TrimSuffix(filepath.ToSlash(stateDir), "/") + "/"
	gitignore := filepath.Join(dir, ".gitignore")

	current, err := os.ReadFile(gitignore)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if ignoresPattern(string(current), pattern) {
		return nil
	}

	what := ".gitignore"
	if len(current) == 0 {
		what = ".gitignore (it does not exist yet)"
	}
	if !yes && !confirm(fmt.Sprintf("add %q to %s?", pattern, what)) {
		fmt.Printf("left alone; add %q yourself if you change your mind\n", pattern)
		return nil
	}

	out := string(current)
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	if out != "" {
		out += "\n"
	}
	out += "# swarm's own state: socket, logs, staged files, the CLI shim\n" + pattern + "\n"
	if err := os.WriteFile(gitignore, []byte(out), 0o644); err != nil {
		return err
	}
	fmt.Printf("added %q to %s\n", pattern, gitignore)
	return nil
}

// ignoresPattern reports whether the file already covers the directory, allowing
// for the shapes people write by hand: with or without a trailing slash, and with
// or without a leading one.
func ignoresPattern(body, pattern string) bool {
	bare := strings.Trim(pattern, "/")
	for _, line := range strings.Split(body, "\n") {
		switch strings.Trim(strings.TrimSpace(line), "/") {
		case "":
		case bare:
			return true
		}
	}
	return false
}

// insideGitRepo walks up looking for a .git. Outside a repository there is
// nothing to ignore, and asking would be noise.
func insideGitRepo(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return false
		}
		abs = parent
	}
}

// confirm asks a yes/no question. A terminal that cannot answer — a pipe, a
// script — declines rather than blocking, which is what -yes is for.
func confirm(question string) bool {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return false
	}
	fmt.Printf("%s [y/N] ", question)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "o", "oui":
		return true
	}
	return false
}
