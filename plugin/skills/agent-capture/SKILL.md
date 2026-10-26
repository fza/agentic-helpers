---
name: agent-capture
description: 'Capturing an sdd graph entry from a draft file with agentic-capture: the draft''s shape, the project''s lint, the pre-flight, what each refusal means. Use before writing a draft, before any capture, and whenever agentic-capture refuses one. Only projects carrying a .sdd/ graph use this.'
license: MIT
---

# Capture

`sdd new "$(cat body.md)"` → second shell re-reads the body → backticks run as commands, every code
identifier gone from an immutable entry. `agentic-capture` passes the body as one argument. Always it,
never `sdd new` by hand.

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
| grounding gate | turn lacks the three reads and the draft is not grounded (`agentic-helpers:agent-grounding`) |
| header | no `type`/`layer`; `listing_prefix` set and no topic carries it |
| sdd fork | `sdd --version` carries no `fza` → `agentic-capture needs sdd built from github.com/fza/sdd`, exit 1, nothing runs. Fix = `brew install fza/tap/sdd`, owner's call |
| project lint | `draft_lint` set and it reports; or it fails and says nothing |
| pre-flight | `sdd new --dry-run` refuses; any `[high]` finding. Provider fault → retried, see below. Skipped on a `--preflight-done` match |
| capture | `sdd new --preflight-verified`, under `.sdd/tmp/capture.lock`. Runs once, never retried |

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
  draft did not cause this``, then sdd's reason, exit 1. Pre-flight or capture step; never retried,
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
