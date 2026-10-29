# Tool creation and lookup cost, measured

Source: Claude Code session transcripts on the maintainer's machine: `~/.claude/projects/` and fdbox's archived autopilot transcripts. Every tool's history journal names the session that created it; 49 of 51 stored tools were attributed. Tokens are the `output_tokens` Claude Code logs per API message (thinking included), each message counted once by `message.id`.

## Method

- Span: from the first tool call writing the tool's `--from` path (Write, Edit, MultiEdit, or a Bash redirect, `tee`, `cp`, `mv`, `install` into it) to the successful `add`.
- Each assistant message in the span is classed: **write** (a tool call writing the path), **use** (any other tool call naming the path: test runs and real use before registering), **add** (the registering call). Messages naming nothing are left out.
- Write is a lower bound on authoring: thinking in earlier messages (planning, reading code) is not counted. Use mixes test runs (authoring) with real use; the two cannot be told apart from the transcript.
- Lookup: the character size of each successful `find`, `list`, `take` reply, and the output tokens of each assistant message issuing exactly one of those verbs.
- Pricing assumption: output tokens cost 5x input tokens; 3.5 bytes per token.

## Per tool (output tokens; messages in parentheses)

| v1 bytes | tool | write | use | add |
|---|---|---|---|---|
| 315 | splice-between-markers | 3062 (1) | 2987 (4) | 1233 (1) |
| 362 | replace-exact | 2167 (2) | 20173 (25) | 1233 (1) |
| 445 | unique-replace-json | 513 (1) | 2312 (5) | 233 (1) |
| 458 | section-word-count | 645 (1) | 0 (0) | 1267 (1) |
| 481 | exact-replace | 1251 (1) | 782 (1) | 1267 (1) |
| 481 | exact-replace-spec | 2054 (1) | 6557 (3) | 325 (1) |
| 567 | docs-index-dead-links | 617 (1) | 248 (1) | 300 (1) |
| 586 | docs-folder-page-coverage | 1344 (2) | 0 (0) | 266 (1) |
| 589 | folder-index-check | 637 (1) | 0 (0) | 1639 (1) |
| 590 | doc-diff-citations-masked | 780 (1) | 662 (1) | 286 (1) |
| 601 | fdgate-chart-value-homes | 832 (1) | 0 (0) | 769 (1) |
| 608 | go-drop-unused-imports | 812 (1) | 3942 (5) | 284 (1) |
| 701 | index-citation-context | 1419 (2) | 1303 (4) | 789 (1) |
| 751 | spec-line-coverage | 748 (1) | 0 (0) | 1639 (1) |
| 780 | tag-pipeline-jobs | 1245 (2) | 0 (0) | 881 (1) |
| 794 | section-mark-out-of-link | 1268 (1) | 0 (0) | 1233 (1) |
| 822 | markdown-fence-links | 835 (1) | 324 (1) | 266 (1) |
| 850 | markdown-empty-headings | 1887 (1) | 740 (1) | 262 (1) |
| 854 | title-homes | 1669 (1) | 2697 (2) | 1233 (1) |
| 854 | worktree-word-diff-review | 895 (1) | 4498 (1) | 894 (1) |
| 857 | spec-file-edit | 713 (1) | 0 (0) | 421 (1) |
| 988 | internals-link-label-align | 1151 (1) | 764 (1) | 1251 (1) |
| 1021 | citation-relink | 1542 (1) | 754 (1) | 1233 (1) |
| 1123 | rpcwire-code-usage | 899 (1) | 0 (0) | 350 (1) |
| 1183 | doc-set-bounds | 841 (1) | 192 (1) | 650 (1) |
| 1235 | branch-segment-collision-search | 1514 (1) | 479 (1) | 424 (1) |
| 1240 | doc-move-spec-split | 1472 (1) | 0 (0) | 618 (1) |
| 1259 | doc-move-paragraphs | 1014 (1) | 1522 (3) | 650 (1) |
| 1349 | docs-path-existence | 784 (1) | 873 (1) | 313 (1) |
| 1363 | moved-sentences | 926 (1) | 272 (1) | 287 (1) |
| 1366 | quoted-phrase-check | 1237 (1) | 762 (1) | 1639 (1) |
| 1388 | md-rewrap-paragraph | 1203 (1) | 0 (0) | 271 (1) |
| 1395 | retarget-index-citation | 2386 (2) | 2589 (2) | 789 (1) |
| 1405 | doc-move-coverage | 2344 (2) | 0 (0) | 769 (1) |
| 1424 | release-after-ci | 1055 (1) | 0 (0) | 1048 (1) |
| 1487 | fenced-links-to-paths | 1367 (1) | 0 (0) | 1639 (1) |
| 1642 | docs-empty-headings | 3450 (1) | 0 (0) | 252 (1) |
| 1643 | bare-section-links | 1897 (1) | 1611 (1) | 538 (1) |
| 1647 | paragraph-split | 1432 (1) | 617 (1) | 1267 (1) |
| 1685 | index-section-home | 1149 (1) | 0 (0) | 290 (1) |
| 1700 | concept-citation-fit | 1362 (1) | 400 (1) | 571 (1) |
| 1755 | docs-structure-check | 1307 (1) | 0 (0) | 362 (1) |
| 1764 | citation-rewrite | 6338 (2) | 356 (1) | 1267 (1) |
| 1799 | ledger-section-rows | 1848 (1) | 0 (0) | 1639 (1) |
| 2002 | above-below-links | 1601 (1) | 0 (0) | 635 (1) |
| 2980 | guard-break | 1458 (1) | 0 (0) | 388 (1) |
| 5316 | doc-move-spec | 5581 (2) | 7746 (4) | 358 (1) |
| 6058 | doc-section-move | 3155 (1) | 0 (0) | 1267 (1) |
| 9781 | doc-move-recite | 5382 (1) | 3030 (2) | 509 (1) |

Medians over 49 tools: write 1307, use 324, add 650. Write tokens per v1 byte, median 1.09. Tools used before their add: 28 of 49.
Tools under 700 bytes: write between 513 and 3062 tokens.

## Lookup

| reply | count | median chars | p90 | max |
|---|---|---|---|---|
| find/all-ok | 44 | 492 | 3197 | 7044 |
| list/all-ok | 22 | 489 | 6996 | 7469 |
| take/all-ok | 51 | 370 | 1261 | 7324 |
| find/bash/pure/ok | 18 | 349 | 742 | 3399 |
| list/bash/pure/ok | 3 | 6573 | 7411 | 7411 |

| message issuing | count | median output tokens | p90 |
|---|---|---|---|
| find | 45 | 477 | 1120 |
| take | 56 | 291.5 | 1057 |
| list | 24 | 385.5 | 630 |

Take then read (next 40 tool calls after 51 successful takes): 26 read the taken file before running it, 14 ran it unread, 11 did neither.

## Break-even

- Lookup: (find 477 + take 292) output tokens x 5 = 3,845 input-equivalents, plus about 250 tokens of replies, plus the file read in 65% of takes (about 0.19 input tokens per byte): about 4,100 + 0.19 x bytes.
- Rewrite, write message only: 1.09 output tokens per byte x 5 = about 5.4 input-equivalents per byte.
- Equal at about 800 bytes. Per tool, write tokens x 5 exceed 4,100 for 40 of 49 tools.
