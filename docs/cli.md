# The CLI

Every command `swarm` takes, and what it does. The fleet is driven from here as much as from the TUI — and so are the agents themselves, which call the same commands to reach each other.

Every command talks to a running swarm over a Unix socket, so you can drive the
fleet from any other terminal — or from a script.

```sh
swarm ls                            # the fleet and its state
swarm status @dev                   # more detail
swarm screen dev-1                  # what that terminal shows right now
swarm attach dev-1                  # take it over in this window
swarm logs dev-1 -f                 # recorded output, escape sequences stripped

swarm inject dev-1 "run the tests"  # type and submit
swarm inject dev-1 -submit=false "typed but not submitted"
swarm keys dev-1 esc ctrl+c         # key presses
swarm keys -list                    # the key names swarm understands
swarm keys -read                    # what this terminal sends for a key
swarm inject dev-1 -file shot.png "what is wrong here?"

swarm send @review "PR 42 is ready" # bus message
swarm send dev-1 -kind blocked "…"  # say what it is for
swarm send dev-1 -final "we ship A" # settled: nobody may answer
swarm broadcast "stopping in 5 min"
swarm inbox dev-1                   # read a mailbox
swarm inbox -wait 30s               # ... or wait for one, if anything is filed there
swarm stage diff.patch              # copy a file where every agent can read it

swarm bus tail -f                   # the messages agents send each other
swarm bus tail -n 0 -f              # ... only what happens from now on
swarm bus stats -since 30m          # how much of the fleet's time went into talking
swarm bus threads                   # the open conversations
swarm bus pause "shipping"          # hold every delivery; the agents keep working
swarm bus resume -flush             # let them through again
swarm done "nothing to change"      # settle what an agent was asked
swarm events -f                     # live event log
swarm start dev-3                   # start / stop / restart one or a group
swarm stop dev-3
swarm restart dev-3
swarm spawn worker "take rq-219"    # an agent for one task, collected when it is done
swarm why dev-22                    # why it is stalled, and how it gets out
swarm info                          # session, socket, web URL and token
swarm shutdown
swarm version                       # which build this is
swarm licenses                      # the terms of everything bundled in the binary
swarm config check [-fix]           # a config that has gone stale

swarm hook test delivery.json       # what the rules would send, offline
swarm hook post delivery.json       # sign a payload and post it for real
swarm hook sign delivery.json       # the digest the listener expects
```

While attached, the bottom row of the window is a status bar showing the agent
name and the detach key; the agent gets the rows above it. `swarm attach
-no-status` gives the whole window to the agent instead.

On Windows there is no status bar: holding a line at the bottom of the screen
needs a scrolling region the console does not honour, and the bar ends up
stacked across the display rather than sitting on one row. The reminder goes in
the window title instead — an agent that sets its own title takes it back, so
keep the detach key in mind there.

An attach follows the window on Windows too, by asking the console its size
four times a second: there is no SIGWINCH, and the resize arrives as a console
input record an attach cannot read without eating the keystrokes it is there to
forward.

## The mouse

Mouse reporting is **off by default**, and that is deliberate: a terminal that
reports mouse events to an application stops selecting text itself, so turning it
on costs you copy-paste of an agent's output. With it on, the wheel scrolls the
pane and a click picks an agent.

`M` toggles it while running, and `mouse: true` starts with it on. Many terminals
also let you select with shift held down while reporting is on.

Clicks reach the agent as well, when it tracks the mouse and the pane is
showing its live screen — not while you are scrolled back, where a position
would name a cell that has moved. Drags travel with them if the agent asked
for movements, which is what selecting text inside an agent is made of.

None of that happens on Windows, and not for want of trying: a pseudoconsole
does not pass an application's private modes back out, so swarm cannot see
whether the agent wants the mouse — and an agent that is sent mouse reports it
never asked for reads them as text. `pgup` and `pgdn` still reach it.

In front of an agent that has taken over the screen the wheel goes to the agent,
for the same reason the page keys do: there is no scrollback here to move
through. What it sends depends on what the agent asked for — a mouse report if
it tracks the mouse, arrow keys if it does not, which is what a terminal sends
in their place and what makes a wheel scroll a pager at all.

## Key names

`swarm keys -list` prints every name, the bytes it sends, and the patterns that
cover the rest: `ctrl+<char>`, `alt+<char>`, `^<char>`, and a modifier on a
navigation key — `ctrl+left`, `shift+home`, `ctrl+shift+pgup`. Several keys in one
call are fine: `swarm keys dev-1 esc ctrl+c enter`.

`swarm keys -read` answers the other direction: press a key, and it prints the
bytes your terminal actually sent and which name swarm would give them. It asks
for mouse reporting too, so "the wheel does nothing" gets the same answer: a
terminal that does not report the wheel sends up and down arrows instead, and
you can see which one you have. That
question is not rhetorical — a Windows console sends a plain backslash for
`ctrl+\`, so the key that detached everywhere else typed into the agent
instead. Where a binding does not fire, this says whether the key ever arrived.

A few names are **sendable but not bindable**, and the listing marks them:
`ctrl+enter` and `shift+enter` send bytes an agent may well act on, but a
terminal produces nothing distinct when you press them, so a key bound to one
would be advertised and never fire. Binding one is refused, with the reason.

## Detaching

`ctrl+\` leaves an attached agent, in the TUI and in `swarm attach` alike —
`ctrl+g` on Windows, whose console cannot produce `ctrl+\`. It is
also what tmux, screen and asciinema like to grab, so it is configurable:

```yaml
detach_key: 'ctrl+g'      # any bindable name: ctrl+], f12, esc esc
```

`swarm run -detach-key ctrl+g` and `swarm attach -detach-key ctrl+g` override it
for one session — handy while recording. Whatever key it is no longer reaches
the agent; the one it replaced does.

Two things a Windows console never passes on, whatever you configure. It
translates keys to escape sequences itself, and its support for ctrl with
punctuation is incomplete: `ctrl+\` arrives as a plain backslash, while a
letter becomes `0x07` the way `ctrl+a`..`ctrl+z` all do. And `alt+enter` is the
console's own full-screen toggle, so it never reaches swarm at all.

`swarm run --no-tui` runs it headless if you would rather drive it entirely from
the CLI or the web.

## Who is asking

Inside an agent the shim sets `$SWARM_AGENT`, and that is the sender of anything
that agent does — `swarm send -from` disagreeing with it is refused rather than
believed, and a `-from` naming an agent that does not exist is refused too,
because `swarm bus stats` reads that record back.

`swarm inject` and `swarm keys` are checked against `can_send`, the same rule
`swarm send` obeys: reaching a peer is reaching a peer, whichever command was
used. Neither becomes a bus message, though. An injection is bytes in a
terminal, `-raw` and `-submit=false` have no equivalent on the bus, and a shell
handed a rendered message would try to run it — so the check is applied and
nothing else changes. `swarm events` records the injection with the agent that
asked for it.

A person's shell has no `$SWARM_AGENT`, so none of this applies to you:
`swarm inject` types raw text into a terminal, which is what it is for.

This bounds what a fleet is meant to do. It is not a security boundary — a
client sets its own environment — and swarm is not a security product. It stops
a prompt that drifted and a command typed by mistake.

## Machine-readable output

`-json` prints the response instead of the prose, on every command that has a
response: `ls`, `status`, `why`, `spawn`, `send`, `inbox`, `done`, `screen`,
`stage`, `events`, `info`, `inject`, `keys`, `shutdown`, and all four of
`swarm bus`. `events` and `bus tail` print one document per line as they arrive,
so they can be piped into something that reads a stream.

```sh
swarm ls -json | jq -r '.[] | select(.state=="stalled") | .name'
swarm why dev-1 -json | jq '.debts[].thread'
swarm bus stats -since 1h -json | jq '.pairs[:3]'
swarm spawn worker "take rq-219" -json | jq -r .text
```

`attach` and `logs` do not offer it: one hands over a terminal and the other
pours out the bytes an agent printed. Neither has a response to serialise, and a
flag that is accepted and ignored is worse than one that is not there.

Flags are accepted after the target, so `swarm inject dev-1 -file shot.png "..."`
works. Free text is taken literally from the first plain word onwards, so a
message can contain `-json` without it being parsed as a flag; use `--` for a
message that starts with a dash.

## Injecting text, files and images

Text is sanitised (control characters that would drive the terminal are dropped)
and wrapped in bracketed paste **only when the agent's UI asked for it**, the way
a real terminal behaves — so a multi-line prompt arrives as one message, and an
agent that does not support it never sees stray `^[[200~`.

Images and other files travel as paths: `-file` copies the file into the shared
directory and injects its absolute path, which is what agent CLIs read. The same
happens when you drop a file in the web UI.

## What was sent to an agent

`swarm logs` shows what an agent *printed*. With `log_input: true`, swarm also
records what it *sent*, in `.swarm/logs/<agent>.input.log`:

```
2026-08-07T00:20:27+02:00	inject	"run the tests"
2026-08-07T00:20:27+02:00	submit	"\r"
2026-08-07T00:20:31+02:00	keys	"\f"
2026-08-07T00:20:33+02:00	terminal-reply	"\x1b[?62;c"
```

Each line says when, where it came from, and the exact bytes — including the
answers the emulator gives to the agent's own queries. It settles "did swarm
type that, or did the agent print it itself?" in one grep. Off by default, and
written 0600: it holds what you typed.

Leaving an attach puts the terminal back the way it was found: alternate screen,
cursor, scrolling region, and every mode the agent may have switched on —
**mouse reporting above all**. An agent is never told the connection ended, so
it never turns those off itself, and a terminal left reporting mouse events
stops selecting text on its own. `M` in the TUI cannot help there: swarm did not
turn it on, so it has nothing to turn off.

## Agents for one task

An entry with `ephemeral: true` is not an agent but the shape of one. Nothing is
started for it; `swarm spawn` makes instances from it that run one task and are
collected when they say they have finished.

```yaml
agents:
  - name: triage
    command: [claude]
    can_spawn: [worker]      # who may launch them

  - name: worker
    ephemeral: true
    command: [claude]
    max_alive: 3
```

```sh
swarm spawn worker "take ticket 219"      # prints: worker-1
swarm spawn worker -f brief.md            # or - for standard input
```

**Spawning opens a debt**, and that is the whole design. The task arrives as a
bus `request`, so everything already built for debts applies to it: `swarm why
worker-1` says what it is on and since when, `on_stalled` asks it where it is
when it goes quiet, the debt survives a restart of the hub, and `swarm done` —
the agent saying the work is finished — is what collects it. Its task is its
life.

A template's name is a group of whatever is alive, `@worker`, which is what you
write into another agent's `can_send`. Launched by an agent, an instance gets
`SWARM_PARENT` and the two can reach each other; when it dies, its parent is
told in a message that stands on its own, since a parent that restarted may
never have known it existed.

Refused, each because it fails later and less clearly: restarting an instance,
which would return knowing nothing of the task it still owes; spawning without
a task, which would make an agent nothing could ever collect; spawning past
`max_alive`; and spawning at all without `can_spawn`.

**`workspace: worktree`** gives each instance its own directory and branch,
sharing the repository's object store — no clone, no fetch, and two instances
cannot edit the same file. It is the ephemeral agent that makes this workable:
a worktree belonging to an agent that never ends is a branch nobody ever
merges.

When an instance is collected, swarm takes its worktree back — and it has to,
since a branch cannot be checked out in two worktrees at once, so leaving the
directory would stop anyone picking the work up. What it will not do is take
work with it. The refusal is git's own: `git worktree remove` declines a
directory holding modified or untracked files, and swarm never passes
`--force`, so a worktree with anything uncommitted in it is kept and its path
printed. Committed work survives either way — removing a worktree keeps the
branch — and the branch itself is only deleted once the remote has every commit
on it.

swarm manages only the worktrees it made, under `<state_dir>/worktrees/`. An
agent that opens one for itself is doing its job, and swarm does not look. What
it cannot do is both: giving an agent a worktree *and* letting it create its own
inside it puts two managers on one tree, which is the one arrangement to avoid —
swarm will not detect it, because knowing that `--worktree` means something to
`claude` and something else to another CLI is exactly the knowledge it refuses
to have.

The [configuration reference](configuration.md#ephemeral-agents) has the
rest.
