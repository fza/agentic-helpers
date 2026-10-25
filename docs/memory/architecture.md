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
└── backup/format-<n>-<UTC stamp>/                      what a migration changed, as it was before
~/.claude/agentic-memory/sessions/<session>.ctx         the context reading the plugin writes
~/.cache/agentic-memory/<project>/                      vectors, heat, per-session state: rebuilt at will
~/.claude/agentic-helpers/<project>/writers.json        the checkouts that may use the store, shared with agentic-tools
```

Admission runs before anything else a call does, the format migration included: `internal/writers`
reads the project's list through checked folder descriptors, or a library's own legacy list while
the project's is missing, and a write claims an unlisted project for the calling checkout under the
list's own `.lock`; a read claims nothing. A hook of a checkout not listed exits silently; the
MCP server checks each call, and starts without migrating where its checkout is not listed. A call
on a project with no store yet, other than `claim` and `trust`, makes no list.

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

`format.json` holds the store's format, `{"format": 3}`; a store without one is format 1, where
events and seat files name the seat, and format 2 keeps seats by id without shelves. The first call
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

A failure before `format.json` undoes every step, says
`agentic-memory: migrating the store failed: <cause>; the store is unchanged and the next call tries again`
once per session, and the call runs on the format-1 store. A store of a newer format than the release
knows is refused: `agentic-memory: this store has format <n>; this release reads up to <m>`, with
hooks silent and nothing written. A store created by this release starts at the current format. The
log's manifest no longer carries a format number; one that does still reads.

`<project>` is the `repo_id` the project's `.sdd/config.yaml` declares, else `local/` and a hash of
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

## The log

Events append to `log/tail.jsonl`. A call changing several entries (`supersede`, `confirm`,
`retain` and `retire` over several ids, `new --absorbs`) checks every id against one fold of the log
and writes all its events in one append under the log's lock, or none: a refused batch leaves the
log byte-identical. The tail seals past 64 KiB into an immutable segment, named for
its content hash, and eight segments merge into one. A merge bounds what history it keeps: every
event older than 14 days, the revive window, folds into the shortest run of existing event kinds
that rebuilds its entry (one `new` row carrying the title, topics and retention as they stood, the
latest `confirm`, and the `retire` or `supersede` still standing), so the log grows with the entries
it holds rather than with every change ever made, and a binary of an earlier release still reads it.
Topic merges and every younger event stay as they are. The same merge drops the body of an entry
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
reused pid never passes. The start time comes from `ps -o lstart=` run under `LC_ALL=C`: `ps` prints
it in the caller's locale otherwise, and callers in two locales would read one process as two. A
claim written by an earlier release may hold `ps` text in another locale, which cannot be read back
into an instant; such a claim is judged by the process alone, and its holder's next claim rewrites
it.

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
| SessionStart (`session-log <n>`) | after a compaction, prints piece `<n>` of the turns since the seat's newest entry; one hook per piece, since Claude Code inlines a hook's output only up to 10,000 characters and swaps anything longer for a file path. Pieces are 9,000 characters (`pieceChars`), at most five (`maxPieces`), the newest kept; where older ones were cut, the first piece's header names their count and the transcript's path |
| UserPromptSubmit (`prompt`) | adds up to three related entries, the nudge near compaction, and the replay nudge once the turns since the seat's newest entry would no longer fit the pieces, once per newest entry; the ladder records the entry it fired for (`replay_nudged_for`) and the transcript size at its last check (`replay_checked_size`), and the transcript is condensed only once it grew by `pieceChars` bytes since; the prompt's vector comes from, or goes to, the session's slot under `~/.cache/agentic-helpers/prompt-vectors/`, which agentic-tools' offer shares |
| PostToolUse (`tool`) | tells the seat about arrivals as `additionalContext`; where the log's tail and manifest are as they were at the seat's last look, two `stat` calls and nothing more |
| Stop (`stop`) | blocks with the backstop once the nudge has gone unanswered twice and the context crossed the line |
| Stop (`wait`, `asyncRewake`) | the idle waiter, below |
| SessionEnd (`session-end`) | keeps the session's last turns as its seat's slice, empties the session's prompt vector slot, and, for any reason but `clear` or `resume`, closes the seat and purges what is due |

## Links

A `new` row carries the ids its text named that resolved when it was filed (`text_links`) and the
ones `--link` named (`links`); `link` and `unlink` events add and drop explicit links, each kept with
the seat that made it. A graph id resolves where
`.sdd/graph/<yyyy>/<mm>/<dd>-<hhmmss>-<type>-<layer>-<suffix>.md` exists in the checkout, a memory id
where the folded log holds the entry. A merge keeps one `link` row per net explicit link with its
seat, dropping a link with its `unlink`, and drops a text link with the body it came from.

A graph target's state comes from `sdd export --dependencies none`, run when a read carries a graph
link, reading `repos[].entries[].{id,status,status_by}` of the local repo. The result is cached as
`graph-status.json` in the cache, keyed by the count of `.sdd/graph/**/*.md` and the newest
modification time among them. Where the export fails or its shape no longer carries those fields,
flags fall back to whether the file exists, and the read says once
`agentic-memory: graph status unavailable (sdd export: <why>)`.

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
