# ssh — an agent with hands on another machine

swarm knows nothing about any particular agent: an agent is a command line that
runs in a terminal. `ssh` is a command line that runs in a terminal. So one
member of the fleet can be a live session on another host, and an `opencode`
agent can drive it.

Configuration: [`examples/ssh/swarm.yaml`](../../examples/ssh/swarm.yaml).

## The fleet

```yaml
- name: ops
  command: [opencode]
  workspace: none

- name: box
  role: shell
  command: [ssh, -tt, -o, BatchMode=yes, deploy@staging.example.com]
  workspace: none
  restart_on_exit: true
  restart_max: 5
```

`-tt` forces a pty on the far end, which is what makes `box` a terminal rather
than a pipe: job control, `^C`, and a shell that behaves the way it does when
you are sitting in front of it.

`restart_max` matters more than it looks. An agent whose command cannot start is
relaunched with a doubling wait and given up on after `restart_max` tries. A
host that is down is a command that cannot start, and five attempts is a
reasonable thing to do about it — where the default of retrying for ever is not.

`workspace: none` on both: swarm provisions nothing and presumes nothing.

## How the agent drives the shell

Two commands, and neither is the bus:

```sh
swarm inject box 'systemctl status importer; echo "__rc=$?"'
swarm screen box
```

`inject` types text into an agent's terminal. `screen` prints what that terminal
shows right now, out of the emulator swarm keeps in sync with the pty — so it is
a coherent snapshot, not a slice of a byte stream.

This is the part to get right, and it is why the recipe puts it in the agent's
own prompt: **`inject` types, it does not run.** There is no exit status, no
completion signal, and nothing that says the command has finished rather than
merely started. The convention that fixes it is a marker:

```sh
swarm inject box 'make deploy; echo "__rc=$?"'
# ... then poll:
swarm screen box | grep -o '__rc=[0-9]*'
```

The example config turns the failing case into a state, so it shows in `swarm
ls` and raises an event without anybody polling:

```yaml
patterns:
  - match: '__rc=[1-9]'
    state: error
    notify: true
```

## Two facts about authorisation

`can_send` bounds the **bus**. It is checked when a message is sent, and a
refusal names what the sender may reach, because that error is read by an agent.

`inject` is not the bus and is not bounded by it. Any client that can open the
control socket can type into any agent. The socket lives in the state directory,
so the boundary is the filesystem — the same boundary as the logs, which hold
what the agents printed.

That is worth saying plainly in a recipe that hands a shell to an agent: the
thing standing between this fleet and your staging host is the ssh
configuration on the far end, not swarm.

## The catch

**Do not use `reply:` for a password prompt.** A pattern with `reply:` answers on
your behalf, every time it matches, with no way to reconsider. The example marks
a password prompt as `attention` and raises an event instead, so it reaches a
person. Better still, arrange for it never to be asked: `BatchMode=yes` in the
command fails instead of prompting.

**`swarm screen` is a snapshot, not a transcript.** A command whose output
scrolled past is gone from it. `swarm logs box` has the recorded output.

**An injected command runs as whoever ssh logged in as, with no confirmation
step.** Give the far end an account that can do what this fleet is for, and
nothing else.
