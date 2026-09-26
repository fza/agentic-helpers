---
name: agent-grounding
description: 'Grounding a decision graph before asking, drafting or capturing: both search modes, the listing, read depths, ref kinds. Use when reading or writing entries in an sdd graph, when the grounding gate refuses a call, before putting a question or editing a draft, and before any capture. Only projects carrying a .sdd/ graph use this.'
license: MIT
---

# Grounding

Agent asked about a graph answers from the entry it already read, or from what it believes the graph
holds. Both → wrong answer, written down, confident.

**Only a project carrying `.sdd/`.** No graph → gate silent, nothing here applies.

## Three reads, before any question, draft edit, or capture

```bash
sdd search --query "<how somebody would phrase it>"    # semantic
sdd search --term  "<literal token: flag, command, id>"
sdd view --layout "active:as-counts"                   # every topic, no prefix set
sdd view --layout "topic(<prefix><name>):as-list"      # one topic, prefix set
```

- **Which listing → `.sdd/grounding.yaml`.** `listing_prefix: area-` there → list one topic
  carrying it, whole. No file, no key → the view of every topic. The refusal names the one owed.
- **Capture turn → the reads again, unless the draft is grounded.** Grounded = saved through
  `Write`/`Edit` under a `drafts/` directory in a turn carrying all three, unchanged since, graph
  holding the same entries. Draft written through `Bash`, edited after, or another capture landed
  in between → run the three before `agentic-capture`.
- **Plain `sdd new` → the three reads, always.** No draft, nothing grounded. `--dry-run` exempt.
  Wrapping it (`bash -c`, subshell, `xargs`, full path) changes nothing.

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
and the refusal names the depth owed.

```yaml
show_depth:
  down: 3                    # left out → 2; 0 → nothing owed that way
  up: 1                      # left out → 1
  exempt_seats: [reviewer]   # carry-forward seats owing no depth
```

- Body immutable → surface it names keeps that spelling forever, including after a rename. Rename
  lives downstream, nowhere else.
- Status stays `active` throughout. Refinement does not close its target.
- `--down 2` → refinement of a refinement. `--up 1` → what the entry answered, which says whether it
  still applies.

Two exceptions the gate allows:

- **`prc`-layer rules entry.** Downstream chain = every entry ever captured under it.
- **Session holding a seat named in `exempt_seats`.** Seat = the carry-forward claim under
  `.memory/roles/` naming the session; its subagents count as it. No claim → the depth is owed.
  Lifts the depth alone: a discarded stream, a question, a draft edit, a capture and a plain
  `sdd new` owe what they owe every seat.

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

## What the gate refuses

| Refused | Why |
|---|---|
| question or draft edit, three reads missing | answering from memory |
| capture, three reads missing, draft not grounded | writing what the graph never confirmed |
| `sdd new` without `--dry-run`, three reads missing | same, by the path that skips the draft |
| `sdd` call sending a stream to `/dev/null` | wrong flag prints usage to stderr. Discarded → reads as an empty graph |
| `sdd show` short of the depth owed (`--down 2 --up 1` by default) | rename lives downstream; upstream says if it still applies |

## Traps

- **`--term`, repeatable. Not `--terms`.** Wrong flag → usage on stderr, non-zero exit.
- **Never `2>/dev/null` an `sdd` call.** Filter instead: `| grep -E '^  [0-9]'`.
- **A listing is enormous.** Another capture mid-pass invalidates it. Read the first run whole,
  then only what changed.
- **Capture through `agentic-capture`.** It lints the draft first; nothing lints an entry after.
  The `agent-capture` skill carries the rest.

## Capturing

- **Every entry carries at least one topic.** A project naming a listing prefix → at least one
  topic carrying it.
- **Name every surface the decision creates** - command, flag, RPC verb, config key, label, storage
  path, environment variable. "We'll add a command" → not settled. Exact spelling → settled.
- **No acceptance criterion waits on a decision the entry could take itself.**
- Body carries: **what**, **why**, **rejected alternatives with their reasoning**, **accepted
  costs**. No discarded alternatives → re-proposed within the month.
