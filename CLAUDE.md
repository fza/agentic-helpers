@AGENTS.md

# CLAUDE.md

The rules for this repository live in [`AGENTS.md`](AGENTS.md), imported above. They are not restated
here: two copies drift, and a reader obeys whichever they opened. Go work additionally loads
[`source/AGENTS.md`](source/AGENTS.md) when a session touches code under `source/`.

What follows is Claude-Code-specific and applies on top.

## The hooks run in this very session

These hooks are wired into the global settings file, so the session editing them is also running
them.

- **A running session keeps the wiring it started with.** After `./install.py` rewires an entry, this
  session still runs the old command until it ends.
- **Never delete a file the current wiring still runs.** A missing script makes `python3` exit 2,
  and exit 2 from a `UserPromptSubmit` hook blocks every prompt that follows. Keep the file on disk,
  untracked, until the session ends.
- **Rewiring edits `~/.claude/settings.json`, which also holds credentials.** Ask before running
  `./install.py` without `--print`.

## The decision graph

**The `/sdd` skill drives this repository's graph.** Invoke it rather than composing `sdd` commands
by hand for anything conversational: it carries the playback-before-capture discipline and the
entry-shape rules a bare command does not.
