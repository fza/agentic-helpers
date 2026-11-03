# The plugin: architecture

## Layout

| Path | Holds |
|---|---|
| `.claude-plugin/marketplace.json` | the marketplace, naming the plugin in `plugin/` |
| `plugin/.claude-plugin/plugin.json` | the plugin's manifest: its `version`, which names the release the launcher runs, and its `userConfig` options, every switch and picker declaring its built-in `default` and no text or number field one, and none of the `config.yaml`-only keys |
| `plugin/hooks/hooks.json` | every hook the plugin wires, each through `plugin/hooks/run` |
| `plugin/hooks/run` | the launcher |
| `plugin/hooks/register.ts` | the hooks module, writing agentic-memory's context reading |
| `plugin/.mcp.json` | the `memory` and `tools` MCP servers, each started through the launcher |
| `plugin/bin/` | the commands the Bash tool finds, each handing its call to the launcher |
| `plugin/skills/` | one skill per hook |
| `plugin/README.md` | what the plugin is, and everything it runs, sends, writes and decides, for a user reading the installed plugin |
| `source/hooks/` | one Go module: under `cmd/`, a binary per guarding hook program and `agentic-helpers`, which runs as `agentic-memory`, `agentic-tools`, `agentic-capture` and `agentic-scratch` under those names (its `links/` holds them as links for the release archive); everything else under `internal/` |

Claude Code loads every component it finds at a plugin's root, so nothing but the plugin's own
files goes into `plugin/`.

## Hooks

| Event | Binaries |
|---|---|
| SessionStart | `agentic-memory session-start`, `agentic-memory session-log`, `agentic-scratch start`, `agentic-grounding-gate start`, `agentic-helpers record` |
| InstructionsLoaded | `agentic-memory instructions` |
| UserPromptSubmit | `agentic-memory prompt`, `agentic-tools prompt`, `agentic-helpers record` |
| PreToolUse on `AskUserQuestion`, `Write`, `Edit`, `NotebookEdit`, `Bash`, `Monitor`, `mcp__sdd__show`, `mcp__sdd__start_procedure`, `mcp__sdd__next`, `mcp__sdd__stage_attachment` | `agentic-grounding-gate gate` |
| PreToolUse on `Bash`, `Monitor` | `agentic-shell-guard` |
| PreToolUse on `Agent`, `Task`, `Bash`, `mcp__plugin_agentic-helpers_memory__*` | `agentic-memory pre-tool` |
| PermissionRequest on `Bash`, `Monitor` | `agentic-shell-guard permission`, with a timeout past the longest countdown `shell_guard_ask_timeout` allows |
| PreToolUse and PermissionRequest on every tool | `agentic-helpers record` |
| PostToolUse on `Bash`, `mcp__sdd__search`, `mcp__sdd__view`, `mcp__sdd__next`, `Write`, `Edit` | `agentic-grounding-gate record` |
| PostToolUse on every tool | `agentic-shell-guard output`, `agentic-memory tool`, `agentic-tools notice`, `agentic-helpers record` |
| SubagentStop | `agentic-memory subagent-stop` |
| Stop | `agentic-memory stop`, `agentic-tools stop`, `agentic-helpers record`, and `agentic-memory wait` marked `asyncRewake` with a timeout past its 30-minute deadline |
| SessionEnd | `agentic-memory session-end`, `agentic-scratch end`, `agentic-grounding-gate end`, `agentic-tools end`, `agentic-helpers end`, `agentic-helpers record` |

### Session records

`internal/sessionrecord` keeps a session's record ([usage](usage.md#session-records)) under its
session id in `<cache>/session-records/sessions.slots`, a slot store as every per-session state is
(`internal/slots`): a write rewrites only that key's twin, readers take no lock, and SessionStart's
`ReclaimStale` drops a key unwritten for a week at most once a day. Each event is one `Update`
under the writer lock, so parallel hooks of one session never lose each other's write. The record
is JSON carrying its `schema`; one of another schema reads as no record, and the next event writes
a fresh one.

The Claude Code process is the nearest ancestor named `claude` (`AGENTIC_MEMORY_PROCESS_NAME`
renames it, as for memory's seats), its start read in whole seconds from the kernel as a claim's
is (`process.Table.StartTime`). Its controlling terminal comes from the kernel too: on darwin the
`e_tdev` of `kern.proc.pid`, a pseudo-terminal device named `/dev/ttysNNN` by its minor; on Linux
field 7 of `/proc/<pid>/stat`, named `/dev/pts/N` or `/dev/ttyN`. The name is kept only where the
device file at it is that device, so a reused name never passes for another terminal. The git
common dir is one `git rev-parse` bounded at two seconds, run before the writer lock is taken, and
only at SessionStart or the first event of a session without a record.

`record` and `sessions` read no settings and skip the migration every other `agentic-helpers`
command settles first: the record hook runs on every tool call, and the records live in a cache no
earlier release laid out. The hook decodes only the four payload fields it reads, and its session
id check is a byte loop rather than a pattern compiled at every start, so the program's package
initialisation stays as it was.

Rejected: a record per session as a file of its own, which brings back the thousands of small files
the slot store removed; cutting a long path or terminal id to fit, which names another one.

## The home

`internal/home` maps a checkout to its store ([usage](usage.md#the-home)). `home.Locate` settles the
two roots from `AGENTIC_HELPERS_HOME`, `XDG_DATA_HOME` and `XDG_CACHE_HOME`; `home.Inspect` reads
what a checkout is matched by: its git common dir, the pointer there, the volume UUID and inode of
that directory, its declared `repo_id`, its legacy key and, through `graphconfig.Link`, the graph
repository a split layout names. The remotes come from one `git config --get-regexp` call made only
once the pointer alone does not settle the store, since every hook would pay the spawn.

The volume UUID comes from `getattrlist(2)` with `ATTR_VOL_UUID` on darwin (x/sys carries the
constants but no wrapper, so the call goes through `syscall.Syscall6`) and from the
`/dev/disk/by-uuid` link whose device is the volume's on linux; elsewhere a checkout is matched by
pointer and path alone. Rejected: `st_dev`, which a remount changes. An inode match is checked
against the store's `repo_id` and origin as a path match is, since linux gives a removed directory's
inode to the next one created.

`home.Resolver` answers `Find` (never creating a store), `Ensure` (creating one where nothing
matches and no question stands), `Bind`, `BindNew`, `Merge`, `Adopt`, `Unclaim` and
`SweepUnclaimed`. `Adopt` and `Unclaim` write no `format.json`: the migration writes it last. A pointer naming a store that admits the checkout exactly as it stands answers
without a lock; everything else runs under `<data>/.lock`, reads the index, checks every hit
against the identity it names and rebuilds the index from every `identity.json` where one
disagrees, before a store is created, or where the file is missing. A pointer is confirmed by the
store admitting the checkout's git common dir, or its volume and inode, or no admitted checkout
still existing (a move across volumes); a pointer copied with a checkout confirms nothing and asks.
A checkout admitted under its git common dir, or its volume and inode, is updated in place. A
volume with no UUID (a tmpfs, a container's own disk) gives no inode identity, so a moved checkout
there matches no entry; where its pointer names the store and no admitted checkout still exists,
it replaces them instead of being added beside them.
Every write to an identity, a pointer or the index happens under the lock, and the index is
rewritten whole from the identities after each.

Where the code side of a split layout has no store, `mapping.joinGraph` in
`internal/home/resolve.go` decides whether it joins the graph repository's store: silently where
that store's `identity.json` already admits the checkout (by git common dir and path, or by volume
and inode, as after a bind or an earlier join), else with a `ReasonSplit` question naming the graph
folder and the graph store, nothing joined and no pointer written. The link is committed config a
clone brings along, so it counts no more than a shared origin, which asks too. Rejected: following
only `.sdd/config.local.yaml`, which would stop stock sdd's committed `graph_dir` and the fork's
`project_dir` from ever joining; requiring the graph repository to name the code repository back,
which no sdd release writes.

On darwin the cache root carries `com.apple.metadata:com_apple_backup_excludeItem`, the value
`tmutil addexclusion` writes without `-p` (`com.apple.backupd` as a binary property list), set
through `unix.Setxattr` so no `tmutil` process runs. `migration.Machine`, which every binary's
`main` settles first, registers the cache root with `safefs.OnCreate`, so `safefs.EnsureRoot` marks
it when it is the call making the root; it pays one lstat for that root alone. `agentic-helpers
check`, as the SessionStart hook, calls `Roots.ExcludeCache`, which reads the attribute and sets it
where the root exists without it. A failure to set it is ignored: the backup only costs more.
Rejected: `tmutil addexclusion` as a subprocess, a process per check where one system call does the
same; a path exclusion (`-p`), which needs root and breaks once the folder moves; a plugin option to
opt out, for a case nobody has. The data root carries no mark.

The home is bookkeeping, not a boundary: any process able to write it can edit a pointer or an
identity, which decides which store a call works on, never whether it may. `<data>/format.json`
versions the layout; each component keeps its own format beside its data.

## Writes

A process allowed to write part of the home, or a repository's committed files, can plant a link
where the owner's next write goes. So every file agentic-helpers creates, opens for writing, appends
to, renames or makes a folder for below a root goes through `internal/safefs`. The roots are the
data home (`$XDG_DATA_HOME/agentic-helpers` or `AGENTIC_HELPERS_HOME`), the cache home
(`$XDG_CACHE_HOME/agentic-helpers`), a checkout's top folder (`.tmp/` and its seat folders, a
draft's lint body), the folder holding a graph's directory (`.sdd/tmp/capture.lock`, the grounding
gate's ledgers) and a git common dir (the `agentic-store` pointer). The migration walks the old
layout under `.claude` from each path's own folder, and `agentic-memory read --to` from the resolved
work tree or `/tmp`. A root and every folder above it are the owner's and may be links: a symlinked
`~/.local/share` keeps working.

Below the root no path is opened whole. Each component is lstat-checked, refused as a link, opened
relative to its parent's descriptor through `os.Root` and held to the checked folder, so a folder
swapped for a link in between is caught; `os.Root` alone follows a link that stays inside its tree.
A new file is created with `O_EXCL`; a replaced file is written under a fresh random name and renamed
over the old one, so a link at the old name is replaced, never written through; an appended or
locked file must be the regular file of one name its folder names before a byte is written. A file
with a second name, a hard link, is refused wherever it is written. A refusal names the path:
`<path> is a link`, `<path> changed while it was opened`, `<path> is no regular file`,
`<path> has a second name, a hard link; register a copy of it`.

What stays on plain `os` lies outside every root: git's global excludes file, Claude Code's
`settings.local.json`, and the folder of `config.yaml` under the XDG config home (the file itself is
replaced through `safefs`, its folder as the root).

## The migration

`internal/migration` moves the layout of the releases before the home into it
([usage](usage.md#moving-from-claude)). Every binary's `main` calls `migration.Prepare` before its
`Main`. Where none of `~/.claude/agentic-memory`, `~/.claude/agentic-tools` and
`~/.claude/agentic-helpers` is a folder, it returns at once. Otherwise it takes
`<data>/.migration.lock` shared, and returns where `<data>/format.json` stands; else it takes the
lock exclusively, checks again, and migrates. The lock is its own file, not the resolver's
`<data>/.lock`: the run calls `Resolver.Adopt` and `Resolver.Unclaim`, which take that one.

- **Keys**: a folder two segments deep or more below an old root is a key where it holds a memory
  store (`seats/`, `log/`, `format.json` or `seats.json`), a tool library (`index.json`, `.lock` or a
  tool folder with `current`) or a writers list. A folder holding only a stub is none. The writers
  list is `~/.claude/agentic-helpers/<key>/writers.json`, else a library's own
  `~/.claude/agentic-tools/<key>/writers.json`; only clean absolute paths in a regular file count.
- **Owner**: a git common dir listed under several keys belongs to the key its checkout has now
  (`projectkey.Key`), else to the first listing it, so no two stores admit one checkout.
- **Offline**: a missing git common dir below a mount root (`/Volumes` on darwin; `/media`,
  `/run/media` and `/mnt` elsewhere) whose nearest existing folder is on the mount root's own device:
  the volume is gone, not just the checkout.
- **Moves**: by rename; across volumes (`EXDEV`) the source is renamed aside beside itself, copied,
  and the copy read back and compared by path, kind, mode and content hash; the aside copy is
  removed only after `format.json`.
- **Order**: the metadata backup, then each key (its store created, memory moved and stubbed, tools
  moved), the fences, the session tree with its readings nested, the attachments, and
  `format.json` last.
- **Journal**: every change is named in `<data>/.migration.journal` before it is made, one JSON
  object per line, appended and synced: a rename, a path created, a stub, a fence, a pointer's
  earlier content, the entries `<data>` held before, a tree to discard once done. Each undo checks
  the change happened before reversing it (a rename only where its target stands and its source
  does not, a stub or fence only while it still is one), so a record whose change a kill prevented
  undoes nothing; a path the run is about to create is refused where it exists. A last line cut
  short is a record a kill interrupted and is ignored.
- **Rollback**: a failure before `format.json` runs the journal's undo newest first, restoring each
  pointer and removing every entry of `<data>` the run created, then removes the journal. A run
  killed half way, as Claude Code kills a hook at its timeout, leaves the journal: the next call
  finding it takes the lock exclusively, runs the same undo (or, where `format.json` already
  stands, removes the trees set aside), removes the journal and migrates afresh. The journal goes
  only after `format.json`, so a call meeting both finishes the run rather than undoing it. The
  failure line is said once per session, its marker under `<cache>/migration-failed/<session>`
  holding the line, since a hook's stderr reaches no transcript; every call removes markers a week
  old, and every marker once the home is settled, since no failure is left to say. A settled home (`format.json` standing, no journal) is never locked: the old trees stand
  until the sweep, and a call that cannot open the lock, as a sandboxed one, would otherwise fail
  on every run.
- **Fences**: a stub is the old store's folder holding `{"format":999}`, beside Finder's `.DS_Store`
  at most; a fence is a file starting
  `agentic-helpers moved this data to <data>`. Each old tree's remainder is renamed aside and
  removed after `format.json`.

What an older release does with a fence, verified against v2.15.0: its writers list opens
`~/.claude/agentic-helpers` refusing a root that is a link or no folder, so memory admits no call
on an existing store (its hooks exit 0 silently, its commands and MCP tools refuse with the writers
line) and tools trust nothing; its tool library cannot create `~/.claude/agentic-tools/<key>`; a
directory-keyed memory store outside git reaches the stub and refuses it as a newer store.

The sweep (`migration.Sweep`) runs where `format.json` stands and a stub folder, a fence or an old
cache remains. It lists every process of the user whose name starts with `agentic-` (darwin:
`kern.proc.uid` and the path in `kern.procargs2`; linux: `/proc/<pid>/exe`) and stops where any
runs from another folder than its own binary's, or where the table cannot be read. It takes the
migration lock without waiting, then removes the old caches, each fence, each stub, the readings an
older session's hooks module (`register.ts`) keeps writing to `~/.claude/agentic-memory/sessions/`
while that root exists, and every folder left empty below `~/.claude/agentic-memory`, a folder
holding only `.DS_Store` files counting as empty: Finder writes one into every folder it shows, and
a tree once browsed would otherwise stand for good, the migration pending and a sweep owed at
every session's end. `mcpserver.ServeIdle` runs it after two minutes
without a message, again after every further two; `agentic-helpers end` starts `agentic-helpers
sweep` in a new session (`setsid`) and returns.

Rejected: a migration per project at its first call (old trees and stores would coexist, and an
older session could write a project already moved); an explicit `agentic-helpers migrate` (every
component would refuse until someone ran it); a full copy as backup (it doubles every store's space
for a failure the renames already undo); fencing the caches (an older session's shell-guard would
lose its countdown state for nothing).

## Format steps

A persisted component records its format in its folder's `format.json`, absent read as format 1,
and moves to the format a release writes through `internal/formatsteps`: the component declares a
step table, `[]formatsteps.Step{{From: 7, To: 8, Ops: ...}}`, exactly one step per format, each
moving one format on through an ordered list of operations (copy, rename, write a file, rebuild the
log, remove), and `formatsteps.Run` applies it:

1. read `format.json`; at the newest format the table reaches it returns, taking no lock;
2. refuse a format newer than the table reaches, `<component> has format <n>; this release reads up
   to <m>`, writing nothing;
3. take the component's lock and read the format again, refusing a newer one again, since a newer
   release may have moved the component while the call waited;
4. run each step from the format found: list its operations from what the folder holds now, apply
   them in order, and write `format.json` with the step's `To`.

A run stopped anywhere, by a failure or a kill, leaves the component at the format of the step it
stopped in; the next run lists that step's operations again from what the folder holds then and
applies them from the first. Every operation is therefore safe to run again from any point a kill
can stop it at, and one too large to split, a log rebuild, carries its own crash safety. A step
whose new format only keeps older binaries off rows they would misread has no operations: the
format is all it writes.

| Component | Table | Lock |
|---|---|---|
| memory store | `formatSteps`, `internal/memory/migrate.go` ([memory architecture](../memory/architecture.md#the-format-and-its-migration)) | the store lock |
| tool library | `libraryComponent`, `internal/tools/format.go` ([tools architecture](../tools/architecture.md)) | the library's lock, which every caller already holds |
| slot-file adoption | `File.Adopt`, `internal/slots/slots.go`: to format 2 taking in the per-session files, for shell-guard's state, the prompt vectors and the tools' seen list; to format 3 laying the fixed-width file out as a slot directory with its classes, for the prompt vectors and the seen list, while shell-guard's 4 KiB file, bounded by construction, stays fixed-width | the slot file's writers' lock |
| home | `Roots.Layout`, `internal/home/home.go`, an empty table: format 1 is the only one the home has held | none taken while the table is empty |

The move from the layout before the home (above) is no step of that last table: it moves trees
between two roots rather than one folder between formats, and stays on its journal.

The cut test proves a table per step: for every step and every k, it lays a fresh copy of a store of
the step's starting format, applies the step's first k operations as a run killed there would have,
runs the table again in a new process and compares the result with an uninterrupted run, the minted
seat and shelf ids named by what they stand for. `TestEveryStepCarriesOnFromAnyOperation` covers the
memory store, on the oldest hand-laid store and on every committed release;
`TestEveryLibraryOperationCarriesOn`, `TestEveryLayoutOperationCarriesOn`,
`TestAdoptionCarriesOnFromAnyOperation` and `TestAdoptionLaysAFixedFileOutFromAnyOperation` the tool
library and adoption. `TestEveryStoreKeepsItsSessionSlotsThroughTheLayoutStep` lays every committed
release's store out and checks every key of its slot file keeps its value, generation and time. A new step is covered by declaring its operations; no production code exists for the test,
and no operation carries a hook to stop at.

### Release fixtures

Every release proves it migrates what earlier releases wrote, on real data. `scripts/release-fixture
<version>` downloads the release's binaries for the machine from GitHub, checks them against the
release's `checksums.txt`, drives them through a scripted session in a temporary home and checkout
(two seats, an entry of every kind, a todo's items and due time, each seat's state, an attachment,
links, a retire and a supersede, the session hooks, tools added and taken, a shell-guard ask), and
writes what they left under `source/hooks/test/fixtures/releases/<version>/`: `data/` and `cache/`,
`checkout/`, and `dump.json`, the reads (`brief --json`, `show --json` of every entry, the tools
list) with what the release answered. The checkout's `.git` stays out, since a repository cannot
hold one; `@WORK@` stands for the folder the release ran in; a file past 64 KiB, a slot file, is
gzipped as `<name>.fixture-gz`. The network is reached in the script and never in a test.

`TestEveryCommittedReleaseMigratesToWhatItHeld` (`internal/formatsteps/releases_test.go`) copies
every committed release into a temporary folder, lets each component's runner move it, replays the
reads through the current `Main`s and compares them with `dump.json`, or with its successor where a
format changed an answer: `successors` holds one function per such format, format 8 turning a
seat's state slot into the ledger's `state#old`; format 9, which names every entry, adds to each
entry object the name the migrated store's index holds, after checking the index names every entry
the fixture filed, no two alike. It then checks every slot file the release left is a slot directory
once its component opened it, shell-guard's fixed-width file aside. `TestTheNamedReleaseHasAFixture` fails while no
fixture exists for the release `plugin/.claude-plugin/plugin.json` names.

Rejected: hand-built fixtures per format, which show what format N was thought to be, not what a
release wrote; a CI job running the old release, which breaks the no-network rule for tests, proves
nothing locally and covers nothing the committed fixture does not. The stores below format 7 stay
hand-laid until fixtures of those releases exist. Accepted costs: the script, a fixture of a few
hundred KB per release, and its step in the release.

Rejected: rebuilding every derived file from the log, since the seat registry and the shelves hold
ids minted outside the log, and the log itself still needs steps; copying a component forward into a
folder per format, which older binaries keep writing, losing their writes, while the disk holds both;
reusing `internal/migration`'s write-ahead journal, an undo for every change, a journal format to keep
stable, and a hook to halt at that only tests use.

## Where the binaries come from

The plugin holds no binaries. Every hook runs through `plugin/hooks/run`, which runs the binary of
the release named by the plugin's own `version`, kept under the plugin's data directory
(`~/.claude/plugins/data/agentic-helpers-agentic-helpers/<version>/`):

1. `AGENTIC_HELPERS_BIN_DIR` set: the binary in that directory runs, and nothing is fetched. For
   `agentic-memory`, `agentic-tools`, `agentic-capture` and `agentic-scratch` the launcher first
   links the name to `agentic-helpers` beside it where the name is missing, or is a program an
   earlier build left that `agentic-helpers` is newer than, since `go install` builds
   `agentic-helpers` alone. The link is made under a name of its own and renamed into place, so a
   hook starting at the same moment never finds the name missing.
2. The plugin's version is there: it runs.
3. At SessionStart, or for a command an agent runs (a `bin/` shim, the `memory` or `tools` MCP server), the
   release archive for this machine and the release's `checksums.txt` are downloaded; the archive
   is unpacked only where its sha256 matches. A prompt or a tool-call hook never downloads.
4. Otherwise the newest version already there runs. A session that cannot write the data directory,
   such as a sandboxed job, runs what an earlier session fetched.
5. No binary at all: the grounding gate refuses every call it guards and shell-guard every Bash
   call, each with a message naming the fix, rather than letting work run ungrounded or unguarded;
   shell-guard's output scan says on stderr, exit 1, that tool output reaches the agent unscanned;
   every other hook does nothing. The SessionStart that failed to fetch says why, once.

The archive is unpacked in the fetch's work directory and renamed into place, so a version
directory is complete or absent.

The archive holds `agentic-shell-guard`, `agentic-grounding-gate` and `agentic-helpers` as
programs, and `agentic-memory`, `agentic-tools`, `agentic-capture` and `agentic-scratch` as links
to `agentic-helpers`, which `tar` keeps as links. `agentic-helpers` picks its command by the base
of the path it runs under, so the launcher runs `<version>/agentic-memory` as it runs any other
name, and an earlier release whose archive holds a program under each name runs unchanged. Each
program carries its own copy of the Go runtime and of the packages it shares, so one program for
the four commands and `agentic-helpers` keeps one copy: a release unpacks to 25 to 28 MB across
darwin and linux, amd64 and arm64, where a program per name took 50 MB. The hook programs that
refuse a call when they fail stay apart: a fault in the startup code of a memory, tool-library or
scratch package (a Go panic exits 2, which Claude Code reads as a refusal) breaks every name of
`agentic-helpers`, never a Bash or graph call, and shell-guard's start loads only its own packages.
agentic-scratch refuses nothing: it runs only at SessionStart and SessionEnd, so such a fault
leaves a session's scratch folder unprepared and unswept, and its start pays the other commands'
package setup, under a millisecond. agentic-memory starts a landed-commit round from its own name
beside the program, since Linux names a running program by the file its links point to. Building
every program and the links is `.goreleaser.yaml`'s.

A SessionStart hook that runs a binary from a version directory first writes
`<data>/<version>/used` with the shell's own `echo`, so no process is spawned for it; a directory
it cannot write leaves the mark as it was and changes nothing else. The daily `agentic-helpers
prune` (`cacheprune.Run`, [usage](usage.md#cache-pruning)) reads that mark: it keeps the version
the plugin's `plugin.json` names (read from `CLAUDE_PLUGIN_ROOT`), every version a process of the
user named `agentic-*` executes from (darwin: `kern.proc.uid` and the path in `kern.procargs2`;
linux: `/proc/<pid>/exe`), compared against the data directory as given and with its links
resolved, and every version whose mark is under 14 days old, and deletes every other folder named
as a version. A session still running an older plugin version runs that version's programs, and
without them its grounding gate and shell-guard refuse every call, so a folder goes only once no
session can need it. Rejected: keeping the newest three, a fixed bound of about 150 MB, which
leaves a session running an older version without its programs; leaving every release, about
50 MB more per release without end.

## The fetch lock

Claude Code starts the MCP server alongside the SessionStart hooks, and the SessionStart hooks run
in parallel, so the first to fetch holds a lock, `.fetch-<version>` in the data directory, and the
rest wait for it, up to 100 seconds.

- **Owner**: the lock is a symlink whose target is the fetching process's pid, created with
  `ln -s`. `symlink(2)` creates the name and its target in one call and fails on a name that exists,
  on darwin and Linux alike, so no lock is ever visible without its owner. A lock directory with the
  pid written into it afterwards leaves a gap a `SIGKILL` can land in, and renaming a prepared
  directory into place cannot refuse an existing one, since macOS `mv` has no `-T`.
- **Work**: the fetch downloads and unpacks in `.fetch-<version>.<pid>/`, beside the lock.
- **Signals**: on `HUP`, `INT` or `TERM` the fetch removes its lock and its work and stops its
  download, which runs in the background so the trap fires at once. Claude Code kills an MCP server
  that has not connected within 30 seconds, and the server fetches first, so a fetch can be killed
  mid-download.
- **Takeover**: a waiter finding the owner's process gone, as after `SIGKILL`, removes the lock and
  that owner's work at once and fetches itself; a `.fetch-<version>.takeover` directory keeps two
  waiters from both doing so. A lock whose owner runs is waited on, however old.
- **Earlier launchers** took the lock as a directory, `.fetch-<version>/`, with the pid in its
  `owner` file. Such a lock is still honoured: waited on while its owner runs, taken over once its
  owner is gone, and, with no `owner` file, taken over once older than five minutes. `ln -s` onto
  an existing directory puts its link inside and succeeds, so a fetch takes the lock only when the
  symlink names its own pid, and removes the stray link otherwise.

## The release

Before each release, the fixture of the release before it is committed: `scripts/release-fixture
v<version>` for the version `plugin/.claude-plugin/plugin.json` names, then `go test -race ./...`
in `source/hooks` and a `test:` commit ([release fixtures](#release-fixtures)). The `release` workflow
runs the tests first, so a release cannot be cut until that fixture stands.

A release is cut by starting the `release` workflow by hand (`gh workflow run release.yml`, or the
Actions tab). It runs the tests, then semantic-release reads the commits since the last tag: `fix:`
cuts a patch, `feat:` a minor, a `!` after the type a major, and every other type, `sdd:` included,
cuts nothing. A releasable run writes the new version into `plugin/.claude-plugin/plugin.json`,
commits it to `main` as `chore(release): v<version> [skip ci]`, tags that commit, and creates the
GitHub release as a draft with the generated notes; GoReleaser finds that draft by its name
(`v<version>`), adds an archive per platform (darwin and linux, arm64 and amd64) and
`checksums.txt` to it, and publishes it. The repository's releases are immutable, so nothing can be
added to a release once it is published: the draft is what lets the archives land at all. A
breaking change marked without `!` ships as a minor.
Pull before pushing after a release: `main` carries the release commit.

## Skill budgets

A skill's `SKILL.md` loads whole into context each time it is needed, so its growth is paid on every
load. Each one has a budget in characters, `skillBudgets` in
`source/hooks/internal/plugin/skillbudget_test.go`, recorded there with the reason for its figure;
`TestEverySkillFitsItsBudget` fails where a `SKILL.md` exceeds its budget, where a skill has none,
and where a budget names no skill. Each figure sat a little above the skill's size when it was set.
Raising one is a change of its own, stating what the added text earns. Rejected: one budget for the
whole skill directory, under which one file's growth hides behind another's cut.

A skill too large for its budget splits into a core plus reference files beside it, as
`agent-memory` and `agent-shell-guard` do: the core keeps what an agent acts on at a refusal (every
reason it meets and its fix) and names each reference file from its "Read when" table; a reference
file loads only when its trigger shows up and carries no budget.
`TestEveryReferenceFileIsNamedFromItsSkill` fails where a reference file is linked from no
`SKILL.md`, and shell-guard's `TestTheSkillQuotesEveryRefusal` holds every reason to the core.

## Tests

```bash
cd source/hooks && go vet ./... && go test -race ./...   # every Go suite
scripts/linux-test                                       # every Go suite on Linux, in a container
claude plugin validate .                                 # the plugin and marketplace manifests
claude plugin test plugin                                # the plugin's hooks module, register.ts
```

The rules for this code live in [`source/AGENTS.md`](../../source/AGENTS.md). Each suite drives its
binary's `Main` against a tree built in a temporary directory, with processes faked, never the real
ones. The plugin's suite runs the real `plugin/hooks/run` and `plugin/bin/` shims against a fake
`curl` and a temporary data directory, and checks that every hook in `plugin/hooks/hooks.json` goes
through the launcher and that the release builds every binary under `source/hooks/cmd/` and packs
every name under `cmd/agentic-helpers/links/` as a link to `agentic-helpers`. The folded program's
own suite starts the test binary under each name through a link and checks that the name runs its
command. Every guard
is asserted from both sides: the same call is driven once with the evidence present and once with it
missing, and the two outcomes have to differ.

Pushes and pull requests run the same suites on Linux and macOS (`.github/workflows/test.yml`).
`scripts/linux-test` runs `go vet` and `go test -race` on a macOS machine inside a Linux container
of Apple's `container` tool, with the checkout mounted, the suites run as the calling user (as root,
every test of a permission refusal would pass), and Go's caches kept in `.tmp/linux-test/`; its
arguments go to `go test`. The README's Testing section says how to start the service.
