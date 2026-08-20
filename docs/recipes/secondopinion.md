# secondopinion — two models, one referee

The same question to two agents that cannot see each other, and a third whose
job is to decide. Useful when the cost of a wrong answer is higher than the cost
of asking twice: a migration plan, a diagnosis, a design you will live with.

Configuration: [`examples/secondopinion/swarm.yaml`](../../examples/secondopinion/swarm.yaml).

## Two agents, two models

An agent is a command line, so choosing a model is choosing arguments:

```yaml
- name: first
  role: candidate
  command: [opencode, -m, anthropic/claude-sonnet-5]
  can_send: [referee]

- name: second
  role: candidate
  command: [opencode, -m, anthropic/claude-opus-4-5]
  can_send: [referee]
```

`opencode models` lists what your providers offer. Nothing about this is
opencode-specific — `command` is argv, so the same recipe works with two
different agent CLIs, or with the same one and different `env`.

## Independence is the point

```yaml
can_send: [referee]
```

Neither candidate can reach the other. That is not politeness, it is the
experiment: two answers are only worth having if the second was not written
after reading the first.

Their prompts say the same thing in words — *you cannot reach the other
candidate, and you should not try to guess what it said* — because a model that
knows it is being compared will hedge towards the middle.

```yaml
bus:
  max_turns: 12
  escalate_to: referee
```

## The referee's actual job

The prompt is where the recipe earns its keep:

> Where they agree, say so and move on — agreement between two models is weak
> evidence, not proof. Where they differ, that is the interesting part: find the
> claim they disagree about, and check it yourself rather than counting votes.

That is not decoration. Two models trained on overlapping data agree for
reasons that have nothing to do with being right, and a referee that counts
votes turns two plausible answers into one confident one. The disagreement is
the signal; the agreement is mostly correlated error.

Ask, then read both:

```sh
swarm inject first  "Should the importer stream or buffer? Say what would make you wrong."
swarm inject second "Should the importer stream or buffer? Say what would make you wrong."
swarm screen first,second
```

`swarm send referee -kind decision "..."` is how the referee records what it
settled: a `decision` closes a debt rather than opening one.

## The catch

**Two answers cost twice.** For a question whose answer you can check in a
minute, checking is cheaper.

**This does not make an answer true.** It makes disagreement visible, which is
a different and smaller thing. A claim both candidates assert and neither
measured is exactly as unverified as it was before you asked twice.

**Model names go stale.** The two in the example config are names, not
recommendations; `opencode models` is the current list.
