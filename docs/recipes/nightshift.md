# nightshift — a fleet woken by a clock

Nobody is watching. A timer fires, an agent is created for one job, it does the
job, writes down what it found, and goes away. In the morning there is a branch
and a message.

Configuration: [`examples/nightshift/swarm.yaml`](../../examples/nightshift/swarm.yaml).

## The webhook as a local trigger

swarm has no scheduler, and there is no reason for it to have one — the machine
already has a good one. What it has is an HTTP listener, so the timer talks to
the fleet the same way GitHub would:

```yaml
hooks:
  enabled: true
  addr: 127.0.0.1:7779
  token: change-me-and-keep-it-out-of-git
  from: clock
  rules:
    - name: the nightly dependency audit
      when: {job: deps}
      to: dispatcher
      message: 'scheduled job "{job}" is due: {what}'
```

Bound to loopback and protected by a token rather than a signature: nothing
crosses a network, so an HMAC would be ceremony. The token is accepted as
`X-Swarm-Token`, as a bearer token, or as `?t=`.

A systemd user timer is the whole scheduling half:

```ini
# ~/.config/systemd/user/swarm-deps.service
[Service]
Type=oneshot
ExecStart=/usr/bin/curl -fsS -X POST http://127.0.0.1:7779/ \
  -H 'X-Swarm-Token: change-me-and-keep-it-out-of-git' \
  -H 'Content-Type: application/json' \
  -d '{"job":"deps","what":"go get -u ./... and report what moved"}'
```

```ini
# ~/.config/systemd/user/swarm-deps.timer
[Timer]
OnCalendar=*-*-* 03:00:00
Persistent=true
[Install]
WantedBy=timers.target
```

```sh
systemctl --user enable --now swarm-deps.timer
```

## Why there is a dispatcher

**A webhook rule cannot start an agent.** Its `to` is a delivery target: a rule
turns a request into a message and stops there. So the fleet holds one durable
agent whose entire job is to read that message and spawn the one that does the
work:

```yaml
- name: dispatcher
  command: [opencode]
  workspace: clone
  can_spawn: [auditor]
```

That is not a detour, it is the seam. A rule that could spawn would let a
payload decide how many agents exist, which is the same reason `to` is not
templated: the delivery says *what happened*, the fleet decides *what to do*.

```yaml
- name: auditor
  ephemeral: true
  workspace: worktree
  max_alive: 2
  max_lifetime: 90m       # nobody is awake to notice one that hangs
```

`max_lifetime` is the setting that makes an unattended fleet safe to leave. An
instance that has not finished by then is killed.

## Testing it before you go to bed

```sh
swarm hook test -c swarm.yaml job.json     # what the rules would send, offline
swarm hook post -c swarm.yaml job.json     # sign it and post it for real
```

```
the nightly dependency audit → dispatcher (dispatcher)
    scheduled job "deps" is due: go get -u ./... and report what moved
```

An unrecognised job still reaches somebody, because of `unmatched`:

```
unmatched → dispatcher (dispatcher)
    a timer fired with a job I have no rule for: backup
```

## Hearing about it in the morning

```yaml
outgoing:
  enabled: true
  url: https://hooks.example.com/swarm-nightshift
  secret_env: SWARM_OUTGOING_SECRET
  signature_header: X-Swarm-Signature-256
  rules:
    - when: {event: '~^agent\.(done|error|stalled)$'}
      body: '{agent}: {text}'
```

## The catch

**Deliveries are not deduplicated.** A timer with `Persistent=true` fires a
missed run at boot; two boots in a morning are two jobs. Bound the damage with
`max_alive` rather than hoping.

**An unattended fleet writes to your repository.** The auditors work in their own
worktrees on their own branches, and nothing is merged — but the branches
accumulate. Deleting them is yours: swarm removes a worktree and deliberately
keeps the branch.

**The token is in the config file.** For a loopback trigger that is a reasonable
trade; if the file is in git, it is not. `secret_env` and `secret_path` exist for
the case where it matters.
