# gate — an agent whose tests take ten minutes

An agent that has launched an integration suite is quiet, shows nothing, and is
working. An agent that is waiting for an answer it has forgotten it owes is
quiet, shows nothing, and is stuck. This recipe is about swarm telling them
apart, and about what it does when it is the second one.

Configuration: [`examples/gate/swarm.yaml`](../../examples/gate/swarm.yaml).

## Why quiet is not enough

`idle_after` says how long an agent may show nothing before swarm calls it
`idle`. That is a statement about the screen, not about progress — and it is
right to be: swarm cannot know what a child process is doing.

What makes the difference is whether the agent **owes** anything. A question
sent to it and not answered is a debt, and an agent that is idle while holding
one is `stalled`. That state is the whole point:

```sh
swarm why dev-1
```

says what it owes, to whom, since when, and the exact command that ends it —
which the agent itself has usually forgotten by then.

## The settings that matter

```yaml
defaults:
  idle_after: 45s          # a compile is not "gone quiet"

bus:
  stalled_after: 20m       # a gate takes twelve; waiting for it is not stalling
  on_stalled:
    - to: self
      kind: fyi
    - to: lead
      kind: question
      after: 15m
      every: 30m
      max: 3
```

`stalled_after` is counted from the moment the agent goes idle, so it *adds* to
`idle_after`. The default is twenty minutes for the reason this recipe exists: a
suite that takes twelve produces an agent that looks stalled and is not.

`on_stalled` is a ladder, not a single action. The first entry tells the agent
itself — `kind: fyi` opens no debt, so reminding an agent about a debt does not
create another one. The second waits a further fifteen minutes and asks a
different agent, repeating every half hour, at most three times. `after` is
measured past `stalled_after`, so that second entry fires at 35 minutes of
silence.

## Patterns, so the screen says something

```yaml
patterns:
  - match: '(?i)^--- FAIL|^FAIL\b|\bBUILD FAILED\b'
    state: error
    notify: true
  - match: '\? \[y/N\]|\(y/n\)'
    state: waiting
    notify: true
```

A pattern is a Go regexp against what the agent prints. `state` is what `swarm
ls` and the TUI show while it matches — any label you like, not a fixed list.
`notify: true` raises an event, which is what an outgoing webhook can carry to
your phone.

## Hearing about it when you are not watching

```yaml
outgoing:
  enabled: true
  url: https://hooks.example.com/swarm
  secret_env: SWARM_OUTGOING_SECRET
  signature_header: X-Swarm-Signature-256
  rules:
    - when: {event: agent.stalled}
      body: '{agent} has been quiet {text}'
```

The same rule language as an incoming webhook, read backwards. The events are
`agent.started`, `agent.exited`, `agent.idle`, `agent.done`, `agent.stalled`,
`agent.attention` and `agent.error`.

`agent.done` is declared, never deduced — it is raised by `swarm done`. An
earlier version guessed it from a quiet agent and a dirty tree, which was wrong
in both directions: an agent that comments on a pull request through an MCP tool
touches no file and would never have finished, while one that changed three
files and stopped to ask a question always would have.

## The catch

**`reply:` on a pattern answers a prompt on your behalf.** It is in the config
reference and it is not in this recipe, deliberately: use it only for prompts
you would always answer the same way, and never for one that asks whether to
proceed.

**A stalled agent is not a broken one.** Read `swarm screen <agent>` before you
nudge it. The lead agent here is told exactly that, in its own prompt, because
an agent that wakes a working agent every fifteen minutes is worse than no
supervision.
