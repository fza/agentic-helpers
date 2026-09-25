---
name: agent-carryforward
description: Seats and carry-forward files: what survives a context compaction. Use when a session starts and holds no seat, when a hook says the carry-forward is due or overdue, when claiming/releasing/taking a seat, when asked to hand off, or when deciding what belongs in .memory/ versus the graph versus docs. Only projects carrying a .memory/ directory use this.
license: MIT
---

# Carry-forward

Session runs out of context → gets summarised → next session knows nothing. Carry-forward = what
survives that. One file per seat.

**Only a project carrying `.memory/` uses any of this.** No `.memory/` → hook silent, nothing here
applies. Opt in: `mkdir .memory`.

## Seat

Seat = named job, one session at a time. Two sessions, one seat → each overwrites the other, last
write wins, silently. So: claim it.

```bash
HOOK="$HOME/Codepot/sources/agentic-helpers/hooks/carryforward.py"

python3 "$HOOK" holder <seat>              # who holds it. exit 0 held, 1 free
python3 "$HOOK" claim <seat> [session-id]  # take a free seat. refuses a live holder
python3 "$HOOK" take <seat> [session-id]   # seize it anyway, holder wedged, not gone
python3 "$HOOK" release [seat]             # give it up
python3 "$HOOK" sweep                      # list every seat, reap dead holders
python3 "$HOOK" refreshed [seat]           # carry-forward now current, reset the ladder
```

Seat name → lower case, hyphens, ≤24 chars. Common: `main`, `drive`, `finalize`, `validate`,
`insource`, `idea`. New name fine, set is a suggestion, not a gate.

**`sweep` reaps.** Dead holder → seat freed. Run it → seat may vanish. Read-only alternative:
`holder <seat>`.

**Session id:** needed only where session started before machinery recorded it.

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

| Goes in `.memory/` | Goes elsewhere |
|---|---|
| where work stopped, half-done | decision → the graph |
| judgment call made without confirmation | defect, gap, blocker → the graph |
| open question nobody answered | system shape → `docs/` |
| measured fact nothing else records | how work is done → `AGENTS.md` |
| trap that already cost time | code structure, git history → nowhere |

**Never a second copy of a decision.** Graph holds it. `.memory/` points at its id, stops there.

**A tool the project merely uses never appears here.** Its state, decisions, measurements → its own
repo.

**Write as you go**, not at the end. Compaction does not wait.

## When a hook nudges

Nudge carries the files and the command. Do what it says, that turn:

1. Fold `.memory/transcripts/<session>.md` → your `carryforward/<seat>-memory.md`.
2. Amend where something changed. Leave untouched where nothing did.
3. `python3 "$HOOK" refreshed` → ladder resets.

Ignore twice → backstop, harder. Still nothing → next session starts blind.

Nudge says nothing about capturing to a graph, other `.memory/` files, or a handoff prompt. Do
none of those unless asked.

## Register

**Caveman ultra.** No articles, no filler, fragments + arrows. Ids, paths, commands exact.
Every carry-forward is read by an agent, not a person.

## Handoff

Triggered by `handoff`. Three steps, order fixed, all mandatory.

**1, PERSIST.** Nothing survives as context alone. Before writing the prompt:

- stopped work, open questions, judgment calls, half-done → `.memory/`
- defect / gap / blocker → the graph, never a `.memory/` note standing in
- decided → the graph. `.memory/` points at its id
- system shape → `docs/`. How work is done → `AGENTS.md`

**2, WIPE YOUR OWN SEAT'S FILE ONLY.**

- Read your seat from `.memory/roles/`, or `python3 "$HOOK" sweep`.
- Rewrite `carryforward/<your seat>-memory.md` whole. Never patch, never keep a paragraph "just in
  case".
- **Every other file under `carryforward/` untouched.** Belongs to a running session. Overwrite →
  that seat loses everything it had not captured.
- Exempt, always, wholly: any file the project marks so. Standing grants, parked work. Never
  rewritten, never trimmed, never folded in.
- Every other `.memory/` file → must the next session read it? No → delete. Yes → rewrite terse.
- **Delete beats keep.** Stale note costs more than a missing one; next session verifies from the
  repo anyway.
- Rebuild `MEMORY.md` from what survived. Pointer lines only. Dead pointer = defect, fix same turn.

**3, EMIT. Hard cap 300 chars.**

- ≤300 → print fenced, paste-ready. Count first.
- \>300 → do not print. Write `.tmp/handoff-<unique-suffix>.md`. Print a short prompt naming that
  exact path: read it, act, delete it immediately. Unique suffix stops two handoffs colliding.

Prompt = pointer to persisted state. Never the carrier. Prompt that has to explain the work → step 1
was skipped.

## Concurrent seats

- **Keep other git commands off the tree while a capture runs.** Plain `git status` takes the index
  lock → kills a capture mid-commit → entry written, never committed. Read with
  `git --no-optional-locks status`.
- **Worktrees live under `.claude/worktrees/`**, never beside the repo. A sibling directory is
  invisible to `git status` and gets forgotten.
- **Prune a worktree the moment it stops being used.** Stale one holds a second checkout of every
  file and hides its own uncommitted work.
