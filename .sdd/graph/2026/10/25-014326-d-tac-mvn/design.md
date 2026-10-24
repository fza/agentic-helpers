# agentic-memory: design

`agentic-memory` replaces `agentic-carryforward`. A session no longer rewrites a carry-forward file:
it files entries, each with a 3-10 word title, a free-form body and one or more topics it chooses,
and `agentic-memory` timestamps and indexes each one the moment it is filed. Nothing is ever
rewritten: entries are retired or superseded, and the tool chooses what returns to a fresh context.

## Surfaces

- **Every agent-facing operation is both a command and an MCP tool.** `agentic-memory <subcommand>`
  for the Bash tool and any agent without MCP, and an MCP server the plugin declares, so a client
  speaking MCP files and searches without a shell. One implementation backs both; the two surfaces
  differ in transport only.
- Hooks stay commands: an MCP server is only ever called, and cannot answer a hook event.
- **The server is `memory`**, declared in `plugin/.mcp.json` and started as `agentic-memory mcp`
  (stdio) through `${CLAUDE_PLUGIN_ROOT}/hooks/run`, like every binary of the plugin. It shows in
  `/mcp` as `plugin:agentic-helpers:memory`, and each tool carries its subcommand's name:
  `mcp__plugin_agentic-helpers_memory__new`, `..._search`, `..._brief`, and so on.
- Rejected: naming the server `agentic-memory`. The plugin prefix already says agentic-helpers, and
  `mcp__plugin_agentic-helpers_agentic-memory__new` lengthens every permission rule and matcher.

## Storage

- **The log is the truth: `~/.claude/agentic-memory/<project>/`.** It holds the append-only event
  log and the seat claims. It sits under `~/.claude/` beside the rest of the agent's own state, not
  in the repository, so no `.memory/` directory exists any more.
- **The log is sealed segments, the layout sdd's search index runs.** Under `log/`: events append
  to `tail.jsonl`, the open segment (`O_APPEND`, fsync, under a file lock). A tail past its size
  bound seals into an immutable segment named by its content hash, `<sha256[:8]>.seg`, and
  `manifest.json`, replaced by rename, lists the sealed segments in order. Segments merge into one
  once eight exist or dead rows outnumber live ones, and a merge is where the body of an entry
  superseded more than 14 days earlier is finally dropped.
- **A sealed segment is binary, its row table first, mapped read-only like sdd's.** Header (magic
  `AGMSEG\0`, format byte, row count, table length), then one fixed row per event (kind: `new`,
  `retire`, `supersede`, `merge`, `confirm`, `retain`, `revive`, `retitle`, `tag`; entry id, time,
  seat, target id, payload offset and length), then the JSON payloads (title, body, topics, anchors). A call maps each segment with
  `syscall.Mmap` (`PROT_READ`, `MAP_SHARED`), folds status from the row tables alone, and decodes a
  payload only when it needs that body. A fault in the mapping surfaces as an error through
  `debug.SetPanicOnFault`. A platform without `syscall.Mmap` reads the file whole instead. The MCP
  server keeps its mappings and rechecks the manifest's file identity on every call.
- Rejected: reading each segment whole with `os.ReadFile`. Simpler, and fast at a few megabytes,
  but every call would copy every body it never reads.
- Rejected: sealed JSONL segments plus a folded-state snapshot in the cache. A second derived
  artifact to keep consistent, and a cache loss means one full parse.
- **The tail seals past 64 KB.** The write that pushes `tail.jsonl` past 65536 bytes seals it:
  the segment is written and renamed into place, the manifest replaced by rename, and the tail
  replaced by an empty one, all under the log's lock. Every call parses the tail whole, so the bound
  keeps that parse well under a millisecond; a merge then falls about every 512 KB filed.
- Rejected: 256 KB. Fewer seals and merges, but every call, every hook event included, parses up to
  256 KB of JSON.
- Rejected: a bound in rows. One large body makes a large tail regardless of the count.
- Accepted cost: an own binary format with a version byte, inspected through a dump subcommand.
- Rejected: one JSONL replayed whole on every call. The cost of each call grows with the whole
  history, and hooks call on every prompt.
- Rejected: one JSONL with a snapshot of the folded state at a byte offset. Dropping superseded
  bodies would rewrite the whole log under lock.
- Rejected: one file per entry, sdd's graph layout. One open per entry per call; sdd needs a
  whole-store cache for it, and every write invalidates that cache.
- Rejected: SQLite through `modernc.org/sqlite`. A large transpiled dependency, against
  standard-library-first.
- **The index is a cache: `~/.cache/agentic-memory/<project>/`.** Vectors and term data derive
  from the log and are rebuilt from it whenever they are missing or stale, the same `~/.cache`
  convention sdd follows for its own search index. `XDG_CACHE_HOME` relocates it.
- Rejected: everything under `~/.cache`. Any cache cleanup erases every memory with no way back,
  because a cache directory is by definition safe to delete.
- Rejected: the log under `~/.local/share` (`XDG_DATA_HOME`). Memory is agent state and belongs
  beside it under `~/.claude/`.
- **`CLAUDE_CONFIG_DIR` does not move the log.** The root is `~/.claude/agentic-memory/` whatever
  config directory a session runs under, so a project has one memory on the machine across every
  Claude Code account.
- Rejected: the log following `CLAUDE_CONFIG_DIR`. A seat claimed under one account would be
  invisible to a session of another on the same repository, splitting one project's memory in two.
- **`<project>` is keyed the way sdd keys its search index.** The `repo_id` a project's
  `.sdd/config.yaml` declares, where it declares one (`github.com/fza/agentic-helpers`), else
  `local/<first 12 hex of sha256 of the stable root>`. The stable root is the main checkout,
  resolved through `git rev-parse --git-common-dir`, so every worktree of a repository shares one
  memory; outside git it is the directory itself. One rule across both tools means one project
  identity on the machine.
- Rejected: the root commit hash. Survives moves, but a fork would share its upstream's memory, and
  it is a second identity scheme beside the one sdd already runs.
- Rejected: the `origin` URL. Needs a fallback for a repository without a remote, and changing the
  remote orphans the memory.
- Accepted cost: a project declaring no `repo_id` loses its memory to a move or a fresh clone.
- Accepted cost: two roots. Losing the cache costs one re-embed; losing the log costs the memory.
- Accepted cost: a session launched with a separate config directory, a clean debug launch
  included, still writes under `~/.claude/`.

## Entry ids

- **An id is `m-` and four lowercase alphanumerics**, `m-7q2k`, drawn under the log's lock and
  redrawn on a collision within the project. An id is never reused: an entry whose body was dropped
  keeps its rows.
- **Every place an entry shows carries the date and time it was filed**, brief and search result
  lines included; a superseded entry also shows when it was superseded. The id carries no time, so
  the line has to.
- Rejected: a per-project sequence, `m-142`. Two projects both hold an `m-142`, so an id pasted into
  the wrong project silently names another entry.
- Rejected: an sdd-style id, `20261025-141503-m-7q2`. Long in every result line and command.
- Accepted cost: an id says nothing about order.

## Large bodies

- **`new` accepts a body of any size.** A refusal would make the agent send the whole entry again,
  and those tokens are exactly what filing must not waste.
- **A body embeds as chunks**: at most 3,200 runes each, 320 overlapping, split on blank lines, then
  lines, then spaces, sdd's separators, without a Markdown parser. Each chunk is prefixed with the
  entry's title (`Entry: <title>`), and an entry scores as its best chunk.
- **The skill names a word limit, and the limit is the tool's configuration**: `userConfig` key
  `entry_max_words`, substituted into the skill as `${user_config.entry_max_words}` and read by
  `agentic-memory` from `pluginConfigs` like the embedding keys. An entry past it still lands; the
  reply notes the count against the limit.
- **`entry_max_words` defaults to 500**, about 3,200 runes, one chunk: an entry within it has one
  vector and the sharpest semantic match. Rejected: 1000, whose two halves match a query
  separately, each weaker than an entry about one thing; 300, which spends more calls on filing.
- Rejected: a cap refusing an oversized body. The agent resends the whole entry, split, at the cost
  of every token twice.
- Rejected: a small cap with one vector per entry. Forces terse bodies, against unconstrained
  memory.
- Accepted cost: one pasted log can seal the tail at once, and weighs on every keyword scan.

## Seats

- **Seats stay: different seats do different jobs.** A seat is still a named job held by one
  session at a time, and claiming one is what turns `agentic-memory` on for a session.
- **Every entry carries the seat that filed it**, recorded by the tool from the claim, never typed
  by the agent.
- **Search defaults to the searching session's own seat.** It can name other seats or take every
  seat of the project, so one seat learns what another holds: work can move between seats, and
  nothing is done twice.
- **Seat commands, each also an MCP tool:**

  ```
  agentic-memory holder <seat>          exit 0 held, 1 free
  agentic-memory claim <seat>           a free seat; prints the brief and the slice
  agentic-memory claim <seat> --force   seizes it from a wedged holder; prints the same
  agentic-memory release [<seat>]
  agentic-memory sweep                  lists every seat, frees each whose holder is gone
  ```

  A seat name matches `^[a-z][a-z0-9-]{0,23}$`, and a session holds one seat at a time.
- Rejected: a separate `take` beside `claim`. Two verbs for one act, differing in one condition.
- Rejected: keeping `claim`'s session-id argument. Seat lookup goes through the owning process, so
  the argument has nothing left to tell.
- `refreshed` goes: filing answers the nudge.
- **A call finds its seat through the process that owns it.** Command or MCP tool alike, it walks
  up to the owning `claude` process and matches that pid and start time against the claims, the
  way the carry-forward resolves a seat today. A Bash-run command and the plugin's MCP server both
  descend from that process, so both get the same answer, and `/clear` changes nothing. A process
  holding no claim is refused: `claim a seat first`.
- **The session id is attribution only.** Each entry records `CLAUDE_CODE_SESSION_ID` as the caller
  sees it, and nothing resolves through it.
- Rejected: resolving through `CLAUDE_CODE_SESSION_ID`. An MCP server keeps the id it was spawned
  with, so it is stale after `/clear` and may be the startup id after `--continue`.
- Rejected: the seat as an argument on every call. Burden on each call, and nothing would stop a
  session filing under a seat it does not hold.
- Accepted cost: seat lookup reads the process table.
- **A session holding no seat hears about memory only where some exists.** Where the project has a
  store under `~/.claude/agentic-memory/<project>/`, `SessionStart` prints one line naming each seat
  with its holder or `free` and its live count, and the claim command:
  `agentic-memory: this session holds no seat. Seats: main (db1e192e, 23 live), reviewer (free, 4
  live). Claim one: agentic-memory claim <seat>`. Without a store it prints nothing.
- Rejected: the line in every project. A line in every session on the machine, most of which never
  use memory.
- Rejected: never printing it. A project with memory but no instruction line would never learn the
  memory is waiting.
- Accepted cost: a project's first claim comes only from a prompt or an instruction line.
- Rejected: one shared pool without seat attribution. A seat's view would drown in other seats'
  working notes, and no one could tell whose finding an entry is.

## Stale truths

An entry outlives the code it describes, and the next session acts on it. Staleness is computed from
the tree at read time, never left to the agent's discipline. Ships from the first release.

- **Stamped at filing, by the tool:** time, seat, session, HEAD commit, branch. The commit shows
  abbreviated to at least 8 characters (`git rev-parse --short=8`, longer where git needs it to
  stay unambiguous).
- **Anchors, automatic and explicit.** Every token in the title or body that resolves to a file in
  the tree at filing time anchors the entry (`grounding.go:324` anchors `grounding.go`), and
  `--about <path>` adds one. Each anchor records the file's content hash.
- **Flags on every result, computed at read time:** `drifted: <path>` where an anchored file's
  content changed, `gone: <path>` where it no longer exists, `other branch: <name>` where the entry
  was filed on a branch other than the current one, and the entry's age.
- **An anchor is a path relative to the checkout root**, hashed in the filing checkout and compared
  at read time against the caller's own: a worktree on another branch shows `drifted`, and
  `other branch` says why.
- **`confirm <id>` re-anchors.** A session that verified an entry records that in one call: anchors
  re-hashed against the current tree, a verified stamp added, nothing rewritten.
- **A flag never hides an entry.** Staleness neither retires nor supersedes; both stay the agent's
  call.
- Rejected: anchors from `--about` alone. Agents forget the flag, so most entries would carry none
  and staleness would fall back to age.
- Rejected: commit distance alone. Every entry flags after any commit, and a flag on everything
  trains the agent to ignore flags.
- Accepted cost: a path named only as an example anchors too, and drifts until `confirm` clears it.
- Deferred: claim kinds decaying at different rates, and suggesting a supersede automatically.

## Embedding configuration

- **One embedder per machine, set with the plugin.** Every project's vectors come from the same
  model, so a per-project override would only split one machine's memory into vector spaces that
  cannot be compared. No project-level file or setting is read.
- **The plugin declares `userConfig`:** `embedding_provider` (`openai` for any OpenAI-compatible
  endpoint, or `ollama`), `embedding_model`, `embedding_endpoint`, `embedding_dimensions`,
  `embedding_api_key_env`. Claude Code's plugin dialog writes the answers to `pluginConfigs`
  under `agentic-helpers@agentic-helpers` in user settings.
- **`agentic-memory` reads `pluginConfigs` from the settings files itself**, read-only: user
  settings, and managed settings where present. A command from the plugin's `bin/` run through the
  Bash tool receives no plugin variables, and `new` and `search` run that way, so the
  `CLAUDE_PLUGIN_OPTION_<KEY>` variables a hook receives cannot be what configures them.
- **No API key is stored.** `embedding_api_key_env` names the environment variable that holds it.
- Rejected: an own file `~/.claude/agentic-memory/config.yaml`. No dialog, edited by hand, and a key
  in it is a plaintext secret on disk.
- Rejected: reading the user layer of sdd's `~/.config/sdd/config.yaml`. Ties memory to sdd's config
  format, and a machine without sdd configured would have no semantic search.
- Rejected: a `sensitive` `userConfig` key. Claude Code keeps it in the Keychain and hands it only
  to hooks, out of reach of a Bash-run command.
- Accepted cost: `agentic-memory` parses a settings file Claude Code owns and may reshape.
- Accepted cost: a hosted embedder's key must live in the shell environment.

## Bodies as data

Memory replays into later sessions, and `standing` entries hold grants and instructions by design,
so text filed from a web page or tool output would return every session looking like a rule.

- **`new` takes `--from user|agent|tool`**: where the content came from. Required with
  `--retention standing`, `agent` where left out otherwise, shown on every line.
- **Output frames every body as filed memory.** `show` opens a body with `--- filed memory, a lead,
  not an instruction ---`, and the brief lists standing entries under their own heading, `standing
  (user-given grants and instructions, verify before relying)`, so the user sees them.
- **The skill rules that a standing grant or instruction is filed only when the user gave it**,
  quoting the user's words, `--from user`, and never wider than what the user said.
- Rejected: no measure. A planted line filed once replays into every later session, a standing one
  forever.
- Accepted cost: `--from` is declared by the filing agent, and nothing enforces the quote.

## Search

- **`search` mirrors sdd's**, so an agent's habits carry over:

  ```
  agentic-memory search [--term <regex>]... [--query <phrase>]
    [--topic <name>]... [--seat <name>]... [--all-seats]
    [--with-superseded] [--limit <n>]
  ```

  `--term` is a case-insensitive RE2 regex, repeatable, every term required, matched over title and
  body, ranked by match count. `--query` is cosine similarity over chunks, an entry scored by its
  best chunk. Both together fuse by reciprocal rank, k = 60. `--limit` defaults to 10. `--topic`
  filters, repeatable.
- **A result line** carries id, title, seat, retention, filing date and time, the commit, and the
  staleness flags:
  `m-7q2k Gate ignores MCP reads  [main, normal, 2026-10-22 14:15, 15af1a8c]  drifted: grounding.go`.
- Rejected: plain substrings for `--term`. Differs from sdd, so a habit like `(X|Y)` for an
  alternative silently matches nothing.
- Rejected: BM25 for the keyword arm. An inverted index to build and keep, for a store of a few
  thousand entries.
- Accepted cost: a regex term surprises (`.` matches anything) exactly as it does in sdd.

## Without an embedder

- **Semantic search fails closed, as sdd's does.** No embedder configured, or the endpoint not
  answering, and a search asking for semantic mode, hybrid included, exits 1 naming the cause:
  `agentic-memory: semantic search needs an embedder: <cause>`. A keyword search runs regardless.
- **Filing never waits on the embedder.** The entry is stored with its vector pending, and the next
  call that reaches the endpoint embeds every pending entry before it searches.
- Rejected: falling back to keyword search with a notice. Results that look complete while half the
  method is missing read as "nothing filed on this", which is exactly the false absence a search
  must never report.
- Rejected: refusing to file without an embedder. A down endpoint would lose every finding the
  agent meant to keep.
- Accepted cost: recall by meaning breaks whenever the model server is down, until it is back.

## Transcript

- **Claude Code's own transcript is read in place.** Every hook payload names it in
  `transcript_path`; nothing is copied, so `.memory/transcripts/` and its `.ctx` offset files go.
- **After a compaction the recent turns come back.** At `SessionStart` with source `compact`, the
  turns recorded after the seat's newest entry are condensed (prompt, tool summaries, reply) and
  printed in pieces of at most 9,000 characters, at most five, newest last: Claude Code swaps hook
  output past 10,000 characters for a file path, which a compaction summary then outranks. The
  entry's timestamp decides where to start, so no state file tracks it.
- Rejected: filing the condensed turns as an `unreviewed` entry when a seat filed nothing for a
  whole compaction window. Raw logs in search results bury the findings, and each would be one
  large body.
- Rejected: reading no transcript. Whatever the compaction summary drops and nobody filed is gone.
- Accepted cost: the transcript's format is Claude Code's, and may change under the reader.
- Accepted cost: the re-injected turns overlap the compaction summary.

## Nudge before compaction

- **The ladder stays, and filing answers it.** The `UserPromptSubmit` hook nudges once the context
  passes 65% of the window it compacts at and re-arms ten points higher each time; without a fresh
  context reading, 400 KB of transcript growth since the last answer stands in. The nudge reads:
  `Context near compaction. File what the summary would lose: findings, open questions, judgment
  calls, where work stands.` followed by the `new` command. Any entry the seat files after a
  nudge answers it; there is no `refreshed` step.
- **Two unanswered nudges, and the `Stop` hook exits 2** with the backstop, as the carry-forward's
  does.
- **The context reading lives at `~/.claude/agentic-memory/sessions/<session>.ctx`**, written by
  the status line as `{"used_percentage", "size", "at"}`. Keyed by session alone, so the status
  line writes it without knowing the project key, and session ids are unique across projects.
- Rejected: a nudge with no backstop. An agent deep in work ignores it, and what it never filed
  survives only in the capped re-injected turns.
- Rejected: no nudge at all. Anything older than the re-injected turns and never filed is lost at
  compaction.
- Accepted cost: the backstop still forces a turn, and a status line writing the old path stops
  feeding the ladder until it moves.

## No handoff

- **Handoff is gone.** No handoff prompt, no handoff file, no rewrite. Filing as work goes and the
  nudge keep memory current; ending work is `agentic-memory release`, or nothing, since the sweep
  reaps a dead holder; the next claim gets the brief.
- **A session's last turns survive its end.** At `SessionEnd`, a session holding a seat condenses
  the turns recorded after the seat's newest entry from `transcript_path`, the way a compaction
  re-injects them, into `~/.claude/agentic-memory/<project>/seats/<seat>.slice`, replacing the
  previous one. A slice is not an entry: it is never searched and never briefed beyond the next
  claim.
- **The claim records the holder's `transcript_path`**, so a session that ended without
  `SessionEnd` (killed, crashed) still leaves its turns: the next claim reads them from that
  transcript directly.
- **The next claim prints the slice after the brief**, at most 20,000 characters, newest last: from
  the slice file where it is newer than the seat's newest entry, else from the recorded transcript.
- Rejected: the 300-character handoff prompt. The brief at claim carries what it pointed at, so the
  prompt duplicated entries, and a longer one needed a courier file to delete.
- Rejected: a handoff entry shown first at the next claim. A second mechanism beside filing, for
  what a short-retention entry already does.
- Rejected: the slice taken at `SessionEnd` alone. A killed session runs no hook, and its last turns
  would be lost.
- Accepted cost: a transcript Claude Code has already cleaned up (`cleanupPeriodDays`) leaves no
  slice for a session that ended without `SessionEnd`.
- Accepted cost: nothing marks a deliberate end of work.

## What comes back to an agent

Every entry is kept, so the tool, not the agent, chooses what returns to a fresh context. Sessions
differ too much to key that choice on session events: one runs for days through many compactions,
one compacts once, one is closed early and its seat claimed again often. The choice keys on time,
use and importance instead, and claim and compaction follow the same rules.

- **Brief, at claim and after every compaction.** One piece of at most 9,000 characters, filled in
  priority order until the budget runs out: the entries an agent must not forget, then this seat's
  live entries ranked by heat, then titles other seats filed since this seat's previous brief, then
  topic counts with the number of entries cut and the command that lists them.
- **Heat** grows when an entry is filed, confirmed, shown, or returned by a search, and decays over
  wall-clock time. It lives in the cache, so a read never writes to the log, and losing the cache
  resets the ranking without losing memory.
- **Recall on prompt.** The `UserPromptSubmit` hook embeds each prompt and injects the top three
  entries above a similarity floor as titles and ids, skipping any already shown since the last
  compaction. An old entry returns when the work touches it again. The shown set is per session, in
  the cache. With no embedder the hook stays silent beyond one notice per session; a search still
  fails closed.
- **Recall searches the own seat, and other seats only above a higher bar.** Per prompt every live
  entry of the project is scored against it; a hit counts where it stands out from that
  distribution: the own seat's at `μ + 2σ` or above, another seat's at `μ + 3σ` or above, shown with
  its seat. Below 20 live entries the statistics are too thin and recall stays silent.
- Rejected: the own seat alone. A seat duplicating another's work would learn of it only by
  thinking to search across seats.
- Rejected: every seat at one floor. Other seats' working notes would crowd the three slots.
- Rejected: absolute cosine floors in the configuration. Their defaults fit one model, and switching
  the embedder would mean retuning them by hand.
- Rejected: floors calibrated from the store's pairwise similarities. A pairwise pass and a
  calibration to keep fresh, where the query's own distribution already says what stands out.
- Accepted cost: a floor in standard deviations is harder to explain than a number.
- **Pull** through `search` and `show`, unchanged.
- Rejected: brief and pull alone. An entry outside the working set returns only if the agent thinks
  to search for it, which right after a compaction it rarely does.
- Rejected: the newest entries until the budget runs out. In a long session yesterday's trap falls
  off the brief as soon as enough new entries land.
- Accepted cost: one embedding call per prompt.

### Retention

- **Filing takes `--retention <level>`, and the level sets heat's half-life.** `standing` never
  decays and is always briefed: standing grants, standing instructions, traps. `long` 90 days,
  `normal` 14 days (the default), `short` 3 days, `scratch` 12 hours, the short-term memory.
- **`standing` is the brief's first tier, capped at a third of the brief.** Past the cap the brief
  says so: `standing entries exceed a third of the brief: <n>; demote some`.
- **`agentic-memory retain <id> <level>`** changes it later, as one event in the log.
- Rejected: numeric severity `0`-`5`. Agents read numbers inconsistently, and the word suggests how
  bad a bug is, where the value sets how long an entry stays in view.
- Rejected: a severity weight beside a separate pin. Two knobs for one intent drift apart, and a
  weight multiplying a decaying heat has to be balanced against it.
- Accepted cost: one more word for agents to learn, coarser than a number.

## Filing aids

- **An entry always lands; the reply then points at what it may duplicate.** Where `new` coins a
  new topic, the reply names up to three nearest existing topics with their counts and the merge
  command. It also names up to three similar live entries of the same seat with their similarity and
  the supersede command:

  ```
  filed m-9xq [main, normal, 15af1a8c]
  new topic authn-gate; near: gate (6), grounding (2)
    → agentic-memory merge authn-gate gate
  similar: m-7q2 Gate ignores MCP reads (0.91)
    → agentic-memory supersede m-7q2 --by m-9xq
  ```

- **A malformed entry lands too, with a note.** A title outside 3-10 words or an entry without a
  topic is filed as given, and the reply names the fix:
  `note: title has 1 word, a title takes 3-10; agentic-memory retitle m-9xq '<title>'`.
  `agentic-memory retitle <id> '<title>'` and `agentic-memory tag <id> <topic>...` are events like
  every other, the seat's own entries only.
- Rejected: refusing before the body is read. A CLI caller piping the body still sends it, and the
  rule against resending a whole entry holds for every refusal.
- Rejected: refusing the whole entry. The agent resends it, the cost ruled out for long bodies.
- Accepted cost: two more subcommands and event kinds, and a malformed entry stays until fixed.
- Nearness comes from the embedder, and from keyword and spelling distance when it does not answer,
  since filing never waits on it.
- Rejected: holding the write on a near match until the agent reruns with a choice. Every match,
  false ones included, costs a second call, and a refused write is a finding at risk.
- Rejected: no aid, tidying at handoff. Sprawl accumulates between handoffs, and a session that
  never hands off never tidies.
- Accepted cost: a duplicate lands first, and its cleanup is a second call the agent may skip.

## Lifecycle

- **Retire keeps an entry in view.** A retired entry still shows in search results, flagged
  `retired`, and leaves the brief.
- **Supersede carries exactly one of `--by <id>` or `--because '<why>'`.** `--by` names what replaced
  the entry; `--because` says why it was wrong with nothing to replace it. Neither or both is a usage
  error. The event, and with it the successor or the reason, outlives the body.
- **A superseded entry leaves the results.** Superseded `--by` another entry, the successor shadows
  it for good: no search returns it again, `--with-superseded` included. Superseded `--because`,
  nothing stands in for it, so `--with-superseded` brings it back into a search, flagged
  `superseded: <why>`. Either way the body stays indexed for 14 days, and the next merge after that
  drops it.
- Rejected: `--by` required. An entry that was simply wrong has no successor, so it could only be
  retired and would stay in results, flagged as if it had once been true.
- Rejected: a bare `supersede <id>`. Nobody could later tell replaced from wrong from removed by
  mistake.
- Accepted cost: one more argument on every supersede.
- **Another seat may supersede or confirm an entry; retire and retain stay with the seat that filed
  it.** Every event records the acting seat, and the owning seat's next brief lists what other seats
  changed: `changed by other seats: m-7q2 superseded by sdd-1c: fixed in 2d1fc1f8`. A retire or
  retain on another seat's entry is refused: `m-7q2 belongs to main`.
- Rejected: the filing seat alone. A wrong entry would stay in every seat's results until its owner
  seat is claimed again, which for a rarely used seat may be never.
- Rejected: any seat, any event. One seat's handoff could retire another seat's working set behind
  its back.
- Accepted cost: a seat can take another seat's entry out of results, though never unseen.
- **Topics are one namespace per project, shared by every seat.** `agentic-memory merge <from>
  <into>` is an event applied at read time: every seat's entries tagged `<from>` read as `<into>`.
  It is attributed, and every other seat's next brief lists it: `topic authn-gate merged into gate
  by main`. Filing aids look across every seat's topics.
- Rejected: topics per seat. One name would mean different sets per seat, and a search by topic
  across seats would fragment.
- Rejected: one namespace with a merge re-tagging only the merging seat's entries. Topics would end
  half-merged, the sprawl kept in every seat that never merges.
- Accepted cost: one seat's merge reshapes another seat's topic list.
- **`agentic-memory revive <id>` reverses the latest retire or supersede**, under the rights that
  event needed: a retire by the owning seat only, a supersede by any seat while the body is still
  indexed. Past 14 days it answers `m-7q2 body dropped; file it again`. Attributed like every event.
- Rejected: undoing a retire only, a supersede being final. A premature supersede by another seat
  would cost the owner a re-file.
- Rejected: no undo. A mistaken retire at handoff would lose the entry's age, anchors and heat.

## Switch-over

- **One release replaces the carry-forward.** It adds `agentic-memory` and its skill
  `agentic-helpers:agent-memory`, and removes `agentic-carryforward`, the
  `agentic-helpers:agent-carryforward` skill, and every read of `.memory/`. Every caller changes in
  the same release: the hook texts quoting `agentic-carryforward`, the skills, fdbox's seat,
  instruction files naming the old skill, and a status line writing the context reading under
  `.memory/transcripts/`.
- **The grounding gate's `exempt_seats` resolves through the same seat lookup**, the owning
  process, in place of the session id under `.memory/roles/`. A subagent shares its session's
  process, so it still counts as that seat, and `/clear` no longer drops the exemption.
- **The scratch sweep's `.memory/` check goes:** nothing is left for it to measure.
- **No import command: the agent files the old memory itself.** The `agentic-helpers:agent-memory`
  skill tells a session claiming a seat whose `.memory/carryforward/<seat>-memory.md` still exists
  to read it once, file each finding as a proper entry with its own title, topics and retention,
  and then delete the file. A project deletes `.memory/` once no carry-forward file is left.
- Rejected: one entry per `##` section, filed mechanically. Coarse entries titled after a heading,
  each needing a later refine that may never come.
- Rejected: one entry per bullet. Titles cut mid-thought, and dozens of entries of uneven quality.
- Accepted cost: whatever the agent skips is gone once the file is deleted.
- Rejected: a trial beside the carry-forward. Two seat stores and a gate checking both, for a
  design that replaces rather than extends.
- Rejected: a forwarding shim under the old name. A shim to carry and remove, and no real trial,
  since the old memory stops at once anyway.
- Accepted cost: no fallback if memory disappoints, short of reverting the release.

## Every surface

### Agent commands, each also an MCP tool of the same name

```
agentic-memory new --title '<3-10 words>' --topic <name>... [--retention <level>]
                   [--from user|agent|tool] [--about <path>]...        body on stdin
agentic-memory search [--term <regex>]... [--query <phrase>] [--topic <name>]...
                      [--seat <name>]... [--all-seats] [--with-superseded] [--limit <n>]
agentic-memory show <id>...           framed body, stamps, anchors, flags, events
agentic-memory brief [--topic <name>]  the brief; with --topic, that topic's entries past the budget
agentic-memory topics                 every topic of the project with live counts per seat
agentic-memory confirm <id>
agentic-memory retain <id> <level>
agentic-memory retire <id>...
agentic-memory supersede <id> --by <id> | --because '<why>'
agentic-memory revive <id>
agentic-memory retitle <id> '<title>'
agentic-memory tag <id> <topic>...
agentic-memory merge <from> <into>
agentic-memory holder <seat>
agentic-memory claim <seat> [--force]
agentic-memory release [<seat>]
agentic-memory sweep
```

- Over MCP the body is the `body` parameter, and repeatable flags are arrays.
- Exit codes: 0 done, 1 refused or usage (the message names which), 2 only from the `stop` hook.

### Operator commands

```
agentic-memory mcp                    the stdio MCP server
agentic-memory dump <segment>|--tail  a log segment or the tail, decoded, one event per line
```

### Hook events, wired in `plugin/hooks/hooks.json` through `hooks/run`

| Event | Command | Does |
|---|---|---|
| `SessionStart` | `agentic-memory session-start` | no-seat line where a store exists; after a compaction, the brief |
| `SessionStart` | `agentic-memory session-log <1-5>` | after a compaction, piece n of the recent turns |
| `UserPromptSubmit` | `agentic-memory prompt` | nudge ladder, prompt recall |
| `Stop` | `agentic-memory stop` | backstop after two unanswered nudges (exit 2) |
| `SessionEnd` | `agentic-memory session-end` | the seat's slice |

### `userConfig` keys of the plugin

| Key | Type | Default |
|---|---|---|
| `embedding_provider` | string, `openai` or `ollama` | none: semantic search fails closed |
| `embedding_model` | string | none |
| `embedding_endpoint` | string | the provider's default |
| `embedding_dimensions` | number | the model's own |
| `embedding_api_key_env` | string, an environment variable's name | none |
| `entry_max_words` | number | 500 |

### Paths

```
~/.claude/agentic-memory/<project>/log/tail.jsonl          open segment
~/.claude/agentic-memory/<project>/log/<sha256[:8]>.seg    sealed segments
~/.claude/agentic-memory/<project>/log/manifest.json       sealed segments in order
~/.claude/agentic-memory/<project>/log/.lock               the log's lock
~/.claude/agentic-memory/<project>/seats/<seat>.json       claim: pid, start, session, transcript_path
~/.claude/agentic-memory/<project>/seats/<seat>.slice      last turns of the seat's last session
~/.claude/agentic-memory/sessions/<session>.ctx            context reading from the status line
~/.cache/agentic-memory/<project>/<sha256(fingerprint)[:12]>/vectors.seg   chunk vectors
~/.cache/agentic-memory/<project>/heat.json                heat per entry
~/.cache/agentic-memory/<project>/shown/<session>.json     entries shown since the last compaction
```

- **Vectors are keyed by the embedder's fingerprint**, provider, model and dimensions, as sdd keys
  its index: a changed embedder starts a fresh vector store instead of mixing spaces.

### Defaults not settled elsewhere

- **Heat increments:** `new` +1, `confirm` +1, `show` +0.5, a search hit +0.25, a recall shown
  +0.25. A brief adds nothing, or the brief would keep what it shows hot by showing it.
- **Cache cleanup:** `SessionEnd` removes the session's `shown/` file; `SessionStart` removes
  `sessions/*.ctx` and `shown/*.json` older than 7 days.

## Acceptance criteria

Storage and filing:

1. `agentic-memory new` from a process holding no seat exits 1 with `claim a seat first`, and writes
   nothing.
2. A filed entry has an id matching `^m-[a-z0-9]{4}$`, and a stamp carrying time, seat, session,
   branch and the HEAD commit abbreviated to at least 8 characters.
3. A 40 KB body is filed; past `entry_max_words` the reply names the word count against the limit.
4. A title outside 3-10 words, or no topic, is filed, and the reply names `retitle` or `tag`.
5. The write that pushes `log/tail.jsonl` past 65536 bytes seals it: a new `<sha256[:8]>.seg`, the
   manifest listing it, an empty tail.
6. Eight sealed segments merge into one. The merge drops the body of an entry superseded more than
   14 days earlier and keeps one superseded more recently.
7. Events from two processes appending at once both land, and neither corrupts the tail.

Search:

8. `--term` is case-insensitive, every term required, over title and body; a search covers the own
   seat unless `--seat` or `--all-seats` widens it, and `--topic` narrows it.
9. `--query` or hybrid with no embedder configured, or the endpoint not answering, exits 1 with
   `agentic-memory: semantic search needs an embedder: <cause>`; `--term` alone still answers.
10. An entry filed while the endpoint is down is embedded by the next call that reaches it.
11. Every result line carries the filing date and time; a superseded entry shown also carries the
    time of its supersession.

Lifecycle:

12. `supersede` with neither or both of `--by` and `--because` is a usage error.
13. An entry superseded `--by` never returns in a search, `--with-superseded` included; one
    superseded `--because` returns with `--with-superseded`, flagged with its reason.
14. `retire`, `retain`, `retitle` or `tag` on another seat's entry exits 1 with
    `<id> belongs to <seat>`; `supersede` and `confirm` succeed, and the owner's next brief lists
    them under `changed by other seats`.
15. `merge <from> <into>` makes every seat's `<from>` entries read as `<into>`.
16. `revive` reverses the latest retire or supersede; past 14 days it answers
    `<id> body dropped; file it again`.

Stale truths:

17. A token in title or body resolving to a file anchors the entry, `--about` adds one; a changed
    file shows `drifted: <path>`, a deleted one `gone: <path>`, a filing branch other than the
    current one `other branch: <name>`; `confirm` clears the drift.

Recall:

18. The brief stays within 9,000 characters, opens with `standing` entries capped at a third of it
    and names the overflow, lists no retired entry, and names the entries cut.
19. Prompt recall stays silent below 20 live entries, injects an own-seat hit at `μ + 2σ` or above
    and another seat's at `μ + 3σ` or above, and never repeats an entry shown since the last
    compaction.

Seats and sessions:

20. A Bash-run command and an MCP tool call from the same session resolve the same seat, also after
    `/clear`.
21. `claim` refuses a live holder, `claim --force` seizes it, a session holds one seat at a time,
    and `sweep` frees a seat whose holder is gone.
22. `SessionStart` in a project without a store prints nothing; with one, a seatless session gets
    the one-line seat list.
23. The prompt hook nudges past 65% of the compaction window; an entry filed after the nudge answers
    it; after two unanswered nudges `stop` exits 2.
24. After a compaction `SessionStart` prints the turns after the seat's newest entry in at most five
    pieces of at most 9,000 characters.
25. `SessionEnd` writes `seats/<seat>.slice`; the next `claim` prints brief and slice within 20,000
    characters, and reads a killed session's turns from the recorded `transcript_path`.

Configuration and switch-over:

26. The embedder is read from `pluginConfigs` in user and managed settings; project settings are
    never read.
27. The MCP server `memory` lists every agent command as a tool, and a tool call answers what the
    command prints.
28. `agentic-carryforward`, its skill and every `.memory/` read are gone; the grounding gate's
    `exempt_seats` resolves through the owning process; `.goreleaser.yaml` builds `agentic-memory`.
29. `go vet ./...` and `go test -race ./...` pass in `source/hooks`, and `claude plugin validate .`
    passes.
