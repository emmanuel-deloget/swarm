# Working on swarm

What the tests and the checks expect, and where the code lives.

```sh
go test ./...                  # add -race; CI runs -race -shuffle=on
golangci-lint run ./...        # config in .golangci.yml
govulncheck ./...              # run it with the latest Go, as CI does
```

**Adding or removing** a dependency changes which notices the binary needs, so
they have to be regenerated:

```sh
go generate ./internal/licenses    # collects each module's licence from the module cache
```

Upgrading one does not. The notices hold licence texts, which a patch release
does not change, and no versions at all — those come from the binary itself,
which Go records at build time and `go version -m` reads back. A version copied
beside a licence would be stale from the next bump onwards, and it was: every
dependency update failed the licence test for a reason that had nothing to do
with licences.

You are unlikely to have to remember any of this. Nothing about a missing notice
fails to build, so the check is a test instead: it asks the toolchain what is
actually linked — once per operating system, since conpty is only linked on
Windows and termios only away from it — and fails both ways, on a module with
no notice and on a notice for a module that is gone. The failure names the
module and the command to run.

A module that ships no licence file at all stops the generator, on purpose.
Write down what its terms actually are in `internal/licenses/data/manual/`,
under the module's path; quoting what upstream declares is better than
synthesising a licence text in someone else's name.

CI runs the suite on Linux and macOS at the Go version in `go.mod`, plus the
current Go release, and separately checks `go vet`, `gofmt`, `go mod tidy`,
golangci-lint and govulncheck. It also runs weekly, so an advisory published
without a commit still shows up. A Windows runner covers the terminal, the
fleet, the bus, the control socket and the end-to-end tests; three packages
still drive their children through a shell and stay out of it.

Commits are signed off and signed. Two hooks check it, in `hooks/`, which git
uses once you point it there:

```sh
git config core.hooksPath hooks
```

`commit-msg` refuses a message with no `Signed-off-by` — checked rather than
added, since a sign-off a tool writes on your behalf attests to nothing.
`pre-push` refuses to send a commit that is not signed off, or not signed with
the key in `user.signingkey`; the signature can only be checked once the commit
exists, and a push is the last moment before it leaves the machine.

`govulncheck` deliberately runs with the latest Go rather than the version in
`go.mod`: it reports standard-library advisories for the toolchain it runs with,
and those are fixed by the newest patch release.

| | |
|---|---|
| `cmd/swarm` | the CLI, including `run` |
| `internal/vterm` | pty + terminal emulator, injection primitives |
| `internal/agent` | one supervised agent: lifecycle, state, patterns |
| `internal/hub` | the fleet, the environment agents get, routing |
| `internal/bus` | mailboxes, threads, what the fleet said |
| `internal/workspace` | provisions an agent's clone, and reads where it stands |
| `internal/guide` | the AGENTS.md a fleet generates for itself |
| `internal/hook` | inbound webhooks: rules, signatures, the delivery log |
| `internal/ipc` | the Unix socket protocol |
| `internal/ui` | the TUI |
| `internal/web` | the remote control |
