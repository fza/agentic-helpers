# The grounding gate: architecture

`agentic-grounding-gate` runs in a project carrying `.sdd/`, at the project root or above it, and
does nothing elsewhere or where `.sdd/grounding.yaml` says `enabled: false`.

## Which graph

The hooks find a project's graph where sdd does. They walk up to the governing `.sdd/` and read its
`config.yaml`, overlaid by `config.local.yaml`. Where that sets `project_dir`, sdd's split layout,
the code repository's `.sdd/` holds only that key, and the project is the graph repository it names:
its `.sdd/` holds `grounding.yaml`, the gate's ledgers and `agentic-capture`'s lock. Entries live
under that project's `graph_dir`, `.sdd/graph` where unset. A relative `project_dir` or `graph_dir`
joins to the folder holding the `.sdd/` that sets it, never to the git toplevel. A `graph_dir`
leaving that folder reads as unset: sdd refuses it, and the split layout names its graph repository
through `project_dir` instead. A `project_dir` naming no `.sdd/` keeps the code repository's own
`.sdd/` and reads an empty graph, creating nothing in the missing checkout.

## Hooks

| Event | Mode | What it does |
|---|---|---|
| SessionStart | `start` | removes the ledgers of sessions that left a week ago without ending cleanly; with `source: compact`, forgets this session's reads and keeps its grounded drafts |
| PostToolUse on `Bash`, `mcp__sdd__search`, `mcp__sdd__view`, `mcp__sdd__next` | `record` | credits the reads the call performed; after the session's own capture (`agentic-capture` printing `<id>.md`, or a `next` result carrying `produced.entryId`), moves the ledger to the new graph state when that entry is the only change, keeping the reads |
| PostToolUse on `Write`, `Edit` | `record` | grounds a draft saved under a `drafts/` (or `draft/`) directory while every read stands, hashing the file as saved |
| PreToolUse on `AskUserQuestion`, `Write`, `Edit`, `NotebookEdit`, `Bash`, `mcp__sdd__show`, `mcp__sdd__start_procedure` | `gate` | for `AskUserQuestion`, `Write`, `Edit` and `NotebookEdit` (by `file_path` or `notebook_path` under `drafts/`), exits 0 with the refusal's text as `hookSpecificOutput.additionalContext`; for every other call, refuses with exit 2 and the refusal on stderr; or passes with exit 0 and nothing |
| SessionEnd | `end` | removes the session's ledger |

## The ledger

A ledger's reads carry `graph`, the graph's state when they were recorded: the count of entry files
under the resolved `graph_dir` and the newest entry's name, the state grounded drafts carry too. `record` compares it before
crediting a read, and a different state clears the query, term and listings first; `gate` treats
reads recorded under another state as none.

The gate keeps each session's credited reads and grounded drafts under
`.sdd/tmp/grounding-gate/`, one ledger per session. A ledger that cannot be written holds the reads nowhere, so the gate refuses, the safe side. A grounded draft is recorded with the graph's
entry count and newest entry at the time it was saved; a capture of it passes only while the draft
file is byte-identical and the graph still holds the same entries.

## The engine refusal

On `Bash`, a call running `agentic-capture`, recognised in every spelling the gate sees a capture
in, is refused with `refusals/engine.txt` when the project's `.mcp.json`, read from the project root,
declares `mcpServers.sdd`, the server name sdd's `mcp__sdd__*` tools carry, and the resolved
`.sdd/grounding.yaml` sets no `cli_capture: true`. Where the governing `.sdd/` names its graph
repository through `project_dir`, the server counts only where the installed sdd lists
`--project-dir` in `sdd init --help`: an sdd without it ignores the key, and its server would serve
the code repository's own `.sdd/`. The probe's answer is cached per binary. The check runs before the reads: credit decides
nothing here. A `.mcp.json` that is missing or does not parse declares no server. An sdd server at
user or local scope is not read.

## Crediting a read

A command is read twice. The shell-stripped form, with quoted spans emptied, says whether a call
happened, because crediting a quoted mention would let an `echo` satisfy the gate. The raw form says
what the call asked for, because the layout and the search mode live inside quotes. A
`read_command` or `gather_command` word is matched against the stripped form, so each must be plain
words without quotes, variables or operators, or no call would ever match it.

## Seats

An exempt seat is the one the session's owning `claude` process claimed in agentic-memory's store,
found the way agentic-memory finds it, so a subagent of the session reads as the seat its session
holds.
