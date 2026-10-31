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

**How much of the dialogue stays the owner's is sdd's setting, not the gate's.** The sdd fork reads
`dialogue.mode` (`guided`, the default, or `autonomous`) from the user's own sdd config or
`.sdd/config.local.yaml`, never the committed `.sdd/config.yaml`: `sdd config set [--local]
dialogue.mode autonomous`. Under `autonomous` the agent starts and concludes moves and sessions
itself and answers a delegable chooser, the playback included, with its own `reason`; overriding a
failed pre-flight stays the owner's. A changed mode reaches new sessions only. The gate's reads and
credential scan hold under either mode.

**A graph write holding a credential is refused, whatever else holds.** A Bash `sdd new` or
`agentic-capture` command's text and every file a plain `sdd new` attaches, the `agentic-capture`
draft and every file its header's `attach` names, the `report` of `mcp__sdd__next`, the `params`
of `mcp__sdd__start_procedure` and a file staged through `mcp__sdd__stage_attachment` are scanned by
the shared [credential scanner](../secrets/usage.md), with stock sdd and the fork alike. The
session `label` of an engine call is not scanned: it names the dialogue and never reaches the
graph. On a finding the call is refused before any read, grounded draft or exempt seat is weighed,
since an entry is never edited and every session reads it:
`GROUNDING GATE: refused. The capture holds what looks like a credential (<rule ids>, <where>, line <n>); the graph is shared and immutable`.
`<where>` is `the command`, the draft or attached file as the command or header names it,
`the mcp__sdd__next report` / `the mcp__sdd__start_procedure params`, or `the staged <name>`. A scan
that could not finish refuses as `scan-incomplete`, except one the hook's own interrupt cut short:
that names no credential and refuses ahead of every other check but a credential found, since the
reads the gate would weigh next go unread too:
`GROUNDING GATE: refused. The hook was cut short before its credential scan finished, and nothing passes unscanned; run the call again`.

**What the gate cannot scan is refused**, once nothing else refuses the call:

- `GROUNDING GATE: refused. The capture names <file>, which cannot be read to scan for a credential; it must exist before the Bash call that captures it`:
  a draft or attached file not on disk when the call is judged, a draft written by the same
  command among them, or a draft header that does not parse.
- `GROUNDING GATE: refused. The capture attaches standard input as <name>, which cannot be scanned for a credential; attach a file instead`:
  an `--attach -:<name>`, in a draft header or on a plain `sdd new`.
- `GROUNDING GATE: refused. The capture names no draft that can be read to scan for a credential; name the draft file in the call`:
  an `agentic-capture` handed its draft in a way the gate cannot read, through `xargs` with no
  `.md` name in the command.
- `GROUNDING GATE: refused. The capture names <file>, which cannot be read to scan for a credential: <why>; attach it by a literal absolute path`:
  an attached file (a plain `sdd new`'s `--attach`, a draft's `attach`, a staged `path`) the gate
  will not place. It reads a literal absolute path, or a literal relative one in a command that
  neither changes directory nor puts a launcher in front of the call; `<why>` is
  `it holds a shell expansion` (any `$` or `~`), `it holds a shell glob or brace` (any argument
  of a plain `sdd new` or an `agentic-capture` call holding an unquoted `*`, `?`, `[`, an extended
  glob such as `@(...)`, or a brace expansion such as `{a,b}` or `{1..3}`, naming that word: the
  shell turns it into other words, so `run*.log` may attach files the gate never read and
  `--att{ach,x}` an `--attach` it never saw; quoted or escaped they stay text and are read, and a
  draft header's `attach` is no shell word; so is any call with a glob or brace in its command
  word, or in the subcommand of a call naming `sdd`, where any word of the call names `new` or
  holds `sdd` or `capture` once lower-cased and stripped of `[]{},*?`: `sd? new`, `{,}sdd new`,
  `[s]dd new`, `SD? new`, `x{,} sdd new`, `sdd n?w`, naming that word. The pattern is never
  matched, only refused, and code the parser cannot read, `sd[[=d=]] new`, is refused the same
  way. A patterned command naming neither, `./scripts/*.sh` or `l? -la`, is left alone),
  `it is relative after a directory change` (`cd`,
  `pushd`, `popd`, a launcher's chdir option, anywhere in the command), or
  `it is relative behind a launcher` (`env`, `sudo`, `xargs`, `nice`, `timeout`, `flock`,
  `ionice`, and every other launcher the shell walk knows).
- `GROUNDING GATE: refused. The <tool> call's input cannot be read to scan for a credential`:
  an engine call whose input is no JSON object.

The scanner stands against a credential written by accident, not an agent working around it; its
accepted limits are listed in the [scanner's architecture](../secrets/architecture.md#what-it-guards-against).

`agentic-capture` scans the exact bytes it hands sdd once more, right before each `sdd new`, so a
file changed after the gate's scan is still caught ([capture](../capture/architecture.md#credentials)).

**A question, a draft edit and a notebook edit under `drafts/` are let through, never refused.**
With reads owed the call goes ahead and the agent gets a note, opening
`GROUNDING GATE: let through, reads owed before the next question or draft.`; a draft edit's note
adds `This draft is not grounded: its capture owes the reads.`, since a draft saved while reads are
missing reads never grounds. A `.sdd/grounding.yaml` that does not parse lets them through as
`GROUNDING GATE: let through.` with the parse error. Claude Code draws any refusal of these tools as
a red error in its default view, while a note is never shown to the user. Every other check stays a
refusal, exit 2 with the reason on stderr: a capture, a plain `sdd new`, a discarded stream, a short
show and an MCP capture, each a call Claude Code folds into one grey line. A refusal for reads owed
opens `GROUNDING GATE: refused. Run the reads owed, then run this call again.`

**A note or refusal names only the reads still owed**, under one header, each as a short label
above the command that answers it; a read already credited is not named. A question asked with
both searches credited and no listing gets:

    GROUNDING GATE: let through, reads owed before the next question or draft.

    Owed since the graph last changed:
    listing of every topic
      sdd view --layout "active:as-counts"

`Empty search ≠ absence; a listing settles membership.` follows the reads only when a search is
owed.

**The note fires once per cause**: the cause is the set of reads missing under the graph as it
stands. Once it fired, later gated calls owing the same reads go ahead silently. A graph change, a
compaction clearing the reads' credit, or a changed set of missing reads arms it again, and the next
gated call carries it. Refusals are not rationed: a capture, a plain `sdd new`, a short show and a
discarded stream are refused with their full text on every call.

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
the reads `agentic-capture` owes. Every other MCP tool passes, apart from the project's own rules
below.

## A capture through sdd's engine

A capture through sdd's engine never passes through `agentic-capture`, so the gate holds it to the
two rules `agentic-capture` holds a draft to, wherever `.sdd/grounding.yaml` sets them: the
`listing_prefix` topic rule and the `draft_lint`. sdd's writing guide and pre-flight know nothing of
either. The gate checks `mcp__sdd__start_procedure` starting `capture` and every `mcp__sdd__next` to
a capture it saw start, reading the `params` or `report` and the `fields` of a chooser answer:

- **`listing_prefix`**: `topics` carrying no label opening with the prefix are refused:
  ``GROUNDING GATE: refused. These `topics` carry none opening with `<prefix>`, the `listing_prefix` `.sdd/grounding.yaml` sets: ...``.
  The playback's `confirm` is refused while the capture holds no such topic, seeded or reported:
  ``GROUNDING GATE: refused. This capture holds no topic opening with `<prefix>`, the `listing_prefix` `.sdd/grounding.yaml` sets, so its playback cannot be confirmed.``
- **`draft_lint`**: the body a call leaves the capture with is linted as `agentic-capture` lints a
  draft: written to a file in the gate's scratch directory, the command run on it from the
  project's root. A finding refuses the call, each one named with the file shown as `body` and its
  line counted from the body's first:
  ``GROUNDING GATE: refused. The project's draft lint (`draft_lint` in `.sdd/grounding.yaml`) reports on the body this call leaves, lines counted from the body's first:``.
  A lint that fails and prints nothing never read the body, and refuses as
  ``GROUNDING GATE: refused. The project's draft lint (`draft_lint` in `.sdd/grounding.yaml`) did not read the body this call leaves: <why>``.
- **A `bodyPatch`** is applied by the gate to the last whole body it saw for the capture, by sdd's
  own rule (each old text found exactly once, in order, all or nothing), and the result linted. A
  patch sdd would refuse passes the gate unlinted, since sdd refuses the report itself. A patch to a
  capture whose body the gate holds no record of is refused once, asking for the whole body:
  ``GROUNDING GATE: refused. This `bodyPatch` edits a body the gate holds no record of, so the project's draft lint (`draft_lint` in `.sdd/grounding.yaml`) cannot read the result.``
- **A `bodyHandle`**, a file staged through `mcp__sdd__stage_attachment` named as the body, has
  the text the gate recorded for that handle linted, after a leading header block opened and
  closed by a `---` line, as sdd reads it. The gate records each staging that went through, where
  `draft_lint` is set: the `content` given, the file a `path` names, or `patches` applied by sdd's
  edit rule to the file staged under that `name`; a staging it cannot follow (a patch it cannot
  apply, a file it cannot read, bytes whose sha256 differs from the one sdd answered with) drops
  what it held. A handle the gate holds no text for (staged before the hook ran, or the ledger
  lost) is refused once, asking to stage the file again or report the body as `body`:
  ``GROUNDING GATE: refused. This `bodyHandle` names `<handle>`, a staged file the gate holds no record of, so the project's draft lint (`draft_lint` in `.sdd/grounding.yaml`) cannot read the body.``

The gate learns a capture from its start, and keeps its body and topics per instance in its ledger
from every call that went through; neither a compaction nor a graph change clears them. A capture
that completed or was abandoned is dropped. A report to an instance the gate never saw start
passes, since it cannot be told from another procedure's. A seat `exempt_seats` names passes these
checks as it passes every other.

The staged text sits in the same ledger, by handle, and goes with it at session end. A project
without `draft_lint` records none, and a capture by `bodyHandle` passes there as any other. A
staging answer naming no handle records nothing; only a report or start params naming
`bodyHandle` can be refused. An sdd whose capture takes no `bodyHandle` never names one, so
nothing changes there.

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
offers that call ahead of the reads owed, however many are. A successful call through it counts as
a semantic search when it carries `--query`, a literal one when it carries `--term`, and a listing for each
`--area <name>` carrying the listing prefix; naming no `--area`, it counts as a listing of every area
its `--entry` subject carries. A call missing a mode still owes it. A project naming no
`gather_command` gets nothing credited for a call shaped like one.

```yaml
read_command: python3 scripts/graph.py      # plain words: no quotes, variables or operators
gather_command: python3 scripts/gather.py   # plain words too; absent, no call counts as one
read_suffix: --entry <entry>                # one line, printed only
listing_prefix: area-                       # the topic prefix a listing reads
draft_lint: vale --output=line              # the body's lint, see ../capture/usage.md and above
```

## Off on the machine

The plugin option `grounding_gate_enabled` switches the gate on or off for the machine, on unless set. Off, every gate hook lets every call through and keeps no evidence. `agentic-capture` goes off with it, since its grounding check is the gate's. With `memory_enabled` off, `exempt_seats` exempts nobody: a seat is an agentic-memory claim, and one left from before the switch must not exempt anything.
