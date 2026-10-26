---
name: agent-capture
description: 'Which way an sdd graph entry is captured: through sdd''s engine (mcp__sdd__start_procedure, canonical capture) wherever mcp__sdd__* tools are present, through agentic-capture from a draft file only where they are not. Covers the draft''s shape, the project''s lint, the pre-flight, what each refusal means. Use before any capture, and whenever agentic-capture or the grounding gate refuses one. Only projects carrying a .sdd/ graph use this.'
license: MIT
---

# Capture

## Which way

| Session has | Capture through |
|---|---|
| `mcp__sdd__*` tools (sdd's engine) | `mcp__sdd__start_procedure` with `canonical: capture`, driven by the `sdd` skill. Never `agentic-capture` |
| no sdd MCP server, or one that refuses the graph | `agentic-capture <draft.md>`, below |

- **The engine plays the entry back** for the owner's confirmation, runs a writing-guide review and
  checks the generated summary. `agentic-capture` does none of these.
- **The gate holds it.** `agentic-capture` in a project whose `.mcp.json` declares `mcpServers.sdd`
  → refused, `GROUNDING GATE: refused. This project serves sdd's engine ...`. Switch to the engine.
  `cli_capture: true` in `.sdd/grounding.yaml` lifts it: the owner's setting, never yours.
- **Never a credential in an entry.** Either path, a draft, an attached file or an engine report
  holding what looks like one → refused, `GROUNDING GATE: refused. The capture holds what looks like
  a credential (<rule ids>, <where>, line <n>); the graph is shared and immutable`. Name the variable
  or file instead, then capture again. `agentic-capture` scans what it hands sdd once more right
  before each `sdd new` and refuses the same way, exit 1, opening `agentic-capture: refused. The
  capture holds what looks like a credential (`.
- **The draft and every attached file exist before the capture's own call.** Written in the same
  Bash call, or gone → `... The capture names <file>, which cannot be read to scan for a
  credential` (the gate adds `; it must exist before the Bash call that captures it`). An
  `attach: ['-:<name>']` → `... The capture attaches standard input as <name>, which cannot be
  scanned for a credential; attach a file instead`. Write the file first, attach it by path.
  A path with `$` or `~`, or a relative one with a `cd` or a launcher (`env`, `sudo`, `xargs`,
  ...) in the command → `... which cannot be read to scan for a credential: <why>; attach it by a
  literal absolute path`. Run the capture as its own plain call.
- **`sdd-engine` is deprecated.** The `sdd` skill is the engine flow.

## agentic-capture

`sdd new "$(cat body.md)"` → second shell re-reads the body → backticks run as commands, every code
identifier gone from an immutable entry. `agentic-capture` passes the body as one argument.
Outside the engine, always it, never `sdd new` by hand.

**Only a project carrying `.sdd/`.** No graph → refused, nothing created.

```bash
agentic-capture <draft.md>                   # lint, pre-flight, capture
agentic-capture <draft.md> --force           # capture despite a high finding. Owner's call only
agentic-capture <draft.md> --skip-preflight  # entry marked unchecked. Only where the tool advises it
agentic-capture <draft.md> --preflight-limit 8m   # cut off each pre-flight try after 8m. Default 5m
CAPTURE_PREFLIGHT_LIMIT=8m agentic-capture <draft.md>  # same, for a caller. The flag wins
agentic-capture <draft.md> --preflight-route glm-5.3=<layer.yaml>  # fallback for the pre-flight. Repeatable
agentic-capture <draft.md> --preflight-done <sha256>  # caller ran the pre-flight on these bytes already
agentic-capture --help                       # usage + one line per flag, stdout, exit 0
```

- **`--preflight-done <sha256>`** = sha256 of the draft file's bytes as read from disk, front matter
  included. Match → header + lint still run, pre-flight skipped, stderr
  `pre-flight skipped: the caller ran it on these exact bytes`, entry recorded verified. Never
  unchecked. Mismatch → `--preflight-done names <given>, but the draft hashes to <actual>; running
  the pre-flight`, then pre-flight as usual. Beside `--skip-preflight` → usage, exit 1.

## Draft

```markdown
---
type: d
layer: tac
kind: directive
intent: pending
confidence: high
participants: [Felix]
topics: [area-hooks]
refs:
  - {id: 20260925-144721-d-tac-a9x, kind: refines, desc: why it points there}
---

Body. Markdown, unfenced.
```

- **Header = `sdd new` flags.** `type` + `layer` required. `kind`, `confidence`, `intent`,
  `canonical`, `actor`, `actor_kind`, `class`, `summary`, `title` → one value. `topics`, `closes`,
  `supersedes`, `participants`, `aliases`, `actors` → list. `refs`, `involvement`, `topic` → one
  object per item. `when`, `index` → one object. `attach` → list of paths. Every value check →
  `sdd new` itself.
- **Any other header key → refused, exit 1**, naming it and listing the accepted ones. Nothing runs.
  Never a silent drop: an entry written without the field stays without it.
- **Draft lives where the project keeps drafts.** `.tmp/drafts/` where `.tmp/` exists.
- **Never fence the body.** A lint reads a fenced body as code and reports nothing.

## What runs, in order

| Step | Blocks when |
|---|---|
| grounding gate | the project serves sdd's engine and sets no `cli_capture: true`; or the reads are owed and the draft is not grounded (`agentic-helpers:agent-grounding`) |
| header | no `type`/`layer`; `listing_prefix` set and no topic carries it |
| sdd | no `sdd` → `agentic-capture needs sdd on the PATH`, exit 1. Draft or call asks for a fork-only surface the installed sdd lacks → refused naming `brew install fza/tap/sdd`, exit 1, nothing runs. Drop the key or have the owner install the fork |
| project lint | `draft_lint` set and it reports; or it fails and says nothing |
| pre-flight | `sdd new --dry-run` refuses; any `[high]` finding. Provider fault → retried, see below. Skipped on a `--preflight-done` match |
| capture | `sdd new --preflight-verified`, under `.sdd/tmp/capture.lock`. Runs once, never retried |

- **Stock sdd (no `--preflight-verified`)** → no dry run: one `sdd new` under the lock checks and
  writes. `[high]` → exit 2, nothing written. Provider fault → retried. Cut off by the limit → exit 1,
  never retried: `git log` first. `--force` past a `[high]` → written with `--skip-preflight`,
  recorded unchecked.
- **Fork-only:** draft keys `title`, `actor_kind`; `--preflight-route`; `--preflight-done`; a graph on
  schema 2. Probed from `sdd --help`, never from the version.

- **Split layout** (`project_dir` in the code repo's `.sdd/config.yaml`) → `grounding.yaml` and the
  lock are the graph repo's. sdd runs from the code repo.

- **Project config → `.sdd/grounding.yaml`.** `listing_prefix: area-` → topic rule.
  `draft_lint: <command>` → run from the repo root on `.<draft>.body.md` beside the draft, findings
  shifted to the draft's lines. No key → no rule, no lint.
- **`[medium]` → shown, captures anyway.** Read each. Fold what holds, say why for what does not.
- **`[low]` → never shown.**
- **Exit 2 = pre-flight held it.** Fix the draft, run again. `--force` → owner decides, never you.
- **Exit 1 = anything else.** The message names it.

## Provider fault

Pre-flight is a model call. Provider failing → not the draft's fault → retried, not refused.

| Fault | sdd output carries |
|---|---|
| answered nothing | `no content or tool calls in response` |
| rate-limited | `RateLimitError`, status 429 |
| failed with status | `API error: status code` 408, 409, 5xx |
| timed out | `timed out` (sdd's own `llm.timeout`) |
| no answer within the limit | try ran past `--preflight-limit`; sdd and its children killed |

- **Waits 10s, then 30s, then gives up.** Three tries. One stderr line per retry:
  `the pre-flight's provider answered nothing; trying again in 10s`.
- **Gave up → exit 1:** `... on all 3 tries; the draft did not cause it, so capture it again later`,
  then sdd's output of the last try, where it printed any. Do not edit the draft for it. Run again later.
- **Fallback routes** (`--preflight-route <model>=<layer>`, in order given): primary gave up → each
  route one try, no wait. sdd gets `--model <model> --local-config <layer>`. First answering decides,
  findings as the primary's. Capture step stays on the caller's own config.
  - Routes named → stdout opens `AGENTIC_CAPTURE_ROUTE <model>` (or `primary`).
  - Every route failed → `route <name>: <fault>: <last line>` per route, then `... failed on every
    route; the draft did not cause it, so capture it again later`. Exit 1.
  - Layer missing, unreadable, not yours, or group/other-readable → `route <model> skipped: <layer>
    <why>`. Never read, never printed: holds an API key.
- **Never retried:** a `[high]` finding, `validation failed`, status 400/401/403/404, anything else.
  Refusal checked first: a `[high]` finding quoting "timed out" is still a refusal.
- **Graph on an older schema** → stderr ``the graph needs `sdd migrate` before sdd writes to it; the
  draft did not cause this``, then sdd's reason, exit 1. Stock sdd has no `migrate` → ``sdd refuses to
  write this graph; the draft did not cause this``. Pre-flight or capture step; never retried,
  never routed. Fix = `sdd migrate`: owner's call, rewrites the whole graph, asks y/N. Never edit the
  draft for it.
- **Capture step never retried.** Its one model call (summary) fails soft, before the write. A
  failure there = file or git, entry may be on disk. Check `git log` before running again.

## After

- **Read the generated summary.** Drift (wrong actor, wrong ref attribution, lost identifier) →
  `sdd summarize <id> --text '<faithful summary>'`.
- **The draft stays.** Delete it once the entry reads right.

## Off on the machine

**Off on the machine** → `agentic-capture: off on this machine (plugin option capture_enabled)`, or `agentic-capture: off while grounding gate is off (plugin option grounding_gate_enabled)`. Exit 1, nothing runs. Tell the user; never capture with `sdd new` instead.
