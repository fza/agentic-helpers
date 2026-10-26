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
`.sdd/tmp/grounding-gate/`, one ledger per session. A ledger is replaced through a temporary file of
its writer's own, renamed over it (`internal/atomicfile`), so the parallel hooks of one call never
leave it half written; a torn ledger would refuse reads the session made. A ledger that cannot be
written holds the reads nowhere, so the gate refuses, the safe side. A grounded draft is recorded with the graph's
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

A command is parsed as bash with `mvdan.cc/sh/v3/syntax`, and `internal/shellwalk`, the walk
shell-guard judges through too, lists every command it runs: in a subshell, a pipe or a command
substitution, after a launcher (`env`, `xargs`, `sudo`, `find -exec`, ...) past the launcher's
options, assignments and counts, and inside code a shell runs (`bash -c`, `eval`, `trap`, a
here-document fed to a shell). A quoted argument and a heredoc body are words, never a call, so
crediting a quoted mention cannot let an `echo` satisfy the gate. Each call's words are read with
their quoting removed (`$'...'` decoded, a backslash inside double quotes dropped), and the search
mode, layout and depth are read from those words. A command's first word counts by its base name;
a `read_command` or `gather_command` is matched word for word after it, so each must be plain words
without quotes, variables or operators.

Only the calls the command itself runs are credited: a read inside code a shell runs is checked for
a discarded stream and its depth, but credits nothing. A stream counts as discarded where a
redirection in the same stretch of code (the command, or one piece of code a shell inside it runs)
sends output to `/dev/null`.

A command that does not parse is read from its text: wherever `sdd` or `agentic-capture` stands in
it as a word, it counts as a plain `sdd new`, so its reads stay owed even with shell-guard switched
off, and a discarded stream or a short show in its text is refused. It credits nothing.

## Seats

An exempt seat is the one the session's owning `claude` process claimed in agentic-memory's store,
found the way agentic-memory finds it, so a subagent of the session reads as the seat its session
holds.
