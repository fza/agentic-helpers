# The grounding gate: architecture

`agentic-grounding-gate` runs in a project carrying `.sdd/`, at the project root or above it, and
does nothing elsewhere or where `.sdd/grounding.yaml` says `enabled: false`.

## Hooks

| Event | Mode | What it does |
|---|---|---|
| SessionStart | `start` | checks once that `sdd --version` carries `fza`, and otherwise turns the gate off for the session with one notice; removes the ledgers of sessions that left a week ago without ending cleanly |
| UserPromptSubmit | `turn` | opens a new turn: the reads credited so far stop counting, grounded drafts stay |
| PostToolUse on `Bash`, `mcp__sdd__search`, `mcp__sdd__view` | `record` | credits the reads the call performed |
| PostToolUse on `Write`, `Edit` | `record` | grounds a draft saved under a `drafts/` (or `draft/`) directory in a turn carrying every read, hashing the file as saved |
| PreToolUse on `AskUserQuestion`, `Write`, `Edit`, `NotebookEdit`, `Bash`, `mcp__sdd__show`, `mcp__sdd__start_procedure` | `gate` | refuses with exit 2 and the refusal on stderr, or passes with exit 0 |
| SessionEnd | `end` | removes the session's ledger |

## The ledger

The gate keeps each session's credited reads and grounded drafts under
`.sdd/tmp/grounding-gate/`, one ledger per session. A ledger that cannot be written holds the turn's reads nowhere, so the gate refuses, the safe side. A grounded draft is recorded with the graph's
entry count and newest entry at the time it was saved; a capture of it passes only while the draft
file is byte-identical and the graph still holds the same entries.

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
