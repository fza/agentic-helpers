@AGENTS.md

# CLAUDE.md

The rules for this repository live in [`AGENTS.md`](AGENTS.md), imported above. They are not restated
here: two copies drift, and a reader obeys whichever they opened. Go work additionally loads
[`source/AGENTS.md`](source/AGENTS.md) when a session touches code under `source/`.

What follows is Claude-Code-specific and applies on top.

## The hooks run in this very session

The plugin's hooks run in every session on the machine, so the session editing them is also running
them.

- **A hook change reaches this session through `AGENTIC_HELPERS_BIN_DIR`.** With it pointing at the
  `go install` directory, the next hook event runs what `go install -C source/hooks ./cmd/...` just
  built; without it, the session runs the release the plugin names.
- **Never delete a binary a hook still runs.** A missing one leaves its hook doing nothing, and the
  grounding gate refusing every call it guards.
- **Setting `AGENTIC_HELPERS_BIN_DIR` edits `~/.claude/settings.json`, which also holds
  credentials.** Ask before writing to it.

## The decision graph

**The `/sdd` skill drives this repository's graph.** Invoke it rather than composing `sdd` commands
by hand for anything conversational: it carries the playback-before-capture discipline and the
entry-shape rules a bare command does not.
