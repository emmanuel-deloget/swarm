# symposium — four philosophers and a moderator

No repository, no git, nothing to build. Four agents argue about something, and
none of them can reach any of the others: every message goes through a
moderator. It is the smallest fleet that shows what swarm is when there is no
code in the way — and the clearest illustration of what `can_send` is for.

Configuration: [`examples/symposium/swarm.yaml`](../../examples/symposium/swarm.yaml).

## The shape is one line, repeated

```yaml
- name: diogenes
  role: philosopher
  command: [opencode]
  can_send: [moderator]
```

Four of those and a moderator with `can_send: [all]` is a star. A refused send
tells the sender what it *may* reach rather than only saying no, because that
text is read by an agent: telling it "no" produces a retry, telling it where to
go produces a route.

The whole send is refused before anything is delivered — half a broadcast is
worse than none.

## Personality is a prompt

```yaml
message: |
  You argue as Spinoza: from definitions towards consequences, patiently, and
  without appealing to intention in nature. You may only reply to the
  moderator.
```

`message` is typed into the agent once per run, at launch. `message_file` reads
it from a file instead, which is what you want past a few lines.

## Making it feel live

```yaml
defaults:
  delivery: push
  workspace: none
```

`push` types a message straight into the recipient's prompt. The alternatives
are `pull`, which leaves it for `swarm inbox`, and `defer`, which holds it until
the agent falls quiet — right for a fleet writing code, wrong for a debate.

## The brake, and why it needs two parts

```yaml
bus:
  max_turns: 16
  escalate_to: moderator
  allow_self_inject: false
```

`max_turns` bounds one conversation, and it really is one conversation: an
agent's message inherits the thread it was last written to on, so the whole
debate hangs off the thread the moderator opened. When the budget runs out the
sender is refused, told the number, and told who arbitrates:

```
swarm: this thread has used its 4 turns; decide alone or escalate to moderator
```

Sixteen is two rounds. A round is eight turns — four questions out, four
answers back — so raise it once you have seen what a round costs, not before.

The budget alone is not enough, and this is the part worth taking from this
recipe. A moderator that relays each answer to each of the others turns four
answers into twelve messages, and twelve into thirty-six: the cost is
quadratic in the number of philosophers, and the budget only decides how far
along that curve you get. So the moderator's prompt puts it in rounds instead:

```
swarm send @philosopher "the question, and what a good answer looks like"
```

One message, four recipients — `@philosopher` is their role, which is a target
for free. Collect the four answers, post one summary of where they actually
disagree, and only then decide whether a second round earns its cost.

`allow_self_inject: false` is the default and worth leaving alone: an agent
sending to itself is a loop with no moderator in it.

Watch what it costs while it runs:

```sh
swarm bus stats -since 10m     # busiest pairs, who sends and who only receives
swarm bus tail -f              # the messages, as they are carried
swarm bus pause "enough"       # hold every delivery; the agents keep working
swarm bus resume -flush
```

Agents that coordinate instead of working is the failure mode of a fleet, and it
is invisible from the terminals. Here it is the entire activity, which makes this
a good place to learn what those numbers look like.

## The catch

**It still gets chatty, and quickly.** That is not a criticism of the recipe,
it is the recipe: five agents talking is exactly the shape you want to be able
to recognise and stop. Set `max_turns` before you start, not after — and keep
`swarm bus pause "enough"` within reach, which holds every delivery without
stopping the agents.

**A moderator is a single point of failure by design.** If it exits, the
philosophers can reach nobody, so the example gives it `restart_on_exit: true`
— which is not the default, and costs what a restart always costs: the agent
comes back without its context and is handed its `message` again.
