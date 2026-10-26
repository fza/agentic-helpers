# Memory: architecture

## Where it keeps what

```
~/.claude/agentic-memory/<project>/
├── format.json                                         the store's one format version
├── seats.json                                          seat id → its current name and its shelf
├── shelves.json                                        shelf id → its name, follows and lifetime
├── log/tail.jsonl, log/<hash>.seg, log/manifest.json   the append-only log: the truth
├── seats/<id>.json                                     who holds the seat, which process, which transcript
├── seats/<id>.last.json                                the claim of the seat's last holder
├── seats/<id>.slice                                    the last turns of the seat's last session
├── attachments.refs                                    the attachment hashes its entries still holding a body name
├── credentials-found.json                              what the migration scan found, until each seat's brief tells it
└── backup/format-<n>-<UTC stamp>/                      what a migration changed, as it was before
~/.claude/agentic-memory/sessions/<session>.ctx         the context reading the plugin writes
~/.claude/agentic-memory/attachments/<sha256>[.gz]      every project's attached files, once each
~/.cache/agentic-memory/<project>/                      vectors, heat, per-session state: rebuilt at will
~/.claude/agentic-helpers/<project>/writers.json        the checkouts that may use the store, shared with agentic-tools
```

Admission runs before anything else a call does, the format migration included: `internal/writers`
reads the project's list through checked folder descriptors, or a library's own legacy list while
the project's is missing, and a write claims an unlisted project for the calling checkout under the
list's own `.lock`; a read claims nothing. A hook of a checkout not listed exits silently; the
MCP server checks each call, and starts without migrating where its checkout is not listed. A call
on a project with no store yet, other than `claim` and `trust`, makes no list.

Every file above that is replaced rather than appended to (`format.json`, `seats.json`,
`shelves.json`, a claim, `vectors.gob`, `heat.json` and the cache's other JSON files) is written to
a temporary file named for that one writer, `.<name>.<random>.tmp` beside it, then renamed over it
(`internal/atomicfile`). The hooks of one event run in parallel, and a temporary name every writer
shares would let two of them interleave in it and the second rename publish the mix; a reader sees
the old file or the new one. Unlike the log, these files are not synced: a sync on darwin is a full
flush of the drive, paid inside hook calls.

## Seat ids

A seat has an id, `s-` and 6 random characters from `0-9a-z`, minted the first time its name is
claimed and never changed. Events, claim files, slices and the cache's brief marks carry the id;
`seats.json` maps each id to the seat's one current name, and no two seats share a name. A name
reaches the store only through the registry: the log folds with every id turned into the name the
seat has now, so a `rename` shows everywhere at once. No seat may be named in an id's shape.
`seats.json` is written under the store's lock (`.lock` in the store root), through a temporary
file renamed into place.

## Shelves

A shelf has an id, `h-` and 6 random characters from `0-9a-z`, and `shelves.json` maps it to its
name, the topics its seats follow, and its lifetime. Each `seats.json` entry names its shelf
(`"shelf": "<shelf id>"`), fixed when the seat is minted. An entry's shelf is its filing seat's, so
the log names no shelf and gains no event kind: a `new` row's seat key leads to the seat, and the
seat to its shelf. A `--for` stores shelf ids; one an earlier release stored as a seat id stands for
that seat's shelf. Both files are written under the store lock, through a temporary file renamed
into place, and a seat and a shelf minted together are minted under one hold of it.

## The format and its migration

`format.json` holds the store's format, `{"format": 4}`; a store without one is format 1, where
events and seat files name the seat, format 2 keeps seats by id without shelves, and format 3 files
entries with a retention level and no kind. The first call
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

A failure before `format.json` undoes every step, says
`agentic-memory: migrating the store failed: <cause>; the store is unchanged and the next call tries again`
once per session, and the call runs on the store at the format it had. A store of a newer format than
the release knows is refused: `agentic-memory: this store has format <n>; this release reads up to
<m>`, with hooks silent and nothing written; a binary of an earlier release refuses a format-4 store
that way. A store created by this release starts at the current format. The log's manifest no longer
carries a format number; one that does still reads.

`<project>` is the `repo_id` the project's `.sdd/config.yaml` declares (the graph repository's,
where `project_dir` names one, so a code repository and its graph repository share one store), else `local/` and a hash of
its git common directory, the key sdd gives its own search index, so every worktree of a repository
shares one store. A directory counts as inside git when `git rev-parse --is-inside-work-tree` says
`true`; a `.git` directory and a bare repository do not. Outside git there is no project key; with
`memory_require_git` set to `false`, the store is keyed `local/` and a hash of the directory's
absolute path.

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

Events append to `log/tail.jsonl`. A call changing several entries (`supersede`, `confirm`,
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
A claim made with `--session` for a session holding no seat is a reservation: `reserved` holds its
time and `pid` is 0; any hook whose payload carries that session id, run by a process holding no
seat, writes its owning `claude` process, start time and transcript into it. A handover writes the
new seat's claim from the old one, process and all, with `moved_from` naming the old seat until the
session's next hook reports it. A call finds its own
seat by walking up the process tree to the nearest `claude` process.

A holder is alive while its pid runs `claude` and that process started at the recorded instant, so a
reused pid never passes. The process table is read from the kernel, never through a `ps` spawn:
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

SessionStart runs the purge over every store at the current format under
`~/.claude/agentic-memory/`, the caller's own included, before it looks at its own project; a
`release` and a closing SessionEnd run it over their own store.

## The context reading

The nudge reads how full the context is from `~/.claude/agentic-memory/sessions/<session>.ctx`,
`{"used_percentage": <n>, "size": <window>, "at": "<RFC 3339 UTC>"}`; a numeric `at`, as a plugin of
an earlier release writes it, still reads as Unix seconds. Claude Code hands that figure to
a status line and to a mod, never to a command hook, so the plugin carries a hooks module,
`plugin/hooks/register.ts`, which writes the file on `session.measure` after every turn wherever
`~/.claude/agentic-memory` exists. A Claude Code too old to load mods writes no reading, and the
nudge falls back to transcript growth.

## Hooks

Every hook exits at once in a project without a store.

| Event | What memory does |
|---|---|
| SessionStart (`session-start`) | purges every store's due temporary shelves; for a seated session, points the claim at the session and transcript, and after a compaction or `/clear` prints the brief; for a seatless one, frees the seats whose holders are gone and names the project's seats |
| SessionStart (`session-log <n>`) | after a compaction, prints piece `<n>` of the turns since the seat's newest entry or state write; one hook per piece, since Claude Code inlines a hook's output only up to 10,000 characters and swaps anything longer for a file path. Pieces are 9,000 characters (`pieceChars`), at most five (`maxPieces`), the newest kept; where older ones were cut, the first piece's header names their count and the transcript's path |
| UserPromptSubmit (`prompt`) | adds up to three related entries, the nudge near compaction, and the replay nudge once the turns since the seat's newest entry or state write would no longer fit the pieces, once per newest entry or state write; the ladder records the time of the one it fired for (`replay_nudged_for`) and the transcript size at its last check (`replay_checked_size`), and the transcript is condensed only once it grew by `pieceChars` bytes since; the prompt's vector comes from, or goes to, the session's slot under `~/.cache/agentic-helpers/prompt-vectors/`, which agentic-tools' offer shares; a prompt holding a credential is never embedded: recall scores entries by its words and the notice prints once |
| PostToolUse (`tool`) | tells the seat about arrivals, graph gains and the traps the call touched, in one `additionalContext`, and settles the do-now items the call proves; where the log's tail and manifest are as they were at the seat's last look, two `stat` calls for arrivals, and one for the graph's directory of the current month |
| Stop (`stop`) | holds the turn while do-now items are pending, at most twice per moment; then blocks with the backstop once the nudge has gone unanswered twice and the context crossed the line |
| Stop (`wait`, `asyncRewake`) | the idle waiter, below |
| SessionEnd (`session-end`) | keeps the session's last turns as its seat's slice, empties the session's prompt vector slot, and, for any reason but `clear` or `resume`, closes the seat and purges what is due |

## Links

A `new` row carries the ids its text named that resolved when it was filed (`text_links`) and the
ones `--link` named (`links`); `link` and `unlink` events add and drop explicit links, each kept with
the seat that made it. A graph id resolves where
`<yyyy>/<mm>/<dd>-<hhmmss>-<type>-<layer>-<suffix>.md` exists under the project's resolved
`graph_dir` (`.sdd/graph` where unset, the graph repository's where `project_dir` names one, as the
grounding gate resolves it), a memory id
where the folded log holds the entry. A merge keeps one `link` row per net explicit link with its
seat, dropping a link with its `unlink`, and drops a text link with the body it came from.

A graph target's state comes from `sdd export --dependencies all`, run when a read carries a graph
link, reading `repos[].entries[].{id,status,status_by}` of every repo it carries: the local one keyed by
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
content hash of each `changed:` path at filing (`until_hashes`, empty where no file was there). Every
brief runs the end check over the live entries on the checking seat's shelf, rules aside, before it
counts them: `closed:` reads the graph statuses links read, cached the same way; `merged:` asks git
`rev-parse --verify --quiet refs/heads/<branch>` and `merge-base --is-ancestor refs/heads/<branch>
<into>`, `<into>` defaulting to `symbolic-ref --quiet refs/remotes/origin/HEAD`, else `main`, once
per branch per check; `claim` reads the filing seat's claim and last claim for a session other than
the filing one claimed after the filing; `captured` reads the stamp a linked graph id carries. A
reached end takes the store lock, re-reads the log, and appends a `retire` row by the checking seat
with `because` `end reached: <condition>` for every entry still live. `ended since last brief` lists
the retires with that reason after the seat's previous brief mark; `retire?` is computed from the
link flags, the anchors and the `merged:` branches, and writes nothing. `link` to a graph id runs the
check for that entry's `captured` condition alone.

The tool hook finds graph gains without reading a capture's output: it keeps, per session, the
graph directory of the current month (`<graph_dir>/<yyyy>/<mm>`, resolved once a month through the
graph resolver), that directory's modification time and size, and the entry files it held, in
`graph-seen/<session>.json` in the cache. A call `stat`s the directory; only a changed stamp lists
it, and an entry file not listed before is a gain. A session's first look records and says nothing.

## Attachments

One attachment store serves every project on the machine: `~/.claude/agentic-memory/attachments/`,
each file named for the SHA-256 of its original bytes, gzip-compressed as `<sha256>.gz` where that
shrinks it and raw otherwise, written through a temporary file renamed into place. A hash already
stored is left as it is, so one file attached to entries of several projects is stored once. An
`attach` row carries the entry, the file's base name, its hash, size and storage form; a merge keeps
it while its entry holds its body and drops it with the body.

Every SessionStart, after the purges, sweeps the store: each store at the current format writes the
hashes its entries still holding their body name to `<store>/attachments.refs`; a store this release
cannot read keeps the refs it last wrote; then, under `attachments/.lock`, every stored file no refs
list names is removed. A `drop` sweeps the same way. A body counts as held until its drop is due by
time, so a file outlives a retire by `retired_body_days` and a supersede by 14 days whether or not a
merge has run.

## Tool-call triggers

A `new` row carries a trap's `when`, and a rule's `on` and `proof`, each trigger spelled
`<form>:<value>`. Globs are matched with `github.com/bmatcuk/doublestar/v4` (`doublestar.Match`),
since `path.Match` has no `**`; command patterns are RE2.

The tool hook keeps the seat's traps, its own and received, in `traps/<seat id>.json` in the cache:
each trap's id, anchored paths and triggers, rebuilt whenever the hook finds the log changed, and
removed where the seat holds none. The seat's seen mark records how many traps it held then
(`traps`), so a seat holding none reads nothing more on a tool call than it did before. A trap told
is added to the session's shown set (`shown/<session>.json`), which a compaction empties.

Do-now items pending for a seat sit beside the nudge ladder, `ladder/<seat id>.do-now.json`: the
session they are owed in, each item's rule, moment and proofs, and how often `Stop` held a turn per
moment. A claim from a shell may not know its session yet, so the file is keyed by the seat and names
the session inside; items of another session read as skipped. Skipped items wait in
`briefs/<seat id>.skipped.json` until the seat's next do-now list prints them. A settlement is a
`settle` row on the rule (`how`: `proof`, `ack` or `decline`, with `proof` or `because`).

## Arrivals

Each seat keeps a seen mark in the cache, `briefs/<id>.seen.json`: the filing time of the newest
received entry it was told about, and how the log stood at its last look. The prompt hook, the tool
hook and the waiter each tell the seat about every received entry filed after the mark, then move
it, so an arrival is told once.

The waiter is the `Stop` hook `agentic-memory wait`, marked `asyncRewake`: Claude Code runs it in the
background once a turn ends, and its exit 2 with the arrival lines on stderr starts a new turn with
them. It looks every 5 seconds for up to 30 minutes. Each look first checks that the seat's claim
still names this session and that the session's `claude` process runs, and that no later turn's
waiter has taken its place (`waiters/<session>` in the cache names the newest); otherwise it exits 0
waking nothing. A session holding no seat starts none. The waiter delivers its own lines and
nothing else, so a tool running its own `asyncRewake` waiter beside it keeps its deliveries apart.

agentic-memory never reads or writes a project's `.memory/` directory.
