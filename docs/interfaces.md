# The TUI and the web UI

Two ways of watching the same fleet: a terminal interface, and a page served by swarm itself.

```
swarm run
```

| key | |
|---|---|
| `j` `k` `↑` `↓`, `tab` | select an agent |
| `1`…`9` | jump to an agent |
| `↵` | attach: your keys go to that agent, the detach key comes back |
| `A` | attach full screen, with a byte-perfect keyboard |
| `pgup` `pgdn` | scroll back through the agent's output — or page the agent itself, when it has taken the whole screen and keeps no scrollback |
| `m` | mosaic: every agent at once |
| `w` | who writes to whom: the fleet as a shape |
| `l` | show/hide the event log |
| `M` | mouse reporting on/off (see below) |
| `i` `s` `b` | inject / send a bus message / broadcast |
| `f` | stage a file and inject its path |
| `K` | send key presses |
| `S` `x` `r` | start / stop / restart |
| `d` | dialogue lock, on by default: typing talks to the agent (see below) |
| `esc` | in dialogue: one shortcut · `esc esc` leaves the lock |
| `:` | command line |
| `↑` `↓` `ctrl+r` | on the command line: history, and search through it |
| `q` | quit and stop every agent |

Command line: `:inject`, `:type` (no Enter), `:keys`, `:send`, `:broadcast`,
`:file`, `:start`, `:stop`, `:restart`, `:resize`, `:web`, `:q`. **Omit the
target and the command applies to the selected agent** — `:send how is it
going?` reaches the agent you are looking at.

**Tab completes** commands, targets (agents, `@group`, `@role`, `all`), key
names after `:keys`, and file paths after `:file`. One match is filled in;
several extend as far as they agree, then tab cycles through them and the
candidates are listed under the line.

**The dialogue lock is on by default**: you are here to talk to the agent on
screen, so typing does that. A printable key opens the inject line carrying that
key — you just type, no `i` first. `esc` reaches a shortcut for one key, the way
a prefix does; `esc esc` leaves the lock for good, and `d` brings it back.

Whether the lock is on is remembered between runs, so a fleet comes back the way
you left it. Each bar names the door to the other: with the lock off it carries
`d dialogue`, with it on `↵ attach`, since attaching goes on working — `↵` is
not text, so the lock never sees it.

It matters because eighteen letters are shortcuts otherwise, three of them
acting on an agent's life: typing *merci pour la relecture* at a fleet would
cycle the mosaic, restart an agent and open an inject line. Arrows and tab still
select an agent, since they are not text, and `ctrl+` combinations are
untouched. The status bar always says which mode you are in, and leaving the
lock is remembered between runs.

**`↑` and `↓` walk back through what you typed before**, and `ctrl+r` searches
it — type to narrow, `ctrl+r` again for an older match, `esc` to put back the
line you were writing. The history is kept **per command**: opening the line
with `s` offers what you sent, not the last file you staged, because the two
share no shape and neither can be reused where the other was typed. The bare
`:` line sees everything, since anything can be typed there.

It is written to `<state_dir>/history`, 0600 — it holds what you typed — and
bounded at 500 lines, since older entries name agents that no longer exist.

The agent on display is resized to the pane it occupies and follows the window
as it changes, so its own layout adapts instead of being cropped on the right.
An agent nobody is looking at keeps its configured geometry. Set
`follow_window: false` to pin that geometry instead — `:resize` then sets it by
hand.

`pgup` scrolls back into the agent's scrollback (`scrollback:` lines per agent)
and stops at the start of the session; the pane header shows how far back you
are. `pgdn` returns to the live output.

In front of an agent that has taken over the screen there is nothing to scroll:
a full-screen application keeps no scrollback and remembers what came before in
its own way. The key is sent to the agent instead, and pages that — which used
to mean attaching, pressing it, and detaching again.

With `web.enabled`, `swarm run` prints a URL carrying a token:

```
http://127.0.0.1:7777?t=1a2b3c...
```

The page shows the agent list, the selected terminal (live, and you can type into
it), a grid view, the event log, and a composer that either types into the prompt
or sends a bus message. Uploading a file stages it and injects its path.

Past localhost, treat that URL as a shell on your machine:

- set `web.token` to something you chose, or keep the generated one;
- set `web.read_only: true` if you only want to watch;
- put it behind TLS (`web.tls_cert` / `web.tls_key`) or a tunnel
  (`ssh -R`, `cloudflared`) rather than binding `0.0.0.0` in the open.

## What moves in the agent list

Three things, and they are three because they mean three different things.

**A message reaching an agent** draws chevrons pointing at the name, closing
into an envelope, while the name itself flashes in the bus colour: ` ‹‹`, ` ‹`,
` ✉`. The badge sits to the right of the name, so a mark that points left is one
coming in.

**A message leaving** is the same thing the other way, in the accent colour so
the two halves of an exchange are not confused: ` ✉`, ` ››`, ` ›`. It carries the
count when there is more than one, because a broadcast is a single command and
nine messages arriving milliseconds apart — nine flashes on one line read as a
tremble, and ` ››9` says it once.

**A message merely queued** does not move at all. It shows as ` 2✉`, a count. A
message waiting in a mailbox has interrupted nobody, and drawing it the same way
as one that landed would say the fleet is busier than it is.

**A stalled agent breathes.** The state dot rises and falls over two seconds and
keeps doing it, because stalled is a state rather than an event: it lasts as
long as the agent owes something and says nothing, where a fade would stop while
the trouble carried on.

## The fleet as a shape

`w` draws who may write to whom, with what was actually said on top.

```
  to →
          my   de   dev  devt ke   se   go
myself    ·    █    ▓    ▓    ▒    ▒    ▒
devone    ░    ·    ✗    ✗    ·    ·    ·
devtwo    ▒    ✗    ·    ✗    ·    ·    ·
devthree  ·    ✗    ✗    ·    ·    ·    ·
keystone  ·    ·    ·    ·    ·    ✗    ✗
sentinel  ·    ·    ·    ·    ✗    ·    ✗
gopher    ░    ·    ·    ·    ✗    ✗    ·

  ✗ can_send says no   · allowed, silent   ░▒▓█ what was said
```

`can_send` is written one agent at a time and read one agent at a time, so a
topology nothing like the one its author had in mind stays invisible. The fleet
above was meant as a star through `myself`; the crosses show what it is — the
developers cannot reach each other and neither can the reviewers, but every
other pair is open, which is a near-complete bipartite graph with a hub. Nobody
sees that in the file.

The shading is the last ten minutes. One full row and six near-empty ones is
what a fleet talking to itself looks like from outside.

A matrix rather than a graph, because a graph drawn in characters stops being
readable at about six nodes and this is for fleets with more.

## While an agent is starting

An agent CLI can take a while to draw its first frame, and a blank pane during
that is indistinguishable from one that failed to start. swarm draws its own
mark there instead — five agents wired in a ring, with a pulse going round it —
until the terminal has something to show.

It is drawn in braille. A terminal cell is about twice as tall as it is wide, so
the mark's diagonals are nothing like 45°, and box drawing has no glyph for a
shallow one: the first version came out as a staircase. Braille gives eight
sub-cells to place a line in, and the line comes out as a line.

The pulse thickens the wire as well as brightening it, because colour alone is
invisible on a terminal that has none and swarm cannot know whether this one
does. On a Windows console with a raster font the mark is blank rather than
wrong — it is decoration, and the pane says what it is waiting for in words
underneath.
