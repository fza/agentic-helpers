# Memory: architecture

## Where it keeps what

```
~/.claude/agentic-memory/<project>/
├── format.json                                         the store's one format version
├── seats.json                                          seat id → its current name and follows
├── log/tail.jsonl, log/<hash>.seg, log/manifest.json   the append-only log: the truth
├── seats/<id>.json                                     who holds the seat, which process, which transcript
├── seats/<id>.last.json                                the claim of the seat's last holder
├── seats/<id>.slice                                    the last turns of the seat's last session
└── backup/format-1-<UTC stamp>/                        the log and seats as they were before a migration
~/.claude/agentic-memory/sessions/<session>.ctx         the context reading the plugin writes
~/.cache/agentic-memory/<project>/                      vectors, heat, per-session state: rebuilt at will
```

## Seat ids

A seat has an id, `s-` and 6 random characters from `0-9a-z`, minted the first time its name is
claimed and never changed. Events, claim files, slices and the cache's brief marks carry the id;
`seats.json` maps each id to the seat's one current name, and no two seats share a name. A name
reaches the store only through the registry: the log folds with every id turned into the name the
seat has now, so a `rename` shows everywhere at once. No seat may be named in an id's shape.
`seats.json` is written under the store's lock (`.lock` in the store root), through a temporary
file renamed into place.

## The format and its migration

`format.json` holds the store's format, `{"format": 2}`; a store without one is format 1, where
events and seat files name the seat. The first call that meets a format-1 store migrates it, holding
the store lock from the first step to the last, so another call waits and then reads the migrated
store:

1. copy `log/` and `seats/` to `backup/format-1-<UTC stamp>/`;
2. mint an id for every seat name an event or a seat file mentions;
3. write a new log beside the old one, every event's seat replaced by its id;
4. rename every seat file to its id, swap the new log in, write `seats.json`;
5. write `format.json` last: its presence marks the migration done.

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

Events append to `log/tail.jsonl`. The tail seals past 64 KiB into an immutable segment, named for
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
(`started`, RFC 3339 in UTC), when it was claimed, and the session's transcript. A call finds its own
seat by walking up the process tree to the nearest `claude` process.

A holder is alive while its pid runs `claude` and that process started at the recorded instant, so a
reused pid never passes. The start time comes from `ps -o lstart=` run under `LC_ALL=C`: `ps` prints
it in the caller's locale otherwise, and callers in two locales would read one process as two. A
claim written by an earlier release may hold `ps` text in another locale, which cannot be read back
into an instant; such a claim is judged by the process alone, and its holder's next claim rewrites
it.

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
| SessionStart (`session-start`) | for a seated session, points the claim at the session and transcript, and after a compaction or `/clear` prints the brief; for a seatless one, frees the seats whose holders are gone and names the project's seats |
| SessionStart (`session-log <n>`) | after a compaction, prints piece `<n>` of the turns since the seat's newest entry; one hook per piece, since Claude Code inlines a hook's output only up to 10,000 characters and swaps anything longer for a file path |
| UserPromptSubmit (`prompt`) | adds up to three related entries, and the nudge near compaction |
| PostToolUse (`tool`) | tells the seat about arrivals as `additionalContext`; where the log's tail and manifest are as they were at the seat's last look, two `stat` calls and nothing more |
| Stop (`stop`) | blocks with the backstop once the nudge has gone unanswered twice and the context crossed the line |
| Stop (`wait`, `asyncRewake`) | the idle waiter, below |
| SessionEnd (`session-end`) | keeps the session's last turns as its seat's slice |

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
