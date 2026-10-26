# Credential scanner: usage

Text agentic-helpers keeps is shared: a tool script reaches every session and project on the
machine, and sandboxed jobs read the stores. A token pasted into one, from a log, a verbose `curl`
or an environment dump, spreads to every reader and cannot be taken back. Every piece writing such
text scans it first with one shared scanner, `source/hooks/internal/secrets`, and refuses rather
than keeps what looks like a credential. The same scan keeps a prompt holding one from the
embedding endpoint, which may be remote.

## Where it runs

| Piece | What is scanned | On a finding |
|---|---|---|
| `agentic-tools add`, `update`, `merge` (CLI and MCP) | every file of the version written (by its name and as plain text), a fork's first version included; `--summary`, `--usage`, `--inputs`, `--outputs`, `--distinct`, `--why` | refused, nothing embedded or written ([tools](../tools/usage.md#credentials)) |
| the grounding gate, on every graph write | a Bash `sdd new` or `agentic-capture` command's text, every file a plain `sdd new` attaches, the `agentic-capture` draft and every file its header's `attach` names (scanned by its name in the entry, and as nameless text where that finds nothing; only a literal path, refusing `$`, `~` and a relative one after a `cd` or behind a launcher), the `report` of `mcp__sdd__next` and the `params` of `mcp__sdd__start_procedure` (every key and string, one per line; never the session `label`), a file staged through `mcp__sdd__stage_attachment` | refused, whichever sdd is installed, reads, grounded draft or exempt seat notwithstanding; what it cannot scan (standard input, a file not on disk) is refused too ([grounding gate](../grounding-gate/usage.md)) |
| `agentic-capture`, right before each `sdd new` | exactly what it hands sdd: the body, every header value as its flag carries it, every attached file | refused, exit 1; the gate's scan reads the files earlier, so this one catches a file changed since ([capture](../capture/architecture.md#credentials)) |
| shell-guard, on every Bash call | the command's whole text | `ask`, `deny` or nothing as `shell_guard_secrets` says ([shell-guard](../shell-guard/usage.md)) |
| agentic-memory `new`, `state`, `retitle`, `retire --because`, `supersede --because`, `ack --decline` (CLI and MCP) | the title, body, state or reason, as plain text | refused, nothing embedded or written ([memory](../memory/usage.md#writes-holding-a-credential)) |
| agentic-memory's migration to format 4 | every entry still holding its body, once | listed in the filing seat's next brief as `possible credential: <id> <title> (<rule ids>); supersede it --because 'credential'` |
| agentic-memory's and agentic-tools' prompt hooks | each user prompt, before it is embedded for recall | the prompt is not embedded; recall answers by terms; agentic-memory says so once ([memory](../memory/usage.md#prompt-recall-and-credentials)) |

## What a refusal says

A refusal names where the credential lies, every rule found on the first line holding one (sorted,
comma-separated) and that line. It never prints the matched text, and nothing is redacted: a
redacted copy beside a missed variant of the same token is worse than a refusal. Each piece's
wording is part of its contract, quoted by its skill:

    agentic-tools: <file or flag> looks like a credential (<rule ids>, line <n>); the library never holds one
    agentic-memory: this prompt looks like it holds a credential (<rule ids>); it was not sent to the embedding endpoint
    GROUNDING GATE: refused. The capture holds what looks like a credential (<rule ids>, <where>, line <n>); the graph is shared and immutable
    agentic-capture: refused. The capture holds what looks like a credential (<rule ids>, <where>, line <n>); the graph is shared and immutable
    agentic-shell-guard: the command holds what looks like a credential (<rule ids>, line <n>); history, logs and files keep it; reference it instead: "$NAME" or a file
    agentic-memory: <where> looks like a credential (<rule ids>, line <n>); memory never holds one

The way out is to reference the credential instead of holding it: an environment variable or a file
read at run time. A false positive, a credential-shaped example, is rewritten until it no longer
reads as one; nothing overrides a refusal, gitleaks' inline `gitleaks:allow` marker included.

A rule id `scan-incomplete` means the scan could not finish (the context ended, or a vendored rule
failed to load), and the text was refused rather than passed unscanned.
