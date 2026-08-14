# Configuration reference

Every key swarm understands, with its default and what it does. `swarm init`
writes a short starter file; this page is the exhaustive version.

The file is looked up as described in [the README](../README.md#where-the-config-is-looked-up).
Unknown keys are an error, not a warning — a typo in a key name would otherwise
be silently ignored, and you would spend an evening wondering why a setting has
no effect.

Relative paths resolve against **the directory holding the config file**, never
against your working directory. `~/` and `$VAR` are expanded.

- [Top level](#top-level)
- [`defaults`](#defaults)
- [`agents`](#agents)
- [`patterns`](#patterns)
- [`groups`](#groups)
- [`web`](#web)
- [`bus`](#bus)
- [`outgoing`](#outgoing)
- [`hooks`](#hooks)
- [Durations](#durations)

## Top level

| key | default | |
|---|---|---|
| `session` | `default` | Names this swarm. It picks the control socket, so two swarms with different session names run side by side. No slashes or spaces. |
| `workdir` | the config's directory | Working directory for every agent that does not override it. |
| `state_dir` | `.swarm` | Everything swarm writes: the control socket, the logs, the CLI shim, the staged files, the TUI's command history, and `owed.json` — what each agent has been asked and has not settled, kept across restarts so `swarm why` can still answer afterwards, and `ephemeral.json` — the instance name counters, and what became of the ephemeral agents that are gone. `swarm init` offers to add it to `.gitignore`. |
| `ephemeral` | — | Limits that apply to every agent made for one task; see [`ephemeral`](#ephemeral). |
| `shared` | `<state_dir>/shared` | Where injected files are staged so every agent can reach them by path. Agents get it as `$SWARM_SHARED`. |
| `env` | `{}` | Added to the environment of every agent. Per-agent `env` wins. |
| `detach_key` | `ctrl+\`, `ctrl+g` on Windows | Leaves an attached agent, in the TUI and in `swarm attach`. Any name `swarm keys -list` marks as bindable: `ctrl+g`, `ctrl+]`, `f12`, `esc esc`, `ctrl+left`. Configurable because the default is what tmux, screen and asciinema like to grab. |
| `log_input` | `false` | Record everything swarm *sends* to an agent in `.swarm/logs/<agent>.input.log`. Off by default and written 0600: it holds what you typed. |
| `mouse` | `false` | Mouse reporting in the TUI. Off by default because a terminal that reports mouse events stops selecting text itself — reading an agent's output matters more than the wheel. `M` toggles it at runtime. |
| `defaults` | — | Inherited by every agent; see below. |
| `agents` | — | The fleet. At least one is required. |
| `groups` | `{}` | Named sets of agents. |
| `delivery_by_kind` | `{}` | Overrides every agent's `delivery` for one kind of message, e.g. `{blocked: push}`. |
| `agents_template` | — | Your own template for `AGENTS.md`, rendered with the same data as the built-in one. |
| `web` | — | The remote control. |
| `bus` | — | Inter-agent messaging. |
| `hooks` | — | Incoming webhooks. |
| `outgoing` | — | What the fleet tells the world about itself. |

`swarm run` writes `<state_dir>/AGENTS.md` — the file your agents read to learn
how to talk to each other. It is generated from this configuration, so it
describes the mechanisms that are actually switched on and no others: an agent
told about a turn budget in a fleet that has none learns a rule that does not
exist. `agents_template` replaces it with a [Go
template](https://pkg.go.dev/text/template) getting the same data, which is what
makes an override as informed as what it replaces (`.Agents`, `.Groups`,
`.Kinds`, `.MaxTurns`, `.EscalateTo`, `.Deferred`, `.Restricted`, `.Workspaces`,
`.Hooks`, `.From`, plus `join` and `dash`).

## `defaults`

Every key here is also an agent key. An agent that leaves one empty inherits it.

| key | default | |
|---|---|---|
| `cols` | `200` | Terminal width given to the agent. Keep it wide: agent CLIs lay out to it. With `follow_window: true` this is only the size the agent has before anyone looks at it. |
| `rows` | `50` | Terminal height, same remark. |
| `scrollback` | `5000` | Lines kept above the screen, which is what `pgup` walks back through. |
| `idle_after` | `3s` | Quiet for this long → the agent is reported `idle`, which usually means it is waiting for you. |
| `autostart` | `true` | Launch the agent with the swarm. |
| `restart_on_exit` | `false` | Relaunch the agent when its process exits. |
| `restart_backoff` | `2s` | Delay before an automatic restart, and the base of the backoff: it doubles with each death that follows a short run. |
| `restart_max_wait` | `1m` | Cap on that doubling, and what counts as having run — a longer life starts the streak over. |
| `restart_max` | `5` | Deaths in a row before swarm stops answering. `0` never stops. |
| `key_delay` | `40ms` | Pause between one key press and the next when several are sent at once. `0` sends them as one burst. |
| `submit_delay` | `120ms` | Pause between pasting text and sending the newline that submits it. Agent UIs that re-render on paste need this; too short and the newline lands before the paste has been absorbed. |
| `bracketed_paste` | `true` | Allow injected text to be wrapped in `ESC[200~`/`ESC[201~`, so a multi-line prompt arrives as one message. It is only actually used when the agent's own UI turned the mode on, the way a real terminal behaves. |
| `follow_window` | `true` | Resize the displayed agent to the pane showing it, so its layout adapts instead of being cropped. Turn it off to pin `cols`/`rows` — which is then what the web UI and `swarm screen` show. |
| `delivery` | `push` | What happens to a bus message addressed to this agent — `push`, `pull` or `defer`, see below. |
| `can_send` | — | The only agents this one may write to. Unset means everyone. |
| `message` | — | Sent to the agent once it is up. Multi-line, as a block scalar. |
| `message_file` | — | Same, read from a file. One or the other, not both. |
| `message_template` | `[swarm] message from {from}: {body}` | How a pushed bus message is rendered before injection. Placeholders: `{id}` `{thread}` `{from}` `{to}` `{body}` `{files}` `{time}`. |
| `workspace` | `shared` | What swarm does about an agent's working copy — `shared`, `clone`, `worktree` or `none`, described under [`agents`](#agents). Inheritable, which is how a whole fleet is given its own clones in one line. |
| `on_start` | — | Command run before the agent starts, as an argv. See [`agents`](#agents). |
| `on_exit` | — | Command run after it exits, as an argv. See [`agents`](#agents). |

A command that is simply broken used to relaunch every two seconds forever. The
wait now doubles — 2s, 4s, 8s… up to `restart_max_wait` — and after
`restart_max` deaths in a row swarm gives up and says so, naming the command to
run once it is fixed. A death that follows a run longer than `restart_max_wait`
is not part of a streak: an agent that dies once a day is restarted promptly
every time.

`key_delay` is `submit_delay`'s counterpart for key presses. `swarm keys dev-1
enter shift+tab shift+tab shift+tab` sent as one block arrives in a single read,
and an agent whose UI changes state on a key — a prompt submitted, a mode
cycled — acts on the first and drops the rest along with the buffer it was
holding. Each key is its own write now, spaced by this.

### `delivery`

| mode | |
|---|---|
| `push` | Typed into the terminal as it arrives. Suits an agent waiting for work. |
| `pull` | Queued until the agent runs `swarm inbox`. Suits an agent that must not be interrupted at all. |
| `defer` | Held until the agent falls quiet, then handed over. |

`defer` is push without the interruption: the work in progress is not cut into,
and nothing has to be asked for. Several messages that arrived during the same
stretch of work are delivered as one — three interruptions collapsed into the
one the agent was going to get anyway.

The top-level `delivery_by_kind` is what makes that usable in practice: the
fleet defers, and `blocked` still gets through. It is a fleet-wide rule rather
than a per-agent one on purpose — "somebody is stuck" means the same thing
whoever is being told.

```yaml
delivery_by_kind:
  blocked: push
  fyi: pull

defaults:
  delivery: defer
```

### `can_send`

```yaml
agents:
  - name: dev-1
    can_send: [triage, "@review"]
```

Groups and roles are allowed as targets. A refused send names what the sender
*may* reach, because that error text is read by an agent: telling it "no" and
leaving it to guess produces a retry, telling it where to go produces a route.

The whole send is refused before anything is delivered — half a broadcast is
worse than none.

### `message` and `message_file`

The prompt an agent gets on launch, once per run:

```yaml
agents:
  - name: reviewer-1
    message: |
      You review pull requests. Start with `swarm inbox`.
      Do not push to main.
```

It is sent when the agent first falls quiet, not the instant the process
starts — a CLI still drawing its banner would swallow it. A restart sends it
again; a reconnection does not.

## `agents`

```yaml
agents:
  - name: dev-1
    role: dev
    command: [claude]
```

| key | default | |
|---|---|---|
| `name` | — | **Required.** The handle used everywhere: CLI, TUI, web, bus. No spaces, `@` or commas. Must be unique. |
| `role` | `""` | Free-form (`dev`, `review`, `triage`). It drives display and gives you `@role` as a target. |
| `command` | — | **Required.** The argv of the agent CLI, as a list. swarm is agnostic: anything that runs in a terminal works. |
| `workdir` | the top-level `workdir` | Where this agent's process runs. |
| `env` | — | Added to (and overriding) the top-level `env`. |
| `patterns` | `[]` | Regexps classifying what the agent shows; see below. |
| `workspace` | `shared` | What swarm does about this agent's working copy: `shared`, `clone`, `worktree` or `none` — see below. |
| `on_start` | — | An argv run before the process is launched. A failure stops the agent rather than launching it into a half-prepared directory. |
| `ephemeral` | `false` | Makes this entry a **template** rather than an agent — see [ephemeral agents](#ephemeral-agents). |
| `max_alive` | `3` | Instances of this template that may run at once. Templates only. |
| `max_lifetime` | `0` | Kills an instance that has not finished by then. `0` means no limit. Templates only. |
| `worktree_base` | *(the remote's default branch)* | What a `worktree` workspace branches from. `head` uses the repository's current commit instead. |
| `can_spawn` | `[]` | Templates this agent may launch with `swarm spawn`. |
| `on_exit` | — | An argv run after the process has gone. A stop waits for it, bounded by the grace period. |

Plus every key from [`defaults`](#defaults), which overrides the inherited value
for this agent alone.

Agents also receive, whatever their `workdir`:

| variable | |
|---|---|
| `$SWARM_AGENT` | its own name |
| `$SWARM_ROLE` | its role, empty when it has none |
| `$SWARM_PEERS` | the other agents, comma separated |
| `$SWARM_ROOT` | the directory holding the config file — the project the fleet was started for |
| `$SWARM_SHARED` | the shared directory, absolute |
| `$SWARM_SESSION` | the session name |
| `$SWARM_SOCKET` | the control socket, used automatically by the `swarm` command |
| `$SWARM_STATE_DIR` | the state directory, absolute |
| `PATH` | prefixed with `<state_dir>/bin`, which holds a `swarm` pointing at this session |

`$SWARM_ROOT` is the one an agent cannot work out for itself: relative paths in
the config resolve against it, and an agent with `workspace: clone` runs in its
own checkout somewhere under `<state_dir>/workspaces`, with no way back to the
project otherwise. Every other path swarm hands out is absolute, so none of them
depends on where the agent happens to be.

swarm also sets `TERM=xterm-256color` and `COLORTERM=truecolor`, because the
emulator does support both and agent CLIs lay out far better when they can
assume it.

It deliberately does **not** set `LINES` and `COLUMNS`, and removes any it
inherited. An agent is resized to the pane showing it, and a running process's
environment cannot be changed, so those numbers could only ever be the geometry
at launch — wrong from the first relayout onwards. They are not harmless:
Python's `shutil.get_terminal_size`, which Textual and Rich are built on, prefers
them to asking the terminal. A real terminal does not export them either.

There is one state directory per config file, shared by every agent — a
per-agent `workdir` changes where the process runs, not which fleet it belongs
to.

### `on_start` and `on_exit`

The part of isolation no agent can arrange for itself: installing dependencies,
copying a `.env`, pointing at a dedicated test database, taking down whatever
was started. Each is an argv, run in the agent's working directory with the
agent's environment, so a script needs to know nothing about swarm.

```yaml
defaults:
  on_start: ["./scripts/prepare-agent.sh"]
  on_exit:  ["./scripts/cleanup-agent.sh"]

agents:
  - name: dev-1
    workspace: clone
    env:
      PORT: "{alloc_port}"
```

`{alloc_port}` in any environment value becomes a port nobody is listening on,
picked once per agent so a restart does not move the server from under whatever
was pointing at it. Two agents both running a dev server both want 3000, and no
amount of talking to each other settles that.

They run per **agent**, not per swarm: at autostart, at `swarm start`, at
`swarm restart`, and on every automatic restart. `on_exit` runs whenever the
process dies, whatever killed it.

A stop waits for `on_exit` before returning — a hook that frees a port is
worthless if swarm exits from under it — bounded by the grace period, so a hung
script delays a shutdown rather than preventing one.

### `workspace`

`workdir` says *where* an agent runs; `workspace` says what swarm does there.

| mode | swarm does | `workdir` |
|---|---|---|
| `shared` | nothing | the common one |
| `clone` | provisions a durable clone, once | `<state_dir>/workspaces/<name>` unless you name one |
| `worktree` | provisions a git worktree, and collects it when an ephemeral instance ends | `<state_dir>/worktrees/<name>` |
| `none` | nothing, and presumes nothing | yours; the agent is free to move |

Following an agent that moves needs asking the operating system where a process
currently is, which only Linux answers (through `/proc`). On macOS and Windows,
`workspace: none` reports the branch of the directory the agent was started in,
even after it has wandered elsewhere.

A clone rather than a worktree, because two worktrees cannot have the same
branch checked out — which rules them out as soon as two agents sit on the main
branch between tasks. It is taken from the local repository, so git hardlinks
the object store: the cost is the working tree, not the history.

Two things are fixed up afterwards, since a fresh clone does not inherit them
and cannot work without them: `origin` is pointed at what the source calls
`origin`, and `user.*`, `gpg.*`, `commit.gpgsign`, `tag.gpgsign`,
`credential.helper` and `init.defaultbranch` are carried over. Without those an
agent commits under the wrong name, unsigned, or not at all.

A directory that is already a checkout is left exactly as it is, so restarting
an agent never touches its work — and a fleet of hand-made clones can adopt
`workspace: clone` by adding one word per agent.

swarm never fetches, rebases or merges. A durable clone drifts; telling agents
about it is a job for a webhook rule on a push to the main branch, or for the
standing instruction in `AGENTS.md`. `swarm ls` shows the drift (`main* 3↑ 12↓`,
as of the last fetch) so you can see it without swarm acting on it.

`none` differs from `shared` only in what swarm presumes: it reports the git
state of the process's actual directory rather than the configured one, since an
agent that manages its own isolation is expected to have moved. Reading a
process's directory needs `/proc`, so on macOS the configured one is used.

## Ephemeral agents

An entry with `ephemeral: true` is not an agent: it is the shape of one. Nothing
is started for it, and it never appears in `swarm ls`, the TUI or the grid.
`swarm spawn` makes instances from it, named after it — `worker-1`, `worker-2` —
which run one task and are collected when they say they have finished.

```yaml
agents:
  - name: triage
    command: [claude]
    can_spawn: [worker]        # who may launch them

  - name: worker
    ephemeral: true            # a template, not an agent
    command: [claude]
    role: dev
    max_alive: 3
    max_lifetime: 2h
```

```sh
swarm spawn worker "take ticket 219"   # prints: worker-1
swarm spawn worker -f brief.md         # or - for standard input
```

**Spawning opens a debt.** Launching an instance sends it a `request` carrying
its task, so everything that already exists applies to it: `swarm why worker-1`
says what it is on and since when, `on_stalled` asks it where it is if it goes
quiet, and the debt survives a restart of the hub. `swarm done` is how it says
it has finished — and its task being its life, that is what collects it.

A template gives its instances a group: `@worker` is whichever of them are
alive, which is what you write in another agent's `can_send`. Sending to it when
none exist is an error rather than a silent success.

Launched by an agent, an instance gets `SWARM_PARENT`, and the two are added to
each other's `can_send`. Launched by you, it has no parent.

**What is refused, and why.** `restart` on an instance: it would come back with
no memory of the work and the same debt still open. `can_spawn` together with
`restart_on_exit: false`: an agent that launches ephemerals has to be there when
they finish, or their debts have nobody to go back to. `max_alive` or
`max_lifetime` on something that is not a template: they would mean nothing.

## `ephemeral`

What applies to every agent made for one task, rather than to one template.

```yaml
ephemeral:
  max_alive: 12     # across the whole fleet, all templates together
  remember: 100     # instances kept on record after they are gone
```

| key | default | |
|---|---|---|
| `max_alive` | `12` | Instances that may run at once across the fleet. A template's own `max_alive` bounds one kind of work; this one bounds the machine. |
| `remember` | `100` | How many finished instances are kept on record. |

**Why a fleet-wide ceiling as well.** Three templates of three is nine agents,
which is a number nobody chose by writing three — and nine agent processes is
real memory and a real API bill. The per-template limit says how much of one
kind of work may happen at once; this one says how much may happen at all.

**What `remember` is for.** An instance that is gone can still be owed
something: a debt outlives an instance spawned by a person, deliberately, since
there is no parent to tell. `swarm why worker-3` then has to answer that the
agent is dead — with what it was, what it was asked and when it went — rather
than that no such agent exists, which reads as a typo and sends nobody looking
for the work. Keeping that record is what makes the answer possible. Keeping it
for ever is not the point, so it is a count: what matters beyond it is in the
event log and in git.

## `patterns`

A regexp matched against the tail of the agent's rendered screen. While it
matches, the agent carries a state badge.

```yaml
    patterns:
      - match: "Run this command\\? \\(y/n\\)"
        state: approval
        notify: true
        reply: "y"
```

| key | default | |
|---|---|---|
| `match` | — | **Required.** A Go regexp. It must compile, or the config is refused. |
| `state` | `""` | The state to report while it matches: `waiting`, `blocked`, `attention`, `error`, or any label you like. It shows in the TUI and in `swarm ls`. |
| `notify` | `false` | Raise an event in the log when the pattern appears. |
| `reply` | `""` | Injected automatically when the pattern appears. This answers a prompt on your behalf — use it only for prompts you would always answer the same way. |

## `groups`

```yaml
groups:
  backend: [dev-1, dev-2]
```

Usable as `@backend` anywhere a target is expected. Every member must be a
defined agent, checked when the file is read.

A **target** is an agent name, `@group`, `@role`, `all` (or `*`), or a
comma-separated list of those.

## `web`

The remote control: one page showing every terminal, live, typeable.

| key | default | |
|---|---|---|
| `enabled` | `false` | Serve the page at all. |
| `addr` | `127.0.0.1:7777` | Listen address. `0.0.0.0` reaches it from your phone — read the warning below first. |
| `token` | `""` | Required on every request. Empty means a fresh one is generated at each start and printed in the TUI. |
| `read_only` | `false` | Serve the terminals but refuse keystrokes, injections and uploads. |
| `tls_cert` / `tls_key` | `""` | Enable HTTPS. Both or neither. |

Past localhost, treat the URL as a shell on your machine: it can type into every
agent. Set a token you chose, or `read_only: true`, and put it behind TLS or a
tunnel rather than binding `0.0.0.0` in the open.

## `bus`

| key | default | |
|---|---|---|
| `enabled` | `true` | Inter-agent messaging. On by default: agents reaching each other is a core feature, not an opt-in. `false` forbids `swarm send` entirely. |
| `history` | `200` | Messages kept per mailbox, for replay and for `swarm inbox -peek`. |
| `allow_self_inject` | `false` | Let an agent send a message to itself. |
| `max_turns` | `0` | Messages allowed on one conversation. `0` means no bound. |
| `escalate_to` | `""` | Who arbitrates when a conversation runs out of turns. |
| `on_stalled` | *(none)* | What to do about an agent that is stalled: ask it, tell somebody else, or nothing. See [`on_stalled`](#on_stalled). |

A *thread* is one conversation. An agent answering someone stays on the thread
it was written to on, so nothing has to carry an identifier around; a person or
a webhook always starts a new one. `swarm bus threads` lists them.

With `max_turns` set, the message that would exceed the budget is refused, and
the refusal is the instruction — the agent reads *this thread has used its 6
turns; decide alone or escalate to triage*. One turn earlier, the delivery
carries a warning, so the last turn can be spent on an answer rather than on
discovering the limit. Something genuinely else to say is always allowed:
`swarm send --new-thread` starts a fresh conversation with its own budget.

`swarm send --final` closes a matter: the bus refuses its recipient the right to
answer. Use it for decisions, which is what `escalate_to` produces — a saturated
thread is handed to that agent with everything that was said, and its answer is
expected to come back final.

### Kinds, and what they commit you to

Every message has a kind, `note` unless `swarm send -kind` says otherwise. It is
what `delivery_by_kind` dispatches on, what `swarm bus stats` counts — and what
decides whether an agent now owes something.

| kind | what it says | opens a debt | settles one |
|---|---|---|---|
| *(none)* | something said | | |
| `fyi` | information, and expecting nothing back **is** the message | | |
| `question` | I expect an answer | ✔ | |
| `answer` | here it is | | ✔ |
| `request` | I expect work | ✔ | |
| `done` | it is finished, or there was nothing to do | | ✔ |
| `decision` | it is settled | | ✔ |
| `blocked` | I cannot go on | ✔ | |

A message that opens a debt carries the way to close it, appended to the body:

```
[swarm] when this is settled: swarm done -thread 7
```

Left to guess, an agent guesses, and a wrong guess costs a turn to discover
while the work stays open. This is the same idea as a refusal carrying its
instruction.

`swarm done [note]` settles everything outstanding, or one thread with
`-thread`. It exists because a request had no way of ending: an answer closes a
question, a decision closes a debate, and a demand for work closed nothing at
all — so an agent that had finished looked exactly like one that never started.
It also covers the case no artefact can: *I looked, there was nothing to do.*

Whoever asked is told, on the thread they asked on — unless they have no mailbox
(you, or a webhook), in which case the debt is settled and nobody is written to.

### Stalled

| key | default | |
|---|---|---|
| `stalled_after` | `10m` | How long an agent may be **idle** while owing something before swarm says so. Counted from the moment it goes idle, so it adds to that agent's `idle_after`. `0` switches it off. |

An agent that has owed something for `stalled_after` and is idle right now is
reported — in the event log, and as `agent.stalled` to an outgoing webhook.
Both halves are needed: an agent with nothing to do is quiet and that is
normal, and an agent that is writing is not stalled whatever it owes.

It shows as `stalled` where the state is shown — the agent list, the pane
header, `swarm ls` — in orange, and the TUI header counts them beside the
working and idle ones, and the pane header says how
many things are outstanding. `Info.State` itself stays `idle`: the bus decides
this, not the agent, and changing what `idle` means would change the delivery
paths that key on it.

**Nothing is restarted, injected or killed on the strength of it**, because the
state is a guess: an agent waiting on a long build is silent and does owe work.
Ask it and it will say so — which is why the false positive costs a question
rather than an interruption.

Asking is the one thing swarm will do about it, and only if you configure
[`on_stalled`](#on_stalled). With no rules the state is a signal and nothing
else, which is what it was for its first year.

What is timed is the age of the debt, not the length of the silence. An agent
parked on a configuration screen redraws every few minutes, and timing the
silence let every redraw push the state back to zero — it blinked out and took a
full cycle to return, while nothing had been settled. A redraw settles nothing.

Being idle stays a condition, since an agent that is writing may be working on
exactly what is owed. The threshold adds to that agent's `idle_after`, so the
two settings add up rather than compete: with `idle_after: 3s` and
`stalled_after: 10m`, work owed for ten minutes and three seconds is reported as
soon as the agent is quiet.

### When it gets away from you

```sh
swarm bus pause "shipping, stop talking"   # hold every delivery
swarm bus status                           # whether anything is held back
swarm bus resume -flush                    # let them through, hand over the pile
swarm bus threads                          # the open conversations
```

A paused bus still records everything; it just stops interrupting anybody with
it. The agents keep working — this stops the talking, not the fleet. Without
`-flush`, resuming leaves what piled up in the mailboxes for `swarm inbox`.

## `on_stalled`

A list of things to do when an agent has been stalled. Each entry is a message
swarm sends; the list is walked in order and every entry that applies fires.

```yaml
bus:
  stalled_after: 15m
  on_stalled:
    # Ask the agent itself, three times, quarter of an hour apart.
    - to: self
      every: 15m
      max: 3

    # If it is still stuck two hours later, the triage agent should know.
    - to: myself
      after: 2h
      kind: question
      max: 1
```

| key | default | |
|---|---|---|
| `to` | `self` | Who is told. `self` is the stalled agent; anything else is an agent name, an `@role` or a `@group`. |
| `kind` | `fyi` | The message kind. `fyi` tells without asking, so it opens no debt. |
| `after` | `0` | Extra delay past `stalled_after` before this entry applies, so one list can ask now and escalate later. |
| `every` | `0` | Repeat interval. `0` sends once for a given debt. Under a minute is refused. |
| `max` | `1`, or `3` when `every` is set | How many times this entry may fire for one debt. |
| `text` | *(composed)* | The message. Left empty, swarm writes what `swarm why` would say. |
| `push` | `true` | Type it into the recipient's terminal whatever its `delivery` is. |

**Why the default text is usually the right one.** An agent stalled for a day
has been compacted several times, so the message that put it there is gone from
its own memory — asking *what are you doing?* gets an honest shrug. What swarm
sends instead is what it still knows: who is waiting, since when, the question
itself, and the command that settles it.

**Sending `question` or `request` to `self` is refused.** It would open a second
debt on top of the one the agent is stuck on, and answering it would settle
neither — so the same rule fires again, for ever. When a debt is what you want
opened, open it from an agent that knows the work: `to: <your triage agent>`
with `kind: question`, and let it ask properly.

**`push` defaults to true** because an agent that is not reading its mailbox is
exactly the one this is for, and a message that waits politely in a queue is a
message that never arrives. It is an interruption, deliberately.

**Repeats are counted per debt.** Settle it and the counter goes with it; a new
question later starts again. Once an entry has used its `max`, it stops and says
so in the event log rather than going quiet as if it had worked.

## `outgoing`

The other direction: the incoming rules read backwards. Conditions on paths into
a fleet event, a body rendered from the same paths, a signed POST. swarm does not
know what is at the far end and does not want to — Telegram, a CI job or a shell
script behind a reverse proxy are the same thing to it.

```yaml
outgoing:
  enabled: true
  url: https://ci.example/swarm
  secret_path: .swarm/out-secret     # or secret_env
  signature_header: X-Swarm-Signature
  rules:
    - name: finished
      when: {event: agent.done}
      body: "{agent} finished on {data.branch}"
    - name: gave-up
      when: {event: agent.error, text: "~not restarting"}
      body: "{agent}: {text}"
```

| key | default | |
|---|---|---|
| `enabled` | `false` | Off by default: it talks to the network. |
| `url` | — | **Required when enabled.** |
| `secret` / `secret_env` / `secret_path` | — | Signs the body, exactly as the listener verifies one. Exactly one of the three. |
| `signature_header` | — | Required with a secret. |
| `token` | `""` | Sent as `X-Swarm-Token`. |
| `timeout` | `10s` | Bounds one attempt. |
| `retries` | `0` | Further attempts a failure earns; the wait doubles from `retry_backoff`. |
| `retry_backoff` | `2s` | First wait between attempts. |
| `queue` | `256` | Notices held while the far end is slow. Past it they are dropped, and said to be. |
| `log` | `true` | Record every attempt in `webhooks.log`, beside the incoming ones. |
| `rules` | `[]` | Tried against every event; each match sends. |

### The events

| `event` | when |
|---|---|
| `agent.started` | the process was launched |
| `agent.exited` | it died — `text` is the status |
| `agent.idle` | it fell quiet with nothing to show |
| `agent.done` | it settled what it was asked, with `swarm done` |
| `agent.stalled` | it owes something and has been silent for `bus.stalled_after` |
| `agent.attention` | a `pattern` with `notify: true` matched |
| `agent.error` | anything swarm logged as an error, the restart streak included |

`agent.done` is declared, never deduced. An earlier version of this raised it
from the git tree — quiet plus a dirty checkout — which was wrong in both
directions: an agent commenting on a pull request through an MCP tool touches no
file and would never have finished, while one that changed three files and
stopped to ask a question always would have. Whether work is done is not visible
from outside; it is stated by whoever did it.

A rule addresses `event`, `agent`, `text`, `at`, and everything under `data.`:
`data.branch`, `data.dirty`, `data.ahead`, `data.behind`, `data.session`. The
body uses the same `{placeholder}` syntax as an incoming message.

The POST carries the whole notice as JSON — event, agent, text, data, and the
rendered `body` — so the far end can use either. Retrying is deliberately
modest and the queue lives in memory only: swarm tells the world what happened,
and making sure the world listened is the world's job. A queue that survived
restarts would be a message broker, which this is not.

## `hooks`

Incoming webhooks, turned into bus messages by declarative rules. See the
[README section](../README.md#webhooks) for the reasoning; this is the key list.

| key | default | |
|---|---|---|
| `enabled` | `false` | Listen at all. |
| `addr` | `127.0.0.1:7778` | Listen address. Its own, not a route on the web remote control. |
| `token` | `""` | If set, required as `X-Swarm-Token`, as a bearer token, or as `?t=`. Independent of the signature; both apply when both are set. |
| `secret` | `""` | HMAC-SHA256 secret, in the file. Prefer one of the two below. |
| `secret_env` | `""` | Name of an environment variable holding the secret. |
| `secret_path` | `""` | File holding the secret. It must not be readable by group or others, and trailing newlines are stripped. |
| `signature_header` | `""` | The header carrying the digest, e.g. `X-Hub-Signature-256`. Required as soon as a secret is set — there is no universal name for it. |
| `from` | `webhook` | The sender name agents see, and what `{from}` renders as. |
| `max_body` | `1048576` | Request body limit, in bytes. |
| `log` | `true` | Record every delivery in full in `.swarm/logs/webhooks.log`. Written 0600. |
| `rules` | `[]` | Tried against every delivery; every match fires. |
| `unmatched` | — | One rule, used only when none of `rules` matched. |

`swarm attach` follows the window on Windows too, by asking the console its
size four times a second: there is no SIGWINCH, and the resize is reported as a
console input record an attach cannot read without eating the keystrokes it is
there to forward.

`swarm attach` has no status bar on Windows: holding a line at the bottom of
the screen needs a scrolling region the console does not honour, and the bar
ends up stacked across the display rather than sitting on one row. The reminder
goes in the window title instead — an agent that sets its own title takes it
back, so keep the detach key in mind there.

Two things a Windows console never passes on, whatever you configure. It
translates keys to escape sequences itself, and its support for ctrl with
punctuation is incomplete — `ctrl+\` arrives as a plain backslash, which is why
the default is `ctrl+g` there: a letter becomes `0x07` the way `ctrl+a`..`ctrl+z`
all do. And `alt+enter` is the console's own full-screen toggle, so it never
reaches swarm at all.

Exactly one of `secret`, `secret_env` and `secret_path` may be named. Two is an
error rather than a precedence rule: silently preferring one is how a swarm ends
up verifying against a secret its owner thought they had replaced.

`secret_path` is checked on Unix only. Windows has no POSIX modes — every
readable file reports `0666` — and who may open a file is decided by its ACL,
which mode bits cannot express. Rather than apply a rule that would reject
every secret ever written, or keep a promise nothing verifies, swarm does not
check the file's permissions there. On a shared Windows machine, put the secret
somewhere your account alone can read.

A block with `enabled: false` is still checked for shape — unknown targets,
broken regexps, two secret sources — but its secret is not read, so a switched-off
listener never makes the rest of the config unloadable.

### A rule

| key | default | |
|---|---|---|
| `name` | `""` | Labels the rule in the log and in `swarm hook test`. An unnamed rule is called `rule #<n>` after its position. |
| `when` | `{}` | Conditions, all of which must hold. No conditions matches anything. |
| `to` | — | **Required.** A target. Deliberately not templated: a payload must never choose which agent it wakes up. |
| `message` | — | **Required.** The body, with `{path}` placeholders filled from the delivery. |

A **path** addresses the decoded JSON body, or a header when it starts with
`header.`. It walks objects and arrays: `data.commits.0.message`,
`header.X-Hub-Event`. Header names are case-insensitive.

A **condition value** is matched three ways:

| written | meaning |
|---|---|
| `action: opened` | exactly that |
| `number: "*"` | the path merely has to exist |
| `ref: '~^refs/heads/main$'` | a regexp, after the tilde |

Note that `"*"` matches a JSON `null` too: the key is present. Use `'~.'` — at
least one character — for "present and not empty".

Values interpolated into a message are truncated at 400 characters.

## Keeping a config current

Defaults change as keys are added, and a value written when an older default was
in force can quietly stop meaning what its author intended — without ever
becoming an error. `swarm config check` names those, and `-fix` applies them.

`swarm run` checks too. With a terminal it offers to fix; without one — under
systemd, in a script — it warns and starts anyway, since a warning never broke
anything. Fixes are surgical on the text, so comments survive.

## Durations

`idle_after`, `restart_backoff`, `key_delay` and `submit_delay` take Go
duration strings:
`150ms`, `3s`, `2m`, `1h30m`. A bare number is **not** valid.

## Booleans that are three-valued

`autostart`, `restart_on_exit`, `bracketed_paste`, `follow_window` and
`bus.enabled` distinguish "unset" from `false`, so that an agent leaving one out
inherits the default rather than silently getting `false`. In practice this only
matters when you want an agent to override a `defaults` entry back to `true`:
write it explicitly.
