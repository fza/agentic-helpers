# agentic-tools: architecture

The binary is `source/hooks/cmd/agentic-tools`; every decision lives in
`source/hooks/internal/tools`.

## Store

One store per project, `~/.claude/agentic-tools/<project>/`, the project keyed as agentic-memory
keys it (`.sdd/config.yaml` `repo_id`, the graph repository's where `project_dir` names one, else
a hash of the main checkout), so worktrees share it.

| Path | Holds |
|---|---|
| `<tool>/current` | the current version number |
| `<tool>/history.jsonl` | one line per version written: `{v, by, role, at, why}`, `by` the session |
| `<tool>/takes.jsonl` | one line per copy taken: `{v, by, role, at}` |
| `<tool>/v<n>/tool.json` | `name`, `summary`, `usage`, `inputs`, `outputs`, `files`, and on a fork `forked_from: {project, tool, v}` |
| `<tool>/v<n>/...` | the version's files |
| `<tool>/merged` | a tombstone naming the survivor |
| `<tool>/pruned.jsonl` | one line per version pruned to make room: `{v, at, hashes}`, its files' hashes and sizes |
| `index.json` | per tool its current version and fields, the hash of its description, takers, roles, the file hashes and sizes of every stored version, when each was written, and the hashes of every pruned one; the merged names |
| `.lock` | the write lock |
| `sessions/<session>.json` | what a session holds and has been told |
| `copies/` | copies taken where the checkout has no `.tmp/` |

Every read (find, list, take, the duplicate checks, the offer, the notices) uses `index.json` and
never lists tool folders; a term search reads the current version's files by the names the index
gives. A missing, corrupt or other-schema index is rebuilt from the folders on read, and written
back for the own store under the lock; `reindex` forces the rebuild. A tool folder that cannot be
read is skipped and counted. A store is any folder under `~/.claude/agentic-tools/` holding an index, a lock
or a tool folder with a `current`, found at most six folders deep, so a library renamed into place
reads as is.

## Space

`~/.claude/agentic-tools/.space.json` is the machine's ledger: per store key, `stored` (the bytes
of every stored version) and `held` (those of versions that cannot be pruned), from the sizes the
index recorded when it read each version, never from a stat of the files. A walk over every store
is far too slow for a figure every write reply and the brief print. `add` and `update` rewrite it
after writing, the own store's figures, or every store's where the caller reaches every store; the
SessionStart pass rewrites it whole. Writes to it hold `.space.json.lock` (`internal/space`).

A write needing room sums the stored bytes of every store in reach (with `AGENTIC_TOOLS_SCOPE=project`,
the own store plus the ledger's figures for the rest), and, holding the own store's lock, prunes the
oldest prunable versions until the write fits, each foreign store under its own lock. Pruning is the
one change a write makes to another project's store: the limit is one per machine, so the room may
lie anywhere. Pruning appends the version's hashes to `<tool>/pruned.jsonl` first, so a crash before
the folder goes leaves the duplicate check whole, then removes `v<n>/` through `os.Root` and reads
the tool's index row again. `tools.Prune`, run from agentic-memory's SessionStart pass, does the
same over every store on the machine, passing over a store whose lock it cannot take within 200 ms,
and writes back any index it had to rebuild, so the next pass reads recorded sizes again. Index
schema 3 carries the sizes; an index of an older schema is rebuilt once.

Rejected: refusing at the limit with no pruning (the library fills with history and refuses new
tools); counting only current versions (the disk grows past the limit unbounded); a 5 MB per-tool
cap (a helper tool is a script, and 1 MiB holds any reasonable one).

## Vectors

Vectors live outside every store, in `~/.cache/agentic-tools/vectors/<embedder>/`, one directory
per embedder fingerprint, as segments of `internal/vectorseg`, the store agentic-memory keeps its
entry vectors in (`docs/memory/architecture.md`, "The vector store"). A row is one description's
vector, keyed by the description's hash, so one vector serves every store naming that description
and a rebuild embeds only a description no row is held for. `add`, `update`, `merge` and a rebuild
each file one segment holding only the rows they embedded, after the own index names them, then
merge. A row counts while some store on the machine names its description, read from every store's
index whatever `AGENTIC_TOOLS_SCOPE` says, so a merge drops the vector of a description every store
replaced. `find --query`, `add`'s near-duplicate check and the offer score through one mapped
snapshot; the offer embeds the prompt only where an own tool not yet shown has a row. A cache
`<embedder>.json` left from the whole-file store becomes one segment on first open, embedding
nothing, and is removed.

Every write takes `.lock` with `flock`, waiting at most two seconds; the holder writes its pid into
the file, and a refusal names it. Under the lock the writer is checked against the project's list,
`~/.claude/agentic-helpers/<project>/writers.json`, which `internal/writers` keeps for agentic-tools
and agentic-memory both: a project with no list is claimed by recording the writer's git common
directory, a listed writer goes ahead, any other is refused, and a list that cannot be read (a link,
a pipe, a hard link, past 64 KiB, not an array of clean absolute paths) refuses everyone rather than
being claimed over. A library's own `writers.json` from an earlier release moves into the project's
list under that list's lock and is removed; until then it binds every check, so neither piece claims
past it. A take line and an index a read rebuilt are written under the lock with a check that claims
nothing. The hooks check the list only where the library exists, so a project keeping none gets no
list. A store's key is the declared
`repo_id`, which any checkout can declare; the list ties the right to write to the checkout, which
it cannot. A version's files, its `tool.json` and its history line land
before `current` names it, and the index is rewritten whole after every write; after a crash the
index can trail the folders until the next write or `reindex`.

## Trust

Every value in every store is untrusted: a sandboxed process may write `~/.claude`, and a foreign
store holds another project's agents' text.

- **Reads follow no link and wait on no pipe.** A walk opens each folder relative to its parent's
  descriptor (`os.Root`), lstat-checked first and held to the checked folder after opening; `os.Root`
  alone follows a link that stays inside its tree. A file is read only where its lstat is a regular
  file with one name (a hard link could be an outside file linked in), opened with `O_NONBLOCK` and
  held to the lstat. Caps: a tool's files 1 MiB, a version folder
  1 MiB + 64 KiB, `index.json`, `history.jsonl` and `takes.jsonl` 4 MiB each, a session file 1 MiB,
  `current` 64 bytes, a walk six folders deep and 2000 files. A malformed line is skipped.
- **Writes open no path.** A write holds the descriptor of each folder, made and opened relative to
  its checked parent as a read opens it, and creates, appends and renames relative to that
  descriptor, so a folder swapped for a link after its check is never written through. A new file
  is created with `O_EXCL`, which opens no existing name; a replaced file (`current`, `index.json`,
  a session file) is written under a fresh random name and renamed over the old one; an appended
  file (`history.jsonl`, `takes.jsonl`, `.lock`) must be the regular file of one name its folder's
  lstat names.
- **Every printed field is stripped of control characters**, an index row whose name is not its key
  is dropped, and every listing opens with the framing lead.
- **Pushed lines carry names, paths and commands only.**

## Reach and names

Reads span every store found, the own one first; writes touch the own store only, pruning to make
room ([Space](#space)) aside, so a take of a
foreign tool records the session's holding and writes no take line into the other store.
`AGENTIC_TOOLS_SCOPE=project` lists the own store alone. Labels are the last segment of each
project key, grown by a leading segment while two keys share one.

## Hooks

| Event | Verb | Does |
|---|---|---|
| `UserPromptSubmit` | `prompt` | delivers the nudges `stop` recorded, then offers up to three own tools whose vector lies at or above `tools_query_floor` (0.45 unless set) to the prompt's, not yet shown this session, same-role first; a prompt holding a credential is never embedded and gets no offer |
| `PostToolUse` | `notice` | for each holding older than its tool's current version, and each held fork whose origin moved on, adds a line once; it compares the stats of the indexes involved with what it saw last, and reads nothing more when none moved |
| `Stop` | `stop` | records, without blocking or printing, scripts (`.py .sh .go .awk`) in the session's scratch whose content and name no version of any tool in reach holds, and taken copies whose files changed |
| `SessionEnd` | `end` | removes the session's file and its prompt vector slot |

The offer's embed is bounded at five seconds, as agentic-memory's recall is; a timeout adds
nothing. A prompt's vector lives in a slot per session under
`~/.cache/agentic-helpers/prompt-vectors/`, which agentic-memory's recall fills too: whichever hook
embeds the prompt first leaves its vector for the other. The hooks of one event run in parallel, so
the slot mostly saves an embed where recall finished first.

## MCP answers

The `tools` server answers JSON as the `memory` server does, through the same
`internal/mcpserver`: `find` and `list` their `--json` listing (`listing`, whose type gives their
`outputSchema`), every other verb `message` beside a fields struct (`mcpanswers.go`), a refusal
`{"error": "refused: <why>"}` as an error result.

## Tests

`internal/tools` drives `tools.Main` against stores under `t.TempDir()` with a fake embedder: every
verb and refusal, names across projects, the scope, the lock held by another descriptor, links and
pipes planted in a store and in a write's path, the credential refusals of `add`, `update` and `merge` (CLI and
MCP) and of every version `writeVersion` lays down, the vector segments (a write leaving every
segment written before untouched, the whole-file cache migrated without an embed, a replaced
description merged away, another store's kept), the hooks, and the MCP server. fdbox's backfilled
library is the fixture `test/fixtures/tools/fdbox-library`, renamed into place and read as is.
