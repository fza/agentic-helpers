# Session-end wrap-up: measurements

Taken over every Claude Code transcript on the owner's machine on 2026-10-29.

## What a claim replays today

42 claims carried the previous session's last turns (`last turns of the previous session (...)`,
bounded by `claimBudget`, 20000 characters for brief and slice together).

| | characters |
|---|---|
| mean | 4170 |
| median | 2247 |
| p90 | 13869 |
| max | 18203 |

6 distinct slices exceeded 5000 characters.

## What the post-compaction replay carries that the compaction does not

81 compactions followed by an agentic-memory replay (`session-log <n>`), 109 transcripts.

| | characters |
|---|---|
| replay mean | 10770 |
| replay max | 43531 (never cut; 5 x 9000 budget) |
| Claude Code summary mean | 17379 |
| messages kept verbatim mean | 4132 |

Of 3745 distinct exact identifiers in the replays (graph ids, memory ids, hashes, paths, backtick
spans), 36% appear in the summary, 12% in the kept messages, 38% in either.

## Wrap-up spike

Each of the 6 slices over 5000 characters ran through `claude -p --safe-mode --tools ''
--no-session-persistence --output-format json`, asked for a state plus memory entries (kind, title,
topics, body of at most 120 words), only from the turns, identifiers verbatim.

| setting | cost per run | entries | identifiers kept |
|---|---|---|---|
| haiku, effort low | $0.0015 to $0.0020 | 4 to 9 | 19% to 100% (6 of 32 on the longest slice) |
| sonnet, effort low | $0.022 to $0.040 | 3 to 6 | similar (8 of 32 on the longest slice) |

- No identifier was invented. Flagged ones were path tails of paths in the turns, except one commit
  hash sonnet took from the runner's own git status: its working directory sat inside the
  repository. A runner has to start outside any repository, as sdd's does.
- Entries read accurate against the turns.
- Both models filed a decision the turns reported as standing ("json key removal postponed to
  v3.0.0, do not do it before then") as a lasting entry. The graph has overturned it since
  (20261029-083737-d-tac-kib).
- Saving: 1.3k to 3.3k main-model tokens per claim over 5000 characters, once per claim.
