---
name: agent-scratch
description: 'Where throwaway work goes. Use before writing any probe, one-off script, captured output or draft, and whenever reaching for /tmp. Says what the session sweep removes and what it can never touch. Only projects carrying a .tmp/ directory use this.'
license: MIT
---

# Scratch

Work written outside the project → invisible to whoever reviews it. Work piled at the root of
`.tmp/` → nothing tells a throwaway from a draft that still matters, and nothing ever removes
either.

**Only a project carrying `.tmp/`.** No `.tmp/` → sweep silent, nothing created. A seat claim in a
git checkout creates it. None wanted → say
so before reaching for `/tmp`.

## Layout

```
.tmp/
├── claude/<seat>/         throwaway of a seated session. SWEPT as scratch_sweep says.
├── claude/<session-id>/   throwaway of a session holding no seat. SWEPT when it ends.
├── claude/<name>/         kept. Never swept.
├── drafts/                awaiting capture. Never swept.
└── <the project's own>    never touched
```

**Seated → `.tmp/claude/<seat>/`.** The claim and every post-compaction brief name it:
`scratch: .tmp/claude/<seat>/`. Ends `(a keep, never swept)` → the folder was somebody's before the
seat: nothing in it is ever swept, delete your own.

## Rules

- **Project `.tmp/` beats `/tmp`.** Always, where one exists.
- **Throwaway → the seat's folder, else `.tmp/claude/<session-id>/`.** Probe, one-off script,
  captured output, anything the next turn will not read.
- **Never throwaway at the root of `.tmp/`.** Nothing sweeps it there.
- **Anything a later session reads → a named directory.** `.tmp/claude/<name>/` or `.tmp/<name>/`.
  Same for anything a memory entry or a graph entry points at.
- **Delete your own throwaway when the turn that made it ends.** Sweep is the backstop, not the plan.
- **`.tmp/` must be gitignored.** Session start adds `.tmp/` to git's global excludes file where git
  does not ignore it: `agentic-scratch: added .tmp/ to <file>, ...`. Creating `.tmp/` in a project
  that has none → say so first; the next session start ignores it.
- **Never a credential, in any of it.**

## What the sweep does

| When | What |
|---|---|
| `agentic-memory release` | removes the seat's folder (`scratch_sweep: release`, the default) |
| session ends | removes `.tmp/claude/<that session's id>/`; the seat's folder only under `scratch_sweep: session` |
| session starts | reaps a seat folder whose newest file is 7 days old, and a session directory untouched 7 days |
| rename, handover | the seat's folder follows the seat's new name |

`scratch_sweep: idle` → a seat's folder goes only by the 7-day reap.

**Only a folder proven throwaway is ever removed:** a seat folder whose marker `.agentic-scratch`
names one of the store's seats, or a directory named for a session. Anything else is a deliberate
keep and survives whatever its age. To keep something, give it a name. Never write, move or delete
`.agentic-scratch`.

Session directories reap by directory mtime: a keep whose name looks like a session id is not a keep.
Old session directories move into their seat's folder once, at a session start.

## Off on the machine

**Off on the machine** (`scratch_enabled: false`) → no seat folder, nothing swept. Throwaway still goes under `.tmp/claude/<session-id>/`; delete your own. shell-guard's `mktemp` ban is off with it.
