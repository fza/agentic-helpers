# The scratch sweep: usage

An agent writes probes, one-off scripts and captured output. Sent to the system temporary directory
they are invisible to whoever reviews the work; left at the root of the project's own `.tmp/` they
pile up, and nothing tells a throwaway apart from a draft that still matters.

So an agent writes its throwaway under one folder of its own. A session holding an agentic-memory
seat gets `.tmp/claude/<seat>/`, made when it claims the seat and named on the claim and after every
compaction: `scratch: .tmp/claude/<seat>/`. An agent always knows its seat, rarely its session id. A
session holding no seat writes under `.tmp/claude/<session-id>/`, which goes when the session ends.

```
.tmp/
├── claude/<seat>/         throwaway of a seat. Swept as scratch_sweep says.
├── claude/<session-id>/   throwaway of a session holding no seat. Swept when it ends.
├── claude/<name>/         kept. Never swept.
├── drafts/                awaiting capture. Never swept.
└── <the project's own>    never touched
```

**Only a folder the sweep can prove is throwaway is ever removed.** A seat's folder carries a marker
file, `.agentic-scratch`, naming the seat's id; only a folder whose marker names a seat of the
checkout's memory store goes. A session folder goes only where its name is a session id. Any other
folder is somebody's deliberate keep and survives whatever its age, a folder at a seat's name without
the marker included: the claim then says `scratch: .tmp/claude/<seat>/ (a keep, never swept)`.

The plugin option `scratch_sweep` chooses when a seat's folder goes:

| Value | The folder goes |
|---|---|
| `release` (default) | when `agentic-memory release` releases the seat |
| `session` | when the session holding the seat ends (`/clear` and `/resume` keep it) |
| `idle` | on no event |

In every mode a session start reaps a marked folder whose newest file is a week old, and a session
folder a week old: the backstop for a session that crashed or a seat nobody released. A rename and a
launcher moving a session to another seat carry the folder to the new name.

A checkout that kept session folders before seats had their own moves them once: at a session start
each `.tmp/claude/<session-id>/` whose session a seat's claim or the session's transcript names
moves into that seat's folder, a name already there going into a `<first 8 characters of the
session id>/` subfolder. A folder no seat claims stays, and goes a week after its last change. Loose
files at the root of `.tmp/claude/` are never touched.

A project opts in by carrying `.tmp/`. Claiming an agentic-memory seat in a git checkout counts as
opting in: the claim creates `.tmp/` where it is missing, and adds it to git's global excludes at
once. agentic-scratch itself never creates the folder. As a session starts there, the sweep keeps `.tmp/` out of every
repository: where git does not ignore it already, `.tmp/` is appended to git's global excludes file
(`core.excludesFile`, else `$XDG_CONFIG_HOME/git/ignore`, else `~/.config/git/ignore`), said once:
`agentic-scratch: added .tmp/ to <file>, so git ignores it in every repository`.

## Off on the machine

The plugin option `scratch_enabled` switches the sweep on or off for the machine, on unless set. Off, `agentic-scratch` removes nothing and says nothing. Shell-guard's `mktemp` ban goes off with it, since its refusal would send scratch files to a `.tmp/` nothing sweeps.
