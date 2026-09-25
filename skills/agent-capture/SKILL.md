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
```

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
  `canonical`, `actor`, `class`, `summary` → one value. `topics`, `closes`, `supersedes`,
  `participants`, `aliases`, `actors` → list. `refs`, `involvement`, `topic` → one object per item.
  `when` → one object. `attach` → list of paths. Every value check → `sdd new` itself.
- **Draft lives where the project keeps drafts.** `.tmp/drafts/` where `.tmp/` exists.
- **Never fence the body.** A lint reads a fenced body as code and reports nothing.

## What runs, in order

| Step | Blocks when |
|---|---|
| grounding gate | turn lacks the three reads and the draft is not grounded (`agent-grounding`) |
| header | no `type`/`layer`; `listing_prefix` set and no topic carries it |
| project lint | `draft_lint` set and it reports; or it fails and says nothing |
| pre-flight | `sdd new --dry-run` refuses; any `[high]` finding |
| capture | `sdd new --preflight-verified`, under `.sdd/tmp/capture.lock` |

- **Project config → `.sdd/grounding.yaml`.** `listing_prefix: area-` → topic rule.
  `draft_lint: <command>` → run from the repo root on `.<draft>.body.md` beside the draft, findings
  shifted to the draft's lines. No key → no rule, no lint.
- **`[medium]` → shown, captures anyway.** Read each. Fold what holds, say why for what does not.
- **`[low]` → never shown.**
- **Exit 2 = pre-flight held it.** Fix the draft, run again. `--force` → owner decides, never you.
- **Exit 1 = anything else.** The message names it.

## After

- **Read the generated summary.** Drift (wrong actor, wrong ref attribution, lost identifier) →
  `sdd summarize <id> --text '<faithful summary>'`.
- **The draft stays.** Delete it once the entry reads right.
