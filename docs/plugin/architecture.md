# The plugin: architecture

## Layout

| Path | Holds |
|---|---|
| `.claude-plugin/marketplace.json` | the marketplace, naming the plugin in `plugin/` |
| `plugin/.claude-plugin/plugin.json` | the plugin's manifest: its `version`, which names the release the launcher runs, and its `userConfig` options |
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
| SessionEnd | `agentic-memory session-end`, `agentic-scratch end`, `agentic-grounding-gate end`, `agentic-tools end` |

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
