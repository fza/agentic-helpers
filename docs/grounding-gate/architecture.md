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
| PostToolUse on `Bash`, `mcp__sdd__search`, `mcp__sdd__view`, `mcp__sdd__start_procedure`, `mcp__sdd__next` | `record` | credits the reads the call performed; after the session's own capture (`agentic-capture` printing `<id>.md`, or a `next` result carrying `produced.entryId`), moves the ledger to the new graph state when that entry is the only change, keeping the reads; keeps the body and topics an engine capture's start or report left it with (`recordEngineCapture`) |
| PostToolUse on `Write`, `Edit` | `record` | grounds a draft saved under a `drafts/` (or `draft/`) directory while every read stands, hashing the file as saved |
| PreToolUse on `AskUserQuestion`, `Write`, `Edit`, `NotebookEdit`, `Bash`, `mcp__sdd__show`, `mcp__sdd__start_procedure`, `mcp__sdd__next`, `mcp__sdd__stage_attachment` | `gate` | for `AskUserQuestion`, `Write`, `Edit` and `NotebookEdit` (by `file_path` or `notebook_path` under `drafts/`), exits 0 with the refusal's text as `hookSpecificOutput.additionalContext`, its first line saying the call went ahead and, for reads owed, that they are owed before the next question or draft (`letThrough`); for every other call, refuses with exit 2 and the refusal on stderr; or passes with exit 0 and nothing |
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

`captures` holds each engine capture the session started, by its instance handle: the last whole
body and the topics the engine holds for it, and whether the gate already asked for the whole body
once. The serve a start answers with names the instance; the gate reads it whether the client hands
on the serve itself, the MCP result's `structuredContent`, or JSON in a text block. Only a call that
went through is recorded, a `bodyPatch` applied by sdd's own rule (`applyPatch`), and a serve
reporting the capture `completed` or `abandoned` drops it. Neither a graph change nor a compaction
clears `captures`: it mirrors the engine's state, not a read.

`noted` holds the missing reads the last let-through note named (`firstNote`). A gated call whose
missing reads match it gets no note. A different graph state clears it with the reads, a compaction
clears it with them, and a different set of missing reads no longer matches, so each of the three
fires the note again. A ledger that cannot be written leaves the note firing.

## The reads refusal

`refusals/reads.txt` holds the frame: its first line, the `gather_command` call where the project
names one, and the reads owed. `owedReads` fills the last from the ledger, one reason and command
for each read the ledger does not credit, and appends the search hint only when a search is among
them. A read the ledger credits is never named, so an agent is not sent to repeat it. On a gated
tool `letThrough` swaps the first line for the let-through one: the call already ran, so the reads
are owed before the next question or draft rather than before the call again. The note carrying
reads owed fires once per cause (see [the ledger](#the-ledger)); a refusal fires on every call.

## The engine capture checks

`engineCaptureRefusal` holds a capture start, once its reads stand, and every `mcp__sdd__next` to an
instance in `captures`, to the project's `listing_prefix` and `draft_lint`. It reads the start's
`params` or the report, with a chooser answer's `fields` folded in. Topics lacking the prefix refuse
(`refusals/engine-topics.txt`), and so does a `confirm` to the `playback` chooser while the held
topics lack it (`engine-confirm.txt`). The body a call leaves, a whole `body` or a `bodyPatch`
applied to the held body, goes through `internal/draftlint`, the runner `agentic-capture` shares:
written to `.body-*.md` in the ledger directory, run from the project's root, each finding named with
the file as `body` (`engine-lint.txt`); a run that fails silently refuses (`engine-lint-unread.txt`).
A patch with no held body refuses once (`engine-body.txt`) and passes after. The capture procedure's
field names, its `playback` step and sdd's edit rule are restated here, so a change to either in sdd
needs one here.

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
here-document fed to a shell). An option's value that is neither fixed text without a glob
or brace nor wholly double-quoted without an `@`, `*` or `!` expansion (`sudo -u $user sdd ...`) is
taken for the command, as it may split into one, so a read behind it is not credited. A quoted argument and a heredoc body are words, never a call, so
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

## Credentials

`gate` scans a graph write before anything else, the exempt-seat check included
(`internal/grounding/credentials.go`). A Bash call writes where the parse tree holds an
`agentic-capture` or `sdd new` call, or code that does not parse names either; unlike the reads,
a write into another repository's graph is scanned too. What it scans:

- **The command's whole text.**
- **A plain `sdd new`'s `--attach` files**, in both the `--attach value` and `--attach=value`
  forms, read against the working directory.
- **An `agentic-capture` draft**: the call's draft argument (its first word that is no flag or flag
  value, `~` and variables expanded), or, where the call names none (a draft handed over through
  `xargs`), every `.md` name in the command; read against the working directory, else the project.
  Then every file its header's `attach` names, read from the directory holding the governing
  `.sdd/`, where agentic-capture runs sdd.
- **An attach value** splits as sdd splits it, on the last colon: `source:target` reads `source`;
  `-:target` reads standard input. Every attached or staged file is scanned by its name in
  the entry, so path rules fire, and where that finds nothing, as nameless text, since gitleaks' global allowlist skips whole
  files by name (`.bin`, images, fonts, office files) and `secret.txt:secret.bin` would otherwise
  pass unscanned. Only a literal path is read, never expanded: an absolute one, or a relative one
  in a command with no directory change anywhere and no launcher in front of the call (the
  launchers and their chdir options are `shellwalk.Options`, the table the walk reads too).
  Anything a launcher or an expansion would change is refused, not resolved. A plain `sdd new` or
  `agentic-capture` call is refused outright where any argument is a glob or brace the shell
  expands into other words, since the words the gate reads are spelled, not expanded: an
  attachment it never read, or an `--attach` it never saw, could reach sdd. That is decided on the
  parsed word (`shellwalk.Patterned`), not its text: an unquoted, unescaped `*` or `?`, an
  unquoted, unescaped `[` with a `]` after it in the same word (a lone `[`, as the test command
  `[`, is text in bash), an extended glob, or a brace expansion; quoted or escaped, the same
  characters are text. A call
  the gate would never classify as a write is refused the same way, before classifying, where its
  command word, or the subcommand of a call naming `sdd`, is patterned at all and any word of the
  call names `new` or holds `sdd` or `capture`, lower-cased and stripped of `[]{},*?`. The gate
  does not match the pattern: bracket classes (`[=d=]`, `[.d.]`), case folding inside brackets and
  brace alternatives that shift every word along (`{,}sdd new`) each read differently in bash
  from what a matcher would guess, so naming a write tool anywhere in such a call is enough. Code
  the parser cannot read (`sd[[=d=]] new`, which bash runs and mvdan.cc/sh rejects) is refused
  where it holds `*`, `?`, `[` or `{` and names a write tool the same way. A call behind a
  launcher counts as `callsCapture` counts one: certain, or handed an argument.
- **A call behind a launcher's options** the walk cannot place (`sudo -iD dir sdd new`,
  `flock lockfile agentic-capture d.md`) still counts as a write: `sdd new`, or `agentic-capture`
  handed an argument. It credits and checks no read.
- **For `mcp__sdd__next` the `report`, for `mcp__sdd__start_procedure` the `params`**, decoded,
  every key and string in them one per line in key order: the JSON text itself is not scanned,
  since its escapes hide a token from the rules. The session `label` and the other fields naming
  the dialogue are not scanned: they never reach the graph.
- **`mcp__sdd__stage_attachment`**: every key and string of the call, one per line in key order (its
  inline `content` and each patch's `old` and `new` text among them), and the local file its `path`
  names, read against the session's working directory; all by the staged `name`. A patch is scanned
  as the text it writes, not the file it leaves (see the scanner's
  [accepted limits](../secrets/architecture.md#what-it-guards-against)).

A finding refuses with `refusals/credential.txt`, naming the rules on the first line found and that
line (`secrets.Describe`), never the matched text, ahead of every other check. A `scan-incomplete`
finding refuses like any other, except once the hook's context has ended: the scan it cut short
found no credential, so any finding then refuses with `refusals/credential-interrupted.txt`, after
a credential an earlier scan found and ahead of every other check, since under an ended context the
ledger reads as empty and the reads check would name reads owed that are not.

The gate fails closed: what it cannot scan refuses. Standard input (`refusals/credential-stdin.txt`),
a file that cannot be read or a draft header that does not parse
(`refusals/credential-unreadable.txt`), a capture naming no draft
(`refusals/credential-no-draft.txt`), a write call with any argument holding a shell glob or
brace, an attachment path it will not place, holding `$` or `~`, or relative after a directory change or behind a launcher (`refusals/credential-unresolved.txt`,
naming why), and an engine input that is no JSON object
(`refusals/credential-undecodable.txt`) refuse once no other
check does, so a turn still owing reads
hears that first; an exempt seat is refused too. A credential anywhere in the write outranks what
could not be scanned.

The gate's scan is the early refusal: it reads files before the Bash call runs, so a draft written
by the same command is unreadable to it, and a file changed afterwards escapes it. `agentic-capture`
closes that gap by scanning the exact bytes it hands sdd right before each `sdd new`
([capture](../capture/architecture.md#credentials)); a plain `sdd new` and the engine have no
second scan.
