# Todolists for agentic-memory todos: measurement and exploration

## Measurement

Every live agentic-memory store on one machine, read through `agentic-memory dump` of each log segment and tail, backups excluded.

| Measure | Value |
|---|---|
| entries | 1067 |
| todos | 126 |
| todos written as lists (2+ list lines) | 32, 183 list lines, about 6 items each |
| words per list item | median 23, p90 61, mean 30 |
| todos superseded | 37 |
| words in superseded todo bodies | 7264 (about 9.5k output tokens) |
| todos filed with links | 74 (147 links); 50 gained links later |
| todos carrying `--until` | 90, 83 of them `captured` |
| live list-shaped todos | 5 |

Supersede chains of list-shaped todos (items before -> after, items unchanged, words before -> after):

| Todo | Rewrites | Items | Words |
|---|---|---|---|
| Store layout redesign: settled and open points | 9 | 5 -> 13, 0 to 9 unchanged per rewrite | 218 -> 574 at peak, 423 last |
| Size limits for attachments and tools | 7 | 4 -> 8, 1 to 4 unchanged per rewrite | 198 -> 320 |
| Live checks owed after fork seating release | 2 | 3 -> 2 -> 0, none unchanged | 109 -> 139 -> 53 |

Most rewrites reworded items they did not change, which is where an item drifts or drops out unnoticed.

Brief size on one seat: `brief --json` 21.8 KB; its 13 todos 6.9 KB as JSON with bodies, about 1.1 KB as id and title alone.

## Exploration

Five isolated frames (logistics, inversion, on-call, attacker, speedrunner), six ideas each, scored on novelty, viability and fit; the three strongest clusters deepened.

Shortlist:

- Items as log records on the todo, addressed by a stable id, so ticking or adding costs a few tokens and untouched items cannot drift.
- Linked items whose state is derived at read from the target, never stored; a reopened target unticks for free; a superseded target is a third state, not done.
- One brief line per todolist with the count and what comes next; the full list on `show`.
- The finished list ending the todo through the existing end machinery.

Traps:

- Ticks written into the state slot: overwritten each write, and prose parsing is fragile.
- Ticks as tags: tags are topic labels.
- Ticks from commit trailers: bound to a branch, and not every tick has a commit.
- Ticks batched until the compaction nudge: the nudge does not fire reliably.
- Plain items as memory entries of their own: brief and search noise, per-item decay and embedding.
- An unticking reason filed as a trap entry: pollutes traps.

Risks named by the deepening:

- A cursor that points at finished work when the agent forgot to tick.
- Agents keeping their habit of writing lists into the body.
- The existing `closed:` end and `retire?` inference reading superseded as ended, which an item must not.
