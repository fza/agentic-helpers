# AGENTS.md

Canonical guidance for `agentic-helpers`, agent-agnostic. Claude Code loads it through the
`@AGENTS.md` import in [`CLAUDE.md`](CLAUDE.md), where anything Claude-Code-specific lives. **Go work
loads [`source/AGENTS.md`](source/AGENTS.md) too** whenever a session touches `source/`.

## Project overview

`agentic-helpers` is a set of machine-wide Claude Code hooks and the `agentic-capture` command, each
paired with a skill that tells the agent what it is doing. The grounding gate and `agentic-capture`
exist for [sdd](https://github.com/networkteam/sdd): sdd's decision graph under `.sdd/` motivates
them and drives what they check. They install once for every project on the machine and stay silent in
a project that does not carry the directory a hook works on. [`README.md`](README.md) describes each
hook's behaviour and how a project opts in.

| Path | Holds |
|---|---|
| `source/hooks/` | one Go module: a binary per hook and `agentic-capture` under `cmd/`, everything else under `internal/` |
| `skills/` | one skill per hook, linked into the global skills directory |
| `.sdd/` | the decision graph: why the hooks are the way they are |

## Setup and commands

Go 1.27 or newer.

```bash
go install -C source/hooks ./cmd/...   # build every hook into ~/go/bin (or GOBIN)
agentic-install                        # wire them into settings.json, link the skills
agentic-install --print                # show what would land, write nothing
cd source/hooks && go vet ./... && go test -race ./...   # every suite
```

**A hook runs from `~/go/bin`, not from this checkout.** A change reaches a session only after
`go install` runs again. Claude Code reloads its settings, so a running session picks up rewiring
at once.

## Working agreements

- **Never push.** Commit only when asked.
- **Never build to check that code compiles - run the tests.** `go vet ./...` inside `source/hooks`
  is the quick compile check.
- **A commit message is one line**: a Conventional Commits prefix (`feat:`, `fix:`, `docs:`,
  `chore:`, `refactor:`, `test:`, `build:`), then the description, 100 characters at most. Nothing
  follows the subject line.
- **The `sdd:` prefix belongs to that tool.** Never write it, never amend a commit carrying it.
- **One commit per coherent change.** Never fold an unrelated change into a commit whose message
  does not describe it.
- **A hook's output wording is part of its contract.** Skills and agents quote it. Change it on
  purpose, in the same change as every skill quoting it.
- **A change to what a hook does updates its skill and the README in the same change.**
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

`.sdd/` answers why the hooks are the way they are. `README.md` answers what they do.

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
