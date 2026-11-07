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
| shell-guard, on every Bash call | the command's whole text | `ask`, `deny` or nothing as `shell_guard_secrets` says ([shell-guard](../shell-guard/usage.md)) |
| shell-guard, after every tool call | every string of the output, where it comes from outside the project and `shell_guard_secret_output` covers the tool | each line holding one is withheld from the agent, the rest shown ([shell-guard](../shell-guard/usage.md#credentials-in-tool-output)) |
| every agentic-memory write to its log (CLI and MCP) | every text the write carries: title, body, topics, anchors, declared conditions and triggers, state, link target, reason; every file it attaches (by its name, and as nameless text where that finds nothing) | refused, nothing embedded or written ([memory](../memory/usage.md#writes-holding-a-credential)) |
| agentic-memory's migration to format 4 | every entry still holding its body, once | listed in the filing seat's next brief as `possible credential: <id> <title> (<rule ids>); supersede it --because 'credential'` |
| agentic-memory's and agentic-tools' prompt hooks | each user prompt, before it is embedded for recall | the prompt is not embedded; recall answers by terms; agentic-memory says so once ([memory](../memory/usage.md#prompt-recall-and-credentials)) |

## What a refusal says

A refusal names where the credential lies, every rule found on the first line holding one (sorted,
comma-separated) and that line. It never prints the matched text, and nothing written is redacted:
a redacted copy beside a missed variant of the same token is worse than a refusal. Tool output is
the one exception, as a call that has run cannot be refused: shell-guard withholds each line
holding a credential, as `[agentic-shell-guard: credential withheld (<rule ids>)]`, and shows the
rest. Each piece's wording is part of its contract, quoted by its skill:

    agentic-tools: <file or flag> looks like a credential (<rule ids>, line <n>); the library never holds one
    agentic-memory: this prompt looks like it holds a credential (<rule ids>); it was not sent to the embedding endpoint
    agentic-shell-guard: the command holds what looks like a credential (<rule ids>, line <n>); history, logs and files keep it; reference it instead: "$NAME" or a file
    agentic-memory: <where> looks like a credential (<rule ids>, line <n>); memory never holds one

The way out is to reference the credential instead of holding it: an environment variable or a file
read at run time. A false positive, a credential-shaped example, is rewritten until it no longer
reads as one; nothing overrides a refusal, gitleaks' inline `gitleaks:allow` marker included.

A rule id `scan-incomplete` means the scan could not finish (the context ended, or a vendored rule
failed to load), and the text was refused rather than passed unscanned. Where the hook's own
context ended, shell-guard says the scan was cut short instead, and names no credential.
