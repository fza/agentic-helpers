---
name: agent-carryforward
description: 'Seats and carry-forward files: what survives a context compaction. Use when a session starts and holds no seat, when a hook says the carry-forward is due or overdue, when claiming/releasing/taking a seat, when asked to hand off, or when deciding what belongs in .memory/ versus the graph versus docs. Only projects carrying a .memory/ directory use this.'
license: MIT
---

# Carry-forward

Session runs out of context → summarised → next session knows nothing. Carry-forward = what
survives. One file per seat.

**Only a project carrying `.memory/`.** No `.memory/` → hook silent, nothing here applies. Opt in:
`mkdir .memory`.

## Seat

Seat = named job, one session at a time. Two sessions one seat → each overwrites the other, last
write wins, silently. So: claim it.

```bash
agentic-carryforward holder <seat>              # exit 0 held, 1 free
agentic-carryforward claim <seat> [session-id]  # take a free seat. refuses a live holder
agentic-carryforward take <seat> [session-id]   # seize it. holder wedged, not gone
agentic-carryforward release [seat]             # give it up
agentic-carryforward sweep                      # list every seat, reap dead holders
agentic-carryforward refreshed [seat]           # carry-forward current, ladder resets
```

- Seat name → lower case, hyphens, ≤24 chars. Any name fine; none reserved.
- **`sweep` reaps.** Dead holder → seat freed. Run it → a seat may vanish. Read-only: `holder`.
- **Session id** → only where the session started before the machinery recorded it.

`.memory/` lives **in the repo, gitignored**. Never the external
`~/.claude/projects/.../memory/` path: project memory sits beside the code it is about, not in a
path keyed to one checkout.

## Layout

```
.memory/
├── MEMORY.md                        pointer lines only, one per file
├── carryforward/<seat>-memory.md    one writer: the seat's holder
├── roles/<seat>.json                holder, pid, claimed-at
└── transcripts/<session>.md         rolling log of this session
```

## What goes in

**Transition, never knowledge.** Repo can rebuild it → not here.

| In `.memory/` | Elsewhere |
|---|---|
| where work stopped, half-done | decision → the graph |
| judgment call made without confirmation | defect, gap, blocker → the graph |
| open question nobody answered | system shape → `docs/` |
| measured fact nothing else records | how work is done → `AGENTS.md` |
| trap that already cost time | code structure, git history → nowhere |

- **Never a second copy of a decision.** Graph holds it. `.memory/` points at its id, stops.
- **Write as you go**, not at the end. Compaction does not wait.
- **One pointer line per file in `MEMORY.md`.** Dead pointer = defect, fix same turn.
- **Past 20 KB a carry-forward has stopped being a handover.** Cut what the repo rebuilds. The
  scratch hook reports it at session start.
- **No credential, ever.**
- **Register: caveman ultra.** No articles, no filler. Fragments + arrows. Ids, paths, commands
  exact. An agent reads it, not a person.

## When a hook nudges

Nudge carries the files and the command. Do it that turn:

1. Fold `.memory/transcripts/<session>.md` → your `carryforward/<seat>-memory.md`.
2. Amend what changed. Leave untouched what did not.
3. `agentic-carryforward refreshed` → ladder resets.

Ignore twice → backstop, harder. Still nothing → next session starts blind.

Nudge asks for nothing else. No graph capture, no other `.memory/` file, no handoff prompt.

## Handoff

Triggered by `handoff`. Three steps, order fixed, all mandatory.

**1 - PERSIST.** Nothing survives as context alone. Before writing the prompt:

| What | Where |
|---|---|
| stopped work, open question, judgment call, half-done | `.memory/` |
| defect, gap, blocker | the graph. Never a `.memory/` note standing in |
| something decided | the graph. `.memory/` points at its id |
| system shape changed | `docs/` |
| how work is done | `AGENTS.md` |

**2 - WIPE YOUR OWN SEAT'S FILE ONLY.**

- Your seat → `.memory/roles/`, or `agentic-carryforward sweep`.
- Rewrite `carryforward/<your seat>-memory.md` **whole**. Never patch. Never keep a paragraph "just
  in case".
- **Every other file under `carryforward/` untouched.** Belongs to a running session. Overwrite →
  that seat loses everything it never captured.
- **Exempt, wholly, always:** any file the project marks so. Standing grants, parked work. Never
  rewritten, trimmed, or folded in.
- Every other `.memory/` file → must the next session read it? No → delete. Yes → rewrite terse.
- **Delete beats keep.** Stale note costs more than a missing one. Next session verifies from the
  repo anyway.
- Rebuild `MEMORY.md` from survivors. Pointer lines only. Dead pointer = defect, fix same turn.

**3 - EMIT. Hard cap 300 chars.**

- ≤300 → print fenced, paste-ready. Count first.
- \>300 → do not print. Write `.tmp/claude/handoff/<unique>.md`, a named directory the scratch
  sweep never touches. Print a short prompt naming that exact path: read it, act, delete it
  immediately. Unique suffix stops two handoffs colliding.

Prompt = pointer to persisted state. Never the carrier. Prompt explaining the work → step 1 was
skipped.

## Concurrent seats

- **No other git command on the tree while a capture runs.** Plain `git status` takes the index lock
  → kills a capture mid-commit → entry written, never committed. Read with
  `git --no-optional-locks status`.
- **Worktrees under `.claude/worktrees/`**, never beside the repo. Sibling directory → invisible to
  `git status`, forgotten.
- **Prune a worktree the moment it stops being used.** Stale one holds a second checkout of every
  file and hides its own uncommitted work.
