# tickets — one agent per ticket, collected when it is done

A durable agent that hands out work, and a *template* rather than an agent for
the work itself. `swarm spawn worker "take rq-219"` makes `worker-1`, which is
created already owing that task, gets its own branch, and goes away when it says
the task is done.

Configuration: [`examples/tickets/swarm.yaml`](../../examples/tickets/swarm.yaml).
Run it inside a git repository: the workers need one to branch from.

## A template is not an agent

```yaml
- name: worker
  role: dev
  command: [opencode]
  ephemeral: true          # this makes it a template
  workspace: worktree
  max_alive: 3
  max_lifetime: 4h
  can_send: [lead]
```

`ephemeral: true` means nothing starts at `swarm run`. The entry describes what
an instance will look like when you ask for one:

```sh
swarm spawn worker "take rq-219: the importer drops empty rows"
```

The task is not a note attached to the instance — it is a `request` on the bus,
addressed to it, from the agent that spawned it. That is a debt: `swarm ls`
shows the instance, `swarm why worker-1` says what it owes and to whom, and the
instance is collected when it settles it:

```sh
swarm done "fixed in 4 commits on swarm/worker-1; the empty-row case has a test"
```

`max_alive` bounds one template; the top-level `ephemeral.max_alive` bounds the
whole fleet, whatever any single template allows. `max_lifetime` kills an
instance that has not finished in time, which is the only thing standing between
a stuck agent and a night of tokens.

## Who may spawn

```yaml
- name: lead
  can_spawn: [worker]
```

Declared rather than implied, and checked when the file is read: `can_spawn`
naming something that is not an ephemeral template in the same file is a
refusal, not a warning.

An agent with `can_spawn` is also required to survive its children. The loader
refuses the combination that would leave orphans:

```
agent "lead": can_spawn with restart_on_exit: false — an agent that launches
ephemerals has to be there when they finish, or their debts have nobody to go
back to. Drop the restart_on_exit line, or drop can_spawn
```

By default an ephemeral cannot spawn. It may, if you write `can_spawn` on the
template — a template that spawns its own kind is a fleet that grows while
nobody is watching, and the only thing between it and the bill is `max_alive`.

## Branches, and what happens to them

`workspace: worktree` gives each instance a directory under the state directory
and a branch named `swarm/<agent>`, branched from the remote's default branch —
or from the current commit with `worktree_base: head`.

When an instance ends, swarm removes the worktree. It never passes `--force`, so
git itself refuses while there is uncommitted or untracked work in it: the
directory stays, and swarm says so rather than deleting anything. **The branch
is never deleted.** Removing a worktree keeps the branch and its commits, which
is the whole point — the work outlives the agent that did it.

## The catch

**Nothing merges.** swarm reports where each agent works and how far its base
has drifted. Reading the branches and deciding what to keep is yours.

**An instance that dies without settling leaves the debt open.** That is
deliberate — it is how you find out — and `swarm why` is where it shows.

**`swarm spawn -f file` reads the task from a file.** A task worth more than one
line usually is one; typing a paragraph into a shell argument is how quoting
mistakes get into prompts.
