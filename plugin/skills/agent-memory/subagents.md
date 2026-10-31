# Subagents

A subagent shares its spawner's session and runs under every tool-call hook, but no SessionStart
fires for it and no skill is loaded. What it gets of agentic-helpers is what its spawner names.

## The marker

One line in the Agent call's prompt, alone on its line:

```
agentic-helpers: shared
```

`none`, `rules`, `silent`, `shared` or `full`. No line, or a word that is none of these, is `none`.
Only the first marker line counts; the PreToolUse hook on the Agent tool drops every other one.

## What each level gets

Each level holds everything of the one below.

- **`none`**: the scratch folder (`scratch: .tmp/seats/<seat>/`, the spawner's seat's, or
  `.tmp/seats/<session-id>/` where the session holds no seat) and the machine line
  (`agentic-helpers: this machine: ...`) where the machine needs one. No level line.
- **`rules`**: the seat's live rules under `rules (from the user, verify before relying):`, each with
  its body, within a third of the brief (`+<n> more rules on seat <seat>` past that).
- **`silent`**: `agentic-memory: read-only on seat <seat> (search, show, brief, topics, view, who);
  every write is refused.` and the skills to load.
- **`shared`**: at its tool calls, `agentic-memory: new for <seat>` and `agentic-memory: changed on
  shelf <shelf>` lines when a shelf mate files or changes an entry while it runs. Its own mark: the
  seat is still told of the same entries.
- **`full`**: `agentic-helpers level: full, seat <shelf>-<n>`: a seat of its own on the spawner's
  shelf, held from the spawn and released when SubagentStop fires for it (or at session end). It
  files there, its entries name that seat, its state slot is its own, and notices run both ways
  through the shelf. Its scratch folder stays the spawner's.

The block opens with `agentic-helpers level: <level> (...)` from `rules` up; the subagent's hooks
read its level back from that line. A line in that shape written into a prompt by hand is dropped.

## Capped

- A nested subagent never gets more than the subagent spawning it; one spawned from a `none`
  subagent is `none` whatever it asks.
- Memory off, outside a git work tree, or no store: at most `rules`.
- The session holds no seat: at most `silent` (no shelf to share).

## Refusals

| Line | Means |
|---|---|
| `the subagent runs at agentic-helpers level <level>, which gives it no memory access` | `none` or `rules`: no memory call at all |
| `the subagent runs at agentic-helpers level <level>, which reads memory but never writes it` | a write below `full`: report the finding to the spawner |
| `<verb> stays with the session that spawned the subagent` | `claim`, `release`, `rename`, `private`, `lifetime`, `drop`, `bind`, `merge`, `follow`, `unfollow`, `mcp`: never from a subagent |
| `run agentic-memory directly, not inside code another program runs` | `full`: a write inside `bash -c`, `eval` or interpreter code cannot reach its seat |

## `--agent`

`agentic-memory --agent <agent-id> <subcommand>` works on the seat of that `full` subagent. **The
PreToolUse hook sets it, never type it**: on a `full` subagent's every direct `agentic-memory` call
it inserts the flag, replacing any already there; a typed one from a lower level is replaced by the
calling subagent's own id, and one from the main thread is refused. The command line refuses an
`--agent` naming no `full` subagent of the calling session.

Over MCP the server finds the subagent by the call's id, which the PreToolUse hook kept.

An `agentic-memory` call a script file makes is out of the hook's reach and works on the session's
own seat.
