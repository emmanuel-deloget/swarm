# Agents talking to each other

One `swarm send` reaches an agent whether a person typed it or another agent did. This is what that costs, what bounds it, and how a conversation ends.

Every agent gets `swarm` on its `PATH`, already pointed at the running session:

| variable | |
|---|---|
| `$SWARM_AGENT` | its own name |
| `$SWARM_ROLE` | its role |
| `$SWARM_PEERS` | the other agents |
| `$SWARM_ROOT` | the directory holding the config file |
| `$SWARM_SHARED` | a directory every agent can read and write |
| `$SWARM_SESSION` | the session name |
| `$SWARM_SOCKET` | the control socket (used automatically) |
| `$SWARM_STATE_DIR` | where swarm keeps its state |

`$SWARM_ROOT` matters to an agent working in its own clone: it is the way back
to the project the fleet was started for. The rest of the paths are absolute.

So an agent can do this on its own:

```sh
swarm ls                                  # the other agents and their state
swarm send review-2 "please review PR 42"
swarm send @dev -file report.md "findings"
swarm inbox -wait 30s                     # block until something arrives
swarm done "nothing to change"            # settle what you were asked
```

`message:` is what an agent is told at launch — its standing brief, written
inline as a block scalar or kept in `message_file:`. It is typed when the agent
first falls quiet rather than when its process starts: a CLI still drawing its
banner would swallow it.

`swarm run` writes `.swarm/AGENTS.md` describing the fleet and these commands —
point your agents' instructions at it and they can coordinate without you. It is
generated from your configuration, so it only describes what you switched on:
message kinds, a turn budget, who may reach whom. `agents_template` replaces it
with your own.

Push recipients get messages typed into their prompt; pull recipients keep them
queued until they ask; `defer` recipients get theirs when they next fall quiet,
several at once if several arrived. Pull suits an agent that must not be
interrupted at all, push one that is waiting for work, and defer most of the
rest — it is push without cutting into what the agent was doing.

A `question`, a `request` or a `blocked` addressed to an agent leaves something
outstanding until it answers or runs `swarm done` — and the message says which,
so nobody has to guess. An agent that has owed something for `bus.stalled_after`
and is idle is reported as **stalled** — in the agent list, the
pane header and `swarm ls`, with its own glyph, as well as in the event log and
to an outgoing webhook. Both halves matter: an agent with nothing to do is quiet and
that is normal. swarm never restarts, kills or reassigns anything because of
it, since the guess can be wrong and asking costs less.

Asking is the one thing it will do, and only if you ask it to:

```yaml
bus:
  stalled_after: 15m
  on_stalled:
    - to: self          # ask the agent itself, three times, quarter-hour apart
      every: 15m
      max: 3
    - to: myself        # still stuck two hours later? triage should know
      after: 2h
      kind: question
```

With no rules, stalled stays what it was: something shown and something sent,
and nothing else.

The message swarm writes is `swarm why`, addressed — who is waiting, since
when, the question itself, and the commands that end it. That is the point of
sending anything at all: an agent stalled long enough to be asked has been
compacted, so *what are you doing?* gets an honest shrug, while the bus still
has everything the agent lost.

Two shapes are refused, both learned the hard way. A `question` to the stalled
agent itself is rejected at load: it opens a second debt on top of the one the
agent is stuck on, and answering it settles neither, so the rule fires for ever.
When a debt is what you want opened, open it from an agent that knows the work —
`to: <triage>` with `kind: question` — and let it ask properly. And a debt swarm
opened itself never starts the rules again, or telling triage about a stalled
agent makes triage stalled, and swarm ends up chasing its own notices. That one
was seen on a real fleet within a minute of the feature working.

Reminders are counted per debt and bounded by `max`; when a rule has used its
last one it says so in the event log rather than going quiet as though it had
worked. Messages are typed into the recipient's terminal whatever its
`delivery` is — an agent that is not reading its mailbox is exactly the one this
is for — which `push: false` turns off.

`swarm why` turns that signal into something anyone can act on:

```sh
swarm why dev-22      # or just `swarm why` inside an agent
```

It names who is waiting, what they asked, on which thread and since when, shows
the message that opened the debt, and ends with the command that closes it.

That last part is the reason it exists. An agent stalled for two days has been
through several context compactions by then, so the message that put it there
is gone from the one place a reader would think to look — the agent's own
memory, which is also why asking the agent gets you nowhere. The bus still has
it: a debt lives until it is settled, so who asked and when survive anything
that happens inside the agent. swarm is the fleet's external memory, and this
is the command that reads it back.

What is outstanding survives a restart, in `owed.json` inside the state
directory. It is the only thing swarm writes down that way, and the reason is
circular: an agent stuck for days is itself why someone restarts the fleet — to
upgrade the binary, to change the config, to try anything — and the restart used
to take the explanation with it. The agents keep their own sessions across a
restart; it was swarm that forgot, which is backwards for the part whose job is
to remember what the agents cannot.

Each debt carries the question that opened it, so what comes back is the whole
answer rather than its metadata. Restoring is reported and never silent: how
many came back, how old the oldest is, and any debt belonging to an agent the
fleet no longer has, which is dropped because nobody could settle it. A debt
that is no longer true is cleared with `swarm done`.

Messages themselves are not persisted — they are bounded anyway, and `swarm bus
tail` already answers for them. So a question can still outlive the history it
was carried in, and `swarm why` says so in as many words rather than printing a
blank where the question should be.

Beyond that, the configuration can bound the talking rather than hope for the
best: `delivery_by_kind` lets the fleet defer while `blocked` still gets
through, `can_send` says who may reach whom, `bus.max_turns` gives a
conversation an end, and `swarm bus pause` stops all of it without stopping the
fleet. The [configuration reference](configuration.md#bus) has the detail.
