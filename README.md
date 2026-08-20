# swarm

<img src="assets/icon.svg" alt="" width="88" align="right">

[![CI](https://github.com/emmanuel-deloget/swarm/actions/workflows/ci.yml/badge.svg)](https://github.com/emmanuel-deloget/swarm/actions/workflows/ci.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go 1.25+](https://img.shields.io/badge/go-1.25%2B-00ADD8.svg)](go.mod)

**[emmanueldeloget.com/swarm](https://www.emmanueldeloget.com/swarm/)** — what it
looks like, in fewer words than this page.

Run a fleet of terminal agents — `claude`, `codex`, anything with a CLI — each in
its own virtual terminal, and drive them all from one place: a TUI, a web page,
or the `swarm` command itself. The agents get that same command, so they can talk
to each other without you relaying messages.

swarm knows nothing about any particular agent. An agent is a command line.

```
swarm init          # write a starter swarm.yaml, and offer to .gitignore .swarm/
$EDITOR swarm.yaml  # list your agents
swarm run           # start the fleet + the TUI + the web remote
```

![The swarm TUI: the agent list on the left with a state per agent, the selected
agent's terminal on the right, and the event log underneath](assets/tui.png)

## What it does

- **One window for the whole fleet.** A list with live state, the selected
  agent's terminal beside it, and a mosaic view showing every agent at once.
- **A state per agent**, derived from what it prints: `working`, `idle` — quiet
  long enough that it is probably waiting for input — plus whatever the
  configured regexps name, such as `approval` or `error`. The header counts
  them by state. An agent that owes an answer and has gone quiet is `stalled`,
  and `swarm why` says what it owes, to whom, since when, and the command that
  ends it — which the agent itself has usually forgotten by then.
- **Input from anywhere.** Type into an agent from the TUI, from
  `swarm inject`, or from a browser. Send key presses (`esc`, `ctrl+c`, arrows).
  Stage a file or an image and inject its path.
- **A message bus.** `swarm send dev-3 "..."` reaches an agent whether you type
  it or another agent does. Three modes: `push` types it into the recipient's
  prompt, `pull` leaves it for `swarm inbox`, `defer` holds it until the agent
  falls quiet. What a fleet says to itself can also be bounded — kinds,
  `can_send`, a turn budget per conversation, and a pause switch.
- **A fleet that keeps running.** An agent whose command cannot start is
  relaunched with a doubling wait and given up on after `restart_max` tries,
  rather than every two seconds for ever; keys sent together are spaced by
  `key_delay`, so an agent whose UI changes state on one does not drop the
  rest.
- **A view of the talking.** `swarm bus tail` and `swarm bus stats` show what the
  fleet says to itself — busiest pairs, threads, who is sending and who is only
  receiving — and the TUI marks an agent putting a lot on the bus. Agents that
  coordinate instead of working is the failure mode of a fleet, and it is
  invisible from the terminals.
- **Agents for one task.** A template rather than an agent: `swarm spawn worker
  "take ticket 219"` makes `worker-1`, which is created owing that task and
  collected when it says the task is done. With `workspace: worktree` it gets
  its own directory and branch, taken back when it goes — and never taken with
  work still in it.
- **Incoming webhooks.** Declarative rules turn an HTTP delivery into a bus
  message, so the fleet reacts to a pull request or a ticket without you
  relaying it. The listener is signature-checked and lives on its own port.
- **Outgoing webhooks**, the same rules read backwards: an agent that finished,
  died or needs you becomes a signed POST to your endpoint. The fleet can say so
  without anyone watching it.
- **Remote control over HTTP**, token-protected, with no JavaScript terminal
  library: swarm already emulates the terminals and sends ready-made HTML.

## Install

```sh
go install github.com/emmanuel-deloget/swarm/cmd/swarm@latest
```

Or from a checkout:

```sh
go build -o swarm ./cmd/swarm
```

Requires Go 1.25+. Linux and macOS are the tested platforms; Windows works and
is newer — see [Windows](#windows).

On Windows, install it this way rather than downloading a binary. Nothing is
published as a signed `.exe` yet, and an unsigned Go binary is regularly taken
for malware by heuristics — `go install` compiles on your machine, so the
question does not arise.

## Recipes

Fleets that do something, each with a configuration you can copy and a test that
keeps it loading: code and review driven by GitHub webhooks, an agent whose test
gate takes ten minutes, one agent per ticket on its own branch, an agent with
hands on another machine over ssh, four philosophers and a moderator, a fleet
woken by a clock, two models and a referee.

**[docs/recipes/](docs/recipes/README.md)**

## Configuration

`swarm init` writes a starter `swarm.yaml`: one agent, nothing listening on a
port, and every other setting present as a commented example. Uncomment what you
need — each block is written so it loads as it stands. `swarm.example.yaml` in
this repository is that same file, if you would rather read it before installing
anything.

**[docs/configuration.md](docs/configuration.md)** is the exhaustive reference:
every key, its default, what it does, and where the file is looked up. Unknown
keys are an error rather than a warning, so a typo in a key name is reported
instead of quietly ignored.

## Where an agent works

Six agents on one checkout take turns at the index rather than working at once.
`workspace:` says what swarm does about that, per agent: nothing, its own
durable clone, or a git worktree for an agent that exists for one task. swarm
never fetches, rebases or merges — it reports where each agent works and how far
its base has drifted, and leaves the repository alone otherwise.

The modes, what they provision, and the hooks that prepare a working copy are in
[docs/configuration.md](docs/configuration.md#workspace).

## Watching the fleet

A terminal interface — a list with live state, the selected agent beside it, a
mosaic of every agent at once — and the same fleet in a browser, served by swarm
itself with no JavaScript terminal library.

**[docs/interfaces.md](docs/interfaces.md)** has the keys, the panes, attaching
and detaching, and what the web UI does and does not allow.

## Driving it from the command line

```sh
swarm run                           # the fleet, the TUI, the socket, the web UI
swarm ls                            # the agents and their state
swarm inject dev-1 "run the tests"  # type into an agent
swarm send review-1 "ready for you" # a bus message, from you or from an agent
swarm spawn worker "take rq-219"    # an agent for one task
swarm why dev-22                    # why it is stalled, and how it gets out
```

Every command, its flags, the key names, the mouse and how attaching works:
**[docs/cli.md](docs/cli.md)**.

## Agents talking to each other

One `swarm send` reaches an agent whether you typed it or another agent did.
Messages have kinds, a question leaves something outstanding until it is
answered, and a conversation can be given a turn budget so it ends.
**[docs/bus.md](docs/bus.md)** covers the delivery modes, what a debt is, the
stalled state, and how to bound the talking.

## Webhooks

Declarative rules turn an HTTP delivery into a bus message, and the same rules
read backwards turn a finished agent into a signed POST to your endpoint.
**[docs/webhooks.md](docs/webhooks.md)** has the rules, the signatures and how
to find out why nothing happened.

## How it works

```
                        ┌──────────────┐
  swarm run ───────────►│     hub      │  fleet, events, message bus
                        └──┬────────┬──┘
             ┌─────────────┘        └──────────────┐
      ┌──────▼──────┐                       ┌──────▼──────┐
      │ agent dev-1 │  pty + VT emulator    │ agent rev-1 │
      └──────┬──────┘                       └─────────────┘
             │ argv: claude, codex, ...
             ▼
   ┌───────────────────┬────────────────────┬──────────────────┐
   │ TUI (bubbletea)   │ unix socket (IPC)  │ HTTP + WebSocket │
   │                   │  ← swarm CLI       │  ← browser       │
   │                   │  ← agents          │                  │
   └───────────────────┴────────────────────┴──────────────────┘
```

Each agent runs in a real pty, so it behaves exactly as it would in your
terminal — job control, `^C`, terminal queries, alternate screen. swarm keeps a
virtual terminal emulator in sync with each pty, which is what makes it possible
to render a snapshot of any agent at any time instead of replaying a byte stream
that may start mid-sequence. The TUI renders that snapshot as ANSI, the web
server renders it as HTML lines and sends only the ones that changed.

The control socket lives in `.swarm/<session>.sock`, or in the runtime directory
with a pointer file when the project path is too long for a Unix socket.

## Windows

swarm runs on **Windows 10 build 17763 (1809) or later**, which is what
`CreatePseudoConsole` requires. Everything the tour above describes works
there: the TUI, attaching, the bus, the control socket, `swarm send` from
inside an agent. Continuous integration runs it on a Windows runner alongside
Linux and macOS: the terminal, the fleet, the bus, the control socket and the
end-to-end tests that drive the real binary.

It is the youngest of the three, and these are its differences. None is a
surprise waiting to be found — they are here because they are what a first day
on Windows runs into.

| | |
|---|---|
| `detach_key` | `ctrl+g`, not `ctrl+\`. A Windows console translates keys itself and its support for ctrl with punctuation is incomplete: `ctrl+\` and `ctrl+]` arrive as a plain backslash and bracket, so neither can be a shortcut. Everything else came through — the arrows with ctrl, shift and alt included. |
| `alt+enter` | The console's own full-screen toggle. It never reaches swarm, so it cannot be bound. |
| `swarm attach` | No status bar on the last row: holding one needs a scrolling region the console does not honour, and the bar ends up stacked across the screen. The reminder goes in the window title instead, until an agent sets a title of its own. |
| `secret_path` | Not checked. Windows has no POSIX modes — every readable file reports `0666` — and who may open a file is its ACL, which mode bits cannot express. On a shared machine, put the secret somewhere your account alone can read. |
| `workspace: none` | Reports the branch of the directory an agent started in, even after it has moved. Following a process needs `/proc`, which only Linux has; macOS is in the same position. |
| The mouse | Clicks, drags and the wheel are not passed to agents. A pseudoconsole does not carry an application's private modes back out, so swarm cannot tell whether an agent wants them — and sending them regardless would be read as text. Mouse mode still works for swarm's own interface. |
| Fonts | The shortcut bar writes `enter` where it writes `↵` elsewhere: the raster fonts the older console offers have no glyph for it. Windows Terminal does. |

Two things are worth knowing about the console you run it in. The older
`conhost` (the plain "Command Prompt" window) works, and swarm asks it to
interpret escape sequences at startup — but its font may lack the symbols
above. Windows Terminal has them, and is the default on Windows 11.

If a key does not do what you expect, `swarm keys -read` prints the bytes your
terminal actually sent for it, and the name swarm gives them. That is how the
list above was established rather than guessed.

## Limits

- Windows is supported and newer than the rest; its differences are listed
  above.
- Attaching from the TUI (`↵`) reconstructs key bytes from parsed events, which
  covers text, control keys, arrows and arrows held with ctrl/shift/alt, but not
  exotic sequences or mouse input. `A` runs the real `swarm attach` instead,
  which passes bytes through unchanged.
- `reply:` in a pattern answers a prompt on your behalf. Use it only for prompts
  you would always answer the same way.
- The webhook listener holds one secret, so it trusts one sender: giving a
  second source the same secret means either can impersonate the other. It also
  does not deduplicate retries — a sender that resends a delivery it thinks
  failed will produce a second message.

## Working on swarm

Tests, linting and where the code lives: **[CONTRIBUTING.md](CONTRIBUTING.md)**.

## Licence

MIT — see [LICENSE](LICENSE).

swarm carries other people's work inside its binary: the Go modules it links
against, and [JuliaMono](https://github.com/cormullion/juliamono), the font the
web UI draws a terminal with. Their terms travel with it, and any copy can be
asked for them:

```
swarm licenses                      # what is in this binary, and under what terms
swarm licenses juliamono            # one of them in full
swarm licenses -all > NOTICES.txt   # every text, for an audit or a release
```

The same list is a page in the web UI, linked from the header. Neither fetches
anything from the network.

The font is bundled rather than named in a CSS font stack because a stack can
only ask for what the machine already has: a terminal draws its frames out of
box-drawing characters, and a machine without them borrows them from a
proportional font, which pulls every frame apart. It is the reason the binary
is about two megabytes larger than it would otherwise be.
