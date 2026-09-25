@AGENTS.md

# CLAUDE.md

The rules for this repository live in [`AGENTS.md`](AGENTS.md), imported above. They are not restated
here: two copies drift, and a reader obeys whichever they opened. Go work additionally loads
[`source/AGENTS.md`](source/AGENTS.md) when a session touches code under `source/`.

What follows is Claude-Code-specific and applies on top.

## The hooks run in this very session

These hooks are wired into the global settings file, so the session editing them is also running
them.

- **A running session picks up rewiring at once.** Claude Code reloads its settings, so after
  `agentic-install` rewires an entry, this session runs the new command from its next hook event.
- **Never delete a file the wiring still runs.** A missing hook command exits non-zero, and exit 2
  from a `UserPromptSubmit` hook blocks every prompt that follows. Rewire first, delete after.
- **Rewiring edits `~/.claude/settings.json`, which also holds credentials.** Ask before running
  `agentic-install` without `--print`.

## The decision graph

**The `/sdd` skill drives this repository's graph.** Invoke it rather than composing `sdd` commands
by hand for anything conversational: it carries the playback-before-capture discipline and the
entry-shape rules a bare command does not.
