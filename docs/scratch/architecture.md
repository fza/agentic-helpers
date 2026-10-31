# The scratch sweep: architecture

`agentic-scratch` runs on two hook events, and does nothing in a project without `.tmp/`. A seat's
folder is agentic-memory's to keep, since only it knows seats; both work in the main checkout, the
parent of the git common directory, so a worktree's session writes where the main checkout's does.

| Event | What it removes |
|---|---|
| SessionEnd (`end`) | `.tmp/seats/<session-id>/` of the ending session, printing `swept <path>` |
| SessionStart (`start`) | moves what `.tmp/claude/` and `.tmp/seats/` hold out of place (see [Relocation](#relocation)), then every `.tmp/seats/<session-id>/` whose modification time is older than seven days, printing how many it reaped; with agentic-memory off, also every seat folder whose marker names a seat id and whose newest entry is older than seven days (`Scratch: reaped <n> seat folder(s) nobody came back to.`), since nothing else would; and, where `git check-ignore` does not ignore `.tmp`, appends `.tmp/` to git's global excludes file through `internal/gitignore`, printing the file |

**Matching a session identifier is the whole safety of the sweep.** A directory under
`.tmp/seats/` is removed only when its name is a lowercase UUID, the shape Claude Code gives a
session id; any other name is a keep. A payload that names no session, or names one in another
shape, removes nothing.

Reaping goes by the directory's own modification time, so a keep whose name happens to look like a
session id is not a keep. A removal that fails leaves nothing a hook's caller could act on; the next
start tries it again.

## Seat folders

`internal/scratch` holds the seat folder's rules; agentic-memory calls them.

| When | What |
|---|---|
| `claim` | makes `.tmp/seats/<seat>/` with the marker `.agentic-scratch` holding the seat's id and prints `scratch: .tmp/seats/<seat>/`; anything at the name whose marker does not name the seat moves to `.tmp/keep/<seat>` first (a taken name suffixed `-2`, `-3` and on), printing `Scratch: moved .tmp/seats/<seat>/ to .tmp/keep/<seat>/` above it |
| `release` | removes the folder where `scratch_sweep` is `release` and the marker names the seat |
| SessionEnd closing the seat | removes it where `scratch_sweep` is `session` |
| `rename`, a launcher's handover | renames the folder to the new seat and re-marks it, where nothing holds the new name |
| SessionStart | moves each `.tmp/claude/<seat>/` whose marker names a seat of the store to `.tmp/seats/<seat>/`, each session folder under `.tmp/seats/` or `.tmp/claude/` into the folder of the seat its session held, then reaps every folder whose marker names a seat of the store and whose newest entry, marker aside, is older than seven days |
| a compaction's brief | prints the `scratch:` line again |

**The marker, not the name, is the safety.** A folder is removed only where `.agentic-scratch` is a
plain file naming a seat id of the checkout's store; a link, a folder without the marker or one
naming another seat is a keep. A reap reads at most 10,000 entries of one folder to date it, and
leaves a larger one alone. Every folder, marker and move is written from the checkout's top folder
down through `internal/safefs`, so a `.tmp` committed as a link gets no write through it
([plugin architecture](../plugin/architecture.md#writes)).

**Migration.** A session folder's seat comes from the seat claims and last claims in the store, else
from the session's transcript beside the current one (`<session-id>.jsonl`), read up to 64 MiB for
the last `agentic-memory: seat <name>,` line, `agentic-memory claim <name>` call or MCP claim. A
session neither names is listed in the store cache's `scratch-unmapped` file, so its transcript is
read once. A folder at the seat's name that is a keep takes nothing: what moved into it would never
be swept.

## Relocation

`.tmp/seats/` belongs to agentic-helpers alone; work kept beyond a session's scratch lives under
`.tmp/keep/`, which nothing sweeps. A session start moves what sits out of place, and removes nothing
but an emptied `.tmp/claude/`:

| Entry | Goes to | When |
|---|---|---|
| `.tmp/claude/<seat>/` whose marker names a seat of the store | `.tmp/seats/<seat>/` | at once; agentic-memory moves it, or agentic-scratch with memory off, for any marker naming a seat id |
| `.tmp/claude/<session-id>/` | `.tmp/seats/<session-id>/` | at once |
| any other unmarked entry of `.tmp/claude/`, a folder or a loose file | `.tmp/keep/<name>` | once its newest modification time is 24 hours old |
| an unmarked entry of `.tmp/seats/` that is no folder named for a session | `.tmp/keep/<name>` | once its newest modification time is 24 hours old |

The split keeps one hook per kind of entry, so the two session-start hooks never move the same
entry. A folder marked for a seat of another store stays where it is. Each move prints `Scratch:
moved <from> to <to>`, and the removal `Scratch: removed the empty .tmp/claude/`.

**A taken name is never merged into or replaced.** A folder bound for `.tmp/seats/<name>/` where
something already holds the name stays, printing `Scratch: left <from> where it is, <to> is taken.`;
the newer folder goes by its own sweep, and the old one moves at the next start after that. Merging
two seat folders' throwaway would gain nothing a later start does not, and could misfile a keep. A
stray bound for `.tmp/keep/<name>` takes the first of `<name>`, `<name>-2`, `<name>-3` and on that
nothing holds, up to `-1000`. A file's suffix goes before its last extension (`notes-2.txt`,
`archive.tar-2.gz`), so the kept copy still opens as the kind of file it is; a leading dot opens a
name rather than an extension (`.env-2`). A folder always takes the suffix at the end. A file
moves there through `safefs.RenameExclusive`: hard-linked at the new name, which fails where
something filled the name since the check, then unlinked at the old, so it never replaces another
file; one with a second hard link stays where it is.

Dating a stray reads at most 10,000 entries, as the reap does; a larger one stays. `.tmp/claude/` is
removed only as a plain folder the removal itself finds empty, never as a link. Every move goes
through `internal/safefs`, so a link planted at `.tmp/claude`, `.tmp/seats` or `.tmp/keep` gets
nothing moved through it.

agentic-tools' turn-end check reads the seat's folder as well as the session's for scripts the
library lacks.
