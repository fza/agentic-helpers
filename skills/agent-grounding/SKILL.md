---
name: agent-grounding
description: Grounding a decision graph before asking, drafting or capturing: both search modes, the area listing, read depths, ref kinds. Use when reading or writing entries in an sdd graph, when the grounding gate refuses a call, before putting a question or editing a draft, and before any capture. Only projects carrying a .sdd/ graph use this.
license: MIT
---

# Grounding

Agent asked about a decision graph answers from the entry it already read, or from what it believes
the graph holds. Both are how a wrong answer gets written down with confidence.

**Only a project carrying `.sdd/` uses any of this.** No graph → gate silent, nothing here applies.

## Before a question, a draft edit, or a capture

Three reads. All of them, this turn:

```bash
sdd search --query "<how somebody would phrase the subject>"   # semantic
sdd search --term  "<the literal token: a flag, a command, an id>"
sdd view --layout "topic(area-<name>):as-list"                 # the area, whole
```

**Both modes, always.** Semantic matches phrasing → misses the entry saying it in other words.
Literal matches the token → misses everything worded differently. A live decision already owning
your subject is exactly what one-mode-only hides.

**An empty result is never evidence of absence.** `sdd search` finds what it matched; `sdd show`
reaches only what something already cited. The area listing is the only read that settles what an
area holds.

**Ask which modes exist once per session**, before the first search: `sdd info` → `vector` where an
embedding endpoint answers, `text` otherwise.

## Reading an entry

**Every grounding read is `sdd show <id> --down 2 --up 1`.** Not a default to weigh, the depths are
part of the command.

- A body is immutable → a surface it names keeps that spelling forever, including after a later
  entry renamed it. The rename lives in the downstream chain and nowhere else.
- Status stays `active` throughout, because a refinement does not close its target.
- `--down 2` reaches a refinement of a refinement. `--up 1` shows what the entry was answering,
  which is what says whether it still applies.
- Depth zero answered `fdbox deploy config` when three refinements had already made it
  `fdbox deploy manage`.

One exception the gate allows: a `prc`-layer rules entry, whose downstream chain is every entry ever
captured under it.

## An entry's body is a lead, never ground truth

Before acting on any entry:

0. **Search for a decision that already owns it.** Active decision settles it → entry is
   implementable, not open. Drafting another → duplicate that disagrees the moment either changes.
   `addresses` on a live decision is the usual marker.
1. **Read the code it names, at the line it names.** A `file:line` was true when written.
2. **Read the spec section under `docs/` that governs it.** Entry describes the code; document says
   what the code owes. They disagree more often than either admits.
3. **Verify every external claim against that party's own documentation.** A provider limit, an
   API's absence, a tool's behaviour.

**Report which claims held and which did not.** A settled entry whose premise was false is a
different outcome from one that was right.

Failure shapes that pass a casual read:

| Shape | What it looks like |
|---|---|
| Stale | tree moved, entry did not; the gap is already closed |
| Wrong blocker | names a real defect that governs another surface |
| Prose-only mechanism | cites a function or recovery path existing only in the comment naming it |
| Wrong component | picks the wrong binary or party; check the credential's scope first |
| Already decided | body poses a question a live decision elsewhere answers |

## Ref kinds: pick the sharp one, check the direction

Refs are immutable. Get it right before the capture.

| Kind | When |
|---|---|
| `refines` | narrows or corrects a **live** commitment, in place |
| `addresses` | supplies what an **active** target asked for |
| `builds-on` | forward step, or a **closed** target |
| `grounded-in` | this entry reasons **from** the target |
| `required-by` | recorded from the prerequisite's side |
| `related` | the floor. Understates every time; pre-flight flags it |

## What the gate refuses

| Refusal | Why |
|---|---|
| question or draft edit with the three reads missing | answering from memory |
| a capture whose draft records a gap | capture the decision that settles it, never the problem |
| an `sdd` call reaching the tool some other way | ledger never sees it, so the reads do not count |
| an `sdd` call sending a stream to `/dev/null` | wrong flag prints usage to stderr; discarded, the refusal reads as an empty graph |
| `sdd show` that suppresses the downstream chain | a rename lives only there |

Exempt: a draft a verification already read. Its fixes owe no fresh reads.

## Traps

- **`--term` is the flag, repeatable. Not `--terms`.** Wrong flag → usage on stderr, non-zero exit.
- **Never `2>/dev/null` an `sdd` call.** Filter what came back instead: `| grep -E '^  [0-9]'`.
- **An area listing is enormous** and another capture mid-pass invalidates it. Read the first run
  whole, then read only what changed.
- **Lint a draft before capture. Nothing can lint it after.** Piping a body into `vale -` reports
  nothing, and a fenced draft reports nothing either, leave a draft unfenced.

## Capturing

- **Never capture a gap.** A gap is a finding: repair it, file it against the row that owns the
  repair, or put it to the owner. Capture the decision that settles it.
- **Every entry names at least one area** where the project uses them. Nothing outside an area.
- **Name every surface a decision creates**, command, flag, RPC verb, config key, label, storage
  path, environment variable. "We'll add a command" is not settled; the exact spelling is.
- **An acceptance criterion never waits on a decision the entry could take itself.**
- Body carries the **what**, the **why**, the **rejected alternatives with their reasoning**, and
  the **accepted costs**. A decision recorded without its discarded alternatives gets re-proposed.
