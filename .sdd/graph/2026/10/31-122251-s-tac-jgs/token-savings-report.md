# Keeping agent token use low: what agentic-helpers did

Audience: the agent working on sdd (decision-graph CLI with an MCP engine serving procedures,
searches and entry chains). Source: the agentic-helpers decision graph (`.sdd/`), its memory notes
with measurements, its git history, and the last output audit. Ids are graph ids; short forms such
as `d-tac-00q` resolve with `sdd show` in the agentic-helpers checkout. `m-xxxxxx` ids are
agentic-memory notes (`agentic-memory show <id> --no-heat --again`), not graph entries.

Units: "chars" are characters of text as the agent receives it; tokens were estimated as chars/4
unless a line says the figure came from logged `output_tokens`.

---

## 1. Principles and rules

| Rule | Id | Why, in one line |
|---|---|---|
| Token-cost criteria: least risky option first (keeps agents informed, avoids mistakes, keeps them obeying rules), fewest tokens second among equal-risk options; agents decide without asking the owner and record the reasoning | 20261029-021515-d-tac-00q | Every surface loads into many sessions so savings multiply, but an uninformed agent costs more than the tokens saved |
| A saving that hides information an agent acts on, or weakens rule wording, breaks the criteria | 20261029-021515-d-tac-00q | Fixes the floor under every trim, so "fewest tokens" can never win alone |
| Hidden (agent-only) hook messages fire only when they change what the agent does next, at most once per cause, one line naming cause and the one command that answers it; thresholds are owner options; a hook that can act without judgment acts silently | 20261027-222117-d-tac-yjy | A frequent message trains agents to skip it and weakens every message beside it |
| No cut loses accuracy: a value printed twice prints once; settled or ended items fold into a count line naming the command that brings them back; a value a reader cannot reconstruct stays | 20261031-102607-d-tac-lsl | The owner's bar for the full output audit |
| Empty-context test: every trim must hold for an agent that knows nothing beyond the output in front of it (after compaction, `/clear`, resume, new session); a trim may point at earlier output only within one context | 20261031-102822-d-tac-n3k | The surfaces exist so an agent that lost its context can pick the work up |
| Every fold or paging line carries its own run trigger, e.g. `127 more: --skip 2  (run before concluding none fits)`; the rule is also stated once in the skills and MCP descriptions | 20261031-115812-d-tac-wit | Agents ignore a bare `5 more` and conclude from the visible part; skills are gone after compaction, a printed trigger is not |
| Once-per-context presentation: a repeat read of something already shown prints a pointer `<id> <title> (seen)`; reset on compaction, `/clear`, resume; `--again` forces full output | 20261029-110631-d-tac-99b, 20261029-110801-d-tac-myr | Overlapping searches re-spent full lines and bodies already in context |
| Agent-only stores are written terse (memory entries, tool notes); graph entries stay plain prose | 20261029-021652-d-tac-xzt | Agents are the only readers there; people read the graph |
| Agents state outcomes, never play back the user's prompt; only a rule keeps the user's words verbatim | 20261030-105846-d-tac-y40 | Playback costs output tokens (about 5x input) once and input tokens on every later read |
| Agent output into stores is the costliest token kind: write the state slot only when a nudge asks | 20261030-125612-d-tac-hz8 | 48% of measured state writes were unprompted and redundant |
| Skill core holds what nearly every session uses plus every rule obeyed unprompted; rare-path text goes into files beside it, routed by a "Read when" table keyed on reply strings | 20261029-021831-d-tac-93f, 20261030-125759-d-tac-eh9 | Skills load whole and stay in context; the core regrew as features landed |
| MCP answers to agents are text (`{"message": ...}`); JSON stays for programs (CLI `--json`) and for answers where JSON is no larger | 20261030-202708-d-tac-962, 20261030-204038-d-tac-ahb, 20261030-211222-d-tac-dqs | Agents read text as readily as keys; JSON cost 2 to 7x its text |
| Write replies never echo what the caller just sent | 20261030-202708-d-tac-962, 20261030-204038-d-tac-ahb | The caller has its own input; only computed or normalized values are news |
| JSON contract: a key appears only when it holds a value, a reader treats an absent key as empty, keys are never renamed | 20261029-130234-d-tac-s4c | Empty keys were most of a search hit's bytes; the contract keeps scripts working |
| Do not list what the model never reads: no MCP `outputSchema`; seldom-run commands CLI-only | 20261029-022449-d-tac-ygu | Claude Code shows only description and input schema; output schemas were two thirds of the listing |
| Visibility beats saving: memory bodies stay append-only (supersede, never edit in place) | 20261029-093149-d-tac-ml8 | An in-place edit hides a change other agents already acted on |
| Spend a read where the risk sits (read foreign tools before running, own-project tools may run unread) | 20261030-011145-d-tac-3kj | Least risk first applied to a token cost that buys safety |
| Release level follows the real readers: token trims to agent-read answers ship as minor releases | 20261029-131013-d-tac-xcu | A major release holds back a saving for a contract few readers notice |
| A hook's output wording is part of its contract: change it on purpose, in the same change as every skill and doc quoting it | AGENTS.md; restated in 20261029-023134-d-tac-xv3, 20261029-203232-d-tac-def | Skills and agents quote hook text; drift breaks routing ("Read when" tables, rules keyed on reply strings) |

---

## 2. Method

**Measure first, on real transcripts.** The program opened as an owner todo (m-5qr8jk): measure
every surface that lands in context (brief, recall, PostToolUse notices, MCP server instructions,
tool names and schemas, skills when loaded, CLI and MCP replies, JSON vs text, link-flag lines)
with a tokenizer or bytes/4 on real sessions, then optimize.

- **Baseline census** (m-bo6xq8, 3 days, 87 seated sessions, `~/.claude/projects/*/*.jsonl`,
  chars/4): agent-memory skill 32.3k chars per load x 50 loads; filing replies 407k chars over 1026
  filings, 62% similar-entry hints; brief 2,237 chars avg; recall 235 per prompt; grounding gate
  note 529 avg; memory `tools/list` 47.7k chars for 32 tools; skill listing share 1,415 chars per
  session. The census also showed which costs were not agentic-helpers' own.
- **Re-measure after release** (m-8kippq, 36 h, 71 sessions): brief 2,237 -> 2,446, recall
  235 -> 206, gate note 529 -> 444, skill listing share 1,415 -> 763; found the new top costs
  (state writes 611, 48% unprompted; skill core regrown 11.6k -> 19k; MCP `who` 9.5k per call),
  which drove d-tac-hz8, d-tac-eh9 and d-tac-dqs.
- **Output tokens from the log, not estimates** (d-tac-oee, attachment `measurements.md`):
  `output_tokens` per API message, deduplicated by `message.id`, thinking included; spans classed as
  write, use and register. Stated assumptions: output priced 5x input, 3.5 bytes per token. This
  overturned an estimate-based decision (d-tac-fwc) whose figures were off by more than 10x.
- **Act-on rates**: what share of shown items an agent then used. Similar-entry hints by score band
  (d-tac-xx4: under 0.6, 101 shown, 4 acted on); recall hits named in the next 15 tool calls (16%,
  d-tac-u8e); usage lines in tool picks (0 of 19 lookups turned on usage, s-tac-nim); takes read
  before running (18 to 26 of 40 to 51, d-tac-3n3, d-tac-3kj).
- **Information-overlap analysis** before cutting a recovery surface (m-kodfew, d-tac-jcu): of 3,745
  exact identifiers in post-compaction replays, only 38% also appear in Claude Code's summary or
  kept messages; the replay carries the other 62%, so its budget stayed.
- **Spike before deciding** (d-tac-jcu): a session-end LLM wrap-up was run on the six longest
  slices with two models before rejecting it on measured quality and saving.
- **Content counts** of the store itself (d-tac-y40: share of quoted text; m-brnlbc: body length
  distribution before deciding whether empty bodies may be allowed; d-tac-jkj: words re-emitted by
  37 superseded todos, about 9.5k output tokens).
- **Full audit in an isolated home** (`.tmp/claude/main/audit/`): scripts set up a throwaway
  `HOME` and repository, ran every verb and hook (reads, writes, hook events, MCP `tools/list` and
  calls over stdio), captured each output to a file with its byte count, and attached `file:line`
  per finding (`trim-spec.md`). `est.py` replayed candidate folds on the captures to estimate
  savings before any code changed; `size.py` sized each tool's description, input and output schema.
  The audit also surfaced bugs that cost tokens (a `--limit` ignored: 125 lines for `--limit 2`).

**How choices were made.**
- Rank by d-tac-00q: list options with their risk, drop any that hides an acted-on value, then pick
  the cheapest; record rejected options with figures (each entry's "Rejected" section).
- Prefer cuts that remove pure duplication (echo of input, typed fields repeating the text, empty
  keys, values printed twice) before cuts that remove information.
- Fold rather than drop: settled or ended items become a count line naming the command that
  brings them back, plus a trigger saying when to run it (d-tac-lsl, d-tac-wit).
- Apply the empty-context test as a second pass: four trims that passed the first bar were
  withdrawn (d-tac-n3k).
- Owner left choices to the criteria where risk was equal (d-tac-3n3, d-tac-s6o); owner set only
  release levels (d-tac-xcu) and the bar.

**How each was verified.**
- Tests per criterion; functional tests against trees in `t.TempDir()`; golden files for the brief
  and `--json` reads.
- Break every new guard, watch its own assertion fail, restore byte-identically; guards no test can
  fail are removed (five in s-tac-8yz) or named unreachable (one in the replay loop).
- Live proof on the installed build against the real store or a stdio MCP probe: `tools/list`
  9,189 chars with no `json` key (s-tac-sz7); gate note fired on the first of two identical
  questions only (s-tac-7st); todo `show` 8,805 -> 870 bytes (s-tac-8yz).
- Re-measure in acceptance: y40's no-playback rule re-counted after release (52 of 189 entries
  narrated the owner before, 1 of 23 after, s-tac-n66); hz8's unprompted share re-measured after
  release (open todo m-dgkly9, due 2026-11-06).
- Wording checks: a script verified every quoted reply string survived a skill move (s-tac-zx5).

---

## 3. Every change, by surface

Before/after figures are as recorded in the named entries. "Rej." lists rejected alternatives worth
knowing.

### 3.1 Brief (printed at seat claim and after every compaction)

- **d-tac-5no** Short line `<id> <title>  [<age>[, <seat>][, retain <level>]]  <flags>`: seat only
  when not the reader's, no source, stamp or commit; notes capped by `brief_note_limit` (10) with
  `+<n> more notes: agentic-memory brief --kind note`; superseded citers leave backlink lists.
  Rej.: short line in `search` too (similar titles told apart by time and commit, every pick would
  cost a `show`).
- **d-tac-u8e** Ended link flags fold into a count keeping successor ids
  (`→ 17 ended, 1 superseded by <id>; ← 1`); open and settled links stay listed; `retire?` flags
  printed in place instead of a section repeating whole lines. Link flags were 1.9k of a 7.2k brief
  (one note with 18 links: 1.2k). Brief 7,385 -> 4,775 chars (s-tac-5j2). Rej.: links as a bare
  count (hides successor ids), cap at three flags, folding settled links, `retire?` as ids only.
- **d-tac-bon** Narrower inferred-end set (no candidate from a memory target the entry itself
  superseded; no todo end from memory links): fewer false `retire?` lines.
- **d-tac-8h5** Due times: an entry due later costs one count line `<n> due later (next <time>)`
  instead of a line per brief; due entries get a capped section (5, then `+<n> more due`).
- **d-tac-lsl (M5, M6, M8)** Citing line drops the seat when it is the reader's own; rules skip
  do-now ids printed above; news skips entries printed in a section above; runs of 2+ seen entries
  fold into `<n> seen above, unchanged`; a seat's first brief replaces other seats' lists with
  `+<n> from other seats: agentic-memory search --all-seats --status live`; claim prints one header.
- **d-tac-n3k** Kept: the citing line's date (printed nowhere else).
- **d-tac-xzt** Header names the style (`entries in <lang>, terse`), so agents write terse.
- **d-tac-fki** Hook-written state drafts print nothing and count for no nudge.
- **d-tac-mvn** Outer budget 9,000 chars with a footer; claim budget 20,000 for brief plus slice.

### 3.2 Per-prompt recall (UserPromptSubmit)

- **d-tac-mvn** Top three hits standing out at mean + 2 sd (another seat's at + 3 sd); never repeats
  an entry before a compaction.
- **d-tac-u8e** Measured 1,976 blocks, 412k chars, about 208 chars (~50 tokens) per prompt; only
  the link fold applies. Rej.: dropping `[seat, date]` on cited-by sublines (5% saved, loses who and
  when); an absolute similarity floor (a floor too high hides a needed entry, unmeasurable since use
  is silent).
- **d-tac-99b** Recall prints no entry a read already printed in the current context.
- **d-tac-ak9** Showing in recall adds no heat; only use warms (keeps recall from re-ranking itself
  upward).
- **d-tac-qwp** Recall failure line once per outage, re-armed by a successful recall:
  `agentic-memory: prompt recall failed, retried each prompt: <error>`.
- **d-tac-lsl (B3)** Silent when the embedder is switched off (no failure line for a deliberate off).
- **d-tac-l3a** Prompt and query embeddings sized to the embedder's limit (an over-limit input was
  silently cut to its head).

### 3.3 Post-compaction replay and claim slice

- **d-tac-303** Replay capped at 5 pieces x 9,000 chars because Claude Code inlines hook output only
  up to 10,000 chars; a replay nudge fires once per newest entry when unfiled turns outgrow it; a
  cut replay names the cut count and transcript path in its first piece instead of dropping silently.
- **m-kodfew / d-tac-jcu** Replay mean 10,770 chars, never cut; 62% of its identifiers are in
  neither the summary nor kept messages, so the budget was not reduced.
- **d-tac-lsl (M10)** Replay markup `## <n>  <hh:mm>` per turn under a dated piece header, inline
  `asked:`, `replied:`, `did:` labels; pieces stay char-capped, so the saving buys more turns.
- **d-tac-n3k** Kept: the last-turns slice when a session re-claims its own seat (a compacted
  session holds only a summary of those turns). Rej.: guarding the skip with compaction detection.
- **d-tac-abw** Compaction plus replay stays the only recovery path; no `/clear` replay, no
  blocking auto-compaction (grows every turn's cost and fails unattended runs at the limit).

### 3.4 Hook messages and nudges

- **d-tac-yjy** The standing scarcity rule (section 1). Rej.: a per-session cap on hidden messages
  (silences the one that matters once spent); leaving frequency to each hook.
- **d-tac-ac7** Shipped-work state request holds Stop once per quiet period; threshold
  `state_stale_after_writes` (default 5) is an option.
- **d-tac-wbu** That request moved to hidden context on the next UserPromptSubmit; Stop holds only
  near compaction (Stop context is shown to the user).
- **d-tac-hz8** Agents write state only when a nudge asks: 611 writes in 36 h, median 15 per
  session, p50 713 chars, 48% unprompted, about 210k chars of agent output per 36 h. Rej.: raising
  the write threshold (leaves unprompted writes); a word cap on state (cuts what a resumed session
  reads first).
- **d-tac-lsl (B2)** Near-compaction nudge fires at most twice per rung, then the Stop backstop
  holds (it had fired on every prompt until the seat filed).
- **d-tac-qxo** Nudges measure against the window Claude Code actually compacts at
  (`compact_window` from `session.usage`), so the first rung is not past compaction.
- **d-tac-nu3, d-tac-oq1** Trap, note and todo notices at the matching tool call, once per session
  (again after compaction), at most one notice per call, no heat on delivery.
- **d-tac-427** Arrivals from other seats told once through seen marks; **d-tac-lsl (M7)** arrival
  lines use the short line.
- **d-tac-pih** Update notice at most once per machine per day, never on resume, silenceable.
- **d-tac-lsl (T3)** Tool library changed-copy nudge drops the copy path its command already holds.

### 3.5 MCP tool listings

- **d-tac-0gp** Descriptions in the words of schema keys, no CLI usage line; every key described
  (`ids` "an array even for one"); rules between keys stated. Cut refused calls (agents sent `id`
  for `ids`, both of two exclusive keys). Rej.: generated JSON examples (a schema cannot express
  cross-key rules), a replay token in refusals.
- **d-tac-ygu** Memory server `tools/list` 52.2k -> 9,189 chars, 32 -> 18 tools (s-tac-sz7): no
  `outputSchema` (35.7k of the listing, `brief` alone 16k), terse key descriptions (inputs 11.8k ->
  ~6k), 14 commands under 15 uses per 3 days made CLI-only and named in the skill. Audit later: memory
  11.2k, tools 6.4k chars. Rej.: keeping output schemas (never read by Claude).
- **d-tac-kib** `json` key dropped from both servers' schemas but let through when sent (no caller
  breaks). Replaced d-tac-lko's removal-with-refusal in a major release.
- **m-bo6xq8** Claude Code defers MCP schemas behind ToolSearch by default: names and server
  instructions load up front, a tool's description and inputs load when selected. Tool count and
  names are the always-paid part.
- **d-tac-wit** Search, show, brief, find and list descriptions carry the fold rule clause.
- **d-tac-y40** The `new` tool's `body` description states the no-playback rule.

### 3.6 MCP answers

- **d-tac-04v** (baseline, since undone in steps) One JSON object per call with `structuredContent`
  and an `outputSchema` per tool.
- **d-tac-s4c** Read answers drop empty keys (empty list or map, null, empty string, false); every
  ranked hit carries an integer percent `score`. Three hits had been 1,674 bytes JSON vs 513 as text.
  Rej. at the time: text over MCP (later adopted by d-tac-dqs); dropping default-valued fields.
- **d-tac-xcu** Shipped minor, not major, since readers are agents and the plugin's own skills.
- **d-tac-962** `new` answers `{"message": ...}` alone, first line `filed <id>` plus computed flags:
  42 replies 15.6k -> 2.3k chars, 85% (s-tac-lgp). Intermediate options measured: empty keys and int
  scores 12.5k (20%), message alone with echo 6.8k (56%).
- **d-tac-ahb** Every memory write tool answers message alone without echo: 907 replies 68.6k ->
  26.6k chars, 61%. `confirm`, `claim`, `release` unchanged (they carry what the caller did not
  send). Rej.: verb-only for `ack --add` and `confirm` (new item ids and re-hash counts are acted on).
- **d-tac-dqs** `search`, `brief`, `view`, `who` answer their CLI text: 10-hit search 5.1-7.4k ->
  1.9-3.7k bytes, brief 20.9k -> 3.0k, view list 20.3k -> 6.5k, `who --all` 14.8k -> 5.6k; about
  320k chars per 3 days, half JSON syntax. `show` and `topics` stay JSON (no larger than text).
  MCP `who` gained an `unused` key so the text's count stays reachable (s-tac-khj). Rej.: JSON with
  stamps stripped (30-40% saved, still costlier than text).
- **d-tac-lsl (T4, M1)** Tools `take` moves summary and merged-from into `tools[]`, drops
  `message`; find and list JSON drop `shown`, `page`, `matching` (equal to a count or input the
  reader has); a todo item's JSON `text` omitted where it equals `link`. `score` kept.

### 3.7 CLI read replies (agentic-memory `show`, `search`, `view`, `who`)

- **d-tac-99b** Seen pointer `<id> <title> (seen)` for repeats, two levels (line, body), keeps rank
  order; JSON `{"id","title","seen":true}`. Pointer with title at ~15 tokens chosen over a bare id
  at ~5 (the agent must recognise the entry without searching back). Rej.: one footer for seen hits
  (loses rank order, makes a page look emptier); dedupe within one command only; per-agent seen sets
  (no agent identity reaches the tools). Fresh subagents share the session id: skill tells them to
  pass `again`.
- **d-tac-lsl (M1)** Todo `show` folds settled items: `<n> settled (<d> done, <x> dropped): agentic-memory show <id> --all-items`;
  a 39-item todo 8,805 -> 870 bytes text, 5,625 -> 868 JSON (s-tac-8yz). The todo's own item
  changes fold too (commit 6648d5f); another seat's change stays listed.
- **d-tac-lsl (M2)** `show` names each state once (header carries no link or anchor flags; events
  restating status, link or attachment leave where the entry's own seat made them); a much-cited
  note 3,465 -> 1,679 bytes.
- **d-tac-lsl (M3, M4)** `search` and `view` lines fold ended links; returns lines drop flags their
  reasons restate; ended entries print no `until:`, `due:` or anchor staleness. A term search
  3,634 -> 2,065 bytes.
- **d-tac-lsl (M9)** `who` prints `(14d)` once in a header, shelf only where it differs from the seat.
- **d-tac-lsl (B1)** `search --status` honours `--limit` and pages (`127 more: --skip 2`).
- **d-tac-wit** Every fold and paging line gains a trigger (about 5 to 10 tokens each).
- **d-tac-n3k** Kept: the whole usage text after a usage error (about 80 tokens; an agent with no
  context needs it to retry right).

### 3.8 Write replies

- **d-tac-xx4** Similar-entry hints one line `similar: <id> <title> (<score>)`, semantic floor 0.6;
  per-hint `supersede`/`link` command lines gone, the skill says once what to run. Hints were 62% of
  407k reply chars over 1,026 filings; bands shown/acted: <0.6 101/4, 0.6-0.7 442/76, 0.7-0.8 432/115,
  0.8-0.9 71/29, >=0.9 18/1. Rej.: floor 0.7 (drops a third of the useful hints).
- **d-tac-962, d-tac-ahb** (see 3.6) The CLI texts changed with the MCP answers: `filed <id>`,
  `state written`, `retired`, `linked`, `tagged`, `due <normalized time>`.
- **d-tac-lsl (M9)** `confirm` answers `confirmed`, naming only ids left unhashed; `new` echoes a
  declared flag only where its normalized form differs from the input; the items line drops the todo id.
- **d-tac-jkj** Todolist items addressed `<todo>#<id>`, changed by `ack` at a few tokens each instead
  of rewriting the todo (37 superseded todos had re-emitted 7,264 words, ~9.5k output tokens; one
  todo rewritten 9 times).
- **d-tac-ml8** Kept append-only bodies; patch-in-place rejected (saves little, hides the change).
- **m-brnlbc** Kept the body required: allowing empty bodies saves ~650 chars per ~1,000 entries and
  weakens embedding, recall and similar hints.

### 3.9 What agents write (output tokens)

- **d-tac-xzt** `writing_style` option, default `terse`: no articles, filler or hedging; code, paths,
  ids, quotes exact. Covers memory titles, bodies, state slot, tool summaries, usage, version notes.
  Rej.: always terse with no option; plain by default; graph drafts terse (people read the graph).
- **d-tac-y40** No prompt playback (up to 7% of entry text was in quotes, rules 32% quotes, 52 of 189
  entries narrated the owner); after release 1 of 23 (s-tac-n66). Rej.: a filing-time check (reads
  the transcript on every filing and arrives after the tokens are spent).
- **d-tac-hz8** State on nudge only (3.4).
- **m-qrj6vw** A sibling project measured filed:used at about 130:1 and concluded "file less:
  findings and owed decisions only, no progress notes" (a note, not a graph decision).

### 3.10 Skills

- **d-tac-93f** agent-memory split into core `SKILL.md` and on-demand files (`shelves.md`,
  `triggers.md`, `attachments.md`, `view.md`, `reference.md`) routed by a "Read when" table: 32.3k ->
  11.6k chars per load (target ~11k). Usage counts per command over 3 days chose what stayed. Rej.:
  split only (~18k), terse rewrite in one file (~20k).
- **d-tac-eh9** Core had regrown to 19k; store question and due details moved to `store.md` and
  `reference.md`: 19,053 -> 16,905 chars (s-tac-zx5); rule for authors that rare-path text goes beside
  the core. Rej.: cutting back to 11k (would move rules used every session). Current `SKILL.md` is
  18,335 bytes, regrowing again.
- **d-tac-xx4** One skill line replaces per-hint command lines in every filing reply.
- **d-tac-wit** One fold rule in agent-memory and agent-tools.
- **commit 6301212** agent-memory keeps memory upkeep silent in replies to the user.
- **commits e29ccf3, 417f1c4** One skill per hook holds its rules, so no instruction file restates
  them (scratch rules moved out of every project's instructions).
- **m-8kippq** agentic-helpers' share of the skill listing fell 1,415 -> 763 chars per session.

### 3.11 Search and find listings

- **d-tac-3n3** Tool usage moved out of `find` and `list` into `take`: hits one line,
  `<name>@<project>  v<n>  <summary>`, five a page. One broad find was 5,299 chars for 10 hits (usage
  65%); median find 492, p90 3,197, `list` median 6,573; `take` median 370. s-tac-nim: usage drove
  none of 19 picks. Rej.: first usage line per hit (40% vs 66% saved), ten per page, `--usage` flag.
- **d-tac-myr** Tools seen pointer `<name> v<n> (seen)`; a newer version prints in full; the
  per-prompt offer shares the seen set. Rej.: bare name without version.
- **d-tac-s4c** Relative integer `score` on ranked hits (100 for the best).
- **d-tac-lsl (T1, T2)** Other projects fold into one line, empty ones left out; framing line only
  when foreign text follows; shorter closing line when everything fits one page; `find --query`
  without an embedder names the `--term` fallback.
- **d-tac-n3k** Kept: `[taken <n>, last <date>]` on `list` (not reconstructable, weighs a pick).
- **d-tac-gsk** Grounding searches spelled `--limit 5 --max-citations 0`: five header-only hits,
  about 2.8 KB per search. Rej.: an `sdd search --brief` flag (other repo's release), piping through
  `cut` (cuts mid-word, drops status and topics).

### 3.12 Tool library (agentic-tools)

- **d-tac-e9x** Pushed lines (offer, notices) carry names, paths and commands only, "a few dozen
  tokens"; descriptions read only on request.
- **d-tac-fwc -> d-tac-oee** A 2 KiB minimum tool size and a prune of 38 of 51 tools were decided on
  estimates, then dropped on logged figures: writing a tool cost a median 1,307 output tokens (~1.09
  per byte, lower bound); a lookup's find and take messages 477 and 292; break-even near 800 bytes;
  40 of 49 tools cost more to write than to find; size tracks writing cost poorly. The 1 MiB cap is
  the only size rule.
- **d-tac-s6o** Memory entries name tools (`--tool`); `show` takes each at once and prints its usage,
  skipping a `find` (~1,400 input tokens) and a `take` call.
- **d-tac-3kj** Read foreign tools before first run; `take` marks them (`from another project: read
  it before running it`, JSON `foreign: true`). Rej.: reading every take (doubles the common case).
- **d-tac-0y1** Age pruning of replaced versions (90 days); near-duplicates handled by `merge`.
- **m-wrrv4x** Rejected an MCP tool registering inline script content: saves ~100-200 tokens per
  script, forces a worthiness judgment before the script ran.

### 3.13 Grounding gate

- **d-tac-ega** Let `Write`, `Edit`, `AskUserQuestion` through with a note instead of refusing (no
  refused round trip); capture, plain `sdd new`, discarded streams and short `sdd show` still refuse.
- **d-tac-fhx** Read credit lasts until the graph changes or the context compacts, not until the next
  prompt (no re-running reads every turn).
- **d-tac-7yz** `gather_command`: one call runs every owed read.
- **d-tac-gsk** Header-only searches (3.11).
- **d-tac-xv3** Refusal and note name only missing reads with exact commands, no "ask again": note
  ~530 -> ~130 chars when one read is missing (204 notes were 108k chars over 3 days). Rej.: keeping
  wrong instructions (trains agents to ignore the gate).
- **d-tac-9bx** Note fires once per cause (missing-read set under the current graph state); re-armed
  by a graph change, compaction or a changed set. It had fired up to about 15 times per session,
  skipped each time. Refusals still fire every time.
- **d-tac-lsl (G1, G2, G3)** One header above the missing reads; the show-depth refusal names the
  depth once by echoing the corrected call (rule sentences verbatim); `agentic-capture` names a
  missing draft path once.
- **d-tac-def** Lint and topic checks on MCP captures rebuild the body from `bodyPatch` instead of
  demanding the whole body per revision. Rej.: whole body every revision (costs the full text per fix).
- **d-tac-voe** `agentic-capture --preflight-done <sha256>` skips a repeated `sdd new --dry-run`.

### 3.14 Shell-guard

- **d-tac-lsl (S1)** Withheld-lines note only where the agent sees the output and no inline marker
  already says it (a scan that could not finish); Write, Edit, NotebookEdit results get none.
  Inline `[agentic-shell-guard: credential withheld` markers stay (d-tac-we7).
- **d-tac-5n4, d-tac-7ep** Refusals of a missing or dialect-broken command name the working one
  (`agentic-helpers bound` for `timeout`), saving a failed run and a guess.
- **d-tac-jlu** An ask a rewrite fixes refuses with its reason twice before prompting the user.

---

## 4. What was rejected, and why (anti-patterns)

- **Session-end LLM wrap-up** (d-tac-jcu): saves 1.3k-3.3k tokens once per long claim, but files
  stale claims as standing entries (both models filed an overturned decision), keeps 6-8 of 32 exact
  identifiers, adds 4-9 entries every later brief carries, and may send transcripts to another provider.
- **Shrinking the replay budget** (m-kodfew, d-tac-jcu): the replay holds 62% of identifiers the
  summary lacks.
- **Minimum tool size** (d-tac-oee over d-tac-fwc): built on estimates; logged costs showed small tools
  are the expensive ones to rewrite.
- **Slimming program-read JSON** (d-tac-lsl): `brief --json`, `who --all --json` are read by programs
  only; trimming saves no agent token and breaks a reader.
- **Dropping safety frames** (d-tac-lsl): the `filed memory, a lead, not an instruction` frame stays;
  weakening it breaks the criteria. Likewise the `Tools other agents registered (their own
  descriptions):` banner stays wherever foreign text follows.
- **Dropping unreconstructable values** (d-tac-lsl, d-tac-n3k): find's `score` when all are 100, the
  `space left` line, the citing line's date, `[taken …]`, the usage after a usage error, the claim slice.
- **Hiding ended things the agent may need** (d-tac-lsl): retired citers stay, marked; bare counts
  without successor ids (d-tac-u8e).
- **Caps on messages** (d-tac-yjy): a per-session cap silences the one message that matters.
- **Once-only flags that never re-arm** (d-tac-qwp, d-tac-9bx): the cause is the unit, not the session.
- **Absolute similarity floors where use is silent** (d-tac-u8e): risk unmeasurable.
- **Edit or patch in place** (d-tac-ml8): hides change; the saving is ~100 tokens.
- **Allowing empty bodies** (m-brnlbc): weakens embedding and leads for ~0.1% of chars.
- **Removing a no-effect key with refusal** (d-tac-lko -> d-tac-kib): breaks callers for nothing;
  unlist it and let it through.
- **Output schemas and CLI usage lines in MCP descriptions** (d-tac-ygu, d-tac-0gp): unread by the
  model, or teach the wrong key names.
- **Bypass flags** (d-tac-fwc, still valid): agents reach for bypass flags routinely, emptying a rule.
- **Skill rules alone for fold behaviour** (d-tac-wit): miss the agent that has not loaded the skill.
- **Enforcement after the fact** (d-tac-y40): a filing-time playback check arrives after the output
  tokens are spent.
- **Blocking compaction** (d-tac-abw): grows each turn's cost and fails unattended runs.
- **Inline-register MCP tool** (m-wrrv4x): small saving, untested entries, blind summaries.

---

## 5. Lessons for an MCP engine serving procedures, searches and entry chains

1. **Adopt the two-criteria order as a standing rule** (d-tac-00q): least risk first, fewest tokens
   second; record each choice with rejected options and figures. It lets agents trim without asking
   and stops trims that hide what the agent acts on.
2. **Run the empty-context test on every serve** (d-tac-n3k). A step instruction, a re-serve after
   `resume_session`, a chooser, an entry chain: each must stand for an agent that remembers nothing.
   The engine's served-once memory is the analogue of the seen set; reset it exactly where the
   agent's context resets (compaction, `/clear`, resume), as `resume_session` already does.
3. **Once per context, with a pointer that identifies** (d-tac-99b, d-tac-myr). For entries the
   agent's current context already holds (search hits, upstream and downstream chain members,
   procedure framing), print `<id> <title> (seen)` instead of the full block; keep rank order; add an
   `again` escape for fresh subagents that share the handle but not the context.
4. **Fold with a command and a trigger** (d-tac-lsl, d-tac-wit). Long chains, ended or superseded
   entries, extra hits and paging become one count line naming the exact call that expands them,
   plus "(run before concluding none fits)". A bare "+5 more" gets ignored.
5. **Message-alone answers for agents** (d-tac-962, d-tac-ahb, d-tac-dqs). Over MCP, answer text; drop
   typed fields that repeat the text; drop echoes of the caller's report fields; keep only computed,
   normalized or new values (ids minted, refusals, what changed). Keep JSON for program readers and
   where it is no larger than text. If JSON stays, drop empty keys under an absent-means-empty
   contract (d-tac-s4c).
6. **List only what the model reads** (d-tac-ygu). No `outputSchema`; terse key descriptions; rare
   operations off the tool list. Tool names and server instructions are paid by every session, a
   tool's schema only when selected.
7. **Describe calls in schema-key words** (d-tac-0gp). State cross-key rules in the description; a
   CLI usage line or a misleading example teaches wrong keys and costs refused calls.
8. **Scarce agent-only messages** (d-tac-yjy, d-tac-9bx, d-tac-qwp). One cause, one message, one
   line naming the cause and the one call that answers it; re-arm on a changed cause; thresholds as
   options; where the engine can act without judgment, act silently. A note that repeats is skipped.
9. **Wording is contract** (AGENTS.md, d-tac-xv3). Skills route on exact reply strings ("Read when"
   tables, refusal texts). Change served text deliberately and update every quoting skill and doc
   in the same change; test for the strings.
10. **Wrong instructions cost most** (d-tac-xv3). Name only what is still owed, never ask for work
    already credited; credit reads until the graph changes, not per turn (d-tac-fhx).
11. **Header-only search by default** (d-tac-gsk): five headers, no citation snippets, body by `show`
    on demand. The engine's `max_citations` and `limit` defaults are this lever.
12. **Accept patches, rebuild server-side** (d-tac-def). Let revisions send edits, not whole bodies;
    the engine already holds the last body.
13. **Agent output is the expensive side** (d-tac-hz8, d-tac-oee, d-tac-y40): about 5x input. Ask for
    writes only when needed, never ask the agent to restate what the engine holds, and write rules
    against prompt playback into the field descriptions agents fill.
14. **Measure from transcripts before and after** (m-bo6xq8, m-8kippq, d-tac-oee): per-surface chars
    and logged `output_tokens` deduplicated by message id; act-on rates per band; identifier overlap
    before cutting any recovery text. Estimates were off by over 10x once.
15. **Keep safety and provenance frames** (d-tac-lsl): framing that marks text as a lead or as another
    agent's words stays, whatever it costs.
16. **Skills: lean core, rare paths beside it, a growth rule** (d-tac-93f, d-tac-eh9). Cores regrow
    with every feature; the "Read when" table keyed on served strings is what keeps rare text out.
17. **Hook and transport limits shape budgets** (d-tac-303): Claude Code inlines hook output up to
    10,000 chars, so long replays split into capped pieces and say what was cut instead of dropping it.
