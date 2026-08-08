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
- [`hooks`](#hooks)
- [Durations](#durations)

## Top level

| key | default | |
|---|---|---|
| `session` | `default` | Names this swarm. It picks the control socket, so two swarms with different session names run side by side. No slashes or spaces. |
| `workdir` | the config's directory | Working directory for every agent that does not override it. |
| `state_dir` | `.swarm` | Everything swarm writes: the control socket, the logs, the CLI shim, the staged files. `swarm init` offers to add it to `.gitignore`. |
| `shared` | `<state_dir>/shared` | Where injected files are staged so every agent can reach them by path. Agents get it as `$SWARM_SHARED`. |
| `env` | `{}` | Added to the environment of every agent. Per-agent `env` wins. |
| `detach_key` | `ctrl+\` | Leaves an attached agent, in the TUI and in `swarm attach`. Any name `swarm keys -list` marks as bindable: `ctrl+g`, `ctrl+]`, `f12`, `esc esc`. Configurable because the default is what tmux, screen and asciinema like to grab. |
| `log_input` | `false` | Record everything swarm *sends* to an agent in `.swarm/logs/<agent>.input.log`. Off by default and written 0600: it holds what you typed. |
| `mouse` | `false` | Mouse reporting in the TUI. Off by default because a terminal that reports mouse events stops selecting text itself — reading an agent's output matters more than the wheel. `M` toggles it at runtime. |
| `defaults` | — | Inherited by every agent; see below. |
| `agents` | — | The fleet. At least one is required. |
| `groups` | `{}` | Named sets of agents. |
| `web` | — | The remote control. |
| `bus` | — | Inter-agent messaging. |
| `hooks` | — | Incoming webhooks. |

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
| `restart_backoff` | `2s` | Delay before an automatic restart. |
| `submit_delay` | `120ms` | Pause between pasting text and sending the newline that submits it. Agent UIs that re-render on paste need this; too short and the newline lands before the paste has been absorbed. |
| `bracketed_paste` | `true` | Allow injected text to be wrapped in `ESC[200~`/`ESC[201~`, so a multi-line prompt arrives as one message. It is only actually used when the agent's own UI turned the mode on, the way a real terminal behaves. |
| `follow_window` | `true` | Resize the displayed agent to the pane showing it, so its layout adapts instead of being cropped. Turn it off to pin `cols`/`rows` — which is then what the web UI and `swarm screen` show. |
| `delivery` | `push` | What happens to a bus message addressed to this agent. `push` types it into the terminal right away; `pull` queues it until the agent runs `swarm inbox`. |
| `message_template` | `[swarm] message from {from}: {body}` | How a pushed bus message is rendered before injection. Placeholders: `{id}` `{thread}` `{from}` `{to}` `{body}` `{files}` `{time}`. |

Pull suits an agent that should not be interrupted mid-task; push suits one that
is waiting for work.

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
| `workspace` | `shared` | What swarm does about this agent's working copy — see below. |

Plus every key from [`defaults`](#defaults), which overrides the inherited value
for this agent alone.

Agents also receive, whatever their `workdir`:

| variable | |
|---|---|
| `$SWARM_AGENT` | its own name |
| `$SWARM_ROLE` | its role |
| `$SWARM_PEERS` | the other agents, comma separated |
| `$SWARM_SHARED` | the shared directory |
| `$SWARM_SESSION` | the session name |
| `$SWARM_SOCKET` | the control socket, used automatically by the `swarm` command |
| `$SWARM_STATE_DIR` | `.swarm` |
| `PATH` | prefixed with `.swarm/bin`, which holds a `swarm` pointing at this session |

There is one state directory per config file, shared by every agent — a
per-agent `workdir` changes where the process runs, not which fleet it belongs
to.

### `workspace`

`workdir` says *where* an agent runs; `workspace` says what swarm does there.

| mode | swarm does | `workdir` |
|---|---|---|
| `shared` | nothing | the common one |
| `clone` | provisions a durable clone, once | `<state_dir>/workspaces/<name>` unless you name one |
| `none` | nothing, and presumes nothing | yours; the agent is free to move |

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

Exactly one of `secret`, `secret_env` and `secret_path` may be named. Two is an
error rather than a precedence rule: silently preferring one is how a swarm ends
up verifying against a secret its owner thought they had replaced.

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

`idle_after`, `restart_backoff` and `submit_delay` take Go duration strings:
`150ms`, `3s`, `2m`, `1h30m`. A bare number is **not** valid.

## Booleans that are three-valued

`autostart`, `restart_on_exit`, `bracketed_paste`, `follow_window` and
`bus.enabled` distinguish "unset" from `false`, so that an agent leaving one out
inherits the default rather than silently getting `false`. In practice this only
matters when you want an agent to override a `defaults` entry back to `true`:
write it explicitly.
