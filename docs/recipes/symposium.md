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

## The brake

```yaml
bus:
  max_turns: 32
  escalate_to: moderator
  allow_self_inject: false
```

Four agents that can answer each other will. `max_turns` bounds one
conversation; when it runs out, the thread escalates to `escalate_to` — here the
moderator, whose own prompt says that this is the cue to summarise and stop.

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

**This burns tokens with nothing to show for it.** That is not a criticism of
the recipe, it is the recipe: five agents talking is exactly the shape you want
to be able to recognise and stop. Set `max_turns` before you start, not after.

**A moderator is a single point of failure by design.** If it exits, the
philosophers can reach nobody, so the example gives it `restart_on_exit: true`
— which is not the default, and costs what a restart always costs: the agent
comes back without its context and is handed its `message` again.
