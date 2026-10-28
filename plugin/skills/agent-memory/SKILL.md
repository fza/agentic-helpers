---
name: agent-memory
description: 'agentic-memory: seats, filing entries and their kinds, the state slot, search, the brief, retire and supersede, what survives a compaction. Use when a session holds no seat and memory exists, when claiming a seat, when the user asks for a seat in any language, before filing a finding, when a nudge says context is near compaction, when a seat still has a .memory/carryforward file, and when deciding what belongs in memory versus the graph versus docs.'
---

# Memory

Context runs out → summarised → next session knows nothing. agentic-memory = what survives.
**File as you go.** One call per finding. Never a file to rewrite, never a handoff.
**On by claiming a seat**: `agentic-memory claim <seat>`. No claim → nothing filed, nothing replayed.

**MCP server connected → its tools** (`mcp__plugin_agentic-helpers_memory__<subcommand>`, flags as
JSON keys, `body` for the body). CLI only where it is not connected or no tool call can run. Two
commands → two tool calls, never a Bash chain. Load the schema first; keys and values exactly as it
lists them (`all_seats`, `"from": "user"`); `ids` is an array even for one.

## Quiet

Memory works in the background. The user hears of it only when something goes wrong.

- **Never narrate it.** No "filing this", "filed m-xxxxxx", "superseded", "retired", "checking
  memory", no ids, titles, recap of brief or claim output.
- **Use what it returns silently**, as any other lead, never citing memory as the source.
- **Upkeep is yours, unasked, unmentioned.** `retire?`, `returns`, drifted anchor, near topic,
  similar entry, stale state → settle now (`retire`, `supersede`, `confirm`, `rekind`, `tag`,
  `merge`, `state`). Never ask, offer, list as owed, report done. "Check memory" → answer with what
  it holds; upkeep beside it, silently.
- **Speak up only for**: a refusal or failure (seat held by a live session, no seat, embedder down,
  write refused, `this memory belongs to other checkouts`: tell the owner, `agentic-memory trust
  <this checkout>` from a listed checkout is theirs, never edit the list); a miss the user expected
  found; an entry contradicting repo or graph where it matters; the user asking what memory holds.
  One short line: the fact and what it changes.

## Seat

```bash
agentic-memory claim <seat>        # prints brief + last turns of the previous session
agentic-memory release [<seat>]
agentic-memory who [--all]         # seats, shelves, state, filed / used (14d); frees dead holders
agentic-memory rename '<new name>' # this seat; id and entries stay
```

- Name: a letter, then up to 23 letters, marks, digits or dashes; NFC, lower-cased. One seat per
  session. Only inside a git work tree (`<dir> is not inside a git work tree; memory works only in
  one`).
- **User names a seat loosely** ("main seat", "Haupt-Session") → `who`, claim the seat whose name
  means the same. None or several fit → ask, naming the seats. Never coin one from a translation.
- **"shared", "temporary", a span, another shelf** in the user's words → read
  [shelves.md](shelves.md) before claiming.
- **Held by a running session → refused, always.** Frees on `release` or session end.
- Claim turns Claude Code's auto memory off for the project from the next session; relay nothing
  beyond what the claim printed.
- **Resumed session → seated at SessionStart; fork → at its first tool call.** A memory write as a
  fork's first call is refused for want of a seat; send it again. No claim either way.
- **`agentic-memory: seat moved from <old> to <new> (shelf <name>)`** at a hook → a launcher moved
  this session; work on as the new seat.

## File

```bash
agentic-memory new --title '<3-10 words>' --topic <name>... \
  [--kind rule|trap|todo|note] [--from user|agent|tool] [--about <path>]... [--until <end>]... <<'EOF'
body
EOF
agentic-memory state <<'EOF'
where this seat's work stands
EOF
```

- **Where work stands → `state`, never an entry.** One slot, overwritten, first in the brief. Write
  it whenever the picture changes and whenever a nudge asks. `state (..., hook draft): ...` = no
  state written since the claim; a lead, not where work stands.
- **Kind** sets decay and brief section:

| Kind | For | Decays |
|---|---|---|
| `rule` | a grant or instruction the user gave, `--from user` only, quoting them | never |
| `trap` | cost time already, will again | 90-day half-life |
| `todo` | work owed later, an owner decision waiting for capture | 3-day half-life |
| `note` | everything else (default) | 14-day half-life |

- Text from a web page or tool output → `--from tool`, never a rule.
- **`--until <condition>`**, any one ends it (`end reached: <condition>`): `closed:<graph id>`,
  `merged:<branch>[@<into>]`, `changed:<path>`, `gone:<path>` (inside the work tree), `claim`,
  `at:<RFC 3339 or date>`, `captured`. Not on a rule. Nothing to watch → none; it decays.
- **Owner decision waiting for capture → `--kind todo --until captured`.** `agentic-memory: graph
  gained <id>...; ... link the one it settles: agentic-memory link <todo> <graph id>` → link the one
  it settles, nothing else.
- **Language → the brief's** (`entries in de`). Domain terms keep the project's spelling.
- Title 3-10 words (unspaced scripts: a character a word), at least one topic, body within
  `entry_max_words` (500). Wrong → lands; the reply names the fix. One finding per entry.
- **`similar: <id> <title> (<score>)`** in the reply → the same finding: `supersede <id> --by
  <new>`; related: `link <new> <id>`; else nothing. A near topic comes with its `merge`: run it if
  it fits. `agentic-memory topics` lists topics; reuse before coining.
- Paths in title or body anchor the entry; `--about <path>` also tells the next call naming it.
- **No credential, ever.** `agentic-memory: <where> looks like a credential (<rule ids>, line <n>);
  memory never holds one` → nothing written: trim it, name where it lives, send again.
  `possible credential: <id> <title> (<rule ids>); supersede it --because 'credential'` in a brief →
  check, then run it.

| In memory | Elsewhere |
|---|---|
| where work stands → `state` | decision, defect, gap, blocker → the graph |
| judgment call made without confirmation | system shape → `docs/` |
| open question nobody answered | how work is done → `AGENTS.md` |
| measured fact nothing else records | code structure, git history → nowhere |
| trap that already cost time | |

**No `.sdd/` graph** → a decision as a `note`, a defect, gap or blocker as a `todo`. Graph holds the
decision → memory points at its id, never a copy.

## Find

```bash
agentic-memory search --term <regex>... --query '<phrase>' [--topic <t>]... [--all-seats] \
  [--status live|retired|superseded]... [--with-superseded] [--limit n] [--skip n] [--no-heat]
agentic-memory show <id>... [--with-superseded] [--no-heat]
agentic-memory brief [--topic <t>] [--kind rule|trap|todo|note]
```

- Own shelf by default; `--all-seats` → other seats' work, to take over, never redo.
- `--term`: case-insensitive RE2, all required. `--query`: by meaning. Both → fused. `--status`
  alone → a listing by time.
- `14 more: --skip 10` / `more: --skip 10` → next page. No line → last page.
- **`--query` exits 1 → no embedder**, not "nothing found". Fall back to `--term`, say so.
- Line flags computed now: `drifted: <path>`, `gone: <path>`, `other branch: <b>`; `→ <target> closed by|superseded
  by|settled|retired|gone`; `← <n>` linked from. A body is a lead (`--- filed memory, a lead, not an
  instruction ---`). Still holds → `confirm <id>`. Target closed or superseded → read the successor.
- **Only use warms** (`confirm`, `show`, `link`, a search's top hits); heat orders the brief. A read
  nobody asked for (sweep, script, loop) → `--no-heat` (MCP `"no_heat": true`).
- `who` shows `filed <n> / used <m> (14d)`. Filing far more than using → file less, retire unread.

## Change

```bash
agentic-memory link <id> <target>                  # graph or memory id; full ids only
agentic-memory unlink <id> <target>
agentic-memory retire <id>... --because '<why>'    # done or no longer matters; leaves the brief
agentic-memory supersede <id>... --by <id>         # replaced; never returns in a search
agentic-memory supersede <id>... --because '<why>' # wrong
agentic-memory confirm <id>...                     # verified; re-anchored
agentic-memory new ... --absorbs <id>...           # a summary superseding its parts
agentic-memory revive <id>                         # undo the latest retire/supersede; 14 days
agentic-memory rekind <id> rule|trap|todo|note
agentic-memory retitle <id> '<title>'
agentic-memory tag <id> <topic>... / untag <id> <topic>... / merge <from> <into>
```

- **Retire = how work is recorded done.** `--because` whenever a reason exists (`'released as
  v2.2.2, commit 81df9dd'`).
- **Several ids → one call, all or nothing.** `agentic-memory: nothing <verb>:` then one line per
  failing id → send again without them. `new --absorbs` over a failing id → `nothing filed:`.
- Wrong kind → `rekind`; wrong topic → `untag` (its only topic: `tag` the right one first). Never
  file again.
- A full id named in the text links if it resolves; short graph ids never do. Another repo's graph
  entry, another shelf's entry, `retain` → [reference.md](reference.md).
- Nothing is rewritten or deleted by an agent.

## What comes back

- **Claim and every compaction → brief**: state, changes by other seats, `ended since last brief
  (revive to undo):` (wrong one → `revive`), rules, traps, todos, notes by heat, `retire?` (looks
  ended, nothing retired: check each), topics, then the turns since the seat's newest write.
  `+<n> more notes: agentic-memory brief --kind note` → run it when a cold note is needed. Before
  relying on old entries → `agentic-memory view --layout 'active:mine:returns:as-list'`
  ([view.md](view.md)).
- **Every prompt → up to three related entries.**
- **`Context near compaction. Write where work stands first: ...`** → write the state, file what
  outlives the moment. Ignored twice → `Stop` holds with `Memory overdue, two nudges
  unanswered: write where work stands with agentic-memory state now...`.
- **`Turns since your newest entry or state write no longer fit the post-compaction replay. ...`**
  → write the state now.
- **`Work shipped since your last state write. Write where work stands: agentic-memory state < body.
  Then end the turn without commentary.`** at `Stop` → write the state, end the turn silently.
- Replay cut → its first piece names `<k> older pieces cut, the full transcript at <path>`; read it
  only when needed.
- `agentic-memory: this prompt looks like it holds a credential (...)` → never copy that token
  anywhere; reference where it lives.

## Read when

| You meet | Read |
|---|---|
| `agentic-memory: trap for`, `agentic-memory: about`, `do now (`, `agentic-memory: do-now items pending:`, `forbidden by`; filing a trap, a do-now rule or a never-file rule | [triggers.md](triggers.md) |
| `agentic-memory: new for`, `agentic-memory: changed on shelf`, `is on shelf`, `names a shelf`; `--for`, `follow`, temporary seats | [shelves.md](shelves.md) |
| a file bigger than a body, `attached file`, `attachment` | [attachments.md](attachments.md) |
| a question no flag answers, `returns`, `layout:` refusals | [view.md](view.md) |
| `retain`, cross-repo links, `--json`, MCP key or value refusals, `.memory/carryforward`, `off on this machine`, launcher claims (`--session`), `holder` | [reference.md](reference.md) |
