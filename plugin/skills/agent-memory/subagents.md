# Subagents

A subagent shares its spawner's session and runs under every tool-call hook, but no SessionStart
fires for it and no skill is loaded. What it gets of agentic-helpers is what its spawner names.

## The marker

One line in the Agent call's prompt, alone on its line:

```
agentic-helpers: shared
```

`none`, `rules`, `silent`, `shared` or `full`. No line, or a word that is none of these, is `none`.
Only the first marker line counts: the PreToolUse hook on the Agent tool swaps it for the block and
keeps every later one as text.

**Passing a level on.** For a subagent that spawns its own (a builder spawning a reviewer), write
the nested line alone on its line below your own marker, saying what it is for:

```
agentic-helpers: silent
Spawn the reviewer with this line, alone on its line, in its prompt:
agentic-helpers: rules
```

The child copies it into its Agent prompt as is, no backticks. It never raises the nested child
above the child's own level: a `silent` child copying `agentic-helpers: full` spawns at `silent`.

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
| `run agentic-memory directly, not inside code another program runs` | `silent` and up: a call inside `bash -c`, `eval` or interpreter code cannot carry the subagent's id |

## `--agent` and `agent`

`agentic-memory --agent <agent-id> <subcommand>`, and the key `agent` of every memory MCP tool,
name the subagent a call comes from: its own shown set, and at `full` its seat. **The PreToolUse
hook sets them, never type them**: on every direct `agentic-memory` call and every memory tool call
of a subagent at `silent` and up it sets its id, replacing any already there; one from the main
thread is refused. The command line and the server refuse one naming no subagent of the calling
session reading memory.

## Seen pointers

Each subagent has its own record of what it was shown. A fresh subagent's starts empty: its first
read of an entry prints it in full, its next one `<id> <title> (seen)`, so no `--again`. A fork's
starts with what its spawner was shown before the spawn, which its inherited context holds. What a
subagent reads never turns into a pointer for its spawner.

Trap and note notices (`agentic-memory: trap for <what matched>:`, `agentic-memory: about <path>:`)
reach a subagent at every level and go by the same record: a fresh subagent is told of a trap its
spawner already heard, a fork is not, and a notice a subagent got is still told to its spawner.

An `agentic-memory` call a script file makes is out of the hook's reach: it works on the session's
own seat and counts as the session's read.
