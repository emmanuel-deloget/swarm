# review — code and review, driven by GitHub

Two agents writing code on their own branches, one reading what they write, and
one whose only job is to decide who should see what arrives from GitHub. A pull
request opened on the repository reaches the reviewer without you relaying it.

Configuration: [`examples/review/swarm.yaml`](../../examples/review/swarm.yaml).

## The fleet

Four agents, three roles, and a `can_send` that makes the shape explicit:

```yaml
- name: dev-1
  role: dev
  command: [opencode]
  workspace: worktree      # its own directory and branch
  can_send: [triage, "@review"]
```

The devs can reach the reviewer and triage, and nobody else. The reviewer can
reach the devs — `@dev` is their role, which is a target for free. Triage can
reach everyone, because routing is what it is for.

`workspace: worktree` is what lets two devs work at once. Six agents on one
checkout take turns at the index; a worktree gives each its own directory and
its own branch, and swarm takes it back when the agent goes — never with
uncommitted work still in it.

The reviewer gets `workspace: clone`: a durable copy it can read without
touching anyone's branch.

## The webhook

```yaml
hooks:
  enabled: true
  addr: 127.0.0.1:7778
  secret_env: GITHUB_WEBHOOK_SECRET
  signature_header: X-Hub-Signature-256
  from: github
  rules:
    - name: a pull request wants reading
      when:
        header.X-GitHub-Event: pull_request
        action: '~^(opened|reopened|synchronize)$'
      to: review-1
      message: |
        PR #{number} — {pull_request.title}
        {pull_request.html_url}
        {pull_request.head.ref} into {pull_request.base.ref}, by {pull_request.user.login}
```

Three things about a rule are worth knowing before you write one.

`when` is a set of paths into the decoded body, or into a header when the path
starts with `header.`. A bare value must match exactly, `"*"` means the path
merely has to exist, and a leading `~` makes the rest a regexp. Every condition
must hold, and every matching rule fires.

`to` is **not** templated, on purpose: a payload must never choose which agent
it wakes up. That is why routing goes through a triage agent rather than through
a placeholder — a rule decides *whether*, an agent decides *who*.

`unmatched` is the one rule used when none of the others matched. Without it a
delivery you did not anticipate disappears silently.

## Setting it up on GitHub

Generate a secret and keep it out of the file:

```sh
openssl rand -hex 32 > ~/.config/swarm/github.secret
export GITHUB_WEBHOOK_SECRET=$(cat ~/.config/swarm/github.secret)
```

Then, in the repository's **Settings → Webhooks → Add webhook**: the payload URL
is wherever swarm's listener is reachable, the content type is
`application/json`, the secret is the one above, and the events are *Pull
requests*, *Pull request reviews* and *Issue comments*.

The hard part is not swarm, it is that GitHub has to reach you. If the fleet
runs on a laptop, the least troublesome answer is GitHub's own forwarder, which
is an extension rather than part of `gh`:

```sh
gh extension install cli/gh-webhook
gh webhook forward --repo=you/yourrepo --url=http://127.0.0.1:7778/ \
    --events=pull_request,pull_request_review,issue_comment
```

## Developing the rules without GitHub

`swarm hook test` runs the rules against a payload on disk and prints what would
be sent, without a listener and without a fleet:

```sh
swarm hook test -c swarm.yaml -H "X-GitHub-Event: pull_request" pr.json
```

```
a pull request wants reading → review-1 (review-1)
    PR #219 — importer: stop dropping empty rows
    https://github.com/emmanuel-deloget/swarm/pull/219
    swarm/dev-1 into main, by someone
```

Save a real delivery from GitHub's **Recent Deliveries** tab and you are
developing against the payload you will actually get. `swarm hook post` sends
one to the running listener, signed, when you want the whole path.

## The catch

**The listener holds one secret, so it trusts one sender.** Give a second
source the same secret and either can impersonate the other.

**Retries are not deduplicated.** A sender that resends a delivery it thinks
failed produces a second message. GitHub does resend.

**Nothing here merges anything.** swarm reports where each agent works and how
far its base has drifted; it never fetches, rebases or merges. The reviewer
answers the dev, and you decide what happens to the branch.
