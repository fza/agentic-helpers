# AGENTS.md

Canonical guidance for `agentic-helpers`, agent-agnostic. Claude Code loads it through the
`@AGENTS.md` import in [`CLAUDE.md`](CLAUDE.md), where anything Claude-Code-specific lives. **Go work
loads [`source/AGENTS.md`](source/AGENTS.md) too** whenever a session touches `source/`.

## Project overview

`agentic-helpers` is a set of machine-wide Claude Code hooks, shell-guard among them, and the
commands `agentic-capture`, `agentic-memory` and `agentic-tools`, each paired with a skill that tells
the agent what it is doing. The grounding gate
and `agentic-capture` exist for [sdd](https://github.com/networkteam/sdd): sdd's decision graph under
`.sdd/` motivates them and drives what they check. They install once for every project on the
machine and stay silent where a hook has nothing to work on: a project without the directory it
needs, a session holding no memory seat. Shell-guard always has something to work on, every Bash
call, so it judges every project. [`README.md`](README.md) says what the repository is and
how to install it; [`docs/`](docs/index.md) says how each piece is used and how it works.

| Path | Holds |
|---|---|
| `source/hooks/` | one Go module: a binary per hook, `agentic-capture`, `agentic-memory`, `agentic-tools` and `agentic-helpers` (the plugin's own `check`) under `cmd/`, everything else under `internal/`, the pieces and their ties in `internal/pieces` |
| `.claude-plugin/marketplace.json` | the marketplace, naming the plugin in `plugin/` |
| `plugin/` | the plugin and nothing else: its manifest, `.mcp.json` (the `memory` and `tools` MCP servers), `hooks/` (wiring, launcher, and `register.ts`, the hooks module writing agentic-memory's context reading), `bin/` (the commands the Bash tool finds) and `skills/`, one per hook, shipped as `agentic-helpers:<skill>` |
| `docs/` | one folder per piece, each with `usage.md` and `architecture.md` |
| `.sdd/` | the decision graph: why the hooks are the way they are |

## Setup and commands

Go 1.27 or newer.

```bash
go install -C source/hooks ./cmd/...   # build every binary into ~/go/bin (or GOBIN)
cd source/hooks && go vet ./... && go test -race ./...   # every Go suite
claude plugin validate .               # the plugin and marketplace manifests
claude plugin test plugin              # the plugin's hooks module, register.ts
```

**The hooks ship as a Claude Code plugin** from `plugin/`, and `plugin/hooks/run` runs the binaries
of the release named in `plugin/.claude-plugin/plugin.json`. Claude Code loads every component it
finds at a plugin's root, so nothing but the plugin's own files goes into `plugin/`; the repository's
`.mcp.json` and instruction files stay at the root. A machine
working on them sets `AGENTIC_HELPERS_BIN_DIR` to the `go install` directory, so a change reaches a
session on the next hook event after `go install` runs again. A release is cut only by the
`release` workflow; `docs/plugin/` describes both.

## Working agreements

- **Never push.** Commit every change that lands, without asking, as soon as its tests pass.
- **Never build to check that code compiles - run the tests.** `go vet ./...` inside `source/hooks`
  is the quick compile check.
- **A commit message is one line**: a Conventional Commits prefix (`feat:`, `fix:`, `docs:`,
  `chore:`, `refactor:`, `test:`, `build:`, `style:`), then the description, 100 characters at most.
  Nothing follows the subject line.
- **The `sdd:` prefix belongs to that tool.** Never write it, never amend a commit carrying it.
- **One commit per coherent change.** Never fold an unrelated change into a commit whose message
  does not describe it.
- **A hook's output wording is part of its contract.** Skills and agents quote it. Change it on
  purpose, in the same change as every skill quoting it.
- **A change that lands updates the `docs/` pages it makes untrue, in the same change.** A skill
  changes with it wherever the skill describes what changed.
- **Break every new guard, watch its test fail, and restore the file byte-identically.** A guard no
  test can fail is removed rather than kept.

## Authoring

- **State the what and the why, never how it came to be.** No comment, document, graph entry or
  commit message references a discussion, a session, or the order the work happened in. Rejected
  alternatives with their reasoning are welcome.
- **A comment only for what the code cannot say**: a rule imposed from outside the file, a mechanism
  a reader would otherwise reconstruct, a rejected alternative, an invariant a later edit would break.
- **No authored text states a count of what the tree holds.** Name the set instead: "every hook",
  not a number that goes stale on the next change.
- **No em dashes in outward-facing prose.** Commas, colons, periods, parentheses.

## The graph

`.sdd/` answers why the hooks are the way they are. `docs/` answers what they do.

- **A body is never edited, and no entry deleted.** Supersede a decision, close a signal.
  `sdd summarize` replaces a summary that misleads.
- **Search before a capture and before taking a direction**: `sdd search --query` and
  `sdd search --term` both, since each misses what the other finds. An empty result is never
  evidence of absence.
- **Every entry carries at least one topic.** Reuse a label where one fits.
- **Name every surface a decision creates**: a binary, a subcommand, a flag, a settings key, a path.

## Memory

**No parallel memory store for this project.** A rule about how the work happens here belongs in this
file, a decision about the hooks in the graph, and anything one developer's own in their gitignored
`CLAUDE.local.md`.
