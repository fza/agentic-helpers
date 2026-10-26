# The grounding gate: usage

An agent asked to reason about a decision graph will answer from the entry it already read, or from
what it believes the graph holds. Both are how a wrong answer gets written down with confidence.

## What it refuses

The gate holds a question, a draft edit or an `agentic-capture` call to real reads of the graph: a literal search and a semantic one, because each misses what the other finds, and a
listing, because an empty search result is never evidence of absence. A project naming a topic
prefix in `.sdd/grounding.yaml` (`listing_prefix: area-`) owes a listing of one topic carrying it;
every other project owes the view of every topic, `sdd view --layout "active:as-counts"`. It also
refuses a call that would discard the graph tool's own output, since a swallowed refusal reads
exactly like an empty graph, and a read that stops at the entry itself, since a body is immutable
and a later entry may have renamed every surface it names.

**A project serving sdd's engine captures through it.** Where `.mcp.json` declares
`mcpServers.sdd`, the gate refuses `agentic-capture` outright, whatever reads the session holds, and
names the engine's capture, `mcp__sdd__start_procedure` with `canonical: capture`: the engine plays
the entry back for the owner's confirmation and checks its summary, which `agentic-capture` does
not. `cli_capture: true` in `.sdd/grounding.yaml` lifts the refusal for a project capturing through
`agentic-capture` on purpose. A plain `sdd new` keeps its own rule.

**A question, a draft edit and a notebook edit under `drafts/` are let through, never refused.**
With reads owed the call goes ahead and the agent gets a note, opening
`GROUNDING GATE: let through, reads owed.` and naming what is still owed; a draft edit's note
adds `This draft is not grounded: its capture owes the reads.`, since a draft saved while reads are
missing reads never grounds. A `.sdd/grounding.yaml` that does not parse lets them through as
`GROUNDING GATE: let through.` with the parse error. Claude Code draws any refusal of these tools as
a red error in its default view, while a note is never shown to the user. Every other check stays a
refusal, exit 2 with the reason on stderr: a capture, a plain `sdd new`, a discarded stream, a short
show and an MCP capture, each a call Claude Code folds into one grey line.

The searches the gate suggests read five hits, header lines only, for a project on bare sdd:

    sdd search --query '<subject>' --limit 5 --max-citations 0
    sdd search --term '<literal>' --limit 5 --max-citations 0

The header lines show what the graph holds on the subject; a body is `sdd show`'s. An agent widens
with a higher `--limit` where the subject is broad, and a search counts as the read whatever its
limit. A project naming a `read_command` or a `gather_command` gets its reads spelled without these
flags, since its command need not take sdd's.

A plain `sdd new` without `--dry-run` owes the reads too, and since it names no draft, nothing
grounded stands in for them. `--help` or `-h`, on `sdd new` or on `agentic-capture`, writes nothing
and owes nothing. A capture counts however the shell spells it: after a runner, in a
subshell, inside `bash -c` or `eval`, fed to a shell by a here-document, under any path, in any
quoting. A command that does not parse as bash and names `sdd` or `agentic-capture` as a word owes
the reads as a plain `sdd new` does.

## sdd's MCP tools

sdd's MCP tools count and owe the same. `mcp__sdd__search` counts as the semantic read with `query`
and the literal read with `terms`, `mcp__sdd__view` as a listing by its `layout`, and only a call
that succeeded counts. `mcp__sdd__show` owes the same depth as `sdd show`, an omitted `down` or `up`
counting as the server's default (1 and 2), and `mcp__sdd__start_procedure` starting `capture` owes
the reads `agentic-capture` owes. Every other MCP tool passes.

## Grounded drafts

A capture also passes on a grounded draft: one a `Write` or `Edit` saved, as it stands now, while
every read stood, with the graph holding the same entries (their count and the newest). A draft
written through the shell, changed since, or captured after the graph moved owes the reads.

## How long the reads count

Credit for the reads lasts until the graph changes or the session's context is compacted, not
until the next prompt: a session that read the graph once works across prompts on an unchanged
graph without reading it again. A capture by any session adds an entry file, and so voids the credit
of every session, with one exception: a session's own capture, through `agentic-capture` or sdd's
MCP capture, keeps that session's credit when its entry is the graph's only change, one entry more
and the newest the one it wrote. A compaction voids this session's credit, since the reads' output is
gone from its context. Grounded drafts survive a compaction: their own graph state guards them.

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
cli_capture: true            # agentic-capture passes beside an sdd server in .mcp.json
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

## Off on the machine

The plugin option `grounding_gate_enabled` switches the gate on or off for the machine, on unless set. Off, every gate hook lets every call through and keeps no evidence. `agentic-capture` goes off with it, since its grounding check is the gate's. With `memory_enabled` off, `exempt_seats` exempts nobody: a seat is an agentic-memory claim, and one left from before the switch must not exempt anything.
