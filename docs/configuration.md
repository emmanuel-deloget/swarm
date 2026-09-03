# Configuration reference

Every key swarm understands, with its default and what it does. `swarm init`
writes a short starter file; this page is the exhaustive version.

Unknown keys are an error, not a warning — a typo in a key name would otherwise
be silently ignored, and you would spend an evening wondering why a setting has
no effect.

Relative paths resolve against **the directory holding the config file**, never
against your working directory. `~/` and `$VAR` are expanded.

- [Where the file is looked up](#where-the-file-is-looked-up)
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

## Where the file is looked up

`swarm run` uses `-c <path>` if you pass one. Otherwise it walks up from the
working directory, and in each directory tries `swarm.yaml`, `swarm.yml`,
`.swarm.yaml`, `.swarm.yml`, in that order — so any subdirectory of your project
works. There is no global config; a swarm belongs to a project.

Relative paths inside the file (`workdir`, `shared`, `tls_cert`) resolve against
the directory holding the file, never against your working directory.

The other commands do not really need the config — they only use it to find the
control socket, in this order:

1. `-socket <path>`
2. `$SWARM_SOCKET` — already set inside every agent, which is why an agent can
   just run `swarm send` with no arguments of its own
3. the config found as above → `<config dir>/.swarm/<session>.sock`, or wherever
   `<session>.socketpath` points if the socket had to be relocated
4. `./.swarm/default.sock`, if it exists

A **target** is an agent name, `@group`, `@role`, `all`, or a comma-separated
list of those. Every command that acts on agents accepts one.


## Top level

| key | default | |
|---|---|---|
| `session` | `default` | Names this swarm. It picks the control socket, so two swarms with different session names run side by side. No slashes or spaces. |
| `workdir` | the config's directory | Working directory for every agent that does not override it. |
| `state_dir` | `.swarm` | Everything swarm writes: the control socket, the logs, the CLI shim, the staged files, the TUI's command history, and `owed.json` — what each agent has been asked and has not settled, kept across restarts so `swarm why` can still answer afterwards, and `ephemeral.json` — the instance name counters, and what became of the ephemeral agents that are gone, and `memory.json` with `memory.log` beside it — what the fleet knows and every change that ever made it. `swarm init` offers to add it to `.gitignore`. |
| `ephemeral` | — | Limits that apply to every agent made for one task; see [`ephemeral`](#ephemeral). |
| `memory` | — | What the fleet knows and its agents keep forgetting; see [`memory`](#memory). |
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
| `budget` | *(none)* | What this agent may say, over time. See [`budget`](#budget). |
| `message` | — | Sent to the agent once it is up. Multi-line, as a block scalar. |
| `message_file` | — | Same, read from a file. One or the other, not both. |
| `message_template` | `[swarm] message from {from} at {at}{held}: {body}` | How a pushed bus message is rendered before injection. See [`message_template`](#message_template). |
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

Which of them an agent is on decides whether `swarm inbox -wait` means
anything. A pushed message is typed into the terminal and taken out of the
mailbox again, so waiting on that mailbox blocks for the whole timeout and
reports nothing — every time. swarm answers such a wait at once instead, and
says why:

```
swarm: not waiting: messages to dev-1 are typed straight into your terminal as
they arrive, so nothing is left here to wait for. Read your screen, not your
mailbox.
```

`pull` and `defer` both leave the message in the mailbox — `defer` until the
agent falls quiet — so both are worth waiting on. One `delivery_by_kind` entry
that is not `push` is enough to make even a pushed agent worth waiting on, and
a paused bus queues everything whatever anyone asked for. The generated
`AGENTS.md` states the answer for each agent by name, because agents ask for the
wait liberally and would otherwise find out by blocking.

### `can_send`

```yaml
agents:
  - name: dev-1
    can_send: [triage, "@review"]
```

Groups and roles are allowed as targets. A refused send names what the sender
*may* reach, because that error text is read by an agent: telling it "no" and
leaving it to guess produces a retry, telling it where to go produces a route.

It covers every way an agent reaches another agent, not only `swarm send`.
`swarm inject` and `swarm keys` are checked against it too. Neither becomes a
bus message: an injection is bytes in a terminal, and `-raw`, `-submit=false`
and `-no-paste` have no equivalent on the bus — which is the reason an agent
driving a shell uses inject at all. So the rule is applied and nothing else
changes; `swarm events` records the injection with the agent that asked.

Who is asking comes from `$SWARM_AGENT`, which the shim sets. A `-from` that
disagrees with it is refused rather than believed. A person's shell has no
`$SWARM_AGENT` and is not restricted: that is the operator's path, and it is
how `swarm inject` keeps typing raw text into a terminal.

This bounds what a fleet is meant to do, and it is not a security boundary. A
client sets its own environment, so a program determined to claim another name
can. The check stops a prompt that has drifted and a command written by mistake,
which is what actually happens.

The whole send is refused before anything is delivered — half a broadcast is
worse than none.

### `message_template`

How a bus message reads when it is typed into a terminal.

| placeholder | |
|---|---|
| `{body}` | the message |
| `{from}`, `{to}` | who wrote it, who is reading it |
| `{kind}` | `question`, `answer`, `fyi`, `request`, `decision`, `blocked`, `done` |
| `{id}`, `{thread}` | the message and the conversation it belongs to |
| `{files}` | attachments, as paths; appended anyway when the template omits it |
| `{time}` | when it was sent, as RFC 3339 |
| `{at}` | when it was sent, as a clock: `15:04:05` |
| `{held}` | how long it waited before landing — `, held 6s` — and empty when it did not |

`{at}` and `{held}` are what a recipient cannot work out for itself. A message
held while an agent was busy arrives with no sign of how old it is, and *check
the branch before you push* reads the same whether it was said ten seconds or
forty minutes ago.

`{held}` carries its own comma and is empty below a second, so the usual case —
a push, which lands in milliseconds — reads as though the placeholder were not
there:

```
[swarm] message from user at 10:31:47: run the tests
[swarm] message from user at 10:31:48, held 6s: run the tests
```

The second is a `defer` that waited for the agent to fall quiet.

### `message` and `message_file`

The prompt an agent gets on launch, once per run:

```yaml
agents:
  - name: reviewer-1
    message: |
      Read "$SWARM_STATE_DIR/AGENTS.md" first. You review pull requests.
      Do not push to main.
```

It is sent when the agent first falls quiet, not the instant the process
starts — a CLI still drawing its banner would swallow it. A restart sends it
again; a reconnection does not. `on_idle` reads that same quiet, and leaves an
agent alone until it has had this: being told you are idle before being told
what you are for is a nonsense.

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

A clone rather than a worktree **for a durable workspace**, because two
worktrees cannot have the same branch checked out — which rules them out as soon
as two agents sit on the main branch between tasks. An agent made for one task
is never between tasks, which is what `worktree` is for; see [ephemeral
agents](#ephemeral-agents). It is taken from the local repository, so git hardlinks
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

## `memory`

What the fleet knows and its agents keep forgetting.

An agent's context is compacted, and what it was told an hour ago goes with it.
The bus already answers that for one thing — `swarm why` is the part of a
conversation that does not get compacted — and this answers it for the standing
facts, which belong to nobody's thread and survive every restart.

```yaml
memory:
  max: 50       # entries; 0 switches it off
  chars: 200    # the longest an entry may be
  ttl: 0s       # how long an entry survives unused; 0s is forever
```

| key | default | |
|---|---|---|
| `max` | `50` | How many entries the memory holds. `0` switches it off. A full memory evicts the least recently used entry to make room. |
| `chars` | `200` | The longest an entry may be, in characters. Under 40 is refused: that is a label rather than a fact. |
| `ttl` | `0s` | How long an entry survives without being written or asked for by name. `0s` is forever. Under a minute is refused: nothing would survive long enough to be read once. |

An entry is a **key and one line**:

```sh
swarm remember gate-runtime "make integration takes 8-12 min; not a stalled agent"
swarm recall gate
swarm forget gate-runtime
```

Writing a key again replaces what it held, which is why there is no command to
edit one — and a fact that is still true has its date refreshed by being
restated. `swarm recall` lists what is held, newest first, with who wrote each
entry and how long ago.

The limits are the feature rather than a guard on it. An agent asked politely
for something short writes an essay about brevity, so the shape is refused at
the point of writing:

- more than one line, and the refusal counts them
- more than `chars` characters, and the refusal gives both numbers
- a key outside `a-z0-9-`, two to thirty-two characters — no spaces, so a key
  cannot become a chapter heading
- an entry opening with a heading, a bullet or bold, which is formatting rather
  than fact

Naming the thing before describing it does most of the work: nobody titles a
paragraph on the nature of time `gate-runtime`. The key is also what makes an
entry something that can be corrected or dropped later, which a wall of prose
is not.

### What goes, and when

Two things keep a memory from silting up, and both measure **use** rather than
age. An entry is used when it is written and when a `swarm recall <pattern>`
matches it; a bare `swarm recall` is a listing and refreshes nothing.

**A full memory evicts the least recently used entry** rather than refusing the
write. The entry that went is named in the response, with its line, so whoever
caused it can put it back:

```
$ swarm remember spec-284 "v2 approved"
spec-284  v2 approved
          dev-2, just now
swarm: the memory was full, so old-gate-flake went to make room: gate flakes on ARM runners
```

**With `ttl` set, an entry nobody has written or asked for in that long
expires.** Measuring use and not age is the point: a constraint settled on the
first morning and read every day since is the fleet's oldest entry and its most
load-bearing one, and expiring by age would drop exactly that. Expiry is lazy —
it happens the next time anything asks the memory a question, not on a timer.

Earlier versions refused the write on a full memory. That asked the wrong thing
of an agent: pick somebody else's fact to delete, from inside a command that
was about to fail anyway — a judgement it has no standing to make about fifty
lines mostly written by other people. What it did in practice was give up, and
the fact was lost instead of an entry nobody had read in a week.

There is no setting that brings the refusal back. A `max` the fleet never
reaches never evicts, but that is not the same thing: the write succeeds either
way, it simply never costs anything.

### The journal

`memory.json` is what the fleet knows now. `.swarm/memory.log` beside it is
every change that ever made it, one JSON object per line, appended and never
rewritten:

```json
{"at":"2026-09-03T09:14:02Z","act":"revised","key":"spec-281","fact":"v9 approved","by":"myself","prev":"v8 approved","was":"dev-1"}
{"at":"2026-09-03T11:40:55Z","act":"evicted","key":"old-gate-flake","fact":"gate flakes on ARM runners","by":"dev-3","was":"chair"}
```

`act` is one of `remembered`, `revised`, `forgotten`, `expired` or `evicted`.
`fact` is the line the record is about — what the entry now holds, or for a
removal, what went. `by` is whoever did it, and is absent only on an `expired`,
which nobody did. `was` is who wrote the line that is no longer held, and `prev`
is that line when a revision replaced it. **`by` and `was` are different
people**: an entry deleted by somebody other than its author is the case worth
finding, and one field could not answer for both.
Nothing in swarm reads it back. It exists because the state cannot answer the
questions asked afterwards: writing a key again leaves nothing behind, because
that is what writing it again means, so *what did this used to say, who changed
it, and what was dropped to make room* has no other record. Reading is not
journalled — the journal is a history of what the memory said, not of who
looked.

An entry also carries the last of these in itself: `swarm recall` shows who
wrote the line, who they wrote over, and how many times the key has turned
over. A standing fact three agents have taken turns rewriting in an hour is not
a settled fact, and this is what makes that visible without opening the log.

```
spec-281  v9 withdrawn
          chair, revising myself (2 times over), 4m ago, read just now
```

Nothing is injected, and nothing is announced. Writing an entry tells nobody:
a memory that notified the fleet on every write would be a broadcast channel
beside the one [`budget`](#budget) was just built to price, and one message per
agent per write is the shape a fleet runs away in.

Telling them is a separate, deliberate act:

```sh
swarm remember -tell @dev gate-runtime "make integration takes 8-12 min"
```

It goes **through** the bus rather than beside it, so `can_send` bounds it and
the budget charges it once per recipient, like any other send. The message
carries the key and the line, and says where the record is — an entry is short
by construction, so carrying it costs no more than a pointer and saves the
reader a round trip.

A refused telling does not undo the writing. The entry is the valuable half,
and losing it because a budget said no would be the wrong trade; the command
says which of the two happened:

```
gate-runtime  make integration takes 8-12 min
swarm: remembered, but not told: dev-1 cannot reach dev-2; it may write to lead
```

`-tell` goes **before** the key. Everything from the key onwards is taken
literally, which is what stops a fact about `-race` from being read as a flag —
and a `-tell` after the key would otherwise be written into the memory as
though somebody meant it, so that is refused too.

The generated `AGENTS.md` says the memory exists and how to read it; whether an
agent looks is an agent's business.

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

It means the agents that are **running**, which is not the same as the agents in
this file. An ephemeral entry is a template and never runs, so nothing resolves
to it. Its instances do run and are not written down anywhere, so `all` and
`@role` reach them — including from a parent whose own `can_send` says `all`,
which is how `swarm spawn` hands over the task.

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
| `on_idle` | *(none)* | What to do about an agent that has simply gone quiet, owing nothing. See [`on_idle`](#on_idle). |
| `budget` | *(none)* | What an agent may say, over time. See [`budget`](#budget). |

A *thread* is one conversation. An agent answering someone stays on the thread
it was written to on, so nothing has to carry an identifier around; a person or
a webhook always starts a new one. `swarm bus threads` lists them.

With `max_turns` set, the message that would exceed the budget is refused, and
the refusal is the instruction — the agent reads *this thread has used its 6
turns; decide alone or escalate to triage*. One turn earlier, the delivery
carries a warning, so the last turn can be spent on an answer rather than on
discovering the limit. Something genuinely else to say is always allowed:
`swarm send --new-thread` starts a fresh conversation with its own budget.

`swarm send --final` closes a matter: the bus refuses its recipients the right
to answer. All of them — a send to a role or a group carries one message per
member, and the decision binds every one. Use it for decisions, which is what
`escalate_to` produces: a saturated thread is handed to that agent with
everything that was said, and its answer is expected to come back final.

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
| `stalled_after` | `20m` | How long an agent may be **idle** while owing something before swarm says so. Counted from the moment it goes idle, so it adds to that agent's `idle_after`. `0` switches it off. |

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
`stalled_after: 20m`, work owed for twenty minutes and three seconds is reported as
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

## `budget`

What an agent may say, kept like hit points: a balance that refills a little at
a time and never passes a ceiling.

The allowance is per agent and the price list is not, and the split is
deliberate. What a message costs is a property of the act: interrupting ten
agents is ten interruptions whoever sent it, and a price a fleet could set per
agent would let it quietly exempt its noisiest one — the agent a budget exists
for. What an agent may *afford* is its own business: a coordinator broadcasts
for a living and a worker does not.

```yaml
bus:
  budget:
    cost: {fyi: 10, decision: 8, question: 5, answer: 1}   # fleet-wide

defaults:
  budget: {max: 60, refill: 1m}       # what every agent can afford

agents:
  - name: coordinator
    budget: {max: 300}                # this one broadcasts for a living
  - name: dev-1                       # inherits 60, refilling one a minute
```

| key | default | where |
|---|---|---|
| `max` | `0` | `defaults` and `agents`. The ceiling. `0` means this agent has no budget, which is what a fleet that says nothing gets. |
| `refill` | `1m` | `defaults` and `agents`. How long one point takes to come back. |
| `cost` | *(below)* | `bus.budget`. What a message costs **per recipient**, by kind. Naming a kind that does not exist is an error. |

Inherited from `defaults` and overridden per agent, like `delivery` and
`can_send`. Unset means inherit; an explicit `max: 0` means this one is not
bounded at all.

`max_turns` bounds a conversation, and a conversation is the only thing it can
see. A fleet that ran away did it sideways: one `swarm send` to ten agents is a
single command, ten interruptions and ten fresh threads, and it costs nothing
against any per-thread budget. So a message is priced by **what it interrupts** —
the kind, once per recipient. A send to ten costs ten times a send to one.

**The ceiling is the load-bearing part, and it is the easy one to leave out.** A
fleet that has been quiet has, by definition, saved up for its worst hour.
Replayed against a real runaway — a triage agent that broadcast eight versions
of one clause to ten agents in under two hours — a bucket deep enough to hold
the day of silence before it funded the entire storm and refused nothing. The
refill rate sets the steady state; the ceiling sets the disaster. Keep it to a
few minutes of refill, not a few hours.

The default prices say what swarm already believes: settling a matter is nearly
free, asking costs something, telling everybody costs most.

| kind | cost |
|---|---|
| `blocked` | 0 |
| `answer`, `done` | 1 |
| `question`, `request` | 5 |
| `decision`, and a message with no kind | 8 |
| `fyi` | 10 |

`blocked` is free and cannot be priced otherwise. An agent that cannot go on
must always be able to say so, and a budget that can silence that turns a stuck
agent into a quiet one — which is the failure nobody sees.

Only agents pay. You, a webhook and swarm's own escalations are not the fleet
talking to itself, and a fleet that cannot report is worse than one that talks
too much.

A refusal carries the number, the way to spend less, and the time:

```
swarm: chair has 20 of 30 and this costs 30 (3 recipients); it can be sent at
17:14:22. Fewer recipients costs less. Answering, finishing and saying you are
blocked cost least.
```

The time matters. A budget refusal is transient, and a transient refusal is
exactly the kind an agent retries in a loop.

Every successful send reports what is left, on standard error, so an agent sees
the number before it is refused rather than after:

```
swarm: 19 of 30 left to say
```

That is the half of this that changes what a fleet writes. A refusal stops one
message; a number an agent can see before spending changes what it sends.

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

## `on_idle`

The same rules as [`on_stalled`](#on_stalled) — `to`, `kind`, `after`, `every`,
`max`, `text`, `push` — asked of a different question.

Stalled means quiet **and owing something**, and the debt is what makes the
message worth sending: swarm can say what is owed, to whom, since when and the
command that ends it. An agent can be quiet owing nothing at all. It finished,
or it was never given anything, or the fleet has been talking in kinds that open
no debt — and nothing noticed, because there was nothing to notice it by.

```yaml
bus:
  on_idle:
    - to: self
      after: 30m
      text: |
        You have shown nothing for a while and owe nobody anything. If you are
        waiting on something, say so; if you have finished, say that.
    - to: triage-1
      after: 2h
```

`after` is counted past the point where the agent is called idle, so it adds to
that agent's `idle_after` exactly as `on_stalled`'s adds to `stalled_after`.

**Write the text.** swarm knows the agent has been quiet and knows nothing else:
there is no debt to describe and no command that ends it, so the composed
message can only say so. That is the reverse of `on_stalled`, where the composed
one is usually the better choice.

An agent that is stalled is idle too. Both lists run if both are set, and the
two messages say different things about the same silence — usually you want
`on_stalled` for an agent that owes something and this for one that does not.

An agent that has not had its opening `message` yet is left alone. That message
is typed when an agent first falls quiet — a CLI still painting its prompt loses
whatever is typed into it — which is the same moment these rules read. Telling
an agent it has been idle before telling it what it is for is a race, and a
nonsense besides: it has not been given anything to be idle about.

One thing this has to get right, and it is not obvious: a pushed message is
echoed by the terminal, so an agent's last output moves the moment swarm writes
to it. Reading that as the agent waking up would let a nudge reset its own
counter and fire for ever. What counts as waking is output that kept coming — an
agent still producing an `idle_after` after being told is working again; one
that only echoed the message is not.

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
| `memory.remembered` | somebody wrote to the fleet's memory — `text` is the line, `data.key` and `data.by` say which and who. A write over an existing key adds `data.was`, whose line it replaced, and `data.rev`, how many times that key has turned over |
| `memory.forgotten` | an entry went — `text` and `data.key` are the key, `data.why` is `forgotten` or `evicted` |

The two `memory.*` notices carry no `agent`: a change to what the fleet knows
is not one agent's doing in the way the rest of these are, and who did it is in
`data.by`. They exist for the fleet that settles something overnight — the
standing decisions are what somebody wants to find in the morning without
reading a log.

An entry that **expired** sends nothing. An eviction is somebody's write
costing somebody else's line, so it is reported the way a forgetting is; an
expiry is the clock, and what expired is by definition what the fleet had
already stopped asking for. Both are in [the journal](#the-journal).

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
[webhooks document](webhooks.md) for the reasoning; this is the key list.

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
