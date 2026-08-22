# Recipes

Fleets that do something, each with a configuration you can copy. Every one of
them has a working `swarm.yaml` under [`examples/`](../../examples), and a test
loads all of them through the real config loader — so a key that gets renamed
breaks the build rather than waiting for you to hit it.

Every agent here is `opencode`, because it runs without an account of its own.
Swap the command for `claude`, `codex` or anything else with a terminal: swarm
knows nothing about any particular agent.

| | | |
|---|---|---|
| [review](review.md) | code and review, driven by GitHub | webhooks, worktrees, `can_send` |
| [gate](gate.md) | an agent whose tests take ten minutes | patterns, `stalled_after`, `on_stalled` |
| [tickets](tickets.md) | one agent per ticket, collected when done | ephemeral templates, worktrees, `swarm spawn` |
| [ssh](ssh.md) | an agent with hands on another machine | `swarm inject`, `swarm screen`, `workspace: none` |
| [symposium](symposium.md) | four philosophers and a moderator | a star topology, `max_turns`, no git at all |
| [nightshift](nightshift.md) | a fleet woken by a clock | webhooks as a local trigger, ephemerals |
| [secondopinion](secondopinion.md) | two models, one referee | per-agent `command`, `escalate_to` |

Start with [gate](gate.md) if you already run one agent and want swarm to stop
guessing that it is stuck. Start with [symposium](symposium.md) if you want to
see what a fleet is without a repository in the way.

## Running one

```sh
cp -r examples/gate /tmp/gate && cd /tmp/gate
swarm run
```

## One thing every one of these had to learn

`message:` is typed into the agent when it launches, and an agent given a
description of its job will start doing it. In a fleet that is a problem: four
philosophers begin arguing the moment `swarm run` returns, and by the time you
have a prompt back there is no gap left to ask them anything in.

So every prompt here ends by saying not to start. Some of them read oddly out of
context — *say nothing until the moderator has put something to you* — and that
sentence is the whole difference between a fleet you drive and a fleet you
watch.

It is worth knowing before you write your own, because it does not look like a
bug when it happens. It looks like enthusiasm.

Each recipe says what it expects — a repository, an environment variable, a
reachable host. An agent whose command is not on your PATH shows as `exited` in
`swarm ls`, and `swarm events` names it:

```
error  nope  vterm: start opencode: exec: "opencode": executable file not found in $PATH
```
