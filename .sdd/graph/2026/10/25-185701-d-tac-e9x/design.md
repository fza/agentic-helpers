# agentic-tools: design

agentic-tools is a library of small helper tools agents write, machine-wide across projects, with each project's own tools preferred. It replaces fdbox's `pilot tools`, whose branch redesign it ports. Every section names its surfaces, the rejected alternatives and the accepted costs.

## Surfaces

- Binary `agentic-tools` under `source/hooks/cmd/agentic-tools/`, decisions under `source/hooks/internal/tools/`, listed in `.goreleaser.yaml`.
- Shim `plugin/bin/agentic-tools`, as `plugin/bin/agentic-memory`.
- MCP server `tools` in `plugin/.mcp.json`: tools `find`, `list`, `take`, `add`, `update`, `merge`, `reindex`, served as `mcp__plugin_agentic-helpers_tools__<verb>`, each taking exactly the keys its schema lists (`additionalProperties: false`), as the memory server does (20261025-034140-d-tac-9os).
- Skill `agentic-helpers:agent-tools` under `plugin/skills/agent-tools/`.
- Docs `docs/tools/usage.md`, `docs/tools/architecture.md`, a line in `docs/index.md`.
- Hooks in `plugin/hooks/hooks.json`: `agentic-tools prompt` on `UserPromptSubmit`, `agentic-tools notice` on `PostToolUse`, `agentic-tools stop` on `Stop`, `agentic-tools end` on `SessionEnd`.
- Environment: `AGENTIC_TOOLS_ROLE`, `AGENTIC_TOOLS_SCRATCH`, `AGENTIC_TOOLS_SCOPE`.
- Plugin option `tools_duplicate_floor` (default `0.85`). Embedding settings are the ones agentic-memory reads (`embedding_provider`, `embedding_model`, `embedding_endpoint`, `embedding_api_key_env`, `embedding_dimensions`).

## Store

- **One store per project**, `~/.claude/agentic-tools/<project>/`, the project keyed as agentic-memory keys it (`.sdd/config.yaml` `repo_id`, else a hash of the main checkout), so worktrees share it. The git work tree rule and `memory_require_git` apply as for memory.
- **Per tool**, the layout of fdbox's branch: `<tool>/current` (the current version number), `<tool>/history.jsonl` (`{v, by, role, at, why}`, `by` the session id), `<tool>/takes.jsonl` (`{v, by, role, at}`), `<tool>/v<n>/tool.json` (`name`, `summary`, `usage`, `inputs`, `outputs`, `files`, and on a fork `forked_from: {project, tool, v}`) beside the tool's files. A tombstone is `<tool>/merged` naming the survivor.
- **`index.json`** per store: per tool its current version, name, summary, usage, inputs, outputs, vector, takers and role set, and the file content hashes of every version. Rewritten whole under the store lock after every write; at most 4 MiB. find, list, the duplicate guard and the offer read it and never walk tool folders. A missing, corrupt or schema-mismatched index is rebuilt for its store, reusing vectors by content hash and embedding only what lacks one; `agentic-tools reindex` forces the rebuild.
- **`.lock`** in the store root: `flock` exclusive for every write, with a 2 s timeout; a timeout refuses naming the holder's pid where the lock file records one.
- **Sessions**: `sessions/<session>.json` holds the session's holdings (tool, version, copy folder, offered state hashes) and the tools it was shown; `agentic-tools end` removes it.
- Rejected: one machine-wide store (project tools clutter every project; one project's writer reaches code another project runs, unasked); a machine shelf with a promote verb (a tool helps another project only after someone promotes it); a store inside the repository (a gitignore line per project, and a clean deletes it); one machine index (any project's writer corrupts every project's search); walking stores on every read (time grows with the library).
- Accepted: index and tool folders can disagree after a crash until the next write or rebuild.

## Names and reach

- Name rule `[a-z][a-z0-9]*(-[a-z0-9]+)*`, at most 40 characters; summary one line, at most 200 characters; usage required; inputs, outputs optional; no field holds control characters other than newline and tab, and summary and `--why` hold neither.
- **Reads span every project store on the machine**, writes touch only the caller's own. `AGENTIC_TOOLS_SCOPE=project` limits reads to the own store, the mode a sandboxed caller's launcher sets.
- **A bare name** resolves to the own project's tool, else to a unique foreign one; any other is `<name>@<label>`, the label the last segment of the project key (`format-d-fdbox`), with more leading segments only where two projects share the last one. A bare name naming two foreign tools and no own one is refused, naming both. find and list print foreign tools as `name@label`.
- Rejected: own tools hiding same-named foreign ones (a better foreign variant goes invisible); names unique machine-wide (an unrelated tool elsewhere takes a project's natural name).

## Verbs

Every refusal prints `refused: <why and what to run instead>` and exits 1. Every output listing tools opens with `Tools other agents registered (their own descriptions):`.

    agentic-tools add <name> --from <path> --summary '<one line>' --usage '<how to run it>' [--inputs '<...>'] [--outputs '<...>'] [--distinct '<how it differs>']
    agentic-tools update <name> [--from <path>] [--summary ...] [--usage ...] [--inputs ...] [--outputs ...] --why '<what changed>'
    agentic-tools find [--query '<need>'] [--term '<literal>']... [--limit <n>] [--page <n>] [--json]
    agentic-tools list [--json]
    agentic-tools take <name>...
    agentic-tools merge <survivor> <member>... --from <path> --why '<reason>'
    agentic-tools reindex

- **add** registers v1 from a file or a folder in the caller's own reach (the checkout outside `.git`, or `AGENTIC_TOOLS_SCRATCH`). No link anywhere on or in the source, no hard-linked file, no pipe, at most 1 MiB of files. A folder's files land at the top of `v1/`, never under the folder's own name, unless it is a taken copy, whose `tool.json` is left out. Refused: a taken name; a merged name; files byte-identical to any version of any tool in any project (`refused: <file> is <tool> v<n> byte for byte: \`agentic-tools take <tool>\` copies it`); a description whose vector is at or above `tools_duplicate_floor` to any current tool in any project, naming up to three with `agentic-tools update <name>` as the fix, unless `--distinct` is given, which the history line keeps. Prints `added <name>: \`agentic-tools take <name>\` copies it`.
- **update** writes `v<n+1>` and makes it current; fields not given carry over, files too without `--from`. Refused when nothing changes, and when the old usage names a file or a long option the new version no longer holds unless `--usage` rewrites it. On a foreign tool, update forks: own `v1` is the origin's current version with the history line `forked from <project> <tool> v<n>` and `forked_from` in its `tool.json`, then the update lands as `v2`: `forked <name> from <project> into this project; updated it, now current`. The caller's holding moves to the version written when `--from` lies in a taken copy.
- **find** needs `--query`, a `--term`, or both. A term is a case-insensitive literal over the name, the fields and the tool's file text; `--query` ranks by cosine over the index vectors with a floor of 0.6. Order: term hits, then score = cosine + take lift (`min(0.05 × log1p(takers), 0.1)`, takers being distinct sessions other than the registering one) + own lift (0.05 for the own project), then name. The floor applies before the lifts. `--limit` defaults to 10, `--page` to 1. Closing line: `<k>-<m> of <n> matching tools shown, of <t> in <p> projects; \`agentic-tools take <name> ...\` copies them`, and `--page <n+1> shows the next <c>` where more remain. No embedder answering: `--query` is refused naming `--term`.
- **find --json** prints one object: `{"tools": [{"name", "label", "own", "version", "summary", "usage", "inputs", "outputs", "score", "takers"}], "shown", "matching", "total", "projects", "page"}`, with `label` null for the own project and `score` null for a term-only hit. Fields are the stored, control-character-free text; the framing lead is the JSON's `"framing": "Tools other agents registered (their own descriptions)"` key.
- **list** prints the own project's tools, `<name>  <summary>  [taken <n>, last <time>]`, then one line per other project: `<label>: <n> tools, \`agentic-tools find\` searches them`. `--json` as find's shape without score.
- **take** copies the current version of each named tool into `.tmp/tools/<name>/v<n>/` of the checkout, or under `~/.claude/agentic-tools/<project>/copies/` where the checkout has no `.tmp/`; a copy folder already there is reused, never overwritten. All names are checked first; one unknown name refuses the whole call naming every unknown one; a merged name takes its survivor, saying so. Records a holding per tool and a take line per copy written. Prints `taken into <dir>` then per tool `<name>/v<n>: <summary>` and its usage, inputs and outputs indented, under the framing lead.
- **merge** writes the survivor's next version from `--from` with `--why`, removes each member's folder under the lock, appends their take lines to the survivor's, and leaves `<member>/merged` naming the survivor. Own store only. Prints `merged <k> into <survivor>, now current`.
- **reindex** rebuilds the own store's `index.json`.
- Rejected: CLI only (no typed schema for agents); best-effort `take` of known names (an agent may not notice it got part of what it asked); printing `tool.json` as JSON on take (quotes and braces cost tokens, nothing parses it); `take <name>@<version>` (older versions stay history; agents always get the current); a remove verb beside merge (two calls per member and takes not folded); no merge verb (near-duplicates already in a store cannot be folded).
- Accepted: a merge removes tools with no review inside agentic-tools (a caller such as fdbox's board may wrap it with one); tombstones stay forever.

## Who calls

- A caller is its session id, found through the owning `claude` process as agentic-memory finds it; none found, every verb refuses. Its role is `AGENTIC_TOOLS_ROLE` where set, else the agentic-memory seat the session holds, else none. The role is written to history and takes and printed by no verb; it orders the offer alone.
- Rejected: session alone with no role (fdbox's same-role ordering goes); a seat required for writes (seatless sessions and sandboxed jobs could not register).
- Accepted: the environment variable is forgeable, which reorders offers and nothing else.

## Trust

Every value in every store is untrusted: a sandboxed process can write the stores where its sandbox allows `~/.claude`, and a foreign store is another project's agents' text.

- Readers follow no link (`O_NOFOLLOW` on every component, walks by descriptor), wait on no pipe (`O_NONBLOCK`, regular files only), and cap every read: a tool 1 MiB, a version folder 1 MiB + 64 KiB, `index.json`, `history.jsonl` and `takes.jsonl` 4 MiB each, a session file 1 MiB, `current` 64 bytes, walks 6 folders deep and 2000 files. A malformed line is skipped and counted.
- Every printed field is stripped of control characters; every listing and `take` opens with the framing lead.
- Pushed lines (the offer, version notices, nudges) carry tool names, paths and commands only, never a summary, usage or changelog line.
- Rejected: framed changelogs pushed to every session (another agent's text in an owner's session unasked); trusting versions marked as written outside a sandbox (the mark lives in a store a sandboxed process can write).
- Accepted: pushed lines say less; every reader carries the hardening.

## Pushed lines

- **Offer.** `agentic-tools prompt` embeds the prompt (agentic-memory's prompt embedding where it already ran), scores the own project's current tools against it, and adds as `additionalContext` up to three tools at or above the 0.6 floor not yet shown this session, same-role tools first: `tool library: near this prompt: section-word-count, moved-sentences. \`agentic-tools take <name>\` copies one`. Nothing near, nothing is added. Each tool is shown once per session. The embed is bounded by agentic-memory's recall timeout; a timeout adds nothing.
- **Version notices.** `agentic-tools notice` after every tool call adds, for each holding older than current not yet notified, `tool library: newer versions of \`word-count\`, \`paragraph-split\`. \`agentic-tools take word-count paragraph-split\` copies them`. A fork's holders also hear of its origin's newer versions: `tool library: \`word-count\` in github.com/fza/other has v6; yours forked at v5`. The hook compares the session file and the stores' index sizes and times first and reads nothing more when they did not move.
- **Nudges.** `agentic-tools stop` at turn end records, without blocking, scripts (`.py .sh .go .awk`) in the session's scratch (`.tmp/claude/<session>/`, or `AGENTIC_TOOLS_SCRATCH`) whose content hash and file name no version of any tool in reach holds, and taken copies whose files changed since the take. The next `prompt` adds each once per session: `tool library: your scratch holds scripts the library lacks: check.py, split.sh. \`agentic-tools add <name> --from <path> --summary '...' --usage '...'\` registers one`, and `tool library: you changed your copy of \`word-count\` (<copy>): \`agentic-tools update word-count --from <copy> --why '...'\``. The same lines open the session's next agentic-tools call.
- Rejected: an offer at session start by topics (an interactive session has no topics; a start block costs every session whether or not it fits); no offer, the skill alone (agents skip a verb they must remember); a `Stop` hook blocking to ask (a forced model turn per session); nudging only inside the agent's own next call (a session that never calls again never registers).
- Accepted: an embed per prompt where agentic-memory does not run; a session ending right after its turn never hears its nudge; two sessions holding one shared copy both hear its change.

## Copies

- `.tmp/tools/<name>/v<n>/` is a kept folder: no sweep removes it, sessions share it, and holdings point into it.
- Rejected: `.tmp/claude/<session>/tools/` (a copy dies with its session); store-side copies only (long paths outside the checkout).
- Accepted: copies pile up; two sessions can edit one copy.

## fdbox's switch

fdbox removes `pilot tools`: its agents call `agentic-tools`; its board sets `AGENTIC_TOOLS_ROLE`, `AGENTIC_TOOLS_SCRATCH` and, for jobs, `AGENTIC_TOOLS_SCOPE=project`, routes a job's writes through its own launcher, and builds its start-of-job offer from `find --json`. Its library moves by running its branch backfill (`card.md` to `tool.json`, role in history) under its library lock with its board paused, then renaming `.autopilot/lib/tools` to `~/.claude/agentic-tools/gitlab.com/format-d/devops/format-d-fdbox`; nothing is copied, and the first read builds `index.json`. The nesting in `section-word-count/v2` and the standing `citation-rewrite` are fdbox's to settle at the move. agentic-tools reads no `card.md`.

- Rejected: an import verb (code carried for one migration); a script in fdbox writing this store format (couples fdbox to the format, untested here); a fresh start (loses the tools and their take history); agentic-tools reading `card.md` (parsing carried for good).
- Accepted: the move waits for fdbox's switch, since today's board reads only `card.md`.

## Acceptance criteria

1. `add` from a file and from a folder writes `v1/` with the files at its top and `tool.json`, rewrites `index.json`, and prints the take line; each refusal named under add fires with its text, and the byte-identical and near-duplicate refusals name tools in other projects too; `--distinct` lets a near duplicate land and keeps its reason in history.
2. `update` carries fields and files over, refuses an unchanged update and a stale usage; on a foreign tool it forks as described, the origin untouched.
3. `find` orders term hits, then score with both lifts after the floor, then name; `--limit`, `--page` and the closing line behave as described; `--json` prints the stated shape; with no embedder `--query` is refused and `--term` works.
4. A bare name resolves own first, then a unique foreign one; `name@label` reaches a foreign tool; two foreign tools under one bare name refuse naming both; labels grow segments only on a clash.
5. `AGENTIC_TOOLS_SCOPE=project` keeps find, list, take and the duplicate guard to the own store.
6. `take` of several names copies all or none, reuses an existing copy, takes a merged name's survivor, and prints under the framing lead.
7. `merge` removes members, folds takes, leaves tombstones; `add` of a merged name is refused; find never shows a member.
8. A store lock held elsewhere makes a write refuse within 2 s; a link, a pipe or an oversized file anywhere a reader goes is refused or skipped, never followed or waited on.
9. The offer adds at most three own tools at or above the floor, once each per session, nothing when none is near; notices and nudges carry names, paths and commands only; the fork-origin notice fires on an origin's newer version; the `notice` hook reads no store when nothing moved.
10. `stop` records unregistered scripts and changed copies without blocking; the next `prompt` delivers each once.
11. A missing or corrupt `index.json` is rebuilt on read; `reindex` rebuilds it; neither re-embeds an unchanged description.
12. A store renamed into place from fdbox's backfilled library is read as is: list, find and take work, and `index.json` is built on the first read.
13. The MCP tools take exactly their schema's keys; `claude plugin validate .` passes; `docs/tools/` and `agentic-helpers:agent-tools` describe every verb, line and environment variable above.
