# Memory: architecture

## Where it keeps what

```
<data>/stores/<id>/memory/                              the project's store; <data> is the home's data root (plugin usage)
├── format.json                                         the store's one format version
├── seats.json                                          seat id → its current name, its shelf, its private mark
├── shelves.json                                        shelf id → its name, follows and lifetime
├── log/tail.jnl, log/<hash>.seg, log/manifest.json    the append-only log: the truth
├── seats/<id>.json                                     who holds the seat, which process, which transcript
├── seats/<id>.last.json                                the claim of the seat's last holder
├── seats/<id>.slice                                    the last turns of the seat's last session
├── seats/.lock                                         held while a claim is read, judged and written
├── attachments.refs                                    the attachment hashes its entries still holding a body name
├── credentials-found.json                              what the migration scan found, until each seat's brief tells it
├── .ledger                                             the size ledger: every file below the store, its length and crc32c
└── backup/format-<n>-<UTC stamp>/                      what a migration changed, as it was before
<data>/stores/<id>/identity.json                        the store's checkouts and identifiers (internal/home)
<data>/sessions/claude/<session>/context.json           the context reading the plugin writes, with the compaction window
<data>/attachments/<sha256>[.gz]                        every project's attached files, once each
<data>/attachments/.ledger                              the size ledger: each stored file, its length, crc32c and the size its attach recorded
<cache>/stores/<id>/memory/                             vectors, heat, the folded log, anchor hashes: rebuilt at will
<cache>/stores/<id>/memory/sessions.slots               per-session and per-seat state, one slot each
```

`agentic-helpers dump <file>` prints any of the binary files above (the log's tail and segments, a
slot file, a ledger) as JSON lines ([File formats](#file-formats)).

`internal/home` maps the checkout to its store before anything else a call does, the format
migration included ([plugin architecture](../plugin/architecture.md#the-home)). A hook or a read on
a checkout matching no store works on an id nothing holds yet and creates nothing; the first call
that writes creates the store, through `home.Resolver.Ensure`. Where the mapping is the user's call,
the store id is empty: a hook stays silent, SessionStart prints the question, every other call is
refused with it, and the MCP server, which resolves again on each call while unbound, starts
without migrating. `bind` and `merge` of two store ids run whatever the mapping says, but an
agent's reach only the stores the open question lists (`home.Question.Offers`) and the checkout's
own; a terminal on standard input (`hookio.Terminal`, asked for its termios settings, since
`/dev/null` is a character device too) with no owner process among the caller's ancestors
(`seat.UnderOwner`, the walk `seat.Owner` makes) lifts that, and the MCP server's off-listing
`bind` and `merge` refuse as the command line refuses an agent. Rejected: the terminal alone, which
an agent opens with `script`; a typed challenge, which an agent driving the pty reads and answers.

Before that, every call lets `internal/migration` move the layout of the releases before the home
into it, once for the machine ([plugin architecture](../plugin/architecture.md#the-migration)): an
old store under `~/.claude/agentic-memory/<key>/` moves whole into its store's `memory` folder and
leaves a stub holding only `{"format":999}`, which every release before the home refuses as a newer
store.

Every file above that is replaced rather than appended to (`format.json`, `seats.json`,
`shelves.json`, a claim, `.ledger`, `fold.gob`, `heat.json` and the cache's other JSON files) is written to
a temporary file named for that one writer, `.<name>.<random>.tmp` beside it, then renamed over it
(`internal/atomicfile`). The hooks of one event run in parallel, and a temporary name every writer
shares would let two of them interleave in it and the second rename publish the mix; a reader sees
the old file or the new one. Unlike the log, these files are not synced: a sync on darwin is a full
flush of the drive, paid inside hook calls.

## File formats

Store format 6 keeps what changes often in binary formats agentic-tools and shell-guard share:

- **Slot file** (`internal/slots`): what a session or seat keeps between hook calls, in one
  preallocated file per store, `sessions.slots`, rather than a file per session and purpose. A
  20-byte header (`AGSLOT\0`, format 1, the slot width, the slot count, its crc32c) heads fixed-width
  slots in A/B twin pairs. Each twin opens with a 32-byte head: a crc32c over the head and key, a
  crc32c of the value, a generation counter, the write time, the key's and value's lengths; then the
  key and value. A write goes with `pwrite` to the older twin of its key's pair at the next
  generation, so a torn write leaves the other twin whole; a read takes, with `pread`, the newer twin
  whose checksums hold, the older one where they do not. Clearing a key (a SessionEnd, or the
  SessionStart pass reclaiming state older than `staleAfter`) frees its pair for the next key, so the
  file holds its header's count of slots of its width and nothing more. A file out of free pairs is
  written anew with twice the pairs it needs and renamed over the old one. Writers hold
  `sessions.slots.lock`; readers take no lock. Keys are `<purpose>/<session or seat id>`: `shown`,
  `ladder`, `graph-seen`, `fork`, `waiter`, `migration-failed` per session; `traps`, `do-now`,
  `skipped`, `brief`, `seen` per seat; `graph-names/<yyyy>/<mm>` per month. A value is the JSON the
  per-session file held.
- **Journal** (`internal/memorylog`): the log's tail, `log/tail.jnl`, is the 8-byte header
  `AGMJNL\0` and format 1, then length-prefixed records, each a length, a crc32c over the length and
  the record, a kind byte, a schema byte and the body, appended under the log's lock. A reader skips a
  kind or schema it does not know and stops at the first record whose length runs past the file or
  whose checksum fails; the next locked write cuts the tail back to the last whole record. An event's
  body is its kind, its time in Unix nanoseconds, its entry, seat and target, then its payload. The
  sealed segments keep the `AGMSEG` codec.
- **Size ledger** (`internal/space`): `.ledger` in a component's directory, a journal of one record
  per data file below it: its name, length, crc32c, modification time, whether it counts as stored
  or held, and the bytes it counts. Every write of the component rewrites it under `.ledger.lock`,
  carrying the checksum of a file whose length and time are unchanged, so a figure reads the ledger
  and never walks the files. Lock files, temp files and links are no data files.

The seen list of the capture notice is a bitset over the store's table of the month's entry names
(`graph-names/<yyyy>/<mm>`, appended under the slot file's writer lock, so two sessions never give
two names one position), not the JSON list of names. On `BenchmarkCaptureNotice` over a month of
2000 entries (Apple M4 Pro), a tool call finding the month unchanged took 19 µs, 4 KB and 24
allocations with the bitset and 125 µs, 209 KB and 2035 with the names; a call finding an entry
added took about 4.5 ms either way, since both then load the store. A table past its slot's width
leaves the names it has no room for unnamed. The tables of months before the last go with the
SessionStart pass.

The migration to format 6 runs under the store lock and writes `format.json` last: it seals the
JSON-lines tail an earlier release kept, `log/tail.jsonl`, into a segment, takes every per-session
and per-seat file of the cache (`shown/`, `ladder/`, `traps/`, `briefs/`) into `sessions.slots`,
refreshes the ledger, then removes those folders and the per-process markers (`forks/`, `waiters/`,
`migration-failed/`, `graph-seen/`); a mark lost there costs a session one capture notice. Prompt
vectors and the attachments' ledger move at the next SessionStart pass. A release reading up to
format 5 refuses a format-6 store as newer, and this release refuses one past 6. Rejected: a
database (a dependency, and a server or a CGO build for what a fixed-width file does); one file per
session in a binary codec (the file count and the directory walks stay); keeping JSON-lines files
(a torn line reads as garbage with no checksum to tell).

## Seat ids

A seat has an id, `s-` and 6 random characters from `0-9a-z`, minted the first time its name is
claimed and never changed. Events, claim files, slices and the slot file's seat keys carry the id;
`seats.json` maps each id to the seat's one current name, and no two seats share a name. A name
reaches the store only through the registry: the log folds with every id turned into the name the
seat has now, so a `rename` shows everywhere at once. No seat may be named in an id's shape.
`seats.json` is written under the store's lock (`.lock` in the store root), through a temporary
file renamed into place. A seat's `"private": true` lives there too, beside its name, so the mark
outlasts every claim file: `who --all` reads each other store's registry and drops its private seats
before counting or listing any.

## Shelves

A shelf has an id, `h-` and 6 random characters from `0-9a-z`, and `shelves.json` maps it to its
name, the topics its seats follow, and its lifetime. Each `seats.json` entry names its shelf
(`"shelf": "<shelf id>"`), fixed when the seat is minted. An entry's shelf is its filing seat's, so
the log names no shelf and gains no event kind: a `new` row's seat key leads to the seat, and the
seat to its shelf. A `--for` stores shelf ids; one an earlier release stored as a seat id stands for
that seat's shelf. Both files are written under the store lock, through a temporary file renamed
into place, and a seat and a shelf minted together are minted under one hold of it.

## The format and its migration

`format.json` holds the store's format, `{"format": 5}`; a store without one is format 1, where
events and seat files name the seat, format 2 keeps seats by id without shelves, format 3 files
entries with a retention level and no kind, and format 4 holds no `item` rows. The first call
that meets an older store migrates it a step at a time, holding the store lock from the first step
to the last, so another call waits and then reads the migrated store. From format 1:

1. copy `log/` and `seats/` to `backup/format-1-<UTC stamp>/`;
2. mint an id for every seat name an event or a seat file mentions;
3. write a new log beside the old one, every event's seat replaced by its id;
4. rename every seat file to its id, swap the new log in, write `seats.json`;
5. write `format.json` last: its presence marks the migration done.

From format 2, the log untouched:

1. copy `seats.json` to `backup/format-2-<UTC stamp>/`;
2. give every seat a shelf of its own under its name, its follows moved there;
3. write `shelves.json`, then `seats.json`, then `format.json`.

From format 3:

1. copy `log/` to `backup/format-3-<UTC stamp>/`;
2. run every migration pass over the log's events: the kind pass makes every entry whose decay in
   force (its filed retention as later `retain` events left it) is `standing` a `rule`, keeping its
   source, and every other a `note`, keeping its filed retention as a retain override, so no entry
   moves in any brief; the credential pass scans every entry still holding its body and keeps what
   it finds, by filing seat, in `credentials-found.json` in the store, which each seat's next brief
   reads and empties of its own;
3. build the migrated log as `log.migrating`, set the old one aside as `log.format-3`, swap the new one
   in, write `format.json`, and remove `log.format-3`. A log no pass changed stays as it is, and only
   `format.json` is written.

From format 4, the log untouched: copy `log/` to `backup/format-4-<UTC stamp>/`, then write
`format.json`. Format 5 adds the `item` event kind a todolist is kept in, which a format-4 binary
cannot decode, so the format keeps such a binary off the store.

A failure before `format.json` undoes every step, says
`agentic-memory: migrating the store failed: <cause>; the store is unchanged and the next call tries again`
once per session, and the call runs on the store at the format it had. A store of a newer format than
the release knows is refused: `agentic-memory: this store has format <n>; this release reads up to
<m>`, with hooks silent and nothing written; a binary of an earlier release refuses a format-5 store
that way. A store created by this release starts at the current format. The log's manifest no longer
carries a format number; one that does still reads.

Which store a checkout works on is `internal/home`'s answer: the pointer in its git common
directory, else the identifiers the stores' `identity.json` record, so every worktree of a
repository shares one store, and a code repository shares its graph repository's. A directory counts
as inside git when `git rev-parse --is-inside-work-tree` says `true`; a `.git` directory and a bare
repository do not. Outside git, with `memory_require_git` set to `false`, the store is found by the
directory's absolute path.

## What a call asks git

A process asks git for the main checkout once per directory (`project.Remembered`) and reads the
graph's `.sdd/config.yaml` once, however many links, end conditions or language reads ask for them,
so a read spawns the same subprocesses whatever the number of links the store holds. The
caller's own checkout (whether it is a work tree, its top level, its HEAD commit and its branch) is
one `git rev-parse --is-inside-work-tree --show-toplevel HEAD --abbrev-ref HEAD` per call, asked
again by each MCP tool call and each look of the idle waiter, since a branch can change while either
runs. `--short` would make git verify a single revision and refuse the branch beside it, so the
commit is the full one cut to eight characters. A checkout without a commit fails that one call and
is asked question by question.

## Read caches

A read reuses what an earlier call worked out, from two files in the cache, and checks each against
what it stands for first. Any sandboxed job can write the cache as it can the store, so a key that
does not match costs the work again, never a wrong answer.

- **`fold.gob`, the folded log.** Keyed by the stamp (name, size, modification time) of every sealed
  segment, the number of tail rows it took in with a digest of every field of each, a digest of
  `seats.json` (a fold carries the seat names), the zone its stamps were printed in, and the release.
  `memorylog.Log.Unsealed` reads the manifest, stats the segments and reads the tail without mapping
  a segment; where only the tail grew, the call folds the rows appended since onto the cached fold
  and keeps the result. A second read of an unchanged store reads no segment and folds no row; any
  other difference, a seal or a merge included, folds the whole log again.
- **`anchor-hashes.json`, anchored files' content hashes**, keyed by absolute path, size and
  modification time. A file whose stamp moved is hashed again. `returns` checks that a file can still
  be opened before it trusts a cached hash, so a file no read could open reads as unchecked.

What a read shows is worked out only for the entries it shows or a filter asks about: an entry's link
flags, and with them the graph's statuses, the first time a line, a JSON entry or `retire?` asks for
them; who links in, which needs no file, for every entry. A section cut by `n` keeps its best `skip`
plus `n` entries in a bounded heap rather than sorting all it kept.

## The vector store

`internal/vectorseg` keeps every entry's chunk vectors in immutable segment files under
`<cache>/stores/<id>/memory/<sha256(fingerprint)[:12]>/segments/`, one store per embedder, so
a changed embedder starts a fresh store instead of mixing two vector spaces. The layout follows sdd's
search segments:

```
header   magic agvecseg, format, dims, row count, chunk count, table length
table    per entry: id, content hash, chunk count (uvarint lengths and count)
padding  zeros up to a 64-byte boundary
vectors  chunk count * dims little-endian float32, an entry's chunks contiguous, in table order
```

- **No manifest.** The fold says which rows count: an entry's row whose hash is the entry's current
  content hash (sha256 of title and body, 16 hex digits), or any row of an entry whose body was
  dropped, since nothing embeds it again. A row of older content is dead without anything recording
  it.
- **A search** maps every segment read-only and shared, and scans the mapped vector block in place,
  decoding and copying no vector: each counted entry scores by its best chunk, its first counted row
  deciding. A long-running process, the MCP server, maps a segment once and keeps it across calls.
  A merge that drops a segment between a search's listing and its open sends the search to list
  again.
- **A write** embeds only the live entries no segment holds for their current content and writes
  them as one segment, through a temporary `.segment-*` file renamed to the first 16 hex digits of
  the content's sha256: its cost follows the rows written, never the store.
- **A merge** runs after every write, under `segments.lock`, where every temporary file is one a
  killed writer left, and is deleted. A segment with no counted row is deleted; one whose dead rows
  outnumber its live ones is rewritten alone; and size classes, each 4 times the one below, merge
  once one holds 4 segments, the result taking its own class. Each class then holds at most 3
  segments, and a row is rewritten a logarithmic number of times over its life. The merged segment
  is written before its sources go, so a crash between the two leaves copies a search counts once.
- **A mapping outlives the deletion of its file**, and a segment is unmapped only once the last
  search holding it closes, so no search reads a file that shrinks. A fault in a mapping (its file
  truncated by another program) is `vectorseg.ErrFault`, not a crash.
- **Segments are not synced**, as the cache's other files are not. A segment a crash left torn fails
  its size check, is skipped by a search and deleted by the next merge, and its entries are embedded
  again.
- **The whole-file `vectors.gob`** an older release kept beside `segments/` is read once, written as
  one segment and deleted, so no entry it held is embedded again.

Any process that writes the cache can plant vectors that skew ranking. A planted row never puts an
entry in a result that the fold does not hold.

## Times

Every time the store and its cache keep in a JSON file is an RFC 3339 string in UTC, ending in `Z`,
with the precision the value carries: a log row's `time`, a claim's `claimed` and `started`, the
cache's heat, brief marks and nudge ladders, and the context reading. RFC 3339 reads as a date, and
UTC keeps every string in time order when sorted as text and never repeats an hour when the clocks
go back. The binary segments keep their little-endian Unix nanoseconds. A log row an earlier release
wrote with Unix nanoseconds reads as that. Printed stamps turn every time local, with the zone's
abbreviation.

## Heat

`heat.json` in the cache keeps, per entry, the heat as last set (`value`), when (`at`), and when a
use last warmed it (`used`). Heat halves per retention half-life from `at`; an entry with no record
reads as 1.0 from its filing. Filing writes a record with no `used`; `confirm`, `show`, `link` to a
memory entry and a search's top three hits add heat and set `used`, and nothing that only displays an
entry writes the file. `who` counts a seat's used entries from `used`, so a lost cache reads as no
use, as it reads as no heat.

## The log

Events append to `log/tail.jnl`, a journal ([File formats](#file-formats)). A call changing several entries (`supersede`, `confirm`,
`retain` and `retire` over several ids, `new --absorbs`) checks every id against one fold of the log
and writes all its events in one append under the log's lock, or none: a refused batch leaves the
log byte-identical. The tail seals past 64 KiB into an immutable segment, named for
its content hash, and eight segments merge into one. A merge bounds what history it keeps: every
event older than 14 days, the revive window, folds into the shortest run of existing event kinds
that rebuilds its entry (one `new` row carrying the title, topics, kind and retain override as they
stood, the latest `confirm`, and the `retire` or `supersede` still standing), so the log grows with
the entries it holds rather than with every change ever made. Topic merges and every younger event
stay as they are. An `untag` row carries every name the entry was filed or tagged with that reads as
a removed topic, so folding it needs no topic merge, and past the horizon it folds into the `new`
row like any other.

A `state` row names a seat and no entry, and carries the seat's state slot: `body`, `session`,
`branch`, `commit`, its time the row's. The newest `state` row of a seat is its slot. A merge keeps
only each seat's newest `state` row, and a purge drops the `state` rows of the seats it purges. The
`untag`, `state`, `rekind` and `settle` rows are kinds binaries before format 4 do not read; the format bump
keeps those binaries off the store. The same merge drops the body of an entry
superseded more than 14 days earlier, or retired more than `retired_body_days` earlier; the entry
stays in search, flagged, and `revive` refuses it.

## Seats and liveness

A claim file, under the seat's id, names the holding session, the pid of its `claude` process, that process's start time
(`started`, RFC 3339 in UTC), when it was claimed, and the session's transcript. A `release` or a
SessionEnd closing the seat moves the claim to `<id>.last.json` with `closed`, the time it closed.
A claim records `commit`, HEAD when it was taken, which a hook-written draft of the seat's state
counts the session's commits from. A claim made with `--session` for a session holding no seat is a reservation: `reserved` holds its
time and `pid` is 0; any hook whose payload carries that session id, run by a process holding no
seat, writes its owning `claude` process, start time and transcript into it. A handover writes the
new seat's claim from the old one, process and all, with `moved_from` naming the old seat until the
session's next hook reports it. A call finds its own
seat by walking up the process tree to the nearest `claude` process.

`fork_uuid` is the uuid a fork of the holder's session would start its transcript with. A fork
names no parent and runs under a session id of its own, but copies the parent's records with their
uuids unchanged from the parent's latest compaction boundary on, or from its first message where it
never compacted; a resume continues the same transcript under the same session id. Every hook that
writes a transcript into a claim, and every hook finding `fork_uuid` empty, sets it to the uuid of
the transcript's latest `compactMetadata` record, else its first record carrying a uuid. A
compaction's SessionStart empties it, since the boundary record lands in the transcript after that
hook runs, so the session's next hook reads it afresh. A SessionStart in a process holding no seat
looks through every claim and last claim for its parent: one naming its session id, else one whose
`fork_uuid` is the uuid its own transcript starts with, the latest claimed among equals. A parent
whose holder is gone is claimed as a seat is; one whose holder runs is left to it, and a new seat is
claimed on its shelf. A claim from a release without `fork_uuid` matches by session id alone.

Claude Code writes a fork's transcript only after its first prompt's UserPromptSubmit hooks return,
and the hook input names no parent session. A SessionStart with source `fork` that matches nothing
while its transcript holds no record therefore prints nothing and leaves a marker, the slot `fork/<session
id>` in the project's slot file. Every later UserPromptSubmit and PostToolUse of a seatless
session carrying the marker reads the transcript's first record. While there is none, the marker
stays and the hook says nothing about it. Once there is one, the hook removes the marker and runs the
same match: a match prints the claim's output as the hook's context (the prompt's output, or
PostToolUse `additionalContext`), no match the seatless notice, once. A session that claimed a seat
itself has its marker removed at the next of those hooks, unmatched. Stop and SessionEnd leave the
marker alone; one no hook resolved goes with the cache sweep after `staleAfter`. Rejected: retrying
the match at every seatless hook without a marker (sessions meant to stay seatless would read their
transcript head on every hook), seating at the first turn's Stop (a seat the agent would hold for a
turn without knowing it) or holding that Stop with the claim text (one more hidden turn per fork),
matching from the `claude` process's `--resume <id>` argument (a `--continue --fork-session` names
no id), a detached waiter reserving the seat with `claim --session` (a process, a timeout and a race
per fork), and waiting inside a hook (the transcript is written only once the hook returns).

Every change to a claim file reads, judges and writes it under the seats lock (`seats/.lock`): a
claim, a reservation and its takeover, a handover, a hook re-pointing a claim at a new session id or
transcript, a release, a SessionEnd close, and `who` freeing a gone holder. Two sessions judging one
free seat apart would both write it and both be told they hold it; under the lock the second finds
the first's claim and is refused. A hook's rewrite and `who`'s sweep change a claim only while it
still names the holder they judged, so a claim written since stays. The lock is never held while
the store's lock is taken: a seat is minted before its claim is judged. The memory MCP server reads
`seats.json` and `shelves.json` afresh on every call, since other sessions mint, rename and shelve
seats while it runs; a registry kept from an earlier call would judge a seat minted since as having
no claim and overwrite its holder's.

A holder is alive while its pid runs `claude` and that process started at the recorded instant, so a
reused pid never passes. A process on its way out counts as gone: on darwin a zombie or one inside
exit (`P_WEXIT`, state `E` in `ps`), on Linux state `Z` or `X`. Such a husk keeps its pid and name
until its parent reaps it, which may never happen when the parent is a husk too, and it runs no
session any more. The process table is read from the kernel, never through a `ps` spawn:
`sysctl kern.proc.pid` on darwin (parent pid, name and start time in one call), and on Linux
`/proc/<pid>/stat` with the boot time from `/proc/stat`, ticks counted at 100 a second. The name is
the kernel's, truncated to 16 bytes, which `claude` fits. Start times are whole seconds, the same
instant earlier releases read from `ps -o lstart=`, so their claims still compare. A claim written
by an earlier release may hold `ps` text: in the C locale it reads back as an instant; in another
locale it cannot, so such a claim is judged by the process alone, and its holder's next claim
rewrites it.

## The purge

A shelf's lifetime is `lifetime` in `shelves.json`: none for durable, `temporary` for the default
retention, or a span. A temporary shelf is due once no seat on it is in use and its retention has
passed since the latest close among its seats: a last claim's `closed`, else the modification time
of the transcript a dead holder's claim names, else the time it was claimed. A purge, under the
store lock, decides the entries the shelf filed that no `--for` names another shelf for, rewrites
the log through `memorylog.Log.Rewrite` (the tail sealed, every segment merged) with the merge
policy run over every row but theirs, removes the seats' claim, last-claim and slice files, marks the
seats `expired` in `seats.json` while a remaining row names them and deletes them otherwise, and
deletes the shelves from `shelves.json`. An expired seat's name is free: `IDOf` passes over it, and a
line names it `<name> (expired)`.

SessionStart runs the purge over every store at the current format in the home, the caller's own
included, before it looks at its own project; a `release` and a closing SessionEnd run it over their
own store. The same pass deletes every unclaimed store whose `orphan_store_grace` has passed
(`home.Resolver.SweepUnclaimed`).

## The context reading

The nudge reads how full the context is from `<data>/sessions/claude/<session>/context.json`,
`{"used_percentage": <n>, "size": <window>, "compact_window": <tokens>, "at": "<RFC 3339 UTC>"}`; a
numeric `at`, as a plugin of an earlier release writes it, still reads as Unix seconds. Claude Code
hands that figure to a status line and to a mod, never to a command hook, so the plugin carries a
hooks module, `plugin/hooks/register.ts`, which writes the file on `session.measure` after every turn
wherever `<data>/stores` exists, locating `<data>` as `internal/home` does. A Claude Code too old to load mods writes no reading, and
the nudge falls back to transcript growth.

`used_percentage` is a share of the model's whole window (`size`), while Claude Code compacts at a
window it may set smaller: from `CLAUDE_CODE_AUTO_COMPACT_WINDOW`, the `--autocompact` flag,
`modelSettings[<model>].autoCompactWindow` or a top-level `autoCompactWindow` in settings, the
account or an experiment. The module asks Claude Code for the window it settled,
`$.session.usage({ breakdown: 'summary' })`, a breakdown estimated locally with no request, and
writes its `rawMaxTokens` as `compact_window`. The nudge rescales the reading to `compact_window`
where it is smaller than `size`; a reading without it (a Claude Code lacking the call, or a plugin of
an earlier release) is rescaled to `CLAUDE_CODE_AUTO_COMPACT_WINDOW` where set, else read as it is.

A session that ends takes its reading with it, at SessionEnd. Every SessionStart, in any project,
removes the readings older than the nudge reads (`sensorMaxAge`, five minutes): a session that ended
without SessionEnd leaves none behind, and a running one writes its own again after its next turn.

## Hooks

Every hook exits at once in a project without a store.

| Event | What memory does |
|---|---|
| SessionStart (`session-start`) | purges every store's due temporary shelves, the stale context readings and the unclaimed stores past their grace; prints the store question where one stands on the checkout; for a seated session, points the claim at the session and transcript, and after a compaction or `/clear` prints the brief; for a seatless one, frees the seats whose holders are gone, then takes over the seat of the session it resumes or forks, or a new seat on its shelf, else names the project's seats; a fork whose transcript is not written yet gets a marker and no notice |
| SessionStart (`session-log <n>`) | after a compaction, prints piece `<n>` of the turns since the seat's newest entry or state write; one hook per piece, since Claude Code inlines a hook's output only up to 10,000 characters and swaps anything longer for a file path. Pieces are 9,000 characters (`pieceChars`), at most five (`maxPieces`), the newest kept; where older ones were cut, the first piece's header names their count and the transcript's path |
| UserPromptSubmit (`prompt`) | for a fork carrying a marker, once its transcript is written, removes the marker and, where it holds no seat, seats it as SessionStart would, printing the claim's output or the seatless notice instead of anything else; otherwise adds up to three related entries, the nudge near compaction, the shipped-work request once work shipped since the seat's last state write (spent without a line where the nudge fires), and the replay nudge once the turns since the seat's newest entry or state write would no longer fit the pieces, once per newest entry or state write; the ladder records the time of the one it fired for (`replay_nudged_for`) and the transcript size at its last check (`replay_checked_size`), and the transcript is condensed only once it grew by `pieceChars` bytes since; the prompt's vector comes from, or goes to, the session's slot under `<cache>/prompt-vectors/`, which agentic-tools' offer shares; a prompt holding a credential is never embedded: recall scores entries by its words and the notice prints once |
| PostToolUse (`tool`) | for a fork carrying a marker, does what UserPromptSubmit does for it, the claim's output or the seatless notice as `additionalContext`; tells the seat about arrivals, graph gains and the traps the call touched, in one `additionalContext`, and settles the do-now items the call proves; where the log's tail and manifest are as they were at the seat's last look, two `stat` calls for arrivals, and one for the graph's directory of the current month |
| Stop (`stop`) | writes the seat's state draft where no agent-written state is newer than its claim; holds the turn while do-now items are pending, at most twice per moment; then, near compaction only, holds it once for a state write when work shipped since the seat's last one; then holds it with the backstop once the nudge has gone unanswered twice and the context crossed the line; every hold is Stop `additionalContext` with exit 0, which continues the turn as exit 2 would without the hook error exit 2 shows the user |
| Stop (`wait`, `asyncRewake`) | the idle waiter, below |
| SessionEnd (`session-end`) | removes the session's context reading; writes the seat's state draft as Stop does, keeps the session's last turns as its seat's slice, empties the session's prompt vector slot, and, for any reason but `clear` or `resume`, closes the seat and purges what is due |

## MCP answers

Every tool of the `memory` server answers a JSON object (`internal/mcpserver`): the value goes out
as `structuredContent` and, serialized, as the text block MCP asks of a structured result for
clients reading only text. A read answers what its `--json` builds; any other command its text as
`message` beside a fields struct, the `with` a command's result carries (`mcpanswers.go`). A refusal
answers `{"error": <text>}` as an error result.

No tool lists an `outputSchema`. The listing loads into every session's context, and the schemas,
derived by reflection from each answer's Go type, made up most of it, while an agent reads the keys
off the answer it gets. Keeping the schemas was rejected for that cost; a shorter hand-written
schema was rejected since it would drift from the answer. The shape stays checked: the test
harness derives each tool's schema from the very type the answer is built from
(`mcpserver.SchemaOf` and `mcpserver.MessageSchema`, following what `encoding/json` writes: a field
without `omitempty` or `omitzero` is required, a nil slice, map or pointer may be `null`, objects
stay open) and holds every MCP answer any test draws to it. The `tools` server still lists its
output schemas.

A command marked `cliOnly` in `commands.go` is no tool: its `mcpserver.Tool` carries `Unlisted`,
which keeps it off `tools/list` while a call naming it is still answered, with a refusal naming its
command line. Those are the commands an agent seldom runs, each costing every session its share of
the listing. Dropping the name outright was rejected: the server would answer an unknown tool with
a bare protocol error, where the refusal says where the command went.

## Links

A `new` row carries the ids its text named that resolved when it was filed (`text_links`) and the
ones `--link` named (`links`); `link` and `unlink` events add and drop explicit links, each kept with
the seat that made it. A graph id resolves where
`<yyyy>/<mm>/<dd>-<hhmmss>-<type>-<layer>-<suffix>.md` exists under the project's resolved
`graph_dir` (`.sdd/graph` where unset, the graph repository's where `project_dir` names one, as the
grounding gate resolves it), a memory id
where the folded log holds the entry. A merge keeps one `link` row per net explicit link with its
seat, dropping a link with its `unlink`, and drops a text link with the body it came from.

A graph target's state comes from `sdd export --dependencies all`, run when a read shows an entry carrying a
graph link, the first time its flags are asked for, reading `repos[].entries[].{id,status,status_by}` of every repo it carries: the local one keyed by
the bare id, each connected repo keyed by `<repo_id>:<id>` from `repos[].repo_id` (a connected repo with
no `repo_id` is skipped). Memory keeps no list of connected repos of its own: sdd's dependency
declaration decides what the export carries. The result is cached as `graph-status.json` in the cache
with the connected repos it carried, keyed by the count of `**/*.md` under the resolved `graph_dir`
and the newest modification time among them, and the same for each connected repo's cache, the clone
sdd keeps under `$XDG_CACHE_HOME/sdd/<repo_id>` (`~/.cache` where unset), so a `sdd repo sync` pulling
into it refreshes the flags. Memory runs no sync of its own. An export carrying no local entry is not
trusted. A bare graph id the checkout's graph lacks resolves through the connected repos' keys; a
qualified id naming the local `repo_id` is stored bare. Where the export fails or its shape no longer carries those fields,
flags fall back to whether the file exists, and the read says once
`agentic-memory: graph status unavailable (sdd export: <why>)`. Where the installed sdd lists no `export`
command in `sdd --help` (no stock release carries it), no export runs and the read says nothing:
flags fall back to the file quietly.

## Declared ends

A `new` row carries its `--until` conditions (`until`), each path relative to the work tree, and the
content hash of each `changed:` path at filing (`until_hashes`, empty where no file was there), and,
for `session`, the claim the filing seat held (`until_hold`: the seat id and the claim's `claimed`
stamp). Every
brief runs the end check over the live entries on the checking seat's shelf, rules aside, before it
counts them: `closed:` reads the graph statuses links read, cached the same way; `merged:` asks git
`rev-parse --verify --quiet refs/heads/<branch>` and `merge-base --is-ancestor refs/heads/<branch>
<into>`, `<into>` defaulting to `symbolic-ref --quiet refs/remotes/origin/HEAD`, else `main`, once
per branch per check; `claim` reads the filing seat's claim and last claim for a session other than
the filing one claimed after the filing; `session` reads the seat's claim file, ended where none is
there (a close, which records `closed`, or a sweep moved it to the last claim) or where its
`claimed` differs from `until_hold`'s, and stands through a `/clear`, which rewrites only the
claim's session id; `captured` reads the stamp a linked graph id carries. A
reached end takes the store lock, re-reads the log, and appends a `retire` row by the checking seat
with `because` `end reached: <condition>` for every entry still live. `ended since last brief` lists
the retires with that reason after the seat's previous brief mark; `retire?` is computed from the
link flags, the anchors and the `merged:` branches, and writes nothing. Its link count
(`inferringLinks` and `linkEnded` in `ends.go`, shared with `returns`) takes a todo's graph links
alone and every link of any other kind, and counts neither a `settled` target nor a memory target
superseded by the entry itself as ended. `link` to a graph id runs the check for that entry's
`captured` condition alone; `release` and the SessionEnd close run it for the released seat's
shelf, `session` conditions alone, after closing the claim.

`revive` reads the condition back from a retire whose `because` opens with `end reached: `
(`reachedEnd` in `ends.go`). For `session`, the `revive` row carries the reviving seat's current
claim as `until_hold`, which the fold puts in place of the filed one, and a merge folding that row
past the horizon writes it into the `new` row. For any other form, `revive` asks the end check's
question once more and refuses while the condition is met. A `revive` row from an earlier release
carries `{}` and changes the status alone.

The tool hook finds graph gains without reading a capture's output: it keeps, per session, the
graph directory of the current month (`<graph_dir>/<yyyy>/<mm>`, resolved once a month through the
graph resolver), that directory's modification time and size, and the entry files it held, as a
bitset in the slot `graph-seen/<session>` ([File formats](#file-formats)). A call `stat`s the directory; only a changed stamp lists
it, and an entry file not listed before is a gain. A session's first look records and says nothing.

## Todolists

An item is an `item` event on its todo, payload `{op, id, text, link, state, reason, from, order}`,
`op` one of add, check, uncheck, drop, remove and order. The fold keeps the todo's items in order,
removed ones included, each with the state an ack gave it and whether one did (an ack's state wins
over a linked target's). Ids are drawn from `crypto/rand` and checked against every item the todo
ever held. A carried item's add brings its state, reason and `from`, the `<old>#<id>` it came from,
which is how an old address resolves through a supersede.

A linked item's state is worked out at read, as a link flag is: the fold's `itemLinks`, installed
with the link flagger, follows a memory target's supersede chain through the log and a graph
target's through the export's statuses, at most 16 hops against a cycle. The end check counts a
todo's items like a declared end; a revive after the last item change keeps the todo live, since the
fold records when an item last changed and when the entry was last revived. The fold cache carries
both and the items, its layout moved up with them.

## Views

`internal/layout` parses sdd's view layout grammar and knows no vocabulary: sections, each a run of
functions, each argument a function or a quoted string, every part keeping where it starts in
characters. sdd's own parser lives in sdd's module, which no other module may import, and memory must
need no change to sdd; the grammar is small enough to own.

The vocabulary is one filter set (`internal/memory/filters.go`): every filter word is defined once
with its arity, its closed set of values or what a free value may be (the nearest-word candidates a
refusal draws from), its predicate, and the costly state it needs (links, the seen mark, files,
graph statuses, git). A layout compiles each section against it; `search`'s `--seat`, `--all-seats`,
`--status`, `--topic` and `--with-superseded`, and the `brief`'s `--kind` and `--topic`, compile to
the same filters through the same argument parsing, so a word means one thing wherever it is used.
A section runs its filters in one pass over the folded log, the cheap ones first, then ranks, skips
and cuts. A view reads `heat.json` for `rank(heat)` and `as-counts` and writes nothing.
`rank(in-degree)` and `expand(refs)` read the inbound and outbound links every load works out
(`annotateLinks`), so neither costs a read of its own; `as-grouped` blocks the page after the cut,
so `n` and `skip` count entries, not blocks.

The brief's rules, traps, todos and notes are the sections of a fixed layout (`briefLayout` in
`internal/memory/brief.go`), compiled and run like any other with the note limit as `n`; the
brief keeps around them what is no list of entries (its first line, the state slot, the changes,
the ended and inferred ends, the news, the topics), the character budget and the rules' third.
`brief --kind` and `brief --topic` are `active:mine` narrowed by `kind` or `topic`.

`inbox(<seat>)` keeps what `receives` (`internal/memory/shared.go`) says reaches the seat, the
function the arrival notice routes by, so a view and the notice cannot diverge; `unread` reads the
seat's seen mark, the slot `seen/<id>`, once per section and never writes it.

`returns` computes an entry's reasons once per read, the first time a filter or a section asks:
each anchor's file hashed against its hash at filing, each link's target from the folded log or the
graph entry's file and the cached graph statuses (`graph-status.json`, read only while its key
matches the graph; a view never runs `sdd export`), each declared end through the end check's probe,
`merged:` asking git once per branch, and the inferred ends `retire?` names. What the cache or a file
cannot answer is `unchecked`.

## Attachments

One attachment store serves every project on the machine: `<data>/attachments/`,
each file named for the SHA-256 of its original bytes, gzip-compressed as `<sha256>.gz` where that
shrinks it and raw otherwise, written through a temporary file renamed into place. A hash already
stored is left as it is, so one file attached to entries of several projects is stored once. An
`attach` row carries the entry, the file's base name, its hash, size and storage form; a merge keeps
it while its entry holds its body and drops it with the body.

Every SessionStart, after the purges, sweeps the store: each store at the current format writes the
hashes its entries still holding their body name to `<store>/attachments.refs` and refreshes its
ledger; a store this release cannot read keeps the refs it last wrote; then, under `attachments/.lock`, every stored file no refs
list names is removed. A `drop` sweeps the same way. A body counts as held until its drop is due by
time, so a file outlives a retire by `retired_body_days` and a supersede by 14 days whether or not a
merge has run.

### Space

`attachments_total_mb` counts every stored file once, across every project, on the size of its
original bytes. The figure comes from `attachments/.ledger`, the store's size ledger
([File formats](#file-formats)): each stored file's length and crc32c, and the size of its original
bytes as its attach recorded it, never a stat of the store: the brief and every attach reply print
it, and a walk over thousands of files is far too slow for either. An attach checks the ledger's
total, plus the project's own held files a ledger written before them may lack, against the limit,
then refreshes the ledger with its files' sizes under `attachments/.ledger.lock`. The SessionStart
sweep refreshes it with what every log at the current format records, dropping the files it removed,
so a total that drifted through a crash between a store and its record is right again by the next
session; a file only an unreadable store names keeps its recorded size, and a file no attach
recorded counts nothing. The JSON ledger an earlier release kept, `attachments/.space.json`, goes
with the first sweep.

The ledger and every log are writable by anything sandboxed, so neither may shrink the total. Where
the ledger and the project's own log both record a file, the larger size counts; the sweep never
writes a negative size the log records; and every total saturates rather than wraps. A forged
figure can still refuse attaches, never let one past the limit.

The brief's space line reads the same ledger, and the ledger of every tool store
([tools architecture](../tools/architecture.md#space)), and prints
only a store with under a tenth of its limit left. The same SessionStart pass, the tools piece on,
runs agentic-tools' pruning over every tool store on the machine (`tools.Prune`), so one pass visits
every store of both. Rejected: a `du` or stat walk per figure (too slow per call); a PreToolUse hook
announcing space before every write, or a read command for it (a hook message per write, or a call
the agent must think to make); the line in every brief (about 60 characters at every claim and
compaction that change nothing while space is plentiful).

## Tool-call triggers

A `new` row carries a trap's `when`, and a rule's `on` and `proof`, each trigger spelled
`<form>:<value>`. Globs are matched with `github.com/bmatcuk/doublestar/v4` (`doublestar.Match`),
since `path.Match` has no `**`; command patterns are RE2.

The tool hook keeps the seat's traps, its own and received, in the slot `traps/<seat id>`:
each trap's id, anchored paths and triggers, and each note or todo declaring `--about` paths
(`notes`, the paths as `new` stored them in the event's `about`), rebuilt whenever the hook finds the
log changed, and removed where the seat holds neither. The seat's seen mark records how many it held
then (`traps`), so a seat holding none reads nothing more on a tool call than it did before. A trap or
note told is added to the session's shown set (the slot `shown/<session>`, `traps` and `notes`), which a
compaction, `/clear` and a resume empty. The same slot holds what reads printed: `ids`, each entry
whose line a brief, recall or read printed, and `bodies`, each whose body `show` or a view printed,
both as `<id>@<digest>`, the digest over the entry's full line and its event count, so an entry that
changed since never matches. A read opens the set once for the seated session it runs in and writes
it back only where it marked something; a read inside another (the brief a claim prints) shares the
outer one's. `--again` bypasses the lookup and still marks. The index lists notes hottest first when built, but a `show` warms without a log
event, so the hook ranks the matching notes by the heat read at the call.

A `state` row the Stop or SessionEnd hook writes carries `draft: true`, the files the session
wrote (`wrote` on its ladder, each once, the first 20) and its last prompt (`last_prompt`, kept only
from a prompt holding no credential, and never from a turn Claude Code sends itself through
UserPromptSubmit: one opening with `<task-notification>`, `<local-command-caveat>`,
`<local-command-stdout>` or `<bash-stdout>`). The fold never lets a draft replace a non-draft state row of
the same session, since the hook reads the slot and appends apart and the agent's write can land
between; a merge keeps the state row the fold reads, not merely the newest. The point a seat's
replay and nudges count from skips a draft.

The shipped-work hold keeps its measure on the session's ladder: `ship_since`, when the window
began measuring, set at its first tool call; `ship_base`, HEAD then, replaced by the commit the
seat's next state write stamps; `state_seen`, that write's time; `writes`, the Edit, Write and
NotebookEdit calls since; `captured`, the graph entries the capture notice saw added since; and
`held`, set by whichever hook delivers the request and cleared with the counts by a state write newer
than `state_seen`. Every Stop and prompt compares the state slot with `state_seen`, delivering or
not, so counts reset at the first hook after a state write rather than at the next delivery. Each
reads HEAD with the one `rev-parse` the checkout asks. The ladder goes with the window at a
compaction or `/clear`, and the measure with it.

Do-now items pending for a seat sit beside the nudge ladder, in the slot `do-now/<seat id>`: the
session they are owed in, each item's rule, moment and proofs, and how often `Stop` held a turn per
moment. A claim from a shell may not know its session yet, so the slot is keyed by the seat and names
the session inside; items of another session read as skipped. Skipped items wait in the slot
`skipped/<seat id>` until the seat's next do-now list prints them. A settlement is a
`settle` row on the rule (`how`: `proof`, `ack` or `decline`, with `proof` or `because`).

## Arrivals

Each seat keeps a seen mark in the slot `seen/<id>`: the filing time of the newest
received entry it was told about, and how the log stood at its last look. The prompt hook, the tool
hook and the waiter each tell the seat about every received entry filed after the mark, then move
it, so an arrival is told once.

The waiter is the `Stop` hook `agentic-memory wait`, marked `asyncRewake`: Claude Code runs it in the
background once a turn ends, and its exit 2 with the arrival lines on stderr starts a new turn with
them. It looks every 5 seconds for up to 30 minutes. Each look first checks that the seat's claim
still names this session and that the session's `claude` process runs, and that no later turn's
waiter has taken its place (the slot `waiter/<session>` names the newest); otherwise it exits 0
waking nothing. A session holding no seat starts none. The waiter delivers its own lines and
nothing else, so a tool running its own `asyncRewake` waiter beside it keeps its deliveries apart.

agentic-memory never reads or writes a project's `.memory/` directory.
