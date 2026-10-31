# agentic-tools: architecture

The binary is `source/hooks/cmd/agentic-tools`; every decision lives in
`source/hooks/internal/tools`.

## Store

One library per project, `<data>/stores/<id>/tools/`, in the store the agent-neutral home maps the
checkout to, as agentic-memory's store is ([plugin architecture](../plugin/architecture.md#the-home)),
so every worktree and every agent of the project shares it.

| Path | Holds |
|---|---|
| `<tool>/current` | the current version number |
| `<tool>/history.jnl` | one record per version written: `{v, by, role, at, why}`, `by` the session |
| `<tool>/takes.jnl` | one record per copy taken: `{v, by, role, at}` |
| `<tool>/v<n>/tool.json` | `name`, `summary`, `usage`, `inputs`, `outputs`, `files`, and on a fork `forked_from: {project, tool, v}` |
| `<tool>/v<n>/...` | the version's files |
| `<tool>/merged` | a tombstone naming the survivor |
| `<tool>/pruned.jnl` | one record per version pruned to make room: `{v, at, hashes}`, its files' hashes and sizes |
| `index.json` | per tool its current version and fields, the hash of its description, takers, roles, the file hashes and sizes of every stored version, when each was written, and the hashes of every pruned one; the merged names |
| `.lock` | the write lock |
| `.ledger` | the store's size ledger: every file below it, its length and crc32c, and the bytes it counts |
| `format.json` | the library's format, 2 |
| `copies/` | copies taken where the checkout has no `.tmp/` |

What a session holds and has been told lives outside the store, in the slot `session/<session>` of
`<cache>/stores/<id>/tools/sessions.slots`. The `.jnl` files are journals of typed records,
each record's body the line as JSON; the slot file, the journal and the ledger are the formats
agentic-memory keeps ([memory architecture](../memory/architecture.md#file-formats)), and
`agentic-helpers dump <file>` prints each as JSON lines. A tool folder's journal is only ever
created holding every line of the JSON-lines file an earlier release kept under the `.jsonl` name,
which then goes; a reader finding no journal reads that file. A journal cut short reads up to its
last whole record, and the next append rewrites it whole.

Library format 2 is moved to under the store's lock, by the first call of a release reading it:
every tool folder's `.jsonl` files become journals, the session files of `sessions/` move into the
slot file, `format.json` is written, then `sessions/` goes. A store of a format past 2 is skipped by
every read, and refused by a write.

Every read (find, list, take, the duplicate checks, the offer, the notices) uses `index.json` and
never lists tool folders; a term search reads the current version's files by the names the index
gives. A missing, corrupt or other-schema index is rebuilt from the folders on read, and written
back for the own store under the lock; `reindex` forces the rebuild. A tool folder that cannot be
read is skipped and counted. A store is any `<data>/stores/<id>/tools/` folder holding an index, a
lock or a tool folder with a `current`, so a library renamed into place reads as is. A store is
labelled by the last part of its name (its `repo_id`, else its origin), with leading parts added
where two would share a label; a fork's origin is recorded by store id and named by the store's
name.

A library of a release before the home is renamed into its store's `tools` folder whole by
`internal/migration`, the same markers telling it a library
([plugin architecture](../plugin/architecture.md#the-migration)).

## Space

Each store's `.ledger` counts, of every stored version's files, the size the index recorded when
it read the version (`stored`), and of a version that cannot be pruned the same again as `held`,
never a stat of the files; every other file below the store is listed and counts nothing. The
machine's figure is the sum of every store's ledger: a walk over every store is far too slow for a
figure every write reply and the brief print. Every write and prune refreshes its store's ledger
under `.ledger.lock` (`internal/space`); the SessionStart pass writes one for a store that has none.
The machine-wide `<data>/tools/.space.json` an earlier release kept goes with that pass.

A write needing room sums the stored bytes of every store in reach (with `AGENTIC_TOOLS_SCOPE=project`,
the own store plus the other stores' ledgers), and, holding the own store's lock, prunes the
oldest prunable versions until the write fits, each foreign store under its own lock. Pruning is the
one change a write makes to another project's store: the limit is one per machine, so the room may
lie anywhere. Pruning appends the version's hashes to `<tool>/pruned.jnl` first, so a crash before
the folder goes leaves the duplicate check whole, then removes `v<n>/` through `os.Root` and reads
the tool's index row again. `tools.Prune`, run from agentic-memory's SessionStart pass, first
prunes every prunable version replaced longer than `tools_version_lifetime` ago, by the earliest
`written` time of a later version in the index, then does the same as a write over every store on
the machine, passing over a store whose lock it cannot take within 200 ms,
and writes back any index it had to rebuild, so the next pass reads recorded sizes again. Index
schema 3 carries the sizes; an index of an older schema is rebuilt once.

A store's index and the ledger are untrusted like the rest of a store, and the limit is shared, so
one store's claims must not make room by pruning another's valid versions, nor slip a write past
the limit. Recorded sizes only decide whether a write or the pass looks further: once they put the
machine past the limit, every version in reach is measured on disk (the `lstat` sizes of its
folder's regular files, at most 1 MiB + 64 KiB a version, six folders deep), and the prune is
decided on those sizes. A scoped caller measures every other store the same way, by its
`<tool>/v<n>/` folders: the stores a listing of `<data>/stores/` finds, never the keys the
ledger names, so no folder elsewhere on the machine counts. The own store is told apart by its
folder's identity (`os.SameFile`), not its key, since a case-insensitive file system resolves the
own key and the key its folder was made under to one folder. A version counts, and may be pruned,
only while its `v<n>/` folder is there (one `lstat` per version). A recorded size counts at most
the 1 MiB a tool may hold, a negative one as nothing, and every total saturates rather than wraps. An index recording a file
size that is negative or past the cap, or a version past it, is not read at all: it is rebuilt
from the folders, which every read caps. A current version and a fork's origin are never pruned,
whatever an index says of their age.

The cost: an index or ledger claiming more than it holds makes every write measure the stores in
reach, a walk bounded by the caps above. Rejected: capping each store's recorded figure at
`tools_total_mb` (a figure that only decides whether to measure gains nothing from a cap), and
trusting a ledger key once its folder is checked (a key may name a folder outside the library).

Rejected: refusing at the limit with no pruning (the library fills with history and refuses new
tools); counting only current versions (the disk grows past the limit unbounded); a 5 MB per-tool
cap (a helper tool is a script, and 1 MiB holds any reasonable one).

## Vectors

Vectors live outside every store, in `<cache>/tools/vectors/<embedder>/`, one directory
per embedder fingerprint (provider, model, dimensions, template hash, as memory's), as segments of `internal/vectorseg`, the store agentic-memory keeps its
entry vectors in (`docs/memory/architecture.md`, "The vector store"). A row is one description's
vectors, keyed by the description's hash, so one vector serves every store naming that description
and a fill embeds only a description no row is held for. Texts are sized as memory sizes them: the
endpoint's calibration, shared with memory under `<cache>/embedding-calibration/`, gives the cap,
`manifest.json` in the vector directory records it as `max_runes`, a row's hash is
`embed.RowHash` of the description's hash and that cap, and a description longer than the cap is
embedded in pieces of it, scored by its best. A fill runs before `find --query` and before the
offer compares a prompt, so an upgrade re-embeds the library on its own, once; `add`, `update` and
`merge` cut to the cap the directory records, calibrating where it records none. `find --query`
cuts its phrase and the offer its prompt to the recorded cap, never probing. `add`, `update` and `merge` each file
one segment holding only the rows they embedded, after the own index names them, then merge; a
rebuild files one segment per embedding request (`embed.RequestTexts` descriptions at most), each
with its own `embedding_timeout` deadline, so a rebuild the embedder fails keeps what answered. A
fill runs under `fill.lock` in the vector directory, taken without waiting: held by another
process, it embeds nothing, and a `find --query` finding no cap recorded yet refuses. A failed
probe or a calibration that does not read refuses `find --query` and `reindex` naming it; the offer
stays silent. A row counts while some store on the machine names its description, read from every store's
index whatever `AGENTIC_TOOLS_SCOPE` says, so a merge drops the vector of a description every store
replaced. `find --query`, `add`'s near-duplicate check and the offer score through one mapped
snapshot; the offer embeds the prompt only where an own tool not yet shown has a row. A cache
`<embedder>.json` left from the whole-file store is removed on first open: its vectors were embedded
uncut, so the next fill embeds its descriptions again.

Every write takes `.lock` with `flock`, waiting at most two seconds; the holder writes its pid into
the file, and a refusal names it. Before the lock, the writer's checkout is bound to its store: a
checkout matching none gets one through `home.Resolver.Ensure`, and one whose store is the user's
call is refused with the question, so no folder is made for a store nothing holds. A take line and
an index a read rebuilt are written the same way; a read creates nothing in the store, and a hook's
session slot is written under the slot file's own lock in the cache.
A version's files, its `tool.json` and its history line land
before `current` names it, and the index is rewritten whole after every write; after a crash the
index can trail the folders until the next write or `reindex`.

## Trust

Every value in every store is untrusted: a sandboxed process may write the home, and a foreign
store holds another project's agents' text.

- **Reads follow no link and wait on no pipe.** A walk opens each folder relative to its parent's
  descriptor (`os.Root`), lstat-checked first and held to the checked folder after opening; `os.Root`
  alone follows a link that stays inside its tree. A file is read only where its lstat is a regular
  file with one name (a hard link could be an outside file linked in), opened with `O_NONBLOCK` and
  held to the lstat. Caps: a tool's files 1 MiB, a version folder
  1 MiB + 64 KiB, `index.json`, `history.jnl` and `takes.jnl` 4 MiB each, a session's slot 128 KiB,
  `current` 64 bytes, a walk six folders deep and 2000 files. A malformed line is skipped.
- **Writes open no path.** A write holds the descriptor of each folder, made and opened relative to
  its checked parent as a read opens it, and creates, appends and renames relative to that
  descriptor, so a folder swapped for a link after its check is never written through. A new file
  is created with `O_EXCL`, which opens no existing name; a replaced file (`current`, `index.json`,
  a journal cut short) is written under a fresh random name and renamed over the old one; an appended
  file (`history.jnl`, `takes.jnl`, `.lock`) must be the regular file of one name its folder's
  lstat names. The walk is `internal/safefs`, which every writer of agentic-helpers shares, the
  library's own store walked from the data home
  ([plugin architecture](../plugin/architecture.md#writes)).
- **Every printed field is stripped of control characters**, an index row whose name is not its key
  is dropped, and every listing printing another agent's summary or usage opens with the framing
  lead.
- **Pushed lines carry names, paths and commands only.**

## Reach and names

Reads span every store found, the own one first; writes touch the own store only, pruning to make
room ([Space](#space)) aside, so a take of a
foreign tool records the session's holding and writes no take line into the other store.
`AGENTIC_TOOLS_SCOPE=project` lists the own store alone. Labels are the last segment of each
store's name, grown by a leading segment while two names share one.

## Hooks

| Event | Verb | Does |
|---|---|---|
| `UserPromptSubmit` | `prompt` | delivers the nudges `stop` recorded, then offers up to three own tools whose vector lies at or above `tools_query_floor` (0.45 unless set) to the prompt's, not yet named or printed in this context, same-role first; a prompt holding a credential is never embedded and gets no offer |
| `PostToolUse` | `notice` | for each holding older than its tool's current version, and each held fork whose origin moved on, adds a line once; it compares the stats of the indexes involved with what it saw last, and reads nothing more when none moved |
| `Stop` | `stop` | records, without blocking or printing, scripts (`.py .sh .go .awk`) in the session's scratch whose content and name no version of any tool in reach holds, and taken copies whose files changed |
| `SessionStart` | `start` | on a compaction, `/clear` or resume, clears the seen set |
| `SessionEnd` | `end` | clears the session's slot, its prompt vector slot and the seen set |

The offer's embed is bounded at five seconds, as agentic-memory's recall is; a timeout adds
nothing. A prompt's vector lives in a slot per session under
`<cache>/prompt-vectors/`, which agentic-memory's recall fills too: whichever hook
embeds the prompt first leaves its vector for the other. The hooks of one event run in parallel, so
the slot mostly saves an embed where recall finished first.

What the context already holds lives in a seen set, the slot `seen/<pid>-<start>` of
`<cache>/tools/sessions.slots` (format 2 of that cache, which takes in the `seen/<pid>-<start>.json`
files an earlier release kept there): `lines`, each tool version find
or list printed as `<name> v<n>`, and `offered`, the own tools the offer named. It is keyed on the
owning `claude` process (its pid and start time, found as agentic-memory finds a seat's owner)
rather than the session id: the MCP server keeps the `CLAUDE_CODE_SESSION_ID` it started with across
a `/clear`, so a key on it would outlive the context it describes. The start time keeps a reused pid
from reading a dead process's set. The offer prints names only, so it marks nothing printed: a
find after it still prints the tool in full.

## MCP answers

The `tools` server answers JSON as the `memory` server does, through the same
`internal/mcpserver`: `find` and `list` their `--json` listing (`findListing` and `listing`, whose
types give their `outputSchema`), `take` its fields struct alone (`mcpBare`), since those fields
carry every value its text prints, every other verb `message` beside a fields struct
(`mcpanswers.go`), a refusal `{"error": "refused: <why>"}` as an error result.

While its client sends nothing for two minutes, the server sweeps what the migration left for older
releases (`mcpserver.ServeIdle` with `migration.Idle`), silently; `memory` does the same.

## Tests

`internal/tools` drives `tools.Main` against stores under `t.TempDir()` with a fake embedder: every
verb and refusal, names across projects, the scope, the lock held by another descriptor, links and
pipes planted in a store and in a write's path, the credential refusals of `add`, `update` and `merge` (CLI and
MCP) and of every version `writeVersion` lays down, the vector segments (a write leaving every
segment written before untouched, the whole-file cache removed and embedded again, a replaced
description merged away, another store's kept), the hooks, and the MCP server. fdbox's backfilled
library is the fixture `test/fixtures/tools/fdbox-library`, renamed into place and read as is.
