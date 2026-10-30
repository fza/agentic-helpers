# The scratch sweep: architecture

`agentic-scratch` runs on two hook events, and does nothing in a project without `.tmp/`. A seat's
folder is agentic-memory's to keep, since only it knows seats; both work in the main checkout, the
parent of the git common directory, so a worktree's session writes where the main checkout's does.

| Event | What it removes |
|---|---|
| SessionEnd (`end`) | `.tmp/claude/<session-id>/` of the ending session, printing `swept <path>` |
| SessionStart (`start`) | every `.tmp/claude/<session-id>/` whose modification time is older than seven days, printing how many it reaped; with agentic-memory off, also every seat folder whose marker names a seat id and whose newest entry is older than seven days (`Scratch: reaped <n> seat folder(s) nobody came back to.`), since nothing else would; and, where `git check-ignore` does not ignore `.tmp`, appends `.tmp/` to git's global excludes file through `internal/gitignore`, printing the file |

**Matching a session identifier is the whole safety of the sweep.** A directory under
`.tmp/claude/` is removed only when its name is a lowercase UUID, the shape Claude Code gives a
session id; any other name is a keep. A payload that names no session, or names one in another
shape, removes nothing.

Reaping goes by the directory's own modification time, so a keep whose name happens to look like a
session id is not a keep. A removal that fails leaves nothing a hook's caller could act on; the next
start tries it again.

## Seat folders

`internal/scratch` holds the seat folder's rules; agentic-memory calls them.

| When | What |
|---|---|
| `claim` | makes `.tmp/claude/<seat>/` with the marker `.agentic-scratch` holding the seat's id, unless something holds the name already; prints `scratch: .tmp/claude/<seat>/`, ending ` (a keep, never swept)` where the folder there carries no marker naming the seat |
| `release` | removes the folder where `scratch_sweep` is `release` and the marker names the seat |
| SessionEnd closing the seat | removes it where `scratch_sweep` is `session` |
| `rename`, a launcher's handover | renames the folder to the new seat and re-marks it, where nothing holds the new name |
| SessionStart | moves each `.tmp/claude/<session-id>/` into the folder of the seat its session held, then reaps every folder whose marker names a seat of the store and whose newest entry, marker aside, is older than seven days |
| a compaction's brief | prints the `scratch:` line again |

**The marker, not the name, is the safety.** A folder is removed only where `.agentic-scratch` is a
plain file naming a seat id of the checkout's store; a link, a folder without the marker or one
naming another seat is a keep. A reap reads at most 10,000 entries of one folder to date it, and
leaves a larger one alone.

**Migration.** A session folder's seat comes from the seat claims and last claims in the store, else
from the session's transcript beside the current one (`<session-id>.jsonl`), read up to 64 MiB for
the last `agentic-memory: seat <name>,` line, `agentic-memory claim <name>` call or MCP claim. A
session neither names is listed in the store cache's `scratch-unmapped` file, so its transcript is
read once. A folder at the seat's name that is a keep takes nothing: what moved into it would never
be swept.

agentic-tools' turn-end check reads the seat's folder as well as the session's for scripts the
library lacks.
