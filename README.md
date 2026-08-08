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
swarm init          # write a starter swarm.yaml
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
  them by state.
- **Input from anywhere.** Type into an agent from the TUI, from
  `swarm inject`, or from a browser. Send key presses (`esc`, `ctrl+c`, arrows).
  Stage a file or an image and inject its path.
- **A message bus.** `swarm send dev-3 "..."` reaches an agent whether you type
  it or another agent does. Push mode types it into the recipient's prompt; pull
  mode leaves it for `swarm inbox`.
- **Incoming webhooks.** Declarative rules turn an HTTP delivery into a bus
  message, so the fleet reacts to a pull request or a ticket without you
  relaying it. The listener is signature-checked and lives on its own port.
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

Requires Go 1.25+ and a Unix-like system (Linux, macOS).

## Configuration

`swarm init` writes a starter `swarm.yaml`: one agent, nothing listening on a
port, and every other setting present as a commented example. Uncomment what you
need — each block is written so it loads as it stands.

**[docs/configuration.md](docs/configuration.md) is the exhaustive reference**:
every key, its default, and what it does. `swarm.example.yaml` in this repository
is the starter file, if you would rather read it before installing anything.

The short version:

```yaml
session: default          # picks the control socket; two swarms can coexist
workdir: .                # default working directory for agents
shared: .swarm/shared     # where injected files land, readable by every agent

defaults:                 # inherited by every agent
  cols: 200               # geometry before anyone looks at the agent
  rows: 50
  follow_window: true     # resize the displayed agent to its pane
  idle_after: 3s          # quiet for this long → "idle"
  delivery: push          # bus messages are typed into the prompt
  submit_delay: 150ms     # pause between pasting and pressing Enter

web:
  enabled: true
  addr: 127.0.0.1:7777
  token: ""               # empty → a fresh one at every start, shown in the TUI

hooks:                    # incoming webhooks — see "Webhooks" below
  enabled: false
  addr: 127.0.0.1:7778

groups:                   # usable as @dev anywhere a target is expected
  dev: [dev-1, dev-2]

agents:
  - name: dev-1
    role: dev             # a role is a target too: @dev
    command: [claude]     # any argv; this is the only required field
    patterns:
      - match: "(?i)\\b(do you want|proceed\\?|\\[y/n\\])"
        state: approval   # shows up as a badge, and in the event log
        notify: true
  - name: dev-2
    role: dev
    command: [codex]
  - name: review-1
    role: review
    command: [codex]
    delivery: pull        # do not interrupt; it will run `swarm inbox`
```

Add as many agents as you have work for; nothing in swarm assumes a number.

### Where the config is looked up

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

### Patterns

A pattern is a regexp matched against the tail of the agent's screen. When it
matches, the agent gets a state badge, optionally raises an event, and can even
be answered automatically:

```yaml
    patterns:
      - match: "Run this command\\? \\(y/n\\)"
        state: approval
        notify: true
        reply: "y"        # auto-answer — only for prompts you trust
```

## The TUI

```
swarm run
```

| key | |
|---|---|
| `j` `k` `↑` `↓`, `tab` | select an agent |
| `1`…`9` | jump to an agent |
| `↵` | attach: your keys go to that agent, the detach key comes back |
| `A` | attach full screen, with a byte-perfect keyboard |
| `pgup` `pgdn` | scroll back through the agent's output |
| `m` | mosaic: every agent at once |
| `l` | show/hide the event log |
| `M` | mouse reporting on/off (see below) |
| `i` `s` `b` | inject / send a bus message / broadcast |
| `f` | stage a file and inject its path |
| `K` | send key presses |
| `S` `x` `r` | start / stop / restart |
| `:` | command line |
| `q` | quit and stop every agent |

Command line: `:inject`, `:type` (no Enter), `:keys`, `:send`, `:broadcast`,
`:file`, `:start`, `:stop`, `:restart`, `:resize`, `:web`, `:q`. **Omit the
target and the command applies to the selected agent** — `:send how is it
going?` reaches the agent you are looking at.

**Tab completes** commands, targets (agents, `@group`, `@role`, `all`), key
names after `:keys`, and file paths after `:file`. One match is filled in;
several extend as far as they agree, then tab cycles through them and the
candidates are listed under the line.

The agent on display is resized to the pane it occupies and follows the window
as it changes, so its own layout adapts instead of being cropped on the right.
An agent nobody is looking at keeps its configured geometry. Set
`follow_window: false` to pin that geometry instead — `:resize` then sets it by
hand.

`pgup` scrolls back into the agent's scrollback (`scrollback:` lines per agent)
and stops at the start of the session; the pane header shows how far back you
are. `pgdn` returns to the live output.

## The CLI

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
swarm inject dev-1 -file shot.png "what is wrong here?"

swarm send @review "PR 42 is ready" # bus message
swarm broadcast "stopping in 5 min"
swarm inbox dev-1                   # read a mailbox
swarm stage diff.patch              # copy a file where every agent can read it

swarm events -f                     # live event log
swarm restart dev-3
swarm shutdown

swarm hook test delivery.json       # what the rules would send, offline
swarm hook post delivery.json       # sign a payload and post it for real
swarm hook sign delivery.json       # the digest the listener expects
```

While attached, the bottom row of the window is a status bar showing the agent
name and the detach key; the agent gets the rows above it. `swarm attach
-no-status` gives the whole window to the agent instead.

### The mouse

Mouse reporting is **off by default**, and that is deliberate: a terminal that
reports mouse events to an application stops selecting text itself, so turning it
on costs you copy-paste of an agent's output. With it on, the wheel scrolls the
pane and a click picks an agent.

`M` toggles it while running, and `mouse: true` starts with it on. Many terminals
also let you select with shift held down while reporting is on.

### Key names

`swarm keys -list` prints every name, the bytes it sends, and the three patterns
that cover the rest: `ctrl+<char>`, `alt+<char>`, `^<char>`. Several keys in one
call are fine: `swarm keys dev-1 esc ctrl+c enter`.

A few names are **sendable but not bindable**, and the listing marks them:
`ctrl+enter` and `shift+enter` send bytes an agent may well act on, but a
terminal produces nothing distinct when you press them, so a key bound to one
would be advertised and never fire. Binding one is refused, with the reason.

### Detaching

`ctrl+\` leaves an attached agent, in the TUI and in `swarm attach` alike. It is
also what tmux, screen and asciinema like to grab, so it is configurable:

```yaml
detach_key: 'ctrl+g'      # any bindable name: ctrl+], f12, esc esc
```

`swarm run -detach-key ctrl+g` and `swarm attach -detach-key ctrl+g` override it
for one session — handy while recording. Whatever key it is no longer reaches
the agent; the one it replaced does.

`swarm run --no-tui` runs it headless if you would rather drive it entirely from
the CLI or the web.

Flags are accepted after the target, so `swarm inject dev-1 -file shot.png "..."`
works. Free text is taken literally from the first plain word onwards, so a
message can contain `-json` without it being parsed as a flag; use `--` for a
message that starts with a dash.

### Injecting text, files and images

Text is sanitised (control characters that would drive the terminal are dropped)
and wrapped in bracketed paste **only when the agent's UI asked for it**, the way
a real terminal behaves — so a multi-line prompt arrives as one message, and an
agent that does not support it never sees stray `^[[200~`.

Images and other files travel as paths: `-file` copies the file into the shared
directory and injects its absolute path, which is what agent CLIs read. The same
happens when you drop a file in the web UI.

### What was sent to an agent

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

## Agents talking to each other

Every agent gets `swarm` on its `PATH`, already pointed at the running session:

| variable | |
|---|---|
| `$SWARM_AGENT` | its own name |
| `$SWARM_ROLE` | its role |
| `$SWARM_PEERS` | the other agents |
| `$SWARM_SHARED` | a directory every agent can read and write |
| `$SWARM_SOCKET` | the control socket (used automatically) |

So an agent can do this on its own:

```sh
swarm ls                                  # the other agents and their state
swarm send review-2 "please review PR 42"
swarm send @dev -file report.md "findings"
swarm inbox -wait 30s                     # block until something arrives
```

`swarm run` writes `.swarm/AGENTS.md` describing the fleet and these commands —
point your agents' instructions at it and they can coordinate without you.

Push recipients get messages typed into their prompt; pull recipients keep them
queued until they ask. Pull suits an agent that should not be interrupted
mid-task, push suits one that is waiting for work.

## Webhooks

With `hooks.enabled`, swarm listens for HTTP deliveries and turns them into bus
messages, so the fleet reacts to something happening elsewhere instead of
waiting for you to relay it.

It knows nothing about GitHub or any other sender. A rule names conditions on
paths into the delivery and renders a message from the same paths:

```yaml
hooks:
  enabled: true
  addr: 127.0.0.1:7778
  secret_path: .swarm/hook-secret     # or secret_env: HOOK_SECRET
  signature_header: X-Hub-Signature-256
  rules:
    - name: review-requested
      when:
        event: pull_request.review_requested   # a path into the JSON body
        data.member_id: "6aa593d4-…"           # …and another
      to: review-1
      message: "a review was asked of you on {data.repository}#{data.pull_request}"

    - name: merged-with-loose-issues
      when:
        header.X-Hub-Event: pull_request.merged  # a header, not the body
        data.mentioned_issues_left_open: '~\[.+\]'
      to: triage-1
      message: "{data.repository}#{data.number} was merged leaving issues open"

  unmatched:                          # only when no rule matched
    to: triage-1
    message: "unhandled event ({event}) — worth a rule?"
```

A path addresses the decoded JSON body, or a header when it starts with
`header.`. It walks objects and arrays: `data.commits.0.message`. A value is
matched exactly, or `"*"` for mere presence, or `~` followed by a regexp.
Conditions are ANDed; **every** matching rule fires.

`to:` is deliberately not templated. A payload must never choose which agent it
wakes up — route by member with one rule per member, so the config decides what
an identifier means and an unknown one wakes nobody.

### Signatures

The digest covers the raw body, so it is checked before the payload is even
decoded, and a delivery without a valid one is refused: an endpoint that accepts
unsigned payloads once accepts them always.

Senders disagree on the encoding, and guessing wrong looks exactly like a wrong
secret — so hex and base64 are both accepted, with or without a `sha256=` label.
`swarm hook sign` prints both forms; compare them against a real delivery and
whichever matches tells you the convention. If neither does, the secret is wrong.

The secret comes from exactly one of `secret_path`, `secret_env` or `secret`.
Naming two is an error rather than a precedence rule. A file must not be
readable by group or others, and trailing newlines are stripped — the one
`openssl rand -hex 32 > file` leaves behind is invisible and changes the digest
completely.

The listener has its own address rather than a route on the web remote control.
That one is guarded by a token which travels in URLs and can type into every
terminal; a webhook endpoint has to be reachable by whatever sends the events.
Those two exposures have no business sharing a socket.

### Working out why nothing happened

A webhook that does nothing looks the same from the outside whether it never
arrived, was refused, matched no rule, or reached a stopped agent. Every
delivery is recorded in full in `.swarm/logs/webhooks.log`:

```
=== 2026-08-07T16:44:58+02:00  delivery #3  accepted, 1 delivery(ies) ===
  header   X-Hub-Signature-256: sha256=89dc3a50…
  body     111 bytes
  {"data":{"mentioned_issues_left_open":[448],"number":294},"event":"pull_request.merged"}
  rule     review-requested       no     event is "pull_request.merged", want "pull_request.review_requested"
  rule     merged-loose-issues    MATCH
  send     merged-loose-issues → triage-1: reqwire#294 was merged leaving…
  answered 202

--- 2026-08-07T16:44:59+02:00  delivered: merged-loose-issues → triage-1
```

Each rule says which condition failed and what was there instead. The body is on
one line so it can be pasted into a file and replayed offline with `swarm hook
test`, which goes through the same matching code the listener uses — a
simulation, not a second implementation that could drift.

The log is on by default (`hooks.log: false` turns it off) and written 0600: a
payload carries whatever the sender put in it. Credentials in headers are
redacted; the signature is not, since comparing it is what settles a rejection.

### Writing the message

The message ends up in an agent's prompt, and a title or a branch name is
written by whoever opened the pull request — on a public repository, by anyone.
Prefer structural fields (a number, a URL, a repository) and let the agent fetch
the rest itself; values are truncated, but truncation is not a defence against
text that reads as an instruction.

`swarm run -no-hooks` disables the listener for one run.

## Remote control

With `web.enabled`, `swarm run` prints a URL carrying a token:

```
http://127.0.0.1:7777?t=1a2b3c...
```

The page shows the agent list, the selected terminal (live, and you can type into
it), a grid view, the event log, and a composer that either types into the prompt
or sends a bus message. Uploading a file stages it and injects its path.

Past localhost, treat that URL as a shell on your machine:

- set `web.token` to something you chose, or keep the generated one;
- set `web.read_only: true` if you only want to watch;
- put it behind TLS (`web.tls_cert` / `web.tls_key`) or a tunnel
  (`ssh -R`, `cloudflared`) rather than binding `0.0.0.0` in the open.

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

## Layout

| | |
|---|---|
| `cmd/swarm` | the CLI, including `run` |
| `internal/vterm` | pty + terminal emulator, injection primitives |
| `internal/agent` | one supervised agent: lifecycle, state, patterns |
| `internal/hub` | the fleet, the environment agents get, routing |
| `internal/bus` | mailboxes |
| `internal/hook` | inbound webhooks: rules, signatures, the delivery log |
| `internal/ipc` | the Unix socket protocol |
| `internal/ui` | the TUI |
| `internal/web` | the remote control |

## Limits

- Unix only: it uses ptys, Unix sockets and process groups.
- Attaching from the TUI (`↵`) reconstructs key bytes from parsed events, which
  covers text, control keys and arrows but not exotic sequences or mouse input.
  `A` runs the real `swarm attach` instead, which passes bytes through
  unchanged.
- `reply:` in a pattern answers a prompt on your behalf. Use it only for prompts
  you would always answer the same way.
- The webhook listener holds one secret, so it trusts one sender: giving a
  second source the same secret means either can impersonate the other. It also
  does not deduplicate retries — a sender that resends a delivery it thinks
  failed will produce a second message.

## Development

```sh
go test ./...                  # add -race; CI runs -race -shuffle=on
golangci-lint run ./...        # config in .golangci.yml
govulncheck ./...              # run it with the latest Go, as CI does
```

CI runs the suite on Linux and macOS at the Go version in `go.mod`, plus the
current Go release, and separately checks `go vet`, `gofmt`, `go mod tidy`,
golangci-lint and govulncheck. It also runs weekly, so an advisory published
without a commit still shows up. There is no Windows job: swarm needs ptys,
Unix sockets and process groups.

`govulncheck` deliberately runs with the latest Go rather than the version in
`go.mod`: it reports standard-library advisories for the toolchain it runs with,
and those are fixed by the newest patch release.

## Licence

MIT — see [LICENSE](LICENSE).
