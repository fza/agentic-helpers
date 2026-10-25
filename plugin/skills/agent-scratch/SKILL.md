---
name: agent-scratch
description: 'Where throwaway work goes. Use before writing any probe, one-off script, captured output or draft, and whenever reaching for /tmp. Says what the session sweep removes and what it can never touch. Only projects carrying a .tmp/ directory use this.'
license: MIT
---

# Scratch

Work written outside the project → invisible to whoever reviews it. Work piled at the root of
`.tmp/` → nothing tells a throwaway from a draft that still matters, and nothing ever removes
either.

**Only a project carrying `.tmp/`.** No `.tmp/` → sweep silent, nothing created. None wanted → say
so before reaching for `/tmp`.

## Layout

```
.tmp/
├── claude/<session-id>/   throwaway. SWEPT when the session ends.
├── claude/<name>/         kept. Never swept.
├── drafts/                awaiting capture. Never swept.
└── <the project's own>    never touched
```

## Rules

- **Project `.tmp/` beats `/tmp`.** Always, where one exists.
- **Throwaway → `.tmp/claude/<session-id>/`.** Probe, one-off script, captured output, anything the
  next turn will not read.
- **Never throwaway at the root of `.tmp/`.** Nothing sweeps it there.
- **Anything a later session reads → a named directory.** `.tmp/claude/<name>/` or `.tmp/<name>/`.
  Same for anything a memory entry or a graph entry points at.
- **Delete your own throwaway when the turn that made it ends.** Sweep is the backstop, not the plan.
- **`.tmp/` must be gitignored.** Not ignored → say so rather than creating it.
- **Never a credential, in any of it.**

## What the sweep does

| When | What |
|---|---|
| session ends | removes `.tmp/claude/<that session's id>/` |
| session starts | reaps a session directory nobody came back to after 7 days |

**Only a directory named for a session is ever removed.** A name the sweep cannot parse as a session
identifier is a deliberate keep and survives whatever its age. That is the whole safety of it: to
keep something, give it a name.

Reaping is by directory mtime. A keep whose name looks like a session id is not a keep.

## Off on the machine

**Off on the machine** (`scratch_enabled: false`) → nothing swept. Throwaway still goes under `.tmp/claude/<session-id>/`; delete your own. shell-guard's `mktemp` ban is off with it.
