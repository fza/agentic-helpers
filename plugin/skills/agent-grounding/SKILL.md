---
name: agent-grounding
description: 'Grounding a decision graph before asking, drafting or capturing: both search modes, the listing, read depths, ref kinds. Use when reading or writing entries in an sdd graph, when the grounding gate refuses a call or lets one through with a note, before putting a question or editing a draft, and before any capture. Only projects carrying a .sdd/ graph use this.'
license: MIT
---

# Grounding

Agent asked about a graph answers from the entry it already read, or from what it believes the graph
holds. Both → wrong answer, written down, confident.

**Only a project carrying `.sdd/`.** No graph → gate silent, nothing here applies.

**Gate holds on stock sdd and on the fork alike.** It reads commands only; every one it matches
exists in both.

**Gate on unless `.sdd/grounding.yaml` lifts it.** `enabled: false` → gate silent for every session.
Top-level `exempt_seats: [reviewer]` → a session holding that seat passes every check. Gate silent
→ the reads are still how an answer stays right, only nothing enforces them.

## Three reads, before any question, draft edit, or capture

**Credit lasts until the graph changes or the context compacts.** Read once, then work across
prompts on an unchanged graph. Another session's capture or a compaction → read again. Your own
capture keeps your reads, as long as its entry is the only change.

```bash
sdd search --query "<how somebody would phrase it>" --limit 5 --max-citations 0   # semantic
sdd search --term  "<literal token: flag, command, id>" --limit 5 --max-citations 0
sdd view --layout "active:as-counts"                   # every topic, no prefix set
sdd view --layout "topic(<prefix><name>):as-list"      # one topic, prefix set
```

- **Five header lines per search** (`--limit 5 --max-citations 0`): enough to see what the graph
  holds on the subject. Broad subject → widen with a higher `--limit`. A body → `sdd show`. Any
  limit counts as the read.

- **Which listing → `.sdd/grounding.yaml`.** `listing_prefix: area-` there → list one topic
  carrying it, whole. No file, no key → the view of every topic. The refusal names the one owed.
- **Which command → `.sdd/grounding.yaml` too.** `read_command` set → every read runs through it
  in place of `sdd`, and `read_suffix` ends every read: `python3 scripts/graph.py search --term x
  --entry <entry>`. Neither set → bare `sdd`, as spelled here. The refusal prints the project's form.
- **One call → `gather_command`.** Set → it runs all three: `--query` counts as semantic, `--term`
  as literal, each `--area <prefix><name>` as that listing, no `--area` as the `--entry` subject's
  areas. A flag left out → that mode still owed. The refusal prints the call.
- **Capture → the reads standing, or a grounded draft.** Grounded = saved through `Write`/`Edit`
  under a `drafts/` directory while all three stood, unchanged since, graph holding the same
  entries. Draft written through `Bash`, edited after, or another capture landed in between → run
  the three before `agentic-capture`.
- **Plain `sdd new` → the three reads, always.** No draft, nothing grounded. `--dry-run`, `--help`, `-h` exempt, on `agentic-capture` too.
  Wrapping it (`bash -c`, `eval`, subshell, `xargs`, full path, any quoting) changes nothing. A
  command that does not parse and names `sdd` or `agentic-capture` → owes the three too.
- **sdd's MCP tools count the same.** `mcp__sdd__search` `query` = semantic, `terms` = literal;
  `mcp__sdd__view` `layout` = the listing, same prefix rule. Only a call that succeeded counts.
  `mcp__sdd__start_procedure` with `canonical: capture` → the three reads, like `agentic-capture`.

- **Both modes. Always.** Semantic matches phrasing → misses other wording. Literal matches the
  token → misses everything phrased differently. Live decision already owning your subject → exactly
  what one-mode-only hides.
- **Empty result ≠ absence.** `sdd search` finds what it matched. `sdd show` reaches only what
  something cited. A listing → only read that settles what a topic holds.
- **`sdd info` once per session**, before the first search. `vector` where an embedding endpoint
  answers, `text` otherwise.

## Reading an entry

**Every grounding read → `sdd show <id> --down 2 --up 1`.** Depths are part of the command, not a
default to weigh. A project may owe another depth: `show_depth` in `.sdd/grounding.yaml` sets it,
and the refusal names the depth owed. Through MCP → `mcp__sdd__show` with `"down": 2, "up": 1`
spelled out: an omitted `down` reads as 1 and is refused.

```yaml
show_depth:
  down: 3                    # left out → 2; 0 → nothing owed that way
  up: 1                      # left out → 1
  exempt_seats: [reviewer]   # agentic-memory seats owing no depth
```

- Body immutable → surface it names keeps that spelling forever, including after a rename. Rename
  lives downstream, nowhere else.
- Status stays `active` throughout. Refinement does not close its target.
- `--down 2` → refinement of a refinement. `--up 1` → what the entry answered, which says whether it
  still applies.

Two exceptions the gate allows:

- **`prc`-layer rules entry.** Downstream chain = every entry ever captured under it.
- **Session holding a seat named in `exempt_seats`.** Seat = the `agentic-memory claim` of the
  `claude` process running the session; its subagents count as it. No claim → the depth is owed.
  Lifts the depth alone: a discarded stream, a question, a draft edit, a capture and a plain
  `sdd new` owe what they owe every seat. Top-level `exempt_seats` lifts all of them.

## Body = lead. Never ground truth.

0. **Search for a decision already owning it.** Active decision settles it → implementable, not
   open. Draft another → duplicate that disagrees the moment either changes. Marker: `addresses` on
   a live decision.
1. **Read the code it names, at the line it names.** `file:line` was true when written.
2. **Read the spec section under `docs/` governing it.** Entry describes code. Document says what
   code owes. They disagree more often than either admits.
3. **Verify every external claim against that party's own docs.** Provider limit, API absence, tool
   behaviour.

**Report which claims held, which did not.** Settled entry on a false premise ≠ one that was right.

| Failure shape | Reads as |
|---|---|
| Stale | tree moved, entry did not. Gap already closed |
| Wrong blocker | real defect named, governs another surface |
| Prose-only mechanism | function or recovery path exists only in the comment naming it |
| Wrong component | wrong binary or party. Check credential scope first |
| Already decided | body poses a question a live decision elsewhere answers |

## Ref kinds

Immutable. Get it right before capture.

| Kind | When |
|---|---|
| `refines` | narrows or corrects a **live** commitment, in place |
| `addresses` | supplies what an **active** target asked for |
| `builds-on` | forward step, or **closed** target |
| `grounded-in` | this entry reasons **from** the target |
| `required-by` | recorded from the prerequisite's side |
| `related` | floor kind. Understates every time. Pre-flight flags it |

## What the gate refuses, and what it lets through

**A question, a draft edit or a notebook edit is never refused.** Reads missing → the call goes
ahead, and a note follows it, opening ``GROUNDING GATE: let through, reads owed.`` and naming what
is still owed. A draft edit's note adds `This draft is not grounded: its capture owes the
reads.` Treat the note as the refusal it replaces: run the reads, then go on. A question already
asked → read before answering what it settled.

A broken `.sdd/grounding.yaml` lets them through as ``GROUNDING GATE: let through.`` with the
parse error. Fix the file.

| Refused | Why |
|---|---|
| `agentic-capture` where `.mcp.json` declares `mcpServers.sdd`, no `cli_capture: true` | the engine's capture plays the entry back and checks its summary; use `mcp__sdd__start_procedure` |
| capture, three reads missing, draft not grounded | writing what the graph never confirmed |
| `sdd new` without `--dry-run` or `--help`, three reads missing | same, by the path that skips the draft |
| `sdd` call sending a stream to `/dev/null` | wrong flag prints usage to stderr. Discarded → reads as an empty graph |
| `sdd show` short of the depth owed (`--down 2 --up 1` by default) | rename lives downstream; upstream says if it still applies |
| a write holding what looks like a credential: `sdd new`'s command or a file it `--attach`es, an `agentic-capture` draft or a file its `attach` names, an `mcp__sdd__next` report, `mcp__sdd__start_procedure` params, an `mcp__sdd__stage_attachment` file. `GROUNDING GATE: refused. The capture holds what looks like a credential (<rule ids>, <where>, line <n>); the graph is shared and immutable` | an entry is never edited and every session reads it. Reads, a grounded draft or an exempt seat change nothing: take the credential out (name the variable or the file holding it), then capture again. `scan-incomplete` = the scan could not finish |
| a write whose scan the hook's own interrupt cut short, ahead of every check but a credential found. `GROUNDING GATE: refused. The hook was cut short before its credential scan finished, and nothing passes unscanned; run the call again` | names no credential and no reads owed: the gate never saw the write whole. Run the same call again |
| a write the gate cannot scan, once nothing else refuses it. `GROUNDING GATE: refused. The capture names <file>, which cannot be read to scan for a credential; it must exist before the Bash call that captures it` / `GROUNDING GATE: refused. The capture attaches standard input as <name>, which cannot be scanned for a credential; attach a file instead` / `GROUNDING GATE: refused. The capture names no draft that can be read to scan for a credential; name the draft file in the call` / `GROUNDING GATE: refused. The capture names <file>, which cannot be read to scan for a credential: <why>; attach it by a literal absolute path` / `GROUNDING GATE: refused. The <tool> call's input cannot be read to scan for a credential` | write the draft or file in a call of its own first, attach a file rather than `-:<name>`, name the draft in the `agentic-capture` call, attach by a literal absolute path: `<why>` is `it holds a shell expansion` (`$`, `~`), `it holds a shell glob or brace` (unquoted `*`, `?`, `[`, `@(...)`, `{a,b}` in any argument of the `sdd new` or `agentic-capture` call, or a command word or `sdd` subcommand a pattern could turn into one, as `sd? new` or `sdd n?w`, named as `<file>`: quote it or name each file), `it is relative after a directory change` or `it is relative behind a launcher` |

## Traps

- **`--term`, repeatable. Not `--terms`.** Wrong flag → usage on stderr, non-zero exit.
- **Never `2>/dev/null` an `sdd` call.** Filter instead: `| grep -E '^  [0-9]'`.
- **A listing is enormous.** Another capture mid-pass invalidates it. Read the first run whole,
  then only what changed.
- **Capture through sdd's engine where `mcp__sdd__*` tools exist, else `agentic-capture`.** Never
  `sdd new` by hand. The `agentic-helpers:agent-capture` skill carries the rest.

## Capturing

- **Every entry carries at least one topic.** A project naming a listing prefix → at least one
  topic carrying it.
- **Name every surface the decision creates** - command, flag, RPC verb, config key, label, storage
  path, environment variable. "We'll add a command" → not settled. Exact spelling → settled.
- **No acceptance criterion waits on a decision the entry could take itself.**
- Body carries: **what**, **why**, **rejected alternatives with their reasoning**, **accepted
  costs**. No discarded alternatives → re-proposed within the month.

## Off on the machine

**Off on the machine** (`grounding_gate_enabled: false`) → the gate lets every call through and `agentic-capture` refuses `agentic-capture: off while grounding gate is off (plugin option grounding_gate_enabled)`. Run the reads anyway; capture is the user's to switch back on.
