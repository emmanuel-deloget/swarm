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
  #     Read "$SWARM_STATE_DIR/AGENTS.md" first: swarm writes it for this
  #     fleet and it says how to reach the other agents. You review pull
  #     requests.
  #
  # An entry with ephemeral: true is not an agent but the shape of one. Nothing
  # is started for it: "swarm spawn worker <task>" makes worker-1, worker-2 and
  # so on, each created owing that task and collected when it runs "swarm done".
  # Its task is its life.
  #
  # - name: worker
  #   ephemeral: true
  #   command: [claude]
  #   max_alive: 3          # how many may run at once
  #   max_lifetime: 2h      # collected if it never says it has finished
  #   # Its own directory and branch, taken back when it is collected. swarm
  #   # never removes one holding uncommitted work: git refuses, and swarm
  #   # never passes --force.
  #   #
  #   # Do not also tell the agent to make its own worktree — some CLIs have a
  #   # flag for it. Two managers on one tree is the one arrangement that goes
  #   # wrong, and swarm cannot warn you: it runs whatever you name here and
  #   # does not read its options.
  #   workspace: worktree
  #
  # And who may start them. The ability to create agents is the ability to
  # spend, so it is declared rather than assumed:
  #
  # - name: triage
  #   command: [claude]
  #   can_spawn: [worker]

# What applies to every ephemeral agent rather than to one template. A
# template's own max_alive bounds one kind of work; this bounds the machine,
# since three templates of three is nine agent processes nobody asked for.
#
# ephemeral:
#   max_alive: 12         # instances at once across the whole fleet
#   remember: 100         # gone instances kept on record, so swarm why can
#                         # still answer for one that died owing something

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
#   # {at} is when it was sent and {held} how long it waited before landing,
#   # empty when it did not: a deferred message arrives with no sign of its
#   # age otherwise. Also {id} {thread} {to} {kind} {files} {time}.
#   message_template: "[swarm] message from {from} at {at}{held}: {body}"
#   workspace: shared     # shared, clone, or none
#   # Run before the process starts and after it has gone, in the agent's
#   # working directory with its environment: install dependencies, copy a
#   # .env, take down what was started. A failing on_start stops the agent
#   # rather than launching it into a half-prepared directory.
#   on_start: ["./scripts/prepare-agent.sh"]
#   on_exit: ["./scripts/cleanup-agent.sh"]
#   # The only agents this one may write to. Unset means everyone.
#   can_send: ["@review", triage-1]
#   # An allowance for talking, as hit points: a balance that refills a little
#   # at a time and never passes max. max_turns bounds one conversation and can
#   # only see depth; a fleet runs away sideways — one send to ten agents is a
#   # single command and ten interruptions, and costs nothing per thread.
#   # Keep max to a few minutes of refill: a fleet that has been quiet has
#   # saved up for its worst hour, and a deep bucket funds it. Override it on a
#   # coordinator, which broadcasts for a living. bus.budget.cost sets prices.
#   budget:
#     max: 60
#     refill: 1m

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

# The key that leaves an attached agent. The default is ctrl+\ , except on
# Windows, whose console cannot produce it: there it is ctrl+g. ctrl+\ is also
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
#   # How long an agent may be idle while owing something before swarm says so.
#   # Counted from the moment it goes idle, so it adds to its idle_after.
#   stalled_after: 20m
#   max_turns: 6
#   escalate_to: triage-1 # arbitrates a thread that ran out of turns
#   # What a message costs, per recipient, by kind. The price of an act, not
#   # of an actor: interrupting ten agents is ten interruptions whoever sent
#   # it. What each agent may afford is defaults.budget, below.
#   budget:
#     cost: {fyi: 10, decision: 8, question: 5, answer: 1}
#   # What to do about an agent that is stalled. Nothing, unless you say so:
#   # swarm asks, and never restarts or reassigns anything, because the state
#   # is a guess — an agent waiting on a long test run is silent and does owe
#   # work. Left without text, the message is what "swarm why" would say.
#   # And the other question: an agent that is quiet owing *nothing* is not
#   # stalled, and nothing else notices it — it finished, or was never given
#   # anything. Same rules, and here the text is worth writing: swarm knows it
#   # has been quiet and knows nothing else.
#   on_idle:
#     - to: self
#       after: 30m
#       text: "Quiet, and you owe nobody anything. Waiting on something? Say so."
#   on_stalled:
#     - to: self          # ask the agent itself
#       every: 15m
#       max: 3
#     - to: triage-1      # still stuck two hours later? someone should know
#       after: 2h
#       kind: question

# What the fleet knows and its agents keep forgetting. An agent's context is
# compacted and what it was told an hour ago goes with it; this survives that,
# and every restart. An entry is a key and one line — "swarm remember <key>
# <line>", "swarm recall", "swarm forget <key>" — and writing a key again
# replaces what it held, which is how one is corrected.
#
# The limits are the feature: asked politely for something short, an agent
# writes an essay about brevity, so anything longer is refused rather than
# trimmed. A full memory refuses new entries rather than dropping the oldest,
# because a memory that tidies itself is a cache. max: 0 switches it off.
#
# memory:
#   max: 50
#   chars: 200

# Overrides every agent's delivery for one kind of message: the fleet can defer
# while "somebody is stuck" still gets through. Kinds are set with
# "swarm send -kind": question, answer, fyi, request, decision, blocked, done.
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
