# Webhooks

HTTP in, bus messages out — and the same rules read backwards, so the fleet can tell you what happened without anyone watching it.

With `hooks.enabled`, swarm listens for HTTP deliveries and turns them into bus
messages, so the fleet reacts to something happening elsewhere instead of
waiting for you to relay it.

It knows nothing about GitHub or any other sender. A rule names conditions on
paths into the delivery and renders a message from the same paths:

```yaml
hooks:
  enabled: true
  addr: 127.0.0.1:7778
  secret_path: .swarm/hook-secret     # or secret_env: HOOK_SECRET
  signature_header: X-Hub-Signature-256
  rules:
    - name: review-requested
      when:
        event: pull_request.review_requested   # a path into the JSON body
        data.member_id: "6aa593d4-…"           # …and another
      to: review-1
      message: "a review was asked of you on {data.repository}#{data.pull_request}"

    - name: merged-with-loose-issues
      when:
        header.X-Hub-Event: pull_request.merged  # a header, not the body
        data.mentioned_issues_left_open: '~\[.+\]'
      to: triage-1
      message: "{data.repository}#{data.number} was merged leaving issues open"

  unmatched:                          # only when no rule matched
    to: triage-1
    message: "unhandled event ({event}) — worth a rule?"
```

A path addresses the decoded JSON body, or a header when it starts with
`header.`. It walks objects and arrays: `data.commits.0.message`. A value is
matched exactly, or `"*"` for mere presence, or `~` followed by a regexp.
Conditions are ANDed; **every** matching rule fires.

`to:` is deliberately not templated. A payload must never choose which agent it
wakes up — route by member with one rule per member, so the config decides what
an identifier means and an unknown one wakes nobody.

## Signatures

The digest covers the raw body, so it is checked before the payload is even
decoded, and a delivery without a valid one is refused: an endpoint that accepts
unsigned payloads once accepts them always.

Senders disagree on the encoding, and guessing wrong looks exactly like a wrong
secret — so hex and base64 are both accepted, with or without a `sha256=` label.
`swarm hook sign` prints both forms; compare them against a real delivery and
whichever matches tells you the convention. If neither does, the secret is wrong.

The secret comes from exactly one of `secret_path`, `secret_env` or `secret`.
Naming two is an error rather than a precedence rule. A file must not be
readable by group or others, and trailing newlines are stripped — the one
`openssl rand -hex 32 > file` leaves behind is invisible and changes the digest
completely.

The listener has its own address rather than a route on the web remote control.
That one is guarded by a token which travels in URLs and can type into every
terminal; a webhook endpoint has to be reachable by whatever sends the events.
Those two exposures have no business sharing a socket.

## Working out why nothing happened

A webhook that does nothing looks the same from the outside whether it never
arrived, was refused, matched no rule, or reached a stopped agent. Every
delivery is recorded in full in `.swarm/logs/webhooks.log`:

```
=== 2026-08-07T16:44:58+02:00  delivery #3  accepted, 1 delivery(ies) ===
  header   X-Hub-Signature-256: sha256=89dc3a50…
  body     111 bytes
  {"data":{"mentioned_issues_left_open":[448],"number":294},"event":"pull_request.merged"}
  rule     review-requested       no     event is "pull_request.merged", want "pull_request.review_requested"
  rule     merged-loose-issues    MATCH
  send     merged-loose-issues → triage-1: reqwire#294 was merged leaving…
  answered 202

--- 2026-08-07T16:44:59+02:00  delivered: merged-loose-issues → triage-1
```

Each rule says which condition failed and what was there instead. The body is on
one line so it can be pasted into a file and replayed offline with `swarm hook
test`, which goes through the same matching code the listener uses — a
simulation, not a second implementation that could drift.

The log is on by default (`hooks.log: false` turns it off) and written 0600: a
payload carries whatever the sender put in it. Credentials in headers are
redacted; the signature is not, since comparing it is what settles a rejection.

## Writing the message

The message ends up in an agent's prompt, and a title or a branch name is
written by whoever opened the pull request — on a public repository, by anyone.
Prefer structural fields (a number, a URL, a repository) and let the agent fetch
the rest itself; values are truncated, but truncation is not a defence against
text that reads as an instruction.

`swarm run -no-hooks` disables the listener for one run.
