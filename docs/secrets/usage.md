# Credential scanner: usage

Text agentic-helpers keeps is shared: a tool script reaches every session and project on the
machine, and sandboxed jobs read the stores. A token pasted into one, from a log, a verbose `curl`
or an environment dump, spreads to every reader and cannot be taken back. Every piece writing such
text scans it first with one shared scanner, `source/hooks/internal/secrets`, and refuses rather
than keeps what looks like a credential.

## Where it runs

| Piece | What is scanned | On a finding |
|---|---|---|
| `agentic-tools add`, `update` (CLI and MCP) | every file of the version written, a fork's first version included; `--summary`, `--usage`, `--inputs`, `--outputs`, `--distinct`, `--why` | refused, nothing embedded or written ([tools](../tools/usage.md#credentials)) |

## What a refusal says

A refusal names where the credential lies, every rule found on the first line holding one (sorted,
comma-separated) and that line. It never prints the matched text, and nothing is redacted: a
redacted copy beside a missed variant of the same token is worse than a refusal. Each piece's
wording is part of its contract, quoted by its skill:

    agentic-tools: <file or flag> looks like a credential (<rule ids>, line <n>); the library never holds one

The way out is to reference the credential instead of holding it: an environment variable or a file
read at run time. A false positive, a credential-shaped example, is rewritten until it no longer
reads as one; nothing overrides a refusal, gitleaks' inline `gitleaks:allow` marker included.

A rule id `scan-incomplete` means the scan could not finish (the context ended, or a vendored rule
failed to load), and the text was refused rather than passed unscanned.
