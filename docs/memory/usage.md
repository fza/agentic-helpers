# Memory: usage

A session runs out of context and the next one starts knowing nothing. With agentic-memory a session
files what it learns as it works, one entry per finding, and the tool stamps, indexes and replays
it. Nothing is ever rewritten: an entry is retired or superseded, and the tool chooses what returns
to a fresh context.

## Memory and the graph

sdd's graph is the project's long-term memory, and agentic-memory its short-term memory. They hold
different things, and an entry belongs in exactly one:

| | sdd graph (`.sdd/`) | agentic-memory |
|---|---|---|
| Holds | decisions, the signals behind them, why the project is the way it is | where work stands, a judgment call made without confirmation, an open question, a measured fact nothing else records, a trap that already cost time |
| For | everyone working on the project, for as long as it stands | one job, the seat, across compactions and sessions |
| Lives | in the repository, under version control, reviewed like code | on the machine, under `~/.claude/agentic-memory/`, outside the repository |
| Changes | never in place: a decision is superseded, a signal closed | retired or superseded, and fading by retention (`standing` to `scratch`) |
| Found by | `sdd search`, `sdd show`, and the grounding gate making an agent read it | the brief, prompt recall, `search` |

A memory entry about a decision points at the graph entry by its id and never restates it: two
copies drift, and a reader acts on whichever it opened. A project without a graph files its
decisions and its defects in memory, at `long` retention, since nothing else would keep them.

## Where it works

agentic-memory works only inside a git work tree, a fresh `git init` without commits included.
Outside one, every subcommand and every tool of the `memory` MCP server refuses with
`agentic-memory: <dir> is not inside a git work tree; memory works only in one`, every hook stays
silent, and nothing is read or created. `help` answers anywhere. The plugin option
`memory_require_git: false` lifts the limit for the whole machine: a directory outside git then
keeps a store keyed by its own path.

## Who writes a project's memory

A project's store is found by the `repo_id` its `.sdd/config.yaml` declares (the graph
repository's, where `project_dir` names one), which any checkout can declare. The checkouts that may use it are listed, by their git common directory, in the project's
one list `~/.claude/agentic-helpers/<project>/writers.json`, which agentic-tools reads too; every
worktree of a listed checkout is listed with it. The first call that writes an unlisted
project's store (a `claim`, a filing, any change) lists the calling checkout; a read (`search`,
`show`, `brief`, `topics`) claims nothing. While the project's list is missing, a library's own
`writers.json` from agentic-tools binds memory too, and moves into the project's list on the first
write. Any other checkout is refused every subcommand and memory MCP tool, its hooks stay
silent, and none of its calls migrates the store:

    agentic-memory: this memory belongs to other checkouts: <checkouts>; run `agentic-memory trust <this checkout>` from one of them to let this one write

`agentic-memory trust <path>` (MCP `trust`, key `path`), run from a listed checkout, adds the
checkout at `<path>`, for agentic-memory and agentic-tools both; `agentic-tools trust <path>` does
the same. `help`, `who` and `holder` answer anywhere, and claim no project; `who` frees dead claims
only for a listed checkout. A list that cannot be read (a
link, a pipe, a hard link, past 64 KiB, not an array of clean absolute paths) admits no one until it
is mended. A store keyed by a directory outside git, under `memory_require_git: false`, has no
declared key and is not listed.

## Seats

A **seat** is a named job, held by one session at a time; claiming one is what turns memory on for
a session, by a prompt or an instruction line. Every seat files onto a **shelf**, its own unless it
was claimed onto another (see [Shelves](#shelves)). Every entry carries the seat that filed it and
belongs to that seat's shelf, and a search covers the own shelf unless it names seats, so one seat
sees what another holds and nothing is done twice. A call finds its seat through the `claude` process that owns it, so a command run
from the Bash tool, a tool call to the plugin's MCP server and a hook all agree, also after `/clear`.

```bash
agentic-memory claim main
agentic-memory new --title 'Gate ignores MCP reads' --topic gate --retention normal <<'BODY'
reads are recorded only for Bash, so a search through MCP never counts
BODY
agentic-memory search --term mcp --query 'gate blind to tool calls'
agentic-memory supersede m-7q2k4f --by m-9xq0b1
```

A seat name is a word in any script: a letter, then up to 23 letters, combining marks, digits or
dashes, counted in characters (`main`, `haupt`, `главная`, `メイン`, `मुख्य`). Every name agentic-memory
reads (`claim`, `release`, `holder`, `--seat`, the MCP keys carrying a seat, `exempt_seats` in
`.sdd/grounding.yaml`) is normalised to Unicode NFC and lower-cased first, so `Café` typed
precomposed or decomposed names one seat with one file.

`rename '<new name>'` (MCP `rename`, key `name`) gives the seat the calling session holds a new
name; its id, its entries and its files stay, and a shelf of its own, carrying its old name, takes
the new one too. A name in use by a seat or a shelf, an invalid one, or one in a seat id's shape
(`s-` and 6 letters or digits) is refused. The old name is free afterwards, and claiming it starts a
new seat.

An agent asked for a seat in words that are not its exact name ("Haupt-Session") lists the seats
with `who`, maps the words onto the seat meaning the same, and asks when none or several fit.

A seat whose holder still runs is refused to every other session, always: nothing tells a stuck
holder from a busy one, and a busy one losing its seat would find every write refused without
noticing. The seat frees when its holder runs `release` or its session ends, which closes it; a
seat whose holder has gone is free to claim, and `who` frees every such seat of the project.

### Claude Code's own auto memory

A claim turns Claude Code's own auto memory off for the project, so agentic-memory is the one memory
an agent there files into and reads back. It adds `"autoMemoryEnabled": false` to
`.claude/settings.local.json` at the main checkout's root, the file Claude Code reads for the
project and every worktree of it, and says so once:

    agentic-memory: Claude Code's own auto memory is off for this project from the next session (.claude/settings.local.json); plugin option memory_turns_off_auto: false leaves it on

The setting takes effect from the next session; the claiming session keeps the memory it started
with. Every other key and the file's text stay as they were. A file already setting
`autoMemoryEnabled`, to either value, is the owner's setting and stays untouched, and a file that
does not parse is left alone with a line saying so, as is a `.claude/` or settings file that is a
symbolic link, which a claim never writes through. Where git does not ignore the file already,
`**/.claude/settings.local.json` is appended to git's global excludes file (`core.excludesFile`,
else `$XDG_CONFIG_HOME/git/ignore`, else `~/.config/git/ignore`), said once:
`agentic-memory: added **/.claude/settings.local.json to <file>, so git ignores it in every
repository`. A claim for another session (below) does the same, before that session starts.
`memory_turns_off_auto: false` leaves the file alone.

## Claiming for another session

A launcher seats the sessions it starts or keeps from outside them, by the session id it gives
`claude --session-id <uuid>`:

```bash
agentic-memory claim impl-7ah --session <uuid> --shelf row-7ah --lifetime 3d
```

- **For a session holding no seat it is a reservation**: the seat's claim names the session and no
  process, and the reply says `reserved <seat> for session <id> (shelf <name>)`. The session's first
  agentic-memory hook (SessionStart, else its first prompt or tool call) takes it over, and from
  then on the seat is held like any other. A reservation untaken for 10 minutes frees the seat. A
  reserved seat refuses every other claim, `<seat> is reserved for session <id> until it starts`,
  and keeps a temporary shelf from purging.
- **For a session holding a seat it is a handover**: in one step the session's seat closes and the
  named seat is claimed for the same running process, which acts as the new seat from its next call:
  `moved session <id> from <old> to <new> (shelf <name>)`. Its next hook tells it,
  `agentic-memory: seat moved from <old> to <new> (shelf <name>)`, followed by the new seat's brief.
- A seat held by another running session, or reserved for another session, is refused either way;
  so is a seat on another shelf than `--shelf` names. MCP: key `session` on `claim`.

## Shelves

A **shelf** is the memory one or more seats share. A seat claimed without `--shelf` gets a shelf of
its own, under its name, so a store where no seat shares behaves as one memory per seat. Several
seats on one shelf, as the agents working one board row, read and write one memory, while each
still holds its own seat and every entry still names the seat that filed it.

```bash
agentic-memory claim impl-7ah --shelf row-7ah   # a new seat onto shelf row-7ah, made where none exists
agentic-memory claim --shelf row-7ah            # a new seat row-7ah-<n>, the lowest free n, onto it
```

- **A seat stays on its shelf for good.** `--shelf` takes effect when the seat is first claimed; a
  later claim naming another shelf is refused, `<seat> is on shelf <name>`. MCP: key `shelf` on
  `claim`, and `name` left out for a minted seat.
- **Shelf names** follow the seat-name rule, no two shelves share one, and none takes an id's shape.
  A new seat claimed without `--shelf` under a name a shelf already carries is refused:
  `<name> names a shelf; claim it with --shelf <name>`.
- **A seat's memory is its shelf.** Its brief, a search without `--seat` or `--all-seats` and
  prompt recall treat every entry on the shelf as the seat's own; `search --seat <s>` narrows to one
  filer. The brief of a seat on a shelf of another name opens `agentic-memory: seat rev on shelf
  row-7ah, ...`.
- **Every seat on the shelf may retire, retain, retitle, tag and untag its entries.** A seat on another
  shelf is refused: `<id> is on shelf <name>`.
- **The seats on a shelf are its subscribers.** A shelf mate's new entry arrives as
  `agentic-memory: new for <seat> (<n>):`, and a retire, retain, retitle, tag or untag a shelf mate made
  arrives as `agentic-memory: changed on shelf <name> (<n>):`, one line per change,
  `<id> <title>: <what> by <seat>`, both through the paths shared entries arrive by.

## Temporary shelves

A shelf is **durable**, as every shelf is unless set otherwise, or **temporary**: it keeps its
entries while a seat on it is in use, and once its last seat has closed, a retention runs; when it
runs out, the shelf, every seat on it and every entry they filed are purged from the store, bodies
and events alike. Nothing of a purged entry is left to search, list, show or revive, and every name
it freed can be claimed anew, carrying nothing over. Claiming any seat on the shelf before the
retention runs out keeps everything.

```bash
agentic-memory lifetime temporary      # temporary, the default retention
agentic-memory lifetime 2d             # temporary, purged 2 days after its last seat closes
agentic-memory lifetime 0              # temporary, purged as its last seat closes
agentic-memory lifetime durable
agentic-memory claim impl-7ah --shelf row-7ah --lifetime 3d
agentic-memory drop row-7ah            # purge a temporary shelf no seat uses, at once
```

- **`lifetime durable|temporary|<span>`** (MCP `lifetime`, key `value`) sets the lifetime of the
  shelf of the seat the session holds, for every seat on it; `claim --lifetime` (MCP key `lifetime`)
  does it while claiming. A span is minutes, hours or days (`90m`, `12h`, `2d`) or `0`; `temporary`
  takes the default, the plugin option `temporary_seat_lifetime`, `1d` unless set. A value neither
  word nor span is refused. The reply says how the shelf ends:
  `shelf row-7ah is temporary: purged 2d after its last seat closes`.
- **A seat closes** when its holder runs `release`, or its session ends with SessionEnd for any
  reason but `clear` or `resume`, which leave the process and its seat in place. A holder gone
  without SessionEnd, a crash or a kill, closed at its transcript's last write. A seat in use, held
  by a running session, keeps the clock from starting.
- **The purge runs** at every SessionStart, in any project, over every store at the current format
  under `~/.claude/agentic-memory/`; a store holding no temporary shelf is only read. A retention of
  `0` purges inside the `release` or SessionEnd closing the last seat.
- **What survives**: an entry filed `--for` a shelf outside the purge, or `--for all`. It stays in
  its recipients' brief and search, names its filer as `<seat> (expired)`, and every seat on a
  shelf it was filed for may retire, retain, retitle, tag and untag it. Entries other shelves filed for the
  purged one stay their filers'.
- **`drop <shelf>`** (MCP `drop`, key `shelf`) purges a temporary shelf at once, from any process,
  and says `dropped <shelf> (<n> seats, <m> entries)`. It is refused while a seat on the shelf is in
  use, `<seat> holds a seat on <shelf>`, and for a durable shelf,
  `<shelf> is durable; make it temporary first`.

## Entries

An entry has a 3-10 word title, a free body, topics the agent chooses (one namespace per project,
merged with `merge`), a retention level setting how long it stays in view (`standing`, `long`,
`normal`, `short`, `scratch`) and a source (`--from user|agent|tool`). The tool stamps time, seat,
session, branch and commit, and anchors the entry to every file it names by content hash: a name of
letters, digits and marks of any script holding a dot or a slash, where a file exists under it,
with a trailing `:<line>` dropped.

Words are counted for the body against `entry_max_words` and for the title against 3-10, and the
similar-entry hint compares entries by their words. In scripts written without spaces (Han,
Hiragana, Katakana, Thai, Lao, Khmer, Myanmar) every character counts as a word; elsewhere a word is
a run between spaces.

An entry is written in the project's language, never in the language the conversation happens to
run in: the graph's `language` from `.sdd/config.yaml` (the graph repository's, where
`project_dir` names one), else the plugin option `memory_language`,
else `en`. The brief's first line names it, as in
`agentic-memory: seat main, 14 live, 6 retired, entries in de`. A domain term (a word naming a thing
in the project's code, documents or business, such as `Lieferschein`) keeps the project's spelling
and is never translated, so `--term` still finds the word the code uses; a gloss in the entry's
language may follow it in parentheses.

`tag <id> <topic>...` (MCP `tag`, keys `id` and `topic`) adds topics to an entry, and
`untag <id> <topic>...` (MCP `untag`, keys `id` and `topics`) removes them, naming each as the entry
reads now, through topic merges. Every entry keeps at least one topic, which `brief --topic` and the
topic counts rely on: removing the last one is refused,
`<id> keeps at least one topic; tag the right one first: agentic-memory tag <id> <topic>`, and a
topic the entry does not carry is refused as `<id> carries no topic <topic>`. Replacing an entry's
only topic is a `tag`, then an `untag`.

An entry id is `m-` and `entry_id_length` characters of `a-z0-9`, 6 unless set and never fewer, at
most 16; an id filed at another length keeps working.

## Shared entries

An entry can reach seats besides the one that filed it. A seat receives an entry when any of these
holds:

- the entry names its shelf: `new ... --for <name>`, repeatable (MCP key `for`, an array), a shelf
  name for that shelf or a seat name for the seat's shelf;
- the entry is for every seat, present and future: `--for all`, which is why no seat may be named
  `all`;
- the seat's shelf follows one of the entry's topics: `follow <topic>...` and
  `unfollow <topic>...` (MCP `follow`, `unfollow`, key `topic`) change what the shelf of the seat
  the session holds follows, for every seat on it, read through topic merges.

The filing seat's own shelf is never a recipient. A received entry stands with the seat's own: in
its brief, in a search without `--seat` or `--all-seats`, and in prompt recall at the own seat's
bar. Ownership stays the filing shelf's: only its seats retire, retain, retitle, tag or untag the entry,
while any seat may supersede or confirm it. An entry line names its recipients after the filer:
`[main → all, ...]`, `[main → idea, row-7ah, ...]`, or `[main → infra followers, ...]` where only a
follow brings it.

A seat hears about every entry it receives, once, as the lines `agentic-memory: new for <seat> (<n>):`
and one entry line each: at the next prompt, at the agent's next tool call, and, for a session idle
at its prompt, through a waiter that wakes it with a new turn.

## Links

A memory entry can link to a graph entry and to another memory entry: a full graph id
(`20261025-162357-d-tac-427`) or a memory id (`m-` and its characters). A short graph id
(`d-tac-427`) is never a link. Ids are per project.

- **From the text**: every full id the title or body names that resolves when `new` files the entry
  links it. One that resolves to nothing stays text, and the reply names it:
  `not linked (no such entry): m-zzzz99`.
- **`new --link <id>`**, repeatable (MCP key `link`): an id resolving to nothing refuses the filing,
  `agentic-memory: <id>: no such entry`.
- **`link <id> <target>`** and **`unlink <id> <target>`** (MCP `link` and `unlink`, keys `id` and
  `target`) add or drop a link later. Any seat may; the owner's brief lists it under changes by
  other seats. A link from the text holds as long as the text does: `unlink` on it refuses with
  `<target> is named in <id>'s text; supersede the entry instead`.

A link is read, never followed for bodies. A line flags each target that is no longer live, active or
open: `→ <target> closed by <id>`, `superseded by <id>`, `settled`, `retired`, or `gone` where the
graph entry's file is missing from the checkout. `← <n>` counts the entries linking to it. `show` adds
`links:` and `linked from:`. Recall and the brief name, under a hit, up to two entries linking to it
that were filed after it, newest first, title only: `← <id> <title> [<seat>, <date>]`. The
similar-entry hint offers both fixes: `replaces it → agentic-memory supersede <old> --by <new>` and
`relates → agentic-memory link <new> <old>`.

## Ending an entry

`retire <id>... [--because '<why>']` (MCP `retire`, key `because`) ends entries that are done or no
longer matter: they leave the brief and stay in search, flagged `retired <stamp>: <why>`, the reason
standing after the stamp where one was given. `supersede <id>... --by <id>` replaces entries and
`supersede <id>... --because '<why>'` marks them wrong; `revive <id>` undoes the latest of either
within 14 days. Only the seats on an entry's shelf retire or retain it; any seat may supersede or
confirm.

`supersede`, `confirm <id>...`, `retain <id>... <level>` and `retire` take several ids, and land all
or nothing: every id is checked against one load of the log (it exists, the seat may change it, its
state allows the change), and one failing id refuses the whole call, exit 1, naming every failing
id, with nothing written:

    agentic-memory: nothing superseded:
      m-b is already superseded
      m-c is on shelf main

Otherwise every change lands in one append and the reply names each id:
`superseded m-a, m-b, m-c by m-d`. Over MCP, `supersede`, `confirm` and `retain` take `ids`, an
array, and refuse `id`.

`new --absorbs <id>`, repeatable (MCP key `absorbs`, an array), files a summary and supersedes each
named entry by it, in the same append as the filing. An absorbed id that fails refuses the filing
the same way, as `nothing filed:`.

## Search

Search is sdd's: `--term` is a case-insensitive regex, every term required, matched against the
title, the body and the reason an ended entry gives; `--query` searches by
meaning; both fuse by reciprocal rank. Every result carries `drifted`, `gone` or `other branch` as
computed against the tree right then, and `confirm` re-anchors a verified entry. A `show` and a
search's top three hits add heat, which orders the brief; `--no-heat` (MCP `"no_heat": true`) reads
without it, for a sweep or a script no person is behind.

`--status live|retired|superseded` (MCP `status`, an array), repeatable, keeps only entries in the
named statuses; `--status superseded` includes superseded entries as `--with-superseded` does. Given
a status, search runs without `--term` or `--query` and lists: the most recently ended first for
retired and superseded entries, the most recently filed first for live ones. A listing matches
nothing, so it warms nothing.

A search returns `--limit` results, 10 unless set, and `--skip <n>` (MCP `skip`) drops the first
`n` before the limit cuts the page. A page with results past it ends with the next page's flag:
`14 more: --skip 10` where the remainder is exact (a `--term` search, a listing), `more: --skip 10`
where a `--query` takes part, since every candidate carries a score. A hybrid search's arms each keep
`skip + limit` candidates before fusion, so a later page is the fused ranking's next slice. Only the
first page warms its top hits.

## What comes back

A brief at every claim and compaction (standing entries, the seat's live entries by heat, what other
seats changed and filed, the topics), the turns since the seat's newest entry, and up to three
entries a prompt touches that stand out from the rest. Near compaction a nudge asks the seat to file
what the summary would lose; a session's last turns survive its end for the next claim. There is no
handoff to write.

The turns since the seat's newest entry come back after a compaction in at most five pieces of
9,000 characters, the newest kept. Once they would no longer fit, the next prompt asks the seat to
file, once per newest entry:

    Turns since your newest entry no longer fit the post-compaction replay. File where work stands: findings, open questions, judgment calls. agentic-memory new --title '<3-10 words>' --topic <name>... < body

Filing any entry moves the replay's start forward and re-arms the nudge, which counts toward neither
the compaction nudge's two nor the `Stop` backstop. A replay that still cuts turns opens with the
count and where the rest is:

    agentic-memory: the turns since seat <name>'s newest entry, in <n> pieces, newest last; <k> older pieces cut, the full transcript at <path>:

## MCP tools

Every subcommand is also a tool of the plugin's `memory` MCP server,
`mcp__plugin_agentic-helpers_memory__<subcommand>`, and `agentic-memory help` lists them. A tool
takes exactly the keys its schema lists, spelled exactly (`all_seats`, not `ALL_SEATS`), and its
schema says so with `additionalProperties: false`; any other key refuses the call with
`agentic-memory: <tool> takes no key "<key>"; it takes <keys>`.

## Seats of the project, and of the machine

`agentic-memory who` (MCP `who`) lists every seat of the current project, one row each, grouped by
shelf, then by seat:

    impl   shelf row-7ah  c3fb65eb pid 200  since 2026-10-25 23:10 CET
    rev    shelf row-7ah  reserved a1b2c3d4
    main   shelf main     free
    gone   shelf gone     free (holder a1b2c3d4 gone, freed)

It frees the project's claims whose holder is gone and its reservations nobody took over in 10
minutes, saying `freed` on their row; from a checkout not listed for the project it frees nothing.
`who --all` (MCP key `all`) adds every other project's seats on the machine, a migration's backups
aside, under a line naming the project key; it never writes to another project's store. `who`
migrates no store and runs anywhere: outside a project it says `no seat in this project` and, with
`--all`, lists the others.

## Other tools

Other tools read seats through `agentic-memory holder <seat> --json` and
`agentic-memory holder --session <id> --json`: exit 0 for a held seat, 1 for a free one, and 1
with `"reserved": true` beside `"alive": false` for a seat reserved for a session that has not yet
taken it over.

## Options

Set once per machine in the plugin's configuration dialog; no project overrides them.

| Option | Sets |
|---|---|
| `embedding_provider`, `embedding_model`, `embedding_endpoint`, `embedding_dimensions`, `embedding_api_key_env` | the embedder search by meaning and prompt recall run on |
| `entry_max_words` | the words a body should stay within, 500 unless set; longer bodies land with a note |
| `retired_body_days` | how long a retired entry keeps its body, 90 unless set, never under 14 |
| `entry_id_length` | the characters after `m-` in a new entry id |
| `memory_language` | the locale entries are written in where the project's `.sdd/config.yaml` names none; `en` unless set |
| `memory_require_git` | `true` unless set: memory works only inside a git work tree; `false` lets a directory outside git keep a store of its own |
| `memory_turns_off_auto` | `true` unless set: a claim turns Claude Code's own auto memory off for the project through its `.claude/settings.local.json`; `false` leaves the file alone |
| `temporary_seat_lifetime` | how long a closed temporary shelf keeps its entries, a span as `lifetime` takes one (`12h`, `1d`, `0`); `1d` unless set, and `1d`, with a note in the reply, where it holds no span |

Without an embedder, a search by meaning exits 1 naming the cause, a keyword search still answers,
and filing never waits: the next call that reaches the endpoint embeds what is pending.

## Off on the machine

The plugin option `memory_enabled` switches agentic-memory on or off for the machine, on unless set. Off, every memory hook says nothing, every subcommand exits 1 with `agentic-memory: off on this machine (plugin option memory_enabled)`, and the `memory` MCP server lists no tools, its instructions naming the option. The grounding gate's `exempt_seats` and the seat name agentic-tools stamps go off with it.
