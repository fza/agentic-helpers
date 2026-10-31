# The scratch sweep: usage

An agent writes probes, one-off scripts and captured output. Sent to the system temporary directory
they are invisible to whoever reviews the work; left at the root of the project's own `.tmp/` they
pile up, and nothing tells a throwaway apart from a draft that still matters.

So an agent writes its throwaway under one folder of its own. A session holding an agentic-memory
seat gets `.tmp/seats/<seat>/`, made when it claims the seat and named on the claim and after every
compaction: `scratch: .tmp/seats/<seat>/`. An agent always knows its seat, rarely its session id. A
session holding no seat writes under `.tmp/seats/<session-id>/`, which goes when the session ends.
Work a later session reads goes to `.tmp/keep/<name>/`, which nothing sweeps.

A subagent writes into its spawner's folder: its prompt gains `scratch: .tmp/seats/<seat>/`, or
`.tmp/seats/<session-id>/` where the session holds no seat, at every level and with memory off
(see [memory's usage](../memory/usage.md#subagents)). A `full` subagent holding a seat of its own
still writes there, so what it leaves stays where the spawner looks, and its seat's release sweeps
nothing.

```
.tmp/
├── seats/<seat>/          throwaway of a seat. Swept as scratch_sweep says.
├── seats/<session-id>/    throwaway of a session holding no seat. Swept when it ends.
├── keep/<name>/           kept. Never swept.
├── drafts/                awaiting capture. Never swept.
└── <the project's own>    never touched
```

**`.tmp/seats/` belongs to agentic-helpers alone.** An agent writes inside its own seat's or
session's folder there and never creates another name. Anything else found under `.tmp/seats/`, a
folder without a marker or a loose file not named for a session, moves to `.tmp/keep/<name>` once
nothing in it changed for 24 hours, said in one line: `Scratch: moved .tmp/seats/<name>/ to
.tmp/keep/<name>/`. A name `.tmp/keep/` already holds gets `-2`, `-3` and on: a folder at the end
of its name (`probe/` to `probe-2/`), a file before its last extension (`notes.txt` to
`notes-2.txt`, `archive.tar.gz` to `archive.tar-2.gz`). A file without one, or whose only dot leads
its name, takes it at the end (`Makefile-2`, `.env-2`).

**Only a folder the sweep can prove is throwaway is ever removed.** A seat's folder carries a marker
file, `.agentic-scratch`, naming the seat's id; only a folder whose marker names a seat of the
checkout's memory store goes. A session folder goes only where its name is a session id. A claim
finding anything else at its seat's name moves it to `.tmp/keep/<seat>/` at once, saying so, and
then makes the seat's own marked folder, so a seat's throwaway never mixes into somebody's keep.

The plugin option `scratch_sweep` chooses when a seat's folder goes:

| Value | The folder goes |
|---|---|
| `release` (default) | when `agentic-memory release` releases the seat |
| `session` | when the session holding the seat ends (`/clear` and `/resume` keep it) |
| `idle` | on no event |

In every mode a session start reaps a marked folder whose newest file is a week old, and a session
folder a week old: the backstop for a session that crashed or a seat nobody released. A rename and a
launcher moving a session to another seat carry the folder to the new name.

**A checkout that kept its scratch under `.tmp/claude/` moves it, never deleting it.** At a
session start each `.tmp/claude/<seat>/` whose marker names a seat of the store moves to
`.tmp/seats/<seat>/`, and each `.tmp/claude/<session-id>/` to `.tmp/seats/<session-id>/`. Every
other entry of `.tmp/claude/`, a folder without a marker or a loose file, moves to
`.tmp/keep/<name>` once nothing in it changed for 24 hours, so a session still writing there keeps
its paths through the day. A name already taken under `.tmp/seats/` is never merged into: the old
folder stays, said as `Scratch: left .tmp/claude/<name>/ where it is, .tmp/seats/<name>/ is taken.`,
and moves at a later start once the sweep took the newer one. An emptied `.tmp/claude/` then goes,
the only removal the migration makes. Each move is said in one line naming where it went.

A checkout that kept session folders before seats had their own moves them once: at a session start
each session folder, under `.tmp/seats/` or `.tmp/claude/`, whose session a seat's claim or the
session's transcript names moves into that seat's folder, a name already there going into a
`<first 8 characters of the session id>/` subfolder. A folder no seat claims stays, and goes a week
after its last change.

A project opts in by carrying `.tmp/`. Claiming an agentic-memory seat in a git checkout counts as
opting in: the claim creates `.tmp/` where it is missing, and adds it to git's global excludes at
once. agentic-scratch itself never creates the folder. As a session starts there, the sweep keeps `.tmp/` out of every
repository: where git does not ignore it already, `.tmp/` is appended to git's global excludes file
(`core.excludesFile`, else `$XDG_CONFIG_HOME/git/ignore`, else `~/.config/git/ignore`), said once:
`agentic-scratch: added .tmp/ to <file>, so git ignores it in every repository`.

## Off on the machine

The plugin option `scratch_enabled` switches the sweep on or off for the machine, on unless set. Off, `agentic-scratch` removes nothing and says nothing. Shell-guard's `mktemp` ban goes off with it, since its refusal would send scratch files to a `.tmp/` nothing sweeps.
