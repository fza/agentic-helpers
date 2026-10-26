# The scratch sweep: architecture

`agentic-scratch` runs on two hook events, and does nothing in a project without `.tmp/`.

| Event | What it removes |
|---|---|
| SessionEnd (`end`) | `.tmp/claude/<session-id>/` of the ending session, printing `swept <path>` |
| SessionStart (`start`) | every `.tmp/claude/<session-id>/` whose modification time is older than seven days, printing how many it reaped; and, where `git check-ignore` does not ignore `.tmp`, appends `.tmp/` to git's global excludes file through `internal/gitignore`, printing the file |

**Matching a session identifier is the whole safety of the sweep.** A directory under
`.tmp/claude/` is removed only when its name is a lowercase UUID, the shape Claude Code gives a
session id; any other name is a keep. A payload that names no session, or names one in another
shape, removes nothing.

Reaping goes by the directory's own modification time, so a keep whose name happens to look like a
session id is not a keep. A removal that fails leaves nothing a hook's caller could act on; the next
start tries it again.
