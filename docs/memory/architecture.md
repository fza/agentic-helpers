# Memory: architecture

## Where it keeps what

```
<data>/stores/<id>/memory/                              the project's store; <data> is the home's data root (plugin usage)
├── format.json                                         the store's one format version
├── seats.json                                          seat id → its current name, its shelf, its private mark
├── shelves.json                                        shelf id → its name, follows and lifetime
├── names.json                                          entry id → its name (The name index)
├── log/tail.jnl, log/<hash>.seg, log/manifest.json    the append-only log: the truth
├── seats/<id>.json                                     who holds the seat, which process, which transcript
├── seats/<id>.last.json                                the claim of the seat's last holder
├── seats/<id>.slice                                    the last turns of the seat's last session
├── seats/.lock                                         held while a claim is read, judged and written
├── attachments.refs                                    the attachment hashes its entries still holding a body name
├── credentials-found.json                              what the migration scan found, until each seat's brief tells it
├── .ledger                                             the size ledger: every file below the store, its length and crc32c
├── scratch-paths.json                                  marks the scratch path pass done
├── clock.json                                          the newest time the store holds (The clock mark)
├── backup/format-<n>-<UTC stamp>/                      what a format migration changed, as it was before, for 30 days
└── backup/scratch-paths-<UTC stamp>/                   the log before the scratch path pass, for 30 days
<data>/stores/<id>/identity.json                        the store's checkouts and identifiers (internal/home)
<data>/sessions/claude/<session>/context.json           the context reading the plugin writes, with the compaction window
<data>/update-check.json                                the latest release GitHub named, when it was asked, the day the notice showed
<data>/attachments/<sha256>[.gz]                        every project's attached files, once each
<data>/attachments/.ledger                              the size ledger: each stored file, its length, crc32c and the size its attach recorded
<cache>/stores/<id>/memory/                             vectors, heat, the folded log, anchor hashes: rebuilt at will
<cache>/stores/<id>/memory/sessions.slots               per-session and per-seat state, one slot each: the slot directory
<cache>/stores/<id>/memory/sessions-{2k,8k,32k,128k}.slots  the class files holding the values past the directory's twin
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
`shelves.json`, `names.json`, a claim, `.ledger`, `fold.gob`, `heat.json` and the cache's other JSON files) is written to
a temporary file named for that one writer, `.<name>.<random>.tmp` beside it, then renamed over it
(`internal/atomicfile`), each folder from the data or cache home down walked through `internal/safefs`,
which refuses a planted link or a file with a second name
([plugin architecture](../plugin/architecture.md#writes)). The hooks of one event run in parallel, and a temporary name every writer
shares would let two of them interleave in it and the second rename publish the mix; a reader sees
the old file or the new one. Unlike the log, these files are not synced: a sync on darwin is a full
flush of the drive, paid inside hook calls.

## File formats

Store format 6 keeps what changes often in binary formats agentic-tools and shell-guard share:

- **Slot file** (`internal/slots`): what a session or seat keeps between hook calls, in a handful
  of preallocated files per store rather than a file per session and purpose. Every file opens with
  a 20-byte header (`AGSLOT\0`, its format, the slot width, the slot count, its crc32c), then slots
  in A/B twin pairs. Each twin opens with a 32-byte head: a crc32c over the head and key, a crc32c of
  the value, a generation counter, the write time, the key's and value's lengths; then the key and
  value. A write goes with `pwrite` to the older twin of its key's pair at the next generation, so a
  torn write leaves the other twin whole; a read takes, with `pread`, the newer twin whose checksums
  hold, the older one where they do not. The store's `sessions.slots` is a directory (format 2) of
  512-byte twins: it places a key by the FNV-1a hash of the key with linear probing, at most 64
  pairs on from its home pair, so a lookup of a missing key reads that probe run alone up to its
  first blank pair, never every used pair. A write placing a new key more than 16 pairs from its
  home writes the directory anew instead, at twice the pairs it has keys or half as large again,
  whichever is more, so runs stay short. A value up to about 430 bytes lives in the directory's
  twin, after a tag byte; a larger one
  in the smallest class file that holds it, `sessions-2k.slots`, `-8k`, `-32k` or `-128k` (format 3,
  twins of 2, 8, 32 and 128 KiB, the header's count zero and its pairs counted from its size), and
  the directory's twin names the class, the pair and the generation of the twin holding it. The
  class twin is written before the directory twin naming it, and a value staying in its class goes
  to the class twin the directory does not name, so a torn write at any point leaves the older value
  readable; a reader following a pointer to a twin a writer has moved on reads the directory again,
  three times at most. A value moving between the directory and a class, or between classes, frees
  its old class pair once the directory names the new one. A class file takes a new key at its first
  free pair and grows by appending one. A write of the value a key already holds writes nothing
  while the held copy is under a day old: most hook calls put back an unchanged value. A day bounds
  the skip because readers drop a key by its write time, a week on at the shortest, so a key still
  in use is written again once a day. Clearing a key frees its pair for the next key. A SessionEnd
  clears the session's `shown`, `instructions`, `ladder`, `waiter`, `graph-seen` and `fork` keys; the
  SessionStart pass clears every session key older than `staleAfter` (a session that never reached
  its end), and every seat key whose seat the registry no longer holds or holds expired, as the
  purge of a temporary shelf leaves it: a store running a temporary seat per job would otherwise keep
  every job's keys. The same SessionStart clears every prompt vector of
`<cache>/prompt-vectors/sessions.slots` no write touched for a week, at most once a day, as
agentic-tools' SessionStart does for its own slot files ([tools architecture](../tools/architecture.md)). A directory is written anew at twice the pairs it
  has keys (half full, not rounded to a power of two, since a pair costs 1 KiB), half as large
  again while a key finds no pair within its run, and renamed over the old one; a pass leaving a
  quarter of the pairs or fewer in use writes it anew at that size, clears every class twin
  no directory value names (a crash between a class write and its directory write leaves one), and
  cuts each class file after its last pair holding a value. On APFS a sparse file costs its whole
  span up to its highest write, so the layout keeps every file dense: the directory pays 1 KiB a
  pair, a class file only for the pairs it holds. Writers hold `sessions.slots.lock`; readers take no
  lock. Keys are `<purpose>/<session or seat id>`: `shown`,
  `ladder`, `graph-seen`, `fork`, `waiter`, `migration-failed`, `instructions` per session; `traps`,
  `do-now`, `skipped`, `brief`, `seen`, `ledger-ended`, `ledger-pressure`, `channel` per seat; `graph-names/<yyyy>/<mm>` per month. A
  value is the JSON the per-session file held. A hook takes its payload's `session_id` only in the
  shape `^[A-Za-z0-9_-]{1,128}$` (`hookSessionShape`, as the tool library and the prompt vectors
  take it); a payload carrying none or one of another shape reads as carrying none, and every
  per-session key of that hook is empty (`sessionKey`), which the slot file refuses, so it reads and
  writes no per-session state, not even under the session its claim names, while the brief and the
  seat's own state work as before. An unchecked id past about 230 bytes would push every such key
  over the 255-byte key limit and lose the session's state silently, and one holding `/` could land
  on another session's key (`spawn/<a>/<call>`). Hex-encoding such an id was rejected: a second rule
  unlike the tool library's, and a long id would still need hashing to fit.
- **Ceilings** (`slots.Fitted`, `internal/memory/slotkinds.go`): every kind of value declares a
  ceiling in encoded JSON bytes, never in words or runes, since a run without spaces is one word of
  any length. A write applies the kind's caps, then shrinks a value past its ceiling step by step to
  a minimal form that always fits, so no value a hook builds is refused; one test per kind builds its
  worst case and stores it. The ladder keeps the prompt cut to the words of the asked line
  (`EntryTargetWords`), then to 3,500 bytes at a rune boundary; its written files to 20 paths and
  3,500 bytes, a path past them left out; the owed line to 3,500 bytes of whole lines and a `+N
  more` line. A month's table of names grows by parts. A trap index past its ceiling becomes the
  marker `{"traps":[],"from_log":true}`, and the tool hook matches the seat's traps from the log. A
  landed round whose mappings would pass 300 or the ceiling keeps the commits that fit and leaves its
  scan mark at the last of them, so the next round takes the rest. Shown sets, the seen mark's due
  entries and the ended lines' mark drop their older half. The ceilings: `ladder` 11,000; `shown`
  32,768; `spawn` 33,000; `agents` 110,000 (800 records, id and seat key 64 bytes); `instructions`
  65,536 (a path past 1,024 bytes is not watched); `graph-seen` 2,200 (past it the month reads as
  holding no graph); a part of `graph-names` 120,000; `traps`, `do-now`, `skipped`, `seen`,
  `ledger-ended` 65,536 (an anchor or `--about` path 1,024, a trigger 512; a do-now title 512, a
  trigger 256, 4 proof triggers of 128); `ledger-pressure` 20,000 (the newest 256 changes, which
  fit it at any number's width); `landed` 120,000 (a target 64, a subject and test 200);
  `branch-tips` 95,000 (300 branches of 255 bytes); `fork`, `waiter`, `migration-failed`, `brief`,
  `landed-scan` 200. A slot write that fails is reported on stderr, `agentic-memory: keeping <key>:
  <error>` (`clearing` for a removal, `reclaiming` for the SessionStart pass), which the agent never
  reads: a lost write costs session state, never the call.
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
added took about 4.5 ms either way, since both then load the store. A table grows by parts of 6,000
names (120,000 bytes): the first is keyed by the month, each later one
`graph-names/<yyyy>/<mm>/<n>`, and a name's position is its part's number times 6,000 plus its line.
The tables of months before the last go with the SessionStart pass, every part of them.

The migration to format 6 runs under the store lock and writes `format.json` last: it seals the
JSON-lines tail an earlier release kept, `log/tail.jsonl`, into a segment, takes every per-session
and per-seat file of the cache (`shown/`, `ladder/`, `traps/`, `briefs/`) into `sessions.slots`,
refreshes the ledger, then removes those folders and the per-process markers (`forks/`, `waiters/`,
`migration-failed/`, `graph-seen/`); a mark lost there costs a session one capture notice. Prompt
vectors and the attachments' ledger move at the next SessionStart pass. A release reading up to
format 5 refuses a format-6 store as newer. Rejected: a
database (a dependency, and a server or a CGO build for what a fixed-width file does); one file per
session in a binary codec (the file count and the directory walks stay); keeping JSON-lines files
(a torn line reads as garbage with no checksum to tell).

The slot directory and its classes replace one file of 128 KiB twins, which on APFS spent the whole
span up to its highest written twin, 2.3 GiB for one store holding about 45 MB of values. Rejected
for it: `go.etcd.io/bbolt` (its file lock couples readers and writers across processes, each commit
needs an `fsync`, and the file never shrinks without compaction); `modernc.org/sqlite` (megabytes
in every hook binary, a slower open in hooks living milliseconds, `-wal` and `-shm` side files); a
fixed width per kind of value (every key pays its kind's worst case, and every cap change is a
migration); one file per session or seat (the thousands of small files return); punching holes into
the fixed-width file (macOS only, and it bounds nothing); an append log (a lookup needs an index or
a full scan).

## Seat ids

A seat has an id, `s-` and 6 random characters from `0-9a-z`, minted the first time its name is
claimed and never changed. Events, claim files, slices and the slot file's seat keys carry the id;
`seats.json` maps each id to the seat's one current name, and no two seats share a name. A name
reaches the store only through the registry: the log folds with every id turned into the name the
seat has now, so a `rename` shows everywhere at once. No seat may be named in an id's shape.
`seats.json` is written under the store's lock (`.lock` in the store root), through a temporary
file renamed into place. A seat's `"private": true` lives there too, beside its name, so the mark
outlasts every claim file: `who --all` reads each other store's registry and drops its private seats
before counting or listing any, as plain `who` does before counting them into its fold line.

`who` names a live holder's session from Claude Code's own per-process record,
`sessions/<pid>.json` under its configuration folder, read for the claim's pid and trusted only where
its `sessionId` is the claim's: a pid is reused once its process ends, so a record left behind or
written for another session names no holder. The file is Claude Code's, not memory's: where its
shape changes, the `message:` part disappears and nothing else in the row changes. The process
table names no session, which is why `ps` is not asked.

## Shelves

A shelf has an id, `h-` and 6 random characters from `0-9a-z`, and `shelves.json` maps it to its
name, the topics its seats follow, and its lifetime. Each `seats.json` entry names its shelf
(`"shelf": "<shelf id>"`), fixed when the seat is minted. An entry's shelf is its filing seat's, so
the log names no shelf and gains no event kind: a `new` row's seat key leads to the seat, and the
seat to its shelf. A `--for` stores shelf ids; one an earlier release stored as a seat id stands for
that seat's shelf. Both files are written under the store lock, through a temporary file renamed
into place, and a seat and a shelf minted together are minted under one hold of it.

## The name index

`names.json` maps every entry id to its name ([usage](usage.md#names)); the id stays the entry's
identity in the log, and the name is an alias the index alone holds. A filing mints the name under
the store lock, reading the index afresh, and writes it before the entry reaches the log, so two
filings never take one name and a kill between the two leaves a name no entry holds, never an entry
without one. A filing the log then refuses takes its name back. The title is scanned for a
credential before the name is minted, since a name is made of its words. A name is never given again
once taken, purged entry or not. Every command resolves the names a request carries to ids after
reading its arguments, from the command line and the MCP server alike, before the command runs, so
no command reads a name; the fold carries each entry's name for the lines and JSON objects that
print it. `merge` of two stores carries the source's names over, a name the target gives numbered
apart. Rejected: the name in the `new` row's payload, which a format step cannot add to entries
already filed without rewriting the log; a random two-word name such as `amber-otter`, which says
nothing about the entry and is easy to mix up; list numbers alone, which change from one listing to
the next.

## The format and its migration

`format.json` holds the store's format, `{"format": 11}`; a store without one is format 1, where
events and seat files name the seat, format 2 keeps seats by id without shelves, format 3 files
entries with a retention level and no kind, format 4 holds no `item` rows, format 5 keeps its tail as
`tail.jsonl` and a JSON file per session in the cache, format 6 holds no `due` rows, format 7
holds no state ledger, format 8 names no entry, format 9 keeps the cache's session state in one
fixed-width slot file, and format 10 may hold todos without items. The first call that meets an older store moves it through the store's step
table (`formatSteps` in `internal/memory/migrate.go`), run by the shared runner
([format steps](../plugin/architecture.md#format-steps)): one step per format, each doing one job
as an ordered list of operations, the store lock held from the first step to the last, so another
call waits and then reads the migrated store, and `format.json` written after each step.

From format 1, seats by id:

1. copy `log/` and `seats/` to `backup/format-1-<UTC stamp>/`;
2. mint an id for every seat name an event or a seat file mentions and write `seats.json`; a name the
   registry already holds keeps its id, so a run carrying the step on mints none twice;
3. rename every seat file to its id;
4. build a new log as `log.migrating`, every event's seat replaced by its id, set the old one aside as
   `log.format-1`, put the new one in place, and remove `log.format-1`.

From format 2, shelves, the log untouched:

1. copy `seats.json` to `backup/format-2-<UTC stamp>/`;
2. give every seat a shelf of its own under its name, its follows moved there, and write
   `shelves.json`; a shelf already under the seat's name is the one a cut run minted, and is kept;
3. write `seats.json`, every seat naming its shelf.

From format 3, entry kinds:

1. copy `log/` to `backup/format-3-<UTC stamp>/`;
2. run every migration pass over the log's events: the kind pass makes every entry whose decay in
   force (its filed retention as later `retain` events left it) is `standing` a `rule`, keeping its
   source, and every other a `note`, keeping its filed retention as a retain override, so no entry
   moves in any brief; the credential pass scans every entry still holding its body and keeps what
   it finds, by filing seat, in `credentials-found.json` in the store, which each seat's next brief
   reads and empties of its own;
3. build the migrated log as `log.migrating`, set the old one aside as `log.format-3`, put the new
   one in place and remove `log.format-3`. A log no pass changed stays as it is: only a set-aside
   log a cut run left is removed.

From format 4, from 6 and from 7 the step writes `format.json` and nothing else: each new format
lets the log hold rows a binary of the format before would misread, so the format keeps such a
binary off the store. Format 5 adds the `item` event kind a todolist is kept in, which a format-4
binary cannot decode. Format 7 adds the `due` event kind moving or clearing an entry's due time,
which a format-6 binary cannot decode: once one session runs a format-7 release, every session on an
earlier one refuses memory calls as on a newer store until its plugin reloads, as each format step
before cost. A due time set at filing rides in the `new` row's payload; a merge folds an entry's `due`
rows past the horizon into that payload, the last one standing. Rejected: a due time at filing only,
which an older binary would ignore with no format step, but which could never move or clear; riding
an existing event kind, each of which changes state in an older binary (`retain` resets the decay,
`confirm` re-anchors, `settle` feeds the do-now items). Format 8 keeps each seat's state ledger as
`item` rows of the reserved entry `state`, which a format-7 binary would fold as items of an entry it
does not hold and drop unread. A seat's `state` rows stay as they are: the newest one the agent wrote
reads as the ledger's `state#old` until the agent ticks or removes it, and a hook's draft is never read
again. Rejected: folding the old body into a line at migration, which the agent would never split;
starting every ledger empty, which loses where every live seat's work stands mid-task; copying the
log to a backup in a step that changes nothing but the format.

From format 8, entry names, the log untouched: one operation writes `names.json`, naming every
entry the log holds from its title as it stands, in filing order, so of two entries suggesting one
name the older keeps it bare and the younger is numbered apart. The index is replaced whole, so a cut
run leaves it whole or absent, and the run carrying the step on names only what it lacks. No backup: the step adds a file and changes none. The format keeps a
format-8 binary off the store, which would file entries the index never names.

From format 9, the slot layout, the log untouched: one operation lays `sessions.slots` out as a
slot directory with its class files (`slots.File.Relayout`), every key at the generation and time it
held, a value within its kind's ceiling as it was, one past it fitted (`refit` in
`internal/memory/slotkinds.go`) and one that no longer decodes as its kind left out; a key of no
kind `slotKinds` names is kept as it was up to `slots.MaxValue` (130,785 bytes) and left out past
it; a table of names keeps the names of its first part. The class files are written first and the directory renamed
over the fixed-width file last, so a cut run finds the fixed-width file and lays it out again,
clearing the class files it left. The format keeps a format-9 binary off the store, which refuses a
slot directory as no slot file.

From format 10, todos without items, the log only appended to: one operation (`todosToLines` in
`internal/memory/todolines.go`) folds the store and, for every live todo whose items are none or all
removed, appends an `item` row adding a line `<title> (<id>)` to the ledger of the seat that filed
it, carrying the todo's `until`, `until_hashes`, `until_hold`, session and due time, then a `rekind`
row to `note` whose `line` names that line, which clears the entry's ends and due time in the fold
and in a merge folding the row into the `new` row. The rows are appended in one write. A run cut
between the two finds the line already on the ledger by its text and appends the rekind alone; a run
after the rekind finds no todo left, so the step adds nothing twice. No backup: no row is rewritten,
and a rekind back restores the kind, the line keeping the ends. The format keeps a format-10 binary off the store, which would
file todos without items again and drop a line's `due` unread. Rejected: rebuilding the log with the
todo's `new` row rewritten, which would lose the entry's history of being a todo and needs a
backup; leaving the ends on the note as well, which would end the note and the line separately.

From format 5, the binary formats, the log's rows untouched:

1. seal the log's `tail.jsonl` into a segment through the log's own crash-safe steps;
2. put every per-session and per-seat JSON file of the cache into the slot file, each at its
   modification time; a file the slot file already holds under its key, value and time is not
   written again;
3. remove the folders of those files (`legacyCacheDirs`), which every SessionStart pass also does.

Once the last step ran, the size ledger is rebuilt. A run stopped anywhere, by a failure or a kill,
leaves the store at the format of the step it stopped in, and the next call runs that step again
from its first operation: every operation is safe to repeat from wherever it stopped, a backup a cut
run made is kept and no second one written, and a set-aside log left by a cut run, `log.format-1` or
`log.format-3`, is read where `log/` is missing and cleared before the live log takes its name. A
failure says
`agentic-memory: migrating the store failed: moving this store from format <n> to <n+1>: <operation>: <cause>; the next call carries it on`
once per session, and the call runs on the store at the format it reached. A store of a newer format
than the release knows is refused, also when a newer release moved it while the call waited for the
store lock: `agentic-memory: this store has format <n>; this release reads up to <m>`, with hooks
silent and nothing written; a binary of an earlier release refuses a newer store that way. A store
created by this release starts at the current format. The log's manifest no longer carries a format
number; one that does still reads.


### The scratch path pass

Scratch moved from `.tmp/claude/<name>/` to `.tmp/seats/<seat or session id>/` and
`.tmp/keep/<name>/`, so every store rewrites the scratch paths its entries quote, once, in place.
The first call of a release carrying the pass on a store without `scratch-paths.json`, after any
format migration and under the store lock:

1. runs the scratch path pass over a copy of the log's events; a log it does not change stays as it
   is, with no backup, and only the marker is written;
2. takes the log lock and, holding it, copies `log/` to `backup/scratch-paths-<UTC stamp>/`, so no
   row appended in between is missing from the copy;
3. still holding it, rewrites the log through its own merge, every event through the pass: the
   merge builds one segment and lists it in place of the old ones in a single manifest write, so
   the rebuilt log swaps in whole;
4. writes `scratch-paths.json` last.

The pass maps by rule, never by a record of moves: `.tmp/claude/<seat>/...` becomes
`.tmp/seats/<seat>/...` for any seat the store's `seats.json` names, expired ones included;
`.tmp/claude/<session id>/...`, a lower-case UUID, becomes `.tmp/seats/<session id>/...`; any other
`.tmp/claude/<name>...` becomes `.tmp/keep/<name>...`. `.tmp/claude/` followed by no name, by a
placeholder (`<seat>`, `$SESSION`) or by a glob (`*`) names the old layout, and stays. It rewrites a
`new` row's title, body and anchor paths, a `retitle`'s title, a `confirm`'s anchor paths, an
`item`'s text and a `state` row's body, of every entry, retired and superseded ones included. A
`new` row's path fields are mapped too, since an end or trigger naming a moved folder would
misfire: every `--about` path, the path of each `changed:` and `gone:` end (and the
`changed:` hash keyed by it), and the glob of each `path:` trigger in `when` and `proof`. A glob
maps only where its name is literal, `.tmp/claude/<name>/` or the name closing it; one whose
wildcard lies within or before the name (`.tmp/claude/ma*`, `.tmp/claude/*`) stays, since it
could match a seat folder and a keep alike. No other event kind carries those fields; a retire's
or supersede's reason stays as written. Releases before it trimmed a path's leading dot as
punctuation, storing `.tmp/claude/<name>` as `tmp/claude/<name>` in anchors, `--about` paths and
ends: in those stored path fields alone, a path opening with `tmp/claude/` maps as its dotted form,
to the dotted new path; any other dotless `tmp/` path stays as stored, since nothing says it once
had a dot, and text and triggers keep what was written. A
payload it does not change stays byte for byte. An anchor keeps its hash under its new path, since
the move left the file's content as it was. Ids, rows, times, seats, links and heat stay; an entry
whose title or body changed has a new content hash, so the next fill embeds it again.

The format stays 7: the pass changes payload text only and adds no event kind, so a format-7 binary
without it reads the rewritten store, where a format step would lock it out as newer. A run stopped
anywhere leaves a log either release reads: before the manifest write the old one, after it the
rewritten one. The next call runs the pass again, which finds nothing left to change in a rewritten
log. A store created by this release carries the marker from the start. Text an older release writes
after the pass still names old paths. A failure says
`agentic-memory: rewriting the scratch paths the store's entries name failed: <cause>; the next call tries again`
once per session, and the call runs on the store as it is.

Rejected: superseding each affected entry with a corrected copy, whose new ids every link and note
would have to follow; rewriting what memory prints while the store keeps the old text, which costs
every read a lookup and keeps the stale text in exports and searches; a new event kind for body
edits, which an older binary reads as a corrupt log.

### Backup lifetime

Every backup a migration writes, `backup/format-<n>-<UTC stamp>/` and
`backup/scratch-paths-<UTC stamp>/`, is removed once it is 30 days old, a lifetime fixed in code
(`home.BackupLifetime`) with no option. A session's start removes its store's expired backups under
the store lock. The age is the stamp in the folder's name, never its times, so a copy or a touch
neither keeps a backup alive nor ages it early. A folder whose name carries no readable stamp, a
loose file and a link under `backup/` stay; the removal goes through `internal/safefs`, never
following a link. Rejected: an option, a value nobody tunes; keeping only the newest backup, which
loses the one from before the first of two migrations close together.

Which store a checkout works on is `internal/home`'s answer: the pointer in its git common
directory, else the identifiers the stores' `identity.json` record, so every worktree of a
repository shares one store, and a code repository shares its graph repository's once that store
admits it ([plugin architecture](../plugin/architecture.md#the-home)). A directory counts
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
a changed embedder starts a fresh store instead of mixing two vector spaces. The fingerprint names the
provider, the model, the dimensions and a hash of the query and document templates
(`openai/<model>/<dims>/t<hash>`), every one of which changes the vectors; the endpoint and its path
stay out, so moving a local server to another port keeps the store. A store no binary has used for
`cache_retention` is deleted by the daily `agentic-helpers prune` (`internal/cacheprune`, judged by
`used.json` in `segments/`; [plugin usage](../plugin/usage.md#cache-pruning)). The layout follows sdd's
search segments:

```
header   magic agvecseg, format, dims, row count, chunk count, table length
table    per entry: id, content hash, chunk count (uvarint lengths and count)
padding  zeros up to a 64-byte boundary
vectors  chunk count * dims little-endian float32, an entry's chunks contiguous, in table order
```

- **The manifest records only the cap.** `segments/manifest.json` holds `max_runes`, the
  cap every text of the store's rows was cut to, as sdd names it; a store without one (0) predates
  the cap. The fold says which rows count: an entry's row whose hash is `embed.RowHash` of the
  entry's title and body and the manifest's cap (sha256, 16 hex digits), or any row of an entry
  whose body was dropped, since nothing embeds it again. A row of older content, or one cut to
  another cap, is dead without anything recording it.
- **Input sizing.** An embedder past its input limit discards the tail without an error and answers
  the vector of the head, so every text is sized to the limit before it is sent. The limit is
  measured once per endpoint (`internal/embed`, `calibrate.go`, matching sdd's calibration): a
  probe of 1000 runes of Latin text, one of 1000 runes of CJK (the worst tokens per rune), and an
  overflow of 20000 runes. A count stuck below 0.9 of its linear growth is the limit of a provider
  truncating silently (`plateau`); a refused overflow is bisected to 64 runes between the last size
  accepted and the first refused (`bisect`); a count that keeps growing is a lower bound
  (`unbounded`); a provider reporting no count (OpenAI's `usage.prompt_tokens`, Ollama's
  `prompt_eval_count`) gets 2048 runes (`unreported`). The cap is the limit times 0.9 over the worst
  tokens per rune, rounded down to a 64-rune step, so probe noise below a step changes nothing. The
  result is stored as `<cache>/embedding-calibration/calibration-<sha256(key)[:16]>.json`
  (`method`, `limit_tokens`, `tokens_per_rune`, `max_runes`), written whole by rename, the key
  being the vector fingerprint, the endpoint URL and the path, one per line: a changed endpoint or
  path is measured again and re-embeds nothing by itself. A file that does not read is an error
  naming it, never measured over.
- **Where calibration runs.** Only a fill measures, and only where no calibration is stored for its
  endpoint: the first search by meaning, prompt recall included, after an upgrade or a change of
  endpoint. The probes share the fill's per-request deadline, the 20-second hook limit in a hook, so
  no hook waits on calibration past the deadline it already keeps. A failed probe is an error: a
  search refuses naming it, and recall tells it once through its failure line.
- **Chunks and queries.** A fill cuts each entry into chunks of at most the cap, `Entry: <title>`
  included, the title taking at most a quarter of it and the body pieces at most 3200 runes. A
  search cuts its phrase, and recall its prompt, to the cap the manifest records, never probing; a
  phrase longer than the cap is embedded by its head.
- **A search** maps every segment read-only and shared, and scans the mapped vector block in place,
  decoding and copying no vector: each counted entry scores by its best chunk, its first counted row
  deciding. A long-running process, the MCP server, maps a segment once and keeps it across calls.
  A merge that drops a segment between a search's listing and its open sends the search to list
  again.
- **A write** embeds only the live entries no segment holds for their current content and cap, and writes
  them one segment per embedding request (`embed.RequestTexts` texts at most, whole entries), each
  through a temporary `.segment-*` file renamed to the first 16 hex digits of the content's sha256:
  its cost follows the rows written, never the store. As sdd fills its index, each request has its
  own `embedding_timeout` deadline, so one search lands a re-embed of the whole store however many
  requests it takes; a write the embedder fails or the caller's own deadline (a prompt's recall)
  cuts short keeps every request answered before.
- **A fill** runs under `fill.lock` in the store's directory, taken without waiting before the
  snapshot: a process finding another one filling embeds nothing and scores the rows held so far,
  so concurrent sessions never embed the same texts at once. A store without a cap takes the
  calibrated one at once, since none of its rows counts under any other; a store whose cap differs
  from the calibrated one keeps counting its old rows while the fill embeds every entry again, then
  records the new cap. An upgrade is silent: the first fill calibrates and re-embeds the store once,
  saved per request, so a hook's deadline only defers the rest. A store without a cap when a search
  needs one, its first fill running in another process, refuses the search.
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
- **The whole-file `vectors.gob`** an older release kept beside `segments/` is deleted: its rows were
  embedded uncut, so none counts under a cap, and the next fill embeds its entries again.

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

### The clock mark

A system clock set back would make everything stored before look as if it lay in the future. The
store keeps a high-water mark of the newest time it holds, `clock.json` beside the log
(`{"mark": <time>}`), and memory's time (`now` in `clock.go`) is the system clock, or one nanosecond
past the mark while the clock trails it. Every place that stamps or compares a time reads it: filing
and every log row, claims and their closes, reservations, brief and seen marks, nudge ladders, state
lines and their ends, heat, due times, `at:` ends, `who`'s 14-day window, the purge's retention and
`revive`'s window. So an entry filed after a rollback sorts after the earlier ones and arrives as
new in another seat's brief, no heat is computed from a negative age, and a nudge
answered after a rollback does not repeat.

The mark rises with every stored time: a write to the log or a claim (through the size ledger's
refresh), a slot, the heat cache. Each raises it to the latest time `now` answered in the process,
never lower than the mark another process left, and refreshes the store's size ledger. A store
without the file starts from the newest stamp in its log. A process reads the mark once, the MCP
server once per call. It is written only where memory writes the store anyway: never into a store
that does not exist, one of a newer format, or one outside git where memory keeps to git. The file
is new beside the log and changes nothing an earlier release reads, so it took no store format step.

Not covered: while the system clock trails the mark, memory's time stands at the mark, so due times
and `at:` ends fire late by up to the rollback and decay pauses for that span. Content-hash anchors
(`changed:`) compare hashes, not times, and are unaffected. A clock set forward and then corrected
leaves the mark ahead, which reads the same as a rollback of that span. The scratch sweep ages
folders by their modification times, which the file system stamps, not memory.

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
the entries it holds rather than with every change ever made. Topic merges, every `item` row (a
todolist's items, which the `new` row does not carry) and every younger event stay as they are. An `untag` row carries every name the entry was filed or tagged with that reads as
a removed topic, so folding it needs no topic merge, and past the horizon it folds into the `new`
row like any other.

A `state` row names a seat and no entry, and carries the free-text slot a seat wrote before the
state ledger: `body`, `session`, `branch`, `commit`, `draft` where a hook wrote it, its time the
row's. No release writes one now; the newest a seat's agent wrote reads as its ledger's `state#old`
([State ledger](#state-ledger)). A merge keeps only each seat's newest `state` row, and a purge
drops the `state` rows and the ledger rows of the seats it purges. The
`untag`, `state`, `rekind` and `settle` rows are kinds binaries before format 4 do not read; the format bump
keeps those binaries off the store. The same merge drops the body of an entry
superseded more than 14 days earlier, or retired more than `retired_body_lifetime` earlier; the entry
stays in search, flagged, and `revive` refuses it.

## Seats and liveness

A claim file, under the seat's id, names the holding session, the pid of its `claude` process, that process's start time
(`started`, RFC 3339 in UTC), when it was claimed, and the session's transcript. A `release` or a
SessionEnd closing the seat moves the claim to `<id>.last.json` with `closed`, the time it closed.
A claim records `commit`, HEAD when it was taken, which the `landed` lines of the seat's state ledger
count the session's commits from. A claim made with `--session` for a session holding no seat is a reservation: `reserved` holds its
time and `pid` is 0; any hook whose payload carries that session id, run by a process holding no
seat, writes its owning `claude` process, start time and transcript into it, and prints the seat's
`scratch:` line and brief, except at a resumed session's SessionStart. A handover writes the
new seat's claim from the old one, process and all, with `moved_from` naming the old seat until the
session's next hook reports it. A call finds its own
seat by walking up the process tree to the nearest `claude` process.

A seat held for a `full` subagent carries `agent`: `pending` from the spawn until the subagent's
first hook names it, then its agent id. Its claim names the session and its `claude` process, as
the session's own does, so every lookup by process (`seat.Of`) or by session (`seat.OfSession`), and
the inheritance of a resumed or forked session, passes it over: a subagent's call shares its
spawner's process and session id, and only `--agent <agent-id>` on the command line or the key
`agent` of an MCP call reaches it. Claude Code hands an MCP tool the input a PreToolUse hook's
`updatedInput` rewrote, so the `agent` the hook sets reaches the server; a join on the call's
`_meta` `claudecode/toolUseId` is rejected: a second path to the same subagent, with records to
keep. The record is the session's `agents/<session>` slot: each subagent's level and its seat's key
at `full`. The command line looks the agent up under `CLAUDE_CODE_SESSION_ID`; the server under the
session its caller's seat claim names, else the one it started in, since a `/clear` leaves the
server's environment behind.

A subagent's level is read back from the level line its own transcript opens with, or from the
PostToolUse of a background Agent call. A fork's transcript opens with a `fork-context-ref` record
instead, so the PreToolUse hook on an Agent call whose `subagent_type` is `fork` keeps the level
granted, a `full` one's seat, and the spawner's shown set as it stands (the session's, or the
spawning subagent's own) in the slot `spawn/<session>/<tool_use_id>`. The fork's first hook finds
the call's id in `agent-<agent-id>.meta.json` (`toolUseId`) beside its transcript, learns its level
from that slot, and its first memory call or trap check copies the set into its own and removes the
slot. The slot is kept for a fork at every level, since trap and note notices reach one below
`silent` too.

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

Every hook exits at once in a project without a store. What a hook says to the agent goes out as one
`additionalContext` answer for its event, which Claude Code hands the agent without showing the
user: inside a hook `say` holds each line, and `sayHidden` answers with them once the hook returns.
A hook whose event has no hidden channel (Stop) says nothing; no hook prints plain stdout.

| Event | What memory does |
|---|---|
| SessionStart (`session-start`) | purges every store's due temporary shelves, the stale context readings and the unclaimed stores past their grace; prints the store question where one stands on the checkout; for a seated session, points the claim at the session and transcript, and after a compaction or `/clear` prints the brief, on resume the `state#old` request where the seat's old slot waits; for a seatless one, frees the seats whose holders are gone, then takes over the seat of the session it resumes or forks, or a new seat on its shelf, else names the project's seats; a fork whose transcript is not written yet gets a marker and no notice |
| SessionStart (`session-log <n>`) | after a compaction, prints piece `<n>` of the turns since the seat's newest entry or agent change to its state ledger; one hook per piece, since Claude Code inlines a hook's output only up to 10,000 characters and swaps anything longer for a file path. Pieces are 9,000 characters (`pieceChars`), at most five (`maxPieces`), the newest kept; where older ones were cut, the first piece's header names their count and the transcript's path; a piece opening inside a day repeats that day's `# <date> UTC` header within the 9,000, as `transcript.Tail` does for a claim's slice |
| UserPromptSubmit (`prompt`) | first, for any session, seat or none, hashes every file its `instructions/<session>` slot records and prints `agentic-memory: <path> changed since loaded; re-read it` for each whose hash moved, recording the new hash; a session with no slot yet seeds it with the fixed set ([usage](usage.md#instruction-files-that-change)) and prints nothing for it; then, for a fork carrying a marker, once its transcript is written, removes the marker and, where it holds no seat, seats it as SessionStart would, printing the claim's output or the seatless notice instead of anything else; otherwise adds up to three related entries, the nudge near compaction (at every prompt from the first rung crossed until answered, `remind`) and the replay nudge once the turns since the seat's newest entry or agent change to its state ledger would no longer fit the pieces, once per newest entry or ledger change; the ladder records the time of the one it fired for (`replay_nudged_for`) and the transcript size at its last check (`replay_checked_size`), and the transcript is condensed only once it grew by `pieceChars` bytes since; the prompt's vector comes from, or goes to, the session's slot under `<cache>/prompt-vectors/`, which agentic-tools' offer shares; a prompt holding a credential is never embedded: recall scores entries by its words and the notice prints once; without an embedder configured (`embed.ErrNotConfigured`), recall prints nothing; a recall the embedder fails prints `agentic-memory: prompt recall failed, retried each prompt: <error>` once per outage, and the next working recall re-arms it |
| InstructionsLoaded (`instructions`) | records the file Claude Code reports (`file_path`) with its content hash under the session's `instructions/<session>` slot, through the slot file's `Update`, so files reported at once never lose each other; needs no seat; says nothing |
| PostToolUse (`tool`) | first the instruction-file check UserPromptSubmit runs, as `additionalContext`, where an `Edit`, `Write` or `NotebookEdit` of a recorded path takes the new hash in silently; then, for a fork carrying a marker, does what UserPromptSubmit does for it, the claim's output or the seatless notice as `additionalContext`; on the main thread's call, gives the line the last Stop owed and counts the call in the ladder (`tool_calls`), every tenth one answering or repeating the nudge near compaction as UserPromptSubmit does (`owedAtTool`); tells the seat about arrivals, graph gains and the traps the call touched, in one `additionalContext`, and settles the do-now items the call proves; adds the state ledger's `captured` lines for the graph's gains, and after a Bash `git commit`, `merge` or `cherry-pick` its `landed` lines; where the log's tail and manifest are as they were at the seat's last look, two `stat` calls for arrivals, and one for the graph's directory of the current month |
| PreToolUse (`pre-tool`) | on a Bash call whose `tool_input.command` does not contain `agentic-memory`, exits 0 writing nothing as soon as the payload is read, before the store and the seat are looked up: every Bash call runs the hook side by side with shell-guard, and the slower of the two sets the delay; on every other Bash call, on the Agent tool and on the memory MCP tools, runs the subagent checks ([usage](usage.md#subagents)): rewrites the Agent prompt and, for a fork, keeps its spawn slot; refuses a subagent's call its level does not allow; sets `--agent` on a reading subagent's every direct `agentic-memory` call and `agent` on its every memory tool call; refuses either from the main thread |
| SubagentStop (`subagent-stop`) | removes the subagent's shown set and any spawn slot kept for it, and releases a `full` subagent's seat |
| Stop (`stop`) | adds the state ledger's `landed` lines for commits since the claim and keeps its one `dirty` and one `asked` line current ([State ledger](#state-ledger)); while do-now items are pending, at most twice per moment, marks their line owed in the ladder's `owed` field; prints nothing and holds no turn, since Stop `additionalContext` always shows the user `Stop hook feedback`; the main thread's next PreToolUse (where its subagent checks give the call no answer), PostToolUse, UserPromptSubmit or SessionStart carries the line once |
| Stop (`wait`, `asyncRewake`) | the idle waiter, below |
| SessionEnd (`session-end`) | removes the session's context reading and its `instructions/<session>` slot; closes every seat still held for one of its subagents and forgets its `agents/<session>` record, its subagents' shown sets and its spawn slots; adds the state ledger's hook lines as Stop does, keeps the session's last turns as its seat's slice, empties the session's prompt vector slot, and, for any reason but `clear` or `resume`, closes the seat and purges what is due |

## The update check

`internal/updates` decides the claim's notice of a newer release
([usage](usage.md#a-newer-release)). `claim` asks it last, after the carry-forward hint, only for a
claim the caller makes for itself: `claimFor` answers a `--session` claim before reaching it, and
the claim SessionStart, a prompt or a tool call makes for a resumed or forked session carries the
request's unexported `inherited` mark, which neither the CLI nor the MCP arguments can set.

The check reads in order of cost and stops at the first no: the option, then whether the running
version is three numbers (a `go install` build or a pre-release is none), then every settings file
and `known_marketplaces.json` for an `agentic-helpers` entry turning `autoUpdate` on or naming a
`ref` or `sha`, so a machine that never shows the notice never touches the network. It then takes
`<data>/.update-check.lock` without waiting; a claim finding it held shows nothing, so two claims at
once neither ask twice nor both show the notice. Under the lock it reads `<data>/update-check.json`:

```json
{"checked": "2026-10-30T21:00:00+01:00", "latest": "v2.18.0", "shown": "2026-10-30"}
```

`shown` equal to today's local date ends the check. A `checked` 24 hours old or more sends one GET,
bounded by a 3-second context and the client's own 3-second timeout, and records the time and the
tag, an empty tag for a failed or refused request or an answer naming no `tag_name`, so a failure
waits a day like an answer. A newer tag sets `shown` and returns the lines. The cache is replaced
whole through `internal/safefs`; one that does not parse reads as never checked.

The client is an interface (`Do`), `net/http`'s client in the binary and `test.Releases` in the
tests, and a nil client in memory's `Env` shows nothing, so no test reaches GitHub.

## MCP answers

Every tool of the `memory` server answers a JSON object (`internal/mcpserver`): the value goes out
as `structuredContent` and, serialized, as the text block MCP asks of a structured result for
clients reading only text. `show` and `topics` answer what their `--json` builds (`mcpJSONReads`); every other command,
the reads `search`, `brief`, `view` and `who` included, `{"message": <its text>}` alone, since the
text already names what the call produced or found and typed fields would only repeat it at two to
seven times the size. A refusal
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

Every description the listing carries has a budget in characters, recorded with its reason in
`source/hooks/internal/memory/descriptionbudget_test.go`: `toolBudgets` for each tool's
description, `keyBudget` for every key description, and `keyBudgets` for a key by its bare name on
every tool, `agent` among them, whose growth is paid once per tool listed.
`TestEveryDescriptionFitsItsBudget` reads the server's `tools/list` and fails where a description
exceeds its budget, where a tool has none, and where a budget names no tool. Each figure sat about a
tenth above the description's size when it was set; raising one is a change of its own. No
description was shortened to fit: a caller without the skill forms its call from the listing, and
key descriptions keep naming the values and shapes they take, since values are taken exactly.
Rejected: trimming the largest descriptions, a small saving for less guidance on the tools carrying
the most rules.

## Hint forms

Every command a reply or a hook line names is a `hintCall` (`internal/memory/hints.go`): its verb
and its arguments, each with its MCP key, its place (a flag, a positional, the body on stdin) and
its command-line spelling, held once and spelled for a channel. `in(channelCLI)` writes
`agentic-memory <verb> ...`, a key's flag its hyphenated name; `in(channelMCP)` writes
`memory <verb> {...}`, the keys in the order given and `<` kept unescaped, so a placeholder reads as
typed. A verb whose command is `cliOnly` is spelled as a command line in both. `hintChoice`,
`hintArgs`, `hintKey` and `hintVerb` spell a choice of calls, bare arguments, one key and a verb
alone. Holding both forms in one value keeps them from drifting; two hand-written strings per hint
were rejected for that. A test holds every MCP form to its tool's schema through `exactKeys`.

The channel a run spells for is `mem.hints`. A command sets its own before it runs: the command
line `cli`, `callTool` `mcp`. A hook settles it on the first hint it spells (`hintChannel`): the
call a tool hook follows where that is a memory call (a tool under `memoryToolPrefix` → `mcp`, a
Bash command whose parse holds an `agentic-memory` call → `cli`), else the seat's last channel,
`mcp` where it has none, since the plugin always ships the server.

The last channel is the slot `channel/<seat id>`, `{"last_channel":"mcp"|"cli"}`, a fixed-shape
value of the per-seat kind the brief mark is, which every memory call writes once it ran
(`noteChannel`), except where a subagent reads on the seat's behalf; the slot file writes nothing
where the value stands. It lives in the slot file with the other per-seat marks rather than in the claim file: a
claim rewrite takes the seats lock and writes the store at every call, and a new claim would drop
it. No format step: an older release passes the unknown key over, and the housekeeping reclaims it
with the seat's other keys.

## Links

A `new` row carries the ids its text named that resolved when it was filed (`text_links`) and the
ones `--link` named (`links`); `link` and `unlink` events add and drop explicit links, each kept with
the seat that made it. A graph id resolves where
`<yyyy>/<mm>/<dd>-<hhmmss>-<type>-<layer>-<suffix>.md` exists under the project's resolved
`graph_dir` (`.sdd/graph` where unset, the graph repository's where `project_dir` names one, as the
grounding gate resolves it), a memory id
where the folded log holds the entry. A merge keeps one `link` row per net explicit link with its
seat, dropping a link with its `unlink`, and drops a text link with the body it came from.

A tool link is a `link` event whose target is `tool:<name>`, or `tool:<name>@<store key>` for
another project's tool: a label is worked out against every store in reach and changes as projects
come and go, a store key does not. It rides the `link` kind rather than one of its own, which every
older release reading the same log would refuse as unknown; such a release reads it as a link to an
entry that is gone. `links()` leaves tool links out, so link flags, ends, `returns` and filters never
count one; `toolLinks()` holds them. The library opens once per process, through
`tools.OpenLinked`, and only for an entry naming a tool, since it reads every store's index. `show`'s
take runs `tools.Linked.Take`: the copy of `agentic-tools take`, the holding recorded, no take
journal line, and the seen set marking `<name> v<n> usage`, a key no find hit carries since a hit
lost its usage.

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

A probe that finds a `merged:` branch records its tip in the slot file under `branch-tips`; once
the branch is gone, the end asks `merge-base --is-ancestor <recorded tip> <into>`. A landed-commit
round records the second parent of the newest merge subjected `Merge branch '<branch>'` for every
watched branch that is gone, and drops the tips of branches no live end names.

## Landed commits

`landed.go`. The prompt and tool hooks read the checkout's scan mark, `landed-scan/<hash of the
session's directory>` in the slot file (`head`, its committer `date`, and `at`, when the last round
finished), and start a round where `landed_scan_interval` passed or a Bash call matched
`git … merge|commit|pull|rebase|cherry-pick`. Starting takes `landed-scan-<hash>.lock` in the
store's cache with `O_EXCL`, writing a fresh token, then starts `agentic-memory landed-round
<token>` detached (`Setsid`), from the `agentic-memory` link beside the program, since
`agentic-helpers` picks its command by the name it runs under; a lock older than ten minutes was left by a round that died and is
taken over. The round runs only while the lock holds its token, and removes it when done.

A round asks `git log -1 --format=%H%x1f%cI HEAD`; a mark equal to it reads nothing more. Otherwise
it reads one stream, `git log --topo-order --reverse --format=%x1e%H%x1f%P%x1f%cI%x1f%B%x1f
--name-only --diff-merges=first-parent` over `<mark>..HEAD` where `merge-base --is-ancestor <mark>
HEAD` holds, else `--since=<mark date> HEAD`, and on a first round `--since=<oldest floor> HEAD`.
It processes the first 200 commits and moves the mark to the last of them, or to `HEAD` where the
stream held no more. Merges group the commits of their side by parent links in the stream; a round
reading by date that finds a side commit outside its stream reads `git log <first parent>..<merge>`
once. Every landing is judged against every target before anything is stored, the texts in one
batched embedder call: a commit's subject for an entry or a state line, and for an open item the
whole commit, its message and changed file names, cut to 2,000 runes. An entry or a line matches
at a cosine of 0.75 or more (`landedSimilarity`). An item never meets that: an item and the commit
doing it score 0.56 to 0.67 under embeddinggemma-300m, while a todo's items crowd each other within
a few hundredths. Items are ranked within their todo instead: per landing and todo, the item the
landing sits closest to matches where its cosine is at least `landedItemFloor` (0.55) and
`landedItemMargin` (0.05) ahead of the todo's next closest item, its strength the cosine stretched
onto 0..1 above the floor. Both constants are tuned to that model. A near tie flags nothing, since
a hint naming the wrong item costs more than a missed one; the items of two todos never rank
against each other. Comparing items against the subject alone was rejected: a subject names the
change, the body and file names the work. No embedder configured (`embed.ErrNotConfigured`) skips the subject test;
an embedder that fails or times out leaves the mark before the first commit it left
unjudged, so the next round reads that commit again rather than never judging it, at the cost of
re-reading up to the batch from there each round while the outage lasts. The mappings go to the
slot `landed` (`target`, `commit`, `subject`, `test`,
`merge`, `score`), one per target, a higher score replacing a lower. Briefs read that slot and
rewrite it without targets that ended; `landed dismiss` rewrites it without the targets it names.
Delivery removes nothing: each brief or hook line naming a mapping marks `<target>@<sha>` in the
`landed` list of the context's shown set (`shown/<session>`), so the prompt and tool line names a
mapping once per context, and a brief prints a held mapping on the entry's pointer rather than its
whole line. The set empties at a compaction, `/clear` and resume, as for every read.

## Todolists

A todo is a todolist. `new` (CLI and MCP alike, the one door every filing passes) works out the
items first, its own and those `--items carry` moves; a todo with none is written as a `new` row of
kind `note` without `until`, `until_hashes`, `until_hold` or `due`, followed in the same append by
an `item` row on the filing seat's ledger, `<title> (<id>)`, carrying them (`todoLine` in
`internal/memory/todolines.go`). The line cites the note, so it ends once the note is retired or
superseded, as well as at its own ends. Rejected: refusing a todo without items, which costs the
filer a retry for a choice the tool can make; keeping it a todo, which leaves a single task in the
todos section while the ledger is where the seat's work stands.

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

## State ledger

A seat's state ledger is a run of `item` rows whose entry is the reserved id `state`, filed under the
seat they belong to, so the fold keys a ledger by seat name as it does the slot it replaces
(`folded.ledgers[seatName]`, `internal/memory/ledger.go`) and no seat reaches another's: `ack
state` and `show state` resolve the calling seat first. No memory id collides, every one starting
`m-`. A row's payload is the todo item's, `{op, id, text, link}`, with `until`, `until_hashes` and
`until_hold` for declared ends, `due` for the time it comes due, `session`, and for a hook's line `hook` (`landed`, `captured`,
`dirty`, `asked`), `commit` or `paths`. `op` is add, check, uncheck or remove, and `text` for a hook
replacing its one dirty or asked line. Ids come from the todo items' draw, `old` reserved. The fold
keeps each line with its state and whether an ack set it, and `changed`, the time of the seat's
newest row carrying no `hook`: the point the replay, the nudges and the replay nudge count from
(`newestOwn`), which a hook line therefore never moves. A nudge also waits on the ledger's
`landed` and `captured` lines added since the first unanswered one (`ledger.unsettled`, the ladder's
`nudged_at` set only by that first nudge): each counts as settled once ticked, removed, or cited by
an agent line that is not removed, through its `state#<item>` address, a commit word matching its
commit (`sameCommit`) or, for a captured line, its graph id in the text or as the pin. An `uncheck` sets the state too, so a line
brought back stays open whatever ended it.

A line's end is worked out at read, never stored: a declared end through the end check's probe, run
against an entry built from the line (its seat, session, time added, hashes and claim, and for
`captured` the graph ids of the `captured` lines added after it); a pinned target through the fold's
`itemLinks`, as a linked todo item; the ids its text cites through the folded log and the graph's
statuses. A hook line added before the seat's newest claim stamp (`claimed` of the claim it holds or,
released, of its last one, read once per probe, `endProbe.claimedAt`) ends, `the seat was claimed
since`; Stop and SessionEnd keep the `dirty` and `asked` lines of the current claim only
(`ledger.openHook`), so a new claim starts its own. A `landed` or `captured` line ends once 12 or more `landed` and `captured` lines the log
records open were added after it and it is over a day old (`newerHookLines`, `briefHookLines`,
`hookLineLifetime`); the nudge's `ledger.unsettled` reads the logged state, so such a line still
holds a nudge until the agent settles it. The brief names an ended agent line once: the slot
`ledger-ended/<seat id>` keeps the ids of the lines it named, and a line that comes back open leaves
it. `show state` and its JSON read one selection, `stateLines`, which holds back the landed and
captured lines out of the newest 12 open ones and every other settled line, counting each group for
its fold line. Rejected: storing an end as a row, which would need a write at read and could not
follow a target that reopens.

The ledger's size is watched per seat (`internal/memory/ledgerpressure.go`). `ack state --add`,
`--done` and `--remove`, and the brief first naming lines that ended on their own (`endedLines`),
each append one event to the slot `ledger-pressure/<seat id>`: `{"at":<unix seconds>,"open":<n>}`,
with `"settled":true` for a settle, `open` the seat's open agent lines after the change; `--undo`
records none. The slot also keeps the backoff, `fired` and `further`. Each write first drops events
older than 14 days and keeps the newest 256 (`windowed`). The marks are worked out at every add and
brief, never stored: once the window holds 10 settles, `ownMarks` takes the median of `open` at
settle events as the low mark and adds the interpolated p75 minus p25 of every `open` in the window,
at least 2, as the high one; short of that, `marksFor` reads the slot of every other seat the
registry holds and takes the median of the calibrated seats' low and high marks (`borrowedMarks`),
else 6 and 10. `ledgerPressure.record` decides the nudge: a count at or below the low mark clears
`fired` and `further`; the first add above the high mark sets `fired` and fires; each later add
counts `further` and fires above the high mark on 2, 4, 8 and 16, then every 16th (`pressureFires`).
A settle above the low mark moves neither. The nudge lists the five oldest open agent lines by time
added, each cut to 12 words, with `settleHint(state#<id>, done)` in the call's channel. The brief
prints the same lines at the end of its state section whenever the open count stands above the high
mark, with no backoff. Cost: a sort of at most 256 numbers per add, and at a seat's cold start one
slot read per seat of the store. A release that does not know the slot keeps it as a key of an
unknown purpose.

A line's due time is read as an entry's is (`dueNear`): the brief's state section holds an open
agent line due more than a day ahead in one count, and `dueLines` gives the come-due notice, the
seen mark and the next-due time of the tool call's quick check the seat's open agent lines that
carry one. Only lines carrying a due time have their state worked out there, so a ledger without
one costs that path no end check. A line is keyed in the seen mark's `due` list as
`state#<id>@<RFC 3339 due>`, beside the entries' `<id>@<due>`. The ledger stays out of the brief's
due section: a line due soon already stands whole in the state section.

Hooks add lines through the same rows: PostToolUse after a Bash `git commit`, `merge` or
`cherry-pick`, and Stop and SessionEnd, read `git log --oneline -n 20 <claim commit>..HEAD` and add
a `landed` line for every commit no line records; PostToolUse adds a `captured` line for each graph
entry the capture notice found added, its title read from the entry's front matter; Stop and
SessionEnd keep the one `dirty` line on `git status --porcelain -z --untracked-files=all --` over
the paths the session wrote and the line named, and the one `asked` line on the session's last
prompt. Each writes only where a line would change. A landed round adds every open agent line of
every seat to its targets, addressed `state:<seat>#<id>`; such a mapping stands while its line is
open, rides under it in the brief and `show state`, and stays out of the landed notice and
`landed dismiss`.

The fold cache carries the ledgers, its layout moved up with them. A merge keeps every ledger row,
as it keeps every event past the horizon of an entry it does not hold; a purge drops a purged seat's.

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
brief keeps around them what is no list of entries (its first line, the state ledger, the changes,
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
time, so a file outlives a retire by `retired_body_lifetime` and a supersede by 14 days whether or not a
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
note told is added to the calling agent's shown set, picked by the payload's `agent_id`: the
session's (the slot `shown/<session>`, `traps` and `notes`) for the main thread, the subagent's own
for a subagent, at every level, a fork's first taking over its spawn slot. A compaction, `/clear` and
a resume empty the session's. The same slot holds what reads printed: `ids`, each entry
whose line a brief, recall or read printed, and `bodies`, each whose body `show` or a view printed,
both as `<id>@<digest>`, the digest over the entry's full line in UTC and its event count, so an
entry that changed since never matches and a zone change matches every one still. A read opens the set once for the seated session it runs in and writes
it back only where it marked something; a read inside another (the brief a claim prints) shares the
outer one's. A read naming a subagent (`--agent`, or the MCP key `agent`) opens that subagent's own
set instead, the slot `shown/<session>/agent/<agent-id>`: empty for a fresh subagent, a copy of its
spawner's set at the spawn for a fork. Keeping the session's set aside for the length of a
subagent's call was rejected: a spawner's read landing while the call runs would read with the
subagent's set. SubagentStop removes the subagent's set, and the session's end every one of its
subagents' sets and spawn slots. `--again` bypasses the lookup and still marks. The index lists notes hottest first when built, but a `show` warms without a log
event, so the hook ranks the matching notes by the heat read at the call.

The session's ladder also keeps what the ledger's `dirty` and `asked` lines are made from: the files
the session's Edit, Write and NotebookEdit calls named (`wrote`, each once, the first 20) and its
last prompt (`last_prompt`, kept only from a prompt holding no credential, and never from a turn
Claude Code sends itself through UserPromptSubmit: one opening with `<task-notification>`,
`<local-command-caveat>`, `<local-command-stdout>` or `<bash-stdout>`). The ladder goes with the
window at a compaction or `/clear`; the lines it made stay in the ledger.

Do-now items pending for a seat sit beside the nudge ladder, in the slot `do-now/<seat id>`: the
session they are owed in, each item's rule, moment and proofs, and how many `Stop`s owed them per
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
