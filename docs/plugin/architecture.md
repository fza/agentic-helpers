# The plugin: architecture

## Layout

| Path | Holds |
|---|---|
| `.claude-plugin/marketplace.json` | the marketplace, naming the plugin in `plugin/` |
| `plugin/.claude-plugin/plugin.json` | the plugin's manifest: its `version`, which names the release the launcher runs, and its `userConfig` options, none declaring a `default` and none of the `config.yaml`-only keys |
| `plugin/hooks/hooks.json` | every hook the plugin wires, each through `plugin/hooks/run` |
| `plugin/hooks/run` | the launcher |
| `plugin/hooks/register.ts` | the hooks module, writing agentic-memory's context reading |
| `plugin/.mcp.json` | the `memory` and `tools` MCP servers, each started through the launcher |
| `plugin/bin/` | the commands the Bash tool finds, each handing its call to the launcher |
| `plugin/skills/` | one skill per hook |
| `source/hooks/` | one Go module: a binary per hook and command under `cmd/`, everything else under `internal/` |

Claude Code loads every component it finds at a plugin's root, so nothing but the plugin's own
files goes into `plugin/`.

## Hooks

| Event | Binaries |
|---|---|
| SessionStart | `agentic-memory session-start`, `agentic-memory session-log`, `agentic-scratch start`, `agentic-grounding-gate start` |
| UserPromptSubmit | `agentic-memory prompt`, `agentic-tools prompt` |
| PreToolUse on `AskUserQuestion`, `Write`, `Edit`, `NotebookEdit`, `Bash`, `mcp__sdd__show`, `mcp__sdd__start_procedure`, `mcp__sdd__next`, `mcp__sdd__stage_attachment` | `agentic-grounding-gate gate` |
| PreToolUse on `Bash` | `agentic-shell-guard` |
| PermissionRequest on `Bash` | `agentic-shell-guard permission`, with a timeout past the longest countdown `shell_guard_ask_timeout` allows |
| PostToolUse on `Bash`, `mcp__sdd__search`, `mcp__sdd__view`, `mcp__sdd__next`, `Write`, `Edit` | `agentic-grounding-gate record` |
| PostToolUse on every tool | `agentic-shell-guard output`, `agentic-memory tool`, `agentic-tools notice` |
| Stop | `agentic-memory stop`, `agentic-tools stop`, and `agentic-memory wait` marked `asyncRewake` with a timeout past its 30-minute deadline |
| SessionEnd | `agentic-memory session-end`, `agentic-scratch end`, `agentic-grounding-gate end`, `agentic-tools end`, `agentic-helpers end` |

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
  failure line is said once per session, its marker under `<cache>/migration-failed/`.
- **Fences**: a stub is the old store's folder holding `{"format":999}`; a fence is a file starting
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
while that root exists, and every folder left empty below `~/.claude/agentic-memory`. `mcpserver.ServeIdle` runs it after two minutes
without a message, again after every further two; `agentic-helpers end` starts `agentic-helpers
sweep` in a new session (`setsid`) and returns.

Rejected: a migration per project at its first call (old trees and stores would coexist, and an
older session could write a project already moved); an explicit `agentic-helpers migrate` (every
component would refuse until someone ran it); a full copy as backup (it doubles every store's space
for a failure the renames already undo); fencing the caches (an older session's shell-guard would
lose its countdown state for nothing).

## Where the binaries come from

The plugin holds no binaries. Every hook runs through `plugin/hooks/run`, which runs the binary of
the release named by the plugin's own `version`, kept under the plugin's data directory
(`~/.claude/plugins/data/agentic-helpers-agentic-helpers/<version>/`):

1. `AGENTIC_HELPERS_BIN_DIR` set: the binary in that directory runs, and nothing is fetched.
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

A release is cut by starting the `release` workflow by hand (`gh workflow run release.yml`, or the
Actions tab). It runs the tests, then semantic-release reads the commits since the last tag: `fix:`
cuts a patch, `feat:` a minor, a `!` after the type a major, and every other type, `sdd:` included,
cuts nothing. A releasable run writes the new version into `plugin/.claude-plugin/plugin.json`,
commits it to `main` as `chore(release): v<version> [skip ci]`, tags that commit, and creates the
GitHub release with the generated notes; GoReleaser adds an archive per platform (darwin and linux,
arm64 and amd64) and `checksums.txt` to it. A breaking change marked without `!` ships as a minor.
Pull before pushing after a release: `main` carries the release commit.

## Tests

```bash
cd source/hooks && go vet ./... && go test -race ./...   # every Go suite
claude plugin validate .                                 # the plugin and marketplace manifests
claude plugin test plugin                                # the plugin's hooks module, register.ts
```

The rules for this code live in [`source/AGENTS.md`](../../source/AGENTS.md). Each suite drives its
binary's `Main` against a tree built in a temporary directory, with processes faked, never the real
ones. The plugin's suite runs the real `plugin/hooks/run` and `plugin/bin/` shims against a fake
`curl` and a temporary data directory, and checks that every hook in `plugin/hooks/hooks.json` goes
through the launcher and that the release builds every binary under `source/hooks/cmd/`. Every guard
is asserted from both sides: the same call is driven once with the evidence present and once with it
missing, and the two outcomes have to differ.

Pushes and pull requests run the same suites on Linux and macOS (`.github/workflows/test.yml`).
