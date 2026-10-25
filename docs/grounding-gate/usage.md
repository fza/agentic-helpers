# The grounding gate: usage

An agent asked to reason about a decision graph will answer from the entry it already read, or from
what it believes the graph holds. Both are how a wrong answer gets written down with confidence.

## What it refuses

The gate holds a question, a draft edit or an `agentic-capture` call to real reads in the turn: a literal search and a semantic one, because each misses what the other finds, and a
listing, because an empty search result is never evidence of absence. A project naming a topic
prefix in `.sdd/grounding.yaml` (`listing_prefix: area-`) owes a listing of one topic carrying it;
every other project owes the view of every topic, `sdd view --layout "active:as-counts"`. It also
refuses a call that would discard the graph tool's own output, since a swallowed refusal reads
exactly like an empty graph, and a read that stops at the entry itself, since a body is immutable
and a later entry may have renamed every surface it names.

**A question, a draft edit and a notebook edit under `drafts/` are let through, never refused.**
With reads owed the call goes ahead and the agent gets a note, opening
`GROUNDING GATE: let through, reads owed.` and naming what the turn still owes; a draft edit's note
adds `This draft is not grounded: its capture owes the reads.`, since a draft saved in a turn
missing reads never grounds. A `.sdd/grounding.yaml` that does not parse lets them through as
`GROUNDING GATE: let through.` with the parse error. Claude Code draws any refusal of these tools as
a red error in its default view, while a note is never shown to the user. Every other check stays a
refusal, exit 2 with the reason on stderr: a capture, a plain `sdd new`, a discarded stream, a short
show and an MCP capture, each a call Claude Code folds into one grey line.

A plain `sdd new` without `--dry-run` owes the reads too, and since it names no draft, nothing
grounded stands in for them. A capture counts however the shell spells it: after a runner, in a
subshell, inside `bash -c`, under any path.

## sdd's MCP tools

sdd's MCP tools count and owe the same. `mcp__sdd__search` counts as the semantic read with `query`
and the literal read with `terms`, `mcp__sdd__view` as a listing by its `layout`, and only a call
that succeeded counts. `mcp__sdd__show` owes the same depth as `sdd show`, an omitted `down` or `up`
counting as the server's default (1 and 2), and `mcp__sdd__start_procedure` starting `capture` owes
the reads `agentic-capture` owes. Every other MCP tool passes.

## Grounded drafts

A capture also passes on a grounded draft: one a `Write` or `Edit` saved, as it stands now, in a
turn carrying every read, while the graph holds the same entries (their count and the newest). The
turn confirming a playback then need not repeat the reads the draft turn ran. A draft written
through the shell, changed since, or captured after the graph moved owes the reads in the capture
turn.

## Configuration: `.sdd/grounding.yaml`

A show owes `--down 2 --up 1` unless the file sets another depth, and a project can name seats that
owe none. A seat is agentic-memory's: the gate looks up the seat the `claude` process running the
hook claimed, so a session holding no claim owes the depth. The exemption lifts the depth alone;
every other refusal holds for every seat.

```yaml
show_depth:
  down: 3                    # a direction left out keeps its default; 0 owes nothing
  up: 1
  exempt_seats: [reviewer]
```

A project keeping a graph can still lift the gate. `enabled: false` switches it off for every
session, as if the project carried no graph; left out, the gate is on. Top-level `exempt_seats`
names seats the gate never refuses: a session holding one passes every check, the reads, the
discarded stream and the depth alike. A file that does not parse keeps the gate on and refusing.

```yaml
enabled: false               # left out or true: the gate runs
exempt_seats: [reviewer]     # seats the gate never refuses; show_depth's own lifts the depth alone
```

A project running its reads through a wrapper names it, and the refusals suggest reads the wrapper
accepts. `read_command` replaces `sdd` in every suggested read and counts as a read beside `sdd`
wherever the gate looks for one; `read_suffix` ends every suggested read and is never required. A
project naming neither gets bare `sdd`. Graph writes stay with `sdd new` and `agentic-capture`.

A project whose tooling runs every read in one call names it as `gather_command`, and the refusal
offers that call ahead of the separate reads. A successful call through it counts as a semantic
search when it carries `--query`, a literal one when it carries `--term`, and a listing for each
`--area <name>` carrying the listing prefix; naming no `--area`, it counts as a listing of every area
its `--entry` subject carries. A call missing a mode still owes it. A project naming no
`gather_command` gets nothing credited for a call shaped like one.

```yaml
read_command: python3 scripts/graph.py      # plain words: no quotes, variables or operators
gather_command: python3 scripts/gather.py   # plain words too; absent, no call counts as one
read_suffix: --entry <entry>                # one line, printed only
listing_prefix: area-                       # the topic prefix a listing reads
draft_lint: vale --output=line              # agentic-capture's lint, see ../capture/usage.md
```
